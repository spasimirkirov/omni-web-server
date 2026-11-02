package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPhpVC(t *testing.T) {
	for minor, want := range map[string]string{"7.1": "vc14", "7.4": "vc15", "8.0": "vs16", "8.3": "vs16", "8.4": "vs17", "8.5": "vs17"} {
		if got := phpVC(minor); got != want {
			t.Errorf("phpVC(%s) = %s, want %s", minor, got, want)
		}
	}
}

func TestPeclAssetName(t *testing.T) {
	if got := peclAssetName("redis", "6.3.0", "8.4", false); got != "php_redis-6.3.0-8.4-nts-vs17-x64.zip" {
		t.Fatalf("nts: %s", got)
	}
	if got := peclAssetName("redis", "5.3.7", "7.4", true); got != "php_redis-5.3.7-7.4-ts-vc15-x64.zip" {
		t.Fatalf("ts: %s", got)
	}
}

func TestParsePeclReleases(t *testing.T) {
	page := `<A HREF="/~windows/pecl/releases/">[To Parent Directory]</A><br>
<a href="5.3.7/">5.3.7/</a> <a href="6.0.0RC1/">x</a> <a href="6.3.0/">y</a>
<a href="6.0.2/">z</a> <a href="6.1.0beta2/">b</a> <a href="6.3.0/">dup</a> <a href="5.10.0/">v</a>`
	got := strings.Join(parsePeclReleases(page), ",")
	if got != "6.3.0,6.0.2,5.10.0,5.3.7" {
		t.Fatalf("releases: %s", got)
	}
}

func TestPhpThreadSafe(t *testing.T) {
	dir := t.TempDir()
	if phpThreadSafe(dir) {
		t.Fatal("empty folder is NTS")
	}
	os.WriteFile(filepath.Join(dir, "php8.dll"), nil, 0o644)
	if phpThreadSafe(dir) {
		t.Fatal("php8.dll alone is NTS")
	}
	os.WriteFile(filepath.Join(dir, "php8ts.dll"), nil, 0o644)
	if !phpThreadSafe(dir) {
		t.Fatal("php8ts.dll means TS")
	}
}

func TestExtMissing(t *testing.T) {
	// patch upgrade: redis stays enabled in the config, its DLL went with the old folder
	got := extMissing([]string{"mbstring", "redis", "opcache"}, []string{"opcache"}, []string{"mbstring"})
	if strings.Join(got, ",") != "redis" {
		t.Fatalf("missing: %v", got)
	}
	if got := extMissing([]string{"redis"}, nil, []string{"redis"}); len(got) != 0 {
		t.Fatalf("present DLL: %v", got)
	}
}

func TestNewerPatch(t *testing.T) {
	cases := []struct {
		inst, cat string
		want      bool
	}{
		{"8.4.26", "8.4.27", true},
		{"8.4.26", "8.4.26", false},
		{"8.4.27", "8.4.26", false},
		{"8.3.9", "8.4.1", false}, // other minor line
		{"8.3.9", "8.3.10", true}, // numeric, not text, comparison
		{"", "8.4.1", false},
	}
	for _, c := range cases {
		if got := newerPatch(c.inst, c.cat); got != c.want {
			t.Errorf("newerPatch(%q, %q) = %v", c.inst, c.cat, got)
		}
	}
}

func TestPeclRegistry(t *testing.T) {
	if !peclKnown("redis") || peclKnown("nope") || peclKnown("../x") {
		t.Fatal("registry lookup")
	}
}

// An enabled extension without its DLL must not reach php.ini (PHP would warn on every start).
func TestMissingExtensionDLLSkipped(t *testing.T) {
	got := withDependencies([]string{"redis", "mbstring"}, []string{"mbstring"})
	if strings.Join(got, ",") != "mbstring" {
		t.Fatalf("got %v", got)
	}
}
