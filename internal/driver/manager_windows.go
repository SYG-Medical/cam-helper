//go:build windows

package driver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
	"reflect"

	"rtsp-virtual-cam-agent/internal/config"
	"rtsp-virtual-cam-agent/internal/logging"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	MaxSharedImageSize = 3840 * 2160 * 4 // Support up to 4K ARGB
)

type Manager struct {
	cfg    config.Config
	root   string
	logger *logging.Logger
	writer *UnityWriter
}

type UnityWriter struct {
	mapping      windows.Handle
	mutex        windows.Handle
	eventWant    windows.Handle
	eventSent    windows.Handle
	buffer       []byte
	header       *sharedMemHeader
	maxFrameSize int
}

type sharedMemHeader struct {
	MaxSize    uint32
	Width      int32
	Height     int32
	Stride     int32
	Format     int32
	ResizeMode int32
	MirrorMode int32
	Timeout    int32
}

func New(cfg config.Config, logger *logging.Logger) (*Manager, error) {
	root, err := executableRoot()
	if err != nil {
		return nil, err
	}
	return &Manager{cfg: cfg, root: root, logger: logger}, nil
}

func (m *Manager) EnsureInstalled(ctx context.Context) error {
	if ok, _ := m.VirtualCameraPresent(ctx); ok {
		return nil
	}

	// For UnityCapture, we can pre-configure the name in registry so it matches our config
	// even before registration, or right after.
	if m.cfg.TargetVirtualCamera != "" {
		m.configureUnityCaptureName(m.cfg.TargetVirtualCamera)
	}

	// We need to register the driver, check for admin rights first
	if !isAdmin() {
		return errors.New("Virtual camera driver is not registered. Please run the application as Administrator once, or use the setup installer to register the driver.")
	}

	// Try multiple locations for the UnityCapture DLL
	candidates := []string{
		"third_party/driver/virtual-camera-installer.dll",
		"internal/assets/third_party/driver/virtual-camera-installer.dll",
	}

	for _, candidate := range candidates {
		dllPath := m.resolve(candidate)
		if _, err := os.Stat(dllPath); err == nil {
			m.logger.Printf("registering virtual camera filter: %s", dllPath)
			// regsvr32 /s requires admin
			cmd := exec.CommandContext(ctx, "regsvr32", "/s", dllPath)
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("register filter (%s): %w", dllPath, err)
			}
			return nil
		}
	}

	// Fallback to legacy installer if exists
	installer := m.resolve(m.cfg.DriverInstaller)
	if _, err := os.Stat(installer); err == nil {
		m.logger.Printf("running legacy driver installer: %s", installer)
		cmd := exec.CommandContext(ctx, installer, "/S")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		return cmd.Run()
	}

	return errors.New("virtual camera driver not found and no installer available. Please run the setup installer as Administrator.")
}

func isAdmin() bool {
	var sid *windows.SID
	err := windows.AllocateAndInitializeSid(
		&windows.SECURITY_NT_AUTHORITY,
		2,
		windows.SECURITY_BUILTIN_DOMAIN_RID,
		windows.DOMAIN_ALIAS_RID_ADMINS,
		0, 0, 0, 0, 0, 0,
		&sid,
	)
	if err != nil {
		return false
	}
	defer windows.FreeSid(sid)

	token := windows.Token(0)
	member, err := token.IsMember(sid)
	if err != nil {
		return false
	}
	return member
}

func (m *Manager) VirtualCameraPresent(ctx context.Context) (bool, error) {
	if m.cfg.TargetVirtualCamera == "" {
		return false, errors.New("target virtual camera is empty")
	}

	// First try Registry check (most reliable for DirectShow filters)
	if m.checkFilterInRegistry(m.cfg.TargetVirtualCamera) {
		return true, nil
	}

	// Fallback to PowerShell PnP check
	script := fmt.Sprintf(`Get-PnpDevice -Class Camera,Image | Where-Object { $_.FriendlyName -like '*%s*' } | Select-Object -ExpandProperty FriendlyName`, escapePowerShellLike(m.cfg.TargetVirtualCamera))
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err == nil && strings.TrimSpace(string(out)) != "" {
		return true, nil
	}

	return false, nil
}

