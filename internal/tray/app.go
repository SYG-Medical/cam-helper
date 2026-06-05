package tray

import (
	_ "embed"
	"fmt"
	"image"
	"io"
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
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"rtsp-virtual-cam-agent/internal/autostart"
	"rtsp-virtual-cam-agent/internal/config"
	"rtsp-virtual-cam-agent/internal/logging"
	"rtsp-virtual-cam-agent/internal/stream"
	"rtsp-virtual-cam-agent/internal/ui"
)

//go:embed resources/icon.png
var iconData []byte

type App struct {
	fyneApp      fyne.App
	window       fyne.Window
	cfg          *config.Config
	cfgPath      string
	logger       *logging.Logger
	multiManager *stream.MultiManager
	recorder     *stream.Recorder
	mu           sync.Mutex

	// UI elements
	statusLabel     *widget.Label
	startStopAllBtn *widget.Button
	cameraGrid      *fyne.Container
	gridContainer   *fyne.Container
	cameraPanels    map[string]*ui.CameraPanel
	selectedCamera  string
	recordBtn       *widget.Button
	recordTimer     *time.Ticker
	recordStart     time.Time

	// Camera ordering (for consistent display)
	cameraOrder []string

	rtspUIStopped bool
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

	// Auto-detect webcams and merge with config
	detected := config.DetectWebcams()
	cfg = mergeDetectedWebcams(cfg, detected)

	multiMgr := stream.NewMultiManager(&cfg, cfgPath, logger)
	recorder := stream.NewRecorder(logger)

	a := app.NewWithID("com.syg.camera-helper")
	w := a.NewWindow("SYG Camera Helper")
	w.SetIcon(fyne.NewStaticResource("icon.png", iconData))

	appObj := &App{
		fyneApp:      a,
		window:       w,
		cfg:          &cfg,
		cfgPath:      cfgPath,
		logger:       logger,
		multiManager: multiMgr,
		recorder:     recorder,
		cameraPanels: make(map[string]*ui.CameraPanel),
		cameraOrder:  getCameraOrder(cfg.Cameras),
	}

	appObj.setupUI()
	appObj.setupTray()
	appObj.setupFrameCallbacks()

	return appObj, nil
}

// mergeDetectedWebcams adds newly detected webcams to the config if they don't exist yet.
func mergeDetectedWebcams(cfg config.Config, detected []config.CameraSource) config.Config {
	existingDevices := make(map[string]bool)
	for _, c := range cfg.Cameras {
		if c.Type == "webcam" && c.Device != "" {
			existingDevices[c.Device] = true
		}
	}

	for _, d := range detected {
		if !existingDevices[d.Device] && len(cfg.Cameras) < config.MaxCameras {
			d.ID = config.NextCameraID(cfg.Cameras)
			cfg.Cameras = append(cfg.Cameras, d)
		}
	}
	return cfg
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
	a.statusLabel = widget.NewLabelWithStyle("SYG Camera Helper", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

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
	addBtn := widget.NewButtonWithIcon("", theme.ContentAddIcon(), func() {
		a.showAddCameraDialog()
	})

	// Remove camera button
	removeBtn := widget.NewButtonWithIcon("", theme.ContentRemoveIcon(), func() {
		a.removeSelectedCamera()
	})

	// Start/Stop all streams button
	a.startStopAllBtn = widget.NewButtonWithIcon("Tümünü Başlat", theme.MediaPlayIcon(), func() {
		a.toggleAllStreams()
	})
	a.startStopAllBtn.Importance = widget.SuccessImportance

	// Record button
	a.recordBtn = widget.NewButtonWithIcon("Kayıt", theme.MediaRecordIcon(), func() {
		a.toggleRecording()
	})

	// Settings button
	settingsBtn := widget.NewButtonWithIcon("", theme.SettingsIcon(), func() {
		a.showSettingsDialog()
	})

	// Save layout button
	saveLayoutBtn := widget.NewButtonWithIcon("", theme.DocumentSaveIcon(), func() {
		a.showSaveLayoutDialog()
	})

	// Load layout button
	loadLayoutBtn := widget.NewButtonWithIcon("", theme.FolderOpenIcon(), func() {
		a.showLoadLayoutDialog()
	})

	// Layout: [+][-] | [start/stop] | [record] | [settings][save][load]
	leftGroup := container.NewHBox(addBtn, removeBtn)
	middleGroup := container.NewHBox(a.startStopAllBtn, widget.NewSeparator(), a.recordBtn)
	rightGroup := container.NewHBox(widget.NewSeparator(), settingsBtn, saveLayoutBtn, loadLayoutBtn)

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
			a.startStopAllBtn.SetText("Tümünü Durdur")
			a.startStopAllBtn.Icon = theme.MediaStopIcon()
			a.startStopAllBtn.Importance = widget.WarningImportance
		} else {
			a.startStopAllBtn.SetText("Tümünü Başlat")
			a.startStopAllBtn.Icon = theme.MediaPlayIcon()
			a.startStopAllBtn.Importance = widget.SuccessImportance
		}
		a.startStopAllBtn.Refresh()
	})
}

