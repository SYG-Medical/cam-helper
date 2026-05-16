//go:build windows

package tray

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/getlantern/systray"

	"rtsp-virtual-cam-agent/internal/autostart"
	"rtsp-virtual-cam-agent/internal/config"
	"rtsp-virtual-cam-agent/internal/driver"
	"rtsp-virtual-cam-agent/internal/logging"
	"rtsp-virtual-cam-agent/internal/stream"
)

type App struct {
	cfg      config.Config
	cfgPath  string
	logger   *logging.Logger
	streamer *stream.Manager
	driver   *driver.Manager
	menu     menuItems
	mu       sync.Mutex
}

type menuItems struct {
	status     *systray.MenuItem
	start      *systray.MenuItem
	stop       *systray.MenuItem
	reload     *systray.MenuItem
	openConfig *systray.MenuItem
	openLogs   *systray.MenuItem
	toggleAuto *systray.MenuItem
	quit       *systray.MenuItem
}

func New() (*App, error) {
	cfg, cfgPath, err := config.LoadOrCreate()
	if err != nil {
		return nil, err
	}

	logger, err := logging.New()
	if err != nil {
		return nil, err
	}

	drv, err := driver.New(cfg, logger)
	if err != nil {
		return nil, err
	}

	streamer := stream.New(cfg, logger, drv)
	return &App{cfg: cfg, cfgPath: cfgPath, logger: logger, streamer: streamer, driver: drv}, nil
}

func (a *App) Run() error {
	systray.Run(a.onReady, a.onExit)
	return nil
}

func (a *App) onReady() {
	systray.SetTitle("SYG RTSP Cam")
	systray.SetTooltip("SYG RTSP Virtual Cam Agent")

	a.menu.status = systray.AddMenuItem("Status: starting", "Current pipeline state")
	a.menu.status.Disable()
	systray.AddSeparator()
	a.menu.start = systray.AddMenuItem("Start stream", "Start the RTSP pipeline")
	a.menu.stop = systray.AddMenuItem("Stop stream", "Stop the RTSP pipeline")
	a.menu.reload = systray.AddMenuItem("Reload config", "Reload config from AppData")
	systray.AddSeparator()
	a.menu.openConfig = systray.AddMenuItem("Open config", "Open config file in the default editor")
	a.menu.openLogs = systray.AddMenuItem("Open logs folder", "Open the logs directory")
	a.menu.toggleAuto = systray.AddMenuItem("Autostart: checking...", "Toggle Windows autostart")
	systray.AddSeparator()
	a.menu.quit = systray.AddMenuItem("Quit", "Quit the agent")

	a.refreshAutostartLabel()
	a.refreshStatusLabel()

	if a.cfg.AutoStart {
		if err := a.streamer.Start(); err != nil {
			a.logger.Printf("autostart stream failed: %v", err)
		}
	}

	go a.eventLoop()
	go a.statusLoop()
}

func (a *App) onExit() {
	a.streamer.Stop()
	_ = a.logger.Close()
}

func (a *App) eventLoop() {
	for {
		select {
		case <-a.menu.start.ClickedCh:
			if err := a.streamer.Start(); err != nil {
				a.logger.Printf("start failed: %v", err)
			}
		case <-a.menu.stop.ClickedCh:
			a.streamer.Stop()
		case <-a.menu.reload.ClickedCh:
			a.reloadConfig()
		case <-a.menu.openConfig.ClickedCh:
			openPath(a.cfgPath)
		case <-a.menu.openLogs.ClickedCh:
			if logDir, err := config.LogsDir(); err == nil {
				openPath(logDir)
			}
		case <-a.menu.toggleAuto.ClickedCh:
			a.toggleAutostart()
		case <-a.menu.quit.ClickedCh:
			systray.Quit()
			return
		}
	}
}

func (a *App) statusLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		a.refreshStatusLabel()
	}
}

func (a *App) refreshStatusLabel() {
	state := a.streamer.State()
	label := fmt.Sprintf("Status: %s", statusText(state))
	if state.RestartCount > 0 {
		label = fmt.Sprintf("%s | restarts: %d", label, state.RestartCount)
	}
	if state.LastError != "" {
		label = fmt.Sprintf("%s | last error: %s", label, state.LastError)
	}
	if a.menu.status != nil {
		a.menu.status.SetTitle(label)
	}
	systray.SetTooltip(label)
}

func (a *App) refreshAutostartLabel() {
	enabled, err := autostart.IsEnabled()
	if err != nil {
		a.menu.toggleAuto.SetTitle("Autostart: error")
		a.logger.Printf("autostart check failed: %v", err)
		return
	}
	if enabled {
		a.menu.toggleAuto.SetTitle("Disable autostart")
		return
	}
	a.menu.toggleAuto.SetTitle("Enable autostart")
}

func (a *App) toggleAutostart() {
	enabled, err := autostart.IsEnabled()
	if err != nil {
		a.logger.Printf("autostart check failed: %v", err)
		return
	}
	if err := autostart.SetEnabled(!enabled); err != nil {
		a.logger.Printf("autostart toggle failed: %v", err)
		return
	}
	a.refreshAutostartLabel()
}

func (a *App) reloadConfig() {
	cfg, cfgPath, err := config.LoadOrCreate()
	if err != nil {
		a.logger.Printf("reload config failed: %v", err)
		return
	}

	a.mu.Lock()
	a.cfg = cfg
	a.cfgPath = cfgPath
	a.mu.Unlock()
	a.driver.UpdateConfig(cfg)
	a.streamer.UpdateConfig(cfg)
	wasRunning := a.streamer.State().Running
	if wasRunning {
		a.streamer.Stop()
	}
	if cfg.AutoStart && !a.streamer.State().Running {
		if err := a.streamer.Start(); err != nil {
			a.logger.Printf("restart after reload failed: %v", err)
		}
	}
	a.refreshAutostartLabel()
	a.refreshStatusLabel()
}

func statusText(state stream.State) string {
	if !state.Running {
		return "stopped"
	}
	if state.LastError != "" {
		return "recovering"
	}
	return "running"
}

func openPath(target string) {
	if runtime.GOOS != "windows" {
		return
	}
	cleaned := filepath.Clean(target)
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", cleaned).Start()
}
