package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNextFastFails(t *testing.T) {
	boom := errors.New("exit status 1")
	cases := []struct {
		name    string
		prev    int
		recycle bool
		err     error
		uptime  time.Duration
		want    int
	}{
		{"crash right after start counts", 2, true, boom, time.Second, 3},
		{"clean exit of a worker is a recycle", 4, true, nil, time.Second, 0},
		{"clean exit of a service still counts", 1, false, nil, time.Second, 2},
		{"long run resets", 4, true, boom, time.Minute, 0},
	}
	for _, c := range cases {
		if got := nextFastFails(c.prev, c.recycle, c.err, c.uptime); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

func TestWorkerEnvAndRecommendations(t *testing.T) {
	env := strings.Join(workerEnv(1000, "C:/ini"), " ")
	if !strings.Contains(env, "PHP_FCGI_MAX_REQUESTS=1000") || !strings.Contains(env, "PHPRC=C:/ini") {
		t.Fatal(env)
	}
	p := &PHPInstall{}
	if p.maxRequests() != 1000 {
		t.Fatal("default max requests")
	}
	zero := 0
	p.MaxRequests = &zero
	if p.maxRequests() != 0 {
		t.Fatal("0 must stay 0 (never)")
	}
	for cpus, want := range map[int]int{2: 4, 8: 4, 16: 8, 64: 16} {
		if got := recommendedWorkers(cpus); got != want {
			t.Errorf("recommendedWorkers(%d) = %d want %d", cpus, got, want)
		}
	}
	for ram, want := range map[int]int{1024: 256, 8192: 2048 / 1, 4096: 1024, 61000: 2048} {
		_ = want
		if got := recommendBufferPoolMB(ram); got < 256 || got > 2048 {
			t.Errorf("pool for %d MB = %d", ram, got)
		}
	}
	if recommendBufferPoolMB(4096) != 1024 || recommendBufferPoolMB(1024) != 256 {
		t.Fatal("pool steps")
	}
}

func TestNormalizers(t *testing.T) {
	if v, err := normalizeTLD(" .Test "); err != nil || v != "test" {
		t.Fatal(v, err)
	}
	for _, bad := range []string{"", "a.b", "-x", "te st", "x_y"} {
		if _, err := normalizeTLD(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
	if v, err := normalizePool("1g"); err != nil || v != "1G" {
		t.Fatal(v, err)
	}
	if _, err := normalizePool("lots"); err == nil {
		t.Fatal("pool")
	}
	if v, err := normalizeRedisMax("512MB"); err != nil || v != "512mb" {
		t.Fatal(v, err)
	}
	if _, err := normalizeRedisMax("0"); err != nil {
		t.Fatal("0 means no limit")
	}
	if _, err := normalizeRedisMax("12 gigs"); err == nil {
		t.Fatal("redis max")
	}
}

func TestTLDMigration(t *testing.T) {
	// no file: new install gets the new default
	cfg, fresh, err := loadConfig(t.TempDir())
	if err != nil || !fresh || cfg.TLD != "test" {
		t.Fatalf("fresh: %v %v %q", err, fresh, cfg.TLD)
	}
	// an older omni.json without "tld" stays on .local
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, configFile), []byte(`{"dashboard_port": 8801}`), 0o644)
	cfg, _, err = loadConfig(root)
	if err != nil || cfg.TLD != "local" {
		t.Fatalf("legacy: %v %q", err, cfg.TLD)
	}
	// a saved value is kept, also through clone
	os.WriteFile(filepath.Join(root, configFile), []byte(`{"tld": "Dev.Lan"}`), 0o644)
	cfg, _, _ = loadConfig(root)
	if cfg.TLD != "dev.lan" || cfg.clone().TLD != "dev.lan" {
		t.Fatalf("saved: %q", cfg.TLD)
	}
	// worker numbers: saved ones are kept, the cap is 32
	os.WriteFile(filepath.Join(root, configFile), []byte(`{"php": {"8.4": {"workers": 4}, "8.3": {"workers": 99}, "8.2": {}}}`), 0o644)
	cfg, _, _ = loadConfig(root)
	if cfg.PHP["8.4"].Workers != 4 || cfg.PHP["8.3"].Workers != 32 || cfg.PHP["8.2"].Workers != 4 {
		t.Fatalf("workers %d %d %d", cfg.PHP["8.4"].Workers, cfg.PHP["8.3"].Workers, cfg.PHP["8.2"].Workers)
	}
	if migrateTLD("  ") != "local" || migrateTLD("test") != "test" {
		t.Fatal("migrateTLD")
	}
}

func iniMap(ini string) map[string]string {
	m := map[string]string{}
	for _, l := range strings.Split(strings.ReplaceAll(ini, "\r\n", "\n"), "\n") {
		if k, v, ok := strings.Cut(l, " = "); ok {
			m[k] = strings.Trim(v, `"`)
		}
	}
	return m
}

func TestRenderPHPIniTuning(t *testing.T) {
	a := &App{root: t.TempDir()}
	cfg := defaultConfig()
	p := &PHPInstall{Dir: "php", Extensions: []string{"opcache"}, INI: map[string]string{}}
	m := iniMap(a.renderPHPIni(cfg, "8.4", p))
	want := map[string]string{
		"opcache.revalidate_freq": "2", "opcache.memory_consumption": "256", "opcache.interned_strings_buffer": "32",
		"opcache.max_accelerated_files": "32531", "realpath_cache_size": "16M", "realpath_cache_ttl": "600",
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %q want %q", k, m[k], v)
		}
	}
	if _, ok := m["opcache.jit"]; ok {
		t.Error("JIT must stay untouched")
	}
	// instant mode
	cfg.Tuning.CodeVisible = "0"
	if m := iniMap(a.renderPHPIni(cfg, "8.4", p)); m["opcache.revalidate_freq"] != "0" {
		t.Error("instant mode")
	}
	// a value the user set wins
	p.INI["opcache.revalidate_freq"] = "7"
	p.INI["opcache.memory_consumption"] = "512"
	m = iniMap(a.renderPHPIni(cfg, "8.4", p))
	if m["opcache.revalidate_freq"] != "7" || m["opcache.memory_consumption"] != "512" {
		t.Errorf("user override lost: %v", m)
	}
	// xdebug: off by default, switchable, no xdebug keys without the extension
	if _, ok := m["xdebug.mode"]; ok {
		t.Error("xdebug keys without xdebug")
	}
	p.Extensions = append(p.Extensions, "xdebug")
	m = iniMap(a.renderPHPIni(cfg, "8.4", p))
	if m["xdebug.mode"] != "off" || m["xdebug.start_with_request"] != "trigger" {
		t.Errorf("xdebug default: %v", m)
	}
	p.XdebugMode = "debug,develop"
	if m = iniMap(a.renderPHPIni(cfg, "8.4", p)); m["xdebug.mode"] != "debug,develop" {
		t.Errorf("xdebug mode: %v", m["xdebug.mode"])
	}
	p.XdebugMode = "bogus"
	if p.xdebugMode() != "off" {
		t.Error("invalid mode falls back to off")
	}
}

func TestCLIOpcacheArgs(t *testing.T) {
	cfg := defaultConfig()
	p := &PHPInstall{Extensions: []string{"opcache"}, INI: map[string]string{}}
	got := strings.Join(cliOpcacheArgs(cfg.Tuning, p, "C:/o/84"), " ")
	if got != "-d opcache.enable_cli=1 -d opcache.file_cache=C:/o/84" {
		t.Fatal(got)
	}
	off := false
	cfg.Tuning.OpcacheCLI = &off
	if cliOpcacheArgs(cfg.Tuning, p, "x") != nil {
		t.Error("setting off")
	}
	cfg.Tuning.OpcacheCLI = nil
	p.INI["opcache.enable_cli"] = "0"
	if cliOpcacheArgs(cfg.Tuning, p, "x") != nil {
		t.Error("user override must win")
	}
	if cliOpcacheArgs(cfg.Tuning, &PHPInstall{}, "x") != nil {
		t.Error("no opcache, no flags")
	}
	// the shims carry the flags, the php.ini does not
	a := &App{root: t.TempDir()}
	cfg.PHP["8.4"] = &PHPInstall{CLI: filepath.Join(a.root, "php.exe"), Extensions: []string{"opcache"}, INI: map[string]string{}}
	cfg.DefaultPHP = "8.4"
	s := a.shims(cfg)["php"]
	if !strings.Contains(strings.Join(s.args, " "), "opcache.enable_cli=1") {
		t.Errorf("shim args %v", s.args)
	}
}

func TestMyIniTuning(t *testing.T) {
	var tn Tuning
	if myIniTuning("mysql", "8.4.6", tn) != nil || myIniBufferPool(tn) != "innodb_buffer_pool_size = 256M" {
		t.Fatal("profile is off by default and keeps 256M")
	}
	tn.FastDB = true
	join := func(flavor, ver string) string { return strings.Join(myIniTuning(flavor, ver, tn), "\n") }
	my := join("mysql", "8.4.6")
	for _, w := range []string{"skip-log-bin", "innodb_flush_log_at_trx_commit = 2", "innodb_redo_log_capacity = 512M", "performance_schema = OFF"} {
		if !strings.Contains(my, w) {
			t.Errorf("mysql 8.4 misses %q", w)
		}
	}
	if strings.Contains(my, "innodb_log_file_size") {
		t.Error("mysql 8.4 must not get the old log option")
	}
	old := join("mysql", "8.0.29")
	if !strings.Contains(old, "innodb_log_file_size = 512M") || strings.Contains(old, "redo_log_capacity") {
		t.Errorf("mysql 8.0.29: %s", old)
	}
	if !strings.Contains(join("mysql", ""), "innodb_log_file_size") {
		t.Error("unknown version uses the old option")
	}
	maria := join("mariadb", "11.4.2")
	if strings.Contains(maria, "skip-log-bin") || strings.Contains(maria, "performance_schema") || strings.Contains(maria, "redo_log_capacity") {
		t.Errorf("MySQL-only options in MariaDB:\n%s", maria)
	}
	if !strings.Contains(maria, "innodb_log_file_size = 512M") || !strings.Contains(maria, "innodb_flush_log_at_trx_commit = 2") {
		t.Errorf("mariadb:\n%s", maria)
	}
	if myIniBufferPool(tn) != "innodb_buffer_pool_size = 512M" {
		t.Error("buffer pool default 512M")
	}
	tn.BufferPool = "2G"
	if myIniBufferPool(tn) != "innodb_buffer_pool_size = 2G" {
		t.Error("buffer pool setting")
	}
}

func TestRenderMyIniFastDB(t *testing.T) {
	a := &App{root: t.TempDir(), versions: map[string]string{"mysql": "8.4.6"}}
	cfg := defaultConfig()
	cfg.MySQL.Flavor = "mysql"
	if ini := a.renderMyIni(cfg); strings.Contains(ini, "skip-log-bin") || !strings.Contains(ini, "innodb_buffer_pool_size = 256M") {
		t.Errorf("default must not change:\n%s", ini)
	}
	cfg.Tuning.FastDB = true
	ini := a.renderMyIni(cfg)
	if !strings.Contains(ini, "skip-log-bin") || strings.Contains(ini, "innodb_buffer_pool_size = 256M") {
		t.Errorf("fast profile:\n%s", ini)
	}
}

func TestRedisTuning(t *testing.T) {
	var tn Tuning
	c := redisTuning(tn)
	for _, w := range []string{`save ""`, "maxmemory 512mb", "maxmemory-policy volatile-lru", "appendonly no"} {
		if !strings.Contains(c, w) {
			t.Errorf("missing %q in\n%s", w, c)
		}
	}
	if strings.Contains(c, "allkeys") || strings.Contains(c, "save 900") {
		t.Error("allkeys-lru and snapshots must be off by default")
	}
	tn.RedisPersist, tn.RedisMaxMemory = true, "1gb"
	c = redisTuning(tn)
	if !strings.Contains(c, "save 900 1") || !strings.Contains(c, "save 300 10") || strings.Contains(c, `save ""`) || !strings.Contains(c, "maxmemory 1gb") {
		t.Errorf("persist:\n%s", c)
	}
	a := &App{root: t.TempDir()}
	if conf := a.renderRedisConf(defaultConfig()); !strings.Contains(conf, "maxmemory-policy volatile-lru") {
		t.Error("renderRedisConf")
	}
}

func TestLogRotation(t *testing.T) {
	if shouldRotate(5, 10) || !shouldRotate(10, 10) || shouldRotate(100, 0) {
		t.Fatal("shouldRotate")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "apache_access.log")
	write := func(p, s string) { os.WriteFile(p, []byte(s), 0o644) }
	read := func(p string) string { b, _ := os.ReadFile(p); return string(b) }
	write(log, "small")
	if ok, err := rotateLog(log, 10, 3); ok || err != nil {
		t.Fatal("small log must stay")
	}
	for i, content := range []string{"AAAAAAAAAAAA", "BBBBBBBBBBBB", "CCCCCCCCCCCC", "DDDDDDDDDDDD"} {
		write(log, content)
		if ok, err := rotateLog(log, 10, 3); !ok || err != nil {
			t.Fatalf("round %d: %v %v", i, ok, err)
		}
		if read(log) != "" {
			t.Fatal("log not truncated")
		}
	}
	// newest first, only 3 kept, the oldest (A) is gone
	if read(log+".1") != "DDDDDDDDDDDD" || read(log+".2") != "CCCCCCCCCCCC" || read(log+".3") != "BBBBBBBBBBBB" {
		t.Fatalf("chain: %q %q %q", read(log+".1"), read(log+".2"), read(log+".3"))
	}
	if exists(log + ".4") {
		t.Fatal("too many old files")
	}
}

func TestApacheTuningLines(t *testing.T) {
	base := apacheInput{Port: 80, Logs: "/l", Tmp: "/t", CustomDir: "/c", WWW: "/w",
		Vhosts: []apacheVhost{{Name: "a", Host: "a.test", DocRoot: "/w/a/public", FastRewrite: true}, {Name: "b", Host: "b.test", DocRoot: "/w/b"}}}
	conf := renderApacheConf(base)
	for _, w := range []string{"KeepAlive On", "MaxKeepAliveRequests 500", "KeepAliveTimeout 5", "env=!nolog", "SetEnvIf Request_URI", "EnableSendfile off"} {
		if !strings.Contains(conf, w) {
			t.Errorf("missing %q", w)
		}
	}
	if strings.Count(conf, "FallbackResource /index.php") != 1 || strings.Count(conf, "AllowOverride None") != 2 { // 1 for "/" + 1 fast vhost
		t.Errorf("fast rewrite must apply to one vhost only:\n%s", conf)
	}
	if !strings.Contains(conf, "AllowOverride All") {
		t.Error("other vhosts keep AllowOverride All")
	}
	base.LogStatic = true
	if conf = renderApacheConf(base); strings.Contains(conf, "nolog") {
		t.Error("log static files on: no filter")
	}
	// h2 only on HTTPS vhosts
	base.HTTPSPort, base.HTTP2 = 443, true
	base.LocalCert = [2]string{"c", "k"}
	base.Vhosts[0].Cert, base.Vhosts[0].Key = "c", "k"
	conf = renderApacheConf(base)
	if strings.Count(conf, "Protocols h2 http/1.1") != 2 { // localhost + a.test over TLS
		t.Errorf("h2 on TLS vhosts:\n%s", conf)
	}
	base.HTTP2 = false
	if strings.Contains(renderApacheConf(base), "Protocols") {
		t.Error("no h2 without the module")
	}
	if !fastRewriteOK("laravel") || fastRewriteOK("wordpress") {
		t.Error("fastRewriteOK")
	}
}

func TestDefenderScript(t *testing.T) {
	s, err := defenderScript("apply", []string{`C:\Omni Web\bin`, `D:\it's\www`}, []string{"php.exe"})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"Add-MpPreference -ExclusionPath 'C:\\Omni Web\\bin','D:\\it''s\\www'", "Add-MpPreference -ExclusionProcess 'php.exe'"} {
		if !strings.Contains(s, w) {
			t.Errorf("missing %q in\n%s", w, s)
		}
	}
	if r, _ := defenderScript("remove", []string{"C:\\x"}, []string{"a.exe"}); !strings.Contains(r, "Remove-MpPreference -ExclusionPath") {
		t.Error("remove")
	}
	if _, err := defenderScript("apply", []string{"C:\\x\"; calc"}, nil); err == nil {
		t.Error("double quote must be refused")
	}
	if _, err := defenderScript("apply", []string{"C:\\x\nrm"}, nil); err == nil {
		t.Error("newline must be refused")
	}
	if _, err := defenderScript("wipe", nil, nil); err == nil {
		t.Error("unknown action")
	}
	a := &App{root: `C:\Omni`}
	cfg := defaultConfig()
	cfg.WWWDir = `D:\www`
	paths, procs := a.defenderTargets(cfg)
	if len(paths) != 4 || !contains(procs, "php-cgi.exe") || !contains(procs, "redis-server.exe") {
		t.Errorf("targets %v %v", paths, procs)
	}
}

