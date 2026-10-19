package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

// Import from Laragon / XAMPP: find an existing stack, copy its projects, move
// its databases (dumped with its own MySQL, imported into ours), switch it off
// and make Omni Web Server the main server. Nothing of the old stack is deleted.

type migrationSource struct {
	Kind        string   `json:"kind"` // laragon | xampp
	Name        string   `json:"name"`
	Root        string   `json:"root"`
	WWW         string   `json:"www"`
	Projects    []string `json:"projects"`
	Databases   []string `json:"databases"`
	MySQLBin    string   `json:"mysql_bin"`
	DataDir     string   `json:"data_dir"`
	Flavor      string   `json:"flavor"` // mysql | mariadb
	PHP         string   `json:"php"`    // full version, e.g. 8.3.30
	HasPassword bool     `json:"has_password"`
	Steps       []string `json:"steps"`
	// Already in Omni Web Server (same folder or database name), filled in by
	// the dashboard API; these are skipped.
	ImportedProjects  []string `json:"imported_projects"`
	ImportedDatabases []string `json:"imported_databases"`

	password string
}

var (
	migrateMu    sync.Mutex
	migrateCache []migrationSource
	migrateTime  time.Time
)

// detectSources finds Laragon and XAMPP installations (cached for 30 seconds).
func detectSources(fresh bool) []migrationSource {
	migrateMu.Lock()
	defer migrateMu.Unlock()
	if !fresh && migrateCache != nil && time.Since(migrateTime) < 30*time.Second {
		return migrateCache
	}
	out := []migrationSource{}
	if runtime.GOOS == "windows" {
		for _, root := range candidateRoots("laragon", "laragon.exe") {
			out = append(out, detectLaragon(root))
		}
		for _, root := range candidateRoots("xampp", "xampp-control.exe") {
			out = append(out, detectXAMPP(root))
		}
	}
	migrateCache, migrateTime = out, time.Now()
	return out
}

