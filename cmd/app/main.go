package main

import (
	"log"
	"os"
	"path/filepath"
	"runtime"

	"rtsp-virtual-cam-agent/internal/assets"
	"rtsp-virtual-cam-agent/internal/tray"
)

func main() {
	if runtime.GOOS == "windows" {
		exe, err := os.Executable()
		if err == nil {
			_ = assets.ExtractAll(filepath.Dir(exe))
		}
	}
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		log.Println("warning: this app targets Windows runtime behavior; launching in compatibility mode")
	}

	app, err := tray.New()
	if err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}

	if err := app.Run(); err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}
}
