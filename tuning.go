package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Performance settings. Every choice has a safe default and a plain-words plus
// and minus that the dashboard shows next to it. Fields that are default-on use
// pointers so that an older omni.json without the field gets the new default.

const (
	defaultTLD      = "test"  // new installs
	legacyTLD       = "local" // omni.json written before the suffix was stored
	defaultWorkers  = 8       // new PHP registrations
	legacyWorkers   = 4       // a saved entry without a number
	maxWorkers      = 32
	defaultMaxReqs  = 1000
	maxMaxReqs      = 1000000
	defaultPool     = "512M"
	defaultRedisMax = "512mb"
)

// Tuning holds the global performance settings (omni.json "tuning").
type Tuning struct {
	// CodeVisible: "" or "2" = OPcache checks files every 2 s; "0" = every request.
	CodeVisible string `json:"code_visible,omitempty"`
	// OpcacheCLI: OPcache with a file cache for php/artisan/composer on the command line (default on).
	OpcacheCLI *bool `json:"opcache_cli,omitempty"`
	// LogStatic: write css/js/images/fonts to the Apache access log (default off).
	LogStatic bool `json:"log_static,omitempty"`
	// HTTP2: Protocols h2 http/1.1 on HTTPS vhosts when mod_http2 exists (default on).
	HTTP2 *bool `json:"http2,omitempty"`
	// FastDB switches on the MySQL/MariaDB development profile (default off).
	FastDB     bool   `json:"fast_db,omitempty"`
	BufferPool string `json:"innodb_buffer_pool,omitempty"`
	// RedisPersist keeps RDB snapshots (default off: the Windows build emulates fork).
	RedisPersist   bool   `json:"redis_persist,omitempty"`
	RedisMaxMemory string `json:"redis_maxmemory,omitempty"`
	// DefenderApplied remembers that Omni added the exclusions (Windows cannot always read them back).
	DefenderApplied bool `json:"defender_applied,omitempty"`
}

func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

func (t Tuning) opcacheCLIOn() bool { return boolOr(t.OpcacheCLI, true) }
func (t Tuning) http2On() bool      { return boolOr(t.HTTP2, true) }

func (t Tuning) revalidateFreq() string {
	if t.CodeVisible == "0" {
		return "0"
	}
	return "2"
}

func (t Tuning) bufferPool() string {
	if t.BufferPool == "" {
		return defaultPool
	}
	return t.BufferPool
}

func (t Tuning) redisMax() string {
	if t.RedisMaxMemory == "" {
		return defaultRedisMax
	}
	return t.RedisMaxMemory
}

