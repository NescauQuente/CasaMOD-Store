// Package app ties everything together: install, update and remove items,
// periodic update checks, the CasaMOD restart and the notifier mini-mod.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/usuariodejava/casamod-store/internal/docker"
	"github.com/usuariodejava/casamod-store/internal/github"
	"github.com/usuariodejava/casamod-store/internal/installer"
	"github.com/usuariodejava/casamod-store/internal/notifier"
	"github.com/usuariodejava/casamod-store/internal/store"
)

// App is the running service.
type App struct {
	State       *store.State
	GH          *github.Client
	Inst        *installer.Installer
	Docker      *docker.Client
	Restart     *docker.Restarter
	ModStoreDir string
	Version     string
	Commit      string // commit the binary was built from ("" for local builds)

	op sync.Mutex // one install/update/remove/check at a time

	mu          sync.Mutex
	busy        string
	catalog     *catalogCache
	log         []LogEntry
	restartInfo RestartInfo
	wake        chan struct{}
}

// LogEntry is shown in the activity panel of the web UI.
type LogEntry struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"` // info | error
	Message string    `json:"message"`
}

// RestartInfo tells the UI whether CasaMOD still has to be restarted.
type RestartInfo struct {
	Pending   bool      `json:"pending"` // a restart is scheduled
	Needed    bool      `json:"needed"`  // the last automatic restart failed
	LastTime  time.Time `json:"last_time,omitempty"`
	LastError string    `json:"last_error,omitempty"`
}

type catalogCache struct {
	fetched time.Time
	items   []store.Item
	sources []SourceStatus
}

// SourceStatus reports how a store source loaded.
type SourceStatus struct {
	Source   string          `json:"source"`
	Label    string          `json:"label"`
	Count    int             `json:"count"`
	Error    string          `json:"error,omitempty"`
	Warnings []store.Warning `json:"warnings,omitempty"`
}

// Options configure New.
type Options struct {
	DataDir      string
	CasaMODDir   string // /DATA/AppData/casamod
	DockerSocket string
	Version      string
	Commit       string
	RestartDelay time.Duration
}

// New loads the state and prepares the service.
func New(opt Options) (*App, error) {
	st, err := store.Load(opt.DataDir)
	if err != nil {
		return nil, fmt.Errorf("loading state: %w", err)
	}
	a := &App{
		State:       st,
		Inst:        &installer.Installer{ModDir: filepath.Join(opt.CasaMODDir, "mod")},
		Docker:      docker.New(opt.DockerSocket),
		ModStoreDir: filepath.Join(opt.CasaMODDir, "mod_store"),
		Version:     opt.Version,
		Commit:      opt.Commit,
		wake:        make(chan struct{}, 1),
	}
	a.GH = github.New(st.Token)
	if opt.RestartDelay == 0 {
		opt.RestartDelay = 3 * time.Second
	}
	a.Restart = &docker.Restarter{
		Docker: a.Docker,
		Delay:  opt.RestartDelay,
		Container: func() string {
			st.Lock()
			defer st.Unlock()
			return st.Config.CasaMODContainer
		},
		OnResult: a.restartDone,
	}
	if err := os.MkdirAll(a.Inst.ModDir, 0o755); err != nil {
		return nil, err
	}
	a.Inst.Recover()
	return a, nil
}

// Logf records an activity line (and prints it).
func (a *App) Logf(level, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Printf("[%s] %s", level, msg)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.log = append(a.log, LogEntry{Time: time.Now(), Level: level, Message: msg})
	if len(a.log) > 100 {
		a.log = a.log[len(a.log)-100:]
	}
}

func (a *App) setBusy(s string) {
	a.mu.Lock()
	a.busy = s
	a.mu.Unlock()
}

func (a *App) requestRestart() {
	a.mu.Lock()
	a.restartInfo.Pending = true
	a.mu.Unlock()
	a.Restart.Request()
}

