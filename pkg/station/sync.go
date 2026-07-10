package station

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/daesob/http3proxy/pkg/setup"
)

// syncManager manages sync tokens and cached snapshots.
type syncManager struct {
	mu          sync.Mutex
	snapshots   map[string]*SyncSnapshot // token → snapshot
	creating    bool                      // true while snapshot is being created
	lastError   string                    // error from last create attempt
	progress    int                       // files hashed so far
	total       int                       // total files to hash
	currentFile string                    // file currently being hashed
}

// SyncSnapshot represents a point-in-time snapshot of the WorkDir.
type SyncSnapshot struct {
	Token     string         `json:"-"`
	NodeName  string         `json:"node_name"`
	CreatedAt time.Time      `json:"created_at"`
	ExpiresAt time.Time      `json:"expires_at"`
	Errors    []string       `json:"errors"`
	Files     []SnapshotFile `json:"files"`
}

// SnapshotFile represents a single file in the snapshot.
type SnapshotFile struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
	Size int64  `json:"size"`
}

func newSyncManager() *syncManager {
	return &syncManager{
		snapshots: make(map[string]*SyncSnapshot),
	}
}

// generateToken creates a cryptographically random hex token.
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// syncDir returns the .sync/ directory path.
func syncDir(workDir string) string {
	return filepath.Join(workDir, ".sync")
}

// persistSnapshot saves the snapshot data and token to separate files.
func persistSnapshot(workDir string, snap *SyncSnapshot) error {
	dir := syncDir(workDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// snapshot.json — no token
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "snapshot.json"), data, 0o644); err != nil {
		return err
	}
	// token file — token + expiry only
	tokenData, _ := json.Marshal(map[string]string{
		"token":      snap.Token,
		"expires_at": snap.ExpiresAt.Format(time.RFC3339),
	})
	return os.WriteFile(filepath.Join(dir, "token.json"), tokenData, 0o600)
}

// loadSnapshotFromDisk restores snapshot + token from disk.
func loadSnapshotFromDisk(workDir string) (*SyncSnapshot, error) {
	snapData, err := os.ReadFile(filepath.Join(syncDir(workDir), "snapshot.json"))
	if err != nil {
		return nil, err
	}
	var snap SyncSnapshot
	if err := json.Unmarshal(snapData, &snap); err != nil {
		return nil, err
	}
	tokenData, err := os.ReadFile(filepath.Join(syncDir(workDir), "token.json"))
	if err != nil {
		return nil, err
	}
	var tok struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(tokenData, &tok); err != nil {
		return nil, err
	}
	snap.Token = tok.Token
	return &snap, nil
}

// excludedDirs lists directories excluded from sync scanning.
var excludedDirs = map[string]bool{
	"logs":  true,
	".temp": true,
	".sync": true,
}

// excludedBasenames lists file basenames that are never included in the
// snapshot. The installer binary is excluded because it would be running
// on the slave at sync time (Windows can't overwrite a running .exe).
var excludedBasenames = map[string]bool{
	setup.FetcherBin:          true,
	setup.FetcherBin + ".exe": true,
}

// isExcludedFile returns true if the given slash-separated relative path
// should be skipped during snapshot scanning (both for scanWorkDir and
// listWorkDir).
func isExcludedFile(relSlash string) bool {
	// Check excluded top-level directories.
	topDir := strings.SplitN(relSlash, "/", 2)[0]
	if excludedDirs[topDir] {
		return true
	}
	// Check excluded basenames anywhere in the tree.
	base := relSlash
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if excludedBasenames[base] {
		return true
	}
	return false
}

