package singleinstance

import "log"

const WakeUpPayload = "WAKE_UP"

// IPCManager defines the platform-independent interface for IPC operations.
type IPCManager interface {
	// Listen starts the IPC server listener.
	// Returns true if this is the first instance (listening succeeded).
	// Returns false if another instance is already running (listening failed because it's in use).
	Listen() (bool, error)

	// AcceptWakeup blocks until a connection is accepted and checks if the wake-up signal matches.
	// Returns true if a valid wake-up signal was received.
	AcceptWakeup() (bool, error)

	// NotifyPrimary connects to the primary instance, sends the wake-up signal, and closes.
	NotifyPrimary() error

	// Close cleans up resources (listeners, files, handles).
	Close() error
}

var activateCallback func()

// SetActivateCallback registers the callback to run when another instance is launched.
func SetActivateCallback(cb func()) {
	activateCallback = cb
}

// TriggerActivate invokes the registered activation callback.
func TriggerActivate() {
	log.Println("[singleinstance] TriggerActivate: wakeup signal received, calling activateCallback")
	if activateCallback != nil {
		activateCallback()
	} else {
		log.Println("[singleinstance] TriggerActivate WARNING: activateCallback is nil")
	}
}
