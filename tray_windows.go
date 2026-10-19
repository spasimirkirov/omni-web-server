//go:build windows

package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Notification-area (tray) icon: a right-click menu to open the dashboard,
// start/stop/restart services, open the projects and logs folders and quit.
// Double-click opens the dashboard. Plain Win32 through syscall.

var (
	user32  = syscall.NewLazyDLL("user32.dll")
	shell32 = syscall.NewLazyDLL("shell32.dll")

	procRegisterClassEx       = user32.NewProc("RegisterClassExW")
	procCreateWindowEx        = user32.NewProc("CreateWindowExW")
	procDefWindowProc         = user32.NewProc("DefWindowProcW")
	procDestroyWindow         = user32.NewProc("DestroyWindow")
	procGetMessage            = user32.NewProc("GetMessageW")
	procTranslateMessage      = user32.NewProc("TranslateMessage")
	procDispatchMessage       = user32.NewProc("DispatchMessageW")
	procPostQuitMessage       = user32.NewProc("PostQuitMessage")
	procPostMessage           = user32.NewProc("PostMessageW")
	procRegisterWindowMessage = user32.NewProc("RegisterWindowMessageW")
	procCreatePopupMenu       = user32.NewProc("CreatePopupMenu")
	procAppendMenu            = user32.NewProc("AppendMenuW")
	procSetMenuDefaultItem    = user32.NewProc("SetMenuDefaultItem")
	procTrackPopupMenu        = user32.NewProc("TrackPopupMenu")
	procDestroyMenu           = user32.NewProc("DestroyMenu")
	procGetCursorPos          = user32.NewProc("GetCursorPos")
	procSetForegroundWindow   = user32.NewProc("SetForegroundWindow")
	procLoadIcon              = user32.NewProc("LoadIconW")
	procShellNotifyIcon       = shell32.NewProc("Shell_NotifyIconW")
	procExtractIconEx         = shell32.NewProc("ExtractIconExW")
	procGetModuleHandle       = kernel32.NewProc("GetModuleHandleW")
	trayTaskbarCreated        uint32 // broadcast when Explorer restarts
)

const (
	wmNull          = 0x0000
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205
	wmApp           = 0x8000
	wmTray          = wmApp + 1

	nimAdd    = 0
	nimModify = 1
	nimDelete = 2

	nifMessage = 0x1
	nifIcon    = 0x2
	nifTip     = 0x4
	nifInfo    = 0x10

	niifError = 0x3

	mfString    = 0x0
	mfGrayed    = 0x1
	mfPopup     = 0x10
	mfSeparator = 0x800

	tpmRightButton = 0x2
	tpmReturnCmd   = 0x100
	tpmNoNotify    = 0x80
)

type notifyIconData struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         [16]byte
	HBalloonIcon     uintptr
}

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type winMsg struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
	_       uint32
}

// trayState is the single tray icon of this process.
var trayState struct {
	sync.Mutex
	app     *App
	hwnd    uintptr
	icon    uintptr
	tip     string
	actions map[uintptr]func() // menu command id -> action
}

func copyUTF16(dst []uint16, s string) {
	u, _ := syscall.UTF16FromString(s)
	if len(u) > len(dst) {
		u = append(u[:len(dst)-1], 0)
	}
	copy(dst, u)
}

func (a *App) trayNotifyData(flags uint32) notifyIconData {
	d := notifyIconData{UID: 1, UFlags: flags, UCallbackMessage: wmTray, HIcon: trayState.icon, HWnd: trayState.hwnd}
	d.CbSize = uint32(unsafe.Sizeof(d))
	copyUTF16(d.SzTip[:], trayState.tip)
	return d
}

