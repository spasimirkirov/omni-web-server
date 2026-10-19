package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const configFile = "omni.json"

// Config is the persisted state of the stack (omni.json in the root folder).
// Paths may be relative to the root folder so the whole stack stays portable.
type Config struct {
	DashboardPort int    `json:"dashboard_port"`
	HTTPPort      int    `json:"http_port"`
	TLD           string `json:"tld"`
	WWWDir        string `json:"www_dir"`
	// AutoVhost enables a vhost automatically for every new folder in www.
	AutoVhost   bool `json:"auto_vhost"`
	ManageHosts bool `json:"manage_hosts"`
	// WildcardDNS answers *.TLD with 127.0.0.1 from a built-in DNS server.
	WildcardDNS bool `json:"wildcard_dns"`
	DNSPort     int  `json:"dns_port"`
	// SSL serves every enabled site over HTTPS with a locally trusted certificate.
	SSL       bool   `json:"ssl"`
	HTTPSPort int    `json:"https_port"`
	HostsFile string `json:"hosts_file,omitempty"`
	// Autostart starts Apache/PHP/MySQL when the dashboard starts.
	Autostart   bool `json:"autostart"`
	OpenBrowser bool `json:"open_browser"`
	// SkipUpdateCheck turns off the daily check for a newer release.
	SkipUpdateCheck bool `json:"skip_update_check,omitempty"`
	// AddToPath puts bin/shims (php, composer, node, mysql, ...) on the user PATH.
	AddToPath  bool   `json:"add_to_path"`
	DefaultPHP string `json:"default_php"`

	PHP    map[string]*PHPInstall `json:"php"`
	Apache ApacheConfig           `json:"apache"`
	MySQL  MySQLConfig            `json:"mysql"`
	Sites  map[string]*SiteConfig `json:"sites"`

	// PendingInstalls are components chosen in the installer, downloaded on first start.
	PendingInstalls []string `json:"pending_installs,omitempty"`

	Mailpit MailpitConfig `json:"mailpit"`
	Redis   RedisConfig   `json:"redis"`
	Node    NodeConfig    `json:"node"`
}

// MailpitConfig is the local SMTP server that catches all outgoing mail, with a web inbox.
type MailpitConfig struct {
	Dir      string `json:"dir,omitempty"`
	Bin      string `json:"bin,omitempty"`
	SMTPPort int    `json:"smtp_port"`
	UIPort   int    `json:"ui_port"`
}

type RedisConfig struct {
	Dir  string `json:"dir,omitempty"`
	Bin  string `json:"bin,omitempty"`
	Port int    `json:"port"`
}

// NodeConfig maps installed Node.js versions ("22.11.0") to their folders.
type NodeConfig struct {
	Versions map[string]string `json:"versions,omitempty"`
	Default  string            `json:"default,omitempty"`
}

// PHPInstall is one PHP version, keyed by "major.minor" in Config.PHP.
type PHPInstall struct {
	Dir     string `json:"dir"`
	CGI     string `json:"cgi,omitempty"`
	CLI     string `json:"cli,omitempty"`
	Version string `json:"version"`
	Workers int    `json:"workers"`
	// Extensions lists the shared extensions that are switched on.
	Extensions []string `json:"extensions"`
	// Builtin lists extensions compiled into the binary (cannot be toggled).
	Builtin []string          `json:"builtin,omitempty"`
	ExtDir  string            `json:"ext_dir,omitempty"`
	BaseINI string            `json:"base_ini,omitempty"`
	INI     map[string]string `json:"ini"`
}

type ApacheConfig struct {
	Dir        string `json:"dir,omitempty"`
	Bin        string `json:"bin,omitempty"`
	ModulesDir string `json:"modules_dir,omitempty"`
	MimeTypes  string `json:"mime_types,omitempty"`
}

type MySQLConfig struct {
	Dir          string `json:"dir,omitempty"`
	Bin          string `json:"bin,omitempty"`
	Flavor       string `json:"flavor,omitempty"` // mysql | mariadb
	Port         int    `json:"port"`
	RootPassword string `json:"root_password"`
	DataDir      string `json:"data_dir,omitempty"`
}

// SiteConfig holds the per-project settings. Projects live in www/<name>
// unless Path points somewhere else.
type SiteConfig struct {
	Enabled bool   `json:"enabled"`
	PHP     string `json:"php,omitempty"`
	DocRoot string `json:"docroot,omitempty"`
	Path    string `json:"path,omitempty"`
}

