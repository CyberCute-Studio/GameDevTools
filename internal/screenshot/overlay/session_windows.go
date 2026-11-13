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

	// overlayTintAlpha dims the desktop outside the selection to about 62%
	// brightness. The tint keeps the input-capturing surface visible at all
	// times (SCR-052) and never reaches capture output because the session
	// windows are destroyed before the rectangle is read (SCR-068).
	overlayTintAlpha = 96

	toolbarCaptureCommand = 1
	toolbarCancelCommand  = 2
	toolbarWidthCommand   = 3
	toolbarHeightCommand  = 4

	// Logical toolbar measurements, scaled by the cursor display's DPI.
	toolbarLogicalWidth  = 512
	toolbarLogicalHeight = 76
	toolbarLogicalMargin = 16
	toolbarLogicalInset  = 12

	// Logical selection visuals, scaled by the cursor display's DPI. The
	// border ring is three pixels thick so a highlight core keeps a dark
	// outline on both sides; handles extend half their size outside the
	// selection edge they sit on.
	borderLogicalThickness = 3
	handleLogicalSize      = 8
	handleLogicalGrabExtra = 8

	wmDestroy        = 0x0002
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

	enKillFocus = 0x0200
	enChange    = 0x0300

	vkEscape = 0x1b
	vkReturn = 0x0d

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
	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010

	ulwAlpha   = 0x00000002
	acSrcAlpha = 0x01

	dibRGBColors = 0

	colorWindow     = 5
	colorWindowText = 8
	colorHighlight  = 13
	transparent     = 1

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
	procIsDialogMessageW            = user32.NewProc("IsDialogMessageW")
	procLoadCursorW                 = user32.NewProc("LoadCursorW")
	procSetCursor                   = user32.NewProc("SetCursor")
	procSetCapture                  = user32.NewProc("SetCapture")
	procReleaseCapture              = user32.NewProc("ReleaseCapture")
	procSetFocus                    = user32.NewProc("SetFocus")
	procGetFocus                    = user32.NewProc("GetFocus")
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
// for handle drags, the anchor pointer position for draw and move drags,
// and the selection rectangle as it was when the drag started.
type dragState struct {
	op       dragOp
	zone     zone
	anchor   display.Point
	origin   display.Rect
	originPt display.Point
	moved    bool
}