func (a *App) getCameraDropdownOptions() ([]string, map[string]string) {
	options := []string{"Pasif (Devre Dışı)", "IP Kamera"}
	webcamMap := make(map[string]string)

	detected := config.DetectWebcams()
	for _, wc := range detected {
		label := fmt.Sprintf("Webcam: %s (%s)", wc.Name, wc.Device)
		options = append(options, label)
		webcamMap[label] = wc.Device
	}

	return options, webcamMap
}

func (a *App) getCameraSelectedOption(cam config.CameraSource, options []string, webcamMap map[string]string) string {
	if !cam.Enabled {
		return "Pasif (Devre Dışı)"
	}
	if cam.Type == "rtsp" {
		return "IP Kamera"
	}
	if cam.Type == "webcam" {
		for label, dev := range webcamMap {
			if dev == cam.Device {
				return label
			}
		}
		return fmt.Sprintf("Webcam: Bağlı Değil (%s)", cam.Device)
	}
	return "Pasif (Devre Dışı)"
}

func (a *App) refreshCameraDropdowns() {
	options, webcamMap := a.getCameraDropdownOptions()

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

	if selectedVal == "Pasif (Devre Dışı)" {
		cam.Enabled = false
	} else if selectedVal == "IP Kamera" {
		// Check if there is already an RTSP camera other than this one
		hasOtherRTSP := false
		for i, c := range a.cfg.Cameras {
			if i != camIdx && c.Type == "rtsp" && c.Enabled {
				hasOtherRTSP = true
				break
			}
		}
		if hasOtherRTSP {
			a.mu.Unlock()
			dialog.ShowError(fmt.Errorf("En fazla 1 adet IP kamera eklenmesine izin verilir."), a.window)
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

			// Disable other cameras using this device to prevent conflicts
			for i := range a.cfg.Cameras {
				if i != camIdx && a.cfg.Cameras[i].Enabled && a.cfg.Cameras[i].Type == "webcam" && a.cfg.Cameras[i].Device == dev {
					a.cfg.Cameras[i].Enabled = false
					camerasToStop = append(camerasToStop, a.cfg.Cameras[i].ID)
				}
			}
		} else if strings.HasPrefix(selectedVal, "Webcam: Bağlı Değil ") {
			cam.Enabled = true
			cam.Type = "webcam"
		}
	}

	_ = config.Save(*a.cfg, a.cfgPath)
	a.mu.Unlock()

	// Stop other streams that were disabled
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

	nameEntry := widget.NewEntry()
	nameEntry.SetText(selectedCam.Name)

	sourceType := widget.NewRadioGroup([]string{"IP", "Webcam"}, nil)
	if selectedCam.Type == "webcam" {
		sourceType.SetSelected("Webcam")
	} else {
		sourceType.SetSelected("IP")
	}

	urlEntry := widget.NewEntry()
	urlEntry.SetText(selectedCam.RTSPURL)
	urlEntry.SetPlaceHolder("http://... veya rtsp://...")

	if selectedCam.Type == "webcam" {
		urlEntry.Disable()
	} else {
		urlEntry.Enable()
	}

	sourceType.OnChanged = func(selected string) {
		if selected == "IP" {
			urlEntry.Enable()
		} else {
			urlEntry.Disable()
		}
	}

	formItems := []*widget.FormItem{
		widget.NewFormItem("Kamera İsmi", nameEntry),
		widget.NewFormItem("Tip", sourceType),
		widget.NewFormItem("IP URL", urlEntry),
	}

	d := dialog.NewForm(selectedCam.Name+" - Detaylı Ayarlar", "Kaydet", "İptal", formItems, func(ok bool) {
		defer a.refreshCameraDropdowns()

		if !ok {
			return
		}

		if sourceType.Selected == "IP" {
			// Check if there is already an RTSP camera other than this one
			hasOtherRTSP := false
			for i, c := range a.cfg.Cameras {
				if i != selectedIdx && c.Type == "rtsp" && c.Enabled {
					hasOtherRTSP = true
					break
				}
			}
			if hasOtherRTSP && a.cfg.Cameras[selectedIdx].Enabled {
				dialog.ShowError(fmt.Errorf("En fazla 1 adet IP kamera eklenmesine izin verilir."), a.window)
				return
			}
		}

		a.mu.Lock()
		cam := &a.cfg.Cameras[selectedIdx]
		cam.Name = nameEntry.Text
		if sourceType.Selected == "IP" {
			cam.Type = "rtsp"
		} else {
			cam.Type = "webcam"
		}
		cam.RTSPURL = strings.TrimSpace(urlEntry.Text)
		// We no longer update Device or resolution/fps from this dialog.

		_ = config.Save(*a.cfg, a.cfgPath)

		if panel, exists := a.cameraPanels[cameraID]; exists {
			panel.SetName(cam.Name)
		}

		a.multiManager.UpdateCamera(*cam)
		a.mu.Unlock()
	}, a.window)

	d.Resize(fyne.NewSize(450, 400))
	d.Show()
}

