package stream

import (
	"context"
	"fmt"
	"image"
	"image/draw"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"nystavision/internal/logging"
)

// CompositeCellW and CompositeCellH define the per-camera cell dimensions inside
// the composite video. 640×360 is 16:9 and keeps CPU/memory overhead low.
const (
	CompositeCellW = 640
	CompositeCellH = 360
	CompositeTargetFPS = 60
)

// CompositeRecorder records all cameras into a single composite grid video file
// in real-time by reading the preview frames already being produced by each
// Manager's FFmpeg process.
type CompositeRecorder struct {
	mu       sync.Mutex
	ffmpegPath string
	outFile  string
	cols     int
	rows     int
	totalW   int
	totalH   int
	targetFPS int
	currentFPS int

	// frame store — one RGBA frame per camera (latest available)
	frames   map[string]*image.RGBA
	framesMu sync.RWMutex

	// cameraOrder defines the rendering order in the grid
	cameraOrder []string

	stdin    io.WriteCloser
	cmd      *exec.Cmd
	cmdDone  chan error
	cancel   context.CancelFunc

	recording bool
	logger    *logging.Logger

	// lag detection
	lagCounter int

	// perf callback — called when FPS is auto-reduced due to system lag
	onPerfWarning func(currentFPS int)
	// called when the recording has fully stopped
	onStopped func(outFile string, err error)
}

// NewCompositeRecorder creates a recorder that will write to outFile.
func NewCompositeRecorder(
	ffmpegPath string,
	outFile string,
	cameraOrder []string,
	cols, rows int,
	logger *logging.Logger,
	onPerfWarning func(currentFPS int),
) *CompositeRecorder {
	return &CompositeRecorder{
		ffmpegPath:    ffmpegPath,
		outFile:       outFile,
		cols:          cols,
		rows:          rows,
		totalW:        cols * CompositeCellW,
		totalH:        rows * CompositeCellH,
		targetFPS:     CompositeTargetFPS,
		currentFPS:    CompositeTargetFPS,
		frames:        make(map[string]*image.RGBA),
		cameraOrder:   append([]string(nil), cameraOrder...),
		logger:        logger,
		lagCounter:    0,
		onPerfWarning: onPerfWarning,
	}
}

// UpdateFrame stores the latest frame for a given camera. Called from each
// Manager's frame callback on the preview pipeline goroutine.
func (cr *CompositeRecorder) UpdateFrame(cameraID string, width, height int, pix []byte) {
	// Copy pix into an image.RGBA so the recorder owns the data.
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	if len(pix) == len(img.Pix) {
		copy(img.Pix, pix)
	}
	cr.framesMu.Lock()
	cr.frames[cameraID] = img
	cr.framesMu.Unlock()
}

// Start launches the FFmpeg process and begins compositing frames.
// It is non-blocking; the actual frame-write loop runs in a goroutine.
func (cr *CompositeRecorder) Start() error {
	cr.mu.Lock()
	defer cr.mu.Unlock()

	if cr.recording {
		return fmt.Errorf("composite recorder already running")
	}

	partial := cr.partialPath()
	_ = os.Remove(partial)

	args := []string{
		"-hide_banner", "-loglevel", "warning",
		// rawvideo input from stdin
		"-f", "rawvideo",
		"-pixel_format", "rgba",
		"-video_size", fmt.Sprintf("%dx%d", cr.totalW, cr.totalH),
		"-framerate", fmt.Sprintf("%d", cr.currentFPS),
		"-use_wallclock_as_timestamps", "1",
		"-i", "pipe:0",
		// encode with the fastest possible settings — quality is secondary to
		// keeping up with real-time.
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		"-crf", "23",
		"-pix_fmt", "yuv420p",
		"-movflags", "+faststart",
		"-vsync", "2",
		"-an",
		"-y", partial,
	}

	cmd := exec.Command(cr.ffmpegPath, args...)
	setHideWindow(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("composite recorder stdin pipe: %w", err)
	}

	// Attach stderr so we can log issues without blocking.
	var stderrBuf struct {
		mu sync.Mutex
		b  []byte
	}
	stderrPipe, _ := cmd.StderrPipe()
	if stderrPipe != nil {
		go func() {
			buf := make([]byte, 4096)
			for {
				n, err := stderrPipe.Read(buf)
				if n > 0 {
					stderrBuf.mu.Lock()
					stderrBuf.b = append(stderrBuf.b, buf[:n]...)
					if len(stderrBuf.b) > 8192 {
						stderrBuf.b = stderrBuf.b[len(stderrBuf.b)-8192:]
					}
					stderrBuf.mu.Unlock()
				}
				if err != nil {
					return
				}
			}
		}()
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("composite recorder start ffmpeg: %w", err)
	}

	cr.cmd = cmd
	cr.stdin = stdin
	cr.cmdDone = make(chan error, 1)
	cr.recording = true
	cr.lagCounter = 0
	cr.currentFPS = cr.targetFPS

	go func() {
		err := cmd.Wait()
		if err != nil {
			stderrBuf.mu.Lock()
			errStr := string(stderrBuf.b)
			stderrBuf.mu.Unlock()
			if cr.logger != nil {
				cr.logger.Printf("[composite_recorder] ffmpeg exited with error: %v: %s", err, errStr)
			}
		}
		cr.cmdDone <- err
	}()

	go cr.writeLoop()

	if cr.logger != nil {
		cr.logger.Printf("[composite_recorder] started %dx%d @ %d fps → %s",
			cr.totalW, cr.totalH, cr.currentFPS, cr.outFile)
	}
	return nil
}

