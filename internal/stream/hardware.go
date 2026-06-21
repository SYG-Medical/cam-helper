package stream

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"nystavision/internal/logging"
)

// HardwareProfile is a verified H.264 encoding path.
type HardwareProfile struct {
	Name       string
	Encoder    string
	InitArgs   []string
	Filter     string
	EncodeArgs []string
	Hardware   bool
}

func SoftwareHardwareProfile() HardwareProfile {
	return HardwareProfile{
		Name:       "libx264",
		Encoder:    "libx264",
		EncodeArgs: []string{"-preset", "veryfast", "-crf", "23"},
	}
}

// DetectHardwareProfile performs a real one-frame encode probe. Encoder presence
// in `ffmpeg -encoders` alone is intentionally not trusted.
func DetectHardwareProfile(ctx context.Context, ffmpegPath string, logger *logging.Logger) HardwareProfile {
	for _, candidate := range hardwareCandidates() {
		if probeHardwareProfile(ctx, ffmpegPath, candidate) {
			if logger != nil {
				logger.Printf("[hardware] selected verified encoder: %s", candidate.Name)
			}
			return candidate
		}
		if logger != nil {
			logger.Printf("[hardware] encoder probe failed: %s", candidate.Name)
		}
	}
	profile := SoftwareHardwareProfile()
	if logger != nil {
		logger.Printf("[hardware] using software encoder: %s", profile.Name)
	}
	return profile
}

func hardwareCandidates() []HardwareProfile {
	nvenc := HardwareProfile{
		Name:       "NVENC",
		Encoder:    "h264_nvenc",
		EncodeArgs: []string{"-preset", "p4", "-tune", "hq", "-cq", "23", "-b:v", "0"},
		Hardware:   true,
	}
	qsv := HardwareProfile{
		Name:       "Quick Sync",
		Encoder:    "h264_qsv",
		EncodeArgs: []string{"-preset", "veryfast", "-global_quality", "23"},
		Hardware:   true,
	}
	amf := HardwareProfile{
		Name:       "AMD AMF",
		Encoder:    "h264_amf",
		EncodeArgs: []string{"-quality", "balanced", "-rc", "cqp", "-qp_i", "23", "-qp_p", "23"},
		Hardware:   true,
	}
	vaapi := HardwareProfile{
		Name:       "VAAPI",
		Encoder:    "h264_vaapi",
		InitArgs:   []string{"-vaapi_device", "/dev/dri/renderD128"},
		Filter:     "format=nv12,hwupload",
		EncodeArgs: []string{"-qp", "23"},
		Hardware:   true,
	}

	switch runtime.GOOS {
	case "windows":
		return []HardwareProfile{nvenc, qsv, amf}
	case "linux":
		candidates := []HardwareProfile{nvenc, qsv}
		if _, err := os.Stat("/dev/dri/renderD128"); err == nil {
			candidates = append(candidates, vaapi)
		}
		return candidates
	default:
		return nil
	}
}

func probeHardwareProfile(parent context.Context, ffmpegPath string, profile HardwareProfile) bool {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()

	args := []string{"-hide_banner", "-loglevel", "error"}
	args = append(args, profile.InitArgs...)
	args = append(args, "-f", "lavfi", "-i", "color=size=128x72:rate=1", "-frames:v", "1")
	if profile.Filter != "" {
		args = append(args, "-vf", profile.Filter)
	}
	args = append(args, "-c:v", profile.Encoder)
	args = append(args, profile.EncodeArgs...)
	args = append(args, "-f", "null", "-")

	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	setHideWindow(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	return cmd.Run() == nil
}

func appendEncoderArgs(args []string, profile HardwareProfile, filterPrefix string) []string {
	filter := profile.Filter
	if filterPrefix != "" {
		if filter != "" {
			filter = filterPrefix + "," + filter
		} else {
			filter = filterPrefix
		}
	}
	if filter != "" {
		args = append(args, "-vf", filter)
	}
	args = append(args, "-c:v", profile.Encoder)
	args = append(args, profile.EncodeArgs...)
	if profile.Encoder != "h264_vaapi" {
		args = append(args, "-pix_fmt", "yuv420p")
	}
	return args
}

func profileDescription(profile HardwareProfile) string {
	kind := "software"
	if profile.Hardware {
		kind = "hardware"
	}
	return fmt.Sprintf("%s (%s, %s)", profile.Name, profile.Encoder, kind)
}

func looksLikeEncoderFailure(stderr string) bool {
	text := strings.ToLower(stderr)
	return strings.Contains(text, "error while opening encoder") ||
		strings.Contains(text, "failed to create") ||
		strings.Contains(text, "no device") ||
		strings.Contains(text, "device setup failed")
}
