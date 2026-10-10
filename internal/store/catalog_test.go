package store

import (
	"testing"

	"github.com/nescauquente/casamod-store/internal/github"
)

func TestParseCatalogRef(t *testing.T) {
	cases := map[string]CatalogRef{
		"me/repo": {Src: github.Source{Repo: "me/repo", Path: "store.json"}},
		"https://github.com/me/repo/tree/dev/catalogs":          {Src: github.Source{Repo: "me/repo", Branch: "dev", Path: "catalogs/store.json"}},
		"https://github.com/me/repo/blob/main/x/mine.json":      {Src: github.Source{Repo: "me/repo", Branch: "main", Path: "x/mine.json"}},
		"https://raw.githubusercontent.com/me/repo/main/a.json": {Src: github.Source{Repo: "me/repo", Branch: "main", Path: "a.json"}},
		"https://example.com/store.json":                        {URL: "https://example.com/store.json"},
	}
	for in, want := range cases {
		got, err := ParseCatalogRef(in)
		if err != nil || got.Src != want.Src || got.URL != want.URL {
			t.Errorf("ParseCatalogRef(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	if _, err := ParseCatalogRef("not a catalog"); err == nil {
		t.Error("garbage accepted")
	}
}

func TestDefaultID(t *testing.T) {
	cases := map[github.Source]string{
		{Repo: "me/repo"}:                         "repo",
		{Repo: "me/repo", Path: "a/My Mod!"}:      "My-Mod",
		{Repo: "me/repo", Path: "star-wallpaper"}: "star-wallpaper",
	}
	for src, want := range cases {
		if got := DefaultID(src); got != want {
			t.Errorf("DefaultID(%+v) = %q, want %q", src, got, want)
		}
	}
}
