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
func Discover(ctx context.Context, gh *github.Client, src github.Source) (label string, items []Item, err error) {
	label = path.Base(src.Repo)
	ref := src.Branch
	if ref == "" {
		ref = "HEAD"
	}
	tree, err := gh.Tree(ctx, src.Repo, ref)
	if err != nil {
		return label, nil, err
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
	// Wallpaper Engine projects (project.json) come first: a folder that
	// holds one is a wallpaper project, even if it also carries a stray
	// mod.js, and nothing inside a project is a separate item.
	projects := map[string]string{}
	for dir, names := range files {
		if names["project.json"] {
			projects[dir] = installer.TypeWallpaper
		}
	}
	types := map[string]string{}
	for dir := range projects {
		if !hasAncestor(dir, projects) {
			types[dir] = installer.TypeWallpaper
		}
	}
	others := map[string]string{}
	for dir, names := range files {
		switch {
		case projects[dir] != "":
		case names["mod.js"] || names["mod.css"]:
			others[dir] = installer.TypeMod
		case names["index.html"] && dir != src.Path:
			// A page at the root of the source is usually a demo or gallery.
			others[dir] = installer.TypeWallpaper
		}
	}
	for dir, typ := range others {
		if !hasAncestor(dir, others) && !hasAncestor(dir, projects) && !hasDescendant(dir, projects) {
			types[dir] = typ
		}
	}
	var dirs []string
	for dir := range types {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	if len(dirs) > maxDiscovered {
		dirs = dirs[:maxDiscovered]
	}

	items = make([]Item, 0, len(dirs))
	for _, dir := range dirs {
		items = append(items, Item{
			Type: types[dir], Source: github.Source{Repo: src.Repo, Branch: src.Branch, Path: dir},
			Origin: OriginCatalog, OriginLabel: label,
		})
	}
	describe(ctx, gh, ref, items, files)

	// Ids are folder names, except for wallpapers named by their project:
	// "wallpaper-engine/" folders become "starry-orbit", "neural-plexus"...
	out := items[:0]
	seen := map[string]bool{}
	for _, it := range items {
		if it.dropped {
			continue
		}
		id := DefaultID(it.Source)
		if it.Type == installer.TypeWallpaper && slug(it.title) != "" {
			id = slug(it.title)
		}
		if it.Name == "" {
			it.Name = id
		}
		it.ID = id
		if installer.ValidateID(it.Type, id) != nil || seen[it.Type+":"+id] {
			continue
		}
		seen[it.Type+":"+id] = true
		out = append(out, it)
	}
	return label, out, nil
}

func skipped(dir string) bool {
	for _, part := range strings.Split(dir, "/") {
		if strings.HasPrefix(part, ".") || skipDirs[strings.ToLower(part)] {
			return true
		}
	}
	return false
}

func hasDescendant(dir string, set map[string]string) bool {
	for d := range set {
		if d != dir && (dir == "" || strings.HasPrefix(d, dir+"/")) {
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
				Type        string `json:"type"`
			}
			if json.Unmarshal(data, &m) != nil {
				return
			}
			if meta == "project.json" {
				// Only web wallpapers run in a browser.
				if t := strings.ToLower(m.Type); t != "" && t != "web" {
					it.dropped = true
					return
				}
				it.title = strings.TrimSpace(m.Title)
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

// slug turns a title into a folder name: "Starry Orbit" -> "starry-orbit".
func slug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	id := strings.Trim(b.String(), "-")
	if len(id) > 64 {
		id = strings.Trim(id[:64], "-")
	}
	return id
}
