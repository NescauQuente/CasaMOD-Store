package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nescauquente/casamod-store/internal/github"
	"github.com/nescauquente/casamod-store/internal/installer"
)

// Item is an entry shown in the store.
type Item struct {
	ID          string        `json:"id"`
	Type        string        `json:"type"`
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Author      string        `json:"author,omitempty"`
	Version     string        `json:"version,omitempty"`
	Preview     string        `json:"preview,omitempty"`
	Source      github.Source `json:"source"`
	Origin      string        `json:"origin"`
	OriginLabel string        `json:"origin_label"`
}

// catalogFile is the store.json format. A bare array of items is accepted too.
type catalogFile struct {
	Name  string        `json:"name"`
	Items []catalogItem `json:"items"`
}

type catalogItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Repo        string `json:"repo"`
	Branch      string `json:"branch"`
	Path        string `json:"path"`
	Description string `json:"description"`
	Preview     string `json:"preview"`
	Author      string `json:"author"`
	Version     string `json:"version"`
}

// CatalogRef is where a catalog lives: a file in a GitHub repository, or any
// other https URL.
type CatalogRef struct {
	Src  github.Source // Path is the store.json file
	URL  string        // set for non GitHub URLs
	Text string
	// Folder is set when a repository or folder was given (not a .json
	// file): without a store.json there, its contents are discovered.
	Folder bool
}

// ParseCatalogRef accepts "owner/repo", GitHub repo/tree/blob URLs, raw
// GitHub URLs and plain https URLs. Folders get "store.json" appended.
func ParseCatalogRef(raw string) (CatalogRef, error) {
	raw = strings.TrimSpace(raw)
	ref := CatalogRef{Text: raw}
	if u, err := url.Parse(raw); err == nil && u.Host == "raw.githubusercontent.com" {
		parts := strings.SplitN(strings.Trim(u.Path, "/"), "/", 4)
		if len(parts) == 4 {
			ref.Src = github.Source{Repo: parts[0] + "/" + parts[1], Branch: parts[2], Path: parts[3]}
			return ref, ref.Src.Validate()
		}
	}
	src, err := github.ParseURL(raw)
	if err != nil {
		if u, uerr := url.Parse(raw); uerr == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
			ref.URL = raw
			return ref, nil
		}
		return ref, err
	}
	// ParseURL turns a blob URL into its folder; recover the file name.
	if strings.Contains(raw, "/blob/") && strings.HasSuffix(strings.ToLower(raw), ".json") {
		src.Path = path.Join(src.Path, path.Base(strings.SplitN(raw, "?", 2)[0]))
	} else {
		src.Path = path.Join(src.Path, "store.json")
		ref.Folder = true
	}
	ref.Src = src
	return ref, nil
}

// LoadCatalog downloads and parses a catalog.
func LoadCatalog(ctx context.Context, gh *github.Client, raw string) (label string, items []Item, warnings []Warning, err error) {
	ref, err := ParseCatalogRef(raw)
	if err != nil {
		return raw, nil, nil, err
	}
	var data []byte
	if ref.URL != "" {
		data, err = gh.Fetch(ctx, ref.URL, 4<<20)
	} else {
		data, err = gh.RawFile(ctx, ref.Src.Repo, ref.Src.Branch, ref.Src.Path, 4<<20)
	}
	label = ref.Src.Repo
	if ref.URL != "" {
		label = ref.URL
	}
	if err != nil && ref.Folder && errors.Is(err, github.ErrNotFound) {
		dir := ref.Src
		dir.Path = path.Dir(dir.Path)
		if dir.Path == "." {
			dir.Path = ""
		}
		return Discover(ctx, gh, dir)
	}
	if err != nil {
		return label, nil, nil, err
	}
	var cf catalogFile
	if err := json.Unmarshal(data, &cf); err != nil {
		var arr []catalogItem
		if json.Unmarshal(data, &arr) != nil {
			return label, nil, nil, fmt.Errorf("invalid catalog JSON: %w", err)
		}
		cf.Items = arr
	}
	if cf.Name != "" {
		label = cf.Name
	}
	for _, ci := range cf.Items {
		it, err := ci.toItem(ref)
		if err != nil {
			continue // a broken entry does not hide the rest of the catalog
		}
		it.OriginLabel = label
		items = append(items, it)
	}
	return label, items, nil, nil
}

