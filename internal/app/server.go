package app

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/usuariodejava/casamod-store/internal/github"
	"github.com/usuariodejava/casamod-store/internal/store"
)

// Handler returns the HTTP handler: the JSON API under /api/ and the web UI.
func (a *App) Handler(webFS fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", a.handleState)
	mux.HandleFunc("GET /api/store", a.handleStore)
	mux.HandleFunc("POST /api/resolve", a.handleResolve)
	mux.HandleFunc("POST /api/install", a.handleInstall)
	mux.HandleFunc("POST /api/remove", a.handleRemove)
	mux.HandleFunc("POST /api/update", a.handleUpdate)
	mux.HandleFunc("POST /api/check", a.handleCheck)
	mux.HandleFunc("POST /api/policy", a.handlePolicy)
	mux.HandleFunc("POST /api/settings", a.handleSettings)
	mux.HandleFunc("POST /api/restart", a.handleRestart)
	mux.HandleFunc("GET /api/notifier/report", a.handleNotifierReport)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	files := http.FileServer(http.FS(webFS))
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	}))
	return guard(mux)
}

// guard rejects state changing requests that do not carry the custom header
// the web UI sends. Browsers cannot add it cross origin without a CORS
// preflight (which is never answered), so other web pages cannot drive the API.
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get("X-CasaMOD-Store") != "1" {
				writeErr(w, http.StatusForbidden, errors.New("missing X-CasaMOD-Store header"))
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errors.New("invalid request: " + err.Error())
	}
	return nil
}

// opContext detaches long operations from the request: closing the browser
// tab must not abort an install half way.
func opContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Minute)
}

type installedView struct {
	*store.Installed
	Key       string `json:"key"`
	HasUpdate bool   `json:"has_update"`
	Missing   bool   `json:"missing"`
}

type configView struct {
	HasToken           bool     `json:"has_token"`
	CheckIntervalHours float64  `json:"check_interval_hours"`
	Catalogs           []string `json:"catalogs"`
	CasaMODContainer   string   `json:"casamod_container"`
	IncludeCasaMODMods bool     `json:"include_casamod_mods"`
	Notifier           bool     `json:"notifier"`
	UIPort             int      `json:"ui_port"`
	PublicURL          string   `json:"public_url"`
}

func (a *App) handleState(w http.ResponseWriter, r *http.Request) {
	a.State.Lock()
	cfg := a.State.Config
	var items []installedView
	for _, it := range a.State.SortedLocked() {
		cp := *it
		items = append(items, installedView{Installed: &cp, Key: it.Key(), HasUpdate: it.HasUpdate()})
	}
	lastCheck := a.State.LastCheck
	self := a.selfViewLocked()
	a.State.Unlock()
	for i := range items {
		items[i].Missing = !a.Inst.Exists(items[i].Type, items[i].ID)
	}
	var next *time.Time
	if cfg.CheckIntervalHours > 0 && !lastCheck.IsZero() {
		t := lastCheck.Add(time.Duration(cfg.CheckIntervalHours * float64(time.Hour)))
		next = &t
	}
	a.mu.Lock()
	busy := a.busy
	logCopy := append([]LogEntry(nil), a.log...)
	restart := a.restartInfo
	a.mu.Unlock()
	restart.Pending = restart.Pending || a.Restart.Pending()
	var last *time.Time
	if !lastCheck.IsZero() {
		last = &lastCheck
	}
	if items == nil {
		items = []installedView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":          a.Version,
		"self":             self,
		"busy":             busy,
		"restart":          restart,
		"docker_available": a.Docker.Available(),
		"last_check":       last,
		"next_check":       next,
		"installed":        items,
		"manual":           a.Manual(),
		"log":              logCopy,
		"config": configView{
			HasToken: cfg.Token != "", CheckIntervalHours: cfg.CheckIntervalHours, Catalogs: cfg.Catalogs,
			CasaMODContainer: cfg.CasaMODContainer, IncludeCasaMODMods: cfg.IncludeCasaMODMods,
			Notifier: cfg.Notifier, UIPort: cfg.UIPort, PublicURL: cfg.PublicURL,
		},
	})
}

func (a *App) handleStore(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	items, sources := a.Catalog(ctx, r.URL.Query().Get("refresh") == "1")
	if items == nil {
		items = []store.Item{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "sources": sources})
}

func (a *App) handleResolve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	src, err := github.ParseURL(req.URL)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	typ := "mod"
	if strings.Contains(strings.ToLower(src.Path+" "+src.Repo), "wallpaper") {
		typ = "wallpaper"
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": src, "id": store.DefaultID(src), "type": typ})
}

type result struct {
	Key      string `json:"key"`
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Conflict bool   `json:"conflict,omitempty"`
}

func results(keys []string, errs []error) []result {
	out := make([]result, len(keys))
	for i, err := range errs {
		out[i] = result{Key: keys[i], OK: err == nil}
		if err != nil {
			out[i].Error = err.Error()
			out[i].Conflict = errors.Is(err, ErrConflict)
		}
	}
	return out
}

