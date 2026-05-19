package tray

import (
	_ "embed"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"rtsp-virtual-cam-agent/internal/autostart"
	"rtsp-virtual-cam-agent/internal/config"
	"rtsp-virtual-cam-agent/internal/driver"
	"rtsp-virtual-cam-agent/internal/logging"
	"rtsp-virtual-cam-agent/internal/stream"
)

//go:embed resources/icon.png
var iconData []byte

type App struct {
	fyneApp  fyne.App
	window   fyne.Window
	cfg      config.Config
	cfgPath  string
	logger   *logging.Logger
	streamer *stream.Manager
	driver   *driver.Manager
	mu       sync.Mutex

	statusLabel *widget.Label

	previewWin  fyne.Window
	previewImg  *canvas.Image
	rgbaImg     *image.RGBA
	urlTimer    *time.Timer
}

func New() (*App, error) {
	cfg, cfgPath, err := config.LoadOrCreate()
	if err != nil {
		return nil, err
	}

	logger, err := logging.New()
	if err != nil {
		return nil, err
	}

	drv, err := driver.New(cfg, logger)
	if err != nil {
		return nil, err
	}

	streamer := stream.New(cfg, logger, drv)

	a := app.NewWithID("com.syg.rtsp-agent")
	w := a.NewWindow("SYG RTSP Agent")
	w.SetIcon(fyne.NewStaticResource("icon.png", iconData))

	appObj := &App{
		fyneApp:  a,
		window:   w,
		cfg:      cfg,
		cfgPath:  cfgPath,
		logger:   logger,
		streamer: streamer,
		driver:   drv,
	}

	appObj.setupUI()
	appObj.setupTray()

	streamer.SetOnFrame(func(width, height int, pix []byte) {
		appObj.mu.Lock()
		imgWidget := appObj.previewImg
		if imgWidget == nil {
			appObj.mu.Unlock()
			return
		}

		if appObj.rgbaImg == nil || appObj.rgbaImg.Rect.Dx() != width || appObj.rgbaImg.Rect.Dy() != height {
			appObj.rgbaImg = image.NewRGBA(image.Rect(0, 0, width, height))
			imgWidget.Image = appObj.rgbaImg
		}

		// Convert BGRA -> RGBA into the pre-allocated reusable texture buffer
		if runtime.GOOS == "windows" {
			for i := 0; i < len(pix); i += 4 {
				appObj.rgbaImg.Pix[i]   = pix[i+2] // R
				appObj.rgbaImg.Pix[i+1] = pix[i+1] // G
				appObj.rgbaImg.Pix[i+2] = pix[i]   // B
				appObj.rgbaImg.Pix[i+3] = pix[i+3] // A
			}
		} else {
			// Linux stdout is already RGBA, use fast memory copy
			copy(appObj.rgbaImg.Pix, pix)
		}
		appObj.mu.Unlock()

		imgWidget.Refresh()
	})

	return appObj, nil
}