// StartCreateSnapshot begins fully async snapshot creation. Returns
// immediately with an error only if another snapshot is already being
// created. Process checks, file listing, and hashing all happen in a
// background goroutine — callers must poll Status() for progress/errors.
//
// listProcs is called inside the goroutine so slow disk scans (pid files,
// package manifests) don't block the HTTP response.
func (sm *syncManager) StartCreateSnapshot(
	workDir string,
	nodeName string,
	listProcs func() ([]ProcessInfo, error),
	ttl time.Duration,
) error {
	sm.mu.Lock()
	if sm.creating {
		sm.mu.Unlock()
		return fmt.Errorf("snapshot creation already in progress")
	}
	sm.creating = true
	sm.lastError = ""
	sm.progress = 0
	sm.total = 0
	sm.currentFile = ""
	sm.mu.Unlock()

	// Background goroutine for heavy work — including the process check,
	// which may touch disk and can be slow on large WorkDirs.
	go func() {
		// Process check (moved here from the HTTP handler).
		if listProcs != nil {
			procs, err := listProcs()
			if err != nil {
				sm.mu.Lock()
				sm.creating = false
				sm.lastError = "list processes: " + err.Error()
				sm.mu.Unlock()
				return
			}
			var errors []string
			for _, p := range procs {
				if p.Running {
					errors = append(errors, fmt.Sprintf("service %s is running (PID %d). Stop all services before sync.", p.Name, p.PID))
				}
			}
			if len(errors) > 0 {
				sm.mu.Lock()
				sm.creating = false
				sm.lastError = strings.Join(errors, "\n")
				sm.mu.Unlock()
				return
			}
		}
		token, err := generateToken()
		if err != nil {
			sm.mu.Lock()
			sm.creating = false
			sm.lastError = "generate token: " + err.Error()
			sm.mu.Unlock()
			return
		}

		// Phase 1: collect file list (fast, no hashing)
		filePaths, err := listWorkDir(workDir)
		if err != nil {
			sm.mu.Lock()
			sm.creating = false
			sm.lastError = "scan: " + err.Error()
			sm.mu.Unlock()
			return
		}

		sm.mu.Lock()
		sm.total = len(filePaths)
		sm.mu.Unlock()

		// Phase 2: hash each file with progress
		files := make([]SnapshotFile, 0, len(filePaths))
		for i, fp := range filePaths {
			sm.mu.Lock()
			sm.progress = i
			sm.currentFile = fp.Path
			sm.mu.Unlock()

			absPath := filepath.Join(workDir, filepath.FromSlash(fp.Path))
			hash, err := hashFile(absPath)
			if err != nil {
				continue // skip unhashable
			}
			files = append(files, SnapshotFile{
				Path: fp.Path,
				Hash: hash,
				Size: fp.Size,
			})
		}

		sm.mu.Lock()
		sm.progress = sm.total
		sm.currentFile = ""
		sm.mu.Unlock()

		snap := &SyncSnapshot{
			Token:     token,
			NodeName:  nodeName,
			CreatedAt: time.Now(),
			ExpiresAt: time.Now().Add(ttl),
			Files:     files,
		}

		if err := persistSnapshot(workDir, snap); err != nil {
			sm.mu.Lock()
			sm.creating = false
			sm.lastError = "persist: " + err.Error()
			sm.mu.Unlock()
			return
		}

		sm.mu.Lock()
		sm.snapshots[token] = snap
		sm.creating = false
		sm.lastError = ""
		sm.mu.Unlock()
	}()

	return nil
}

