package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const appVersion = "1.0.0"

type App struct {
	root string
	log  *log.Logger

	mu            sync.Mutex
	cfg           *Config
	sites         []Site
	webWanted     bool
	mysqlWanted   bool
	warnings      []string
	phpWarnings   map[string][]string
	staticMods    map[string]map[string]bool
	hostsDeclined string
	versions      map[string]string

	dnsErr  string
	dnsSnap dnsStatus

	applyMu sync.Mutex
	// guarded by applyMu
	dns              *dnsServer
	dnsKey           string
	resolverState    *string
	resolverDeclined string

	apache   *Proc
	mysql    *Proc
	pools    map[string]*phpPool
	tasks    *TaskManager
	quit     chan struct{}
	quitOnce sync.Once
}

type phpPool struct {
	minor      string
	workers    []*Proc
	dispatcher *fcgiDispatcher
	iniHash    string
}

func NewApp(root string) (*App, error) {
	a := &App{
		root:        root,
		phpWarnings: map[string][]string{},
		staticMods:  map[string]map[string]bool{},
		versions:    map[string]string{},
		pools:       map[string]*phpPool{},
		tasks:       NewTaskManager(),
		quit:        make(chan struct{}),
	}
	for _, d := range []string{"www", "bin", "etc/apache/custom", "etc/php", "etc/mysql", "etc/ssl", "data", "logs", "tmp/sessions", "tmp/uploads", "tmp/downloads"} {
		if err := os.MkdirAll(a.path(strings.Split(d, "/")...), 0o755); err != nil {
			return nil, err
		}
	}
	lf, err := os.OpenFile(a.path("logs", "devstack.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	a.log = log.New(multiWriter{os.Stdout, lf}, "", log.LstdFlags)
	cfg, fresh, err := loadConfig(root)
	if err != nil {
		return nil, err
	}
	a.cfg = cfg
	if fresh {
		a.discoverSystem(cfg)
		a.writeWelcomeProject()
	}
	a.discoverLocal(cfg)
	cfg.normalize()
	if err := saveConfig(root, cfg); err != nil {
		return nil, err
	}
	a.apache = NewProc(ProcSpec{
		Name:    "apache",
		LogPath: a.path("logs", "apache_console.log"),
		Port:    cfg.HTTPPort,
		Build:   func() (*exec.Cmd, error) { return a.apacheCmd(a.config()) },
	})
	a.mysql = NewProc(ProcSpec{
		Name:     "mysql",
		LogPath:  a.path("logs", "mysql_console.log"),
		Port:     cfg.MySQL.Port,
		Build:    func() (*exec.Cmd, error) { return a.mysqlCmd(a.config()) },
		Graceful: func() error { return a.mysqlShutdown(a.config()) },
	})
	return a, nil
}

func (a *App) path(parts ...string) string {
	return filepath.Join(append([]string{a.root}, parts...)...)
}

func (a *App) abs(p string) string {
	if p == "" {
		return ""
	}
	p = filepath.FromSlash(p)
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(a.root, p)
}

// rel stores paths inside the root as relative so the folder can be moved.
func (a *App) rel(p string) string {
	if p == "" {
		return ""
	}
	if r, err := filepath.Rel(a.root, p); err == nil && !strings.HasPrefix(r, "..") && !filepath.IsAbs(r) {
		return filepath.ToSlash(r)
	}
	return p
}

// config returns a snapshot of the configuration.
func (a *App) config() *Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.clone()
}

// update mutates the configuration under lock, saves it and re-applies it.
func (a *App) update(fn func(c *Config) error) error {
	a.mu.Lock()
	if err := fn(a.cfg); err != nil {
		a.mu.Unlock()
		return err
	}
	a.cfg.normalize()
	err := saveConfig(a.root, a.cfg)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	return a.Apply(false)
}

// discoverSystem finds system-wide Apache/PHP/MySQL on Linux/macOS (first run only).
func (a *App) discoverSystem(cfg *Config) {
	if runtime.GOOS == "windows" {
		return
	}
	for _, p := range []string{"/usr/sbin/apache2", "/usr/sbin/httpd", "/usr/local/sbin/httpd", "/opt/homebrew/bin/httpd", "/usr/local/bin/httpd"} {
		if exists(p) {
			cfg.Apache.Bin = p
			break
		}
	}
	for _, d := range []string{"/usr/lib/apache2/modules", "/usr/lib64/httpd/modules", "/usr/lib/httpd/modules", "/opt/homebrew/lib/httpd/modules"} {
		if isDir(d) {
			cfg.Apache.ModulesDir = d
			break
		}
	}
	cgis, _ := filepath.Glob("/usr/bin/php-cgi*")
	more, _ := filepath.Glob("/usr/local/bin/php-cgi*")
	cgis = append(cgis, more...)
	sort.Strings(cgis)
	for _, cgi := range cgis {
		if real, err := filepath.EvalSymlinks(cgi); err == nil && real != cgi && contains(cgis, real) {
			continue
		}
		if minor, err := a.registerPHP(cfg, "", cgi); err == nil {
			a.log.Printf("found PHP %s at %s", minor, cgi)
		}
	}
	for _, p := range []string{"/usr/sbin/mariadbd", "/usr/sbin/mysqld", "/usr/bin/mariadbd", "/usr/bin/mysqld", "/usr/local/bin/mysqld"} {
		if exists(p) {
			cfg.MySQL.Bin = p
			break
		}
	}
}

// discoverLocal registers installs unpacked into bin/ (by DevStack or by hand).
func (a *App) discoverLocal(cfg *Config) {
	if cfg.Apache.Dir == "" && cfg.Apache.Bin == "" {
		if dirs, _ := filepath.Glob(a.path("bin", "apache", "*", "bin", exeName("httpd"))); len(dirs) > 0 {
			sort.Strings(dirs)
			cfg.Apache.Dir = a.rel(filepath.Dir(filepath.Dir(dirs[len(dirs)-1])))
		}
	}
	if cfg.MySQL.Dir == "" && cfg.MySQL.Bin == "" {
		var found []string
		for _, n := range []string{"mysqld", "mariadbd"} {
			m, _ := filepath.Glob(a.path("bin", "mysql", "*", "bin", exeName(n)))
			found = append(found, m...)
		}
		if len(found) > 0 {
			sort.Strings(found)
			cfg.MySQL.Dir = a.rel(filepath.Dir(filepath.Dir(found[len(found)-1])))
			cfg.MySQL.Flavor = ""
		}
	}
	known := map[string]bool{}
	for _, p := range cfg.PHP {
		if p.Dir != "" {
			known[filepath.Clean(a.abs(p.Dir))] = true
		}
	}
	cgis, _ := filepath.Glob(a.path("bin", "php", "*", exeName("php-cgi")))
	sort.Slice(cgis, func(i, j int) bool { return compareVersions(versionInPath(cgis[i]), versionInPath(cgis[j])) < 0 })
	for _, cgi := range cgis {
		dir := filepath.Dir(cgi)
		if known[filepath.Clean(dir)] {
			continue
		}
		if minor, err := a.registerPHP(cfg, dir, ""); err == nil {
			a.log.Printf("registered PHP %s from %s", minor, dir)
		} else {
			a.log.Printf("skipping %s: %v", dir, err)
		}
	}
	for k, p := range cfg.PHP {
		if !exists(a.phpCGI(p)) {
			a.log.Printf("PHP %s no longer found at %s, removing", k, a.phpCGI(p))
			delete(cfg.PHP, k)
		}
	}
	if cfg.MySQL.Flavor == "" && a.mysqlBin(cfg) != "" && exists(a.mysqlBin(cfg)) {
		cfg.MySQL.Flavor = a.mysqlFlavor(cfg)
	}
}

func versionInPath(p string) string {
	m := versionRe.FindString(p)
	return m
}

func (a *App) writeWelcomeProject() {
	dir := a.path("www", "welcome")
	if exists(dir) {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "index.php"), []byte(welcomePHP), 0o644)
}

