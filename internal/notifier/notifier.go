// Package notifier embeds the casamod-store-notifier mini-mod that the
// container installs into CasaMOD's mod folder.
package notifier

import (
	"bytes"
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

// ID is the mod folder name.
const ID = "casamod-store-notifier"

// StatusFile is written by the container into the mod folder.
const StatusFile = "status.json"

//go:embed files
var embedded embed.FS

// Files returns the mod files (without status.json).
func Files() map[string][]byte {
	out := map[string][]byte{}
	fs.WalkDir(embedded, "files", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := embedded.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("files", p)
		out[filepath.ToSlash(rel)] = data
		return nil
	})
	return out
}

// UpToDate reports whether dir holds exactly the embedded files (status.json aside).
func UpToDate(dir string) bool {
	want := Files()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	seen := 0
	for _, e := range entries {
		if e.Name() == StatusFile {
			continue
		}
		data, ok := want[e.Name()]
		if !ok || e.IsDir() {
			return false
		}
		got, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil || !bytes.Equal(got, data) {
			return false
		}
		seen++
	}
	return seen == len(want)
}

// Status is the content of status.json.
type Status struct {
	Version   int          `json:"version"`
	UIPort    int          `json:"ui_port"`
	UIURL     string       `json:"ui_url,omitempty"`
	CheckedAt string       `json:"checked_at,omitempty"`
	Items     []StatusItem `json:"items"`
}

// StatusItem is one item with an update waiting.
type StatusItem struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Name   string `json:"name"`
	Latest string `json:"latest"`
}
