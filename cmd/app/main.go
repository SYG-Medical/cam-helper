package main

import (
	"log"
	"os"
	"path/filepath"
	"runtime"

	"nystavision/internal/assets"
	"nystavision/internal/singleinstance"
	"nystavision/internal/tray"

	"github.com/go-gl/glfw/v3.3/glfw"
)

func init() {
	// GLFW functions must be called on the main thread.
	runtime.LockOSThread()
}

func main() {
	// Ensure only one instance runs at a time
	first, err := singleinstance.Acquire()
	if err != nil {
		log.Printf("single-instance check error: %v", err)
	}
	if !first {
		// Another instance is already running — exit silently
		os.Exit(0)
	}
	defer singleinstance.Release()

	if runtime.GOOS == "windows" {
		exe, err := os.Executable()
		if err == nil {
			_ = assets.ExtractAll(filepath.Dir(exe))
		}
	}
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		log.Println("warning: this app targets Windows runtime behavior; launching in compatibility mode")
	}

	// Fallback to software rendering if GPU acceleration is not available
	if runtime.GOOS == "linux" || runtime.GOOS == "windows" {
		if err := glfw.Init(); err != nil {
			log.Printf("GPU acceleration initialization failed: %v. Falling back to software rendering.", err)
			os.Setenv("FYNE_RENDER", "software")
			os.Setenv("LIBGL_ALWAYS_SOFTWARE", "1")
		} else {
			glfw.Terminate()
		}
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