func TestParseDefenderStatus(t *testing.T) {
	paths := []string{`C:\Omni\bin`, `D:\www`}
	procs := []string{"php.exe"}
	all := `{"p":["C:\\Omni\\bin","D:\\www"],"x":["php.exe"]}`
	if got := parseDefenderStatus(all, paths, procs); got != "applied" {
		t.Error(got)
	}
	if got := parseDefenderStatus(`{"p":["C:\\Omni\\bin"],"x":[]}`, paths, procs); got != "partial" {
		t.Error(got)
	}
	if got := parseDefenderStatus(`{"p":[],"x":[]}`, paths, procs); got != "none" {
		t.Error(got)
	}
	if got := parseDefenderStatus(`{"p":["N/A: Must be an administrator to view exclusions"],"x":[]}`, paths, procs); got != "unknown" {
		t.Error(got)
	}
	if parseDefenderStatus("", paths, procs) != "unknown" {
		t.Error("empty output is unknown")
	}
}

func TestHostsMissing(t *testing.T) {
	hosts := "127.0.0.1 localhost\r\n# BEGIN\r\n127.0.0.1\tblog.test\r\n::1\t\twww.blog.test # x\r\n"
	if m := hostsMissing(hosts, []string{"blog.test", "shop.test", "WWW.blog.test"}); len(m) != 1 || m[0] != "shop.test" {
		t.Fatal(m)
	}
}