// candidateRoots looks for <drive>:\<name>\<marker> and for folders named
// <name> that appear in PATH entries (installs in custom locations).
func candidateRoots(name, marker string) []string {
	var roots []string
	seen := map[string]bool{}
	add := func(dir string) {
		key := strings.ToLower(filepath.Clean(dir))
		if !seen[key] && exists(filepath.Join(dir, marker)) {
			seen[key] = true
			roots = append(roots, filepath.Clean(dir))
		}
	}
	for d := 'C'; d <= 'Z'; d++ {
		add(string(d) + `:\` + name)
	}
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		lower := strings.ToLower(p)
		if i := strings.Index(lower, `\`+name+`\`); i >= 0 {
			add(p[:i+1+len(name)])
		}
	}
	return roots
}

func detectLaragon(root string) migrationSource {
	s := migrationSource{Kind: "laragon", Name: "Laragon", Root: root, WWW: filepath.Join(root, "www"), Flavor: "mysql"}
	ini := readINI(filepath.Join(root, "usr", "laragon.ini"))
	if v := ini["documentroot"]; v != "" && isDir(v) {
		s.WWW = filepath.Clean(v)
	}
	s.password = ini["mysql_root_password"]
	dataRoot := ini["datadir"]
	if dataRoot == "" || !isDir(dataRoot) {
		dataRoot = filepath.Join(root, "data")
	}
	var servers []string
	for _, n := range []string{"mysqld", "mariadbd"} {
		m, _ := filepath.Glob(filepath.Join(root, "bin", "mysql", "*", "bin", exeName(n)))
		servers = append(servers, m...)
	}
	sort.Slice(servers, func(i, j int) bool { return compareVersions(versionInPath(servers[i]), versionInPath(servers[j])) < 0 })
	if len(servers) > 0 {
		s.MySQLBin = servers[len(servers)-1]
		if strings.Contains(strings.ToLower(s.MySQLBin), "maria") {
			s.Flavor = "mariadb"
		}
		// Laragon keeps one data folder per server line: data\mysql-8.4, data\mariadb-11.4
		minor := minorOf(versionInPath(s.MySQLBin))
		dirs, _ := os.ReadDir(dataRoot)
		for _, d := range dirs {
			p := filepath.Join(dataRoot, d.Name())
			if !d.IsDir() || !isDir(filepath.Join(p, "mysql")) {
				continue
			}
			if s.DataDir == "" || (minor != "" && strings.HasSuffix(d.Name(), minor)) {
				s.DataDir = p
			}
		}
	}
	// PHP: the version on the user's PATH (the one Laragon is switched to), else the newest.
	phps, _ := filepath.Glob(filepath.Join(root, "bin", "php", "php-*"))
	sort.Slice(phps, func(i, j int) bool { return compareVersions(versionInPath(phps[i]), versionInPath(phps[j])) < 0 })
	for _, p := range phps {
		s.PHP = versionInPath(p)
	}
	for _, e := range filepath.SplitList(os.Getenv("PATH")) {
		for _, p := range phps {
			if strings.EqualFold(filepath.Clean(e), filepath.Clean(p)) {
				s.PHP = versionInPath(p)
			}
		}
	}
	s.finish(nil)
	return s
}

var pmaPasswordRe = regexp.MustCompile(`\$cfg\['Servers'\]\[\$i\]\['password'\]\s*=\s*'([^']*)'`)

func detectXAMPP(root string) migrationSource {
	s := migrationSource{Kind: "xampp", Name: "XAMPP", Root: root, WWW: filepath.Join(root, "htdocs"), Flavor: "mysql"}
	for _, n := range []string{"mariadbd", "mysqld"} {
		if p := filepath.Join(root, "mysql", "bin", exeName(n)); exists(p) {
			s.MySQLBin = p
			break
		}
	}
	if s.MySQLBin != "" {
		s.DataDir = filepath.Join(root, "mysql", "data")
		if strings.Contains(strings.ToLower(binVersion(s.MySQLBin, regexp.MustCompile(`(?i)(mariadb)`), "--version")), "mariadb") {
			s.Flavor = "mariadb"
		}
	}
	if b, err := os.ReadFile(filepath.Join(root, "phpMyAdmin", "config.inc.php")); err == nil {
		if m := pmaPasswordRe.FindSubmatch(b); m != nil {
			s.password = string(m[1])
		}
	}
	s.PHP = binVersion(filepath.Join(root, "php", exeName("php")), regexp.MustCompile(`PHP (\d+\.\d+\.\d+)`), "-n", "-v")
	// XAMPP's own start page and tools in htdocs are not projects.
	s.finish(map[string]bool{"dashboard": true, "img": true, "webalizer": true, "xampp": true, "forbidden": true, "restricted": true})
	return s
}

// finish lists projects and databases and writes the plan shown to the user.
func (s *migrationSource) finish(skip map[string]bool) {
	s.HasPassword = s.password != ""
	if entries, err := os.ReadDir(s.WWW); err == nil {
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && !skip[strings.ToLower(e.Name())] {
				s.Projects = append(s.Projects, e.Name())
			}
		}
	}
	s.Databases = dataDirDatabases(s.DataDir)
	if s.Projects == nil {
		s.Projects = []string{}
	}
	if s.Databases == nil {
		s.Databases = []string{}
	}
	s.Steps = s.plan()
}

var importSkipDatabases = map[string]bool{"information_schema": true, "mysql": true, "performance_schema": true, "sys": true, "phpmyadmin": true}

// dataDirDatabases reads database names from a MySQL data folder (one
// subfolder per database; special characters are stored as @XXXX).
func dataDirDatabases(dir string) []string {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := decodeDBDir(e.Name())
		if e.IsDir() && !strings.HasPrefix(name, "#") && !importSkipDatabases[strings.ToLower(name)] {
			out = append(out, name)
		}
	}
	return out
}

var dbDirEscapeRe = regexp.MustCompile(`@([0-9a-fA-F]{4})`)

func decodeDBDir(name string) string {
	return dbDirEscapeRe.ReplaceAllStringFunc(name, func(m string) string {
		r, err := strconv.ParseUint(m[1:], 16, 32)
		if err != nil {
			return m
		}
		return string(rune(r))
	})
}

