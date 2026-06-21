package stream

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"nystavision/internal/logging"
)

// PostProcessor converts compressed per-camera segments into compatible MP4
// files and creates the general grid only after live recording has stopped.
type PostProcessor struct {
	ffmpegPath string
	logger     *logging.Logger
	profile    HardwareProfile
}

func NewPostProcessor(ffmpegPath string, logger *logging.Logger, disableHW bool) *PostProcessor {
	profile := SoftwareHardwareProfile()
	if !disableHW {
		profile = DetectHardwareProfile(context.Background(), ffmpegPath, logger)
	}
	return &PostProcessor{
		ffmpegPath: ffmpegPath,
		logger:     logger,
		profile:    profile,
	}
}

func (p *PostProcessor) HardwareProfile() HardwareProfile {
	return p.profile
}

type ProcessResult struct {
	OutputDir string
	Files     []string
	Err       error
}

// Process creates one native-resolution MP4 per camera and then the general
// grid. Temporary session data is deliberately not removed here; the caller
// removes it only after this method reports complete success.
func (p *PostProcessor) Process(
	ctx context.Context,
	session *RecordingSession,
	outDir string,
	progress chan<- float64,
) ProcessResult {
	snapshot := session.Snapshot()
	cameras := session.CameraList()
	if snapshot.EndedAt.IsZero() {
		snapshot.EndedAt = time.Now()
	}
	if len(cameras) == 0 {
		return ProcessResult{OutputDir: outDir, Err: fmt.Errorf("recording session contains no cameras")}
	}

	timestamp := time.Now().Format("20060102_150405")
	alignedOutputs := make(map[string]string, len(cameras))
	var files []string
	var mu sync.Mutex
	var firstErr error
	
	tracker := &progressTracker{
		totalJobs:   len(cameras)*2 + 1,
		activeJobs:  make(map[string]float64),
		progressOut: progress,
	}

	// Keep simultaneous encodes bounded. Consumer GPUs and libx264 both behave
	// more predictably with two jobs than with one process per camera.
	sem := make(chan struct{}, 2)
	var wg sync.WaitGroup
	for i := range cameras {
		camera := cameras[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			safeName := sanitizeFilename(camera.Name)
			if safeName == "" {
				safeName = sanitizeFilename(camera.ID)
			}
			outFile := filepath.Join(outDir, fmt.Sprintf("%s_%s.mp4", safeName, timestamp))
			
			if err := p.renderCamera(ctx, snapshot, camera, outFile, func(f float64) { tracker.updateJob(outFile, f) }); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("process camera %s: %w", camera.Name, err)
				}
				mu.Unlock()
			} else {
				tracker.finishJob(outFile)
				alignedFile := filepath.Join(snapshot.TempDir, fmt.Sprintf("aligned_%s.mp4", sanitizeFilename(camera.ID)))
				if err := p.renderAlignedCamera(ctx, snapshot, camera, alignedFile, func(f float64) { tracker.updateJob(alignedFile, f) }); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("align camera %s: %w", camera.Name, err)
					}
					mu.Unlock()
				} else {
					tracker.finishJob(alignedFile)
					mu.Lock()
					alignedOutputs[camera.ID] = alignedFile
					files = append(files, outFile)
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	if ctx.Err() != nil {
		return ProcessResult{OutputDir: outDir, Files: files, Err: ctx.Err()}
	}
	if firstErr != nil {
		return ProcessResult{OutputDir: outDir, Files: files, Err: firstErr}
	}

	generalFile := filepath.Join(outDir, fmt.Sprintf("Genel_%s.mp4", timestamp))
	if err := p.renderGeneral(ctx, snapshot, cameras, alignedOutputs, generalFile, func(f float64) { tracker.updateJob(generalFile, f) }); err != nil {
		return ProcessResult{OutputDir: outDir, Files: files, Err: fmt.Errorf("create general video: %w", err)}
	}
	tracker.finishJob(generalFile)
	files = append(files, generalFile)

	return ProcessResult{OutputDir: outDir, Files: files}
}

