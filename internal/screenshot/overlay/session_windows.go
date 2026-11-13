//go:build windows

package overlay

import (
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

const (
	overlayClassName = "GameDevToolsRectangleOverlay"
	toolbarClassName = "GameDevToolsRectangleToolbar"

	// overlayTintAlpha dims the desktop to about 62% brightness. The tint
	// keeps the input-capturing surface visible at all times (SCR-052) and
	// never reaches capture output because the session windows are destroyed
	// before the rectangle is read (SCR-068).
	overlayTintAlpha = 96

	toolbarCaptureCommand = 1
	toolbarCancelCommand  = 2

	// Logical toolbar measurements, scaled by the cursor display's DPI.
	toolbarLogicalWidth  = 480
	toolbarLogicalHeight = 100
	toolbarLogicalMargin = 16
	toolbarLogicalInset  = 12

	wmDestroy        = 0x0002
	wmNCHitTest      = 0x0084
	wmKeyDown        = 0x0100
	wmCommand        = 0x0111
	wmCtlColorStatic = 0x0138

	vkEscape = 0x1b

	htCaption = 2

	wsPopup          = 0x80000000
	wsExLayered      = 0x00080000
	wsExTopmost      = 0x00000008
	wsExToolWindow   = 0x00000080
	wsExNoActivate   = 0x08000000
	lwaAlpha         = 0x00000002
	swShow           = 5
	swShowNoActivate = 4

	blackBrush      = 4
	colorWindow     = 5
	colorWindowText = 8
	transparent     = 1
	idcArrow        = 32512
	idcCross        = 32515
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")

	procRegisterClassExW           = user32.NewProc("RegisterClassExW")
	procCreateWindowExW            = user32.NewProc("CreateWindowExW")
	procDestroyWindow              = user32.NewProc("DestroyWindow")
	procDefWindowProcW             = user32.NewProc("DefWindowProcW")
	procShowWindow                 = user32.NewProc("ShowWindow")
	procSetForegroundWindow        = user32.NewProc("SetForegroundWindow")
	procSetLayeredWindowAttributes = user32.NewProc("SetLayeredWindowAttributes")
	procEnableWindow               = user32.NewProc("EnableWindow")
	procIsChild                    = user32.NewProc("IsChild")
	procIsDialogMessageW           = user32.NewProc("IsDialogMessageW")
	procLoadCursorW                = user32.NewProc("LoadCursorW")
	procGetSysColor                = user32.NewProc("GetSysColor")
	procGetSysColorBrush           = user32.NewProc("GetSysColorBrush")
	procGetModuleHandleW           = kernel32.NewProc("GetModuleHandleW")
	procGetStockObject             = gdi32.NewProc("GetStockObject")
	procSetBkMode                  = gdi32.NewProc("SetBkMode")
	procSetTextColor               = gdi32.NewProc("SetTextColor")

	registerClassesOnce sync.Once
	registerClassesErr  error

	overlayStates = map[windows.Handle]*windowsSession{}
	toolbarStates = map[windows.Handle]*windowsSession{}
)

// windowMessage mirrors the Win32 MSG layout the shell's message loop holds,
// so FilterMessage can inspect and forward the queued message in place.
type windowMessage struct {
	HWND     windows.Handle
	Message  uint32
	_        uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       point
	LPrivate uint32
}

type point struct {
	X int32
	Y int32
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

// windowsSession owns the two topmost windows of one active session: the
// dimmed virtual-desktop overlay and the floating toolbar.
type windowsSession struct {
	overlay       windows.Handle
	toolbar       windows.Handle
	captureButton windows.Handle
	onCapture     func()
	onCancel      func()
}

func registerOverlayClasses() error {
	registerClassesOnce.Do(func() {
		instance, _, err := procGetModuleHandleW.Call(0)
		if instance == 0 {
			registerClassesErr = callError("GetModuleHandleW", err)
			return
		}
		cross, _, err := procLoadCursorW.Call(0, idcCross)
		if cross == 0 {
			registerClassesErr = callError("LoadCursorW(IDC_CROSS)", err)
			return
		}
		arrow, _, err := procLoadCursorW.Call(0, idcArrow)
		if arrow == 0 {
			registerClassesErr = callError("LoadCursorW(IDC_ARROW)", err)
			return
		}
		black, _, err := procGetStockObject.Call(blackBrush)
		if black == 0 {
			registerClassesErr = callError("GetStockObject(BLACK_BRUSH)", err)
			return
		}
		windowBrush, _, err := procGetSysColorBrush.Call(colorWindow)
		if windowBrush == 0 {
			registerClassesErr = callError("GetSysColorBrush(COLOR_WINDOW)", err)
			return
		}
		overlay := wndClassEx{
			LpfnWndProc:   windows.NewCallback(overlayWindowProc),
			HInstance:     windows.Handle(instance),
			HCursor:       windows.Handle(cross),
			HbrBackground: windows.Handle(black),
			LpszClassName: utf16(overlayClassName),
		}
		overlay.CbSize = uint32(unsafe.Sizeof(overlay))
		if atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&overlay))); atom == 0 {
			registerClassesErr = callError("RegisterClassExW(overlay)", err)
			return
		}
		toolbar := wndClassEx{
			LpfnWndProc:   windows.NewCallback(toolbarWindowProc),
			HInstance:     windows.Handle(instance),
			HCursor:       windows.Handle(arrow),
			HbrBackground: windows.Handle(windowBrush),
			LpszClassName: utf16(toolbarClassName),
		}
		toolbar.CbSize = uint32(unsafe.Sizeof(toolbar))
		if atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&toolbar))); atom == 0 {
			registerClassesErr = callError("RegisterClassExW(toolbar)", err)
		}
	})
	return registerClassesErr
}

