package store

import (
	"context"
	"encoding/json"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/nescauquente/casamod-store/internal/github"
	"github.com/nescauquente/casamod-store/internal/installer"
)

// Warning kinds reported for a source.
const (
	WarnMixed   = "mixed"   // a folder has both mod and wallpaper files: read as a mod
	WarnIgnored = "ignored" // an item folder holds other item folders, which are ignored
	WarnEmpty   = "empty"   // the source has no mods or wallpapers
	WarnSameID  = "same_id" // several folders share a name: they would install into the same folder
)

// Warning tells the user a source does not follow the folder rules.
type Warning struct {
	Kind  string   `json:"kind"`
	Path  string   `json:"path,omitempty"`
	Type  string   `json:"type,omitempty"`
	Inner []string `json:"inner,omitempty"`
}

// maxDiscovered caps how many items one repository can add to the store.
const maxDiscovered = 100

// Folders that never hold installable items.
var skipDirs = map[string]bool{
	"test": true, "tests": true, "testdata": true, "fixtures": true, "mock": true, "mocks": true,
	"node_modules": true, "docs": true, "doc": true, "examples": true, "example": true,
}

// Discover lists the items of a repository that has no store.json, with one
// API call for the tree. A folder with mod.js or mod.css at its root is a
// mod; a folder with project.json (Wallpaper Engine) or index.html is a
// wallpaper. Only the outermost match counts (a mod's own files are not
// items), and test/doc folders are skipped. Names, descriptions and
// previews come from mod.json or project.json when present.
func Discover(ctx context.Context, gh *github.Client, src github.Source) (label string, items []Item, warnings []Warning, err error) {
	label = path.Base(src.Repo)
	ref := src.Branch
	if ref == "" {
		ref = "HEAD"
	}
	tree, err := gh.Tree(ctx, src.Repo, ref)
	if err != nil {
		return label, nil, nil, err
	}
	prefix := ""
	if src.Path != "" {
		prefix = src.Path + "/"
	}
	files := map[string]map[string]bool{} // dir -> file names
	for _, e := range tree {
		if e.Type != "blob" || !strings.HasPrefix(e.Path, prefix) {
			continue
		}
		dir, name := path.Split(e.Path)
		dir = strings.TrimSuffix(dir, "/")
		if skipped(strings.TrimPrefix(dir, prefix)) {
			continue
		}
		if files[dir] == nil {
			files[dir] = map[string]bool{}
		}
		files[dir][name] = true
	}
	types := map[string]string{}
	for dir, names := range files {
		isMod := names["mod.js"] || names["mod.css"]
		isWallpaper := names["project.json"] || (names["index.html"] && dir != src.Path)
		if isMod && isWallpaper {
			warnings = append(warnings, Warning{Kind: WarnMixed, Path: dir, Type: installer.TypeMod})
		}
		switch {
		case isMod:
			types[dir] = installer.TypeMod
		case names["project.json"]:
			types[dir] = installer.TypeWallpaper
		case names["index.html"] && dir != src.Path:
			// A page at the root of the source is usually a demo or gallery.
			types[dir] = installer.TypeWallpaper
		}
	}
	var dirs []string
	for dir := range types {
		if !hasAncestor(dir, types) {
			dirs = append(dirs, dir)
		}
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		var inner []string
		for d := range types {
			if d != dir && (dir == "" || strings.HasPrefix(d, dir+"/")) {
				inner = append(inner, d)
			}
		}
		if len(inner) > 0 {
			sort.Strings(inner)
			warnings = append(warnings, Warning{Kind: WarnIgnored, Path: dir, Type: types[dir], Inner: inner})
		}
	}
	sort.Slice(warnings, func(i, j int) bool { return warnings[i].Path < warnings[j].Path })
	if len(dirs) > maxDiscovered {
		dirs = dirs[:maxDiscovered]
	}

	items = make([]Item, 0, len(dirs))
	for _, dir := range dirs {
		itemSrc := github.Source{Repo: src.Repo, Branch: src.Branch, Path: dir}
		id := DefaultID(itemSrc)
		typ := types[dir]
		if installer.ValidateID(typ, id) != nil {
			continue
		}
		items = append(items, Item{
			ID: id, Type: typ, Name: id, Source: itemSrc,
			Origin: OriginCatalog, OriginLabel: label,
		})
	}
	byID := map[string][]string{}
	for _, it := range items {
		byID[it.Type+":"+it.ID] = append(byID[it.Type+":"+it.ID], it.Source.Path)
	}
	for _, it := range items {
		if paths := byID[it.Type+":"+it.ID]; len(paths) > 1 && paths[0] == it.Source.Path {
			warnings = append(warnings, Warning{Kind: WarnSameID, Path: it.ID, Type: it.Type, Inner: paths})
		}
	}
	describe(ctx, gh, ref, items, files)
	return label, items, warnings, nil
}

func skipped(dir string) bool {
	for _, part := range strings.Split(dir, "/") {
		if strings.HasPrefix(part, ".") || skipDirs[strings.ToLower(part)] {
			return true
		}
	}
	return false
}

func hasAncestor(dir string, set map[string]string) bool {
	for dir != "" {
		if i := strings.LastIndex(dir, "/"); i >= 0 {
			dir = dir[:i]
		} else {
			dir = ""
		}
		if _, ok := set[dir]; ok {
			return true
		}
	}
	return false
}

// describe fills names, descriptions and previews from mod.json/project.json
// (raw downloads, not counted against the API limit).
func describe(ctx context.Context, gh *github.Client, ref string, items []Item, files map[string]map[string]bool) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i := range items {
		it := &items[i]
		names := files[it.Source.Path]
		meta := ""
		switch {
		case names["mod.json"]:
			meta = "mod.json"
		case names["project.json"]:
			meta = "project.json"
		default:
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			data, err := gh.RawFile(ctx, it.Source.Repo, it.Source.Branch, path.Join(it.Source.Path, meta), 1<<20)
			if err != nil {
				return
			}
			var m struct {
				Name        string `json:"name"`
				Title       string `json:"title"`
				Description string `json:"description"`
				Version     string `json:"version"`
				Author      string `json:"author"`
				Preview     string `json:"preview"`
			}
			if json.Unmarshal(data, &m) != nil {
				return
			}
			if t := strings.TrimSpace(firstOf(m.Title, m.Name)); t != "" {
				it.Name = t
			}
			it.Description = strings.TrimSpace(m.Description)
			it.Version = m.Version
			it.Author = m.Author
			if p := strings.TrimSpace(m.Preview); p != "" && !strings.Contains(p, "..") {
				if strings.Contains(p, "://") {
					it.Preview = p
				} else {
					it.Preview = "https://raw.githubusercontent.com/" + it.Source.Repo + "/" + ref + "/" + path.Join(it.Source.Path, p)
				}
			}
		}()
	}
	wg.Wait()
}

func firstOf(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
