//go:build !windows

package tray

import (
	"os"
	"os/signal"
	"syscall"

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
	a.logger.Printf("starting headless mode with config %s", a.cfgPath)
	if err := a.streamer.Start(); err != nil {
		return err
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	a.streamer.Stop()
	return a.logger.Close()
}