// createNative shows the session's windows: a translucent black overlay
// covering the whole virtual desktop and a floating toolbar anchored to the
// cursor display. The overlay never activates, so it cannot rise above the
// toolbar or steal keyboard focus; the toolbar takes the foreground so
// Escape, Tab, and Enter work while the main window is hidden.
func createNative(request CreateRequest) (NativeSession, error) {
	if err := registerOverlayClasses(); err != nil {
		return nil, err
	}
	session := &windowsSession{onCapture: request.OnCapture, onCancel: request.OnCancel}
	overlay, _, err := procCreateWindowExW.Call(
		wsExLayered|wsExTopmost|wsExToolWindow|wsExNoActivate,
		uintptr(unsafe.Pointer(utf16(overlayClassName))),
		uintptr(unsafe.Pointer(utf16(ui.OverlayWindowTitle))),
		wsPopup,
		uintptr(uint32(request.Bounds.Left)), uintptr(uint32(request.Bounds.Top)),
		uintptr(uint32(request.Bounds.Width())), uintptr(uint32(request.Bounds.Height())),
		0, 0, 0, 0,
	)
	if overlay == 0 {
		return nil, callError("CreateWindowExW(overlay)", err)
	}
	session.overlay = windows.Handle(overlay)
	overlayStates[session.overlay] = session
	if r, _, err := procSetLayeredWindowAttributes.Call(overlay, 0, overlayTintAlpha, lwaAlpha); r == 0 {
		session.Close()
		return nil, callError("SetLayeredWindowAttributes", err)
	}
	if err := session.createToolbar(request); err != nil {
		session.Close()
		return nil, err
	}
	procShowWindow.Call(overlay, swShowNoActivate)
	procShowWindow.Call(uintptr(session.toolbar), swShow)
	// Best effort: if another window owns the foreground the toolbar stays
	// usable by mouse, and the shell's message filter still routes Escape.
	procSetForegroundWindow.Call(uintptr(session.toolbar))
	return session, nil
}

func (s *windowsSession) createToolbar(request CreateRequest) error {
	dpi := uint32(display.ReferenceDPI)
	anchor := request.Bounds
	if item, err := request.Monitor.CursorDisplay(); err == nil {
		dpi = item.DPIX
		anchor = item.WorkArea
	}
	scale := func(value int32) int32 {
		return display.LogicalToPhysical(value, dpi)
	}
	width, height := scale(toolbarLogicalWidth), scale(toolbarLogicalHeight)
	x, y := toolbarPlacement(request.Bounds, anchor, width, height, scale(toolbarLogicalMargin))
	toolbar, _, err := procCreateWindowExW.Call(
		wsExTopmost|wsExToolWindow,
		uintptr(unsafe.Pointer(utf16(toolbarClassName))),
		uintptr(unsafe.Pointer(utf16(ui.OverlayToolbarTitle))),
		wsPopup,
		uintptr(uint32(x)), uintptr(uint32(y)), uintptr(uint32(width)), uintptr(uint32(height)),
		0, 0, 0, 0,
	)
	if toolbar == 0 {
		return callError("CreateWindowExW(toolbar)", err)
	}
	s.toolbar = windows.Handle(toolbar)
	toolbarStates[s.toolbar] = s
	label, err := ui.NewLabel(s.toolbar, ui.OverlayInstructions)
	if err != nil {
		return err
	}
	captureButton, err := ui.NewButton(s.toolbar, ui.CaptureAction, ui.ButtonOptions{ID: toolbarCaptureCommand, Primary: true, Disabled: true})
	if err != nil {
		return err
	}
	cancelButton, err := ui.NewButton(s.toolbar, ui.CancelAction, ui.ButtonOptions{ID: toolbarCancelCommand})
	if err != nil {
		return err
	}
	font := windows.Handle(request.Font)
	if font != 0 {
		label.SetFont(font)
		captureButton.SetFont(font)
		cancelButton.SetFont(font)
	}
	inset := scale(toolbarLogicalInset)
	label.Move(inset, scale(10), width-2*inset, scale(40))
	buttonWidth, buttonHeight := scale(96), scale(28)
	buttonY := height - inset - buttonHeight
	cancelButton.Move(width-inset-buttonWidth, buttonY, buttonWidth, buttonHeight)
	captureButton.Move(width-inset-2*buttonWidth-scale(8), buttonY, buttonWidth, buttonHeight)
	s.captureButton = captureButton.Handle()
	return nil
}

