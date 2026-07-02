package gui

import (
	"context"
	_ "embed"
	"fmt"
	"image/color"
	"log"
	"os"
	"path/filepath"
	"runtime"
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

	"nystavision/internal/config"
	"nystavision/internal/i18n"
	"nystavision/internal/logging"
	"nystavision/internal/stream"
	"nystavision/internal/ui"
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
	recordingsList          *widget.List
	recordingsData          []recordingEntry
	recordingsDrawerVisible bool
	highlightedDir          string
	recordingsBtn           *widget.Button

	// Views containers for layout draw swapping
	recordingsDrawerContent *fyne.Container
	rightDrawerContent      *fyne.Container

	patientInfoDrawerPanel *fyne.Container

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
		a.patientCache.SetFilePath(filepath.Join(filepath.Dir(cfgPath), "patient_cache.json"))
		a.mu.Unlock()

		a.translateCameraNames()

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

			showMainAndCloseSplash := func() {
				// Show main window first, then close splash screen to avoid event loop termination
				a.window.Show()
				if a.splashWindow != nil {
					a.splashWindow.Close()
				}
			}

			// Ensure the splash screen is visible for at least 1 second (1000ms)
			elapsed := time.Since(startTime)
			remaining := 1000*time.Millisecond - elapsed

			runSetupOrUI := func() {
				if !a.cfg.SetupCompleted {
					a.showSetupScreen(func() {
						if a.cfg.AutoStart {
							a.multiManager.StartAll()
						}
						if !a.cfg.TutorialShown {
							time.AfterFunc(500*time.Millisecond, func() {
								fyne.Do(func() {
									a.showTutorial()
								})
							})
						}
					})
					showMainAndCloseSplash()
				} else {
					a.setupUI()
					a.setupTray()
					a.setupFrameCallbacks()
					showMainAndCloseSplash()

					if a.cfg.AutoStart {
						a.multiManager.StartAll()
					}
					if !a.cfg.TutorialShown {
						time.AfterFunc(500*time.Millisecond, func() {
							fyne.Do(func() {
								a.showTutorial()
							})
						})
					}
				}
			}

			if remaining > 0 {
				time.AfterFunc(remaining, func() {
					fyne.Do(runSetupOrUI)
				})
			} else {
				runSetupOrUI()
			}
		})
	}()

	a.fyneApp.Run()
	return nil
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
				patientInfo, err := stream.LoadPatientInfo(job.PatientDir)
				if err != nil {
					continue
				}

				// Check if verified already, or verify now using fast check first, fallback to VerifyOutput
				allValid := job.Verified
				if !allValid {
					// Gather all video files to check
					var videos []stream.VideoFile
					for _, m := range patientInfo.Maneuvers {
						videos = append(videos, m.Videos...)
					}
					videos = append(videos, patientInfo.Videos...) // include legacy flat list if any

					if len(videos) > 0 {
						allValid = true
						for _, vid := range videos {
							vidPath := filepath.Join(job.PatientDir, vid.FileName)
							// Fast check: does it exist and is non-empty?
							if info, statErr := os.Stat(vidPath); statErr != nil || info.Size() == 0 {
								allValid = false
								break
							}
							// As a safeguard, verify output using ffprobe if not verified
							if err := a.postProc.VerifyOutput(vidPath); err != nil {
								allValid = false
								break
							}
						}
					}
				}

				if allValid {
					// 1. Delete raw directory safely
					rawDir := filepath.Join(job.PatientDir, "raw")
					if _, err := os.Stat(rawDir); err == nil {
						_ = os.RemoveAll(rawDir)
					}

					// 2. Delete Genel_Onizleme_*.mp4 preview files
					files, _ := filepath.Glob(filepath.Join(job.PatientDir, "Genel_Onizleme_*.mp4"))
					for _, f := range files {
						_ = os.Remove(f)
					}

					// 3. Remove Genel_Onizleme references from maneuvers list
					if pInfo, err := stream.LoadPatientInfo(job.PatientDir); err == nil {
						for mIdx := range pInfo.Maneuvers {
							var cleanVids []stream.VideoFile
							for _, v := range pInfo.Maneuvers[mIdx].Videos {
								if !strings.HasPrefix(v.FileName, "Genel_Onizleme_") {
									cleanVids = append(cleanVids, v)
								}
							}
							pInfo.Maneuvers[mIdx].Videos = cleanVids
						}

						var cleanLegacy []stream.VideoFile
						for _, v := range pInfo.Videos {
							if !strings.HasPrefix(v.FileName, "Genel_Onizleme_") {
								cleanLegacy = append(cleanLegacy, v)
							}
						}
						pInfo.Videos = cleanLegacy

						_ = stream.SavePatientInfo(job.PatientDir, pInfo)
					}

					a.jobQueue.DeleteJob(job.ID)
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