func (s *migrationSource) plan() []string {
	var steps []string
	php := "PHP"
	if s.PHP != "" {
		php = "PHP " + minorOf(s.PHP) + " (the version " + s.Name + " uses, as the default)"
	}
	db := "MySQL"
	if s.Flavor == "mariadb" {
		db = "MariaDB"
	}
	steps = append(steps, fmt.Sprintf("Install what is still missing: Apache, %s and %s.", php, db))
	steps = append(steps, fmt.Sprintf("Stop %s: close %s and its Apache, MySQL and other servers. Windows services that run from %s are stopped and set to manual start.", s.Name, s.Name, s.Root))
	if len(s.Projects) > 0 {
		steps = append(steps, fmt.Sprintf("Copy %d project(s) from %s to the Omni Web Server projects folder. Projects that already exist there are skipped; %s's own copies are not touched.", len(s.Projects), s.WWW, s.Name))
	}
	if len(s.Databases) > 0 {
		steps = append(steps, fmt.Sprintf("Export %d database(s) with %s's own MySQL (started briefly on a spare port) and import them into Omni Web Server's MySQL. Databases that already exist are skipped.", len(s.Databases), s.Name))
	}
	if s.HasPassword {
		steps = append(steps, fmt.Sprintf("Give the MySQL root user the same password %s used, so the projects' .env files keep working.", s.Name))
	}
	if s.Kind == "laragon" {
		steps = append(steps, "Remove Laragon's \"#laragon magic!\" lines from the hosts file (Omni Web Server writes its own entries; administrator rights are needed).")
	}
	steps = append(steps, fmt.Sprintf("Remove %s's PHP, MySQL, Apache and Composer folders from your user PATH; Omni Web Server's php, mysql and composer take their place. Other %s tools on PATH (Git, Node.js, Python, ...) stay.", s.Name, s.Name))
	steps = append(steps, fmt.Sprintf("Remove %s from Windows startup.", s.Name))
	steps = append(steps, "Start Apache and MySQL on the standard ports (80 and 3306).")
	steps = append(steps, fmt.Sprintf("Nothing is deleted: %s stays installed with its data, and you can uninstall it afterwards.", s.Name))
	return steps
}

// readINI returns key=value pairs (lowercase keys, sections ignored); handles UTF-16 files.
func readINI(path string) map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	text := string(b)
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		u := make([]uint16, (len(b)-2)/2)
		for i := range u {
			u[i] = uint16(b[2+2*i]) | uint16(b[3+2*i])<<8
		}
		text = string(utf16.Decode(u))
	}
	text = strings.TrimPrefix(text, string(rune(0xFEFF)))
	for _, line := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && !strings.HasPrefix(k, ";") && !strings.HasPrefix(k, "[") {
			out[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
		}
	}
	return out
}

func findSource(kind, root string) (migrationSource, error) {
	for _, s := range detectSources(true) {
		if s.Kind == kind && (root == "" || strings.EqualFold(filepath.Clean(root), s.Root)) {
			return s, nil
		}
	}
	return migrationSource{}, fmt.Errorf("no %s installation found", kind)
}

type foreignProc struct {
	PID  int
	Name string // lowercase, without .exe
}

// foreignServerNames are the processes of the old stack that are stopped.
// Other tools that live in its folder (Node.js, Git, editors) are left alone.
var foreignServerNames = map[string]bool{
	"laragon": true, "xampp-control": true, "httpd": true, "apache": true, "mysqld": true, "mariadbd": true,
	"nginx": true, "php-cgi": true, "redis-server": true, "memcached": true, "mailpit": true, "mongod": true,
	"postgres": true, "pg_ctl": true, "filezillaserver": true, "mercury": true, "tomcat": true,
}

// runMigration performs the import. password, when not nil, overrides the
// detected MySQL root password of the old stack.
// migrateOptions narrows an import. nil lists mean everything.
type migrateOptions struct {
	Password  *string
	Projects  []string
	Databases []string
}

// withImported marks the projects and databases that already exist here.
func (a *App) withImported(srcs []migrationSource) []migrationSource {
	cfg := a.config()
	www := a.abs(cfg.WWWDir)
	var dbs []string
	if a.mysql.Running() {
		dbs, _ = a.listDatabases(cfg)
	}
	out := make([]migrationSource, len(srcs))
	for i, s := range srcs {
		s.ImportedProjects, s.ImportedDatabases = []string{}, []string{}
		for _, p := range s.Projects {
			if exists(filepath.Join(www, p)) {
				s.ImportedProjects = append(s.ImportedProjects, p)
			}
		}
		for _, d := range s.Databases {
			for _, have := range dbs {
				if strings.EqualFold(have, d) {
					s.ImportedDatabases = append(s.ImportedDatabases, d)
				}
			}
		}
		out[i] = s
	}
	return out
}

