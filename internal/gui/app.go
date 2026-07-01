package gui

import (
	"context"
	_ "embed"
	"fmt"
	"image/color"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"nystavision/internal/autostart"
	"nystavision/internal/config"
	"nystavision/internal/i18n"
	"nystavision/internal/logging"
	"nystavision/internal/stream"
	"nystavision/internal/ui"
	"nystavision/internal/version"
)

//go:embed resources/icon.png
var iconData []byte

//go:embed resources/logo.png
var logoData []byte

type App struct {
	fyneApp      fyne.App
	window       fyne.Window
	splashWindow fyne.Window
	cfg          *config.Config
	cfgPath      string
	logger       *logging.Logger
	multiManager *stream.MultiManager
	postProc     *stream.PostProcessor
	jobQueue     *stream.JobQueue
	mu           sync.Mutex

	// UI elements
	statusLabel     *widget.Label
	startStopAllBtn *widget.Button
	cameraGrid      fyne.CanvasObject
	gridContainer   *fyne.Container
	cameraPanels    map[string]*ui.CameraPanel
	selectedCamera  string
	recordBtn       *widget.Button
	recordTimer     *time.Ticker
	recordStart     time.Time

	// Tutorial button references (for spotlight highlight)
	addBtn      *widget.Button
	startAllRef *widget.Button
	settingsRef *widget.Button

	// Camera ordering (for consistent display)
	cameraOrder []string

	rtspUIStopped bool

	// Recording state
	isRecording   bool
	recordSession *stream.RecordingSession
	patientCache  *stream.PatientCache

	// Composite recording
	compositeRecorder *stream.CompositeRecorder

	// Disk space live monitoring
	diskBanner      *fyne.Container
	diskBannerBg    *canvas.Rectangle
	diskBannerText  *canvas.Text
	diskMonitorStop chan struct{}

	// Performance warning banner (shown when composite FPS is auto-reduced)
	perfBanner     *fyne.Container
	perfBannerBg   *canvas.Rectangle
	perfBannerText *canvas.Text

	// Status bar elements
	statusBarLabel     *canvas.Text
	recordingTimeLabel *canvas.Text
	jobQueueLabel      *canvas.Text
	statusBarContainer *fyne.Container

	// Layout drawer elements (Sahneler - left side)
	drawerPanel         *fyne.Container
	layoutListContainer *fyne.Container
	layoutsRef          *widget.Button // for spotlight tutorials
	drawerVisible       bool

	// Recordings drawer elements (Kayıtlarım - right side)
	recordingsDrawerPanel   *fyne.Container
	recordingsListContainer *fyne.Container
	recordingsDrawerVisible bool
	recordingsBtn           *widget.Button

	overlayContainer *fyne.Container

	settingsDialog         dialog.Dialog
	settingsLangSelect     *widget.Select
	settingsAutostartCheck *widget.Check
	settingsCompositeCheck *widget.Check

	removeBtn        *widget.Button
	isCompactToolbar bool

	shownUSBError map[string]bool
}

func New() (*App, error) {
	a := app.NewWithID("com.syg.nystavision")
	a.Settings().SetTheme(&SYGMedicalTheme{}) // Apply theme before creating any windows or splash screen
	w := a.NewWindow("NystaVision") // Placeholder title, updated dynamically on load

	appObj := &App{
		fyneApp:       a,
		window:        w,
		shownUSBError: make(map[string]bool),
		patientCache:  stream.NewPatientCache(10 * time.Minute),
	}

	// Create and show splash screen immediately
	var splash fyne.Window
	if drv, ok := a.Driver().(desktop.Driver); ok {
		splash = drv.CreateSplashWindow()

		// White background splash screen for a clean, professional look
		bg := canvas.NewRectangle(color.White)

		// Logo image
		logoRes := fyne.NewStaticResource("logo.png", logoData)
		logoImg := canvas.NewImageFromResource(logoRes)
		logoImg.FillMode = canvas.ImageFillContain
		logoImg.SetMinSize(fyne.NewSize(280, 111))

		// Infinite progress spinner for modern loading look
		progress := widget.NewProgressBarInfinite()

		// Transparent padding rectangles to constrain progress bar width elegantly
		progressPadding := canvas.NewRectangle(color.Transparent)
		progressPadding.SetMinSize(fyne.NewSize(60, 0))
		progressWrapper := container.NewBorder(nil, nil, progressPadding, progressPadding, progress)

		// Vertical Box layout for elements (centered logo + progress bar)
		contentVBox := container.NewVBox(
			layout.NewSpacer(),
			container.NewCenter(logoImg),
			layout.NewSpacer(),
			progressWrapper,
			layout.NewSpacer(),
		)

		splashContent := container.NewStack(
			bg,
			container.NewPadded(contentVBox),
		)

		splash.SetContent(splashContent)
		splash.Resize(fyne.NewSize(420, 300))
		splash.Show()

		appObj.splashWindow = splash
	}

	return appObj, nil
}

func getCameraOrder(cameras []config.CameraSource) []string {
	order := make([]string, len(cameras))
	for i, c := range cameras {
		order[i] = c.ID
	}
	return order
}

func (a *App) setupUI() {
	width := 960
	height := 640
	if a.cfg.WindowWidth > 100 && a.cfg.WindowHeight > 100 {
		width = a.cfg.WindowWidth
		height = a.cfg.WindowHeight
	}
	a.window.Resize(fyne.NewSize(float32(width), float32(height)))
	a.window.SetCloseIntercept(a.handleClose)

	// Status label (for window title updates)
	a.statusLabel = widget.NewLabelWithStyle(i18n.T("title_app"), fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

	// Build toolbar with card background
	toolbar := a.buildToolbar()
	toolbarBg := canvas.NewRectangle(colorCardSurface)
	toolbarBg.CornerRadius = 8
	toolbarCard := container.NewStack(toolbarBg, container.NewPadded(toolbar))

	// Build status bar
	a.buildStatusBar()

	// Build camera grid
	a.buildCameraGrid()
	a.refreshCameraDropdowns()

	// Build layout drawer (Sahneler - left side)
	a.buildLayoutDrawer()

	// Build recordings drawer (Kayıtlarım - right side)
	a.buildRecordingsDrawer()

	// Content area: toolbar on top, status bar on bottom, camera grid in center
	mainContent := container.NewBorder(
		container.NewVBox(toolbarCard),
		a.statusBarContainer,
		nil, nil,
		a.gridContainer,
	)

	// Final layout: Sahneler drawer on the left, Kayıtlarım drawer on the right
	content := container.NewBorder(
		nil, nil,
		a.drawerPanel, a.recordingsDrawerPanel, // Left: Sahneler, Right: Kayıtlarım
		mainContent,
	)

	a.overlayContainer = container.NewStack()
	stackedContent := container.NewStack(content, a.overlayContainer)

	a.window.SetContent(stackedContent)

	// Apply split offsets on startup if we have saved ones
	if len(a.cfg.SplitOffsets) > 0 {
		ApplySplitOffsets(a.cameraGrid, a.cfg.SplitOffsets)
		offsets := a.cfg.SplitOffsets
		go func() {
			for _, delay := range []time.Duration{100 * time.Millisecond, 300 * time.Millisecond} {
				time.Sleep(delay)
				fyne.Do(func() {
					ApplySplitOffsets(a.cameraGrid, offsets)
				})
			}
		}()
	}

	// Start status update loop
	go a.statusLoop()

	// Start window size polling loop for responsive toolbar labels
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			if a.window == nil {
				return
			}
			a.updateToolbarLabels()
		}
	}()
}

func (a *App) buildStatusBar() {
	// Status bar background
	statusBarBgColor := colorCardSurface
	statusBarBgColor.A = 200
	statusBarBg := canvas.NewRectangle(statusBarBgColor)
	statusBarBg.CornerRadius = 6

	// Left: camera status
	a.statusBarLabel = canvas.NewText("0/0", color.NRGBA{R: 236, G: 240, B: 241, A: 255})
	a.statusBarLabel.TextSize = 12
	a.statusBarLabel.TextStyle = fyne.TextStyle{Bold: true}

	// Right: recording timer (hidden by default)
	a.recordingTimeLabel = canvas.NewText("", color.NRGBA{R: 231, G: 76, B: 60, A: 255}) // Red Critical
	a.recordingTimeLabel.TextSize = 12
	a.recordingTimeLabel.TextStyle = fyne.TextStyle{Bold: true}
	a.recordingTimeLabel.Alignment = fyne.TextAlignTrailing

	// Center: job queue status
	a.jobQueueLabel = canvas.NewText("", color.NRGBA{R: 243, G: 156, B: 18, A: 255}) // Amber
	a.jobQueueLabel.TextSize = 12
	a.jobQueueLabel.TextStyle = fyne.TextStyle{Bold: true}
	a.jobQueueLabel.Alignment = fyne.TextAlignCenter

	statusContent := container.NewBorder(
		nil, nil,
		container.NewPadded(a.statusBarLabel),
		container.NewPadded(a.recordingTimeLabel),
		container.NewPadded(a.jobQueueLabel),
	)

	a.statusBarContainer = container.NewStack(statusBarBg, statusContent)
}

func (a *App) buildToolbar() *fyne.Container {
	// Add camera button — with label
	a.addBtn = widget.NewButtonWithIcon(i18n.T("btn_toolbar_add"), theme.ContentAddIcon(), func() {
		a.showAddCameraDialog()
	})

	// Remove camera button — with label
	a.removeBtn = widget.NewButtonWithIcon(i18n.T("btn_toolbar_remove"), theme.ContentRemoveIcon(), func() {
		a.removeSelectedCamera()
	})

	// Start/Stop all streams button
	a.startStopAllBtn = widget.NewButtonWithIcon(i18n.T("btn_start_all"), theme.MediaPlayIcon(), func() {
		a.toggleAllStreams()
	})
	a.startStopAllBtn.Importance = widget.SuccessImportance
	a.startAllRef = a.startStopAllBtn

	// Record button
	a.recordBtn = widget.NewButtonWithIcon(i18n.T("btn_record"), theme.MediaRecordIcon(), func() {
		a.toggleRecording()
	})

	// Settings button
	settingsBtn := widget.NewButtonWithIcon("", theme.SettingsIcon(), func() {
		a.showSettingsDialog()
	})
	a.settingsRef = settingsBtn

	// Layouts/Scenes drawer toggle button — with label (far left)
	layoutsBtn := widget.NewButtonWithIcon(i18n.T("btn_layouts"), theme.MenuIcon(), func() {
		a.toggleLayoutDrawer()
	})
	a.layoutsRef = layoutsBtn

	// Recordings drawer toggle button — with label (far right)
	a.recordingsBtn = widget.NewButtonWithIcon(i18n.T("btn_recordings"), theme.FolderIcon(), func() {
		a.toggleRecordingsDrawer()
	})

	// Help/Tutorial button
	helpBtn := widget.NewButtonWithIcon("", theme.QuestionIcon(), func() {
		a.showTutorial()
	})

	// Layout: [Sahneler] | [+ Ekle] [- Sil] | [▶ Tümünü Başlat] [⏺ Kayıt] | [⚙] [?] | [Kayıtlarım]
	leftGroup := container.NewHBox(layoutsBtn, widget.NewSeparator(), a.addBtn, a.removeBtn)
	middleGroup := container.NewHBox(a.startStopAllBtn, widget.NewSeparator(), a.recordBtn)
	rightGroup := container.NewHBox(widget.NewSeparator(), settingsBtn, helpBtn, widget.NewSeparator(), a.recordingsBtn)

	return container.NewBorder(nil, nil, leftGroup, rightGroup, container.NewCenter(middleGroup))
}

func (a *App) toggleAllStreams() {
	if a.blockWhileRecording() {
		return
	}
	anyWebcamRunning := false
	for _, cam := range a.cfg.Cameras {
		if cam.Enabled && cam.Type == "webcam" {
			if a.multiManager.GetState(cam.ID).Running {
				anyWebcamRunning = true
				break
			}
		}
	}

	content := container.NewVBox(widget.NewLabel(i18n.T("msg_please_wait")), widget.NewProgressBarInfinite())
	progress := a.NewToastProgress(i18n.T("msg_please_wait"), content)
	progress.Show()

	go func() {
		if anyWebcamRunning {
			a.rtspUIStopped = true
			a.multiManager.StopAll()
			a.updateStartStopAllBtn(false)
		} else {
			a.rtspUIStopped = false
			a.multiManager.StartAll()
			a.updateStartStopAllBtn(true)
		}
		fyne.Do(func() {
			progress.Hide()
		})
	}()
}

func (a *App) updateStartStopAllBtn(running bool) {
	fyne.Do(func() {
		a.mu.Lock()
		isCompact := a.isCompactToolbar
		a.mu.Unlock()

		if running {
			if isCompact {
				a.startStopAllBtn.SetText("")
			} else {
				a.startStopAllBtn.SetText(i18n.T("btn_stop_all"))
			}
			a.startStopAllBtn.Icon = theme.MediaStopIcon()
			a.startStopAllBtn.Importance = widget.WarningImportance
		} else {
			if isCompact {
				a.startStopAllBtn.SetText("")
			} else {
				a.startStopAllBtn.SetText(i18n.T("btn_start_all"))
			}
			a.startStopAllBtn.Icon = theme.MediaPlayIcon()
			a.startStopAllBtn.Importance = widget.SuccessImportance
		}
		a.startStopAllBtn.Refresh()
	})
}

func (a *App) updateRecordBtn() {
	fyne.Do(func() {
		a.mu.Lock()
		isRecording := a.isRecording
		isCompact := a.isCompactToolbar
		a.mu.Unlock()

		if isRecording {
			if isCompact {
				a.recordBtn.SetText("")
			} else {
				a.recordBtn.SetText(i18n.T("btn_stop"))
			}
			a.recordBtn.Icon = theme.MediaStopIcon()
			a.recordBtn.Importance = widget.DangerImportance
		} else {
			if isCompact {
				a.recordBtn.SetText("")
			} else {
				a.recordBtn.SetText(i18n.T("btn_record"))
			}
			a.recordBtn.Icon = theme.MediaRecordIcon()
			a.recordBtn.Importance = widget.MediumImportance
		}
		a.recordBtn.Refresh()
	})
}

