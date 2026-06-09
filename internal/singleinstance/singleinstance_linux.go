//go:build linux

package singleinstance

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

var lockFile *os.File

// Acquire tries to acquire the single-instance lock via flock on a lock file.
// Returns true if this is the first instance, false if another is running.
func Acquire() (bool, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return false, fmt.Errorf("cache dir: %w", err)
	}

	lockDir := filepath.Join(cacheDir, "SYG", "NystaVision")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return false, fmt.Errorf("mkdir: %w", err)
	}

	lockPath := filepath.Join(lockDir, "app.lock")

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return false, fmt.Errorf("open lock: %w", err)
	}

	// LOCK_EX | LOCK_NB — exclusive, non-blocking
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		f.Close()
		// EWOULDBLOCK means another instance holds the lock
		if err == syscall.EWOULDBLOCK {
			return false, nil
		}
		return false, fmt.Errorf("flock: %w", err)
	}

	lockFile = f
	return true, nil
}

// Release releases the flock and removes the lock file.
func Release() {
	if lockFile != nil {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		lockFile.Close()
		lockFile = nil
	}
}
