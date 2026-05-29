//go:build windows

package config

// DetectWebcams on Windows returns an empty list.
// Windows webcam enumeration would require DirectShow or WMF,
// which is out of scope for now — users add webcams manually.
func DetectWebcams() []CameraSource {
	return nil
}
