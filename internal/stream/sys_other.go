//go:build !windows

package stream

import (
	"os/exec"
)

func setHideWindow(cmd *exec.Cmd) {
	// No-op on other platforms
}
