package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Discoverability: launcher shims in bin/shims (php, phpXY, composer, node,
// npm, npx, mysql, mysqldump, redis-cli, ows) that a single PATH entry exposes to
// every terminal and IDE, plus registry values under
// HKCU\Software\OmniWebServer that describe where everything lives.

// homeEnv is the user environment variable that points to the install folder.
// It used to be OMNI_HOME, which is too generic a name; that one is removed
// when it points to this install.
const (
	homeEnv       = "OMNI_WEB_SERVER_HOME"
	legacyHomeEnv = "OMNI_HOME"
)

type shim struct {
	target string   // executable to run
	args   []string // fixed arguments before the user's
	env    string   // extra environment (unix only), e.g. PHP_INI_SCAN_DIR=
}

func (a *App) shims(cfg *Config) map[string]shim {
	out := map[string]shim{}
	for minor, p := range cfg.PHP {
		cli := a.phpCLI(p)
		if cli == "" {
			continue
		}
		// Command line only: OPcache with a file cache per PHP version (see cliOpcacheArgs).
		cliArgs := []string{"-c", a.phpIniPath(minor)}
		if extra := cliOpcacheArgs(cfg.Tuning, p, slash(a.path("tmp", "opcache", strings.ReplaceAll(minor, ".", "")))); extra != nil {
			_ = os.MkdirAll(a.path("tmp", "opcache", strings.ReplaceAll(minor, ".", "")), 0o755)
			cliArgs = append(cliArgs, extra...)
		}
		s := shim{target: cli, args: cliArgs, env: "PHP_INI_SCAN_DIR="}
		out["php"+strings.ReplaceAll(minor, ".", "")] = s
		if minor == cfg.DefaultPHP {
			out["php"] = s
			if exists(a.composerPhar()) {
				out["composer"] = shim{target: cli, args: append(append([]string{}, cliArgs...), a.composerPhar()), env: "PHP_INI_SCAN_DIR="}
			}
		}
	}
	// A system-wide Node.js is on PATH by itself; shims would shadow it.
	if dir := a.nodeDir(cfg); dir != "" && toolPath("node") == "" {
		out["node"] = shim{target: filepath.Join(dir, exeName("node"))}
		for _, n := range []string{"npm", "npx", "corepack"} {
			if p := filepath.Join(dir, n+".cmd"); exists(p) {
				out[n] = shim{target: p}
			}
		}
	}
	if server := a.mysqlBin(cfg); installed(server) && cfg.MySQL.Dir != "" {
		for _, n := range []string{"mysql", "mysqldump", "mysqladmin", "mariadb", "mariadb-dump"} {
			if p := filepath.Join(filepath.Dir(server), exeName(n)); exists(p) {
				out[n] = shim{target: p}
			}
		}
	}
	if b := a.redisBin(cfg); installed(b) && cfg.Redis.Dir != "" {
		if p := filepath.Join(filepath.Dir(b), exeName("redis-cli")); exists(p) {
			out["redis-cli"] = shim{target: p}
		}
	}
	// The server's own command line ("ows status"), from the install folder.
	if p := a.path(exeName("ows")); exists(p) {
		out["ows"] = shim{target: p}
	}
	return out
}

func quoteArgs(args []string) string {
	var b strings.Builder
	for _, a := range args {
		b.WriteString(` "` + a + `"`)
	}
	return b.String()
}

// shQuote quotes s for a POSIX shell (single quotes keep $, \ and spaces literal).
func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// shShim is the #!/bin/sh launcher. On Windows it sits next to the .cmd one
// (like composer and npm do) because Git Bash, MSYS2 and Cygwin only run
// extensionless or .exe files by name and never pick up "php.cmd" as "php".
// Paths use forward slashes there, which both MSYS and native Windows programs accept.
func shShim(s shim) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	if s.env != "" {
		b.WriteString(s.env + " ")
	}
	b.WriteString("exec " + shQuote(slash(s.target)))
	for _, arg := range s.args {
		b.WriteString(" " + shQuote(slash(arg)))
	}
	b.WriteString(" \"$@\"\n")
	return b.String()
}

