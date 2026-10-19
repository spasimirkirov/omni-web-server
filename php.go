package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// Extensions switched on when a PHP version is first registered (if available).
// It covers what Laravel and most frameworks need.
var recommendedExtensions = []string{
	"bcmath", "bz2", "ctype", "curl", "dom", "exif", "fileinfo", "gd", "gettext", "iconv", "intl",
	"mbstring", "mysqli", "mysqlnd", "opcache", "openssl", "pdo", "pdo_mysql", "pdo_sqlite", "phar",
	"simplexml", "sodium", "sqlite3", "tokenizer", "xml", "xmlreader", "xmlwriter", "xsl", "zip",
}

var zendExtensions = map[string]bool{"opcache": true, "xdebug": true}

// Extensions that must be loaded before others (for split-package builds like Debian's).
var extDependencies = map[string][]string{
	"pdo_mysql":    {"pdo", "mysqlnd"},
	"pdo_sqlite":   {"pdo"},
	"pdo_pgsql":    {"pdo"},
	"pdo_odbc":     {"pdo"},
	"pdo_firebird": {"pdo"},
	"mysqli":       {"mysqlnd"},
	"dom":          {"xml"},
	"simplexml":    {"xml"},
	"xmlreader":    {"xml", "dom"},
	"xmlwriter":    {"xml"},
	"xsl":          {"xml", "dom"},
	"wddx":         {"xml"},
}

var extLoadFirst = []string{"mysqlnd", "pdo", "xml", "dom", "sockets", "json"}

var defaultPHPIni = [][2]string{
	{"memory_limit", "512M"},
	{"upload_max_filesize", "128M"},
	{"post_max_size", "128M"},
	{"max_execution_time", "300"},
	{"max_input_time", "300"},
	{"max_input_vars", "5000"},
	{"date.timezone", "UTC"},
	{"display_errors", "On"},
	{"error_reporting", "E_ALL"},
	{"opcache.revalidate_freq", "0"},
}

type phpProbe struct {
	Version string   `json:"version"`
	ExtDir  string   `json:"extension_dir"`
	Loaded  []string `json:"loaded"`
	SAPI    string   `json:"sapi"`
}

const probeScript = `<?php echo json_encode(['version'=>PHP_VERSION,'extension_dir'=>ini_get('extension_dir'),'loaded'=>array_map('strtolower',array_merge(get_loaded_extensions(),get_loaded_extensions(true))),'sapi'=>PHP_SAPI]);`

// probePHP runs php-cgi without any ini to learn its version and built-in extensions.
func (a *App) probePHP(cgi string) (*phpProbe, error) {
	script := a.path("tmp", "probe.php")
	if err := writeFileAtomic(script, []byte(probeScript)); err != nil {
		return nil, err
	}
	cmd := hiddenCommand(cgi, "-n", "-q", script)
	cmd.Env = append(os.Environ(), "PHP_INI_SCAN_DIR=")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("running %s: %v", cgi, err)
	}
	s := string(out)
	if i := strings.Index(s, "{"); i >= 0 {
		s = s[i:]
	}
	var p phpProbe
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return nil, fmt.Errorf("unexpected output from %s: %.200s", cgi, out)
	}
	return &p, nil
}

func (a *App) phpCGI(p *PHPInstall) string {
	if p.CGI != "" {
		return a.abs(p.CGI)
	}
	return filepath.Join(a.abs(p.Dir), exeName("php-cgi"))
}

func (a *App) phpCLI(p *PHPInstall) string {
	if p.CLI != "" {
		return a.abs(p.CLI)
	}
	if p.Dir != "" {
		if c := filepath.Join(a.abs(p.Dir), exeName("php")); exists(c) {
			return c
		}
	}
	return ""
}

func (a *App) phpExtDir(p *PHPInstall) string {
	if p.ExtDir != "" {
		return a.abs(p.ExtDir)
	}
	return filepath.Join(a.abs(p.Dir), "ext")
}

