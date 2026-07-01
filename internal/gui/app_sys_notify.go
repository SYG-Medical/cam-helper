package gui

import (
	"log"
	"runtime"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"

	"nystavision/internal/i18n"
	"nystavision/internal/system"
)

func (a *App) setupTray() {
	log.Println("[App] Setting up system tray...")
	desk, ok := a.fyneApp.(desktop.App)
	if !ok {
		log.Println("[App] WARNING: fyneApp does not implement desktop.App! System tray will not be created.")
		return
	}

	if runtime.GOOS == "linux" {
		if err := system.InstallLinuxIcon(iconData); err != nil {
			log.Printf("[App] Failed to install Linux desktop icon: %v", err)
		} else {
			log.Println("[App] Linux desktop icon installed/verified successfully.")
		}
	}

	m := fyne.NewMenu(i18n.T("title_app"),
		fyne.NewMenuItem(i18n.T("tray_show"), func() {
			log.Println("[App] Tray menu: Show clicked")
			a.window.Show()
			a.window.RequestFocus()
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem(i18n.T("tray_start_all"), func() {
			log.Println("[App] Tray menu: Start All clicked")
			a.rtspUIStopped = false
			a.multiManager.StartAll()
		}),
		fyne.NewMenuItem(i18n.T("tray_stop_all"), func() {
			log.Println("[App] Tray menu: Stop All clicked")
			a.rtspUIStopped = true
			a.multiManager.StopAll()
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem(i18n.T("tray_quit"), func() {
			log.Println("[App] Tray menu: Quit clicked")
			a.Quit()
		}),
	)

	desk.SetSystemTrayMenu(m)
	log.Printf("[App] System tray menu set. Embedded icon size: %d bytes", len(iconData))
	desk.SetSystemTrayIcon(fyne.NewStaticResource("icon.png", iconData))
	log.Println("[App] System tray icon set successfully.")
}

func (a *App) minimizeToBackground() {
	a.rtspUIStopped = true
	a.multiManager.StopAll()
	a.updateStartStopAllBtn(false)

	if a.isRecording {
		a.stopRecording()
		// Give a moment for post-processing dialog to appear before hiding
		time.AfterFunc(300*time.Millisecond, func() {
			fyne.Do(func() {
				a.window.Hide()
			})
		})
	} else {
		a.window.Hide()
	}
}

func (a *App) sendOSNotification(title, message string) {
	appName := i18n.T("title_app")
	if appName == "" {
		appName = "NystaVision"
	}
	fullTitle := appName
	if title != "" && title != appName {
		fullTitle = appName + " - " + title
	}

	if runtime.GOOS == "linux" {
		if err := system.SendLinuxDBusNotification(fullTitle, message, 4000); err == nil {
			return
		}
	}

	a.fyneApp.SendNotification(fyne.NewNotification(fullTitle, message))
}