func TestFinalTaskMessage(t *testing.T) {
	if finalMessage("Downloading composer.phar: 0.0 MB", 0.9) != "Done" {
		t.Error("stale progress line must be replaced")
	}
	if finalMessage("Downloading x", -1) != "Done" {
		t.Error("indeterminate progress")
	}
	if finalMessage("Installed PHP 8.4 (8.4)", 1) != "Installed PHP 8.4 (8.4)" {
		t.Error("a final message at progress 1 is kept")
	}
	if finalMessage("", 1) != "Done" {
		t.Error("empty message")
	}
	// through the manager
	m := NewTaskManager()
	tk := m.Run("x", func(t *Task) error { t.Set("Downloading composer.phar: 0.0 MB", 0.0); return nil })
	for i := 0; i < 100 && tk.View().Status == "running"; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if v := tk.View(); v.Status != "done" || v.Message != "Done" || v.Progress != 1 {
		t.Errorf("%+v", v)
	}
	tk = m.Run("y", func(t *Task) error { t.Set("Composer 2.8 installed", 1); return nil })
	for i := 0; i < 100 && tk.View().Status == "running"; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if v := tk.View(); v.Message != "Composer 2.8 installed" {
		t.Errorf("%+v", v)
	}
}

func TestProgressWriterFinish(t *testing.T) {
	tk := &Task{}
	pw := &progressWriter{t: tk, label: "Downloading a", total: 4_000_000}
	pw.Write(make([]byte, 100)) // first write reports (last is zero)
	pw.Write(make([]byte, 3_900_000))
	pw.finish()
	if !strings.Contains(tk.View().Message, "3.9 MB") {
		t.Errorf("final update missing: %q", tk.View().Message)
	}
}

func TestDispatcherStats(t *testing.T) {
	d := &fcgiDispatcher{}
	d.busy.Add(2)
	d.queued.Add(1)
	if b, q := d.stats(); b != 2 || q != 1 {
		t.Fatal(b, q)
	}
}

// A worker that exits cleanly again and again (PHP_FCGI_MAX_REQUESTS) is
// restarted and never marked failed; the same loop for a service is.
func TestProcRecycleNeverFails(t *testing.T) {
	mk := func(recycle bool) *Proc {
		return NewProc(ProcSpec{Name: "w", LogPath: filepath.Join(t.TempDir(), "w.log"), RecycleOK: recycle,
			Build: func() (*exec.Cmd, error) { return exec.Command(os.Args[0], "-test.run=^$"), nil }})
	}
	w := mk(true)
	if err := w.Start(); err != nil {
		t.Skip("cannot start helper:", err)
	}
	time.Sleep(2500 * time.Millisecond)
	if st := w.Status(); st.State == "failed" {
		t.Fatalf("recycling worker marked failed: %+v", st)
	}
	_ = w.Stop()
}
