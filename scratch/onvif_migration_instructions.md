# ONVIF & PTZ Integration - Backend Migration Guide (Updated with Auto-Detection)

This document contains the exact code changes and files required to implement the ONVIF stream resolution, PTZ controls, and automatic ONVIF capability discovery directly from RTSP URLs.

---

## 1. File: `internal/config/config.go`
Add the following fields inside the `CameraSource` struct:

```go
type CameraSource struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`     // "rtsp" or "webcam"
	RTSPURL     string `json:"rtsp_url"` // used when Type == "rtsp"
	Device      string `json:"device"`   // used when Type == "webcam", e.g. "/dev/video0"
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	FPS         int    `json:"fps"`
	PixelFormat string `json:"pixel_format,omitempty"`
	DevicePath  string `json:"device_path,omitempty"`
	Enabled     bool   `json:"enabled"`

	// ONVIF & PTZ fields
	ONVIFAddress  string  `json:"onvif_address,omitempty"`  // e.g., "192.168.1.100:80" or "http://192.168.1.100/onvif/device_service"
	ONVIFUsername string  `json:"onvif_username,omitempty"` // optional username if not in ONVIF URL
	ONVIFPassword string  `json:"onvif_password,omitempty"` // optional password if not in ONVIF URL
	PTZSpeed      float64 `json:"ptz_speed,omitempty"`      // 0.0-1.0 (default: 0.3)
}
```

---

## 2. File: `config.example.json`
Add the new ONVIF fields as configuration examples under the first camera object:

```json
    {
      "id": "cam-1",
      "name": "Kamera 1",
      "type": "rtsp",
      "rtsp_url": "rtsp://192.168.1.100:554/live",
      "onvif_address": "192.168.1.100:80",
      "onvif_username": "admin",
      "onvif_password": "password123",
      "ptz_speed": 0.3,
      "device": "",
      "width": 1280,
      ...
```

---

## 3. File: `internal/stream/onvif.go` (NEW FILE)
Create a new file at `internal/stream/onvif.go` and paste the following content:

```go
package stream

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/use-go/onvif"
	media_ws "github.com/use-go/onvif/media"
	ptz_ws "github.com/use-go/onvif/ptz"
	"github.com/use-go/onvif/xsd"
	xsd_onvif "github.com/use-go/onvif/xsd/onvif"
)

type soapEnvelope struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    soapBody `xml:"Body"`
}

type soapBody struct {
	GetProfilesResponse  getProfilesResponse  `xml:"GetProfilesResponse"`
	GetStreamUriResponse getStreamUriResponse `xml:"GetStreamUriResponse"`
}

type getProfilesResponse struct {
	Profiles []struct {
		Token string `xml:"token,attr"`
	} `xml:"Profiles"`
}

type getStreamUriResponse struct {
	MediaUri struct {
		Uri string `xml:"Uri"`
	} `xml:"MediaUri"`
}

type ONVIFClient struct {
	dev        *onvif.Device
	directMode bool
	endpoint   string
	username   string
	password   string
	httpClient *http.Client

	mu           sync.Mutex
	profileToken string
	hasPTZ       bool

	timerMu   sync.Mutex
	stopTimer *time.Timer
}

func NewONVIFClient(address, username, password string) (*ONVIFClient, error) {
	addr := address
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}

	u, err := url.Parse(addr)
	if err == nil && u.User != nil {
		username = u.User.Username()
		if pass, ok := u.User.Password(); ok {
			password = pass
		}
		u.User = nil
		addr = u.String()
	}

	if !strings.Contains(addr, "/onvif/device_service") {
		addr = strings.TrimSuffix(addr, "/") + "/onvif/device_service"
	}

	dev, err := onvif.NewDevice(onvif.DeviceParams{
		Xaddr:    addr,
		Username: username,
		Password: password,
	})
	if err == nil {
		client := &ONVIFClient{
			dev:      dev,
			username: username,
			password: password,
		}
		client.hasPTZ = dev.GetEndpoint("ptz") != ""
		return client, nil
	}

	httpClient := &http.Client{Timeout: 10 * time.Second}
	probe := buildSOAPRequest("", "", `<tds:GetSystemDateAndTime xmlns:tds="http://www.onvif.org/ver10/device/wsdl"/>`)

	resp, postErr := httpClient.Post(addr, "application/soap+xml; charset=utf-8", strings.NewReader(probe))
	if postErr != nil {
		return nil, fmt.Errorf("onvif device creation failed (standard: %w, direct: %v)", err, postErr)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("onvif device creation failed: standard method: %w; direct SOAP returned HTTP %d", err, resp.StatusCode)
	}

	client := &ONVIFClient{
		directMode: true,
		endpoint:   addr,
		username:   username,
		password:   password,
		httpClient: httpClient,
	}

	client.hasPTZ = client.discoverPTZ()
	return client, nil
}

