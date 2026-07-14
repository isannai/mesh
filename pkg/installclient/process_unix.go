//go:build !windows

package installclient

import (
	"os"
	"syscall"
)

func isProcessAlive(pid int) bool {
	return IsProcessAlive(pid)
}

// IsProcessAlive reports whether the given PID refers to a live process.
// Exported so other packages (e.g. installer) can reuse the implementation
// for lock file / stale PID detection.
func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
