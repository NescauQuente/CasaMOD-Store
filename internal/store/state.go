// Package store keeps the configuration, the list of installed items and
// reads the catalogs (store.json files, the CasaMOD built-in mods).
package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/nescauquente/casamod-store/internal/github"
)

// Update policies.
const (
	PolicyNotify = "notify"
	PolicyAuto   = "auto"
)

// Origins of an item.
const (
	OriginCatalog = "catalog"
	OriginCasaMOD = "casamod"
	OriginURL     = "url"
	OriginSystem  = "system"
)

// Config is saved in config.json. The token never leaves the server.
type Config struct {
	Token              string   `json:"github_token,omitempty"`
	CheckIntervalHours float64  `json:"check_interval_hours"`
	Catalogs           []string `json:"catalogs"`
	CasaMODContainer   string   `json:"casamod_container"`
	IncludeCasaMODMods bool     `json:"include_casamod_mods"`
	Notifier           bool     `json:"notifier"`
	UIPort             int      `json:"ui_port"`
	PublicURL          string   `json:"public_url,omitempty"` // link used by the notifier, optional
}

// DefaultCatalogs are the sources every store starts with. Repositories
// without a store.json are scanned for mods and wallpapers.
var DefaultCatalogs = []string{"NescauQuente/CasaMODs", "NescauQuente/WE-Wallpapers"}

// oldDefaultCatalog was the default before DefaultCatalogs; configs that
// still hold only it are migrated.
const oldDefaultCatalog = "https://github.com/NescauQuente/CasaMOD-Store/blob/main/store.json"

// DefaultConfig returns the configuration used on first start.
func DefaultConfig() Config {
	return Config{
		CheckIntervalHours: 6,
		Catalogs:           append([]string(nil), DefaultCatalogs...),
		CasaMODContainer:   "casamod",
		IncludeCasaMODMods: true,
		Notifier:           true,
		UIPort:             8418,
	}
}

// Installed describes an item this container installed.
type Installed struct {
	ID          string        `json:"id"`
	Type        string        `json:"type"`
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Origin      string        `json:"origin"`
	Source      github.Source `json:"source"`
	SHA         string        `json:"sha"`
	Policy      string        `json:"policy"`
	InstalledAt time.Time     `json:"installed_at"`
	UpdatedAt   time.Time     `json:"updated_at"`

	// Filled by update checks.
	LatestSHA     string    `json:"latest_sha,omitempty"`
	LatestDate    time.Time `json:"latest_date,omitempty"`
	LatestMessage string    `json:"latest_message,omitempty"`
	CheckedAt     time.Time `json:"checked_at,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
}

// Key identifies an item: the same id may be used by a mod and a wallpaper.
func (it *Installed) Key() string { return Key(it.Type, it.ID) }

// HasUpdate reports whether a newer commit is known.
func (it *Installed) HasUpdate() bool {
	return it.LatestSHA != "" && it.LatestSHA != it.SHA
}

// Key builds an item key.
func Key(typ, id string) string { return typ + ":" + id }

// SelfUpdate is the newest published image of the store itself.
type SelfUpdate struct {
	LatestSHA  string    `json:"latest_sha,omitempty"`
	LatestDate time.Time `json:"latest_date,omitempty"`
	CheckedAt  time.Time `json:"checked_at,omitempty"`
	LastError  string    `json:"last_error,omitempty"`
}

// State is everything saved on disk.
type State struct {
	mu        sync.Mutex
	dir       string
	Config    Config
	Items     map[string]*Installed
	LastCheck time.Time
	Self      SelfUpdate
}

type installedFile struct {
	LastCheck time.Time    `json:"last_check,omitempty"`
	Self      SelfUpdate   `json:"self"`
	Items     []*Installed `json:"items"`
}

// Load reads config.json and installed.json from dir (missing files are fine).
func Load(dir string) (*State, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &State{dir: dir, Config: DefaultConfig(), Items: map[string]*Installed{}}
	if err := readJSON(filepath.Join(dir, "config.json"), &s.Config); err != nil {
		return nil, err
	}
	if len(s.Config.Catalogs) == 1 && s.Config.Catalogs[0] == oldDefaultCatalog {
		s.Config.Catalogs = append([]string(nil), DefaultCatalogs...)
	}
	var inst installedFile
	if err := readJSON(filepath.Join(dir, "installed.json"), &inst); err != nil {
		return nil, err
	}
	s.LastCheck = inst.LastCheck
	s.Self = inst.Self
	for _, it := range inst.Items {
		if it.Policy != PolicyAuto {
			it.Policy = PolicyNotify
		}
		s.Items[it.Key()] = it
	}
	return s, nil
}

func readJSON(path string, out any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

// Lock and Unlock guard Config, Items and LastCheck.
func (s *State) Lock()   { s.mu.Lock() }
func (s *State) Unlock() { s.mu.Unlock() }

// SaveLocked writes both files. The caller holds the lock.
func (s *State) SaveLocked() error {
	if err := writeJSON(filepath.Join(s.dir, "config.json"), s.Config, 0o600); err != nil {
		return err
	}
	return writeJSON(filepath.Join(s.dir, "installed.json"), installedFile{LastCheck: s.LastCheck, Self: s.Self, Items: s.SortedLocked()}, 0o644)
}

// SortedLocked returns the items sorted by type and name.
func (s *State) SortedLocked() []*Installed {
	out := make([]*Installed, 0, len(s.Items))
	for _, it := range s.Items {
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Dir is the folder holding config.json and installed.json.
func (s *State) Dir() string { return s.dir }

// Token returns the configured GitHub token.
func (s *State) Token() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Config.Token
}

// writeJSON writes atomically (temp file + rename).
func writeJSON(path string, v any, perm os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// WriteJSONFile is exported for the notifier status file.
func WriteJSONFile(path string, v any) error { return writeJSON(path, v, 0o644) }
