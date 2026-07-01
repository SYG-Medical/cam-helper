package gui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

type clickableCard struct {
	widget.BaseWidget
	content  fyne.CanvasObject
	onTapped func()
}

func newClickableCard(content fyne.CanvasObject, onTapped func()) *clickableCard {
	c := &clickableCard{
		content:  content,
		onTapped: onTapped,
	}
	c.ExtendBaseWidget(c)
	return c
}

func (c *clickableCard) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.content)
}

func (c *clickableCard) Tapped(_ *fyne.PointEvent) {
	if c.onTapped != nil {
		c.onTapped()
	}
}

func CollectSplitOffsets(o fyne.CanvasObject) []float64 {
	var offsets []float64
	var traverse func(fyne.CanvasObject)
	traverse = func(obj fyne.CanvasObject) {
		if obj == nil {
			return
		}
		if split, ok := obj.(*container.Split); ok {
			offsets = append(offsets, split.Offset)
			traverse(split.Leading)
			traverse(split.Trailing)
		} else if co, ok := obj.(*fyne.Container); ok {
			for _, child := range co.Objects {
				traverse(child)
			}
		}
	}
	traverse(o)
	return offsets
}

func ApplySplitOffsets(o fyne.CanvasObject, offsets []float64) int {
	if len(offsets) == 0 {
		return 0
	}
	idx := 0
	var traverse func(fyne.CanvasObject)
	traverse = func(obj fyne.CanvasObject) {
		if obj == nil || idx >= len(offsets) {
			return
		}
		if split, ok := obj.(*container.Split); ok {
			if idx < len(offsets) {
				split.Offset = offsets[idx]
				idx++
				split.Refresh()
			}
			traverse(split.Leading)
			traverse(split.Trailing)
		} else if co, ok := obj.(*fyne.Container); ok {
			for _, child := range co.Objects {
				traverse(child)
			}
		}
	}
	traverse(o)
	return idx
}
