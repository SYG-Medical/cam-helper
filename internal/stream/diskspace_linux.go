//go:build !windows

package stream

import (
	"os"
	"path/filepath"
	"syscall"
)

// DiskFreeBytes returns the free disk space in bytes for the directory path.
// If the path does not exist, it traverses up to the first existing parent directory.
func DiskFreeBytes(path string) (uint64, error) {
	checkPath := path
	for {
		if _, err := os.Stat(checkPath); err == nil {
			break
		}
		parent := filepath.Dir(checkPath)
		if parent == checkPath {
			break
		}
		checkPath = parent
	}

	var stat syscall.Statfs_t
	err := syscall.Statfs(checkPath, &stat)
	if err != nil {
		return 0, err
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil
}