func (a *App) handleInstall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Items []InstallRequest `json:"items"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := opContext()
	defer cancel()
	errs := a.Install(ctx, req.Items)
	keys := make([]string, len(req.Items))
	for i, it := range req.Items {
		typ := strings.ToLower(strings.TrimSpace(it.Type))
		if typ == "" {
			typ = "mod"
		}
		keys[i] = store.Key(typ, strings.TrimSpace(it.ID))
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results(keys, errs)})
}

func (a *App) handleRemove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Keys []string `json:"keys"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results(req.Keys, a.Remove(req.Keys))})
}

func (a *App) handleUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Keys []string `json:"keys"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := opContext()
	defer cancel()
	keys := req.Keys
	if len(keys) == 0 {
		a.State.Lock()
		for _, it := range a.State.SortedLocked() {
			if it.HasUpdate() {
				keys = append(keys, it.Key())
			}
		}
		a.State.Unlock()
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results(keys, a.Update(ctx, keys))})
}

func (a *App) handleCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := opContext()
	defer cancel()
	if err := a.Check(ctx); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) handlePolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key    string `json:"key"`
		Policy string `json:"policy"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := a.SetPolicy(req.Key, req.Policy); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) handleSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token              *string   `json:"github_token"`
		ClearToken         bool      `json:"clear_token"`
		CheckIntervalHours *float64  `json:"check_interval_hours"`
		Catalogs           *[]string `json:"catalogs"`
		CasaMODContainer   *string   `json:"casamod_container"`
		IncludeCasaMODMods *bool     `json:"include_casamod_mods"`
		Notifier           *bool     `json:"notifier"`
		UIPort             *int      `json:"ui_port"`
		PublicURL          *string   `json:"public_url"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.CheckIntervalHours != nil && (*req.CheckIntervalHours < 0 || *req.CheckIntervalHours > 24*30) {
		writeErr(w, http.StatusBadRequest, errors.New("the check interval must be between 0 (off) and 720 hours"))
		return
	}
	if req.UIPort != nil && (*req.UIPort < 1 || *req.UIPort > 65535) {
		writeErr(w, http.StatusBadRequest, errors.New("invalid port"))
		return
	}
	var catalogs []string
	if req.Catalogs != nil {
		for _, c := range *req.Catalogs {
			c = strings.TrimSpace(c)
			if c == "" {
				continue
			}
			if _, err := store.ParseCatalogRef(c); err != nil {
				writeErr(w, http.StatusBadRequest, errors.New("catalog "+c+": "+err.Error()))
				return
			}
			catalogs = append(catalogs, c)
		}
	}
	if req.PublicURL != nil {
		u := strings.TrimSpace(*req.PublicURL)
		if u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			writeErr(w, http.StatusBadRequest, errors.New("the store address must start with http:// or https://"))
			return
		}
		req.PublicURL = &u
	}
	a.State.Lock()
	c := &a.State.Config
	oldNotifier := c.Notifier
	if req.ClearToken {
		c.Token = ""
	} else if req.Token != nil && strings.TrimSpace(*req.Token) != "" {
		c.Token = strings.TrimSpace(*req.Token)
	}
	if req.CheckIntervalHours != nil {
		c.CheckIntervalHours = *req.CheckIntervalHours
	}
	if req.Catalogs != nil {
		c.Catalogs = catalogs
	}
	if req.CasaMODContainer != nil && strings.TrimSpace(*req.CasaMODContainer) != "" {
		c.CasaMODContainer = strings.TrimSpace(*req.CasaMODContainer)
	}
	if req.IncludeCasaMODMods != nil {
		c.IncludeCasaMODMods = *req.IncludeCasaMODMods
	}
	if req.Notifier != nil {
		c.Notifier = *req.Notifier
	}
	if req.UIPort != nil {
		c.UIPort = *req.UIPort
	}
	if req.PublicURL != nil {
		c.PublicURL = *req.PublicURL
	}
	newNotifier := c.Notifier
	err := a.State.SaveLocked()
	a.State.Unlock()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	a.InvalidateCatalog()
	a.Reschedule()
	if oldNotifier != newNotifier {
		a.op.Lock()
		a.EnsureNotifier()
		a.op.Unlock()
	} else {
		a.writeNotifierStatus()
	}
	a.Logf("info", "Settings saved")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) handleRestart(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if err := a.Restart.Now(ctx); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleNotifierReport records what happened when the notifier mini-mod
// asked CasaOS to update the store (it can only reach us with a plain GET,
// as an image, from the CasaOS page). It only writes to the activity log.
func (a *App) handleNotifierReport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	detail := q.Get("detail")
	if len(detail) > 200 {
		detail = detail[:200]
	}
	detail = strings.Map(func(c rune) rune {
		if c < 32 {
			return ' '
		}
		return c
	}, detail)
	if q.Get("event") == "self_update" {
		if q.Get("result") == "ok" {
			a.Logf("info", "CasaOS was asked to update CasaMOD Store: %s", detail)
		} else {
			a.Logf("error", "Asking CasaOS to update CasaMOD Store failed: %s", detail)
		}
	}
	w.Header().Set("Content-Type", "image/gif")
	w.Header().Set("Cache-Control", "no-store")
	// 1x1 transparent GIF.
	w.Write([]byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\x00\x00\x00!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;"))
}
