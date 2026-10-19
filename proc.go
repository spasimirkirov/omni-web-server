package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"
)

// ProcSpec describes a supervised child process.
type ProcSpec struct {
	Name    string
	LogPath string
	Build   func() (*exec.Cmd, error)
	// Port, when set, is used as the readiness check.
	Port int
	// Graceful asks the process to stop before it is killed (optional).
	Graceful func() error
}

// Proc supervises one child: it restarts it after crashes with backoff and
// gives up after repeated fast failures.
type Proc struct {
	spec ProcSpec

	mu        sync.Mutex
	cmd       *exec.Cmd
	exited    chan struct{}
	wanted    bool
	state     string // stopped | starting | running | restarting | failed
	lastErr   string
	startedAt time.Time
	fastFails int
	restarts  int
}

type ProcStatus struct {
	State    string `json:"state"`
	PID      int    `json:"pid,omitempty"`
	Error    string `json:"error,omitempty"`
	Uptime   int64  `json:"uptime,omitempty"`
	Restarts int    `json:"restarts"`
}

func NewProc(spec ProcSpec) *Proc { return &Proc{spec: spec, state: "stopped"} }

func (p *Proc) Status() ProcStatus {
	p.refresh()
	p.mu.Lock()
	defer p.mu.Unlock()
	st := ProcStatus{State: p.state, Error: p.lastErr, Restarts: p.restarts}
	if p.cmd != nil && p.cmd.Process != nil {
		st.PID = p.cmd.Process.Pid
		st.Uptime = int64(time.Since(p.startedAt).Seconds())
	}
	return st
}

func (p *Proc) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cmd != nil
}

func (p *Proc) launchLocked() error {
	cmd, err := p.spec.Build()
	if err != nil {
		p.state, p.lastErr = "failed", err.Error()
		return err
	}
	logf, err := os.OpenFile(p.spec.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	fmt.Fprintf(logf, "\n==== %s starting at %s ====\n", p.spec.Name, time.Now().Format(time.RFC3339))
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		logf.Close()
		p.state, p.lastErr = "failed", err.Error()
		return err
	}
	p.cmd = cmd
	p.startedAt = time.Now()
	p.state = "starting"
	exited := make(chan struct{})
	p.exited = exited
	go func() {
		werr := cmd.Wait()
		logf.Close()
		p.onExit(cmd, werr)
		close(exited)
	}()
	return nil
}

func (p *Proc) onExit(cmd *exec.Cmd, werr error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != cmd {
		return
	}
	p.cmd = nil
	if !p.wanted {
		p.state = "stopped"
		return
	}
	if time.Since(p.startedAt) < 10*time.Second {
		p.fastFails++
	} else {
		p.fastFails = 0
	}
	p.lastErr = fmt.Sprintf("exited unexpectedly (%v): %s", werr, lastLines(p.spec.LogPath, 6))
	if p.fastFails >= 5 {
		p.state = "failed"
		p.wanted = false
		return
	}
	p.state = "restarting"
	p.restarts++
	delay := time.Second << min(p.fastFails, 4)
	go func() {
		time.Sleep(delay)
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.wanted && p.cmd == nil {
			_ = p.launchLocked()
		}
	}()
}

// Start launches the process and waits until it accepts connections.
func (p *Proc) Start() error {
	p.mu.Lock()
	if p.cmd != nil {
		p.wanted = true
		p.mu.Unlock()
		return nil
	}
	if p.spec.Port != 0 && portInUse(p.spec.Port) {
		p.mu.Unlock()
		err := fmt.Errorf("port %d is already in use%s", p.spec.Port, portOwner(p.spec.Port))
		p.setFailed(err)
		return err
	}
	p.wanted = true
	p.fastFails = 0
	p.lastErr = ""
	if err := p.launchLocked(); err != nil {
		p.wanted = false
		p.mu.Unlock()
		return err
	}
	cmd, exited := p.cmd, p.exited
	p.mu.Unlock()
	return p.waitReady(cmd, exited, 30*time.Second)
}

// queue shows "starting" for a process that is about to be started (StartAll
// starts them one after another, so the later ones would show "stopped" for a
// few seconds). unqueue undoes it if the start never happened.
func (p *Proc) queue() {
	p.mu.Lock()
	if p.cmd == nil && (p.state == "stopped" || p.state == "failed") {
		p.state, p.lastErr = "starting", ""
	}
	p.mu.Unlock()
}

func (p *Proc) unqueue() {
	p.mu.Lock()
	if p.cmd == nil && p.state == "starting" {
		p.state = "stopped"
	}
	p.mu.Unlock()
}

func (p *Proc) setFailed(err error) {
	p.mu.Lock()
	p.state, p.lastErr = "failed", err.Error()
	p.mu.Unlock()
}

func (p *Proc) waitReady(cmd *exec.Cmd, exited chan struct{}, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		select {
		case <-exited:
			err := fmt.Errorf("%s exited during startup: %s", p.spec.Name, lastLines(p.spec.LogPath, 8))
			p.mu.Lock()
			if p.cmd == nil && p.state != "restarting" {
				p.state = "failed"
			}
			p.lastErr = err.Error()
			p.mu.Unlock()
			return err
		default:
		}
		if p.spec.Port == 0 || dialOK(p.spec.Port) {
			p.mu.Lock()
			if p.cmd == cmd {
				p.state = "running"
			}
			p.mu.Unlock()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not open port %d within %s", p.spec.Name, p.spec.Port, timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// refresh flips "starting" to "running" once a process restarted by the
// supervisor accepts connections.
func (p *Proc) refresh() {
	p.mu.Lock()
	cmd, starting := p.cmd, p.state == "starting"
	p.mu.Unlock()
	if cmd == nil || !starting || (p.spec.Port != 0 && !dialOK(p.spec.Port)) {
		return
	}
	p.mu.Lock()
	if p.cmd == cmd && p.state == "starting" {
		p.state = "running"
	}
	p.mu.Unlock()
}

func (p *Proc) Stop() error {
	p.mu.Lock()
	p.wanted = false
	cmd, exited := p.cmd, p.exited
	if cmd == nil {
		if p.state != "failed" {
			p.state = "stopped"
		}
		p.mu.Unlock()
		return nil
	}
	p.state = "stopping"
	p.mu.Unlock()

	if p.spec.Graceful != nil {
		if err := p.spec.Graceful(); err == nil {
			select {
			case <-exited:
				return nil
			case <-time.After(20 * time.Second):
			}
		}
	}
	terminateTree(cmd)
	select {
	case <-exited:
		return nil
	case <-time.After(8 * time.Second):
	}
	killTree(cmd)
	select {
	case <-exited:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("%s did not stop", p.spec.Name)
	}
}

func (p *Proc) Restart() error {
	if err := p.Stop(); err != nil {
		return err
	}
	return p.Start()
}

func dialOK(port int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func portInUse(port int) bool {
	if dialOK(port) {
		return true
	}
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return true
	}
	l.Close()
	return false
}

func lastLines(path string, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, _ := f.Stat()
	size := st.Size()
	const max = 16 * 1024
	off := size - max
	if off < 0 {
		off = 0
	}
	buf := make([]byte, size-off)
	_, _ = f.ReadAt(buf, off)
	lines := splitLines(string(buf))
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			if line != "" {
				out = append(out, line)
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
