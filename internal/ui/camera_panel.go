package ui

import (
	"image"
	"image/color"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"nystavision/internal/i18n"
)

// Brand colors matching the SYG Medical theme.
var (
	panelDarkSurface  = color.NRGBA{R: 12, G: 15, B: 19, A: 255}   // Deep blackish-gray (#0C0F13)
	panelCardSurface  = color.NRGBA{R: 22, G: 27, B: 34, A: 255}   // Dark card surface (#161B22)
	panelTextPrimary  = color.NRGBA{R: 236, G: 240, B: 241, A: 255} // #ECF0F1
	panelMedicalBlue  = color.NRGBA{R: 46, G: 134, B: 193, A: 255}  // #2E86C1
	panelFreshGreen   = color.NRGBA{R: 39, G: 174, B: 96, A: 255}   // #27AE60
	panelAmberWarning = color.NRGBA{R: 243, G: 156, B: 18, A: 255}  // #F39C12
	panelRedCritical  = color.NRGBA{R: 231, G: 76, B: 60, A: 255}   // #E74C3C
	panelOverlayColor = color.NRGBA{R: 12, G: 15, B: 19, A: 180}   // Dark semi-transparent overlay
)

// CameraPanel represents a single camera view in the grid.
type CameraPanel struct {
	widget.BaseWidget

	CameraID   string
	CameraName string

	img              *canvas.Image
	nameLabel        *canvas.Text
	statusDot        *canvas.Circle
	sourceSelect     *widget.Select
	selectionRect    *canvas.Rectangle
	stoppedRect      *canvas.Rectangle
	stoppedText      *canvas.Text
	stoppedContainer *fyne.Container
	overlay          *fyne.Container
	overlayBg        *canvas.Rectangle
	panelBg          *canvas.Rectangle
	content          *fyne.Container

	mu      sync.Mutex
	rgbaImg *image.RGBA

	onSelect     func(cameraID string)
	onRightClick func(cameraID string, pe *fyne.PointEvent)
	selected     bool
}

