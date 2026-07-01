package gui

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"

	"nystavision/internal/i18n"
)

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