// availableExtensions lists the shared extensions found in the extension folder.
func (a *App) availableExtensions(p *PHPInstall) []string {
	entries, err := os.ReadDir(a.phpExtDir(p))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		n := strings.ToLower(e.Name())
		var name string
		switch {
		case runtime.GOOS == "windows" && strings.HasPrefix(n, "php_") && strings.HasSuffix(n, ".dll"):
			name = strings.TrimSuffix(strings.TrimPrefix(n, "php_"), ".dll")
		case runtime.GOOS != "windows" && strings.HasSuffix(n, ".so"):
			name = strings.TrimSuffix(n, ".so")
		default:
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// withDependencies adds shared extensions that the enabled ones require.
func withDependencies(enabled, available []string) []string {
	set := map[string]bool{}
	var add func(string)
	add = func(e string) {
		if set[e] || !contains(available, e) {
			return
		}
		set[e] = true
		for _, d := range extDependencies[e] {
			add(d)
		}
	}
	for _, e := range enabled {
		add(e)
	}
	return orderExtensions(set)
}

func orderExtensions(set map[string]bool) []string {
	var out []string
	for _, e := range extLoadFirst {
		if set[e] {
			out = append(out, e)
		}
	}
	var rest []string
	for e := range set {
		if !contains(extLoadFirst, e) {
			rest = append(rest, e)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

var extLineRe = regexp.MustCompile(`^\s*(zend_extension|extension|extension_dir)\s*=`)

func (a *App) phpIniPath(minor string) string { return a.path("etc", "php", minor, "php.ini") }

// baseIni returns the php.ini-development template shipped with PHP, if present.
func (a *App) baseIni(minor string, p *PHPInstall) string {
	cands := []string{}
	if p.BaseINI != "" {
		cands = append(cands, a.abs(p.BaseINI))
	}
	if p.Dir != "" {
		cands = append(cands, filepath.Join(a.abs(p.Dir), "php.ini-development"))
	}
	cands = append(cands, "/usr/lib/php/"+minor+"/php.ini-development", "/usr/share/php"+minor+"-common/php.ini-development")
	for _, c := range cands {
		if b, err := os.ReadFile(c); err == nil {
			return string(b)
		}
	}
	return "[PHP]\n"
}

func iniQuote(v string) string {
	if v == "" || strings.ContainsAny(v, " ;=:/\\\"'") {
		return `"` + strings.ReplaceAll(v, `"`, ``) + `"`
	}
	return v
}

// renderPHPIni builds the full php.ini: the shipped template with its extension
// lines commented out, followed by DevStack's managed section (later keys win).
func (a *App) renderPHPIni(minor string, p *PHPInstall) string {
	var b strings.Builder
	sc := bufio.NewScanner(strings.NewReader(a.baseIni(minor, p)))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if extLineRe.MatchString(line) {
			line = ";" + line
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	tag := strings.ReplaceAll(minor, ".", "")
	fmt.Fprintf(&b, "\n; ------------------------------------------------------------------\n")
	fmt.Fprintf(&b, "; DevStack managed section (generated). Change these from the dashboard.\n")
	fmt.Fprintf(&b, "; ------------------------------------------------------------------\n[PHP]\n")
	fmt.Fprintf(&b, "extension_dir = %s\n", iniQuote(slash(a.phpExtDir(p))))
	available := a.availableExtensions(p)
	for _, e := range withDependencies(p.Extensions, available) {
		if contains(p.Builtin, e) {
			continue
		}
		if zendExtensions[e] {
			fmt.Fprintf(&b, "zend_extension = %s\n", e)
		} else {
			fmt.Fprintf(&b, "extension = %s\n", e)
		}
	}
	fixed := [][2]string{
		{"cgi.fix_pathinfo", "1"},
		{"error_log", slash(a.path("logs", "php"+tag+"_error.log"))},
		{"log_errors", "On"},
		{"session.save_path", slash(a.path("tmp", "sessions"))},
		{"upload_tmp_dir", slash(a.path("tmp", "uploads"))},
		{"sys_temp_dir", slash(a.path("tmp"))},
	}
	if ca := a.path("etc", "ssl", "cacert.pem"); exists(ca) {
		fixed = append(fixed, [2]string{"curl.cainfo", slash(ca)}, [2]string{"openssl.cafile", slash(ca)})
	}
	for _, kv := range fixed {
		fmt.Fprintf(&b, "%s = %s\n", kv[0], iniQuote(kv[1]))
	}
	merged := map[string]string{}
	for _, kv := range defaultPHPIni {
		merged[kv[0]] = kv[1]
	}
	for k, v := range p.INI {
		merged[k] = v
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "%s = %s\n", k, iniQuote(merged[k]))
	}
	return b.String()
}

// phpPorts are the worker ports of a version; the dispatcher uses phpPortBase.
func phpPorts(minor string, workers int) []int {
	base := phpPortBase(minor)
	ports := make([]int, workers)
	for i := range ports {
		ports[i] = base + 1 + i
	}
	return ports
}

func (a *App) phpWorkerCmd(minor string, p *PHPInstall, port int) (*exec.Cmd, error) {
	cgi := a.phpCGI(p)
	if !exists(cgi) {
		return nil, fmt.Errorf("php-cgi not found: %s", cgi)
	}
	cmd := hiddenCommand(cgi, "-b", fmt.Sprintf("127.0.0.1:%d", port), "-c", a.phpIniPath(minor))
	cmd.Dir = filepath.Dir(cgi)
	cmd.Env = append(os.Environ(),
		"PHP_FCGI_MAX_REQUESTS=0",
		"PHP_FCGI_CHILDREN=0",
		"PHP_INI_SCAN_DIR=",
		"PHPRC="+filepath.Dir(a.phpIniPath(minor)),
	)
	return cmd, nil
}

// checkPHPIni loads the generated ini and returns PHP startup warnings, if any.
func (a *App) checkPHPIni(minor string, p *PHPInstall) []string {
	cmd := hiddenCommand(a.phpCGI(p), "-c", a.phpIniPath(minor), "-v")
	cmd.Env = append(os.Environ(), "PHP_INI_SCAN_DIR=")
	out, _ := cmd.CombinedOutput()
	var warns []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(stripTags(line))
		if strings.Contains(line, "Startup") || strings.Contains(line, "Warning") || strings.Contains(line, "Fatal") {
			if !seen[line] {
				seen[line] = true
				warns = append(warns, line)
			}
		}
	}
	return warns
}

var tagRe = regexp.MustCompile(`<[^>]+>`)

func stripTags(s string) string { return tagRe.ReplaceAllString(s, "") }

// registerPHP probes a php-cgi binary and adds/updates it in cfg.PHP.
func (a *App) registerPHP(cfg *Config, dir, cgi string) (string, error) {
	inst := &PHPInstall{Dir: a.rel(dir), Workers: 4, INI: map[string]string{}}
	if cgi != "" {
		inst.CGI = a.rel(cgi)
	}
	probe, err := a.probePHP(a.phpCGI(inst))
	if err != nil {
		return "", err
	}
	minor := minorOf(probe.Version)
	inst.Version = probe.Version
	for _, e := range probe.Loaded {
		if e == "zend opcache" {
			e = "opcache"
		}
		inst.Builtin = append(inst.Builtin, e)
	}
	if runtime.GOOS != "windows" && probe.ExtDir != "" {
		inst.ExtDir = probe.ExtDir
	}
	if cgi != "" && runtime.GOOS != "windows" {
		inst.Dir = ""
		if cli := filepath.Join(filepath.Dir(cgi), strings.Replace(filepath.Base(cgi), "php-cgi", "php", 1)); exists(cli) {
			if pr, err := a.probeCLIVersion(cli); err == nil && minorOf(pr) == minor {
				inst.CLI = cli
			}
		}
	}
	if old, ok := cfg.PHP[minor]; ok && compareVersions(old.Version, probe.Version) > 0 && exists(a.phpCGI(old)) {
		return minor, nil // keep the newer build already registered
	}
	if old, ok := cfg.PHP[minor]; ok {
		inst.Workers = old.Workers
		inst.INI = old.INI
		inst.Extensions = old.Extensions
	} else {
		avail := a.availableExtensions(inst)
		for _, e := range recommendedExtensions {
			if contains(avail, e) && !contains(inst.Builtin, e) {
				inst.Extensions = append(inst.Extensions, e)
			}
		}
	}
	cfg.PHP[minor] = inst
	if cfg.DefaultPHP == "" {
		cfg.DefaultPHP = minor
	}
	return minor, nil
}

var cliVersionRe = regexp.MustCompile(`PHP (\d+\.\d+\.\d+)`)

func (a *App) probeCLIVersion(cli string) (string, error) {
	v := binVersion(cli, cliVersionRe, "-n", "-v")
	if v == "" {
		return "", fmt.Errorf("no version")
	}
	return v, nil
}

// writeShims creates php.cmd / phpXY.cmd (or shell scripts) in bin/shims so the
// CLI uses the same php.ini as the web server.
func (a *App) writeShims(cfg *Config) {
	dir := a.path("bin", "shims")
	_ = os.MkdirAll(dir, 0o755)
	write := func(name, cli, ini string) {
		if runtime.GOOS == "windows" {
			body := fmt.Sprintf("@echo off\r\n\"%s\" -c \"%s\" %%*\r\n", cli, ini)
			_, _ = writeIfChanged(filepath.Join(dir, name+".cmd"), []byte(body))
		} else {
			body := fmt.Sprintf("#!/bin/sh\nPHP_INI_SCAN_DIR= exec \"%s\" -c \"%s\" \"$@\"\n", cli, ini)
			p := filepath.Join(dir, name)
			_, _ = writeIfChanged(p, []byte(body))
			_ = os.Chmod(p, 0o755)
		}
	}
	for minor, p := range cfg.PHP {
		cli := a.phpCLI(p)
		if cli == "" {
			continue
		}
		write("php"+strings.ReplaceAll(minor, ".", ""), cli, a.phpIniPath(minor))
		if minor == cfg.DefaultPHP {
			write("php", cli, a.phpIniPath(minor))
		}
	}
}
