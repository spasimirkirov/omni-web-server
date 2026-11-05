package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ---------- pure helpers (tested in gitproject_test.go) ----------

var (
	scpURLRe  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*@[A-Za-z0-9][A-Za-z0-9.-]*:\S+$`)
	gitRefRe  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._/+@-]{0,199}$`)
	repoCharR = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
)

// validGitURL accepts https://, http://, ssh://, file:// and the scp-like
// git@host:path form, and nothing else. A leading dash (an option for git or
// ssh), whitespace and control characters are rejected.
func validGitURL(raw string) error {
	if raw == "" {
		return errors.New("enter the repository URL")
	}
	if strings.HasPrefix(raw, "-") {
		return errors.New("the repository URL must not start with a dash")
	}
	for _, r := range raw {
		if r <= ' ' || r == 0x7f {
			return errors.New("the repository URL must not contain spaces or line breaks")
		}
	}
	if scheme, _, ok := strings.Cut(raw, "://"); ok {
		switch strings.ToLower(scheme) {
		case "https", "http", "ssh", "file":
		default:
			return fmt.Errorf("unsupported URL scheme %q: use https://, http://, ssh://, file:// or git@host:path", scheme)
		}
		u, err := url.Parse(raw)
		if err != nil {
			return fmt.Errorf("invalid repository URL: %v", err)
		}
		if strings.EqualFold(u.Scheme, "file") {
			if strings.Trim(u.Path, "/") == "" {
				return errors.New("the file:// URL has no path")
			}
			return nil
		}
		host := u.Hostname()
		if host == "" || strings.HasPrefix(host, "-") || (u.User != nil && strings.HasPrefix(u.User.Username(), "-")) || strings.Trim(u.Path, "/") == "" {
			return errors.New("the repository URL needs a host and a path")
		}
		return nil
	}
	if scpURLRe.MatchString(raw) {
		return nil
	}
	return errors.New("not a repository URL: use https://github.com/user/repo.git or git@github.com:user/repo.git")
}

// validGitRef checks a branch or tag name given by the user.
func validGitRef(ref string) error {
	if ref == "" {
		return nil
	}
	if !gitRefRe.MatchString(ref) || strings.Contains(ref, "..") || strings.HasSuffix(ref, "/") || strings.HasSuffix(ref, ".lock") {
		return errors.New("invalid branch or tag name")
	}
	return nil
}

// gitRepoName derives a folder name from a repository URL ("" if none fits).
func gitRepoName(raw string) string {
	s := strings.TrimSpace(raw)
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimRight(s, "/")
	if i := strings.LastIndexAny(s, "/:\\"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimSuffix(s, ".git")
	s = strings.Trim(repoCharR.ReplaceAllString(s, "-"), "-._")
	if len(s) > 63 {
		s = strings.Trim(s[:63], "-._")
	}
	if !validSiteName(s) {
		return ""
	}
	return s
}

// gitCloneArgs builds the argument list of git clone (never run through a
// shell). "--" keeps the URL and the target from being read as options.
func gitCloneArgs(repo, branch string, shallow bool, target string) []string {
	args := []string{"-c", "core.longpaths=true", "clone"}
	if shallow {
		args = append(args, "--depth", "1")
	}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	return append(args, "--", repo, target)
}

// redactGitURL hides a password embedded in an http(s) URL, for logs.
func redactGitURL(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.User != nil {
		if _, has := u.User.Password(); has {
			u.User = url.UserPassword(u.User.Username(), "***")
			return u.String()
		}
	}
	return raw
}

const privateRepoHint = "private repo: set up a credential helper or SSH key (git cannot ask for a password here)"

// cloneFailure adds the private-repository hint when git's output looks like
// an authentication problem.
func cloneFailure(err error) error {
	low := strings.ToLower(err.Error())
	for _, k := range []string{"authentication", "could not read username", "could not read password", "terminal prompts disabled",
		"permission denied", "publickey", "host key verification", "repository not found", "access denied", "not found", "403", "401"} {
		if strings.Contains(low, k) {
			return fmt.Errorf("%v - %s", err, privateRepoHint)
		}
	}
	return err
}

// gitEnv makes git fail fast instead of waiting for a password, a passphrase
// or a host key confirmation nobody can type.
func gitEnv() []string {
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		env = append(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	}
	return env
}

// removeAllForce removes a tree that may hold read-only files (git's object
// files are read-only on Windows).
func removeAllForce(path string) error {
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err == nil {
			_ = os.Chmod(p, 0o777)
		}
		return nil
	})
	return os.RemoveAll(path)
}

