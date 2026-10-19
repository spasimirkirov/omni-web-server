package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed web/index.html web/favicon.ico web/favicon-48x48.png web/apple-touch-icon.png
var webFS embed.FS

type ServiceView struct {
	Name      string     `json:"name"`
	Title     string     `json:"title"`
	Installed bool       `json:"installed"`
	Version   string     `json:"version"`
	Port      int        `json:"port"`
	Path      string     `json:"path"`
	URL       string     `json:"url,omitempty"`
	Info      string     `json:"info,omitempty"`
	Status    ProcStatus `json:"status"`
}

type DevToolsView struct {
	Composer     string   `json:"composer"` // version, "" when not installed
	ComposerPath string   `json:"composer_path"`
	Node         []string `json:"node"`
	NodeDefault  string   `json:"node_default"`
	Shims        []string `json:"shims"`
}

type ExtView struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Builtin bool   `json:"builtin"`
}

type PHPView struct {
	Minor      string            `json:"minor"`
	Version    string            `json:"version"`
	Path       string            `json:"path"`
	IniPath    string            `json:"ini_path"`
	Default    bool              `json:"default"`
	InUse      bool              `json:"in_use"`
	Workers    int               `json:"workers"`
	Running    int               `json:"running"`
	Port       int               `json:"port"`
	Ports      []int             `json:"ports"`
	Extensions []ExtView         `json:"extensions"`
	INI        map[string]string `json:"ini"`
	Defaults   map[string]string `json:"ini_defaults"`
	Warnings   []string          `json:"warnings"`
	Removable  bool              `json:"removable"`
}

type StateView struct {
	Version   string            `json:"version"`
	Root      string            `json:"root"`
	Platform  string            `json:"platform"`
	Admin     bool              `json:"admin"`
	Settings  SettingsView      `json:"settings"`
	Services  []ServiceView     `json:"services"`
	Sites     []Site            `json:"sites"`
	PHP       []PHPView         `json:"php"`
	Tasks     []TaskView        `json:"tasks"`
	Warnings  []string          `json:"warnings"`
	WebOn     bool              `json:"web_on"`
	ShimsDir  string            `json:"shims_dir"`
	HostsFile string            `json:"hosts_file"`
	DNS       dnsStatus         `json:"dns"`
	SSL       sslStatus         `json:"ssl"`
	Tools     DevToolsView      `json:"tools"`
	Shares    map[string]string `json:"shares"`
}

type SettingsView struct {
	DashboardPort int    `json:"dashboard_port"`
	HTTPPort      int    `json:"http_port"`
	MySQLPort     int    `json:"mysql_port"`
	TLD           string `json:"tld"`
	WWWDir        string `json:"www_dir"`
	AutoVhost     bool   `json:"auto_vhost"`
	ManageHosts   bool   `json:"manage_hosts"`
	WildcardDNS   bool   `json:"wildcard_dns"`
	AddToPath     bool   `json:"add_to_path"`
	DNSPort       int    `json:"dns_port"`
	SSL           bool   `json:"ssl"`
	HTTPSPort     int    `json:"https_port"`
	Autostart     bool   `json:"autostart"`
	OpenBrowser   bool   `json:"open_browser"`
	DefaultPHP    string `json:"default_php"`
	MySQLFlavor   string `json:"mysql_flavor"`
	HasRootPass   bool   `json:"has_root_password"`
}

func (a *App) cachedVersion(key string, fn func() string) string {
	a.mu.Lock()
	v, ok := a.versions[key]
	a.mu.Unlock()
	if ok {
		return v
	}
	v = fn()
	a.mu.Lock()
	a.versions[key] = v
	a.mu.Unlock()
	return v
}

