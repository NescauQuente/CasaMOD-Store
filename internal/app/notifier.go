package app

import (
	"path/filepath"
	"time"

	"github.com/usuariodejava/casamod-store/internal/installer"
	"github.com/usuariodejava/casamod-store/internal/notifier"
	"github.com/usuariodejava/casamod-store/internal/store"
)

// EnsureNotifier installs, updates or removes the notifier mini-mod according
// to the settings. Updating its files needs no restart; adding or removing
// the folder does.
func (a *App) EnsureNotifier() {
	a.State.Lock()
	enabled := a.State.Config.Notifier
	a.State.Unlock()
	dir := a.Inst.Dir(installer.TypeMod, notifier.ID)
	if !enabled {
		if a.Inst.Exists(installer.TypeMod, notifier.ID) {
			if err := a.Inst.Remove(installer.TypeMod, notifier.ID); err != nil {
				a.Logf("error", "Removing the notifier mod failed: %v", err)
				return
			}
			a.Logf("info", "Notifier mod removed")
			a.requestRestart()
		}
		return
	}
	if !notifier.UpToDate(dir) {
		created, err := a.Inst.InstallFiles(installer.TypeMod, notifier.ID, notifier.Files())
		if err != nil {
			a.Logf("error", "Installing the notifier mod failed: %v", err)
			return
		}
		if created {
			a.Logf("info", "Notifier mod installed")
			a.requestRestart()
		}
	}
	a.writeNotifierStatus()
}

// writeNotifierStatus writes status.json for the notifier mini-mod.
func (a *App) writeNotifierStatus() {
	dir := a.Inst.Dir(installer.TypeMod, notifier.ID)
	if !a.Inst.Exists(installer.TypeMod, notifier.ID) {
		return
	}
	a.State.Lock()
	st := notifier.Status{Version: 1, UIPort: a.State.Config.UIPort, UIURL: a.State.Config.PublicURL, Items: []notifier.StatusItem{}}
	if !a.State.LastCheck.IsZero() {
		st.CheckedAt = a.State.LastCheck.Format(time.RFC3339)
	}
	if selfHasUpdate(a.Commit, a.State.Self) {
		if selfPolicy(a.State.Config) == store.PolicyAuto {
			// The mod updates the app; no notice needed.
			st.SelfUpdate = &notifier.SelfUpdate{AppID: SelfAppID, Latest: a.State.Self.LatestSHA}
		} else {
			st.Items = append(st.Items, notifier.StatusItem{ID: "casamod-store", Type: "store", Name: "CasaMOD Store", Latest: a.State.Self.LatestSHA})
		}
	}
	for _, it := range a.State.SortedLocked() {
		if it.HasUpdate() {
			st.Items = append(st.Items, notifier.StatusItem{ID: it.ID, Type: it.Type, Name: it.Name, Latest: it.LatestSHA})
		}
	}
	a.State.Unlock()
	if err := store.WriteJSONFile(filepath.Join(dir, notifier.StatusFile), st); err != nil {
		a.Logf("error", "Writing the notifier status failed: %v", err)
	}
}
