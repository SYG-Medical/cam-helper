package stream

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"rtsp-virtual-cam-agent/internal/config"
	"rtsp-virtual-cam-agent/internal/driver"
	"rtsp-virtual-cam-agent/internal/logging"
)

type State struct {
	Running      bool
	LastError    string
	StartedAt    time.Time
	RestartCount uint64
}

type Manager struct {
	cfg          config.Config
	logger       *logging.Logger
	driver       *driver.Manager
	mu           sync.Mutex
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	state        State
	restartCount uint64
	onFrame      func(width, height int, pix []byte)
	onFrameMu    sync.Mutex

	// HTTP MJPEG Server fields
	httpServer  *http.Server
	listeners   map[chan []byte]bool
	listenersMu sync.Mutex
	rgbaImg     *image.RGBA
	rgbaImgMu   sync.Mutex
}

func New(cfg config.Config, logger *logging.Logger, drv *driver.Manager) *Manager {
	m := &Manager{
		cfg:       cfg,
		logger:    logger,
		driver:    drv,
		listeners: make(map[chan []byte]bool),
	}
	m.startHTTPServer()
	return m
}

func (m *Manager) SetOnFrame(cb func(width, height int, pix []byte)) {
	m.onFrameMu.Lock()
	m.onFrame = cb
	m.onFrameMu.Unlock()
}

func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.state.Running {
		return nil
	}
	if strings.TrimSpace(m.cfg.RTSPURL) == "" {
		return errors.New("rtsp_url is empty")
	}

	if user, busy, err := m.driver.IsDeviceBusy(); err == nil && busy {
		return fmt.Errorf("virtual camera device is already in use by: %s", user)
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.state = State{Running: true, StartedAt: time.Now(), RestartCount: m.restartCount}
	m.wg.Add(1)
	go m.supervise(ctx)
	return nil
}

func (m *Manager) Stop() {
	m.mu.Lock()
	cancel := m.cancel
	m.cancel = nil
	m.mu.Unlock()

	if cancel != nil {
		cancel()
		m.wg.Wait()
	}

	m.mu.Lock()
	m.state.Running = false
	m.mu.Unlock()
}

func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *Manager) UpdateConfig(cfg config.Config) {
	m.mu.Lock()
	m.cfg = cfg
	m.mu.Unlock()
}

func (m *Manager) supervise(ctx context.Context) {
	defer m.wg.Done()

	for {
		select {
		case <-ctx.Done():
			m.logger.Printf("stream supervisor stopped")
			return
		default:
		}

		if err := m.driver.EnsureInstalled(ctx); err != nil {
			m.recordError(err)
			if !sleepOrDone(ctx, 8*time.Second) {
				return
			}
			continue
		}

		if user, busy, err := m.driver.IsDeviceBusy(); err == nil && busy {
			m.recordError(fmt.Errorf("virtual camera is in use by: %s", user))
			if !sleepOrDone(ctx, 5*time.Second) {
				return
			}
			continue
		}

		bridgeCmd, ffmpegCmd, err := m.preparePipeline(ctx)
		if err != nil {
			m.recordError(err)
			if !sleepOrDone(ctx, 8*time.Second) {
				return
			}
			continue
		}

		bridgeStarted := false
		if bridgeCmd != nil {
			m.logger.Printf("starting bridge: %s", strings.Join(bridgeCmd.Args, " "))
			if err := bridgeCmd.Start(); err != nil {
				m.recordError(fmt.Errorf("start bridge: %w", err))
				if !sleepOrDone(ctx, 8*time.Second) {
					return
				}
				continue
			}
			bridgeStarted = true
		}

		useBuiltinWriter := !m.driver.UseBridge()
		if useBuiltinWriter {
			stdout, err := ffmpegCmd.StdoutPipe()
			if err != nil {
				m.recordError(fmt.Errorf("ffmpeg stdout pipe: %w", err))
				if bridgeStarted {
					_ = bridgeCmd.Process.Kill()
				}
				if !sleepOrDone(ctx, 8*time.Second) {
					return
				}
				continue
			}
			go m.forwardFrames(ctx, stdout)

			// Still attach stderr logs
			stderr, err := ffmpegCmd.StderrPipe()
			if err == nil {
				go scanPipe("ffmpeg stderr", stderr, m.logger)
			}
		} else {
			attachLogs("ffmpeg", ffmpegCmd, m.logger)
		}

		m.logger.Printf("starting ffmpeg: %s", strings.Join(ffmpegCmd.Args, " "))
		if err := ffmpegCmd.Start(); err != nil {
			if bridgeStarted && bridgeCmd.Process != nil {
				_ = bridgeCmd.Process.Kill()
			}
			m.recordError(fmt.Errorf("start ffmpeg: %w", err))
			if !sleepOrDone(ctx, 8*time.Second) {
				return
			}
			continue
		}

		ffmpegDone := make(chan error, 1)
		bridgeDone := make(chan error, 1)
		go func() { ffmpegDone <- ffmpegCmd.Wait() }()
		if bridgeStarted {
			go func() { bridgeDone <- bridgeCmd.Wait() }()
		}

		m.recordRunning()

		var exitErr error
		select {
		case <-ctx.Done():
			_ = ffmpegCmd.Process.Kill()
			if bridgeStarted && bridgeCmd.Process != nil {
				_ = bridgeCmd.Process.Kill()
			}
			<-ffmpegDone
			if bridgeStarted {
				<-bridgeDone
			}
			return
		case err := <-ffmpegDone:
			exitErr = fmt.Errorf("ffmpeg exited: %w", err)
			if bridgeStarted && bridgeCmd.Process != nil {
				_ = bridgeCmd.Process.Kill()
				<-bridgeDone
			}
		case err := <-bridgeDone:
			exitErr = fmt.Errorf("bridge exited: %w", err)
			_ = ffmpegCmd.Process.Kill()
			<-ffmpegDone
		}

		m.mu.Lock()
		m.restartCount++
		m.mu.Unlock()
		m.recordError(exitErr)
		if !sleepOrDone(ctx, 5*time.Second) {
			return
		}
	}
}