func defaultConfig() *Config {
	return &Config{
		DashboardPort: 8800,
		HTTPPort:      80,
		TLD:           "local",
		WWWDir:        "www",
		AutoVhost:     true,
		ManageHosts:   true,
		Autostart:     true,
		OpenBrowser:   true,
		AddToPath:     true,
		PHP:           map[string]*PHPInstall{},
		Sites:         map[string]*SiteConfig{},
		MySQL:         MySQLConfig{Port: 3306},
		DNSPort:       53,
		HTTPSPort:     443,
	}
}

func loadConfig(root string) (cfg *Config, fresh bool, err error) {
	cfg = defaultConfig()
	data, err := os.ReadFile(filepath.Join(root, configFile))
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	// Notepad and PowerShell 5 save UTF-8 with a byte order mark.
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, false, fmt.Errorf("%s: %w", configFile, err)
	}
	cfg.normalize()
	return cfg, false, nil
}

func (c *Config) normalize() {
	if c.PHP == nil {
		c.PHP = map[string]*PHPInstall{}
	}
	if c.Sites == nil {
		c.Sites = map[string]*SiteConfig{}
	}
	for k, p := range c.PHP {
		if p == nil {
			delete(c.PHP, k)
			continue
		}
		if p.Workers <= 0 {
			p.Workers = 4
		}
		if p.Workers > 9 {
			p.Workers = 9
		}
		if p.INI == nil {
			p.INI = map[string]string{}
		}
	}
	for k, s := range c.Sites {
		if s == nil {
			delete(c.Sites, k)
		}
	}
	if c.MySQL.Port == 0 {
		c.MySQL.Port = 3306
	}
	if c.HTTPPort == 0 {
		c.HTTPPort = 80
	}
	if c.Mailpit.SMTPPort == 0 {
		c.Mailpit.SMTPPort = 1025
	}
	if c.Mailpit.UIPort == 0 {
		c.Mailpit.UIPort = 8025
	}
	if c.Redis.Port == 0 {
		c.Redis.Port = 6379
	}
	if c.Node.Versions == nil {
		c.Node.Versions = map[string]string{}
	}
	if _, ok := c.Node.Versions[c.Node.Default]; !ok {
		c.Node.Default = ""
		for v := range c.Node.Versions {
			if c.Node.Default == "" || compareVersions(v, c.Node.Default) > 0 {
				c.Node.Default = v
			}
		}
	}
	if c.HTTPSPort == 0 {
		c.HTTPSPort = 443
	}
	if c.DNSPort == 0 {
		c.DNSPort = 53
	}
	if c.DashboardPort == 0 {
		c.DashboardPort = 8800
	}
	if c.TLD == "" {
		c.TLD = "local"
	}
	c.TLD = strings.Trim(strings.ToLower(c.TLD), ".")
	if c.WWWDir == "" {
		c.WWWDir = "www"
	}
	if _, ok := c.PHP[c.DefaultPHP]; !ok {
		c.DefaultPHP = ""
		if vs := c.phpVersions(); len(vs) > 0 {
			c.DefaultPHP = vs[len(vs)-1]
		}
	}
}

func saveConfig(root string, c *Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(root, configFile), append(data, '\n'))
}

func (c *Config) clone() *Config {
	data, _ := json.Marshal(c)
	out := defaultConfig()
	_ = json.Unmarshal(data, out)
	out.normalize()
	return out
}

// phpVersions returns installed version keys sorted ascending ("7.4" < "8.3").
func (c *Config) phpVersions() []string {
	vs := make([]string, 0, len(c.PHP))
	for k := range c.PHP {
		vs = append(vs, k)
	}
	sort.Slice(vs, func(i, j int) bool { return compareVersions(vs[i], vs[j]) < 0 })
	return vs
}

func compareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// minorOf turns "8.3.12" into "8.3".
func minorOf(v string) string {
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return v
	}
	return parts[0] + "." + parts[1]
}

// phpPortBase gives each PHP version its own block of FastCGI ports: 8.3 -> 9830
// (dispatcher) and 9831.. (workers).
func phpPortBase(minor string) int {
	parts := strings.Split(minor, ".")
	if len(parts) < 2 {
		return 9900
	}
	maj, _ := strconv.Atoi(parts[0])
	min, _ := strconv.Atoi(parts[1])
	return 9000 + (maj*10+min)*10
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// writeIfChanged writes the file only when its content differs, and reports whether it did.
func writeIfChanged(path string, data []byte) (bool, error) {
	old, err := os.ReadFile(path)
	if err == nil && string(old) == string(data) {
		return false, nil
	}
	return true, writeFileAtomic(path, data)
}
