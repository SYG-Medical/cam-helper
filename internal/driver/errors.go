package driver

import "errors"

// ErrNoConsumer is returned when the virtual camera filter is not yet
// consuming frames (e.g. the DirectShow filter hasn't opened the device).
// Callers should treat this as a transient, non-fatal condition.
var ErrNoConsumer = errors.New("virtual camera has no consumer yet")