// selectNames keeps the names in want (all when want is nil) that are not
// already imported.
func selectNames(all, want, imported []string) []string {
	out := []string{}
	for _, n := range all {
		if (want == nil || containsFold(want, n)) && !containsFold(imported, n) {
			out = append(out, n)
		}
	}
	return out
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

func (a *App) runMigration(t *Task, kind, root string, opts migrateOptions) error {
	src, err := findSource(kind, root)
	if err != nil {
		return err
	}
	if opts.Password != nil {
		src.password = *opts.Password
	}
	marked := a.withImported([]migrationSource{src})[0]
	src.Projects = selectNames(src.Projects, opts.Projects, marked.ImportedProjects)
	src.Databases = selectNames(src.Databases, opts.Databases, marked.ImportedDatabases)
	report := &importReport{a: a}
	report.add("Import from %s (%s) started", src.Name, src.Root)
	report.add("projects: %s", strings.Join(src.Projects, ", "))
	report.add("databases: %s", strings.Join(src.Databases, ", "))
	if n := len(marked.ImportedProjects) + len(marked.ImportedDatabases); n > 0 {
		report.add("already imported, skipped: %s", strings.Join(append(append([]string{}, marked.ImportedProjects...), marked.ImportedDatabases...), ", "))
	}
	defer report.save()

	// 1. our own stack
	cfg := a.config()
	if !installed(a.apacheBin(cfg)) {
		if err := a.installApache(t, ""); err != nil {
			return fmt.Errorf("installing Apache: %v", err)
		}
	}
	if src.PHP != "" {
		minor := minorOf(src.PHP)
		if _, ok := a.config().PHP[minor]; !ok {
			if err := a.installPHP(t, minor, ""); err != nil {
				report.add("PHP %s from the current releases: %v; trying %s", minor, err, src.PHP)
				if err := a.installPHP(t, src.PHP, ""); err != nil {
					report.warn("could not install PHP %s: %v", minor, err)
				}
			}
		}
		if _, ok := a.config().PHP[minor]; ok {
			_ = a.update(func(c *Config) error { c.DefaultPHP = minor; return nil })
			report.add("default PHP: %s", minor)
		}
	}
	if !installed(a.mysqlBin(a.config())) {
		if err := a.installMySQL(t, src.Flavor, ""); err != nil {
			return fmt.Errorf("installing %s: %v", src.Flavor, err)
		}
	}

	// 2. stop the old stack
	t.Set("Stopping "+src.Name, -1)
	a.stopForeign(t, &src, report)

	// 3. export its databases with its own server
	dumps := map[string]string{}
	if len(src.Databases) > 0 && src.MySQLBin != "" && src.DataDir != "" {
		var err error
		if dumps, err = a.dumpForeign(t, &src, report); err != nil {
			report.warn("databases were not exported: %v", err)
		}
	}

	// 4. import into ours
	if len(dumps) > 0 || src.password != "" {
		t.Set("Starting MySQL", -1)
		if err := a.StartMySQL(); err != nil {
			return fmt.Errorf("starting MySQL: %v", err)
		}
		cfg = a.config()
		if src.password != "" && cfg.MySQL.RootPassword == "" {
			if err := a.setRootPassword(cfg, src.password); err != nil {
				report.warn("setting the MySQL root password: %v", err)
			} else {
				_ = a.update(func(c *Config) error { c.MySQL.RootPassword = src.password; return nil })
				report.add("MySQL root password set to %s's", src.Name)
			}
		} else if src.password != "" && cfg.MySQL.RootPassword != src.password {
			report.warn("MySQL root password kept as it was (differs from %s's); update DB_PASSWORD in the projects' .env files", src.Name)
		}
		a.importDumps(t, dumps, report)
	}

	// 5. projects
	a.copyProjects(t, &src, report)

	// 6. system integration
	t.Set("Cleaning up "+src.Name+"'s system entries", -1)
	if src.Kind == "laragon" {
		if n, err := a.removeHostsLines(a.config(), "#laragon magic!"); err != nil {
			report.warn("hosts file: %v", err)
		} else if n > 0 {
			report.add("removed %d Laragon lines from the hosts file", n)
		}
	}
	var prefixes []string
	if src.Kind == "laragon" {
		for _, d := range []string{"php", "mysql", "mariadb", "apache", "nginx", "composer"} {
			prefixes = append(prefixes, filepath.Join(src.Root, "bin", d))
		}
	} else {
		for _, d := range []string{"php", "mysql", "apache"} {
			prefixes = append(prefixes, filepath.Join(src.Root, d))
		}
	}
	if err := removeUserPathUnder(prefixes); err != nil {
		report.warn("PATH: %v", err)
	}
	if err := removeStartupEntries(src.Kind, src.Root); err != nil {
		report.warn("startup entries: %v", err)
	}

	// 7. take over
	t.Set("Starting Omni Web Server's services", -1)
	if err := a.Apply(true); err != nil {
		report.warn("apply: %v", err)
	}
	if err := a.StartAll(); err != nil {
		report.warn("starting services: %v", err)
	}
	msg := fmt.Sprintf("Imported %d project(s) and %d database(s) from %s", report.projects, report.databases, src.Name)
	if len(report.warnings) > 0 {
		msg += fmt.Sprintf(" with %d warning(s): see the import log", len(report.warnings))
	}
	report.add("%s", msg)
	t.Set(msg, 1)
	return nil
}

type importReport struct {
	a         *App
	lines     []string
	warnings  []string
	projects  int
	databases int
}

func (r *importReport) add(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	r.a.log.Printf("import: %s", line)
	r.lines = append(r.lines, time.Now().Format("15:04:05 ")+line)
}

func (r *importReport) warn(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	r.warnings = append(r.warnings, line)
	r.add("WARNING: %s", line)
}

func (r *importReport) save() {
	_ = os.WriteFile(r.a.path("logs", "import.log"), []byte(strings.Join(r.lines, "\n")+"\n"), 0o644)
}

// stopForeign shuts the old database down cleanly, then ends the old stack's
// server processes and switches off its Windows services.
func (a *App) stopForeign(t *Task, src *migrationSource, report *importReport) {
	// A clean MySQL shutdown, on the port that process really listens on.
	admin := foreignTool(src, "mysqladmin", "mariadb-admin")
	for _, p := range foreignProcesses(src.Root) {
		if (p.Name == "mysqld" || p.Name == "mariadbd") && admin != "" {
			for _, port := range listeningPorts(p.PID) {
				_ = hiddenCommand(admin, "--no-defaults", "-h", "127.0.0.1", "-P", fmt.Sprint(port), "-u", "root", "--password="+src.password, "shutdown").Run()
			}
		}
	}
	waitForeignGone(src.Root, map[string]bool{"mysqld": true, "mariadbd": true}, 20*time.Second)
	// The control panel first, so it does not restart what is stopped next.
	for _, panel := range []bool{true, false} {
		for _, p := range foreignProcesses(src.Root) {
			isPanel := p.Name == "laragon" || p.Name == "xampp-control"
			if foreignServerNames[p.Name] && isPanel == panel {
				if err := killPID(p.PID); err == nil {
					report.add("stopped %s (PID %d)", p.Name, p.PID)
				}
			}
		}
	}
	if n, err := stopForeignServices(src.Root); err != nil {
		report.warn("Windows services: %v", err)
	} else if n > 0 {
		report.add("stopped %d Windows service(s) and set them to manual start", n)
	}
	waitForeignGone(src.Root, foreignServerNames, 10*time.Second)
}

func waitForeignGone(root string, names map[string]bool, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		left := false
		for _, p := range foreignProcesses(root) {
			if names[p.Name] {
				left = true
			}
		}
		if !left {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func foreignTool(src *migrationSource, names ...string) string {
	if src.MySQLBin == "" {
		return ""
	}
	for _, n := range names {
		if p := filepath.Join(filepath.Dir(src.MySQLBin), exeName(n)); exists(p) {
			return p
		}
	}
	return ""
}

func freePort(from int) int {
	for p := from; p < from+100; p++ {
		if !portInUse(p) {
			return p
		}
	}
	return from
}

// dumpForeign starts the old MySQL on a spare port with its own data folder and
// dumps every user database with its own mysqldump.
func (a *App) dumpForeign(t *Task, src *migrationSource, report *importReport) (map[string]string, error) {
	dump := foreignTool(src, "mysqldump", "mariadb-dump")
	client := foreignTool(src, "mysql", "mariadb")
	if dump == "" || client == "" {
		return nil, fmt.Errorf("mysqldump not found next to %s", src.MySQLBin)
	}
	port := freePort(3399)
	t.Set(fmt.Sprintf("Starting %s's MySQL on port %d", src.Name, port), -1)
	logFile := a.path("logs", "import_mysqld.log")
	args := []string{"--no-defaults", "--basedir=" + filepath.Dir(filepath.Dir(src.MySQLBin)), "--datadir=" + src.DataDir,
		fmt.Sprintf("--port=%d", port), "--bind-address=127.0.0.1", "--skip-log-bin", "--log-error=" + logFile}
	if runtime.GOOS == "windows" {
		args = append(args, "--standalone")
	}
	server := hiddenCommand(src.MySQLBin, args...)
	if err := server.Start(); err != nil {
		return nil, err
	}
	exited := make(chan struct{})
	go func() { _ = server.Wait(); close(exited) }()
	conn := []string{"--no-defaults", "-h", "127.0.0.1", "-P", fmt.Sprint(port), "-u", "root", "--password=" + src.password}
	defer func() {
		if admin := foreignTool(src, "mysqladmin", "mariadb-admin"); admin != "" {
			_ = hiddenCommand(admin, append(conn, "shutdown")...).Run()
		}
		select {
		case <-exited:
		case <-time.After(30 * time.Second):
			killTree(server)
		}
	}()
	deadline := time.Now().Add(120 * time.Second)
	for !dialOK(port) {
		select {
		case <-exited:
			return nil, fmt.Errorf("%s's MySQL did not start: %s", src.Name, lastLines(logFile, 6))
		default:
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%s's MySQL did not open port %d", src.Name, port)
		}
		time.Sleep(300 * time.Millisecond)
	}
	out, err := hiddenCommand(client, append(conn, "-N", "-B", "-e", "SHOW DATABASES")...).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			err = fmt.Errorf("%s (wrong root password? enter it in the import dialog)", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, err
	}
	var dbs []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" && !importSkipDatabases[strings.ToLower(l)] {
			dbs = append(dbs, l)
		}
	}
	dir := a.path("tmp", "import")
	_ = os.MkdirAll(dir, 0o755)
	dumps := map[string]string{}
	for i, db := range dbs {
		t.Set(fmt.Sprintf("Exporting database %s (%d/%d)", db, i+1, len(dbs)), float64(i)/float64(len(dbs))*0.5)
		file := filepath.Join(dir, safeFileName(db)+".sql")
		dargs := append(append([]string{}, conn...), "--single-transaction", "--routines", "--triggers", "--events", "--hex-blob",
			"--default-character-set=utf8mb4", "--result-file="+file, "--databases", db)
		if src.Flavor == "mysql" {
			dargs = append(dargs, "--set-gtid-purged=OFF")
		}
		if out, err := hiddenCommand(dump, dargs...).CombinedOutput(); err != nil {
			report.warn("exporting %s: %v %s", db, err, strings.TrimSpace(string(out)))
			continue
		}
		dumps[db] = file
	}
	return dumps, nil
}

func safeFileName(s string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(`<>:"/\|?*`, r) || r < 32 {
			return '_'
		}
		return r
	}, s)
}