func (a *App) updateToolbarLabels() {
	width := a.window.Canvas().Size().Width
	isCompact := width < 800

	a.mu.Lock()
	if a.isCompactToolbar == isCompact {
		a.mu.Unlock()
		return
	}
	a.isCompactToolbar = isCompact
	a.mu.Unlock()

	fyne.Do(func() {
		if isCompact {
			a.addBtn.SetText("")
			if a.removeBtn != nil {
				a.removeBtn.SetText("")
			}
			a.startStopAllBtn.SetText("")
			a.recordBtn.SetText("")
			if a.layoutsRef != nil {
				a.layoutsRef.SetText("")
			}
			if a.recordingsBtn != nil {
				a.recordingsBtn.SetText("")
			}
		} else {
			a.addBtn.SetText(i18n.T("btn_toolbar_add"))
			if a.removeBtn != nil {
				a.removeBtn.SetText(i18n.T("btn_toolbar_remove"))
			}
			if a.layoutsRef != nil {
				a.layoutsRef.SetText(i18n.T("btn_layouts"))
			}
			if a.recordingsBtn != nil {
				a.recordingsBtn.SetText(i18n.T("btn_recordings"))
			}

			// Update startStopAllBtn text based on running streams
			a.mu.Lock()
			cameras := make([]config.CameraSource, len(a.cfg.Cameras))
			copy(cameras, a.cfg.Cameras)
			isRecording := a.isRecording
			a.mu.Unlock()

			anyWebcamRunning := false
			for _, cam := range cameras {
				if cam.Enabled && cam.Type == "webcam" {
					if a.multiManager.GetState(cam.ID).Running {
						anyWebcamRunning = true
						break
					}
				}
			}
			if anyWebcamRunning {
				a.startStopAllBtn.SetText(i18n.T("btn_stop_all"))
			} else {
				a.startStopAllBtn.SetText(i18n.T("btn_start_all"))
			}

			// Update recordBtn text based on recording status
			if isRecording {
				a.recordBtn.SetText(i18n.T("btn_stop"))
			} else {
				a.recordBtn.SetText(i18n.T("btn_record"))
			}
		}

		a.addBtn.Refresh()
		if a.removeBtn != nil {
			a.removeBtn.Refresh()
		}
		a.startStopAllBtn.Refresh()
		a.recordBtn.Refresh()
		if a.layoutsRef != nil {
			a.layoutsRef.Refresh()
		}
		if a.recordingsBtn != nil {
			a.recordingsBtn.Refresh()
		}
	})
}

func (a *App) getCameraDropdownOptions() ([]string, map[string]string) {
	options := []string{i18n.T("opt_passive"), i18n.T("opt_ip_camera")}
	webcamMap := make(map[string]string)

	detected := config.DetectWebcams()
	for _, wc := range detected {
		label := i18n.T("opt_webcam_format", wc.Name, wc.Device)
		options = append(options, label)
		webcamMap[label] = wc.Device
	}

	return options, webcamMap
}

func (a *App) getCameraSelectedOption(cam config.CameraSource, options []string, webcamMap map[string]string) string {
	if !cam.Enabled {
		return i18n.T("opt_passive")
	}
	if cam.Type == "rtsp" {
		return i18n.T("opt_ip_camera")
	}
	if cam.Type == "webcam" {
		for label, dev := range webcamMap {
			if dev == cam.Device {
				return label
			}
		}
		return i18n.T("opt_webcam_disconnected", cam.Device)
	}
	return i18n.T("opt_passive")
}

func (a *App) refreshCameraDropdowns() {
	go func() {
		options, webcamMap := a.getCameraDropdownOptions()

		fyne.Do(func() {
			for _, cam := range a.cfg.Cameras {
				camID := cam.ID
				panel, exists := a.cameraPanels[camID]
				if !exists {
					continue
				}

				panelOptions := make([]string, len(options))
				copy(panelOptions, options)

				selected := a.getCameraSelectedOption(cam, panelOptions, webcamMap)

				found := false
				for _, opt := range panelOptions {
					if opt == selected {
						found = true
						break
					}
				}
				if !found {
					lastIdx := len(panelOptions) - 1
					panelOptions = append(panelOptions[:lastIdx], append([]string{selected}, panelOptions[lastIdx:]...)...)
				}

				panel.UpdateSources(panelOptions, selected, func(val string) {
					a.handleSourceSelectionChanged(camID, val, webcamMap)
				})
			}
		})
	}()
}

func (a *App) handleSourceSelectionChanged(cameraID string, selectedVal string, webcamMap map[string]string) {
	var camIdx = -1
	for i, c := range a.cfg.Cameras {
		if c.ID == cameraID {
			camIdx = i
			break
		}
	}
	if camIdx < 0 {
		return
	}

	var camerasToStop []string

	a.mu.Lock()
	cam := &a.cfg.Cameras[camIdx]

	if selectedVal == i18n.T("opt_passive") {
		cam.Enabled = false
	} else if selectedVal == i18n.T("opt_ip_camera") {
		hasOtherRTSP := false
		for i, c := range a.cfg.Cameras {
			if i != camIdx && c.Type == "rtsp" && c.Enabled {
				hasOtherRTSP = true
				break
			}
		}
		if hasOtherRTSP {
			a.mu.Unlock()
			dialog.ShowError(fmt.Errorf("%s", i18n.T("msg_max_ip_camera")), a.window)
			a.refreshCameraDropdowns()
			return
		}

		cam.Enabled = true
		cam.Type = "rtsp"
		if strings.TrimSpace(cam.RTSPURL) == "" {
			a.mu.Unlock()
			a.showEditCameraDialog(cameraID)
			return
		}
	} else {
		if dev, ok := webcamMap[selectedVal]; ok {
			cam.Enabled = true
			cam.Type = "webcam"
			if cam.Device != dev {
				cam.Device = dev
				cam.Width = 0
				cam.Height = 0
				cam.FPS = 0
				cam.PixelFormat = ""
			}

			for i := range a.cfg.Cameras {
				if i != camIdx && a.cfg.Cameras[i].Enabled && a.cfg.Cameras[i].Type == "webcam" && a.cfg.Cameras[i].Device == dev {
					a.cfg.Cameras[i].Enabled = false
					camerasToStop = append(camerasToStop, a.cfg.Cameras[i].ID)
				}
			}
		} else if !strings.Contains(selectedVal, i18n.T("opt_ip_camera")) && !strings.Contains(selectedVal, i18n.T("opt_passive")) {
			cam.Enabled = true
			cam.Type = "webcam"
		}
	}

	_ = config.Save(*a.cfg, a.cfgPath)
	a.mu.Unlock()

	for _, id := range camerasToStop {
		a.multiManager.StopCamera(id)
	}

	a.multiManager.UpdateCamera(a.cfg.Cameras[camIdx])
	a.refreshCameraDropdowns()
}

func (a *App) showEditCameraDialog(cameraID string) {
	if a.blockWhileRecording() {
		return
	}
	var selectedCam config.CameraSource
	selectedIdx := -1
	for i, c := range a.cfg.Cameras {
		if c.ID == cameraID {
			selectedCam = c
			selectedIdx = i
			break
		}
	}
	if selectedIdx < 0 {
		return
	}

	content := container.NewVBox(widget.NewLabel(i18n.T("msg_please_wait")), widget.NewProgressBarInfinite())
	progress := a.NewToastProgress(i18n.T("msg_cameras_searching"), content)
	progress.Show()

	go func() {
		detected := config.DetectWebcams()

		fyne.Do(func() {
			progress.Hide()

			nameEntry := widget.NewEntry()
			nameEntry.Text = selectedCam.Name

			typeSelect := widget.NewSelect([]string{"rtsp", "webcam"}, nil)
			typeSelect.SetSelected(selectedCam.Type)

			urlEntry := widget.NewEntry()
			urlEntry.Text = selectedCam.RTSPURL
			urlEntry.SetPlaceHolder(i18n.T("placeholder_url"))

			formatSelect := widget.NewSelect([]string{i18n.T("lbl_format_auto")}, nil)
			formatSelect.SetSelected(i18n.T("lbl_format_auto"))

			var linuxFPSEntry *widget.Entry
			var formatUI fyne.CanvasObject = formatSelect

			if runtime.GOOS == "linux" {
				linuxFPSEntry = widget.NewEntry()
				linuxFPSEntry.SetPlaceHolder("Örn: 60 (Sadece rakam)")
				if selectedCam.FPS > 0 {
					linuxFPSEntry.SetText(strconv.Itoa(selectedCam.FPS))
				}
				formatUI = container.NewVBox(
					formatSelect,
					widget.NewLabel("Linux FPS Zorlama:"),
					linuxFPSEntry,
				)
			}

			if a.cfg.CompositeRecording {
				formatSelect.Disable()
				if linuxFPSEntry != nil {
					linuxFPSEntry.Disable()
				}
				warningLbl := widget.NewLabel(i18n.T("msg_composite_format_disabled"))
				warningLbl.Wrapping = fyne.TextWrapWord
				warningLbl.TextStyle = fyne.TextStyle{Italic: true}
				formatUI = container.NewVBox(warningLbl, formatUI)
			}

			accItem := widget.NewAccordionItem(i18n.T("lbl_camera_format"), formatUI)
			formatAccordion := widget.NewAccordion(accItem)

			webcamSelect := widget.NewSelect(nil, nil)
			var wcNames []string
			wcDevMap := make(map[string]string)
			for _, wc := range detected {
				wcNames = append(wcNames, wc.Name)
				wcDevMap[wc.Name] = wc.Device
			}
			if len(wcNames) == 0 {
				wcNames = append(wcNames, i18n.T("msg_webcam_not_found"))
			}
			webcamSelect.Options = wcNames
			if selectedCam.Type == "webcam" {
				found := false
				for k, v := range wcDevMap {
					if v == selectedCam.Device {
						webcamSelect.SetSelected(k)
						found = true
						break
					}
				}
				if !found {
					webcamSelect.SetSelected(wcNames[0])
				}
			} else {
				webcamSelect.SetSelected(wcNames[0])
			}

			var capMap map[string]stream.CameraCapability
			loadCapabilities := func(deviceName string) {
				formatSelect.Options = []string{i18n.T("lbl_format_auto"), i18n.T("lbl_format_loading")}
				formatSelect.SetSelected(i18n.T("lbl_format_auto"))

				devicePath := wcDevMap[deviceName]
				if runtime.GOOS == "windows" {
					devicePath = "video=" + deviceName
				}

				go func() {
					caps, err := stream.QueryCapabilities(context.Background(), a.cfg.FFmpegPath, devicePath, a.logger)
					fyne.Do(func() {
						options := []string{i18n.T("lbl_format_auto")}
						capMap = make(map[string]stream.CameraCapability)
						if err == nil {
							for _, c := range caps {
								formatStr := c.PixelFormat
								if c.VCodec == "mjpeg" {
									formatStr = "mjpeg"
								}
								s := fmt.Sprintf("%dx%d @ %d FPS (%s)", c.Width, c.Height, int(c.FPS), formatStr)
								if _, exists := capMap[s]; !exists {
									options = append(options, s)
									capMap[s] = c
								}
							}
						}
						formatSelect.Options = options
						// If the selected camera has saved dimensions, try to select them
						if selectedCam.Device == wcDevMap[deviceName] && selectedCam.Width > 0 {
							savedFormatStr := selectedCam.PixelFormat
							s := fmt.Sprintf("%dx%d @ %d FPS (%s)", selectedCam.Width, selectedCam.Height, selectedCam.FPS, savedFormatStr)
							if _, ok := capMap[s]; ok {
								formatSelect.SetSelected(s)
							}
						}
					})
				}()
			}

			webcamSelect.OnChanged = func(s string) {
				if typeSelect.Selected == "webcam" {
					loadCapabilities(s)
				}
			}

			if selectedCam.Type == "rtsp" {
				urlEntry.Show()
				webcamSelect.Hide()
				formatAccordion.Hide()
			} else {
				urlEntry.Hide()
				webcamSelect.Show()
				formatAccordion.Show()
				if webcamSelect.Selected != "" {
					loadCapabilities(webcamSelect.Selected)
				}
			}

			typeSelect.OnChanged = func(s string) {
				if s == "rtsp" {
					urlEntry.Show()
					webcamSelect.Hide()
					formatAccordion.Hide()
				} else {
					urlEntry.Hide()
					webcamSelect.Show()
					formatAccordion.Show()
					if webcamSelect.Selected != "" {
						loadCapabilities(webcamSelect.Selected)
					}
				}
			}

			formItems := []*widget.FormItem{
				widget.NewFormItem(i18n.T("lbl_camera_name"), nameEntry),
				widget.NewFormItem(i18n.T("lbl_type"), typeSelect),
				widget.NewFormItem(i18n.T("lbl_ip_url"), urlEntry),
				widget.NewFormItem(i18n.T("lbl_webcam"), webcamSelect),
				widget.NewFormItem("", formatAccordion),
			}

			innerForm := widget.NewForm(formItems...)
			scrollableContent := container.NewVScroll(innerForm)
			scrollableContent.SetMinSize(fyne.NewSize(500, 450))

			d := dialog.NewCustomConfirm(
				fmt.Sprintf("%s - %s", i18n.T("menu_edit"), selectedCam.Name),
				i18n.T("btn_save"),
				i18n.T("btn_cancel"),
				scrollableContent,
				func(ok bool) {
					defer a.refreshCameraDropdowns()
					if !ok {
						return
					}

					a.mu.Lock()
					defer a.mu.Unlock()

				camPtr := &a.cfg.Cameras[selectedIdx]
				camPtr.Name = nameEntry.Text
				camPtr.Type = typeSelect.Selected
				camPtr.RTSPURL = strings.TrimSpace(urlEntry.Text)
				if camPtr.Type == "webcam" {
					camPtr.Device = wcDevMap[webcamSelect.Selected]
					if formatSelect.Selected != i18n.T("lbl_format_auto") && formatSelect.Selected != i18n.T("lbl_format_loading") {
						if cap, ok := capMap[formatSelect.Selected]; ok {
							camPtr.Width = cap.Width
							camPtr.Height = cap.Height
							camPtr.FPS = int(cap.FPS)
							if cap.VCodec == "mjpeg" {
								camPtr.PixelFormat = "mjpeg"
							} else {
								camPtr.PixelFormat = cap.PixelFormat
							}
						}
					} else {
						// Auto/Default selected, reset format overrides
						camPtr.Width = 0
						camPtr.Height = 0
						camPtr.FPS = 0
						camPtr.PixelFormat = ""
					}

					if runtime.GOOS == "linux" && linuxFPSEntry != nil && linuxFPSEntry.Text != "" {
						if customFPS, err := strconv.Atoi(strings.TrimSpace(linuxFPSEntry.Text)); err == nil && customFPS > 0 {
							camPtr.FPS = customFPS
						}
					}
				}
				_ = config.Save(*a.cfg, a.cfgPath)
				a.multiManager.UpdateCamera(*camPtr)
				if panel, exists := a.cameraPanels[cameraID]; exists {
					panel.SetName(camPtr.Name)
				}
			}, a.window)
			d.Resize(fyne.NewSize(500, 450))
			d.Show()
		})
	}()
}

