package stream

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordingSessionSegmentsAndSnapshot(t *testing.T) {
	session, err := NewRecordingSession(t.TempDir(), []CameraRecording{{
		ID: "cam-1", Name: "Camera 1", Width: 640, Height: 480, FPS: 15,
	}}, 1, 1, "")
	if err != nil {
		t.Fatal(err)
	}

	start := session.StartedAt.Add(2 * time.Second)
	index, path, err := session.BeginSegment("cam-1", start)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(path) != ".mkv" {
		t.Fatalf("segment must be compressed container, got %q", path)
	}
	if err := os.WriteFile(path, []byte("compressed"), 0o644); err != nil {
		t.Fatal(err)
	}
	session.EndSegment("cam-1", index, start.Add(3*time.Second))
	session.Finish(start.Add(5 * time.Second))

	snapshot := session.Snapshot()
	camera := snapshot.Cameras["cam-1"]
	if camera == nil || len(camera.Segments) != 1 {
		t.Fatalf("unexpected snapshot: %#v", snapshot.Cameras)
	}
	if got := camera.Segments[0].EndedAt.Sub(camera.Segments[0].StartedAt); got != 3*time.Second {
		t.Fatalf("segment duration = %v", got)
	}
	if session.DirectorySize() != uint64(len("compressed")) {
		t.Fatalf("unexpected directory size: %d", session.DirectorySize())
	}
}

func TestEstimateCameraRecordingUsesPerCameraProperties(t *testing.T) {
	low := []CameraRecording{
		{Width: 640, Height: 360, FPS: 5},
		{Width: 1280, Height: 720, FPS: 30},
	}
	allHigh := []CameraRecording{
		{Width: 1280, Height: 720, FPS: 30},
		{Width: 1280, Height: 720, FPS: 30},
	}
	if EstimateCameraRecordingBytesPerMinute(low, 2, 1) >= EstimateCameraRecordingBytesPerMinute(allHigh, 2, 1) {
		t.Fatal("low-resolution/low-FPS camera should reduce the storage estimate")
	}
}
