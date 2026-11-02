package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Changing the domain suffix touches vhosts, the hosts file, certificates, the
// wildcard DNS rule and Apache. The whole switch runs as one task with a step
// per area, and every step that did not work says what to do about it.

// hostsMissing returns the names that have no entry in the hosts file content.
func hostsMissing(content string, hosts []string) []string {
	have := map[string]bool{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		for _, h := range f[1:] {
			have[strings.ToLower(h)] = true
		}
	}
	var out []string
	for _, h := range hosts {
		if !have[strings.ToLower(h)] {
			out = append(out, h)
		}
	}
	return out
}

func (a *App) startTLDChange(raw string) (*Task, error) {
	tld, err := normalizeTLD(raw)
	if err != nil {
		return nil, badRequest("%v", err)
	}
	if cur := a.config().TLD; cur == tld {
		return nil, badRequest("the domain suffix is already .%s", tld)
	}
	title := "Change domain suffix to ." + tld
	if a.tasks.Busy(title) {
		return nil, badRequest("%s is already running", title)
	}
	return a.tasks.Run(title, func(t *Task) error { return a.changeTLD(t, tld) }), nil
}

func (a *App) warningsSnapshot() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.warnings...)
}

func (a *App) changeTLD(t *Task, tld string) error {
	old := a.config().TLD
	t.SetSteps([]string{
		"Save the new suffix",
		"Re-render virtual hosts",
		"Update the hosts file",
		"Issue HTTPS certificates",
		"Update wildcard DNS",
		"Restart Apache",
	})
	var problems []string
	fail := func(i int, msg string) {
		t.StepStatus(i, "failed")
		t.AddLog(msg)
		problems = append(problems, msg)
	}

	// 1. save
	t.StepStatus(0, "running")
	a.mu.Lock()
	a.cfg.TLD = tld
	err := saveConfig(a.root, a.cfg)
	a.mu.Unlock()
	if err != nil {
		t.StepStatus(0, "failed")
		return fmt.Errorf("could not save the new suffix: %v", err)
	}
	t.StepStatus(0, "done")

	// 2. one Apply: vhosts, certificates, DNS, hosts file, Apache restart
	t.StepStatus(1, "running")
	if err := a.Apply(true); err != nil {
		fail(1, "re-rendering failed: "+err.Error())
	} else {
		t.StepStatus(1, "done")
	}
	cfg := a.config()
	warns := a.warningsSnapshot()
	a.mu.Lock()
	sites := append([]Site(nil), a.sites...)
	dns := a.dnsSnap
	sslErr := a.sslErr
	a.mu.Unlock()
	var hosts []string
	for _, s := range sites {
		if s.Enabled && !s.Missing {
			hosts = append(hosts, s.Host)
		}
	}

	// 3. hosts file
	switch {
	case !cfg.ManageHosts:
		t.StepStatus(2, "skipped")
	case dns.Active:
		t.StepStatus(2, "skipped") // the wildcard DNS rule answers for every name
	default:
		t.StepStatus(2, "running")
		content, rerr := os.ReadFile(a.hostsFile(cfg))
		if rerr != nil {
			fail(2, "cannot read the hosts file: "+rerr.Error())
		} else if miss := hostsMissing(string(content), hosts); len(miss) > 0 {
			fail(2, "The hosts file does not have the new names yet (administrator rights are needed). Click Settings > Rewrite hosts file now and accept the prompt, or start Omni Web Server as administrator.")
		} else {
			t.StepStatus(2, "done")
		}
	}

	// 4. certificates
	if !cfg.SSL {
		t.StepStatus(3, "skipped")
	} else {
		t.StepStatus(3, "running")
		missing := 0
		for _, h := range hosts {
			crt, key := a.siteCertPaths(h)
			if !exists(crt) || !exists(key) {
				missing++
			}
		}
		if sslErr != "" {
			fail(3, "HTTPS certificates: "+sslErr)
		} else if missing > 0 {
			fail(3, fmt.Sprintf("%d certificate(s) for .%s were not created; see the logs and click Rewrite hosts file now to retry", missing, tld))
		} else {
			a.removeOldCerts(old)
			t.StepStatus(3, "done")
		}
	}

	// 5. wildcard DNS
	if !cfg.WildcardDNS {
		t.StepStatus(4, "skipped")
	} else {
		t.StepStatus(4, "running")
		switch {
		case !dns.Running:
			fail(4, "Wildcard DNS is not running: "+dns.Error)
		case dns.Resolver == "nrpt" && !dns.Active:
			fail(4, fmt.Sprintf("The Windows DNS rule for .%s is not in place (administrator rights are needed). Click Apply DNS rule in Settings and accept the prompt.", tld))
		default:
			t.StepStatus(4, "done")
		}
	}

	// 6. Apache (Apply restarted it because the vhost file changed)
	bin := a.apacheBin(cfg)
	switch {
	case bin == "" || !exists(bin):
		t.StepStatus(5, "skipped")
	case !a.apache.Running():
		t.StepStatus(5, "skipped") // not running: the next start uses the new names
		t.AddLog("Apache is not running; it will use the new names when it starts.")
	default:
		t.StepStatus(5, "running")
		conf, _ := os.ReadFile(a.apacheConfPath())
		bad := ""
		if len(hosts) > 0 && !strings.Contains(string(conf), "ServerName "+hosts[0]) {
			bad = "httpd.conf does not contain the new names"
			for _, w := range warns {
				if strings.Contains(strings.ToLower(w), "apache") {
					bad = w
				}
			}
		}
		if bad != "" {
			fail(5, "Apache: "+bad)
		} else if st := a.apache.Status().State; st != "running" && st != "starting" {
			fail(5, "Apache is "+st+" after the change: "+a.apache.Status().Error)
		} else {
			t.StepStatus(5, "done")
		}
	}

	if len(problems) > 0 {
		return errors.New("The suffix is now ." + tld + ", but: " + strings.Join(problems, " | "))
	}
	t.Set("Domain suffix is now ."+tld+". Update bookmarks and APP_URL in your projects.", 1)
	return nil
}

// removeOldCerts deletes the certificates issued for the previous suffix.
func (a *App) removeOldCerts(old string) {
	if old == "" {
		return
	}
	dir := a.path("etc", "ssl", "sites")
	for _, ext := range []string{".crt", ".key"} {
		m, _ := filepath.Glob(filepath.Join(dir, "*."+old+ext))
		for _, f := range m {
			_ = os.Remove(f)
		}
	}
}
