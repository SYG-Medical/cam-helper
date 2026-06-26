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
	Name           string      `json:"name"`                      // Required
	TC             string      `json:"tc,omitempty"`               // Optional — Turkish ID
	PatientHistory string      `json:"patient_history,omitempty"`  // Optional — brief patient note
	RecordDate     time.Time   `json:"record_date"`
	Duration       string      `json:"duration,omitempty"`         // Filled after processing
	Videos         []VideoFile `json:"videos,omitempty"`           // Filled after processing
}

// VideoFile describes a single output video file within the patient directory.
type VideoFile struct {
	FileName string `json:"file_name"`          // e.g. "Genel_20260626_120000.mp4"
	Type     string `json:"type"`               // "general" or "camera"
	Camera   string `json:"camera,omitempty"`   // Camera name (only for type == "camera")
}

// SavePatientInfo writes patient info as JSON into the given directory.
func SavePatientInfo(dir string, info PatientInfo) error {
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal patient info: %w", err)
	}
	path := filepath.Join(dir, patientInfoFile)
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// LoadPatientInfo reads patient info from the given directory.
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
	return info, nil
}

// UpdatePatientInfoVideos adds video file entries and duration to an existing patient info.
func UpdatePatientInfoVideos(dir string, duration string, videos []VideoFile) error {
	info, err := LoadPatientInfo(dir)
	if err != nil {
		return err
	}
	info.Duration = duration
	info.Videos = videos
	return SavePatientInfo(dir, info)
}