// Status returns the current state of the sync manager.
// workDir is used to restore from disk if memory is empty.
func (sm *syncManager) Status(workDir string) map[string]interface{} {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Try restore from disk if memory is empty
	if len(sm.snapshots) == 0 && !sm.creating {
		if diskSnap, err := loadSnapshotFromDisk(workDir); err == nil && time.Now().Before(diskSnap.ExpiresAt) {
			sm.snapshots[diskSnap.Token] = diskSnap
		}
	}

	if sm.creating {
		return map[string]interface{}{
			"status":       "creating",
			"progress":     sm.progress,
			"total":        sm.total,
			"current_file": sm.currentFile,
		}
	}
	if sm.lastError != "" {
		return map[string]interface{}{"status": "error", "error": sm.lastError}
	}

	// Find latest snapshot
	var latest *SyncSnapshot
	for _, snap := range sm.snapshots {
		if latest == nil || snap.CreatedAt.After(latest.CreatedAt) {
			latest = snap
		}
	}
	if latest != nil && time.Now().Before(latest.ExpiresAt) {
		var totalSize int64
		for _, f := range latest.Files {
			totalSize += f.Size
		}
		return map[string]interface{}{
			"status":      "done",
			"token":       latest.Token,
			"files_count": len(latest.Files),
			"total_size":  totalSize,
			"created_at":  latest.CreatedAt.Format(time.RFC3339),
			"expires_at":  latest.ExpiresAt.Format(time.RFC3339),
		}
	}

	return map[string]interface{}{"status": "idle"}
}

// GetSnapshot returns a cached snapshot by token, loading from disk if needed.
func (sm *syncManager) GetSnapshot(workDir, token string) (*SyncSnapshot, error) {
	sm.mu.Lock()
	snap, ok := sm.snapshots[token]
	sm.mu.Unlock()

	if !ok {
		// Try restoring from disk
		diskSnap, err := loadSnapshotFromDisk(workDir)
		if err != nil || diskSnap.Token != token {
			return nil, fmt.Errorf("invalid token")
		}
		snap = diskSnap
		sm.mu.Lock()
		sm.snapshots[token] = snap
		sm.mu.Unlock()
	}

	if time.Now().After(snap.ExpiresAt) {
		sm.mu.Lock()
		delete(sm.snapshots, token)
		sm.mu.Unlock()
		return nil, fmt.Errorf("token expired")
	}
	return snap, nil
}

// Cleanup removes expired snapshots from memory.
func (sm *syncManager) Cleanup() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	for token, snap := range sm.snapshots {
		if time.Now().After(snap.ExpiresAt) {
			delete(sm.snapshots, token)
		}
	}
}

// RunCleanupLoop periodically cleans up expired snapshots.
func (sm *syncManager) RunCleanupLoop(stop <-chan struct{}) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			sm.Cleanup()
		case <-stop:
			return
		}
	}
}

// fileEntry holds path + size without hash (used for phase 1 listing).
type fileEntry struct {
	Path string
	Size int64
}

// listWorkDir recursively lists files in workDir (no hashing, fast).
func listWorkDir(workDir string) ([]fileEntry, error) {
	var files []fileEntry

	err := filepath.Walk(workDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(workDir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		topDir := strings.SplitN(rel, "/", 2)[0]
		if excludedDirs[topDir] {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if isExcludedFile(rel) {
			return nil
		}
		files = append(files, fileEntry{Path: rel, Size: info.Size()})
		return nil
	})

	return files, err
}

// scanWorkDir recursively scans workDir and returns file entries with SHA256 hashes.
func scanWorkDir(workDir string) ([]SnapshotFile, error) {
	var files []SnapshotFile

	err := filepath.Walk(workDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable
		}

		rel, err := filepath.Rel(workDir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)

		if rel == "." {
			return nil
		}

		// Check excluded dirs
		topDir := strings.SplitN(rel, "/", 2)[0]
		if excludedDirs[topDir] {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			return nil
		}

		if isExcludedFile(rel) {
			return nil
		}

		hash, err := hashFile(path)
		if err != nil {
			return nil // skip unhashable files
		}

		files = append(files, SnapshotFile{
			Path: rel,
			Hash: hash,
			Size: info.Size(),
		})
		return nil
	})

	return files, err
}

// hashFile computes SHA256 of a file and returns "sha256:<hex>" string.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}


// ProcessInfo is used to check running processes before snapshot creation.
type ProcessInfo struct {
	Name    string
	PID     int
	Running bool
}
