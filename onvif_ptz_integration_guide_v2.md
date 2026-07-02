# SYG Camera Helper - ONVIF & PTZ Entegrasyon Rehberi (Düzeltilmiş)

Bu rehber, Go tabanlı ONVIF kamera kontrolünü, dinamik RTSP adresi çözmeyi ve Fyne UI üzerinde yön tuşları (PTZ) entegrasyonunu adım adım nasıl yapacağınızı anlatmaktadır.

> [!NOTE]
> Bu rehber, `use-go/onvif` kütüphanesinin gerçek tip yapılarına (`onvif.ReferenceToken`, `*onvif.Vector2D`, `*onvif.Vector1D` vb.) ve mevcut `cam-helper` projesinin kod yapısına uygun olarak düzeltilmiştir.

---

## Adım 1: Bağımlılıkların Eklenmesi

İlk olarak projenizin kök dizininde terminalden ONVIF kütüphanesini indirin:

```bash
go get github.com/use-go/onvif
```

---

## Adım 2: Konfigürasyon Yapısının Güncellenmesi

`internal/config/config.go` dosyasındaki `CameraSource` struct'ına ONVIF bağlantı bilgilerini ve PTZ hız ayarını ekleyin:

```go
// internal/config/config.go dosyasında CameraSource struct'ını şu şekilde güncelleyin:
type CameraSource struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Type          string `json:"type"`     // "rtsp" veya "webcam"
	RTSPURL       string `json:"rtsp_url"` // Statik olarak girilirse kullanılmaya devam eder
	Device        string `json:"device"`   
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	FPS           int    `json:"fps"`
	PixelFormat   string `json:"pixel_format,omitempty"`
	DevicePath    string `json:"device_path,omitempty"`
	Enabled       bool   `json:"enabled"`

	// ONVIF ve PTZ Bağlantı Alanları
	ONVIFAddress  string  `json:"onvif_address,omitempty"`  // Örn: "192.168.1.100:80" veya "http://admin:pass@192.168.1.100/onvif/device_service"
	ONVIFUsername string  `json:"onvif_username,omitempty"` // İsteğe bağlı (URL'de yoksa buraya yazılır)
	ONVIFPassword string  `json:"onvif_password,omitempty"` // İsteğe bağlı
	PTZSpeed      float64 `json:"ptz_speed,omitempty"`      // 0.0-1.0 arası (varsayılan: 0.3 — tıbbi cihaz için güvenli hız)
}
```

Ayrıca `config.example.json` dosyasını da güncelleyerek kullanıcılara yeni alanları gösterin:

```json
{
  "cameras": [
    {
      "id": "cam-1",
      "name": "Kamera 1",
      "type": "rtsp",
      "rtsp_url": "",
      "onvif_address": "192.168.1.100:80",
      "onvif_username": "admin",
      "onvif_password": "12345",
      "ptz_speed": 0.3,
      "width": 1280,
      "height": 720,
      "fps": 30,
      "enabled": true
    }
  ]
}
```

---

## Adım 3: ONVIF İletişim İstemcisi (`internal/stream/onvif.go`)

Yeni bir `internal/stream/onvif.go` dosyası oluşturun.

> [!IMPORTANT]
> `use-go/onvif` kütüphanesinde PTZ ve Media struct tipleri (ör. `PTZSpeed`, `Vector2D`, `ReferenceToken`) **ana pakette** (`github.com/use-go/onvif`) tanımlıdır, alt paketlerde (`ptz`, `media`) değil. Alt paketler yalnızca SOAP istek yapılarını (`ContinuousMove`, `Stop`, `GetProfiles` vb.) tanımlar.

