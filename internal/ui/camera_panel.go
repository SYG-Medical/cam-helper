package ui

import (
	"image"
	"image/color"
	"runtime"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// CameraPanel represents a single camera view in the grid.
type CameraPanel struct {
	widget.BaseWidget

	CameraID   string
	CameraName string

	img           *canvas.Image
	nameLabel     *canvas.Text
	statusDot     *canvas.Circle
	sourceSelect  *widget.Select
	selectionRect *canvas.Rectangle
	overlay       *fyne.Container
	content       *fyne.Container

	mu      sync.Mutex
	rgbaImg *image.RGBA

	onSelect func(cameraID string)
	selected bool
}

// NewCameraPanel creates a new camera panel widget.
func NewCameraPanel(cameraID, cameraName string, onSelect func(string)) *CameraPanel {
	cp := &CameraPanel{
		CameraID:   cameraID,
		CameraName: cameraName,
		onSelect:   onSelect,
	}

	// Create placeholder image
	placeholder := image.NewRGBA(image.Rect(0, 0, 320, 240))
	// Fill with dark gray
	for i := 0; i < len(placeholder.Pix); i += 4 {
		placeholder.Pix[i] = 30   // R
		placeholder.Pix[i+1] = 30 // G
		placeholder.Pix[i+2] = 30 // B
		placeholder.Pix[i+3] = 255 // A
	}

	cp.img = canvas.NewImageFromImage(placeholder)
	cp.img.FillMode = canvas.ImageFillContain
	cp.img.ScaleMode = canvas.ImageScaleFastest

	cp.nameLabel = canvas.NewText(cameraName, color.White)
	cp.nameLabel.TextSize = 14
	cp.nameLabel.TextStyle = fyne.TextStyle{Bold: true}

	// Status dot - red by default (not streaming)
	cp.statusDot = canvas.NewCircle(color.RGBA{R: 200, G: 50, B: 50, A: 255})
	cp.statusDot.Resize(fyne.NewSize(10, 10))

	// Overlay with name and status
	cp.overlay = container.NewHBox(
		cp.statusDot,
		cp.nameLabel,
	)

	// Selection border rectangle
	cp.selectionRect = canvas.NewRectangle(color.Transparent)
	cp.selectionRect.StrokeWidth = 3
	cp.selectionRect.StrokeColor = color.Transparent

	// Stack containing image, text overlay, and selection border
	previewStack := container.NewStack(
		cp.img,
		container.NewVBox(
			cp.overlay,
		),
		cp.selectionRect,
	)

	// Dropdown selector
	cp.sourceSelect = widget.NewSelect(nil, nil)
	cp.sourceSelect.PlaceHolder = "Kaynak Seçin"

	// Combine: preview on top, select dropdown at the bottom
	cp.content = container.NewBorder(
		nil,
		cp.sourceSelect,
		nil,
		nil,
		previewStack,
	)

	cp.ExtendBaseWidget(cp)
	return cp
}

// CreateRenderer implements fyne.Widget.
func (cp *CameraPanel) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(cp.content)
}

// Tapped handles tap events to select this camera.
func (cp *CameraPanel) Tapped(_ *fyne.PointEvent) {
	if cp.onSelect != nil {
		cp.onSelect(cp.CameraID)
	}
}

// TappedSecondary handles right-click (unused for now).
func (cp *CameraPanel) TappedSecondary(_ *fyne.PointEvent) {}

// UpdateSources updates the options and selection of the dropdown.
func (cp *CameraPanel) UpdateSources(options []string, selected string, onChanged func(string)) {
	fyne.Do(func() {
		cp.sourceSelect.OnChanged = nil
		cp.sourceSelect.Options = options
		cp.sourceSelect.SetSelected(selected)
		cp.sourceSelect.OnChanged = onChanged
		cp.sourceSelect.Refresh()
	})
}

// UpdateFrame updates the camera panel with a new video frame.
func (cp *CameraPanel) UpdateFrame(width, height int, pix []byte) {
	cp.mu.Lock()
	if cp.rgbaImg == nil || cp.rgbaImg.Rect.Dx() != width || cp.rgbaImg.Rect.Dy() != height {
		cp.rgbaImg = image.NewRGBA(image.Rect(0, 0, width, height))
		cp.img.Image = cp.rgbaImg
	}

	if runtime.GOOS == "windows" {
		// BGRA → RGBA
		for i := 0; i < len(pix) && i < len(cp.rgbaImg.Pix); i += 4 {
			cp.rgbaImg.Pix[i] = pix[i+2]
			cp.rgbaImg.Pix[i+1] = pix[i+1]
			cp.rgbaImg.Pix[i+2] = pix[i]
			cp.rgbaImg.Pix[i+3] = pix[i+3]
		}
	} else {
		copy(cp.rgbaImg.Pix, pix)
	}
	cp.mu.Unlock()

	fyne.Do(func() {
		cp.img.Refresh()
	})
}

// GetLastFrame returns a copy of the last frame for recording.
func (cp *CameraPanel) GetLastFrame() *image.RGBA {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	if cp.rgbaImg == nil {
		return nil
	}
	// Return the image directly (caller should not modify)
	return cp.rgbaImg
}

// SetStatus updates the status indicator color.
func (cp *CameraPanel) SetStatus(running bool, hasError bool) {
	var c color.Color
	if running && !hasError {
		c = color.RGBA{R: 50, G: 200, B: 50, A: 255} // Green
	} else if running && hasError {
		c = color.RGBA{R: 230, G: 180, B: 30, A: 255} // Yellow
	} else {
		c = color.RGBA{R: 200, G: 50, B: 50, A: 255} // Red
	}
	fyne.Do(func() {
		cp.statusDot.FillColor = c
		cp.statusDot.Refresh()
	})
}

// SetSelected updates the visual selection state.
func (cp *CameraPanel) SetSelected(selected bool) {
	cp.selected = selected
	fyne.Do(func() {
		if selected {
			cp.selectionRect.StrokeColor = color.RGBA{R: 0, G: 150, B: 255, A: 255} // Blue border
		} else {
			cp.selectionRect.StrokeColor = color.Transparent
		}
		cp.selectionRect.Refresh()
	})
}

// SetName updates the camera name displayed on the panel.
func (cp *CameraPanel) SetName(name string) {
	cp.CameraName = name
	fyne.Do(func() {
		cp.nameLabel.Text = name
		cp.nameLabel.Refresh()
	})
}