func (a *App) importDumps(t *Task, dumps map[string]string, report *importReport) {
	if len(dumps) == 0 {
		return
	}
	cfg := a.config()
	existing, _ := a.listDatabases(cfg)
	client := a.mysqlTool(cfg, "mysql", "mariadb")
	if client == "" {
		report.warn("mysql client not found; dumps are in %s", a.path("tmp", "import"))
		return
	}
	names := make([]string, 0, len(dumps))
	for db := range dumps {
		names = append(names, db)
	}
	sort.Strings(names)
	toMySQL := a.mysqlFlavor(cfg) != "mariadb"
	for i, db := range names {
		if contains(existing, db) {
			report.warn("database %s already exists in Omni Web Server's MySQL; skipped (dump kept in %s)", db, dumps[db])
			continue
		}
		t.Set(fmt.Sprintf("Importing database %s (%d/%d)", db, i+1, len(names)), 0.5+float64(i)/float64(len(names))*0.4)
		if err := importDump(client, a.mysqlClientArgs(cfg), dumps[db], toMySQL); err != nil {
			report.warn("importing %s: %v (dump kept in %s)", db, err, dumps[db])
			continue
		}
		report.databases++
		report.add("imported database %s", db)
		_ = os.Remove(dumps[db])
	}
}

func importDump(client string, conn []string, file string, toMySQL bool) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	cmd := hiddenCommand(client, append(append([]string{}, conn...), "--default-character-set=utf8mb4")...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	copyErr := sanitizeDump(f, stdin, toMySQL)
	stdin.Close()
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(stderr.String()))
	}
	return copyErr
}

