package main

import (
	"strings"
	"testing"
	"time"
)

var testWingetIDs = []string{"Git.Git", "OpenJS.NodeJS.LTS", "Microsoft.VisualStudioCode", "dbeaver.dbeaver", "HeidiSQL.HeidiSQL", "Docker.DockerDesktop", "Cloudflare.cloudflared"}

const wingetListEN = `Name                          Id                          Version        Available      Source
-----------------------------------------------------------------------------------------------
Git                           Git.Git                     2.45.1         2.46.0         winget
Node.js LTS                   OpenJS.NodeJS.LTS           20.14.0        22.11.0        winget
Microsoft Visual Studio Code  Microsoft.VisualStudioCode  1.94.2                        winget
DBeaver Community             dbeaver.dbeaver             24.2.1.202409  winget
Docker Desktop                Docker.DockerDesktop        < 4.30.0       winget
`

// Bulgarian headers, long names cut with an ellipsis, carriage returns, a spinner line.
const wingetListBG = "   - \r   \\ \r\r\n" +
	"Име                           Идентификатор               Версия         Налично        Източник\r\n" +
	"-----------------------------------------------------------------------------------------------\r\n" +
	"Microsoft Visual Studio Co…   Microsoft.VisualStudioCode  1.93.0         1.94.2         winget\r\n" +
	"HeidiSQL 12.8.0.6908          HeidiSQL.HeidiSQL           12.8.0.6908    winget\r\n" +
	"Cloudflare cloudflared …      Cloudflare.cloudflared      2024.9.1       2024.10.0\r\n" +
	"Нещо друго                    Other.Thing                 9.9.9          winget\r\n"

func TestParseWingetListEnglish(t *testing.T) {
	got := parseWingetList(wingetListEN, testWingetIDs)
	want := map[string]wingetPkg{
		"Git.Git":                    {"2.45.1", "2.46.0"},
		"OpenJS.NodeJS.LTS":          {"20.14.0", "22.11.0"},
		"Microsoft.VisualStudioCode": {"1.94.2", ""},
		"dbeaver.dbeaver":            {"24.2.1.202409", ""},
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: got %+v, want %+v", id, got[id], w)
		}
	}
	// "< 4.30.0" is not an exact version: no entry rather than a wrong one.
	if p, ok := got["Docker.DockerDesktop"]; ok {
		t.Errorf("Docker should have no version, got %+v", p)
	}
}

func TestParseWingetListLocalizedTruncated(t *testing.T) {
	got := parseWingetList(wingetListBG, testWingetIDs)
	if p := got["Microsoft.VisualStudioCode"]; p.Version != "1.93.0" || p.Available != "1.94.2" {
		t.Errorf("vscode: %+v", p)
	}
	if p := got["HeidiSQL.HeidiSQL"]; p.Version != "12.8.0.6908" || p.Available != "" {
		t.Errorf("heidisql: %+v", p)
	}
	// no Source column at all: version and available
	if p := got["Cloudflare.cloudflared"]; p.Version != "2024.9.1" || p.Available != "2024.10.0" {
		t.Errorf("cloudflared: %+v", p)
	}
	if _, ok := got["Other.Thing"]; ok {
		t.Error("an id that was not asked for must not appear")
	}
}

func TestParseWingetListJunk(t *testing.T) {
	for _, in := range []string{"", "No installed package found matching input criteria.", "Git.Git", "Git.Git ???", "x Git.Git a b c d e"} {
		if got := parseWingetList(in, testWingetIDs); len(got) != 0 {
			t.Errorf("%q: expected nothing, got %v", in, got)
		}
	}
}

func TestWingetResult(t *testing.T) {
	cases := []struct {
		code int
		ok   bool
		msg  string
	}{
		{0, true, ""},
		{int(int32(-1978335189)), true, "Already up to date"}, // 0x8A15002B as a signed exit code
		{0x8A15002B, true, "Already up to date"},
		{int(int32(-1978335135)), true, "Already up to date"}, // 0x8A150061
		{1, false, ""},
		{-1, false, ""},
		{0x8A150014, false, ""},
	}
	for _, c := range cases {
		ok, msg := wingetResult(c.code)
		if ok != c.ok || msg != c.msg {
			t.Errorf("code %#x: got %v %q, want %v %q", uint32(c.code), ok, msg, c.ok, c.msg)
		}
	}
}

func TestWingetArgsAndMessage(t *testing.T) {
	a := strings.Join(wingetArgs("upgrade", "Git.Git"), " ")
	want := "upgrade --id Git.Git -e --source winget --silent --accept-source-agreements --accept-package-agreements --disable-interactivity"
	if a != want {
		t.Errorf("upgrade args: %s", a)
	}
	if !strings.Contains(strings.Join(wingetArgs("install", "x"), " "), "--source winget") {
		t.Error("install must use --source winget")
	}
	m := wingetTimeoutMessage(20*time.Minute, wingetArgs("install", "Git.Git"))
	if !strings.HasPrefix(m, "winget did not finish in 20 minutes; check for a UAC prompt or run the command in a terminal: winget install --id Git.Git") {
		t.Errorf("message: %s", m)
	}
	if m := wingetTimeoutMessage(60*time.Second, nil); !strings.Contains(m, "1 minute;") {
		t.Errorf("message: %s", m)
	}
}

func TestWingetCacheOneCall(t *testing.T) {
	calls := 0
	done := make(chan struct{})
	old := wingetListFn
	wingetListFn = func(ids []string) (map[string]wingetPkg, error) {
		calls++
		defer close(done)
		return map[string]wingetPkg{"Git.Git": {"1.0", "2.0"}}, nil
	}
	defer func() { wingetListFn = old }()
	var c wingetCache
	if _, checking := c.get(testWingetIDs); !checking {
		t.Error("first get must report checking")
	}
	<-done
	time.Sleep(50 * time.Millisecond)
	for i := 0; i < 5; i++ {
		pk, checking := c.get(testWingetIDs)
		if checking || pk["Git.Git"].Available != "2.0" {
			t.Fatalf("get %d: %v %v", i, pk, checking)
		}
	}
	if calls != 1 {
		t.Errorf("winget called %d times, want 1", calls)
	}
}

func TestParseComposerLatest(t *testing.T) {
	body := `{"stable":[{"path":"/download/2.8.1/composer.phar","version":"2.8.1","min-php":70205},{"path":"/download/2.2.24/composer.phar","version":"2.2.24"}],"preview":[{"version":"2.9.0-RC1"}],"snapshot":[]}`
	v, err := parseComposerLatest(body)
	if err != nil || v != "2.8.1" {
		t.Errorf("got %q %v", v, err)
	}
	for _, bad := range []string{"", "{}", `{"stable":[]}`, `{"stable":[{"version":"latest"}]}`, "<html>"} {
		if v, err := parseComposerLatest(bad); err == nil {
			t.Errorf("%q: expected an error, got %q", bad, v)
		}
	}
}