func (m *Manager) preparePipeline(ctx context.Context) (*exec.Cmd, *exec.Cmd, error) {
	var bridgeCmd *exec.Cmd
	var err error
	if m.driver.UseBridge() {
		bridgeCmd, err = m.driver.StartBridge(ctx)
		if err != nil {
			return nil, nil, err
		}
		attachLogs("bridge", bridgeCmd, m.logger)
	}

	ffmpegCmd, err := m.buildFFmpegCommand(ctx)
	if err != nil {
		return nil, nil, err
	}

	return bridgeCmd, ffmpegCmd, nil
}

func (m *Manager) buildFFmpegCommand(ctx context.Context) (*exec.Cmd, error) {
	path, err := m.resolveFFmpegPath()
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	cfg := m.cfg
	m.mu.Unlock()

	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-rtsp_transport", "tcp",
		"-fflags", "+genpts",
		"-thread_queue_size", "1024",
		"-i", cfg.RTSPURL,
		"-an",
		"-vf", fmt.Sprintf("scale=%d:%d,fps=%d", cfg.Width, cfg.Height, cfg.FPS),
	}

	if m.driver.UseBridge() {
		args = append(args,
			"-pix_fmt", "yuv420p",
			"-f", "mpegts",
			m.driver.FFmpegOutputTarget(),
		)
	} else {
		if runtime.GOOS == "windows" {
			// On Windows, use rawvideo for builtin bridge
			args = append(args,
				"-pix_fmt", "bgra",
				"-f", "rawvideo",
				m.driver.FFmpegOutputTarget(),
			)
		} else {
			// On Linux, we output to both:
			// 1. The v4l2 device (using yuv420p format)
			// 2. stdout (using bgra rawvideo format) so Go can read it for live preview and MJPEG HTTP server
			args = append(args,
				"-pix_fmt", "yuv420p",
				"-f", "v4l2", m.driver.FFmpegOutputTarget(),
				"-pix_fmt", "bgra",
				"-f", "rawvideo", "-",
			)
		}
	}

	cmd := exec.CommandContext(ctx, path, args...)
	setHideWindow(cmd)
	return cmd, nil
}

func (m *Manager) resolveFFmpegPath() (string, error) {
	m.mu.Lock()
	candidate := m.cfg.FFmpegPath
	m.mu.Unlock()

	if runtime.GOOS != "windows" {
		if path, err := exec.LookPath("ffmpeg"); err == nil {
			return path, nil
		}
	}

	if path, err := exec.LookPath(candidate); err == nil {
		return path, nil
	}
	if runtime.GOOS != "windows" && strings.HasSuffix(strings.ToLower(candidate), "ffmpeg.exe") {
		if path, err := exec.LookPath("ffmpeg"); err == nil {
			return path, nil
		}
	}

	if filepath.IsAbs(candidate) {
		if _, err := os.Stat(candidate); err != nil {
			return "", fmt.Errorf("ffmpeg missing: %s", candidate)
		}
		return candidate, nil
	}

	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	resolved := filepath.Join(filepath.Dir(exe), filepath.FromSlash(candidate))
	if _, err := os.Stat(resolved); err != nil {
		return "", fmt.Errorf("ffmpeg missing: %s", resolved)
	}
	return resolved, nil
}