func (c *ONVIFClient) discoverPTZ() bool {
	body := `<tds:GetCapabilities xmlns:tds="http://www.onvif.org/ver10/device/wsdl">
		<tds:Category>PTZ</tds:Category>
	</tds:GetCapabilities>`

	respBody, err := c.doSOAP(body)
	if err != nil {
		return false
	}
	return strings.Contains(string(respBody), "PTZ") || strings.Contains(string(respBody), "ptz")
}

func (c *ONVIFClient) HasPTZ() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hasPTZ
}

func (c *ONVIFClient) ResolveProfileAndStreamURI() (string, error) {
	if !c.directMode {
		return c.resolveViaLibrary()
	}
	return c.resolveViaDirect()
}

func (c *ONVIFClient) resolveViaLibrary() (string, error) {
	resp, err := c.dev.CallMethod(media_ws.GetProfiles{})
	if err != nil {
		return "", fmt.Errorf("failed to get profiles: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var env soapEnvelope
	if err := xml.Unmarshal(bodyBytes, &env); err != nil {
		return "", fmt.Errorf("unmarshal profiles error: %w", err)
	}

	if len(env.Body.GetProfilesResponse.Profiles) == 0 {
		return "", fmt.Errorf("no camera media profiles found")
	}

	token := env.Body.GetProfilesResponse.Profiles[0].Token

	c.mu.Lock()
	c.profileToken = token
	c.mu.Unlock()

	getStreamUri := media_ws.GetStreamUri{
		StreamSetup: xsd_onvif.StreamSetup{
			Stream:    xsd_onvif.StreamType("RTP-Unicast"),
			Transport: xsd_onvif.Transport{Protocol: "RTSP"},
		},
		ProfileToken: xsd_onvif.ReferenceToken(token),
	}

	respStream, err := c.dev.CallMethod(getStreamUri)
	if err != nil {
		return "", fmt.Errorf("failed to get stream URI: %w", err)
	}
	defer respStream.Body.Close()

	streamBytes, err := io.ReadAll(respStream.Body)
	if err != nil {
		return "", err
	}

	var envStream soapEnvelope
	if err := xml.Unmarshal(streamBytes, &envStream); err != nil {
		return "", fmt.Errorf("unmarshal stream URI error: %w", err)
	}

	uri := envStream.Body.GetStreamUriResponse.MediaUri.Uri
	if uri == "" {
		return "", fmt.Errorf("stream URI not found in ONVIF response")
	}

	return uri, nil
}

func (c *ONVIFClient) resolveViaDirect() (string, error) {
	body := `<trt:GetProfiles xmlns:trt="http://www.onvif.org/ver10/media/wsdl"/>`
	respBytes, err := c.doSOAP(body)
	if err != nil {
		return "", fmt.Errorf("failed to get profiles: %w", err)
	}

	var env soapEnvelope
	if err := xml.Unmarshal(respBytes, &env); err != nil {
		return "", fmt.Errorf("unmarshal profiles error: %w", err)
	}

	if len(env.Body.GetProfilesResponse.Profiles) == 0 {
		return "", fmt.Errorf("no camera media profiles found")
	}

	token := env.Body.GetProfilesResponse.Profiles[0].Token

	c.mu.Lock()
	c.profileToken = token
	c.mu.Unlock()

	body = fmt.Sprintf(`<trt:GetStreamUri xmlns:trt="http://www.onvif.org/ver10/media/wsdl" xmlns:tt="http://www.onvif.org/ver10/schema">
		<trt:StreamSetup>
			<tt:Stream>RTP-Unicast</tt:Stream>
			<tt:Transport><tt:Protocol>RTSP</tt:Protocol></tt:Transport>
		</trt:StreamSetup>
		<trt:ProfileToken>%s</trt:ProfileToken>
	</trt:GetStreamUri>`, token)

	respBytes, err = c.doSOAP(body)
	if err != nil {
		return "", fmt.Errorf("failed to get stream URI: %w", err)
	}

	var envStream soapEnvelope
	if err := xml.Unmarshal(respBytes, &envStream); err != nil {
		return "", fmt.Errorf("unmarshal stream URI error: %w", err)
	}

	uri := envStream.Body.GetStreamUriResponse.MediaUri.Uri
	if uri == "" {
		return "", fmt.Errorf("stream URI not found in ONVIF response")
	}

	return uri, nil
}

func (c *ONVIFClient) ContinuousMove(pan, tilt, zoom float64) error {
	c.mu.Lock()
	token := c.profileToken
	ptz := c.hasPTZ
	c.mu.Unlock()

	if token == "" {
		return fmt.Errorf("no profile token; call ResolveProfileAndStreamURI first")
	}
	if !ptz {
		return fmt.Errorf("camera does not support PTZ")
	}

	if c.directMode {
		return c.continuousMoveViaDirect(token, pan, tilt, zoom)
	}
	return c.continuousMoveViaLibrary(token, pan, tilt, zoom)
}

func (c *ONVIFClient) continuousMoveViaLibrary(token string, pan, tilt, zoom float64) error {
	move := ptz_ws.ContinuousMove{
		ProfileToken: xsd_onvif.ReferenceToken(token),
		Velocity: xsd_onvif.PTZSpeed{
			PanTilt: xsd_onvif.Vector2D{X: pan, Y: tilt},
			Zoom:    xsd_onvif.Vector1D{X: zoom},
		},
	}

	resp, err := c.dev.CallMethod(move)
	if err != nil {
		return fmt.Errorf("ptz move failed: %w", err)
	}
	resp.Body.Close()

	c.resetFailSafe()
	return nil
}

func (c *ONVIFClient) continuousMoveViaDirect(token string, pan, tilt, zoom float64) error {
	body := fmt.Sprintf(`<tptz:ContinuousMove xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl" xmlns:tt="http://www.onvif.org/ver10/schema">
		<tptz:ProfileToken>%s</tptz:ProfileToken>
		<tptz:Velocity>
			<tt:PanTilt x="%.2f" y="%.2f"/>
			<tt:Zoom x="%.2f"/>
		</tptz:Velocity>
	</tptz:ContinuousMove>`, token, pan, tilt, zoom)

	_, err := c.doSOAP(body)
	if err != nil {
		return fmt.Errorf("ptz move failed: %w", err)
	}

	c.resetFailSafe()
	return nil
}

func (c *ONVIFClient) Stop() error {
	c.timerMu.Lock()
	if c.stopTimer != nil {
		c.stopTimer.Stop()
		c.stopTimer = nil
	}
	c.timerMu.Unlock()

	c.mu.Lock()
	token := c.profileToken
	ptz := c.hasPTZ
	c.mu.Unlock()

	if token == "" {
		return fmt.Errorf("no profile token")
	}
	if !ptz {
		return nil
	}

	if c.directMode {
		return c.stopViaDirect(token)
	}
	return c.stopViaLibrary(token)
}

func (c *ONVIFClient) stopViaLibrary(token string) error {
	stop := ptz_ws.Stop{
		ProfileToken: xsd_onvif.ReferenceToken(token),
		PanTilt:      xsd.Boolean(true),
		Zoom:         xsd.Boolean(true),
	}

	resp, err := c.dev.CallMethod(stop)
	if err != nil {
		return fmt.Errorf("ptz stop failed: %w", err)
	}
	resp.Body.Close()
	return nil
}

func (c *ONVIFClient) stopViaDirect(token string) error {
	body := fmt.Sprintf(`<tptz:Stop xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl">
		<tptz:ProfileToken>%s</tptz:ProfileToken>
		<tptz:PanTilt>true</tptz:PanTilt>
		<tptz:Zoom>true</tptz:Zoom>
	</tptz:Stop>`, token)

	_, err := c.doSOAP(body)
	if err != nil {
		return fmt.Errorf("ptz stop failed: %w", err)
	}
	return nil
}

func (c *ONVIFClient) resetFailSafe() {
	c.timerMu.Lock()
	if c.stopTimer != nil {
		c.stopTimer.Stop()
	}
	c.stopTimer = time.AfterFunc(4*time.Second, func() {
		_ = c.Stop()
	})
	c.timerMu.Unlock()
}

func (c *ONVIFClient) doSOAP(innerBody string) ([]byte, error) {
	envelope := buildSOAPRequest(c.username, c.password, innerBody)

	resp, err := c.httpClient.Post(c.endpoint, "application/soap+xml; charset=utf-8", strings.NewReader(envelope))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SOAP request failed with HTTP %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

func buildSOAPRequest(username, password, body string) string {
	securityHeader := ""
	if username != "" {
		securityHeader = buildWSSecurity(username, password)
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<soap:Envelope
	xmlns:soap="http://www.w3.org/2003/05/soap-envelope"
	xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd"
	xmlns:wsu="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd">
	<soap:Header>%s</soap:Header>
	<soap:Body>%s</soap:Body>
</soap:Envelope>`, securityHeader, body)
}

func buildWSSecurity(username, password string) string {
	nonce := make([]byte, 16)
	rand.Read(nonce)
	created := time.Now().UTC().Format(time.RFC3339Nano)

	h := sha1.New()
	h.Write(nonce)
	h.Write([]byte(created))
	h.Write([]byte(password))
	digest := base64.StdEncoding.EncodeToString(h.Sum(nil))

	nonceB64 := base64.StdEncoding.EncodeToString(nonce)

	return fmt.Sprintf(`<wsse:Security>
		<wsse:UsernameToken>
			<wsse:Username>%s</wsse:Username>
			<wsse:Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">%s</wsse:Password>
			<wsse:Nonce EncodingType="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary">%s</wsse:Nonce>
			<wsu:Created>%s</wsu:Created>
		</wsse:UsernameToken>
	</wsse:Security>`, username, digest, nonceB64, created)
}

// AutoDetectONVIF attempts to parse an RTSP URL, extract host and credentials,
// and probe common ONVIF ports to return a working ONVIFClient.
func AutoDetectONVIF(rtspURL string) (*ONVIFClient, error) {
	u, err := url.Parse(rtspURL)
	if err != nil {
		return nil, fmt.Errorf("invalid RTSP URL: %w", err)
	}

	username := ""
	password := ""
	if u.User != nil {
		username = u.User.Username()
		password, _ = u.User.Password()
	}

	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("could not extract host from RTSP URL")
	}

	// Try common ONVIF ports
	ports := []string{"2020", "80", "8899", "8080", "8000"}
	var lastErr error

	for _, port := range ports {
		onvifAddr := fmt.Sprintf("http://%s:%s/onvif/device_service", host, port)
		client, err := NewONVIFClient(onvifAddr, username, password)
		if err == nil {
			// Test if we can resolve profiles (to verify credentials are correct)
			_, err = client.ResolveProfileAndStreamURI()
			if err == nil {
				return client, nil
			}
			lastErr = err
		} else {
			lastErr = err
		}
	}

	if lastErr != nil {
		return nil, fmt.Errorf("failed to auto-detect ONVIF on common ports: %w", lastErr)
	}
	return nil, fmt.Errorf("no ONVIF service detected on common ports for host %s", host)
}

var _ = bytes.Compare
```

---

## 4. File: `internal/stream/manager.go`

### 4.A fields inside `Manager` struct
Add the `onvifClient` field at the end of the `Manager` struct:
```go
type Manager struct {
	// ... existing fields ...

	onvifClient *ONVIFClient // ONVIF client for this camera stream
}
```

### 4.B in method `Start()`
Change the `case "rtsp"` validation block:

```go
	// Validate source
	switch m.cam.Type {
	case "rtsp":
		if strings.TrimSpace(m.cam.RTSPURL) == "" && strings.TrimSpace(m.cam.ONVIFAddress) == "" {
			return fmt.Errorf("IP URL is empty for camera %q", m.cam.Name)
		}
```

### 4.C replace method `preparePipeline()`
Update the method to resolve the RTSP URI dynamically using ONVIF when available, falling back to auto-detecting it from standard `RTSPURL` configurations:

```go
func (m *Manager) preparePipeline(ctx context.Context) (*exec.Cmd, error) {
	m.mu.Lock()
	isWebcam := m.cam.Type == "webcam"
	onvifAddr := m.cam.ONVIFAddress
	onvifUser := m.cam.ONVIFUsername
	onvifPass := m.cam.ONVIFPassword
	rtspURL := m.cam.RTSPURL
	m.mu.Unlock()

	if isWebcam {
		m.applyBestCapability(ctx)
	} else if onvifAddr != "" {
		m.logger.Printf("[%s] Resolving RTSP URL via ONVIF: %s", m.cam.Name, onvifAddr)
		client, err := NewONVIFClient(onvifAddr, onvifUser, onvifPass)
		if err != nil {
			m.logger.Printf("[%s] ONVIF client initialization failed: %v", m.cam.Name, err)
		} else {
			uri, err := client.ResolveProfileAndStreamURI()
			if err != nil {
				m.logger.Printf("[%s] ONVIF RTSP URL resolution error: %v", m.cam.Name, err)
			} else {
				m.logger.Printf("[%s] ONVIF RTSP URL successfully resolved: %s", m.cam.Name, uri)
				m.mu.Lock()
				m.cam.RTSPURL = uri
				m.onvifClient = client
				m.mu.Unlock()
			}
		}
	} else if rtspURL != "" {
		m.logger.Printf("[%s] ONVIF address not configured. Attempting to auto-detect from RTSP URL...", m.cam.Name)
		client, err := AutoDetectONVIF(rtspURL)
		if err != nil {
			m.logger.Printf("[%s] ONVIF auto-detection failed (standard RTSP stream will run): %v", m.cam.Name, err)
		} else {
			m.logger.Printf("[%s] ONVIF auto-detected successfully. PTZ controls enabled.", m.cam.Name)
			m.mu.Lock()
			m.onvifClient = client
			m.mu.Unlock()
		}
	}

	ffmpegCmd, err := m.buildFFmpegCommand(ctx)
	if err != nil {
		return nil, err
	}

	return ffmpegCmd, nil
}
```

### 4.D end of file
Append the following three functions at the very end of `internal/stream/manager.go`:

```go
// TriggerPTZ moves the camera continuously in a non-blocking goroutine.
func (m *Manager) TriggerPTZ(pan, tilt, zoom float64) {
	m.mu.Lock()
	client := m.onvifClient
	m.mu.Unlock()

	if client != nil {
		go func() {
			if err := client.ContinuousMove(pan, tilt, zoom); err != nil {
				m.logger.Printf("[%s] PTZ continuous move error: %v", m.cam.Name, err)
			}
		}()
	}
}

// TriggerPTZStop stops camera movement in a non-blocking goroutine.
func (m *Manager) TriggerPTZStop() {
	m.mu.Lock()
	client := m.onvifClient
	m.mu.Unlock()

	if client != nil {
		go func() {
			if err := client.Stop(); err != nil {
				m.logger.Printf("[%s] PTZ stop error: %v", m.cam.Name, err)
			}
		}()
	}
}

// HasPTZ returns true if the camera is configured with ONVIF and has PTZ support.
func (m *Manager) HasPTZ() bool {
	m.mu.Lock()
	client := m.onvifClient
	m.mu.Unlock()

	if client != nil {
		return client.HasPTZ()
	}
	return false
}
```

---

## 5. File: `internal/stream/multi_manager.go`

### 5.A inside `NewMultiManager()` function
Update the auto-start check conditional to:

```go
	// Always start all enabled RTSP streams immediately in the background
	for _, cam := range cfg.Cameras {
		if cam.Enabled && cam.Type == "rtsp" && (cam.RTSPURL != "" || cam.ONVIFAddress != "") {
			mgr := mm.streams[cam.ID]
			go func(c config.CameraSource, m *Manager) {
				if err := m.Start(); err != nil {
					logger.Printf("Failed to auto-start RTSP stream %q at init: %v", c.Name, err)
				}
			}(cam, mgr)
		}
	}
```

### 5.B inside `AddCamera()` method
Update the auto-start conditional checks to:

```go
	// Auto-start if source is configured
	if cam.Enabled && ((cam.Type == "rtsp" && (cam.RTSPURL != "" || cam.ONVIFAddress != "")) || (cam.Type == "webcam" && cam.Device != "")) {

---

## 6. File: `internal/ui/ptz_button.go`
Update the `NewPTZButton` function to accept `fyne.Resource` instead of a string:

```go
package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

type PTZButton struct {
	widget.Button
	OnPress   func()
	OnRelease func()
}

func NewPTZButton(icon fyne.Resource, onPress, onRelease func()) *PTZButton {
	b := &PTZButton{
		OnPress:   onPress,
		OnRelease: onRelease,
	}
	b.Icon = icon
	b.ExtendBaseWidget(b)
	return b
}
```

---

## 7. File: `internal/ui/camera_panel.go`

### 7.A Imports
Add `theme` to the imports:
```go
import (
	"image"
	"image/color"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"nystavision/internal/i18n"
)
```

### 7.B inside `buildPTZOverlay()` function
Update the button creation block to use Fyne vector icons instead of unicode arrows:

```go
	btnUp := NewPTZButton(theme.MoveUpIcon(), onDir(0, 1, 0), onStop)
	btnDown := NewPTZButton(theme.MoveDownIcon(), onDir(0, -1, 0), onStop)
	btnLeft := NewPTZButton(theme.NavigateBackIcon(), onDir(-1, 0, 0), onStop)
	btnRight := NewPTZButton(theme.NavigateNextIcon(), onDir(1, 0, 0), onStop)
	btnZoomIn := NewPTZButton(theme.ZoomInIcon(), onDir(0, 0, 1), onStop)
	btnZoomOut := NewPTZButton(theme.ZoomOutIcon(), onDir(0, 0, -1), onStop)
```
```