func (a *App) buildCameraGrid() {
	cols, rows := ui.CalculateGrid(len(a.cfg.Cameras))

	a.cameraPanels = make(map[string]*ui.CameraPanel)
	objects := make([]fyne.CanvasObject, 0, len(a.cfg.Cameras))

	for _, cam := range a.cfg.Cameras {
		panel := ui.NewCameraPanel(cam.ID, cam.Name, func(cameraID string) {
			a.selectCamera(cameraID)
		}, func(cameraID string, pe *fyne.PointEvent) {
			a.showCameraContextMenu(cameraID, pe)
		})
		a.cameraPanels[cam.ID] = panel
		objects = append(objects, panel)
	}

	a.cameraGrid = ui.BuildResizableCameraGrid(objects, cols, rows)
	a.gridContainer = container.NewStack(a.cameraGrid)
	a.initDiskBanner()

	if len(a.cameraOrder) > 0 && a.selectedCamera == "" {
		a.selectedCamera = a.cameraOrder[0]
	}
}

func (a *App) rebuildGrid() {
	a.cameraOrder = getCameraOrder(a.cfg.Cameras)
	cols, rows := ui.CalculateGrid(len(a.cfg.Cameras))

	objects := make([]fyne.CanvasObject, 0, len(a.cfg.Cameras))
	newPanels := make(map[string]*ui.CameraPanel)

	for _, cam := range a.cfg.Cameras {
		panel, exists := a.cameraPanels[cam.ID]
		if !exists {
			panel = ui.NewCameraPanel(cam.ID, cam.Name, func(cameraID string) {
				a.selectCamera(cameraID)
			}, func(cameraID string, pe *fyne.PointEvent) {
				a.showCameraContextMenu(cameraID, pe)
			})
		}
		newPanels[cam.ID] = panel
		objects = append(objects, panel)
	}

	a.cameraPanels = newPanels
	a.cameraGrid = ui.BuildResizableCameraGrid(objects, cols, rows)
	a.gridContainer.RemoveAll()
	a.gridContainer.Add(a.cameraGrid)
	if a.diskBanner != nil {
		a.gridContainer.Add(a.diskBanner)
	}
	a.gridContainer.Refresh()
	if a.window != nil && a.window.Content() != nil {
		a.window.Content().Refresh()
	}

	a.setupFrameCallbacks()
	a.refreshCameraDropdowns()
}

func (a *App) setupFrameCallbacks() {
	for _, cam := range a.cfg.Cameras {
		camID := cam.ID
		panel, exists := a.cameraPanels[camID]
		if !exists {
			continue
		}
		a.multiManager.SetOnFrame(camID, func(width, height int, pix []byte) {
			if camID == a.cfg.RTSPServerCamera && a.rtspUIStopped {
				return
			}
			panel.UpdateFrame(width, height, pix)

			// Feed to composite recorder if running
			a.mu.Lock()
			compRec := a.compositeRecorder
			a.mu.Unlock()
			if compRec != nil {
				compRec.UpdateFrame(camID, width, height, pix)
			}
		})
	}
}

func (a *App) selectCamera(cameraID string) {
	a.selectedCamera = cameraID

	for id, panel := range a.cameraPanels {
		panel.SetSelected(id == cameraID)
	}
}

// --- Add Camera Dialog ---

func (a *App) showAddCameraDialog() {
	if a.blockWhileRecording() {
		return
	}
	if len(a.cfg.Cameras) >= config.MaxCameras {
		a.sendOSNotification(i18n.T("err_limit"), fmt.Sprintf(i18n.T("err_camera_limit"), config.MaxCameras))
		return
	}

	content := container.NewVBox(widget.NewLabel(i18n.T("msg_please_wait")), widget.NewProgressBarInfinite())
	progress := a.NewToastProgress(i18n.T("msg_cameras_searching"), content)
	progress.Show()

	go func() {
		detected := config.DetectWebcams()

		fyne.Do(func() {
			progress.Hide()

			nameEntry := widget.NewEntry()
			nameEntry.SetPlaceHolder(i18n.T("placeholder_camera_name"))
			nameEntry.Text = i18n.T("default_camera_name", len(a.cfg.Cameras)+1)

			sourceType := widget.NewSelect([]string{"IP", "Webcam"}, nil)
			sourceType.SetSelected("IP")

			urlEntry := widget.NewEntry()
			urlEntry.SetPlaceHolder(i18n.T("placeholder_url"))

			webcamSelect := widget.NewSelect(nil, nil)
			var wcNames []string
			wcDevMap := make(map[string]string)
			for _, wc := range detected {
				wcNames = append(wcNames, wc.Name)
				wcDevMap[wc.Name] = wc.Device
			}
			if len(wcNames) == 0 {
				wcNames = append(wcNames, i18n.T("msg_webcam_not_found"))
			}
			webcamSelect.Options = wcNames
			webcamSelect.SetSelected(wcNames[0])
			webcamSelect.Hide()

			sourceType.OnChanged = func(s string) {
				if s == "IP" {
					urlEntry.Show()
					webcamSelect.Hide()
				} else {
					urlEntry.Hide()
					webcamSelect.Show()
				}
			}

			formItems := []*widget.FormItem{
				widget.NewFormItem(i18n.T("lbl_name"), nameEntry),
				widget.NewFormItem(i18n.T("lbl_source"), sourceType),
				widget.NewFormItem(i18n.T("lbl_ip_url"), urlEntry),
				widget.NewFormItem(i18n.T("lbl_webcam"), webcamSelect),
			}

			innerForm := widget.NewForm(formItems...)
			scrollableContent := container.NewVScroll(innerForm)
			scrollableContent.SetMinSize(fyne.NewSize(500, 300))

			d := dialog.NewCustomConfirm(
				i18n.T("title_add_camera"),
				i18n.T("btn_add_camera"),
				i18n.T("btn_cancel"),
				scrollableContent,
				func(ok bool) {
					if !ok {
						return
					}

				if sourceType.Selected == "IP" {
					hasRTSP := false
					for _, c := range a.cfg.Cameras {
						if c.Type == "rtsp" && c.Enabled {
							hasRTSP = true
							break
						}
					}
					if hasRTSP {
						dialog.ShowError(fmt.Errorf("%s", i18n.T("msg_max_ip_camera")), a.window)
						return
					}
				}

				cam := config.CameraSource{
					ID:      config.NextCameraID(a.cfg.Cameras),
					Name:    nameEntry.Text,
					Width:   1280,
					Height:  720,
					FPS:     30,
					Enabled: true,
				}

				if sourceType.Selected == "IP" {
					cam.Type = "rtsp"
					cam.RTSPURL = strings.TrimSpace(urlEntry.Text)
				} else {
					cam.Type = "webcam"
					cam.Device = wcDevMap[webcamSelect.Selected]
				}

				prevServerCam := a.cfg.RTSPServerCamera
				if err := a.multiManager.AddCamera(cam); err != nil {
					dialog.ShowError(err, a.window)
					return
				}

				if prevServerCam != a.cfg.RTSPServerCamera {
					a.multiManager.Close()
					a.multiManager = stream.NewMultiManager(a.cfg, a.cfgPath, a.logger)
					if a.cfg.AutoStart {
						a.multiManager.StartAll()
					}
				}

				a.rebuildGrid()
			}, a.window)

			d.Resize(fyne.NewSize(500, 350))
			d.Show()
		})
	}()
}

func (a *App) removeSelectedCamera() {
	if a.blockWhileRecording() {
		return
	}
	if len(a.cfg.Cameras) <= config.MinCameras {
		a.sendOSNotification(i18n.T("err_limit"), i18n.T("msg_min_cameras", config.MinCameras))
		return
	}

	if a.selectedCamera == "" {
		a.sendOSNotification(i18n.T("err_selection"), i18n.T("msg_select_to_delete"))
		return
	}

	camName := a.selectedCamera
	for _, c := range a.cfg.Cameras {
		if c.ID == a.selectedCamera {
			camName = c.Name
			break
		}
	}

	dialog.ShowConfirm(i18n.T("title_delete_camera"), i18n.T("msg_confirm_delete", camName), func(ok bool) {
		if !ok {
			return
		}

		prevServerCam := a.cfg.RTSPServerCamera
		if err := a.multiManager.RemoveCamera(a.selectedCamera); err != nil {
			dialog.ShowError(err, a.window)
			return
		}

		if prevServerCam != a.cfg.RTSPServerCamera {
			a.multiManager.Close()
			a.multiManager = stream.NewMultiManager(a.cfg, a.cfgPath, a.logger)
			if a.cfg.AutoStart {
				a.multiManager.StartAll()
			}
		}

		a.mu.Lock()
		if len(a.cfg.Cameras) > 0 {
			a.selectedCamera = a.cfg.Cameras[0].ID
		}
		a.mu.Unlock()

		a.rebuildGrid()
	}, a.window)
}

// --- Per-camera recording → post-process grid ---

func (a *App) blockWhileRecording() bool {
	a.mu.Lock()
	recording := a.isRecording
	a.mu.Unlock()
	if recording {
		a.sendOSNotification(i18n.T("record_locked_title"), i18n.T("record_locked_msg"))
	}
	return recording
}

func (a *App) toggleRecording() {
	if a.isRecording {
		a.stopRecording()
	} else {
		a.startRecording()
	}
}

func (a *App) startRecording() {
	a.mu.Lock()
	if a.isRecording {
		a.mu.Unlock()
		return
	}
	cfgCameras := append([]config.CameraSource(nil), a.cfg.Cameras...)
	order := append([]string(nil), a.cameraOrder...)
	a.mu.Unlock()

	byID := make(map[string]config.CameraSource, len(cfgCameras))
	for _, camera := range cfgCameras {
		byID[camera.ID] = camera
	}
	recordings := make([]stream.CameraRecording, 0, len(cfgCameras))
	for _, id := range order {
		camera, ok := byID[id]
		if !ok || !camera.Enabled {
			continue
		}
		manager := a.multiManager.GetManager(id)
		if manager == nil {
			continue
		}
		width, height := manager.ActiveResolution()
		fps := manager.ActiveFPS()
		if width <= 0 {
			width = camera.Width
		}
		if height <= 0 {
			height = camera.Height
		}
		if fps <= 0 {
			fps = camera.FPS
		}
		recordings = append(recordings, stream.CameraRecording{
			ID:         id,
			Name:       camera.Name,
			Width:      width,
			Height:     height,
			FPS:        fps,
			Order:      len(recordings),
			WasRunning: manager.State().Running,
		})
	}
	if len(recordings) == 0 {
		dialog.ShowError(fmt.Errorf("no enabled camera is available for recording"), a.window)
		return
	}
	cols, rows := ui.CalculateGrid(len(recordings))

	freeBytes, err := stream.DiskFreeBytes(a.cfg.RecordingsDir)
	if err != nil {
		a.logger.Printf("[recording] check disk space failed: %v", err)
		dialog.ShowError(fmt.Errorf(i18n.T("disk_check_error"), err), a.window)
		// Proceed anyway so a system error doesn't completely block recording
		a.showPatientNameDialogForRecording(recordings, cols, rows)
		return
	}

	var availableMins float64
	if a.cfg.CompositeRecording {
		availableMins = stream.EstimateCompositeAvailableMinutes(freeBytes, len(recordings), cols, rows)
	} else {
		availableMins = stream.EstimateCameraRecordingAvailableMinutes(freeBytes, recordings, cols, rows)
	}
	a.logger.Printf("[recording] disk space check: freeBytes=%d, estimated_mins=%.2f", freeBytes, availableMins)

	if availableMins < 1.0 {
		a.showDiskSpaceBlocked(func() {
			a.changeRecordingsDirAndRetry()
		})
		return
	} else if availableMins < 15.0 {
		a.showDiskSpaceWarning(availableMins, func() {
			a.showPatientNameDialogForRecording(recordings, cols, rows)
		}, func() {
			a.changeRecordingsDirAndRetry()
		})
		return
	}

	a.showPatientNameDialogForRecording(recordings, cols, rows)
}

