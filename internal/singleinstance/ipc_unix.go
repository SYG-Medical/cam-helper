//go:build linux || darwin

package singleinstance

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
)

type UnixIPCManager struct {
	socketPath string
	listener   net.Listener
}

// NewIPCManager initializes a new IPCManager for Unix.
func NewIPCManager() (IPCManager, error) {
	var socketPath string
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		socketPath = filepath.Join(xdg, "nystavision.sock")
	} else {
		socketPath = filepath.Join(os.TempDir(), "nystavision.sock")
	}

	return &UnixIPCManager{
		socketPath: socketPath,
	}, nil
}

// Listen starts the UDS listener.
// Detects and cleans up dead/orphaned socket files automatically.
func (m *UnixIPCManager) Listen() (bool, error) {
	if _, err := os.Stat(m.socketPath); err == nil {
		// Socket file exists, try connecting to see if another instance is alive
		conn, err := net.Dial("unix", m.socketPath)
		if err == nil {
			// Active instance is running
			conn.Close()
			return false, nil
		}

		// Connection failed - socket is dead/stale. Let's remove it.
		if err := os.Remove(m.socketPath); err != nil {
			return false, fmt.Errorf("failed to clean up stale socket file: %w", err)
		}
	}

	l, err := net.Listen("unix", m.socketPath)
	if err != nil {
		return false, fmt.Errorf("failed to listen on Unix socket: %w", err)
	}

	m.listener = l
	return true, nil
}

// AcceptWakeup blocks and reads the wake-up signal.
// It only returns an error if the underlying listener is closed or encounters a fatal error.
func (m *UnixIPCManager) AcceptWakeup() (bool, error) {
	if m.listener == nil {
		return false, errors.New("listener not initialized")
	}

	conn, err := m.listener.Accept()
	if err != nil {
		return false, err
	}
	defer conn.Close()

	buf := make([]byte, len(WakeUpPayload))
	_, err = io.ReadFull(conn, buf)
	if err != nil {
		// Non-fatal client error, keep loop running by returning no error
		return false, nil
	}

	return string(buf) == WakeUpPayload, nil
}

// NotifyPrimary sends the wake-up payload to the running instance.
func (m *UnixIPCManager) NotifyPrimary() error {
	conn, err := net.Dial("unix", m.socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to primary instance socket: %w", err)
	}
	defer conn.Close()

	_, err = conn.Write([]byte(WakeUpPayload))
	return err
}

// Close closes the listener and deletes the UDS socket file.
func (m *UnixIPCManager) Close() error {
	var err error
	if m.listener != nil {
		err = m.listener.Close()
		m.listener = nil
	}
	if m.socketPath != "" {
		if rmErr := os.Remove(m.socketPath); rmErr != nil && !os.IsNotExist(rmErr) {
			if err == nil {
				err = rmErr
			}
		}
	}
	return err
}
