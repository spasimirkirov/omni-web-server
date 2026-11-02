package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---- winget: versions, timeouts, exit codes ----

const (
	wingetListTimeout    = 60 * time.Second
	wingetInstallTimeout = 20 * time.Minute
	wingetCacheTTL       = 5 * time.Minute
)

// Exit codes of winget (HRESULTs) that mean "nothing to do", not a failure.
const (
	wingetUpdateNotApplicable = 0x8A15002B // APPINSTALLER_CLI_ERROR_UPDATE_NOT_APPLICABLE: no newer package
	wingetAlreadyInstalled    = 0x8A150061 // APPINSTALLER_CLI_ERROR_PACKAGE_ALREADY_INSTALLED
)

// wingetResult maps winget's exit code to success or failure. The message is
// set for the "nothing to do" codes, which count as success.
func wingetResult(code int) (ok bool, msg string) {
	switch uint32(code) {
	case 0:
		return true, ""
	case wingetUpdateNotApplicable, wingetAlreadyInstalled:
		return true, "Already up to date"
	}
	return false, ""
}

// wingetPkg is what `winget list` says about one package.
type wingetPkg struct {
	Version   string `json:"version"`
	Available string `json:"available,omitempty"`
}

var wingetVersionRe = regexp.MustCompile(`^\d[0-9A-Za-z.+_-]*$`)

// parseWingetList reads the table printed by `winget list`. It does not rely on
// column positions or header words (both depend on the console width and on the
// Windows language, and long names are cut with an ellipsis): a row belongs to a
// package when one of its whitespace-separated words is exactly the package Id.
// What follows the Id is Version, then optionally Available, then Source. A row
// that does not parse cleanly (for example "< 1.2", a version winget could not
// read exactly) gives no entry, so a wrong version is never shown.
func parseWingetList(out string, ids []string) map[string]wingetPkg {
	res := map[string]wingetPkg{}
	out = strings.ReplaceAll(out, "\r\n", "\n")
	for _, line := range strings.Split(out, "\n") {
		if i := strings.LastIndex(line, "\r"); i >= 0 { // progress spinner redraws
			line = line[i+1:]
		}
		f := strings.Fields(line)
		for i := 1; i < len(f); i++ {
			id := ""
			for _, want := range ids {
				if strings.EqualFold(f[i], want) {
					id = want
					break
				}
			}
			if id == "" {
				continue
			}
			rest := f[i+1:]
			if n := len(rest); n > 0 && (rest[n-1] == "winget" || rest[n-1] == "msstore") {
				rest = rest[:n-1]
			}
			ok := len(rest) == 1 || len(rest) == 2
			for _, v := range rest {
				if !wingetVersionRe.MatchString(v) {
					ok = false
				}
			}
			if ok {
				if _, seen := res[id]; !seen {
					p := wingetPkg{Version: rest[0]}
					if len(rest) == 2 {
						p.Available = rest[1]
					}
					res[id] = p
				}
			}
			break
		}
	}
	return res
}

func wingetArgs(verb, id string) []string {
	return []string{verb, "--id", id, "-e", "--source", "winget", "--silent",
		"--accept-source-agreements", "--accept-package-agreements", "--disable-interactivity"}
}

func wingetListArgs() []string {
	return []string{"list", "--source", "winget", "--accept-source-agreements", "--disable-interactivity"}
}

func wingetTimeoutMessage(timeout time.Duration, args []string) string {
	n, unit := int(timeout.Minutes()), "minutes"
	if timeout < time.Minute {
		n, unit = int(timeout.Seconds()), "seconds"
	} else if n == 1 {
		unit = "minute"
	}
	return fmt.Sprintf("winget did not finish in %d %s; check for a UAC prompt or run the command in a terminal: winget %s",
		n, unit, strings.Join(args, " "))
}

func findWinget() string {
	return detectTool([]string{"winget", `%LOCALAPPDATA%\Microsoft\WindowsApps\winget.exe`})
}