// Stop signals the recorder to finish, waits for FFmpeg to finalize the file,
// and renames the partial file to the final output path.
// It blocks until FFmpeg exits (or up to 15 seconds before force-killing).
func (cr *CompositeRecorder) Stop() error {
	cr.mu.Lock()
	if !cr.recording {
		cr.mu.Unlock()
		return nil
	}
	cr.recording = false
	stdin := cr.stdin
	cmdDone := cr.cmdDone
	cmd := cr.cmd
	cr.stdin = nil
	cr.mu.Unlock()

	// Gracefully signal EOF to FFmpeg so it can flush/finalize.
	if stdin != nil {
		_ = stdin.Close()
	}

	// Wait for FFmpeg to exit.
	var ffErr error
	select {
	case ffErr = <-cmdDone:
	case <-time.After(15 * time.Second):
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-cmdDone
		ffErr = fmt.Errorf("composite recorder ffmpeg timeout; force killed")
	}

	partial := cr.partialPath()
	if ffErr != nil {
		_ = os.Remove(partial)
		return fmt.Errorf("composite recorder ffmpeg: %w", ffErr)
	}
	if err := os.Rename(partial, cr.outFile); err != nil {
		_ = os.Remove(partial)
		return fmt.Errorf("composite recorder finalize: %w", err)
	}
	if cr.logger != nil {
		cr.logger.Printf("[composite_recorder] saved %s", cr.outFile)
	}
	return nil
}

// IsRecording returns whether the recorder is currently active.
func (cr *CompositeRecorder) IsRecording() bool {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	return cr.recording
}

// OutFile returns the configured output path.
func (cr *CompositeRecorder) OutFile() string { return cr.outFile }

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func (cr *CompositeRecorder) partialPath() string {
	ext := filepath.Ext(cr.outFile)
	return cr.outFile[:len(cr.outFile)-len(ext)] + ".partial" + ext
}

// writeLoop runs in its own goroutine and pushes composite frames to FFmpeg.
func (cr *CompositeRecorder) writeLoop() {
	frameDuration := time.Second / time.Duration(cr.currentFPS)
	ticker := time.NewTicker(frameDuration)
	defer ticker.Stop()

	// Pre-allocate composite buffer to avoid per-frame allocations.
	frameSize := cr.totalW * cr.totalH * 4
	compositePixels := make([]byte, frameSize)

	for {
		tickStart := <-ticker.C

		cr.mu.Lock()
		recording := cr.recording
		stdin := cr.stdin
		cr.mu.Unlock()

		if !recording || stdin == nil {
			return
		}

		// Build composite frame into compositePixels.
		cr.buildCompositeFrame(compositePixels)

		// Write to FFmpeg stdin.
		start := time.Now()
		if _, err := stdin.Write(compositePixels); err != nil {
			if cr.logger != nil {
				cr.logger.Printf("[composite_recorder] stdin write error: %v", err)
			}
			return
		}
		elapsed := time.Since(tickStart)
		_ = start

		// Lag detection — measure how long the full frame cycle took versus
		// the target frame duration (with a 15 % margin for GC spikes).
		cr.mu.Lock()
		currentFPS := cr.currentFPS
		cr.mu.Unlock()

		frameDur := time.Second / time.Duration(currentFPS)
		if elapsed > frameDur+frameDur/6 {
			cr.lagCounter++
		} else if cr.lagCounter > 0 {
			cr.lagCounter--
		}

		// If the system has been lagging for >30 consecutive frames, drop FPS.
		if cr.lagCounter > 30 && currentFPS > 15 {
			maxPossible := int(time.Second / elapsed)
			newFPS := maxPossible - 2
			if newFPS < 15 {
				newFPS = 15
			}
			if newFPS >= currentFPS {
				newFPS = currentFPS - 5
			}

			cr.mu.Lock()
			cr.currentFPS = newFPS
			cr.lagCounter = 0
			cr.mu.Unlock()

			ticker.Reset(time.Second / time.Duration(newFPS))

			if cr.logger != nil {
				cr.logger.Printf("[composite_recorder] system lag — dropped to %d fps", newFPS)
			}
			if cr.onPerfWarning != nil {
				cr.onPerfWarning(newFPS)
			}
		}
	}
}

