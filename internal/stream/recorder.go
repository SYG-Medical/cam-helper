package stream

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"rtsp-virtual-cam-agent/internal/logging"
)

// Recorder records the composite camera grid view as an MP4 file.
type Recorder struct {
	mu        sync.Mutex
	recording bool
	cancel    context.CancelFunc
	ffmpegCmd *exec.Cmd
	stdin     io.WriteCloser
	tempFile  string
	width     int
	height    int
	fps       int
	logger    *logging.Logger

	frameMu    sync.Mutex
	lastFrame  *image.RGBA
	frameReady chan struct{}
}

// NewRecorder creates a new Recorder instance.
func NewRecorder(logger *logging.Logger) *Recorder {
	return &Recorder{
		logger:     logger,
		frameReady: make(chan struct{}, 1),
	}
}

// Start begins recording with the specified grid dimensions.
func (r *Recorder) Start(width, height, fps int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.recording {
		return fmt.Errorf("already recording")
	}

	// Create temp file
	tempDir := os.TempDir()
	timestamp := time.Now().Format("20060102_150405")
	r.tempFile = filepath.Join(tempDir, fmt.Sprintf("syg_recording_%s.mp4", timestamp))
	r.width = width
	r.height = height
	r.fps = fps

	// Resolve ffmpeg path
	ffmpegPath := "ffmpeg"
	if runtime.GOOS == "windows" {
		if p, err := exec.LookPath("ffmpeg.exe"); err == nil {
			ffmpegPath = p
		}
	} else {
		if p, err := exec.LookPath("ffmpeg"); err == nil {
			ffmpegPath = p
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel

	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-f", "rawvideo",
		"-pix_fmt", "rgba",
		"-s", fmt.Sprintf("%dx%d", width, height),
		"-r", fmt.Sprintf("%d", fps),
		"-i", "pipe:0",
		"-c:v", "libx264",
		"-preset", "fast",
		"-crf", "23",
		"-pix_fmt", "yuv420p",
		"-y",
		r.tempFile,
	}

	r.ffmpegCmd = exec.CommandContext(ctx, ffmpegPath, args...)
	setHideWindow(r.ffmpegCmd)

	var err error
	r.stdin, err = r.ffmpegCmd.StdinPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("create stdin pipe: %w", err)
	}

	// Capture stderr for logging
	stderr, _ := r.ffmpegCmd.StderrPipe()
	if stderr != nil {
		go func() {
			buf := make([]byte, 4096)
			for {
				n, err := stderr.Read(buf)
				if n > 0 {
					r.logger.Printf("[recorder] ffmpeg: %s", strings.TrimSpace(string(buf[:n])))
				}
				if err != nil {
					return
				}
			}
		}()
	}

	if err := r.ffmpegCmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("start ffmpeg recorder: %w", err)
	}

	r.recording = true

	// Start frame writer goroutine
	go r.frameWriter(ctx)

	r.logger.Printf("[recorder] Recording started: %s (%dx%d @ %d fps)", r.tempFile, width, height, fps)
	return nil
}

// WriteFrame submits a composite frame for recording.
func (r *Recorder) WriteFrame(frame *image.RGBA) {
	r.frameMu.Lock()
	r.lastFrame = frame
	r.frameMu.Unlock()

	select {
	case r.frameReady <- struct{}{}:
	default:
	}
}

// frameWriter runs in a goroutine, writing frames at the recording FPS.
func (r *Recorder) frameWriter(ctx context.Context) {
	r.mu.Lock()
	fps := r.fps
	r.mu.Unlock()

	ticker := time.NewTicker(time.Second / time.Duration(fps))
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.frameMu.Lock()
			frame := r.lastFrame
			r.frameMu.Unlock()

			if frame == nil {
				continue
			}

			r.mu.Lock()
			stdin := r.stdin
			r.mu.Unlock()

			if stdin == nil {
				return
			}

			if _, err := stdin.Write(frame.Pix); err != nil {
				r.logger.Printf("[recorder] frame write error: %v", err)
				return
			}
		}
	}
}

// Stop ends the recording and returns the temp file path.
func (r *Recorder) Stop() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.recording {
		return "", fmt.Errorf("not recording")
	}

	r.recording = false

	// Close stdin to signal EOF to ffmpeg
	if r.stdin != nil {
		r.stdin.Close()
		r.stdin = nil
	}

	// Cancel context
	if r.cancel != nil {
		// Wait for ffmpeg to finish encoding, don't kill it immediately
		done := make(chan error, 1)
		go func() {
			done <- r.ffmpegCmd.Wait()
		}()

		select {
		case err := <-done:
			if err != nil {
				r.logger.Printf("[recorder] ffmpeg exited with: %v", err)
			}
		case <-time.After(10 * time.Second):
			r.cancel()
			r.logger.Printf("[recorder] ffmpeg timeout, force killed")
		}
	}

	tempFile := r.tempFile
	r.logger.Printf("[recorder] Recording stopped: %s", tempFile)

	// Verify file exists
	if _, err := os.Stat(tempFile); err != nil {
		return "", fmt.Errorf("recording file not found: %w", err)
	}

	return tempFile, nil
}

