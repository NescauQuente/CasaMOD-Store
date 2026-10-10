package installer

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Limits protects the server from huge or malicious archives.
type Limits struct {
	MaxBytes int64 // total size of the extracted files
	MaxFiles int
}

// DefaultLimits fit mods and web wallpapers (which may carry videos).
var DefaultLimits = Limits{MaxBytes: 1 << 30, MaxFiles: 20000}

// ErrEmpty is returned when the requested folder is not in the archive.
var ErrEmpty = errors.New("the folder was not found in the repository (check the path)")

// ExtractSubdir reads a GitHub tarball (gzip) and writes the files under
// subdir into dest, which must already exist. GitHub tarballs wrap everything
// in a single "<repo>-<sha>/" folder, which is stripped. Symlinks and other
// special entries are skipped, and no entry may escape dest.
func ExtractSubdir(r io.Reader, subdir, dest string, lim Limits) (files int, err error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return 0, fmt.Errorf("download is not a valid archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	prefix := strings.Trim(subdir, "/")
	if prefix != "" {
		prefix += "/"
	}
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return files, fmt.Errorf("reading archive: %w", err)
		}
		name := strings.TrimPrefix(hdr.Name, "./")
		// Strip the "<repo>-<sha>/" wrapper.
		i := strings.IndexByte(name, '/')
		if i < 0 {
			continue
		}
		name = name[i+1:]
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		rel := strings.TrimPrefix(name, prefix)
		if rel == "" {
			continue
		}
		clean := path.Clean(rel)
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
			return files, fmt.Errorf("archive entry %q escapes the target folder", hdr.Name)
		}
		target := filepath.Join(dest, filepath.FromSlash(clean))

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return files, err
			}
		case tar.TypeReg:
			files++
			if lim.MaxFiles > 0 && files > lim.MaxFiles {
				return files, fmt.Errorf("too many files (limit %d)", lim.MaxFiles)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return files, err
			}
			remaining := lim.MaxBytes - total
			n, err := writeFile(target, tr, remaining, hdr.FileInfo().Mode())
			total += n
			if err != nil {
				return files, err
			}
		default:
			// Symlinks, hard links, devices: skipped on purpose.
		}
	}
	if files == 0 {
		return 0, ErrEmpty
	}
	return files, nil
}

func writeFile(target string, r io.Reader, remaining int64, mode os.FileMode) (int64, error) {
	perm := os.FileMode(0o644)
	if mode&0o111 != 0 {
		perm = 0o755
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, io.LimitReader(r, remaining+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, err
	}
	if n > remaining {
		return n, errors.New("download is larger than the size limit")
	}
	return n, nil
}
