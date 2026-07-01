package gui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"nystavision/internal/i18n"
	"nystavision/internal/stream"
)

func (a *App) showPatientInfoDrawerAfterRecording(session *stream.RecordingSession, compositeRec *stream.CompositeRecorder) {
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

	var dlg dialog.Dialog

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

		actualTempDir := filepath.Dir(session.TempDir)

		dlg.Hide()
		
		a.postProcessRecording(session, compositeRec, actualTempDir, finalPatientDir, note)
	})

	saveBtn.Importance = widget.HighImportance

	content := container.NewVBox(
		widget.NewForm(
			widget.NewFormItem(i18n.T("record_patient_label"), nameEntry),
			widget.NewFormItem(i18n.T("record_patient_id_label"), patientIDEntry),
			widget.NewFormItem(i18n.T("record_patient_history_label"), noteEntry),
		),
		widget.NewSeparator(),
		container.NewHBox(layout.NewSpacer(), saveBtn),
	)

	dlg = dialog.NewCustomWithoutButtons("Hasta Bilgileri", content, a.window)
	dlg.Resize(fyne.NewSize(400, 350))
	dlg.Show()
}

func (a *App) showPatientInfoDialog(info stream.PatientInfo) {
	details := fmt.Sprintf("Ad: %s\nID: %s\nTarih: %s\n\nManevralar:\n", 
		info.Name, info.PatientID, info.RecordDate.Format("02.01.2006 15:04"))
	
	for i, v := range info.Videos {
		if v.Type == "general" {
			details += fmt.Sprintf("%d. Manevra: %s\n", i+1, v.Note)
		}
	}
	
	dialog.ShowInformation("Hasta Bilgileri", details, a.window)
}

func (a *App) showEditPatientDialog(dir string, info stream.PatientInfo) {
	nameEntry := widget.NewEntry()
	nameEntry.SetText(info.Name)
	
	patientIDEntry := widget.NewEntry()
	patientIDEntry.SetText(info.PatientID)
	
	// Create entries for notes
	var noteEntries []*widget.Entry
	var formItems []*widget.FormItem
	
	formItems = append(formItems, widget.NewFormItem("Ad", nameEntry))
	formItems = append(formItems, widget.NewFormItem("ID", patientIDEntry))
	
	for _, v := range info.Videos {
		if v.Type == "general" {
			noteEntry := widget.NewMultiLineEntry()
			noteEntry.SetText(v.Note)
			noteEntry.SetMinRowsVisible(2)
			noteEntries = append(noteEntries, noteEntry)
			formItems = append(formItems, widget.NewFormItem(fmt.Sprintf("%d. Manevra Notu", len(noteEntries)), noteEntry))
		}
	}
	
	form := widget.NewForm(formItems...)
	
	d := dialog.NewCustomConfirm("Hasta Düzenle", "Kaydet", "İptal", container.NewVScroll(form), func(ok bool) {
		if ok {
			info.Name = strings.TrimSpace(nameEntry.Text)
			info.PatientID = strings.TrimSpace(patientIDEntry.Text)
			
			noteIdx := 0
			for i, v := range info.Videos {
				if v.Type == "general" {
					info.Videos[i].Note = strings.TrimSpace(noteEntries[noteIdx].Text)
					noteIdx++
				}
			}
			
			if err := stream.SavePatientInfo(dir, info); err != nil {
				dialog.ShowError(err, a.window)
			} else {
				a.refreshRecordingsList()
			}
		}
	}, a.window)
	d.Resize(fyne.NewSize(400, 500))
	d.Show()
}