func (m *Manager) checkFilterInRegistry(name string) bool {
	// DirectShow Video Input Category
	const videoInputCat = `SOFTWARE\Microsoft\ActiveMovie\devenum\{860BB310-5D01-11D0-BD3B-00A0C911CE86}`

	// Check both HKLM and HKCU
	roots := []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER}
	for _, root := range roots {
		k, err := registry.OpenKey(root, videoInputCat, registry.READ)
		if err != nil {
			continue
		}
		defer k.Close()

		subkeys, err := k.ReadSubKeyNames(-1)
		if err != nil {
			continue
		}

		for _, subkey := range subkeys {
			sk, err := registry.OpenKey(k, subkey, registry.READ)
			if err != nil {
				continue
			}
			friendlyName, _, err := sk.GetStringValue("FriendlyName")
			sk.Close()
			if err == nil && strings.Contains(strings.ToLower(friendlyName), strings.ToLower(name)) {
				return true
			}
		}
	}
	return false
}

func (m *Manager) configureUnityCaptureName(name string) {
	// UnityCapture reads its device name from this registry key
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\UnityCapture`, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	_ = k.SetStringValue("DeviceName", name)
}

func (m *Manager) UpdateConfig(cfg config.Config) {
	m.cfg = cfg
}

func (m *Manager) BridgePath() string {
	return m.resolve(m.cfg.DriverBridge)
}

func (m *Manager) StartBridge(ctx context.Context) (*exec.Cmd, error) {
	// If we find a way to avoid the bridge, we return nil here.
	// For now, we still try to resolve it but allow it to be missing.
	bridge := m.BridgePath()
	if _, err := os.Stat(bridge); err != nil {
		m.logger.Printf("virtual camera bridge not found, attempting direct mode")
		return nil, nil 
	}

	args := []string{
		"--camera-name", m.cfg.TargetVirtualCamera,
		"--listen", fmt.Sprintf("udp://127.0.0.1:%d", m.cfg.BridgePort),
		"--width", fmt.Sprintf("%d", m.cfg.Width),
		"--height", fmt.Sprintf("%d", m.cfg.Height),
		"--fps", fmt.Sprintf("%d", m.cfg.FPS),
	}

	cmd := exec.CommandContext(ctx, bridge, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd, nil
}

func (m *Manager) UseBridge() bool {
	// If bridge exists, use it. Otherwise, try direct dshow output.
	_, err := os.Stat(m.BridgePath())
	return err == nil
}

func (m *Manager) IsDeviceBusy() (string, bool, error) {
	return "", false, nil
}

func (m *Manager) FFmpegOutputTarget() string {
	if m.UseBridge() {
		return fmt.Sprintf("udp://127.0.0.1:%d?pkt_size=1316", m.cfg.BridgePort)
	}
	// Built-in mode uses pipe
	return "-"
}

func (m *Manager) OpenWriter() error {
	if m.writer != nil {
		return nil
	}

	capNum := 0 // Default to device 0
	suffix := ""
	if capNum > 0 {
		suffix = fmt.Sprintf("%d", capNum)
	}

	mutexName, _ := windows.UTF16PtrFromString("Global\\UnityCapture_Mutx" + suffix)
	eventWantName, _ := windows.UTF16PtrFromString("Global\\UnityCapture_Want" + suffix)
	eventSentName, _ := windows.UTF16PtrFromString("Global\\UnityCapture_Sent" + suffix)
	dataName, _ := windows.UTF16PtrFromString("Global\\UnityCapture_Data" + suffix)

	// Create/Open mapping
	hMapping, err := windows.CreateFileMapping(windows.InvalidHandle, nil, windows.PAGE_READWRITE, 0, uint32(unsafe.Sizeof(sharedMemHeader{})+MaxSharedImageSize), dataName)
	if err != nil {
		// Try without Global prefix if Global fails (e.g. no permission)
		mutexName, _ = windows.UTF16PtrFromString("UnityCapture_Mutx" + suffix)
		eventWantName, _ = windows.UTF16PtrFromString("UnityCapture_Want" + suffix)
		eventSentName, _ = windows.UTF16PtrFromString("UnityCapture_Sent" + suffix)
		dataName, _ = windows.UTF16PtrFromString("UnityCapture_Data" + suffix)
		hMapping, err = windows.CreateFileMapping(windows.InvalidHandle, nil, windows.PAGE_READWRITE, 0, uint32(unsafe.Sizeof(sharedMemHeader{})+MaxSharedImageSize), dataName)
		if err != nil {
			return fmt.Errorf("create file mapping: %w", err)
		}
	}

	ptr, err := windows.MapViewOfFile(hMapping, windows.FILE_MAP_WRITE, 0, 0, 0)
	if err != nil {
		windows.CloseHandle(hMapping)
		return fmt.Errorf("map view of file: %w", err)
	}

	hMutex, err := windows.CreateMutex(nil, false, mutexName)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		windows.UnmapViewOfFile(ptr)
		windows.CloseHandle(hMapping)
		return fmt.Errorf("create mutex: %w", err)
	}

	hEventWant, err := windows.CreateEvent(nil, 0, 0, eventWantName)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		windows.CloseHandle(hMutex)
		windows.UnmapViewOfFile(ptr)
		windows.CloseHandle(hMapping)
		return fmt.Errorf("create event want: %w", err)
	}

	hEventSent, err := windows.CreateEvent(nil, 0, 0, eventSentName)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		windows.CloseHandle(hEventWant)
		windows.CloseHandle(hMutex)
		windows.UnmapViewOfFile(ptr)
		windows.CloseHandle(hMapping)
		return fmt.Errorf("create event sent: %w", err)
	}

	// Initialize header under mutex
	s, err := windows.WaitForSingleObject(hMutex, 2000)
	if err == nil && s == windows.WAIT_OBJECT_0 {
		header := (*sharedMemHeader)(unsafe.Pointer(ptr))
		header.MaxSize = uint32(MaxSharedImageSize)
		windows.ReleaseMutex(hMutex)
	} else {
		m.logger.Printf("warning: could not lock mutex for header initialization: %v (status %d)", err, s)
	}

	header := (*sharedMemHeader)(unsafe.Pointer(ptr))

	// Create a slice that points to the data area
	dataPtr := ptr + unsafe.Sizeof(sharedMemHeader{})
	var dataSlice []byte
	sh := (*reflect.SliceHeader)(unsafe.Pointer(&dataSlice))
	sh.Data = dataPtr
	sh.Len = MaxSharedImageSize
	sh.Cap = MaxSharedImageSize

	m.writer = &UnityWriter{
		mapping:   hMapping,
		mutex:     hMutex,
		eventWant: hEventWant,
		eventSent: hEventSent,
		buffer:    dataSlice,
		header:    header,
	}

	return nil
}

func (m *Manager) CloseWriter() {
	if m.writer == nil {
		return
	}
	windows.CloseHandle(m.writer.eventSent)
	windows.CloseHandle(m.writer.eventWant)
	windows.CloseHandle(m.writer.mutex)
	windows.UnmapViewOfFile(uintptr(unsafe.Pointer(m.writer.header)))
	windows.CloseHandle(m.writer.mapping)
	m.writer = nil
}

// ErrNoConsumer is returned when the virtual camera filter is not yet
// consuming frames (e.g. the DirectShow filter hasn't opened the device).
// Callers should treat this as a transient, non-fatal condition.
var ErrNoConsumer = errors.New("virtual camera has no consumer yet")

func (m *Manager) WriteFrame(width, height int, pix []byte) error {
	if m.writer == nil {
		if err := m.OpenWriter(); err != nil {
			return err
		}
	}

	w := m.writer
	if len(pix) > MaxSharedImageSize {
		return errors.New("frame too large")
	}

	// Wait for the DirectShow filter to release the mutex.
	// WAIT_OBJECT_0  (0)    – we own it, proceed.
	// WAIT_ABANDONED (0x80) – previous owner (filter) died; ownership is
	//                         still transferred to us so we MUST release.
	// WAIT_TIMEOUT   (258)  – no consumer has opened the device yet; skip
	//                         this frame without logging noise.
	s, err := windows.WaitForSingleObject(w.mutex, 100)
	switch {
	case err != nil:
		return fmt.Errorf("wait for mutex: %w", err)
	case s == windows.WAIT_OBJECT_0, s == windows.WAIT_ABANDONED:
		// We own the mutex — always release it when done.
		defer func() {
			if rerr := windows.ReleaseMutex(w.mutex); rerr != nil {
				m.logger.Printf("release mutex error: %v", rerr)
			}
		}()
	case s == uint32(windows.WAIT_TIMEOUT):
		// Filter not running yet; silently drop the frame.
		return ErrNoConsumer
	default:
		return fmt.Errorf("wait for mutex: unexpected status %d", s)
	}

	w.header.Width = int32(width)
	w.header.Height = int32(height)
	w.header.Stride = int32(width * 4) // BGRA: 4 bytes per pixel
	w.header.Format = 0                // FORMAT_UINT8
	w.header.Timeout = 1000

	copy(w.buffer, pix)

	// Signal the filter that a new frame is ready.
	windows.SetEvent(w.eventSent)

	return nil
}

func (m *Manager) resolve(candidate string) string {
	if filepath.IsAbs(candidate) {
		return candidate
	}
	return filepath.Join(m.root, filepath.FromSlash(candidate))
}

func executableRoot() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	return filepath.Dir(exe), nil
}

func escapePowerShellLike(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}
