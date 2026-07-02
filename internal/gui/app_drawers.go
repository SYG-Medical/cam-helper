package gui

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"nystavision/internal/config"
	"nystavision/internal/i18n"
	"nystavision/internal/stream"
	"nystavision/internal/system"
	"nystavision/internal/ui"
)

// --- Layout Scenes Drawer ---

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

		gridCols, gridRows := ui.CalculateGrid(len(l.Cameras))

		tree := buildGridTree(gridCols, gridRows, len(l.Cameras))
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

		var nameColor color.Color = colorTextPrimary
		if layoutName == activeName {
			nameColor = colorMedicalBlue
		}
		nameText := canvas.NewText(layoutName, nameColor)
		nameText.TextSize = 12
		nameText.TextStyle = fyne.TextStyle{Bold: true}

		infoText := canvas.NewText(fmt.Sprintf(i18n.T("lbl_camera_count"), len(l.Cameras)), colorTextSecondary)
		infoText.TextSize = 9

		textCol := container.NewVBox(nameText, infoText)

		lname := layoutName

		deleteBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
			a.deleteLayoutByName(lname)
		})
		deleteBtn.Importance = widget.DangerImportance

		visualContent := container.NewBorder(nil, nil, miniGridWrapper, nil, container.NewCenter(textCol))

		cardClickable := newClickableCard(visualContent, func() {
			a.loadLayoutByName(lname)
		})

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

func (a *App) showSaveLayoutDialogAfterSplitUpdate() {
	a.saveWindowLayoutToConfig()
}

func (a *App) showLoadLayoutDialog() {
	a.toggleLayoutDrawer()
}

// --- Recordings Drawer (Kayıtlarım) ---

type recItemUI struct {
	NameText *canvas.Text
	InfoText *canvas.Text
	OpenBtn  *widget.Button
	InfoBtn  *widget.Button
	EditBtn  *widget.Button
	Bg       *canvas.Rectangle
}

var (
	recordingListMap = make(map[fyne.CanvasObject]*recItemUI)
	recordingListMu  sync.Mutex
)

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

	a.recordingsList = widget.NewList(
		func() int {
			a.mu.Lock()
			defer a.mu.Unlock()
			return len(a.recordingsData)
		},
		func() fyne.CanvasObject {
			nameText := canvas.NewText("", colorTextPrimary)
			nameText.TextSize = 12
			nameText.TextStyle = fyne.TextStyle{Bold: true}

			infoText := canvas.NewText("", colorTextSecondary)
			infoText.TextSize = 10

			textCol := container.NewVBox(nameText, infoText)

			openBtn := widget.NewButtonWithIcon("", theme.FolderOpenIcon(), nil)
			openBtn.Importance = widget.LowImportance

			infoBtn := widget.NewButtonWithIcon("", theme.InfoIcon(), nil)
			infoBtn.Importance = widget.LowImportance

			editBtn := widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), nil)
			editBtn.Importance = widget.LowImportance

			actions := container.NewHBox(infoBtn, editBtn, openBtn)
			itemContent := container.NewBorder(nil, nil, nil, actions, container.NewPadded(textCol))

			itemBg := canvas.NewRectangle(colorInputSurface)
			itemBg.CornerRadius = 6

			stack := container.NewStack(itemBg, container.NewPadded(itemContent))

			recordingListMu.Lock()
			recordingListMap[stack] = &recItemUI{
				NameText: nameText,
				InfoText: infoText,
				OpenBtn:  openBtn,
				InfoBtn:  infoBtn,
				EditBtn:  editBtn,
				Bg:       itemBg,
			}
			recordingListMu.Unlock()

			return stack
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			a.mu.Lock()
			if int(i) >= len(a.recordingsData) {
				a.mu.Unlock()
				return
			}
			rec := a.recordingsData[i]
			highlighted := a.highlightedDir == rec.Dir
			a.mu.Unlock()

			recordingListMu.Lock()
			uiElems := recordingListMap[o]
			recordingListMu.Unlock()

			if uiElems == nil {
				return
			}

			if highlighted {
				uiElems.Bg.FillColor = colorAmberWarning
			} else {
				uiElems.Bg.FillColor = colorInputSurface
			}
			uiElems.Bg.Refresh()

			uiElems.NameText.Text = rec.Name
			if uiElems.NameText.Text == "" {
				uiElems.NameText.Text = "—"
			}
			uiElems.NameText.Refresh()

			dateStr := rec.Date.Format("02.01.2006 15:04")
			infoStr := fmt.Sprintf("%s - %s", dateStr, fmt.Sprintf(i18n.T("recordings_maneuver_count"), rec.ManeuverCount))
			uiElems.InfoText.Text = infoStr
			uiElems.InfoText.Refresh()

			recDir := rec.Dir
			uiElems.OpenBtn.OnTapped = func() {
				system.OpenPath(recDir)
			}
			uiElems.InfoBtn.OnTapped = func() {
				info, err := stream.LoadPatientInfo(recDir)
				if err == nil {
					a.showPatientInfoDialog(info)
				}
			}
			uiElems.EditBtn.OnTapped = func() {
				info, err := stream.LoadPatientInfo(recDir)
				if err == nil {
					a.showEditPatientDialog(recDir, info, func(updated stream.PatientInfo) {
						a.mu.Lock()
						for idx := range a.recordingsData {
							if a.recordingsData[idx].Dir == recDir {
								a.recordingsData[idx].Name = updated.Name
								break
							}
						}
						a.mu.Unlock()
						if a.recordingsList != nil {
							a.recordingsList.RefreshItem(i)
						}
					})
				}
			}
		},
	)

	a.recordingsListContainer = container.NewStack(a.recordingsList)

	a.recordingsDrawerContent = container.NewBorder(
		container.NewVBox(header, widget.NewSeparator()),
		nil,
		nil, nil,
		a.recordingsListContainer,
	)

	a.rightDrawerContent = container.NewStack(drawerBg, drawerSpacer, a.recordingsDrawerContent)
	a.recordingsDrawerPanel = a.rightDrawerContent
	a.recordingsDrawerPanel.Hide()
	a.recordingsDrawerVisible = false
}

