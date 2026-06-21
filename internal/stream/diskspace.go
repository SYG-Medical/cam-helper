package stream

import (
	"math"
)

// EstimateCameraRecordingBytesPerMinute estimates compressed H.264/MKV bytes.
// No raw frame data is written to disk by the recording pipeline.
func EstimateCameraRecordingBytesPerMinute(cameras []CameraRecording, cols, rows int) uint64 {
	if len(cameras) == 0 {
		return 0
	}
	var cameraPerSecond float64
	maxW, maxH, maxFPS := 0, 0, 0
	for _, camera := range cameras {
		w, h, fps := normalizedVideoParams(camera.Width, camera.Height, camera.FPS)
		cameraPerSecond += float64(w*h*fps) * 0.07
		if w > maxW {
			maxW = w
		}
		if h > maxH {
			maxH = h
		}
		if fps > maxFPS {
			maxFPS = fps
		}
	}
	if maxFPS > 60 {
		maxFPS = 60
	}
	if cols <= 0 {
		cols = 1
	}
	if rows <= 0 {
		rows = 1
	}
	generalPerSecond := float64(cols*maxW*rows*maxH*maxFPS) * 0.07

	// Peak post-process usage: source segments + individual outputs + aligned
	// temporary camera files + final grid.
	peakPerMinute := (cameraPerSecond*3.0 + generalPerSecond) * 60.0
	return uint64(math.Ceil(peakPerMinute * 1.20))
}

func EstimateCameraRecordingAvailableMinutes(freeDiskBytes uint64, cameras []CameraRecording, cols, rows int) float64 {
	bytesPerMinute := EstimateCameraRecordingBytesPerMinute(cameras, cols, rows)
	if bytesPerMinute == 0 {
		return 0
	}
	return float64(freeDiskBytes) / float64(bytesPerMinute)
}

// EstimateRecordingBytesPerMinute calculates the estimated peak bytes used per minute
// of recording. This accounts for the temp file and post-processed outputs (approx. 2x temp file size)
// plus a 15% safety margin.
func EstimateRecordingBytesPerMinute(gridW, gridH, fps, numCameras int) uint64 {
	if gridW <= 0 || gridH <= 0 || fps <= 0 {
		return 0
	}
	bytesPerSecond := float64(gridW*gridH*fps) * 0.07 // CRF 23 upper bound estimate
	tempPerMinute := bytesPerSecond * 60.0
	// Peak disk usage is during post-processing where temp file and outputs coexist.
	// We use the 2.0x multiplier as a safe upper bound.
	totalPerMinute := tempPerMinute * 2.0
	totalWithMargin := totalPerMinute * 1.15 // 15% safety margin
	return uint64(math.Ceil(totalWithMargin))
}

// EstimateAvailableMinutes calculates how many minutes of recording space is available
// on the disk block given its free space bytes.
func EstimateAvailableMinutes(freeDiskBytes uint64, gridW, gridH, fps, numCameras int) float64 {
	bytesPerMinute := EstimateRecordingBytesPerMinute(gridW, gridH, fps, numCameras)
	if bytesPerMinute == 0 {
		return 0
	}
	return float64(freeDiskBytes) / float64(bytesPerMinute)
}
