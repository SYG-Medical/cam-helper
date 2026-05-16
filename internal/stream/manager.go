package stream

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
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
}

func New(cfg config.Config, logger *logging.Logger, drv *driver.Manager) *Manager {
	return &Manager{cfg: cfg, logger: logger, driver: drv}
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
	attachLogs("ffmpeg", ffmpegCmd, m.logger)

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
		"-fflags", "+genpts+nobuffer",
		"-flags", "low_delay",
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
			// On Windows, use dshow for direct output if bridge is disabled
			args = append(args,
				"-pix_fmt", "yuv420p",
				"-f", "dshow",
				m.driver.FFmpegOutputTarget(),
			)
		} else {
			args = append(args,
				"-pix_fmt", "yuv420p",
				"-f", "v4l2",
				m.driver.FFmpegOutputTarget(),
			)
		}
	}

	cmd := exec.CommandContext(ctx, path, args...)
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	}
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
