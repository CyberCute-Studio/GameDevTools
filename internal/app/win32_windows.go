//go:build windows

package app

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wmDestroy        = 0x0002
	wmSize           = 0x0005
	wmClose          = 0x0010
	wmCommand        = 0x0111
	wmVScroll        = 0x0115
	wmCtlColorStatic = 0x0138
	wmSetFont        = 0x0030
	wmMouseWheel     = 0x020a
	wmTimer          = 0x0113
	wmDisplayChange  = 0x007e
	wmDPIChanged     = 0x02e0
	wmUser           = 0x0400
	wmAppDispatch    = wmUser + 1

	wsClipChildren     = 0x02000000
	wsOverlappedWindow = 0x00cf0000
	wsVScroll          = 0x00200000

	bmSetCheck  = 0x00f1
	bstChecked  = 1
	bnSetFocus  = 6
	enSetFocus  = 0x0100
	cbnSetFocus = 3

	sbVert          = 1
	sbLineUp        = 0
	sbLineDown      = 1
	sbPageUp        = 2
	sbPageDown      = 3
	sbThumbPosition = 4
	sbThumbTrack    = 5
	sbTop           = 6
	sbBottom        = 7

	sifRange = 0x0001
	sifPage  = 0x0002
	sifPos   = 0x0004
	sifAll   = sifRange | sifPage | sifPos | 0x0010

	swHide          = 0
	swShowMaximized = 3
	swShow          = 5

	swpNoMove     = 0x0002
	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010

	monitorDefaultToNearest = 2

	cwUseDefault = 0x80000000

	colorWindow     = 5
	colorWindowText = 8
	transparent     = 1
	fwNormal        = 400
	fwSemiBold      = 600

	defaultCharset   = 1
	clearTypeQuality = 5

	dtWordBreak = 0x0010
	dtCalcRect  = 0x0400
	dtNoPrefix  = 0x0800
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")

	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procRegisterClassExW              = user32.NewProc("RegisterClassExW")
	procCreateWindowExW               = user32.NewProc("CreateWindowExW")
	procDefWindowProcW                = user32.NewProc("DefWindowProcW")
	procDestroyWindow                 = user32.NewProc("DestroyWindow")
	procSetWindowPos                  = user32.NewProc("SetWindowPos")
	procShowWindow                    = user32.NewProc("ShowWindow")
	procUpdateWindow                  = user32.NewProc("UpdateWindow")
	procMoveWindow                    = user32.NewProc("MoveWindow")
	procGetClientRect                 = user32.NewProc("GetClientRect")
	procGetWindowRect                 = user32.NewProc("GetWindowRect")
	procGetWindowPlacement            = user32.NewProc("GetWindowPlacement")
	procIsZoomed                      = user32.NewProc("IsZoomed")
	procMonitorFromWindow             = user32.NewProc("MonitorFromWindow")
	procGetMonitorInfoW               = user32.NewProc("GetMonitorInfoW")
	procGetScrollInfo                 = user32.NewProc("GetScrollInfo")
	procSetScrollInfo                 = user32.NewProc("SetScrollInfo")
	procMapWindowPoints               = user32.NewProc("MapWindowPoints")
	procLoadCursorW                   = user32.NewProc("LoadCursorW")
	procGetMessageW                   = user32.NewProc("GetMessageW")
	procIsDialogMessageW              = user32.NewProc("IsDialogMessageW")
	procTranslateMessage              = user32.NewProc("TranslateMessage")
	procDispatchMessageW              = user32.NewProc("DispatchMessageW")
	procPostQuitMessage               = user32.NewProc("PostQuitMessage")
	procPostMessageW                  = user32.NewProc("PostMessageW")
	procSendMessageW                  = user32.NewProc("SendMessageW")
	procEnableWindow                  = user32.NewProc("EnableWindow")
	procSetWindowTextW                = user32.NewProc("SetWindowTextW")
	procGetDpiForWindow               = user32.NewProc("GetDpiForWindow")
	procGetSysColor                   = user32.NewProc("GetSysColor")
	procGetSysColorBrush              = user32.NewProc("GetSysColorBrush")
	procGetDC                         = user32.NewProc("GetDC")
	procReleaseDC                     = user32.NewProc("ReleaseDC")
	procDrawTextW                     = user32.NewProc("DrawTextW")
	procGetModuleHandleW              = kernel32.NewProc("GetModuleHandleW")
	procCreateFontIndirectW           = gdi32.NewProc("CreateFontIndirectW")
	procSelectObject                  = gdi32.NewProc("SelectObject")
	procDeleteObject                  = gdi32.NewProc("DeleteObject")
	procSetBkMode                     = gdi32.NewProc("SetBkMode")
	procSetTextColor                  = gdi32.NewProc("SetTextColor")
)

type rect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type monitorInfo struct {
	CbSize  uint32
	Monitor rect
	Work    rect
	Flags   uint32
}

type windowPlacement struct {
	Length         uint32
	Flags          uint32
	ShowCmd        uint32
	MinPosition    point
	MaxPosition    point
	NormalPosition rect
}

type scrollInfo struct {
	CbSize    uint32
	FMask     uint32
	NMin      int32
	NMax      int32
	NPage     uint32
	NPos      int32
	NTrackPos int32
}

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

type logFont struct {
	Height         int32
	Width          int32
	Escapement     int32
	Orientation    int32
	Weight         int32
	Italic         byte
	Underline      byte
	StrikeOut      byte
	CharSet        byte
	OutPrecision   byte
	ClipPrecision  byte
	Quality        byte
	PitchAndFamily byte
	Face           [32]uint16
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

func lowWord(value uintptr) uint16 {
	return uint16(value & 0xffff)
}

func highWord(value uintptr) uint16 {
	return uint16((value >> 16) & 0xffff)
}

func postShellMessage(hwnd uintptr, message uint32) error {
	r, _, err := procPostMessageW.Call(hwnd, uintptr(message), 0, 0)
	if r == 0 {
		return callError("PostMessageW", err)
	}
	return nil
}

func setWindowText(hwnd windows.Handle, value string) {
	procSetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(utf16(value))))
}

func moveWindow(hwnd windows.Handle, x, y, width, height int32) {
	procMoveWindow.Call(uintptr(hwnd), uintptr(x), uintptr(y), uintptr(width), uintptr(height), 1)
}

func setControlFont(hwnd windows.Handle, font windows.Handle) {
	procSendMessageW.Call(uintptr(hwnd), wmSetFont, uintptr(font), 1)
}

func createFont(height int32, weight int32) (windows.Handle, error) {
	description := logFont{
		Height:  height,
		Weight:  weight,
		CharSet: defaultCharset,
		Quality: clearTypeQuality,
	}
	face, err := windows.UTF16FromString("Segoe UI")
	if err != nil {
		return 0, err
	}
	copy(description.Face[:], face)
	font, _, err := procCreateFontIndirectW.Call(uintptr(unsafe.Pointer(&description)))
	if font == 0 {
		return 0, callError("CreateFontIndirectW", err)
	}
	return windows.Handle(font), nil
}