func (a *App) restartDone(err error) {
	a.mu.Lock()
	a.restartInfo.Pending = false
	a.restartInfo.LastTime = time.Now()
	if err != nil {
		a.restartInfo.Needed = true
		a.restartInfo.LastError = err.Error()
	} else {
		a.restartInfo.Needed = false
		a.restartInfo.LastError = ""
	}
	a.mu.Unlock()
	if err != nil {
		a.Logf("error", "Restarting CasaMOD failed: %v", err)
	} else {
		a.Logf("info", "CasaMOD restarted")
	}
}

// ErrConflict means the target folder exists but was not installed by the
// store (or came from another source). The UI asks before replacing it.
var ErrConflict = errors.New("conflict")

// ConflictError carries the reason of a conflict.
type ConflictError struct{ Msg string }

func (e *ConflictError) Error() string { return e.Msg }
func (e *ConflictError) Unwrap() error { return ErrConflict }

// InstallRequest asks to install one item.
type InstallRequest struct {
	ID          string        `json:"id"`
	Type        string        `json:"type"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Source      github.Source `json:"source"`
	Origin      string        `json:"origin"`
	Policy      string        `json:"policy"`
	Force       bool          `json:"force"`
}

// Install installs (or reinstalls) items. A CasaMOD restart is scheduled
// once if any new mod folder was created.
func (a *App) Install(ctx context.Context, reqs []InstallRequest) []error {
	a.op.Lock()
	defer a.op.Unlock()
	defer a.setBusy("")
	errs := make([]error, len(reqs))
	restart := false
	for i, r := range reqs {
		a.setBusy("Installing " + r.ID)
		created, err := a.install(ctx, r)
		if err != nil {
			if !errors.Is(err, ErrConflict) {
				a.Logf("error", "Installing %s failed: %v", r.ID, err)
			}
			errs[i] = err
			continue
		}
		if created && r.Type == installer.TypeMod {
			restart = true
		}
	}
	if restart {
		a.requestRestart()
	}
	a.writeNotifierStatus()
	return errs
}

func (a *App) install(ctx context.Context, r InstallRequest) (bool, error) {
	r.Type = strings.ToLower(strings.TrimSpace(r.Type))
	if r.Type == "" {
		r.Type = installer.TypeMod
	}
	r.ID = strings.TrimSpace(r.ID)
	r.Source = r.Source.Normalize()
	if err := installer.ValidateID(r.Type, r.ID); err != nil {
		return false, err
	}
	if err := r.Source.Validate(); err != nil {
		return false, err
	}
	if r.Type == installer.TypeMod && r.ID == notifier.ID {
		return false, fmt.Errorf("%q is used by the store itself", r.ID)
	}
	key := store.Key(r.Type, r.ID)

	a.State.Lock()
	prev := a.State.Items[key]
	a.State.Unlock()
	if !r.Force {
		if prev == nil && a.Inst.Exists(r.Type, r.ID) {
			return false, &ConflictError{fmt.Sprintf("A folder named %q already exists and was not installed by the store. Replace it?", r.ID)}
		}
		if prev != nil && (prev.Source.Repo != r.Source.Repo || prev.Source.Path != r.Source.Path) {
			return false, &ConflictError{fmt.Sprintf("%q is already installed from %s. Replace it with %s?", r.ID, prev.Source, r.Source)}
		}
	}
	if r.Source.Branch == "" {
		b, err := a.GH.DefaultBranch(ctx, r.Source.Repo)
		if err != nil {
			return false, err
		}
		r.Source.Branch = b
	}
	commit, err := a.GH.LatestCommit(ctx, r.Source)
	if err != nil {
		return false, err
	}
	created, err := a.Inst.Install(ctx, r.Type, r.ID, r.Source.Path, func(ctx context.Context) (io.ReadCloser, error) {
		return a.GH.Tarball(ctx, r.Source.Repo, commit.SHA)
	})
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	it := &store.Installed{
		ID: r.ID, Type: r.Type, Name: firstNonEmpty(r.Name, r.ID), Description: r.Description,
		Origin: firstNonEmpty(r.Origin, store.OriginURL), Source: r.Source, SHA: commit.SHA,
		Policy: store.PolicyNotify, InstalledAt: now, UpdatedAt: now,
		LatestSHA: commit.SHA, LatestDate: commit.Date, LatestMessage: commit.Message, CheckedAt: now,
	}
	if r.Policy == store.PolicyAuto {
		it.Policy = store.PolicyAuto
	}
	a.State.Lock()
	if prev != nil {
		it.InstalledAt = prev.InstalledAt
		if r.Policy == "" {
			it.Policy = prev.Policy
		}
	}
	a.State.Items[key] = it
	err = a.State.SaveLocked()
	a.State.Unlock()
	verb := "Installed"
	if prev != nil {
		verb = "Reinstalled"
	}
	a.Logf("info", "%s %s %s from %s (%s)", verb, r.Type, r.ID, r.Source, short(commit.SHA))
	return created, err
}

// Remove deletes installed items. Folders the store did not install are never touched.
func (a *App) Remove(keys []string) []error {
	a.op.Lock()
	defer a.op.Unlock()
	errs := make([]error, len(keys))
	restart := false
	for i, key := range keys {
		a.State.Lock()
		it := a.State.Items[key]
		a.State.Unlock()
		if it == nil {
			errs[i] = fmt.Errorf("%s is not installed by the store", key)
			continue
		}
		existed := a.Inst.Exists(it.Type, it.ID)
		if err := a.Inst.Remove(it.Type, it.ID); err != nil {
			errs[i] = err
			a.Logf("error", "Removing %s failed: %v", it.ID, err)
			continue
		}
		a.State.Lock()
		delete(a.State.Items, key)
		errs[i] = a.State.SaveLocked()
		a.State.Unlock()
		a.Logf("info", "Removed %s %s", it.Type, it.ID)
		if existed && it.Type == installer.TypeMod {
			restart = true
		}
	}
	if restart {
		a.requestRestart()
	}
	a.writeNotifierStatus()
	return errs
}

// SetPolicy changes the update policy of an item.
func (a *App) SetPolicy(key, policy string) error {
	if policy != store.PolicyAuto && policy != store.PolicyNotify {
		return fmt.Errorf("invalid policy %q", policy)
	}
	a.State.Lock()
	if key == SelfKey {
		a.State.Config.SelfPolicy = policy
		err := a.State.SaveLocked()
		a.State.Unlock()
		a.writeNotifierStatus()
		return err
	}
	defer a.State.Unlock()
	it := a.State.Items[key]
	if it == nil {
		return fmt.Errorf("%s is not installed", key)
	}
	it.Policy = policy
	return a.State.SaveLocked()
}

// Update installs the latest version of the given items (all items with a
// known update when keys is empty). Updating files of an existing mod does
// not need a CasaMOD restart: the files are served live.
func (a *App) Update(ctx context.Context, keys []string) []error {
	a.op.Lock()
	defer a.op.Unlock()
	defer a.setBusy("")
	if len(keys) == 0 {
		a.State.Lock()
		for _, it := range a.State.SortedLocked() {
			if it.HasUpdate() {
				keys = append(keys, it.Key())
			}
		}
		a.State.Unlock()
	}
	errs := make([]error, len(keys))
	restart := false
	for i, key := range keys {
		created, err := a.update(ctx, key, true)
		errs[i] = err
		if created {
			restart = true
		}
	}
	if restart {
		a.requestRestart()
	}
	a.writeNotifierStatus()
	return errs
}

// update fetches the latest commit and reinstalls when it changed (or when
// the folder went missing). The caller holds a.op.
func (a *App) update(ctx context.Context, key string, fresh bool) (created bool, err error) {
	a.State.Lock()
	cur := a.State.Items[key]
	var it store.Installed
	if cur != nil {
		it = *cur
	}
	a.State.Unlock()
	if cur == nil {
		return false, fmt.Errorf("%s is not installed", key)
	}
	a.setBusy("Updating " + it.ID)
	target := it.LatestSHA
	if fresh || target == "" {
		c, err := a.GH.LatestCommit(ctx, it.Source)
		if err != nil {
			a.recordCheck(key, store.Installed{}, err)
			return false, err
		}
		a.recordCheck(key, store.Installed{LatestSHA: c.SHA, LatestDate: c.Date, LatestMessage: c.Message}, nil)
		target = c.SHA
	}
	missing := !a.Inst.Exists(it.Type, it.ID)
	if target == it.SHA && !missing {
		return false, nil
	}
	created, err = a.Inst.Install(ctx, it.Type, it.ID, it.Source.Path, func(ctx context.Context) (io.ReadCloser, error) {
		return a.GH.Tarball(ctx, it.Source.Repo, target)
	})
	if err != nil {
		a.recordCheck(key, store.Installed{}, err)
		a.Logf("error", "Updating %s failed: %v", it.ID, err)
		return false, err
	}
	a.State.Lock()
	if cur := a.State.Items[key]; cur != nil {
		cur.SHA = target
		cur.UpdatedAt = time.Now().UTC()
		cur.LastError = ""
		err = a.State.SaveLocked()
	}
	a.State.Unlock()
	a.Logf("info", "Updated %s %s to %s", it.Type, it.ID, short(target))
	return created && it.Type == installer.TypeMod, err
}

func (a *App) recordCheck(key string, res store.Installed, err error) {
	a.State.Lock()
	defer a.State.Unlock()
	it := a.State.Items[key]
	if it == nil {
		return
	}
	it.CheckedAt = time.Now().UTC()
	if err != nil {
		it.LastError = err.Error()
	} else {
		it.LastError = ""
		it.LatestSHA = res.LatestSHA
		it.LatestDate = res.LatestDate
		it.LatestMessage = res.LatestMessage
	}
	a.State.SaveLocked()
}

// Check looks for updates of every installed item and applies the ones whose
// policy is "auto".
func (a *App) Check(ctx context.Context) error {
	a.op.Lock()
	defer a.op.Unlock()
	defer a.setBusy("")
	a.setBusy("Checking for updates")
	a.State.Lock()
	items := a.State.SortedLocked()
	a.State.Unlock()

	// Items sharing a source share one API call.
	type result struct {
		c   github.Commit
		err error
	}
	cache := map[string]result{}
	var firstErr error
	updates, failed := 0, 0
	restart := false
	for _, it := range items {
		k := it.Source.String()
		res, ok := cache[k]
		if !ok {
			c, err := a.GH.LatestCommit(ctx, it.Source)
			res = result{c, err}
			cache[k] = res
		}
		a.recordCheck(it.Key(), store.Installed{LatestSHA: res.c.SHA, LatestDate: res.c.Date, LatestMessage: res.c.Message}, res.err)
		if res.err != nil {
			failed++
			if firstErr == nil {
				firstErr = res.err
			}
			if errors.Is(res.err, github.ErrRateLimited) {
				break
			}
			continue
		}
		if res.c.SHA == it.SHA {
			continue
		}
		if it.Policy == store.PolicyAuto {
			created, err := a.update(ctx, it.Key(), false)
			if err == nil && created {
				restart = true
			}
			continue
		}
		updates++
	}
	if !errors.Is(firstErr, github.ErrRateLimited) {
		a.checkSelf(ctx)
	}
	a.State.Lock()
	a.State.LastCheck = time.Now().UTC()
	a.State.SaveLocked()
	a.State.Unlock()
	if restart {
		a.requestRestart()
	}
	a.writeNotifierStatus()
	if failed > 0 {
		a.Logf("error", "Update check: %d item(s) could not be checked: %v", failed, firstErr)
	} else {
		a.Logf("info", "Update check finished: %d update(s) waiting", updates)
	}
	return firstErr
}

// Catalog returns the store items from every source, cached for 30 minutes.
func (a *App) Catalog(ctx context.Context, refresh bool) ([]store.Item, []SourceStatus) {
	a.mu.Lock()
	c := a.catalog
	a.mu.Unlock()
	if c != nil && !refresh && time.Since(c.fetched) < 30*time.Minute {
		return c.items, c.sources
	}
	a.State.Lock()
	catalogs := append([]string(nil), a.State.Config.Catalogs...)
	withCasaMOD := a.State.Config.IncludeCasaMODMods
	a.State.Unlock()

	type part struct {
		items  []store.Item
		status SourceStatus
	}
	parts := make([]part, len(catalogs)+1)
	var wg sync.WaitGroup
	for i, cat := range catalogs {
		wg.Add(1)
		go func(i int, cat string) {
			defer wg.Done()
			label, items, warnings, err := store.LoadCatalog(ctx, a.GH, cat)
			st := SourceStatus{Source: cat, Label: label, Count: len(items), Warnings: warnings}
			if err != nil {
				st.Error = err.Error()
			} else if len(items) == 0 {
				st.Warnings = append(st.Warnings, store.Warning{Kind: store.WarnEmpty})
			}
			parts[i] = part{items, st}
		}(i, cat)
	}
	if withCasaMOD {
		wg.Add(1)
		go func() {
			defer wg.Done()
			items, err := store.CasaMODMods(ctx, a.GH, a.ModStoreDir)
			st := SourceStatus{Source: store.CasaMODRepo, Label: "CasaMOD", Count: len(items)}
			if err != nil {
				st.Error = err.Error()
			}
			parts[len(catalogs)] = part{items, st}
		}()
	}
	wg.Wait()
	var items []store.Item
	var sources []SourceStatus
	seen := map[string]bool{}
	for _, p := range parts {
		if p.status.Source == "" {
			continue
		}
		sources = append(sources, p.status)
		for _, it := range p.items {
			k := store.Key(it.Type, it.ID) + "|" + it.Source.Repo + "|" + it.Source.Path
			if seen[k] {
				continue
			}
			seen[k] = true
			items = append(items, it)
		}
	}
	a.mu.Lock()
	a.catalog = &catalogCache{fetched: time.Now(), items: items, sources: sources}
	a.mu.Unlock()
	return items, sources
}

// InvalidateCatalog drops the cached catalog (after settings change).
func (a *App) InvalidateCatalog() {
	a.mu.Lock()
	a.catalog = nil
	a.mu.Unlock()
}

// ManualItem is a folder in mod/ (or mod/wallpapers/) the store did not install.
type ManualItem struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// Manual lists folders that were installed by hand.
func (a *App) Manual() []ManualItem {
	a.State.Lock()
	defer a.State.Unlock()
	var out []ManualItem
	for _, name := range installer.ListDirs(a.Inst.ModDir) {
		if name == installer.WallpapersDir || name == notifier.ID {
			continue
		}
		if a.State.Items[store.Key(installer.TypeMod, name)] == nil {
			out = append(out, ManualItem{ID: name, Type: installer.TypeMod})
		}
	}
	for _, name := range installer.ListDirs(filepath.Join(a.Inst.ModDir, installer.WallpapersDir)) {
		if a.State.Items[store.Key(installer.TypeWallpaper, name)] == nil {
			out = append(out, ManualItem{ID: name, Type: installer.TypeWallpaper})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type+out[i].ID < out[j].Type+out[j].ID })
	return out
}

// Run performs the periodic update checks until ctx ends.
func (a *App) Run(ctx context.Context) {
	first := time.Minute
	for {
		a.State.Lock()
		interval := time.Duration(a.State.Config.CheckIntervalHours * float64(time.Hour))
		last := a.State.LastCheck
		a.State.Unlock()
		var wait time.Duration
		if interval <= 0 {
			wait = 24 * time.Hour // checks disabled: only re-read the settings
		} else {
			wait = time.Until(last.Add(interval))
			if wait < first {
				wait = first
			}
		}
		first = 0
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-a.wake:
			t.Stop()
			continue
		case <-t.C:
		}
		if interval > 0 {
			cctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
			a.Check(cctx)
			cancel()
		}
	}
}

// Reschedule makes Run re-read the interval.
func (a *App) Reschedule() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