func (a *App) State() StateView {
	cfg := a.config()
	a.mu.Lock()
	sites := append([]Site(nil), a.sites...)
	warnings := append([]string(nil), a.warnings...)
	phpWarn := a.phpWarnings
	webOn := a.webWanted
	dns := a.dnsSnap
	pools := map[string]*phpPool{}
	for k, v := range a.pools {
		pools[k] = v
	}
	a.mu.Unlock()

	st := StateView{
		Version:  appVersion,
		Root:     a.root,
		Platform: runtime.GOOS,
		Admin:    isAdmin(),
		Settings: SettingsView{
			DashboardPort: cfg.DashboardPort, HTTPPort: cfg.HTTPPort, MySQLPort: cfg.MySQL.Port,
			TLD: cfg.TLD, WWWDir: a.abs(cfg.WWWDir), AutoVhost: cfg.AutoVhost, ManageHosts: cfg.ManageHosts,
			WildcardDNS: cfg.WildcardDNS, AddToPath: cfg.AddToPath, DNSPort: cfg.DNSPort, SSL: cfg.SSL, HTTPSPort: cfg.HTTPSPort,
			Autostart: cfg.Autostart, OpenBrowser: cfg.OpenBrowser, DefaultPHP: cfg.DefaultPHP,
			MySQLFlavor: cfg.MySQL.Flavor, HasRootPass: cfg.MySQL.RootPassword != "",
		},
		Sites:     sites,
		Tasks:     a.tasks.List(),
		Warnings:  warnings,
		WebOn:     webOn,
		ShimsDir:  a.path("bin", "shims"),
		HostsFile: a.hostsFile(cfg),
		DNS:       dns,
		SSL:       a.sslStatus(cfg),
	}
	apacheBin := a.apacheBin(cfg)
	st.Services = append(st.Services, ServiceView{
		Name: "apache", Title: "Apache", Installed: apacheBin != "" && exists(apacheBin),
		Version: a.cachedVersion("apache", func() string { return binVersion(apacheBin, apacheVersionRe, "-v") }),
		Port:    cfg.HTTPPort, Path: apacheBin, Status: a.apache.Status(),
	})
	mysqlBin := a.mysqlBin(cfg)
	title := "MySQL"
	if cfg.MySQL.Flavor == "mariadb" {
		title = "MariaDB"
	}
	st.Services = append(st.Services, ServiceView{
		Name: "mysql", Title: title, Installed: mysqlBin != "" && exists(mysqlBin),
		Version: a.cachedVersion("mysql", func() string { return binVersion(mysqlBin, mysqlVersionRe, "--version") }),
		Port:    cfg.MySQL.Port, Path: mysqlBin, Status: a.mysql.Status(),
	})
	mailpitBin := a.mailpitBin(cfg)
	st.Services = append(st.Services, ServiceView{
		Name: "mailpit", Title: "Mailpit", Installed: installed(mailpitBin),
		Version: a.cachedVersion("mailpit", func() string { return binVersion(mailpitBin, mailpitVersionRe, "version") }),
		Port:    cfg.Mailpit.SMTPPort, Path: mailpitBin, Status: a.mailpit.Status(),
		URL:  fmt.Sprintf("http://localhost:%d/", cfg.Mailpit.UIPort),
		Info: fmt.Sprintf("SMTP 127.0.0.1:%d · inbox on port %d", cfg.Mailpit.SMTPPort, cfg.Mailpit.UIPort),
	})
	redisBin := a.redisBin(cfg)
	st.Services = append(st.Services, ServiceView{
		Name: "redis", Title: "Redis", Installed: installed(redisBin),
		Version: a.cachedVersion("redis", func() string { return binVersion(redisBin, redisVersionRe, "--version") }),
		Port:    cfg.Redis.Port, Path: redisBin, Status: a.redis.Status(),
	})
	st.Tools = DevToolsView{NodeDefault: cfg.Node.Default}
	if exists(a.composerPhar()) {
		st.Tools.ComposerPath = a.composerPhar()
		st.Tools.Composer = a.cachedVersion("composer", func() string {
			if cli := a.phpCLI(cfg.PHP[cfg.DefaultPHP]); cfg.DefaultPHP != "" && cli != "" {
				return binVersion(cli, composerVersionRe, a.composerPhar(), "--version", "--no-ansi")
			}
			return "installed"
		})
	}
	for v := range cfg.Node.Versions {
		st.Tools.Node = append(st.Tools.Node, v)
	}
	sort.Slice(st.Tools.Node, func(i, j int) bool { return compareVersions(st.Tools.Node[i], st.Tools.Node[j]) > 0 })
	for n := range a.shims(cfg) {
		st.Tools.Shims = append(st.Tools.Shims, n)
	}
	sort.Strings(st.Tools.Shims)
	st.Shares = a.shareURLs()

	required := requiredPHP(cfg, sites)
	defaults := map[string]string{}
	for _, kv := range defaultPHPIni {
		defaults[kv[0]] = kv[1]
	}
	for _, minor := range cfg.phpVersions() {
		p := cfg.PHP[minor]
		v := PHPView{
			Minor: minor, Version: p.Version, Path: a.phpCGI(p), IniPath: a.phpIniPath(minor),
			Default: minor == cfg.DefaultPHP, InUse: contains(required, minor), Workers: p.Workers,
			Port: phpPortBase(minor), Ports: phpPorts(minor, p.Workers), INI: p.INI, Defaults: defaults, Warnings: phpWarn[minor],
			Removable: p.Dir != "" && strings.HasPrefix(a.abs(p.Dir), a.path("bin")+string(os.PathSeparator)),
		}
		if pool := pools[minor]; pool != nil {
			for _, w := range pool.workers {
				if w.Status().State == "running" {
					v.Running++
				}
			}
		}
		enabled := withDependencies(p.Extensions, a.availableExtensions(p))
		seen := map[string]bool{}
		for _, e := range a.availableExtensions(p) {
			seen[e] = true
			v.Extensions = append(v.Extensions, ExtView{Name: e, Enabled: contains(enabled, e) || contains(p.Builtin, e), Builtin: contains(p.Builtin, e)})
		}
		for _, e := range p.Builtin {
			if !seen[e] && e != "core" && e != "standard" {
				v.Extensions = append(v.Extensions, ExtView{Name: e, Enabled: true, Builtin: true})
			}
		}
		sort.Slice(v.Extensions, func(i, j int) bool { return v.Extensions[i].Name < v.Extensions[j].Name })
		st.PHP = append(st.PHP, v)
	}
	return st
}

