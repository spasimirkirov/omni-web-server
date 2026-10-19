//go:build windows

package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

const (
	createNoWindow         = 0x08000000
	createNewProcessGroup  = 0x00000200
	detachedProcess        = 0x00000008
	jobObjectExtendedLimit = 9
	jobKillOnClose         = 0x00002000
)

func exeName(n string) string { return n + ".exe" }

func hiddenCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return cmd
}

type jobBasicLimit struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type ioCounters struct {
	ReadOperationCount, WriteOperationCount, OtherOperationCount uint64
	ReadTransferCount, WriteTransferCount, OtherTransferCount    uint64
}

type jobExtendedLimit struct {
	Basic                 jobBasicLimit
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObject    = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJob  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJob = kernel32.NewProc("AssignProcessToJobObject")
	procGetCurrentProcess  = kernel32.NewProc("GetCurrentProcess")
	procGetConsoleWindow   = kernel32.NewProc("GetConsoleWindow")
	procMessageBox         = syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
)

// hasConsole is false for omniw.exe (built with -H windowsgui), which the
// shortcuts and the autostart entry use so no console window appears.
func hasConsole() bool {
	h, _, _ := procGetConsoleWindow.Call()
	return h != 0
}

func showError(msg string) {
	title, _ := syscall.UTF16PtrFromString("Omni Web Server")
	text, _ := syscall.UTF16PtrFromString(msg)
	const mbIconError = 0x10
	procMessageBox.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), mbIconError)
}

// singleInstance claims a per-user mutex so only one Omni Web Server runs at
// a time, whichever folder it is in. The handle stays open until exit.
func singleInstance() bool {
	name, _ := syscall.UTF16PtrFromString(`Local\OmniWebServer.Serve`)
	h, _, err := kernel32.NewProc("CreateMutexW").Call(0, 0, uintptr(unsafe.Pointer(name)))
	const errorAlreadyExists = syscall.Errno(183)
	return h != 0 && err != errorAlreadyExists
}

// initProcessGroup puts Omni Web Server in a job object that kills every child
// (Apache, php-cgi, mysqld) when Omni Web Server exits, even if it is closed abruptly.
func initProcessGroup() {
	h, _, _ := procCreateJobObject.Call(0, 0)
	if h == 0 {
		return
	}
	var info jobExtendedLimit
	info.Basic.LimitFlags = jobKillOnClose
	r, _, _ := procSetInformationJob.Call(h, jobObjectExtendedLimit, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
	if r == 0 {
		return
	}
	// The handle is never closed on purpose: the job ends (and kills the
	// children) only when this process exits.
	self, _, _ := procGetCurrentProcess.Call()
	_, _, _ = procAssignProcessToJob.Call(h, self)
}

func terminateTree(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = hiddenCommand("taskkill", "/T", "/F", "/PID", fmt.Sprint(cmd.Process.Pid)).Run()
	}
}

func killTree(cmd *exec.Cmd) {
	terminateTree(cmd)
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func isAdmin() bool {
	f, err := os.Open(`\\.\PHYSICALDRIVE0`)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: detachedProcess | createNewProcessGroup}
}

