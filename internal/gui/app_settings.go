package gui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"nystavision/internal/autostart"
	"nystavision/internal/config"
	"nystavision/internal/i18n"
	"nystavision/internal/system"
	"nystavision/internal/ui"
	"nystavision/internal/version"
)

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

		a.mu.Lock()
		a.cfg.Language = newLang
		_ = config.Save(*a.cfg, a.cfgPath)
		a.mu.Unlock()

		i18n.Init(newLang)

		if a.settingsDialog != nil {
			a.settingsDialog.Hide()
			a.settingsDialog = nil
		}

		a.reloadUI()
	}

	configBtn := widget.NewButtonWithIcon(i18n.T("btn_open_config"), theme.SettingsIcon(), func() {
		system.OpenPath(a.cfgPath)
	})

	logsBtn := widget.NewButtonWithIcon(i18n.T("btn_show_logs"), theme.FolderOpenIcon(), func() {
		if logDir, err := config.LogsDir(); err == nil {
			system.OpenPath(logDir)
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

func (a *App) reloadUI() {
	a.mu.Lock()
	drawerVisible := a.drawerVisible
	recordingsVisible := a.recordingsDrawerVisible
	a.mu.Unlock()

	// Translate camera names in the configuration based on new language settings
	a.translateCameraNames()

	// Rebuild widgets
	toolbar := a.buildToolbar()
	toolbarBg := canvas.NewRectangle(colorCardSurface)
	toolbarBg.CornerRadius = 8
	toolbarCard := container.NewStack(toolbarBg, container.NewPadded(toolbar))

	a.buildStatusBar()

	// Rebuild grid
	a.rebuildGrid()

	// Rebuild drawers
	a.buildLayoutDrawer()
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

	// Restore drawer visibilities
	a.mu.Lock()
	a.drawerVisible = drawerVisible
	a.recordingsDrawerVisible = recordingsVisible
	a.mu.Unlock()

	if drawerVisible {
		a.refreshLayoutList()
		a.drawerPanel.Show()
	}
	if recordingsVisible {
		a.showRecordingsListInDrawer()
		go a.refreshRecordingsList()
		a.recordingsDrawerPanel.Show()
	}

	a.window.Content().Refresh()
}

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
		if a.addBtn != nil {
			a.addBtn.Enable()
		}
		if a.removeBtn != nil {
			a.removeBtn.Enable()
		}
		if a.startStopAllBtn != nil {
			a.startStopAllBtn.Enable()
		}
		if a.settingsRef != nil {
			a.settingsRef.Enable()
		}
		if a.layoutsRef != nil {
			a.layoutsRef.Enable()
		}
		if a.recordBtn != nil {
			a.recordBtn.Enable()
		}
		a.updateRecordBtn()
	} else {
		if a.addBtn != nil {
			a.addBtn.Disable()
		}
		if a.removeBtn != nil {
			a.removeBtn.Disable()
		}
		if a.startStopAllBtn != nil {
			a.startStopAllBtn.Disable()
		}
		if a.settingsRef != nil {
			a.settingsRef.Disable()
		}
		if a.layoutsRef != nil {
			a.layoutsRef.Disable()
		}
		if a.recordBtn != nil {
			a.recordBtn.Disable()
		}
	}
}