func (a *App) setupUI() {
	a.window.Resize(fyne.NewSize(450, 350))
	a.window.SetCloseIntercept(a.handleClose)

	a.statusLabel = widget.NewLabelWithStyle("Status: stopped", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

	urlEntry := widget.NewEntry()
	urlEntry.SetText(a.cfg.RTSPURL)
	urlEntry.OnChanged = func(s string) {
		a.mu.Lock()
		a.cfg.RTSPURL = s
		if a.urlTimer != nil {
			a.urlTimer.Stop()
		}
		a.urlTimer = time.AfterFunc(2*time.Second, func() {
			a.mu.Lock()
			cfg := a.cfg
			cfgPath := a.cfgPath
			a.mu.Unlock()

			a.logger.Printf("Auto-saving config and restarting stream with new URL...")
			if err := config.Save(cfg, cfgPath); err != nil {
				a.logger.Printf("Failed to save config on URL change: %v", err)
			} else {
				a.logger.Printf("Config auto-saved on URL change")
			}

			a.streamer.Stop()
			a.streamer.UpdateConfig(cfg)
			if err := a.streamer.Start(); err != nil {
				a.logger.Printf("Failed to restart stream: %v", err)
			} else {
				a.logger.Printf("Stream restarted with new URL: %s", cfg.RTSPURL)
			}
		})
		a.mu.Unlock()
	}

	startBtn := widget.NewButtonWithIcon("Start", theme.MediaPlayIcon(), func() {
		if err := a.streamer.Start(); err != nil {
			dialog.ShowError(err, a.window)
		}
	})

	stopBtn := widget.NewButtonWithIcon("Stop", theme.MediaStopIcon(), func() {
		a.streamer.Stop()
	})

	autostartCheck := widget.NewCheck("Start on login", func(checked bool) {
		a.mu.Lock()
		a.cfg.AutoStart = checked
		a.mu.Unlock()
		if err := autostart.SetEnabled(checked); err != nil {
			dialog.ShowError(err, a.window)
		}
	})
	autostartCheck.SetChecked(a.cfg.AutoStart)

	saveBtn := widget.NewButtonWithIcon("Save Config", theme.DocumentSaveIcon(), func() {
		a.mu.Lock()
		err := config.Save(a.cfg, a.cfgPath)
		a.mu.Unlock()
		if err != nil {
			dialog.ShowError(err, a.window)
		} else {
			dialog.ShowInformation("Success", "Configuration saved.", a.window)
		}
	})

	a.window.SetContent(container.NewPadded(
		container.NewVBox(
			widget.NewLabelWithStyle("SYG RTSP Virtual Camera Agent", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
			a.statusLabel,
			widget.NewForm(
				widget.NewFormItem("RTSP URL", urlEntry),
			),
			autostartCheck,
			container.NewGridWithColumns(2, startBtn, stopBtn),
			container.NewGridWithColumns(2, saveBtn, widget.NewButtonWithIcon("Live Preview", theme.VisibilityIcon(), func() {
				a.openPreview()
			})),
			widget.NewSeparator(),
			widget.NewButtonWithIcon("Open Config", theme.SettingsIcon(), func() {
				openPath(a.cfgPath)
			}),
			widget.NewButtonWithIcon("Open Logs", theme.FolderOpenIcon(), func() {
				if logDir, err := config.LogsDir(); err == nil {
					openPath(logDir)
				}
			}),
		),
	))

	go a.statusLoop()
}

func (a *App) setupTray() {
	if desk, ok := a.fyneApp.(desktop.App); ok {
		m := fyne.NewMenu("SYG RTSP Agent",
			fyne.NewMenuItem("Show Agent", func() {
				a.window.Show()
			}),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("Start Stream", func() {
				_ = a.streamer.Start()
			}),
			fyne.NewMenuItem("Stop Stream", func() {
				a.streamer.Stop()
			}),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("Quit", func() {
				a.Quit()
			}),
		)
		desk.SetSystemTrayMenu(m)
		desk.SetSystemTrayIcon(fyne.NewStaticResource("icon.png", iconData))
	}
}

func (a *App) handleClose() {
	dialog.ShowCustomConfirm("Exit Agent?", "Quit", "Minimize to Tray",
		widget.NewLabel("Do you want to completely quit the application or keep it running in the system tray?"),
		func(quit bool) {
			if quit {
				a.Quit()
			} else {
				a.window.Hide()
			}
		}, a.window)
}

func (a *App) Run() error {
	if a.cfg.AutoStart {
		_ = a.streamer.Start()
	}
	a.window.ShowAndRun()
	return nil
}

func (a *App) Quit() {
	a.streamer.Stop()
	_ = a.logger.Close()
	a.fyneApp.Quit()
	os.Exit(0)
}

func (a *App) statusLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		state := a.streamer.State()
		label := fmt.Sprintf("Status: %s", statusText(state))
		if state.RestartCount > 0 {
			label = fmt.Sprintf("%s (restarts: %d)", label, state.RestartCount)
		}
		a.statusLabel.SetText(label)
	}
}

func statusText(state stream.State) string {
	if !state.Running {
		return "stopped"
	}
	if state.LastError != "" {
		return "recovering"
	}
	return "running"
}

func openPath(target string) {
	cleaned := filepath.Clean(target)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", cleaned)
	case "linux":
		cmd = exec.Command("xdg-open", cleaned)
	default:
		return
	}
	_ = cmd.Start()
}

func (a *App) openPreview() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.previewWin != nil {
		a.previewWin.RequestFocus()
		return
	}

	w := a.fyneApp.NewWindow("SYG RTSP Camera Live Preview")
	w.Resize(fyne.NewSize(640, 360))

	img := canvas.NewImageFromImage(image.NewRGBA(image.Rect(0, 0, int(a.cfg.Width), int(a.cfg.Height))))
	img.FillMode = canvas.ImageFillContain

	w.SetContent(container.NewMax(img))
	w.SetOnClosed(func() {
		a.mu.Lock()
		a.previewWin = nil
		a.previewImg = nil
		a.mu.Unlock()
	})

	a.previewWin = w
	a.previewImg = img
	w.Show()
}
