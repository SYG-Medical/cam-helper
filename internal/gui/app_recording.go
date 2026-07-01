package gui

import (
	"context"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"nystavision/internal/config"
	"nystavision/internal/i18n"
	"nystavision/internal/stream"
	"nystavision/internal/ui"
)

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

	// Show progress toast immediately to keep UI responsive
	content := container.NewVBox(widget.NewLabel(i18n.T("msg_please_wait")), widget.NewProgressBarInfinite())
	progress := a.NewToastProgress(i18n.T("msg_please_wait"), content)
	progress.Show()

	go func() {
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
				CameraRole: camera.CameraRole,
				EyeSide:    camera.EyeSide,
			})
		}

		if len(recordings) == 0 {
			fyne.Do(func() {
				progress.Hide()
				dialog.ShowError(fmt.Errorf("no enabled camera is available for recording"), a.window)
			})
			return
		}
		cols, rows := ui.CalculateGrid(len(recordings))

		freeBytes, err := stream.DiskFreeBytes(a.cfg.RecordingsDir)
		if err != nil {
			a.logger.Printf("[recording] check disk space failed: %v", err)
			fyne.Do(func() {
				progress.Hide()
				dialog.ShowError(fmt.Errorf(i18n.T("disk_check_error"), err), a.window)
			})
			// Proceed in background
			a.startTemporaryRecording(recordings, cols, rows, progress)
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
			fyne.Do(func() {
				progress.Hide()
				a.showDiskSpaceBlocked(func() {
					a.changeRecordingsDirAndRetry()
				})
			})
			return
		} else if availableMins < 15.0 {
			fyne.Do(func() {
				progress.Hide()
				a.showDiskSpaceWarning(availableMins, func() {
					// User accepted: start async
					content2 := container.NewVBox(widget.NewLabel(i18n.T("msg_please_wait")), widget.NewProgressBarInfinite())
					prog2 := a.NewToastProgress(i18n.T("msg_please_wait"), content2)
					prog2.Show()
					go a.startTemporaryRecording(recordings, cols, rows, prog2)
				}, func() {
					a.changeRecordingsDirAndRetry()
				})
			})
			return
		}

		a.startTemporaryRecording(recordings, cols, rows, progress)
	}()
}

func (a *App) startTemporaryRecording(cameras []stream.CameraRecording, cols, rows int, progress *ToastProgress) {
	timestamp := time.Now().Format("20060102_150405")
	tempPatientDir := filepath.Join(a.cfg.RecordingsDir, "Record_"+timestamp)
	if err := os.MkdirAll(tempPatientDir, 0o755); err != nil {
		fyne.Do(func() {
			progress.Hide()
			dialog.ShowError(fmt.Errorf("failed to create temp recording directory: %w", err), a.window)
		})
		return
	}
	// Start with REC01; it will be adjusted if we append to an existing patient later
	a.proceedWithRecording(cameras, cols, rows, tempPatientDir, "_REC01", progress)
}

func (a *App) proceedWithRecording(cameras []stream.CameraRecording, cols, rows int, patientDir string, recordTag string, progress *ToastProgress) {
	session, err := stream.NewRecordingSession(patientDir, cameras, cols, rows, recordTag)
	if err != nil {
		fyne.Do(func() {
			progress.Hide()
			dialog.ShowError(err, a.window)
		})
		return
	}

	var startErr error
	if a.cfg.CompositeRecording {
		// In composite mode, we don't restart FFmpeg pipelines. We just start
		// the composite recorder which reads from existing preview frames.
		session.UpdateStartTime(time.Now())
		ffmpegPath, _ := stream.ResolveFFmpegPath(a.cfg.FFmpegPath)
		outFile := filepath.Join(patientDir, fmt.Sprintf("%s_%s%s.mp4", stream.GeneralVideoPrefix(), time.Now().Format("20060102_150405"), recordTag))

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
						a.recordingTimeLabel.Text = i18n.T("lbl_status_bar_rec", formatDuration(elapsed))
						a.recordingTimeLabel.Refresh()
					}
					a.updateRecordBtnUI()
				})
			}
		}()

		a.startDiskMonitor(session, cameras, cols, rows)
	})
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
	compositeRec := a.compositeRecorder
	a.compositeRecorder = nil
	a.recordSession = nil
	a.mu.Unlock()

	a.updateRecordBtn()

	if a.recordTimer != nil {
		a.recordTimer.Stop()
		a.recordTimer = nil
	}

	a.stopDiskMonitor()
	a.hidePerfBanner()

	fyne.Do(func() {
		if a.recordingTimeLabel != nil {
			a.recordingTimeLabel.Text = ""
			a.recordingTimeLabel.Refresh()
		}
		if session == nil {
			return
		}

		// Open the drawer to get patient info. The drawer's "Save" button will trigger postProcessRecording.
		a.showPatientInfoDrawerAfterRecording(session, compositeRec)
	})
}

