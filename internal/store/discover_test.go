package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/usuariodejava/casamod-store/internal/fakegithub"
	"github.com/usuariodejava/casamod-store/internal/github"
)

func TestDiscover(t *testing.T) {
	gh := fakegithub.New()
	srv := gh.Start()
	defer srv.Close()
	c := github.New(nil)
	c.APIBase, c.RawBase, c.CodeloadBase = srv.URL+"/api", srv.URL+"/raw", srv.URL+"/codeload"

	gh.Push("me/mods", "main", "init", map[string]string{
		"README.md":                          "x",
		"index.html":                         "repo page, not a wallpaper",
		"proj-a/live/mod.js":                 "x",
		"proj-a/live/mod.json":               `{"name":"Live Wallpapers","description":"Animated","version":"1.2.0"}`,
		"proj-a/live/assets/demo/index.html": "inside a mod: not an item",
		"proj-a/tests/mod.js":                "test copy",
		"proj-b/widget/mod.css":              "x",
		"proj-b/widget/preview.webp":         "img",
		".github/x/mod.js":                   "ignored",
	})
	gh.Push("me/walls", "main", "init", map[string]string{
		"star-wallpaper/index.html":      "<html>",
		"star-wallpaper/project.json":    `{"title":"Starry Orbit","preview":"preview.jpg"}`,
		"plexus-wallpaper/index.html":    "<html>",
		"plexus-wallpaper/js/index.html": "nested page",
	})

	_, items, warns, err := LoadCatalog(context.Background(), c, "me/mods")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Item{}
	for _, it := range items {
		got[it.Type+":"+it.ID] = it
	}
	if len(items) != 2 || got["mod:live"].Name != "Live Wallpapers" || got["mod:live"].Version != "1.2.0" ||
		got["mod:live"].Source.Path != "proj-a/live" || got["mod:widget"].OriginLabel != "mods" {
		t.Fatalf("mods: %+v", items)
	}
	// preview.* at the root of the folder is the card's picture.
	if got["mod:widget"].Preview != "https://raw.githubusercontent.com/me/mods/HEAD/proj-b/widget/preview.webp" || got["mod:live"].Preview != "" {
		t.Fatalf("previews: %q %q", got["mod:widget"].Preview, got["mod:live"].Preview)
	}
	// The page inside the mod is ignored, and said so.
	if len(warns) != 1 || warns[0].Kind != WarnIgnored || warns[0].Path != "proj-a/live" ||
		warns[0].Type != "mod" || len(warns[0].Inner) != 1 || warns[0].Inner[0] != "proj-a/live/assets/demo" {
		t.Fatalf("warnings: %+v", warns)
	}

	label, items, _, err := LoadCatalog(context.Background(), c, "https://github.com/me/walls")
	if err != nil || label != "walls" {
		t.Fatal(label, err)
	}
	got = map[string]Item{}
	for _, it := range items {
		got[it.Type+":"+it.ID] = it
	}
	star := got["wallpaper:star-wallpaper"]
	if len(items) != 2 || star.Name != "Starry Orbit" || got["wallpaper:plexus-wallpaper"].Source.Path != "plexus-wallpaper" ||
		star.Preview != "https://raw.githubusercontent.com/me/walls/HEAD/star-wallpaper/preview.jpg" {
		t.Fatalf("wallpapers: %+v", items)
	}

	// A store.json, when present, still wins over discovery.
	gh.Push("me/walls", "main", "catalog", map[string]string{"store.json": `{"items":[{"id":"only","type":"wallpaper","path":"star-wallpaper"}]}`})
	if _, items, _, _ := LoadCatalog(context.Background(), c, "me/walls"); len(items) != 1 || items[0].ID != "only" {
		t.Fatalf("store.json ignored: %+v", items)
	}
	// An explicit .json file that is missing is an error, not a scan.
	if _, _, _, err := LoadCatalog(context.Background(), c, "https://github.com/me/mods/blob/main/nope.json"); err == nil {
		t.Fatal("missing explicit catalog file accepted")
	}
}

