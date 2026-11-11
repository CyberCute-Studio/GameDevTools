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
	wmCtlColorStatic = 0x0138
	wmSetFont        = 0x0030
	wmDPIChanged     = 0x02e0
	wmUser           = 0x0400
	wmAppDispatch    = wmUser + 1

	wsChild            = 0x40000000
	wsVisible          = 0x10000000
	wsDisabled         = 0x08000000
	wsTabStop          = 0x00010000
	wsGroup            = 0x00020000
	wsClipChildren     = 0x02000000
	wsOverlappedWindow = 0x00cf0000

	bsAutoRadioButton = 0x0000009
	bmSetCheck        = 0x00f1
	bstChecked        = 1

	swHide = 0
	swShow = 5

	cwUseDefault = 0x80000000

	colorWindow = 5
	transparent = 1
	fwNormal    = 400
	fwSemiBold  = 600

	defaultCharset   = 1
	clearTypeQuality = 5
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
	procShowWindow                    = user32.NewProc("ShowWindow")
	procUpdateWindow                  = user32.NewProc("UpdateWindow")
	procMoveWindow                    = user32.NewProc("MoveWindow")
	procGetClientRect                 = user32.NewProc("GetClientRect")
	procLoadCursorW                   = user32.NewProc("LoadCursorW")
	procGetMessageW                   = user32.NewProc("GetMessageW")
	procTranslateMessage              = user32.NewProc("TranslateMessage")
	procDispatchMessageW              = user32.NewProc("DispatchMessageW")
	procPostQuitMessage               = user32.NewProc("PostQuitMessage")
	procPostMessageW                  = user32.NewProc("PostMessageW")
	procSendMessageW                  = user32.NewProc("SendMessageW")
	procSetWindowTextW                = user32.NewProc("SetWindowTextW")
	procGetDpiForWindow               = user32.NewProc("GetDpiForWindow")
	procGetSysColorBrush              = user32.NewProc("GetSysColorBrush")
	procGetModuleHandleW              = kernel32.NewProc("GetModuleHandleW")
	procCreateFontIndirectW           = gdi32.NewProc("CreateFontIndirectW")
	procDeleteObject                  = gdi32.NewProc("DeleteObject")
	procSetBkMode                     = gdi32.NewProc("SetBkMode")
)

type rect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
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
