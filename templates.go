package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Project templates: "New project" in the dashboard creates a folder in www
// from a template (Laravel and its starter kits, WordPress, Symfony, plain PHP
// or HTML), optionally with its own MySQL database, and configures it so the
// site works at http://<name>.<tld> right away. The newest five versions of
// each template are offered; the list comes live from Packagist / wordpress.org.

type templateVersion struct {
	Version string `json:"version"` // what is installed: "13.11.0", "8.1.*", "6.8.3"
	Label   string `json:"label"`   // "13.11.0 (latest)", "7.4 LTS"
	PHP     string `json:"php,omitempty"`
}

type projectTemplate struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Database    bool              `json:"database"` // offers a MySQL database
	Node        bool              `json:"node"`     // runs npm install + build
	Versions    []templateVersion `json:"versions"`
	Error       string            `json:"error,omitempty"`

	pkg  string // Packagist package for composer create-project
	kind string // laravel | symfony | wordpress | php | html
}

func baseTemplates() []projectTemplate {
	return []projectTemplate{
		{ID: "laravel", Name: "Laravel", Description: "The PHP framework for web artisans.", Database: true, pkg: "laravel/laravel", kind: "laravel"},
		{ID: "laravel-react", Name: "Laravel + React", Description: "Laravel starter kit with React, Inertia, TypeScript and Tailwind.", Database: true, Node: true, pkg: "laravel/react-starter-kit", kind: "laravel"},
		{ID: "laravel-vue", Name: "Laravel + Vue", Description: "Laravel starter kit with Vue, Inertia, TypeScript and Tailwind.", Database: true, Node: true, pkg: "laravel/vue-starter-kit", kind: "laravel"},
		{ID: "laravel-livewire", Name: "Laravel + Livewire", Description: "Laravel starter kit with Livewire and Tailwind.", Database: true, Node: true, pkg: "laravel/livewire-starter-kit", kind: "laravel"},
		{ID: "wordpress", Name: "WordPress", Description: "Blog and CMS. Installed with your site title and admin account.", Database: true, kind: "wordpress"},
		{ID: "symfony", Name: "Symfony", Description: "Symfony skeleton, optionally as a full web app.", Database: true, pkg: "symfony/skeleton", kind: "symfony"},
		{ID: "php", Name: "Blank PHP", Description: "An empty project with an index.php.", Database: true, kind: "php"},
		{ID: "html", Name: "Static HTML", Description: "An index.html, served by Apache.", kind: "html"},
	}
}

var templateCache struct {
	sync.Mutex
	at   time.Time
	list []projectTemplate
}

// templates returns the catalogue with live version lists (cached for an hour).
func (a *App) templates(fresh bool) []projectTemplate {
	templateCache.Lock()
	defer templateCache.Unlock()
	if !fresh && templateCache.list != nil && time.Since(templateCache.at) < time.Hour {
		return templateCache.list
	}
	list := baseTemplates()
	var wg sync.WaitGroup
	for i := range list {
		wg.Add(1)
		go func(t *projectTemplate) {
			defer wg.Done()
			var err error
			switch {
			case t.pkg != "":
				t.Versions, err = packagistVersions(t.pkg, t.kind == "symfony")
			case t.kind == "wordpress":
				t.Versions, err = wordpressVersions()
			}
			if err != nil {
				t.Error = err.Error()
			}
		}(&list[i])
	}
	wg.Wait()
	templateCache.list, templateCache.at = list, time.Now()
	return list
}

var stableVersionRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

