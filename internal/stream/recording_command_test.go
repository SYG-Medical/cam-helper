package stream

import (
	"strings"
	"testing"

	"nystavision/internal/config"
)

func TestRTSPRecordingUsesCompressedStreamCopyAndPipePreview(t *testing.T) {
	manager := &Manager{
		activeW:          1280,
		activeH:          720,
		activeFPS:        15,
		recordProfile:    SoftwareHardwareProfile(),
		currentSegment:   -1,
		enableHTTPServer: true,
	}
	camera := config.CameraSource{Type: "rtsp", RTSPURL: "rtsp://example/live", FPS: 15}
	args := manager.buildRTSPArgs(camera, "/tmp/cam.mkv", SoftwareHardwareProfile(), true)
	command := strings.Join(args, " ")

	if !strings.Contains(command, "-c:v mjpeg -q:v 2 -f matroska") {
		t.Fatalf("RTSP recording should use compressed mjpeg: %s", command)
	}
	if !strings.Contains(command, "-pix_fmt rgba -f rawvideo -") {
		t.Fatalf("preview should remain an in-memory stdout pipe: %s", command)
	}
	if strings.Contains(command, "rawvideo -y /tmp/cam.mkv") {
		t.Fatalf("raw video must never be written to the recording file: %s", command)
	}
}

func TestWebcamRecordingEncodesMJPEGBeforeMatroska(t *testing.T) {
	manager := &Manager{
		activeW:        640,
		activeH:        480,
		activeFPS:      5,
		activeFormat:   "yuyv422",
		recordProfile:  SoftwareHardwareProfile(),
		currentSegment: -1,
	}
	camera := config.CameraSource{Type: "webcam", Device: "/dev/video0"}
	args := manager.buildWebcamArgs(camera, "/tmp/cam.mkv", SoftwareHardwareProfile())
	command := strings.Join(args, " ")

	if !strings.Contains(command, "-c:v mjpeg") || !strings.Contains(command, "-f matroska -y /tmp/cam.mkv") {
		t.Fatalf("webcam recording should be compressed MJPEG in MKV: %s", command)
	}
}

func TestEncoderArgsKeepNativeFPSIndependent(t *testing.T) {
	manager := &Manager{
		activeW:          320,
		activeH:          240,
		activeFPS:        5,
		enableHTTPServer: true,
		currentSegment:   -1,
	}
	camera := config.CameraSource{Type: "rtsp", RTSPURL: "rtsp://example/slow", FPS: 60}
	command := strings.Join(manager.buildRTSPArgs(camera, "/tmp/slow.mkv", SoftwareHardwareProfile(), true), " ")
	if !strings.Contains(command, "fps=5") {
		t.Fatalf("camera preview/recording pipeline should retain active 5 FPS: %s", command)
	}
}
