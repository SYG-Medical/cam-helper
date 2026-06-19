package stream

import (
	"math"
)

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