// packagistVersions lists the newest five release lines of a package: one per
// major version (Laravel) or per minor version (Symfony, whose x.4 is LTS).
func packagistVersions(pkg string, byMinor bool) ([]templateVersion, error) {
	resp, err := httpGet("https://repo.packagist.org/p2/"+pkg+".json", "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var data struct {
		Packages map[string][]map[string]json.RawMessage `json:"packages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	var out []templateVersion
	seen := map[string]bool{}
	php := ""
	// The p2 format is "minified": a version only lists fields that changed
	// since the one before it, so "require" is carried forward.
	for _, v := range data.Packages[pkg] {
		if raw, ok := v["require"]; ok {
			var req map[string]string
			if json.Unmarshal(raw, &req) == nil {
				php = req["php"]
			} else {
				php = ""
			}
		}
		var ver string
		_ = json.Unmarshal(v["version"], &ver)
		m := stableVersionRe.FindStringSubmatch(ver)
		if m == nil {
			continue
		}
		line := m[1]
		if byMinor {
			line = m[1] + "." + m[2]
		}
		if seen[line] || len(out) == 5 {
			continue
		}
		seen[line] = true
		tv := templateVersion{Version: strings.TrimPrefix(ver, "v"), Label: strings.TrimPrefix(ver, "v"), PHP: php}
		if byMinor {
			// Symfony skeletons are tagged x.y.99; install the x.y line.
			tv.Version, tv.Label = line+".*", line
			if m[2] == "4" {
				tv.Label += " LTS"
			}
		}
		if len(out) == 0 {
			tv.Label += " (latest)"
		}
		out = append(out, tv)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no releases found for %s", pkg)
	}
	return out, nil
}

// wordpressVersions lists the newest patch release of the five newest
// WordPress branches.
func wordpressVersions() ([]templateVersion, error) {
	resp, err := httpGet("https://api.wordpress.org/core/stable-check/1.0/", "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var all map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&all); err != nil {
		return nil, err
	}
	best := map[string][3]int{}
	for v := range all {
		parts := strings.Split(v, ".")
		if len(parts) < 2 {
			continue
		}
		var n [3]int
		ok := true
		for i := 0; i < 3 && i < len(parts); i++ {
			x, err := strconv.Atoi(parts[i])
			if err != nil {
				ok = false
				break
			}
			n[i] = x
		}
		branch := parts[0] + "." + parts[1]
		if cur, have := best[branch]; ok && (!have || versionNewer(n, cur)) {
			best[branch] = n
		}
	}
	vers := make([][3]int, 0, len(best))
	for _, n := range best {
		vers = append(vers, n)
	}
	sort.Slice(vers, func(i, j int) bool { return versionNewer(vers[i], vers[j]) })
	var out []templateVersion
	for i, n := range vers {
		if i == 5 {
			break
		}
		v := fmt.Sprintf("%d.%d", n[0], n[1])
		if n[2] > 0 {
			v += fmt.Sprintf(".%d", n[2])
		}
		tv := templateVersion{Version: v, Label: v, PHP: ">=7.4"}
		if i == 0 {
			tv.Label += " (latest)"
		}
		out = append(out, tv)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no WordPress releases found")
	}
	return out, nil
}

// phpSatisfies checks a Composer PHP constraint ("^8.2", ">=8.1", "^7.3|^8.0")
// against a PHP minor version such as "8.3". Unknown forms are accepted, so
// Composer has the final word.
func phpSatisfies(constraint, minor string) bool {
	have, ok := parseVersion(minor + ".99")
	if constraint == "" || !ok {
		return true
	}
	for _, alt := range regexp.MustCompile(`\s*\|\|?\s*`).Split(constraint, -1) {
		okAll := true
		for _, c := range strings.Fields(strings.ReplaceAll(alt, ",", " ")) {
			if !phpTerm(c, have) {
				okAll = false
				break
			}
		}
		if okAll {
			return true
		}
	}
	return false
}

func phpTerm(c string, have [3]int) bool {
	op := strings.TrimRight(c, "0123456789.*")
	if i := strings.IndexAny(c, "0123456789"); i >= 0 {
		op = c[:i]
	}
	ver := strings.TrimSuffix(strings.TrimPrefix(c, op), ".*")
	parts := strings.Split(ver, ".")
	var v [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return true
		}
		v[i] = n
	}
	ge := !versionNewer(v, have)
	switch op {
	case "^", "~":
		return ge && have[0] == v[0]
	case ">=", "":
		return ge
	case ">":
		return versionNewer(have, v)
	case "<":
		return versionNewer(v, have)
	case "<=":
		return !versionNewer(have, v)
	}
	return true
}

// ---------- creating a project ----------

type newProject struct {
	Template string `json:"template"`
	Version  string `json:"version"`
	Name     string `json:"name"`
	PHP      string `json:"php"`
	Database string `json:"database"` // empty: no database
	Title    string `json:"title"`
	// WordPress admin account
	AdminUser     string `json:"admin_user"`
	AdminPassword string `json:"admin_password"`
	AdminEmail    string `json:"admin_email"`
	// Symfony: full web app instead of the bare skeleton
	WebApp bool `json:"webapp"`
}

func (a *App) startNewProject(p newProject) (*Task, error) {
	var tpl *projectTemplate
	for _, t := range baseTemplates() {
		if t.ID == p.Template {
			tpl = &t
			break
		}
	}
	if tpl == nil {
		return nil, badRequest("unknown template %q", p.Template)
	}
	if !validSiteName(p.Name) {
		return nil, badRequest("invalid project name (letters, digits, dot, dash, underscore)")
	}
	cfg := a.config()
	dir := filepath.Join(a.abs(cfg.WWWDir), p.Name)
	if exists(dir) {
		return nil, badRequest("%s already exists", dir)
	}
	if _, ok := cfg.Sites[p.Name]; ok {
		return nil, badRequest("a site named %s already exists", p.Name)
	}
	if p.PHP == "" {
		p.PHP = cfg.DefaultPHP
	}
	if tpl.kind != "html" {
		if _, ok := cfg.PHP[p.PHP]; !ok {
			return nil, badRequest("PHP %s is not installed", p.PHP)
		}
	}
	if (tpl.pkg != "" || tpl.kind == "wordpress") && p.Version == "" {
		return nil, badRequest("choose a version")
	}
	for _, t := range a.templates(false) {
		for _, v := range t.Versions {
			if t.ID == tpl.ID && v.Version == p.Version && !phpSatisfies(v.PHP, p.PHP) {
				return nil, badRequest("%s %s needs PHP %s: choose another PHP version, or install one on the PHP tab", tpl.Name, v.Label, v.PHP)
			}
		}
	}
	if p.Database != "" {
		if !tpl.Database || !dbNameRe.MatchString(p.Database) {
			return nil, badRequest("invalid database name")
		}
		if !installed(a.mysqlBin(cfg)) {
			return nil, badRequest("MySQL is not installed: install it on the Overview tab, or create the project without a database")
		}
	}
	if tpl.kind == "wordpress" {
		if p.Database == "" {
			return nil, badRequest("WordPress needs a database")
		}
		if p.AdminUser == "" || p.AdminPassword == "" || !strings.Contains(p.AdminEmail, "@") {
			return nil, badRequest("enter the WordPress admin user, password and e-mail")
		}
	}
	if p.Title == "" {
		p.Title = p.Name
	}
	title := "Create " + p.Name + " (" + tpl.Name + ")"
	if a.tasks.Busy(title) {
		return nil, badRequest("%s is already running", title)
	}
	return a.tasks.Run(title, func(t *Task) error {
		err := a.createProject(t, *tpl, p, dir)
		if err != nil && exists(dir) {
			t.Set("Removing the unfinished project", -1)
			_ = os.RemoveAll(dir)
		}
		return err
	}), nil
}

func (a *App) createProject(t *Task, tpl projectTemplate, p newProject, dir string) error {
	cfg := a.config()
	if tpl.kind != "html" {
		if err := a.ensureProjectExtensions(t, p.PHP, tpl.kind); err != nil {
			return err
		}
	}
	if tpl.pkg != "" && !exists(a.composerPhar()) {
		if err := a.installComposer(t); err != nil {
			return fmt.Errorf("installing Composer: %v", err)
		}
	}
	if tpl.Node && a.nodeDir(a.config()) == "" {
		if err := a.installNode(t, "lts"); err != nil {
			return fmt.Errorf("installing Node.js: %v", err)
		}
	}
	if p.Database != "" {
		if err := a.createProjectDatabase(t, p.Database); err != nil {
			return err
		}
	}

	switch tpl.kind {
	case "laravel":
		if err := a.composer(t, p.PHP, a.abs(cfg.WWWDir), "create-project", tpl.pkg+":"+p.Version, p.Name); err != nil {
			return err
		}
		if err := a.configureLaravel(t, p, dir); err != nil {
			return err
		}
	case "symfony":
		if err := a.composer(t, p.PHP, a.abs(cfg.WWWDir), "create-project", tpl.pkg+":"+p.Version, p.Name); err != nil {
			return err
		}
		if p.WebApp {
			if err := a.composer(t, p.PHP, dir, "require", "webapp"); err != nil {
				return err
			}
		}
		if p.Database != "" {
			if err := setEnvValues(filepath.Join(dir, ".env.local"), map[string]string{"DATABASE_URL": a.symfonyDatabaseURL(p.Database)}); err != nil {
				return err
			}
		}
	case "wordpress":
		if err := a.downloadWordPress(t, p.Version, dir); err != nil {
			return err
		}
		if err := a.writeWPConfig(p, dir); err != nil {
			return err
		}
	case "php":
		page := fmt.Sprintf("<?php\n// %s\n\necho '<h1>' . htmlspecialchars(%q) . '</h1>';\necho '<p>PHP ' . PHP_VERSION . '</p>';\n", p.Name, p.Title)
		if p.Database != "" {
			page += fmt.Sprintf("\n$db = new PDO('mysql:host=127.0.0.1;port=%d;dbname=%s;charset=utf8mb4', 'root', %s);\necho '<p>Connected to the database %s.</p>';\n",
				cfg.MySQL.Port, p.Database, phpString(cfg.MySQL.RootPassword), p.Database)
		}
		if err := writeNewFile(filepath.Join(dir, "index.php"), page); err != nil {
			return err
		}
	case "html":
		page := fmt.Sprintf("<!doctype html>\n<html lang=\"en\">\n<head>\n  <meta charset=\"utf-8\">\n  <meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n  <title>%[1]s</title>\n</head>\n<body>\n  <h1>%[1]s</h1>\n</body>\n</html>\n", htmlEscape(p.Title))
		if err := writeNewFile(filepath.Join(dir, "index.html"), page); err != nil {
			return err
		}
	}

	if tpl.Node {
		if err := a.npm(t, p.PHP, dir, "install"); err != nil {
			return err
		}
		if err := a.npm(t, p.PHP, dir, "run", "build"); err != nil {
			return err
		}
	}

	// Register the vhost with the chosen PHP version and serve it.
	t.Set("Setting up http://"+hostLabel(p.Name)+"."+cfg.TLD, -1)
	if err := a.update(func(c *Config) error {
		sc := c.Sites[p.Name]
		if sc == nil {
			sc = &SiteConfig{}
			c.Sites[p.Name] = sc
		}
		sc.Enabled = true
		if p.PHP != c.DefaultPHP {
			sc.PHP = p.PHP
		}
		return nil
	}); err != nil {
		return err
	}
	if err := a.Apply(true); err != nil {
		return err
	}
	if tpl.kind == "wordpress" {
		if err := a.installWordPress(t, p); err != nil {
			return err
		}
	}
	t.Set("Ready: http://"+hostLabel(p.Name)+"."+cfg.TLD, 1)
	return nil
}

// ensureProjectExtensions switches on the PHP extensions a template needs.
func (a *App) ensureProjectExtensions(t *Task, minor, kind string) error {
	need := map[string][]string{
		"laravel":   {"curl", "fileinfo", "mbstring", "openssl", "pdo_mysql", "pdo_sqlite", "sqlite3", "zip"},
		"symfony":   {"curl", "intl", "mbstring", "openssl", "pdo_mysql", "zip"},
		"wordpress": {"curl", "exif", "fileinfo", "gd", "mbstring", "mysqli", "openssl", "zip"},
		"php":       {"mbstring", "openssl", "pdo_mysql"},
	}[kind]
	var added []string
	err := a.update(func(c *Config) error {
		p, ok := c.PHP[minor]
		if !ok {
			return fmt.Errorf("PHP %s is not installed", minor)
		}
		avail := a.availableExtensions(p)
		for _, e := range need {
			if !contains(p.Builtin, e) && !contains(p.Extensions, e) && contains(avail, e) {
				p.Extensions = append(p.Extensions, e)
				added = append(added, e)
			}
		}
		sort.Strings(p.Extensions)
		return nil
	})
	if err != nil || len(added) == 0 {
		return err
	}
	t.Set("Enabling PHP extensions: "+strings.Join(added, ", "), -1)
	return a.Apply(false)
}

func (a *App) createProjectDatabase(t *Task, name string) error {
	cfg := a.config()
	if !a.mysql.Running() {
		t.Set("Starting MySQL", -1)
		if err := a.StartMySQL(); err != nil {
			return fmt.Errorf("starting MySQL: %v", err)
		}
	}
	dbs, err := a.listDatabases(cfg)
	if err != nil {
		return err
	}
	for _, d := range dbs {
		if strings.EqualFold(d, name) {
			return fmt.Errorf("the database %s already exists: choose another name", name)
		}
	}
	t.Set("Creating the database "+name, -1)
	return a.createDatabase(cfg, name)
}

// toolEnv puts the project's PHP, Node.js and the shims first on PATH, so
// Composer scripts ("@php artisan") and npm scripts find the right versions.
func (a *App) toolEnv(minor string) []string {
	cfg := a.config()
	var dirs []string
	if p, ok := cfg.PHP[minor]; ok {
		dirs = append(dirs, filepath.Dir(a.phpCLI(p)))
	}
	if d := a.nodeDir(cfg); d != "" {
		dirs = append(dirs, d)
	}
	dirs = append(dirs, a.path("bin", "shims"))
	env := []string{"PHP_INI_SCAN_DIR=", "COMPOSER_NO_INTERACTION=1", "COMPOSER_ALLOW_SUPERUSER=1"}
	for _, e := range os.Environ() {
		if k, v, ok := strings.Cut(e, "="); ok && strings.EqualFold(k, "PATH") {
			env = append(env, k+"="+strings.Join(dirs, string(os.PathListSeparator))+string(os.PathListSeparator)+v)
		} else if !strings.EqualFold(k, "PHP_INI_SCAN_DIR") {
			env = append(env, e)
		}
	}
	return env
}

func (a *App) composer(t *Task, minor, dir string, args ...string) error {
	cfg := a.config()
	p, ok := cfg.PHP[minor]
	if !ok {
		return fmt.Errorf("PHP %s is not installed", minor)
	}
	full := append([]string{"-c", a.phpIniPath(minor), a.composerPhar()}, args...)
	full = append(full, "--no-interaction", "--no-progress", "--no-ansi")
	if args[0] == "create-project" {
		full = append(full, "--prefer-dist")
	}
	return a.runLogged(t, "composer "+strings.Join(args, " "), dir, a.toolEnv(minor), a.phpCLI(p), full...)
}

func (a *App) artisan(t *Task, minor, dir string, args ...string) error {
	cfg := a.config()
	p := cfg.PHP[minor]
	return a.runLogged(t, "php artisan "+strings.Join(args, " "), dir, a.toolEnv(minor), a.phpCLI(p), append([]string{"-c", a.phpIniPath(minor), "artisan"}, args...)...)
}

func (a *App) npm(t *Task, minor, dir string, args ...string) error {
	npm := filepath.Join(a.nodeDir(a.config()), "npm.cmd")
	if !exists(npm) {
		npm = filepath.Join(a.nodeDir(a.config()), "npm")
	}
	return a.runLogged(t, "npm "+strings.Join(args, " "), dir, a.toolEnv(minor), npm, args...)
}

// runLogged runs a command, shows its latest output line in the task and keeps
// the full output in logs/projects.log.
func (a *App) runLogged(t *Task, label, dir string, env []string, name string, args ...string) error {
	t.Set(label, -1)
	logf, err := os.OpenFile(a.path("logs", "projects.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()
	fmt.Fprintf(logf, "\n==== %s: %s (in %s)\n", time.Now().Format(time.RFC3339), label, dir)
	cmd := hiddenCommand(name, args...)
	cmd.Dir, cmd.Env = dir, env
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		return fmt.Errorf("%s: %v", label, err)
	}
	var last []string
	done := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(ansiRe.ReplaceAllString(sc.Text(), ""))
			fmt.Fprintln(logf, line)
			if line == "" {
				continue
			}
			last = append(last, line)
			if len(last) > 8 {
				last = last[1:]
			}
			if len(line) > 120 {
				line = line[:120] + "…"
			}
			t.Set(label+": "+line, -1)
		}
		close(done)
	}()
	err = cmd.Wait()
	pw.Close()
	<-done
	if err != nil {
		return fmt.Errorf("%s failed: %s (full output in the projects log)", label, strings.Join(last, " | "))
	}
	return nil
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func (a *App) configureLaravel(t *Task, p newProject, dir string) error {
	cfg := a.config()
	scheme := "http"
	if cfg.SSL {
		scheme = "https"
	}
	vals := map[string]string{
		"APP_NAME": envQuote(p.Title),
		"APP_URL":  scheme + "://" + hostLabel(p.Name) + "." + cfg.TLD,
	}
	if p.Database != "" {
		vals["DB_CONNECTION"] = "mysql" // also right for MariaDB, on every Laravel version
		vals["DB_HOST"] = "127.0.0.1"
		vals["DB_PORT"] = strconv.Itoa(cfg.MySQL.Port)
		vals["DB_DATABASE"] = p.Database
		vals["DB_USERNAME"] = "root"
		vals["DB_PASSWORD"] = envQuote(cfg.MySQL.RootPassword)
	}
	if cfg.Mailpit.SMTPPort != 0 {
		vals["MAIL_MAILER"] = "smtp"
		vals["MAIL_HOST"] = "127.0.0.1"
		vals["MAIL_PORT"] = strconv.Itoa(cfg.Mailpit.SMTPPort)
	}
	t.Set("Configuring .env", -1)
	if err := setEnvValues(filepath.Join(dir, ".env"), vals); err != nil {
		return err
	}
	if p.Database != "" {
		return a.artisan(t, p.PHP, dir, "migrate", "--force")
	}
	return nil
}

// setEnvValues sets KEY=value lines in a dotenv file, uncommenting "# KEY="
// lines and appending keys that are missing.
func setEnvValues(path string, vals map[string]string) error {
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	done := map[string]bool{}
	for i, l := range lines {
		k, _, ok := strings.Cut(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "#")), "=")
		k = strings.TrimSpace(k)
		if v, want := vals[k]; ok && want && !done[k] {
			lines[i] = k + "=" + v
			done[k] = true
		}
	}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		if !done[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := strings.TrimRight(strings.Join(lines, "\n"), "\n")
	for _, k := range keys {
		out += "\n" + k + "=" + vals[k]
	}
	return os.WriteFile(path, []byte(strings.TrimLeft(out, "\n")+"\n"), 0o644)
}

// envQuote quotes a dotenv value when it contains spaces or special characters.
func envQuote(v string) string {
	if v == "" || regexp.MustCompile(`^[A-Za-z0-9_./:@-]+$`).MatchString(v) {
		return v
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
}

func (a *App) symfonyDatabaseURL(db string) string {
	cfg := a.config()
	ver := a.cachedVersion("mysql", func() string { return binVersion(a.mysqlBin(cfg), mysqlVersionRe, "--version") })
	server := "8.4"
	if cfg.MySQL.Flavor == "mariadb" {
		server = "11.4-MariaDB"
	}
	if ver != "" {
		server = ver
		if cfg.MySQL.Flavor == "mariadb" && !strings.Contains(ver, "MariaDB") {
			server += "-MariaDB"
		}
	}
	u := url.URL{Scheme: "mysql", User: url.UserPassword("root", cfg.MySQL.RootPassword), Host: fmt.Sprintf("127.0.0.1:%d", cfg.MySQL.Port), Path: "/" + db}
	if cfg.MySQL.RootPassword == "" {
		u.User = url.User("root")
	}
	return `"` + u.String() + "?serverVersion=" + server + `&charset=utf8mb4"`
}

func (a *App) downloadWordPress(t *Task, version, dir string) error {
	zipPath, err := a.download(t, "https://wordpress.org/wordpress-"+version+".zip", "", "")
	if err != nil {
		return err
	}
	defer os.Remove(zipPath)
	tmp := dir + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := extractZip(t, zipPath, tmp); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	defer os.RemoveAll(tmp)
	return os.Rename(filepath.Join(tmp, "wordpress"), dir)
}

var wpSaltRe = regexp.MustCompile(`define\(\s*'([A-Z_]+)',\s*'put your unique phrase here'\s*\);`)

func (a *App) writeWPConfig(p newProject, dir string) error {
	cfg := a.config()
	b, err := os.ReadFile(filepath.Join(dir, "wp-config-sample.php"))
	if err != nil {
		return err
	}
	s := strings.NewReplacer(
		"'database_name_here'", phpString(p.Database),
		"'username_here'", "'root'",
		"'password_here'", phpString(cfg.MySQL.RootPassword),
		"'localhost'", phpString(fmt.Sprintf("127.0.0.1:%d", cfg.MySQL.Port)),
	).Replace(string(b))
	s = wpSaltRe.ReplaceAllStringFunc(s, func(m string) string {
		key := wpSaltRe.FindStringSubmatch(m)[1]
		buf := make([]byte, 32)
		_, _ = rand.Read(buf)
		return fmt.Sprintf("define( '%s', '%s' );", key, hex.EncodeToString(buf))
	})
	return writeNewFile(filepath.Join(dir, "wp-config.php"), s)
}

// installWordPress runs the WordPress installer through the new vhost with the
// site title and admin account from the dialog.
func (a *App) installWordPress(t *Task, p newProject) error {
	cfg := a.config()
	if !a.apache.Running() {
		t.Set("Starting Apache", -1)
		if err := a.StartWeb(); err != nil {
			return fmt.Errorf("starting Apache: %v", err)
		}
	}
	host := hostLabel(p.Name) + "." + cfg.TLD
	base := fmt.Sprintf("http://127.0.0.1:%d", cfg.HTTPPort)
	client := &http.Client{Timeout: 60 * time.Second}
	t.Set("Installing WordPress", -1)
	var lastErr error
	for i := 0; i < 20; i++ { // Apache may still be reloading with the new vhost
		form := url.Values{
			"weblog_title": {p.Title}, "user_name": {p.AdminUser},
			"admin_password": {p.AdminPassword}, "admin_password2": {p.AdminPassword}, "pw_weak": {"1"},
			"admin_email": {p.AdminEmail}, "blog_public": {"0"}, "language": {""},
		}
		req, _ := http.NewRequest("POST", base+"/wp-admin/install.php?step=2", strings.NewReader(form.Encode()))
		req.Host = host
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := client.Do(req)
		if err == nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			switch {
			case resp.StatusCode == 200 && (strings.Contains(string(body), "wp-login.php") || strings.Contains(string(body), "Already Installed")):
				return nil
			case resp.StatusCode == 200:
				lastErr = fmt.Errorf("the WordPress installer did not finish; open http://%s/wp-admin/install.php to complete it", host)
			default:
				lastErr = fmt.Errorf("WordPress installer: HTTP %d", resp.StatusCode)
			}
		} else {
			lastErr = err
		}
		time.Sleep(time.Second)
	}
	return lastErr
}

func writeNewFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func phpString(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}