func (a *App) proceedWithRecording(cameras []stream.CameraRecording, cols, rows int, patientDir string, recordTag string) {
	session, err := stream.NewRecordingSession(patientDir, cameras, cols, rows, recordTag)
	if err != nil {
		dialog.ShowError(err, a.window)
		return
	}

	content := container.NewVBox(widget.NewLabel(i18n.T("msg_please_wait")), widget.NewProgressBarInfinite())
	progress := a.NewToastProgress(i18n.T("msg_please_wait"), content)
	progress.Show()

	go func() {
		var startErr error
		if a.cfg.CompositeRecording {
			// In composite mode, we don't restart FFmpeg pipelines. We just start
			// the composite recorder which reads from existing preview frames.
			session.UpdateStartTime(time.Now())
			ffmpegPath, _ := stream.ResolveFFmpegPath(a.cfg.FFmpegPath)
			outFile := filepath.Join(patientDir, fmt.Sprintf("Genel_%s%s.mp4", time.Now().Format("20060102_150405"), recordTag))
			
			cameraOrder := make([]string, len(cameras))
			for i, c := range cameras {
				cameraOrder[i] = c.ID
			}

			a.mu.Lock()
			a.compositeRecorder = stream.NewCompositeRecorder(
				ffmpegPath,
				outFile,
				cameraOrder,
				cols, rows,
				a.logger,
				func(fps int) {
					fyne.Do(func() {
						a.updatePerfBanner(fps)
						if a.perfBanner != nil {
							a.perfBanner.Show()
						}
					})
				},
			)
			a.mu.Unlock()

			startErr = a.compositeRecorder.Start()
			
			fyne.Do(func() {
				a.initPerfBanner()
			})
		} else {
			startErr = a.multiManager.StartRecording(session, a.postProc.HardwareProfile())
		}

		fyne.Do(func() {
			progress.Hide()
			if startErr != nil {
				_ = os.RemoveAll(session.TempDir)
				a.mu.Lock()
				a.compositeRecorder = nil
				a.mu.Unlock()
				dialog.ShowError(startErr, a.window)
				return
			}

			a.mu.Lock()
			a.isRecording = true
			a.recordSession = session
			a.recordStart = session.StartedAt
			a.mu.Unlock()

			a.updateRecordBtn()

			// Start timer display in status bar
			a.recordTimer = time.NewTicker(time.Second)
			go func() {
				for range a.recordTimer.C {
					a.mu.Lock()
					recording := a.isRecording
					a.mu.Unlock()
					if !recording {
						return
					}
					elapsed := time.Since(a.recordStart).Truncate(time.Second)
					fyne.Do(func() {
						if a.recordingTimeLabel != nil {
							a.recordingTimeLabel.Text = i18n.T("lbl_status_bar_rec", elapsed)
							a.recordingTimeLabel.Refresh()
						}
					})
				}
			}()

			a.startDiskMonitor(session, cameras, cols, rows)
		})
	}()
}

func (a *App) showDiskSpaceWarning(availableMinutes float64, onProceed func(), onChangeDir func()) {
	var d dialog.Dialog

	msg := fmt.Sprintf(i18n.T("disk_warning_msg"), availableMinutes)
	text := widget.NewLabel(msg)

	changeBtn := widget.NewButton(i18n.T("disk_btn_change_dir"), func() {
		fyne.Do(func() {
			d.Hide()
		})
		onChangeDir()
	})
	proceedBtn := widget.NewButton(i18n.T("disk_btn_continue"), func() {
		fyne.Do(func() {
			d.Hide()
		})
		onProceed()
	})
	cancelBtn := widget.NewButton(i18n.T("disk_btn_cancel"), func() {
		fyne.Do(func() {
			d.Hide()
		})
	})

	proceedBtn.Importance = widget.WarningImportance

	buttons := container.NewHBox(
		layout.NewSpacer(),
		changeBtn,
		proceedBtn,
		cancelBtn,
		layout.NewSpacer(),
	)

	content := container.NewVBox(
		text,
		widget.NewSeparator(),
		buttons,
	)

	d = dialog.NewCustomWithoutButtons(
		i18n.T("disk_warning_title"),
		content,
		a.window,
	)
	d.Show()
}

func (a *App) showDiskSpaceBlocked(onChangeDir func()) {
	var d dialog.Dialog

	msg := i18n.T("disk_blocked_msg")
	text := widget.NewLabel(msg)

	changeBtn := widget.NewButton(i18n.T("disk_btn_change_dir"), func() {
		fyne.Do(func() {
			d.Hide()
		})
		onChangeDir()
	})
	cancelBtn := widget.NewButton(i18n.T("disk_btn_cancel"), func() {
		fyne.Do(func() {
			d.Hide()
		})
	})

	buttons := container.NewHBox(
		layout.NewSpacer(),
		changeBtn,
		cancelBtn,
		layout.NewSpacer(),
	)

	content := container.NewVBox(
		text,
		widget.NewSeparator(),
		buttons,
	)

	d = dialog.NewCustomWithoutButtons(
		i18n.T("disk_blocked_title"),
		content,
		a.window,
	)
	d.Show()
}

func (a *App) changeRecordingsDirAndRetry() {
	dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
		if err != nil || uri == nil {
			return
		}
		a.mu.Lock()
		a.cfg.RecordingsDir = uri.Path()
		_ = config.Save(*a.cfg, a.cfgPath)
		a.mu.Unlock()

		// Re-trigger the startRecording() check flow
		a.startRecording()
	}, a.window)
}

func (a *App) startDiskMonitor(session *stream.RecordingSession, cameras []stream.CameraRecording, cols, rows int) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.initDiskBanner()

	a.diskMonitorStop = make(chan struct{})
	stopChan := a.diskMonitorStop
	estimatedPerMinute := stream.EstimateCameraRecordingBytesPerMinute(cameras, cols, rows)

	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		lastCheck := time.Now()
		lastSize := session.DirectorySize()
		for {
			select {
			case <-stopChan:
				fyne.Do(func() {
					if a.diskBanner != nil {
						a.diskBanner.Hide()
					}
				})
				return
			case <-ticker.C:
				freeBytes, err := stream.DiskFreeBytes(a.cfg.RecordingsDir)
				if err != nil {
					a.logger.Printf("[disk monitor] check failed: %v", err)
					continue
				}
				now := time.Now()
				currentSize := session.DirectorySize()
				elapsed := now.Sub(lastCheck).Minutes()
				actualPerMinute := uint64(0)
				if elapsed > 0 && currentSize >= lastSize {
					actualPerMinute = uint64(float64(currentSize-lastSize) / elapsed)
				}
				lastCheck = now
				lastSize = currentSize

				// During post-processing source, individual, aligned, and grid
				// files coexist. Reserve four times the observed live rate.
				requiredPerMinute := estimatedPerMinute
				if actualPerMinute > 0 && actualPerMinute*4 > requiredPerMinute {
					requiredPerMinute = actualPerMinute * 4
				}
				mins := float64(0)
				if requiredPerMinute > 0 {
					mins = float64(freeBytes) / float64(requiredPerMinute)
				}
				fyne.Do(func() {
					a.mu.Lock()
					recording := a.isRecording
					a.mu.Unlock()
					if !recording {
						if a.diskBanner != nil {
							a.diskBanner.Hide()
						}
						return
					}

					if mins < 15.0 {
						a.updateDiskBanner(mins)
						if a.diskBanner != nil {
							a.diskBanner.Show()
						}
					} else {
						if a.diskBanner != nil {
							a.diskBanner.Hide()
						}
					}

					// Leave an absolute 256 MiB emergency margin so FFmpeg can
					// finalize every MKV container before recording stops.
					if freeBytes <= 256*1024*1024 || mins < 0.5 {
						a.stopRecording()
						dialog.ShowError(fmt.Errorf(i18n.T("disk_auto_stop_msg")), a.window)
					}
				})
			}
		}
	}()
}

func (a *App) stopDiskMonitor() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.diskMonitorStop != nil {
		close(a.diskMonitorStop)
		a.diskMonitorStop = nil
	}
	if a.diskBanner != nil {
		a.diskBanner.Hide()
	}
}

func (a *App) initDiskBanner() {
	if a.diskBanner != nil {
		return
	}

	a.diskBannerBg = canvas.NewRectangle(color.NRGBA{R: 243, G: 156, B: 18, A: 200}) // Amber Warning
	a.diskBannerBg.CornerRadius = 8
	a.diskBannerText = canvas.NewText("", color.NRGBA{R: 236, G: 240, B: 241, A: 255}) // Text Primary
	a.diskBannerText.TextStyle = fyne.TextStyle{Bold: true}
	a.diskBannerText.Alignment = fyne.TextAlignCenter
	a.diskBannerText.TextSize = 13

	textContainer := container.NewPadded(a.diskBannerText)
	bannerStack := container.NewStack(a.diskBannerBg, textContainer)
	marginContainer := container.NewPadded(bannerStack)

	a.diskBanner = container.NewBorder(
		nil, nil, nil,
		container.NewVBox(marginContainer),
	)
	a.diskBanner.Hide()

	if a.gridContainer != nil {
		a.gridContainer.Add(a.diskBanner)
	}
}

func (a *App) updateDiskBanner(minutes float64) {
	if a.diskBannerText == nil || a.diskBannerBg == nil {
		return
	}
	if minutes < 3.0 {
		a.diskBannerBg.FillColor = color.NRGBA{R: 231, G: 76, B: 60, A: 200} // Red Critical
		a.diskBannerText.Text = fmt.Sprintf(i18n.T("disk_live_critical"), minutes)
	} else {
		a.diskBannerBg.FillColor = color.NRGBA{R: 243, G: 156, B: 18, A: 200} // Amber Warning
		a.diskBannerText.Text = fmt.Sprintf(i18n.T("disk_live_warning"), minutes)
	}
	a.diskBannerBg.Refresh()
	a.diskBannerText.Refresh()
}

func (a *App) stopRecording() {
	a.mu.Lock()
	if !a.isRecording {
		a.mu.Unlock()
		return
	}
	a.isRecording = false
	session := a.recordSession
	a.recordSession = nil
	compositeRec := a.compositeRecorder
	a.compositeRecorder = nil
	a.mu.Unlock()

	a.stopDiskMonitor()
	a.hidePerfBanner()

	if a.recordTimer != nil {
		a.recordTimer.Stop()
		a.recordTimer = nil
	}

	fyne.Do(func() {
		a.updateRecordBtn()

		// Clear recording timer in status bar
		if a.recordingTimeLabel != nil {
			a.recordingTimeLabel.Text = ""
			a.recordingTimeLabel.Refresh()
		}
	})
	if session == nil {
		return
	}

	// ---------------------------------------------------------------
	// COMPOSITE MODE: CompositeRecorder writes the video in real-time.
	// Stop it (flushes FFmpeg), video is immediately available.
	// ---------------------------------------------------------------
	if compositeRec != nil {
		content := container.NewVBox(widget.NewLabel(i18n.T("msg_please_wait")), widget.NewProgressBarInfinite())
		progress := a.NewToastProgress(i18n.T("msg_please_wait"), content)
		progress.Show()

		go func() {
			// 1. Finalize the composite video file.
			err := compositeRec.Stop()
			// 2. Finalize session timestamps.
			a.multiManager.StopRecording(session)

			actualPatientDir := filepath.Dir(session.TempDir)
			compositeFile := compositeRec.OutFile()

			fyne.Do(func() {
				progress.Hide()

				if err != nil {
					dialog.ShowError(fmt.Errorf("Genel video kaydedilemedi: %v", err), a.window)
					return
				}

				// Save composite file reference to patient info.
				info, infoErr := stream.LoadPatientInfo(actualPatientDir)
				if infoErr == nil {
					info.Videos = append(info.Videos, stream.VideoFile{
						FileName: filepath.Base(compositeFile),
						Type:     "general",
					})
					_ = stream.SavePatientInfo(actualPatientDir, info)
				}

				// Enqueue background decompose job.
				if a.jobQueue != nil {
					_ = a.jobQueue.EnqueueComposite(session, actualPatientDir, compositeFile)
				}

				// Send OS notification instead of blocking dialog
				a.sendOSNotification(i18n.T("record_done_title"), i18n.T("record_saved_notification"))

				// Show in Kayıtlarım drawer with highlight
				a.showRecordingsDrawerWithHighlight(actualPatientDir)
			})
		}()
		return
	}

	// ---------------------------------------------------------------
	// STANDARD MODE: per-camera MJPEG segments → post-process
	// ---------------------------------------------------------------
	content := container.NewVBox(widget.NewLabel(i18n.T("msg_please_wait")), widget.NewProgressBarInfinite())
	progress := a.NewToastProgress(i18n.T("msg_please_wait"), content)
	progress.Show()

	go func() {
		a.multiManager.StopRecording(session)
		
		fyne.Do(func() {
			progress.Hide()
			
			// Start fast single-pass grid processing as a toast popup
			progressBar := widget.NewProgressBar()
			progressLabel := widget.NewLabel(i18n.T("record_processing"))
			progressContent := container.NewVBox(progressLabel, progressBar)
			progressToast := a.NewToastProgress(i18n.T("record_processing"), progressContent)
			progressToast.Show()

			go func() {
				ctx := context.Background()
				progressCh := make(chan float64, 10)
				patientDir := session.Snapshot().TempDir
				// TempDir is set to patientDir/raw in manager.go/recording_session.go.
				// Let me get the actual patientDir.
				_ = patientDir
				actualPatientDir := filepath.Dir(session.TempDir)

				var result stream.ProcessResult
				done := make(chan struct{})

				go func() {
					snap := session.Snapshot()
					cams := session.CameraList()
					result = a.postProc.ProcessGeneralOnly(ctx, snap, cams, actualPatientDir, progressCh, true)
					close(done)
				}()

				// Read from progress channel and update UI
				go func() {
					for val := range progressCh {
						v := val
						fyne.Do(func() {
							progressBar.SetValue(v)
						})
					}
				}()

				<-done
				
				// Enqueue remaining files to JobQueue
				if result.Err == nil && a.jobQueue != nil {
					_ = a.jobQueue.Enqueue(session, actualPatientDir)
				}
				
				// Update patient info with the general video
				if result.Err == nil && len(result.Files) > 0 {
					info, err := stream.LoadPatientInfo(actualPatientDir)
					if err == nil {
						info.Videos = append(info.Videos, stream.VideoFile{
							FileName: filepath.Base(result.Files[0]),
							Type:     "general",
						})
						_ = stream.SavePatientInfo(actualPatientDir, info)
					}
				}

				fyne.Do(func() {
					progressToast.Hide()

					if result.Err != nil {
						detailedErr := fmt.Errorf("Genel video oluşturulamadı: %v", result.Err)
						dialog.ShowError(detailedErr, a.window)
						return
					}

					// Send OS notification instead of blocking dialog
					a.sendOSNotification(i18n.T("record_done_title"), i18n.T("record_saved_notification"))

					// Show in Kayıtlarım drawer with highlight
					a.showRecordingsDrawerWithHighlight(actualPatientDir)
				})
			}()
		})
	}()
}