var (
	poolRe     = regexp.MustCompile(`^[1-9][0-9]{0,4}[MG]$`)
	redisMaxRe = regexp.MustCompile(`^(0|[1-9][0-9]{0,5}(mb|gb))$`)
	tldRe      = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

// normalizePool validates an InnoDB buffer pool size such as "512M" or "2G".
func normalizePool(s string) (string, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if !poolRe.MatchString(s) {
		return "", errors.New("buffer pool size must look like 512M or 2G")
	}
	return s, nil
}

// normalizeRedisMax validates a Redis maxmemory value: "0" (no limit) or "512mb", "1gb".
func normalizeRedisMax(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if !redisMaxRe.MatchString(s) {
		return "", errors.New("Redis memory limit must be 0 (no limit) or look like 512mb or 1gb")
	}
	return s, nil
}

// normalizeTLD validates the domain suffix: one DNS label, no dots.
func normalizeTLD(s string) (string, error) {
	s = strings.Trim(strings.ToLower(strings.TrimSpace(s)), ".")
	if !tldRe.MatchString(s) {
		return "", errors.New("the domain suffix must be one word of letters, digits or hyphens, for example test")
	}
	return s, nil
}

// ---- Workers ----

// recommendedWorkers suggests a pool size from the logical CPU count: half of
// them, but at least 4 (a page that calls its own site needs two) and at most 16.
func recommendedWorkers(cpus int) int {
	n := cpus / 2
	if n < 4 {
		n = 4
	}
	if n > 16 {
		n = 16
	}
	return n
}

func (p *PHPInstall) maxRequests() int {
	if p.MaxRequests == nil {
		return defaultMaxReqs
	}
	return *p.MaxRequests
}

// workerEnv is the php-cgi environment. PHP_FCGI_MAX_REQUESTS makes a worker
// exit cleanly after that many requests, which frees leaked memory (0 = never).
func workerEnv(maxRequests int, iniDir string) []string {
	return []string{
		"PHP_FCGI_MAX_REQUESTS=" + strconv.Itoa(maxRequests),
		"PHP_FCGI_CHILDREN=0",
		"PHP_INI_SCAN_DIR=",
		"PHPRC=" + iniDir,
	}
}

// ---- Supervisor ----

const fastFailWindow = 10 * time.Second

// nextFastFails returns the new count of quick unexpected exits. A worker that
// recycles itself (clean exit 0, recycleOK) never counts and resets the streak;
// any other exit within the window counts, a long run resets the streak.
func nextFastFails(prev int, recycleOK bool, werr error, uptime time.Duration) int {
	if recycleOK && werr == nil {
		return 0
	}
	if uptime < fastFailWindow {
		return prev + 1
	}
	return 0
}

// ---- PHP ini ----

var xdebugModes = []string{"off", "debug", "develop", "debug,develop", "coverage"}

func validXdebugMode(m string) bool { return contains(xdebugModes, m) }

func (p *PHPInstall) xdebugMode() string {
	if validXdebugMode(p.XdebugMode) {
		return p.XdebugMode
	}
	return "off"
}

// phpTuningIni are the OPcache and path cache defaults (user overrides win).
func phpTuningIni(t Tuning) [][2]string {
	return [][2]string{
		{"opcache.revalidate_freq", t.revalidateFreq()},
		{"opcache.memory_consumption", "256"},
		{"opcache.interned_strings_buffer", "32"},
		{"opcache.max_accelerated_files", "32531"},
		{"realpath_cache_size", "16M"},
		{"realpath_cache_ttl", "600"},
	}
}

// phpDefaultIni is the full list of defaults Omni writes before the user's own settings.
func phpDefaultIni(t Tuning) [][2]string {
	return append(append([][2]string{}, defaultPHPIni...), phpTuningIni(t)...)
}

// xdebugIni is what Omni writes when the xdebug extension is on.
func xdebugIni(p *PHPInstall) [][2]string {
	return [][2]string{
		{"xdebug.mode", p.xdebugMode()},
		{"xdebug.start_with_request", "trigger"},
		{"xdebug.client_host", "127.0.0.1"},
		{"xdebug.client_port", "9003"},
	}
}

func phpHas(p *PHPInstall, ext string) bool {
	return contains(p.Extensions, ext) || contains(p.Builtin, ext)
}

// cliOpcacheArgs are the -d options the php/composer shims add so that artisan,
// PHPUnit and composer reuse compiled files between runs. They go on the command
// line (not into php.ini) so the web workers keep a memory-only OPcache. A user
// override of the same keys in the PHP tab wins: the flags are left out then.
func cliOpcacheArgs(t Tuning, p *PHPInstall, cacheDir string) []string {
	if !t.opcacheCLIOn() || !phpHas(p, "opcache") {
		return nil
	}
	for _, k := range []string{"opcache.enable_cli", "opcache.file_cache"} {
		if _, ok := p.INI[k]; ok {
			return nil
		}
	}
	return []string{"-d", "opcache.enable_cli=1", "-d", "opcache.file_cache=" + cacheDir}
}

// ---- Apache ----

// staticLogEnvRule marks requests for static files so the access log skips them.
const staticLogEnvRule = `SetEnvIf Request_URI "(?i)\.(css|js|mjs|map|png|jpe?g|gif|svg|ico|webp|avif|woff2?|ttf|otf|eot)$" nolog`

// fastRewriteFrameworks can use the vhost rewrite instead of .htaccess.
func fastRewriteOK(framework string) bool {
	switch framework {
	case "laravel", "symfony", "codeigniter":
		return true
	}
	return false
}

// ---- Log rotation ----

const (
	accessLogMax  = 10 << 20
	accessLogKeep = 3
)

// shouldRotate decides whether a log of size bytes is rotated.
func shouldRotate(size, max int64) bool { return max > 0 && size >= max }

// rotateLog keeps keep old copies (path.1 newest ... path.keep oldest). It copies
// and truncates instead of renaming, so it also works while Apache holds the file
// open on Windows. It reports whether it rotated.
func rotateLog(path string, max int64, keep int) (bool, error) {
	st, err := os.Stat(path)
	if err != nil || !shouldRotate(st.Size(), max) {
		return false, nil
	}
	_ = os.Remove(fmt.Sprintf("%s.%d", path, keep))
	for i := keep - 1; i >= 1; i-- {
		old, next := fmt.Sprintf("%s.%d", path, i), fmt.Sprintf("%s.%d", path, i+1)
		if exists(old) {
			if err := os.Rename(old, next); err != nil {
				return false, err
			}
		}
	}
	src, err := os.Open(path)
	if err != nil {
		return false, err
	}
	dst, err := os.Create(path + ".1")
	if err != nil {
		src.Close()
		return false, err
	}
	_, cerr := io.Copy(dst, src)
	src.Close()
	if err := dst.Close(); cerr == nil {
		cerr = err
	}
	if cerr != nil {
		return false, cerr
	}
	if err := os.Truncate(path, 0); err != nil {
		return false, err
	}
	return true, nil
}

func (a *App) rotateLogs() {
	p := a.path("logs", "apache_access.log")
	if ok, err := rotateLog(p, accessLogMax, accessLogKeep); err != nil {
		a.log.Printf("rotating %s: %v", p, err)
	} else if ok {
		a.log.Printf("rotated %s (kept %d old files)", p, accessLogKeep)
	}
}

// ---- MySQL / MariaDB ----

// recommendBufferPoolMB is a quarter of the RAM, between 256 MB and 2 GB, in 256 MB steps.
func recommendBufferPoolMB(ramMB int) int {
	mb := ramMB / 4 / 256 * 256
	if mb < 256 {
		mb = 256
	}
	if mb > 2048 {
		mb = 2048
	}
	return mb
}

// versionAtLeast compares dotted versions ("8.4.6" >= "8.0.30").
func versionAtLeast(v, min string) bool {
	return v != "" && compareVersions(v, min) >= 0
}

// myIniTuning returns the extra lines of the "Fast dev database" profile for
// one flavor. MySQL-only options are never written for MariaDB and vice versa.
// version is the server version ("" when unknown: then the old log file option is used).
func myIniTuning(flavor, version string, t Tuning) []string {
	if !t.FastDB {
		return nil
	}
	var l []string
	if flavor != "mariadb" {
		l = append(l, "skip-log-bin") // MariaDB has no binary log unless asked for
	}
	l = append(l, "innodb_flush_log_at_trx_commit = 2")
	if flavor != "mariadb" && versionAtLeast(version, "8.0.30") {
		l = append(l, "innodb_redo_log_capacity = 512M")
	} else {
		l = append(l, "innodb_log_file_size = 512M")
	}
	if flavor != "mariadb" {
		l = append(l, "performance_schema = OFF") // MariaDB ships with it off
	}
	return l
}

// myIniBufferPool is the buffer pool line: 256M unless the profile is on.
func myIniBufferPool(t Tuning) string {
	if t.FastDB {
		return "innodb_buffer_pool_size = " + t.bufferPool()
	}
	return "innodb_buffer_pool_size = 256M"
}

// ---- Redis ----

// redisTuning returns the persistence and memory lines of redis.conf.
func redisTuning(t Tuning) string {
	var b strings.Builder
	if t.RedisPersist {
		b.WriteString("save 900 1\nsave 300 10\n")
	} else {
		b.WriteString("save \"\"\n")
	}
	b.WriteString("appendonly no\n")
	fmt.Fprintf(&b, "maxmemory %s\n", t.redisMax())
	// volatile-lru only evicts keys that have a TTL; allkeys-lru would drop queued jobs and sessions.
	b.WriteString("maxmemory-policy volatile-lru\n")
	return b.String()
}

// ---- Windows Defender ----

var defenderProcesses = []string{"php-cgi.exe", "php.exe", "httpd.exe", "mysqld.exe", "mariadbd.exe", "node.exe", "redis-server.exe"}

func psSingleQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// defenderScript builds the PowerShell for "apply" or "remove". Every value is
// a single-quoted literal; paths with control characters or double quotes are refused.
func defenderScript(action string, paths, procs []string) (string, error) {
	var cmdlet string
	switch action {
	case "apply":
		cmdlet = "Add-MpPreference"
	case "remove":
		cmdlet = "Remove-MpPreference"
	default:
		return "", fmt.Errorf("unknown action %q", action)
	}
	quoteAll := func(list []string) (string, error) {
		q := make([]string, 0, len(list))
		for _, v := range list {
			if v == "" || strings.ContainsAny(v, "\"\r\n\x00`$") {
				return "", fmt.Errorf("refusing path or name %q", v)
			}
			q = append(q, psSingleQuote(v))
		}
		return strings.Join(q, ","), nil
	}
	p, err := quoteAll(paths)
	if err != nil {
		return "", err
	}
	x, err := quoteAll(procs)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("$ErrorActionPreference = 'Stop'\n%s -ExclusionPath %s\n%s -ExclusionProcess %s\n", cmdlet, p, cmdlet, x), nil
}

// parseDefenderStatus reads the JSON of Get-MpPreference (ExclusionPath and
// ExclusionProcess) and reports "applied", "partial", "none" or "unknown".
// Without administrator rights Windows answers "N/A: Must be an administrator".
func parseDefenderStatus(out string, paths, procs []string) string {
	low := strings.ToLower(out)
	if strings.TrimSpace(out) == "" || strings.Contains(low, "must be an administrator") {
		return "unknown"
	}
	found, total := 0, 0
	for _, p := range paths {
		total++
		if strings.Contains(low, strings.ToLower(strings.ReplaceAll(p, `\`, `\\`))) || strings.Contains(low, strings.ToLower(p)) {
			found++
		}
	}
	for _, p := range procs {
		total++
		if strings.Contains(low, `"`+strings.ToLower(p)+`"`) {
			found++
		}
	}
	switch {
	case found == 0:
		return "none"
	case found == total:
		return "applied"
	}
	return "partial"
}

// defenderTargets are the folders and process names Omni asks Defender to skip.
func (a *App) defenderTargets(cfg *Config) (paths, procs []string) {
	paths = []string{a.path("bin"), a.path("data"), a.path("tmp"), a.abs(cfg.WWWDir)}
	return paths, defenderProcesses
}