func openURL(url string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

func openFolder(path string) error {
	return exec.Command("explorer", filepath.FromSlash(path)).Start()
}

func openTerminal(dir string, env []string) error {
	// Raw command line: start's first quoted argument is the window title and
	// Go's argument quoting would turn it into the program name.
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: fmt.Sprintf(`cmd.exe /C start "Omni" /D "%s" cmd.exe /K`, dir)}
	cmd.Env = env
	return cmd.Start()
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// elevatedWriteHosts asks for administrator rights (UAC) to replace the hosts file.
func elevatedWriteHosts(path, content string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	tmp := filepath.Join(os.TempDir(), "omni-hosts.txt")
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	defer os.Remove(tmp)
	ps := fmt.Sprintf("$p = Start-Process -FilePath %s -ArgumentList @('hosts-apply', %s, %s) -Verb RunAs -WindowStyle Hidden -Wait -PassThru; exit $p.ExitCode",
		psQuote(exe), psQuote(`"`+tmp+`"`), psQuote(`"`+path+`"`))
	out, err := hiddenCommand("powershell", "-NoProfile", "-NonInteractive", "-Command", ps).CombinedOutput()
	if err != nil {
		return fmt.Errorf("updating hosts as administrator failed (UAC declined?): %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func flushDNS() { _ = hiddenCommand("ipconfig", "/flushdns").Run() }

func defaultHostsFile() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "drivers", "etc", "hosts")
}

func portOwner(port int) string {
	out, err := hiddenCommand("netstat", "-ano", "-p", "TCP").Output()
	if err != nil {
		return ""
	}
	suffix := fmt.Sprintf(":%d", port)
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 5 && strings.HasSuffix(f[1], suffix) && strings.EqualFold(f[3], "LISTENING") {
			pid := f[4]
			name := ""
			if t, err := hiddenCommand("tasklist", "/FI", "PID eq "+pid, "/FO", "CSV", "/NH").Output(); err == nil {
				if parts := strings.Split(string(t), ","); len(parts) > 0 {
					name = strings.Trim(strings.TrimSpace(parts[0]), `"`)
				}
			}
			if pid == "4" {
				name = "System (IIS / http.sys / World Wide Web Publishing service)"
			}
			return fmt.Sprintf(" by PID %s %s", pid, name)
		}
	}
	return ""
}

// addToUserPath prepends dir to the user's PATH (registry HKCU\Environment);
// Windows broadcasts the change so new terminals and IDEs pick it up.
func addToUserPath(dir string) error {
	return runPowerShell(fmt.Sprintf(`$d=%s; $p=[Environment]::GetEnvironmentVariable('Path','User'); if (-not $p) {$p=''}
if (($p -split ';') -notcontains $d) { [Environment]::SetEnvironmentVariable('Path', ($d + ';' + $p).TrimEnd(';'), 'User') }`, psQuote(dir)), false)
}

func removeFromUserPath(dir string) error {
	return runPowerShell(fmt.Sprintf(`$d=%s; $p=[Environment]::GetEnvironmentVariable('Path','User')
if ($p -and (($p -split ';') -contains $d)) { [Environment]::SetEnvironmentVariable('Path', ((($p -split ';') | Where-Object { $_ -and $_ -ne $d }) -join ';'), 'User') }`, psQuote(dir)), false)
}

const registryKey = `HKCU:\Software\OmniWebServer`

// writeRegistry replaces HKCU\Software\OmniWebServer with the given values and
// sets the OMNI_WEB_SERVER_HOME user variable, so IDEs and scripts can find the stack.
func writeRegistry(vals map[string]map[string]string) error {
	var b strings.Builder
	// Replace the subkeys (PHP versions come and go) but keep values other tools
	// wrote on the main key, such as the installer's language choice.
	fmt.Fprintf(&b, "$ErrorActionPreference='Stop'\nGet-ChildItem -Path %s -ErrorAction SilentlyContinue | Remove-Item -Recurse\n", psQuote(registryKey))
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		path := registryKey
		if k != "" {
			path += `\` + k
		}
		fmt.Fprintf(&b, "New-Item -Path %s -Force | Out-Null\n", psQuote(path))
		for n, v := range vals[k] {
			fmt.Fprintf(&b, "New-ItemProperty -Path %s -Name %s -Value %s -PropertyType String -Force | Out-Null\n", psQuote(path), psQuote(n), psQuote(v))
		}
	}
	if home := vals[""]["InstallDir"]; home != "" {
		fmt.Fprintf(&b, "[Environment]::SetEnvironmentVariable('%s', %s, 'User')\n", homeEnv, psQuote(home))
		fmt.Fprintf(&b, "if ([Environment]::GetEnvironmentVariable('%[1]s', 'User') -eq %[2]s) { [Environment]::SetEnvironmentVariable('%[1]s', $null, 'User') }\n", legacyHomeEnv, psQuote(home))
	}
	return runPowerShell(b.String(), false)
}

func removeRegistry() error {
	script := fmt.Sprintf("Remove-Item -Path %s -Recurse -ErrorAction SilentlyContinue\n[Environment]::SetEnvironmentVariable('%s', $null, 'User')\n", psQuote(registryKey), homeEnv)
	// The old name only goes when it points to an Omni Web Server folder.
	script += fmt.Sprintf("$v = [Environment]::GetEnvironmentVariable('%[1]s', 'User'); if ($v -and (Test-Path (Join-Path $v 'omni.json'))) { [Environment]::SetEnvironmentVariable('%[1]s', $null, 'User') }", legacyHomeEnv)
	return runPowerShell(script, false)
}

// missingRuntimes reports a missing Visual C++ runtime, needed by Apache, PHP and MySQL builds.
func missingRuntimes() []string {
	sys := filepath.Join(os.Getenv("SystemRoot"), "System32")
	for _, dll := range []string{"vcruntime140.dll", "vcruntime140_1.dll", "msvcp140.dll"} {
		if !exists(filepath.Join(sys, dll)) {
			return []string{"Microsoft Visual C++ Redistributable (x64) is missing: install it from https://aka.ms/vs/17/release/vc_redist.x64.exe"}
		}
	}
	return nil
}

func powershellOutput(script string) (string, error) {
	out, err := hiddenCommand("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	return strings.TrimSpace(string(out)), err
}

// foreignProcesses lists the processes whose executable lives under root.
func foreignProcesses(root string) []foreignProc {
	out, err := powershellOutput(fmt.Sprintf(`$r=%s + '\'; Get-Process | Where-Object { $_.Path -and $_.Path.StartsWith($r, [StringComparison]::OrdinalIgnoreCase) } | ForEach-Object { "$($_.Id)|$($_.ProcessName)" }`, psQuote(filepath.Clean(root))))
	if err != nil {
		return nil
	}
	var procs []foreignProc
	for _, line := range strings.Split(out, "\n") {
		id, name, ok := strings.Cut(strings.TrimSpace(line), "|")
		pid, err := strconv.Atoi(id)
		if ok && err == nil {
			procs = append(procs, foreignProc{PID: pid, Name: strings.ToLower(name)})
		}
	}
	return procs
}

func killPID(pid int) error {
	return hiddenCommand("taskkill", "/F", "/PID", strconv.Itoa(pid)).Run()
}

// stopForeignServices stops the Windows services whose program lives under
// root and sets them to manual start (one UAC prompt when there are any).
func stopForeignServices(root string) (int, error) {
	find := fmt.Sprintf(`$r=%s.ToLower(); $s=@(Get-CimInstance Win32_Service | Where-Object { $_.PathName -and $_.PathName.ToLower().Contains($r) })`, psQuote(filepath.Clean(root)))
	out, err := powershellOutput(find + "; $s.Count")
	if err != nil {
		return 0, err
	}
	n, _ := strconv.Atoi(out)
	if n == 0 {
		return 0, nil
	}
	return n, runPowerShell(find+"\n$s | ForEach-Object { Stop-Service -Name $_.Name -Force -ErrorAction SilentlyContinue; Set-Service -Name $_.Name -StartupType Manual }", true)
}

// removeUserPathUnder removes user PATH entries that start with any of the prefixes.
func removeUserPathUnder(prefixes []string) error {
	quoted := make([]string, len(prefixes))
	for i, p := range prefixes {
		quoted[i] = psQuote(strings.ToLower(filepath.Clean(p)))
	}
	return runPowerShell(fmt.Sprintf(`$pre=@(%s); $p=[Environment]::GetEnvironmentVariable('Path','User'); if (-not $p) { exit 0 }
$parts=@($p -split ';' | Where-Object { $_ })
$keep=@($parts | Where-Object { $e=$_.ToLower().TrimEnd('\'); -not ($pre | Where-Object { $e -eq $_ -or $e.StartsWith($_ + '\') }) })
if ($keep.Count -ne $parts.Count) { [Environment]::SetEnvironmentVariable('Path', ($keep -join ';'), 'User') }`, strings.Join(quoted, ",")), false)
}

// removeStartupEntries removes the old stack's Startup-folder shortcut and Run values.
func removeStartupEntries(kind, root string) error {
	return runPowerShell(fmt.Sprintf(`$r=%s.ToLower() + '\'; $sh=New-Object -ComObject WScript.Shell
Get-ChildItem ([Environment]::GetFolderPath('Startup')) -Filter '*.lnk' -ErrorAction SilentlyContinue | Where-Object { $sh.CreateShortcut($_.FullName).TargetPath.ToLower().StartsWith($r) } | Remove-Item -Force
$run='HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
(Get-ItemProperty $run -ErrorAction SilentlyContinue).PSObject.Properties | Where-Object { $_.Name -notlike 'PS*' -and "$($_.Value)".ToLower().Contains($r) } | ForEach-Object { Remove-ItemProperty $run -Name $_.Name }`, psQuote(filepath.Clean(root))), false)
}

// listeningPorts returns the TCP ports a process listens on.
func listeningPorts(pid int) []int {
	out, err := hiddenCommand("netstat", "-ano", "-p", "TCP").Output()
	if err != nil {
		return nil
	}
	var ports []int
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 5 && strings.EqualFold(f[3], "LISTENING") && f[4] == strconv.Itoa(pid) {
			if i := strings.LastIndex(f[1], ":"); i >= 0 {
				if p, err := strconv.Atoi(f[1][i+1:]); err == nil && !containsInt(ports, p) {
					ports = append(ports, p)
				}
			}
		}
	}
	return ports
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

var errResolverUnsupported = errors.New("unsupported")

const nrptComment = "OmniWebServer"

func resolverKind() string { return "nrpt" }

// currentResolverRule returns the namespace of Omni Web Server's NRPT rule ("" if none).
// Reading the table does not need administrator rights.
func currentResolverRule() string {
	ps := fmt.Sprintf(`Get-DnsClientNrptRule | Where-Object { $_.Comment -eq '%s' } | Select-Object -First 1 -ExpandProperty Namespace`, nrptComment)
	out, err := hiddenCommand("powershell", "-NoProfile", "-NonInteractive", "-Command", ps).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// applyResolverRule replaces Omni Web Server's NRPT rule so that only names under
// namespace (".local") are resolved by the DNS server on 127.0.0.1. An empty
// namespace removes the rule.
func applyResolverRule(namespace string, port int) error {
	if namespace != "" && port != 53 {
		return fmt.Errorf("Windows can only send queries to DNS port 53 (configured: %d)", port)
	}
	script := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
Get-DnsClientNrptRule | Where-Object { $_.Comment -eq '%s' } | ForEach-Object { Remove-DnsClientNrptRule -Name $_.Name -Force }
`, nrptComment)
	if namespace != "" {
		script += fmt.Sprintf("Add-DnsClientNrptRule -Namespace %s -NameServers '127.0.0.1' -Comment '%s' | Out-Null\n", psQuote(namespace), nrptComment)
	}
	script += "Clear-DnsClientCache\n"
	return runPowerShell(script, true)
}

// runPowerShell runs a script, elevating through UAC when needed and asked to.
func runPowerShell(script string, elevate bool) error {
	u16 := utf16.Encode([]rune(script))
	b := make([]byte, 2*len(u16))
	for i, c := range u16 {
		b[2*i], b[2*i+1] = byte(c), byte(c>>8)
	}
	enc := base64.StdEncoding.EncodeToString(b)
	if !elevate || isAdmin() {
		out, err := hiddenCommand("powershell", "-NoProfile", "-NonInteractive", "-EncodedCommand", enc).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	ps := fmt.Sprintf("$p = Start-Process -FilePath powershell -ArgumentList @('-NoProfile','-NonInteractive','-EncodedCommand','%s') -Verb RunAs -WindowStyle Hidden -Wait -PassThru; exit $p.ExitCode", enc)
	out, err := hiddenCommand("powershell", "-NoProfile", "-NonInteractive", "-Command", ps).CombinedOutput()
	if err != nil {
		return fmt.Errorf("running as administrator failed (UAC declined?): %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// trustCA adds the CA to the current user's Trusted Root store. Windows shows
// its own confirmation dialog; no administrator rights are needed.
func trustCA(path string) error {
	// No console window, but not HideWindow: that could hide the confirmation dialog.
	cmd := exec.Command("certutil", "-user", "-addstore", "Root", path)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("certutil: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func caInstalled(thumb, path string) bool {
	if thumb == "" {
		return false
	}
	ps := fmt.Sprintf(`if ((Test-Path 'Cert:\CurrentUser\Root\%[1]s') -or (Test-Path 'Cert:\LocalMachine\Root\%[1]s')) { 'yes' }`, thumb)
	out, _ := hiddenCommand("powershell", "-NoProfile", "-NonInteractive", "-Command", ps).Output()
	return strings.TrimSpace(string(out)) == "yes"
}
