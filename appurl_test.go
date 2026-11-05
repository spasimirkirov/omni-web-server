package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWantedEnvURL(t *testing.T) {
	base := "https://shop.test"
	cases := []struct {
		key, value, want string
	}{
		{"APP_URL", "https://shop.test", ""},
		{"APP_URL", "https://shop.test/", ""},
		{"APP_URL", "https://admin.shop.test", ""}, // subdomain of the site (wildcard)
		{"APP_URL", "http://localhost", "https://shop.test"},
		{"APP_URL", "http://shop.test", "https://shop.test"}, // wrong scheme
		{"APP_URL", "https://shop.test:8443", "https://shop.test"},
		{"APP_URL", "http://old-name.local", "https://shop.test"},
		{"APP_URL", "http://127.0.0.1:8000", "https://shop.test"},
		{"APP_URL", "http://localhost/shop/public", "https://shop.test"},
		{"APP_URL", "https://shop.example.com", ""}, // a real domain: left alone
		{"APP_URL", "", ""},
		{"WP_SITEURL", "${WP_HOME}/wp", ""},
		{"WP_SITEURL", "http://localhost/wp", "https://shop.test/wp"},
		{"app.baseURL", "http://localhost:8080/", "https://shop.test/"},
		{"app.baseURL", "https://shop.test/", ""},
	}
	for _, c := range cases {
		if got := wantedEnvURL(c.key, c.value, base, "test"); got != c.want {
			t.Errorf("%s=%q: got %q, want %q", c.key, c.value, got, c.want)
		}
	}
}

func TestCheckAndFixEnvURLs(t *testing.T) {
	dir := t.TempDir()
	env := "APP_NAME=Shop\nAPP_URL='http://localhost'\n# APP_URL=http://old.test\nDB_HOST=127.0.0.1\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{HTTPPort: 8080, TLD: "test"}
	s := Site{Name: "shop", Host: "shop.test", Path: dir, Framework: "laravel"}
	bad := envURLMismatches(checkEnvURLs(cfg, s))
	if len(bad) != 1 || bad[0].Value != "http://localhost" || bad[0].Want != "http://shop.test:8080" {
		t.Fatalf("mismatch = %+v", bad)
	}
	if err := setEnvValues(filepath.Join(dir, ".env"), map[string]string{"APP_URL": envQuote(bad[0].Want)}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".env"))
	if !strings.Contains(string(b), "APP_URL=http://shop.test:8080") || !strings.Contains(string(b), "DB_HOST=127.0.0.1") {
		t.Fatalf(".env after fix:\n%s", b)
	}
	if bad := envURLMismatches(checkEnvURLs(cfg, s)); len(bad) != 0 {
		t.Fatalf("still mismatched after fix: %+v", bad)
	}
}
