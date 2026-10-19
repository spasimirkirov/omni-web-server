package main

import (
	"errors"
	"io/fs"
	"os"
	"strings"
)

const (
	hostsBegin = "# BEGIN Omni Web Server (managed automatically, do not edit)"
	hostsEnd   = "# END Omni Web Server"
)

// renderHosts replaces Omni Web Server's block in a hosts file with entries for hosts.
func renderHosts(existing string, hosts []string) string {
	nl := "\n"
	if strings.Contains(existing, "\r\n") {
		nl = "\r\n"
	}
	var kept []string
	inBlock := false
	for _, line := range strings.Split(strings.ReplaceAll(existing, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == hostsBegin:
			inBlock = true
		case t == hostsEnd && inBlock:
			inBlock = false
		case !inBlock:
			kept = append(kept, line)
		}
	}
	for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
		kept = kept[:len(kept)-1]
	}
	if len(hosts) > 0 {
		if len(kept) > 0 {
			kept = append(kept, "")
		}
		kept = append(kept, hostsBegin)
		for _, h := range hosts {
			kept = append(kept, "127.0.0.1\t"+h, "::1\t\t"+h)
		}
		kept = append(kept, hostsEnd)
	}
	return strings.Join(kept, nl) + nl
}

func (a *App) hostsFile(cfg *Config) string {
	if cfg.HostsFile != "" {
		return cfg.HostsFile
	}
	return defaultHostsFile()
}

// syncHosts writes the hosts file only when the managed block changes. When an
// elevated write was declined, the same content is not retried unless forced,
// so the user is not asked for UAC consent on every rescan.
func (a *App) syncHosts(cfg *Config, sites []Site, force, viaDNS bool) error {
	var hosts []string
	if cfg.ManageHosts && !viaDNS {
		for _, s := range sites {
			if s.Enabled && !s.Missing {
				hosts = append(hosts, s.Host, "www."+s.Host)
			}
		}
	}
	path := a.hostsFile(cfg)
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	next := renderHosts(string(old), hosts)
	if next == string(old) {
		return nil
	}
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		if !errors.Is(err, fs.ErrPermission) {
			return err
		}
		if !force && a.hostsDeclined == next {
			return errors.New("hosts file not updated: administrator rights are needed (use \"Sync hosts\" or run Omni Web Server as administrator)")
		}
		if err := elevatedWriteHosts(path, next); err != nil {
			a.hostsDeclined = next
			return err
		}
		a.hostsDeclined = ""
	}
	flushDNS()
	return nil
}
