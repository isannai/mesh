package queue

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Storage encapsulates disk-backed result persistence for queue jobs.
// Phase 4 introduces this in Provider; Phase 8 removes the equivalent
// logic from engine-runner.
//
// A nil *Storage is valid and means "no disk persistence" — the caller
// keeps body in memory. Pass nil to processors handling streaming or
// small-result engines like vLLM (where SaveToDisk=false).
//
// All filenames live in a single flat directory under Dir, prefixed by
// the owning service name: "{service}_{jobID}.{ext}". This matches the
// engine-runner convention so existing /outputs/{filename} URLs keep
// working when broker URLs swap from engine-runner to Provider.
type Storage struct {
	Dir string        // output directory; created on NewStorage
	TTL time.Duration // retention used by CleanupOrphans
}

// NewStorage creates a Storage and ensures Dir exists (mkdir -p). Returns
// nil + error when Dir is empty or cannot be created.
func NewStorage(dir string, ttl time.Duration) (*Storage, error) {
	if dir == "" {
		return nil, fmt.Errorf("storage: Dir is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("storage: mkdir %s: %w", dir, err)
	}
	return &Storage{Dir: dir, TTL: ttl}, nil
}

// Save persists body to disk and stamps job.ResponseFile / job.URL on
// success. Returns shouldDropBody=true when the caller can safely pass
// nil instead of body up the runJob chain (to free heap).
//
// Nil Storage, missing Dir, nil job, empty body, or write failure all
// fall through with shouldDropBody=false — caller keeps body in memory
// as fallback. This mirrors engine-runner's resilient behavior.
func (s *Storage) Save(job *Job, body []byte, contentType string) (shouldDropBody bool) {
	if s == nil || s.Dir == "" || job == nil || len(body) == 0 {
		return false
	}
	name := job.ServiceName
	if name == "" {
		name = "job"
	}
	filename := fmt.Sprintf("%s_%s%s", name, job.ID, extForContentType(contentType))
	path := filepath.Join(s.Dir, filename)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return false
	}
	job.ResponseFile = path
	job.URL = "/outputs/" + filename
	return true
}

// Cleanup deletes the on-disk file associated with a job. Designed to be
// registered as Queue.SetCleanupHook so TTL/LRU eviction also clears
// disk artifacts. Safe with nil Storage / nil job / no ResponseFile.
func (s *Storage) Cleanup(job *Job) {
	if s == nil || job == nil || job.ResponseFile == "" {
		return
	}
	_ = os.Remove(job.ResponseFile)
}

// CleanupOrphans walks Dir (one level deep) and removes files older than
// s.TTL. Called at Provider startup to evict files left behind by previous
// runs — without this, every restart leaks the entire previous session
// onto disk because the cleanup hook was never invoked.
//
// Returns the number of files removed. Read errors are returned; per-file
// remove errors are ignored (the walk continues — best-effort cleanup).
func (s *Storage) CleanupOrphans() (removed int, err error) {
	if s == nil || s.Dir == "" {
		return 0, nil
	}
	if s.TTL <= 0 {
		// No TTL → cannot decide what's orphan vs fresh. Skip silently.
		return 0, nil
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return 0, fmt.Errorf("storage: read dir %s: %w", s.Dir, err)
	}
	cutoff := time.Now().Add(-s.TTL)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			path := filepath.Join(s.Dir, e.Name())
			if rerr := os.Remove(path); rerr == nil {
				removed++
			}
		}
	}
	return removed, nil
}

// extForContentType returns the file extension (with leading dot) for a
// MIME type. Defaults to .bin for unknown types. Mirrors engine-runner's
// helper — Phase 8 deletes the engine-runner copy and this becomes the
// single source of truth.
func extForContentType(ct string) string {
	switch {
	case strings.Contains(ct, "image/png"):
		return ".png"
	case strings.Contains(ct, "image/jpeg"):
		return ".jpg"
	case strings.Contains(ct, "image/webp"):
		return ".webp"
	case strings.Contains(ct, "json"):
		return ".json"
	case strings.Contains(ct, "text/plain"):
		return ".txt"
	case strings.Contains(ct, "audio/wav"):
		return ".wav"
	case strings.Contains(ct, "audio/mpeg"):
		return ".mp3"
	case strings.Contains(ct, "video/mp4"):
		return ".mp4"
	default:
		return ".bin"
	}
}
