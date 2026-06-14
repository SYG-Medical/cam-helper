//go:build windows

package singleinstance

import (
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/windows"
)

type WindowsIPCManager struct {
	pipeName string
	handle   windows.Handle
	closed   bool
}

// NewIPCManager initializes a new IPCManager for Windows.
func NewIPCManager() (IPCManager, error) {
	return &WindowsIPCManager{
		pipeName: `\\.\pipe\nystavision-ipc`,
		handle:   windows.InvalidHandle,
	}, nil
}

// Listen starts the named pipe.
func (m *WindowsIPCManager) Listen() (bool, error) {
	namePtr, err := windows.UTF16PtrFromString(m.pipeName)
	if err != nil {
		return false, err
	}

	h, err := windows.CreateNamedPipe(
		namePtr,
		windows.PIPE_ACCESS_DUPLEX,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT,
		1, // Max instances = 1
		512,
		512,
		0,
		nil,
	)

	if h == windows.InvalidHandle {
		if err == windows.ERROR_PIPE_BUSY || err == windows.ERROR_ACCESS_DENIED || err == windows.ERROR_ALREADY_EXISTS {
			return false, nil
		}
		return false, fmt.Errorf("CreateNamedPipe failed: %w", err)
	}

	m.handle = h
	return true, nil
}

// AcceptWakeup blocks and reads the wake-up signal from a named pipe connection.
func (m *WindowsIPCManager) AcceptWakeup() (bool, error) {
	if m.handle == windows.InvalidHandle {
		return false, errors.New("named pipe handle is invalid")
	}
	if m.closed {
		return false, errors.New("manager closed")
	}

	err := windows.ConnectNamedPipe(m.handle, nil)
	if err != nil {
		if m.closed {
			return false, nil
		}
		if err != windows.ERROR_PIPE_CONNECTED {
			return false, fmt.Errorf("ConnectNamedPipe failed: %w", err)
		}
	}

	buf := make([]byte, len(WakeUpPayload))
	var bytesRead uint32
	err = windows.ReadFile(m.handle, buf, &bytesRead, nil)
	
	// Disconnect client so next client can connect to the same pipe handle
	_ = windows.DisconnectNamedPipe(m.handle)

	if err != nil {
		// Non-fatal read error (e.g. client closed the connection early), keep loop running
		return false, nil
	}

	return string(buf[:bytesRead]) == WakeUpPayload, nil
}

// NotifyPrimary sends the wake-up payload to the running primary instance.
func (m *WindowsIPCManager) NotifyPrimary() error {
	namePtr, err := windows.UTF16PtrFromString(m.pipeName)
	if err != nil {
		return err
	}

	var h windows.Handle
	// Retry loop if pipe is busy
	for {
		h, err = windows.CreateFile(
			namePtr,
			windows.GENERIC_READ|windows.GENERIC_WRITE,
			0,
			nil,
			windows.OPEN_EXISTING,
			0,
			0,
		)
		if err == nil {
			break
		}
		if err == windows.ERROR_PIPE_BUSY {
			if !windows.WaitNamedPipe(namePtr, 2000) {
				return errors.New("timeout waiting for named pipe")
			}
			continue
		}
		return fmt.Errorf("failed to open named pipe: %w", err)
	}
	defer windows.CloseHandle(h)

	payload := []byte(WakeUpPayload)
	var bytesWritten uint32
	err = windows.WriteFile(h, payload, &bytesWritten, nil)
	if err != nil {
		return fmt.Errorf("failed to write to named pipe: %w", err)
	}

	return nil
}

// Close closes the named pipe handle.
func (m *WindowsIPCManager) Close() error {
	m.closed = true
	if m.handle != windows.InvalidHandle {
		_ = windows.DisconnectNamedPipe(m.handle)
		err := windows.CloseHandle(m.handle)
		m.handle = windows.InvalidHandle
		return err
	}
	return nil
}
