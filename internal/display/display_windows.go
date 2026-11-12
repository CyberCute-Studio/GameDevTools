//go:build windows

package display

import (
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	mdtEffectiveDPI    = 0
	monitorInfoPrimary = 1
	deviceNameLength   = 32
	deviceStringLength = 128
)

var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	shcore = windows.NewLazySystemDLL("shcore.dll")

	procEnumDisplayMonitors = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW     = user32.NewProc("GetMonitorInfoW")
	procEnumDisplayDevicesW = user32.NewProc("EnumDisplayDevicesW")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procGetDpiForMonitor    = shcore.NewProc("GetDpiForMonitor")
)

type monitorInfoEx struct {
	CbSize  uint32
	Monitor Rect
	Work    Rect
	Flags   uint32
	Device  [deviceNameLength]uint16
}

type displayDevice struct {
	Cb           uint32
	DeviceName   [deviceNameLength]uint16
	DeviceString [deviceStringLength]uint16
	StateFlags   uint32
	DeviceID     [deviceStringLength]uint16
	DeviceKey    [deviceStringLength]uint16
}

// enumeration serializes rawDisplays and carries one enumeration's results
// to the shared monitor callback. The callback is registered with the Go
// runtime exactly once: the runtime permits only 2000 distinct callback
// functions per process and never releases them, so a fresh closure per
// enumeration would exhaust the limit and crash the process.
var enumeration = struct {
	sync.Mutex
	displays []Display
	err      error
}{}

var monitorEnumProc = windows.NewCallback(collectMonitor)

func collectMonitor(monitor windows.Handle, _ windows.Handle, _ *Rect, _ uintptr) uintptr {
	info := monitorInfoEx{CbSize: uint32(unsafe.Sizeof(monitorInfoEx{}))}
	r, _, err := procGetMonitorInfoW.Call(uintptr(monitor), uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		enumeration.err = callError("GetMonitorInfoW", err)
		return 0
	}
	item := Display{
		DeviceName: windows.UTF16ToString(info.Device[:]),
		Bounds:     info.Monitor,
		WorkArea:   info.Work,
		Primary:    info.Flags&monitorInfoPrimary != 0,
	}
	hr, _, _ := procGetDpiForMonitor.Call(uintptr(monitor), mdtEffectiveDPI, uintptr(unsafe.Pointer(&item.DPIX)), uintptr(unsafe.Pointer(&item.DPIY)))
	if int32(hr) < 0 {
		enumeration.err = fmt.Errorf("GetDpiForMonitor(%s): HRESULT %#x", item.DeviceName, uint32(hr))
		return 0
	}
	item.Name = friendlyName(item.DeviceName)
	enumeration.displays = append(enumeration.displays, item)
	return 1
}

// rawDisplays enumerates connected displays in callback order; picker
// numbers are assigned by the platform-independent Monitor.
func rawDisplays() ([]Display, error) {
	enumeration.Lock()
	defer enumeration.Unlock()
	enumeration.displays = nil
	enumeration.err = nil
	r, _, err := procEnumDisplayMonitors.Call(0, 0, monitorEnumProc, 0)
	if enumeration.err != nil {
		return nil, enumeration.err
	}
	if r == 0 {
		return nil, callError("EnumDisplayMonitors", err)
	}
	if len(enumeration.displays) == 0 {
		return nil, ErrNoDisplays
	}
	return append([]Display(nil), enumeration.displays...), nil
}

// friendlyName queries the monitor attached to the given adapter device for
// its description. It returns an empty string when Windows provides none,
// which callers present as "where Windows provides it" per SCR-041.
func friendlyName(deviceName string) string {
	device := displayDevice{Cb: uint32(unsafe.Sizeof(displayDevice{}))}
	r, _, _ := procEnumDisplayDevicesW.Call(
		uintptr(unsafe.Pointer(utf16(deviceName))),
		0,
		uintptr(unsafe.Pointer(&device)),
		0,
	)
	if r == 0 {
		return ""
	}
	return windows.UTF16ToString(device.DeviceString[:])
}

// cursorPosition returns the physical virtual-desktop cursor coordinates.
// The process runs per-monitor DPI aware, so no implicit scaling applies.
func cursorPosition() (Point, error) {
	var position Point
	r, _, err := procGetCursorPos.Call(uintptr(unsafe.Pointer(&position)))
	if r == 0 {
		return Point{}, callError("GetCursorPos", err)
	}
	return position, nil
}

func callError(operation string, err error) error {
	if err == windows.ERROR_SUCCESS {
		return fmt.Errorf("%s failed", operation)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func utf16(value string) *uint16 {
	result, err := windows.UTF16PtrFromString(value)
	if err != nil {
		panic(err)
	}
	return result
}