```go
package stream

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/use-go/onvif"
	media_ws "github.com/use-go/onvif/media"
	ptz_ws "github.com/use-go/onvif/ptz"
)

// ── SOAP XML Ayrıştırma Yapıları ─────────────────────────────────
// ONVIF SOAP yanıtları namespace kullanır. Namespace belirtilmezse
// xml.Unmarshal her zaman boş struct döndürür.
//
// NOT: ONVIF SOAP 1.2 kullanır — namespace "http://www.w3.org/2003/05/soap-envelope"

type soapEnvelope struct {
	XMLName xml.Name `xml:"http://www.w3.org/2003/05/soap-envelope Envelope"`
	Body    soapBody `xml:"http://www.w3.org/2003/05/soap-envelope Body"`
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

// ── ONVIFClient ──────────────────────────────────────────────────

// ONVIFClient kamerayla SOAP protokolü üzerinden konuşan yapı.
// Thread-safe: tüm değiştirilebilir alanlar mu ile korunur.
type ONVIFClient struct {
	dev      *onvif.Device
	username string
	password string

	mu           sync.Mutex // profileToken ve hasPTZ'yi korur
	profileToken string
	hasPTZ       bool

	timerMu   sync.Mutex // fail-safe timer'ı korur
	stopTimer *time.Timer
}

// NewONVIFClient yeni bir ONVIF bağlantısı başlatır.
// URL'de gömülü kullanıcı adı/şifre varsa (http://admin:12345@192.168.1.100/...) otomatik olarak ayıklar.
func NewONVIFClient(address, username, password string) (*ONVIFClient, error) {
	addr := address
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}

	// 1. URL içerisinden username ve password ayıklama
	u, err := url.Parse(addr)
	if err == nil {
		if u.User != nil {
			username = u.User.Username()
			if pass, ok := u.User.Password(); ok {
				password = pass
			}
			// Güvenlik ve temiz istek için kimlik bilgilerini URL'den temizliyoruz
			u.User = nil
			addr = u.String()
		}
	}

	if !strings.Contains(addr, "/onvif/device_service") {
		addr = strings.TrimSuffix(addr, "/") + "/onvif/device_service"
	}

	// 2. Ayıklanan verilerle ONVIF istemcisini oluşturma
	dev, err := onvif.NewDevice(onvif.DeviceParams{
		Xaddr:    addr,
		Username: username,
		Password: password,
	})
	if err != nil {
		return nil, fmt.Errorf("onvif device creation failed: %w", err)
	}

	client := &ONVIFClient{
		dev:      dev,
		username: username,
		password: password,
	}

	// PTZ yeteneği olup olmadığını kontrol et.
	// Kütüphane endpoint anahtarlarını strings.ToLower ile kaydettiği için "ptz" küçük harfle sorgulanmalıdır.
	client.hasPTZ = dev.GetEndpoint("ptz") != ""

	return client, nil
}

// HasPTZ kameranın PTZ desteği olup olmadığını döner (thread-safe).
func (c *ONVIFClient) HasPTZ() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hasPTZ
}

// ResolveProfileAndStreamURI aktif profili bulur ve RTSP adresini çeker.
func (c *ONVIFClient) ResolveProfileAndStreamURI() (string, error) {
	// 1. GetProfiles servisini çağır
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

	// İlk bulduğu medya profilinin token'ını al
	token := env.Body.GetProfilesResponse.Profiles[0].Token

	c.mu.Lock()
	c.profileToken = token
	c.mu.Unlock()

	// 2. GetStreamUri servisini çağır
	// NOT: StreamSetup ve Transport tipleri onvif ana paketindedir, media paketinde değil
	getStreamUri := media_ws.GetStreamUri{
		StreamSetup: onvif.StreamSetup{
			Stream:    onvif.StreamType("RTP-Unicast"),
			Transport: onvif.Transport{Protocol: "RTSP"},
		},
		ProfileToken: onvif.ReferenceToken(token),
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

// ContinuousMove motorlu kamerayı hareket ettirir.
// Hız aralığı genellikle -1.0 ile 1.0 arasındadır.
// Her çağrıda 4 saniyelik bir fail-safe timer kurulur:
// eğer bu süre içinde yeni bir hareket veya Stop gelmezse,
// kamera otomatik durdurularak "PTZ runaway" önlenir.
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

	// NOT: PTZSpeed, Vector2D, Vector1D tipleri onvif ana paketindedir.
	// PanTilt ve Zoom alanları pointer'dır (*Vector2D, *Vector1D).
	move := ptz_ws.ContinuousMove{
		ProfileToken: onvif.ReferenceToken(token),
		Velocity: onvif.PTZSpeed{
			PanTilt: &onvif.Vector2D{X: pan, Y: tilt},
			Zoom:    &onvif.Vector1D{X: zoom},
		},
	}

	resp, err := c.dev.CallMethod(move)
	if err != nil {
		return fmt.Errorf("ptz move failed: %w", err)
	}
	resp.Body.Close()

	// ── Fail-Safe Timer ──────────────────────────────
	// Varsa eski güvenlik zamanlayıcısını iptal et
	c.timerMu.Lock()
	if c.stopTimer != nil {
		c.stopTimer.Stop()
	}
	// 4 saniye sonra kamerayı otomatik olarak durduracak zamanlayıcıyı kur
	c.stopTimer = time.AfterFunc(4*time.Second, func() {
		_ = c.Stop()
	})
	c.timerMu.Unlock()

	return nil
}

// Stop kameranın hareketini durdurur.
func (c *ONVIFClient) Stop() error {
	// Önce fail-safe timer'ı iptal et
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

	// NOT: PanTilt ve Zoom alanları *bool tipindedir, doğrudan true atanamaz.
	panTiltStop := true
	zoomStop := true
	stop := ptz_ws.Stop{
		ProfileToken: onvif.ReferenceToken(token),
		PanTilt:      &panTiltStop,
		Zoom:         &zoomStop,
	}

	resp, err := c.dev.CallMethod(stop)
	if err != nil {
		return fmt.Errorf("ptz stop failed: %w", err)
	}
	resp.Body.Close()
	return nil
}
```

