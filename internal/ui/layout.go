package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
)

// CalculateGrid returns the number of columns and rows for a given camera count.
// The layout adapts as cameras are added:
//
//	2 → 2×1, 3-4 → 2×2, 5-6 → 3×2, 7-9 → 3×3
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

// BuildResizableCameraGrid builds a nested resizable grid using Split containers.
func BuildResizableCameraGrid(objects []fyne.CanvasObject, cols, rows int) fyne.CanvasObject {
	if len(objects) == 0 {
		return layout.NewSpacer()
	}

	// Create each row as a horizontal split container
	var rowContainers []fyne.CanvasObject
	for r := 0; r < rows; r++ {
		var rowItems []fyne.CanvasObject
		for c := 0; c < cols; c++ {
			idx := r*cols + c
			if idx < len(objects) {
				rowItems = append(rowItems, objects[idx])
			} else {
				// Fill empty cells with a spacer to maintain alignment
				rowItems = append(rowItems, layout.NewSpacer())
			}
		}
		rowContainers = append(rowContainers, buildHorizontalSplit(rowItems))
	}

	// Split the row containers vertically
	return buildVerticalSplit(rowContainers)
}

func buildHorizontalSplit(items []fyne.CanvasObject) fyne.CanvasObject {
	if len(items) == 0 {
		return layout.NewSpacer()
	}
	if len(items) == 1 {
		return items[0]
	}
	return buildHSplitRecursive(items)
}

func buildHSplitRecursive(items []fyne.CanvasObject) fyne.CanvasObject {
	if len(items) == 0 {
		return layout.NewSpacer()
	}
	if len(items) == 1 {
		return items[0]
	}

	left := items[0]
	right := buildHSplitRecursive(items[1:])

	split := container.NewHSplit(left, right)
	// Calculate the offset to distribute space evenly initially.
	split.Offset = 1.0 / float64(len(items))
	return split
}

func buildVerticalSplit(rows []fyne.CanvasObject) fyne.CanvasObject {
	if len(rows) == 0 {
		return layout.NewSpacer()
	}
	if len(rows) == 1 {
		return rows[0]
	}
	return buildVSplitRecursive(rows)
}

func buildVSplitRecursive(rows []fyne.CanvasObject) fyne.CanvasObject {
	if len(rows) == 0 {
		return layout.NewSpacer()
	}
	if len(rows) == 1 {
		return rows[0]
	}

	top := rows[0]
	bottom := buildVSplitRecursive(rows[1:])

	split := container.NewVSplit(top, bottom)
	// Calculate the offset to distribute space evenly initially.
	split.Offset = 1.0 / float64(len(rows))
	return split
}

