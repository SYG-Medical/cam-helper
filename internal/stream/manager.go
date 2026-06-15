package stream

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
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

	"nystavision/internal/config"
	"nystavision/internal/logging"
)

type State struct {
	Running      bool
	LastError    string
	StartedAt    time.Time
	RestartCount uint64
}

// Manager handles a single camera stream pipeline (FFmpeg process).
type Manager struct {
	cam          config.CameraSource
	globalCfg    config.Config
	logger       *logging.Logger
	mu           sync.Mutex
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	state        State
	restartCount uint64
	onFrame      func(width, height int, pix []byte)
	onFrameMu    sync.Mutex

	// HTTP MJPEG Server fields (only used when this camera is the RTSP server camera)
	httpServer      *http.Server
	listeners       map[chan []byte]bool
	listenersMu     sync.Mutex
	rgbaImg         *image.RGBA
	rgbaImgMu       sync.Mutex
	lastPreviewTime time.Time

	enableHTTPServer bool // whether this manager should start the HTTP MJPEG server
}

// NewFromCamera creates a Manager for a specific camera source.
func NewFromCamera(cam config.CameraSource, globalCfg config.Config, logger *logging.Logger, enableHTTP bool) *Manager {
	m := &Manager{
		cam:              cam,
		globalCfg:        globalCfg,
		logger:           logger,
		listeners:        make(map[chan []byte]bool),
		enableHTTPServer: enableHTTP,
	}
	if enableHTTP {
		m.startHTTPServer()
	}
	return m
}

// New creates a Manager using the legacy single-camera approach.
// Kept for backward compatibility.
func New(cfg config.Config, logger *logging.Logger) *Manager {
	cam := config.CameraSource{
		ID:      "legacy",
		Name:    "Default",
		Type:    "rtsp",
		Width:   1280,
		Height:  720,
		FPS:     30,
		Enabled: true,
	}
	if len(cfg.Cameras) > 0 {
		cam = cfg.Cameras[0]
	}
	return NewFromCamera(cam, cfg, logger, true)
}

func (m *Manager) CameraID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cam.ID
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

	// Validate source
	switch m.cam.Type {
	case "rtsp":
		if strings.TrimSpace(m.cam.RTSPURL) == "" {
			return fmt.Errorf("IP URL is empty for camera %q", m.cam.Name)
		}
	case "webcam":
		if strings.TrimSpace(m.cam.Device) == "" {
			return fmt.Errorf("webcam device is empty for camera %q", m.cam.Name)
		}
	default:
		return fmt.Errorf("unknown camera type %q for camera %q", m.cam.Type, m.cam.Name)
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

// Close stops the stream and shuts down the HTTP server cleanly.
func (m *Manager) Close() {
	m.Stop()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = m.httpServer.Shutdown(ctx)
		m.httpServer = nil
	}
}

func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *Manager) UpdateCamera(cam config.CameraSource) {
	m.mu.Lock()
	m.cam = cam
	m.mu.Unlock()
}

func (m *Manager) UpdateConfig(cfg config.Config) {
	m.mu.Lock()
	m.globalCfg = cfg
	m.mu.Unlock()
}

