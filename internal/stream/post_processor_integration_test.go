package stream

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPostProcessorKeepsGeneralGapAndSkipsIndividualGap(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe is not installed")
	}

	root := t.TempDir()
	segment := func(name, color, duration string) string {
		path := filepath.Join(root, name+".mkv")
		cmd := exec.Command(ffmpeg,
			"-hide_banner", "-loglevel", "error",
			"-f", "lavfi", "-i", "color=c="+color+":s=320x180:r=5",
			"-t", duration, "-c:v", "libx264", "-pix_fmt", "yuv420p",
			"-f", "matroska", "-y", path,
		)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("create segment: %v: %s", err, output)
		}
		return path
	}

	start := time.Unix(1_760_000_000, 0)
	session := &RecordingSession{
		ID:        "test",
		TempDir:   root,
		StartedAt: start,
		EndedAt:   start.Add(4 * time.Second),
		Cols:      2,
		Rows:      1,
		Cameras: map[string]*CameraRecording{
			"cam-1": {
				ID: "cam-1", Name: "Camera 1", Width: 320, Height: 180, FPS: 5, Order: 0,
				Segments: []RecordingSegment{
					{Path: segment("cam1_a", "red", "1"), StartedAt: start, EndedAt: start.Add(time.Second)},
					{Path: segment("cam1_b", "blue", "1"), StartedAt: start.Add(3 * time.Second), EndedAt: start.Add(4 * time.Second)},
				},
			},
			"cam-2": {
				ID: "cam-2", Name: "Camera 2", Width: 320, Height: 180, FPS: 5, Order: 1,
				Segments: []RecordingSegment{
					{Path: segment("cam2", "green", "4"), StartedAt: start, EndedAt: start.Add(4 * time.Second)},
				},
			},
		},
	}
	outDir := filepath.Join(root, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}

	processor := &PostProcessor{ffmpegPath: ffmpeg, profile: SoftwareHardwareProfile()}
	result := processor.Process(context.Background(), session, outDir, nil)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if len(result.Files) != 3 {
		t.Fatalf("expected two camera files and one general file, got %v", result.Files)
	}

	var cameraOne, general string
	for _, file := range result.Files {
		base := filepath.Base(file)
		if strings.HasPrefix(base, "Camera 1_") {
			cameraOne = file
		}
		if strings.HasPrefix(base, "Genel_") {
			general = file
		}
	}
	if cameraOne == "" || general == "" {
		t.Fatalf("missing expected outputs: %v", result.Files)
	}
	cameraDuration := probeDuration(t, ffprobe, cameraOne)
	generalDuration := probeDuration(t, ffprobe, general)
	if cameraDuration < 1.8 || cameraDuration > 2.4 {
		t.Fatalf("individual camera should skip the two-second outage, duration=%f", cameraDuration)
	}
	if generalDuration < 3.8 || generalDuration > 4.4 {
		t.Fatalf("general video should preserve the outage interval, duration=%f", generalDuration)
	}
}

func probeDuration(t *testing.T, ffprobe, path string) float64 {
	t.Helper()
	output, err := exec.Command(ffprobe,
		"-v", "error", "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", path,
	).Output()
	if err != nil {
		t.Fatal(err)
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil {
		t.Fatal(err)
	}
	return duration
}
