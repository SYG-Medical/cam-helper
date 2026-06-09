//go:build !windows && !linux

package singleinstance

// Acquire always returns true on unsupported platforms.
func Acquire() (bool, error) {
	return true, nil
}

// Release is a no-op on unsupported platforms.
func Release() {}
