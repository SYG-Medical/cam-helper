package main

import (
	"log"
	"os"
	"path/filepath"
	"runtime"

	"nystavision/internal/assets"
	"nystavision/internal/singleinstance"
	"nystavision/internal/gui"

	"github.com/go-gl/glfw/v3.3/glfw"
)

func init() {
	// GLFW functions must be called on the main thread.
	runtime.LockOSThread()
}

func main() {
	// Ensure only one instance runs at a time
	log.Println("[main] Checking for running instance...")
	ipc, err := singleinstance.NewIPCManager()
	if err != nil {
		log.Printf("[main] single-instance init error: %v", err)
	}
	if ipc != nil {
		first, err := ipc.Listen()
		if err != nil {
			log.Printf("[main] single-instance check error: %v", err)
		}
		if !first {
			log.Println("[main] Another instance is already running. Notifying primary instance...")
			if notifyErr := ipc.NotifyPrimary(); notifyErr != nil {
				log.Printf("[main] Failed to notify primary instance: %v", notifyErr)
			} else {
				log.Println("[main] Successfully notified primary instance.")
			}
			os.Exit(0)
		}
		log.Println("[main] This is the primary instance. Starting wakeup listener...")
		defer ipc.Close()

		// Start listening for wakeups in the background
		go func() {
			for {
				ok, err := ipc.AcceptWakeup()
				if err != nil {
					log.Printf("[main] AcceptWakeup error (terminating loop): %v", err)
					break
				}
				log.Printf("[main] AcceptWakeup connection received, matches wakeup payload: %v", ok)
				if ok {
					singleinstance.TriggerActivate()
				}
			}
		}()
	}

	if runtime.GOOS == "windows" {
		exe, err := os.Executable()
		if err == nil {
			_ = assets.ExtractAll(filepath.Dir(exe))
		}
	}
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		log.Println("warning: launching in compatibility mode for unsupported OS")
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

	app, err := gui.New()
	if err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}

	singleinstance.SetActivateCallback(func() {
		app.ShowAndFocus()
	})

	if err := app.Run(); err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}
}
