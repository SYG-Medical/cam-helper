package gui

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"nystavision/internal/i18n"
	"nystavision/internal/stream"
)

func (a *App) showNextPendingRecording() {
	a.mu.Lock()
	if len(a.pendingRecordings) == 0 {
		a.mu.Unlock()
		a.showRecordingsListInDrawer()
		return
	}
	pending := a.pendingRecordings[0]
	pendingCount := len(a.pendingRecordings)
	a.mu.Unlock()

	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder(i18n.T("record_patient_placeholder"))

	patientIDEntry := widget.NewEntry()
	patientIDEntry.SetPlaceHolder(i18n.T("record_patient_id_placeholder"))

	noteEntry := widget.NewMultiLineEntry()
	noteEntry.SetPlaceHolder(i18n.T("record_patient_history_placeholder"))
	noteEntry.SetMinRowsVisible(3)

	// Pre-fill fields from cache if valid
	cachedName, cachedPatientID, cachedDir, _, cacheValid := a.patientCache.Get()
	if cacheValid {
		nameEntry.Text = cachedName
		patientIDEntry.Text = cachedPatientID
	}

	saveBtn := widget.NewButton("Kaydet / Devam", func() {
		patientName := strings.TrimSpace(nameEntry.Text)
		patientID := strings.TrimSpace(patientIDEntry.Text)
		note := strings.TrimSpace(noteEntry.Text)

		if patientName == "" {
			dialog.ShowInformation("Hata", "Lütfen hasta adını giriniz.", a.window)
			return
		}

		var finalPatientDir string

		// If cache is valid and user did not change the name, reuse the directory and increment record count
		if cacheValid && patientName == cachedName {
			finalPatientDir = cachedDir
			_ = a.patientCache.IncrementRecordCount()
		} else {
			// Find or create directory
			finalPatientDir = stream.GetOutputDir(a.cfg.RecordingsDir, patientName)
			a.patientCache.Store(patientName, patientID, finalPatientDir)
		}

		// Ensure directory exists
		_ = os.MkdirAll(finalPatientDir, 0o755)

		// Create PatientInfo if not exists, or load existing
		info, err := stream.LoadPatientInfo(finalPatientDir)
		if err != nil {
			info = stream.PatientInfo{
				Name:       patientName,
				PatientID:  patientID,
				TC:         patientID, // For backwards compatibility
				RecordDate: time.Now(),
			}
			_ = stream.SavePatientInfo(finalPatientDir, info)
		}

		// Pop from queue
		a.mu.Lock()
		if len(a.pendingRecordings) > 0 {
			a.pendingRecordings = a.pendingRecordings[1:]
			_ = stream.SavePendingQueue(a.cfg.RecordingsDir, a.pendingRecordings)
		}
		a.mu.Unlock()
		a.updateRecordingsBadge()

		a.postProcessRecording(pending, finalPatientDir, note)
		
		// Immediately process next if available, or go back to list
		a.showNextPendingRecording()
	})

	saveBtn.Importance = widget.HighImportance

	// Form Title with Sequence/Count
	titleText := "Hasta Bilgileri"
	if pendingCount > 1 {
		titleText = fmt.Sprintf("Hasta Bilgileri (Bekleyen: %d)", pendingCount)
	}
	titleLabel := canvas.NewText(titleText, colorTextPrimary)
	titleLabel.TextSize = 15
	titleLabel.TextStyle = fyne.TextStyle{Bold: true}

	dateStr := pending.Timestamp.Format("02.01.2006 15:04:05")
	dateLabel := canvas.NewText(fmt.Sprintf("Kayıt Tarihi: %s", dateStr), colorTextSecondary)
	dateLabel.TextSize = 12

	formHeader := container.NewVBox(
		container.NewPadded(container.NewVBox(titleLabel, dateLabel)),
		widget.NewSeparator(),
	)

	formContent := container.NewBorder(
		formHeader,
		container.NewVBox(widget.NewSeparator(), container.NewPadded(saveBtn)),
		nil, nil,
		container.NewVScroll(container.NewVBox(
			widget.NewForm(
				widget.NewFormItem(i18n.T("record_patient_label"), nameEntry),
				widget.NewFormItem(i18n.T("record_patient_id_label"), patientIDEntry),
				widget.NewFormItem(i18n.T("record_patient_history_label"), noteEntry),
			),
		)),
	)

	drawerBg := canvas.NewRectangle(colorCardSurface)
	drawerSpacer := canvas.NewRectangle(color.Transparent)
	drawerSpacer.SetMinSize(fyne.NewSize(280, 0))

	a.mu.Lock()
	a.recordingsDrawerVisible = true
	a.mu.Unlock()

	fyne.Do(func() {
		a.recordingsDrawerPanel.RemoveAll()
		a.recordingsDrawerPanel.Add(drawerBg)
		a.recordingsDrawerPanel.Add(drawerSpacer)
		a.recordingsDrawerPanel.Add(formContent)
		a.recordingsDrawerPanel.Show()
		a.recordingsDrawerPanel.Refresh()
		a.window.Content().Refresh()
	})
}