func (m *Manager) supervise(ctx context.Context) {
	defer m.wg.Done()

	for {
		select {
		case <-ctx.Done():
			m.logger.Printf("[%s] stream supervisor stopped", m.cam.Name)
			return
		default:
		}

		ffmpegCmd, err := m.preparePipeline(ctx)
		if err != nil {
			m.recordError(err)
			if !sleepOrDone(ctx, 8*time.Second) {
				return
			}
			continue
		}

		stdout, err := ffmpegCmd.StdoutPipe()
		if err != nil {
			m.recordError(fmt.Errorf("ffmpeg stdout pipe: %w", err))
			if !sleepOrDone(ctx, 8*time.Second) {
				return
			}
			continue
		}
		go m.forwardFrames(ctx, stdout)

		// Still attach stderr logs
		stderr, err := ffmpegCmd.StderrPipe()
		if err == nil {
			go scanPipe(fmt.Sprintf("[%s] ffmpeg stderr", m.cam.Name), stderr, m.logger)
		}

		m.logger.Printf("[%s] starting ffmpeg: %s", m.cam.Name, strings.Join(ffmpegCmd.Args, " "))
		if err := ffmpegCmd.Start(); err != nil {
			m.recordError(fmt.Errorf("start ffmpeg: %w", err))
			if !sleepOrDone(ctx, 8*time.Second) {
				return
			}
			continue
		}

		ffmpegDone := make(chan error, 1)
		go func() { ffmpegDone <- ffmpegCmd.Wait() }()

		m.recordRunning()

		var exitErr error
		select {
		case <-ctx.Done():
			_ = ffmpegCmd.Process.Kill()
			<-ffmpegDone
			return
		case err := <-ffmpegDone:
			exitErr = fmt.Errorf("ffmpeg exited: %w", err)
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

func (m *Manager) preparePipeline(ctx context.Context) (*exec.Cmd, error) {
	ffmpegCmd, err := m.buildFFmpegCommand(ctx)
	if err != nil {
		return nil, err
	}

	return ffmpegCmd, nil
}

func (m *Manager) buildFFmpegCommand(ctx context.Context) (*exec.Cmd, error) {
	path, err := m.resolveFFmpegPath()
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	cam := m.cam
	m.mu.Unlock()

	var args []string

	switch cam.Type {
	case "webcam":
		args = m.buildWebcamArgs(cam)
	default: // "rtsp"
		args = m.buildRTSPArgs(cam)
	}

	cmd := exec.CommandContext(ctx, path, args...)
	setHideWindow(cmd)
	return cmd, nil
}

func (m *Manager) PreviewDimensions() (int, int) {
	m.mu.Lock()
	cam := m.cam
	enableHTTP := m.enableHTTPServer
	m.mu.Unlock()

	// If it is the RTSP server camera, we keep the original resolution for the stdout preview pipeline
	if enableHTTP {
		return cam.Width, cam.Height
	}

	// Otherwise, scale preview down to max 640 width to save CPU/memory copy overhead
	if cam.Width > 640 {
		ratio := float64(cam.Height) / float64(cam.Width)
		return 640, int(640 * ratio)
	}
	return cam.Width, cam.Height
}

func (m *Manager) buildRTSPArgs(cam config.CameraSource) []string {
	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-rtsp_transport", "tcp",
		"-probesize", "100K",
		"-analyzeduration", "100K",
		"-fflags", "nobuffer",
		"-flags", "low_delay",
		"-thread_queue_size", "1024",
		"-i", cam.RTSPURL,
		"-an",
	}

	w, h := m.PreviewDimensions()
	args = append(args,
		"-vf", fmt.Sprintf("scale=%d:%d,fps=%d", w, h, cam.FPS),
		"-pix_fmt", "rgba",
		"-f", "rawvideo", "-",
	)

	return args
}

func (m *Manager) buildWebcamArgs(cam config.CameraSource) []string {
	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-fflags", "nobuffer",
		"-flags", "low_delay",
	}

	if runtime.GOOS == "linux" {
		args = append(args,
			"-f", "v4l2",
			"-input_format", "mjpeg", // Force MJPEG for high FPS
			"-framerate", fmt.Sprintf("%d", cam.FPS),
			"-video_size", fmt.Sprintf("%dx%d", cam.Width, cam.Height),
		)
	} else if runtime.GOOS == "windows" {
		args = append(args,
			"-f", "dshow",
			"-framerate", fmt.Sprintf("%d", cam.FPS),
			"-video_size", fmt.Sprintf("%dx%d", cam.Width, cam.Height),
		)
	} else if runtime.GOOS == "darwin" {
		args = append(args,
			"-f", "avfoundation",
			"-framerate", fmt.Sprintf("%d", cam.FPS),
			"-video_size", fmt.Sprintf("%dx%d", cam.Width, cam.Height),
		)
	}

	args = append(args,
		"-i", cam.Device,
		"-an",
	)

	w, h := m.PreviewDimensions()
	// Webcam cameras always output rgba rawvideo to stdout for preview
	args = append(args,
		"-vf", fmt.Sprintf("scale=%d:%d,fps=%d", w, h, cam.FPS),
		"-pix_fmt", "rgba",
		"-f", "rawvideo", "-",
	)

	return args
}

func ResolveFFmpegPath(candidate string) (string, error) {
	// 1. Always check the bundled ffmpeg next to our executable first.
	//    This is the path the installer places ffmpeg into.
	if exe, err := os.Executable(); err == nil {
		localName := "ffmpeg"
		if runtime.GOOS == "windows" {
			localName = "ffmpeg.exe"
		}
		bundled := filepath.Join(filepath.Dir(exe), "third_party", "ffmpeg", localName)
		if info, err := os.Stat(bundled); err == nil && !info.IsDir() {
			return bundled, nil
		}
	}

	// 2. On non-Windows, try system ffmpeg.
	if runtime.GOOS != "windows" {
		if path, err := exec.LookPath("ffmpeg"); err == nil {
			return path, nil
		}
	}

	// 3. Try the config candidate via LookPath (handles PATH and absolute paths).
	if candidate != "" {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}

		// If absolute, check it exists as a file.
		if filepath.IsAbs(candidate) {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
		}

		// Resolve relative candidate against executable directory.
		if exe, err := os.Executable(); err == nil {
			resolved := filepath.Join(filepath.Dir(exe), filepath.FromSlash(candidate))
			if info, err := os.Stat(resolved); err == nil && !info.IsDir() {
				return resolved, nil
			}
		}
	}

	return "", fmt.Errorf("ffmpeg not found: checked bundled path and config candidate %q", candidate)
}