func (a *App) buildCameraGrid() {
	cols, _ := ui.CalculateGrid(len(a.cfg.Cameras))

	// Create camera panels
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

	a.cameraGrid = container.NewGridWithColumns(cols, objects...)
	a.gridContainer = container.NewStack(a.cameraGrid)

	// Select first camera by default
	if len(a.cameraOrder) > 0 && a.selectedCamera == "" {
		a.selectedCamera = a.cameraOrder[0]
	}
}

func (a *App) rebuildGrid() {
	a.cameraOrder = getCameraOrder(a.cfg.Cameras)
	cols, _ := ui.CalculateGrid(len(a.cfg.Cameras))

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
	a.cameraGrid = container.NewGridWithColumns(cols, objects...)
	a.gridContainer.RemoveAll()
	a.gridContainer.Add(a.cameraGrid)
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
		dialog.ShowInformation("Limit", fmt.Sprintf("Maksimum %d kamera eklenebilir.", config.MaxCameras), a.window)
		return
	}

	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder("Kamera ismi")
	nameEntry.SetText(fmt.Sprintf("Kamera %d", len(a.cfg.Cameras)+1))

	sourceType := widget.NewRadioGroup([]string{"IP", "Webcam"}, nil)
	sourceType.SetSelected("IP")

	urlEntry := widget.NewEntry()
	urlEntry.SetPlaceHolder("http://... veya rtsp://...")

	// Detect webcams for dropdown
	webcams := config.DetectWebcams()
	webcamOptions := make([]string, 0)
	webcamMap := make(map[string]string) // display name → device path
	for _, wc := range webcams {
		label := fmt.Sprintf("%s (%s)", wc.Name, wc.Device)
		webcamOptions = append(webcamOptions, label)
		webcamMap[label] = wc.Device
	}
	if len(webcamOptions) == 0 {
		webcamOptions = append(webcamOptions, "Webcam bulunamadı")
	}

	webcamSelect := widget.NewSelect(webcamOptions, nil)
	if len(webcamOptions) > 0 {
		webcamSelect.SetSelected(webcamOptions[0])
	}
	webcamSelect.Hide()

	sourceType.OnChanged = func(selected string) {
		if selected == "IP" {
			urlEntry.Show()
			webcamSelect.Hide()
		} else {
			urlEntry.Hide()
			webcamSelect.Show()
		}
	}

	formItems := []*widget.FormItem{
		widget.NewFormItem("İsim", nameEntry),
		widget.NewFormItem("Kaynak", sourceType),
		widget.NewFormItem("IP URL", urlEntry),
		widget.NewFormItem("Webcam", webcamSelect),
	}

	d := dialog.NewForm("Kamera Ekle", "Ekle", "İptal", formItems, func(ok bool) {
		if !ok {
			return
		}

		if sourceType.Selected == "IP" {
			// Check if there is already an RTSP camera
			hasRTSP := false
			for _, c := range a.cfg.Cameras {
				if c.Type == "rtsp" && c.Enabled {
					hasRTSP = true
					break
				}
			}
			if hasRTSP {
				dialog.ShowError(fmt.Errorf("En fazla 1 adet IP kamera eklenmesine izin verilir."), a.window)
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
			if sel := webcamSelect.Selected; sel != "" {
				if dev, ok := webcamMap[sel]; ok {
					cam.Device = dev
				}
			}
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
}

func (a *App) removeSelectedCamera() {
	if len(a.cfg.Cameras) <= config.MinCameras {
		dialog.ShowInformation("Limit", fmt.Sprintf("Minimum %d kamera olmalı.", config.MinCameras), a.window)
		return
	}

	if a.selectedCamera == "" {
		dialog.ShowInformation("Seçim", "Lütfen silmek için bir kamera seçin.", a.window)
		return
	}

	camName := a.selectedCamera
	for _, c := range a.cfg.Cameras {
		if c.ID == a.selectedCamera {
			camName = c.Name
			break
		}
	}

	dialog.ShowConfirm("Kamera Sil", fmt.Sprintf("%q kamerasını silmek istediğinize emin misiniz?", camName), func(ok bool) {
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

// --- Recording ---

func (a *App) toggleRecording() {
	if a.recorder.IsRecording() {
		a.stopRecording()
	} else {
		a.startRecording()
	}
}

func (a *App) startRecording() {
	cols, rows := ui.CalculateGrid(len(a.cfg.Cameras))
	gridW := cols * 640
	gridH := rows * 480
	fps := 15

	if err := a.recorder.Start(gridW, gridH, fps); err != nil {
		dialog.ShowError(err, a.window)
		return
	}

	a.recordStart = time.Now()
	a.recordBtn.SetText("Durdur")
	a.recordBtn.Icon = theme.MediaStopIcon()
	a.recordBtn.Importance = widget.DangerImportance
	a.recordBtn.Refresh()

	// Start recording frame composition loop
	go a.recordLoop()

	// Start timer display
	a.recordTimer = time.NewTicker(time.Second)
	go func() {
		for range a.recordTimer.C {
			elapsed := time.Since(a.recordStart).Truncate(time.Second)
			fyne.Do(func() {
				a.recordBtn.SetText(fmt.Sprintf("Durdur (%s)", elapsed))
				a.recordBtn.Refresh()
			})
		}
	}()
}

func (a *App) recordLoop() {
	cols, rows := ui.CalculateGrid(len(a.cfg.Cameras))
	gridW := cols * 640
	gridH := rows * 480

	ticker := time.NewTicker(time.Second / 15) // 15 FPS
	defer ticker.Stop()

	for range ticker.C {
		if !a.recorder.IsRecording() {
			return
		}

		// Collect frames from all camera panels
		frames := make(map[string]*image.RGBA)
		for id, panel := range a.cameraPanels {
			if frame := panel.GetLastFrame(); frame != nil {
				frames[id] = frame
			}
		}

		composite := stream.ComposeGridFrame(frames, a.cameraOrder, cols, rows, gridW, gridH)
		a.recorder.WriteFrame(composite)
	}
}

func (a *App) stopRecording() {
	a.stopRecordingWithCallback(nil)
}

func (a *App) stopRecordingWithCallback(onDone func()) {
	if a.recordTimer != nil {
		a.recordTimer.Stop()
		a.recordTimer = nil
	}

	tempFile, err := a.recorder.Stop()
	if err != nil {
		dialog.ShowError(err, a.window)
		a.recordBtn.SetText("Kayıt")
		a.recordBtn.Icon = theme.MediaRecordIcon()
		a.recordBtn.Importance = widget.MediumImportance
		a.recordBtn.Refresh()
		if onDone != nil {
			onDone()
		}
		return
	}

	a.recordBtn.SetText("Kayıt")
	a.recordBtn.Icon = theme.MediaRecordIcon()
	a.recordBtn.Importance = widget.MediumImportance
	a.recordBtn.Refresh()

	// Show file save dialog
	saveDialog := dialog.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
		if err != nil {
			dialog.ShowError(err, a.window)
			if onDone != nil {
				onDone()
			}
			return
		}
		if writer == nil {
			// User cancelled
			_ = os.Remove(tempFile) // Clean up temp file
			if onDone != nil {
				onDone()
			}
			return
		}
		defer writer.Close()

		// Copy temp file to selected location
		src, err := os.Open(tempFile)
		if err != nil {
			dialog.ShowError(err, a.window)
			if onDone != nil {
				onDone()
			}
			return
		}
		defer src.Close()

		if _, err := io.Copy(writer, src); err != nil {
			dialog.ShowError(err, a.window)
			if onDone != nil {
				onDone()
			}
			return
		}

		// Clean up temp file
		_ = os.Remove(tempFile)

		infoDialog := dialog.NewInformation("Kayıt Tamamlandı", "Video başarıyla kaydedildi.", a.window)
		infoDialog.SetOnClosed(func() {
			if onDone != nil {
				onDone()
			}
		})
		infoDialog.Show()
	}, a.window)

	saveDialog.SetFileName(fmt.Sprintf("kayit_%s.mp4", time.Now().Format("20060102_150405")))
	saveDialog.Show()
}

// --- Settings Dialog ---

func (a *App) showSettingsDialog() {
	autostartCheck := widget.NewCheck("Otomatik başlat", nil)
	autostartCheck.SetChecked(a.cfg.AutoStart)

	// Open config / logs buttons
	configBtn := widget.NewButtonWithIcon("Config Aç", theme.SettingsIcon(), func() {
		openPath(a.cfgPath)
	})

	logsBtn := widget.NewButtonWithIcon("Logları Aç", theme.FolderOpenIcon(), func() {
		if logDir, err := config.LogsDir(); err == nil {
			openPath(logDir)
		}
	})

	settingsContent := container.NewVBox(
		widget.NewLabelWithStyle("── Genel Ayarlar ──", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		autostartCheck,
		widget.NewSeparator(),
		container.NewGridWithColumns(2, configBtn, logsBtn),
	)

	d := dialog.NewCustom("SYG Camera Helper Ayarları", "Kaydet ve Kapat", settingsContent, a.window)
	d.Resize(fyne.NewSize(500, 300))

	d.SetOnClosed(func() {
		a.mu.Lock()
		// General settings
		a.cfg.AutoStart = autostartCheck.Checked
		_ = config.Save(*a.cfg, a.cfgPath)

		a.mu.Unlock()

		if autostartCheck.Checked {
			_ = autostart.SetEnabled(true)
		} else {
			_ = autostart.SetEnabled(false)
		}
	})

	d.Show()
}

// --- Layout Save/Load ---

func (a *App) showSaveLayoutDialog() {
	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder("Layout ismi girin...")

	dialog.ShowForm("Layout Kaydet", "Kaydet", "İptal", []*widget.FormItem{
		widget.NewFormItem("İsim", nameEntry),
	}, func(ok bool) {
		if !ok || strings.TrimSpace(nameEntry.Text) == "" {
			return
		}

		layout := config.SavedLayout{
			Name:    nameEntry.Text,
			Cameras: make([]config.CameraSource, len(a.cfg.Cameras)),
		}
		copy(layout.Cameras, a.cfg.Cameras)

		// Replace existing layout with same name, or append
		found := false
		for i, l := range a.cfg.SavedLayouts {
			if l.Name == nameEntry.Text {
				a.cfg.SavedLayouts[i] = layout
				found = true
				break
			}
		}
		if !found {
			a.cfg.SavedLayouts = append(a.cfg.SavedLayouts, layout)
		}

		a.cfg.ActiveLayoutName = nameEntry.Text
		_ = config.Save(*a.cfg, a.cfgPath)

		dialog.ShowInformation("Başarılı", fmt.Sprintf("Layout %q kaydedildi.", nameEntry.Text), a.window)
	}, a.window)
}

func (a *App) showLoadLayoutDialog() {
	if len(a.cfg.SavedLayouts) == 0 {
		dialog.ShowInformation("Boş", "Kaydedilmiş layout bulunamadı.", a.window)
		return
	}

	layoutNames := make([]string, len(a.cfg.SavedLayouts))
	for i, l := range a.cfg.SavedLayouts {
		layoutNames[i] = fmt.Sprintf("%s (%d kamera)", l.Name, len(l.Cameras))
	}

	layoutSelect := widget.NewSelect(layoutNames, nil)
	if len(layoutNames) > 0 {
		layoutSelect.SetSelected(layoutNames[0])
	}

	dialog.ShowForm("Layout Yükle", "Yükle", "İptal", []*widget.FormItem{
		widget.NewFormItem("Layout", layoutSelect),
	}, func(ok bool) {
		if !ok {
			return
		}

		// Find the selected layout
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

		layout := a.cfg.SavedLayouts[idx]

		// Stop all current streams
		a.multiManager.Close()

		// Apply the layout
		a.mu.Lock()
		a.cfg.Cameras = make([]config.CameraSource, len(layout.Cameras))
		copy(a.cfg.Cameras, layout.Cameras)
		a.cfg.ActiveLayoutName = layout.Name
		_ = config.Save(*a.cfg, a.cfgPath)
		a.mu.Unlock()

		// Rebuild everything
		a.multiManager = stream.NewMultiManager(a.cfg, a.cfgPath, a.logger)
		a.rebuildGrid()

		// Start all
		if a.cfg.AutoStart {
			a.multiManager.StartAll()
		}
	}, a.window)
}

// --- Tray ---

func (a *App) setupTray() {
	if desk, ok := a.fyneApp.(desktop.App); ok {
		m := fyne.NewMenu("SYG Camera Helper",
			fyne.NewMenuItem("Göster", func() {
				a.window.Show()
			}),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("Tüm Kameraları Başlat", func() {
				a.rtspUIStopped = false
				a.multiManager.StartAll()
			}),
			fyne.NewMenuItem("Tüm Kameraları Durdur", func() {
				a.rtspUIStopped = true
				a.multiManager.StopAll()
			}),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("Çıkış", func() {
				a.Quit()
			}),
		)
		desk.SetSystemTrayMenu(m)
		desk.SetSystemTrayIcon(fyne.NewStaticResource("icon.png", iconData))
	}
}

func (a *App) minimizeToBackground() {
	// Stop streaming/playback
	a.rtspUIStopped = true
	a.multiManager.StopAll()
	a.updateStartStopAllBtn(false)

	// If recording, stop and show save dialog, then hide window
	if a.recorder.IsRecording() {
		a.stopRecordingWithCallback(func() {
			a.window.Hide()
		})
	} else {
		a.window.Hide()
	}
}

func (a *App) handleClose() {
	dialog.ShowCustomConfirm("Çıkış", "Çıkış Yap", "Tray'e Küçült",
		widget.NewLabel("Uygulamayı tamamen kapatmak mı yoksa system tray'de çalışmaya devam ettirmek mi istiyorsunuz?"),
		func(quit bool) {
			if quit {
				a.Quit()
			} else {
				a.minimizeToBackground()
			}
		}, a.window)
}

func (a *App) Run() error {
	if a.cfg.AutoStart {
		a.multiManager.StartAll()
	}
	a.window.ShowAndRun()
	return nil
}

func (a *App) Quit() {
	if a.recorder.IsRecording() {
		a.recorder.Stop()
	}
	a.multiManager.Close()
	_ = a.logger.Close()
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
		}

		// Count running cameras (total and webcams separately)
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

		recText := ""
		if a.recorder.IsRecording() {
			recText = " | 🔴 Kayıt"
		}

		// Update global start/stop button status based on actual webcam state
		anyWebcamRunning := runningWebcams > 0
		a.updateStartStopAllBtn(anyWebcamRunning)

		fyne.Do(func() {
			a.statusLabel.SetText(fmt.Sprintf("SYG Camera Helper — %d/%d aktif%s", runningTotal, total, recText))
		})
	}
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

// Ensure unused imports are satisfied
var _ = sort.Strings
var _ = canvas.NewImageFromImage

func (a *App) showCameraContextMenu(cameraID string, pe *fyne.PointEvent) {
	menu := fyne.NewMenu("",
		fyne.NewMenuItem("Düzenle", func() {
			a.showEditCameraDialog(cameraID)
		}),
		fyne.NewMenuItem("Sadece bu kamera için kayıt başlat", func() {
			a.toggleSingleCameraRecording(cameraID)
		}),
	)
	
	widget.ShowPopUpMenuAtPosition(menu, a.window.Canvas(), pe.AbsolutePosition)
}

func (a *App) toggleSingleCameraRecording(cameraID string) {
	if a.recorder.IsRecording() {
		a.stopRecording()
	} else {
		a.startSingleCameraRecording(cameraID)
	}
}

func (a *App) startSingleCameraRecording(cameraID string) {
	gridW := 1280
	gridH := 720
	fps := 15

	if err := a.recorder.Start(gridW, gridH, fps); err != nil {
		dialog.ShowError(err, a.window)
		return
	}

	a.recordStart = time.Now()
	a.recordBtn.SetText("Durdur")
	a.recordBtn.Icon = theme.MediaStopIcon()
	a.recordBtn.Importance = widget.DangerImportance
	a.recordBtn.Refresh()

	go a.singleRecordLoop(cameraID, gridW, gridH)

	if a.recordTimer != nil {
		a.recordTimer.Stop()
	}
	a.recordTimer = time.NewTicker(time.Second)
	go func() {
		for range a.recordTimer.C {
			elapsed := time.Since(a.recordStart).Truncate(time.Second)
			fyne.Do(func() {
				a.recordBtn.SetText(fmt.Sprintf("Durdur (%s)", elapsed))
				a.recordBtn.Refresh()
			})
		}
	}()
}

func (a *App) singleRecordLoop(cameraID string, width, height int) {
	ticker := time.NewTicker(time.Second / 15) // 15 FPS
	defer ticker.Stop()

	for range ticker.C {
		if !a.recorder.IsRecording() {
			return
		}

		frames := make(map[string]*image.RGBA)
		panel, exists := a.cameraPanels[cameraID]
		if exists {
			if frame := panel.GetLastFrame(); frame != nil {
				frames[cameraID] = frame
			}
		}

		composite := stream.ComposeGridFrame(frames, []string{cameraID}, 1, 1, width, height)
		a.recorder.WriteFrame(composite)
	}
}
