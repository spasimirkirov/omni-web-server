//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func exeName(n string) string { return n }

func hiddenCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

func initProcessGroup() {}

func terminateTree(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
}

func killTree(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

func isAdmin() bool { return os.Geteuid() == 0 }

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func openURL(url string) error {
	for _, c := range []string{"xdg-open", "open"} {
		if p, err := exec.LookPath(c); err == nil {
			return exec.Command(p, url).Start()
		}
	}
	return fmt.Errorf("no browser opener found")
}

func openFolder(path string) error { return openURL(path) }

func openTerminal(dir string, env []string) error {
	return fmt.Errorf("opening a terminal is only supported on Windows")
}

func elevatedWriteHosts(path, content string) error {
	return fmt.Errorf("permission denied writing %s: run DevStack as root", path)
}

func flushDNS() {}

func defaultHostsFile() string { return "/etc/hosts" }

func portOwner(port int) string {
	out, err := exec.Command("ss", "-ltnpH", fmt.Sprintf("sport = :%d", port)).Output()
	if err != nil || len(out) == 0 {
		return ""
	}
	if i := strings.Index(string(out), "users:"); i >= 0 {
		return " by " + strings.TrimSpace(string(out[i:]))
	}
	return ""
}

func addToUserPath(dir string) error {
	return fmt.Errorf("add %s to PATH in your shell profile", dir)
}

func missingRuntimes() []string { return nil }

var errResolverUnsupported = errors.New("automatic resolver setup is only available on Windows")

// resolverKind describes how *.tld queries reach the DNS server on this OS.
func resolverKind() string { return "manual" }

func currentResolverRule() string { return "" }

// applyResolverRule is a no-op off Windows: point dnsmasq/systemd-resolved at
// the DNS server yourself (see README).
func applyResolverRule(namespace string, port int) error {
	if namespace == "" {
		return nil
	}
	return errResolverUnsupported
}
