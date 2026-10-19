package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---- Composer ----

var composerVersionRe = regexp.MustCompile(`Composer version (\S+)`)

func (a *App) composerPhar() string { return a.path("bin", "composer", "composer.phar") }

func (a *App) installComposer(t *Task) error {
	const base = "https://getcomposer.org/download/latest-stable/"
	t.Set("Downloading Composer", -1)
	sum, err := fetchText(base + "composer.phar.sha256")
	if err != nil {
		return err
	}
	fields := strings.Fields(sum)
	if len(fields) == 0 {
		return errors.New("unexpected composer.phar.sha256")
	}
	file, err := a.download(t, base+"composer.phar", "", fields[0])
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.composerPhar()), 0o755); err != nil {
		return err
	}
	if err := os.Rename(file, a.composerPhar()); err != nil {
		return err
	}
	a.forgetVersion("composer")
	a.writeShims(a.config())
	return nil
}

// ---- Node.js ----

type nodeRelease struct {
	Version string          `json:"version"`
	LTS     json.RawMessage `json:"lts"`
	Files   []string        `json:"files"`
}

func (r nodeRelease) isLTS() bool { return len(r.LTS) > 0 && string(r.LTS) != "false" }

// pickNode resolves "" (latest LTS), "22" (latest 22.x) or "22.11.0".
func pickNode(rels []nodeRelease, want string) (string, error) {
	want = strings.TrimPrefix(want, "v")
	for _, r := range rels { // index.json is newest first
		v := strings.TrimPrefix(r.Version, "v")
		if !contains(r.Files, "win-x64-zip") {
			continue
		}
		switch {
		case want == "" || strings.EqualFold(want, "lts"):
			if r.isLTS() {
				return v, nil
			}
		case v == want || strings.HasPrefix(v, want+"."):
			return v, nil
		}
	}
	return "", fmt.Errorf("no Node.js release matches %q", want)
}

func (a *App) installNode(t *Task, want string) error {
	if runtime.GOOS != "windows" {
		return errors.New("on Linux/macOS install Node.js with your package manager or fnm")
	}
	t.Set("Looking up Node.js releases", -1)
	body, err := fetchText("https://nodejs.org/dist/index.json")
	if err != nil {
		return err
	}
	var rels []nodeRelease
	if err := json.Unmarshal([]byte(body), &rels); err != nil {
		return err
	}
	v, err := pickNode(rels, want)
	if err != nil {
		return err
	}
	name := "node-v" + v + "-win-x64"
	sha := ""
	if sums, err := fetchText("https://nodejs.org/dist/v" + v + "/SHASUMS256.txt"); err == nil {
		for _, l := range strings.Split(sums, "\n") {
			if f := strings.Fields(l); len(f) == 2 && f[1] == name+".zip" {
				sha = f[0]
			}
		}
	}
	dir, err := a.installZip(t, "https://nodejs.org/dist/v"+v+"/"+name+".zip", "", sha, "node", name, exeName("node"))
	if err != nil {
		return err
	}
	return a.update(func(c *Config) error {
		c.Node.Versions[v] = a.rel(dir)
		if c.Node.Default == "" || compareVersions(v, c.Node.Default) > 0 {
			c.Node.Default = v
		}
		return nil
	})
}

func (a *App) nodeDir(cfg *Config) string {
	if d, ok := cfg.Node.Versions[cfg.Node.Default]; ok {
		return a.abs(d)
	}
	return ""
}

// ---- Xdebug (Windows builds from xdebug.org) ----

func xdebugURL(minor string) (string, error) {
	body, err := fetchText("https://xdebug.org/download")
	if err != nil {
		return "", err
	}
	re := regexp.MustCompile(`(/files/php_xdebug-(\d+\.\d+\.\d+)-` + regexp.QuoteMeta(minor) + `-[a-z0-9-]*nts[a-z0-9-]*-x86_64\.dll)`)
	best, bestV := "", ""
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		if bestV == "" || compareVersions(m[2], bestV) > 0 {
			best, bestV = m[1], m[2]
		}
	}
	if best == "" {
		return "", fmt.Errorf("no Xdebug build for PHP %s (NTS x64) found on xdebug.org", minor)
	}
	return "https://xdebug.org" + best, nil
}

func (a *App) installXdebug(t *Task, minor string) error {
	cfg := a.config()
	p, ok := cfg.PHP[minor]
	if !ok {
		return fmt.Errorf("PHP %s is not installed", minor)
	}
	if runtime.GOOS != "windows" {
		return errors.New("on Linux install php-xdebug with your package manager; it then appears as an extension toggle")
	}
	t.Set("Looking up Xdebug for PHP "+minor, -1)
	u, err := xdebugURL(minor)
	if err != nil {
		return err
	}
	file, err := a.download(t, u, "", "")
	if err != nil {
		return err
	}
	target := filepath.Join(a.phpExtDir(p), "php_xdebug.dll")
	if err := os.Rename(file, target); err != nil {
		return fmt.Errorf("cannot replace %s (stop Apache first?): %v", target, err)
	}
	return a.setExtension(minor, "xdebug", true)
}

