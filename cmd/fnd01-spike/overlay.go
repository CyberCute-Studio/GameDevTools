package main

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	overlayBorder = 8
	overlayKey    = 0x00ff00ff
	overlayColor  = 0x00b87518
	wsPopup       = 0x80000000
)

type nativeWindow struct {
	hwnd       windows.Handle
	overlay    bool
	main       bool
	clicked    bool
	testColor  uint32
	onCapture  func() error
	dragging   bool
	dragCursor point
	dragBounds rect
}

var (
	windowStates       = map[windows.Handle]*nativeWindow{}
	procSetWindowRgn   = user32.NewProc("SetWindowRgn")
	procCreateRectRgn  = gdi32.NewProc("CreateRectRgn")
	procCombineRgn     = gdi32.NewProc("CombineRgn")
	procSetCapture     = user32.NewProc("SetCapture")
	procReleaseCapture = user32.NewProc("ReleaseCapture")
)

func applyBorderRegion(hwnd uintptr, width, height int32) error {
	outer, _, err := procCreateRectRgn.Call(0, 0, uintptr(width), uintptr(height))
	if outer == 0 {
		return callError("CreateRectRgn(outer)", err)
	}
	inner, _, err := procCreateRectRgn.Call(overlayBorder, overlayBorder, uintptr(width-overlayBorder), uintptr(height-overlayBorder))
	if inner == 0 {
		procDeleteObject.Call(outer)
		return callError("CreateRectRgn(inner)", err)
	}
	r, _, err := procCombineRgn.Call(outer, outer, inner, 4)
	procDeleteObject.Call(inner)
	if r == 0 {
		procDeleteObject.Call(outer)
		return callError("CombineRgn", err)
	}
	r, _, err = procSetWindowRgn.Call(hwnd, outer, 1)
	if r == 0 {
		procDeleteObject.Call(outer)
		return callError("SetWindowRgn", err)
	}
	return nil
}

func registerWindowClass() error {
	instance, _, err := procGetModuleHandleW.Call(0)
	if instance == 0 {
		return callError("GetModuleHandleW", err)
	}
	cursor, _, err := procLoadCursorW.Call(0, uintptr(32512))
	if cursor == 0 {
		return callError("LoadCursorW", err)
	}
	class := wndClassEx{
		LpfnWndProc:   windows.NewCallback(windowProc),
		HInstance:     windows.Handle(instance),
		HCursor:       windows.Handle(cursor),
		LpszClassName: utf16("GameDevToolsFND01Spike"),
	}
	class.CbSize = uint32(unsafe.Sizeof(class))
	atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class)))
	if atom == 0 {
		return callError("RegisterClassExW", err)
	}
	return nil
}

func createNativeWindow(bounds rect, overlay, main bool) (*nativeWindow, error) {
	exStyle := uintptr(wsExToolWindow)
	style := uintptr(wsPopup)
	if overlay {
		exStyle |= wsExLayered | wsExTopmost
	} else {
		exStyle |= wsExTopmost
		style |= wsVisible
	}
	hwnd, _, err := procCreateWindowExW.Call(
		exStyle,
		uintptr(unsafe.Pointer(utf16("GameDevToolsFND01Spike"))),
		uintptr(unsafe.Pointer(utf16("GameDevTools FND-01 native stack spike"))),
		style,
		uintptr(bounds.Left), uintptr(bounds.Top), uintptr(bounds.width()), uintptr(bounds.height()),
		0, 0, 0, 0,
	)
	if hwnd == 0 {
		return nil, callError("CreateWindowExW", err)
	}
	state := &nativeWindow{hwnd: windows.Handle(hwnd), overlay: overlay, main: main}
	windowStates[state.hwnd] = state
	if overlay {
		r, _, err := procSetLayeredWindowAttributes.Call(hwnd, overlayKey, 0, lwaColorKey)
		if r == 0 {
			procDestroyWindow.Call(hwnd)
			return nil, callError("SetLayeredWindowAttributes", err)
		}
		if err := applyBorderRegion(hwnd, bounds.width(), bounds.height()); err != nil {
			procDestroyWindow.Call(hwnd)
			return nil, err
		}
	}
	return state, nil
}

func (w *nativeWindow) move(x, y int32) error {
	bounds, err := getWindowRect(w.hwnd)
	if err != nil {
		return err
	}
	return setWindowPos(w.hwnd, x, y, bounds.width(), bounds.height(), swpNoZOrder)
}