// A folder with a stray mod.js around a wallpaper is read as a mod, and the
// store warns about both the mixed files and the ignored wallpaper.
func TestDiscoverWarnings(t *testing.T) {
	gh := fakegithub.New()
	srv := gh.Start()
	defer srv.Close()
	c := github.New(nil)
	c.APIBase, c.RawBase, c.CodeloadBase = srv.URL+"/api", srv.URL+"/raw", srv.URL+"/codeload"
	gh.Push("me/walls", "main", "init", map[string]string{
		"star/mod.js":                        "stray",
		"star/index.html":                    "<html>",
		"star/wallpaper-engine/index.html":   "<html>",
		"star/wallpaper-engine/project.json": `{"title":"Starry Orbit"}`,
	})
	_, items, warns, err := LoadCatalog(context.Background(), c, "me/walls")
	if err != nil || len(items) != 1 || items[0].Type != "mod" || items[0].ID != "star" {
		t.Fatalf("items: %+v %v", items, err)
	}
	if len(warns) != 2 || warns[0].Kind != WarnMixed || warns[1].Kind != WarnIgnored ||
		warns[1].Inner[0] != "star/wallpaper-engine" {
		t.Fatalf("warnings: %+v", warns)
	}
}

func TestDiscoverSameID(t *testing.T) {
	gh := fakegithub.New()
	srv := gh.Start()
	defer srv.Close()
	c := github.New(nil)
	c.APIBase, c.RawBase, c.CodeloadBase = srv.URL+"/api", srv.URL+"/raw", srv.URL+"/codeload"
	gh.Push("me/walls", "main", "init", map[string]string{
		"star/wallpaper-engine/index.html":   "<html>",
		"plexus/wallpaper-engine/index.html": "<html>",
	})
	_, items, warns, _ := LoadCatalog(context.Background(), c, "me/walls")
	if len(items) != 2 || len(warns) != 1 || warns[0].Kind != WarnSameID || warns[0].Path != "wallpaper-engine" ||
		len(warns[0].Inner) != 2 {
		t.Fatalf("items %+v warnings %+v", items, warns)
	}
}

func TestOldDefaultCatalogIsMigrated(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"catalogs":["`+oldDefaultCatalog+`"],"check_interval_hours":6}`), 0o600)
	s, err := Load(dir)
	if err != nil || len(s.Config.Catalogs) != 2 || s.Config.Catalogs[0] != "UsuarioDeJava/CasaMODs" {
		t.Fatalf("%+v %v", s.Config.Catalogs, err)
	}
	// Sources and installed items under the old account name are renamed.
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"catalogs":["NescauQuente/CasaMODs","https://github.com/nescauquente/WE-Wallpapers/tree/main/x","other/repo"]}`), 0o600)
	os.WriteFile(filepath.Join(dir, "installed.json"), []byte(`{"items":[{"id":"a","type":"mod","source":{"repo":"NescauQuente/CasaMODs","path":"a"}},{"id":"b","type":"mod","source":{"repo":"other/NescauQuente","path":"b"}}]}`), 0o644)
	s, err = Load(dir)
	if err != nil || s.Config.Catalogs[0] != "UsuarioDeJava/CasaMODs" ||
		s.Config.Catalogs[1] != "https://github.com/UsuarioDeJava/WE-Wallpapers/tree/main/x" || s.Config.Catalogs[2] != "other/repo" {
		t.Fatalf("catalogs: %+v %v", s.Config.Catalogs, err)
	}
	if s.Items["mod:a"].Source.Repo != "UsuarioDeJava/CasaMODs" || s.Items["mod:b"].Source.Repo != "other/NescauQuente" {
		t.Fatalf("items: %+v %+v", s.Items["mod:a"].Source, s.Items["mod:b"].Source)
	}
	os.Remove(filepath.Join(dir, "installed.json"))
	// Catalogs the user changed are kept.
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"catalogs":["a/b"]}`), 0o600)
	if s, _ := Load(dir); len(s.Config.Catalogs) != 1 || s.Config.Catalogs[0] != "a/b" {
		t.Fatalf("%+v", s.Config.Catalogs)
	}
}
