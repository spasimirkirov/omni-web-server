package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/x509"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHostLabel(t *testing.T) {
	cases := map[string]string{"blog": "blog", "My_Site": "my-site", "shop 2.0": "shop-2-0", "--x--": "x", "Ünï": "n"}
	for in, want := range cases {
		if got := hostLabel(in); got != want {
			t.Errorf("hostLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDetectProject(t *testing.T) {
	mk := func(files ...string) string {
		dir := t.TempDir()
		for _, f := range files {
			p := filepath.Join(dir, filepath.FromSlash(f))
			if strings.HasSuffix(f, "/") {
				os.MkdirAll(p, 0o755)
				continue
			}
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, []byte("x"), 0o644)
		}
		return dir
	}
	cases := []struct {
		files       []string
		fw, docroot string
	}{
		{[]string{"artisan", "public/index.php"}, "laravel", "public"},
		{[]string{"public/", "composer.json"}, "php", ""},
		{[]string{"bin/console", "public/index.php"}, "symfony", "public"},
		{[]string{"spark", "public/index.php"}, "codeigniter", "public"},
		{[]string{"wp-config.php", "index.php"}, "wordpress", ""},
		{[]string{"index.html"}, "static", ""},
		{[]string{"public/index.php"}, "php", "public"},
		{[]string{"index.php"}, "php", ""},
	}
	for _, c := range cases {
		fw, dr := detectProject(mk(c.files...))
		if fw != c.fw || dr != c.docroot {
			t.Errorf("%v: got (%s,%s) want (%s,%s)", c.files, fw, dr, c.fw, c.docroot)
		}
	}
	// composer.json with laravel/framework counts even without artisan
	dir := mk("public/")
	os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"require":{"laravel/framework":"^11.0"}}`), 0o644)
	if fw, dr := detectProject(dir); fw != "laravel" || dr != "public" {
		t.Errorf("composer laravel: got %s %s", fw, dr)
	}
}

func TestRenderHosts(t *testing.T) {
	orig := "127.0.0.1 localhost\r\n# my line\r\n"
	out := renderHosts(orig, []string{"blog.local"})
	if !strings.Contains(out, "127.0.0.1\tblog.local\r\n") || !strings.HasPrefix(out, "127.0.0.1 localhost\r\n# my line\r\n") {
		t.Fatalf("unexpected hosts:\n%q", out)
	}
	// idempotent and replaces the block
	if again := renderHosts(out, []string{"blog.local"}); again != out {
		t.Fatalf("not idempotent:\n%q\n%q", out, again)
	}
	out2 := renderHosts(out, []string{"shop.local"})
	if strings.Contains(out2, "blog.local") || !strings.Contains(out2, "shop.local") {
		t.Fatalf("block not replaced:\n%q", out2)
	}
	// removing all entries restores the original content
	if back := renderHosts(out2, nil); back != orig {
		t.Fatalf("not restored:\n%q\n%q", orig, back)
	}
}

func TestWithDependencies(t *testing.T) {
	got := withDependencies([]string{"pdo_mysql", "mbstring", "nope"}, []string{"mbstring", "mysqlnd", "pdo", "pdo_mysql"})
	want := "mysqlnd,pdo,mbstring,pdo_mysql"
	if strings.Join(got, ",") != want {
		t.Fatalf("got %v want %s", got, want)
	}
}

func TestVersions(t *testing.T) {
	if compareVersions("8.10", "8.9") <= 0 || compareVersions("7.4", "8.0") >= 0 || compareVersions("8.3", "8.3") != 0 {
		t.Fatal("compareVersions")
	}
	if minorOf("8.3.12") != "8.3" || phpPortBase("8.3") != 9830 || phpPortBase("7.4") != 9740 {
		t.Fatal("minor/ports")
	}
	if p := phpPorts("8.4", 3); p[0] != 9841 || p[2] != 9843 {
		t.Fatalf("phpPorts %v", p)
	}
	if u := phpArchiveURL("7.4.33"); !strings.HasSuffix(u, "archives/php-7.4.33-nts-Win32-vc15-x64.zip") {
		t.Fatal(u)
	}
	if u := phpArchiveURL("8.2.20"); !strings.HasSuffix(u, "php-8.2.20-nts-Win32-vs16-x64.zip") {
		t.Fatal(u)
	}
}

func TestRenderApacheConf(t *testing.T) {
	conf := renderApacheConf(apacheInput{
		ServerRoot: "C:/Omni Web Server/bin/apache/httpd", WWW: "C:/Omni Web Server/www", Logs: "C:/Omni Web Server/logs", Tmp: "C:/Omni Web Server/tmp",
		CustomDir: "C:/Omni Web Server/etc/apache/custom", Port: 80, Windows: true, Default: "127.0.0.1:9830",
		Modules: []apacheModule{{"rewrite", "C:/x/mod_rewrite.so"}},
		Vhosts:  []apacheVhost{{Name: "blog", Host: "blog.local", DocRoot: "C:/Omni Web Server/www/blog/public", Pool: "127.0.0.1:9740"}},
	})
	for _, want := range []string{
		"Listen 80",
		`LoadModule rewrite_module "C:/x/mod_rewrite.so"`,
		"ServerName blog.local",
		"ServerAlias *.blog.local",
		`DocumentRoot "C:/Omni Web Server/www/blog/public"`,
		`SetHandler "proxy:fcgi://127.0.0.1:9740/"`,
		`SetHandler "proxy:fcgi://127.0.0.1:9830/"`,
		"AllowOverride All",
		"ProxyFCGIBackendType GENERIC",
		`ProxyFCGISetEnvIf "reqenv('SCRIPT_FILENAME') =~ m#^/([A-Za-z]:/.*)$#" SCRIPT_FILENAME "$1"`,
		"AcceptFilter http none",
		`IncludeOptional "C:/Omni Web Server/etc/apache/custom/*.conf"`,
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("config missing %q", want)
		}
	}
}

func TestExtractZipRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "evil.zip")
	f, _ := os.Create(zp)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("../evil.txt")
	w.Write([]byte("x"))
	zw.Close()
	f.Close()
	err := extractZip(&Task{}, zp, filepath.Join(dir, "out"))
	if err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("expected unsafe path error, got %v", err)
	}
}

func TestFindDirWith(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "Apache24", "bin"), 0o755)
	os.WriteFile(filepath.Join(dir, "Apache24", "bin", "httpd.exe"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "ReadMe.txt"), nil, 0o644)
	got, err := findDirWith(dir, "bin/httpd.exe")
	if err != nil || got != filepath.Join(dir, "Apache24") {
		t.Fatalf("got %s %v", got, err)
	}
}

// The dispatcher must give each connection its own backend and queue the rest.
func TestDispatcherQueuesToIdleWorker(t *testing.T) {
	var ports []int
	for i := 0; i < 2; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		ports = append(ports, ln.Addr().(*net.TCPAddr).Port)
		id := byte('A' + i)
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				// one request at a time, like php-cgi on Windows
				buf := make([]byte, 1)
				c.Read(buf)
				time.Sleep(150 * time.Millisecond)
				c.Write([]byte{id})
				c.Close()
			}
		}()
	}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	front := l.Addr().(*net.TCPAddr).Port
	l.Close()
	d, err := startDispatcher(front, ports, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	start := time.Now()
	results := make(chan string, 4)
	for i := 0; i < 4; i++ {
		go func() {
			c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(front)))
			if err != nil {
				results <- "err"
				return
			}
			c.Write([]byte{'x'})
			b, _ := io.ReadAll(c)
			c.Close()
			results <- string(b)
		}()
	}
	seen := map[string]int{}
	for i := 0; i < 4; i++ {
		seen[<-results]++
	}
	elapsed := time.Since(start)
	if seen["A"] != 2 || seen["B"] != 2 {
		t.Fatalf("uneven distribution: %v", seen)
	}
	if elapsed < 280*time.Millisecond || elapsed > 1200*time.Millisecond {
		t.Fatalf("expected two rounds of 150ms, took %s", elapsed)
	}
}

func TestScanSites(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "www", "blog", "public"), 0o755)
	os.WriteFile(filepath.Join(root, "www", "blog", "artisan"), nil, 0o644)
	os.MkdirAll(filepath.Join(root, "www", ".hidden"), 0o755)
	os.WriteFile(filepath.Join(root, "www", "file.txt"), nil, 0o644)
	cfg := defaultConfig()
	cfg.PHP["8.3"] = &PHPInstall{Version: "8.3.1", Workers: 4}
	cfg.DefaultPHP = "8.3"
	cfg.Sites["gone"] = &SiteConfig{Enabled: true}
	a := &App{root: root, cfg: cfg}
	sites, changed := a.scanSitesLocked()
	if !changed || len(sites) != 1 {
		t.Fatalf("changed=%v sites=%+v", changed, sites)
	}
	s := sites[0]
	if s.Host != "blog.local" || !s.Enabled || s.Framework != "laravel" || s.DocRoot != filepath.Join(root, "www", "blog", "public") || s.PHP != "8.3" {
		t.Fatalf("unexpected site %+v", s)
	}
	cfg.AutoVhost = false
	os.MkdirAll(filepath.Join(root, "www", "new"), 0o755)
	sites, _ = a.scanSitesLocked()
	for _, s := range sites {
		if s.Name == "new" && s.Enabled {
			t.Fatal("auto vhost off should add new sites disabled")
		}
	}
	cfg.Sites["blog"].PHP = "5.6"
	sites, _ = a.scanSitesLocked()
	if sites[0].PHP != "8.3" || sites[0].Warning == "" {
		t.Fatalf("missing PHP version should fall back with a warning: %+v", sites[0])
	}
}

func TestWildcardDNS(t *testing.T) {
	l, _ := net.ListenPacket("udp", "127.0.0.1:0")
	port := l.LocalAddr().(*net.UDPAddr).Port
	l.Close()
	srv, err := startDNS(port, "local", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	for _, network := range []string{"udp", "tcp"} {
		r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, fmt.Sprintf("127.0.0.1:%d", port))
		}}
		ctx := context.Background()
		for _, host := range []string{"blog.local", "api.blog.local", "a.b.c.shop.LOCAL", "local"} {
			ips, err := r.LookupIPAddr(ctx, host)
			if err != nil {
				t.Fatalf("%s %s: %v", network, host, err)
			}
			var got []string
			for _, ip := range ips {
				got = append(got, ip.String())
			}
			sort.Strings(got)
			if strings.Join(got, ",") != "127.0.0.1,::1" {
				t.Fatalf("%s %s: got %v", network, host, got)
			}
		}
		if _, err := r.LookupIPAddr(ctx, "example.com"); err == nil {
			t.Fatalf("%s: names outside the TLD must not resolve", network)
		}
		if _, err := r.LookupIPAddr(ctx, "notlocal"); err == nil {
			t.Fatalf("%s: suffix match must be on a label boundary", network)
		}
	}
}

func TestDNSMalformed(t *testing.T) {
	s := &dnsServer{tld: "local"}
	for _, q := range [][]byte{nil, {1, 2, 3}, append([]byte{0, 1, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0}, 0x40)} {
		if resp := s.answer(q); len(resp) >= 4 && resp[3]&0x0f == 0 {
			t.Fatalf("malformed query %v answered without error", q)
		}
	}
	// responses (QR set) are ignored
	if s.answer([]byte{0, 1, 0x80, 0, 0, 1, 0, 0, 0, 0, 0, 0}) != nil {
		t.Fatal("answered a response packet")
	}
}

func TestSiteCertificates(t *testing.T) {
	root := t.TempDir()
	a := &App{root: root, log: log.New(io.Discard, "", 0), versions: map[string]string{}}
	sites := []Site{{Name: "blog", Host: "blog.local", Enabled: true}, {Name: "off", Host: "off.local"}}
	certs, err := a.prepareSSL(defaultConfig(), sites)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := certs["off.local"]; ok {
		t.Fatal("disabled sites must not get certificates")
	}
	caDER, _ := readPEM(a.caCertPath())
	ca, _ := x509.ParseCertificate(caDER)
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	der, _ := readPEM(certs["blog.local"][0])
	leaf, _ := x509.ParseCertificate(der)
	for _, name := range []string{"blog.local", "api.blog.local"} {
		if _, err := leaf.Verify(x509.VerifyOptions{DNSName: name, Roots: pool}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: "shop.local", Roots: pool}); err == nil {
		t.Fatal("certificate must not cover other sites")
	}
	// A second run reuses the CA and the certificate.
	certs2, _ := a.prepareSSL(defaultConfig(), sites)
	der2, _ := readPEM(certs2["blog.local"][0])
	if !bytes.Equal(der, der2) {
		t.Fatal("certificate was reissued without need")
	}
	if b, _ := os.ReadFile(a.caBundlePath()); !strings.Contains(string(b), "BEGIN CERTIFICATE") {
		t.Fatal("CA bundle for PHP not written")
	}
	conf := renderApacheConf(apacheInput{Port: 80, HTTPSPort: 443, Tmp: "/t", LocalCert: certs["localhost"],
		Vhosts: []apacheVhost{{Name: "blog", Host: "blog.local", DocRoot: "/w/blog/public", Cert: "/c.crt", Key: "/c.key"}}})
	for _, want := range []string{"Listen 443", "<VirtualHost *:443>", `SSLCertificateFile "/c.crt"`, "SSLEngine on", "SSLSessionCache"} {
		if !strings.Contains(conf, want) {
			t.Errorf("config missing %q", want)
		}
	}
}

func TestPickNode(t *testing.T) {
	rels := []nodeRelease{
		{Version: "v26.1.0", LTS: []byte("false"), Files: []string{"win-x64-zip"}},
		{Version: "v24.22.0", LTS: []byte(`"Krypton"`), Files: []string{"win-x64-zip"}},
		{Version: "v22.23.3", LTS: []byte(`"Jod"`), Files: []string{"win-x64-zip"}},
		{Version: "v22.23.2", LTS: []byte(`"Jod"`), Files: []string{"linux-x64"}},
	}
	cases := map[string]string{"": "24.22.0", "lts": "24.22.0", "22": "22.23.3", "26": "26.1.0", "v22.23.3": "22.23.3"}
	for in, want := range cases {
		if got, err := pickNode(rels, in); err != nil || got != want {
			t.Errorf("pickNode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := pickNode(rels, "18"); err == nil {
		t.Error("missing version must fail")
	}
}

func TestXdebugAndTunnelPatterns(t *testing.T) {
	page := `<a href="/files/php_xdebug-3.4.5-8.3-nts-vs16-x86_64.dll">a</a>
<a href="/files/php_xdebug-3.4.5-8.3-vs16-x86_64.dll">ts</a>
<a href="/files/php_xdebug-3.3.2-8.3-vs16-nts-x86_64.dll">old</a>
<a href="/files/php_xdebug-3.5.0alpha1-8.3-nts-vs16-x86_64.dll">alpha</a>
<a href="/files/php_xdebug-3.4.5-8.4-nts-vs17-x86_64.dll">84</a>`
	re := regexp.MustCompile(`(/files/php_xdebug-(\d+\.\d+\.\d+)-` + regexp.QuoteMeta("8.3") + `-[a-z0-9-]*nts[a-z0-9-]*-x86_64\.dll)`)
	var got []string
	for _, m := range re.FindAllStringSubmatch(page, -1) {
		got = append(got, m[2])
	}
	if strings.Join(got, ",") != "3.4.5,3.3.2" {
		t.Fatalf("xdebug matches: %v", got)
	}
	log := "2026-10-19T10:00:00Z INF |  https://quiet-river-1234.trycloudflare.com  |"
	if u := tunnelURLRe.FindString(log); u != "https://quiet-river-1234.trycloudflare.com" {
		t.Fatalf("tunnel url: %q", u)
	}
}

func TestShimsAndPendingOrder(t *testing.T) {
	root := t.TempDir()
	phpDir := filepath.Join(root, "bin", "php", "php-8.3.1")
	os.MkdirAll(phpDir, 0o755)
	os.WriteFile(filepath.Join(phpDir, exeName("php")), nil, 0o755)
	os.MkdirAll(filepath.Join(root, "bin", "composer"), 0o755)
	os.WriteFile(filepath.Join(root, "bin", "composer", "composer.phar"), nil, 0o644)
	a := &App{root: root}
	cfg := defaultConfig()
	cfg.PHP["8.3"] = &PHPInstall{Dir: "bin/php/php-8.3.1", Workers: 4}
	cfg.DefaultPHP = "8.3"
	os.MkdirAll(filepath.Join(root, "bin", "shims"), 0o755)
	os.WriteFile(filepath.Join(root, "bin", "shims", "stale"), nil, 0o755)
	a.writeShims(cfg)
	entries, _ := os.ReadDir(filepath.Join(root, "bin", "shims"))
	var names []string
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), ".cmd"))
	}
	if strings.Join(names, ",") != "composer,php,php83" {
		t.Fatalf("shims: %v", names)
	}
	vals := a.registryValues(cfg)
	if vals[`PHP\8.3`]["Path"] == "" || vals[""]["ComposerPhar"] == "" || vals[""]["WWWDir"] != filepath.Join(root, "www") {
		t.Fatalf("registry values: %v", vals)
	}
	items := []string{"tool:git", "composer", "redis", "php", "apache", "mysql"}
	sort.SliceStable(items, func(i, j int) bool { return pendingRank(items[i]) < pendingRank(items[j]) })
	if strings.Join(items, ",") != "apache,php,mysql,composer,redis,tool:git" {
		t.Fatalf("pending order: %v", items)
	}
}

func TestRedisPathMSYS(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin", "redis", "redis-8")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, exeName("redis-server")), nil, 0o755)
	a := &App{root: root}
	cfg := defaultConfig()
	cfg.Redis.Dir = "bin/redis/redis-8"
	if got := a.redisPath(cfg, `C:\x\redis.conf`); got != "C:/x/redis.conf" {
		t.Fatalf("native build: %q", got)
	}
	os.WriteFile(filepath.Join(bin, "msys-2.0.dll"), nil, 0o644)
	if got := a.redisPath(cfg, `C:\x\redis.conf`); got != "/cygdrive/c/x/redis.conf" {
		t.Fatalf("MSYS build: %q", got)
	}
}

func TestProcQueue(t *testing.T) {
	p := NewProc(ProcSpec{Name: "x"})
	p.queue()
	if st := p.Status().State; st != "starting" {
		t.Fatalf("queued: %s", st)
	}
	p.unqueue()
	if st := p.Status().State; st != "stopped" {
		t.Fatalf("unqueued: %s", st)
	}
}

func TestNewestRelease(t *testing.T) {
	rels := []ghRelease{
		{Tag: "v1.1.0", Prerelease: true},
		{Tag: "v1.10.0-beta", Prerelease: true},
		{Tag: "v1.2.0"},
		{Tag: "v9.9.9", Draft: true},
		{Tag: "nightly"},
	}
	r, v, ok := newestRelease(rels)
	if !ok || r.Tag != "v1.10.0-beta" || v != [3]int{1, 10, 0} {
		t.Fatalf("newest = %q %v %v", r.Tag, v, ok)
	}
	cur, _ := parseVersion("1.1.0")
	if !versionNewer(v, cur) || versionNewer(cur, cur) {
		t.Fatal("versionNewer")
	}
	if _, ok := parseVersion("1.2"); ok {
		t.Fatal("1.2 parsed")
	}
}

func TestPHPSatisfies(t *testing.T) {
	cases := []struct {
		c, php string
		want   bool
	}{
		{"^8.2", "8.3", true}, {"^8.2", "8.1", false}, {"^8.3", "8.3", true},
		{">=8.4", "8.3", false}, {">=8.4", "8.5", true}, {"^7.3|^8.0", "8.2", true},
		{"^7.3|^8.0", "7.2", false}, {">=7.4", "8.5", true}, {"", "8.0", true},
	}
	for _, c := range cases {
		if got := phpSatisfies(c.c, c.php); got != c.want {
			t.Errorf("phpSatisfies(%q, %q) = %v", c.c, c.php, got)
		}
	}
}

func TestSetEnvValues(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(p, []byte("APP_NAME=Laravel\nDB_CONNECTION=sqlite\n# DB_HOST=127.0.0.1\n# DB_DATABASE=laravel\n"), 0o644)
	if err := setEnvValues(p, map[string]string{"APP_NAME": envQuote("My Blog"), "DB_CONNECTION": "mysql", "DB_HOST": "127.0.0.1", "DB_DATABASE": "blog", "DB_PASSWORD": ""}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	want := "APP_NAME=\"My Blog\"\nDB_CONNECTION=mysql\nDB_HOST=127.0.0.1\nDB_DATABASE=blog\nDB_PASSWORD=\n"
	if string(b) != want {
		t.Fatalf("got %q", b)
	}
}

// TestRoutesRegister builds the dashboard's HTTP routes; a duplicate or
// malformed pattern panics here instead of when Omni Web Server starts.
func TestRoutesRegister(t *testing.T) {
	// Not t.TempDir: the app keeps logs/omni.log open, which Windows won't delete.
	root, err := os.MkdirTemp("", "omni-routes")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	a, err := NewApp(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if a.routes() == nil {
		t.Fatal("no handler")
	}
}
