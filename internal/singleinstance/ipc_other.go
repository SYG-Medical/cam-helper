//go:build !linux && !darwin && !windows

package singleinstance

type DummyIPCManager struct{}

// NewIPCManager initializes a fallback IPCManager.
func NewIPCManager() (IPCManager, error) {
	return &DummyIPCManager{}, nil
}

// Listen always returns true for fallback.
func (m *DummyIPCManager) Listen() (bool, error) {
	return true, nil
}

// AcceptWakeup blocks forever as a fallback.
func (m *DummyIPCManager) AcceptWakeup() (bool, error) {
	select {}
}

// NotifyPrimary is a no-op for fallback.
func (m *DummyIPCManager) NotifyPrimary() error {
	return nil
}

// Close is a no-op for fallback.
func (m *DummyIPCManager) Close() error {
	return nil
}
