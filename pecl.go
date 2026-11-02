package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---- PHP extensions from the official PECL Windows builds ----

const peclBase = "https://downloads.php.net/~windows/pecl/releases/"

// maxPeclFolders caps how many release folders are tried, newest first. The
// newest release may not have a build for an old PHP yet (or any more).
const maxPeclFolders = 5

// phpVC is the compiler tag in the file names of Windows PHP builds.
func phpVC(minor string) string {
	switch {
	case compareVersions(minor, "7.2") < 0:
		return "vc14"
	case compareVersions(minor, "8.0") < 0:
		return "vc15"
	case compareVersions(minor, "8.4") < 0:
		return "vs16"
	}
	return "vs17"
}

// peclAssetName is the zip of an extension build, e.g. php_redis-6.3.0-8.4-nts-vs17-x64.zip.
func peclAssetName(ext, release, minor string, ts bool) string {
	safety := "nts"
	if ts {
		safety = "ts"
	}
	return fmt.Sprintf("php_%s-%s-%s-%s-%s-x64.zip", ext, release, minor, safety, phpVC(minor))
}

var peclFolderRe = regexp.MustCompile(`(?i)href="(\d+\.\d+\.\d+)/"`)

// parsePeclReleases lists the stable version folders of a directory listing,
// newest first. Folders such as 6.0.0RC1 or 6.1.0beta2 are skipped.
func parsePeclReleases(html string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range peclFolderRe.FindAllStringSubmatch(html, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	sort.Slice(out, func(i, j int) bool { return compareVersions(out[i], out[j]) > 0 })
	return out
}

// phpThreadSafe tells a thread safe (TS) PHP from a non thread safe (NTS) one.
// TS builds ship php8ts.dll / php7ts.dll next to php-cgi.exe; NTS builds have
// no such file. The folder check works on any registered PHP without running
// it (PHP_ZTS would need a probe and a new config field).
func phpThreadSafe(dir string) bool {
	m, _ := filepath.Glob(filepath.Join(dir, "php[0-9]ts.dll"))
	return len(m) > 0
}

// peclExt is one entry of the registry the user picks from. Add a line to
// peclExtensions to offer another extension whose zip holds a self-contained
// php_<name>.dll at its root (imagick is left out: it needs a dozen more DLLs).
type peclExt struct {
	Name string
	Desc string
}

var peclExtensions = []peclExt{
	{"redis", "Redis client (phpredis): REDIS_CLIENT=phpredis, Laravel cache and queues"},
	{"apcu", "In-process user cache (APCu)"},
	{"igbinary", "Faster binary serializer, also used by phpredis"},
	{"xdebug", "Step debugger and profiler"},
	{"mongodb", "MongoDB driver"},
}

func peclKnown(name string) bool {
	for _, e := range peclExtensions {
		if e.Name == name {
			return true
		}
	}
	return false
}

// extMissing lists extensions that are switched on in the config but have no
// DLL in the extension folder (for example after a PHP patch upgrade replaced
// the folder). They are skipped in php.ini and offered for download again.
func extMissing(enabled, builtin, available []string) []string {
	var out []string
	for _, e := range enabled {
		if !contains(builtin, e) && !contains(available, e) {
			out = append(out, e)
		}
	}
	return out
}

// peclBuild is what a PHP version needs: its minor line and thread safety.
type peclBuild struct {
	Minor string
	TS    bool
}

type peclCached struct {
	body string
	at   time.Time
}

// peclListings caches the directory listings of downloads.php.net for a while.
var (
	peclMu       sync.Mutex
	peclListings = map[string]peclCached{}
)

func peclListing(u string) (string, error) {
	peclMu.Lock()
	c, ok := peclListings[u]
	peclMu.Unlock()
	if ok && time.Since(c.at) < 30*time.Minute {
		return c.body, nil
	}
	body, err := fetchText(u)
	if err != nil {
		return "", err
	}
	peclMu.Lock()
	peclListings[u] = peclCached{body, time.Now()}
	peclMu.Unlock()
	return body, nil
}

// peclFind returns the release and zip URL of the newest build of ext for b,
// looking at the newest maxPeclFolders releases only.
func peclFind(ext string, b peclBuild) (release, url string, err error) {
	base := peclBase + ext + "/"
	listing, err := peclListing(base)
	if err != nil {
		return "", "", err
	}
	releases := parsePeclReleases(listing)
	if len(releases) > maxPeclFolders {
		releases = releases[:maxPeclFolders]
	}
	for _, rel := range releases {
		name := peclAssetName(ext, rel, b.Minor, b.TS)
		body, err := peclListing(base + rel + "/")
		if err != nil {
			continue
		}
		if strings.Contains(body, name) {
			return rel, base + rel + "/" + name, nil
		}
	}
	kind := "NTS"
	if b.TS {
		kind = "TS"
	}
	return "", "", fmt.Errorf("no php_%s build for PHP %s (%s, x64, %s) among the newest releases on downloads.php.net", ext, b.Minor, kind, phpVC(b.Minor))
}

// PeclView is one registry entry as shown for one PHP version.
type PeclView struct {
	Name      string `json:"name"`
	Desc      string `json:"desc"`
	Installed bool   `json:"installed"` // DLL in the extension folder
	Enabled   bool   `json:"enabled"`
	Missing   bool   `json:"missing"` // enabled, but the DLL is gone
}

func (a *App) peclViews(p *PHPInstall) []PeclView {
	avail := a.availableExtensions(p)
	var out []PeclView
	for _, e := range peclExtensions {
		if contains(p.Builtin, e.Name) {
			continue
		}
		inst, on := contains(avail, e.Name), contains(p.Extensions, e.Name)
		out = append(out, PeclView{Name: e.Name, Desc: e.Desc, Installed: inst, Enabled: on && inst, Missing: on && !inst})
	}
	return out
}

// installPECL puts php_<name>.dll into the extension folder of a PHP version
// and, when enable is set, switches it on with the normal extension toggle.
func (a *App) installPECL(t *Task, minor, name string, enable bool) error {
	if runtime.GOOS != "windows" {
		return errors.New("on Linux/macOS install the PHP extension (php-" + name + ") with your package manager; it then appears as an extension toggle")
	}
	if !peclKnown(name) {
		return fmt.Errorf("unknown extension %q", name)
	}
	minor = minorOf(minor)
	p, ok := a.config().PHP[minor]
	if !ok {
		return fmt.Errorf("PHP %s is not installed", minor)
	}
	t.Set(fmt.Sprintf("Looking up %s for PHP %s", name, minor), -1)
	_, url, err := peclFind(name, peclBuild{Minor: minor, TS: phpThreadSafe(a.abs(p.Dir))})
	if err != nil {
		return err
	}
	file, err := a.download(t, url, "", "")
	if err != nil {
		return err
	}
	defer os.Remove(file)
	tmp := a.path("tmp", "extract-"+fmt.Sprint(time.Now().UnixNano()))
	defer os.RemoveAll(tmp)
	if err := extractZip(t, file, tmp); err != nil {
		return err
	}
	dll := "php_" + name + ".dll"
	src := filepath.Join(tmp, dll)
	if !exists(src) {
		return errors.New(dll + " not found in the downloaded archive")
	}
	target := filepath.Join(a.phpExtDir(p), dll)
	if exists(target) {
		// A running PHP keeps the loaded DLL open: Windows cannot overwrite it
		// but can rename it; the old file goes away when PHP restarts.
		old := target + ".old"
		_ = os.Remove(old)
		if err := os.Rename(target, old); err != nil {
			return fmt.Errorf("cannot replace %s (stop PHP first?): %v", target, err)
		}
		defer os.Remove(old)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(target, data); err != nil {
		return fmt.Errorf("cannot write %s: %v", target, err)
	}
	if !enable {
		t.Set(dll+" saved; switch it on in the PHP tab", 1)
		return nil
	}
	t.Set("Enabling "+name, 0.99)
	return a.setExtension(minor, name, true)
}

// installPECLAll is the "phpext" / "phpredis" component: one PHP version, or
// every installed one when minor is empty.
func (a *App) installPECLAll(t *Task, minor, name string, enable bool) error {
	if minor != "" {
		return a.installPECL(t, minor, name, enable)
	}
	cfg := a.config()
	if len(cfg.PHP) == 0 {
		return errors.New("no PHP is installed")
	}
	var failed []string
	for _, m := range cfg.phpVersions() {
		if err := a.installPECL(t, m, name, enable); err != nil {
			failed = append(failed, fmt.Sprintf("PHP %s: %v", m, err))
		}
	}
	if len(failed) > 0 {
		return errors.New(strings.Join(failed, "; "))
	}
	return nil
}

// peclAvailability asks downloads.php.net which registry entries have a build
// for a PHP version (network; listings are cached for a while).
func (a *App) peclAvailability(minor string) (map[string]string, error) {
	minor = minorOf(minor)
	p, ok := a.config().PHP[minor]
	if !ok {
		return nil, apiError{404, "PHP " + minor + " is not installed"}
	}
	b := peclBuild{Minor: minor, TS: phpThreadSafe(a.abs(p.Dir))}
	out := map[string]string{}
	for _, e := range peclExtensions {
		rel, _, err := peclFind(e.Name, b)
		if err != nil {
			rel = ""
		}
		out[e.Name] = rel
	}
	return out, nil
}

// newerPatch reports whether the catalog version is a newer patch of the
// installed one (same minor line only).
func newerPatch(installedVer, catalogVer string) bool {
	return installedVer != "" && minorOf(installedVer) == minorOf(catalogVer) && compareVersions(catalogVer, installedVer) > 0
}
