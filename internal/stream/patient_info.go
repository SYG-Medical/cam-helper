package stream

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const patientInfoFile = "patient_info.json"

// PatientInfo holds metadata about a recording session's patient.
type PatientInfo struct {
	Name       string     `json:"name"`                      // Required
	PatientID  string     `json:"patient_id,omitempty"`       // Optional - Patient ID
	TC         string     `json:"tc,omitempty"`               // Deprecated - kept for backwards compatibility
	RecordDate time.Time  `json:"record_date"`
	Duration   string     `json:"duration,omitempty"`         // Filled after processing

	// New maneuver-based structure (preferred)
	Maneuvers []Maneuver `json:"maneuvers,omitempty"`

	// Legacy flat list — kept for backward compatibility; migrated to Maneuvers on load
	Videos []VideoFile `json:"videos,omitempty"`
}

// Maneuver groups a single recording session with its note and all produced video files.
type Maneuver struct {
	Index  int         `json:"index"`          // 1-based maneuver number
	Note   string      `json:"note,omitempty"` // Doctor/technician note for this maneuver
	Videos []VideoFile `json:"videos"`          // All videos produced for this maneuver
}

// VideoFile describes a single output video file within the patient directory.
type VideoFile struct {
	FileName   string `json:"file_name"`               // e.g. "Genel_20260626_120000.mp4"
	Type       string `json:"type"`                    // "general" or "camera"
	Camera     string `json:"camera,omitempty"`        // Camera name (for type == "camera")
	CameraType string `json:"camera_type,omitempty"`   // "environment" or "glasses"
	EyeSide    string `json:"eye_side,omitempty"`      // "right", "left", or "both" (for glasses cameras)
	Note       string `json:"note,omitempty"`          // Legacy: maneuver-specific note (migrated to Maneuver.Note)
}

// SavePatientInfo writes patient info as JSON into the given directory.
func SavePatientInfo(dir string, info PatientInfo) error {
	// When saving, clear the legacy Videos field if Maneuvers is populated
	// to avoid duplicating data on disk.
	if len(info.Maneuvers) > 0 {
		info.Videos = nil
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal patient info: %w", err)
	}
	path := filepath.Join(dir, patientInfoFile)
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// LoadPatientInfo reads patient info from the given directory.
// It automatically migrates the legacy flat Videos list to the Maneuvers structure.
func LoadPatientInfo(dir string) (PatientInfo, error) {
	path := filepath.Join(dir, patientInfoFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return PatientInfo{}, fmt.Errorf("read patient info: %w", err)
	}
	var info PatientInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return PatientInfo{}, fmt.Errorf("parse patient info: %w", err)
	}
	if info.PatientID == "" && info.TC != "" {
		info.PatientID = info.TC
	}

	// --- Backward compatibility migration ---
	// If the file has the old flat Videos list but no Maneuvers, migrate them.
	if len(info.Maneuvers) == 0 && len(info.Videos) > 0 {
		// Group legacy videos into maneuver 1 (they were all recorded together historically)
		maneuver := Maneuver{
			Index:  1,
			Videos: make([]VideoFile, 0, len(info.Videos)),
		}
		// Extract note from the first general video (legacy Note field on VideoFile)
		for _, v := range info.Videos {
			if maneuver.Note == "" && v.Note != "" {
				maneuver.Note = v.Note
			}
			// Migrate: strip the per-file note into the maneuver note, then clear it
			vCopy := v
			vCopy.Note = ""
			maneuver.Videos = append(maneuver.Videos, vCopy)
		}
		info.Maneuvers = []Maneuver{maneuver}
		// Keep Videos for any code that still reads it during this session,
		// but it will be cleared on next SavePatientInfo call.
	}

	return info, nil
}

// AppendManeuver adds a new maneuver (with its videos) to the patient info JSON file.
func AppendManeuver(dir string, maneuver Maneuver) error {
	info, err := LoadPatientInfo(dir)
	if err != nil {
		return err
	}
	// Assign the next index
	maneuver.Index = len(info.Maneuvers) + 1
	info.Maneuvers = append(info.Maneuvers, maneuver)
	return SavePatientInfo(dir, info)
}

// UpdateManeuverNote updates the note for the maneuver at the given 0-based slice index.
func UpdateManeuverNote(dir string, sliceIdx int, note string) error {
	info, err := LoadPatientInfo(dir)
	if err != nil {
		return err
	}
	if sliceIdx < 0 || sliceIdx >= len(info.Maneuvers) {
		return fmt.Errorf("maneuver index %d out of range", sliceIdx)
	}
	info.Maneuvers[sliceIdx].Note = note
	return SavePatientInfo(dir, info)
}

// AppendVideoToManeuver adds a video file to a specific maneuver (0-based slice index).
func AppendVideoToManeuver(dir string, sliceIdx int, video VideoFile) error {
	info, err := LoadPatientInfo(dir)
	if err != nil {
		return err
	}
	if sliceIdx < 0 || sliceIdx >= len(info.Maneuvers) {
		return fmt.Errorf("maneuver index %d out of range", sliceIdx)
	}
	info.Maneuvers[sliceIdx].Videos = append(info.Maneuvers[sliceIdx].Videos, video)
	return SavePatientInfo(dir, info)
}

// UpdatePatientInfoVideos adds video file entries and duration to an existing patient info.
// Deprecated: use AppendManeuver instead. Kept for backward compatibility.
func UpdatePatientInfoVideos(dir string, duration string, videos []VideoFile) error {
	info, err := LoadPatientInfo(dir)
	if err != nil {
		return err
	}
	info.Duration = duration
	// Append to a maneuver if possible, otherwise fall back to legacy Videos
	if len(info.Maneuvers) > 0 {
		lastIdx := len(info.Maneuvers) - 1
		info.Maneuvers[lastIdx].Videos = append(info.Maneuvers[lastIdx].Videos, videos...)
	} else {
		info.Videos = append(info.Videos, videos...)
	}
	return SavePatientInfo(dir, info)
}

// MoveTempToFinal moves or merges a temporary recording directory into the final patient directory.
func MoveTempToFinal(tempDir, finalDir string) error {
	if tempDir == finalDir {
		return nil
	}

	// Read contents of tempDir
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		return fmt.Errorf("read temp dir: %w", err)
	}

	for _, entry := range entries {
		src := filepath.Join(tempDir, entry.Name())
		dst := filepath.Join(finalDir, entry.Name())

		if entry.IsDir() {
			// e.g. "raw" directory
			_ = os.MkdirAll(dst, 0o755)
			MoveTempToFinal(src, dst)
		} else {
			// Move file
			_ = os.Rename(src, dst)
		}
	}
	// Attempt to remove the temp directory after moving contents
	_ = os.Remove(tempDir)
	return nil
}