// renderCamera intentionally removes disconnected intervals. Each segment gets
// its own wall-clock timestamp, so the bottom-right time visibly jumps when the
// camera reconnects instead of pretending frames existed during the outage.
func (p *PostProcessor) renderCamera(ctx context.Context, session RecordingSessionSnapshot, camera CameraRecording, outFile string, onProgress func(float64)) error {
	width, height, fps := normalizedVideoParams(camera.Width, camera.Height, camera.FPS)
	var expectedDuration time.Duration
	args := []string{"-hide_banner", "-loglevel", "warning", "-stats"}
	var filters []string
	var labels []string
	input := 0
	for _, segment := range camera.Segments {
		info, err := os.Stat(segment.Path)
		if err != nil || info.Size() == 0 {
			continue
		}
		end := segment.EndedAt
		if end.IsZero() || end.After(session.EndedAt) {
			end = session.EndedAt
		}
		segmentDuration := end.Sub(segment.StartedAt)
		if segmentDuration <= 10*time.Millisecond {
			continue
		}
		expectedDuration += segmentDuration
		args = append(args, "-t", ffDuration(segmentDuration), "-i", segment.Path)
		label := fmt.Sprintf("v%d", input)
		filters = append(filters, fmt.Sprintf(
			"[%d:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:black,fps=%d,setpts=PTS-STARTPTS,%s[%s]",
			input, width, height, width, height, fps, timestampFilter(segment.StartedAt, 24), label,
		))
		labels = append(labels, "["+label+"]")
		input++
	}

	if len(labels) == 0 {
		duration := session.EndedAt.Sub(session.StartedAt)
		expectedDuration = duration
		if duration <= 0 {
			return fmt.Errorf("camera has no valid recording segments")
		}
		args = append(args,
			"-f", "lavfi", "-t", ffDuration(duration),
			"-i", fmt.Sprintf("color=c=black:s=%dx%d:r=%d", width, height, fps),
		)
		filters = append(filters, fmt.Sprintf("[0:v]%s[outv]", timestampFilter(session.StartedAt, 24)))
	} else {
		filters = append(filters, fmt.Sprintf("%sconcat=n=%d:v=1:a=0[outv]", strings.Join(labels, ""), len(labels)))
	}
	args = append(args, "-filter_complex", strings.Join(filters, ";"), "-map", "[outv]", "-an")
	return p.runEncoded(ctx, args, outFile, expectedDuration, onProgress)
}

// renderAlignedCamera preserves the full session duration for the general grid.
// Camera outages are represented by black frames at the camera's grid cell.
func (p *PostProcessor) renderAlignedCamera(ctx context.Context, session RecordingSessionSnapshot, camera CameraRecording, outFile string, onProgress func(float64)) error {
	duration := session.EndedAt.Sub(session.StartedAt)
	if duration <= 0 {
		return fmt.Errorf("invalid recording duration")
	}
	width, height, fps := normalizedVideoParams(camera.Width, camera.Height, camera.FPS)
	args := []string{"-hide_banner", "-loglevel", "warning", "-stats"}
	var filters []string
	var labels []string
	input := 0
	cursor := session.StartedAt

	addBlack := func(d time.Duration) {
		if d <= 10*time.Millisecond {
			return
		}
		args = append(args,
			"-f", "lavfi",
			"-t", ffDuration(d),
			"-i", fmt.Sprintf("color=c=black:s=%dx%d:r=%d", width, height, fps),
		)
		label := fmt.Sprintf("v%d", input)
		filters = append(filters, fmt.Sprintf("[%d:v]setpts=PTS-STARTPTS[%s]", input, label))
		labels = append(labels, "["+label+"]")
		input++
	}

	for _, segment := range camera.Segments {
		info, err := os.Stat(segment.Path)
		if err != nil || info.Size() == 0 {
			continue
		}
		start := segment.StartedAt
		if start.Before(session.StartedAt) {
			start = session.StartedAt
		}
		end := segment.EndedAt
		if end.IsZero() || end.After(session.EndedAt) {
			end = session.EndedAt
		}
		if end.Before(start) {
			continue
		}
		if start.After(cursor) {
			addBlack(start.Sub(cursor))
		}
		segmentDuration := end.Sub(start)
		if segmentDuration <= 10*time.Millisecond {
			continue
		}
		args = append(args, "-t", ffDuration(segmentDuration), "-i", segment.Path)
		label := fmt.Sprintf("v%d", input)
		filters = append(filters, fmt.Sprintf(
			"[%d:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:black,fps=%d,setpts=PTS-STARTPTS[%s]",
			input, width, height, width, height, fps, label,
		))
		labels = append(labels, "["+label+"]")
		input++
		if end.After(cursor) {
			cursor = end
		}
	}
	if cursor.Before(session.EndedAt) {
		addBlack(session.EndedAt.Sub(cursor))
	}
	if len(labels) == 0 {
		addBlack(duration)
	}

	filters = append(filters, fmt.Sprintf("%sconcat=n=%d:v=1:a=0[outv]", strings.Join(labels, ""), len(labels)))
	args = append(args, "-filter_complex", strings.Join(filters, ";"), "-map", "[outv]", "-an")

	return p.runEncoded(ctx, args, outFile, duration, onProgress)
}

