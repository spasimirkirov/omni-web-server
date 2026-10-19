package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"
)

const usage = `DevStack %s - Apache + PHP + MySQL with automatic virtual hosts

Usage: devstack [command]

  serve [--no-browser]           run the dashboard and services in the foreground (default)
  start                          start DevStack in the background and all services
  stop                           stop Apache, PHP and MySQL
  restart [apache|mysql]         restart services
  quit                           stop services and close DevStack
  status                         show services and sites
  open                           open the dashboard in the browser

  sites                          list projects and their vhosts
  site enable|disable <name>     toggle the vhost of a project
  site php <name> <ver|default>  choose the PHP version of a project
  site root <name> <dir|auto>    override the document root (relative to the project)
  site add <name> <path> [ver]   add a vhost for a folder outside www
  site remove <name>             remove a vhost added with "site add"

  php                            list installed PHP versions
  php available                  list PHP versions that can be downloaded
  php install <ver>              download PHP (e.g. 8.4, or 7.4.33 from the archives)
  php default <ver>              set the default PHP version
  php add <folder|php-cgi>       register an existing PHP folder
  ext <ver>                      list extensions of a PHP version
  ext <ver> on|off <ext...>      enable or disable extensions
  ini <ver> <key>=<value>...     set php.ini values (empty value resets)

  install apache|mysql|mariadb|adminer [url]   download a component
  db                             list databases
  db create|drop <name>          create or drop a database
  hosts                          rewrite the hosts file (and DNS rule) now
  dns [on|off]                   wildcard DNS: every *.local name resolves to this PC
  ssl [on|off|trust]             HTTPS for every site with a locally trusted certificate
  logs <name> [lines]            print a log (apache_error, mysql_error, php83_error, ...)
`

