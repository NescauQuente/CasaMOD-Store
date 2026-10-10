// Package installer places mods and wallpapers into the CasaMOD folders. Every
// install or update is atomic: the new files are extracted next to the target
// and swapped in with renames, so CasaOS never sees a half written mod.
package installer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Item types.
const (
	TypeMod       = "mod"
	TypeWallpaper = "wallpaper"
)

const (
	tmpPrefix = ".casamod-store-tmp."
	oldPrefix = ".casamod-store-old."
	delPrefix = ".casamod-store-del."
)

var idRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidateID checks that an id is safe to use as a folder name.
func ValidateID(typ, id string) error {
	if !idRe.MatchString(id) {
		return fmt.Errorf("invalid id %q: use letters, digits, '.', '_' or '-' (max 64)", id)
	}
	if typ == TypeMod && strings.EqualFold(id, WallpapersDir) {
		return fmt.Errorf("%q is reserved for the wallpapers folder", id)
	}
	if typ != TypeMod && typ != TypeWallpaper {
		return fmt.Errorf("invalid type %q", typ)
	}
	return nil
}

// WallpapersDir is the folder inside mod/ used by the Live Wallpapers mod.
const WallpapersDir = "wallpapers"

// Installer knows where mods and wallpapers live.
type Installer struct {
	ModDir string // e.g. /DATA/AppData/casamod/mod
	Limits Limits
}

// Dir returns the target folder of an item.
func (in *Installer) Dir(typ, id string) string {
	if typ == TypeWallpaper {
		return filepath.Join(in.ModDir, WallpapersDir, id)
	}
	return filepath.Join(in.ModDir, id)
}

// Exists reports whether the target folder of an item exists.
func (in *Installer) Exists(typ, id string) bool {
	_, err := os.Stat(in.Dir(typ, id))
	return err == nil
}

// Opener returns a gzipped tarball stream.
type Opener func(ctx context.Context) (io.ReadCloser, error)

// Install downloads the archive, extracts subdir and swaps it into place.
// It reports whether the item folder did not exist before (a new mod needs a
// CasaMOD restart, an updated one does not).
func (in *Installer) Install(ctx context.Context, typ, id, subdir string, open Opener) (created bool, err error) {
	if err := ValidateID(typ, id); err != nil {
		return false, err
	}
	target := in.Dir(typ, id)
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return false, err
	}
	tmp := filepath.Join(parent, tmpPrefix+randHex()+"."+id)
	if err := os.Mkdir(tmp, 0o755); err != nil {
		return false, err
	}
	defer os.RemoveAll(tmp) // no-op after a successful swap

	rc, err := open(ctx)
	if err != nil {
		return false, err
	}
	_, err = ExtractSubdir(rc, subdir, tmp, in.limits())
	rc.Close()
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := check(typ, tmp); err != nil {
		return false, err
	}
	return in.swap(tmp, target)
}

// InstallFiles writes a set of files (embedded or local) atomically.
func (in *Installer) InstallFiles(typ, id string, files map[string][]byte) (created bool, err error) {
	if err := ValidateID(typ, id); err != nil {
		return false, err
	}
	target := in.Dir(typ, id)
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return false, err
	}
	tmp := filepath.Join(parent, tmpPrefix+randHex()+"."+id)
	if err := os.Mkdir(tmp, 0o755); err != nil {
		return false, err
	}
	defer os.RemoveAll(tmp)
	for name, data := range files {
		p := filepath.Join(tmp, filepath.FromSlash(name))
		if !strings.HasPrefix(p, tmp+string(os.PathSeparator)) {
			return false, fmt.Errorf("invalid file name %q", name)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return false, err
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return false, err
		}
	}
	return in.swap(tmp, target)
}

func (in *Installer) limits() Limits {
	if in.Limits.MaxBytes == 0 {
		return DefaultLimits
	}
	return in.Limits
}

// check makes sure the downloaded folder is what the type promises, so that a
// wrong path does not silently install an empty or unrelated folder.
func check(typ, dir string) error {
	if typ != TypeMod {
		return nil
	}
	for _, f := range []string{"mod.js", "mod.css"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return nil
		}
	}
	return errors.New("this folder has no mod.js or mod.css at its root, so it is not a CasaMOD mod (check the path, or choose type \"wallpaper\")")
}

func (in *Installer) swap(tmp, target string) (created bool, err error) {
	_, statErr := os.Stat(target)
	if errors.Is(statErr, os.ErrNotExist) {
		if err := os.Rename(tmp, target); err != nil {
			return false, err
		}
		return true, nil
	}
	old := filepath.Join(filepath.Dir(target), oldPrefix+randHex()+"."+filepath.Base(target))
	if err := os.Rename(target, old); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, target); err != nil {
		// Put the previous version back.
		if rerr := os.Rename(old, target); rerr != nil {
			return false, fmt.Errorf("%v (and restoring the old version failed: %v)", err, rerr)
		}
		return false, err
	}
	os.RemoveAll(old)
	return false, nil
}

// Remove deletes an item folder atomically (rename first, then delete).
func (in *Installer) Remove(typ, id string) error {
	if err := ValidateID(typ, id); err != nil {
		return err
	}
	target := in.Dir(typ, id)
	if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	old := filepath.Join(filepath.Dir(target), delPrefix+randHex())
	if err := os.Rename(target, old); err != nil {
		return err
	}
	return os.RemoveAll(old)
}

// Recover cleans up after a crash in the middle of a swap: unfinished
// downloads are deleted, and a previous version whose replacement never made
// it into place is restored.
func (in *Installer) Recover() {
	for _, dir := range []string{in.ModDir, filepath.Join(in.ModDir, WallpapersDir)} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			full := filepath.Join(dir, name)
			switch {
			case strings.HasPrefix(name, tmpPrefix), strings.HasPrefix(name, delPrefix):
				os.RemoveAll(full)
			case strings.HasPrefix(name, oldPrefix):
				rest := strings.TrimPrefix(name, oldPrefix)
				i := strings.IndexByte(rest, '.')
				id := ""
				if i >= 0 {
					id = rest[i+1:]
				}
				target := filepath.Join(dir, id)
				if id != "" {
					if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
						if os.Rename(full, target) == nil {
							continue
						}
					}
				}
				os.RemoveAll(full)
			}
		}
	}
}

// ListDirs returns the visible sub folders of a directory.
func ListDirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	return out
}

func randHex() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}
