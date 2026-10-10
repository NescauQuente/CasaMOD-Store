package installer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type entry struct {
	name string
	body string
	typ  byte
	link string
}

func tarball(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		h := &tar.Header{Name: e.name, Typeflag: typ, Mode: 0o644, Size: int64(len(e.body)), Linkname: e.link}
		if typ != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			tw.Write([]byte(e.body))
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func opener(data []byte) Opener {
	return func(context.Context) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestExtractSubdirOnlyTakesTheFolder(t *testing.T) {
	data := tarball(t,
		entry{name: "repo-abc/", typ: tar.TypeDir},
		entry{name: "repo-abc/README.md", body: "root"},
		entry{name: "repo-abc/mods/a/mod.js", body: "a"},
		entry{name: "repo-abc/mods/a/sub/x.css", body: "x"},
		entry{name: "repo-abc/mods/ab/mod.js", body: "not me"},
		entry{name: "repo-abc/mods/a/link", typ: tar.TypeSymlink, link: "/etc/passwd"},
	)
	dest := t.TempDir()
	n, err := ExtractSubdir(bytes.NewReader(data), "mods/a", dest, DefaultLimits)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if read(t, filepath.Join(dest, "mod.js")) != "a" || read(t, filepath.Join(dest, "sub/x.css")) != "x" {
		t.Fatal("wrong content")
	}
	if _, err := os.Lstat(filepath.Join(dest, "link")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("symlink must be skipped")
	}
}

func TestExtractRejectsEscapes(t *testing.T) {
	data := tarball(t, entry{name: "repo-abc/a/../../../evil", body: "x"})
	if _, err := ExtractSubdir(bytes.NewReader(data), "", t.TempDir(), DefaultLimits); err == nil {
		t.Fatal("expected an error")
	}
}

func TestExtractLimits(t *testing.T) {
	data := tarball(t, entry{name: "r/a", body: strings.Repeat("x", 100)}, entry{name: "r/b", body: "y"})
	if _, err := ExtractSubdir(bytes.NewReader(data), "", t.TempDir(), Limits{MaxBytes: 50, MaxFiles: 10}); err == nil {
		t.Fatal("size limit not enforced")
	}
	if _, err := ExtractSubdir(bytes.NewReader(data), "", t.TempDir(), Limits{MaxBytes: 1000, MaxFiles: 1}); err == nil {
		t.Fatal("file limit not enforced")
	}
	if _, err := ExtractSubdir(bytes.NewReader(data), "missing", t.TempDir(), DefaultLimits); !errors.Is(err, ErrEmpty) {
		t.Fatalf("want ErrEmpty, got %v", err)
	}
}

func TestInstallIsAtomic(t *testing.T) {
	in := &Installer{ModDir: t.TempDir()}
	ctx := context.Background()
	v1 := tarball(t, entry{name: "r/m/mod.js", body: "v1"})
	created, err := in.Install(ctx, TypeMod, "m", "m", opener(v1))
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	// A broken download must leave v1 untouched.
	broken := v1[:len(v1)/2]
	if _, err := in.Install(ctx, TypeMod, "m", "m", opener(broken)); err == nil {
		t.Fatal("expected an error for a truncated archive")
	}
	if read(t, filepath.Join(in.ModDir, "m/mod.js")) != "v1" {
		t.Fatal("old version damaged")
	}
	// A folder that is not a mod is refused.
	notMod := tarball(t, entry{name: "r/m/readme.txt", body: "hi"})
	if _, err := in.Install(ctx, TypeMod, "m", "m", opener(notMod)); err == nil {
		t.Fatal("expected an error for a folder without mod.js/mod.css")
	}
	v2 := tarball(t, entry{name: "r/m/mod.js", body: "v2"}, entry{name: "r/m/mod.css", body: "c"})
	created, err = in.Install(ctx, TypeMod, "m", "m", opener(v2))
	if err != nil || created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if read(t, filepath.Join(in.ModDir, "m/mod.js")) != "v2" {
		t.Fatal("not updated")
	}
	// No temporary folders are left behind.
	for _, e := range mustReadDir(t, in.ModDir) {
		if strings.HasPrefix(e, ".") {
			t.Fatalf("leftover %s", e)
		}
	}
	// Wallpapers go to mod/wallpapers/<id> and need no mod.js.
	wp := tarball(t, entry{name: "r/w/index.html", body: "<html>"})
	if _, err := in.Install(ctx, TypeWallpaper, "w", "w", opener(wp)); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(in.ModDir, "wallpapers/w/index.html")) != "<html>" {
		t.Fatal("wallpaper not installed")
	}
	if err := in.Remove(TypeMod, "m"); err != nil || in.Exists(TypeMod, "m") {
		t.Fatalf("remove failed: %v", err)
	}
}

func TestValidateID(t *testing.T) {
	for _, id := range []string{"", ".hidden", "a/b", "..", "wallpapers", strings.Repeat("a", 65)} {
		if ValidateID(TypeMod, id) == nil {
			t.Errorf("%q should be invalid", id)
		}
	}
	if ValidateID(TypeWallpaper, "wallpapers") != nil || ValidateID(TypeMod, "Live_Wallpapers-2.0") != nil {
		t.Error("valid ids refused")
	}
	if ValidateID("theme", "x") == nil {
		t.Error("unknown type accepted")
	}
}

func TestRecover(t *testing.T) {
	in := &Installer{ModDir: t.TempDir()}
	// Crash after moving the old version away, before the new one landed.
	os.MkdirAll(filepath.Join(in.ModDir, oldPrefix+"abc.m"), 0o755)
	os.WriteFile(filepath.Join(in.ModDir, oldPrefix+"abc.m", "mod.js"), []byte("old"), 0o644)
	os.MkdirAll(filepath.Join(in.ModDir, tmpPrefix+"def.m"), 0o755)
	// Crash after the swap, before deleting the old one.
	os.MkdirAll(filepath.Join(in.ModDir, "n"), 0o755)
	os.MkdirAll(filepath.Join(in.ModDir, oldPrefix+"123.n"), 0o755)
	in.Recover()
	if read(t, filepath.Join(in.ModDir, "m/mod.js")) != "old" {
		t.Fatal("old version not restored")
	}
	got := mustReadDir(t, in.ModDir)
	if strings.Join(got, ",") != "m,n" {
		t.Fatalf("unexpected leftovers: %v", got)
	}
}

func mustReadDir(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}