// IsRecording returns whether the recorder is currently recording.
func (r *Recorder) IsRecording() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.recording
}

// ComposeGridFrame creates a composite image from multiple camera frames.
func ComposeGridFrame(frames map[string]*image.RGBA, cameraOrder []string, cols, rows, totalWidth, totalHeight int) *image.RGBA {
	composite := image.NewRGBA(image.Rect(0, 0, totalWidth, totalHeight))

	// Fill background with solid black
	for i := 0; i < len(composite.Pix); i += 4 {
		composite.Pix[i] = 0     // R
		composite.Pix[i+1] = 0   // G
		composite.Pix[i+2] = 0   // B
		composite.Pix[i+3] = 255 // A
	}

	cellW := totalWidth / cols
	cellH := totalHeight / rows

	for idx, camID := range cameraOrder {
		if idx >= cols*rows {
			break
		}
		frame, ok := frames[camID]
		if !ok || frame == nil {
			continue
		}

		col := idx % cols
		row := idx / cols
		x := col * cellW
		y := row * cellH

		srcW := frame.Rect.Dx()
		srcH := frame.Rect.Dy()

		dstW := cellW
		dstH := cellH

		if srcW > 0 && srcH > 0 {
			srcAspect := float64(srcW) / float64(srcH)
			cellAspect := float64(cellW) / float64(cellH)

			if srcAspect > cellAspect {
				// Width limited
				dstW = cellW
				dstH = int(float64(cellW) / srcAspect)
			} else {
				// Height limited
				dstH = cellH
				dstW = int(float64(cellH) * srcAspect)
			}
		}

		offsetX := (cellW - dstW) / 2
		offsetY := (cellH - dstH) / 2

		// Scale frame to target size preserving aspect ratio
		scaled := scaleImage(frame, dstW, dstH)
		draw.Draw(composite, image.Rect(x+offsetX, y+offsetY, x+offsetX+dstW, y+offsetY+dstH), scaled, image.Point{}, draw.Src)
	}

	// Draw timestamp in the bottom-right corner
	timestampText := time.Now().Format("2006-01-02 15:04:05")
	drawTimestamp(composite, timestampText)

	return composite
}

// drawTimestamp draws a timestamp with a semi-transparent black background box in the bottom right.
func drawTimestamp(img *image.RGBA, text string) {
	w := len(text)*7 + 10
	h := 18
	x := img.Bounds().Max.X - w - 10
	y := img.Bounds().Max.Y - h - 10

	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			px := x + dx
			py := y + dy
			if px >= 0 && px < img.Bounds().Max.X && py >= 0 && py < img.Bounds().Max.Y {
				idx := py*img.Stride + px*4
				img.Pix[idx] = 0     // R
				img.Pix[idx+1] = 0   // G
				img.Pix[idx+2] = 0   // B
				img.Pix[idx+3] = 160 // A (semi-transparent)
			}
		}
	}

	point := fixed.Point26_6{X: fixed.I(x + 5), Y: fixed.I(y + 13)}
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(color.RGBA{R: 255, G: 255, B: 255, A: 255}),
		Face: basicfont.Face7x13,
		Dot:  point,
	}
	d.DrawString(text)
}

// scaleImage scales an RGBA image to the target dimensions using nearest-neighbor.
func scaleImage(src *image.RGBA, dstW, dstH int) *image.RGBA {
	srcW := src.Rect.Dx()
	srcH := src.Rect.Dy()
	if srcW == 0 || srcH == 0 {
		return image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	}

	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))

	for y := 0; y < dstH; y++ {
		srcY := y * srcH / dstH
		for x := 0; x < dstW; x++ {
			srcX := x * srcW / dstW
			srcIdx := srcY*src.Stride + srcX*4
			dstIdx := y*dst.Stride + x*4
			if srcIdx+3 < len(src.Pix) && dstIdx+3 < len(dst.Pix) {
				dst.Pix[dstIdx] = src.Pix[srcIdx]
				dst.Pix[dstIdx+1] = src.Pix[srcIdx+1]
				dst.Pix[dstIdx+2] = src.Pix[srcIdx+2]
				dst.Pix[dstIdx+3] = src.Pix[srcIdx+3]
			}
		}
	}
	return dst
}

// FrameToJPEG converts an RGBA image to JPEG bytes.
func FrameToJPEG(img *image.RGBA, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