func (m *Manager) resolveFFmpegPath() (string, error) {
	m.mu.Lock()
	candidate := m.globalCfg.FFmpegPath
	m.mu.Unlock()
	return ResolveFFmpegPath(candidate)
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
		m.logger.Printf("[%s] pipeline error: %v", m.cam.Name, err)
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
	cam := m.cam
	m.mu.Unlock()

	pw, ph := m.PreviewDimensions()
	frameSize := pw * ph * 4

	// Double buffering structure to swap frames without allocations
	var fb struct {
		mu    sync.Mutex
		data  []byte
		fresh bool
	}
	fb.data = make([]byte, frameSize)

	notifyChan := make(chan struct{}, 1)

	// Spawn worker goroutine so we do not block the reader thread during processing/compression/writing
	go func() {
		workBuf := make([]byte, frameSize)
		for {
			select {
			case <-ctx.Done():
				return
			case <-notifyChan:
				fb.mu.Lock()
				if !fb.fresh {
					fb.mu.Unlock()
					continue
				}
				fb.data, workBuf = workBuf, fb.data
				fb.fresh = false
				fb.mu.Unlock()

				// 1. Broadcast to HTTP clients (only for server camera)
				if m.enableHTTPServer {
					m.broadcastFrame(pw, ph, workBuf)
				}

				// 2. Trigger live preview callback (all cameras)
				m.onFrameMu.Lock()
				cb := m.onFrame
				m.onFrameMu.Unlock()
				if cb != nil {
					cb(pw, ph, workBuf)
				}
			}
		}
	}()

	// Reader loop
	readBuf := make([]byte, frameSize)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		_, err := io.ReadFull(r, readBuf)
		if err != nil {
			if err != io.EOF && !errors.Is(err, os.ErrClosed) {
				m.logger.Printf("[%s] frame reader error: %v", cam.Name, err)
			}
			return
		}

		// Swap read buffer with shared buffer
		fb.mu.Lock()
		fb.data, readBuf = readBuf, fb.data
		fb.fresh = true
		fb.mu.Unlock()

		// Non-blocking notification to worker
		select {
		case notifyChan <- struct{}{}:
		default:
		}
	}
}

func (m *Manager) broadcastFrame(width, height int, pix []byte) {
	m.listenersMu.Lock()
	hasListeners := len(m.listeners) > 0
	m.listenersMu.Unlock()

	if !hasListeners {
		return
	}

	m.mu.Lock()
	fps := m.cam.FPS
	m.mu.Unlock()
	if fps <= 0 {
		fps = 30
	}
	interval := time.Second / time.Duration(fps)

	m.rgbaImgMu.Lock()
	now := time.Now()
	if now.Sub(m.lastPreviewTime) < interval {
		m.rgbaImgMu.Unlock()
		return
	}
	m.lastPreviewTime = now

	if m.rgbaImg == nil || m.rgbaImg.Rect.Dx() != width || m.rgbaImg.Rect.Dy() != height {
		m.rgbaImg = image.NewRGBA(image.Rect(0, 0, width, height))
	}

	// FFmpeg's rawvideo output is configured to "-pix_fmt rgba" for both platforms,
	// so the byte order is already RGBA. Just use fast memory copy.
	copy(m.rgbaImg.Pix, pix)

	var buf bytes.Buffer
	err := jpeg.Encode(&buf, m.rgbaImg, &jpeg.Options{Quality: 60})
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
	m.mu.Lock()
	port := m.globalCfg.BridgePort
	m.mu.Unlock()
	if port <= 0 {
		port = 18080
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","service":"SYG Camera Helper"}`))
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

	mux.HandleFunc("/ws-stream", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			http.Error(w, "Not a websocket handshake", http.StatusBadRequest)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "Webserver doesn't support hijacking", http.StatusInternalServerError)
			return
		}
		conn, bufrw, err := hj.Hijack()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer conn.Close()

		key := r.Header.Get("Sec-WebSocket-Key")
		h := sha1.New()
		h.Write([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		accept := base64.StdEncoding.EncodeToString(h.Sum(nil))

		bufrw.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
		bufrw.WriteString("Upgrade: websocket\r\n")
		bufrw.WriteString("Connection: Upgrade\r\n")
		bufrw.WriteString("Sec-WebSocket-Accept: " + accept + "\r\n\r\n")
		bufrw.Flush()

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

		closeChan := make(chan struct{})
		go func() {
			buf := make([]byte, 1024)
			for {
				_, err := conn.Read(buf)
				if err != nil {
					close(closeChan)
					return
				}
			}
		}()

		cn := r.Context()
		for {
			select {
			case <-cn.Done():
				return
			case <-closeChan:
				return
			case jpegBytes, ok := <-ch:
				if !ok {
					return
				}
				length := len(jpegBytes)
				var header []byte
				if length < 126 {
					header = []byte{0x82, byte(length)}
				} else if length < 65536 {
					header = []byte{0x82, 126, byte(length >> 8), byte(length)}
				} else {
					header = []byte{
						0x82, 127,
						byte(length >> 56), byte(length >> 48), byte(length >> 40), byte(length >> 32),
						byte(length >> 24), byte(length >> 16), byte(length >> 8), byte(length),
					}
				}
				if _, err := bufrw.Write(header); err != nil {
					return
				}
				if _, err := bufrw.Write(jpegBytes); err != nil {
					return
				}
				bufrw.Flush()
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
