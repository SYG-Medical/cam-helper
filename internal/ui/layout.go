package ui

// CalculateGrid returns the number of columns and rows for a given camera count.
// The layout adapts as cameras are added:
//   2 → 2×1, 3-4 → 2×2, 5-6 → 3×2, 7-9 → 3×3
func CalculateGrid(cameraCount int) (cols, rows int) {
	switch {
	case cameraCount <= 1:
		return 1, 1
	case cameraCount == 2:
		return 2, 1
	case cameraCount <= 4:
		return 2, 2
	case cameraCount <= 6:
		return 3, 2
	default: // 7-9
		return 3, 3
	}
}
