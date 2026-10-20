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

	line string // release line for the support check: "13", "7.4", "6.8"
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
	// main: installed from the main branch, like the official laravel new.
	// The starter kits' tags are old (they predate PHP 8.5), main is current.
	main bool
}

func baseTemplates() []projectTemplate {
	return []projectTemplate{
		{ID: "laravel", Name: "Laravel", Description: "The PHP framework for web artisans.", Database: true, pkg: "laravel/laravel", kind: "laravel"},
		{ID: "laravel-react", Name: "Laravel + React", Description: "Laravel starter kit with React, Inertia, TypeScript and Tailwind.", Database: true, Node: true, pkg: "laravel/react-starter-kit", kind: "laravel", main: true},
		{ID: "laravel-vue", Name: "Laravel + Vue", Description: "Laravel starter kit with Vue, Inertia, TypeScript and Tailwind.", Database: true, Node: true, pkg: "laravel/vue-starter-kit", kind: "laravel", main: true},
		{ID: "laravel-svelte", Name: "Laravel + Svelte", Description: "Laravel starter kit with Svelte, Inertia, TypeScript and Tailwind.", Database: true, Node: true, pkg: "laravel/svelte-starter-kit", kind: "laravel", main: true},
		{ID: "laravel-livewire", Name: "Laravel + Livewire", Description: "Laravel starter kit with Livewire and Tailwind.", Database: true, Node: true, pkg: "laravel/livewire-starter-kit", kind: "laravel", main: true},
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
			case t.main:
				t.Versions, err = packagistMain(t.pkg)
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
	var support map[string]map[string]eolCycle
	wg.Add(1)
	go func() {
		defer wg.Done()
		support = supportedCycles("laravel", "symfony", "wordpress")
	}()
	wg.Wait()
	for i := range list {
		list[i].Versions = keepSupported(&list[i], support)
	}
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
		if seen[line] || len(out) == 12 {
			continue
		}
		seen[line] = true
		tv := templateVersion{Version: strings.TrimPrefix(ver, "v"), Label: strings.TrimPrefix(ver, "v"), PHP: php, line: line}
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

// eolCycle is one release line on endoflife.date.
type eolCycle struct {
	Cycle   string          `json:"cycle"`
	EOL     json.RawMessage `json:"eol"`     // false, true or "2027-02-24"
	Support json.RawMessage `json:"support"` // same; WordPress only patches what this marks
	PHP     string          `json:"supportedPhpVersions"`
}

// active reads an endoflife.date field: false/true or a date. For "eol",
// active means the date has not passed; for "support", true or a future date.
func eolActive(raw json.RawMessage, isEOL bool) bool {
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return b != isEOL
	}
	var d string
	if json.Unmarshal(raw, &d) == nil {
		if t, err := time.Parse("2006-01-02", d); err == nil {
			return time.Now().Before(t.Add(24 * time.Hour))
		}
	}
	return false
}

// supportedCycles asks endoflife.date which release lines still get fixes.
// A product missing from the result could not be checked.
func supportedCycles(products ...string) map[string]map[string]eolCycle {
	out := map[string]map[string]eolCycle{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, prod := range products {
		wg.Add(1)
		go func(prod string) {
			defer wg.Done()
			resp, err := httpGet("https://endoflife.date/api/"+prod+".json", "")
			if err != nil {
				return
			}
			defer resp.Body.Close()
			var cycles []eolCycle
			if json.NewDecoder(resp.Body).Decode(&cycles) != nil {
				return
			}
			m := map[string]eolCycle{}
			for _, c := range cycles {
				ok := eolActive(c.EOL, true)
				if prod == "wordpress" {
					ok = eolActive(c.Support, false)
				}
				if ok {
					m[c.Cycle] = c
				}
			}
			mu.Lock()
			out[prod] = m
			mu.Unlock()
		}(prod)
	}
	wg.Wait()
	return out
}

// phpRange turns endoflife.date's "8.3 - 8.5" into a constraint with an upper
// bound, so a framework is not paired with a PHP newer than it supports.
func phpRange(r string) string {
	lo, hi, ok := strings.Cut(r, " - ")
	hv, okv := parseVersion(strings.TrimSpace(hi) + ".0")
	if !ok || !okv {
		return ""
	}
	return fmt.Sprintf(">=%s <%d.%d", strings.TrimSpace(lo), hv[0], hv[1]+1)
}

// keepSupported drops release lines that no longer get fixes (they tend to
// break on current PHP) and adds the supported PHP range. When the support
// data is unavailable only the newest version is offered.
func keepSupported(t *projectTemplate, support map[string]map[string]eolCycle) []templateVersion {
	prod := map[string]string{"laravel": "laravel", "symfony": "symfony", "wordpress": "wordpress"}[t.kind]
	if prod == "" || len(t.Versions) == 0 {
		return t.Versions
	}
	cycles, known := support[prod]
	if t.main {
		// Starter kits follow the newest Laravel: use its PHP range.
		newest, best := "", -1
		for c := range cycles {
			if n, err := strconv.Atoi(c); err == nil && n > best {
				newest, best = c, n
			}
		}
		if r := phpRange(cycles[newest].PHP); r != "" {
			t.Versions[0].PHP = r
		}
		return t.Versions
	}
	if !known {
		v := t.Versions[0]
		v.Label += " (support status unknown)"
		return []templateVersion{v}
	}
	var out []templateVersion
	for _, v := range t.Versions {
		c, ok := cycles[v.line]
		if !ok {
			continue
		}
		if r := phpRange(c.PHP); r != "" {
			v.PHP = r
		}
		out = append(out, v)
		if len(out) == 5 {
			break
		}
	}
	if len(out) == 0 {
		return t.Versions[:1]
	}
	return out
}

// packagistMain offers the main branch of a package (with its PHP requirement).
func packagistMain(pkg string) ([]templateVersion, error) {
	resp, err := httpGet("https://repo.packagist.org/p2/"+pkg+"~dev.json", "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var data struct {
		Packages map[string][]struct {
			Version string            `json:"version"`
			Require map[string]string `json:"require"`
		} `json:"packages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	for _, v := range data.Packages[pkg] {
		if v.Version == "dev-main" {
			return []templateVersion{{Version: "dev-main", Label: "latest (main branch, as laravel new installs it)", PHP: v.Require["php"]}}, nil
		}
	}
	return nil, fmt.Errorf("no main branch found for %s", pkg)
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
		if i == 12 {
			break
		}
		v := fmt.Sprintf("%d.%d", n[0], n[1])
		if n[2] > 0 {
			v += fmt.Sprintf(".%d", n[2])
		}
		tv := templateVersion{Version: v, Label: v, PHP: ">=7.4", line: fmt.Sprintf("%d.%d", n[0], n[1])}
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
	Git      bool   `json:"git"` // git init + first commit
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
	www := a.abs(cfg.WWWDir)
	dir := filepath.Join(www, p.Name)
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
		// Checked again when the database is created, in case MySQL is not up yet.
		if a.mysql.Running() {
			if dbs, err := a.listDatabases(cfg); err == nil && containsFold(dbs, p.Database) {
				return nil, badRequest("a database named %s already exists: choose another name", p.Database)
			}
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
	if tpl.Node && a.nodeBinDir(cfg) == "" {
		return nil, badRequest("%s needs Node.js: install it on the Tools tab (it is installed system-wide), then create the project", tpl.Name)
	}
	if p.Git && toolPath("git") == "" {
		return nil, badRequest("Git is not installed: install it from the Tools tab, or untick Git")
	}
	if p.Title == "" {
		p.Title = p.Name
	}
	title := "Create " + p.Name + " (" + tpl.Name + ")"
	if a.tasks.Busy(title) {
		return nil, badRequest("%s is already running", title)
	}
	// Built in a hidden folder (the www scanner skips dot folders) and moved
	// into place at the end, so the site only appears once it works.
	stage := filepath.Join(www, ".omni-new-"+p.Name)
	return a.tasks.Run(title, func(t *Task) error {
		createdDB := false
		err := a.createProject(t, *tpl, p, stage, dir, &createdDB)
		if err != nil {
			t.Set("Cleaning up", -1)
			_ = os.RemoveAll(stage)
			msg := "Removed the unfinished project."
			if createdDB {
				_ = a.dropDatabase(a.config(), p.Database)
				msg = "Removed the unfinished project and its database."
			}
			t.AddLog(msg)
		}
		return err
	}), nil
}

type projectStep struct {
	name string
	run  func() error
}

func (a *App) createProject(t *Task, tpl projectTemplate, p newProject, stage, dir string, createdDB *bool) error {
	cfg := a.config()
	www := a.abs(cfg.WWWDir)
	stageName := filepath.Base(stage)
	host := hostLabel(p.Name) + "." + cfg.TLD
	_ = os.RemoveAll(stage) // leftover of an interrupted run
	var plan []projectStep
	add := func(name string, run func() error) { plan = append(plan, projectStep{name, run}) }

	if tpl.kind != "html" {
		add("Enable the PHP "+p.PHP+" extensions "+tpl.Name+" needs", func() error { return a.ensureProjectExtensions(t, p.PHP, tpl.kind) })
	}
	if tpl.pkg != "" && !exists(a.composerPhar()) {
		add("Install Composer", func() error { return a.installComposer(t) })
	}
	if p.Database != "" {
		add("Create the MySQL database "+p.Database, func() error {
			if err := a.createProjectDatabase(t, p.Database); err != nil {
				return err
			}
			*createdDB = true
			return nil
		})
	}
	switch tpl.kind {
	case "laravel", "symfony":
		add(fmt.Sprintf("Download %s %s and its packages (composer create-project)", tpl.Name, p.Version), func() error {
			return a.composer(t, p.PHP, www, "create-project", tpl.pkg+":"+p.Version, stageName)
		})
	case "wordpress":
		add("Download WordPress "+p.Version, func() error { return a.downloadWordPress(t, p.Version, stage) })
		add("Write wp-config.php (database, security keys)", func() error { return a.writeWPConfig(p, stage) })
	case "php":
		add("Create index.php", func() error {
			page := fmt.Sprintf("<?php\n// %s\n\necho '<h1>' . htmlspecialchars(%q) . '</h1>';\necho '<p>PHP ' . PHP_VERSION . '</p>';\n", p.Name, p.Title)
			if p.Database != "" {
				page += fmt.Sprintf("\n$db = new PDO('mysql:host=127.0.0.1;port=%d;dbname=%s;charset=utf8mb4', 'root', %s);\necho '<p>Connected to the database %s.</p>';\n",
					cfg.MySQL.Port, p.Database, phpString(cfg.MySQL.RootPassword), p.Database)
			}
			return writeNewFile(filepath.Join(stage, "index.php"), page)
		})
	case "html":
		add("Create index.html", func() error {
			page := fmt.Sprintf("<!doctype html>\n<html lang=\"en\">\n<head>\n  <meta charset=\"utf-8\">\n  <meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n  <title>%[1]s</title>\n</head>\n<body>\n  <h1>%[1]s</h1>\n</body>\n</html>\n", htmlEscape(p.Title))
			return writeNewFile(filepath.Join(stage, "index.html"), page)
		})
	}
	if tpl.kind == "symfony" && p.WebApp {
		add("Add the web app packages (composer require webapp)", func() error { return a.composer(t, p.PHP, stage, "require", "webapp") })
	}
	if tpl.kind == "symfony" && p.Database != "" {
		add("Set DATABASE_URL in .env.local", func() error {
			return setEnvValues(filepath.Join(stage, ".env.local"), map[string]string{"DATABASE_URL": a.symfonyDatabaseURL(p.Database)})
		})
	}
	if tpl.kind == "laravel" {
		what := "app name, URL, Mailpit"
		if p.Database != "" {
			what = "app name, URL, database, Mailpit"
		}
		add("Configure .env ("+what+")", func() error { return a.configureLaravel(p, stage) })
		if p.Database != "" {
			add("Run the database migrations", func() error { return a.artisan(t, p.PHP, stage, "migrate", "--force") })
		}
	}
	if tpl.Node {
		add("Install the npm packages (npm install)", func() error { return a.npm(t, p.PHP, stage, "install") })
		add("Build the front end (npm run build)", func() error { return a.npm(t, p.PHP, stage, "run", "build") })
	}
	if p.Git {
		add("Initialize a Git repository", func() error { return a.gitInit(t, stage) })
	}
	add("Publish http://"+host, func() error { return a.publishProject(p, stage, dir) })
	if tpl.kind == "wordpress" {
		add("Install WordPress (site title, admin account)", func() error { return a.installWordPress(t, p) })
	}

	names := make([]string, len(plan))
	for i, s := range plan {
		names[i] = s.name
	}
	t.SetSteps(names)
	for i, s := range plan {
		t.StepStatus(i, "running")
		t.AddLog("── " + s.name)
		if err := s.run(); err != nil {
			return err
		}
		t.StepStatus(i, "done")
	}
	scheme := "http"
	if cfg.SSL {
		scheme = "https"
	}
	t.SetURL(scheme + "://" + host + "/")
	if tpl.kind == "wordpress" {
		t.AddLog("Log in at " + scheme + "://" + host + "/wp-admin/ as " + p.AdminUser + ".")
	}
	t.Set("Ready: "+scheme+"://"+host, 1)
	return nil
}

// removeUnfinishedProjects deletes build folders left behind when Omni Web
// Server was closed while creating a project.
func (a *App) removeUnfinishedProjects() {
	www := a.abs(a.config().WWWDir)
	entries, _ := os.ReadDir(www)
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), ".omni-new-") {
			a.log.Printf("removing the unfinished project %s", e.Name())
			_ = os.RemoveAll(filepath.Join(www, e.Name()))
		}
	}
}

// publishProject moves the finished project into www and serves it with the
// chosen PHP version.
func (a *App) publishProject(p newProject, stage, dir string) error {
	var err error
	for i := 0; i < 10; i++ { // antivirus scanners can hold files for a moment
		if err = os.Rename(stage, dir); err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err != nil {
		return fmt.Errorf("moving the project into place: %v", err)
	}
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
	if !a.apache.Running() {
		return a.StartWeb()
	}
	return nil
}

// gitInit creates a repository with a first commit. Without a Git identity
// (user.name / user.email) the files are only staged.
func (a *App) gitInit(t *Task, dir string) error {
	git := toolPath("git")
	if err := a.runLogged(t, "git init", dir, os.Environ(), git, "init", "-b", "main"); err != nil {
		return err
	}
	if err := a.runLogged(t, "git add", dir, os.Environ(), git, "add", "-A"); err != nil {
		return err
	}
	name, _ := hiddenCommand(git, "config", "user.name").Output()
	email, _ := hiddenCommand(git, "config", "user.email").Output()
	if strings.TrimSpace(string(name)) == "" || strings.TrimSpace(string(email)) == "" {
		t.AddLog("No Git user.name/user.email is set: the files are staged but not committed.")
		return nil
	}
	return a.runLogged(t, "git commit", dir, os.Environ(), git, "commit", "-q", "-m", "Initial commit")
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
	if d := a.nodeBinDir(cfg); d != "" {
		dirs = append(dirs, d)
	}
	dirs = append(dirs, a.path("bin", "shims"))
	// PHPRC makes every PHP started below (Composer's "@php artisan", scripts
	// that call php themselves) load this version's php.ini and extensions.
	env := []string{"PHP_INI_SCAN_DIR=", "PHPRC=" + filepath.Dir(a.phpIniPath(minor)), "COMPOSER_NO_INTERACTION=1", "COMPOSER_ALLOW_SUPERUSER=1"}
	for _, e := range os.Environ() {
		if k, v, ok := strings.Cut(e, "="); ok && strings.EqualFold(k, "PATH") {
			env = append(env, k+"="+strings.Join(dirs, string(os.PathListSeparator))+string(os.PathListSeparator)+v)
		} else if !strings.EqualFold(k, "PHP_INI_SCAN_DIR") && !strings.EqualFold(k, "PHPRC") {
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
		if strings.Contains(args[1], ":dev-") {
			full = append(full, "--stability=dev")
		}
	}
	return a.runLogged(t, "composer "+strings.Join(args, " "), dir, a.toolEnv(minor), a.phpCLI(p), full...)
}

func (a *App) artisan(t *Task, minor, dir string, args ...string) error {
	cfg := a.config()
	p := cfg.PHP[minor]
	return a.runLogged(t, "php artisan "+strings.Join(args, " "), dir, a.toolEnv(minor), a.phpCLI(p), append([]string{"-c", a.phpIniPath(minor), "artisan"}, args...)...)
}

func (a *App) npm(t *Task, minor, dir string, args ...string) error {
	dir0 := a.nodeBinDir(a.config())
	npm := filepath.Join(dir0, "npm.cmd")
	if !exists(npm) {
		npm = filepath.Join(dir0, "npm")
	}
	return a.runLogged(t, "npm "+strings.Join(args, " "), dir, a.toolEnv(minor), npm, args...)
}

// runLogged runs a command, streams its output into the task (shown live in
// the dashboard) and keeps the full output in logs/projects.log.
func (a *App) runLogged(t *Task, label, dir string, env []string, name string, args ...string) error {
	t.AddLog("$ " + label)
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
			// Keep the last few meaningful lines for the error message, without
			// the usage synopsis Composer prints after an error.
			if !strings.Contains(line, "[--") {
				last = append(last, line)
				if len(last) > 4 {
					last = last[1:]
				}
			}
			if len(line) > 200 {
				line = line[:200] + "…"
			}
			t.AddLog(line)
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

func (a *App) configureLaravel(p newProject, dir string) error {
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
	return setEnvValues(filepath.Join(dir, ".env"), vals)
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