var (
	mariaCollationRe = regexp.MustCompile(`(utf8mb[34])_uca1400_[a-z_]+`)
	mysqlCollationRe = regexp.MustCompile(`utf8mb4_0900_[a-z_]+`)
)

// sanitizeDump copies a dump, fixing what one server writes and the other
// rejects: MariaDB's sandbox-mode first line and collations that only exist
// on one side.
func sanitizeDump(r io.Reader, w io.Writer, toMySQL bool) error {
	br := bufio.NewReaderSize(r, 1<<20)
	bw := bufio.NewWriterSize(w, 1<<20)
	first := true
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			switch {
			case first && bytes.HasPrefix(line, []byte("/*M!999999")):
			case toMySQL && bytes.Contains(line, []byte("_uca1400_")):
				line = mariaCollationRe.ReplaceAll(line, []byte("${1}_unicode_ci"))
				fallthrough
			default:
				if !toMySQL && bytes.Contains(line, []byte("_0900_")) {
					line = mysqlCollationRe.ReplaceAll(line, []byte("utf8mb4_unicode_ci"))
				}
				if _, werr := bw.Write(line); werr != nil {
					return werr
				}
			}
			first = false
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return bw.Flush()
}

// copyProjects copies every project folder that does not exist in our www yet.
func (a *App) copyProjects(t *Task, src *migrationSource, report *importReport) {
	www := a.abs(a.config().WWWDir)
	if strings.EqualFold(filepath.Clean(www), filepath.Clean(src.WWW)) {
		report.add("projects folder is already %s; nothing to copy", www)
		return
	}
	for i, name := range src.Projects {
		dest := filepath.Join(www, name)
		if exists(dest) {
			report.warn("project %s already exists in %s; skipped", name, www)
			continue
		}
		t.Set(fmt.Sprintf("Copying project %s (%d/%d)", name, i+1, len(src.Projects)), 0.9)
		files, err := copyTree(filepath.Join(src.WWW, name), dest, func(n int) {
			t.Set(fmt.Sprintf("Copying project %s (%d/%d): %d files", name, i+1, len(src.Projects), n), 0.9)
		})
		if err != nil {
			report.warn("copying %s: %v", name, err)
			continue
		}
		report.projects++
		report.add("copied project %s (%d files)", name, files)
	}
}