// ---- HTTP plumbing ----

type apiError struct {
	code int
	msg  string
}

func (e apiError) Error() string { return e.msg }

func badRequest(format string, args ...any) error {
	return apiError{http.StatusBadRequest, fmt.Sprintf(format, args...)}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func handle(fn func(r *http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := fn(r)
		if err != nil {
			code := http.StatusInternalServerError
			var ae apiError
			if errors.As(err, &ae) {
				code = ae.code
			}
			writeJSON(w, code, map[string]string{"error": err.Error()})
			return
		}
		if v == nil {
			v = map[string]bool{"ok": true}
		}
		writeJSON(w, http.StatusOK, v)
	}
}

func decode(r *http.Request, v any) error {
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v); err != nil {
		return badRequest("invalid JSON body: %v", err)
	}
	return nil
}

// guard blocks cross-site requests and DNS rebinding: the Host header must be
// local, and state-changing requests need a custom header browsers will not
// send cross-origin without a (refused) CORS preflight.
func guard(port int, next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, h := range []string{"localhost", "127.0.0.1", "[::1]"} {
		allowed[fmt.Sprintf("%s:%d", h, port)] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed[r.Host] {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Omni") != "1" {
			http.Error(w, "missing X-Omni header", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		b, _ := webFS.ReadFile("web/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	})
	for _, f := range []string{"favicon.ico", "favicon-48x48.png", "apple-touch-icon.png"} {
		f := f
		mux.HandleFunc("GET /"+f, func(w http.ResponseWriter, r *http.Request) {
			b, _ := webFS.ReadFile("web/" + f)
			w.Header().Set("Cache-Control", "max-age=86400")
			http.ServeContent(w, r, f, time.Time{}, bytes.NewReader(b))
		})
	}
	mux.HandleFunc("GET /api/state", handle(func(r *http.Request) (any, error) { return a.State(), nil }))

	mux.HandleFunc("POST /api/services/{name}/{action}", handle(func(r *http.Request) (any, error) {
		return nil, a.serviceAction(r.PathValue("name"), r.PathValue("action"))
	}))

	mux.HandleFunc("POST /api/rescan", handle(func(r *http.Request) (any, error) { return nil, a.Apply(false) }))
	mux.HandleFunc("POST /api/hosts/sync", handle(func(r *http.Request) (any, error) { return nil, a.Apply(true) }))

	mux.HandleFunc("POST /api/sites", handle(func(r *http.Request) (any, error) {
		var body struct {
			Name, Path, PHP, DocRoot string
		}
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		return nil, a.addSite(body.Name, body.Path, body.PHP, body.DocRoot)
	}))
	mux.HandleFunc("PATCH /api/sites/{name}", handle(func(r *http.Request) (any, error) {
		var body struct {
			Enabled *bool   `json:"enabled"`
			PHP     *string `json:"php"`
			DocRoot *string `json:"docroot"`
		}
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		return nil, a.updateSite(r.PathValue("name"), body.Enabled, body.PHP, body.DocRoot)
	}))
	mux.HandleFunc("DELETE /api/sites/{name}", handle(func(r *http.Request) (any, error) {
		name := r.PathValue("name")
		return nil, a.update(func(c *Config) error {
			s, ok := c.Sites[name]
			if !ok {
				return apiError{404, "no such site"}
			}
			if s.Path == "" {
				return badRequest("projects inside www are removed by deleting or moving their folder")
			}
			delete(c.Sites, name)
			return nil
		})
	}))
	mux.HandleFunc("POST /api/sites/{name}/open", handle(func(r *http.Request) (any, error) {
		var body struct{ What string }
		_ = decode(r, &body)
		return nil, a.openSite(r.PathValue("name"), body.What)
	}))

	mux.HandleFunc("GET /api/php/catalog", handle(func(r *http.Request) (any, error) {
		cat, err := phpCatalog()
		if err != nil {
			return nil, fmt.Errorf("could not load the PHP release list: %v", err)
		}
		cfg := a.config()
		out := make([]PHPRelease, len(cat))
		copy(out, cat)
		for i := range out {
			if p, ok := cfg.PHP[out[i].Minor]; ok {
				out[i].Installed = p.Version
			}
		}
		return out, nil
	}))
	mux.HandleFunc("POST /api/php/add", handle(func(r *http.Request) (any, error) {
		var body struct{ Path string }
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		return nil, a.addPHPFolder(body.Path)
	}))
	mux.HandleFunc("POST /api/php/default", handle(func(r *http.Request) (any, error) {
		var body struct{ Version string }
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		return nil, a.update(func(c *Config) error {
			if _, ok := c.PHP[body.Version]; !ok {
				return badRequest("PHP %s is not installed", body.Version)
			}
			c.DefaultPHP = body.Version
			return nil
		})
	}))
	mux.HandleFunc("POST /api/php/{minor}/extensions", handle(func(r *http.Request) (any, error) {
		var body struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
		}
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		return nil, a.setExtension(r.PathValue("minor"), body.Name, body.Enabled)
	}))
	mux.HandleFunc("POST /api/php/{minor}/settings", handle(func(r *http.Request) (any, error) {
		var body struct {
			Workers int               `json:"workers"`
			INI     map[string]string `json:"ini"`
		}
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		return nil, a.setPHPSettings(r.PathValue("minor"), body.Workers, body.INI)
	}))
	mux.HandleFunc("DELETE /api/php/{minor}", handle(func(r *http.Request) (any, error) {
		return nil, a.removePHP(r.PathValue("minor"))
	}))

	mux.HandleFunc("POST /api/install", handle(func(r *http.Request) (any, error) {
		var body struct {
			Component string `json:"component"`
			Version   string `json:"version"`
			URL       string `json:"url"`
		}
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		t, err := a.startInstall(body.Component, body.Version, body.URL)
		if err != nil {
			return nil, err
		}
		return t.View(), nil
	}))
	mux.HandleFunc("GET /api/tasks/{id}", handle(func(r *http.Request) (any, error) {
		id, _ := strconv.Atoi(r.PathValue("id"))
		t := a.tasks.Get(id)
		if t == nil {
			return nil, apiError{404, "no such task"}
		}
		return t.View(), nil
	}))

	mux.HandleFunc("POST /api/settings", handle(func(r *http.Request) (any, error) {
		var body map[string]json.RawMessage
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		return nil, a.updateSettings(body)
	}))

	mux.HandleFunc("GET /api/databases", handle(func(r *http.Request) (any, error) {
		if !a.mysql.Running() {
			return nil, badRequest("MySQL is not running")
		}
		dbs, err := a.listDatabases(a.config())
		if dbs == nil {
			dbs = []string{}
		}
		return dbs, err
	}))
	mux.HandleFunc("POST /api/databases", handle(func(r *http.Request) (any, error) {
		var body struct{ Name string }
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		return nil, a.createDatabase(a.config(), body.Name)
	}))
	mux.HandleFunc("DELETE /api/databases/{name}", handle(func(r *http.Request) (any, error) {
		return nil, a.dropDatabase(a.config(), r.PathValue("name"))
	}))
	mux.HandleFunc("POST /api/mysql/password", handle(func(r *http.Request) (any, error) {
		var body struct{ Password string }
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		if err := a.setRootPassword(a.config(), body.Password); err != nil {
			return nil, err
		}
		return nil, a.update(func(c *Config) error { c.MySQL.RootPassword = body.Password; return nil })
	}))

	mux.HandleFunc("GET /api/logs", handle(func(r *http.Request) (any, error) {
		entries, _ := os.ReadDir(a.path("logs"))
		var names []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".log") {
				names = append(names, strings.TrimSuffix(e.Name(), ".log"))
			}
		}
		sort.Strings(names)
		return names, nil
	}))
	mux.HandleFunc("GET /api/logs/{name}", handle(func(r *http.Request) (any, error) {
		name := r.PathValue("name")
		if strings.ContainsAny(name, `/\.`) {
			return nil, badRequest("invalid log name")
		}
		n, _ := strconv.Atoi(r.URL.Query().Get("lines"))
		if n <= 0 || n > 2000 {
			n = 300
		}
		return map[string]string{"text": lastLines(a.path("logs", name+".log"), n)}, nil
	}))
	mux.HandleFunc("POST /api/ssl/trust", handle(func(r *http.Request) (any, error) { return nil, a.TrustCA() }))
	mux.HandleFunc("GET /api/ssl/ca.crt", func(w http.ResponseWriter, r *http.Request) {
		if _, _, err := a.loadCA(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-x509-ca-cert")
		w.Header().Set("Content-Disposition", `attachment; filename="omni-ca.crt"`)
		http.ServeFile(w, r, a.caCertPath())
	})
	mux.HandleFunc("GET /api/tools", handle(func(r *http.Request) (any, error) { return a.listTools(), nil }))
	mux.HandleFunc("POST /api/node/default", handle(func(r *http.Request) (any, error) {
		var body struct{ Version string }
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		return nil, a.update(func(c *Config) error {
			if _, ok := c.Node.Versions[body.Version]; !ok {
				return badRequest("Node.js %s is not installed", body.Version)
			}
			c.Node.Default = body.Version
			return nil
		})
	}))
	mux.HandleFunc("POST /api/sites/{name}/share", handle(func(r *http.Request) (any, error) {
		var body struct{ On bool }
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		if !body.On {
			a.stopShare(r.PathValue("name"))
			return nil, nil
		}
		u, err := a.startShare(r.PathValue("name"))
		return map[string]string{"url": u}, err
	}))
	mux.HandleFunc("POST /api/quit", handle(func(r *http.Request) (any, error) {
		go func() { time.Sleep(200 * time.Millisecond); a.Shutdown() }()
		return nil, nil
	}))
	return guard(a.config().DashboardPort, mux)
}

func (a *App) serviceAction(name, action string) error {
	type fns struct{ start, stop func() error }
	m := map[string]fns{
		"all":     {a.StartAll, a.StopAll},
		"apache":  {a.StartWeb, a.StopWeb},
		"mysql":   {a.StartMySQL, a.StopMySQL},
		"mailpit": {a.mailpit.Start, a.mailpit.Stop},
		"redis":   {a.redis.Start, a.redis.Stop},
	}
	f, ok := m[name]
	if !ok {
		return apiError{404, "unknown service " + name}
	}
	switch action {
	case "start":
		return f.start()
	case "stop":
		return f.stop()
	case "restart":
		_ = f.stop()
		return f.start()
	}
	return apiError{404, "unknown action " + action}
}

func (a *App) addSite(name, path, php, docroot string) error {
	if !validSiteName(name) {
		return badRequest("invalid site name (letters, digits, dot, dash, underscore)")
	}
	if path == "" || !filepath.IsAbs(path) || !isDir(path) {
		return badRequest("project path must be an existing absolute folder")
	}
	return a.update(func(c *Config) error {
		if _, ok := c.Sites[name]; ok {
			return badRequest("a site named %s already exists", name)
		}
		if php != "" {
			if _, ok := c.PHP[php]; !ok {
				return badRequest("PHP %s is not installed", php)
			}
		}
		c.Sites[name] = &SiteConfig{Enabled: true, Path: a.rel(filepath.Clean(path)), PHP: php, DocRoot: docroot}
		return nil
	})
}

func (a *App) updateSite(name string, enabled *bool, php, docroot *string) error {
	return a.update(func(c *Config) error {
		s, ok := c.Sites[name]
		if !ok {
			return apiError{404, "no such site: " + name}
		}
		if enabled != nil {
			s.Enabled = *enabled
		}
		if php != nil {
			if *php != "" {
				if _, ok := c.PHP[*php]; !ok {
					return badRequest("PHP %s is not installed", *php)
				}
			}
			s.PHP = *php
		}
		if docroot != nil {
			d := strings.TrimSpace(*docroot)
			if strings.Contains(filepath.ToSlash(d), "../") {
				return badRequest("document root must not contain ..")
			}
			s.DocRoot = filepath.ToSlash(d)
		}
		return nil
	})
}

func (a *App) openSite(name, what string) error {
	a.mu.Lock()
	var site *Site
	for i := range a.sites {
		if a.sites[i].Name == name {
			s := a.sites[i]
			site = &s
		}
	}
	a.mu.Unlock()
	if site == nil {
		return apiError{404, "no such site"}
	}
	switch what {
	case "folder":
		return openFolder(site.Path)
	case "terminal":
		env := os.Environ()
		for i, e := range env {
			if strings.HasPrefix(strings.ToUpper(e), "PATH=") {
				env[i] = e[:5] + a.path("bin", "shims") + string(os.PathListSeparator) + e[5:]
			}
		}
		return openTerminal(site.Path, env)
	default:
		return openURL("http://" + site.Host + portSuffix(a.config().HTTPPort))
	}
}

func portSuffix(p int) string {
	if p == 80 {
		return ""
	}
	return fmt.Sprintf(":%d", p)
}

func (a *App) addPHPFolder(dir string) error {
	cgi := ""
	if isDir(dir) {
		if !exists(filepath.Join(dir, exeName("php-cgi"))) {
			return badRequest("%s does not contain %s", dir, exeName("php-cgi"))
		}
	} else if exists(dir) && strings.Contains(filepath.Base(dir), "php-cgi") {
		cgi, dir = dir, ""
	} else {
		return badRequest("enter a PHP folder or a php-cgi binary")
	}
	return a.update(func(c *Config) error {
		_, err := a.registerPHP(c, dir, cgi)
		return err
	})
}

func (a *App) setExtension(minor, name string, on bool) error {
	return a.update(func(c *Config) error {
		p, ok := c.PHP[minor]
		if !ok {
			return apiError{404, "PHP " + minor + " is not installed"}
		}
		if contains(p.Builtin, name) {
			return badRequest("%s is built into PHP and always on", name)
		}
		if !contains(a.availableExtensions(p), name) {
			return badRequest("extension %s not found in %s", name, a.phpExtDir(p))
		}
		var out []string
		for _, e := range p.Extensions {
			if e != name {
				out = append(out, e)
			}
		}
		if on {
			out = append(out, name)
		}
		sort.Strings(out)
		p.Extensions = out
		return nil
	})
}

var iniKeyRe = func() func(string) bool {
	return func(k string) bool {
		if k == "" || len(k) > 100 {
			return false
		}
		for _, r := range k {
			if !(r == '.' || r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
				return false
			}
		}
		return true
	}
}()

func (a *App) setPHPSettings(minor string, workers int, ini map[string]string) error {
	return a.update(func(c *Config) error {
		p, ok := c.PHP[minor]
		if !ok {
			return apiError{404, "PHP " + minor + " is not installed"}
		}
		if workers != 0 {
			if workers < 1 || workers > 9 {
				return badRequest("workers must be between 1 and 9")
			}
			p.Workers = workers
		}
		if ini != nil {
			clean := map[string]string{}
			for k, v := range ini {
				k = strings.TrimSpace(k)
				if !iniKeyRe(k) || strings.ContainsAny(v, "\r\n") {
					return badRequest("invalid ini setting %q", k)
				}
				if k == "extension" || k == "zend_extension" || k == "extension_dir" {
					return badRequest("use the extension toggles instead of %s", k)
				}
				clean[k] = strings.TrimSpace(v)
			}
			p.INI = clean
		}
		return nil
	})
}

func (a *App) removePHP(minor string) error {
	var dir string
	err := a.update(func(c *Config) error {
		p, ok := c.PHP[minor]
		if !ok {
			return apiError{404, "PHP " + minor + " is not installed"}
		}
		if len(c.PHP) == 1 {
			return badRequest("cannot remove the only PHP version")
		}
		dir = a.abs(p.Dir)
		delete(c.PHP, minor)
		for _, s := range c.Sites {
			if s.PHP == minor {
				s.PHP = ""
			}
		}
		if c.DefaultPHP == minor {
			c.DefaultPHP = ""
		}
		return nil
	})
	if err != nil {
		return err
	}
	if dir != "" && strings.HasPrefix(dir, a.path("bin", "php")+string(os.PathSeparator)) {
		return os.RemoveAll(dir)
	}
	return nil
}

func (a *App) startInstall(component, version, u string) (*Task, error) {
	if u != "" && !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		return nil, badRequest("URL must start with http:// or https://")
	}
	var title string
	var fn func(t *Task) error
	switch component {
	case "php":
		title = "Install PHP " + version
		fn = func(t *Task) error { return a.installPHP(t, version, u) }
	case "apache":
		title = "Install Apache"
		fn = func(t *Task) error { return a.installApache(t, u) }
	case "mysql", "mariadb":
		title = "Install " + map[string]string{"mysql": "MySQL", "mariadb": "MariaDB"}[component]
		fn = func(t *Task) error { return a.installMySQL(t, component, u) }
	case "adminer":
		title = "Install Adminer"
		fn = func(t *Task) error { return a.installAdminer(t) }
	case "composer":
		title = "Install Composer"
		fn = func(t *Task) error { return a.installComposer(t) }
	case "node":
		title = "Install Node.js " + version
		fn = func(t *Task) error { return a.installNode(t, version) }
	case "mailpit":
		title = "Install Mailpit"
		fn = func(t *Task) error { return a.installMailpit(t, u) }
	case "redis":
		title = "Install Redis"
		fn = func(t *Task) error { return a.installRedis(t, u) }
	case "xdebug":
		title = "Install Xdebug for PHP " + version
		fn = func(t *Task) error { return a.installXdebug(t, version) }
	case "tool":
		title = "Install " + version
		fn = func(t *Task) error { return a.installTool(t, version) }
	default:
		return nil, badRequest("unknown component %q", component)
	}
	if a.tasks.Busy(title) {
		return nil, badRequest("%s is already running", title)
	}
	return a.tasks.Run(title, fn), nil
}

func (a *App) updateSettings(body map[string]json.RawMessage) error {
	return a.update(func(c *Config) error {
		for k, raw := range body {
			var err error
			switch k {
			case "auto_vhost":
				err = json.Unmarshal(raw, &c.AutoVhost)
			case "manage_hosts":
				err = json.Unmarshal(raw, &c.ManageHosts)
			case "wildcard_dns":
				err = json.Unmarshal(raw, &c.WildcardDNS)
			case "ssl":
				err = json.Unmarshal(raw, &c.SSL)
			case "autostart":
				err = json.Unmarshal(raw, &c.Autostart)
			case "add_to_path":
				err = json.Unmarshal(raw, &c.AddToPath)
			case "open_browser":
				err = json.Unmarshal(raw, &c.OpenBrowser)
			case "tld":
				var t string
				if err = json.Unmarshal(raw, &t); err == nil {
					t = strings.Trim(strings.ToLower(t), ". ")
					if hostLabel(t) != t {
						return badRequest("invalid TLD")
					}
					c.TLD = t
				}
			case "www_dir":
				var d string
				if err = json.Unmarshal(raw, &d); err == nil {
					if d == "" {
						return badRequest("www folder cannot be empty")
					}
					if err := os.MkdirAll(a.abs(d), 0o755); err != nil {
						return badRequest("cannot use %s: %v", d, err)
					}
					c.WWWDir = a.rel(a.abs(d))
				}
			case "http_port", "mysql_port", "dashboard_port", "dns_port", "https_port":
				var p int
				if err = json.Unmarshal(raw, &p); err == nil {
					if p < 1 || p > 65535 {
						return badRequest("invalid port")
					}
					switch k {
					case "http_port":
						if a.apache.Running() && p != c.HTTPPort {
							return badRequest("stop Apache before changing its port")
						}
						c.HTTPPort = p
						a.apache.spec.Port = p
					case "mysql_port":
						if a.mysql.Running() && p != c.MySQL.Port {
							return badRequest("stop MySQL before changing its port")
						}
						c.MySQL.Port = p
						a.mysql.spec.Port = p
					case "dashboard_port":
						c.DashboardPort = p
					case "dns_port":
						c.DNSPort = p
					case "https_port":
						c.HTTPSPort = p
					}
				}
			default:
				return badRequest("unknown setting %q", k)
			}
			if err != nil {
				return badRequest("invalid value for %s", k)
			}
		}
		return nil
	})
}

// Serve runs the dashboard until Shutdown is called.
func (a *App) Serve(openBrowser bool) error {
	cfg := a.config()
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.DashboardPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("dashboard port %d is in use (is Omni Web Server already running?): %v", cfg.DashboardPort, err)
	}
	srv := &http.Server{Handler: a.routes(), ReadHeaderTimeout: 10 * time.Second}
	url := fmt.Sprintf("http://localhost:%d/", cfg.DashboardPort)
	a.log.Printf("Omni Web Server %s dashboard at %s (root %s)", appVersion, url, a.root)

	if err := a.Apply(false); err != nil {
		a.log.Printf("apply: %v", err)
	}
	go a.watch()
	go func() {
		a.runPendingInstalls()
		if a.config().Autostart {
			if err := a.StartAll(); err != nil {
				a.log.Printf("autostart: %v", err)
			}
		}
	}()
	if openBrowser && cfg.OpenBrowser {
		go func() { time.Sleep(500 * time.Millisecond); _ = openURL(url) }()
	}
	go func() {
		<-a.quit
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (a *App) Shutdown() {
	a.quitOnce.Do(func() {
		a.log.Printf("shutting down")
		_ = a.StopAll()
		a.stopDNS()
		close(a.quit)
	})
}
