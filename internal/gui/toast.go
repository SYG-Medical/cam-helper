package gui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type ToastProgress struct {
	app     *App
	aligned *fyne.Container
	isShown bool
}

func (t *ToastProgress) Show() {
	if !t.isShown && t.app.overlayContainer != nil {
		t.app.setMainButtonsEnabled(false)
		t.app.overlayContainer.Add(t.aligned)
		t.app.overlayContainer.Refresh()
		t.isShown = true
	}
}

func (t *ToastProgress) Hide() {
	if t.isShown && t.app.overlayContainer != nil {
		t.app.setMainButtonsEnabled(true)
		t.app.overlayContainer.Remove(t.aligned)
		t.app.overlayContainer.Refresh()
		t.isShown = false
	}
}

func (a *App) NewToastProgress(title string, content fyne.CanvasObject) *ToastProgress {
	titleLabel := widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	vbox := container.NewVBox(titleLabel, content)

	bg := canvas.NewRectangle(colorCardSurface)
	bg.CornerRadius = 8
	bg.StrokeColor = theme.PrimaryColor()
	bg.StrokeWidth = 2

	padded := container.NewPadded(vbox)
	toastCard := container.NewStack(bg, padded)

	// Inner padding to offset from the absolute corner
	offsetPadded := container.NewPadded(toastCard)

	bottomRow := container.NewHBox(layout.NewSpacer(), offsetPadded)
	aligned := container.NewBorder(nil, bottomRow, nil, nil)

	return &ToastProgress{
		app:     a,
		aligned: aligned,
	}
}
