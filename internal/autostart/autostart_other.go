//go:build !windows && !linux

package autostart

func IsEnabled() (bool, error) {
	return false, nil
}

func SetEnabled(enabled bool) error {
	_ = enabled
	return nil
}
