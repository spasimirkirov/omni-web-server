package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Site is the resolved view of a project: its folder, detected framework and vhost settings.
type Site struct {
	Name      string `json:"name"`
	Host      string `json:"host"`
	Path      string `json:"path"`
	DocRoot   string `json:"docroot"`
	Framework string `json:"framework"`
	Enabled   bool   `json:"enabled"`
	// PHP is the effective version; PHPSetting is "" when following the default.
	PHP             string `json:"php"`
	PHPSetting      string `json:"php_setting"`
	DocRootOverride string `json:"docroot_override"`
	External        bool   `json:"external"`
	Missing         bool   `json:"missing"`
	Warning         string `json:"warning,omitempty"`
}

var invalidHostChars = regexp.MustCompile(`[^a-z0-9-]+`)

// hostLabel turns a folder name into a valid DNS label: "My_App 2" -> "my-app-2".
func hostLabel(name string) string {
	s := invalidHostChars.ReplaceAllString(strings.ToLower(name), "-")
	s = strings.Trim(s, "-")
	if len(s) > 63 {
		s = strings.Trim(s[:63], "-")
	}
	return s
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// detectProject recognises common PHP frameworks and returns the folder
// (relative to the project) that should be the vhost DocumentRoot.
func detectProject(dir string) (framework, docroot string) {
	has := func(rel string) bool { return exists(filepath.Join(dir, filepath.FromSlash(rel))) }
	composer := ""
	if b, err := os.ReadFile(filepath.Join(dir, "composer.json")); err == nil {
		composer = string(b)
	}
	switch {
	case has("artisan") && isDir(filepath.Join(dir, "public")),
		strings.Contains(composer, `"laravel/framework"`) && isDir(filepath.Join(dir, "public")):
		return "laravel", "public"
	case has("bin/console") && has("public/index.php"):
		return "symfony", "public"
	case has("spark") && has("public/index.php"):
		return "codeigniter", "public"
	case has("craft") && has("web/index.php"):
		return "craft", "web"
	case has("yii") && has("web/index.php"):
		return "yii", "web"
	case has("wp-config.php") || has("wp-load.php"):
		return "wordpress", ""
	case has("public/index.php") && !has("index.php"):
		return "php", "public"
	case has("index.php"):
		return "php", ""
	case has("index.html") || has("index.htm"):
		return "static", ""
	}
	return "php", ""
}

// scanSites reconciles cfg.Sites with the folders in www. New folders are
// added (enabled when AutoVhost is on), vanished folders are dropped.
// It reports whether cfg changed.
func (a *App) scanSitesLocked() ([]Site, bool) {
	cfg := a.cfg
	changed := false
	www := a.abs(cfg.WWWDir)
	_ = os.MkdirAll(www, 0o755)

	found := map[string]bool{}
	if entries, err := os.ReadDir(www); err == nil {
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") || hostLabel(name) == "" {
				continue
			}
			full := filepath.Join(www, name)
			if !isDir(full) { // follows symlinks/junctions
				continue
			}
			found[name] = true
			if _, ok := cfg.Sites[name]; !ok {
				cfg.Sites[name] = &SiteConfig{Enabled: cfg.AutoVhost}
				changed = true
			}
		}
	}
	for name, sc := range cfg.Sites {
		if sc.Path == "" && !found[name] {
			delete(cfg.Sites, name)
			changed = true
		}
	}

	names := make([]string, 0, len(cfg.Sites))
	for n := range cfg.Sites {
		names = append(names, n)
	}
	sort.Strings(names)

	seenHosts := map[string]string{}
	sites := make([]Site, 0, len(names))
	for _, name := range names {
		sc := cfg.Sites[name]
		s := Site{
			Name:            name,
			Host:            hostLabel(name) + "." + cfg.TLD,
			Enabled:         sc.Enabled,
			PHPSetting:      sc.PHP,
			DocRootOverride: sc.DocRoot,
			External:        sc.Path != "",
		}
		if s.External {
			s.Path = a.abs(sc.Path)
		} else {
			s.Path = filepath.Join(www, name)
		}
		if !isDir(s.Path) {
			s.Missing = true
			s.Warning = "project folder not found"
		}
		fw, rel := detectProject(s.Path)
		s.Framework = fw
		if sc.DocRoot != "" {
			if filepath.IsAbs(sc.DocRoot) {
				s.DocRoot = filepath.Clean(sc.DocRoot)
			} else {
				s.DocRoot = filepath.Join(s.Path, filepath.FromSlash(sc.DocRoot))
			}
		} else {
			s.DocRoot = filepath.Join(s.Path, filepath.FromSlash(rel))
		}
		if !s.Missing && !isDir(s.DocRoot) {
			s.Warning = "document root does not exist"
		}
		s.PHP = cfg.DefaultPHP
		if sc.PHP != "" {
			if _, ok := cfg.PHP[sc.PHP]; ok {
				s.PHP = sc.PHP
			} else {
				s.Warning = "PHP " + sc.PHP + " is not installed, using default"
			}
		}
		if other, dup := seenHosts[s.Host]; dup {
			s.Warning = "host name collides with " + other
			s.Enabled = false
		} else {
			seenHosts[s.Host] = name
		}
		sites = append(sites, s)
	}
	return sites, changed
}

var siteNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._ -]{0,62}$`)

func validSiteName(n string) bool {
	return siteNameRe.MatchString(n) && hostLabel(n) != "" && !strings.Contains(n, "..")
}
