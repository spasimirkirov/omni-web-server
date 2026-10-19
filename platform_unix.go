//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

func hasConsole() bool { return true }

func showError(msg string) {}

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
	return fmt.Errorf("permission denied writing %s: run Omni Web Server as root", path)
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

// PATH and registry integration are Windows features; on Unix put bin/shims
// on PATH in your shell profile.
func addToUserPath(dir string) error { return nil }

func removeFromUserPath(dir string) error { return nil }

func writeRegistry(map[string]map[string]string) error { return nil }

func removeRegistry() error { return nil }

func missingRuntimes() []string { return nil }

// Importing from Laragon/XAMPP is Windows-only; detectSources finds nothing here.
func foreignProcesses(root string) []foreignProc { return nil }

func killPID(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

func stopForeignServices(root string) (int, error) { return 0, nil }

func removeUserPathUnder(prefixes []string) error { return nil }

func removeStartupEntries(kind, root string) error { return nil }

func listeningPorts(pid int) []int { return nil }

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

const unixCAPath = "/usr/local/share/ca-certificates/omni-ca.crt"

// trustCA installs the CA into the system store (Debian/Ubuntu layout).
// Browsers with their own NSS store (Firefox/Chrome on Linux) need it imported there too.
func trustCA(path string) error {
	if os.Geteuid() != 0 || !isDirPath(filepath.Dir(unixCAPath)) {
		return fmt.Errorf("copy %s to your system/browser trust store (e.g. sudo cp it to %s && sudo update-ca-certificates)", path, unixCAPath)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := os.WriteFile(unixCAPath, b, 0o644); err != nil {
		return err
	}
	if out, err := exec.Command("update-ca-certificates").CombinedOutput(); err != nil {
		return fmt.Errorf("update-ca-certificates: %v %s", err, out)
	}
	return nil
}

func caInstalled(thumb, path string) bool {
	a, err1 := os.ReadFile(path)
	b, err2 := os.ReadFile(unixCAPath)
	return err1 == nil && err2 == nil && string(a) == string(b)
}

func isDirPath(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// The tray icon is Windows-only.
func (a *App) startTray()                                   {}
func closeTray()                                            {}
func (a *App) trayBalloon(title, text string, isError bool) {}
