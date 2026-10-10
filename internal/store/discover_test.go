package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nescauquente/casamod-store/internal/fakegithub"
	"github.com/nescauquente/casamod-store/internal/github"
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
		".github/x/mod.js":                   "ignored",
	})
	gh.Push("me/walls", "main", "init", map[string]string{
		"star-wallpaper/index.html":      "<html>",
		"star-wallpaper/project.json":    `{"title":"Starry Orbit","preview":"preview.jpg"}`,
		"plexus-wallpaper/index.html":    "<html>",
		"plexus-wallpaper/js/index.html": "nested page",
	})

	_, items, err := LoadCatalog(context.Background(), c, "me/mods")
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

	label, items, err := LoadCatalog(context.Background(), c, "https://github.com/me/walls")
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
	if _, items, _ := LoadCatalog(context.Background(), c, "me/walls"); len(items) != 1 || items[0].ID != "only" {
		t.Fatalf("store.json ignored: %+v", items)
	}
	// An explicit .json file that is missing is an error, not a scan.
	if _, _, err := LoadCatalog(context.Background(), c, "https://github.com/me/mods/blob/main/nope.json"); err == nil {
		t.Fatal("missing explicit catalog file accepted")
	}
}

func TestOldDefaultCatalogIsMigrated(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"catalogs":["`+oldDefaultCatalog+`"],"check_interval_hours":6}`), 0o600)
	s, err := Load(dir)
	if err != nil || len(s.Config.Catalogs) != 2 || s.Config.Catalogs[0] != "NescauQuente/CasaMODs" {
		t.Fatalf("%+v %v", s.Config.Catalogs, err)
	}
	// Catalogs the user changed are kept.
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"catalogs":["a/b"]}`), 0o600)
	if s, _ := Load(dir); len(s.Config.Catalogs) != 1 || s.Config.Catalogs[0] != "a/b" {
		t.Fatalf("%+v", s.Config.Catalogs)
	}
}
