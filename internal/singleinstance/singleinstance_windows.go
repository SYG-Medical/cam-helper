//go:build windows

package singleinstance

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	kernel32     = syscall.NewLazyDLL("kernel32.dll")
	createMutexW = kernel32.NewProc("CreateMutexW")
	mutexHandle  syscall.Handle
)

const mutexName = "Global\\SYG_NystaVision_SingleInstance"

// Acquire tries to acquire the single-instance lock.
// Returns true if this is the first instance, false if another is running.
func Acquire() (bool, error) {
	name, err := syscall.UTF16PtrFromString(mutexName)
	if err != nil {
		return false, fmt.Errorf("utf16: %w", err)
	}

	handle, _, callErr := createMutexW.Call(0, 1, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		return false, fmt.Errorf("CreateMutex: %v", callErr)
	}

	mutexHandle = syscall.Handle(handle)

	// ERROR_ALREADY_EXISTS = 183
	if callErr == syscall.ERROR_ALREADY_EXISTS {
		syscall.CloseHandle(mutexHandle)
		mutexHandle = 0
		return false, nil
	}

	return true, nil
}

// Release releases the mutex (called on app exit).
func Release() {
	if mutexHandle != 0 {
		syscall.CloseHandle(mutexHandle)
		mutexHandle = 0
	}
}