func (p *PostProcessor) renderGeneral(
	ctx context.Context,
	session RecordingSessionSnapshot,
	cameras []CameraRecording,
	outputs map[string]string,
	outFile string,
	onProgress func(float64),
) error {
	duration := session.EndedAt.Sub(session.StartedAt)
	maxW, maxH, maxFPS := 0, 0, 0
	for _, camera := range cameras {
		w, h, fps := normalizedVideoParams(camera.Width, camera.Height, camera.FPS)
		if w > maxW {
			maxW = w
		}
		if h > maxH {
			maxH = h
		}
		if fps > maxFPS {
			maxFPS = fps
		}
	}
	if maxFPS > 60 {
		maxFPS = 60
	}

	args := []string{"-hide_banner", "-loglevel", "warning", "-stats"}
	var filters []string
	var labels []string
	var layout []string
	for i, camera := range cameras {
		path := outputs[camera.ID]
		if path == "" {
			return fmt.Errorf("missing processed video for camera %s", camera.Name)
		}
		args = append(args, "-i", path)
		label := fmt.Sprintf("g%d", i)
		filters = append(filters, fmt.Sprintf(
			"[%d:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:black,fps=%d,setpts=PTS-STARTPTS[%s]",
			i, maxW, maxH, maxW, maxH, maxFPS, label,
		))
		labels = append(labels, "["+label+"]")
		col := i % session.Cols
		row := i / session.Cols
		layout = append(layout, fmt.Sprintf("%d_%d", col*maxW, row*maxH))
	}
	if len(labels) == 1 {
		filters = append(filters, fmt.Sprintf("[g0]%s[outv]", timestampFilter(session.StartedAt, 24)))
	} else {
		filters = append(filters, fmt.Sprintf(
			"%sxstack=inputs=%d:layout=%s:fill=black[stack]",
			strings.Join(labels, ""), len(labels), strings.Join(layout, "|"),
		))
		filters = append(filters, fmt.Sprintf("[stack]%s[outv]", timestampFilter(session.StartedAt, 24)))
	}
	args = append(args,
		"-filter_complex", strings.Join(filters, ";"),
		"-map", "[outv]", "-an",
		"-t", ffDuration(session.EndedAt.Sub(session.StartedAt)),
	)
	return p.runEncoded(ctx, args, outFile, duration, onProgress)
}

