package gui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"nystavision/internal/config"
	"nystavision/internal/i18n"
)

func (a *App) showSetupScreen(onDone func()) {
	var buildContent func() fyne.CanvasObject

	buildContent = func() fyne.CanvasObject {
		// Backgrounds
		bg := canvas.NewRectangle(colorDarkSurface)

		// Logo image
		logoRes := fyne.NewStaticResource("logo.png", logoData)
		logoImg := canvas.NewImageFromResource(logoRes)
		logoImg.FillMode = canvas.ImageFillContain
		logoImg.SetMinSize(fyne.NewSize(280, 111))

		// Title and description
		titleText := i18n.T("setup_welcome_title")
		welcomeTitle := canvas.NewText(titleText, colorMedicalBlue)
		welcomeTitle.TextSize = 22
		welcomeTitle.TextStyle = fyne.TextStyle{Bold: true}
		welcomeTitle.Alignment = fyne.TextAlignCenter

		welcomeDesc := widget.NewLabel(i18n.T("setup_welcome_desc"))
		welcomeDesc.Wrapping = fyne.TextWrapWord
		welcomeDesc.Alignment = fyne.TextAlignCenter

		// Language Selector
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
			a.mu.Unlock()

			i18n.Init(newLang)

			// Rebuild setup UI content with the new language
			a.window.SetContent(buildContent())
		}

		// Recordings Directory Picker
		recordingsDirEntry := widget.NewEntry()
		recordingsDirEntry.SetText(a.cfg.RecordingsDir)
		recordingsDirEntry.Disable() // force using the browser button

		browseBtn := widget.NewButtonWithIcon("", theme.FolderOpenIcon(), func() {
			dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
				if err != nil || uri == nil {
					return
				}
				recordingsDirEntry.SetText(uri.Path())
			}, a.window)
		})

		recordingsRow := container.NewBorder(nil, nil, widget.NewLabel(i18n.T("lbl_recordings_dir")+": "), browseBtn, recordingsDirEntry)

		// Composite Recording checkbox
		compositeCheck := widget.NewCheck(i18n.T("lbl_composite_recording"), nil)
		compositeCheck.SetChecked(a.cfg.CompositeRecording)

		// Form panel
		formPanel := container.NewVBox(
			container.NewBorder(nil, nil, widget.NewLabel(i18n.T("lbl_language")+": "), nil, langSelect),
			recordingsRow,
			compositeCheck,
		)

		// Complete setup action button
		completeBtn := widget.NewButtonWithIcon(i18n.T("btn_complete_setup"), theme.ConfirmIcon(), func() {
			selectedLang := "en"
			if code, ok := langMap[langSelect.Selected]; ok {
				selectedLang = code
			}

			a.mu.Lock()
			a.cfg.Language = selectedLang
			a.cfg.RecordingsDir = recordingsDirEntry.Text
			a.cfg.CompositeRecording = compositeCheck.Checked
			a.cfg.SetupCompleted = true
			_ = config.Save(*a.cfg, a.cfgPath)
			a.mu.Unlock()

			// Initialize and transition to the main application
			a.setupUI()
			a.setupTray()
			a.setupFrameCallbacks()

			onDone()
		})
		completeBtn.Importance = widget.HighImportance

		// Card panel wrapping the form and details
		cardBg := canvas.NewRectangle(colorCardSurface)
		cardBg.CornerRadius = 12

		cardContent := container.NewVBox(
			container.NewCenter(logoImg),
			layout.NewSpacer(),
			welcomeTitle,
			container.NewPadded(welcomeDesc),
			widget.NewSeparator(),
			formPanel,
			layout.NewSpacer(),
			widget.NewSeparator(),
			container.NewPadded(completeBtn),
		)

		card := container.NewStack(cardBg, container.NewPadded(cardContent))

		// Set min size for the setup card to look premium
		cardSpacer := canvas.NewRectangle(color.Transparent)
		cardSpacer.SetMinSize(fyne.NewSize(500, 440))
		cardWrapper := container.NewStack(cardSpacer, card)

		// Center the card in the window
		mainLayout := container.NewStack(bg, container.NewCenter(cardWrapper))
		return mainLayout
	}

	a.window.SetContent(buildContent())
}