// --- Perf Banner (composite FPS auto-reduction warning) ---

func (a *App) initPerfBanner() {
	if a.perfBanner != nil {
		return
	}
	a.perfBannerBg = canvas.NewRectangle(color.NRGBA{R: 231, G: 76, B: 60, A: 220})
	a.perfBannerBg.CornerRadius = 4
	a.perfBannerText = canvas.NewText("", color.NRGBA{R: 236, G: 240, B: 241, A: 255})
	a.perfBannerText.TextStyle = fyne.TextStyle{Bold: true}
	a.perfBannerText.Alignment = fyne.TextAlignCenter
	a.perfBannerText.TextSize = 14

	textContainer := container.NewPadded(a.perfBannerText)
	bannerStack := container.NewStack(a.perfBannerBg, textContainer)
	marginContainer := container.NewPadded(bannerStack)

	// Place at the top of the grid container.
	a.perfBanner = container.NewBorder(
		container.NewVBox(marginContainer),
		nil, nil, nil,
	)
	a.perfBanner.Hide()

	if a.gridContainer != nil {
		a.gridContainer.Add(a.perfBanner)
	}
}

func (a *App) updatePerfBanner(fps int) {
	if a.perfBannerText == nil || a.perfBannerBg == nil {
		return
	}
	a.perfBannerText.Text = fmt.Sprintf(i18n.T("perf_warning_msg"), fps)
	a.perfBannerBg.Refresh()
	a.perfBannerText.Refresh()
}

func (a *App) hidePerfBanner() {
	if a.perfBanner != nil {
		a.perfBanner.Hide()
	}
}

func (a *App) showPatientNameDialogForRecording(cameras []stream.CameraRecording, cols, rows int) {
	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder(i18n.T("record_patient_placeholder"))
	
	patientIDEntry := widget.NewEntry()
	patientIDEntry.SetPlaceHolder(i18n.T("record_patient_id_placeholder"))
	
	historyEntry := widget.NewMultiLineEntry()
	historyEntry.SetPlaceHolder(i18n.T("record_patient_history_placeholder"))
	historyEntry.SetMinRowsVisible(3)

	// Pre-fill fields from cache if valid
	cachedName, cachedPatientID, cachedHistory, cachedDir, _, cacheValid := a.patientCache.Get()
	if cacheValid {
		nameEntry.Text = cachedName
		patientIDEntry.Text = cachedPatientID
		historyEntry.Text = cachedHistory
	}

	dlg := dialog.NewForm(
		i18n.T("record_patient_title"),
		i18n.T("record_patient_btn"),
		i18n.T("btn_cancel"),
		[]*widget.FormItem{
			widget.NewFormItem(i18n.T("record_patient_label"), nameEntry),
			widget.NewFormItem(i18n.T("record_patient_id_label"), patientIDEntry),
			widget.NewFormItem(i18n.T("record_patient_history_label"), historyEntry),
		},
		func(ok bool) {
			if !ok {
				return
			}
			patientName := strings.TrimSpace(nameEntry.Text)
			patientID := strings.TrimSpace(patientIDEntry.Text)
			history := strings.TrimSpace(historyEntry.Text)

			var outDir string
			var recordTag string

			// If cache is valid and user did not change any info, reuse the directory and increment record count
			if cacheValid && patientName == cachedName && patientID == cachedPatientID && history == cachedHistory {
				outDir = cachedDir
				count := a.patientCache.IncrementRecordCount()
				recordTag = fmt.Sprintf("_REC%02d", count)
			} else {
				// Otherwise, it's a new patient or user changed the info, create a new directory
				outDir = stream.GetOutputDir(a.cfg.RecordingsDir, patientName)
				if err := os.MkdirAll(outDir, 0o755); err != nil {
					dialog.ShowError(fmt.Errorf("failed to create output directory: %w", err), a.window)
					return
				}
				a.patientCache.Store(patientName, patientID, history, outDir)
				recordTag = "_REC01"
			}
			
			// Create PatientInfo
			info := stream.PatientInfo{
				Name:           patientName,
				PatientID:      patientID,
				TC:             patientID, // For backwards compatibility
				PatientHistory: history,
				RecordDate:     time.Now(),
			}
			if err := stream.SavePatientInfo(outDir, info); err != nil {
				a.logger.Printf("Failed to save patient info: %v", err)
			}
			
			a.proceedWithRecording(cameras, cols, rows, outDir, recordTag)
		},
		a.window,
	)
	dlg.Resize(fyne.NewSize(400, 300))
	dlg.Show()
}

// --- Settings Dialog ---
func (a *App) showSettingsDialog() {
	if a.blockWhileRecording() {
		return
	}
	if a.settingsDialog != nil {
		return
	}
	autostartCheck := widget.NewCheck(i18n.T("lbl_autostart"), nil)
	autostartCheck.SetChecked(a.cfg.AutoStart)

	hwAccelCheck := widget.NewCheck(i18n.T("lbl_disable_hw_accel"), nil)
	hwAccelCheck.SetChecked(a.cfg.DisableHardwareAccel)

	langOptions := []string{"🇹🇷 Türkçe", "🇬🇧 English", "🇸🇦 العربية"}
	langMap := map[string]string{
		"🇹🇷 Türkçe":  "tr",
		"🇬🇧 English": "en",
		"🇸🇦 العربية": "ar",
	}
	reverseLangMap := map[string]string{
		"tr": "🇹🇷 Türkçe",
		"en": "🇬🇧 English",
		"ar": "🇸🇦 العربية",
	}

	langSelect := widget.NewSelect(langOptions, nil)
	if sel, ok := reverseLangMap[a.cfg.Language]; ok {
		langSelect.SetSelected(sel)
	} else {
		langSelect.SetSelected("🇬🇧 English")
	}

	langSelect.OnChanged = func(sel string) {
		newLang := "en"
		if code, ok := langMap[sel]; ok {
			newLang = code
		}

		if a.cfg.Language == newLang {
			return
		}

		i18n.Init(newLang)
		dialog.ShowConfirm(i18n.T("title_lang_restart"), i18n.T("msg_lang_restart"), func(ok bool) {
			if ok {
				a.mu.Lock()
				a.cfg.Language = newLang
				_ = config.Save(*a.cfg, a.cfgPath)
				a.mu.Unlock()

				if a.multiManager != nil {
					a.multiManager.Close()
				}

				restartApp()
			} else {
				i18n.Init(a.cfg.Language)
				if oldSel, ok := reverseLangMap[a.cfg.Language]; ok {
					langSelect.SetSelected(oldSel)
				}
			}
		}, a.window)
	}

	configBtn := widget.NewButtonWithIcon(i18n.T("btn_open_config"), theme.SettingsIcon(), func() {
		openPath(a.cfgPath)
	})

	logsBtn := widget.NewButtonWithIcon(i18n.T("btn_show_logs"), theme.FolderOpenIcon(), func() {
		if logDir, err := config.LogsDir(); err == nil {
			openPath(logDir)
		}
	})

	versionText := canvas.NewText(i18n.T("lbl_version", version.Version), theme.DisabledColor())
	versionText.TextSize = theme.CaptionTextSize()
	versionText.Alignment = fyne.TextAlignTrailing

	recordingsDirEntry := widget.NewEntry()
	recordingsDirEntry.SetText(a.cfg.RecordingsDir)
	recordingsDirEntry.Disable() // Disable direct manual typing to avoid typos, force using browser

	browseBtn := widget.NewButtonWithIcon("", theme.FolderOpenIcon(), func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil || uri == nil {
				return
			}
			recordingsDirEntry.SetText(uri.Path())
		}, a.window)
	})

	recordingsRow := container.NewBorder(nil, nil, widget.NewLabel(i18n.T("lbl_recordings_dir")+": "), browseBtn, recordingsDirEntry)

	compositeCheck := widget.NewCheck(i18n.T("lbl_composite_recording"), nil)
	compositeCheck.SetChecked(a.cfg.CompositeRecording)

	compositeTitleLabel := widget.NewLabel("")
	compositeTitleLabel.TextStyle = fyne.TextStyle{Bold: true}
	compositeDescLabel := widget.NewLabel("")
	compositeDescLabel.Wrapping = fyne.TextWrapWord

	updateCompositeLabels := func(checked bool) {
		if checked {
			compositeTitleLabel.SetText(i18n.T("lbl_composite_on_title"))
			compositeDescLabel.SetText(i18n.T("lbl_composite_on_desc"))
		} else {
			compositeTitleLabel.SetText(i18n.T("lbl_composite_off_title"))
			compositeDescLabel.SetText(i18n.T("lbl_composite_off_desc"))
		}
	}

	compositeCheck.OnChanged = updateCompositeLabels
	updateCompositeLabels(a.cfg.CompositeRecording)

	compositeGroup := container.NewVBox(
		widget.NewLabelWithStyle(i18n.T("lbl_composite_section"), fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		compositeCheck,
		compositeTitleLabel,
		compositeDescLabel,
	)

	settingsContent := container.NewVScroll(container.NewVBox(
		widget.NewLabelWithStyle(i18n.T("title_general_settings"), fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		container.NewBorder(nil, nil, widget.NewLabel(i18n.T("lbl_language")+": "), nil, langSelect),
		recordingsRow,
		autostartCheck,
		hwAccelCheck,
		widget.NewSeparator(),
		compositeGroup,
		widget.NewSeparator(),
		container.NewGridWithColumns(2, configBtn, logsBtn),
		container.NewHBox(layout.NewSpacer(), versionText),
	))
	settingsContent.SetMinSize(fyne.NewSize(500, 400))

	a.settingsLangSelect = langSelect
	a.settingsAutostartCheck = autostartCheck
	a.settingsCompositeCheck = compositeCheck

	d := dialog.NewCustom(i18n.T("title_settings"), i18n.T("btn_save_close"), settingsContent, a.window)
	d.Resize(fyne.NewSize(550, 450))
	a.settingsDialog = d

	d.SetOnClosed(func() {
		a.mu.Lock()
		a.cfg.AutoStart = autostartCheck.Checked
		a.cfg.DisableHardwareAccel = hwAccelCheck.Checked
		a.cfg.CompositeRecording = compositeCheck.Checked
		a.cfg.RecordingsDir = recordingsDirEntry.Text
		_ = config.Save(*a.cfg, a.cfgPath)
		a.mu.Unlock()

		if autostartCheck.Checked {
			_ = autostart.SetEnabled(true)
		} else {
			_ = autostart.SetEnabled(false)
		}

		if a.multiManager != nil {
			a.multiManager.UpdateConfig(*a.cfg)
		}
		a.settingsDialog = nil
	})

	d.Show()
}

// --- Layout Save/Load ---

// --- Layout Save/Load & Drawer System ---

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

func (a *App) saveWindowLayoutToConfig() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg == nil || a.window == nil {
		return
	}
	size := a.window.Canvas().Size()
	if size.Width > 100 && size.Height > 100 {
		a.cfg.WindowWidth = int(size.Width)
		a.cfg.WindowHeight = int(size.Height)
	}
	a.cfg.SplitOffsets = CollectSplitOffsets(a.cameraGrid)
	_ = config.Save(*a.cfg, a.cfgPath)
}

func (a *App) buildLayoutDrawer() {
	drawerBg := canvas.NewRectangle(colorCardSurface)

	drawerSpacer := canvas.NewRectangle(color.Transparent)
	drawerSpacer.SetMinSize(fyne.NewSize(240, 0))

	// Title
	titleLabel := canvas.NewText(i18n.T("btn_layouts"), colorTextPrimary)
	titleLabel.TextSize = 15
	titleLabel.TextStyle = fyne.TextStyle{Bold: true}

	closeBtn := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		a.toggleLayoutDrawer()
	})
	closeBtn.Importance = widget.LowImportance

	header := container.NewBorder(nil, nil, nil, closeBtn, container.NewPadded(titleLabel))

	a.layoutListContainer = container.NewVBox()
	scroll := container.NewVScroll(a.layoutListContainer)

	saveBtn := widget.NewButtonWithIcon(i18n.T("layout_save_btn"), theme.DocumentSaveIcon(), func() {
		a.showSaveLayoutDialog()
	})
	saveBtn.Importance = widget.HighImportance

	drawerContent := container.NewBorder(
		container.NewVBox(header, widget.NewSeparator()),
		container.NewVBox(widget.NewSeparator(), container.NewPadded(saveBtn)),
		nil, nil,
		scroll,
	)

	a.drawerPanel = container.NewStack(drawerBg, drawerSpacer, drawerContent)
	a.drawerPanel.Hide()
	a.drawerVisible = false
}