// showPatientInfoDialog shows a read-only summary of patient info, using Maneuvers.
func (a *App) showPatientInfoDialog(info stream.PatientInfo) {
	details := fmt.Sprintf("Ad: %s\nID: %s\nTarih: %s\n",
		info.Name, info.PatientID, info.RecordDate.Format("02.01.2006 15:04"))

	if len(info.Maneuvers) > 0 {
		details += "\nManevralar:\n"
		for _, m := range info.Maneuvers {
			note := m.Note
			if note == "" {
				note = "—"
			}
			details += fmt.Sprintf("  %d. Manevra: %s\n", m.Index, note)
			for _, v := range m.Videos {
				if v.Type == "camera" && v.CameraType != "" {
					details += fmt.Sprintf("     └ %s (%s)\n", v.FileName, v.Camera)
				}
			}
		}
	} else if len(info.Videos) > 0 {
		// Legacy format fallback
		details += "\nVideolar:\n"
		for _, v := range info.Videos {
			details += fmt.Sprintf("  - %s (%s)\n", v.FileName, v.Type)
		}
	}

	label := widget.NewLabel(details)
	label.Wrapping = fyne.TextWrapWord
	scroll := container.NewVScroll(label)
	scroll.SetMinSize(fyne.NewSize(420, 320))

	d := dialog.NewCustom("Hasta Bilgileri", "Kapat", scroll, a.window)
	d.Show()
}

// showEditPatientDialog allows editing patient name, ID, and per-maneuver notes.
func (a *App) showEditPatientDialog(dir string, info stream.PatientInfo, onSaved func(updated stream.PatientInfo, newDir string)) {
	nameEntry := widget.NewEntry()
	nameEntry.SetText(info.Name)

	patientIDEntry := widget.NewEntry()
	patientIDEntry.SetText(info.PatientID)

	var formItems []*widget.FormItem
	formItems = append(formItems, widget.NewFormItem("Ad", nameEntry))
	formItems = append(formItems, widget.NewFormItem("ID", patientIDEntry))

	// Collect note entries per maneuver
	noteEntries := make([]*widget.Entry, len(info.Maneuvers))
	for i, m := range info.Maneuvers {
		noteEntry := widget.NewMultiLineEntry()
		noteEntry.SetText(m.Note)
		noteEntry.SetMinRowsVisible(2)
		noteEntries[i] = noteEntry

		// Build video summary for this maneuver
		vidSummary := ""
		for _, v := range m.Videos {
			cam := v.Camera
			if cam == "" {
				cam = v.Type
			}
			vidSummary += "• " + v.FileName + " (" + cam + ")\n"
		}

		label := fmt.Sprintf("%d. Manevra", m.Index)
		formItems = append(formItems, widget.NewFormItem(label+" Notu", noteEntry))
		if vidSummary != "" {
			summaryLbl := widget.NewLabel(strings.TrimRight(vidSummary, "\n"))
			summaryLbl.Wrapping = fyne.TextWrapBreak
			formItems = append(formItems, widget.NewFormItem(label+" Videoları", summaryLbl))
		}
	}

	// Legacy: if no maneuvers but flat videos exist, show them for reference
	if len(info.Maneuvers) == 0 && len(info.Videos) > 0 {
		for i, v := range info.Videos {
			if v.Type == "general" {
				noteEntry := widget.NewMultiLineEntry()
				noteEntry.SetText(v.Note)
				noteEntry.SetMinRowsVisible(2)
				noteEntries = append(noteEntries, noteEntry)
				formItems = append(formItems, widget.NewFormItem(fmt.Sprintf("%d. Manevra Notu", i+1), noteEntry))
			}
		}
	}

	form := widget.NewForm(formItems...)
	scrolled := container.NewVScroll(form)
	scrolled.SetMinSize(fyne.NewSize(380, 400))

	d := dialog.NewCustomConfirm("Hasta Düzenle", "Kaydet", "İptal", scrolled, func(ok bool) {
		if !ok {
			return
		}
		
		oldDir := dir
		originalName := info.Name
		
		info.Name = strings.TrimSpace(nameEntry.Text)
		info.PatientID = strings.TrimSpace(patientIDEntry.Text)

		// Update maneuver notes
		for i := range info.Maneuvers {
			if i < len(noteEntries) && noteEntries[i] != nil {
				info.Maneuvers[i].Note = strings.TrimSpace(noteEntries[i].Text)
			}
		}

		// Legacy: update flat Videos notes if no maneuvers
		if len(info.Maneuvers) == 0 {
			noteIdx := 0
			for i, v := range info.Videos {
				if v.Type == "general" && noteIdx < len(noteEntries) {
					info.Videos[i].Note = strings.TrimSpace(noteEntries[noteIdx].Text)
					noteIdx++
				}
			}
		}

		newDir := oldDir
		if info.Name != originalName && info.Name != "" {
			oldBase := filepath.Base(oldDir)
			parts := strings.Split(oldBase, "_")
			suffix := ""
			if len(parts) >= 2 {
				suffix = "_" + strings.Join(parts[1:], "_")
			} else {
				suffix = "_" + info.RecordDate.Format("20060102")
			}
			safeNewName := stream.SanitizeFilename(info.Name)
			if safeNewName == "" {
				safeNewName = "isimsiz"
			}
			newDir = filepath.Join(filepath.Dir(oldDir), safeNewName+suffix)
			if newDir != oldDir {
				if err := os.Rename(oldDir, newDir); err == nil {
					dir = newDir
				} else {
					newDir = oldDir
				}
			}
		}

		if err := stream.SavePatientInfo(dir, info); err != nil {
			dialog.ShowError(err, a.window)
		} else {
			if onSaved != nil {
				onSaved(info, newDir)
			}
		}
	}, a.window)
	d.Resize(fyne.NewSize(420, 520))
	d.Show()
}
