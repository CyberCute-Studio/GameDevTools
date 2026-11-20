//go:build windows

package overlay

import (
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

const (
	overlayClassName = "GameDevToolsRectangleOverlay"
	toolbarClassName = "GameDevToolsRectangleToolbar"

	// overlayTintAlpha dims the desktop outside the selection to about 62%
	// brightness. The tint keeps the input-capturing surface visible at all
	// times (SCR-052) and never reaches capture output because the session
	// windows are destroyed before the rectangle is read (SCR-068).
	overlayTintAlpha = 96

	toolbarCaptureCommand = 1
	toolbarCancelCommand  = 2
	toolbarWidthCommand   = 3
	toolbarHeightCommand  = 4

	// Logical toolbar measurements, scaled by the cursor display's DPI. The
	// height leaves the message label two lines of the shell's 18-pixel
	// font, which are 50 pixels tall: static text word-wraps, so a shorter
	// label would clip the instructions and validation messages. The font is
	// made at the main window's DPI (SetFont), so the two lines fit while
	// the main window's display and the cursor display share a scaling; with
	// the main window on a display scaled higher, the second line is still
	// clipped (the mixed-DPI case ADR-0001 leaves unvalidated).
	toolbarLogicalWidth         = 592
	toolbarLogicalHeight        = 96
	toolbarLogicalMargin        = 16
	toolbarLogicalInset         = 12
	toolbarMessageLogicalHeight = 50

	// Logical selection visuals, scaled by the cursor display's DPI. The
	// border ring is three pixels thick so its core keeps an edge of the
	// contrasting color on both sides (ringColors); handles extend half their
	// size outside the selection edge they sit on.
	borderLogicalThickness = 3
	handleLogicalSize      = 8
	handleLogicalGrabExtra = 8

	wmDestroy        = 0x0002
	wmActivate       = 0x0006
	wmClose          = 0x0010
	wmSetCursor      = 0x0020
	wmNCHitTest      = 0x0084
	wmKeyDown        = 0x0100
	wmCommand        = 0x0111
	wmCtlColorStatic = 0x0138
	wmMouseMove      = 0x0200
	wmLButtonDown    = 0x0201
	wmLButtonUp      = 0x0202
	wmCaptureChanged = 0x0215
	// wmAppRestoreFocus asks the toolbar to give the focus back once a
	// click that activated it has been handled, as toolbarWindowProc
	// describes.
	wmAppRestoreFocus = 0x8000 + 1

	// waInactive, waActive, and waClickActive are WM_ACTIVATE's codes for a
	// window being deactivated, activated otherwise than by a click, and
	// activated by a click.
	waInactive    = 0
	waActive      = 1
	waClickActive = 2

	enKillFocus = 0x0200
	enChange    = 0x0300

	vkTab    = 0x09
	vkEscape = 0x1b
	vkReturn = 0x0d
	vkShift  = 0x10
	vkLeft   = 0x25
	vkUp     = 0x26
	vkRight  = 0x27
	vkDown   = 0x28

	htClient  = 1
	htCaption = 2

	wsPopup        = 0x80000000
	wsExLayered    = 0x00080000
	wsExTopmost    = 0x00000008
	wsExToolWindow = 0x00000080
	wsExNoActivate = 0x08000000

	swShow           = 5
	swShowNoActivate = 4

	swpNoSize     = 0x0001
	swpNoMove     = 0x0002
	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010

	ulwAlpha   = 0x00000002
	acSrcAlpha = 0x01

	dibRGBColors = 0

	colorWindow     = 5
	colorWindowText = 8
	colorHighlight  = 13
	// colorHighlightText is COLOR_HIGHLIGHTTEXT, the text drawn on the
	// highlight color.
	colorHighlightText = 14
	transparent        = 1

	idcArrow    = 32512
	idcCross    = 32515
	idcSizeNWSE = 32642
	idcSizeNESW = 32643
	idcSizeWE   = 32644
	idcSizeNS   = 32645
	idcSizeAll  = 32646
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")

	procRegisterClassExW            = user32.NewProc("RegisterClassExW")
	procCreateWindowExW             = user32.NewProc("CreateWindowExW")
	procDestroyWindow               = user32.NewProc("DestroyWindow")
	procDefWindowProcW              = user32.NewProc("DefWindowProcW")
	procShowWindow                  = user32.NewProc("ShowWindow")
	procSetForegroundWindow         = user32.NewProc("SetForegroundWindow")
	procUpdateLayeredWindowIndirect = user32.NewProc("UpdateLayeredWindowIndirect")
	procSetWindowPos                = user32.NewProc("SetWindowPos")
	procEnableWindow                = user32.NewProc("EnableWindow")
	procIsChild                     = user32.NewProc("IsChild")
	procIsWindowEnabled             = user32.NewProc("IsWindowEnabled")
	procIsWindowVisible             = user32.NewProc("IsWindowVisible")
	procGetNextDlgTabItem           = user32.NewProc("GetNextDlgTabItem")
	procPostMessageW                = user32.NewProc("PostMessageW")
	procIsDialogMessageW            = user32.NewProc("IsDialogMessageW")
	procLoadCursorW                 = user32.NewProc("LoadCursorW")
	procSetCursor                   = user32.NewProc("SetCursor")
	procSetCapture                  = user32.NewProc("SetCapture")
	procReleaseCapture              = user32.NewProc("ReleaseCapture")
	procSetFocus                    = user32.NewProc("SetFocus")
	procGetFocus                    = user32.NewProc("GetFocus")
	procGetKeyState                 = user32.NewProc("GetKeyState")
	procGetCursorPos                = user32.NewProc("GetCursorPos")
	procGetSysColor                 = user32.NewProc("GetSysColor")
	procGetSysColorBrush            = user32.NewProc("GetSysColorBrush")
	procGetModuleHandleW            = kernel32.NewProc("GetModuleHandleW")
	procCreateCompatibleDC          = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection            = gdi32.NewProc("CreateDIBSection")
	procSelectObject                = gdi32.NewProc("SelectObject")
	procDeleteObject                = gdi32.NewProc("DeleteObject")
	procDeleteDC                    = gdi32.NewProc("DeleteDC")
	procSetBkMode                   = gdi32.NewProc("SetBkMode")
	procSetTextColor                = gdi32.NewProc("SetTextColor")

	registerClassesOnce sync.Once
	registerClassesErr  error

	cursorCross    uintptr
	cursorSizeNWSE uintptr
	cursorSizeNESW uintptr
	cursorSizeWE   uintptr
	cursorSizeNS   uintptr
	cursorSizeAll  uintptr

	overlayStates = map[windows.Handle]*windowsSession{}
	toolbarStates = map[windows.Handle]*windowsSession{}
)

// windowMessage mirrors the Win32 MSG layout the shell's message loop holds,
// so FilterMessage can inspect and forward the queued message in place. It
// must stay field-for-field identical to the shell's msg type: the shell
// passes its own messages through unsafe.Pointer.
type windowMessage struct {
	HWND     windows.Handle
	Message  uint32
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

type sizeStruct struct {
	Width  int32
	Height int32
}

type rectStruct struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

// updateLayeredWindowInfo mirrors the Win32 UPDATELAYEREDWINDOWINFO
// structure UpdateLayeredWindowIndirect takes by pointer.
type updateLayeredWindowInfo struct {
	CbSize   uint32
	HdcDst   uintptr
	PptDst   unsafe.Pointer
	PSize    unsafe.Pointer
	HdcSrc   uintptr
	PptSrc   unsafe.Pointer
	CrKey    uint32
	PBlend   unsafe.Pointer
	DwFlags  uint32
	PrcDirty unsafe.Pointer
}

// blendFunction mirrors the Win32 BLENDFUNCTION structure.
type blendFunction struct {
	BlendOp             byte
	BlendFlags          byte
	SourceConstantAlpha byte
	AlphaFormat         byte
}

// bitmapInfoHeader mirrors the Win32 BITMAPINFOHEADER for a top-down 32-bit
// BGRA DIB section.
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

// dragOp identifies the pointer interaction in progress on the overlay.
type dragOp int

const (
	dragNone dragOp = iota
	dragDraw
	dragMove
	dragResize
)

// dragState tracks one pointer interaction: its operation, the resize zone
// for handle drags, the anchor pointer position for draw drags, the press
// position for move and resize drags, and the selection rectangle as it was
// when the drag started. moved records whether the pointer left the press
// position at least once, so a press that never moved is still a click.
type dragState struct {
	op       dragOp
	zone     zone
	anchor   display.Point
	origin   display.Rect
	originPt display.Point
	moved    bool
}

// windowsSession owns the topmost windows of one active session — the Free
// Rectangle dimmed virtual-desktop overlay or the Preset Rectangle exact-size
// frame, plus the floating toolbar — the overlay's backing bitmap, and the
// live selection state.
type windowsSession struct {
	mode          Mode
	monitor       *display.Monitor
	bounds        display.Rect
	overlay       windows.Handle
	toolbar       windows.Handle
	message       *ui.Label
	widthLabel    *ui.Label
	heightLabel   *ui.Label
	widthEdit     *ui.Control
	heightEdit    *ui.Control
	position      *ui.Label
	capture       *ui.Button
	cancel        *ui.Button
	captureButton windows.Handle

	onSelection func(display.Rect)
	onCapture   func()
	onCancel    func()

	selection display.Rect
	drag      dragState
	// syncing is set while the dimension fields are updated
	// programmatically, so the resulting EN_CHANGE is not mistaken for the
	// user typing again.
	syncing bool
	// focus is the toolbar control that had the keyboard focus as the
	// toolbar was last deactivated, which restoreFocus gives it back to.
	focus uintptr

	// frameOrigin is the preset frame window's top-left corner in
	// virtual-desktop coordinates: the selection inflated outward by the
	// border ring. Free Rectangle sessions leave it zero; their bitmap
	// coordinates follow bounds instead.
	frameOrigin display.Point

	// Toolbar placement inputs captured at creation.
	anchor      display.Rect
	dpi         uint32
	toolbarSize sizeStruct
	margin      int32

	// Rendering surface: a top-down 32-bit BGRA DIB composited per-pixel
	// through UpdateLayeredWindowIndirect. It covers the whole virtual
	// desktop in Free Rectangle sessions, where the selection interior
	// stays visually clear while the rest of the desktop stays dimmed, and
	// exactly the frame window in Preset Rectangle sessions, where the
	// alpha-zero interior lets clicks through to the windows beneath.
	memDC       uintptr
	dib         uintptr
	oldBitmap   uintptr
	pixels      unsafe.Pointer
	pixelWidth  int32
	pixelHeight int32

	border     int32
	handleSize int32
	grab       int32
	// borderPx is the selection ring's core and edgePx the one-pixel edge
	// on each side of it, as ringColors chooses them (UX-007).
	borderPx uint32
	edgePx   uint32
}

func registerOverlayClasses() error {
	registerClassesOnce.Do(func() {
		instance, _, err := procGetModuleHandleW.Call(0)
		if instance == 0 {
			registerClassesErr = callError("GetModuleHandleW", err)
			return
		}
		arrow, _, err := procLoadCursorW.Call(0, idcArrow)
		if arrow == 0 {
			registerClassesErr = callError("LoadCursorW(IDC_ARROW)", err)
			return
		}
		loadCursor := func(id uintptr, name string) (uintptr, bool) {
			cursor, _, err := procLoadCursorW.Call(0, id)
			if cursor == 0 {
				registerClassesErr = callError("LoadCursorW("+name+")", err)
				return 0, false
			}
			return cursor, true
		}
		ok := true
		if cursorCross, ok = loadCursor(idcCross, "IDC_CROSS"); !ok {
			return
		}
		if cursorSizeNWSE, ok = loadCursor(idcSizeNWSE, "IDC_SIZENWSE"); !ok {
			return
		}
		if cursorSizeNESW, ok = loadCursor(idcSizeNESW, "IDC_SIZENESW"); !ok {
			return
		}
		if cursorSizeWE, ok = loadCursor(idcSizeWE, "IDC_SIZEWE"); !ok {
			return
		}
		if cursorSizeNS, ok = loadCursor(idcSizeNS, "IDC_SIZENS"); !ok {
			return
		}
		if cursorSizeAll, ok = loadCursor(idcSizeAll, "IDC_SIZEALL"); !ok {
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
			HCursor:       windows.Handle(cursorCross),
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

// createNative shows the session's windows. Free Rectangle sessions get a
// per-pixel-alpha overlay covering the whole virtual desktop; Preset
// Rectangle sessions get an exact-size frame floating above the desktop.
// Neither window activates: the toolbar takes the foreground so Escape, Tab,
// and Enter work while the main window is hidden. Preset sessions report
// their initial frame as the live selection before returning, so the
// controller's Capture command is armed with the exact preset dimensions
// (SCR-020).
func createNative(request CreateRequest) (NativeSession, error) {
	if err := registerOverlayClasses(); err != nil {
		return nil, err
	}
	session := &windowsSession{
		mode:        request.Mode,
		monitor:     request.Monitor,
		bounds:      request.Bounds,
		onSelection: request.OnSelection,
		onCapture:   request.OnCapture,
		onCancel:    request.OnCancel,
	}
	dpi := uint32(display.ReferenceDPI)
	anchor := request.Bounds
	if item, err := request.Monitor.CursorDisplay(); err == nil {
		dpi = item.DPIX
		anchor = item.WorkArea
	}
	session.configure(dpi, anchor)
	if session.mode == ModePresetRectangle {
		session.selection = presetPlacement(anchor, request.Bounds, request.Size.X, request.Size.Y)
		if err := session.createWindow(ui.PresetFrameWindowTitle); err != nil {
			return nil, err
		}
		session.frameOrigin = display.Point{X: session.selection.Left - session.border, Y: session.selection.Top - session.border}
	} else if err := session.createWindow(ui.OverlayWindowTitle); err != nil {
		return nil, err
	}
	overlayStates[session.overlay] = session
	width, height := request.Bounds.Width(), request.Bounds.Height()
	if session.mode == ModePresetRectangle {
		width, height = session.selection.Width()+2*session.border, session.selection.Height()+2*session.border
	}
	if err := session.createSurface(width, height); err != nil {
		session.Close()
		return nil, err
	}
	if err := session.createToolbar(request); err != nil {
		session.Close()
		return nil, err
	}
	procShowWindow.Call(uintptr(session.overlay), swShowNoActivate)
	if err := session.renderAll(); err != nil {
		session.Close()
		return nil, err
	}
	procShowWindow.Call(uintptr(session.toolbar), swShow)
	// Best effort: if another window owns the foreground the toolbar does not
	// take keyboard focus (keystrokes then belong to that other application's
	// thread, not to the shell's message filter), but it stays usable by
	// mouse, and clicking it once foregrounds it and restores Escape.
	procSetForegroundWindow.Call(uintptr(session.toolbar))
	// A Free Rectangle session starts in the width field, so a keyboard user
	// types the size straight away (SCR-015); a Preset Rectangle session
	// keeps the focus on the toolbar itself, where the arrow keys nudge the
	// frame (SCR-027). A screen reader reads the focused control on its own,
	// and the instructions beside it only once they are announced.
	if session.mode == ModeFreeRectangle {
		session.widthEdit.Focus()
	}
	session.message.Announce()
	if session.mode == ModePresetRectangle && session.onSelection != nil {
		session.onSelection(session.selection)
	}
	return session, nil
}

// configure fixes the session's DPI-scaled measurements and toolbar anchor
// before any window is created: the preset frame's border ring decides the
// frame window's size.
func (s *windowsSession) configure(dpi uint32, anchor display.Rect) {
	scale := func(value int32) int32 {
		return display.LogicalToPhysical(value, dpi)
	}
	s.anchor = anchor
	s.dpi = dpi
	s.toolbarSize = sizeStruct{Width: scale(toolbarLogicalWidth), Height: scale(toolbarLogicalHeight)}
	s.margin = scale(toolbarLogicalMargin)
	s.border = scale(borderLogicalThickness)
	s.handleSize = scale(handleLogicalSize)
	s.grab = s.handleSize + scale(handleLogicalGrabExtra)
	s.borderPx, s.edgePx = systemRingColors()
}

// createWindow creates the session's input surface: the desktop-sized Free
// Rectangle tint overlay, or the Preset Rectangle frame sized to the
// selection plus its border ring. The window never activates, so it cannot
// rise above the toolbar or steal keyboard focus.
func (s *windowsSession) createWindow(title string) error {
	window := display.Rect{Left: s.bounds.Left, Top: s.bounds.Top, Right: s.bounds.Right, Bottom: s.bounds.Bottom}
	if s.mode == ModePresetRectangle {
		window = inflateRect(s.selection, s.border)
	}
	overlay, _, err := procCreateWindowExW.Call(
		wsExLayered|wsExTopmost|wsExToolWindow|wsExNoActivate,
		uintptr(unsafe.Pointer(utf16(overlayClassName))),
		uintptr(unsafe.Pointer(utf16(title))),
		wsPopup,
		uintptr(uint32(window.Left)), uintptr(uint32(window.Top)),
		uintptr(uint32(window.Width())), uintptr(uint32(window.Height())),
		0, 0, 0, 0,
	)
	if overlay == 0 {
		return callError("CreateWindowExW(overlay)", err)
	}
	s.overlay = windows.Handle(overlay)
	return nil
}

// createSurface allocates the session's backing bitmap: the whole desktop
// for the Free Rectangle tint, dimmed once here and later repainted in the
// selection's band, or the Preset Rectangle frame window.
func (s *windowsSession) createSurface(width, height int32) error {
	memory, _, err := procCreateCompatibleDC.Call(0)
	if memory == 0 {
		return callError("CreateCompatibleDC", err)
	}
	s.memDC = memory
	bitmap, pixels, err := createDIB(memory, width, height)
	if err != nil {
		return err
	}
	previous, _, selErr := procSelectObject.Call(memory, bitmap)
	if previous == 0 {
		procDeleteObject.Call(bitmap)
		return callError("SelectObject", selErr)
	}
	s.oldBitmap = previous
	s.dib = bitmap
	s.pixels = pixels
	s.pixelWidth = width
	s.pixelHeight = height
	if s.mode == ModeFreeRectangle {
		s.fillLocal(display.Rect{Right: width, Bottom: height}, dimPixel)
	}
	return nil
}

// resizeSurface swaps the backing bitmap for a new size, keeping the old one
// when allocation fails so the session still renders. The preset frame needs
// it when typed dimensions change the selection's size.
func (s *windowsSession) resizeSurface(width, height int32) error {
	bitmap, pixels, err := createDIB(s.memDC, width, height)
	if err != nil {
		return err
	}
	previous, _, selErr := procSelectObject.Call(s.memDC, bitmap)
	if previous == 0 {
		procDeleteObject.Call(bitmap)
		return callError("SelectObject", selErr)
	}
	// previous is the session's current DIB; the stock bitmap it replaced at
	// creation stays in oldBitmap for Close.
	procDeleteObject.Call(previous)
	s.dib = bitmap
	s.pixels = pixels
	s.pixelWidth = width
	s.pixelHeight = height
	return nil
}

// createDIB allocates one top-down 32-bit BGRA DIB section.
func createDIB(memory uintptr, width, height int32) (uintptr, unsafe.Pointer, error) {
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
		memory, uintptr(unsafe.Pointer(&info)), dibRGBColors,
		uintptr(unsafe.Pointer(&pixels)), 0, 0,
	)
	if bitmap == 0 || pixels == nil {
		return 0, nil, callError("CreateDIBSection", err)
	}
	return bitmap, pixels, nil
}

func (s *windowsSession) createToolbar(request CreateRequest) error {
	width, height := s.toolbarSize.Width, s.toolbarSize.Height
	x, y := toolbarPlacement(request.Bounds, s.anchor, s.selection, width, height, s.margin)
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

	// The message shows the instructions and, in their place, why a typed
	// size was refused, so it is a live region a screen reader announces.
	// It is kept at once, so Close clears its marking (ui.Label.ClearLive)
	// even when a later control cannot be created.
	message, err := ui.NewLiveLabel(s.toolbar, s.instructions())
	if err != nil {
		return err
	}
	s.message = message
	widthLabel, err := ui.NewMnemonicLabel(s.toolbar, ui.SelectionWidthLabel)
	if err != nil {
		return err
	}
	widthEdit, err := ui.NewEdit(s.toolbar, toolbarWidthCommand)
	if err != nil {
		return err
	}
	heightLabel, err := ui.NewMnemonicLabel(s.toolbar, ui.SelectionHeightLabel)
	if err != nil {
		return err
	}
	heightEdit, err := ui.NewEdit(s.toolbar, toolbarHeightCommand)
	if err != nil {
		return err
	}
	if s.mode == ModePresetRectangle {
		position, err := ui.NewLabel(s.toolbar, "")
		if err != nil {
			return err
		}
		s.position = position
	}
	captureButton, err := ui.NewButton(s.toolbar, ui.CaptureAction, ui.ButtonOptions{ID: toolbarCaptureCommand, Primary: true, Disabled: true})
	if err != nil {
		return err
	}
	cancelButton, err := ui.NewButton(s.toolbar, ui.CancelAction, ui.ButtonOptions{ID: toolbarCancelCommand})
	if err != nil {
		return err
	}
	// The dimension fields stay enabled in both modes: in a Free Rectangle
	// session without a selection, typing a width and height creates one
	// (SCR-015), so the mode can be completed from the keyboard.
	s.widthLabel = widthLabel
	s.heightLabel = heightLabel
	s.widthEdit = widthEdit
	s.heightEdit = heightEdit
	s.capture = captureButton
	s.cancel = cancelButton

	if request.Font != 0 {
		s.SetFont(request.Font)
	}
	s.layoutToolbarControls()
	s.captureButton = captureButton.Handle()
	s.syncDimensions()
	s.syncPosition()
	return nil
}

// SetFont applies the shell's body font to every toolbar control. It runs at
// creation and again when the shell recreates its fonts for a DPI change:
// the old handle is destroyed in that recreation, so a control left holding
// it would paint with a fallback font at the wrong size.
func (s *windowsSession) SetFont(font uintptr) {
	if s.toolbar == 0 {
		return
	}
	handle := windows.Handle(font)
	labels := []*ui.Label{s.message, s.widthLabel, s.heightLabel}
	if s.position != nil {
		labels = append(labels, s.position)
	}
	for _, control := range labels {
		control.SetFont(handle)
	}
	s.widthEdit.SetFont(handle)
	s.heightEdit.SetFont(handle)
	s.capture.SetFont(handle)
	s.cancel.SetFont(handle)
}

// layoutToolbarControls positions the toolbar's controls for the session's
// current DPI-scaled measurements. It runs at creation and again when a
// display change re-scales the toolbar to a new cursor-display DPI.
func (s *windowsSession) layoutToolbarControls() {
	scale := func(value int32) int32 {
		return display.LogicalToPhysical(value, s.dpi)
	}
	width := s.toolbarSize.Width
	inset := scale(toolbarLogicalInset)
	s.message.Move(inset, scale(6), width-2*inset, scale(toolbarMessageLogicalHeight))
	rowY := scale(58)
	labelWidth, editWidth := scale(48), scale(64)
	s.widthLabel.Move(inset, rowY+scale(3), labelWidth, scale(20))
	s.widthEdit.Move(inset+labelWidth, rowY, editWidth, scale(24))
	heightX := inset + labelWidth + editWidth + inset
	s.heightLabel.Move(heightX, rowY+scale(3), labelWidth, scale(20))
	s.heightEdit.Move(heightX+labelWidth, rowY, editWidth, scale(24))
	buttonWidth, buttonHeight := scale(88), scale(28)
	s.cancel.Move(width-inset-buttonWidth, rowY-scale(2), buttonWidth, buttonHeight)
	captureX := width - inset - 2*buttonWidth - scale(8)
	s.capture.Move(captureX, rowY-scale(2), buttonWidth, buttonHeight)
	if s.position != nil {
		// The X/Y readout (SCR-028) takes the space between the dimension
		// fields and the Capture button.
		positionX := heightX + labelWidth + editWidth + scale(8)
		s.position.Move(positionX, rowY+scale(3), captureX-positionX-scale(8), scale(20))
	}
}

// instructions returns the toolbar's guidance text for the session mode.
func (s *windowsSession) instructions() string {
	if s.mode == ModePresetRectangle {
		return ui.OverlayPresetInstructions
	}
	return ui.OverlayInstructions
}

// systemRingColors returns the selection ring's core and edge pixels for the
// active Windows colors, as ringColors chooses them.
func systemRingColors() (core, edge uint32) {
	color := func(index uintptr) uint32 {
		value, _, _ := procGetSysColor.Call(index)
		return uint32(value)
	}
	return ringColors(color(colorHighlight), color(colorHighlightText), color(colorWindow), color(colorWindowText))
}

func (s *windowsSession) Close() {
	if s.toolbar != 0 {
		// Destroying the toolbar takes the focus from the field holding it,
		// whose focus loss would otherwise commit the typed size — creating
		// a selection, or announcing a refusal, after the session ended — so
		// the toolbar stops reaching the session first. The message's
		// live-region annotation is cleared, as ui.Label.ClearLive
		// describes.
		delete(toolbarStates, s.toolbar)
		if s.message != nil {
			s.message.ClearLive()
		}
		procDestroyWindow.Call(uintptr(s.toolbar))
		s.toolbar = 0
	}
	if s.overlay != 0 {
		procDestroyWindow.Call(uintptr(s.overlay))
		s.overlay = 0
	}
	if s.memDC != 0 {
		if s.oldBitmap != 0 {
			procSelectObject.Call(s.memDC, s.oldBitmap)
			s.oldBitmap = 0
		}
		if s.dib != 0 {
			procDeleteObject.Call(s.dib)
			s.dib = 0
		}
		procDeleteDC.Call(s.memDC)
		s.memDC = 0
		s.pixels = nil
	}
}

func (s *windowsSession) SetSelectionReady(ready bool) {
	if s.captureButton == 0 {
		return
	}
	if !ready && s.toolbar != 0 {
		// The overlay never takes activation, so the Capture button can
		// still hold keyboard focus when a fresh draw disables it; move
		// focus to the toolbar first, the same way a draw moves it off the
		// dimension fields (onButtonDown), so Escape keeps cancelling.
		if focus, _, _ := procGetFocus.Call(); focus == uintptr(s.captureButton) {
			procSetFocus.Call(uintptr(s.toolbar))
		}
	}
	enabled := uintptr(0)
	if ready {
		enabled = 1
	}
	procEnableWindow.Call(uintptr(s.captureButton), enabled)
}

// Attention draws the user back to the active session when the global
// capture shortcut fires while a selection is already on screen (SCR-005):
// the toolbar retakes the foreground so Escape, the commands, and the
// dimension fields are where the user looks. Pressing a registered hotkey
// grants the process foreground rights, so the request is honored while
// the user invokes the shortcut from another application; if another
// window still keeps the foreground, the topmost toolbar stays visible
// above it and one click brings focus back.
func (s *windowsSession) Attention() {
	if s.toolbar == 0 {
		return
	}
	procSetForegroundWindow.Call(uintptr(s.toolbar))
}

// restoreFocus gives the keyboard focus back to the toolbar control that
// had it as the toolbar was last deactivated, as toolbarWindowProc
// describes, while that control can still take it. Otherwise, as when
// nothing was focused or the toolbar itself was, the focus stays on the
// toolbar, where Escape cancels, Tab reaches the controls, and the arrow
// keys nudge a selection.
func (s *windowsSession) restoreFocus() {
	control := s.focus
	if control == 0 {
		return
	}
	if child, _, _ := procIsChild.Call(uintptr(s.toolbar), control); child == 0 {
		return
	}
	if visible, _, _ := procIsWindowVisible.Call(control); visible == 0 {
		return
	}
	if enabled, _, _ := procIsWindowEnabled.Call(control); enabled == 0 {
		return
	}
	procSetFocus.Call(control)
}

// DisplayChanged revalidates the session against a changed virtual desktop
// (SCR-054). The selection keeps its exact dimensions whenever they still
// fit the new desktop — the Preset Rectangle frame's dimensions never change
// (SCR-020) — and moves back inside when its coordinates no longer fit,
// while the Free Rectangle tint is resized to cover the new desktop exactly.
// A selection whose dimensions exceed the new desktop cannot be kept, and
// false tells the controller to end the session with an explanation
// (AC-S12); false is also returned when the new surfaces cannot be
// allocated, because a session that cannot repaint itself to match the
// desktop must not stay up intercepting input (SCR-052). The revalidated
// selection is reported through OnSelection, like every selection change. A
// notification whose physical topology, cursor-display scaling, and anchor
// work area all match the session's current measurements changes nothing the
// surfaces show, so it is answered without re-rendering.
func (s *windowsSession) DisplayChanged(bounds display.Rect) bool {
	selection, ok := revalidatedSelection(s.selection, bounds)
	if !ok {
		return false
	}
	// A topology change can move the cursor display and change its scaling,
	// so the toolbar anchor and the DPI-derived measurements are re-resolved
	// rather than carried over from the old layout.
	dpi := uint32(display.ReferenceDPI)
	anchor := bounds
	if s.monitor != nil {
		if item, err := s.monitor.CursorDisplay(); err == nil {
			dpi = item.DPIX
			anchor = item.WorkArea
		}
	}
	if bounds == s.bounds && dpi == s.dpi && anchor == s.anchor {
		return true
	}
	// Pointer coordinates from a drag in progress belong to the old desktop;
	// end the interaction rather than apply them to the new layout.
	s.drag = dragState{}
	procReleaseCapture.Call()
	s.configure(dpi, anchor)
	s.bounds = bounds
	s.selection = selection
	if s.mode == ModePresetRectangle {
		s.frameOrigin = display.Point{X: selection.Left - s.border, Y: selection.Top - s.border}
		width, height := selection.Width()+2*s.border, selection.Height()+2*s.border
		if width != s.pixelWidth || height != s.pixelHeight {
			if err := s.resizeSurface(width, height); err != nil {
				return false
			}
		}
		// renderFrame's present repositions and resizes the layered frame
		// window to the revalidated selection.
		if err := s.renderFrame(); err != nil {
			return false
		}
	} else {
		if width, height := bounds.Width(), bounds.Height(); width != s.pixelWidth || height != s.pixelHeight {
			if err := s.resizeSurface(width, height); err != nil {
				return false
			}
		}
		// The whole desktop is repainted — re-dimmed with the revalidated
		// selection drawn into it — and uploaded in one pass that also
		// repositions the window to the new desktop origin.
		full := display.Rect{Right: s.pixelWidth, Bottom: s.pixelHeight}
		s.paintSelection(full)
		if err := s.present(full); err != nil {
			return false
		}
	}
	// The toolbar's own window still has the old DPI's size; resize it and
	// re-lay out its controls before placing it near the selection again.
	procSetWindowPos.Call(uintptr(s.toolbar), 0,
		0, 0, uintptr(s.toolbarSize.Width), uintptr(s.toolbarSize.Height),
		swpNoZOrder|swpNoActivate|swpNoMove)
	s.layoutToolbarControls()
	// Without a selection the fields hold the size the user is typing toward
	// one (SCR-015), which a display change must not wipe; with one they show
	// its revalidated size.
	if !selection.Empty() {
		s.syncDimensions()
	}
	s.syncPosition()
	s.placeToolbar()
	if s.onSelection != nil {
		s.onSelection(selection)
	}
	return true
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

func (s *windowsSession) isDimensionEdit(hwnd uintptr) bool {
	return s.widthEdit != nil && hwnd == uintptr(s.widthEdit.Handle()) ||
		s.heightEdit != nil && hwnd == uintptr(s.heightEdit.Handle())
}

// FilterMessage pre-handles the GUI thread's queued messages while a session
// is active. Escape cancels from any session window, Enter inside a
// dimension field commits the typed value instead of triggering the default
// Capture button, arrow keys nudge the selection — the preset frame, or a
// free rectangle once one exists — by one physical pixel (ten with Shift,
// SCR-015, SCR-027), except inside the dimension fields, where they keep
// moving the text caret, and toolbar messages go through IsDialogMessage so
// Tab and Enter work between the toolbar controls even though the toolbar is
// a popup rather than a dialog (in a Free Rectangle session without a
// selection, arrow keys keep their dialog-navigation role there, and Tab out
// of the filled pair of dimension fields creates the typed selection first,
// as commitBeforeTab describes). Messages for other windows pass through
// untouched.
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
	if msg.Message == wmKeyDown && msg.WParam == vkReturn && native.isDimensionEdit(hwnd) {
		native.commitDimensions(true)
		return true
	}
	if !native.selection.Empty() && msg.Message == wmKeyDown && native.owns(hwnd) && !native.isDimensionEdit(hwnd) {
		dx, dy := int32(0), int32(0)
		switch msg.WParam {
		case vkLeft:
			dx = -1
		case vkRight:
			dx = 1
		case vkUp:
			dy = -1
		case vkDown:
			dy = 1
		}
		if dx != 0 || dy != 0 {
			native.nudge(dx, dy, nudgeStep())
			return true
		}
	}
	if msg.Message == wmKeyDown && msg.WParam == vkTab && native.selection.Empty() && native.isDimensionEdit(hwnd) {
		native.commitBeforeTab(hwnd)
	}
	if native.isToolbarMessage(hwnd) {
		handled, _, _ := procIsDialogMessageW.Call(uintptr(native.toolbar), uintptr(message))
		return handled != 0
	}
	return false
}

// setSelection records a new live selection, updates the session surface,
// keeps the toolbar's dimension fields in sync, and reports the change to
// the controller. The toolbar is not repositioned here — that happens when a
// drag or edit finishes, so it does not jump around mid-drag.
func (s *windowsSession) setSelection(selection display.Rect) {
	if s.mode == ModePresetRectangle {
		s.setPresetSelection(selection)
		return
	}
	oldVisual := s.visualBounds()
	s.selection = selection
	if err := s.renderSelection(oldVisual); err != nil {
		// A compositing failure leaves the previous frame visible; the
		// interaction stays usable, so the error is not fatal.
		_ = err
	}
	s.syncDimensions()
	if s.onSelection != nil {
		s.onSelection(selection)
	}
}

// setPresetSelection moves or resizes the preset frame to the new selection
// (SCR-020, SCR-029). A pure move slides the window with its rendered border
// ring untouched; a size change swaps the backing bitmap and repaints, and
// UpdateLayeredWindowIndirect resizes the window to match. The border ring
// always sits outside the selection, so even an incompletely flushed frame
// could never put frame visuals inside the captured pixels (SCR-068).
func (s *windowsSession) setPresetSelection(selection display.Rect) {
	resized := selection.Width() != s.selection.Width() || selection.Height() != s.selection.Height()
	if resized {
		if err := s.resizeSurface(selection.Width()+2*s.border, selection.Height()+2*s.border); err != nil {
			// The backing bitmap could not be swapped for the new size, so
			// the frame keeps the previous rectangle: painting the new ring
			// into the old bitmap would clip it, and the frame would no
			// longer outline the rectangle Capture reads. The dimension
			// fields revert to the actual frame, exactly like a rejected
			// edit.
			s.report(fmt.Sprintf(ui.SelectionResizeFailed, err))
			s.syncDimensions()
			return
		}
	}
	s.selection = selection
	s.frameOrigin = display.Point{X: selection.Left - s.border, Y: selection.Top - s.border}
	if resized {
		if err := s.renderFrame(); err != nil {
			// A compositing failure leaves the previous frame visible; the
			// interaction stays usable, so the error is not fatal.
			_ = err
		}
	} else {
		procSetWindowPos.Call(uintptr(s.overlay), 0,
			uintptr(uint32(s.frameOrigin.X)), uintptr(uint32(s.frameOrigin.Y)), 0, 0,
			swpNoSize|swpNoZOrder|swpNoActivate)
	}
	s.syncDimensions()
	s.syncPosition()
	if s.onSelection != nil {
		s.onSelection(selection)
	}
}

// visualBounds returns the virtual-desktop rectangle the selection's
// painted visuals cover: the selection plus its border and the half of each
// handle that extends outside.
func (s *windowsSession) visualBounds() display.Rect {
	if s.selection.Empty() {
		return display.Rect{}
	}
	pad := max(s.border, s.handleSize/2) + 1
	return display.Rect{
		Left:   s.selection.Left - pad,
		Top:    s.selection.Top - pad,
		Right:  s.selection.Right + pad,
		Bottom: s.selection.Bottom + pad,
	}
}

// clientOrigin returns the virtual-desktop point the session window's client
// coordinates start at: the desktop's top-left corner for the Free Rectangle
// tint, the frame window's top-left corner for the Preset Rectangle frame.
func (s *windowsSession) clientOrigin() display.Point {
	if s.mode == ModePresetRectangle {
		return s.frameOrigin
	}
	return display.Point{X: s.bounds.Left, Y: s.bounds.Top}
}

// toLocal converts virtual-desktop coordinates into session window client
// coordinates, which are also the backing bitmap's pixel coordinates.
func (s *windowsSession) toLocal(r display.Rect) display.Rect {
	origin := s.clientOrigin()
	return display.Rect{
		Left:   r.Left - origin.X,
		Top:    r.Top - origin.Y,
		Right:  r.Right - origin.X,
		Bottom: r.Bottom - origin.Y,
	}
}

func (s *windowsSession) pixelSlice() []uint32 {
	return unsafe.Slice((*uint32)(s.pixels), int(s.pixelWidth)*int(s.pixelHeight))
}

// fillLocal paints one rectangle of the backing bitmap in client
// coordinates, clipped to the bitmap.
func (s *windowsSession) fillLocal(r display.Rect, px uint32) {
	left := max(r.Left, 0)
	top := max(r.Top, 0)
	right := min(r.Right, s.pixelWidth)
	bottom := min(r.Bottom, s.pixelHeight)
	if left >= right || top >= bottom {
		return
	}
	pixels := s.pixelSlice()
	stride := int(s.pixelWidth)
	for y := top; y < bottom; y++ {
		row := int(y) * stride
		for x := left; x < right; x++ {
			pixels[row+int(x)] = px
		}
	}
}

// fillRing paints the band between an outer and an inner rectangle.
func (s *windowsSession) fillRing(outer, inner display.Rect, px uint32) {
	if inner.Empty() {
		s.fillLocal(outer, px)
		return
	}
	s.fillLocal(display.Rect{Left: outer.Left, Top: outer.Top, Right: outer.Right, Bottom: inner.Top}, px)
	s.fillLocal(display.Rect{Left: outer.Left, Top: inner.Bottom, Right: outer.Right, Bottom: outer.Bottom}, px)
	s.fillLocal(display.Rect{Left: outer.Left, Top: inner.Top, Right: inner.Left, Bottom: inner.Bottom}, px)
	s.fillLocal(display.Rect{Left: inner.Right, Top: inner.Top, Right: outer.Right, Bottom: inner.Bottom}, px)
}

func inflateRect(r display.Rect, pad int32) display.Rect {
	return display.Rect{Left: r.Left - pad, Top: r.Top - pad, Right: r.Right + pad, Bottom: r.Bottom + pad}
}

// dimPixel is the premultiplied black tint at overlayTintAlpha.
const dimPixel = uint32(overlayTintAlpha) << 24

// selectionPixel paints the selection interior: visually clear, but not
// alpha zero. A layered window's hit testing follows the alpha channel, so
// alpha-zero pixels pass mouse messages through to the windows beneath —
// clicks inside the selection would reach other applications mid-session
// and the interior move-drag would never start (SCR-012, SCR-052). Alpha 1
// is imperceptible but hit-tests as solid.
const selectionPixel = 1

// renderAll composites the session's initial frame: the fully dimmed
// desktop for Free Rectangle (painted when the surface was created), or the
// preset frame's border ring.
func (s *windowsSession) renderAll() error {
	if s.mode == ModePresetRectangle {
		return s.renderFrame()
	}
	return s.present(display.Rect{Right: s.pixelWidth, Bottom: s.pixelHeight})
}

// renderFrame paints the preset frame: the border ring opaque and draggable,
// the selection interior alpha zero. A layered window's hit testing follows
// the alpha channel, so the interior passes mouse input through to the
// windows beneath — the underlying application receives clicks inside the
// frame while the frame keeps its position and dimensions (SCR-022, AC-S03)
// — and the transparent interior never obscures the content being framed.
// The ring sits outside the selection rectangle, so it cannot reach the
// captured pixels (SCR-068).
func (s *windowsSession) renderFrame() error {
	s.fillLocal(display.Rect{Right: s.pixelWidth, Bottom: s.pixelHeight}, 0)
	sel := s.toLocal(s.selection)
	s.fillRing(inflateRect(sel, s.border), sel, s.borderPx)
	s.fillRing(inflateRect(sel, s.border), inflateRect(sel, s.border-1), s.edgePx)
	s.fillRing(inflateRect(sel, 1), sel, s.edgePx)
	return s.present(display.Rect{Right: s.pixelWidth, Bottom: s.pixelHeight})
}

// renderSelection repaints the band where the selection visuals changed:
// the union of the old and new visual bounds is re-dimmed, the new
// selection interior is cleared to the visually transparent
// selectionPixel, and its border and handles are drawn on top. The border
// ring sits outside the selection rectangle, so even an incompletely
// flushed frame could never put selection visuals inside the captured
// pixels (SCR-068).
func (s *windowsSession) renderSelection(oldVisual display.Rect) error {
	dirty := rectUnion(oldVisual, s.visualBounds())
	if dirty.Empty() {
		return nil
	}
	local := s.toLocal(dirty)
	s.paintSelection(local)
	return s.present(local)
}

// paintSelection paints one band of the backing bitmap, in client
// coordinates: the band is re-dimmed and the selection's interior, border,
// and handles are drawn into it. The band must cover the selection's visual
// bounds; it may be larger, as when the whole desktop is repainted after a
// display change.
func (s *windowsSession) paintSelection(local display.Rect) {
	s.fillLocal(local, dimPixel)
	if !s.selection.Empty() {
		sel := s.toLocal(s.selection)
		s.fillLocal(sel, selectionPixel)
		s.fillRing(inflateRect(sel, s.border), sel, s.borderPx)
		s.fillRing(inflateRect(sel, s.border), inflateRect(sel, s.border-1), s.edgePx)
		s.fillRing(inflateRect(sel, 1), sel, s.edgePx)
		if s.drag.op != dragDraw {
			s.drawHandles(sel)
		}
	}
}

// drawHandles paints the eight resize handles as squares of the ring's core
// color edged in its edge color (ringColors), centered on the selection's
// corners and edge midpoints.
func (s *windowsSession) drawHandles(sel display.Rect) {
	centerX := (sel.Left + sel.Right) / 2
	centerY := (sel.Top + sel.Bottom) / 2
	half := s.handleSize / 2
	for _, at := range []point{
		{sel.Left, sel.Top}, {centerX, sel.Top}, {sel.Right, sel.Top},
		{sel.Right, centerY}, {sel.Right, sel.Bottom}, {centerX, sel.Bottom},
		{sel.Left, sel.Bottom}, {sel.Left, centerY},
	} {
		square := display.Rect{Left: at.X - half, Top: at.Y - half, Right: at.X - half + s.handleSize, Bottom: at.Y - half + s.handleSize}
		s.fillLocal(square, s.edgePx)
		s.fillLocal(inflateRect(square, -1), s.borderPx)
	}
}

// present composites the backing bitmap onto the overlay window. Only the
// dirty band, in overlay client coordinates, is uploaded:
// UpdateLayeredWindow always resizes the window to the source size, so
// compositing goes through UpdateLayeredWindowIndirect, whose dirty
// rectangle uploads just the repainted band while the window keeps covering
// the whole virtual desktop. During a drag this runs per mouse move, and a
// full-desktop upload every move is a noticeable cost on multi-monitor
// desktops.
func (s *windowsSession) present(dirty display.Rect) error {
	dirty = display.Rect{
		Left:   max(dirty.Left, 0),
		Top:    max(dirty.Top, 0),
		Right:  min(dirty.Right, s.pixelWidth),
		Bottom: min(dirty.Bottom, s.pixelHeight),
	}
	if dirty.Empty() {
		return nil
	}
	origin := s.clientOrigin()
	position := point{X: origin.X, Y: origin.Y}
	size := sizeStruct{Width: s.pixelWidth, Height: s.pixelHeight}
	source := point{}
	blend := blendFunction{SourceConstantAlpha: 255, AlphaFormat: acSrcAlpha}
	band := rectStruct{Left: dirty.Left, Top: dirty.Top, Right: dirty.Right, Bottom: dirty.Bottom}
	info := updateLayeredWindowInfo{
		HdcDst:   0,
		PptDst:   unsafe.Pointer(&position),
		PSize:    unsafe.Pointer(&size),
		HdcSrc:   s.memDC,
		PptSrc:   unsafe.Pointer(&source),
		PBlend:   unsafe.Pointer(&blend),
		DwFlags:  ulwAlpha,
		PrcDirty: unsafe.Pointer(&band),
	}
	info.CbSize = uint32(unsafe.Sizeof(info))
	r, _, err := procUpdateLayeredWindowIndirect.Call(uintptr(s.overlay), uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return callError("UpdateLayeredWindowIndirect", err)
	}
	return nil
}

func rectUnion(a, b display.Rect) display.Rect {
	if a.Empty() {
		return b
	}
	if b.Empty() {
		return a
	}
	return display.Rect{
		Left:   min(a.Left, b.Left),
		Top:    min(a.Top, b.Top),
		Right:  max(a.Right, b.Right),
		Bottom: max(a.Bottom, b.Bottom),
	}
}

// placeToolbar moves the toolbar near the selection without covering it
// where practical (SCR-025). It runs when a drag or dimension edit
// finishes, not on every mouse move, so the toolbar does not jump around
// under the pointer mid-drag.
func (s *windowsSession) placeToolbar() {
	if s.toolbar == 0 {
		// WM_CAPTURECHANGED can arrive while Close is destroying the
		// session, after the toolbar is already gone.
		return
	}
	x, y := toolbarPlacement(s.bounds, s.anchor, s.selection, s.toolbarSize.Width, s.toolbarSize.Height, s.margin)
	procSetWindowPos.Call(uintptr(s.toolbar), 0,
		uintptr(uint32(x)), uintptr(uint32(y)), 0, 0,
		swpNoSize|swpNoZOrder|swpNoActivate)
}

// syncDimensions mirrors the live selection dimensions into the toolbar's
// fields (SCR-011). Both fields are updated even while one holds keyboard
// focus: selection changes come from pointer drags and keyboard nudges, and
// leaving a stale value in the focused field would let a later focus-loss
// commit silently apply it over the newer rectangle.
func (s *windowsSession) syncDimensions() {
	if s.widthEdit == nil || s.heightEdit == nil {
		return
	}
	width, height := "", ""
	if !s.selection.Empty() {
		width = fmt.Sprintf("%d", s.selection.Width())
		height = fmt.Sprintf("%d", s.selection.Height())
	}
	s.syncing = true
	defer func() { s.syncing = false }()
	s.widthEdit.SetText(width)
	s.heightEdit.SetText(height)
}

// commitDimensions applies the toolbar's typed width and height to the
// selection (SCR-013, SCR-029). Invalid or non-fitting values are rejected
// with a validation message and the fields revert to the actual dimensions,
// so a mistyped value can never resize the capture silently. The edit is ad
// hoc: it updates the active rectangle only and never touches the saved
// preset. While the pointer is mid-drag the fields track the live drag, so a
// commit (Enter can arrive from a field that kept focus across an overlay
// click) would only fight the drag and jump the toolbar; it is dropped. A
// Free Rectangle session without a selection creates one from the typed
// values instead, as createTypedSelection describes. explicit is whether the
// user asked for the commit, with Enter, rather than moved the focus out of
// a field.
func (s *windowsSession) commitDimensions(explicit bool) {
	if s.drag.op != dragNone {
		return
	}
	if s.selection.Empty() {
		s.createTypedSelection(explicit)
		return
	}
	width, height, refusal := s.typedSize()
	if refusal != "" {
		s.report(refusal)
		s.syncDimensions()
		return
	}
	resized, ok := sizedSelection(s.selection, width, height, s.bounds)
	if !ok {
		s.report(ui.SelectionFitInvalid)
		s.syncDimensions()
		return
	}
	s.applyTypedSize(resized)
}

// typedSize parses the typed width and height (SCR-013), or returns the
// validation message that refuses the first field that is not a size.
func (s *windowsSession) typedSize() (width, height int, refusal string) {
	width, err := ui.ParseDimension(ui.DimensionWidthName, s.widthEdit.Text())
	if err != nil {
		return 0, 0, ui.FormatError(ui.ErrorValidation, err)
	}
	height, err = ui.ParseDimension(ui.DimensionHeightName, s.heightEdit.Text())
	if err != nil {
		return 0, 0, ui.FormatError(ui.ErrorValidation, err)
	}
	return width, height, ""
}

// applyTypedSize makes the selection an accepted typed size describes: the
// instructions replace any refusal shown, and the toolbar follows the
// selection. A size the selection already has only tidies the fields' text:
// the field's focus loss commits again what Enter or Tab out of the pair
// (commitBeforeTab) has just applied, which would otherwise re-render the
// selection and report it unchanged.
func (s *windowsSession) applyTypedSize(selection display.Rect) {
	s.restoreInstructions()
	if selection == s.selection {
		s.syncDimensions()
		return
	}
	s.setSelection(selection)
	s.placeToolbar()
}

// createTypedSelection creates a Free Rectangle selection of the typed width
// and height, centered on the cursor display's work area and kept inside the
// virtual desktop (SCR-015), so the mode can be completed without a pointer.
// Leaving the fields commits the pair only as leavesFilledPair describes;
// Enter with a field empty explains what is missing. A refused size keeps
// the typed text for correcting, as there is no selection to revert to.
// Enter hands the focus to Capture, which the new selection enables, as Tab
// out of the pair does (commitBeforeTab): inside a dimension field the arrow
// keys move the caret and Enter commits the size again, so a focus left
// there would keep the arrow keys from nudging the selection and Enter from
// capturing it, as the instructions promise. A screen reader announces
// Capture as it takes the focus.
func (s *windowsSession) createTypedSelection(explicit bool) {
	if !explicit {
		// A field's focus-loss notification arrives once the focus has
		// moved, so the focus is already in its new place.
		if focus, _, _ := procGetFocus.Call(); !s.leavesFilledPair(focus) {
			return
		}
	}
	selection, refusal := s.typedFreeSelection()
	if refusal != "" {
		s.report(refusal)
		return
	}
	s.applyTypedSize(selection)
	if explicit {
		procSetFocus.Call(uintptr(s.captureButton))
	}
}

// commitBeforeTab creates the typed selection as Tab leaves the pair of
// dimension fields, before the dialog manager picks the control Tab moves to
// (SCR-015). Capture is disabled until a selection exists, and Tab skips
// disabled controls, so a selection created only by the field's focus loss
// would come once Tab had passed Capture for Cancel, where Enter, as the
// instructions suggest, cancels. A refused size is left to the field's focus
// loss to explain, so it is announced once.
func (s *windowsSession) commitBeforeTab(field uintptr) {
	if s.drag.op != dragNone {
		return
	}
	previous := uintptr(0)
	if state, _, _ := procGetKeyState.Call(vkShift); state&0x8000 != 0 {
		previous = 1
	}
	next, _, _ := procGetNextDlgTabItem.Call(uintptr(s.toolbar), field, previous)
	if !s.leavesFilledPair(next) {
		return
	}
	if selection, refusal := s.typedFreeSelection(); refusal == "" {
		s.applyTypedSize(selection)
	}
}

// leavesFilledPair reports whether moving the focus to next, from a
// dimension field of a Free Rectangle session without a selection, commits
// the typed size as Enter does. Moving from one field to the other while
// typing the pair is not a request — a selection created then would be
// resized in place, not centered again, by a correction to the pair — and
// neither is leaving the pair with a field still empty, nor switching to
// another application, which takes the focus from the toolbar's thread
// (next is 0): the toolbar gives the focus back to the field on return
// (restoreFocus), where the user goes on typing the pair.
func (s *windowsSession) leavesFilledPair(next uintptr) bool {
	return next != 0 && !s.isDimensionEdit(next) && strings.TrimSpace(s.widthEdit.Text()) != "" && strings.TrimSpace(s.heightEdit.Text()) != ""
}

// typedFreeSelection returns the Free Rectangle selection the typed width
// and height describe, as typedSelection places it, or the message that
// refuses them.
func (s *windowsSession) typedFreeSelection() (display.Rect, string) {
	width, height, refusal := s.typedSize()
	if refusal != "" {
		return display.Rect{}, refusal
	}
	selection, ok := typedSelection(s.anchor, s.bounds, width, height)
	if !ok {
		return display.Rect{}, ui.SelectionTooLarge
	}
	return selection, ""
}

// report shows a message in the toolbar in place of the instructions and
// announces it to a screen reader, even when it repeats the message shown,
// as a refused edit is news each time the user commits it.
func (s *windowsSession) report(text string) {
	s.message.SetTextQuietly(text)
	s.message.Announce()
}

// restoreInstructions shows the instructions again without announcing them:
// the session announced them when it started, and they return while the
// user types or once an edit is accepted.
func (s *windowsSession) restoreInstructions() {
	s.message.SetTextQuietly(s.instructions())
}

// syncPosition mirrors the live selection's virtual-desktop origin into the
// toolbar's X/Y readout (SCR-028). Negative coordinates on displays left of
// or above the primary display are shown as-is.
func (s *windowsSession) syncPosition() {
	if s.position == nil || s.selection.Empty() {
		return
	}
	s.position.SetText(fmt.Sprintf(ui.SelectionPositionFormat, s.selection.Left, s.selection.Top))
}

// nudge moves the selection — the preset frame, or a free rectangle — by
// whole physical pixels in one direction, clamped inside the virtual desktop
// (SCR-015, SCR-027). Nudges are complete moves, so the toolbar follows
// immediately.
func (s *windowsSession) nudge(dx, dy, step int32) {
	if s.selection.Empty() || s.drag.op != dragNone {
		return
	}
	s.setSelection(moveSelection(s.selection, dx*step, dy*step, s.bounds))
	s.placeToolbar()
}

// nudgeStep reads the Shift key state for the queued keyboard message: with
// Shift down the nudge covers ten physical pixels instead of one (SCR-027).
func nudgeStep() int32 {
	state, _, _ := procGetKeyState.Call(vkShift)
	if state&0x8000 != 0 {
		return 10
	}
	return 1
}

// pointerFromClient decodes the signed 16-bit client coordinates packed
// into a mouse message's lParam and converts them to virtual-desktop
// coordinates.
func (s *windowsSession) pointerFromClient(lParam uintptr) display.Point {
	origin := s.clientOrigin()
	return display.Point{
		X: int32(int16(lParam&0xffff)) + origin.X,
		Y: int32(int16(lParam>>16)) + origin.Y,
	}
}

// endDrag finishes the pointer interaction at the release position the
// button-up message carries: the final WM_MOUSEMOVE ahead of a release can
// be coalesced away, so the selection must catch up to where the button was
// actually released rather than stop at the last delivered move. A press
// that never moved is a click, not a drag: a draw click clears the previous
// selection instead of leaving a tiny rectangle behind from pointer jitter,
// and a handle or interior click released at the press position leaves the
// selection alone — the handle press point sits inside the grab square, off
// the selection's edge, so applying it as the drag end would silently
// resize the capture region on a plain click. A handle or interior press
// released away from the press position still ends at the release position
// even when every move was coalesced away, so a fast flick lands where the
// button came up. A finished draw repaints its band once more: the resize
// handles are skipped while drawing, so without the repaint the resting
// selection would show no resize affordance at all (SCR-012).
func (s *windowsSession) endDrag(lParam unsafe.Pointer) {
	if s.drag.op == dragNone {
		return
	}
	p := s.pointerFromClient(uintptr(lParam))
	releaseMoved := s.drag.moved || p != s.drag.originPt
	switch s.drag.op {
	case dragDraw:
		if s.drag.moved {
			s.setSelection(drawRect(s.drag.anchor, p, s.bounds))
		} else {
			s.setSelection(display.Rect{})
		}
	case dragMove:
		if releaseMoved {
			s.setSelection(moveSelection(s.drag.origin, p.X-s.drag.originPt.X, p.Y-s.drag.originPt.Y, s.bounds))
		}
	case dragResize:
		if releaseMoved {
			s.setSelection(resizeSelection(s.drag.origin, s.drag.zone, p, s.bounds))
		}
	}
	drawing := s.drag.op == dragDraw
	s.drag = dragState{}
	if drawing {
		s.revealHandles()
	}
	procReleaseCapture.Call()
	s.placeToolbar()
}

// revealHandles repaints the selection band so the resize handles the draw
// skipped appear once the draw ends. A compositing failure keeps the
// handle-less frame; the handles still respond to the pointer, so the error
// is not fatal.
func (s *windowsSession) revealHandles() {
	if s.selection.Empty() {
		return
	}
	if err := s.renderSelection(s.visualBounds()); err != nil {
		_ = err
	}
}

func overlayWindowProc(hwnd windows.Handle, message uint32, wParam uintptr, lParam unsafe.Pointer) uintptr {
	session := overlayStates[hwnd]
	if session != nil {
		switch message {
		case wmLButtonDown:
			session.onButtonDown(lParam)
			return 0
		case wmMouseMove:
			session.onMouseMove(lParam)
			return 0
		case wmLButtonUp:
			session.endDrag(lParam)
			return 0
		case wmCaptureChanged:
			// Capture can be lost mid-draw without a button release (another
			// window claiming the capture, or Close tearing down the
			// session); finish the draw the same way a release would.
			drawing := session.drag.op == dragDraw && session.drag.moved
			session.drag = dragState{}
			if drawing {
				session.revealHandles()
			}
			session.placeToolbar()
			return 0
		case wmSetCursor:
			if uint32(uintptr(lParam))&0xffff == htClient {
				procSetCursor.Call(session.zoneCursor())
				return 1
			}
		case wmDestroy:
			delete(overlayStates, hwnd)
			return 0
		}
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, uintptr(lParam))
	return r
}

// onButtonDown starts the pointer interaction the click position selects.
// Free Rectangle: a handle drag resizes, an interior drag moves the
// selection, and a press on the dimmed area starts a new selection
// (SCR-010, SCR-012). Preset Rectangle: the interior is alpha zero and its
// clicks pass to the windows beneath (SCR-022), so a press can only land on
// the border ring and always starts a move drag (SCR-023).
func (s *windowsSession) onButtonDown(lParam unsafe.Pointer) {
	p := s.pointerFromClient(uintptr(lParam))
	if s.mode == ModePresetRectangle {
		s.drag = dragState{op: dragMove, origin: s.selection, originPt: p}
		procSetCapture.Call(uintptr(s.overlay))
		return
	}
	z := hitZone(s.selection, p, s.grab)
	switch {
	case z != zoneOutside && z != zoneInterior:
		s.drag = dragState{op: dragResize, zone: z, origin: s.selection, originPt: p}
	case z == zoneInterior:
		s.drag = dragState{op: dragMove, origin: s.selection, originPt: p}
	default:
		s.drag = dragState{op: dragDraw, anchor: p}
		s.setSelection(display.Rect{})
		// The session starts in the width field (SCR-015), and the overlay
		// never takes the focus, so a draw hands it from a dimension field
		// to the toolbar: Enter then presses Capture and the arrow keys nudge
		// the drawn rectangle. The field's focus loss commits nothing
		// mid-drag.
		if focus, _, _ := procGetFocus.Call(); s.isDimensionEdit(focus) {
			procSetFocus.Call(uintptr(s.toolbar))
		}
	}
	procSetCapture.Call(uintptr(s.overlay))
}

func (s *windowsSession) onMouseMove(lParam unsafe.Pointer) {
	if s.drag.op == dragNone {
		return
	}
	p := s.pointerFromClient(uintptr(lParam))
	switch s.drag.op {
	case dragDraw:
		if !s.drag.moved && p == s.drag.anchor {
			// Windows can deliver a mouse move for a stationary pointer;
			// it must not turn a plain click into a 1 × 1 selection.
			return
		}
		s.drag.moved = true
		s.setSelection(drawRect(s.drag.anchor, p, s.bounds))
	case dragMove, dragResize:
		if !s.drag.moved && p == s.drag.originPt {
			// A stationary move on a handle or the interior must not snap
			// the selection to the press point either: the press point sits
			// inside the handle's grab square, off the selection's edge.
			return
		}
		s.drag.moved = true
		if s.drag.op == dragMove {
			s.setSelection(moveSelection(s.drag.origin, p.X-s.drag.originPt.X, p.Y-s.drag.originPt.Y, s.bounds))
		} else {
			s.setSelection(resizeSelection(s.drag.origin, s.drag.zone, p, s.bounds))
		}
	}
}

// zoneCursor picks the mouse cursor for the current interaction state and
// pointer position.
func (s *windowsSession) zoneCursor() uintptr {
	if s.mode == ModePresetRectangle {
		// The only input the preset frame receives is the border ring's
		// move drag; the size-all cursor advertises it (SCR-023).
		return cursorSizeAll
	}
	z := zoneOutside
	if s.drag.op == dragResize {
		z = s.drag.zone
	} else if s.drag.op == dragMove {
		return cursorSizeAll
	} else if s.drag.op == dragDraw {
		return cursorCross
	} else {
		var position point
		if r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&position))); r == 0 {
			return cursorCross
		}
		z = hitZone(s.selection, display.Point{X: position.X, Y: position.Y}, s.grab)
	}
	switch z {
	case zoneNW, zoneSE:
		return cursorSizeNWSE
	case zoneNE, zoneSW:
		return cursorSizeNESW
	case zoneN, zoneS:
		return cursorSizeNS
	case zoneE, zoneW:
		return cursorSizeWE
	case zoneInterior:
		return cursorSizeAll
	default:
		return cursorCross
	}
}

func toolbarWindowProc(hwnd windows.Handle, message uint32, wParam uintptr, lParam unsafe.Pointer) uintptr {
	session := toolbarStates[hwnd]
	switch message {
	case wmCommand:
		if session != nil {
			id := uint32(wParam & 0xffff)
			notification := uint32(wParam >> 16)
			switch {
			case notification == 0 && id == toolbarCaptureCommand:
				if session.onCapture != nil {
					session.onCapture()
				}
				return 0
			case notification == 0 && id == toolbarCancelCommand:
				if session.onCancel != nil {
					session.onCancel()
				}
				return 0
			case notification == enKillFocus && (id == toolbarWidthCommand || id == toolbarHeightCommand):
				session.commitDimensions(false)
				return 0
			case notification == enChange && (id == toolbarWidthCommand || id == toolbarHeightCommand):
				// Typing again after a rejected edit clears the validation
				// message back to the instructions; programmatic field
				// updates are not typing.
				if !session.syncing {
					session.restoreInstructions()
				}
				return 0
			}
		}
	case wmActivate:
		// Windows focuses the toolbar itself, not the control in it that had
		// the focus, whenever the toolbar is activated again — on Alt+Tab, or
		// as the global shortcut brings it back (Attention) — which leaves a
		// keyboard user typing a size into nothing, so the control that had
		// the focus as the toolbar was deactivated gets it back (UX-006,
		// SCR-015). A click activates the toolbar before the click reaches
		// the control under the pointer, which then takes the focus itself,
		// and a field given the focus first would commit its size again as
		// it lost it; on a click activation the focus is given back once the
		// click has been handled, and only if the click left it on the
		// toolbar itself, as dragging the toolbar by its background does.
		if session != nil {
			switch wParam & 0xffff {
			case waInactive:
				session.focus = 0
				if focused, _, _ := procGetFocus.Call(); focused != 0 {
					if child, _, _ := procIsChild.Call(uintptr(hwnd), focused); child != 0 {
						session.focus = focused
					}
				}
			case waActive:
				r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, uintptr(lParam))
				session.restoreFocus()
				return r
			case waClickActive:
				r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, uintptr(lParam))
				procPostMessageW.Call(uintptr(hwnd), wmAppRestoreFocus, 0, 0)
				return r
			}
		}
	case wmAppRestoreFocus:
		if session != nil {
			if focused, _, _ := procGetFocus.Call(); focused == uintptr(hwnd) {
				session.restoreFocus()
			}
			return 0
		}
	case wmClose:
		// Alt+F4 targets the foreground window, which is the toolbar while a
		// session is active. DefWindowProc would destroy the toolbar alone,
		// leaving the input-capturing overlay up with no keyboard way back
		// (focus leaves the application with the toolbar), so closing the
		// toolbar cancels the whole session instead (SCR-052).
		if session != nil && session.onCancel != nil {
			session.onCancel()
		}
		return 0
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