// writeShims rewrites bin/shims and removes shims for things no longer installed.
func (a *App) writeShims(cfg *Config) {
	dir := a.path("bin", "shims")
	_ = os.MkdirAll(dir, 0o755)
	want := map[string]bool{}
	write := func(file, body string) {
		want[file] = true
		p := filepath.Join(dir, file)
		_, _ = writeIfChanged(p, []byte(body))
		_ = os.Chmod(p, 0o755)
	}
	for name, s := range a.shims(cfg) {
		if runtime.GOOS == "windows" {
			write(name+".cmd", fmt.Sprintf("@echo off\r\n\"%s\"%s %%*\r\n", s.target, quoteArgs(s.args)))
		}
		write(name, shShim(s))
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !want[e.Name()] {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// registryValues is what Omni Web Server publishes under HKCU\Software\OmniWebServer.
// Keys ending in a backslash path are subkeys: "PHP\8.3" -> {"Path": ..., "Ini": ...}.
func (a *App) registryValues(cfg *Config) map[string]map[string]string {
	root := map[string]string{
		"InstallDir": a.root,
		"Version":    appVersion,
		"WWWDir":     a.abs(cfg.WWWDir),
		"ShimsDir":   a.path("bin", "shims"),
		"Dashboard":  fmt.Sprintf("http://localhost:%d/", cfg.DashboardPort),
		"DefaultPHP": cfg.DefaultPHP,
	}
	keys := map[string]map[string]string{"": root}
	for minor, p := range cfg.PHP {
		v := map[string]string{"Version": p.Version, "Ini": a.phpIniPath(minor), "CGI": a.phpCGI(p)}
		if cli := a.phpCLI(p); cli != "" {
			v["Path"] = cli
			if minor == cfg.DefaultPHP {
				root["PHPExecutable"] = cli
			}
		}
		keys[`PHP\`+minor] = v
	}
	if exists(a.composerPhar()) {
		root["ComposerPhar"] = a.composerPhar()
	}
	if d := a.nodeDir(cfg); d != "" {
		root["NodeDir"] = d
		root["NodeVersion"] = cfg.Node.Default
	}
	if b := a.mysqlBin(cfg); installed(b) {
		keys["MySQL"] = map[string]string{"BinDir": filepath.Dir(b), "Port": fmt.Sprint(cfg.MySQL.Port), "Flavor": cfg.MySQL.Flavor}
	}
	if b := a.apacheBin(cfg); installed(b) {
		keys["Apache"] = map[string]string{"Path": b, "Port": fmt.Sprint(cfg.HTTPPort)}
	}
	if b := a.redisBin(cfg); installed(b) {
		keys["Redis"] = map[string]string{"Path": b, "Port": fmt.Sprint(cfg.Redis.Port)}
	}
	if b := a.mailpitBin(cfg); installed(b) {
		keys["Mailpit"] = map[string]string{"Path": b, "SMTPPort": fmt.Sprint(cfg.Mailpit.SMTPPort), "UIPort": fmt.Sprint(cfg.Mailpit.UIPort)}
	}
	return keys
}

// syncDiscovery publishes registry values and puts bin/shims on the user PATH.
// Both only run when something changed, so this is cheap to call from Apply.
func (a *App) syncDiscovery(cfg *Config) []string {
	var warnings []string
	vals := a.registryValues(cfg)
	var sig strings.Builder
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		names := make([]string, 0, len(vals[k]))
		for n := range vals[k] {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(&sig, "%s|%s=%s\n", k, n, vals[k][n])
		}
	}
	sig.WriteString(fmt.Sprintf("path=%v\n", cfg.AddToPath))
	// Rewrite when the variable points elsewhere (e.g. another copy wrote it).
	sig.WriteString(fmt.Sprintf("home=%v\n", os.Getenv(homeEnv) == a.root && os.Getenv(legacyHomeEnv) != a.root))
	stamp := a.path("tmp", "discovery.sig")
	if old, err := os.ReadFile(stamp); err == nil && string(old) == sig.String() {
		return nil
	}
	if err := writeRegistry(vals); err != nil {
		warnings = append(warnings, "Registry: "+err.Error())
	}
	shimDir := a.path("bin", "shims")
	if cfg.AddToPath {
		if err := addToUserPath(shimDir); err != nil {
			warnings = append(warnings, "PATH: "+err.Error())
		}
	} else {
		_ = removeFromUserPath(shimDir)
	}
	if len(warnings) == 0 {
		_ = writeFileAtomic(stamp, []byte(sig.String()))
	}
	return warnings
}

// Cleanup undoes system changes (used by the uninstaller): hosts block, DNS
// rule, PATH entry and registry key. Projects, databases and the CA stay.
func (a *App) Cleanup() []string {
	var errs []string
	cfg := a.config()
	if err := a.syncHosts(cfg, nil, true, false); err != nil {
		errs = append(errs, "hosts: "+err.Error())
	}
	if resolverKind() != "manual" && currentResolverRule() != "" {
		if err := applyResolverRule("", cfg.DNSPort); err != nil {
			errs = append(errs, "DNS rule: "+err.Error())
		}
	}
	if err := removeFromUserPath(a.path("bin", "shims")); err != nil {
		errs = append(errs, "PATH: "+err.Error())
	}
	if err := removeRegistry(); err != nil {
		errs = append(errs, "registry: "+err.Error())
	}
	_ = os.Remove(a.path("tmp", "discovery.sig"))
	return errs
}