const welcomePHP = `<!doctype html>
<html><head><meta charset="utf-8"><title>DevStack works</title>
<style>body{font-family:system-ui,sans-serif;max-width:720px;margin:60px auto;padding:0 16px;color:#1f2933}code{background:#eef2f6;padding:2px 6px;border-radius:4px}</style>
</head><body>
<h1>It works!</h1>
<p>This page is served by Apache with <strong>PHP <?= PHP_VERSION ?></strong> (<?= PHP_SAPI ?>).</p>
<p>Every folder in <code>www</code> becomes <code>http://&lt;folder&gt;.local</code>. Laravel projects are pointed at their <code>public</code> folder automatically.</p>
<p>Loaded extensions: <?= htmlspecialchars(implode(', ', get_loaded_extensions())) ?></p>
</body></html>
`

// requiredPHP is the set of PHP versions the enabled sites (and the default host) use.
func requiredPHP(cfg *Config, sites []Site) []string {
	set := map[string]bool{}
	if cfg.DefaultPHP != "" {
		set[cfg.DefaultPHP] = true
	}
	for _, s := range sites {
		if s.Enabled && !s.Missing && s.PHP != "" {
			set[s.PHP] = true
		}
	}
	var out []string
	for v := range set {
		if _, ok := cfg.PHP[v]; ok {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return compareVersions(out[i], out[j]) < 0 })
	return out
}