---

## Adım 4: stream.Manager ile Dinamik Bağlantı Entegrasyonu

`internal/stream/manager.go` dosyasında iki ana değişiklik yapılmalıdır:

### A. Manager Struct'ına ONVIFClient Alanı Ekleme

```go
// internal/stream/manager.go dosyasında Manager struct'ına ekleyin:
type Manager struct {
	// ... mevcut tüm alanlar aynen kalacak ...

	onvifClient *ONVIFClient // ONVIF İstemci Nesnesi (ONVIF kameralar için)
}
```

### B. Start() Validation Güncellenmesi

Mevcut `Start()` metodu, RTSP URL boş olduğunda hata döndürür. ONVIF kameraları için bu kontrolü bypass etmeliyiz:

```go
// Start() metodunun validation bölümünü şu şekilde güncelleyin:
case "rtsp":
	if strings.TrimSpace(m.cam.RTSPURL) == "" && strings.TrimSpace(m.cam.ONVIFAddress) == "" {
		return fmt.Errorf("IP URL is empty for camera %q", m.cam.Name)
	}
```

> [!IMPORTANT]
> Bu değişiklik yapılmazsa ONVIF adresi girilmiş ama RTSP URL boş bırakılmış kameralar **asla başlatılamaz**. Çünkü `Start()` daha `preparePipeline()` çağrılmadan önce hata döndürür.

### C. preparePipeline() Güncellenmesi

```go
func (m *Manager) preparePipeline(ctx context.Context) (*exec.Cmd, error) {
	m.mu.Lock()
	isWebcam := m.cam.Type == "webcam"
	onvifAddr := m.cam.ONVIFAddress
	onvifUser := m.cam.ONVIFUsername
	onvifPass := m.cam.ONVIFPassword
	m.mu.Unlock()

	if isWebcam {
		m.applyBestCapability(ctx)
	} else if onvifAddr != "" {
		// Dinamik RTSP Çözme İşlemi
		m.logger.Printf("[%s] ONVIF üzerinden RTSP adresi çözümleniyor: %s", m.cam.Name, onvifAddr)
		
		client, err := NewONVIFClient(onvifAddr, onvifUser, onvifPass)
		if err != nil {
			m.logger.Printf("[%s] ONVIF client başlatılamadı: %v", m.cam.Name, err)
		} else {
			uri, err := client.ResolveProfileAndStreamURI()
			if err != nil {
				m.logger.Printf("[%s] ONVIF RTSP URL çözme hatası: %v", m.cam.Name, err)
			} else {
				m.logger.Printf("[%s] ONVIF RTSP adresi başarıyla alındı: %s", m.cam.Name, uri)
				m.mu.Lock()
				m.cam.RTSPURL = uri
				m.onvifClient = client // İleride PTZ kullanmak üzere client'ı sakla
				m.mu.Unlock()
			}
		}
	}

	ffmpegCmd, err := m.buildFFmpegCommand(ctx)
	if err != nil {
		return nil, err
	}

	return ffmpegCmd, nil
}
```