// windowsSession owns the two topmost windows of one active session — the
// dimmed virtual-desktop overlay and the floating toolbar — the overlay's
// backing bitmap, and the live selection state.
type windowsSession struct {
	mode          Mode
	bounds        display.Rect
	overlay       windows.Handle
	toolbar       windows.Handle
	message       *ui.Label
	widthEdit     *ui.Control
	heightEdit    *ui.Control
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

	// Toolbar placement inputs captured at creation.
	anchor      display.Rect
	toolbarSize sizeStruct
	margin      int32

	// Overlay rendering surface: a top-down 32-bit BGRA DIB covering the
	// whole virtual desktop, composited per-pixel through
	// UpdateLayeredWindowIndirect so the selection interior stays fully
	// transparent while the rest of the desktop stays dimmed.
	memDC       uintptr
	dib         uintptr
	oldBitmap   uintptr
	pixels      unsafe.Pointer
	pixelWidth  int32
	pixelHeight int32

	border     int32
	handleSize int32
	grab       int32
	borderPx   uint32
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

// createNative shows the session's windows: a per-pixel-alpha overlay
// covering the whole virtual desktop and a floating toolbar anchored to the
// cursor display. The overlay never activates, so it cannot rise above the
// toolbar or steal keyboard focus; the toolbar takes the foreground so
// Escape, Tab, and Enter work while the main window is hidden.
func createNative(request CreateRequest) (NativeSession, error) {
	if err := registerOverlayClasses(); err != nil {
		return nil, err
	}
	session := &windowsSession{
		mode:        request.Mode,
		bounds:      request.Bounds,
		onSelection: request.OnSelection,
		onCapture:   request.OnCapture,
		onCancel:    request.OnCancel,
	}
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
	if err := session.createSurface(); err != nil {
		session.Close()
		return nil, err
	}
	if err := session.createToolbar(request); err != nil {
		session.Close()
		return nil, err
	}
	procShowWindow.Call(overlay, swShowNoActivate)
	if err := session.renderAll(); err != nil {
		session.Close()
		return nil, err
	}
	procShowWindow.Call(uintptr(session.toolbar), swShow)
	// Best effort: if another window owns the foreground the toolbar stays
	// usable by mouse, and the shell's message filter still routes Escape.
	procSetForegroundWindow.Call(uintptr(session.toolbar))
	return session, nil
}

// createSurface allocates the overlay's desktop-sized backing bitmap. The
// whole desktop is dimmed once here; later selection updates repaint and
// composite only the affected band.
func (s *windowsSession) createSurface() error {
	width, height := s.bounds.Width(), s.bounds.Height()
	memory, _, err := procCreateCompatibleDC.Call(0)
	if memory == 0 {
		return callError("CreateCompatibleDC", err)
	}
	s.memDC = memory
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
		return callError("CreateDIBSection", err)
	}
	s.dib = bitmap
	s.pixels = pixels
	s.pixelWidth = width
	s.pixelHeight = height
	previous, _, err := procSelectObject.Call(memory, bitmap)
	if previous == 0 {
		return callError("SelectObject", err)
	}
	s.oldBitmap = previous
	return nil
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
	s.anchor = anchor
	s.toolbarSize = sizeStruct{Width: width, Height: height}
	s.margin = scale(toolbarLogicalMargin)
	s.border = scale(borderLogicalThickness)
	s.handleSize = scale(handleLogicalSize)
	s.grab = s.handleSize + scale(handleLogicalGrabExtra)
	s.borderPx = highlightPixel()
	x, y := toolbarPlacement(request.Bounds, anchor, display.Rect{}, width, height, s.margin)
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

	instructions := ui.OverlayInstructions
	if request.Mode != ModeFreeRectangle {
		instructions = ui.OverlayPresetPending
	}
	message, err := ui.NewLabel(s.toolbar, instructions)
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
		for _, control := range []*ui.Label{message, widthLabel, heightLabel} {
			control.SetFont(font)
		}
		widthEdit.SetFont(font)
		heightEdit.SetFont(font)
		captureButton.SetFont(font)
		cancelButton.SetFont(font)
	}
	widthEdit.Enable(false)
	heightEdit.Enable(false)
	s.widthEdit = widthEdit
	s.heightEdit = heightEdit

	inset := scale(toolbarLogicalInset)
	message.Move(inset, scale(6), width-2*inset, scale(20))
	rowY := scale(34)
	labelWidth, editWidth := scale(48), scale(64)
	widthLabel.Move(inset, rowY+scale(3), labelWidth, scale(20))
	widthEdit.Move(inset+labelWidth, rowY, editWidth, scale(24))
	heightX := inset + labelWidth + editWidth + inset
	heightLabel.Move(heightX, rowY+scale(3), labelWidth, scale(20))
	heightEdit.Move(heightX+labelWidth, rowY, editWidth, scale(24))
	buttonWidth, buttonHeight := scale(88), scale(28)
	cancelButton.Move(width-inset-buttonWidth, rowY-scale(2), buttonWidth, buttonHeight)
	captureButton.Move(width-inset-2*buttonWidth-scale(8), rowY-scale(2), buttonWidth, buttonHeight)
	s.captureButton = captureButton.Handle()
	return nil
}

// highlightPixel returns the system highlight color as an opaque
// premultiplied BGRA pixel.
func highlightPixel() uint32 {
	c, _, _ := procGetSysColor.Call(colorHighlight)
	r := c & 0xff
	g := (c >> 8) & 0xff
	b := (c >> 16) & 0xff
	return 0xff000000 | uint32(r)<<16 | uint32(g)<<8 | uint32(b)
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

func (s *windowsSession) isDimensionEdit(hwnd uintptr) bool {
	return s.widthEdit != nil && hwnd == uintptr(s.widthEdit.Handle()) ||
		s.heightEdit != nil && hwnd == uintptr(s.heightEdit.Handle())
}

// FilterMessage pre-handles the GUI thread's queued messages while a session
// is active. Escape cancels from any session window, Enter inside a
// dimension field commits the typed value instead of triggering the default
// Capture button, and toolbar messages go through IsDialogMessage so Tab,
// arrow keys, and Enter work between the toolbar controls even though the
// toolbar is a popup rather than a dialog. Messages for other windows pass
// through untouched.
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
		native.commitDimensions()
		return true
	}
	if native.isToolbarMessage(hwnd) {
		handled, _, _ := procIsDialogMessageW.Call(uintptr(native.toolbar), uintptr(message))
		return handled != 0
	}
	return false
}