// startTray shows the tray icon and runs its message loop on a dedicated thread.
func (a *App) startTray() {
	ready := make(chan bool, 1)
	go func() {
		runtime.LockOSThread()
		if !a.createTrayWindow() {
			ready <- false
			return
		}
		ready <- true
		var m winMsg
		for {
			r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 {
				return
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			procDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
		}
	}()
	if <-ready {
		go a.trayTooltipLoop()
	}
}

func (a *App) createTrayWindow() bool {
	name, _ := syscall.UTF16PtrFromString("OmniWebServerTray")
	taskbar, _ := syscall.UTF16PtrFromString("TaskbarCreated")
	r, _, _ := procRegisterWindowMessage.Call(uintptr(unsafe.Pointer(taskbar)))
	trayTaskbarCreated = uint32(r)
	inst, _, _ := procGetModuleHandle.Call(0)
	wc := wndClassEx{LpfnWndProc: syscall.NewCallback(trayWndProc), HInstance: inst, LpszClassName: name}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	if r, _, err := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		a.log.Printf("tray: RegisterClassEx: %v", err)
		return false
	}
	// A hidden top-level window (not message-only, so it gets TaskbarCreated).
	hwnd, _, err := procCreateWindowEx.Call(0, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)), 0, 0, 0, 0, 0, 0, 0, inst, 0)
	if hwnd == 0 {
		a.log.Printf("tray: CreateWindowEx: %v", err)
		return false
	}

	trayState.Lock()
	defer trayState.Unlock()
	trayState.app = a
	trayState.hwnd = hwnd
	trayState.icon = loadAppIcon()
	trayState.tip = a.trayTooltip()
	d := a.trayNotifyData(nifMessage | nifIcon | nifTip)
	if r, _, err := procShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&d))); r == 0 {
		a.log.Printf("tray: Shell_NotifyIcon: %v", err)
		return false
	}
	a.log.Printf("tray icon added")
	return true
}

// loadAppIcon takes the small icon embedded in omni.exe (rsrc_windows_amd64.syso).
func loadAppIcon() uintptr {
	if exe, err := os.Executable(); err == nil {
		p, _ := syscall.UTF16PtrFromString(exe)
		var small uintptr
		if n, _, _ := procExtractIconEx.Call(uintptr(unsafe.Pointer(p)), 0, 0, uintptr(unsafe.Pointer(&small)), 1); n > 0 && small != 0 {
			return small
		}
	}
	h, _, _ := procLoadIcon.Call(0, 32512) // IDI_APPLICATION
	return h
}

func trayWndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch uint32(msg) {
	case wmTray:
		switch uint32(lparam) & 0xffff {
		case wmLButtonDblClk:
			if a := trayApp(); a != nil {
				go a.openDashboard()
			}
		case wmRButtonUp:
			showTrayMenu(hwnd)
		}
		return 0
	case wmClose:
		removeTray()
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	if trayTaskbarCreated != 0 && uint32(msg) == trayTaskbarCreated {
		trayState.Lock()
		if a := trayState.app; a != nil {
			d := a.trayNotifyData(nifMessage | nifIcon | nifTip)
			procShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&d)))
		}
		trayState.Unlock()
		return 0
	}
	r, _, _ := procDefWindowProc.Call(hwnd, msg, wparam, lparam)
	return r
}

func trayApp() *App {
	trayState.Lock()
	defer trayState.Unlock()
	return trayState.app
}

// removeTray deletes the icon so no ghost stays behind after Omni exits.
func removeTray() {
	trayState.Lock()
	defer trayState.Unlock()
	a := trayState.app
	if a == nil {
		return
	}
	d := a.trayNotifyData(0)
	procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&d)))
	trayState.app = nil
}

// closeTray removes the icon and ends the tray's message loop.
func closeTray() {
	trayState.Lock()
	hwnd := trayState.hwnd
	trayState.Unlock()
	removeTray()
	if hwnd != 0 {
		procPostMessage.Call(hwnd, wmClose, 0, 0)
	}
}

type trayService struct{ name, title string }

func (a *App) trayServices() []trayService {
	cfg := a.config()
	var out []trayService
	if installed(a.apacheBin(cfg)) {
		out = append(out, trayService{"apache", "Apache + PHP"})
	}
	if installed(a.mysqlBin(cfg)) {
		title := "MySQL"
		if cfg.MySQL.Flavor == "mariadb" {
			title = "MariaDB"
		}
		out = append(out, trayService{"mysql", title})
	}
	if installed(a.mailpitBin(cfg)) {
		out = append(out, trayService{"mailpit", "Mailpit"})
	}
	if installed(a.redisBin(cfg)) {
		out = append(out, trayService{"redis", "Redis"})
	}
	return out
}

