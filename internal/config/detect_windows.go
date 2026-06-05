//go:build windows

package config

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"sync"
	"time"
)

var (
	cachedWebcams []CameraSource
	cacheTime     time.Time
	cacheMu       sync.Mutex
)

// DetectWebcams enumerates connected cameras on Windows using FFmpeg's dshow list.
func DetectWebcams() []CameraSource {
	cacheMu.Lock()
	if time.Since(cacheTime) < 15*time.Second && cachedWebcams != nil {
		res := make([]CameraSource, len(cachedWebcams))
		copy(res, cachedWebcams)
		cacheMu.Unlock()
		return res
	}
	cacheMu.Unlock()

	var cameras []CameraSource
	idx := 1

	ffmpegPath := "ffmpeg"
	if exe, err := os.Executable(); err == nil {
		localFFmpeg := filepath.Join(filepath.Dir(exe), "third_party", "ffmpeg", "ffmpeg.exe")
		if _, err := os.Stat(localFFmpeg); err == nil {
			ffmpegPath = localFFmpeg
		}
	}

	cmd := exec.Command(ffmpegPath, "-list_devices", "true", "-f", "dshow", "-i", "dummy")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	// ffmpeg prints device list to stderr, and exits with an error code since "dummy" input fails
	output, _ := cmd.CombinedOutput()
	
	scanner := bufio.NewScanner(bytes.NewReader(output))
	inVideoSection := false
	
	for scanner.Scan() {
		line := scanner.Text()
		
		if strings.Contains(line, "DirectShow video devices") {
			inVideoSection = true
			continue
		}
		if strings.Contains(line, "DirectShow audio devices") {
			inVideoSection = false
			continue
		}
		
		if inVideoSection {
			// Extract the name inside quotes. Avoid the alternative name line.
			if strings.Contains(line, "Alternative name") {
				continue
			}
			firstQuote := strings.Index(line, "\"")
			lastQuote := strings.LastIndex(line, "\"")
			if firstQuote != -1 && lastQuote != -1 && firstQuote < lastQuote {
				name := line[firstQuote+1 : lastQuote]
				
				// Avoid adding standard virtual cameras as physical webcams
				if name == "SYG Camera" || name == "OBS Virtual Camera" {
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
		}
	}

	// Fallback to powershell if ffmpeg failed or returned no cameras
	if len(cameras) == 0 {
		cameras = detectWebcamsPowerShell()
	}

	cacheMu.Lock()
	cachedWebcams = make([]CameraSource, len(cameras))
	copy(cachedWebcams, cameras)
	cacheTime = time.Now()
	cacheMu.Unlock()

	return cameras
}

func detectWebcamsPowerShell() []CameraSource {
	cmd := exec.Command("powershell", "-NoProfile", "-Command", "Get-PnpDevice | Where-Object {($_.Class -eq 'Camera' -or $_.Class -eq 'Image') -and $_.Status -eq 'OK'} | Select-Object -ExpandProperty FriendlyName")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	
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