// ---- Windows developer apps (winget) ----

type devTool struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Description string   `json:"description"`
	Winget      string   `json:"winget"`
	URL         string   `json:"url"`
	Installed   bool     `json:"installed"`
	Path        string   `json:"path,omitempty"`
	detect      []string // commands on PATH or file globs (%VAR% expanded)
}

var devTools = []devTool{
	{ID: "git", Name: "Git for Windows", Category: "Essentials", Description: "Version control, plus Git Bash.", Winget: "Git.Git", URL: "https://git-scm.com/download/win",
		detect: []string{"git", `%ProgramFiles%\Git\cmd\git.exe`}},
	{ID: "terminal", Name: "Windows Terminal", Category: "Essentials", Description: "Tabs for cmd, PowerShell, Git Bash and WSL.", Winget: "Microsoft.WindowsTerminal", URL: "https://aka.ms/terminal",
		detect: []string{"wt", `%LOCALAPPDATA%\Microsoft\WindowsApps\wt.exe`}},
	{ID: "vscode", Name: "Visual Studio Code", Category: "Editors", Description: "Editor; add the PHP Intelephense and PHP Debug extensions.", Winget: "Microsoft.VisualStudioCode", URL: "https://code.visualstudio.com/",
		detect: []string{"code", `%LOCALAPPDATA%\Programs\Microsoft VS Code\Code.exe`, `%ProgramFiles%\Microsoft VS Code\Code.exe`}},
	{ID: "phpstorm", Name: "PhpStorm", Category: "Editors", Description: "JetBrains PHP IDE (paid, 30-day trial).", Winget: "JetBrains.PHPStorm", URL: "https://www.jetbrains.com/phpstorm/",
		detect: []string{`%ProgramFiles%\JetBrains\PhpStorm*\bin\phpstorm64.exe`, `%LOCALAPPDATA%\Programs\PhpStorm\bin\phpstorm64.exe`}},
	{ID: "heidisql", Name: "HeidiSQL", Category: "Databases", Description: "Lightweight MySQL/MariaDB client.", Winget: "HeidiSQL.HeidiSQL", URL: "https://www.heidisql.com/",
		detect: []string{`%ProgramFiles%\HeidiSQL\heidisql.exe`}},
	{ID: "dbeaver", Name: "DBeaver", Category: "Databases", Description: "Universal database client (MySQL, PostgreSQL, SQLite, Redis via plugin).", Winget: "dbeaver.dbeaver", URL: "https://dbeaver.io/",
		detect: []string{`%ProgramFiles%\DBeaver\dbeaver.exe`, `%LOCALAPPDATA%\DBeaver\dbeaver.exe`}},
	{ID: "cloudflared", Name: "Cloudflare Tunnel", Category: "Sharing", Description: "Share a local site on a public https URL (used by the Share button).", Winget: "Cloudflare.cloudflared", URL: "https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/",
		detect: []string{"cloudflared", `%ProgramFiles(x86)%\cloudflared\cloudflared.exe`, `%ProgramFiles%\cloudflared\cloudflared.exe`}},
	{ID: "ngrok", Name: "ngrok", Category: "Sharing", Description: "Alternative tunnel for webhooks (needs a free account).", Winget: "Ngrok.Ngrok", URL: "https://ngrok.com/download",
		detect: []string{"ngrok", `%LOCALAPPDATA%\Microsoft\WinGet\Links\ngrok.exe`}},
	{ID: "docker", Name: "Docker Desktop", Category: "Containers", Description: "Run PostgreSQL, Meilisearch, etc. in containers (uses WSL2).", Winget: "Docker.DockerDesktop", URL: "https://www.docker.com/products/docker-desktop/",
		detect: []string{"docker", `%ProgramFiles%\Docker\Docker\Docker Desktop.exe`}},
	{ID: "wsl", Name: "WSL (Windows Subsystem for Linux)", Category: "Containers", Description: "A real Linux next to Windows; needed by Docker Desktop.", Winget: "Microsoft.WSL", URL: "https://learn.microsoft.com/windows/wsl/install",
		detect: []string{`%ProgramFiles%\WSL\wsl.exe`}},
}

var winEnvRe = regexp.MustCompile(`%([^%]+)%`)

func expandWinEnv(p string) string {
	return winEnvRe.ReplaceAllStringFunc(p, func(m string) string { return os.Getenv(strings.Trim(m, "%")) })
}

