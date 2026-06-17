package stream

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"nystavision/internal/logging"
)

// PostProcessor handles the post-recording crop and save pipeline.
type PostProcessor struct {
	ffmpegPath string
	logger     *logging.Logger
}

// NewPostProcessor creates a new PostProcessor.
func NewPostProcessor(ffmpegPath string, logger *logging.Logger) *PostProcessor {
	return &PostProcessor{ffmpegPath: ffmpegPath, logger: logger}
}


// CameraSegment describes one camera's position in the composite grid.
type CameraSegment struct {
	ID   string
	Name string
	X    int
	Y    int
	W    int
	H    int
}

// ProcessResult is returned when processing is complete.
type ProcessResult struct {
	OutputDir string
	Files     []string
	Err       error
}

// Process takes the composite temp file, crops individual cameras, and saves all to outDir.
// Progress is reported via the progress channel (0.0 to 1.0).
// outDir must already exist.
func (p *PostProcessor) Process(
	ctx context.Context,
	compositePath string,
	segments []CameraSegment,
	outDir string,
	startTime time.Time,
	progress chan<- float64,
) ProcessResult {
	ffmpegPath := p.ffmpegPath
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	timestamp := time.Now().Format("20060102_150405")

	var files []string
	total := len(segments) + 1 // N cameras + 1 general
	done := 0

	report := func() {
		if progress != nil {
			select {
			case progress <- float64(done) / float64(total):
			default:
			}
		}
	}

	// Process each camera via crop
	for _, seg := range segments {
		if ctx.Err() != nil {
			return ProcessResult{Err: ctx.Err()}
		}

		safeName := sanitizeFilename(seg.Name)
		outFile := filepath.Join(outDir, fmt.Sprintf("%s_%s.mp4", safeName, timestamp))

		cropFilter := fmt.Sprintf("crop=%d:%d:%d:%d", seg.W, seg.H, seg.X, seg.Y)
		drawtextFilter := fmt.Sprintf(`drawtext=text='%%{pts\:localtime\:%d\:%%Y-%%m-%%d %%H\:%%M\:%%S}':fontcolor=white:fontsize=24:box=1:boxcolor=black@0.5:boxborderw=5:x=w-tw-10:y=h-th-10`, startTime.Unix())
		vfFilter := fmt.Sprintf("%s,%s", cropFilter, drawtextFilter)

		args := []string{
			"-hide_banner",
			"-loglevel", "warning",
			"-i", compositePath,
			"-vf", vfFilter,
			"-c:v", "libx264",
			"-preset", "fast",
			"-crf", "23",
			"-pix_fmt", "yuv420p",
			"-y",
			outFile,
		}

		cmd := exec.CommandContext(ctx, ffmpegPath, args...)
		setHideWindow(cmd)

		stderr, _ := cmd.StderrPipe()
		if err := cmd.Start(); err != nil {
			p.logger.Printf("[post_processor] failed to start ffmpeg crop for %s: %v", seg.Name, err)
			continue
		}

		if stderr != nil {
			go func() {
				buf := make([]byte, 4096)
				for {
					n, err := stderr.Read(buf)
					if n > 0 {
						p.logger.Printf("[post_processor] ffmpeg: %s", strings.TrimSpace(string(buf[:n])))
					}
					if err != nil {
						return
					}
				}
			}()
		}

		if err := cmd.Wait(); err != nil {
			p.logger.Printf("[post_processor] ffmpeg crop with timestamp error for %s: %v. Retrying without timestamp...", seg.Name, err)
			// Fallback: Crop without drawtext
			fallbackArgs := []string{
				"-hide_banner",
				"-loglevel", "warning",
				"-i", compositePath,
				"-vf", cropFilter,
				"-c:v", "libx264",
				"-preset", "fast",
				"-crf", "23",
				"-pix_fmt", "yuv420p",
				"-y",
				outFile,
			}
			cmdFallback := exec.CommandContext(ctx, ffmpegPath, fallbackArgs...)
			setHideWindow(cmdFallback)
			if errFallback := cmdFallback.Run(); errFallback != nil {
				p.logger.Printf("[post_processor] fallback crop error for %s: %v", seg.Name, errFallback)
			} else {
				files = append(files, outFile)
				p.logger.Printf("[post_processor] saved %s (without timestamp)", outFile)
			}
		} else {
			files = append(files, outFile)
			p.logger.Printf("[post_processor] saved %s", outFile)
		}

		done++
		report()
	}

	// Save general composite with timestamp overlay using ffmpeg drawtext
	if ctx.Err() == nil {
		outFile := filepath.Join(outDir, fmt.Sprintf("Genel_%s.mp4", timestamp))
		drawtextFilter := fmt.Sprintf(`drawtext=text='%%{pts\:localtime\:%d\:%%Y-%%m-%%d %%H\:%%M\:%%S}':fontcolor=white:fontsize=24:box=1:boxcolor=black@0.5:boxborderw=5:x=w-tw-10:y=h-th-10`, startTime.Unix())
		args := []string{
			"-hide_banner",
			"-loglevel", "warning",
			"-i", compositePath,
			"-vf", drawtextFilter,
			"-c:v", "libx264",
			"-preset", "fast",
			"-crf", "23",
			"-pix_fmt", "yuv420p",
			"-y",
			outFile,
		}

		cmd := exec.CommandContext(ctx, ffmpegPath, args...)
		setHideWindow(cmd)

		if err := cmd.Run(); err != nil {
			// If drawtext fails (e.g., no fonts), just copy the composite
			p.logger.Printf("[post_processor] drawtext failed (%v), copying composite as-is", err)
			copyArgs := []string{
				"-hide_banner", "-loglevel", "warning",
				"-i", compositePath,
				"-c", "copy",
				"-y", outFile,
			}
			cmd2 := exec.CommandContext(ctx, ffmpegPath, copyArgs...)
			setHideWindow(cmd2)
			if err2 := cmd2.Run(); err2 != nil {
				p.logger.Printf("[post_processor] copy fallback error: %v", err2)
			} else {
				files = append(files, outFile)
			}
		} else {
			files = append(files, outFile)
			p.logger.Printf("[post_processor] saved general %s", outFile)
		}

		done++
		report()
	}

	if len(files) == 0 {
		return ProcessResult{OutputDir: outDir, Files: files, Err: fmt.Errorf("no video files could be processed (FFmpeg may have exited with errors or not been found)")}
	}

	return ProcessResult{OutputDir: outDir, Files: files}
}

// GetOutputDir returns the output folder for the patient inside recordingsDir.
func GetOutputDir(recordingsDir, patientName string) string {
	timestamp := time.Now().Format("20060102_150405")
	folderName := sanitizeFilename(patientName)
	if folderName == "" {
		folderName = "Kayit"
	}
	folderName = folderName + "_" + timestamp

	return filepath.Join(recordingsDir, folderName)
}



// sanitizeFilename removes characters not safe for filenames.
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
