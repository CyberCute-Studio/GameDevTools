package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func main() {
	selfTest := flag.Bool("self-test", false, "exercise real Windows integration and exit")
	output := flag.String("output", "fnd01-capture.png", "PNG path for F9 capture")
	width := flag.Int("width", 640, "overlay width in physical pixels")
	height := flag.Int("height", 360, "overlay height in physical pixels")
	x := flag.Int("x", 0, "overlay X in virtual-desktop physical pixels")
	y := flag.Int("y", 0, "overlay Y in virtual-desktop physical pixels")
	flag.Parse()
	if err := enablePerMonitorDPI(); err != nil {
		fatal(err)
	}
	if *selfTest {
		if err := runSelfTest(); err != nil {
			fatal(err)
		}
		return
	}
	if err := runInteractive(*output, *x, *y, *width, *height); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "FND-01 spike: %v\n", err)
	os.Exit(1)
}

func runInteractive(output string, x, y, width, height int) error {
	if width <= 0 || height <= 0 || width > 16384 || height > 16384 {
		return fmt.Errorf("width and height must be between 1 and 16384 physical pixels")
	}
	displays, err := enumerateDisplays()
	if err != nil {
		return err
	}
	printDisplays(displays)
	primary := primaryDisplay(displays)
	if x == 0 && y == 0 {
		x = int(primary.Bounds.Left + (primary.Bounds.width()-int32(width))/2)
		y = int(primary.Bounds.Top + (primary.Bounds.height()-int32(height))/2)
	}
	if err := registerWindowClass(); err != nil {
		return err
	}
	window, err := createNativeWindow(rect{Left: int32(x), Top: int32(y), Right: int32(x + width), Bottom: int32(y + height)}, true, true)
	if err != nil {
		return err
	}
	absoluteOutput, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	window.onCapture = func() error {
		img, err := captureOverlay(window)
		if err != nil {
			return err
		}
		clipboardErr := setClipboardImage(window.hwnd, img)
		saveErr := savePNG(absoluteOutput, img)
		if clipboardErr == nil {
			fmt.Println("clipboard: CF_DIB image set")
		}
		if saveErr == nil {
			fmt.Printf("saved: %s (%d × %d physical pixels)\n", absoluteOutput, img.Bounds().Dx(), img.Bounds().Dy())
		}
		return errors.Join(clipboardErr, saveErr)
	}
	r, _, hotkeyErr := procRegisterHotKey.Call(uintptr(window.hwnd), 1, modControl|modAlt|modNoRepeat, 'S')
	if r == 0 {
		fmt.Fprintf(os.Stderr, "Ctrl+Alt+S unavailable: %v; use the overlay directly\n", callError("RegisterHotKey", hotkeyErr))
	} else {
		defer procUnregisterHotKey.Call(uintptr(window.hwnd), 1)
	}
	showWindow(window.hwnd, swShow)
	procUpdateWindow.Call(uintptr(window.hwnd))
	fmt.Printf("overlay: %d × %d physical pixels at (%d, %d)\n", width, height, x, y)
	fmt.Println("Ctrl+Alt+S: show overlay; drag its border; arrows/Shift+arrows: nudge; F9: clipboard + PNG; Esc: exit")
	var message msg
	for {
		r, _, err := procGetMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(r) == -1 {
			return callError("GetMessageW", err)
		}
		if r == 0 {
			return nil
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
	}
}

func printDisplays(displays []display) {
	for _, item := range displays {
		fmt.Printf("display %s: (%d,%d)-(%d,%d), %d × %d physical pixels, DPI %d × %d (%.0f%%), primary=%t\n",
			item.Name, item.Bounds.Left, item.Bounds.Top, item.Bounds.Right, item.Bounds.Bottom,
			item.Bounds.width(), item.Bounds.height(), item.DPIX, item.DPIY, float64(item.DPIX)*100/96, item.Primary)
	}
}

func runSelfTest() error {
	displays, err := enumerateDisplays()
	if err != nil {
		return err
	}
	printDisplays(displays)
	if lowWord(0x0000ffff) != -1 || highWord(0xffff0000) != -1 {
		return fmt.Errorf("coordinate decoding: signed 16-bit values were not preserved")
	}
	fmt.Println("PASS coordinate decoding: signed 16-bit hit-test values remain negative")
	if err := registerWindowClass(); err != nil {
		return err
	}
	var cursor point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))
	defer procSetCursorPos.Call(uintptr(cursor.X), uintptr(cursor.Y))
	mixedDPI := false
	for _, item := range displays {
		if item.DPIX != displays[0].DPIX || item.DPIY != displays[0].DPIY {
			mixedDPI = true
		}
		const width, height = int32(321), int32(241)
		x := item.Bounds.Left + (item.Bounds.width()-width)/2
		y := item.Bounds.Top + (item.Bounds.height()-height)/2
		window, err := createNativeWindow(rect{Left: x, Top: y, Right: x + width, Bottom: y + height}, true, false)
		if err != nil {
			return err
		}
		showWindow(window.hwnd, swShow)
		procUpdateWindow.Call(uintptr(window.hwnd))
		pumpMessages()
		bounds, err := getWindowRect(window.hwnd)
		if err != nil {
			return err
		}
		if bounds != (rect{Left: x, Top: y, Right: x + width, Bottom: y + height}) {
			return fmt.Errorf("%s physical bounds: got %+v, want origin (%d,%d), size %d × %d", item.Name, bounds, x, y, width, height)
		}
		if err := window.move(x+17, y-13); err != nil {
			return err
		}
		bounds, err = getWindowRect(window.hwnd)
		if err != nil {
			return err
		}
		if bounds.Left != x+17 || bounds.Top != y-13 || bounds.width() != width || bounds.height() != height {
			return fmt.Errorf("%s physical move changed bounds unexpectedly: %+v", item.Name, bounds)
		}
		fmt.Printf("PASS physical overlay: %s, %d × %d at DPI %d, move preserved dimensions\n", item.Name, bounds.width(), bounds.height(), item.DPIX)
		procDestroyWindow.Call(uintptr(window.hwnd))
		pumpMessages()
	}
	fmt.Printf("mixed-DPI configurations observed: %t\n", mixedDPI)

	primary := primaryDisplay(displays)
	if err := func() error {
		tiny, err := createNativeWindow(rect{Left: primary.Bounds.Left, Top: primary.Bounds.Top, Right: primary.Bounds.Left + 1, Bottom: primary.Bounds.Top + 1}, true, false)
		if err != nil {
			return err
		}
		defer procDestroyWindow.Call(uintptr(tiny.hwnd))
		region, _, err := procCreateRectRgn.Call(0, 0, 0, 0)
		if region == 0 {
			return callError("CreateRectRgn(verification)", err)
		}
		defer procDeleteObject.Call(region)
		r, _, err := procGetWindowRgn.Call(uintptr(tiny.hwnd), region)
		if r == 0 {
			return callError("GetWindowRgn", err)
		}
		var regionBounds rect
		kind, _, err := procGetRgnBox.Call(region, uintptr(unsafe.Pointer(&regionBounds)))
		if kind == 0 {
			return callError("GetRgnBox", err)
		}
		if kind == 1 || regionBounds != (rect{Left: 0, Top: 0, Right: 1, Bottom: 1}) {
			return fmt.Errorf("1 × 1 overlay region: kind %d, bounds %+v", kind, regionBounds)
		}
		return nil
	}(); err != nil {
		return err
	}
	fmt.Println("PASS minimum overlay: 1 × 1 window retains a non-empty interactive region")

	const width, height = int32(320), int32(240)
	x := primary.Bounds.Left + (primary.Bounds.width()-width)/2
	y := primary.Bounds.Top + (primary.Bounds.height()-height)/2
	bounds := rect{Left: x, Top: y, Right: x + width, Bottom: y + height}
	underlying, err := createNativeWindow(bounds, false, false)
	if err != nil {
		return err
	}
	underlying.testColor = colorRef(19, 87, 153)
	defer procDestroyWindow.Call(uintptr(underlying.hwnd))
	overlay, err := createNativeWindow(bounds, true, false)
	if err != nil {
		return err
	}
	defer procDestroyWindow.Call(uintptr(overlay.hwnd))
	showWindow(underlying.hwnd, swShow)
	showWindow(overlay.hwnd, swShow)
	procUpdateWindow.Call(uintptr(underlying.hwnd))
	procUpdateWindow.Call(uintptr(overlay.hwnd))
	pumpMessages()
	time.Sleep(100 * time.Millisecond)
	if !click(x+width/2, y+height/2) {
		return fmt.Errorf("SetCursorPos failed before click-through test")
	}
	pumpMessages()
	if !underlying.clicked {
		return fmt.Errorf("click-through: underlying Win32 window did not receive the real mouse click")
	}
	clickedBounds, err := getWindowRect(overlay.hwnd)
	if err != nil {
		return err
	}
	if clickedBounds != bounds {
		return fmt.Errorf("click-through: overlay moved or resized from %+v to %+v", bounds, clickedBounds)
	}
	fmt.Println("PASS click-through: underlying Win32 window received a real interior click and overlay geometry was unchanged")

	if err := setWindowPos(overlay.hwnd, x, y, width, height, swpShowWindow); err != nil {
		return err
	}
	if !moveCursor(x+overlayBorder/2, y+height/2) {
		return fmt.Errorf("SetCursorPos failed before border drag")
	}
	procMouseEvent.Call(mouseEventFLeftDown, 0, 0, 0, 0)
	injectedButtonDown := true
	defer func() {
		if injectedButtonDown {
			procMouseEvent.Call(mouseEventFLeftUp, 0, 0, 0, 0)
		}
	}()
	time.Sleep(50 * time.Millisecond)
	pumpMessages()
	if !overlay.dragging {
		return fmt.Errorf("border drag: Win32 did not deliver the border mouse-down to the overlay")
	}
	procSetCursorPos.Call(uintptr(x+40+overlayBorder/2), uintptr(y+30+height/2))
	procMouseEvent.Call(mouseEventFMove, 0, 0, 0, 0)
	time.Sleep(100 * time.Millisecond)
	pumpMessages()
	procMouseEvent.Call(mouseEventFLeftUp, 0, 0, 0, 0)
	injectedButtonDown = false
	pumpMessages()
	moved, err := getWindowRect(overlay.hwnd)
	if err != nil {
		return err
	}
	if moved.Left != x+40 || moved.Top != y+30 || moved.width() != width || moved.height() != height {
		return fmt.Errorf("border drag: got %+v, want origin (%d,%d), size %d × %d", moved, x+40, y+30, width, height)
	}
	fmt.Println("PASS interactive border: real drag moved 40 × 30 physical pixels without resizing")

	r, _, err := procRegisterHotKey.Call(uintptr(overlay.hwnd), 90, modControl|modAlt|modShift|modNoRepeat, 0x7b)
	if r == 0 {
		return callError("RegisterHotKey(holder)", err)
	}
	defer procUnregisterHotKey.Call(uintptr(overlay.hwnd), 90)
	r, _, conflictErr := procRegisterHotKey.Call(uintptr(overlay.hwnd), 91, modControl|modAlt|modShift|modNoRepeat, 0x7b)
	if r != 0 {
		procUnregisterHotKey.Call(uintptr(overlay.hwnd), 91)
		return fmt.Errorf("hotkey conflict: second registration unexpectedly succeeded")
	}
	if !errors.Is(conflictErr, windows.Errno(errorHotkeyAlreadyRegistered)) {
		return fmt.Errorf("hotkey conflict: got %v, want ERROR_HOTKEY_ALREADY_REGISTERED", conflictErr)
	}
	fmt.Println("PASS hotkey conflict: Windows returned ERROR_HOTKEY_ALREADY_REGISTERED")

	if err := overlay.move(x, y); err != nil {
		return err
	}
	if err := setWindowPos(underlying.hwnd, x, y, width, height, swpNoZOrder|swpShowWindow); err != nil {
		return err
	}
	procUpdateWindow.Call(uintptr(underlying.hwnd))
	pumpMessages()
	img, err := captureOverlay(overlay)
	if err != nil {
		return err
	}
	if img.Bounds().Dx() != int(width) || img.Bounds().Dy() != int(height) {
		return fmt.Errorf("capture dimensions: got %v", img.Bounds())
	}
	for py := 0; py < int(height); py++ {
		for px := 0; px < int(width); px++ {
			pixel := img.RGBAAt(px, py)
			if pixel.R != 19 || pixel.G != 87 || pixel.B != 153 || pixel.A != 255 {
				return fmt.Errorf("capture content: pixel (%d,%d) is %#v, want RGBA(19,87,153,255)", px, py, pixel)
			}
		}
	}
	fmt.Printf("PASS raw capture: %d × %d physical pixels and every pixel has known RGB content\n", width, height)
	if err := setClipboardImage(overlay.hwnd, img); err != nil {
		return err
	}
	clipboardWidth, clipboardHeight, err := clipboardImageSize()
	if err != nil {
		return err
	}
	if clipboardWidth != width || clipboardHeight != height {
		return fmt.Errorf("clipboard dimensions: got %d × %d", clipboardWidth, clipboardHeight)
	}
	fmt.Println("PASS clipboard: CF_DIB round-trip has exact dimensions")
	directory, err := os.MkdirTemp("", "gdt-fnd01-verification-")
	if err != nil {
		return err
	}
	path := filepath.Join(directory, "capture.png")
	if err := savePNG(path, img); err != nil {
		return err
	}
	if err := verifyPNG(path, int(width), int(height)); err != nil {
		return err
	}
	fmt.Printf("PASS PNG output: %s (decoded at %d × %d)\n", path, width, height)
	fmt.Println("SELF-TEST PASSED")
	return nil
}

func moveCursor(x, y int32) bool {
	r, _, _ := procSetCursorPos.Call(uintptr(x), uintptr(y))
	if r == 0 {
		return false
	}
	time.Sleep(75 * time.Millisecond)
	pumpMessages()
	return true
}

func click(x, y int32) bool {
	if !moveCursor(x, y) {
		return false
	}
	procMouseEvent.Call(mouseEventFLeftDown, 0, 0, 0, 0)
	time.Sleep(40 * time.Millisecond)
	procMouseEvent.Call(mouseEventFLeftUp, 0, 0, 0, 0)
	time.Sleep(100 * time.Millisecond)
	return true
}