func runCLI(root string, args []string) int {
	cmd := args[0]
	rest := args[1:]
	var err error
	switch cmd {
	case "serve", "run":
		err = cmdServe(root, !hasFlag(rest, "--no-browser"))
	case "hosts-apply": // internal: invoked elevated to replace the hosts file
		err = cmdHostsApply(rest)
	case "help", "-h", "--help", "/?":
		fmt.Printf(usage, appVersion)
	case "version", "--version":
		fmt.Println("DevStack", appVersion)
	default:
		c, cerr := newClient(root)
		if cerr != nil {
			err = cerr
			break
		}
		err = c.run(cmd, rest)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func hasFlag(args []string, f string) bool {
	for _, a := range args {
		if a == f {
			return true
		}
	}
	return false
}

func cmdServe(root string, browser bool) error {
	initProcessGroup()
	app, err := NewApp(root)
	if err != nil {
		return err
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		app.Shutdown()
	}()
	fmt.Printf("DevStack %s - dashboard: http://localhost:%d/  (Ctrl+C to stop)\n", appVersion, app.config().DashboardPort)
	return app.Serve(browser)
}

func cmdHostsApply(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: hosts-apply <source> <hosts-file>")
	}
	b, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	return os.WriteFile(args[1], b, 0o644)
}

type client struct {
	root string
	base string
}

func newClient(root string) (*client, error) {
	cfg, _, err := loadConfig(root)
	if err != nil {
		return nil, err
	}
	return &client{root: root, base: fmt.Sprintf("http://127.0.0.1:%d", cfg.DashboardPort)}, nil
}

var errNotRunning = errors.New("DevStack is not running: start it with `devstack start` (or `devstack serve`)")

func (c *client) call(method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	req.Host = strings.Replace(strings.TrimPrefix(c.base, "http://"), "127.0.0.1", "localhost", 1)
	req.Header.Set("X-DevStack", "1")
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return errNotRunning
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		var e struct{ Error string }
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *client) running() bool { return c.call("GET", "/api/state", nil, nil) == nil }

func (c *client) state() (*StateView, error) {
	var st StateView
	return &st, c.call("GET", "/api/state", nil, &st)
}

func need(args []string, n int, what string) error {
	if len(args) < n {
		return fmt.Errorf("usage: devstack %s", what)
	}
	return nil
}

func (c *client) run(cmd string, args []string) error {
	switch cmd {
	case "start":
		if !c.running() {
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			p := exec.Command(exe, "serve", "--no-browser")
			p.Env = append(os.Environ(), "DEVSTACK_HOME="+c.root)
			detach(p)
			if err := p.Start(); err != nil {
				return err
			}
			for i := 0; i < 100 && !c.running(); i++ {
				time.Sleep(100 * time.Millisecond)
			}
			if !c.running() {
				return errors.New("DevStack did not start; see logs/devstack.log")
			}
			fmt.Println("DevStack started in the background")
		}
		if err := c.call("POST", "/api/services/all/start", nil, nil); err != nil {
			return err
		}
		return c.printStatus()
	case "stop":
		return c.call("POST", "/api/services/all/stop", nil, nil)
	case "restart":
		svc := "all"
		if len(args) > 0 {
			svc = args[0]
		}
		if err := c.call("POST", "/api/services/"+svc+"/restart", nil, nil); err != nil {
			return err
		}
		return c.printStatus()
	case "quit":
		return c.call("POST", "/api/quit", nil, nil)
	case "status":
		return c.printStatus()
	case "open":
		return openURL(strings.Replace(c.base, "127.0.0.1", "localhost", 1) + "/")
	case "sites":
		return c.printSites()
	case "site":
		return c.site(args)
	case "php":
		return c.php(args)
	case "ext":
		return c.ext(args)
	case "ini":
		return c.ini(args)
	case "install":
		if err := need(args, 1, "install apache|mysql|mariadb|adminer [url]"); err != nil {
			return err
		}
		body := map[string]string{"component": args[0]}
		if len(args) > 1 {
			body["url"] = args[1]
		}
		return c.install(body)
	case "db":
		return c.db(args)
	case "hosts":
		return c.call("POST", "/api/hosts/sync", nil, nil)
	case "ssl":
		if len(args) > 0 {
			switch args[0] {
			case "on", "off":
				if err := c.call("POST", "/api/settings", map[string]bool{"ssl": args[0] == "on"}, nil); err != nil {
					return err
				}
			case "trust":
				if err := c.call("POST", "/api/ssl/trust", nil, nil); err != nil {
					return err
				}
				fmt.Println("The DevStack CA is now trusted.")
			default:
				return errors.New("usage: devstack ssl [on|off|trust]")
			}
		}
		st, err := c.state()
		if err != nil {
			return err
		}
		s := st.SSL
		switch {
		case !s.Enabled:
			fmt.Println("HTTPS: off")
		case s.Error != "":
			fmt.Println("HTTPS: error:", s.Error)
		default:
			fmt.Printf("HTTPS: on (port %d) - every enabled site is at https://<name>.%s\n", st.Settings.HTTPSPort, st.Settings.TLD)
		}
		if s.CA {
			trust := "NOT trusted yet: run `devstack ssl trust`"
			if s.Trusted {
				trust = "trusted"
			}
			fmt.Printf("Local CA: %s (expires %s), %s\n", s.CAPath, s.Expires, trust)
		}
		return nil
	case "dns":
		if len(args) > 0 {
			on := args[0] == "on"
			if !on && args[0] != "off" {
				return errors.New("usage: devstack dns [on|off]")
			}
			if err := c.call("POST", "/api/settings", map[string]bool{"wildcard_dns": on}, nil); err != nil {
				return err
			}
		}
		st, err := c.state()
		if err != nil {
			return err
		}
		d := st.DNS
		switch {
		case !d.Enabled:
			fmt.Println("Wildcard DNS: off (sites resolve through the hosts file)")
		case !d.Running:
			fmt.Println("Wildcard DNS: not running:", d.Error)
		default:
			fmt.Printf("Wildcard DNS: answering *.%s on %s\n", st.Settings.TLD, d.Address)
			if d.Active {
				fmt.Printf("Windows sends *.%s lookups here (NRPT rule); the hosts file is not needed.\n", st.Settings.TLD)
			} else if d.Resolver == "nrpt" {
				fmt.Println("The Windows resolver rule is not in place yet: run `devstack hosts` and accept the UAC prompt.")
			} else {
				fmt.Printf("Point your resolver at it for *.%s (dnsmasq: server=/%s/127.0.0.1#%d).\n", st.Settings.TLD, st.Settings.TLD, st.Settings.DNSPort)
			}
		}
		return nil
	case "logs":
		if err := need(args, 1, "logs <name> [lines]"); err != nil {
			var names []string
			if e := c.call("GET", "/api/logs", nil, &names); e != nil {
				return e
			}
			fmt.Println(strings.Join(names, "\n"))
			return nil
		}
		lines := "200"
		if len(args) > 1 {
			lines = args[1]
		}
		var out struct{ Text string }
		if err := c.call("GET", "/api/logs/"+args[0]+"?lines="+lines, nil, &out); err != nil {
			return err
		}
		fmt.Print(out.Text)
		return nil
	}
	return fmt.Errorf("unknown command %q (see devstack help)", cmd)
}

func (c *client) printStatus() error {
	st, err := c.state()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SERVICE\tVERSION\tPORT\tSTATE\tPID")
	for _, s := range st.Services {
		state := s.Status.State
		if !s.Installed {
			state = "not installed"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%d\n", s.Title, s.Version, s.Port, state, s.Status.PID)
	}
	for _, p := range st.PHP {
		def := ""
		if p.Default {
			def = " (default)"
		}
		fmt.Fprintf(tw, "PHP %s%s\t%s\t%d\t%d/%d workers\t\n", p.Minor, def, p.Version, p.Port, p.Running, p.Workers)
	}
	tw.Flush()
	for _, s := range st.Services {
		if s.Status.Error != "" {
			fmt.Printf("\n%s: %s\n", s.Title, s.Status.Error)
		}
	}
	for _, w := range st.Warnings {
		fmt.Println("warning:", w)
	}
	fmt.Println()
	return c.printSites()
}

func (c *client) printSites() error {
	st, err := c.state()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SITE\tVHOST\tURL\tTYPE\tPHP\tDOCUMENT ROOT")
	for _, s := range st.Sites {
		on := "off"
		if s.Enabled {
			on = "on"
		}
		php := s.PHP
		if s.PHPSetting == "" {
			php += " (default)"
		}
		fmt.Fprintf(tw, "%s\t%s\thttp://%s%s\t%s\t%s\t%s\n", s.Name, on, s.Host, portSuffix(st.Settings.HTTPPort), s.Framework, php, s.DocRoot)
	}
	tw.Flush()
	return nil
}

func (c *client) site(args []string) error {
	if err := need(args, 2, "site enable|disable|php|root|add|remove <name> ..."); err != nil {
		return err
	}
	name := args[1]
	patch := map[string]any{}
	switch args[0] {
	case "enable", "on":
		patch["enabled"] = true
	case "disable", "off":
		patch["enabled"] = false
	case "php":
		if err := need(args, 3, "site php <name> <version|default>"); err != nil {
			return err
		}
		v := args[2]
		if v == "default" {
			v = ""
		}
		patch["php"] = v
	case "root":
		if err := need(args, 3, "site root <name> <dir|auto>"); err != nil {
			return err
		}
		v := args[2]
		if v == "auto" {
			v = ""
		}
		patch["docroot"] = v
	case "add":
		if err := need(args, 3, "site add <name> <path> [php]"); err != nil {
			return err
		}
		body := map[string]string{"name": name, "path": args[2]}
		if len(args) > 3 {
			body["php"] = args[3]
		}
		if err := c.call("POST", "/api/sites", body, nil); err != nil {
			return err
		}
		return c.printSites()
	case "remove":
		return c.call("DELETE", "/api/sites/"+pathEscape(name), nil, nil)
	default:
		return fmt.Errorf("unknown site command %q", args[0])
	}
	if err := c.call("PATCH", "/api/sites/"+pathEscape(name), patch, nil); err != nil {
		return err
	}
	return c.printSites()
}

func pathEscape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "%", "%25"), " ", "%20")
}