func (p *PostProcessor) runEncoded(ctx context.Context, baseArgs []string, outFile string, expectedDuration time.Duration, onProgress func(float64)) error {
	partial := strings.TrimSuffix(outFile, filepath.Ext(outFile)) + ".partial" + filepath.Ext(outFile)
	_ = os.Remove(partial)

	profiles := []HardwareProfile{p.profile}
	if p.profile.Hardware {
		profiles = append(profiles, SoftwareHardwareProfile())
	}
	var lastErr error
	for _, profile := range profiles {
		// VAAPI requires hwupload inside the existing complex filter graph.
		// Keep the verified VAAPI path for live per-camera recording, and use
		// the guaranteed software fallback for complex timeline/grid filters.
		if profile.Filter != "" {
			continue
		}
		args := append([]string{}, baseArgs[:3]...)
		args = append(args, profile.InitArgs...)
		args = append(args, baseArgs[3:]...)
		args = appendEncoderArgs(args, profile, "")
		args = append(args, "-movflags", "+faststart", "-y", partial)

		cmd := exec.CommandContext(ctx, p.ffmpegPath, args...)
		setHideWindow(cmd)
		
		stderrPipe, err := cmd.StderrPipe()
		if err != nil {
			lastErr = err
			continue
		}

		if err := cmd.Start(); err != nil {
			lastErr = err
			continue
		}

		var stderrBuf struct {
			mu sync.Mutex
			b  []byte
		}

		go func() {
			scanner := bufio.NewScanner(stderrPipe)
			scanner.Split(ffmpegSplitFunc)

			for scanner.Scan() {
				line := scanner.Text()
				stderrBuf.mu.Lock()
				stderrBuf.b = append(stderrBuf.b, line...)
				stderrBuf.b = append(stderrBuf.b, '\n')
				if len(stderrBuf.b) > 16384 {
					stderrBuf.b = stderrBuf.b[len(stderrBuf.b)-16384:]
				}
				stderrBuf.mu.Unlock()

				if onProgress != nil {
					if idx := strings.Index(line, "time="); idx != -1 {
						timeStr := line[idx+5:]
						if spaceIdx := strings.Index(timeStr, " "); spaceIdx != -1 {
							timeStr = timeStr[:spaceIdx]
						}
						parsedTime, err := parseFFmpegTime(timeStr)
						if err == nil && expectedDuration > 0 {
							frac := float64(parsedTime) / float64(expectedDuration)
							if frac > 1.0 {
								frac = 1.0
							}
							onProgress(frac)
						}
					}
				}
			}
		}()

		if err := cmd.Wait(); err != nil {
			stderrBuf.mu.Lock()
			errStr := string(stderrBuf.b)
			stderrBuf.mu.Unlock()
			lastErr = fmt.Errorf("%s failed: %w: %s", profile.Name, err, strings.TrimSpace(errStr))
			if p.logger != nil {
				p.logger.Printf("[post_processor] %v", lastErr)
			}
			_ = os.Remove(partial)
			continue
		}
		if err := os.Rename(partial, outFile); err != nil {
			_ = os.Remove(partial)
			return fmt.Errorf("finalize output: %w", err)
		}
		if p.logger != nil {
			p.logger.Printf("[post_processor] saved %s with %s", outFile, profileDescription(profile))
		}
		return nil
	}
	return lastErr
}

func normalizedVideoParams(width, height, fps int) (int, int, int) {
	if width <= 0 {
		width = 1280
	}
	if height <= 0 {
		height = 720
	}
	if fps <= 0 {
		fps = 30
	}
	if width%2 != 0 {
		width--
	}
	if height%2 != 0 {
		height--
	}
	return width, height, fps
}

func timestampFilter(start time.Time, size int) string {
	return fmt.Sprintf(
		`drawtext=text='%%{pts\:localtime\:%d\:%%Y-%%m-%%d %%H\\\:%%M\\\:%%S}':fontcolor=white:fontsize=%d:box=1:boxcolor=black@0.5:boxborderw=5:x=w-tw-10:y=h-th-10`,
		start.Unix(), size,
	)
}

func ffDuration(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	return strconv.FormatFloat(duration.Seconds(), 'f', 3, 64)
}

func GetOutputDir(recordingsDir, patientName string) string {
	timestamp := time.Now().Format("20060102_150405")
	folderName := sanitizeFilename(patientName)
	if folderName == "" {
		folderName = "Kayit"
	}
	return filepath.Join(recordingsDir, folderName+"_"+timestamp)
}

func sanitizeFilename(name string) string {
	replacer := strings.NewReplacer(
		"/", "-", "\\", "-", ":", "-", "*", "-",
		"?", "", "\"", "", "<", "", ">", "", "|", "",
	)
	result := strings.TrimSpace(replacer.Replace(name))
	if len(result) > 64 {
		result = result[:64]
	}
	return result
}