func (a *App) postProcessRecording(session *stream.RecordingSession, compositeRec *stream.CompositeRecorder, tempDir, actualPatientDir string, note string) {
	// ---------------------------------------------------------------
	// COMPOSITE MODE
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

			compositeFile := compositeRec.OutFile()
			// Move temporary recorded files to final directory if needed
			if tempDir != "" && tempDir != actualPatientDir {
				stream.MoveTempToFinal(tempDir, actualPatientDir)
			}

			// compositeFile is originally inside tempDir, but stream.MoveTempToFinal moves everything 
			// into actualPatientDir with the same filenames. 
			// So the finalFile will just be the basename in actualPatientDir.
			finalFile := filepath.Join(actualPatientDir, filepath.Base(compositeFile))
			compositeFile = finalFile

			fyne.Do(func() {
				progress.Hide()

				if err != nil {
					dialog.ShowError(fmt.Errorf("Genel video kaydedilemedi: %v", err), a.window)
					return
				}

				// Create a new Maneuver for this recording session and append it
				maneuver := stream.Maneuver{
					Note: note,
					Videos: []stream.VideoFile{
						{
							FileName: filepath.Base(compositeFile),
							Type:     "general",
						},
					},
				}
				_ = stream.AppendManeuver(actualPatientDir, maneuver)

				if a.jobQueue != nil {
					_ = a.jobQueue.EnqueueComposite(session, actualPatientDir, compositeFile)
				}

				a.sendOSNotification(i18n.T("record_done_title"), i18n.T("record_saved_notification"))
				a.showRecordingsDrawerWithHighlight(actualPatientDir)
			})
		}()
		return
	}

	// ---------------------------------------------------------------
	// STANDARD MODE
	// ---------------------------------------------------------------
	content := container.NewVBox(widget.NewLabel(i18n.T("msg_please_wait")), widget.NewProgressBarInfinite())
	progress := a.NewToastProgress(i18n.T("msg_please_wait"), content)
	progress.Show()

	go func() {
		a.multiManager.StopRecording(session)

		if tempDir != "" && tempDir != actualPatientDir {
			stream.MoveTempToFinal(tempDir, actualPatientDir)
		}

		fyne.Do(func() {
			progress.Hide()

			progressBar := widget.NewProgressBar()
			progressLabel := widget.NewLabel(i18n.T("record_processing"))
			progressContent := container.NewVBox(progressLabel, progressBar)
			progressToast := a.NewToastProgress(i18n.T("record_processing"), progressContent)
			progressToast.Show()

			go func() {
				ctx := context.Background()
				progressCh := make(chan float64, 10)

				var result stream.ProcessResult
				done := make(chan struct{})

				go func() {
					snap := session.Snapshot()
					cams := session.CameraList()
					result = a.postProc.ProcessGeneralOnly(ctx, snap, cams, actualPatientDir, progressCh, true)
					close(done)
				}()

				go func() {
					for val := range progressCh {
						v := val
						fyne.Do(func() {
							progressBar.SetValue(v)
						})
					}
				}()

				<-done

				if result.Err == nil && a.jobQueue != nil {
					_ = a.jobQueue.Enqueue(session, actualPatientDir)
				}

				if result.Err == nil && len(result.Files) > 0 {
					// Create a new Maneuver for this recording session
					maneuver := stream.Maneuver{
						Note: note,
						Videos: []stream.VideoFile{
							{
								FileName: filepath.Base(result.Files[0]),
								Type:     "general",
							},
						},
					}
					_ = stream.AppendManeuver(actualPatientDir, maneuver)
				}

				fyne.Do(func() {
					progressToast.Hide()

					if result.Err != nil {
						detailedErr := fmt.Errorf("Genel video oluşturulamadı: %v", result.Err)
						dialog.ShowError(detailedErr, a.window)
						return
					}

					a.sendOSNotification(i18n.T("record_done_title"), i18n.T("record_saved_notification"))
					a.showRecordingsDrawerWithHighlight(actualPatientDir)
				})
			}()
		})
	}()
}

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
