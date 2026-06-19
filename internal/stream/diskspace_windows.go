//go:build windows

package stream

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	modkernel32             = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFreeSpaceExW = modkernel32.NewProc("GetDiskFreeSpaceExW")
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

	var freeBytesAvailable uint64
	var totalNumberOfBytes uint64
	var totalNumberOfFreeBytes uint64

	pathPtr, err := syscall.UTF16PtrFromString(checkPath)
	if err != nil {
		return 0, err
	}

	r1, _, errNo := syscall.SyscallN(
		procGetDiskFreeSpaceExW.Addr(),
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		uintptr(unsafe.Pointer(&totalNumberOfBytes)),
		uintptr(unsafe.Pointer(&totalNumberOfFreeBytes)),
	)
	if r1 == 0 {
		if errNo != 0 {
			return 0, errNo
		}
		return 0, syscall.EINVAL
	}

	return freeBytesAvailable, nil
}
