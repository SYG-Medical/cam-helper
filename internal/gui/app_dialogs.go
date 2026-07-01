package gui

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"nystavision/internal/config"
	"nystavision/internal/i18n"
	"nystavision/internal/stream"
)

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

			// ── Camera Role ──────────────────────────────────────────────
			roleOptions := []string{
				i18n.T("cam_role_environment"),
				i18n.T("cam_role_glasses"),
			}
			roleSelect := widget.NewSelect(roleOptions, nil)

			// Determine smart default: suggest whichever role is under-represented
			envCount, glassCount := 0, 0
			for _, c := range a.cfg.Cameras {
				if c.CameraRole == config.CameraRoleGlasses {
					glassCount++
				} else {
					envCount++
				}
			}
			defaultRole := config.CameraRoleEnvironment
			if glassCount < 1 {
				defaultRole = config.CameraRoleGlasses
			}
			if defaultRole == config.CameraRoleGlasses {
				roleSelect.SetSelected(i18n.T("cam_role_glasses"))
			} else {
				roleSelect.SetSelected(i18n.T("cam_role_environment"))
			}

			// ── Eye Side (glasses only) ───────────────────────────────────
			eyeOptions := []string{
				i18n.T("cam_eye_right"),
				i18n.T("cam_eye_left"),
				i18n.T("cam_eye_both"),
			}
			eyeSelect := widget.NewSelect(eyeOptions, nil)
			eyeSelect.SetSelected(i18n.T("cam_eye_right"))
			eyeFormItem := widget.NewFormItem(i18n.T("lbl_eye_side"), eyeSelect)

			// Show/hide eye side selector based on role
			eyeFormItem.Widget.Hide()
			if roleSelect.Selected == i18n.T("cam_role_glasses") {
				eyeFormItem.Widget.Show()
			}

			roleSelect.OnChanged = func(s string) {
				if s == i18n.T("cam_role_glasses") {
					eyeFormItem.Widget.Show()
				} else {
					eyeFormItem.Widget.Hide()
				}
			}

			// ── Source Type ───────────────────────────────────────────────
			sourceType := widget.NewSelect([]string{"IP", "Webcam"}, nil)
			sourceType.SetSelected("IP")

			urlEntry := widget.NewEntry()
			urlEntry.SetPlaceHolder(i18n.T("placeholder_url"))

			// Build webcam options, marking already-used devices
			webcamSelect := widget.NewSelect(nil, nil)
			var wcNames []string
			wcDevMap := make(map[string]string)    // display name → device path
			usedDevices := make(map[string]bool)   // device path → in use

			a.mu.Lock()
			for _, cam := range a.cfg.Cameras {
				if cam.Enabled && cam.Type == "webcam" && cam.Device != "" {
					usedDevices[cam.Device] = true
				}
			}
			a.mu.Unlock()

			for _, wc := range detected {
				var label string
				if usedDevices[wc.Device] {
					label = fmt.Sprintf("%s %s (%s)", i18n.T("lbl_in_use_device"), wc.Name, wc.Device)
				} else {
					label = wc.Name
				}
				wcNames = append(wcNames, label)
				wcDevMap[label] = wc.Device
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
				widget.NewFormItem(i18n.T("lbl_camera_role"), roleSelect),
				eyeFormItem,
				widget.NewFormItem(i18n.T("lbl_source"), sourceType),
				widget.NewFormItem(i18n.T("lbl_ip_url"), urlEntry),
				widget.NewFormItem(i18n.T("lbl_webcam"), webcamSelect),
			}

			innerForm := widget.NewForm(formItems...)
			scrollableContent := container.NewVScroll(innerForm)
			scrollableContent.SetMinSize(fyne.NewSize(500, 340))

			d := dialog.NewCustomConfirm(
				i18n.T("title_add_camera"),
				i18n.T("btn_add_camera"),
				i18n.T("btn_cancel"),
				scrollableContent,
				func(ok bool) {
					if !ok {
						return
					}

					// Map role selection to config constants
					camRole := config.CameraRoleEnvironment
					camEyeSide := ""
					if roleSelect.Selected == i18n.T("cam_role_glasses") {
						camRole = config.CameraRoleGlasses
						switch eyeSelect.Selected {
						case i18n.T("cam_eye_left"):
							camEyeSide = config.EyeSideLeft
						case i18n.T("cam_eye_both"):
							camEyeSide = config.EyeSideBoth
						default:
							camEyeSide = config.EyeSideRight
						}
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
						ID:         config.NextCameraID(a.cfg.Cameras),
						Width:      1280,
						Height:     720,
						FPS:        30,
						Enabled:    true,
						CameraRole: camRole,
						EyeSide:    camEyeSide,
					}

					if sourceType.Selected == "IP" {
						cam.Type = "rtsp"
						cam.RTSPURL = strings.TrimSpace(urlEntry.Text)
					} else {
						cam.Type = "webcam"
						dev := wcDevMap[webcamSelect.Selected]
						// Guard: prevent selecting a device already in use
						if usedDevices[dev] {
							dialog.ShowError(fmt.Errorf("%s", i18n.T("msg_webcam_already_in_use")), a.window)
							return
						}
						cam.Device = dev
					}

					prevServerCam := a.cfg.RTSPServerCamera
					if err := a.multiManager.AddCamera(cam); err != nil {
						dialog.ShowError(err, a.window)
						return
					}

					a.translateCameraNames()
					_ = a.multiManager.SaveConfig()

					if prevServerCam != a.cfg.RTSPServerCamera {
						a.multiManager.Close()
						a.multiManager = stream.NewMultiManager(a.cfg, a.cfgPath, a.logger)
						if a.cfg.AutoStart {
							a.multiManager.StartAll()
						}
					}

					a.rebuildGrid()
				}, a.window)

			d.Resize(fyne.NewSize(500, 400))
			d.Show()
		})
	}()
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

			// ── Camera Role ──────────────────────────────────────────────
			roleOptions := []string{
				i18n.T("cam_role_environment"),
				i18n.T("cam_role_glasses"),
			}
			roleSelect := widget.NewSelect(roleOptions, nil)
			if selectedCam.CameraRole == config.CameraRoleGlasses {
				roleSelect.SetSelected(i18n.T("cam_role_glasses"))
			} else {
				roleSelect.SetSelected(i18n.T("cam_role_environment"))
			}

			// ── Eye Side (glasses only) ───────────────────────────────────
			eyeOptions := []string{
				i18n.T("cam_eye_right"),
				i18n.T("cam_eye_left"),
				i18n.T("cam_eye_both"),
			}
			eyeSelect := widget.NewSelect(eyeOptions, nil)
			switch selectedCam.EyeSide {
			case config.EyeSideLeft:
				eyeSelect.SetSelected(i18n.T("cam_eye_left"))
			case config.EyeSideBoth:
				eyeSelect.SetSelected(i18n.T("cam_eye_both"))
			default:
				eyeSelect.SetSelected(i18n.T("cam_eye_right"))
			}
			eyeFormItem := widget.NewFormItem(i18n.T("lbl_eye_side"), eyeSelect)

			// Show/hide eye side selector based on role
			if roleSelect.Selected == i18n.T("cam_role_glasses") {
				eyeFormItem.Widget.Show()
			} else {
				eyeFormItem.Widget.Hide()
			}

			roleSelect.OnChanged = func(s string) {
				if s == i18n.T("cam_role_glasses") {
					eyeFormItem.Widget.Show()
				} else {
					eyeFormItem.Widget.Hide()
				}
			}

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
				widget.NewFormItem(i18n.T("lbl_camera_role"), roleSelect),
				eyeFormItem,
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

					// Map role and eye selections
					camRole := config.CameraRoleEnvironment
					camEyeSide := ""
					if roleSelect.Selected == i18n.T("cam_role_glasses") {
						camRole = config.CameraRoleGlasses
						switch eyeSelect.Selected {
						case i18n.T("cam_eye_left"):
							camEyeSide = config.EyeSideLeft
						case i18n.T("cam_eye_both"):
							camEyeSide = config.EyeSideBoth
						default:
							camEyeSide = config.EyeSideRight
						}
					}
					camPtr.CameraRole = camRole
					camPtr.EyeSide = camEyeSide

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

					a.translateCameraNames()
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

func (a *App) showUSBBandwidthErrorDialog(cameraName string) {
	msg := fmt.Sprintf(i18n.T("usb_bandwidth_error_msg"), cameraName)
	dialog.ShowInformation(i18n.T("usb_bandwidth_error_title"), msg, a.window)
}

func (a *App) showCameraContextMenu(cameraID string, ev *fyne.PointEvent) {
	menu := fyne.NewMenu("",
		fyne.NewMenuItem(i18n.T("menu_edit"), func() {
			a.showEditCameraDialog(cameraID)
		}),
		fyne.NewMenuItem(i18n.T("menu_record_single"), func() {
			a.toggleRecording() // For now, starts/stops composite recording
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem(i18n.T("menu_delete_camera"), func() {
			a.deleteCameraByID(cameraID)
		}),
	)

	widget.ShowPopUpMenuAtPosition(menu, a.window.Canvas(), ev.AbsolutePosition)
}