### D. PTZ Komutlarını Dışarıya Açma (Non-blocking)

```go
// Manager üzerinden PTZ komutlarını güvenli bir şekilde dışarıya açın:
func (m *Manager) TriggerPTZ(pan, tilt, zoom float64) {
	m.mu.Lock()
	client := m.onvifClient
	m.mu.Unlock()

	if client != nil {
		// Ağ gecikmesi UI thread'ini dondurmasın diye goroutine içinde çalıştırılır
		go func() {
			if err := client.ContinuousMove(pan, tilt, zoom); err != nil {
				m.logger.Printf("[%s] PTZ hareket hatası: %v", m.cam.Name, err)
			}
		}()
	}
}

func (m *Manager) TriggerPTZStop() {
	m.mu.Lock()
	client := m.onvifClient
	m.mu.Unlock()

	if client != nil {
		go func() {
			if err := client.Stop(); err != nil {
				m.logger.Printf("[%s] PTZ durdurma hatası: %v", m.cam.Name, err)
			}
		}()
	}
}

// HasPTZ bu kameranın PTZ desteği olup olmadığını döner.
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

## Adım 5: MultiManager Auto-Start Güncellenmesi

`internal/stream/multi_manager.go` dosyasında ONVIF kameralarının otomatik başlatılması için auto-start koşulunu güncelleyin:

```go
// NewMultiManager fonksiyonu içerisindeki auto-start döngüsünü güncelleyin:
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

Aynı değişiklik `AddCamera()` metodunda da yapılmalıdır:

```go
// AddCamera fonksiyonundaki auto-start koşulunu da güncelleyin:
if cam.Enabled && ((cam.Type == "rtsp" && (cam.RTSPURL != "" || cam.ONVIFAddress != "")) || (cam.Type == "webcam" && cam.Device != "")) {
```

---

## Adım 6: Fyne UI Buton Yapısı ve Arayüz Entegrasyonu

Kullanıcı yön tuşuna bastığı sürece kameranın dönmesi, bıraktığında ise durması gerekir. Standart Fyne butonları basılı tutmayı algılamadığı için, `desktop.Mouseable` destekli özel bir buton geliştireceğiz.

### A. PTZ Kontrol Butonu (`internal/ui/ptz_button.go`)

```go
package ui

import (
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// PTZButton basılı tutma (mouse down) ve bırakma (mouse up) eventlerini yakalayan buton.
type PTZButton struct {
	widget.Button
	OnPress   func()
	OnRelease func()
}

func NewPTZButton(label string, onPress, onRelease func()) *PTZButton {
	b := &PTZButton{
		OnPress:   onPress,
		OnRelease: onRelease,
	}
	b.Text = label
	b.ExtendBaseWidget(b)
	return b
}

// MouseDown butona basıldığında tetiklenir.
func (b *PTZButton) MouseDown(me *desktop.MouseEvent) {
	if me.Button == desktop.MouseButtonPrimary && b.OnPress != nil {
		b.OnPress()
	}
}

// MouseUp buton bırakıldığında tetiklenir.
func (b *PTZButton) MouseUp(me *desktop.MouseEvent) {
	if b.OnRelease != nil {
		b.OnRelease()
	}
}
```

### B. Arayüze Kontrollerin Eklenmesi

Seçili kamera hücresinde PTZ butonlarını göstermek için `internal/gui/app.go` veya ayrı bir dosyaya ekleyin:

