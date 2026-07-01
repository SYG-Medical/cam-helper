package gui

import (
	"fmt"
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"

	"nystavision/internal/config"
	"nystavision/internal/i18n"
	"nystavision/internal/ui"
)

func getCameraOrder(cameras []config.CameraSource) []string {
	order := make([]string, len(cameras))
	for i, c := range cameras {
		order[i] = c.ID
	}
	return order
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
			a.showUSBBandwidthErrorDialog(i18n.T("msg_max_ip_camera")) // Reuse dialog showing or similar
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

func (a *App) deleteCameraByID(cameraID string) {
	a.mu.Lock()
	idx := -1
	for i, c := range a.cfg.Cameras {
		if c.ID == cameraID {
			idx = i
			break
		}
	}
	if idx < 0 {
		a.mu.Unlock()
		return
	}

	// Remove from manager
	a.multiManager.RemoveCamera(cameraID)

	// Remove from list
	a.cfg.Cameras = append(a.cfg.Cameras[:idx], a.cfg.Cameras[idx+1:]...)
	_ = config.Save(*a.cfg, a.cfgPath)
	a.mu.Unlock()

	a.rebuildGrid()
}

func (a *App) translateCameraNames() {
	envCount := 1
	for i := range a.cfg.Cameras {
		cam := &a.cfg.Cameras[i]
		if cam.CameraRole == config.CameraRoleEnvironment {
			cam.Name = fmt.Sprintf(i18n.T("cam_name_auto_env"), envCount)
			envCount++
		} else { // glasses
			switch cam.EyeSide {
			case config.EyeSideLeft:
				cam.Name = i18n.T("cam_name_auto_left")
			case config.EyeSideBoth:
				cam.Name = i18n.T("cam_name_auto_both")
			default:
				cam.Name = i18n.T("cam_name_auto_right")
			}
		}
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

func buildGridTree(cols, rows int, totalCameras int) miniNode {
	if totalCameras == 3 {
		// Custom tree for 3 cameras layout:
		// Top row horizontal split of 0 and 1
		// Bottom row is 2 (fully spans)
		topSplit := &miniSplit{
			horizontal: true,
			offset:     0.5,
			leading:    &miniLeaf{index: 0},
			trailing:   &miniLeaf{index: 1},
		}
		mainSplit := &miniSplit{
			horizontal: false,
			offset:     0.5,
			leading:    topSplit,
			trailing:   &miniLeaf{index: 2},
		}
		return mainSplit
	}

	var rowNodes []miniNode
	cellIdx := 0
	for r := 0; r < rows; r++ {
		var colNodes []miniNode
		for c := 0; c < cols; c++ {
			if cellIdx < totalCameras {
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
