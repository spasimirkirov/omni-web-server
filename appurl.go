package main

import (
	"bufio"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The site URL a project keeps in its dotenv file. Laravel and most frameworks
// use APP_URL; the others below are the same idea under another name.
var envURLKeys = []string{
	"APP_URL",          // Laravel, Lumen, many others
	"DEFAULT_URI",      // Symfony 7.x (router default URI)
	"PRIMARY_SITE_URL", // Craft CMS
	"WP_HOME",          // WordPress Bedrock
	"WP_SITEURL",       // WordPress Bedrock (keeps its /wp path)
	"app.baseURL",      // CodeIgniter 4 (needs a trailing slash)
}

// envURL is one URL setting found in a project's dotenv file.
type envURL struct {
	File  string `json:"file"` // relative to the project, e.g. ".env"
	Key   string `json:"key"`
	Value string `json:"value"`
	Want  string `json:"want"` // what it should be; "" when it is fine
}

// envFilesFor lists the dotenv files to read, the one that wins first.
// Symfony keeps .env in git and local overrides in .env.local.
func envFilesFor(framework string) []string {
	if framework == "symfony" {
		return []string{".env.local", ".env"}
	}
	return []string{".env"}
}

// readEnvKeys returns the wanted keys that are set (not commented out) in a
// dotenv file, with quotes removed.
func readEnvKeys(path string, keys []string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		l = strings.TrimSpace(strings.TrimPrefix(l, "export "))
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		if !want[k] {
			continue
		}
		out[k] = envUnquote(strings.TrimSpace(v)) // last one wins, like dotenv
	}
	return out
}

func envUnquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') {
		if i := strings.IndexByte(v[1:], v[0]); i >= 0 {
			return v[1 : 1+i]
		}
	}
	if i := strings.Index(v, " #"); i >= 0 { // inline comment
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// siteBaseURL is the address Omni serves the site on, without a trailing slash.
func siteBaseURL(cfg *Config, host string) string {
	if cfg.SSL {
		if cfg.HTTPSPort != 0 && cfg.HTTPSPort != 443 {
			return "https://" + host + ":" + strconv.Itoa(cfg.HTTPSPort)
		}
		return "https://" + host
	}
	if cfg.HTTPPort != 0 && cfg.HTTPPort != 80 {
		return "http://" + host + ":" + strconv.Itoa(cfg.HTTPPort)
	}
	return "http://" + host
}

// localDevSuffixes are domain endings that only exist on a developer machine.
var localDevSuffixes = []string{"test", "local", "localhost", "lan", "internal", "home.arpa", "example", "invalid", "wip", "dev.local"}

// isLocalHost reports whether a host name points at this machine or a local
// development domain, as opposed to a real site on the internet.
func isLocalHost(h, tld string) bool {
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	if h == "" || h == "localhost" || !strings.Contains(h, ".") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback() || ip.IsUnspecified() || ip.IsPrivate()
	}
	for _, s := range append([]string{tld}, localDevSuffixes...) {
		if s != "" && (h == s || strings.HasSuffix(h, "."+s)) {
			return true
		}
	}
	return false
}

func defaultPort(scheme string) string {
	if scheme == "https" {
		return "443"
	}
	return "80"
}

// wantedEnvURL returns the value the key should have, or "" when the current
// one is right, or points at a real (non-local) domain, or cannot be judged.
func wantedEnvURL(key, value, base, tld string) string {
	if value == "" || strings.Contains(value, "${") {
		return "" // empty or built from another variable (Bedrock: ${WP_HOME}/wp)
	}
	cur, err := url.Parse(value)
	if err != nil || cur.Host == "" {
		cur, err = url.Parse("http://" + value) // "myapp.test" without a scheme
		if err != nil || cur.Host == "" {
			return ""
		}
	}
	exp, _ := url.Parse(base)
	if !isLocalHost(cur.Hostname(), tld) {
		return ""
	}
	path := ""
	if key == "WP_SITEURL" {
		path = strings.TrimSuffix(cur.Path, "/")
	}
	want := base + path
	if key == "app.baseURL" {
		want += "/"
	}

	curPort, expPort := cur.Port(), exp.Port()
	if curPort == "" {
		curPort = defaultPort(cur.Scheme)
	}
	if expPort == "" {
		expPort = defaultPort(exp.Scheme)
	}
	ch, eh := strings.ToLower(cur.Hostname()), strings.ToLower(exp.Hostname())
	sameHost := ch == eh || strings.HasSuffix(ch, "."+eh) // a subdomain of the site is fine (wildcard DNS)
	if sameHost && strings.EqualFold(cur.Scheme, exp.Scheme) && curPort == expPort {
		return ""
	}
	return want
}

// checkEnvURLs reads the project's dotenv files and reports every URL setting,
// with Want filled in for the ones that do not match the site's local address.
func checkEnvURLs(cfg *Config, s Site) []envURL {
	if s.Missing {
		return nil
	}
	base := siteBaseURL(cfg, s.Host)
	seen := map[string]bool{}
	var out []envURL
	for _, f := range envFilesFor(s.Framework) {
		vals := readEnvKeys(filepath.Join(s.Path, f), envURLKeys)
		for _, k := range envURLKeys {
			v, ok := vals[k]
			if !ok || seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, envURL{File: f, Key: k, Value: v, Want: wantedEnvURL(k, v, base, cfg.TLD)})
		}
	}
	return out
}

// envURLMismatches keeps only the settings that need fixing.
func envURLMismatches(list []envURL) []envURL {
	var out []envURL
	for _, u := range list {
		if u.Want != "" {
			out = append(out, u)
		}
	}
	return out
}

// fixEnvURLs rewrites the wrong URL settings of the given sites (all sites
// with a mismatch when names is empty) and returns a line per change.
func (a *App) fixEnvURLs(names []string) ([]string, error) {
	cfg := a.config()
	a.mu.Lock()
	sites := append([]Site(nil), a.sites...)
	a.mu.Unlock()

	only := map[string]bool{}
	for _, n := range names {
		only[n] = true
	}
	var done []string
	var errs []string
	for _, s := range sites {
		if len(only) > 0 && !only[s.Name] {
			continue
		}
		bad := envURLMismatches(checkEnvURLs(cfg, s))
		if len(bad) == 0 {
			continue
		}
		byFile := map[string]map[string]string{}
		for _, u := range bad {
			if byFile[u.File] == nil {
				byFile[u.File] = map[string]string{}
			}
			byFile[u.File][u.Key] = envQuote(u.Want)
		}
		ok := true
		for f, vals := range byFile {
			if err := setEnvValues(filepath.Join(s.Path, f), vals); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", s.Name, err))
				ok = false
			}
		}
		if !ok {
			continue
		}
		for _, u := range bad {
			done = append(done, fmt.Sprintf("%s: %s %s -> %s", s.Name, u.Key, u.Value, u.Want))
		}
		// A cached Laravel config ignores .env: drop it, as "php artisan config:clear" does.
		if s.Framework == "laravel" {
			_ = os.Remove(filepath.Join(s.Path, "bootstrap", "cache", "config.php"))
		}
	}
	for _, l := range done {
		a.log.Printf("APP_URL fixed: %s", l)
	}
	_ = a.Apply(false) // refresh the warnings
	if len(errs) > 0 {
		return done, fmt.Errorf("could not update %s", strings.Join(errs, "; "))
	}
	return done, nil
}