// buildCompositeFrame renders all camera frames into a flat RGBA byte slice.
// Each camera occupies a fixed CompositeCellW × CompositeCellH cell.
func (cr *CompositeRecorder) buildCompositeFrame(dst []byte) {
	// Black fill.
	for i := 0; i < len(dst); i += 4 {
		dst[i] = 0
		dst[i+1] = 0
		dst[i+2] = 0
		dst[i+3] = 255
	}

	cr.framesMu.RLock()
	defer cr.framesMu.RUnlock()

	for idx, camID := range cr.cameraOrder {
		if idx >= cr.cols*cr.rows {
			break
		}
		src := cr.frames[camID]
		if src == nil {
			continue
		}

		col := idx % cr.cols
		row := idx / cr.cols
		cellX := col * CompositeCellW
		cellY := row * CompositeCellH

		srcW := src.Rect.Dx()
		srcH := src.Rect.Dy()
		if srcW <= 0 || srcH <= 0 {
			continue
		}

		// Fit source into cell preserving aspect ratio (letterbox/pillarbox).
		dstW, dstH := fitAspect(srcW, srcH, CompositeCellW, CompositeCellH)
		offsetX := (CompositeCellW - dstW) / 2
		offsetY := (CompositeCellH - dstH) / 2

		// Scale and blit into composite.
		blitScaled(src, srcW, srcH, dst, cr.totalW,
			cellX+offsetX, cellY+offsetY, dstW, dstH)
	}
}

// fitAspect returns (dstW, dstH) that fit srcW×srcH inside maxW×maxH while
// preserving the aspect ratio.
func fitAspect(srcW, srcH, maxW, maxH int) (int, int) {
	if srcW == 0 || srcH == 0 {
		return maxW, maxH
	}
	// Scale to fit width.
	w := maxW
	h := w * srcH / srcW
	if h > maxH {
		// Scale to fit height instead.
		h = maxH
		w = h * srcW / srcH
	}
	return w, h
}

// blitScaled nearest-neighbor scales src into the flat dst buffer at (dx,dy)
// with the given output dimensions.
func blitScaled(src *image.RGBA, srcW, srcH int, dst []byte, dstStride int,
	dx, dy, dstW, dstH int) {
	for y := 0; y < dstH; y++ {
		srcY := y * srcH / dstH
		for x := 0; x < dstW; x++ {
			srcX := x * srcW / dstW
			si := srcY*src.Stride + srcX*4
			di := (dy+y)*dstStride*4 + (dx+x)*4
			if si+3 < len(src.Pix) && di+3 < len(dst) {
				dst[di] = src.Pix[si]
				dst[di+1] = src.Pix[si+1]
				dst[di+2] = src.Pix[si+2]
				dst[di+3] = src.Pix[si+3]
			}
		}
	}
}

// -------------------------------------------------------------------------
// ComposeGridFrame (kept for backward compat with job_queue decompose code)
// -------------------------------------------------------------------------

// ComposeGridFrame creates a composite *image.RGBA from a map of camera frames.
func ComposeGridFrame(frames map[string]*image.RGBA, cameraOrder []string, cols, rows, totalWidth, totalHeight int) *image.RGBA {
	composite := image.NewRGBA(image.Rect(0, 0, totalWidth, totalHeight))
	// Black background.
	draw.Draw(composite, composite.Bounds(), image.Black, image.Point{}, draw.Src)

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
		dstW, dstH := fitAspect(srcW, srcH, cellW, cellH)
		offsetX := (cellW - dstW) / 2
		offsetY := (cellH - dstH) / 2

		scaled := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
		blitScaled(frame, srcW, srcH, scaled.Pix, dstW, 0, 0, dstW, dstH)
		draw.Draw(composite, image.Rect(x+offsetX, y+offsetY, x+offsetX+dstW, y+offsetY+dstH), scaled, image.Point{}, draw.Src)
	}
	return composite
}