func (a *App) trayProc(name string) *Proc {
	return map[string]*Proc{"apache": a.apache, "mysql": a.mysql, "mailpit": a.mailpit, "redis": a.redis}[name]
}

func showTrayMenu(hwnd uintptr) {
	a := trayApp()
	if a == nil {
		return
	}
	actions := map[uintptr]func(){}
	next := uintptr(100)
	add := func(menu uintptr, label string, fn func()) {
		p, _ := syscall.UTF16PtrFromString(label)
		flags := uintptr(mfString)
		if fn == nil {
			flags |= mfGrayed
		} else {
			actions[next] = fn
		}
		procAppendMenu.Call(menu, flags, next, uintptr(unsafe.Pointer(p)))
		next++
	}
	sep := func(menu uintptr) { procAppendMenu.Call(menu, mfSeparator, 0, 0) }
	service := func(name, action string) func() {
		return func() {
			if err := a.serviceAction(name, action); err != nil {
				a.trayBalloon("Omni Web Server", err.Error())
			}
		}
	}

	menu, _, _ := procCreatePopupMenu.Call()
	defer procDestroyMenu.Call(menu)
	add(menu, "Open dashboard", func() { a.openDashboard() })
	procSetMenuDefaultItem.Call(menu, 100, 0)
	sep(menu)
	add(menu, "Start all", service("all", "start"))
	add(menu, "Stop all", service("all", "stop"))
	add(menu, "Restart all", service("all", "restart"))
	sep(menu)
	for _, s := range a.trayServices() {
		sub, _, _ := procCreatePopupMenu.Call()
		state := a.trayProc(s.name).Status().State
		add(sub, "Start", service(s.name, "start"))
		add(sub, "Stop", service(s.name, "stop"))
		add(sub, "Restart", service(s.name, "restart"))
		label, _ := syscall.UTF16PtrFromString(fmt.Sprintf("%s (%s)", s.title, state))
		procAppendMenu.Call(menu, mfPopup, sub, uintptr(unsafe.Pointer(label)))
	}
	sep(menu)
	cfg := a.config()
	add(menu, "Open projects folder", func() { _ = openFolder(a.abs(cfg.WWWDir)) })
	add(menu, "Open logs folder", func() { _ = openFolder(a.path("logs")) })
	sep(menu)
	add(menu, "Quit Omni Web Server", func() { a.Shutdown() })

	var pt struct{ X, Y int32 }
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// Without this the menu does not close when clicking elsewhere.
	procSetForegroundWindow.Call(hwnd)
	cmd, _, _ := procTrackPopupMenu.Call(menu, tpmRightButton|tpmReturnCmd|tpmNoNotify, uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0)
	procPostMessage.Call(hwnd, wmNull, 0, 0)
	if fn := actions[cmd]; fn != nil {
		go fn()
	}
}

func (a *App) openDashboard() {
	_ = openURL(fmt.Sprintf("http://localhost:%d/", a.config().DashboardPort))
}

// trayTooltip lists the service states, e.g. "Omni Web Server\nApache + PHP: running".
func (a *App) trayTooltip() string {
	lines := []string{"Omni Web Server"}
	for _, s := range a.trayServices() {
		lines = append(lines, fmt.Sprintf("%s: %s", s.title, a.trayProc(s.name).Status().State))
	}
	return strings.Join(lines, "\n")
}

func (a *App) trayTooltipLoop() {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-a.quit:
			return
		case <-t.C:
		}
		tip := a.trayTooltip()
		trayState.Lock()
		if trayState.app != nil && tip != trayState.tip {
			trayState.tip = tip
			d := a.trayNotifyData(nifTip)
			procShellNotifyIcon.Call(nimModify, uintptr(unsafe.Pointer(&d)))
		}
		trayState.Unlock()
	}
}

// trayBalloon shows a notification next to the tray icon (used for errors).
func (a *App) trayBalloon(title, text string) {
	trayState.Lock()
	defer trayState.Unlock()
	if trayState.app == nil {
		return
	}
	d := a.trayNotifyData(nifInfo)
	copyUTF16(d.SzInfoTitle[:], title)
	copyUTF16(d.SzInfo[:], text)
	d.DwInfoFlags = niifError
	procShellNotifyIcon.Call(nimModify, uintptr(unsafe.Pointer(&d)))
}