func (m *Manager) recordRunning() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = State{
		Running:      true,
		StartedAt:    time.Now(),
		RestartCount: m.restartCount,
	}
}

func (m *Manager) recordError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = State{
		Running:      true,
		LastError:    errorString(err),
		StartedAt:    m.state.StartedAt,
		RestartCount: m.restartCount,
	}
	if err != nil {
		m.logger.Printf("pipeline error: %v", err)
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func sleepOrDone(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func attachLogs(prefix string, cmd *exec.Cmd, logger *logging.Logger) {
	stdout, err := cmd.StdoutPipe()
	if err == nil {
		go scanPipe(prefix+" stdout", stdout, logger)
	} else {
		logger.Printf("%s stdout unavailable: %v", prefix, err)
	}

	stderr, err := cmd.StderrPipe()
	if err == nil {
		go scanPipe(prefix+" stderr", stderr, logger)
	} else {
		logger.Printf("%s stderr unavailable: %v", prefix, err)
	}
}

func scanPipe(prefix string, r io.Reader, logger *logging.Logger) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		logger.Printf("%s: %s", prefix, scanner.Text())
	}
}

func (m *Manager) forwardFrames(ctx context.Context, r io.Reader) {
	m.mu.Lock()
	cfg := m.cfg
	m.mu.Unlock()

	frameSize := cfg.Width * cfg.Height * 4
	buf := make([]byte, frameSize)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		_, err := io.ReadFull(r, buf)
		if err != nil {
			if err != io.EOF && !errors.Is(err, os.ErrClosed) {
				m.logger.Printf("frame reader error: %v", err)
			}
			return
		}

		if err := m.driver.WriteFrame(cfg.Width, cfg.Height, buf); err != nil {
			// ErrNoConsumer is expected when no app has the virtual camera
			// open yet — suppress it to avoid log spam.
			if !errors.Is(err, driver.ErrNoConsumer) {
				m.logger.Printf("write frame error: %v", err)
			}
		}

		m.broadcastFrame(cfg.Width, cfg.Height, buf)

		m.onFrameMu.Lock()
		cb := m.onFrame
		m.onFrameMu.Unlock()
		if cb != nil {
			cb(cfg.Width, cfg.Height, append([]byte(nil), buf...))
		}
	}
}

func (m *Manager) broadcastFrame(width, height int, pix []byte) {
	m.rgbaImgMu.Lock()
	if m.rgbaImg == nil || m.rgbaImg.Rect.Dx() != width || m.rgbaImg.Rect.Dy() != height {
		m.rgbaImg = image.NewRGBA(image.Rect(0, 0, width, height))
	}
	// Convert BGRA to RGBA
	for i := 0; i < len(pix); i += 4 {
		m.rgbaImg.Pix[i]   = pix[i+2] // R
		m.rgbaImg.Pix[i+1] = pix[i+1] // G
		m.rgbaImg.Pix[i+2] = pix[i]   // B
		m.rgbaImg.Pix[i+3] = pix[i+3] // A
	}

	var buf bytes.Buffer
	err := jpeg.Encode(&buf, m.rgbaImg, &jpeg.Options{Quality: 75})
	m.rgbaImgMu.Unlock()

	if err != nil {
		return
	}

	jpegBytes := buf.Bytes()

	m.listenersMu.Lock()
	defer m.listenersMu.Unlock()
	for ch := range m.listeners {
		select {
		case ch <- jpegBytes:
		default:
			// Client slow, drop frame
		}
	}
}

func (m *Manager) startHTTPServer() {
	port := m.cfg.BridgePort
	if port <= 0 {
		port = 18080
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","service":"SYG RTSP Agent"}`))
	})

	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
		w.Header().Set("Cache-Control", "no-cache, private, max-age=0, no-transform, no-store, must-revalidate")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")

		ch := make(chan []byte, 5)
		m.listenersMu.Lock()
		m.listeners[ch] = true
		m.listenersMu.Unlock()

		defer func() {
			m.listenersMu.Lock()
			delete(m.listeners, ch)
			m.listenersMu.Unlock()
			close(ch)
		}()

		cn := r.Context()

		for {
			select {
			case <-cn.Done():
				return
			case jpegBytes, ok := <-ch:
				if !ok {
					return
				}
				_, err := fmt.Fprintf(w, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(jpegBytes))
				if err != nil {
					return
				}
				_, err = w.Write(jpegBytes)
				if err != nil {
					return
				}
				_, err = w.Write([]byte("\r\n"))
				if err != nil {
					return
				}
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
			}
		}
	})

	m.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	m.logger.Printf("Starting HTTP MJPEG server on port %d...", port)
	go func() {
		if err := m.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			m.logger.Printf("HTTP server error: %v", err)
		}
	}()
}
