package tray

import (
	"context"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

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
	isRecording    bool
	recordTempFile string
	recordCellW    int
	recordCellH    int
	recordCols     int
	recordRows     int

	// Disk space live monitoring
	diskBanner      *fyne.Container
	diskBannerBg    *canvas.Rectangle
	diskBannerText  *canvas.Text
	diskMonitorStop chan struct{}

	shownUSBError map[string]bool
}

func New() (*App, error) {
	a := app.NewWithID("com.syg.nystavision")
	w := a.NewWindow("NystaVision") // Placeholder title, updated dynamically on load

	appObj := &App{
		fyneApp:       a,
		window:        w,
		shownUSBError: make(map[string]bool),
	}

	// Create and show splash screen immediately
	var splash fyne.Window
	if drv, ok := a.Driver().(desktop.Driver); ok {
		splash = drv.CreateSplashWindow()

		// Premium white background matching the logo's white background
		bg := canvas.NewRectangle(color.RGBA{R: 255, G: 255, B: 255, A: 255})

		// Logo image
		logoRes := fyne.NewStaticResource("logo.png", logoData)
		logoImg := canvas.NewImageFromResource(logoRes)
		logoImg.FillMode = canvas.ImageFillContain
		logoImg.SetMinSize(fyne.NewSize(280, 111))

		// Title and Subtitle
		title := canvas.NewText("NystaVision", color.RGBA{R: 15, G: 23, B: 42, A: 255}) // Slate 900
		title.TextSize = 26
		title.TextStyle = fyne.TextStyle{Bold: true}
		title.Alignment = fyne.TextAlignCenter

		subtitle := canvas.NewText("Virtual Camera Agent", color.RGBA{R: 100, G: 116, B: 139, A: 255}) // Slate 500
		subtitle.TextSize = 12
		subtitle.Alignment = fyne.TextAlignCenter

		// Infinite progress spinner for modern loading look
		progress := widget.NewProgressBarInfinite()

		// Transparent padding rectangles to constrain progress bar width elegantly
		progressPadding := canvas.NewRectangle(color.Transparent)
		progressPadding.SetMinSize(fyne.NewSize(60, 0))
		progressWrapper := container.NewBorder(nil, nil, progressPadding, progressPadding, progress)

		// Vertical Box layout for elements
		contentVBox := container.NewVBox(
			layout.NewSpacer(),
			container.NewCenter(logoImg),
			layout.NewSpacer(),
			title,
			subtitle,
			layout.NewSpacer(),
			progressWrapper,
			layout.NewSpacer(),
		)

		splashContent := container.NewStack(
			bg,
			container.NewPadded(contentVBox),
		)

		splash.SetContent(splashContent)
		splash.Resize(fyne.NewSize(420, 320))
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
	a.window.Resize(fyne.NewSize(960, 640))
	a.window.SetCloseIntercept(a.handleClose)

	// Status label
	a.statusLabel = widget.NewLabelWithStyle(i18n.T("title_app"), fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

	// Build toolbar
	toolbar := a.buildToolbar()

	// Build camera grid
	a.buildCameraGrid()
	a.refreshCameraDropdowns()

	// Main layout: toolbar on top, grid fills the rest
	content := container.NewBorder(
		container.NewVBox(toolbar, widget.NewSeparator()),
		nil, nil, nil,
		a.gridContainer,
	)

	a.window.SetContent(content)

	// Start status update loop
	go a.statusLoop()
}

func (a *App) buildToolbar() *fyne.Container {
	// Add camera button
	a.addBtn = widget.NewButtonWithIcon("", theme.ContentAddIcon(), func() {
		a.showAddCameraDialog()
	})

	// Remove camera button
	removeBtn := widget.NewButtonWithIcon("", theme.ContentRemoveIcon(), func() {
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

	// Save layout button
	saveLayoutBtn := widget.NewButtonWithIcon("", theme.DocumentSaveIcon(), func() {
		a.showSaveLayoutDialog()
	})

	// Load layout button
	loadLayoutBtn := widget.NewButtonWithIcon("", theme.FolderOpenIcon(), func() {
		a.showLoadLayoutDialog()
	})

	// Help/Tutorial button
	helpBtn := widget.NewButtonWithIcon("", theme.QuestionIcon(), func() {
		a.showTutorial()
	})

	// Layout: [+][-] | [start/stop] | [record] | [settings][save][load] [?]
	leftGroup := container.NewHBox(a.addBtn, removeBtn)
	middleGroup := container.NewHBox(a.startStopAllBtn, widget.NewSeparator(), a.recordBtn)
	rightGroup := container.NewHBox(widget.NewSeparator(), settingsBtn, saveLayoutBtn, loadLayoutBtn, widget.NewSeparator(), helpBtn)

	return container.NewHBox(leftGroup, middleGroup, rightGroup)
}

func (a *App) toggleAllStreams() {
	anyWebcamRunning := false
	for _, cam := range a.cfg.Cameras {
		if cam.Enabled && cam.Type == "webcam" {
			if a.multiManager.GetState(cam.ID).Running {
				anyWebcamRunning = true
				break
			}
		}
	}

	if anyWebcamRunning {
		a.rtspUIStopped = true
		a.multiManager.StopAll()
		a.updateStartStopAllBtn(false)
	} else {
		a.rtspUIStopped = false
		a.multiManager.StartAll()
		a.updateStartStopAllBtn(true)
	}
}

func (a *App) updateStartStopAllBtn(running bool) {
	fyne.Do(func() {
		if running {
			a.startStopAllBtn.SetText(i18n.T("btn_stop_all"))
			a.startStopAllBtn.Icon = theme.MediaStopIcon()
			a.startStopAllBtn.Importance = widget.WarningImportance
		} else {
			a.startStopAllBtn.SetText(i18n.T("btn_start_all"))
			a.startStopAllBtn.Icon = theme.MediaPlayIcon()
			a.startStopAllBtn.Importance = widget.SuccessImportance
		}
		a.startStopAllBtn.Refresh()
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
			cam.Device = dev

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

	progress := dialog.NewCustom(i18n.T("msg_cameras_searching"), i18n.T("msg_please_wait"), widget.NewProgressBarInfinite(), a.window)
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

			if selectedCam.Type == "rtsp" {
				urlEntry.Show()
				webcamSelect.Hide()
			} else {
				urlEntry.Hide()
				webcamSelect.Show()
			}

			typeSelect.OnChanged = func(s string) {
				if s == "rtsp" {
					urlEntry.Show()
					webcamSelect.Hide()
				} else {
					urlEntry.Hide()
					webcamSelect.Show()
				}
			}

			form := dialog.NewForm(fmt.Sprintf("%s - %s", i18n.T("menu_edit"), selectedCam.Name), i18n.T("btn_save"), i18n.T("btn_cancel"), []*widget.FormItem{
				widget.NewFormItem(i18n.T("lbl_camera_name"), nameEntry),
				widget.NewFormItem(i18n.T("lbl_type"), typeSelect),
				widget.NewFormItem(i18n.T("lbl_ip_url"), urlEntry),
				widget.NewFormItem(i18n.T("lbl_webcam"), webcamSelect),
			}, func(ok bool) {
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
				}
				_ = config.Save(*a.cfg, a.cfgPath)
				a.multiManager.UpdateCamera(*camPtr)
				if panel, exists := a.cameraPanels[cameraID]; exists {
					panel.SetName(camPtr.Name)
				}
			}, a.window)
			form.Resize(fyne.NewSize(450, 400))
			form.Show()
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
	if len(a.cfg.Cameras) >= config.MaxCameras {
		dialog.ShowInformation(i18n.T("err_limit"), fmt.Sprintf(i18n.T("err_camera_limit"), config.MaxCameras), a.window)
		return
	}

	progress := dialog.NewCustom(i18n.T("msg_cameras_searching"), i18n.T("msg_please_wait"), widget.NewProgressBarInfinite(), a.window)
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

			d := dialog.NewForm(i18n.T("title_add_camera"), i18n.T("btn_add_camera"), i18n.T("btn_cancel"), formItems, func(ok bool) {
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

			d.Resize(fyne.NewSize(450, 300))
			d.Show()
		})
	}()
}

func (a *App) removeSelectedCamera() {
	if len(a.cfg.Cameras) <= config.MinCameras {
		dialog.ShowInformation(i18n.T("err_limit"), i18n.T("msg_min_cameras", config.MinCameras), a.window)
		return
	}

	if a.selectedCamera == "" {
		dialog.ShowInformation(i18n.T("err_selection"), i18n.T("msg_select_to_delete"), a.window)
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

// --- Recording (Composite → Crop) ---

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

	cols, rows := ui.CalculateGrid(len(a.cfg.Cameras))
	cellW, cellH := 1280, 720
	fps := 15

	maxW, maxH, maxFPS := 0, 0, 0
	for _, cam := range a.cfg.Cameras {
		if cam.Enabled {
			mgr := a.multiManager.GetManager(cam.ID)
			if mgr != nil {
				w, h := mgr.ActiveResolution()
				f := mgr.ActiveFPS()
				if w > maxW {
					maxW = w
				}
				if h > maxH {
					maxH = h
				}
				if f > maxFPS {
					maxFPS = f
				}
			}
		}
	}
	if maxW > 0 {
		cellW = maxW
	}
	if maxH > 0 {
		cellH = maxH
	}
	if maxFPS > 0 {
		fps = maxFPS
	}

	gridW := cols * cellW
	gridH := rows * cellH

	a.recordCols = cols
	a.recordRows = rows
	a.recordCellW = cellW
	a.recordCellH = cellH
	a.mu.Unlock()

	numCameras := 0
	for _, cam := range a.cfg.Cameras {
		if cam.Enabled {
			numCameras++
		}
	}

	freeBytes, err := stream.DiskFreeBytes(a.cfg.RecordingsDir)
	if err != nil {
		a.logger.Printf("[recording] check disk space failed: %v", err)
		dialog.ShowError(fmt.Errorf(i18n.T("disk_check_error"), err), a.window)
		// Proceed anyway so a system error doesn't completely block recording
		a.proceedWithRecording(gridW, gridH, fps, cols, rows, numCameras)
		return
	}

	availableMins := stream.EstimateAvailableMinutes(freeBytes, gridW, gridH, fps, numCameras)
	a.logger.Printf("[recording] disk space check: freeBytes=%d, estimated_mins=%.2f", freeBytes, availableMins)

	if availableMins < 1.0 {
		a.showDiskSpaceBlocked(func() {
			a.changeRecordingsDirAndRetry()
		})
		return
	} else if availableMins < 15.0 {
		a.showDiskSpaceWarning(availableMins, func() {
			a.proceedWithRecording(gridW, gridH, fps, cols, rows, numCameras)
		}, func() {
			a.changeRecordingsDirAndRetry()
		})
		return
	}

	a.proceedWithRecording(gridW, gridH, fps, cols, rows, numCameras)
}

func (a *App) proceedWithRecording(gridW, gridH, fps, cols, rows, numCameras int) {
	// Find/resolve recorder
	var ffmpegPath string
	var err error
	if a.cfg != nil {
		ffmpegPath, err = stream.ResolveFFmpegPath(a.cfg.FFmpegPath)
	} else {
		ffmpegPath, err = stream.ResolveFFmpegPath("")
	}
	if err != nil {
		dialog.ShowError(fmt.Errorf("FFmpeg bulunamadı! Kayıt başlatılamıyor. Lütfen ayarlardan FFmpeg yolunu kontrol edin.\n\nHata: %v", err), a.window)
		return
	}
	rec := stream.NewRecorder(ffmpegPath, a.cfg.RecordingsDir, a.logger)
	if err := rec.Start(gridW, gridH, fps); err != nil {
		dialog.ShowError(err, a.window)
		return
	}

	a.mu.Lock()
	a.isRecording = true
	a.mu.Unlock()

	a.recordStart = time.Now()
	fyne.Do(func() {
		a.recordBtn.SetText(i18n.T("btn_stop"))
		a.recordBtn.Icon = theme.MediaStopIcon()
		a.recordBtn.Importance = widget.DangerImportance
		a.recordBtn.Refresh()
	})

	// Start timer display
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
				a.recordBtn.SetText(i18n.T("btn_stop_with_time", elapsed))
				a.recordBtn.Refresh()
			})
		}
	}()

	// Start composite record loop
	go a.compositeRecordLoop(rec, cols, rows, gridW, gridH, fps)

	// Start live disk monitoring
	a.startDiskMonitor(gridW, gridH, fps, numCameras)
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

func (a *App) startDiskMonitor(gridW, gridH, fps, numCameras int) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.initDiskBanner()

	a.diskMonitorStop = make(chan struct{})
	stopChan := a.diskMonitorStop

	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
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
				mins := stream.EstimateAvailableMinutes(freeBytes, gridW, gridH, fps, numCameras)
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

					if mins <= 0 {
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

	a.diskBannerBg = canvas.NewRectangle(color.RGBA{R: 230, G: 126, B: 34, A: 220})
	a.diskBannerBg.CornerRadius = 4
	a.diskBannerText = canvas.NewText("", color.White)
	a.diskBannerText.TextStyle = fyne.TextStyle{Bold: true}
	a.diskBannerText.Alignment = fyne.TextAlignCenter
	a.diskBannerText.TextSize = 14

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
		a.diskBannerBg.FillColor = color.RGBA{R: 231, G: 76, B: 60, A: 220} // Red
		a.diskBannerText.Text = fmt.Sprintf(i18n.T("disk_live_critical"), minutes)
	} else {
		a.diskBannerBg.FillColor = color.RGBA{R: 230, G: 126, B: 34, A: 220} // Orange
		a.diskBannerText.Text = fmt.Sprintf(i18n.T("disk_live_warning"), minutes)
	}
	a.diskBannerBg.Refresh()
	a.diskBannerText.Refresh()
}

func (a *App) compositeRecordLoop(rec *stream.Recorder, cols, rows, gridW, gridH, fps int) {
	ticker := time.NewTicker(time.Second / time.Duration(fps))
	defer ticker.Stop()

	for range ticker.C {
		a.mu.Lock()
		recording := a.isRecording
		a.mu.Unlock()

		if !recording {
			// Stop the recorder and save the temp file path
			tempFile, err := rec.Stop()
			if err != nil {
				a.logger.Printf("[recording] stop error: %v", err)
				return
			}
			a.mu.Lock()
			a.recordTempFile = tempFile
			a.mu.Unlock()
			return
		}

		frames := make(map[string]*image.RGBA)
		for id, panel := range a.cameraPanels {
			if frame := panel.GetLastFrame(); frame != nil {
				frames[id] = frame
			}
		}

		composite := stream.ComposeGridFrame(frames, a.cameraOrder, cols, rows, gridW, gridH)
		rec.WriteFrame(composite)
	}
}

func (a *App) stopRecording() {
	a.mu.Lock()
	if !a.isRecording {
		a.mu.Unlock()
		return
	}
	a.isRecording = false
	a.mu.Unlock()

	a.stopDiskMonitor()

	if a.recordTimer != nil {
		a.recordTimer.Stop()
		a.recordTimer = nil
	}

	fyne.Do(func() {
		a.recordBtn.SetText(i18n.T("btn_record"))
		a.recordBtn.Icon = theme.MediaRecordIcon()
		a.recordBtn.Importance = widget.MediumImportance
		a.recordBtn.Refresh()
	})

	// Wait briefly for compositeRecordLoop to stop and set recordTempFile
	go func() {
		// Give the record loop time to write the final file
		time.Sleep(500 * time.Millisecond)

		a.mu.Lock()
		tempFile := a.recordTempFile
		cols := a.recordCols
		rows := a.recordRows
		cellW := a.recordCellW
		cellH := a.recordCellH
		recStart := a.recordStart
		cameras := make([]config.CameraSource, len(a.cfg.Cameras))
		copy(cameras, a.cfg.Cameras)
		cameraOrder := make([]string, len(a.cameraOrder))
		copy(cameraOrder, a.cameraOrder)
		a.mu.Unlock()

		if tempFile == "" {
			a.logger.Printf("[recording] temp file not available yet, waiting...")
			time.Sleep(2 * time.Second)
			a.mu.Lock()
			tempFile = a.recordTempFile
			a.mu.Unlock()
		}

		if tempFile == "" {
			fyne.Do(func() {
				dialog.ShowError(fmt.Errorf("recording file not available"), a.window)
			})
			return
		}

		// Build segments for each camera
		segments := buildCameraSegments(cameras, cameraOrder, cols, rows, cellW, cellH)

		// Show patient name dialog
		fyne.Do(func() {
			a.showPatientNameDialog(tempFile, segments, recStart)
		})
	}()
}

// buildCameraSegments calculates crop coordinates for each camera in the grid.
func buildCameraSegments(cameras []config.CameraSource, order []string, cols, rows, cellW, cellH int) []stream.CameraSegment {
	// Build name lookup
	nameMap := make(map[string]string)
	for _, cam := range cameras {
		nameMap[cam.ID] = cam.Name
	}

	var segments []stream.CameraSegment
	for idx, camID := range order {
		if idx >= cols*rows {
			break
		}
		col := idx % cols
		row := idx / cols
		segments = append(segments, stream.CameraSegment{
			ID:   camID,
			Name: nameMap[camID],
			X:    col * cellW,
			Y:    row * cellH,
			W:    cellW,
			H:    cellH,
		})
	}
	return segments
}

func (a *App) showPatientNameDialog(tempFile string, segments []stream.CameraSegment, recStart time.Time) {
	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder(i18n.T("record_patient_placeholder"))

	dlg := dialog.NewForm(
		i18n.T("record_patient_title"),
		i18n.T("record_patient_btn"),
		i18n.T("btn_cancel"),
		[]*widget.FormItem{
			widget.NewFormItem(i18n.T("record_patient_label"), nameEntry),
		},
		func(ok bool) {
			if !ok {
				// User cancelled — clean up temp file
				_ = os.Remove(tempFile)
				return
			}
			patientName := strings.TrimSpace(nameEntry.Text)
			outDir := stream.GetOutputDir(a.cfg.RecordingsDir, patientName)

			if err := os.MkdirAll(outDir, 0o755); err != nil {
				dialog.ShowError(fmt.Errorf("failed to create output directory: %w", err), a.window)
				_ = os.Remove(tempFile)
				return
			}

			// Show progress dialog while processing
			progressBar := widget.NewProgressBarInfinite()
			progressLabel := widget.NewLabel(i18n.T("record_processing"))
			progressContent := container.NewVBox(progressLabel, progressBar)
			progressDlg := dialog.NewCustomWithoutButtons(
				i18n.T("record_processing"),
				progressContent,
				a.window,
			)
			progressDlg.Show()

			go func() {
				ctx := context.Background()
				progressCh := make(chan float64, 10)

				var result stream.ProcessResult
				done := make(chan struct{})

				go func() {
					result = a.postProc.Process(ctx, tempFile, segments, outDir, recStart, progressCh)
					close(done)
				}()

				// Drain progress channel
				go func() {
					for range progressCh {
					}
				}()

				<-done

				// Clean up temp file only if no error occurred
				if result.Err == nil {
					_ = os.Remove(tempFile)
				}

				fyne.Do(func() {
					progressDlg.Hide()

					if result.Err != nil {
						detailedErr := fmt.Errorf("%v\n\nRaw recording file was NOT deleted and is saved at:\n%s", result.Err, tempFile)
						dialog.ShowError(detailedErr, a.window)
						return
					}

					// Show success dialog with Open Folder option
					msg := i18n.T("record_done_msg", result.OutputDir)
					openBtn := widget.NewButtonWithIcon(i18n.T("record_done_open"), theme.FolderOpenIcon(), func() {
						openPath(result.OutputDir)
					})
					openBtn.Importance = widget.HighImportance
					closeBtn := widget.NewButton(i18n.T("record_done_close"), nil)

					content := container.NewVBox(
						widget.NewLabel(msg),
						container.NewHBox(layout.NewSpacer(), openBtn, closeBtn),
					)

					successDlg := dialog.NewCustomWithoutButtons(
						i18n.T("record_done_title"),
						content,
						a.window,
					)
					closeBtn.OnTapped = func() {
						successDlg.Hide()
					}
					successDlg.Show()
				})
			}()
		},
		a.window,
	)
	dlg.Resize(fyne.NewSize(400, 160))
	dlg.Show()
}

// --- Settings Dialog ---

func (a *App) showSettingsDialog() {
	autostartCheck := widget.NewCheck(i18n.T("lbl_autostart"), nil)
	autostartCheck.SetChecked(a.cfg.AutoStart)

	useMaxCheck := widget.NewCheck(i18n.T("lbl_use_max_supported"), nil)
	useMaxCheck.SetChecked(a.cfg.UseMaxSupported)

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

	settingsContent := container.NewVBox(
		widget.NewLabelWithStyle(i18n.T("title_general_settings"), fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		container.NewBorder(nil, nil, widget.NewLabel(i18n.T("lbl_language")+": "), nil, langSelect),
		recordingsRow,
		autostartCheck,
		useMaxCheck,
		widget.NewSeparator(),
		container.NewGridWithColumns(2, configBtn, logsBtn),
		container.NewHBox(layout.NewSpacer(), versionText),
	)

	d := dialog.NewCustom(i18n.T("title_settings"), i18n.T("btn_save_close"), settingsContent, a.window)
	d.Resize(fyne.NewSize(500, 280))

	d.SetOnClosed(func() {
		a.mu.Lock()
		a.cfg.AutoStart = autostartCheck.Checked
		a.cfg.RecordingsDir = recordingsDirEntry.Text
		useMaxChanged := a.cfg.UseMaxSupported != useMaxCheck.Checked
		a.cfg.UseMaxSupported = useMaxCheck.Checked
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

		if useMaxChanged && a.multiManager != nil {
			// Restart active webcam streams immediately
			for _, cam := range a.cfg.Cameras {
				if cam.Enabled && cam.Type == "webcam" {
					if a.multiManager.GetState(cam.ID).Running {
						a.multiManager.StopCamera(cam.ID)
						_ = a.multiManager.StartCamera(cam.ID)
					}
				}
			}
		}
	})

	d.Show()
}

// --- Layout Save/Load ---

func (a *App) showSaveLayoutDialog() {
	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder(i18n.T("layout_name_placeholder"))

	dialog.ShowForm(i18n.T("layout_save_title"), i18n.T("layout_save_btn"), i18n.T("btn_cancel"), []*widget.FormItem{
		widget.NewFormItem(i18n.T("layout_name_lbl"), nameEntry),
	}, func(ok bool) {
		if !ok || strings.TrimSpace(nameEntry.Text) == "" {
			return
		}

		savedLayout := config.SavedLayout{
			Name:    nameEntry.Text,
			Cameras: make([]config.CameraSource, len(a.cfg.Cameras)),
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

		dialog.ShowInformation(i18n.T("layout_saved_title"), fmt.Sprintf(i18n.T("layout_saved"), nameEntry.Text), a.window)
	}, a.window)
}

func (a *App) showLoadLayoutDialog() {
	if len(a.cfg.SavedLayouts) == 0 {
		dialog.ShowInformation(i18n.T("layout_empty_title"), i18n.T("layout_empty"), a.window)
		return
	}

	layoutNames := make([]string, len(a.cfg.SavedLayouts))
	for i, l := range a.cfg.SavedLayouts {
		layoutNames[i] = fmt.Sprintf(i18n.T("layout_cameras"), l.Name, len(l.Cameras))
	}

	layoutSelect := widget.NewSelect(layoutNames, nil)
	if len(layoutNames) > 0 {
		layoutSelect.SetSelected(layoutNames[0])
	}

	dialog.ShowForm(i18n.T("layout_load_title"), i18n.T("layout_load_btn"), i18n.T("btn_cancel"), []*widget.FormItem{
		widget.NewFormItem(i18n.T("layout_select_lbl"), layoutSelect),
	}, func(ok bool) {
		if !ok {
			return
		}

		idx := -1
		for i, name := range layoutNames {
			if name == layoutSelect.Selected {
				idx = i
				break
			}
		}
		if idx < 0 {
			return
		}

		loadedLayout := a.cfg.SavedLayouts[idx]

		a.multiManager.Close()

		a.mu.Lock()
		a.cfg.Cameras = make([]config.CameraSource, len(loadedLayout.Cameras))
		copy(a.cfg.Cameras, loadedLayout.Cameras)
		a.cfg.ActiveLayoutName = loadedLayout.Name
		_ = config.Save(*a.cfg, a.cfgPath)
		a.mu.Unlock()

		a.multiManager = stream.NewMultiManager(a.cfg, a.cfgPath, a.logger)
		a.rebuildGrid()

		if a.cfg.AutoStart {
			a.multiManager.StartAll()
		}
	}, a.window)
}

// --- Tutorial ---

func (a *App) showTutorial() {
	steps := []ui.TutorialStep{
		{TargetWidget: nil, TitleKey: "tutorial_title_0", DescKey: "tutorial_desc_0"},
		{TargetWidget: a.addBtn, TitleKey: "tutorial_title_1", DescKey: "tutorial_desc_1"},
		{TargetWidget: a.startAllRef, TitleKey: "tutorial_title_2", DescKey: "tutorial_desc_2"},
		{TargetWidget: a.recordBtn, TitleKey: "tutorial_title_3", DescKey: "tutorial_desc_3"},
		{TargetWidget: a.settingsRef, TitleKey: "tutorial_title_4", DescKey: "tutorial_desc_4"},
	}

	ui.ShowTutorial(a.window, steps, func() {
		a.mu.Lock()
		a.cfg.TutorialShown = true
		_ = config.Save(*a.cfg, a.cfgPath)
		a.mu.Unlock()
	})
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

		// Fallback notification for OS focus-stealing prevention
		notification := fyne.NewNotification(
			i18n.T("title_app"),
			i18n.T("msg_already_running"),
		)
		a.fyneApp.SendNotification(notification)
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
				dialog.ShowError(fmt.Errorf("Kritik Hata: FFmpeg bulunamadı. Uygulama çalıştırılamıyor.\nLog dosyalarını kontrol edin veya sisteminize FFmpeg yükleyin.\n\nHata: %v", err), a.window)
				time.AfterFunc(5*time.Second, func() {
					a.Quit()
				})
			})
			return
		}

		multiMgr := stream.NewMultiManager(&cfg, cfgPath, logger)
		postProc := stream.NewPostProcessor(resolvedFFmpeg, logger)

		// Initialize i18n
		i18n.Init(cfg.Language)

		a.mu.Lock()
		a.cfg = &cfg
		a.cfgPath = cfgPath
		a.logger = logger
		a.multiManager = multiMgr
		a.postProc = postProc
		a.cameraPanels = make(map[string]*ui.CameraPanel)
		a.cameraOrder = getCameraOrder(cfg.Cameras)
		a.mu.Unlock()

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
			remaining := 1000 * time.Millisecond - elapsed

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

func (a *App) Quit() {
	a.mu.Lock()
	a.isRecording = false
	a.mu.Unlock()

	if a.multiManager != nil {
		a.multiManager.Close()
	}
	if a.logger != nil {
		_ = a.logger.Close()
	}
	a.fyneApp.Quit()
	os.Exit(0)
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
			panel.SetStatus(running, state.LastError != "")

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
var _ = canvas.NewImageFromImage

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