func (a *App) refreshLayoutList() {
	if a.layoutListContainer == nil {
		return
	}
	a.layoutListContainer.Objects = nil

	a.mu.Lock()
	layouts := make([]config.SavedLayout, len(a.cfg.SavedLayouts))
	copy(layouts, a.cfg.SavedLayouts)
	activeName := a.cfg.ActiveLayoutName
	a.mu.Unlock()

	if len(layouts) == 0 {
		emptyLabel := widget.NewLabel(i18n.T("layout_empty"))
		emptyLabel.Alignment = fyne.TextAlignCenter
		a.layoutListContainer.Add(emptyLabel)
		a.layoutListContainer.Refresh()
		return
	}

	for _, l := range layouts {
		layoutName := l.Name

		// Miniature silhouette of the grid layout shape using custom rendering tree to avoid thick handles
		gridCols, gridRows := ui.CalculateGrid(len(l.Cameras))
		totalCells := gridCols * gridRows

		tree := buildGridTree(gridCols, gridRows, totalCells)
		if len(l.SplitOffsets) > 0 {
			idx := 0
			assignOffsets(tree, l.SplitOffsets, &idx)
		}

		var miniObjects []fyne.CanvasObject
		miniObjects = renderTree(tree, 0, 0, 48, 36, miniObjects, l.Cameras)

		miniGrid := container.NewWithoutLayout(miniObjects...)

		miniGridSpacer := canvas.NewRectangle(color.Transparent)
		miniGridSpacer.SetMinSize(fyne.NewSize(48, 36))
		miniGridWrapper := container.NewStack(miniGridSpacer, miniGrid)

		// Smaller layout name and camera count texts
		var nameColor color.Color = colorTextPrimary
		if layoutName == activeName {
			nameColor = colorMedicalBlue
		}
		nameText := canvas.NewText(layoutName, nameColor)
		nameText.TextSize = 12
		nameText.TextStyle = fyne.TextStyle{Bold: true}

		infoText := canvas.NewText(fmt.Sprintf(i18n.T("lbl_camera_count"), len(l.Cameras)), colorTextSecondary)
		infoText.TextSize = 9

		// Stack texts vertically
		textCol := container.NewVBox(nameText, infoText)

		// Read closure-safe variables
		lname := layoutName

		deleteBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
			a.deleteLayoutByName(lname)
		})
		deleteBtn.Importance = widget.DangerImportance

		// Visual content that is clickable (silhouette + text)
		visualContent := container.NewBorder(nil, nil, miniGridWrapper, nil, container.NewCenter(textCol))

		cardClickable := newClickableCard(visualContent, func() {
			a.loadLayoutByName(lname)
		})

		// Layout content: clickable part on left/center, delete button on right
		itemContent := container.NewBorder(nil, nil, nil, deleteBtn, cardClickable)

		itemBg := canvas.NewRectangle(colorInputSurface)
		itemBg.CornerRadius = 6

		itemCard := container.NewStack(itemBg, container.NewPadded(itemContent))

		a.layoutListContainer.Add(itemCard)
	}
	a.layoutListContainer.Refresh()
}

func (a *App) toggleLayoutDrawer() {
	a.mu.Lock()
	a.drawerVisible = !a.drawerVisible
	visible := a.drawerVisible
	a.mu.Unlock()

	if visible {
		a.refreshLayoutList()
		a.drawerPanel.Show()
	} else {
		a.drawerPanel.Hide()
	}
	a.window.Content().Refresh()
}

// --- Recordings Drawer (Kayıtlarım) ---

func (a *App) buildRecordingsDrawer() {
	drawerBg := canvas.NewRectangle(colorCardSurface)

	drawerSpacer := canvas.NewRectangle(color.Transparent)
	drawerSpacer.SetMinSize(fyne.NewSize(280, 0))

	// Title
	titleLabel := canvas.NewText(i18n.T("btn_recordings"), colorTextPrimary)
	titleLabel.TextSize = 15
	titleLabel.TextStyle = fyne.TextStyle{Bold: true}

	closeBtn := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		a.toggleRecordingsDrawer()
	})
	closeBtn.Importance = widget.LowImportance

	header := container.NewBorder(nil, nil, nil, closeBtn, container.NewPadded(titleLabel))

	a.recordingsListContainer = container.NewVBox()
	scroll := container.NewVScroll(a.recordingsListContainer)

	drawerContent := container.NewBorder(
		container.NewVBox(header, widget.NewSeparator()),
		nil,
		nil, nil,
		scroll,
	)

	a.recordingsDrawerPanel = container.NewStack(drawerBg, drawerSpacer, drawerContent)
	a.recordingsDrawerPanel.Hide()
	a.recordingsDrawerVisible = false
}

func (a *App) toggleRecordingsDrawer() {
	a.mu.Lock()
	a.recordingsDrawerVisible = !a.recordingsDrawerVisible
	visible := a.recordingsDrawerVisible
	a.mu.Unlock()

	if visible {
		go a.refreshRecordingsList()
		a.recordingsDrawerPanel.Show()
	} else {
		a.recordingsDrawerPanel.Hide()
	}
	a.window.Content().Refresh()
}

type recordingEntry struct {
	Name         string
	Date         time.Time
	ManeuverCount int
	Dir          string
}

func (a *App) refreshRecordingsList() {
	a.mu.Lock()
	recDir := a.cfg.RecordingsDir
	a.mu.Unlock()

	entries, err := os.ReadDir(recDir)
	if err != nil {
		fyne.Do(func() {
			a.recordingsListContainer.Objects = nil
			emptyLabel := widget.NewLabel(i18n.T("recordings_empty"))
			emptyLabel.Alignment = fyne.TextAlignCenter
			a.recordingsListContainer.Add(emptyLabel)
			a.recordingsListContainer.Refresh()
		})
		return
	}

	var recordings []recordingEntry
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirPath := filepath.Join(recDir, entry.Name())
		info, err := stream.LoadPatientInfo(dirPath)
		if err != nil {
			continue
		}

		// Count maneuvers: number of "general" type videos
		maneuverCount := 0
		for _, v := range info.Videos {
			if v.Type == "general" {
				maneuverCount++
			}
		}
		if maneuverCount == 0 {
			maneuverCount = 1 // At least 1 if patient_info exists
		}

		recordings = append(recordings, recordingEntry{
			Name:          info.Name,
			Date:          info.RecordDate,
			ManeuverCount: maneuverCount,
			Dir:           dirPath,
		})
	}

	// Sort by date, newest first
	sort.Slice(recordings, func(i, j int) bool {
		return recordings[i].Date.After(recordings[j].Date)
	})

	// Limit to 50
	if len(recordings) > 50 {
		recordings = recordings[:50]
	}

	fyne.Do(func() {
		a.recordingsListContainer.Objects = nil

		if len(recordings) == 0 {
			emptyLabel := widget.NewLabel(i18n.T("recordings_empty"))
			emptyLabel.Alignment = fyne.TextAlignCenter
			a.recordingsListContainer.Add(emptyLabel)
			a.recordingsListContainer.Refresh()
			return
		}

		for _, rec := range recordings {
			recDir := rec.Dir

			nameText := canvas.NewText(rec.Name, colorTextPrimary)
			nameText.TextSize = 12
			nameText.TextStyle = fyne.TextStyle{Bold: true}
			if rec.Name == "" {
				nameText.Text = "—"
			}

			dateStr := rec.Date.Format("02.01.2006 15:04")
			infoStr := fmt.Sprintf("%s - %s", dateStr, fmt.Sprintf(i18n.T("recordings_maneuver_count"), rec.ManeuverCount))
			infoText := canvas.NewText(infoStr, colorTextSecondary)
			infoText.TextSize = 10

			textCol := container.NewVBox(nameText, infoText)

			openBtn := widget.NewButtonWithIcon("", theme.FolderOpenIcon(), func() {
				openPath(recDir)
			})
			openBtn.Importance = widget.LowImportance

			itemContent := container.NewBorder(nil, nil, nil, openBtn, container.NewPadded(textCol))

			itemBg := canvas.NewRectangle(colorInputSurface)
			itemBg.CornerRadius = 6

			itemCard := container.NewStack(itemBg, container.NewPadded(itemContent))

			a.recordingsListContainer.Add(itemCard)
		}
		a.recordingsListContainer.Refresh()
	})
}

func (a *App) showRecordingsDrawerWithHighlight(patientDir string) {
	a.mu.Lock()
	wasVisible := a.recordingsDrawerVisible
	a.recordingsDrawerVisible = true
	a.mu.Unlock()

	// Refresh list first, then show drawer, then highlight
	go func() {
		a.refreshRecordingsList()

		fyne.Do(func() {
			if !wasVisible {
				a.recordingsDrawerPanel.Show()
				a.window.Content().Refresh()
			}

			// Find the matching item and blink it
			a.blinkRecordingItem(patientDir)
		})
	}()
}

func (a *App) blinkRecordingItem(patientDir string) {
	if a.recordingsListContainer == nil {
		return
	}

	// Find the card that matches the patient dir
	// Each item is a Stack(itemBg, Padded(itemContent))
	// We find it by searching items with matching dir
	a.mu.Lock()
	recDir := a.cfg.RecordingsDir
	a.mu.Unlock()

	targetBase := filepath.Base(patientDir)
	targetFull := filepath.Join(recDir, targetBase)
	_ = targetFull

	// Search through list items to find matching background rect
	for _, obj := range a.recordingsListContainer.Objects {
		if stack, ok := obj.(*fyne.Container); ok && len(stack.Objects) >= 1 {
			if bg, ok := stack.Objects[0].(*canvas.Rectangle); ok {
				// We need to identify the correct item. Since items are in order
				// and we just refreshed, we try to match by checking the patient dir.
				// Use the text content to match the name.
				highlightBg := bg
				go func() {
					normalColor := colorInputSurface
					highlightColor := color.NRGBA{R: 243, G: 156, B: 18, A: 200} // Amber

					for i := 0; i < 3; i++ {
						fyne.Do(func() {
							highlightBg.FillColor = highlightColor
							highlightBg.Refresh()
						})
						time.Sleep(300 * time.Millisecond)
						fyne.Do(func() {
							highlightBg.FillColor = normalColor
							highlightBg.Refresh()
						})
						time.Sleep(300 * time.Millisecond)
					}
				}()
				return // Blink only the first (newest) item, which matches after refresh
			}
		}
	}
}

func (a *App) loadLayoutByName(name string) {
	if a.blockWhileRecording() {
		return
	}
	a.mu.Lock()
	idx := -1
	for i, l := range a.cfg.SavedLayouts {
		if l.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		a.mu.Unlock()
		return
	}
	loadedLayout := a.cfg.SavedLayouts[idx]
	a.cfg.Cameras = make([]config.CameraSource, len(loadedLayout.Cameras))
	copy(a.cfg.Cameras, loadedLayout.Cameras)
	a.cfg.ActiveLayoutName = loadedLayout.Name

	if loadedLayout.WindowWidth > 100 && loadedLayout.WindowHeight > 100 {
		a.cfg.WindowWidth = loadedLayout.WindowWidth
		a.cfg.WindowHeight = loadedLayout.WindowHeight
	}
	if len(loadedLayout.SplitOffsets) > 0 {
		a.cfg.SplitOffsets = make([]float64, len(loadedLayout.SplitOffsets))
		copy(a.cfg.SplitOffsets, loadedLayout.SplitOffsets)
	}

	_ = config.Save(*a.cfg, a.cfgPath)
	a.mu.Unlock()

	a.multiManager.Close()
	a.multiManager = stream.NewMultiManager(a.cfg, a.cfgPath, a.logger)

	a.rebuildGrid()

	if !a.window.FullScreen() && loadedLayout.WindowWidth > 100 && loadedLayout.WindowHeight > 100 {
		a.window.Resize(fyne.NewSize(float32(loadedLayout.WindowWidth), float32(loadedLayout.WindowHeight)))
	}
	if len(loadedLayout.SplitOffsets) > 0 {
		ApplySplitOffsets(a.cameraGrid, loadedLayout.SplitOffsets)
		offsets := loadedLayout.SplitOffsets
		go func() {
			for _, delay := range []time.Duration{50 * time.Millisecond, 150 * time.Millisecond, 300 * time.Millisecond} {
				time.Sleep(delay)
				fyne.Do(func() {
					ApplySplitOffsets(a.cameraGrid, offsets)
				})
			}
		}()
	}

	if a.cfg.AutoStart {
		a.multiManager.StartAll()
	}

	a.refreshLayoutList()
}

func (a *App) deleteLayoutByName(name string) {
	if a.blockWhileRecording() {
		return
	}

	dialog.ShowConfirm(i18n.T("layout_delete_confirm_title"), fmt.Sprintf(i18n.T("layout_delete_confirm_msg"), name), func(yes bool) {
		if !yes {
			return
		}

		a.mu.Lock()
		idx := -1
		for i, l := range a.cfg.SavedLayouts {
			if l.Name == name {
				idx = i
				break
			}
		}
		if idx >= 0 {
			a.cfg.SavedLayouts = append(a.cfg.SavedLayouts[:idx], a.cfg.SavedLayouts[idx+1:]...)
			if a.cfg.ActiveLayoutName == name {
				a.cfg.ActiveLayoutName = ""
			}
			_ = config.Save(*a.cfg, a.cfgPath)
		}
		a.mu.Unlock()

		a.refreshLayoutList()
	}, a.window)
}

func (a *App) showSaveLayoutDialog() {
	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder(i18n.T("layout_name_placeholder"))

	dialog.ShowForm(i18n.T("layout_save_title"), i18n.T("layout_save_btn"), i18n.T("btn_cancel"), []*widget.FormItem{
		widget.NewFormItem(i18n.T("layout_name_lbl"), nameEntry),
	}, func(ok bool) {
		if !ok || strings.TrimSpace(nameEntry.Text) == "" {
			return
		}

		size := a.window.Canvas().Size()
		var wWidth, wHeight int
		if size.Width > 100 && size.Height > 100 {
			wWidth = int(size.Width)
			wHeight = int(size.Height)
		}
		offsets := CollectSplitOffsets(a.cameraGrid)

		savedLayout := config.SavedLayout{
			Name:         nameEntry.Text,
			Cameras:      make([]config.CameraSource, len(a.cfg.Cameras)),
			WindowWidth:  wWidth,
			WindowHeight: wHeight,
			SplitOffsets: offsets,
		}
		copy(savedLayout.Cameras, a.cfg.Cameras)

		found := false
		for i, l := range a.cfg.SavedLayouts {
			if l.Name == nameEntry.Text {
				a.cfg.SavedLayouts[i] = savedLayout
				found = true
				break
			}
		}
		if !found {
			a.cfg.SavedLayouts = append(a.cfg.SavedLayouts, savedLayout)
		}

		a.cfg.ActiveLayoutName = nameEntry.Text
		_ = config.Save(*a.cfg, a.cfgPath)

		a.sendOSNotification(i18n.T("layout_saved_title"), fmt.Sprintf(i18n.T("layout_saved"), nameEntry.Text))
		a.refreshLayoutList()
	}, a.window)
}

