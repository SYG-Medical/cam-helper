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
