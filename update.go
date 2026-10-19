package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Update check: once a day Omni Web Server asks GitHub for the newest release
// and, when it is newer than this build, shows a banner in the dashboard and an
// entry in the tray menu. Installing stays manual: the link opens the release
// page and the user runs the new Setup.exe.

const (
	releasesAPI  = "https://api.github.com/repos/spasimirkirov/omni-web-server/releases?per_page=20"
	releasesPage = "https://github.com/spasimirkirov/omni-web-server/releases"
)

type UpdateView struct {
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	URL       string `json:"url,omitempty"`
	Notes     string `json:"notes,omitempty"`
	Checked   string `json:"checked,omitempty"`
	Error     string `json:"error,omitempty"`
}

type ghRelease struct {
	Tag        string `json:"tag_name"`
	URL        string `json:"html_url"`
	Body       string `json:"body"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// parseVersion turns "v1.2.3" or "1.2.3-beta" into [1 2 3]; ok is false for
// anything else.
func parseVersion(s string) (v [3]int, ok bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func versionNewer(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

// newestRelease picks the highest version among published releases. While the
// project is in development every release is a pre-release, so those count.
func newestRelease(rels []ghRelease) (ghRelease, [3]int, bool) {
	var best ghRelease
	var bestV [3]int
	found := false
	for _, r := range rels {
		v, ok := parseVersion(r.Tag)
		if r.Draft || !ok {
			continue
		}
		if !found || versionNewer(v, bestV) {
			best, bestV, found = r, v, true
		}
	}
	return best, bestV, found
}

func (a *App) updateView() UpdateView {
	a.mu.Lock()
	defer a.mu.Unlock()
	v := a.upd
	v.Enabled = !a.cfg.SkipUpdateCheck
	return v
}

// checkUpdate asks GitHub for the newest release and records the result.
func (a *App) checkUpdate() UpdateView {
	v := UpdateView{Checked: time.Now().Format(time.RFC3339)}
	if err := a.fetchUpdate(&v); err != nil {
		v.Error = err.Error()
	}
	a.mu.Lock()
	notify := v.Available && a.updNotified != v.Version
	if notify {
		a.updNotified = v.Version
	}
	a.upd = v
	a.mu.Unlock()
	if notify {
		a.trayBalloon("Omni Web Server "+v.Version+" is available", "Open the dashboard or the tray menu to download it.", false)
	}
	return a.updateView()
}

func (a *App) fetchUpdate(v *UpdateView) error {
	resp, err := httpGet(releasesAPI, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var rels []ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rels); err != nil {
		return fmt.Errorf("reading the release list: %v", err)
	}
	cur, ok := parseVersion(appVersion)
	r, latest, found := newestRelease(rels)
	if !ok || !found || !versionNewer(latest, cur) {
		return nil
	}
	v.Available = true
	v.Version = strings.TrimPrefix(r.Tag, "v")
	v.URL = r.URL
	if v.URL == "" {
		v.URL = releasesPage
	}
	v.Notes = r.Body
	if len(v.Notes) > 1500 {
		v.Notes = v.Notes[:1500] + "…"
	}
	return nil
}

// updateLoop checks shortly after start and then once a day.
func (a *App) updateLoop() {
	wait := 15 * time.Second
	for {
		select {
		case <-a.quit:
			return
		case <-time.After(wait):
		}
		wait = 24 * time.Hour
		if !a.config().SkipUpdateCheck {
			a.checkUpdate()
		}
	}
}