func (a *App) showLoadLayoutDialog() {
	a.toggleLayoutDrawer()
}

// --- Tutorial ---

func (a *App) showTutorial() {
	var firstPanel fyne.CanvasObject
	a.mu.Lock()
	if len(a.cameraOrder) > 0 {
		firstPanel = a.cameraPanels[a.cameraOrder[0]]
	}
	a.mu.Unlock()

	steps := []ui.TutorialStep{
		{TargetWidget: nil, TitleKey: "tutorial_title_0", DescKey: "tutorial_desc_0"},
		{TargetWidget: a.addBtn, TitleKey: "tutorial_title_1", DescKey: "tutorial_desc_1"},
		{TargetWidget: a.startAllRef, TitleKey: "tutorial_title_2", DescKey: "tutorial_desc_2"},
		{TargetWidget: a.recordBtn, TitleKey: "tutorial_title_3", DescKey: "tutorial_desc_3"},
		{TargetWidget: a.layoutsRef, TitleKey: "tutorial_title_5", DescKey: "tutorial_desc_5"},
		{TargetWidget: firstPanel, TitleKey: "tutorial_title_6", DescKey: "tutorial_desc_6"},
		{TargetWidget: a.settingsRef, TitleKey: "tutorial_title_4", DescKey: "tutorial_desc_4"},
	}

	ui.ShowTutorial(a.window, steps, func() {
		a.mu.Lock()
		a.cfg.TutorialShown = true
		_ = config.Save(*a.cfg, a.cfgPath)
		a.mu.Unlock()

		if a.settingsDialog != nil {
			a.settingsDialog.Hide()
			a.settingsDialog = nil
		}
	})
}

func (a *App) setMainButtonsEnabled(enabled bool) {
	if enabled {
		if a.addBtn != nil { a.addBtn.Enable() }
		if a.removeBtn != nil { a.removeBtn.Enable() }
		if a.startStopAllBtn != nil { a.startStopAllBtn.Enable() }
		if a.settingsRef != nil { a.settingsRef.Enable() }
		if a.layoutsRef != nil { a.layoutsRef.Enable() }
		if a.recordBtn != nil { a.recordBtn.Enable() }
		// Record button state is dynamically managed
		a.updateRecordBtn()
	} else {
		if a.addBtn != nil { a.addBtn.Disable() }
		if a.removeBtn != nil { a.removeBtn.Disable() }
		if a.startStopAllBtn != nil { a.startStopAllBtn.Disable() }
		if a.settingsRef != nil { a.settingsRef.Disable() }
		if a.layoutsRef != nil { a.layoutsRef.Disable() }
		if a.recordBtn != nil { a.recordBtn.Disable() }
	}
}

// --- Tray ---

func (a *App) setupTray() {
	log.Println("[App] Setting up system tray...")
	desk, ok := a.fyneApp.(desktop.App)
	if !ok {
		log.Println("[App] WARNING: fyneApp does not implement desktop.App! System tray will not be created.")
		return
	}

	if runtime.GOOS == "linux" {
		if err := installLinuxIcon(iconData); err != nil {
			log.Printf("[App] Failed to install Linux desktop icon: %v", err)
		} else {
			log.Println("[App] Linux desktop icon installed/verified successfully.")
		}
	}

	m := fyne.NewMenu(i18n.T("title_app"),
		fyne.NewMenuItem(i18n.T("tray_show"), func() {
			log.Println("[App] Tray menu: Show clicked")
			a.window.Show()
			a.window.RequestFocus()
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem(i18n.T("tray_start_all"), func() {
			log.Println("[App] Tray menu: Start All clicked")
			a.rtspUIStopped = false
			a.multiManager.StartAll()
		}),
		fyne.NewMenuItem(i18n.T("tray_stop_all"), func() {
			log.Println("[App] Tray menu: Stop All clicked")
			a.rtspUIStopped = true
			a.multiManager.StopAll()
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem(i18n.T("tray_quit"), func() {
			log.Println("[App] Tray menu: Quit clicked")
			a.Quit()
		}),
	)

	desk.SetSystemTrayMenu(m)
	log.Printf("[App] System tray menu set. Embedded icon size: %d bytes", len(iconData))
	desk.SetSystemTrayIcon(fyne.NewStaticResource("icon.png", iconData))
	log.Println("[App] System tray icon set successfully.")
}

func installLinuxIcon(iconData []byte) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	// Always ensure standard "nystavision" name is installed
	names := []string{"nystavision"}

	// Also install using current executable base name (stripped of extensions and lowercased)
	if exePath, err := os.Executable(); err == nil {
		exeName := filepath.Base(exePath)
		exeName = strings.ToLower(strings.TrimSuffix(exeName, filepath.Ext(exeName)))
		if exeName != "nystavision" && exeName != "" {
			names = append(names, exeName)
		}
	}

	for _, name := range names {
		iconDir := filepath.Join(homeDir, ".local", "share", "icons", "hicolor", "256x256", "apps")
		if err := os.MkdirAll(iconDir, 0755); err != nil {
			return err
		}
		iconPath := filepath.Join(iconDir, name+".png")
		if err := os.WriteFile(iconPath, iconData, 0644); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) minimizeToBackground() {
	a.rtspUIStopped = true
	a.multiManager.StopAll()
	a.updateStartStopAllBtn(false)

	if a.isRecording {
		a.stopRecording()
		// Give a moment for post-processing dialog to appear before hiding
		time.AfterFunc(300*time.Millisecond, func() {
			fyne.Do(func() {
				a.window.Hide()
			})
		})
	} else {
		a.window.Hide()
	}
}

func (a *App) handleClose() {
	bgBtn := widget.NewButtonWithIcon(i18n.T("close_btn_background"), theme.ComputerIcon(), nil)
	bgBtn.Importance = widget.HighImportance
	quitBtn := widget.NewButtonWithIcon(i18n.T("close_btn_quit"), theme.CancelIcon(), nil)

	content := container.NewVBox(
		widget.NewLabel(i18n.T("close_message")),
		widget.NewSeparator(),
		container.NewHBox(layout.NewSpacer(), bgBtn, quitBtn),
	)

	var dlg *dialog.CustomDialog
	dlg = dialog.NewCustomWithoutButtons(i18n.T("close_title"), content, a.window)

	bgBtn.OnTapped = func() {
		dlg.Hide()
		a.minimizeToBackground()
	}
	quitBtn.OnTapped = func() {
		dlg.Hide()
		a.Quit()
	}

	dlg.Show()
}

// ShowAndFocus shows the main window, requests focus, and sends a native notification as fallback.
func (a *App) ShowAndFocus() {
	log.Println("[App] ShowAndFocus called - triggering main thread wake up")
	fyne.Do(func() {
		log.Println("[App] ShowAndFocus running inside main GUI thread (fyne.Do)")
		a.window.Show()
		a.window.RequestFocus()
		log.Println("[App] window.Show() and RequestFocus() executed")

		a.sendOSNotification(i18n.T("title_app"), i18n.T("msg_already_running"))
		log.Println("[App] Fallback notification sent")
	})
}

func (a *App) Run() error {
	// Start application initialization in the background
	startTime := time.Now()
	go func() {
		cfg, cfgPath, err := config.LoadOrCreate()
		if err != nil {
			fyne.Do(func() {
				dialog.ShowError(err, a.window)
				time.AfterFunc(3*time.Second, func() {
					a.Quit()
				})
			})
			return
		}

		logger, err := logging.New()
		if err != nil {
			fyne.Do(func() {
				dialog.ShowError(err, a.window)
				time.AfterFunc(3*time.Second, func() {
					a.Quit()
				})
			})
			return
		}

		resolvedFFmpeg, err := stream.ResolveFFmpegPath(cfg.FFmpegPath)
		if err != nil {
			logger.Printf("[init] FATAL: ffmpeg path resolution failed: %v", err)
			fyne.Do(func() {
				a.window.Show()
				if a.splashWindow != nil {
					a.splashWindow.Close()
				}

				inst := getFFmpegInstallInstructions()

				msgLabel := widget.NewLabel(inst.message)
				msgLabel.Wrapping = fyne.TextWrapWord

				var content *fyne.Container
				if inst.command != "" {
					cmdEntry := widget.NewEntry()
					cmdEntry.SetText(inst.command)

					copyBtn := widget.NewButtonWithIcon("Kopyala", theme.ContentCopyIcon(), func() {
						a.window.Clipboard().SetContent(inst.command)
					})

					cmdRow := container.NewBorder(nil, nil, nil, copyBtn, cmdEntry)
					content = container.NewVBox(msgLabel, cmdRow)
				} else {
					content = container.NewVBox(msgLabel)
				}

				footer := widget.NewLabel("Kurulumu tamamladıktan sonra uygulamayı tekrar başlatın.")
				footer.TextStyle = fyne.TextStyle{Bold: true}

				finalContent := container.NewVBox(content, widget.NewSeparator(), footer)

				d := dialog.NewCustom("Eksik Bileşen (FFmpeg)", "Kapat", finalContent, a.window)
				d.SetOnClosed(func() {
					a.Quit()
				})
				d.Show()
			})
			return
		}

		multiMgr := stream.NewMultiManager(&cfg, cfgPath, logger)
		postProc := stream.NewPostProcessor(resolvedFFmpeg, logger, cfg.DisableHardwareAccel)
		jq, err := stream.NewJobQueue(cfg.RecordingsDir, postProc, logger)
		if err != nil {
			logger.Printf("Failed to init job queue: %v", err)
		}

		// Initialize i18n
		i18n.Init(cfg.Language)

		a.mu.Lock()
		a.cfg = &cfg
		a.cfgPath = cfgPath
		a.logger = logger
		a.multiManager = multiMgr
		a.postProc = postProc
		a.jobQueue = jq
		a.cameraPanels = make(map[string]*ui.CameraPanel)
		a.cameraOrder = getCameraOrder(cfg.Cameras)
		a.mu.Unlock()

		if jq != nil {
			jq.SetCallbacks(
				func(id string, progress float64, status string, isComposite bool) {
					fyne.Do(func() {
						if a.jobQueueLabel != nil {
							if status == stream.JobStatusProcessing {
								active, total := jq.GetProgressInfo()
								if total > 0 {
									jobName := "Kameralar işleniyor"
									if isComposite {
										jobName = "Genel video ayrıştırılıyor"
									}
									a.jobQueueLabel.Text = fmt.Sprintf("%s: %d / %d (%%%d)", jobName, active, total, int(progress))
									a.jobQueueLabel.Refresh()
								}
							}
						}
					})
				},
				func(id string, status string, isComposite bool) {
					fyne.Do(func() {
						if status == stream.JobStatusCompleted {
							a.sendOSNotification(i18n.T("title_app"), i18n.T("record_processing")+" tamamlandı!")
						} else if status == stream.JobStatusFailed {
							a.sendOSNotification(i18n.T("title_app"), i18n.T("record_processing")+" başarısız!")
						}
						
						if a.jobQueueLabel != nil {
							active, total := jq.GetProgressInfo()
							if total > 0 {
								a.jobQueueLabel.Text = fmt.Sprintf("Arka planda işleniyor: %d / %d", active, total)
							} else {
								a.jobQueueLabel.Text = ""
							}
							a.jobQueueLabel.Refresh()
						}
					})
				},
			)
			jq.Start(context.Background())
			_ = jq.Resume()
		}

		// Perform UI setup on the main thread and show main window
		fyne.Do(func() {
			a.window.SetTitle(i18n.T("title_app"))
			a.window.SetIcon(fyne.NewStaticResource("icon.png", iconData))

			a.setupUI()
			a.setupTray()
			a.setupFrameCallbacks()

			showMainAndCloseSplash := func() {
				// Show main window first, then close splash screen to avoid event loop termination
				a.window.Show()
				if a.splashWindow != nil {
					a.splashWindow.Close()
				}

				// Start camera streams if AutoStart is true
				if a.cfg.AutoStart {
					a.multiManager.StartAll()
				}

				// Show tutorial on first run
				if !a.cfg.TutorialShown {
					// Slight delay so the window renders first
					time.AfterFunc(500*time.Millisecond, func() {
						fyne.Do(func() {
							a.showTutorial()
						})
					})
				}
			}

			// Ensure the splash screen is visible for at least 1 second (1000ms)
			elapsed := time.Since(startTime)
			remaining := 1000*time.Millisecond - elapsed

			if remaining > 0 {
				time.AfterFunc(remaining, func() {
					fyne.Do(showMainAndCloseSplash)
				})
			} else {
				showMainAndCloseSplash()
			}
		})
	}()

	a.fyneApp.Run()
	return nil
}

func (a *App) sendOSNotification(title, message string) {
	appName := i18n.T("title_app")
	if appName == "" {
		appName = "NystaVision"
	}
	fullTitle := appName
	if title != "" && title != appName {
		fullTitle = appName + " - " + title
	}

	if runtime.GOOS == "linux" {
		if err := sendLinuxDBusNotification(fullTitle, message, 4000); err == nil {
			return
		}
	}

	a.fyneApp.SendNotification(fyne.NewNotification(fullTitle, message))
}

func sendLinuxDBusNotification(title, message string, timeoutMs int32) error {
	conn, err := dbus.SessionBus()
	if err != nil {
		return err
	}
	obj := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	call := obj.Call("org.freedesktop.Notifications.Notify", 0,
		"NystaVision",              // app_name
		uint32(0),                  // replaces_id
		"nystavision",              // app_icon
		title,                      // summary
		message,                    // body
		[]string{},                 // actions
		map[string]dbus.Variant{},  // hints
		timeoutMs,                  // expire_timeout in ms
	)
	return call.Err
}

func (a *App) Quit() {
	a.saveWindowLayoutToConfig() // Save window size and split position offsets to config on exit

	a.mu.Lock()
	a.isRecording = false
	a.mu.Unlock()

	if a.jobQueue != nil {
		a.jobQueue.Stop()
		
		// Run cleanup for completed jobs
		if jobs, err := a.jobQueue.GetCompletedJobs(); err == nil {
			for _, job := range jobs {
				// Verify if outputs exist for the completed jobs
				patientInfo, err := stream.LoadPatientInfo(job.PatientDir)
				if err == nil && len(patientInfo.Videos) > 0 {
					allValid := true
					for _, vid := range patientInfo.Videos {
						vidPath := filepath.Join(job.PatientDir, vid.FileName)
						if err := a.postProc.VerifyOutput(vidPath); err != nil {
							allValid = false
							break
						}
					}
					// If all processed videos are valid, delete the raw directory safely
					if allValid {
						rawDir := filepath.Join(job.PatientDir, "raw")
						if _, err := os.Stat(rawDir); err == nil {
							_ = os.RemoveAll(rawDir)
						}
						
						// Silme ve Yer Değiştirme Mantığı: Arka plan işi bitince Genel_Onizleme.mp4 dosyasını temizle
						files, _ := filepath.Glob(filepath.Join(job.PatientDir, "Genel_Onizleme_*.mp4"))
						for _, f := range files {
							_ = os.Remove(f)
						}
						
						// Remove Genel_Onizleme from PatientInfo.Videos if it exists
						if pInfo, err := stream.LoadPatientInfo(job.PatientDir); err == nil {
							var updatedVideos []stream.VideoFile
							for _, v := range pInfo.Videos {
								if !strings.HasPrefix(v.FileName, "Genel_Onizleme_") {
									updatedVideos = append(updatedVideos, v)
								}
							}
							pInfo.Videos = updatedVideos
							_ = stream.SavePatientInfo(job.PatientDir, pInfo)
						}

						a.jobQueue.DeleteJob(job.ID)
					}
				}
			}
		}
	}

	if a.multiManager != nil {
		a.multiManager.Close()
	}
	if a.logger != nil {
		_ = a.logger.Close()
	}
	
	fyne.Do(func() {
		// Bypass a.fyneApp.Quit() due to fyne/systray dbus panic on Linux
		// We have already closed multiManager and logger, so it's safe to exit.
		os.Exit(0)
	})
}

func (a *App) statusLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		for camID, panel := range a.cameraPanels {
			state := a.multiManager.GetState(camID)
			running := state.Running
			if camID == a.cfg.RTSPServerCamera && a.rtspUIStopped {
				running = false
			}
			panel.SetStatus(running, state.LastError)

			// Check for USB bandwidth error (exit status 228, No space left on device, or ENOSPC)
			if state.LastError != "" && (strings.Contains(state.LastError, "exit status 228") ||
				strings.Contains(strings.ToLower(state.LastError), "no space left on device") ||
				strings.Contains(strings.ToLower(state.LastError), "enospc")) {
				a.mu.Lock()
				shown := a.shownUSBError[camID]
				if !shown {
					a.shownUSBError[camID] = true
					a.mu.Unlock()

					// Get camera name
					camName := camID
					for _, c := range a.cfg.Cameras {
						if c.ID == camID {
							camName = c.Name
							break
						}
					}

					// Show detailed warning popup
					fyne.Do(func() {
						a.showUSBBandwidthErrorDialog(camName)
					})
				} else {
					a.mu.Unlock()
				}
			} else if state.LastError == "" || state.Running {
				// Reset error shown flag if camera starts working or error is cleared
				a.mu.Lock()
				delete(a.shownUSBError, camID)
				a.mu.Unlock()
			}
		}

		runningWebcams := 0
		runningTotal := 0
		total := len(a.cfg.Cameras)
		for _, cam := range a.cfg.Cameras {
			state := a.multiManager.GetState(cam.ID)
			running := state.Running
			if cam.ID == a.cfg.RTSPServerCamera && a.rtspUIStopped {
				running = false
			}
			if running {
				runningTotal++
				if cam.Type == "webcam" {
					runningWebcams++
				}
			}
		}

		a.mu.Lock()
		recording := a.isRecording
		a.mu.Unlock()

		anyWebcamRunning := runningWebcams > 0
		a.updateStartStopAllBtn(anyWebcamRunning)

		fyne.Do(func() {
			if recording {
				a.statusLabel.SetText(fmt.Sprintf(i18n.T("lbl_status_rec"), runningTotal, total))
			} else {
				a.statusLabel.SetText(fmt.Sprintf(i18n.T("lbl_status"), runningTotal, total))
			}

			// Update status bar
			if a.statusBarLabel != nil {
				a.statusBarLabel.Text = fmt.Sprintf(i18n.T("lbl_status_bar"), runningTotal, total)
				a.statusBarLabel.Refresh()
			}
		})
	}
}

func (a *App) showUSBBandwidthErrorDialog(cameraName string) {
	msg := fmt.Sprintf(i18n.T("usb_bandwidth_error_msg"), cameraName)
	dialog.ShowInformation(i18n.T("usb_bandwidth_error_title"), msg, a.window)
}

func openPath(target string) {
	cleaned := filepath.Clean(target)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", cleaned)
	case "linux":
		cmd = exec.Command("xdg-open", cleaned)
	case "darwin":
		cmd = exec.Command("open", cleaned)
	default:
		return
	}
	_ = cmd.Start()
}

// Ensure unused imports are satisfied
var _ = sort.Strings

// --- Context Menu ---

func (a *App) showCameraContextMenu(cameraID string, ev *fyne.PointEvent) {
	menu := fyne.NewMenu("",
		fyne.NewMenuItem(i18n.T("menu_edit"), func() {
			a.showEditCameraDialog(cameraID)
		}),
		fyne.NewMenuItem(i18n.T("menu_record_single"), func() {
			a.toggleRecording() // For now, starts/stops composite recording
		}),
	)

	widget.ShowPopUpMenuAtPosition(menu, a.window.Canvas(), ev.AbsolutePosition)
}

func restartApp() {
	var exe string
	var err error

	if appImage := os.Getenv("APPIMAGE"); appImage != "" {
		exe = appImage
	} else {
		exe, err = os.Executable()
		if err != nil {
			fmt.Printf("Failed to get executable for restart: %v\n", err)
			os.Exit(0)
		}
	}

	cmd := exec.Command(exe)
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		fmt.Printf("Failed to restart app: %v\n", err)
	}
	os.Exit(0)
}

