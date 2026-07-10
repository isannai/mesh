package queue

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewStorage(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "outputs")
	s, err := NewStorage(subDir, time.Hour)
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	if s.Dir != subDir {
		t.Errorf("Dir = %q", s.Dir)
	}
	if _, err := os.Stat(subDir); err != nil {
		t.Errorf("dir not created: %v", err)
	}
}

func TestNewStorageEmptyDir(t *testing.T) {
	if _, err := NewStorage("", time.Hour); err == nil {
		t.Error("expected error on empty Dir")
	}
}

func TestStorageSaveSetsFileAndURL(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStorage(dir, time.Hour)

	job := &Job{ID: "abc123", ServiceName: "sd-api"}
	body := []byte("PNGDATA")

	if !s.Save(job, body, "image/png") {
		t.Fatal("Save returned false (drop=false)")
	}
	if job.ResponseFile == "" {
		t.Error("ResponseFile not set")
	}
	if job.URL != "/outputs/sd-api_abc123.png" {
		t.Errorf("URL = %q", job.URL)
	}

	// File actually written with the same bytes.
	got, err := os.ReadFile(job.ResponseFile)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "PNGDATA" {
		t.Errorf("file body = %q", string(got))
	}
}

func TestStorageSaveContentTypeMapping(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStorage(dir, time.Hour)

	cases := []struct {
		ct  string
		ext string
	}{
		{"image/png", ".png"},
		{"image/jpeg; charset=binary", ".jpg"},
		{"application/json", ".json"},
		{"text/plain", ".txt"},
		{"audio/wav", ".wav"},
		{"video/mp4", ".mp4"},
		{"application/octet-stream", ".bin"},
		{"", ".bin"},
	}
	for _, tc := range cases {
		t.Run(tc.ct, func(t *testing.T) {
			job := &Job{ID: "x" + tc.ext, ServiceName: "test"}
			s.Save(job, []byte("data"), tc.ct)
			if filepath.Ext(job.ResponseFile) != tc.ext {
				t.Errorf("ext = %q, want %q (file=%s)", filepath.Ext(job.ResponseFile), tc.ext, job.ResponseFile)
			}
		})
	}
}

func TestStorageSaveServiceFallback(t *testing.T) {
	// ServiceName 비어있으면 "job" 으로 prefix.
	dir := t.TempDir()
	s, _ := NewStorage(dir, time.Hour)

	job := &Job{ID: "abc"}
	s.Save(job, []byte("x"), "text/plain")
	if filepath.Base(job.ResponseFile) != "job_abc.txt" {
		t.Errorf("filename = %q", filepath.Base(job.ResponseFile))
	}
}

func TestStorageSaveNoOpCases(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStorage(dir, time.Hour)

	// nil Storage
	var ns *Storage
	if ns.Save(&Job{ID: "x"}, []byte("y"), "text/plain") {
		t.Error("nil Storage should return false")
	}

	// nil job
	if s.Save(nil, []byte("y"), "text/plain") {
		t.Error("nil job should return false")
	}

	// empty body
	job := &Job{ID: "x", ServiceName: "test"}
	if s.Save(job, nil, "text/plain") {
		t.Error("empty body should return false")
	}
	if job.ResponseFile != "" {
		t.Error("ResponseFile should not be set on no-op")
	}
}

func TestStorageCleanup(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStorage(dir, time.Hour)

	job := &Job{ID: "abc", ServiceName: "sd-api"}
	s.Save(job, []byte("data"), "image/png")
	path := job.ResponseFile

	// File exists before cleanup.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file should exist: %v", err)
	}

	s.Cleanup(job)

	// File removed after cleanup.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file should be removed, stat err = %v", err)
	}
}

func TestStorageCleanupNoOp(t *testing.T) {
	// nil Storage / nil job / no ResponseFile — all silent no-op.
	var ns *Storage
	ns.Cleanup(&Job{ResponseFile: "/nonexistent"})
	(&Storage{Dir: "x"}).Cleanup(nil)
	(&Storage{Dir: "x"}).Cleanup(&Job{}) // ResponseFile empty
}

func TestStorageCleanupOrphans(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStorage(dir, 100*time.Millisecond)

	// 옛 파일: mtime 을 과거로 설정
	oldPath := filepath.Join(dir, "old_file.png")
	if err := os.WriteFile(oldPath, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	pastTime := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(oldPath, pastTime, pastTime); err != nil {
		t.Fatal(err)
	}

	// 새 파일: 그대로 (현재 시각 mtime)
	newPath := filepath.Join(dir, "new_file.png")
	if err := os.WriteFile(newPath, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, err := s.CleanupOrphans()
	if err != nil {
		t.Fatalf("CleanupOrphans: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Error("old file should be removed")
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Error("new file should still exist")
	}
}

func TestStorageCleanupOrphansEmptyDir(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStorage(dir, time.Hour)

	removed, err := s.CleanupOrphans()
	if err != nil {
		t.Fatalf("CleanupOrphans: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}

func TestStorageCleanupOrphansNoTTL(t *testing.T) {
	// TTL=0 이면 청소 안 함 (vLLM streaming 모드에선 disk 자체를 안 씀).
	dir := t.TempDir()
	s, _ := NewStorage(dir, 0)

	old := filepath.Join(dir, "ancient.png")
	os.WriteFile(old, []byte("x"), 0o644)
	pastTime := time.Now().Add(-100 * time.Hour)
	os.Chtimes(old, pastTime, pastTime)

	removed, err := s.CleanupOrphans()
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0 (TTL=0 should skip)", removed)
	}
	if _, err := os.Stat(old); err != nil {
		t.Error("file should not be removed")
	}
}

func TestStorageCleanupOrphansSkipsSubdirs(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStorage(dir, 100*time.Millisecond)

	// 서브디렉토리 (혹시 manifest 가 만들었을 수도)
	subDir := filepath.Join(dir, "subdir")
	os.MkdirAll(subDir, 0o755)
	pastTime := time.Now().Add(-1 * time.Hour)
	os.Chtimes(subDir, pastTime, pastTime)

	// 디렉토리 안의 파일 — Cleanup 은 한 단계만 돌아서 안 건드림
	subFile := filepath.Join(subDir, "inner.png")
	os.WriteFile(subFile, []byte("x"), 0o644)
	os.Chtimes(subFile, pastTime, pastTime)

	removed, _ := s.CleanupOrphans()
	if removed != 0 {
		t.Errorf("removed = %d, want 0 (subdirs ignored)", removed)
	}
	if _, err := os.Stat(subDir); err != nil {
		t.Error("subdir should not be removed")
	}
}

func TestStorageNilCleanupOrphans(t *testing.T) {
	var ns *Storage
	removed, err := ns.CleanupOrphans()
	if err != nil {
		t.Errorf("nil Storage err = %v", err)
	}
	if removed != 0 {
		t.Errorf("nil Storage removed = %d", removed)
	}
}

func TestExtForContentType(t *testing.T) {
	cases := map[string]string{
		"image/png":                ".png",
		"image/jpeg":               ".jpg",
		"image/webp":               ".webp",
		"application/json":         ".json",
		"text/plain":               ".txt",
		"text/plain; charset=utf8": ".txt",
		"audio/wav":                ".wav",
		"audio/mpeg":               ".mp3",
		"video/mp4":                ".mp4",
		"":                         ".bin",
		"unknown/type":             ".bin",
	}
	for ct, want := range cases {
		if got := extForContentType(ct); got != want {
			t.Errorf("extForContentType(%q) = %q, want %q", ct, got, want)
		}
	}
}