func (c *client) php(args []string) error {
	if len(args) == 0 {
		st, err := c.state()
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "VERSION\tBUILD\tDEFAULT\tIN USE\tWORKERS\tPATH")
		for _, p := range st.PHP {
			fmt.Fprintf(tw, "%s\t%s\t%v\t%v\t%d/%d\t%s\n", p.Minor, p.Version, p.Default, p.InUse, p.Running, p.Workers, p.Path)
		}
		return tw.Flush()
	}
	switch args[0] {
	case "available":
		var cat []PHPRelease
		if err := c.call("GET", "/api/php/catalog", nil, &cat); err != nil {
			return err
		}
		for _, r := range cat {
			inst := ""
			if r.Installed != "" {
				inst = "  (installed " + r.Installed + ")"
			}
			fmt.Printf("%-6s %s%s\n", r.Minor, r.Version, inst)
		}
		return nil
	case "install":
		if err := need(args, 2, "php install <version>"); err != nil {
			return err
		}
		return c.install(map[string]string{"component": "php", "version": args[1]})
	case "default":
		if err := need(args, 2, "php default <version>"); err != nil {
			return err
		}
		return c.call("POST", "/api/php/default", map[string]string{"version": args[1]}, nil)
	case "add":
		if err := need(args, 2, "php add <folder>"); err != nil {
			return err
		}
		return c.call("POST", "/api/php/add", map[string]string{"path": args[1]}, nil)
	case "remove":
		if err := need(args, 2, "php remove <version>"); err != nil {
			return err
		}
		return c.call("DELETE", "/api/php/"+args[1], nil, nil)
	}
	return fmt.Errorf("unknown php command %q", args[0])
}