```go
import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	
	"nystavision/internal/stream"
	"nystavision/internal/ui"
)

func (a *App) buildPTZControls(manager *stream.Manager) fyne.CanvasObject {
	if manager == nil || !manager.HasPTZ() {
		return widget.NewLabel("") // PTZ desteklenmiyor veya kamera seçilmedi
	}

	// PTZ hız değeri - config'den al, varsayılan 0.3
	speed := 0.3 // Tıbbi cihaz için güvenli varsayılan hız
	// TODO: manager'dan cam.PTZSpeed okuyarak config'den alınabilir

	// Yön Butonları
	btnUp := ui.NewPTZButton("▲",
		func() { manager.TriggerPTZ(0, speed, 0) },
		func() { manager.TriggerPTZStop() },
	)
	btnDown := ui.NewPTZButton("▼",
		func() { manager.TriggerPTZ(0, -speed, 0) },
		func() { manager.TriggerPTZStop() },
	)
	btnLeft := ui.NewPTZButton("◀",
		func() { manager.TriggerPTZ(-speed, 0, 0) },
		func() { manager.TriggerPTZStop() },
	)
	btnRight := ui.NewPTZButton("▶",
		func() { manager.TriggerPTZ(speed, 0, 0) },
		func() { manager.TriggerPTZStop() },
	)

	// Yakınlaştırma (Zoom) Butonları
	btnZoomIn := ui.NewPTZButton("Zoom +",
		func() { manager.TriggerPTZ(0, 0, speed) },
		func() { manager.TriggerPTZStop() },
	)
	btnZoomOut := ui.NewPTZButton("Zoom -",
		func() { manager.TriggerPTZ(0, 0, -speed) },
		func() { manager.TriggerPTZStop() },
	)

	// Yön tuşlarının 3x3 grid düzeni
	directionGrid := container.NewGridWithColumns(3,
		layout.NewSpacer(), btnUp, layout.NewSpacer(),
		btnLeft, layout.NewSpacer(), btnRight,
		layout.NewSpacer(), btnDown, layout.NewSpacer(),
	)

	// Yakınlaştırma grubu
	zoomGroup := container.NewHBox(btnZoomIn, btnZoomOut)

	// Hepsini birleştir
	ptzContainer := container.NewVBox(
		widget.NewLabelWithStyle("PTZ Kontrolü", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		directionGrid,
		container.NewCenter(zoomGroup),
	)

	return ptzContainer
}
```

---

## Adım 7: Doğrulama Kontrol Listesi

Entegrasyondan sonra aşağıdaki senaryoları test edin:

### Derleme Testi
```bash
go build ./...
go vet ./...
go test -race ./internal/stream/...
```

### Fonksiyonel Test Senaryoları

| # | Senaryo | Beklenen Sonuç |
|---|---------|---------------|
| 1 | Sadece `onvif_address` girili, `rtsp_url` boş | Kamera başlar, RTSP otomatik çözülür |
| 2 | Hem `onvif_address` hem `rtsp_url` girili | Statik RTSP kullanılır (ONVIF'e gerek yok) |
| 3 | PTZ butonuna basılı tut → bırak | Kamera döner → durur |
| 4 | PTZ butonuna bas → ağ kablosu çek | 4 saniye sonra kamera otomatik durur (fail-safe) |
| 5 | PTZ desteklemeyen ONVIF kamera | PTZ kontrolleri gizlenir, stream normal çalışır |
| 6 | Yanlış ONVIF şifresi | Hata loglanır, kamera başlamaz, UI amber duruma geçer |
| 7 | Uygulama restart | ONVIF adresi tekrar çözülür (persist edilmez — doğru davranış) |

---

## Bilinen Sınırlamalar

1. **SOAP XML Ayrıştırma**: Bu rehber manuel XML parsing kullanmaktadır. ONVIF SOAP 1.1 ve 1.2 namespace farkları bazı kameralarda sorun çıkarabilir. Eğer sorun yaşanırsa `http://schemas.xmlsoap.org/soap/envelope/` (SOAP 1.1) namespace'ine geçiş denenmelidir.

3. **Kütüphane Bakım Durumu**: `use-go/onvif` aktif olarak bakımda olmayabilir. Alternatif olarak `github.com/0x524a/onvif-go` veya `github.com/gowvp/onvif` fork'ları değerlendirilebilir.
