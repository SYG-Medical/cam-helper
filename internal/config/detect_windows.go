//go:build windows

package config

import (
	"fmt"
	"os/exec"
	"strings"
)

// DetectWebcams enumerates connected cameras on Windows using PowerShell and WMI.
func DetectWebcams() []CameraSource {
	// Query PNP devices that are of class 'Camera' or 'Image' and have Status 'OK'.
	// This captures most webcams and capture cards.
	cmd := exec.Command("powershell", "-NoProfile", "-Command", "Get-PnpDevice | Where-Object {($_.Class -eq 'Camera' -or $_.Class -eq 'Image') -and $_.Status -eq 'OK'} | Select-Object -ExpandProperty FriendlyName")
	
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	lines := strings.Split(string(output), "\n")
	var cameras []CameraSource
	idx := 1

	for _, line := range lines {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}

		cameras = append(cameras, CameraSource{
			ID:      fmt.Sprintf("webcam-win-%d", idx),
			Name:    name,
			Type:    "webcam",
			Device:  "video=" + name,
			Width:   1280,
			Height:  720,
			FPS:     30,
			Enabled: true,
		})
		idx++
	}

	return cameras
}