// toolbarPlacement centers the toolbar at the top of the anchor work area,
// clamped into the virtual desktop so it stays fully reachable on every
// display arrangement, including negative-coordinate displays. Edge-aware
// placement near the selection arrives with the Free Rectangle item.
func toolbarPlacement(desktop, anchor display.Rect, width, height, margin int32) (x, y int32) {
	x = anchor.Left + (anchor.Width()-width)/2
	y = anchor.Top + margin
	if x < desktop.Left {
		x = desktop.Left
	}
	if y < desktop.Top {
		y = desktop.Top
	}
	if x+width > desktop.Right {
		x = desktop.Right - width
	}
	if y+height > desktop.Bottom {
		y = desktop.Bottom - height
	}
	return x, y
}

func (s *windowsSession) Close() {
	if s.toolbar != 0 {
		procDestroyWindow.Call(uintptr(s.toolbar))
		s.toolbar = 0
	}
	if s.overlay != 0 {
		procDestroyWindow.Call(uintptr(s.overlay))
		s.overlay = 0
	}
}

func (s *windowsSession) SetSelectionReady(ready bool) {
	if s.captureButton == 0 {
		return
	}
	enabled := uintptr(0)
	if ready {
		enabled = 1
	}
	procEnableWindow.Call(uintptr(s.captureButton), enabled)
}

// owns reports whether hwnd is one of the session's windows or a toolbar
// control.
func (s *windowsSession) owns(hwnd uintptr) bool {
	return hwnd == uintptr(s.overlay) || s.isToolbarMessage(hwnd)
}

func (s *windowsSession) isToolbarMessage(hwnd uintptr) bool {
	if hwnd == uintptr(s.toolbar) {
		return true
	}
	r, _, _ := procIsChild.Call(uintptr(s.toolbar), hwnd)
	return r != 0
}

// FilterMessage pre-handles the GUI thread's queued messages while a session
// is active. Escape cancels from any session window, and toolbar messages go
// through IsDialogMessage so Tab, arrow keys, Enter, and mnemonics work
// between the Capture and Cancel buttons even though the toolbar is a popup
// rather than a dialog. Messages for other windows pass through untouched.
func (c *Controller) FilterMessage(message unsafe.Pointer) bool {
	msg := (*windowMessage)(message)
	c.mu.Lock()
	s := c.session
	c.mu.Unlock()
	if s == nil {
		return false
	}
	native, ok := s.native.(*windowsSession)
	if !ok {
		return false
	}
	hwnd := uintptr(msg.HWND)
	if msg.Message == wmKeyDown && msg.WParam == vkEscape && native.owns(hwnd) {
		c.Cancel()
		return true
	}
	if native.isToolbarMessage(hwnd) {
		handled, _, _ := procIsDialogMessageW.Call(uintptr(native.toolbar), uintptr(message))
		return handled != 0
	}
	return false
}

func overlayWindowProc(hwnd windows.Handle, message uint32, wParam uintptr, lParam unsafe.Pointer) uintptr {
	session := overlayStates[hwnd]
	switch message {
	case wmKeyDown:
		if session != nil && wParam == vkEscape && session.onCancel != nil {
			session.onCancel()
			return 0
		}
	case wmDestroy:
		delete(overlayStates, hwnd)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, uintptr(lParam))
	return r
}

func toolbarWindowProc(hwnd windows.Handle, message uint32, wParam uintptr, lParam unsafe.Pointer) uintptr {
	session := toolbarStates[hwnd]
	switch message {
	case wmCommand:
		if session != nil && uint32(wParam>>16) == 0 {
			switch uint32(wParam & 0xffff) {
			case toolbarCaptureCommand:
				if session.onCapture != nil {
					session.onCapture()
				}
				return 0
			case toolbarCancelCommand:
				if session.onCancel != nil {
					session.onCancel()
				}
				return 0
			}
		}
	case wmKeyDown:
		if session != nil && wParam == vkEscape && session.onCancel != nil {
			session.onCancel()
			return 0
		}
	case wmNCHitTest:
		// The toolbar background drags the toolbar; child controls still
		// receive their own hit tests.
		return htCaption
	case wmCtlColorStatic:
		procSetBkMode.Call(wParam, transparent)
		textColor, _, _ := procGetSysColor.Call(colorWindowText)
		procSetTextColor.Call(wParam, textColor)
		brush, _, _ := procGetSysColorBrush.Call(colorWindow)
		return brush
	case wmDestroy:
		delete(toolbarStates, hwnd)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, uintptr(lParam))
	return r
}

func utf16(value string) *uint16 {
	result, err := windows.UTF16PtrFromString(value)
	if err != nil {
		panic(err)
	}
	return result
}

func callError(operation string, err error) error {
	if err == windows.ERROR_SUCCESS {
		return fmt.Errorf("%s failed", operation)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
