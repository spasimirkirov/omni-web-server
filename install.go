package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36 OmniWebServer/" + appVersion

var versionRe = regexp.MustCompile(`\d+\.\d+\.\d+`)

var httpClient = &http.Client{Timeout: 0}

func httpGet(u string, referer string) (*http.Response, error) {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	return resp, nil
}

func fetchText(u string) (string, error) {
	resp, err := httpGet(u, "")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return string(b), err
}

type progressWriter struct {
	t     *Task
	label string
	total int64
	n     int64
	last  time.Time
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.n += int64(len(b))
	if time.Since(p.last) > 300*time.Millisecond {
		p.last = time.Now()
		frac := -1.0
		if p.total > 0 {
			frac = float64(p.n) / float64(p.total) * 0.9
		}
		p.t.Set(fmt.Sprintf("%s: %.1f MB", p.label, float64(p.n)/1e6), frac)
	}
	return len(b), nil
}

// download saves u into tmp/downloads, verifying sha256 when given.
func (a *App) download(t *Task, u, referer, wantSHA string) (string, error) {
	name := path.Base(strings.Split(u, "?")[0])
	if name == "" || name == "/" || name == "." {
		name = "download.zip"
	}
	dest := a.path("tmp", "downloads", name)
	resp, err := httpGet(u, referer)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	f, err := os.Create(dest + ".part")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	pw := &progressWriter{t: t, label: "Downloading " + name, total: resp.ContentLength}
	_, err = io.Copy(io.MultiWriter(f, h, pw), resp.Body)
	f.Close()
	if err != nil {
		os.Remove(dest + ".part")
		return "", fmt.Errorf("download interrupted: %v", err)
	}
	if wantSHA != "" && !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), wantSHA) {
		os.Remove(dest + ".part")
		return "", fmt.Errorf("checksum mismatch for %s", name)
	}
	if err := os.Rename(dest+".part", dest); err != nil {
		return "", err
	}
	return dest, nil
}

