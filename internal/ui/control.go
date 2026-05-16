package ui

import (
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"rtsp-virtual-cam-agent/internal/config"
	"rtsp-virtual-cam-agent/internal/stream"
)

type ControlPanel struct {
	app      fyne.App
	window   fyne.Window
	streamer *stream.Manager
	cfg      *config.Config
	onQuit   func()
}

func NewControlPanel(streamer *stream.Manager, cfg *config.Config, onQuit func()) *ControlPanel {
	a := app.New()
	w := a.NewWindow("SYG RTSP Virtual Cam Agent")
	w.Resize(fyne.NewSize(400, 300))

	cp := &ControlPanel{
		app:      a,
		window:   w,
		streamer: streamer,
		cfg:      cfg,
		onQuit:   onQuit,
	}

	w.SetContent(cp.buildUI())
	w.SetCloseIntercept(cp.handleClose)

	return cp
}

func (cp *ControlPanel) Show() {
	cp.window.ShowAndRun()
}

func (cp *ControlPanel) buildUI() fyne.CanvasObject {
	statusLabel := widget.NewLabel("Status: Checking...")
	statusLabel.Alignment = fyne.TextAlignCenter

	urlEntry := widget.NewEntry()
	urlEntry.SetText(cp.cfg.RTSPURL)
	urlEntry.PlaceHolder = "rtsp://..."

	startBtn := widget.NewButtonWithIcon("Start", theme.MediaPlayIcon(), func() {
		cp.cfg.RTSPURL = urlEntry.Text
		// Save config logic would go here if needed, or we just use the updated value
		if err := cp.streamer.Start(); err != nil {
			dialog.ShowError(err, cp.window)
		}
	})

	stopBtn := widget.NewButtonWithIcon("Stop", theme.MediaStopIcon(), func() {
		cp.streamer.Stop()
	})

	go func() {
		ticker := time.NewTicker(time.Second)
		for range ticker.C {
			state := cp.streamer.State()
			status := "Stopped"
			if state.Running {
				status = "Running"
				if state.LastError != "" {
					status = "Recovering"
				}
			}
			statusLabel.SetText(fmt.Sprintf("Status: %s", status))
		}
	}()

	return container.NewVBox(
		widget.NewLabelWithStyle("RTSP Virtual Cam Agent", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		statusLabel,
		widget.NewForm(
			widget.NewFormItem("RTSP URL", urlEntry),
		),
		container.NewHBox(startBtn, stopBtn),
		widget.NewButton("Open Logs", func() {
			// open logs logic
		}),
	)
}

func (cp *ControlPanel) handleClose() {
	dialog.ShowConfirm("Quit Application?", "Do you want to completely quit the application or just minimize to tray?", func(quit bool) {
		if quit {
			cp.onQuit()
			cp.window.Close()
		} else {
			cp.window.Hide()
		}
	}, cp.window)
}