func (a *App) showRecordingsListInDrawer() {
	if a.recordingsDrawerPanel != nil && a.recordingsDrawerContent != nil {
		drawerBg := canvas.NewRectangle(colorCardSurface)
		drawerSpacer := canvas.NewRectangle(color.Transparent)
		drawerSpacer.SetMinSize(fyne.NewSize(280, 0))
		fyne.Do(func() {
			a.recordingsDrawerPanel.RemoveAll()
			a.recordingsDrawerPanel.Add(drawerBg)
			a.recordingsDrawerPanel.Add(drawerSpacer)
			a.recordingsDrawerPanel.Add(a.recordingsDrawerContent)
			a.recordingsDrawerPanel.Refresh()
		})
	}
}

func (a *App) toggleRecordingsDrawer() {
	a.mu.Lock()
	a.recordingsDrawerVisible = !a.recordingsDrawerVisible
	visible := a.recordingsDrawerVisible
	a.mu.Unlock()

	if visible {
		a.showRecordingsListInDrawer()
		go a.refreshRecordingsList()
		a.recordingsDrawerPanel.Show()
	} else {
		a.recordingsDrawerPanel.Hide()
	}
	a.window.Content().Refresh()
}

type recordingEntry struct {
	Name          string
	Date          time.Time
	ManeuverCount int
	Dir           string
}

func (a *App) refreshRecordingsList() {
	a.mu.Lock()
	recDir := a.cfg.RecordingsDir
	a.mu.Unlock()

	entries, err := os.ReadDir(recDir)
	if err != nil {
		fyne.Do(func() {
			emptyLabel := widget.NewLabel(i18n.T("recordings_empty"))
			emptyLabel.Alignment = fyne.TextAlignCenter
			a.recordingsListContainer.Objects = []fyne.CanvasObject{emptyLabel}
			a.recordingsListContainer.Refresh()
		})
		return
	}

	type dirData struct {
		entry   os.DirEntry
		modTime time.Time
	}
	var dirs []dirData
	for _, entry := range entries {
		if entry.IsDir() {
			info, err := entry.Info()
			if err == nil {
				dirs = append(dirs, dirData{entry: entry, modTime: info.ModTime()})
			}
		}
	}

	sort.Slice(dirs, func(i, j int) bool {
		return dirs[i].modTime.After(dirs[j].modTime)
	})

	limit := 50
	if len(dirs) < limit {
		limit = len(dirs)
	}

	var recordings []recordingEntry
	for _, data := range dirs[:limit] {
		dirPath := filepath.Join(recDir, data.entry.Name())
		info, err := stream.LoadPatientInfo(dirPath)
		if err != nil {
			continue
		}

		maneuverCount := len(info.Maneuvers)
		if maneuverCount == 0 {
			maneuverCount = 1
		}

		recordings = append(recordings, recordingEntry{
			Name:          info.Name,
			Date:          info.RecordDate,
			ManeuverCount: maneuverCount,
			Dir:           dirPath,
		})
	}

	sort.Slice(recordings, func(i, j int) bool {
		return recordings[i].Date.After(recordings[j].Date)
	})

	fyne.Do(func() {
		a.mu.Lock()
		a.recordingsData = recordings
		a.mu.Unlock()

		if len(recordings) == 0 {
			emptyLabel := widget.NewLabel(i18n.T("recordings_empty"))
			emptyLabel.Alignment = fyne.TextAlignCenter
			a.recordingsListContainer.RemoveAll()
			a.recordingsListContainer.Add(emptyLabel)
		} else {
			a.recordingsListContainer.RemoveAll()
			a.recordingsListContainer.Add(a.recordingsList)
			a.recordingsList.Refresh()
		}
		a.recordingsListContainer.Refresh()
	})
}

func (a *App) showRecordingsDrawerWithHighlight(patientDir string) {
	a.mu.Lock()
	wasVisible := a.recordingsDrawerVisible
	a.recordingsDrawerVisible = true
	a.mu.Unlock()

	go func() {
		a.showRecordingsListInDrawer()
		a.refreshRecordingsList()

		fyne.Do(func() {
			if !wasVisible {
				a.recordingsDrawerPanel.Show()
				a.window.Content().Refresh()
			}

			a.blinkRecordingItem(patientDir)
		})
	}()
}

func (a *App) blinkRecordingItem(patientDir string) {
	if a.recordingsList == nil {
		return
	}

	go func() {
		// Blink 3 times: toggle highlightedDir on/off and refresh list
		for i := 0; i < 3; i++ {
			a.mu.Lock()
			a.highlightedDir = patientDir
			a.mu.Unlock()
			fyne.Do(func() {
				a.recordingsList.Refresh()
			})
			time.Sleep(350 * time.Millisecond)

			a.mu.Lock()
			a.highlightedDir = ""
			a.mu.Unlock()
			fyne.Do(func() {
				a.recordingsList.Refresh()
			})
			time.Sleep(350 * time.Millisecond)
		}
	}()
}