// Apply rescans projects and makes generated config, hosts file and running
// processes match the configuration. It only touches what changed, so it is
// cheap enough to run every few seconds.
func (a *App) Apply(forceHosts bool) error {
	a.applyMu.Lock()
	defer a.applyMu.Unlock()

	a.mu.Lock()
	sites, changed := a.scanSitesLocked()
	if changed {
		if err := saveConfig(a.root, a.cfg); err != nil {
			a.log.Printf("saving config: %v", err)
		}
	}
	a.sites = sites
	cfg := a.cfg.clone()
	webWanted := a.webWanted
	a.mu.Unlock()

	var warnings []string
	if cfg.DefaultPHP == "" {
		warnings = append(warnings, "No PHP version installed yet: install one from the PHP tab.")
	}
	warnings = append(warnings, missingRuntimes()...)

	// PHP: php.ini per version, worker pools for versions in use.
	required := requiredPHP(cfg, sites)
	phpWarn := map[string][]string{}
	for minor, p := range cfg.PHP {
		ini := a.renderPHPIni(minor, p)
		if ch, err := writeIfChanged(a.phpIniPath(minor), []byte(ini)); err != nil {
			warnings = append(warnings, err.Error())
		} else if ch || a.phpWarningsFor(minor) == nil {
			phpWarn[minor] = a.checkPHPIni(minor, p)
		} else {
			phpWarn[minor] = a.phpWarningsFor(minor)
		}
	}
	a.writeShims(cfg)
	if webWanted {
		for _, minor := range required {
			if err := a.ensurePool(cfg, minor); err != nil {
				warnings = append(warnings, fmt.Sprintf("PHP %s: %v", minor, err))
			}
		}
	}
	for minor := range a.pools {
		if !webWanted || !contains(required, minor) {
			a.stopPool(minor)
		}
	}

	// Apache
	if bin := a.apacheBin(cfg); bin != "" && exists(bin) {
		conf := a.buildApacheConf(cfg, sites)
		changed, err := writeIfChanged(a.apacheConfPath(), []byte(conf))
		if err != nil {
			warnings = append(warnings, err.Error())
		} else if changed && a.apache.Running() {
			if err := testApacheConf(bin, a.apacheConfPath()); err != nil {
				warnings = append(warnings, err.Error())
			} else {
				a.log.Printf("apache config changed, restarting")
				if err := a.apache.Restart(); err != nil {
					warnings = append(warnings, err.Error())
				}
			}
		}
	} else {
		warnings = append(warnings, "Apache is not installed yet: install it from the Services tab.")
	}

	// Wildcard DNS, then the hosts file (not needed while the DNS rule is active)
	warnings = append(warnings, a.syncDNS(cfg, forceHosts)...)
	if err := a.syncHosts(cfg, sites, forceHosts, a.resolverActive(cfg)); err != nil {
		warnings = append(warnings, err.Error())
	}

	a.mu.Lock()
	a.warnings = warnings
	a.phpWarnings = phpWarn
	a.mu.Unlock()
	return nil
}

func (a *App) phpWarningsFor(minor string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.phpWarnings[minor]
}