func (ci catalogItem) toItem(ref CatalogRef) (Item, error) {
	src := github.Source{Repo: ci.Repo, Branch: ci.Branch, Path: ci.Path}.Normalize()
	if src.Repo == "" {
		if ref.URL != "" {
			return Item{}, errors.New("missing repo")
		}
		// Items without a repo live next to the catalog.
		src.Repo = ref.Src.Repo
		if src.Branch == "" {
			src.Branch = ref.Src.Branch
		}
	}
	if err := src.Validate(); err != nil {
		return Item{}, err
	}
	typ := strings.ToLower(strings.TrimSpace(ci.Type))
	if typ == "" {
		typ = installer.TypeMod
	}
	id := strings.TrimSpace(ci.ID)
	if id == "" {
		id = DefaultID(src)
	}
	if err := installer.ValidateID(typ, id); err != nil {
		return Item{}, err
	}
	name := ci.Name
	if name == "" {
		name = id
	}
	preview := ci.Preview
	if preview != "" && !strings.Contains(preview, "://") && ref.URL == "" {
		// Relative previews are resolved against the item's repository.
		branch := src.Branch
		if branch == "" {
			branch = "HEAD"
		}
		p := strings.TrimPrefix(preview, "/")
		if !strings.HasPrefix(preview, "/") && src.Path != "" {
			p = path.Join(src.Path, p)
		}
		preview = "https://raw.githubusercontent.com/" + src.Repo + "/" + branch + "/" + p
	}
	return Item{
		ID: id, Type: typ, Name: name, Description: ci.Description, Author: ci.Author,
		Version: ci.Version, Preview: preview, Source: src, Origin: OriginCatalog,
	}, nil
}

// DefaultID derives a folder name from a source: the last path segment, or
// the repository name.
func DefaultID(src github.Source) string {
	base := path.Base(src.Path)
	if src.Path == "" || base == "." || base == "/" {
		base = path.Base(src.Repo)
	}
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	id := strings.Trim(b.String(), "-.")
	if len(id) > 64 {
		id = id[:64]
	}
	if id == "" {
		id = "item"
	}
	return id
}

// CasaMOD built-in mods live in app/mod/ of the CasaMOD repository.
const (
	CasaMODRepo   = "Cp0204/CasaMOD"
	CasaMODBranch = "main"
	CasaMODPath   = "app/mod"
)

type modJSON struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Author      string `json:"author"`
}

// CasaMODMods lists the mods that ship with CasaMOD. The upstream repository
// is listed with one API call; the local mod_store folder (copied by CasaMOD
// on its first start) provides descriptions and is the fallback when GitHub
// cannot be reached.
func CasaMODMods(ctx context.Context, gh *github.Client, modStoreDir string) ([]Item, error) {
	names := map[string]bool{}
	tree, treeErr := gh.Tree(ctx, CasaMODRepo, CasaMODBranch)
	for _, e := range tree {
		if e.Type != "tree" || !strings.HasPrefix(e.Path, CasaMODPath+"/") {
			continue
		}
		rest := strings.TrimPrefix(e.Path, CasaMODPath+"/")
		if !strings.Contains(rest, "/") {
			names[rest] = true
		}
	}
	if treeErr != nil {
		for _, n := range installer.ListDirs(modStoreDir) {
			names[n] = true
		}
	}
	if len(names) == 0 {
		if treeErr != nil {
			return nil, treeErr
		}
		return nil, nil
	}
	var items []Item
	for name := range names {
		if installer.ValidateID(installer.TypeMod, name) != nil {
			continue
		}
		it := Item{
			ID: name, Type: installer.TypeMod, Name: name,
			Source:      github.Source{Repo: CasaMODRepo, Branch: CasaMODBranch, Path: CasaMODPath + "/" + name},
			Origin:      OriginCasaMOD,
			OriginLabel: "CasaMOD",
			Author:      "CasaMOD",
		}
		local := filepath.Join(modStoreDir, name)
		var mj modJSON
		if readJSON(filepath.Join(local, "mod.json"), &mj) == nil && mj.Description != "" {
			it.Description = mj.Description
			it.Version = mj.Version
			if mj.Author != "" {
				it.Author = mj.Author
			}
		} else if d := readmeSummary(filepath.Join(local, "README.md")); d != "" {
			it.Description = d
		}
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

// readmeSummary returns the first paragraph of a README that is not a title.
func readmeSummary(p string) string {
	data, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	var para []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "![") || strings.HasPrefix(line, "<") {
			if len(para) > 0 {
				break
			}
			continue
		}
		if line == "" {
			if len(para) > 0 {
				break
			}
			continue
		}
		para = append(para, line)
	}
	s := strings.Join(para, " ")
	if len(s) > 300 {
		s = s[:297] + "..."
	}
	return s
}