// copyTree copies a folder recursively. Symlinks and junctions (pnpm, Laravel's
// storage link) are recreated as links when possible, otherwise skipped.
func copyTree(src, dest string, progress func(int)) (int, error) {
	n := 0
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dest, rel)
		switch {
		case d.Type()&os.ModeSymlink != 0 || d.Type()&os.ModeIrregular != 0:
			if link, err := os.Readlink(p); err == nil {
				_ = os.Symlink(link, target)
			}
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		}
		if err := copyFile(p, target); err != nil {
			return err
		}
		n++
		if n%500 == 0 {
			progress(n)
		}
		return nil
	})
	return n, err
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, st.Mode().Perm()|0o200)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(dest, st.ModTime(), st.ModTime())
}

func (a *App) startMigration(kind, root string, opts migrateOptions) (*Task, error) {
	src, err := findSource(kind, root)
	if err != nil {
		return nil, badRequest("%v", err)
	}
	if opts.Projects != nil && opts.Databases != nil && len(opts.Projects) == 0 && len(opts.Databases) == 0 {
		return nil, badRequest("choose at least one project or database to import")
	}
	title := "Import from " + src.Name
	if a.tasks.Busy(title) {
		return nil, badRequest("%s is already running", title)
	}
	return a.tasks.Run(title, func(t *Task) error { return a.runMigration(t, kind, src.Root, opts) }), nil
}