// ensurePool starts the php-cgi workers for a version, restarting them when
// php.ini or the worker count changed.
func (a *App) ensurePool(cfg *Config, minor string) error {
	p := cfg.PHP[minor]
	ini, _ := os.ReadFile(a.phpIniPath(minor))
	hash := fmt.Sprintf("%x|%d|%s", hashBytes(ini), p.Workers, a.phpCGI(p))
	pool := a.pools[minor]
	if pool != nil && pool.iniHash == hash {
		for _, w := range pool.workers {
			if !w.Running() && w.Status().State != "restarting" {
				_ = w.Start()
			}
		}
		return nil
	}
	if pool != nil {
		a.log.Printf("PHP %s settings changed, restarting workers", minor)
		a.stopPool(minor)
	}
	ports := phpPorts(minor, p.Workers)
	disp, err := startDispatcher(phpPortBase(minor), ports, a.log)
	if err != nil {
		return err
	}
	pool = &phpPool{minor: minor, iniHash: hash, dispatcher: disp}
	tag := strings.ReplaceAll(minor, ".", "")
	for i, port := range ports {
		port := port
		inst := *p
		pool.workers = append(pool.workers, NewProc(ProcSpec{
			Name:    fmt.Sprintf("php%s-%d", tag, i),
			LogPath: a.path("logs", "php"+tag+"_cgi.log"),
			Port:    port,
			Build:   func() (*exec.Cmd, error) { return a.phpWorkerCmd(minor, &inst, port) },
		}))
	}
	a.mu.Lock()
	a.pools[minor] = pool
	a.mu.Unlock()
	var errs []string
	var wg sync.WaitGroup
	var emu sync.Mutex
	for _, w := range pool.workers {
		wg.Add(1)
		go func(w *Proc) {
			defer wg.Done()
			if err := w.Start(); err != nil {
				emu.Lock()
				errs = append(errs, err.Error())
				emu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	if len(errs) > 0 {
		return errors.New(errs[0])
	}
	return nil
}

func (a *App) stopPool(minor string) {
	pool := a.pools[minor]
	if pool == nil {
		return
	}
	var wg sync.WaitGroup
	for _, w := range pool.workers {
		wg.Add(1)
		go func(w *Proc) { defer wg.Done(); _ = w.Stop() }(w)
	}
	wg.Wait()
	pool.dispatcher.Close()
	a.mu.Lock()
	delete(a.pools, minor)
	a.mu.Unlock()
}

// StartWeb starts the PHP pools and Apache.
func (a *App) StartWeb() error {
	a.mu.Lock()
	a.webWanted = true
	a.mu.Unlock()
	if err := a.Apply(false); err != nil {
		return err
	}
	cfg := a.config()
	bin := a.apacheBin(cfg)
	if bin == "" || !exists(bin) {
		return errors.New("apache is not installed")
	}
	if err := testApacheConf(bin, a.apacheConfPath()); err != nil {
		a.apache.setFailed(err)
		return err
	}
	return a.apache.Start()
}

func (a *App) StopWeb() error {
	a.mu.Lock()
	a.webWanted = false
	a.mu.Unlock()
	err := a.apache.Stop()
	a.applyMu.Lock()
	for minor := range a.pools {
		a.stopPool(minor)
	}
	a.applyMu.Unlock()
	return err
}

func (a *App) RestartWeb() error {
	_ = a.StopWeb()
	return a.StartWeb()
}

func (a *App) StartMySQL() error {
	cfg := a.config()
	if b := a.mysqlBin(cfg); b == "" || !exists(b) {
		return errors.New("MySQL is not installed")
	}
	a.mu.Lock()
	a.mysqlWanted = true
	a.mu.Unlock()
	return a.mysql.Start()
}

func (a *App) StopMySQL() error {
	a.mu.Lock()
	a.mysqlWanted = false
	a.mu.Unlock()
	return a.mysql.Stop()
}

func (a *App) StartAll() error {
	cfg := a.config()
	var errs []string
	if b := a.mysqlBin(cfg); b != "" && exists(b) {
		if err := a.StartMySQL(); err != nil {
			errs = append(errs, "MySQL: "+err.Error())
		}
	}
	if err := a.StartWeb(); err != nil {
		errs = append(errs, "Apache: "+err.Error())
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (a *App) StopAll() error {
	var wg sync.WaitGroup
	var e1, e2 error
	wg.Add(2)
	go func() { defer wg.Done(); e1 = a.StopWeb() }()
	go func() { defer wg.Done(); e2 = a.StopMySQL() }()
	wg.Wait()
	return errors.Join(e1, e2)
}

// watch rescans the www folder periodically so new projects get vhosts.
func (a *App) watch() {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-a.quit:
			return
		case <-t.C:
			if err := a.Apply(false); err != nil {
				a.log.Printf("apply: %v", err)
			}
			a.apache.refresh()
			a.mysql.refresh()
		}
	}
}

func hashBytes(b []byte) uint64 {
	var h uint64 = 14695981039346656037
	for _, c := range b {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return h
}

type multiWriter []interface{ Write([]byte) (int, error) }

func (m multiWriter) Write(p []byte) (int, error) {
	for _, w := range m {
		_, _ = w.Write(p)
	}
	return len(p), nil
}
