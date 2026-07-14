package setup

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestExtractZip_BackslashEntries guards the Compress-Archive (Windows
// PowerShell / .NET Framework) regression: it writes ZIP entry names with
// backslash separators, violating APPNOTE 4.4.17 (forward slash only). A
// backslash directory entry ("web\broker\") isn't recognized by archive/zip's
// IsDir() (which checks for a trailing '/'), so without normalization it gets
// written as a FILE named "broker" and the nested file under it then fails to
// extract ("cannot find the path specified"). ExtractZip must normalize
// separators so the nested tree lands correctly — this is exactly the shape
// of the suite zip once web/broker/build/ (deeply nested) was added.
func TestExtractZip_BackslashEntries(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "bs.zip")

	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	// Directory entries with backslash separators, as Compress-Archive emits
	// them — note these end in '\' (not '/'), so zip.Writer stores them as
	// plain entries and archive/zip's FileInfo().IsDir() returns false.
	for _, d := range []string{`web\`, `web\broker\`, `web\broker\build\`} {
		if _, err := zw.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	w, err := zw.Create(`web\broker\build\index.html`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("<html></html>")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zf.Close()

	dest := filepath.Join(dir, "out")
	if _, err := ExtractZip(zipPath, dest); err != nil {
		t.Fatalf("ExtractZip: %v", err)
	}

	// The nested file must exist at the normalized path.
	idx := filepath.Join(dest, "web", "broker", "build", "index.html")
	b, err := os.ReadFile(idx)
	if err != nil {
		t.Fatalf("expected extracted file %s: %v", idx, err)
	}
	if string(b) != "<html></html>" {
		t.Fatalf("content = %q, want <html></html>", string(b))
	}

	// "web/broker" must be a directory, not a stray file from a
	// misinterpreted directory entry.
	brokerPath := filepath.Join(dest, "web", "broker")
	fi, err := os.Stat(brokerPath)
	if err != nil {
		t.Fatalf("stat %s: %v", brokerPath, err)
	}
	if !fi.IsDir() {
		t.Fatalf("%s should be a directory (dir entry was misinterpreted as a file)", brokerPath)
	}
}

// TestExtractArchive_TarGzDotPrefix guards the build\linux\*.bat packaging:
// `tar -czf out.tar.gz -C <stage> .` (Windows bsdtar) emits every entry with a
// "./" prefix (./, ./bin/, ./ivm, …). ExtractArchive must dispatch to
// ExtractTarGz, strip the common "./" prefix, land the tree FLAT (matching the
// old Compress-Archive zip layout), and set the exec bit so binaries run on the
// Linux node without a separate chmod.
func TestExtractArchive_TarGzDotPrefix(t *testing.T) {
	dir := t.TempDir()
	tgz := filepath.Join(dir, "pkg.tar.gz")

	f, err := os.Create(tgz)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	// "./"-prefixed entries exactly as `tar -C dir .` produces them.
	for _, d := range []string{"./", "./bin/", "./scripts/", "./scripts/linux/"} {
		if err := tw.WriteHeader(&tar.Header{Name: d, Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"./ivm":                  "binary",
		"./bin/isannd":           "daemon",
		"./scripts/linux/a.sh":   "#!/bin/sh\n",
	}
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	dest := filepath.Join(dir, "out")
	if _, err := ExtractArchive(tgz, dest); err != nil {
		t.Fatalf("ExtractArchive(.tar.gz): %v", err)
	}

	// Files land FLAT — no leading "." dir — at dest root.
	for rel, want := range map[string]string{
		"ivm":                filepath.Join(dest, "ivm"),
		"bin/isannd":         filepath.Join(dest, "bin", "isannd"),
		"scripts/linux/a.sh": filepath.Join(dest, "scripts", "linux", "a.sh"),
	} {
		if _, err := os.Stat(want); err != nil {
			t.Fatalf("expected %s at %s: %v", rel, want, err)
		}
	}
	// Exec bit set on unpack (ExtractTarGz forces |0755), except on Windows where
	// the FS has no Unix mode bits.
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(dest, "ivm"))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode()&0o100 == 0 {
			t.Errorf("ivm should be executable after tar.gz unpack, mode = %v", fi.Mode())
		}
	}
}