// setSelection records a new live selection, repaints the affected band of
// the overlay, keeps the toolbar's dimension fields in sync, and reports
// the change to the controller. The toolbar is not repositioned here — that
// happens when a drag or edit finishes, so it does not jump around
// mid-drag.
func (s *windowsSession) setSelection(selection display.Rect) {
	oldVisual := s.visualBounds()
	s.selection = selection
	if err := s.renderSelection(oldVisual); err != nil {
		// A compositing failure leaves the previous frame visible; the
		// interaction stays usable, so the error is not fatal.
		_ = err
	}
	// Typed dimensions (SCR-013) are only meaningful with a selection on
	// screen; the fields stay disabled until one exists so they cannot be
	// focused or committed into an empty selection. The overlay never takes
	// activation, so clearing the selection can disable the field that still
	// holds keyboard focus; move focus to the toolbar first so Escape keeps
	// cancelling the session.
	if s.widthEdit != nil && s.heightEdit != nil {
		enabled := !selection.Empty()
		if !enabled {
			focus, _, _ := procGetFocus.Call()
			if s.isDimensionEdit(focus) {
				procSetFocus.Call(uintptr(s.toolbar))
			}
		}
		s.widthEdit.Enable(enabled)
		s.heightEdit.Enable(enabled)
	}
	s.syncDimensions()
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

// toLocal converts virtual-desktop coordinates into overlay client
// coordinates, which are also the backing bitmap's pixel coordinates.
func (s *windowsSession) toLocal(r display.Rect) display.Rect {
	return display.Rect{
		Left:   r.Left - s.bounds.Left,
		Top:    r.Top - s.bounds.Top,
		Right:  r.Right - s.bounds.Left,
		Bottom: r.Bottom - s.bounds.Top,
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

// dimPixel is the premultiplied black tint at overlayTintAlpha; zero is a
// fully transparent pixel.
const dimPixel = uint32(overlayTintAlpha) << 24

// renderAll paints the initial fully dimmed frame and composites it.
func (s *windowsSession) renderAll() error {
	s.fillLocal(display.Rect{Right: s.pixelWidth, Bottom: s.pixelHeight}, dimPixel)
	return s.present(display.Rect{Right: s.pixelWidth, Bottom: s.pixelHeight})
}

// renderSelection repaints the band where the selection visuals changed:
// the union of the old and new visual bounds is re-dimmed, the new
// selection interior is cleared to transparent, and its border and handles
// are drawn on top. The border ring sits outside the selection rectangle,
// so even an incompletely flushed frame could never put selection visuals
// inside the captured pixels (SCR-068).
func (s *windowsSession) renderSelection(oldVisual display.Rect) error {
	dirty := rectUnion(oldVisual, s.visualBounds())
	if dirty.Empty() {
		return nil
	}
	local := s.toLocal(dirty)
	s.fillLocal(local, dimPixel)
	if !s.selection.Empty() {
		sel := s.toLocal(s.selection)
		s.fillLocal(sel, 0)
		s.fillRing(inflateRect(sel, s.border), sel, s.borderPx)
		s.fillRing(inflateRect(sel, s.border), inflateRect(sel, s.border-1), 0xff000000)
		s.fillRing(inflateRect(sel, 1), sel, 0xff000000)
		if s.drag.op != dragDraw {
			s.drawHandles(sel)
		}
	}
	return s.present(local)
}

// drawHandles paints the eight resize handles as white squares with a dark
// outline, centered on the selection's corners and edge midpoints.
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
		s.fillLocal(square, 0xff000000)
		s.fillLocal(inflateRect(square, -1), 0xffffffff)
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
	position := point{X: s.bounds.Left, Y: s.bounds.Top}
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
// fields (SCR-011), unless the user is typing in the field.
func (s *windowsSession) syncDimensions() {
	if s.widthEdit == nil || s.heightEdit == nil {
		return
	}
	focus, _, _ := procGetFocus.Call()
	width, height := "", ""
	if !s.selection.Empty() {
		width = fmt.Sprintf("%d", s.selection.Width())
		height = fmt.Sprintf("%d", s.selection.Height())
	}
	s.syncing = true
	defer func() { s.syncing = false }()
	if focus != uintptr(s.widthEdit.Handle()) {
		s.widthEdit.SetText(width)
	}
	if focus != uintptr(s.heightEdit.Handle()) {
		s.heightEdit.SetText(height)
	}
}

// commitDimensions applies the toolbar's typed width and height to the
// selection (SCR-013). Invalid or non-fitting values are rejected with a
// validation message and the fields revert to the actual dimensions, so a
// mistyped value can never resize the capture silently.
func (s *windowsSession) commitDimensions() {
	if s.selection.Empty() {
		return
	}
	width, err := ui.ParseDimension("Width", s.widthEdit.Text())
	if err != nil {
		s.message.SetText(ui.FormatError(ui.ErrorValidation, err))
		s.syncDimensionsForce()
		return
	}
	height, err := ui.ParseDimension("Height", s.heightEdit.Text())
	if err != nil {
		s.message.SetText(ui.FormatError(ui.ErrorValidation, err))
		s.syncDimensionsForce()
		return
	}
	resized, ok := sizedSelection(s.selection, width, height, s.bounds)
	if !ok {
		s.message.SetText(ui.SelectionFitInvalid)
		s.syncDimensionsForce()
		return
	}
	s.message.SetText(ui.OverlayInstructions)
	s.setSelection(resized)
	s.placeToolbar()
}

// syncDimensionsForce reverts the dimension fields to the actual selection
// after a rejected edit, even in the field that has focus.
func (s *windowsSession) syncDimensionsForce() {
	if s.selection.Empty() {
		return
	}
	s.syncing = true
	defer func() { s.syncing = false }()
	s.widthEdit.SetText(fmt.Sprintf("%d", s.selection.Width()))
	s.heightEdit.SetText(fmt.Sprintf("%d", s.selection.Height()))
}

// pointerFromClient decodes the signed 16-bit client coordinates packed
// into a mouse message's lParam and converts them to virtual-desktop
// coordinates.
func (s *windowsSession) pointerFromClient(lParam uintptr) display.Point {
	return display.Point{
		X: int32(int16(lParam&0xffff)) + s.bounds.Left,
		Y: int32(int16(lParam>>16)) + s.bounds.Top,
	}
}

// endDrag finishes the pointer interaction. A press that never moved clears
// the previous selection instead of leaving a 1 × 1 rectangle behind from
// an accidental click.
func (s *windowsSession) endDrag() {
	if s.drag.op == dragNone {
		return
	}
	if s.drag.op == dragDraw && !s.drag.moved {
		s.setSelection(display.Rect{})
	}
	s.drag = dragState{}
	procReleaseCapture.Call()
	s.placeToolbar()
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
			session.endDrag()
			return 0
		case wmCaptureChanged:
			session.drag = dragState{}
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

// onButtonDown starts the pointer interaction the click position selects
// (SCR-010, SCR-012): a handle drag resizes, an interior drag moves the
// selection, and a press on the dimmed area starts a new selection. Preset
// Rectangle sessions do not draw yet; their exact frame arrives with the
// Preset Rectangle item.
func (s *windowsSession) onButtonDown(lParam unsafe.Pointer) {
	if s.mode != ModeFreeRectangle {
		return
	}
	p := s.pointerFromClient(uintptr(lParam))
	z := hitZone(s.selection, p, s.grab)
	switch {
	case z != zoneOutside && z != zoneInterior:
		s.drag = dragState{op: dragResize, zone: z, origin: s.selection}
	case z == zoneInterior:
		s.drag = dragState{op: dragMove, origin: s.selection, originPt: p}
	default:
		s.drag = dragState{op: dragDraw, anchor: p}
		s.setSelection(display.Rect{})
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
	case dragMove:
		s.setSelection(moveSelection(s.drag.origin, p.X-s.drag.originPt.X, p.Y-s.drag.originPt.Y, s.bounds))
	case dragResize:
		s.setSelection(resizeSelection(s.drag.origin, s.drag.zone, p, s.bounds))
	}
}

// zoneCursor picks the mouse cursor for the current interaction state and
// pointer position.
func (s *windowsSession) zoneCursor() uintptr {
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
				session.commitDimensions()
				return 0
			case notification == enChange && (id == toolbarWidthCommand || id == toolbarHeightCommand):
				// Typing again after a rejected edit clears the validation
				// message back to the instructions; programmatic field
				// updates are not typing.
				if !session.syncing && session.mode == ModeFreeRectangle {
					session.message.SetText(ui.OverlayInstructions)
				}
				return 0
			}
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