type installInstruction struct {
	message string
	command string
}

func getFFmpegInstallInstructions() installInstruction {
	if runtime.GOOS == "windows" {
		return installInstruction{
			message: "Lütfen uygulamanın tam yüklendiğinden emin olun veya sisteminize FFmpeg yükleyip yolunu ayarlarda belirtin.",
			command: "",
		}
	}
	if runtime.GOOS == "darwin" {
		return installInstruction{
			message: "macOS için FFmpeg yüklemek üzere terminalde şu komutu çalıştırabilirsiniz:",
			command: "brew install ffmpeg",
		}
	}

	distro := "unknown"
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		content := strings.ToLower(string(data))
		if strings.Contains(content, "ubuntu") || strings.Contains(content, "debian") || strings.Contains(content, "mint") || strings.Contains(content, "pop") || strings.Contains(content, "kali") {
			distro = "apt"
		} else if strings.Contains(content, "fedora") || strings.Contains(content, "centos") || strings.Contains(content, "rhel") || strings.Contains(content, "rocky") || strings.Contains(content, "alma") {
			distro = "dnf"
		} else if strings.Contains(content, "arch") || strings.Contains(content, "manjaro") || strings.Contains(content, "endeavour") || strings.Contains(content, "garuda") {
			distro = "pacman"
		} else if strings.Contains(content, "suse") || strings.Contains(content, "opensuse") {
			distro = "zypper"
		}
	}

	switch distro {
	case "apt":
		return installInstruction{
			message: "Kullandığınız Linux dağıtımı (Ubuntu/Debian/Mint vb.) için FFmpeg yüklü değil.\nLütfen terminali açıp şu komutu çalıştırarak yükleyin:",
			command: "sudo apt update && sudo apt install ffmpeg",
		}
	case "dnf":
		return installInstruction{
			message: "Kullandığınız Linux dağıtımı (Fedora/RHEL vb.) için FFmpeg yüklü değil.\nLütfen terminali açıp şu komutu çalıştırarak yükleyin:",
			command: "sudo dnf install ffmpeg",
		}
	case "pacman":
		return installInstruction{
			message: "Kullandığınız Linux dağıtımı (Arch/Manjaro vb.) için FFmpeg yüklü değil.\nLütfen terminali açıp şu komutu çalıştırarak yükleyin:",
			command: "sudo pacman -S ffmpeg",
		}
	case "zypper":
		return installInstruction{
			message: "Kullandığınız Linux dağıtımı (openSUSE vb.) için FFmpeg yüklü değil.\nLütfen terminali açıp şu komutu çalıştırarak yükleyin:",
			command: "sudo zypper install ffmpeg",
		}
	default:
		return installInstruction{
			message: "Sisteminizde FFmpeg bulunamadı.\nLütfen dağıtımınızın paket yöneticisini kullanarak 'ffmpeg' paketini yükleyin.",
			command: "",
		}
	}
}

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

// Types and logic for layout grid silhouette preview rendering
type miniNode interface{}

type miniLeaf struct {
	index int
}

type miniSplit struct {
	horizontal bool
	offset     float64
	leading    miniNode
	trailing   miniNode
}

func buildHSplitTree(items []miniNode) miniNode {
	if len(items) == 0 {
		return &miniLeaf{index: -1}
	}
	if len(items) == 1 {
		return items[0]
	}
	left := items[0]
	right := buildHSplitTree(items[1:])
	return &miniSplit{
		horizontal: true,
		offset:     1.0 / float64(len(items)),
		leading:    left,
		trailing:   right,
	}
}

func buildVSplitTree(rows []miniNode) miniNode {
	if len(rows) == 0 {
		return &miniLeaf{index: -1}
	}
	if len(rows) == 1 {
		return rows[0]
	}
	top := rows[0]
	bottom := buildVSplitTree(rows[1:])
	return &miniSplit{
		horizontal: false,
		offset:     1.0 / float64(len(rows)),
		leading:    top,
		trailing:   bottom,
	}
}

func buildGridTree(cols, rows int, totalCells int) miniNode {
	var rowNodes []miniNode
	cellIdx := 0
	for r := 0; r < rows; r++ {
		var colNodes []miniNode
		for c := 0; c < cols; c++ {
			if cellIdx < totalCells {
				colNodes = append(colNodes, &miniLeaf{index: cellIdx})
			} else {
				colNodes = append(colNodes, &miniLeaf{index: -1})
			}
			cellIdx++
		}
		rowNodes = append(rowNodes, buildHSplitTree(colNodes))
	}
	return buildVSplitTree(rowNodes)
}

func assignOffsets(node miniNode, offsets []float64, idx *int) {
	if node == nil || *idx >= len(offsets) {
		return
	}
	if split, ok := node.(*miniSplit); ok {
		split.offset = offsets[*idx]
		*idx++
		assignOffsets(split.leading, offsets, idx)
		assignOffsets(split.trailing, offsets, idx)
	}
}

func renderTree(node miniNode, x, y, w, h float32, list []fyne.CanvasObject, cameras []config.CameraSource) []fyne.CanvasObject {
	if node == nil {
		return list
	}

	if leaf, ok := node.(*miniLeaf); ok {
		var cellColor color.Color
		if leaf.index >= 0 && leaf.index < len(cameras) {
			cam := cameras[leaf.index]
			if cam.Enabled {
				cellColor = color.NRGBA{R: 46, G: 134, B: 193, A: 160} // Semi-transparent Medical Blue
			} else {
				cellColor = color.NRGBA{R: 70, G: 80, B: 90, A: 255} // Dark Gray-Blue
			}
		} else {
			cellColor = color.NRGBA{R: 40, G: 45, B: 50, A: 100} // Empty slot
		}

		rect := canvas.NewRectangle(cellColor)
		rect.CornerRadius = 1

		rect.Move(fyne.NewPos(x, y))
		rect.Resize(fyne.NewSize(w, h))

		list = append(list, rect)
		return list
	}

	if split, ok := node.(*miniSplit); ok {
		gap := float32(1.0) // thin separator gap
		if split.horizontal {
			wl := w * float32(split.offset)
			wr := w - wl

			list = renderTree(split.leading, x, y, wl-gap/2, h, list, cameras)
			list = renderTree(split.trailing, x+wl+gap/2, y, wr-gap/2, h, list, cameras)

			// Separator line
			sep := canvas.NewRectangle(color.NRGBA{R: 189, G: 195, B: 199, A: 180})
			sep.Move(fyne.NewPos(x+wl-0.5, y))
			sep.Resize(fyne.NewSize(1, h))
			list = append(list, sep)
		} else {
			ht := h * float32(split.offset)
			hb := h - ht

			list = renderTree(split.leading, x, y, w, ht-gap/2, list, cameras)
			list = renderTree(split.trailing, x, y+ht+gap/2, w, hb-gap/2, list, cameras)

			// Separator line
			sep := canvas.NewRectangle(color.NRGBA{R: 189, G: 195, B: 199, A: 180})
			sep.Move(fyne.NewPos(x, y+ht-0.5))
			sep.Resize(fyne.NewSize(w, 1))
			list = append(list, sep)
		}
	}

	return list
}


