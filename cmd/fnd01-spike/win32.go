package main

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wmDestroy        = 0x0002
	wmPaint          = 0x000f
	wmClose          = 0x0010
	wmKeyDown        = 0x0100
	wmLButtonDown    = 0x0201
	wmMouseMove      = 0x0200
	wmLButtonUp      = 0x0202
	wmCaptureChanged = 0x0215
	wmNCHitTest      = 0x0084
	wmHotkey         = 0x0312

	wsVisible = 0x10000000

	wsExLayered    = 0x00080000
	wsExTopmost    = 0x00000008
	wsExToolWindow = 0x00000080

	swHide    = 0
	swShow    = 5
	swRestore = 9

	hwndTopmost   = ^uintptr(0)
	swpNoZOrder   = 0x0004
	swpShowWindow = 0x0040

	lwaColorKey = 0x00000001

	htTransparent = ^uintptr(0)
	htClient      = 1

	vkEscape = 0x1b
	vkF9     = 0x78
	vkLeft   = 0x25
	vkUp     = 0x26
	vkRight  = 0x27
	vkDown   = 0x28

	modAlt      = 0x0001
	modControl  = 0x0002
	modShift    = 0x0004
	modNoRepeat = 0x4000

	pmRemove = 0x0001

	mdtEffectiveDPI = 0

	cfDIB        = 8
	gmemMoveable = 0x0002

	srcCopy      = 0x00cc0020
	captureBLT   = 0x40000000
	dibRGBColors = 0

	mouseEventFMove     = 0x0001
	mouseEventFLeftDown = 0x0002
	mouseEventFLeftUp   = 0x0004

	errorHotkeyAlreadyRegistered = 1409
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	shcore   = windows.NewLazySystemDLL("shcore.dll")
	dwmapi   = windows.NewLazySystemDLL("dwmapi.dll")

	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procRegisterClassExW              = user32.NewProc("RegisterClassExW")
	procCreateWindowExW               = user32.NewProc("CreateWindowExW")
	procDefWindowProcW                = user32.NewProc("DefWindowProcW")
	procDestroyWindow                 = user32.NewProc("DestroyWindow")
	procShowWindow                    = user32.NewProc("ShowWindow")
	procUpdateWindow                  = user32.NewProc("UpdateWindow")
	procSetWindowPos                  = user32.NewProc("SetWindowPos")
	procGetWindowRect                 = user32.NewProc("GetWindowRect")
	procGetClientRect                 = user32.NewProc("GetClientRect")
	procScreenToClient                = user32.NewProc("ScreenToClient")
	procSetLayeredWindowAttributes    = user32.NewProc("SetLayeredWindowAttributes")
	procLoadCursorW                   = user32.NewProc("LoadCursorW")
	procGetMessageW                   = user32.NewProc("GetMessageW")
	procPeekMessageW                  = user32.NewProc("PeekMessageW")
	procTranslateMessage              = user32.NewProc("TranslateMessage")
	procDispatchMessageW              = user32.NewProc("DispatchMessageW")
	procPostQuitMessage               = user32.NewProc("PostQuitMessage")
	procBeginPaint                    = user32.NewProc("BeginPaint")
	procEndPaint                      = user32.NewProc("EndPaint")
	procFillRect                      = user32.NewProc("FillRect")
	procRegisterHotKey                = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey              = user32.NewProc("UnregisterHotKey")
	procMessageBoxW                   = user32.NewProc("MessageBoxW")
	procEnumDisplayMonitors           = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW               = user32.NewProc("GetMonitorInfoW")
	procGetDC                         = user32.NewProc("GetDC")
	procReleaseDC                     = user32.NewProc("ReleaseDC")
	procOpenClipboard                 = user32.NewProc("OpenClipboard")
	procCloseClipboard                = user32.NewProc("CloseClipboard")
	procEmptyClipboard                = user32.NewProc("EmptyClipboard")
	procSetClipboardData              = user32.NewProc("SetClipboardData")
	procGetClipboardData              = user32.NewProc("GetClipboardData")
	procSetCursorPos                  = user32.NewProc("SetCursorPos")
	procGetCursorPos                  = user32.NewProc("GetCursorPos")
	procMouseEvent                    = user32.NewProc("mouse_event")
	procGetAsyncKeyState              = user32.NewProc("GetAsyncKeyState")

	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	procGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
	procGlobalFree       = kernel32.NewProc("GlobalFree")
	procGlobalLock       = kernel32.NewProc("GlobalLock")
	procGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
	procRtlMoveMemory    = kernel32.NewProc("RtlMoveMemory")

	procCreateSolidBrush   = gdi32.NewProc("CreateSolidBrush")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procBitBlt             = gdi32.NewProc("BitBlt")

	procGetDpiForMonitor = shcore.NewProc("GetDpiForMonitor")
	procDwmFlush         = dwmapi.NewProc("DwmFlush")
)

type rect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

func (r rect) width() int32  { return r.Right - r.Left }
func (r rect) height() int32 { return r.Bottom - r.Top }

type point struct {
	X int32
	Y int32
}

type msg struct {
	HWND     windows.Handle
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       point
	LPrivate uint32
}

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     windows.Handle
	HIcon         windows.Handle
	HCursor       windows.Handle
	HbrBackground windows.Handle
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       windows.Handle
}

type paintStruct struct {
	HDC         windows.Handle
	Erase       int32
	RcPaint     rect
	Restore     int32
	IncUpdate   int32
	RgbReserved [32]byte
}

type monitorInfoEx struct {
	CbSize  uint32
	Monitor rect
	Work    rect
	Flags   uint32
	Device  [32]uint16
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Masks  [3]uint32
}

func enablePerMonitorDPI() error {
	const dpiAwarenessContextPerMonitorAwareV2 = ^uintptr(3)
	r, _, err := procSetProcessDpiAwarenessContext.Call(dpiAwarenessContextPerMonitorAwareV2)
	if r == 0 {
		return callError("SetProcessDpiAwarenessContext", err)
	}
	return nil
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

func lowWord(value uintptr) int32 {
	return int32(int16(uint16(value & 0xffff)))
}

func highWord(value uintptr) int32 {
	return int32(int16(uint16((value >> 16) & 0xffff)))
}

func colorRef(r, g, b uint8) uint32 {
	return uint32(r) | uint32(g)<<8 | uint32(b)<<16
}

func getWindowRect(hwnd windows.Handle) (rect, error) {
	var bounds rect
	r, _, err := procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&bounds)))
	if r == 0 {
		return rect{}, callError("GetWindowRect", err)
	}
	return bounds, nil
}

func setWindowPos(hwnd windows.Handle, x, y, width, height int32, flags uint32) error {
	r, _, err := procSetWindowPos.Call(
		uintptr(hwnd), hwndTopmost, uintptr(x), uintptr(y),
		uintptr(width), uintptr(height), uintptr(flags),
	)
	if r == 0 {
		return callError("SetWindowPos", err)
	}
	return nil
}

func showWindow(hwnd windows.Handle, command int32) {
	procShowWindow.Call(uintptr(hwnd), uintptr(command))
}

func pumpMessages() {
	var m msg
	for {
		r, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, pmRemove)
		if r == 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}