func (c *client) findPHP(minor string) (*PHPView, error) {
	st, err := c.state()
	if err != nil {
		return nil, err
	}
	for i := range st.PHP {
		if st.PHP[i].Minor == minor {
			return &st.PHP[i], nil
		}
	}
	return nil, fmt.Errorf("PHP %s is not installed", minor)
}

func (c *client) ext(args []string) error {
	if err := need(args, 1, "ext <version> [on|off <ext...>]"); err != nil {
		return err
	}
	if len(args) >= 3 {
		on := args[1] == "on" || args[1] == "enable"
		if !on && args[1] != "off" && args[1] != "disable" {
			return errors.New("use on or off")
		}
		for _, e := range args[2:] {
			if err := c.call("POST", "/api/php/"+args[0]+"/extensions", map[string]any{"name": e, "enabled": on}, nil); err != nil {
				return err
			}
		}
	}
	p, err := c.findPHP(args[0])
	if err != nil {
		return err
	}
	for _, e := range p.Extensions {
		mark := "[ ]"
		if e.Enabled {
			mark = "[x]"
		}
		if e.Builtin {
			mark = "[=]"
		}
		fmt.Printf("%s %s\n", mark, e.Name)
	}
	fmt.Println("\n[x] enabled  [ ] disabled  [=] built in")
	for _, w := range p.Warnings {
		fmt.Println("warning:", w)
	}
	return nil
}

func (c *client) ini(args []string) error {
	if err := need(args, 1, "ini <version> [key=value...]"); err != nil {
		return err
	}
	p, err := c.findPHP(args[0])
	if err != nil {
		return err
	}
	ini := map[string]string{}
	for k, v := range p.INI {
		ini[k] = v
	}
	if len(args) > 1 {
		for _, kv := range args[1:] {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				return fmt.Errorf("expected key=value, got %q", kv)
			}
			if v == "" {
				delete(ini, k)
			} else {
				ini[k] = v
			}
		}
		if err := c.call("POST", "/api/php/"+args[0]+"/settings", map[string]any{"ini": ini}, nil); err != nil {
			return err
		}
	}
	for k, v := range p.Defaults {
		if _, ok := ini[k]; !ok {
			fmt.Printf("%s = %s (default)\n", k, v)
		}
	}
	for k, v := range ini {
		fmt.Printf("%s = %s\n", k, v)
	}
	return nil
}

func (c *client) install(body map[string]string) error {
	var t TaskView
	if err := c.call("POST", "/api/install", body, &t); err != nil {
		return err
	}
	last := ""
	for {
		if err := c.call("GET", fmt.Sprintf("/api/tasks/%d", t.ID), nil, &t); err != nil {
			return err
		}
		if t.Message != last {
			fmt.Println(t.Message)
			last = t.Message
		}
		switch t.Status {
		case "done":
			return nil
		case "error":
			return errors.New(t.Error)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (c *client) db(args []string) error {
	if len(args) == 0 {
		var dbs []string
		if err := c.call("GET", "/api/databases", nil, &dbs); err != nil {
			return err
		}
		fmt.Println(strings.Join(dbs, "\n"))
		return nil
	}
	if err := need(args, 2, "db create|drop <name>"); err != nil {
		return err
	}
	switch args[0] {
	case "create":
		return c.call("POST", "/api/databases", map[string]string{"name": args[1]}, nil)
	case "drop":
		return c.call("DELETE", "/api/databases/"+args[1], nil, nil)
	}
	return fmt.Errorf("unknown db command %q", args[0])
}
