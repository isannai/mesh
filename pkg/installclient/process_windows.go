//go:build windows

package installclient

import "syscall"

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
	const PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
	h, err := syscall.OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	syscall.CloseHandle(h)
	return true
}
