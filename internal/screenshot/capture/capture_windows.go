//go:build windows

package capture

import (
	"errors"
	"fmt"
	"image"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
)

const (
	srcCopy      = 0x00cc0020
	captureBLT   = 0x40000000
	dibRGBColors = 0

	swHide = 0

	// swShowNoActivate restores a hidden window without activating it, so
	// Restore never moves input focus to the revealed window.
	swShowNoActivate = 4
	// swShowMinNoActivate restores a window that was minimized when Hide
	// ran to the minimized state, without activating it.
	swShowMinNoActivate = 7
)

var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	gdi32  = windows.NewLazySystemDLL("gdi32.dll")
	dwmapi = windows.NewLazySystemDLL("dwmapi.dll")

	procGetDC              = user32.NewProc("GetDC")
	procReleaseDC          = user32.NewProc("ReleaseDC")
	procShowWindow         = user32.NewProc("ShowWindow")
	procIsIconic           = user32.NewProc("IsIconic")
	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
	procBitBlt             = gdi32.NewProc("BitBlt")
	procDwmFlush           = dwmapi.NewProc("DwmFlush")
)

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

// captureMu serializes native captures. Every call builds its own device
// contexts and bitmap, but all calls read the shared screen device context;
// serializing keeps concurrent callers from racing that surface and bounds
// peak memory when several large captures are requested at once.
var captureMu sync.Mutex

// captureRegion copies the given physical virtual-desktop rectangle from
// the composed desktop into a new opaque RGBA image. The cursor sprite is
// not part of the desktop device context and is therefore never captured.
func captureRegion(region display.Rect) (*image.RGBA, error) {
	captureMu.Lock()
	defer captureMu.Unlock()

	width, height := region.Width(), region.Height()
	screen, _, err := procGetDC.Call(0)
	if screen == 0 {
		return nil, kindError(KindNative, "GetDC", callError(err))
	}
	defer procReleaseDC.Call(0, screen)
	memory, _, err := procCreateCompatibleDC.Call(screen)
	if memory == 0 {
		return nil, kindError(KindNative, "CreateCompatibleDC", callError(err))
	}
	defer procDeleteDC.Call(memory)
	info := bitmapInfo{Header: bitmapInfoHeader{
		Size:      uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:     width,
		Height:    -height, // negative height requests top-down rows
		Planes:    1,
		BitCount:  32,
		SizeImage: uint32(width * height * 4),
	}}
	var pixels unsafe.Pointer
	bitmap, _, err := procCreateDIBSection.Call(
		screen, uintptr(unsafe.Pointer(&info)), dibRGBColors,
		uintptr(unsafe.Pointer(&pixels)), 0, 0,
	)
	if bitmap == 0 || pixels == nil {
		return nil, kindError(KindNative, "CreateDIBSection", callError(err))
	}
	defer procDeleteObject.Call(bitmap)
	previous, _, err := procSelectObject.Call(memory, bitmap)
	if previous == 0 {
		return nil, kindError(KindNative, "SelectObject", callError(err))
	}
	defer procSelectObject.Call(memory, previous)
	r, _, err := procBitBlt.Call(
		memory, 0, 0, uintptr(width), uintptr(height),
		screen, uintptr(region.Left), uintptr(region.Top), srcCopy|captureBLT,
	)
	if r == 0 {
		return nil, kindError(KindNative, "BitBlt", callError(err))
	}
	return convertDIB(pixels, int(width), int(height)), nil
}

// convertDIB converts a top-down 32-bit BGRA DIB into an opaque RGBA image.
func convertDIB(pixels unsafe.Pointer, width, height int) *image.RGBA {
	source := unsafe.Slice((*byte)(pixels), width*height*4)
	result := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		sourceRow := y * width * 4
		resultRow := y * result.Stride
		for x := 0; x < width; x++ {
			s := sourceRow + x*4
			d := resultRow + x*4
			result.Pix[d+0] = source[s+2]
			result.Pix[d+1] = source[s+1]
			result.Pix[d+2] = source[s+0]
			result.Pix[d+3] = 255
		}
	}
	return result
}

// Hide hides the given app-owned windows and waits for the desktop
// compositor to settle, so a following capture cannot contain them
// (SCR-044). It returns the Exclusion that restores the windows; callers
// typically defer Restore. Hide records each window's prior visibility,
// and ShowWindow's return value doubles as the failure signal: a window
// that cannot be hidden reports itself as previously hidden, so Restore
// leaves it alone rather than forcing it visible.
//
// ShowWindow acts through each window's owner thread, so that thread must
// keep pumping messages while Hide runs. Calling Hide while the GUI
// thread is blocked — for example synchronously waiting for the capture
// result, which TECH-005 forbids — deadlocks the caller.
func Hide(hwnd ...uintptr) (*Exclusion, error) {
	exclusion := &Exclusion{windows: make([]hiddenWindow, 0, len(hwnd))}
	for _, window := range hwnd {
		minimized, _, _ := procIsIconic.Call(window)
		shown, _, _ := procShowWindow.Call(window, swHide)
		exclusion.windows = append(exclusion.windows, hiddenWindow{
			hwnd:         window,
			wasVisible:   shown != 0,
			wasMinimized: minimized != 0,
		})
	}
	if hr, _, _ := procDwmFlush.Call(); int32(hr) < 0 {
		exclusion.Restore()
		return nil, kindError(KindNative, "DwmFlush",
			fmt.Errorf("returned HRESULT %#x", uint32(hr)))
	}
	return exclusion, nil
}

// Restore returns each window to the state Hide found it in: windows that
// were visible are shown again, minimized windows are shown minimized
// again, and windows that were already hidden stay hidden, so Restore
// never resurrects a window another flow hid or pops open one the user
// minimized. Nothing is activated, so restoring after capture never moves
// input focus. Restore is idempotent and safe for concurrent use, so a
// deferred Restore is harmless after an earlier explicit call. Like Hide,
// Restore acts through each window's owner thread, which must keep
// pumping messages.
func (e *Exclusion) Restore() {
	if !e.restored.CompareAndSwap(false, true) {
		return
	}
	for _, window := range e.windows {
		switch {
		case !window.wasVisible:
			// Already hidden before Hide; leave it hidden.
		case window.wasMinimized:
			procShowWindow.Call(window.hwnd, swShowMinNoActivate)
		default:
			procShowWindow.Call(window.hwnd, swShowNoActivate)
		}
	}
}

func callError(err error) error {
	if err == windows.ERROR_SUCCESS {
		return errors.New("the operation failed without an extended error")
	}
	return err
}
