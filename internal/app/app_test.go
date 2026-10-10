package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/usuariodejava/casamod-store/internal/fakegithub"
	"github.com/usuariodejava/casamod-store/internal/github"
	"github.com/usuariodejava/casamod-store/internal/notifier"
	"github.com/usuariodejava/casamod-store/internal/store"
)

// fakeDocker records the restart calls made on a unix socket.
type fakeDocker struct {
	mu       sync.Mutex
	restarts []string
}

func (d *fakeDocker) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.restarts)
}

func startDocker(t *testing.T) (*fakeDocker, string) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "docker.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	d := &fakeDocker{}
	srv := &httptest.Server{Listener: l, Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/restart") {
			d.mu.Lock()
			d.restarts = append(d.restarts, r.URL.Path)
			d.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	})}}
	srv.Start()
	t.Cleanup(srv.Close)
	return d, sock
}

type env struct {
	app    *App
	gh     *fakegithub.Server
	docker *fakeDocker
	casa   string
	modDir string
}

func setup(t *testing.T) *env {
	t.Helper()
	gh := fakegithub.New()
	ghSrv := gh.Start()
	t.Cleanup(ghSrv.Close)
	docker, sock := startDocker(t)
	casa := t.TempDir()
	a, err := New(Options{DataDir: t.TempDir(), CasaMODDir: casa, DockerSocket: sock, RestartDelay: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	a.GH.APIBase = ghSrv.URL + "/api"
	a.GH.CodeloadBase = ghSrv.URL + "/codeload"
	a.GH.RawBase = ghSrv.URL + "/raw"
	return &env{app: a, gh: gh, docker: docker, casa: casa, modDir: filepath.Join(casa, "mod")}
}

// waitRestarts waits for the debounced restarts to land.
func (e *env) waitRestarts(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if e.docker.count() == want && !e.app.Restart.Pending() {
			time.Sleep(60 * time.Millisecond) // make sure no extra restart follows
			if e.docker.count() == want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("restarts = %d, want %d", e.docker.count(), want)
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func noErrs(t *testing.T, errs []error) {
	t.Helper()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func req(id, typ, repo, path string) InstallRequest {
	return InstallRequest{ID: id, Type: typ, Name: id, Source: github.Source{Repo: repo, Path: path}, Origin: store.OriginCatalog}
}

func TestInstallUpdateRemoveFlow(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.gh.Push("me/mods", "main", "init", map[string]string{
		"a/mod.js": "a1", "b/mod.css": "b1", "walls/star/index.html": "star1", "README.md": "x",
	})

	// Two mods in one batch: CasaMOD restarts once. A wallpaper alone: no restart.
	noErrs(t, e.app.Install(ctx, []InstallRequest{req("a", "mod", "me/mods", "a"), req("b", "mod", "me/mods", "b")}))
	e.waitRestarts(t, 1)
	noErrs(t, e.app.Install(ctx, []InstallRequest{req("star", "wallpaper", "me/mods", "walls/star")}))
	e.waitRestarts(t, 1)
	if readFile(t, filepath.Join(e.modDir, "a/mod.js")) != "a1" || readFile(t, filepath.Join(e.modDir, "wallpapers/star/index.html")) != "star1" {
		t.Fatal("files not installed")
	}
	// The default branch was resolved and saved.
	if it := e.app.State.Items["mod:a"]; it == nil || it.Source.Branch != "main" || it.Policy != store.PolicyNotify {
		t.Fatalf("bad state: %+v", it)
	}

	// A commit elsewhere in the repository is not an update of "a".
	e.gh.Push("me/mods", "main", "docs", map[string]string{"README.md": "y"})
	if err := e.app.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if e.app.State.Items["mod:a"].HasUpdate() {
		t.Fatal("unrelated commit reported as update")
	}
	// A commit in a/ is.
	e.gh.Push("me/mods", "main", "a v2", map[string]string{"a/mod.js": "a2"})
	if err := e.app.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if !e.app.State.Items["mod:a"].HasUpdate() || e.app.State.Items["mod:b"].HasUpdate() {
		t.Fatal("update not detected correctly")
	}
	// Notify policy: files unchanged until the user updates.
	if readFile(t, filepath.Join(e.modDir, "a/mod.js")) != "a1" {
		t.Fatal("notify policy must not update")
	}
	noErrs(t, e.app.Update(ctx, nil))
	if readFile(t, filepath.Join(e.modDir, "a/mod.js")) != "a2" || e.app.State.Items["mod:a"].HasUpdate() {
		t.Fatal("update not applied")
	}
	e.waitRestarts(t, 1) // updating an existing mod needs no restart

	// Auto policy: the check applies the update.
	if err := e.app.SetPolicy("mod:b", store.PolicyAuto); err != nil {
		t.Fatal(err)
	}
	e.gh.Push("me/mods", "main", "b v2", map[string]string{"b/mod.css": "b2"})
	e.app.Check(ctx)
	if readFile(t, filepath.Join(e.modDir, "b/mod.css")) != "b2" || e.app.State.Items["mod:b"].HasUpdate() {
		t.Fatal("auto update not applied")
	}

	// Remove: wallpaper without restart, mod with one.
	noErrs(t, e.app.Remove([]string{"wallpaper:star"}))
	e.waitRestarts(t, 1)
	noErrs(t, e.app.Remove([]string{"mod:a"}))
	e.waitRestarts(t, 2)
	if _, err := os.Stat(filepath.Join(e.modDir, "a")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("mod folder not removed")
	}
}

func TestStatePersists(t *testing.T) {
	e := setup(t)
	e.gh.Push("me/mods", "main", "init", map[string]string{"a/mod.js": "a1"})
	noErrs(t, e.app.Install(context.Background(), []InstallRequest{req("a", "mod", "me/mods", "a")}))
	data, err := os.ReadFile(filepath.Join(e.app.State.Dir(), "installed.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Items []store.Installed `json:"items"`
	}
	if err := json.Unmarshal(data, &f); err != nil || len(f.Items) != 1 || f.Items[0].SHA == "" {
		t.Fatalf("installed.json: %s (%v)", data, err)
	}
	re, err := store.Load(e.app.State.Dir())
	if err != nil || re.Items["mod:a"] == nil {
		t.Fatalf("reload failed: %v", err)
	}
}

func TestManualFoldersAreProtected(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.gh.Push("me/mods", "main", "init", map[string]string{"a/mod.js": "store"})
	os.MkdirAll(filepath.Join(e.modDir, "a"), 0o755)
	os.WriteFile(filepath.Join(e.modDir, "a/mod.js"), []byte("mine"), 0o644)
	os.MkdirAll(filepath.Join(e.modDir, "wallpapers/handmade"), 0o755)

	manual := e.app.Manual()
	if len(manual) != 2 {
		t.Fatalf("manual = %+v", manual)
	}
	errs := e.app.Install(ctx, []InstallRequest{req("a", "mod", "me/mods", "a")})
	if !errors.Is(errs[0], ErrConflict) {
		t.Fatalf("want conflict, got %v", errs[0])
	}
	if readFile(t, filepath.Join(e.modDir, "a/mod.js")) != "mine" {
		t.Fatal("manual mod overwritten without force")
	}
	if errs := e.app.Remove([]string{"wallpaper:handmade"}); errs[0] == nil {
		t.Fatal("removing a manual folder must fail")
	}
	r := req("a", "mod", "me/mods", "a")
	r.Force = true
	noErrs(t, e.app.Install(ctx, []InstallRequest{r}))
	if readFile(t, filepath.Join(e.modDir, "a/mod.js")) != "store" {
		t.Fatal("force did not replace")
	}
	// Replacing an existing folder is not a new mod: no restart.
	e.waitRestarts(t, 0)
}

func TestPrivateRepoNeedsToken(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.gh.Push("me/private", "main", "init", map[string]string{"m/mod.js": "secret"})
	e.gh.SetPrivate("me/private", true)
	e.gh.Token = "tok"
	errs := e.app.Install(ctx, []InstallRequest{req("m", "mod", "me/private", "m")})
	if !errors.Is(errs[0], github.ErrNotFound) {
		t.Fatalf("want not found, got %v", errs[0])
	}
	e.app.State.Config.Token = "tok"
	noErrs(t, e.app.Install(ctx, []InstallRequest{req("m", "mod", "me/private", "m")}))
	if readFile(t, filepath.Join(e.modDir, "m/mod.js")) != "secret" {
		t.Fatal("private mod not installed")
	}
	if e.gh.Count("/api/repos/me/private/tarball/") != 1 {
		t.Fatal("the API tarball endpoint should be used with a token")
	}
}

func TestRateLimitIsReported(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.gh.Push("me/mods", "main", "init", map[string]string{"a/mod.js": "a1"})
	noErrs(t, e.app.Install(ctx, []InstallRequest{req("a", "mod", "me/mods", "a")}))
	e.gh.RateLimited = true
	if err := e.app.Check(ctx); !errors.Is(err, github.ErrRateLimited) {
		t.Fatalf("want rate limit error, got %v", err)
	}
	if e.app.State.Items["mod:a"].LastError == "" {
		t.Fatal("error not recorded on the item")
	}
}

func TestCatalogs(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.gh.Push("me/catalog", "main", "init", map[string]string{
		"store.json": `{"name":"My store","items":[
			{"id":"live","name":"Live Wallpapers","type":"mod","path":"mods/live","preview":"preview.png","description":"d"},
			{"name":"Star","type":"wallpaper","repo":"me/walls","branch":"main","path":"star"},
			{"id":"../bad","repo":"me/walls"}
		]}`,
		"mods/live/mod.js": "x",
	})
	e.gh.Push("Cp0204/CasaMOD", "main", "init", map[string]string{
		"app/mod/widget-sortable/mod.js": "x", "app/mod/show-dotfiles/mod.js": "y", "app/run.sh": "z",
	})
	// Local mod_store provides descriptions.
	os.MkdirAll(filepath.Join(e.casa, "mod_store/show-dotfiles"), 0o755)
	os.WriteFile(filepath.Join(e.casa, "mod_store/show-dotfiles/mod.json"), []byte(`{"description":"Show dot files","version":"1.0.0","author":"metisro"}`), 0o644)
	e.app.State.Config.Catalogs = []string{"me/catalog", "https://github.com/me/missing"}

	items, sources := e.app.Catalog(ctx, true)
	byID := map[string]store.Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	if len(items) != 4 {
		t.Fatalf("items = %+v", items)
	}
	live := byID["live"]
	if live.Source.Repo != "me/catalog" || live.Source.Path != "mods/live" || live.OriginLabel != "My store" ||
		!strings.HasSuffix(live.Preview, "/me/catalog/HEAD/mods/live/preview.png") {
		t.Fatalf("live = %+v", live)
	}
	if byID["star"].Type != "wallpaper" {
		t.Fatalf("star = %+v", byID["star"])
	}
	sd := byID["show-dotfiles"]
	if sd.Avatar != "https://github.com/Cp0204.png?size=160" || live.Avatar != "https://github.com/me.png?size=160" {
		t.Fatalf("avatars: %q %q", sd.Avatar, live.Avatar)
	}
	if sd.Origin != store.OriginCasaMOD || sd.Description != "Show dot files" || sd.Source.Path != "app/mod/show-dotfiles" {
		t.Fatalf("show-dotfiles = %+v", sd)
	}
	var missingErr bool
	for _, s := range sources {
		if s.Source == "https://github.com/me/missing" && s.Error != "" {
			missingErr = true
		}
	}
	if !missingErr {
		t.Fatalf("missing catalog error not reported: %+v", sources)
	}
	// A source without any item is flagged.
	e.gh.Push("me/empty", "main", "init", map[string]string{"README.md": "x"})
	e.app.State.Config.Catalogs = []string{"me/empty"}
	_, sources = e.app.Catalog(ctx, true)
	if len(sources[0].Warnings) != 1 || sources[0].Warnings[0].Kind != store.WarnEmpty || sources[0].Error != "" {
		t.Fatalf("empty source: %+v", sources[0])
	}
	e.app.State.Config.Catalogs = []string{"me/catalog", "https://github.com/me/missing"}
	// Cached: no new requests.
	before := e.gh.Count("/")
	e.app.Catalog(ctx, false)
	if e.gh.Count("/") != before {
		t.Fatal("catalog not cached")
	}
}

func TestNotifier(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.app.EnsureNotifier()
	dir := filepath.Join(e.modDir, notifier.ID)
	if !notifier.UpToDate(dir) {
		t.Fatal("notifier not installed")
	}
	e.waitRestarts(t, 1)
	e.app.EnsureNotifier() // already there: nothing to do
	e.waitRestarts(t, 1)

	e.gh.Push("me/mods", "main", "init", map[string]string{"a/mod.js": "a1"})
	noErrs(t, e.app.Install(ctx, []InstallRequest{req("a", "mod", "me/mods", "a")}))
	e.gh.Push("me/mods", "main", "v2", map[string]string{"a/mod.js": "a2"})
	e.app.Check(ctx)
	var st notifier.Status
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(dir, notifier.StatusFile))), &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Items) != 1 || st.Items[0].ID != "a" || st.UIPort != 8418 || st.Script != notifier.ScriptVersion() || len(st.Script) != 12 {
		t.Fatalf("status = %+v", st)
	}
	// The notifier is not listed as a manual mod and cannot be installed over.
	for _, m := range e.app.Manual() {
		if m.ID == notifier.ID {
			t.Fatal("notifier listed as manual")
		}
	}
	if errs := e.app.Install(ctx, []InstallRequest{req(notifier.ID, "mod", "me/mods", "a")}); errs[0] == nil {
		t.Fatal("installing over the notifier must fail")
	}
	// Modified files are restored.
	os.WriteFile(filepath.Join(dir, "mod.js"), []byte("tampered"), 0o644)
	e.app.EnsureNotifier()
	if !notifier.UpToDate(dir) {
		t.Fatal("notifier not repaired")
	}
	// Disabling removes it (and restarts CasaMOD).
	e.waitRestarts(t, 2)
	e.app.State.Config.Notifier = false
	e.app.EnsureNotifier()
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("notifier not removed")
	}
	e.waitRestarts(t, 3)
}

func TestRestartFailureIsReported(t *testing.T) {
	e := setup(t)
	e.app.Docker.Socket = filepath.Join(t.TempDir(), "nope.sock")
	e.gh.Push("me/mods", "main", "init", map[string]string{"a/mod.js": "a1"})
	noErrs(t, e.app.Install(context.Background(), []InstallRequest{req("a", "mod", "me/mods", "a")}))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		e.app.mu.Lock()
		info := e.app.restartInfo
		e.app.mu.Unlock()
		if info.Needed {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("failed restart not reported")
}

func TestHTTPGuardAndAPI(t *testing.T) {
	e := setup(t)
	e.gh.Push("me/mods", "main", "init", map[string]string{"a/mod.js": "a1"})
	srv := httptest.NewServer(e.app.Handler(fstest.MapFS{"index.html": {Data: []byte("ui")}}))
	defer srv.Close()

	body := `{"items":[{"id":"a","type":"mod","source":{"repo":"me/mods","path":"a"}}]}`
	resp, _ := http.Post(srv.URL+"/api/install", "application/json", strings.NewReader(body))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing header: status %d", resp.StatusCode)
	}
	post := func(path, body string) map[string]any {
		r, _ := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
		r.Header.Set("X-CasaMOD-Store", "1")
		r.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		return out
	}
	out := post("/api/install", body)
	res := out["results"].([]any)[0].(map[string]any)
	if res["ok"] != true || res["key"] != "mod:a" {
		t.Fatalf("install: %+v", out)
	}
	out = post("/api/settings", `{"github_token":"secret","check_interval_hours":12}`)
	if out["ok"] != true {
		t.Fatalf("settings: %+v", out)
	}
	resp, _ = http.Get(srv.URL + "/api/state")
	var st map[string]any
	json.NewDecoder(resp.Body).Decode(&st)
	raw, _ := json.Marshal(st)
	if strings.Contains(string(raw), "secret") {
		t.Fatal("token leaked in /api/state")
	}
	cfg := st["config"].(map[string]any)
	if cfg["has_token"] != true || cfg["check_interval_hours"] != 12.0 {
		t.Fatalf("config: %+v", cfg)
	}
	resp, _ = http.Get(srv.URL + "/api/notifier/report?event=self_update&result=error&detail=HTTP%20401%0Abad")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/gif" {
		t.Fatalf("report: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if l := e.app.log[len(e.app.log)-1]; l.Level != "error" || l.Message != "Asking CasaOS to update CasaMOD Store failed: HTTP 401 bad" {
		t.Fatalf("report log: %+v", l)
	}
	out = post("/api/resolve", `{"url":"https://github.com/me/walls/tree/main/star-wallpaper"}`)
	if out["id"] != "star-wallpaper" || out["type"] != "wallpaper" {
		t.Fatalf("resolve: %+v", out)
	}
	if out = post("/api/settings", `{"check_interval_hours":-1}`); out["error"] == nil {
		t.Fatal("invalid interval accepted")
	}
	resp, _ = http.Get(srv.URL + "/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("index: %d", resp.StatusCode)
	}
}

func TestSelfUpdate(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.app.EnsureNotifier()
	statusItems := func() []notifier.StatusItem {
		var st notifier.Status
		json.Unmarshal([]byte(readFile(t, filepath.Join(e.modDir, notifier.ID, notifier.StatusFile))), &st)
		return st.Items
	}

	// Local builds (no commit) never look for a new version.
	e.app.Check(ctx)
	if n := e.gh.Count("/api/repos/" + SelfRepo); n != 0 {
		t.Fatalf("dev build queried GitHub %d times", n)
	}

	e.app.Commit = "aaa"
	e.gh.Push(SelfRepo, "main", "init", map[string]string{"go.mod": "x"})
	e.gh.SetTag(SelfRepo, SelfTag, "aaa")
	e.app.Check(ctx)
	if v := e.app.selfViewLocked(); v.HasUpdate || v.LatestSHA != "aaa" {
		t.Fatalf("running the newest image: %+v", v)
	}
	// Other tags do not count.
	e.gh.SetTag(SelfRepo, "v9", "ccc")
	e.gh.SetTag(SelfRepo, SelfTag, "bbb")
	e.app.Check(ctx)
	if v := e.app.selfViewLocked(); !v.HasUpdate || v.LatestSHA != "bbb" {
		t.Fatalf("new image not detected: %+v", v)
	}
	if items := statusItems(); len(items) != 1 || items[0].Type != "store" || items[0].Latest != "bbb" {
		t.Fatalf("notifier status: %+v", items)
	}
	// "auto": the notice hides the store and tells the mod to update it.
	if err := e.app.SetPolicy(SelfKey, store.PolicyAuto); err != nil {
		t.Fatal(err)
	}
	var st notifier.Status
	json.Unmarshal([]byte(readFile(t, filepath.Join(e.modDir, notifier.ID, notifier.StatusFile))), &st)
	if len(st.Items) != 0 || st.SelfUpdate == nil || st.SelfUpdate.AppID != "casamod-store" || st.SelfUpdate.Latest != "bbb" {
		t.Fatalf("auto status: %+v", st)
	}
	if v := e.app.selfViewLocked(); v.Policy != store.PolicyAuto || v.Key != SelfKey {
		t.Fatalf("view: %+v", v)
	}
	e.app.SetPolicy(SelfKey, store.PolicyNotify)
	// Survives a restart of the store.
	re, err := store.Load(e.app.State.Dir())
	if err != nil || re.Self.LatestSHA != "bbb" {
		t.Fatalf("self state not saved: %+v %v", re.Self, err)
	}
	// After the container is updated the notice goes away.
	e.app.Commit = "bbb"
	e.app.Check(ctx)
	if e.app.selfViewLocked().HasUpdate || len(statusItems()) != 0 {
		t.Fatal("update still reported after updating")
	}
}