// extractZip unpacks src into dest, refusing entries that escape dest.
func extractZip(t *Task, src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("not a valid zip archive: %v", err)
	}
	defer r.Close()
	destClean := filepath.Clean(dest) + string(os.PathSeparator)
	for i, f := range r.File {
		if i%200 == 0 {
			t.Set(fmt.Sprintf("Extracting %d/%d files", i, len(r.File)), 0.9+0.09*float64(i)/float64(len(r.File)))
		}
		target := filepath.Join(dest, filepath.FromSlash(f.Name))
		if !strings.HasPrefix(target, destClean) {
			return fmt.Errorf("unsafe path in archive: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode()|0o600)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// findDirWith returns the folder under root that contains marker (e.g. "bin/httpd.exe").
func findDirWith(root, marker string) (string, error) {
	var found string
	errFound := errors.New("found")
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if rel, _ := filepath.Rel(root, p); strings.Count(rel, string(os.PathSeparator)) > 3 {
			return filepath.SkipDir
		}
		if exists(filepath.Join(p, filepath.FromSlash(marker))) {
			found = p
			return errFound
		}
		return nil
	})
	if found != "" {
		return found, nil
	}
	if err != nil && err != errFound {
		return "", err
	}
	return "", fmt.Errorf("%s not found in archive", marker)
}

// installZip downloads and unpacks an archive into bin/<kind>/<name>.
func (a *App) installZip(t *Task, u, referer, sha, kind, name, marker string) (string, error) {
	file, err := a.download(t, u, referer, sha)
	if err != nil {
		return "", err
	}
	defer os.Remove(file)
	tmp := a.path("tmp", "extract-"+fmt.Sprint(time.Now().UnixNano()))
	defer os.RemoveAll(tmp)
	if err := extractZip(t, file, tmp); err != nil {
		return "", err
	}
	src, err := findDirWith(tmp, marker)
	if err != nil {
		return "", err
	}
	target := a.path("bin", kind, name)
	if exists(target) {
		if err := os.RemoveAll(target); err != nil {
			return "", fmt.Errorf("cannot replace %s (is it running?): %v", target, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(src, target); err != nil {
		return "", err
	}
	return target, nil
}

// ---- PHP (windows.php.net) ----

type PHPRelease struct {
	Minor     string `json:"minor"`
	Version   string `json:"version"`
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	Installed string `json:"installed"`
}

var (
	catalogMu    sync.Mutex
	catalogCache []PHPRelease
	catalogTime  time.Time
)

const phpReleasesBase = "https://windows.php.net/downloads/releases/"

func phpCatalog() ([]PHPRelease, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	if catalogCache != nil && time.Since(catalogTime) < 15*time.Minute {
		return catalogCache, nil
	}
	body, err := fetchText(phpReleasesBase + "releases.json")
	if err != nil {
		return nil, err
	}
	var raw map[string]map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil, fmt.Errorf("unexpected releases.json: %v", err)
	}
	var out []PHPRelease
	for minor, entry := range raw {
		var version string
		_ = json.Unmarshal(entry["version"], &version)
		keys := make([]string, 0, len(entry))
		for k := range entry {
			if strings.HasPrefix(k, "nts-") && strings.HasSuffix(k, "-x64") {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		if len(keys) == 0 {
			continue
		}
		var build struct {
			Zip struct {
				Path   string `json:"path"`
				SHA256 string `json:"sha256"`
			} `json:"zip"`
		}
		_ = json.Unmarshal(entry[keys[len(keys)-1]], &build)
		if build.Zip.Path == "" {
			continue
		}
		out = append(out, PHPRelease{Minor: minor, Version: version, URL: phpReleasesBase + build.Zip.Path, SHA256: build.Zip.SHA256})
	}
	sort.Slice(out, func(i, j int) bool { return compareVersions(out[i].Minor, out[j].Minor) > 0 })
	catalogCache, catalogTime = out, time.Now()
	return out, nil
}

// phpArchiveURL guesses the archive download URL for an older release, e.g. 7.4.33.
func phpArchiveURL(version string) string {
	vc := "vs17"
	switch m := minorOf(version); {
	case compareVersions(m, "7.2") < 0:
		vc = "vc14"
	case compareVersions(m, "8.0") < 0:
		vc = "vc15"
	case compareVersions(m, "8.4") < 0:
		vc = "vs16"
	}
	return fmt.Sprintf("%sarchives/php-%s-nts-Win32-%s-x64.zip", phpReleasesBase, version, vc)
}

func (a *App) installPHP(t *Task, want, customURL string) error {
	if runtime.GOOS != "windows" && customURL == "" {
		return errors.New("automatic PHP downloads are Windows builds; on Linux install php-cgi with your package manager (Omni Web Server detects it)")
	}
	u, sha, version := customURL, "", want
	if u == "" {
		t.Set("Looking up PHP releases", -1)
		cat, err := phpCatalog()
		if err != nil {
			return err
		}
		if (want == "" || want == "latest") && len(cat) > 0 {
			want = cat[0].Minor // newest release line
		}
		for _, r := range cat {
			if r.Minor == want || r.Version == want {
				u, sha, version = r.URL, r.SHA256, r.Version
				break
			}
		}
		if u == "" {
			if !versionRe.MatchString(want) {
				return fmt.Errorf("PHP %s is not in the current release list; enter a full version like 7.4.33 to install from the archives", want)
			}
			u = phpArchiveURL(want)
		}
	}
	if !versionRe.MatchString(version) {
		version = versionRe.FindString(path.Base(u))
	}
	name := "php-" + version
	if version == "" {
		name = "php-" + fmt.Sprint(time.Now().Unix())
	}
	dir, err := a.installZip(t, u, "", sha, "php", name, exeName("php-cgi"))
	if err != nil {
		return err
	}
	t.Set("Registering PHP", 0.99)
	a.ensureCACert(t)
	var minor, oldDir string
	err = a.update(func(c *Config) error {
		if p, ok := c.PHP[minorOf(version)]; ok {
			oldDir = a.abs(p.Dir)
		}
		var err error
		minor, err = a.registerPHP(c, dir, "")
		return err
	})
	if err != nil {
		return err
	}
	// An upgrade within the same minor version replaces the previous build.
	if oldDir != "" && filepath.Clean(oldDir) != filepath.Clean(dir) && strings.HasPrefix(oldDir, a.path("bin", "php")+string(os.PathSeparator)) {
		if err := os.RemoveAll(oldDir); err != nil {
			a.log.Printf("removing old PHP build %s: %v", oldDir, err)
		}
	}
	t.Set("Installed PHP "+version+" ("+minor+")", 1)
	return nil
}

// ensureCACert downloads Mozilla's CA bundle so curl/openssl in PHP can verify HTTPS.
func (a *App) ensureCACert(t *Task) {
	dest := a.path("etc", "ssl", "cacert.pem")
	if exists(dest) {
		return
	}
	t.Set("Downloading CA certificates", 0.99)
	resp, err := httpGet("https://curl.se/ca/cacert.pem", "")
	if err != nil {
		a.log.Printf("cacert: %v", err)
		return
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err == nil && strings.Contains(string(b), "BEGIN CERTIFICATE") {
		_ = writeFileAtomic(dest, b)
	}
}

// ---- Apache (Apache Lounge) ----

var apacheLoungeRe = regexp.MustCompile(`(?i)href="([^"]*httpd-2\.4\.\d+-[^"]*win64-VS\d+\.zip)"`)

func latestApacheURL() (string, error) {
	const page = "https://www.apachelounge.com/download/"
	body, err := fetchText(page)
	if err != nil {
		return "", err
	}
	m := apacheLoungeRe.FindStringSubmatch(body)
	if m == nil {
		return "", errors.New("could not find an Apache download on apachelounge.com; use 'install from URL'")
	}
	base, _ := url.Parse(page)
	ref, err := url.Parse(m[1])
	if err != nil {
		return "", err
	}
	return base.ResolveReference(ref).String(), nil
}

func (a *App) installApache(t *Task, customURL string) error {
	if runtime.GOOS != "windows" && customURL == "" {
		return errors.New("automatic Apache downloads are Windows builds; on Linux install apache2/httpd with your package manager")
	}
	u := customURL
	if u == "" {
		t.Set("Looking up the latest Apache build", -1)
		var err error
		if u, err = latestApacheURL(); err != nil {
			return err
		}
	}
	version := versionRe.FindString(path.Base(u))
	running := a.apache.Running()
	if running {
		_ = a.apache.Stop()
	}
	dir, err := a.installZip(t, u, "https://www.apachelounge.com/download/", "", "apache", "httpd-"+version, "bin/"+exeName("httpd"))
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.staticMods = map[string]map[string]bool{}
	delete(a.versions, "apache")
	a.mu.Unlock()
	if err := a.update(func(c *Config) error {
		c.Apache = ApacheConfig{Dir: a.rel(dir)}
		return nil
	}); err != nil {
		return err
	}
	t.Set("Installed Apache "+version, 1)
	if running {
		return a.StartWeb()
	}
	return nil
}

// ---- MySQL / MariaDB ----

const fallbackMySQL = "8.4.6"

var mysqlZipRe = regexp.MustCompile(`mysql-(8\.4\.\d+)-winx64\.zip`)

func latestMySQLVersion() string {
	best := ""
	for _, page := range []string{"https://dev.mysql.com/downloads/mysql/8.4.html", "https://dev.mysql.com/downloads/mysql/"} {
		body, err := fetchText(page)
		if err != nil {
			continue
		}
		for _, m := range mysqlZipRe.FindAllStringSubmatch(body, -1) {
			if best == "" || compareVersions(m[1], best) > 0 {
				best = m[1]
			}
		}
	}
	if best == "" {
		return fallbackMySQL
	}
	return best
}

func mysqlURLs(version string) []string {
	mm := minorOf(version)
	f := "mysql-" + version + "-winx64.zip"
	return []string{
		"https://cdn.mysql.com/Downloads/MySQL-" + mm + "/" + f,
		"https://dev.mysql.com/get/Downloads/MySQL-" + mm + "/" + f,
		"https://cdn.mysql.com/archives/mysql-" + mm + "/" + f,
		"https://downloads.mysql.com/archives/get/p/23/file/" + f,
	}
}

// mariadbLatest queries the MariaDB REST API for the Windows zip of a release series.
func mariadbLatest(series string) (u, sha, version string, err error) {
	body, err := fetchText("https://downloads.mariadb.org/rest-api/mariadb/" + series + "/latest/")
	if err != nil {
		return "", "", "", err
	}
	var data any
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		return "", "", "", err
	}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			name, _ := x["file_name"].(string)
			if u == "" && strings.HasSuffix(name, "-winx64.zip") && !strings.Contains(name, "debug") {
				u, _ = x["file_download_url"].(string)
				if cs, ok := x["checksum"].(map[string]any); ok {
					sha, _ = cs["sha256sum"].(string)
				}
				version = versionRe.FindString(name)
			}
			for _, c := range x {
				walk(c)
			}
		case []any:
			for _, c := range x {
				walk(c)
			}
		}
	}
	walk(data)
	if u == "" {
		return "", "", "", errors.New("no Windows zip found for MariaDB " + series)
	}
	return u, sha, version, nil
}

func (a *App) installMySQL(t *Task, flavor, customURL string) error {
	if runtime.GOOS != "windows" && customURL == "" {
		return errors.New("automatic MySQL downloads are Windows builds; on Linux install mysql-server or mariadb-server with your package manager")
	}
	if a.mysql.Running() {
		return errors.New("stop MySQL before installing a new server")
	}
	marker := "bin/" + exeName("mysqld")
	var dir string
	var err error
	switch {
	case customURL != "":
		version := versionRe.FindString(path.Base(customURL))
		if strings.Contains(strings.ToLower(customURL), "mariadb") {
			flavor = "mariadb"
		}
		dir, err = a.installZip(t, customURL, "", "", "mysql", flavor+"-"+version, marker)
	case flavor == "mariadb":
		t.Set("Looking up MariaDB releases", -1)
		u, sha, version, lerr := mariadbLatest("11.4")
		if lerr != nil {
			return lerr
		}
		dir, err = a.installZip(t, u, "", sha, "mysql", "mariadb-"+version, marker)
	default:
		flavor = "mysql"
		t.Set("Looking up MySQL releases", -1)
		version := latestMySQLVersion()
		err = errors.New("no download URL worked")
		for _, u := range mysqlURLs(version) {
			if dir, err = a.installZip(t, u, "", "", "mysql", "mysql-"+version, marker); err == nil {
				break
			}
			a.log.Printf("mysql download %s: %v", u, err)
		}
	}
	if err != nil {
		return err
	}
	a.mu.Lock()
	delete(a.versions, "mysql")
	a.mu.Unlock()
	return a.update(func(c *Config) error {
		c.MySQL.Dir = a.rel(dir)
		c.MySQL.Bin = ""
		c.MySQL.Flavor = flavor
		return nil
	})
}

// installAdminer drops Adminer into www/adminer so it is served at adminer.<tld>.
func (a *App) installAdminer(t *Task) error {
	t.Set("Downloading Adminer", -1)
	resp, err := httpGet("https://www.adminer.org/latest-mysql-en.php", "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(strings.TrimSpace(string(b)), "<?php") {
		return errors.New("unexpected Adminer download")
	}
	cfg := a.config()
	if err := writeFileAtomic(filepath.Join(a.abs(cfg.WWWDir), "adminer", "index.php"), b); err != nil {
		return err
	}
	return a.update(func(c *Config) error {
		if s, ok := c.Sites["adminer"]; ok {
			s.Enabled = true
		} else {
			c.Sites["adminer"] = &SiteConfig{Enabled: true}
		}
		return nil
	})
}

// pendingOrder installs dependencies first (Composer needs PHP).
var pendingOrder = []string{"apache", "php", "mysql", "mariadb", "composer", "node", "mailpit", "redis", "adminer", "tool", "migrate"}

// alreadyInstalled reports whether a queued item ("php", "php:8.3",
// "tool:git", ...) is present, so re-running the installer over an existing
// install doesn't download everything again.
func (a *App) alreadyInstalled(cfg *Config, item string) bool {
	component, version, _ := strings.Cut(item, ":")
	switch component {
	case "apache":
		return installed(a.apacheBin(cfg))
	case "php":
		if version == "" {
			return len(cfg.PHP) > 0
		}
		_, ok := cfg.PHP[minorOf(version)]
		return ok
	case "mysql", "mariadb":
		return cfg.MySQL.Dir != "" && installed(a.mysqlBin(cfg))
	case "composer":
		return exists(a.composerPhar())
	case "node":
		return version == "" && a.nodeDir(cfg) != ""
	case "mailpit":
		return installed(a.mailpitBin(cfg))
	case "redis":
		return installed(a.redisBin(cfg))
	case "adminer":
		return exists(filepath.Join(a.abs(cfg.WWWDir), "adminer", "index.php"))
	case "tool":
		return toolPath(version) != ""
	}
	return false
}

func pendingRank(item string) int {
	for i, p := range pendingOrder {
		if strings.HasPrefix(item, p) {
			return i
		}
	}
	return len(pendingOrder)
}

// runPendingInstalls downloads the components picked in the installer, one at
// a time, and removes each from the queue once it is done (or has failed, so
// a broken download does not block every start; it can be retried from the UI).
func (a *App) runPendingInstalls() {
	items := append([]string(nil), a.config().PendingInstalls...)
	sort.SliceStable(items, func(i, j int) bool { return pendingRank(items[i]) < pendingRank(items[j]) })
	for _, item := range items {
		component, version, _ := strings.Cut(item, ":") // "php:8.3", "tool:git"
		t, err := a.startInstall(component, version, "")
		if err != nil {
			a.log.Printf("first-start install %s: %v", item, err)
		} else {
			for {
				v := t.View()
				if v.Status != "running" {
					if v.Status == "error" {
						a.log.Printf("first-start install %s failed: %s", item, v.Error)
					}
					break
				}
				time.Sleep(500 * time.Millisecond)
			}
		}
		_ = a.update(func(c *Config) error {
			var rest []string
			for _, p := range c.PendingInstalls {
				if p != item {
					rest = append(rest, p)
				}
			}
			c.PendingInstalls = rest
			return nil
		})
	}
}