func windowProc(hwnd windows.Handle, message uint32, wParam, lParam uintptr) uintptr {
	state := windowStates[hwnd]
	switch message {
	case wmNCHitTest:
		if state != nil && state.overlay {
			cursor := point{X: lowWord(lParam), Y: highWord(lParam)}
			r, _, _ := procScreenToClient.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&cursor)))
			if r != 0 {
				var client rect
				procGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&client)))
				if cursor.X >= 0 && cursor.Y >= 0 && cursor.X < client.Right && cursor.Y < client.Bottom {
					if cursor.X < overlayBorder || cursor.Y < overlayBorder ||
						cursor.X >= client.Right-overlayBorder || cursor.Y >= client.Bottom-overlayBorder {
						return htClient
					}
					return htTransparent
				}
			}
		}
	case wmLButtonDown:
		if state != nil {
			if state.overlay {
				state.dragging = true
				procGetCursorPos.Call(uintptr(unsafe.Pointer(&state.dragCursor)))
				state.dragBounds, _ = getWindowRect(hwnd)
				procSetCapture.Call(uintptr(hwnd))
			} else {
				state.clicked = true
			}
			return 0
		}
	case wmMouseMove:
		if state != nil && state.dragging {
			var cursor point
			procGetCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))
			x := state.dragBounds.Left + cursor.X - state.dragCursor.X
			y := state.dragBounds.Top + cursor.Y - state.dragCursor.Y
			_ = state.move(x, y)
			return 0
		}
	case wmLButtonUp:
		if state != nil && state.dragging {
			state.dragging = false
			procReleaseCapture.Call()
			return 0
		}
	case wmCaptureChanged:
		if state != nil {
			state.dragging = false
		}
	case wmPaint:
		var paint paintStruct
		hdc, _, _ := procBeginPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&paint)))
		if state != nil && hdc != 0 {
			var client rect
			procGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&client)))
			fillColor := state.testColor
			if state.overlay {
				fillColor = overlayKey
			}
			fill, _, _ := procCreateSolidBrush.Call(uintptr(fillColor))
			if fill != 0 {
				procFillRect.Call(hdc, uintptr(unsafe.Pointer(&client)), fill)
				procDeleteObject.Call(fill)
			}
			if state.overlay {
				border, _, _ := procCreateSolidBrush.Call(overlayColor)
				if border != 0 {
					left := rect{Left: client.Left, Top: client.Top, Right: client.Left + overlayBorder, Bottom: client.Bottom}
					top := rect{Left: client.Left, Top: client.Top, Right: client.Right, Bottom: client.Top + overlayBorder}
					right := rect{Left: client.Right - overlayBorder, Top: client.Top, Right: client.Right, Bottom: client.Bottom}
					bottom := rect{Left: client.Left, Top: client.Bottom - overlayBorder, Right: client.Right, Bottom: client.Bottom}
					for _, part := range []rect{left, top, right, bottom} {
						procFillRect.Call(hdc, uintptr(unsafe.Pointer(&part)), border)
					}
					procDeleteObject.Call(border)
				}
			}
		}
		procEndPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&paint)))
		return 0
	case wmHotkey:
		if state != nil {
			showWindow(state.hwnd, swRestore)
		}
		return 0
	case wmKeyDown:
		if state == nil {
			break
		}
		switch wParam {
		case vkEscape:
			procDestroyWindow.Call(uintptr(hwnd))
			return 0
		case vkF9:
			if state.onCapture != nil {
				if err := state.onCapture(); err != nil {
					fmt.Fprintf(os.Stderr, "capture failed: %v\n", err)
					procMessageBoxW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(utf16(err.Error()))), uintptr(unsafe.Pointer(utf16("FND-01 capture failed"))), 0x10)
				}
			}
			return 0
		case vkLeft, vkUp, vkRight, vkDown:
			bounds, err := getWindowRect(hwnd)
			if err == nil {
				distance := int32(1)
				shift, _, _ := procGetAsyncKeyState.Call(0x10)
				if uint16(shift)&0x8000 != 0 {
					distance = 10
				}
				switch wParam {
				case vkLeft:
					bounds.Left -= distance
				case vkRight:
					bounds.Left += distance
				case vkUp:
					bounds.Top -= distance
				case vkDown:
					bounds.Top += distance
				}
				_ = state.move(bounds.Left, bounds.Top)
			}
			return 0
		}
	case wmClose:
		procDestroyWindow.Call(uintptr(hwnd))
		return 0
	case wmDestroy:
		if state != nil {
			delete(windowStates, hwnd)
			if state.main {
				procPostQuitMessage.Call(0)
			}
		}
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
	return r
}