func detectTool(candidates []string) string {
	for _, c := range candidates {
		if !strings.ContainsAny(c, `\/%`) {
			if p, err := exec.LookPath(c); err == nil {
				return p
			}
			continue
		}
		if runtime.GOOS != "windows" {
			continue
		}
		m, _ := filepath.Glob(expandWinEnv(c))
		if len(m) > 0 {
			sort.Strings(m)
			return m[len(m)-1]
		}
	}
	return ""
}

func toolPath(id string) string {
	for _, t := range devTools {
		if t.ID == id {
			return detectTool(t.detect)
		}
	}
	return ""
}

func (a *App) listTools() []devTool {
	out := make([]devTool, len(devTools))
	copy(out, devTools)
	for i := range out {
		out[i].Path = detectTool(out[i].detect)
		out[i].Installed = out[i].Path != ""
	}
	return out
}

func (a *App) installTool(t *Task, id string) error {
	var tool *devTool
	for i := range devTools {
		if devTools[i].ID == id {
			tool = &devTools[i]
		}
	}
	if tool == nil {
		return fmt.Errorf("unknown tool %q", id)
	}
	if runtime.GOOS != "windows" {
		return fmt.Errorf("install %s from %s", tool.Name, tool.URL)
	}
	winget := detectTool([]string{"winget", `%LOCALAPPDATA%\Microsoft\WindowsApps\winget.exe`})
	if winget == "" {
		return fmt.Errorf("winget (App Installer) is not available; download %s from %s", tool.Name, tool.URL)
	}
	t.Set("Installing "+tool.Name+" with winget (this can take a few minutes)", -1)
	out, err := hiddenCommand(winget, "install", "--id", tool.Winget, "-e", "--silent",
		"--accept-source-agreements", "--accept-package-agreements", "--disable-interactivity").CombinedOutput()
	if err != nil {
		return fmt.Errorf("winget: %v\n%s", err, tailString(strings.TrimSpace(string(out)), 600))
	}
	return nil
}

// ---- Share a site publicly through a Cloudflare quick tunnel ----

type share struct {
	proc *Proc
	url  string
}

var tunnelURLRe = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

func (a *App) startShare(name string) (string, error) {
	cfg := a.config()
	var site *Site
	a.mu.Lock()
	for i := range a.sites {
		if a.sites[i].Name == name {
			s := a.sites[i]
			site = &s
		}
	}
	existing := a.shares[name]
	a.mu.Unlock()
	if site == nil {
		return "", apiError{404, "no such site"}
	}
	if existing != nil && existing.url != "" {
		return existing.url, nil
	}
	bin := toolPath("cloudflared")
	if bin == "" {
		return "", badRequest("install Cloudflare Tunnel first (Tools tab)")
	}
	logPath := a.path("logs", "share-"+hostLabel(name)+".log")
	_ = os.Remove(logPath)
	p := NewProc(ProcSpec{
		Name:    "share-" + name,
		LogPath: logPath,
		Build: func() (*exec.Cmd, error) {
			return hiddenCommand(bin, "tunnel", "--no-autoupdate",
				"--url", fmt.Sprintf("http://127.0.0.1:%d", cfg.HTTPPort),
				"--http-host-header", site.Host), nil
		},
	})
	if err := p.Start(); err != nil {
		return "", err
	}
	sh := &share{proc: p}
	a.mu.Lock()
	a.shares[name] = sh
	a.mu.Unlock()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if u := findInFile(logPath, tunnelURLRe); u != "" {
			a.mu.Lock()
			sh.url = u
			a.mu.Unlock()
			return u, nil
		}
		if !p.Running() {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	a.stopShare(name)
	return "", fmt.Errorf("the tunnel did not start: %s", lastLines(logPath, 5))
}

func findInFile(path string, re *regexp.Regexp) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if m := re.FindString(sc.Text()); m != "" {
			return m
		}
	}
	return ""
}

func (a *App) stopShare(name string) {
	a.mu.Lock()
	sh := a.shares[name]
	delete(a.shares, name)
	a.mu.Unlock()
	if sh != nil {
		_ = sh.proc.Stop()
	}
}

func (a *App) stopAllShares() {
	a.mu.Lock()
	names := make([]string, 0, len(a.shares))
	for n := range a.shares {
		names = append(names, n)
	}
	a.mu.Unlock()
	var wg sync.WaitGroup
	for _, n := range names {
		wg.Add(1)
		go func(n string) { defer wg.Done(); a.stopShare(n) }(n)
	}
	wg.Wait()
}

func (a *App) shareURLs() map[string]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := map[string]string{}
	for n, sh := range a.shares {
		out[n] = sh.url
	}
	return out
}