// gitDBEnv is what goes into .env when the user ticked the database option.
func gitDBEnv(cfg *Config, db string) map[string]string {
	return map[string]string{
		"DB_CONNECTION": "mysql",
		"DB_HOST":       "127.0.0.1",
		"DB_PORT":       strconv.Itoa(cfg.MySQL.Port),
		"DB_DATABASE":   db,
		"DB_USERNAME":   "root",
		"DB_PASSWORD":   envQuote(cfg.MySQL.RootPassword),
	}
}

// ---------- the task ----------

var errSkipStep = errors.New("skipped")

func (a *App) startGitProject(p newProject) (*Task, error) {
	p.Repo, p.Branch = strings.TrimSpace(p.Repo), strings.TrimSpace(p.Branch)
	if err := validGitURL(p.Repo); err != nil {
		return nil, badRequest("%v", err)
	}
	if err := validGitRef(p.Branch); err != nil {
		return nil, badRequest("%v", err)
	}
	if p.Name == "" {
		p.Name = gitRepoName(p.Repo)
	}
	if !validSiteName(p.Name) {
		return nil, badRequest("invalid project name (letters, digits, dot, dash, underscore)")
	}
	if toolPath("git") == "" {
		return nil, badRequest("Git is not installed: install it from the Tools tab, then try again")
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
	if p.Composer || p.Env {
		if _, ok := cfg.PHP[p.PHP]; !ok {
			return nil, badRequest("PHP %s is not installed: install a version on the PHP tab first", p.PHP)
		}
	}
	if p.Npm && a.nodeBinDir(cfg) == "" {
		return nil, badRequest("npm install needs Node.js: install it on the Tools tab, or untick it")
	}
	if p.Database != "" {
		if !dbNameRe.MatchString(p.Database) {
			return nil, badRequest("invalid database name")
		}
		if !installed(a.mysqlBin(cfg)) {
			return nil, badRequest("MySQL is not installed: install it on the Overview tab, or create the project without a database")
		}
		if a.mysql.Running() {
			if dbs, err := a.listDatabases(cfg); err == nil && containsFold(dbs, p.Database) {
				return nil, badRequest("a database named %s already exists: choose another name", p.Database)
			}
		}
	}
	title := "Clone " + p.Name + " (Git)"
	if a.tasks.Busy(title) {
		return nil, badRequest("%s is already running", title)
	}
	// Cloned into a hidden folder this task owns (the www scanner skips dot
	// folders) and moved into place at the end, like the template flow. A
	// folder that existed before is never touched.
	stage := filepath.Join(www, ".omni-new-"+p.Name)
	return a.tasks.Run(title, func(t *Task) error {
		createdDB := false
		err := a.cloneProject(t, p, stage, dir, &createdDB)
		if err != nil {
			t.Set("Cleaning up", -1)
			_ = removeAllForce(stage)
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

func (a *App) cloneProject(t *Task, p newProject, stage, dir string, createdDB *bool) error {
	cfg := a.config()
	host := hostLabel(p.Name) + "." + cfg.TLD
	_ = removeAllForce(stage) // leftover of an interrupted run
	git := toolPath("git")
	var plan []projectStep
	add := func(name string, run func() error) { plan = append(plan, projectStep{name, run}) }
	has := func(rel string) bool { return exists(filepath.Join(stage, rel)) }

	add("Clone "+redactGitURL(p.Repo), func() error {
		args := gitCloneArgs(p.Repo, p.Branch, p.Shallow, stage)
		label := "git " + strings.Join(gitCloneArgs(redactGitURL(p.Repo), p.Branch, p.Shallow, filepath.Base(stage))[2:], " ")
		if err := a.runLogged(t, label, filepath.Dir(stage), gitEnv(), git, args...); err != nil {
			return cloneFailure(err)
		}
		return nil
	})
	if p.Composer {
		add("Install the PHP packages (composer install)", func() error {
			if !has("composer.json") {
				t.AddLog("No composer.json in the repository.")
				return errSkipStep
			}
			kind := "php"
			if has("artisan") {
				kind = "laravel"
			}
			if err := a.ensureProjectExtensions(t, p.PHP, kind); err != nil {
				return err
			}
			if !exists(a.composerPhar()) {
				if err := a.installComposer(t); err != nil {
					return err
				}
			}
			return a.composer(t, p.PHP, stage, "install")
		})
	}
	if p.Env {
		add("Copy .env.example to .env", func() error {
			if has(".env") {
				t.AddLog(".env already exists: left as it is.")
				return errSkipStep
			}
			if !has(".env.example") {
				t.AddLog("No .env.example in the repository.")
				return errSkipStep
			}
			b, err := os.ReadFile(filepath.Join(stage, ".env.example"))
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(stage, ".env"), b, 0o644)
		})
	}
	if p.Database != "" {
		add("Create the MySQL database "+p.Database, func() error {
			if err := a.createProjectDatabase(t, p.Database); err != nil {
				return err
			}
			*createdDB = true
			return setEnvValues(filepath.Join(stage, ".env"), gitDBEnv(cfg, p.Database))
		})
	}
	if p.Env {
		add("Generate the application key (php artisan key:generate)", func() error {
			if !has("artisan") || !has(filepath.Join("vendor", "autoload.php")) || !has(".env") {
				t.AddLog("Needs artisan, vendor/ (tick composer install) and .env: not a Laravel project, or not ready.")
				return errSkipStep
			}
			if err := a.ensureProjectExtensions(t, p.PHP, "laravel"); err != nil {
				return err
			}
			return a.artisan(t, p.PHP, stage, "key:generate", "--force")
		})
	}
	if p.Npm {
		add("Install the npm packages (npm install)", func() error {
			if !has("package.json") {
				t.AddLog("No package.json in the repository.")
				return errSkipStep
			}
			return a.npm(t, p.PHP, stage, "install")
		})
	}
	add("Publish http://"+host, func() error {
		if exists(dir) {
			return fmt.Errorf("%s appeared in the meantime: choose another name", dir)
		}
		return a.publishProject(p, stage, dir)
	})

	names := make([]string, len(plan))
	for i, s := range plan {
		names[i] = s.name
	}
	t.SetSteps(names)
	for i, s := range plan {
		t.StepStatus(i, "running")
		t.AddLog("── " + s.name)
		err := s.run()
		if errors.Is(err, errSkipStep) {
			t.StepStatus(i, "skipped")
			continue
		}
		if err != nil {
			return err
		}
		t.StepStatus(i, "done")
	}
	scheme := "http"
	if cfg.SSL {
		scheme = "https"
	}
	t.SetURL(scheme + "://" + host + "/")
	t.Set("Ready: "+scheme+"://"+host, 1)
	return nil
}

// ---------- CLI: ows new <name> --git <url> ... ----------

func (c *client) newProject(args []string) error {
	usage := "new <name> --git <url> [--branch x] [--depth 1] [--php 8.3] [--composer] [--env] [--npm] [--db [--db-name n]]"
	if err := need(args, 1, usage); err != nil {
		return err
	}
	p := newProject{Source: "git", Name: args[0]}
	dbName, wantDB := "", false
	for i := 1; i < len(args); i++ {
		val := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", args[i])
			}
			i++
			return args[i], nil
		}
		var err error
		switch args[i] {
		case "--git":
			p.Repo, err = val()
		case "--branch":
			p.Branch, err = val()
		case "--php":
			p.PHP, err = val()
		case "--db-name":
			dbName, err = val()
		case "--depth":
			var d string
			if d, err = val(); err == nil && d != "1" {
				err = errors.New("only --depth 1 is supported")
			}
			p.Shallow = true
		case "--composer":
			p.Composer = true
		case "--env":
			p.Env = true
		case "--npm":
			p.Npm = true
		case "--db":
			wantDB = true
		default:
			err = fmt.Errorf("unknown option %q (usage: ows %s)", args[i], usage)
		}
		if err != nil {
			return err
		}
	}
	if p.Repo == "" {
		return fmt.Errorf("usage: ows %s", usage)
	}
	if dbName != "" && !wantDB {
		return errors.New("--db-name needs --db")
	}
	if wantDB {
		p.Database = dbName
		if p.Database == "" {
			p.Database = strings.Trim(regexp.MustCompile(`[^a-z0-9_]+`).ReplaceAllString(strings.ToLower(p.Name), "_"), "_")
		}
	}
	var t TaskView
	if err := c.call("POST", "/api/projects", p, &t); err != nil {
		return err
	}
	if err := c.waitTask(t); err != nil {
		return err
	}
	var done TaskView
	if err := c.call("GET", fmt.Sprintf("/api/tasks/%d", t.ID), nil, &done); err == nil && done.URL != "" {
		fmt.Println(done.URL)
	}
	return nil
}
