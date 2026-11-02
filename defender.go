package main

import (
	"runtime"
)

// "Speed up (Windows Defender)": exclusions for Omni's folders and processes.
// Never applied automatically. The status needs administrator rights to read;
// without them the dashboard says "unknown" and shows what Omni last did.

type DefenderView struct {
	Supported     bool     `json:"supported"`
	State         string   `json:"state"` // applied | partial | none | unknown | unsupported
	AppliedByOmni bool     `json:"applied_by_omni"`
	Admin         bool     `json:"admin"`
	Paths         []string `json:"paths"`
	Processes     []string `json:"processes"`
}

func (a *App) defenderView() DefenderView {
	cfg := a.config()
	paths, procs := a.defenderTargets(cfg)
	v := DefenderView{Supported: runtime.GOOS == "windows", State: "unsupported", AppliedByOmni: cfg.Tuning.DefenderApplied,
		Admin: isAdmin(), Paths: paths, Processes: procs}
	if v.Supported {
		v.State = defenderStatus(paths, procs)
	}
	return v
}

// defenderAction runs "apply" or "remove" through an elevated PowerShell (UAC prompt).
func (a *App) defenderAction(action string) error {
	if runtime.GOOS != "windows" {
		return badRequest("Windows Defender exclusions exist only on Windows")
	}
	if action != "apply" && action != "remove" {
		return badRequest("unknown action %q", action)
	}
	cfg := a.config()
	paths, procs := a.defenderTargets(cfg)
	if err := defenderRun(action, paths, procs); err != nil {
		return err
	}
	a.mu.Lock()
	a.cfg.Tuning.DefenderApplied = action == "apply"
	err := saveConfig(a.root, a.cfg)
	a.mu.Unlock()
	return err
}
