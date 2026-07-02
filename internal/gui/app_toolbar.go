package gui

import (
	"fmt"
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"nystavision/internal/config"
	"nystavision/internal/i18n"
	"nystavision/internal/ui"
)

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

	// Badge for pending recordings
	a.recordingsBadgeBg = canvas.NewRectangle(color.NRGBA{R: 231, G: 76, B: 60, A: 255}) // Red
	a.recordingsBadgeBg.CornerRadius = 8
	a.recordingsBadgeText = canvas.NewText("0", color.White)
	a.recordingsBadgeText.TextSize = 10
	a.recordingsBadgeText.TextStyle = fyne.TextStyle{Bold: true}
	a.recordingsBadgeText.Alignment = fyne.TextAlignCenter
	
	badgePadded := container.NewPadded(a.recordingsBadgeText)
	a.recordingsBadge = container.NewStack(a.recordingsBadgeBg, badgePadded)
	a.recordingsBadge.Hide()

	badgeOverlay := container.NewVBox(
		container.NewHBox(layout.NewSpacer(), a.recordingsBadge),
	)
	recordingsBtnWithBadge := container.NewStack(a.recordingsBtn, badgeOverlay)

	// Help/Tutorial button
	helpBtn := widget.NewButtonWithIcon("", theme.QuestionIcon(), func() {
		a.showAboutDialog()
	})

	// Layout: [Sahneler] | [+ Ekle] [- Sil] | [▶ Tümünü Başlat] [⏺ Kayıt] | [⚙] [?] | [Kayıtlarım]
	leftGroup := container.NewHBox(layoutsBtn, widget.NewSeparator(), a.addBtn, a.removeBtn)
	middleGroup := container.NewHBox(a.startStopAllBtn, widget.NewSeparator(), a.recordBtn)
	rightGroup := container.NewHBox(widget.NewSeparator(), settingsBtn, helpBtn, widget.NewSeparator(), recordingsBtnWithBadge)

	borderLayout := layout.NewBorderLayout(nil, nil, leftGroup, rightGroup)
	overrideLayout := ui.NewMinSizeOverridingLayout(borderLayout, fyne.NewSize(100, 0))
	return container.New(overrideLayout, leftGroup, rightGroup, container.NewCenter(middleGroup))
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

func formatDuration(elapsed time.Duration) string {
	h := elapsed / time.Hour
	m := (elapsed % time.Hour) / time.Minute
	s := (elapsed % time.Minute) / time.Second
	if h > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

func (a *App) updateRecordBtn() {
	fyne.Do(func() {
		a.updateRecordBtnUI()
	})
}

func (a *App) updateRecordBtnUI() {
	a.mu.Lock()
	isRecording := a.isRecording
	isCompact := a.isCompactToolbar
	recordStart := a.recordStart
	a.mu.Unlock()

	if isRecording {
		elapsed := time.Since(recordStart).Truncate(time.Second)
		elapsedStr := formatDuration(elapsed)

		if isCompact {
			a.recordBtn.SetText(elapsedStr)
		} else {
			a.recordBtn.SetText(fmt.Sprintf("%s (%s)", i18n.T("btn_stop"), elapsedStr))
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
		}

		a.addBtn.Refresh()
		if a.removeBtn != nil {
			a.removeBtn.Refresh()
		}
		a.startStopAllBtn.Refresh()
		a.updateRecordBtnUI()
		if a.layoutsRef != nil {
			a.layoutsRef.Refresh()
		}
		if a.recordingsBtn != nil {
			a.recordingsBtn.Refresh()
		}
	})
}

func (a *App) updateRecordingsBadge() {
	if a.recordingsBadge == nil {
		return
	}
	a.mu.Lock()
	count := len(a.pendingRecordings)
	a.mu.Unlock()

	fyne.Do(func() {
		if count > 0 {
			a.recordingsBadgeText.Text = fmt.Sprintf("%d", count)
			a.recordingsBadgeText.Refresh()
			a.recordingsBadge.Show()
		} else {
			a.recordingsBadge.Hide()
		}
	})
}