// runWinget runs winget and kills it (with its child installers) when the
// timeout passes. The exit code is -1 when winget did not exit by itself.
func runWinget(timeout time.Duration, args ...string) (string, int, error) {
	exe := findWinget()
	if exe == "" {
		return "", -1, fmt.Errorf("winget (App Installer) is not available")
	}
	cmd := hiddenCommand(exe, args...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		return "", -1, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		code := 0
		if err != nil {
			code = -1
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			}
		}
		return buf.String(), code, err
	case <-timer.C:
		if runtime.GOOS == "windows" {
			_ = hiddenCommand("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
		}
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
		return "", -1, fmt.Errorf("%s", wingetTimeoutMessage(timeout, args))
	}
}

// ---- cache of `winget list`: one call, refreshed in the background ----

type wingetCache struct {
	mu   sync.Mutex
	at   time.Time
	busy bool
	pkgs map[string]wingetPkg
}

var wingetVersions wingetCache

// wingetListFn runs the real `winget list`; tests replace it.
var wingetListFn = func(ids []string) (map[string]wingetPkg, error) {
	out, _, err := runWinget(wingetListTimeout, wingetListArgs()...)
	pk := parseWingetList(out, ids)
	if err != nil && len(pk) == 0 {
		return nil, err
	}
	return pk, nil
}

// get returns what is known now and starts a refresh when it is old. It never
// waits for winget, so the Tools tab opens at once. checking is true while a
// refresh runs.
func (c *wingetCache) get(ids []string) (pkgs map[string]wingetPkg, checking bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.busy && time.Since(c.at) > wingetCacheTTL {
		c.busy = true
		go func() {
			pk, err := wingetListFn(ids)
			c.mu.Lock()
			if err == nil {
				c.pkgs = pk
			}
			c.at = time.Now() // a failure also waits for the TTL: no retry storm
			c.busy = false
			c.mu.Unlock()
		}()
	}
	return c.pkgs, c.busy
}

// invalidate makes the next get refresh, and forgets the "newer version" of one
// package that was just updated.
func (c *wingetCache) invalidate(updatedID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = time.Time{}
	if p, ok := c.pkgs[updatedID]; ok {
		p.Available = ""
		c.pkgs[updatedID] = p
	}
}

// ---- Composer: latest stable version ----

// parseComposerLatest reads https://getcomposer.org/versions and returns the
// newest version of the stable channel.
func parseComposerLatest(body string) (string, error) {
	var v struct {
		Stable []struct {
			Version string `json:"version"`
		} `json:"stable"`
	}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return "", err
	}
	if len(v.Stable) == 0 || !wingetVersionRe.MatchString(v.Stable[0].Version) {
		return "", fmt.Errorf("no stable version in the Composer versions list")
	}
	return v.Stable[0].Version, nil
}

type composerLatestCache struct {
	mu   sync.Mutex
	at   time.Time
	busy bool
	ver  string
}

var composerLatest composerLatestCache

func (c *composerLatestCache) refresh() {
	ver := ""
	if body, err := fetchText("https://getcomposer.org/versions"); err == nil {
		ver, _ = parseComposerLatest(body)
	}
	c.mu.Lock()
	c.at = time.Now()
	if ver != "" {
		c.ver = ver
	} else {
		c.at = time.Now().Add(-50 * time.Minute) // retry in 10 minutes
	}
	c.busy = false
	c.mu.Unlock()
}

// peek returns the cached latest version ("" when unknown) and refreshes it in
// the background once an hour. It never waits.
func (c *composerLatestCache) peek() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.busy && time.Since(c.at) > time.Hour {
		c.busy = true
		go c.refresh()
	}
	return c.ver
}

// wait is peek for a background task: it may wait for the first answer.
func (c *composerLatestCache) wait() string {
	if v := c.peek(); v != "" {
		return v
	}
	for i := 0; i < 150; i++ {
		c.mu.Lock()
		b, v := c.busy, c.ver
		c.mu.Unlock()
		if !b {
			return v
		}
		time.Sleep(100 * time.Millisecond)
	}
	return ""
}