// NewCameraPanel creates a new camera panel widget.
func NewCameraPanel(cameraID, cameraName string, onSelect func(string), onRightClick func(string, *fyne.PointEvent)) *CameraPanel {
	cp := &CameraPanel{
		CameraID:     cameraID,
		CameraName:   cameraName,
		onSelect:     onSelect,
		onRightClick: onRightClick,
	}

	// Create placeholder image with dark surface color
	placeholder := image.NewRGBA(image.Rect(0, 0, 320, 240))
	for i := 0; i < len(placeholder.Pix); i += 4 {
		placeholder.Pix[i] = panelDarkSurface.R
		placeholder.Pix[i+1] = panelDarkSurface.G
		placeholder.Pix[i+2] = panelDarkSurface.B
		placeholder.Pix[i+3] = 255
	}

	cp.img = canvas.NewImageFromImage(placeholder)
	cp.img.FillMode = canvas.ImageFillContain
	cp.img.ScaleMode = canvas.ImageScaleFastest

	// Camera name label
	cp.nameLabel = canvas.NewText(cameraName, panelTextPrimary)
	cp.nameLabel.TextSize = 13
	cp.nameLabel.TextStyle = fyne.TextStyle{Bold: true}

	// Status dot - red by default (not streaming)
	cp.statusDot = canvas.NewCircle(panelRedCritical)
	cp.statusDot.Resize(fyne.NewSize(10, 10))

	// Glassmorphism overlay background for name area
	cp.overlayBg = canvas.NewRectangle(panelOverlayColor)
	cp.overlayBg.CornerRadius = 6

	// Overlay with frosted background, name and status
	overlayContent := container.NewHBox(
		cp.statusDot,
		cp.nameLabel,
	)
	cp.overlay = container.NewStack(
		cp.overlayBg,
		container.NewPadded(overlayContent),
	)

	// Selection border rectangle
	cp.selectionRect = canvas.NewRectangle(color.Transparent)
	cp.selectionRect.StrokeWidth = 3
	cp.selectionRect.StrokeColor = color.Transparent
	cp.selectionRect.CornerRadius = 8

	// Stopped overlay with glassmorphism
	cp.stoppedRect = canvas.NewRectangle(panelOverlayColor)
	cp.stoppedText = canvas.NewText(i18n.T("lbl_stopped"), panelTextPrimary)
	cp.stoppedText.Alignment = fyne.TextAlignCenter
	cp.stoppedText.TextSize = 16
	cp.stoppedText.TextStyle = fyne.TextStyle{Bold: true}
	cp.stoppedContainer = container.NewStack(
		cp.stoppedRect,
		container.NewCenter(cp.stoppedText),
	)
	cp.stoppedContainer.Hide()

	// Panel background card
	cp.panelBg = canvas.NewRectangle(panelCardSurface)
	cp.panelBg.CornerRadius = 8

	// Stack containing image, text overlay, stopped overlay, and selection border
	previewStack := container.NewStack(
		cp.img,
		cp.stoppedContainer,
		container.NewVBox(
			cp.overlay,
		),
		cp.selectionRect,
	)

	// Dropdown selector
	cp.sourceSelect = widget.NewSelect(nil, nil)
	cp.sourceSelect.PlaceHolder = i18n.T("lbl_select_source")

	// Combine: panel background, preview on top, select dropdown at the bottom
	innerContent := container.NewBorder(
		nil,
		cp.sourceSelect,
		nil,
		nil,
		previewStack,
	)

	cp.content = container.NewStack(
		cp.panelBg,
		container.NewPadded(innerContent),
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

// TappedSecondary handles right-click.
func (cp *CameraPanel) TappedSecondary(pe *fyne.PointEvent) {
	if cp.onRightClick != nil {
		cp.onRightClick(cp.CameraID, pe)
	}
}

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

	// FFmpeg provides native RGBA so we can do a fast memory copy directly
	copy(cp.rgbaImg.Pix, pix)
	cp.mu.Unlock()

	fyne.Do(func() {
		cp.img.Refresh()
	})
}

// SetStatus updates the status indicator color and stopped state.
func (cp *CameraPanel) SetStatus(running bool, lastError string) {
	hasError := lastError != ""
	var c color.Color
	if running && !hasError {
		c = panelFreshGreen // Green - active
	} else if running && hasError {
		c = panelAmberWarning // Amber - warning
	} else {
		c = panelRedCritical // Red - stopped
	}
	fyne.Do(func() {
		cp.statusDot.FillColor = c
		cp.statusDot.Refresh()

		if !running || hasError {
			cp.makeGrayscale()

			text := i18n.T("lbl_stopped")
			if hasError {
				if strings.Contains(lastError, "I/O error") || strings.Contains(lastError, "Device or resource busy") {
					text = i18n.T("lbl_in_use")
				} else if strings.Contains(lastError, "device not found") {
					text = i18n.T("lbl_not_found")
				} else {
					text = i18n.T("lbl_error")
				}
			}
			cp.stoppedText.Text = text

			cp.stoppedContainer.Show()
			cp.img.Refresh()
		} else {
			cp.stoppedContainer.Hide()
		}
		cp.stoppedContainer.Refresh()
	})
}

// makeGrayscale converts the current preview image to grayscale.
func (cp *CameraPanel) makeGrayscale() {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	if cp.rgbaImg == nil {
		return
	}
	for i := 0; i < len(cp.rgbaImg.Pix); i += 4 {
		r := cp.rgbaImg.Pix[i]
		g := cp.rgbaImg.Pix[i+1]
		b := cp.rgbaImg.Pix[i+2]
		// Standard Luma formula
		gray := uint8(0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b))
		cp.rgbaImg.Pix[i] = gray
		cp.rgbaImg.Pix[i+1] = gray
		cp.rgbaImg.Pix[i+2] = gray
	}
}

// SetSelected updates the visual selection state.
func (cp *CameraPanel) SetSelected(selected bool) {
	cp.selected = selected
	fyne.Do(func() {
		if selected {
			cp.selectionRect.StrokeColor = panelMedicalBlue // Medical Blue border
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
