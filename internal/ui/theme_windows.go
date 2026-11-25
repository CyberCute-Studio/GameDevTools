//go:build windows

package ui

import (
	"math"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The theme draws the standard controls the package creates in the
// application's palette while they stay standard controls: their keyboard
// behavior, their accessibility, and the messages that drive them are
// Windows' own. It paints through the means Windows documents for that —
// the colors a parent gives a control (WM_CTLCOLOR*), the Common Controls'
// custom draw for buttons (NM_CUSTOMDRAW), owner-drawn list items
// (WM_DRAWITEM), and a field's own frame (WM_NCPAINT) — and leaves each
// control's behavior to its window procedure. Under a Windows contrast theme
// the palette holds the system colors, and the buttons draw themselves.

const (
	wmEnable          = 0x000a
	wmPaint           = 0x000f
	wmEraseBkgnd      = 0x0014
	wmSysColorChange  = 0x0015
	wmSettingChange   = 0x001a
	wmSetFocus        = 0x0007
	wmKillFocus       = 0x0008
	wmGetFont         = 0x0031
	wmDrawItem        = 0x002b
	wmMeasureItem     = 0x002c
	wmNotify          = 0x004e
	wmStyleChanged    = 0x007d
	wmNCDestroy       = 0x0082
	wmNCCalcSize      = 0x0083
	wmNCHitTest       = 0x0084
	wmNCPaint         = 0x0085
	wmQueryUIState    = 0x0129
	wmCtlColorEdit    = 0x0133
	wmCtlColorListBox = 0x0134
	wmCtlColorBtn     = 0x0135
	wmCtlColorStatic  = 0x0138
	wmMouseMove       = 0x0200
	wmMouseLeave      = 0x02a3
	wmThemeChanged    = 0x031a

	htClient = 1

	nmCustomDraw    = ^uint32(11) // NM_CUSTOMDRAW, -12
	cddsPrePaint    = 0x00000001
	cdrfDoDefault   = 0x00000000
	cdrfSkipDefault = 0x00000004
	cdisSelected    = 0x0001
	cdisDisabled    = 0x0004
	cdisFocus       = 0x0010
	cdisHot         = 0x0040

	uisfHideFocus = 0x1
	uisfHideAccel = 0x2

	odtListBox      = 2
	odtComboBox     = 3
	odsSelected     = 0x0001
	odsDisabled     = 0x0004
	odsFocus        = 0x0010
	odsNoFocusRect  = 0x0200
	odsComboBoxEdit = 0x1000

	bmGetCheck = 0x00f0
	bstChecked = 1

	cbGetDroppedState = 0x0157
	cbSetItemHeight   = 0x0153
	cbGetComboBoxInfo = 0x0164
	lbSetItemHeight   = 0x01a0

	lbsOwnerDrawFixed = 0x0010
	lbsHasStrings     = 0x0040
	cbsOwnerDrawFixed = 0x0010
	cbsHasStrings     = 0x0200

	dtCenter      = 0x00000001
	dtVCenter     = 0x00000004
	dtSingleLine  = 0x00000020
	dtCalcRect    = 0x00000400
	dtNoPrefix    = 0x00000800
	dtEndEllipsis = 0x00008000
	dtHidePrefix  = 0x00100000

	themeTransparent = 1
	themeOpaque      = 2
	srcCopy          = 0x00cc0020
	dibRGBColors     = 0

	swpNoSize        = 0x0001
	swpNoMove        = 0x0002
	swpNoZOrder      = 0x0004
	swpNoActivate    = 0x0010
	swpFrameChanged  = 0x0020
	rdwInvalidate    = 0x0001
	rdwErase         = 0x0004
	rdwAllChildren   = 0x0080
	rdwFrame         = 0x0400
	tmeLeave         = 0x00000002
	gwlpWndProc      = ^uintptr(3) // GWLP_WNDPROC, -4
	spiGetHighContr  = 0x0042
	hcfHighContrast  = 0x00000001
	fwSemiBoldWeight = 600

	dwmwaUseImmersiveDarkMode = 20
	dwmwaCornerPreference     = 33
	dwmwaBorderColor          = 34
	dwmwaCaptionColor         = 35
	dwmwaColorDefault         = 0xffffffff
	dwmwcpRound               = 2

	colorWindowIndex        = 5
	colorWindowFrameIndex   = 6
	colorWindowTextIndex    = 8
	colorHighlightIndex     = 13
	colorHighlightTextIndex = 14
	colorBtnFaceIndex       = 15
	colorGrayTextIndex      = 17
	colorBtnTextIndex       = 18
)

var (
	themeGDI32   = windows.NewLazySystemDLL("gdi32.dll")
	themeUXTheme = windows.NewLazySystemDLL("uxtheme.dll")
	themeDWM     = windows.NewLazySystemDLL("dwmapi.dll")

	procThemeBeginPaint            = user32.NewProc("BeginPaint")
	procThemeEndPaint              = user32.NewProc("EndPaint")
	procThemeGetDC                 = user32.NewProc("GetDC")
	procThemeGetWindowDC           = user32.NewProc("GetWindowDC")
	procThemeReleaseDC             = user32.NewProc("ReleaseDC")
	procThemeGetClientRect         = user32.NewProc("GetClientRect")
	procThemeGetWindowRect         = user32.NewProc("GetWindowRect")
	procThemeMapWindowPoints       = user32.NewProc("MapWindowPoints")
	procThemeGetParent             = user32.NewProc("GetParent")
	procThemeRedrawWindow          = user32.NewProc("RedrawWindow")
	procThemeSetWindowPos          = user32.NewProc("SetWindowPos")
	procThemeSetWindowLongPtrW     = user32.NewProc("SetWindowLongPtrW")
	procThemeCallWindowProcW       = user32.NewProc("CallWindowProcW")
	procThemeDefWindowProcW        = user32.NewProc("DefWindowProcW")
	procThemeTrackMouseEvent       = user32.NewProc("TrackMouseEvent")
	procThemeSystemParametersInfoW = user32.NewProc("SystemParametersInfoW")
	procThemeGetDpiForWindow       = user32.NewProc("GetDpiForWindow")
	procThemeFillRect              = user32.NewProc("FillRect")
	procThemeDrawTextW             = user32.NewProc("DrawTextW")
	procThemeDrawIconEx            = user32.NewProc("DrawIconEx")
	procThemeGetSysColor           = user32.NewProc("GetSysColor")
	procThemeIsWindowEnabled       = user32.NewProc("IsWindowEnabled")
	procThemeIsWindowVisible       = user32.NewProc("IsWindowVisible")
	procThemeIsChild               = user32.NewProc("IsChild")

	procThemeCreateCompatibleDC  = themeGDI32.NewProc("CreateCompatibleDC")
	procThemeCreateDIBSection    = themeGDI32.NewProc("CreateDIBSection")
	procThemeDeleteDC            = themeGDI32.NewProc("DeleteDC")
	procThemeSelectObject        = themeGDI32.NewProc("SelectObject")
	procThemeDeleteObject        = themeGDI32.NewProc("DeleteObject")
	procThemeBitBlt              = themeGDI32.NewProc("BitBlt")
	procThemeSetBkMode           = themeGDI32.NewProc("SetBkMode")
	procThemeSetBkColor          = themeGDI32.NewProc("SetBkColor")
	procThemeSetTextColor        = themeGDI32.NewProc("SetTextColor")
	procThemeCreateSolidBrush    = themeGDI32.NewProc("CreateSolidBrush")
	procThemeGetTextMetricsW     = themeGDI32.NewProc("GetTextMetricsW")
	procThemeGetTextFaceW        = themeGDI32.NewProc("GetTextFaceW")
	procThemeCreateFontIndirectW = themeGDI32.NewProc("CreateFontIndirectW")
	procThemeExcludeClipRect     = themeGDI32.NewProc("ExcludeClipRect")

	procSetWindowTheme        = themeUXTheme.NewProc("SetWindowTheme")
	procDwmSetWindowAttribute = themeDWM.NewProc("DwmSetWindowAttribute")
)

type themeRect struct {
	Left, Top, Right, Bottom int32
}

func (r themeRect) width() int32  { return r.Right - r.Left }
func (r themeRect) height() int32 { return r.Bottom - r.Top }

type themePaintStruct struct {
	hdc       uintptr
	erase     int32
	paint     themeRect
	restore   int32
	incUpdate int32
	reserved  [32]byte
}

type themeNMHdr struct {
	hwndFrom uintptr
	idFrom   uintptr
	code     uint32
}

type themeNMCustomDraw struct {
	hdr       themeNMHdr
	drawStage uint32
	hdc       uintptr
	rc        themeRect
	itemSpec  uintptr
	itemState uint32
	itemParam uintptr
}

type themeDrawItem struct {
	ctlType    uint32
	ctlID      uint32
	itemID     uint32
	itemAction uint32
	itemState  uint32
	hwndItem   uintptr
	hdc        uintptr
	rcItem     themeRect
	itemData   uintptr
}

type themeMeasureItem struct {
	ctlType    uint32
	ctlID      uint32
	itemID     uint32
	itemWidth  uint32
	itemHeight uint32
	itemData   uintptr
}

type themeBitmapInfo struct {
	size          uint32
	width         int32
	height        int32
	planes        uint16
	bitCount      uint16
	compression   uint32
	sizeImage     uint32
	xPelsPerMeter int32
	yPelsPerMeter int32
	clrUsed       uint32
	clrImportant  uint32
	colors        [1]uint32
}

type themeTextMetric struct {
	height   int32
	rest     [10]int32
	chars    [4]uint16
	bytes    [5]uint8
	reserved [3]uint8
}

type themeHighContrast struct {
	size   uint32
	flags  uint32
	scheme uintptr
}

type themeTrackMouse struct {
	size      uint32
	flags     uint32
	track     uintptr
	hoverTime uint32
}

type themeLogFont struct {
	Height, Width, Escapement, Orientation, Weight int32

	Italic, Underline, StrikeOut, CharSet, OutPrecision, ClipPrecision, Quality, PitchAndFamily uint8

	Face [32]uint16
}

// themeComboBoxInfo is COMBOBOXINFO, which ends at its list's handle:
// CB_GETCOMBOBOXINFO refuses a size other than the structure's own.
type themeComboBoxInfo struct {
	size      uint32
	item      themeRect
	button    themeRect
	state     uint32
	hwndCombo uintptr
	hwndItem  uintptr
	hwndList  uintptr
}

// controlRole is what a themed control is drawn as.
type controlRole uint8

const (
	roleLabel controlRole = iota + 1
	rolePushButton
	roleRadioButton
	roleNavigation
	roleEdit
	roleReadOnly
	roleComboBox
	roleListBox
)

// themedControl is what the theme keeps of a control it draws.
type themedControl struct {
	role    controlRole
	style   TextStyle
	primary bool
	icon    rune
	// previous is the window procedure the theme's subclass calls on, or 0
	// for a control the theme does not subclass.
	previous uintptr
	// hot is whether the pointer is over a drop-down list.
	hot bool
}

// Surface is a background part of a window is painted in.
type Surface uint8

const (
	// SurfaceWindow is the content background, every window's own.
	SurfaceWindow Surface = iota
	// SurfaceSidebar is the navigation's background, set off from the
	// content by a divider along its right edge.
	SurfaceSidebar
)

// SurfaceRegion is a rectangle of a window's client area, in client
// coordinates, painted in another surface than the window's own. It may run
// past the client area, as the main window's sidebar does to span the
// window's height whatever that becomes; only the part inside is painted.
type SurfaceRegion struct {
	Left, Top, Right, Bottom int32
	Surface                  Surface
}

func (r SurfaceRegion) contains(x, y int32) bool {
	return x >= r.Left && x < r.Right && y >= r.Top && y < r.Bottom
}

var (
	// themeMutex guards the theme's state, which the GUI thread uses; tests
	// may create windows on more than one thread.
	themeMutex     sync.Mutex
	themedControls = map[windows.Handle]*themedControl{}
	themedWindows  = map[windows.Handle]themedWindow{}
	windowSurfaces = map[windows.Handle][]SurfaceRegion{}
	themeBrushes   = map[Color]uintptr{}
	iconFonts      = map[int32]windows.Handle{}
	activePalette  Palette
	paletteLoaded  bool
	// paletteGeneration counts the changes of the palette, so a window can
	// tell whether it was drawn in the palette in use.
	paletteGeneration uint64
	themeSubclassCB   = sync.OnceValue(func() uintptr { return windows.NewCallback(themedControlProc) })

	// highContrastActive reports whether a Windows contrast theme is on;
	// tests replace it.
	highContrastActive = func() bool {
		info := themeHighContrast{size: uint32(unsafe.Sizeof(themeHighContrast{}))}
		r, _, _ := procThemeSystemParametersInfoW.Call(spiGetHighContr, uintptr(info.size), uintptr(unsafe.Pointer(&info)), 0)
		return r != 0 && info.flags&hcfHighContrast != 0
	}
)

// CurrentPalette returns the palette windows are drawn in now: the dark
// palette, or under a Windows contrast theme the theme's system colors.
func CurrentPalette() Palette {
	themeMutex.Lock()
	defer themeMutex.Unlock()
	return paletteLocked()
}

func paletteLocked() Palette {
	if !paletteLoaded {
		activePalette = choosePalette()
		paletteLoaded = true
	}
	return activePalette
}

func choosePalette() Palette {
	if highContrastActive() {
		return systemPalette()
	}
	return DarkPalette
}

// systemPalette maps the palette onto the system colors of the contrast
// theme in use, so every surface, text, and outline is one the user chose.
// A contrast theme has no secondary text or severity colors: those draw in
// the text color, and a notice's severity stays in its words.
func systemPalette() Palette {
	color := func(index uintptr) Color {
		value, _, _ := procThemeGetSysColor.Call(index)
		return colorFromRef(value)
	}
	window, text := color(colorWindowIndex), color(colorWindowTextIndex)
	face, buttonText := color(colorBtnFaceIndex), color(colorBtnTextIndex)
	highlight, highlightText := color(colorHighlightIndex), color(colorHighlightTextIndex)
	gray := color(colorGrayTextIndex)
	return Palette{
		Window: window, Sidebar: window, Divider: color(colorWindowFrameIndex),
		Field: window, FieldBorder: text,
		Control: face, ControlHover: face, ControlPressed: face, ControlBorder: buttonText,
		Selected: highlight, SelectedText: highlightText, Hover: window,
		Text: text, TextMuted: text, TextDisabled: gray,
		Accent: highlight, AccentHover: highlight, AccentPressed: highlight, AccentText: highlightText,
		AccentDisabled: face,
		Focus:          text,
		Success:        text, Warning: text, Error: text,
		HighContrast: true,
	}
}

// refreshPalette chooses the palette again, as after Windows reports a change
// of the system colors or the contrast theme, and returns its generation,
// which changes whenever the palette does.
func refreshPalette() uint64 {
	themeMutex.Lock()
	defer themeMutex.Unlock()
	previous, loaded := activePalette, paletteLoaded
	activePalette = choosePalette()
	paletteLoaded = true
	if loaded && activePalette != previous {
		paletteGeneration++
		for color, brush := range themeBrushes {
			procThemeDeleteObject.Call(brush)
			delete(themeBrushes, color)
		}
	}
	return paletteGeneration
}

// refreshWindow redraws a themed top-level window, frame and controls, when
// the palette changed since it was last drawn. Windows reports a change of
// the system colors or the contrast theme to every top-level window, each on
// the thread that owns it, so each window redraws itself, and no thread
// waits on a window another thread owns.
func refreshWindow(hwnd windows.Handle) {
	generation := refreshPalette()
	themeMutex.Lock()
	window, ok := themedWindows[hwnd]
	stale := ok && window.generation != generation
	if stale {
		window.generation = generation
		themedWindows[hwnd] = window
	}
	themeMutex.Unlock()
	if stale {
		applyWindowFrame(hwnd, window.kind)
		refreshListScrollBars(hwnd)
		procThemeRedrawWindow.Call(uintptr(hwnd), 0, 0, rdwInvalidate|rdwErase|rdwFrame|rdwAllChildren)
	}
}

// brush returns a solid brush of the color, kept for as long as the palette
// holds it, as a WM_CTLCOLOR* answer must outlive the message.
func brush(color Color) uintptr {
	themeMutex.Lock()
	defer themeMutex.Unlock()
	if existing, ok := themeBrushes[color]; ok {
		return existing
	}
	created, _, _ := procThemeCreateSolidBrush.Call(color.colorRef())
	if created != 0 {
		themeBrushes[color] = created
	}
	return created
}

func (p Palette) surfaceColor(surface Surface) Color {
	if surface == SurfaceSidebar {
		return p.Sidebar
	}
	return p.Window
}

// WindowKind is how a top-level window's frame is drawn.
type WindowKind uint8

// themedWindow is what the theme keeps of a top-level window: how its frame
// is drawn, and the palette generation it was last drawn in.
type themedWindow struct {
	kind       WindowKind
	generation uint64
}

const (
	// WindowMain has a title bar, drawn dark with the sidebar's color.
	WindowMain WindowKind = iota
	// WindowPopup is a floating surface without a title bar, such as the
	// capture toolbar or the result card, given rounded corners and an
	// outline.
	WindowPopup
)

// ThemeWindow draws a top-level window's frame in the theme and keeps it
// there when the theme changes. Its window procedure must pass its messages
// to ThemeMessage first. The frame's colors and corners are DWM attributes
// Windows 11 documents; a Windows build without one keeps its own frame.
func ThemeWindow(hwnd windows.Handle, kind WindowKind) {
	themeMutex.Lock()
	paletteLocked()
	themedWindows[hwnd] = themedWindow{kind: kind, generation: paletteGeneration}
	themeMutex.Unlock()
	applyWindowFrame(hwnd, kind)
}

func applyWindowFrame(hwnd windows.Handle, kind WindowKind) {
	p := CurrentPalette()
	setAttribute := func(attribute uintptr, value uint32) {
		procDwmSetWindowAttribute.Call(uintptr(hwnd), attribute, uintptr(unsafe.Pointer(&value)), 4)
	}
	dark := uint32(1)
	caption, border := uint32(p.Sidebar.colorRef()), uint32(dwmwaColorDefault)
	if kind == WindowPopup {
		border = uint32(p.ControlBorder.colorRef())
	}
	if p.HighContrast {
		dark, caption, border = 0, dwmwaColorDefault, dwmwaColorDefault
	}
	setAttribute(dwmwaUseImmersiveDarkMode, dark)
	setAttribute(dwmwaCaptionColor, caption)
	setAttribute(dwmwaBorderColor, border)
	if kind == WindowPopup {
		setAttribute(dwmwaCornerPreference, dwmwcpRound)
	}
	setScrollBarTheme(hwnd)
}

// setScrollBarTheme draws the window's scroll bars dark. The dark scroll bar
// is the Explorer's "DarkMode_Explorer" theme class, which Windows does not
// document; SetWindowTheme falls back to the standard theme for a class a
// Windows build lacks, which leaves the scroll bar light and working.
func setScrollBarTheme(hwnd windows.Handle) {
	name := utf16("DarkMode_Explorer")
	if CurrentPalette().HighContrast {
		name = utf16("Explorer")
	}
	procSetWindowTheme.Call(uintptr(hwnd), uintptr(unsafe.Pointer(name)), 0)
}

// SetSurfaces records the regions of the window's client area painted in
// another surface than its own, replacing those recorded before, and
// repaints the window's background. The controls in a region are drawn on
// its surface.
func SetSurfaces(hwnd windows.Handle, regions []SurfaceRegion) {
	themeMutex.Lock()
	previous := windowSurfaces[hwnd]
	same := len(previous) == len(regions)
	for i := 0; same && i < len(regions); i++ {
		same = previous[i] == regions[i]
	}
	windowSurfaces[hwnd] = append([]SurfaceRegion(nil), regions...)
	themeMutex.Unlock()
	// A hidden window is drawn whole once it is shown, so it is not asked to
	// draw again now.
	if visible, _, _ := procThemeIsWindowVisible.Call(uintptr(hwnd)); !same && visible != 0 {
		procInvalidateRect.Call(uintptr(hwnd), 0, 1)
	}
}

// surfaceAt returns the surface of the window at a point of its client
// area.
func surfaceAt(hwnd windows.Handle, x, y int32) Surface {
	themeMutex.Lock()
	defer themeMutex.Unlock()
	for _, region := range windowSurfaces[hwnd] {
		if region.contains(x, y) {
			return region.Surface
		}
	}
	return SurfaceWindow
}

// backgroundBehind returns the color of the surface a control sits on: its
// parent's at the control's top left corner, or, for a control partly
// scrolled out of the parent's client area, at the corner of the part that
// shows, which is the only part drawn.
func backgroundBehind(control windows.Handle) Color {
	p := CurrentPalette()
	parent, _, _ := procThemeGetParent.Call(uintptr(control))
	if parent == 0 {
		return p.Window
	}
	var bounds, client themeRect
	if r, _, _ := procThemeGetWindowRect.Call(uintptr(control), uintptr(unsafe.Pointer(&bounds))); r == 0 {
		return p.Window
	}
	procThemeMapWindowPoints.Call(0, parent, uintptr(unsafe.Pointer(&bounds)), 2)
	procThemeGetClientRect.Call(parent, uintptr(unsafe.Pointer(&client)))
	x := max(min(bounds.Left, client.Right-1), 0)
	y := max(min(bounds.Top, client.Bottom-1), 0)
	return p.surfaceColor(surfaceAt(windows.Handle(parent), x, y))
}

// themeControl records how a control the package created is drawn and
// subclasses it: the fields, drop-down lists, and list boxes to paint their
// frame or face, and every control to forget it once it is destroyed, before
// Windows can give its handle to another window.
func themeControl(hwnd windows.Handle, control *themedControl) {
	themeMutex.Lock()
	themedControls[hwnd] = control
	themeMutex.Unlock()
	previous, _, _ := procThemeSetWindowLongPtrW.Call(uintptr(hwnd), gwlpWndProc, themeSubclassCB())
	themeMutex.Lock()
	control.previous = previous
	themeMutex.Unlock()
	switch control.role {
	case roleListBox, roleComboBox:
		themeListScrollBar(hwnd, control.role)
	case roleEdit, roleReadOnly:
		// The frame takes the padding the subclass now measures.
		procThemeSetWindowPos.Call(uintptr(hwnd), 0, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate|swpFrameChanged)
	}
}

// themeListScrollBar draws the scroll bar of a list box, or of a drop-down
// list's list, which Windows creates with the drop-down list, in the palette.
func themeListScrollBar(hwnd windows.Handle, role controlRole) {
	if role == roleListBox {
		setScrollBarTheme(hwnd)
		return
	}
	info := themeComboBoxInfo{size: uint32(unsafe.Sizeof(themeComboBoxInfo{}))}
	if r, _, _ := procSendMessageW.Call(uintptr(hwnd), cbGetComboBoxInfo, 0, uintptr(unsafe.Pointer(&info))); r != 0 && info.hwndList != 0 {
		setScrollBarTheme(windows.Handle(info.hwndList))
	}
}

// refreshListScrollBars draws the scroll bars of the window's list boxes and
// drop-down lists in the palette again. A control keeps the theme class it
// was given until it is given another, so a scroll bar themed under one
// palette would otherwise keep that palette's look. Only the window's own
// controls are themed again: they belong to the thread that is refreshing
// the window.
func refreshListScrollBars(hwnd windows.Handle) {
	type list struct {
		hwnd windows.Handle
		role controlRole
	}
	var lists []list
	themeMutex.Lock()
	for control, c := range themedControls {
		if c.role == roleListBox || c.role == roleComboBox {
			lists = append(lists, list{control, c.role})
		}
	}
	themeMutex.Unlock()
	for _, l := range lists {
		if child, _, _ := procThemeIsChild.Call(uintptr(hwnd), uintptr(l.hwnd)); child != 0 {
			themeListScrollBar(l.hwnd, l.role)
		}
	}
}

func themed(hwnd windows.Handle) *themedControl {
	themeMutex.Lock()
	defer themeMutex.Unlock()
	return themedControls[hwnd]
}

// setTextStyle changes the style a themed label's text is drawn in and
// redraws it.
func setTextStyle(hwnd windows.Handle, style TextStyle) {
	themeMutex.Lock()
	control := themedControls[hwnd]
	changed := control != nil && control.style != style
	if changed {
		control.style = style
	}
	themeMutex.Unlock()
	if changed {
		procInvalidateRect.Call(uintptr(hwnd), 0, 1)
	}
}

// ThemeMessage draws the controls a window holds, and the window's
// background, in the theme. Call it first from the window procedure of every
// window that is the parent of the package's controls, and return its result
// when it reports the message handled. It also follows the system colors and
// the contrast theme as Windows reports them changed, which it leaves for the
// window to handle too.
func ThemeMessage(hwnd windows.Handle, message uint32, wParam, lParam uintptr) (uintptr, bool) {
	switch message {
	case wmEraseBkgnd:
		paintBackground(hwnd, wParam)
		return 1, true
	case wmCtlColorStatic, wmCtlColorEdit, wmCtlColorListBox, wmCtlColorBtn:
		return controlColors(message, wParam, windows.Handle(lParam)), true
	case wmNotify:
		if lParam == 0 {
			return 0, false
		}
		draw := *(**themeNMCustomDraw)(unsafe.Pointer(&lParam))
		if draw.hdr.code != nmCustomDraw {
			return 0, false
		}
		control := themed(windows.Handle(draw.hdr.hwndFrom))
		if control == nil {
			return 0, false
		}
		switch control.role {
		case rolePushButton, roleRadioButton, roleNavigation:
			return drawButton(draw, control), true
		}
	case wmDrawItem:
		if lParam == 0 {
			return 0, false
		}
		item := *(**themeDrawItem)(unsafe.Pointer(&lParam))
		if (item.ctlType == odtListBox || item.ctlType == odtComboBox) && themed(windows.Handle(item.hwndItem)) != nil {
			drawListItem(item)
			return 1, true
		}
	case wmMeasureItem:
		if lParam == 0 {
			return 0, false
		}
		item := *(**themeMeasureItem)(unsafe.Pointer(&lParam))
		if item.ctlType == odtListBox || item.ctlType == odtComboBox {
			// The heights follow the font once it is set (fitItemHeights);
			// this is the height until then.
			item.itemHeight = uint32(scaleFor(hwnd, 28))
			return 1, true
		}
	case wmSysColorChange, wmSettingChange, wmThemeChanged:
		refreshWindow(hwnd)
	case wmNCDestroy:
		themeMutex.Lock()
		delete(themedWindows, hwnd)
		delete(windowSurfaces, hwnd)
		themeMutex.Unlock()
	}
	return 0, false
}

// paintBackground fills the window's client area with its surfaces, and a
// sidebar's divider.
func paintBackground(hwnd windows.Handle, dc uintptr) {
	p := CurrentPalette()
	var client themeRect
	procThemeGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&client)))
	procThemeFillRect.Call(dc, uintptr(unsafe.Pointer(&client)), brush(p.Window))
	themeMutex.Lock()
	regions := append([]SurfaceRegion(nil), windowSurfaces[hwnd]...)
	themeMutex.Unlock()
	for _, region := range regions {
		// A region may run past the client area; only the part inside it is
		// painted.
		area := themeRect{
			Left: max(region.Left, client.Left), Top: max(region.Top, client.Top),
			Right: min(region.Right, client.Right), Bottom: min(region.Bottom, client.Bottom),
		}
		if area.Left >= area.Right || area.Top >= area.Bottom {
			continue
		}
		procThemeFillRect.Call(dc, uintptr(unsafe.Pointer(&area)), brush(p.surfaceColor(region.Surface)))
		if region.Surface == SurfaceSidebar {
			divider := themeRect{Left: region.Right - 1, Top: area.Top, Right: region.Right, Bottom: area.Bottom}
			procThemeFillRect.Call(dc, uintptr(unsafe.Pointer(&divider)), brush(p.Divider))
		}
	}
}

// controlColors answers a WM_CTLCOLOR* message: the text and background
// colors of the control, and the brush its background is painted with.
func controlColors(message uint32, dc uintptr, control windows.Handle) uintptr {
	p := CurrentPalette()
	text, background := p.Text, backgroundBehind(control)
	// A label's text is drawn over the surface behind it. A field's text is
	// drawn opaque, on the field fill: a field draws text again over the
	// glyphs already there as it scrolls a line or its selection changes, and
	// transparent text would thicken them. A read-only or disabled field asks
	// for its colors as a label does, with WM_CTLCOLORSTATIC.
	transparent := message == wmCtlColorStatic
	if c := themed(control); c != nil {
		switch c.role {
		case roleLabel:
			text = p.textColor(c.style)
		case roleEdit, roleReadOnly, roleListBox, roleComboBox:
			background, transparent = fieldFill(control, p), false
		}
	} else if message == wmCtlColorListBox || message == wmCtlColorEdit {
		// A drop-down list's list, which Windows creates itself.
		background = p.Field
	}
	procThemeSetTextColor.Call(dc, text.colorRef())
	procThemeSetBkColor.Call(dc, background.colorRef())
	mode := uintptr(themeOpaque)
	if transparent {
		mode = themeTransparent
	}
	procThemeSetBkMode.Call(dc, mode)
	return brush(background)
}

// scaleFor scales a length at 96 DPI to the window's DPI.
func scaleFor(hwnd windows.Handle, value int32) int32 {
	dpi, _, _ := procThemeGetDpiForWindow.Call(uintptr(hwnd))
	if dpi == 0 {
		dpi = 96
	}
	return int32((int64(value)*int64(dpi) + 48) / 96)
}

// scaleFloat scales a length at 96 DPI to the window's DPI, unrounded, for
// the antialiased shapes.
func scaleFloat(hwnd windows.Handle, value float64) float64 {
	dpi, _, _ := procThemeGetDpiForWindow.Call(uintptr(hwnd))
	if dpi == 0 {
		dpi = 96
	}
	return value * float64(dpi) / 96
}

// paintBuffer is an offscreen bitmap a control is drawn in, shapes then
// text, before it is copied to the screen in one piece, so nothing flickers.
type paintBuffer struct {
	dc     uintptr
	bitmap uintptr
	old    uintptr
	canvas *canvas
}

func newPaintBuffer(target uintptr, width, height int32) (*paintBuffer, bool) {
	if width <= 0 || height <= 0 {
		return nil, false
	}
	dc, _, _ := procThemeCreateCompatibleDC.Call(target)
	if dc == 0 {
		return nil, false
	}
	info := themeBitmapInfo{size: 40, width: width, height: -height, planes: 1, bitCount: 32}
	var bits unsafe.Pointer
	bitmap, _, _ := procThemeCreateDIBSection.Call(dc, uintptr(unsafe.Pointer(&info)), dibRGBColors, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bitmap == 0 || bits == nil {
		procThemeDeleteDC.Call(dc)
		return nil, false
	}
	old, _, _ := procThemeSelectObject.Call(dc, bitmap)
	pixels := unsafe.Slice((*Color)(bits), int(width)*int(height))
	return &paintBuffer{
		dc: dc, bitmap: bitmap, old: old,
		canvas: &canvas{width: int(width), height: int(height), pixels: pixels},
	}, true
}

func (b *paintBuffer) copyTo(target uintptr, x, y int32) {
	procThemeBitBlt.Call(target, uintptr(x), uintptr(y), uintptr(b.canvas.width), uintptr(b.canvas.height), b.dc, 0, 0, srcCopy)
}

func (b *paintBuffer) release() {
	b.canvas.pixels = nil
	procThemeSelectObject.Call(b.dc, b.old)
	procThemeDeleteObject.Call(b.bitmap)
	procThemeDeleteDC.Call(b.dc)
}

// text draws text in the buffer in the font and color, within bounds, with
// DrawText's format flags.
func (b *paintBuffer) text(text string, font uintptr, color Color, bounds themeRect, format uintptr) {
	if text == "" {
		return
	}
	previous := uintptr(0)
	if font != 0 {
		previous, _, _ = procThemeSelectObject.Call(b.dc, font)
	}
	procThemeSetBkMode.Call(b.dc, themeTransparent)
	procThemeSetTextColor.Call(b.dc, color.colorRef())
	procThemeDrawTextW.Call(b.dc, uintptr(unsafe.Pointer(utf16(text))), ^uintptr(0), uintptr(unsafe.Pointer(&bounds)), format)
	if previous != 0 {
		procThemeSelectObject.Call(b.dc, previous)
	}
}

// textWidth measures text in the font as the buffer would draw it.
func (b *paintBuffer) textWidth(text string, font uintptr, format uintptr) int32 {
	if text == "" {
		return 0
	}
	previous := uintptr(0)
	if font != 0 {
		previous, _, _ = procThemeSelectObject.Call(b.dc, font)
	}
	bounds := themeRect{Right: 1 << 15, Bottom: 1 << 15}
	procThemeDrawTextW.Call(b.dc, uintptr(unsafe.Pointer(utf16(text))), ^uintptr(0), uintptr(unsafe.Pointer(&bounds)), format|dtCalcRect)
	if previous != 0 {
		procThemeSelectObject.Call(b.dc, previous)
	}
	return bounds.Right - bounds.Left
}

func controlFont(hwnd windows.Handle) uintptr {
	font, _, _ := procSendMessageW.Call(uintptr(hwnd), wmGetFont, 0, 0)
	return font
}

// keyboardCues returns which of the focus indicator and the access-key
// underlines the control hides, as the Windows keyboard-cues setting and the
// user's last input decide.
func keyboardCues(hwnd windows.Handle) uintptr {
	state, _, _ := procSendMessageW.Call(uintptr(hwnd), wmQueryUIState, 0, 0)
	return state
}

// drawButton paints a push button, radio button, or navigation item whole
// at the custom-draw prepaint stage and skips the button's own drawing. Under
// a contrast theme the button draws itself in the theme's colors.
func drawButton(draw *themeNMCustomDraw, control *themedControl) uintptr {
	p := CurrentPalette()
	if draw.drawStage != cddsPrePaint || p.HighContrast {
		return cdrfDoDefault
	}
	hwnd := windows.Handle(draw.hdr.hwndFrom)
	buffer, ok := newPaintBuffer(draw.hdc, draw.rc.width(), draw.rc.height())
	if !ok {
		return cdrfDoDefault
	}
	defer buffer.release()
	cues := keyboardCues(hwnd)
	state := buttonState{
		disabled: draw.itemState&cdisDisabled != 0,
		pressed:  draw.itemState&cdisSelected != 0,
		hot:      draw.itemState&cdisHot != 0,
		focused:  draw.itemState&cdisFocus != 0 && cues&uisfHideFocus == 0,
	}
	if control.role != rolePushButton {
		checked, _, _ := procSendMessageW.Call(uintptr(hwnd), bmGetCheck, 0, 0)
		state.checked = checked&bstChecked != 0
	}
	format := uintptr(dtSingleLine | dtVCenter)
	if cues&uisfHideAccel != 0 {
		format |= dtHidePrefix
	}
	look := buttonLook{
		hwnd: hwnd, palette: p, background: backgroundBehind(hwnd),
		text: Control{hwnd: hwnd}.Text(), font: controlFont(hwnd), format: format,
	}
	switch control.role {
	case rolePushButton:
		look.pushButton(buffer, state, control.primary)
	case roleRadioButton:
		look.radioButton(buffer, state)
	case roleNavigation:
		look.navigationItem(buffer, state, control.icon)
	}
	buffer.copyTo(draw.hdc, draw.rc.Left, draw.rc.Top)
	return cdrfSkipDefault
}

type buttonState struct {
	disabled, pressed, hot, focused, checked bool
}

type buttonLook struct {
	hwnd       windows.Handle
	palette    Palette
	background Color
	text       string
	font       uintptr
	format     uintptr
}

func (l buttonLook) scale(value float64) float64 { return scaleFloat(l.hwnd, value) }

func (l buttonLook) pushButton(b *paintBuffer, state buttonState, primary bool) {
	p := l.palette
	c := b.canvas
	c.fill(l.background)
	box := shape{width: float64(c.width), height: float64(c.height), radius: l.scale(5)}
	textColor := p.Text
	switch {
	case primary:
		fill := p.Accent
		switch {
		case state.disabled:
			fill, textColor = p.AccentDisabled, p.TextDisabled
		case state.pressed:
			fill = p.AccentPressed
		case state.hot:
			fill = p.AccentHover
		}
		c.fillShape(box, fill)
		if state.focused {
			c.strokeShape(box.inset(l.scale(2)), l.scale(1.5), p.AccentText)
		}
		if !state.disabled {
			textColor = p.AccentText
		}
	default:
		fill, border := p.Control, p.ControlBorder
		switch {
		case state.disabled:
			fill, border, textColor = l.background.blend(p.Control, 0.5), p.Divider, p.TextDisabled
		case state.pressed:
			fill = p.ControlPressed
		case state.hot:
			fill = p.ControlHover
		}
		c.filledBox(box, fill, border, l.scale(1))
		if state.focused {
			c.strokeShape(box, l.scale(2), p.Focus)
		}
	}
	bounds := themeRect{Left: int32(l.scale(8)), Right: int32(c.width) - int32(l.scale(8)), Bottom: int32(c.height)}
	b.text(l.text, l.font, textColor, bounds, l.format|dtCenter)
}

func (l buttonLook) radioButton(b *paintBuffer, state buttonState) {
	p := l.palette
	c := b.canvas
	c.fill(l.background)
	diameter := math.Min(l.scale(18), float64(c.height))
	left := l.scale(2)
	circle := shape{x: left, y: (float64(c.height) - diameter) / 2, width: diameter, height: diameter, radius: diameter / 2}
	textColor := p.Text
	if state.disabled {
		textColor = p.TextDisabled
	}
	if state.checked {
		fill, dot := p.Accent, 0.42
		switch {
		case state.disabled:
			fill = p.TextDisabled
		case state.pressed:
			fill, dot = p.AccentPressed, 0.34
		case state.hot:
			fill, dot = p.AccentHover, 0.5
		}
		c.fillShape(circle, fill)
		inner := diameter * dot
		center := shape{
			x: circle.x + (diameter-inner)/2, y: circle.y + (diameter-inner)/2,
			width: inner, height: inner, radius: inner / 2,
		}
		dotColor := p.AccentText
		if state.disabled {
			dotColor = l.background
		}
		c.fillShape(center, dotColor)
	} else {
		fill, border := p.Field, p.FieldBorder
		switch {
		case state.disabled:
			border = p.TextDisabled
		case state.pressed:
			fill, border = p.Hover, p.TextMuted
		case state.hot:
			border = p.TextMuted
		}
		c.filledBox(circle, fill, border, l.scale(1.25))
	}
	textLeft := int32(left + diameter + l.scale(8))
	if state.focused {
		width := b.textWidth(l.text, l.font, l.format)
		right := math.Min(float64(textLeft+width)+l.scale(5), float64(c.width))
		c.strokeShape(shape{width: right, height: float64(c.height), radius: l.scale(4)}, l.scale(1.5), p.Focus)
	}
	bounds := themeRect{Left: textLeft, Right: int32(c.width), Bottom: int32(c.height)}
	b.text(l.text, l.font, textColor, bounds, l.format|dtEndEllipsis)
}

func (l buttonLook) navigationItem(b *paintBuffer, state buttonState, icon rune) {
	p := l.palette
	c := b.canvas
	c.fill(l.background)
	box := shape{width: float64(c.width), height: float64(c.height), radius: l.scale(6)}
	textColor := p.TextMuted
	switch {
	case state.checked:
		c.fillShape(box, p.Selected)
		textColor = p.SelectedText
	case state.pressed, state.hot:
		c.fillShape(box, p.Hover)
		textColor = p.Text
	}
	if state.checked {
		height := math.Min(l.scale(16), float64(c.height)-l.scale(8))
		c.fillShape(shape{x: l.scale(1), y: (float64(c.height) - height) / 2, width: l.scale(3), height: height, radius: l.scale(1.5)}, p.Focus)
	}
	if state.focused {
		c.strokeShape(box, l.scale(2), p.Focus)
	}
	textLeft := int32(l.scale(14))
	if icon != 0 {
		size := int32(l.scale(16))
		if font := iconFont(size); font != 0 {
			bounds := themeRect{Left: textLeft, Right: textLeft + int32(l.scale(20)), Bottom: int32(c.height)}
			b.text(string(icon), uintptr(font), textColor, bounds, dtSingleLine|dtVCenter|dtCenter|dtNoPrefix)
		}
		textLeft += int32(l.scale(30))
	}
	bounds := themeRect{Left: textLeft, Right: int32(c.width) - int32(l.scale(8)), Bottom: int32(c.height)}
	b.text(l.text, l.font, textColor, bounds, l.format|dtEndEllipsis)
}

// drawListItem paints a row of a themed list box or of a drop-down list's
// list.
func drawListItem(item *themeDrawItem) {
	if item.ctlType == odtComboBox && item.itemState&odsComboBoxEdit != 0 {
		// A drop-down list draws its face itself, without a repaint, when its
		// selection or its items change, as a view's Select or Reset does. The
		// face is the subclass's to paint, whole, so it is repainted instead.
		procInvalidateRect.Call(item.hwndItem, 0, 0)
		return
	}
	if int32(item.itemID) < 0 {
		return
	}
	hwnd := windows.Handle(item.hwndItem)
	p := CurrentPalette()
	buffer, ok := newPaintBuffer(item.hdc, item.rcItem.width(), item.rcItem.height())
	if !ok {
		return
	}
	defer buffer.release()
	c := buffer.canvas
	// A disabled list's rows sit on the fill its frame and empty area take.
	c.fill(fieldFill(hwnd, p))
	row := shape{x: scaleFloat(hwnd, 3), y: 1, width: float64(c.width) - scaleFloat(hwnd, 6), height: float64(c.height) - 2, radius: scaleFloat(hwnd, 4)}
	textColor := p.Text
	if item.itemState&odsSelected != 0 {
		c.fillShape(row, p.Selected)
		textColor = p.SelectedText
	}
	if item.itemState&odsDisabled != 0 {
		textColor = p.TextDisabled
	}
	if item.itemState&odsFocus != 0 && item.itemState&odsNoFocusRect == 0 {
		c.strokeShape(row, scaleFloat(hwnd, 1), p.Focus)
	}
	var text string
	if item.ctlType == odtComboBox {
		text = itemText(hwnd, cbGetLBTextLen, cbGetLBText, int(item.itemID))
	} else {
		text = itemText(hwnd, lbGetTextLen, lbGetText, int(item.itemID))
	}
	padding := scaleFor(hwnd, 12)
	bounds := themeRect{Left: padding, Right: int32(c.width) - padding, Bottom: int32(c.height)}
	buffer.text(text, controlFont(hwnd), textColor, bounds, dtSingleLine|dtVCenter|dtNoPrefix|dtEndEllipsis)
	buffer.copyTo(item.hdc, item.rcItem.Left, item.rcItem.Top)
}

// themedControlProc is the subclass of the fields, drop-down lists, and list
// boxes whose frame or face the theme paints.
func themedControlProc(hwnd windows.Handle, message uint32, wParam uintptr, lParam uintptr) uintptr {
	control := themed(hwnd)
	if control == nil || control.previous == 0 {
		r, _, _ := procThemeDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
		return r
	}
	previous := control.previous
	call := func() uintptr {
		r, _, _ := procThemeCallWindowProcW.Call(previous, uintptr(hwnd), uintptr(message), wParam, lParam)
		return r
	}
	if message == wmNCDestroy {
		procThemeSetWindowLongPtrW.Call(uintptr(hwnd), gwlpWndProc, previous)
		themeMutex.Lock()
		delete(themedControls, hwnd)
		themeMutex.Unlock()
		return call()
	}
	switch control.role {
	case roleEdit, roleReadOnly:
		return fieldProc(hwnd, message, wParam, lParam, call)
	case roleComboBox:
		return comboBoxProc(hwnd, control, message, lParam, call)
	case roleListBox:
		return listBoxProc(hwnd, message, call)
	}
	return call()
}

// fieldProc pads a text field inside a frame the theme paints, its text
// centered vertically, and redraws the frame as the field's state changes.
func fieldProc(hwnd windows.Handle, message uint32, wParam, lParam uintptr, call func() uintptr) uintptr {
	switch message {
	case wmNCCalcSize:
		r := call()
		if lParam != 0 {
			padField(hwnd, *(**themeRect)(unsafe.Pointer(&lParam)))
		}
		return r
	case wmNCPaint:
		paintFieldFrame(hwnd)
		return 0
	case wmNCHitTest:
		// The padding is part of the field: a click on it places the caret,
		// as a click on the text does.
		if r := call(); r != htClient {
			var bounds themeRect
			procThemeGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&bounds)))
			x, y := int32(int16(lParam)), int32(int16(lParam>>16))
			if x >= bounds.Left && x < bounds.Right && y >= bounds.Top && y < bounds.Bottom {
				return htClient
			}
			return r
		}
		return htClient
	case wmSetFont:
		r := call()
		procThemeSetWindowPos.Call(uintptr(hwnd), 0, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate|swpFrameChanged)
		return r
	case wmSetFocus, wmKillFocus, wmEnable, wmStyleChanged:
		r := call()
		procThemeRedrawWindow.Call(uintptr(hwnd), 0, 0, rdwFrame|rdwInvalidate)
		return r
	}
	return call()
}

// padField narrows the client area Windows proposed for a field, inside its
// edge, so the text keeps clear of the frame: a fixed padding at the sides,
// and the rest of the height split above and below the line of text.
func padField(hwnd windows.Handle, client *themeRect) {
	side := scaleFor(hwnd, 7)
	if client.width() > 2*side+1 {
		client.Left += side
		client.Right -= side
	}
	// A line that cannot be measured leaves the height as Windows proposed
	// it, rather than counting all of it spare and leaving no room for text.
	line := lineHeight(hwnd)
	if spare := client.height() - line; line > 0 && spare > 0 {
		client.Top += spare / 2
		client.Bottom -= spare - spare/2
	}
}

// lineHeight returns the height of a line of the control's font, or 0 when
// it cannot be measured.
func lineHeight(hwnd windows.Handle) int32 {
	dc, _, _ := procThemeGetDC.Call(uintptr(hwnd))
	if dc == 0 {
		return 0
	}
	defer procThemeReleaseDC.Call(uintptr(hwnd), dc)
	if font := controlFont(hwnd); font != 0 {
		previous, _, _ := procThemeSelectObject.Call(dc, font)
		defer procThemeSelectObject.Call(dc, previous)
	}
	var metrics themeTextMetric
	if r, _, _ := procThemeGetTextMetricsW.Call(dc, uintptr(unsafe.Pointer(&metrics))); r == 0 {
		return 0
	}
	return metrics.height
}

// windowAndClient returns the control's window rectangle at the origin and
// its client rectangle within it.
func windowAndClient(hwnd windows.Handle) (window, client themeRect, ok bool) {
	if r, _, _ := procThemeGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&window))); r == 0 {
		return window, client, false
	}
	procThemeGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&client)))
	procThemeMapWindowPoints.Call(uintptr(hwnd), 0, uintptr(unsafe.Pointer(&client)), 2)
	client = themeRect{
		Left: client.Left - window.Left, Top: client.Top - window.Top,
		Right: client.Right - window.Left, Bottom: client.Bottom - window.Top,
	}
	window = themeRect{Right: window.width(), Bottom: window.height()}
	return window, client, true
}

// fieldColors returns the fill and outline, and the outline's width, of a
// field, drop-down list, or list box in its state.
func fieldColors(hwnd windows.Handle, focused, hot bool) (fill, border Color, width float64) {
	p := CurrentPalette()
	fill, border, width = fieldFill(hwnd, p), p.FieldBorder, scaleFloat(hwnd, 1)
	if enabled, _, _ := procThemeIsWindowEnabled.Call(uintptr(hwnd)); enabled == 0 {
		return fill, p.Divider, width
	}
	switch {
	case focused:
		border, width = p.Focus, scaleFloat(hwnd, 2)
	case hot:
		border = p.TextMuted
	}
	return fill, border, width
}

// paintFieldFrame paints a text field's frame, its padding included, around
// the client area the field paints itself.
func paintFieldFrame(hwnd windows.Handle) {
	window, client, ok := windowAndClient(hwnd)
	if !ok {
		return
	}
	dc, _, _ := procThemeGetWindowDC.Call(uintptr(hwnd))
	if dc == 0 {
		return
	}
	defer procThemeReleaseDC.Call(uintptr(hwnd), dc)
	buffer, ok := newPaintBuffer(dc, window.width(), window.height())
	if !ok {
		return
	}
	defer buffer.release()
	focus, _, _ := procGetFocus.Call()
	fill, border, width := fieldColors(hwnd, windows.Handle(focus) == hwnd, false)
	buffer.canvas.fill(backgroundBehind(hwnd))
	box := shape{width: float64(window.width()), height: float64(window.height()), radius: scaleFloat(hwnd, 4)}
	buffer.canvas.filledBox(box, fill, border, width)
	procThemeExcludeClipRect.Call(dc, uintptr(client.Left), uintptr(client.Top), uintptr(client.Right), uintptr(client.Bottom))
	buffer.copyTo(dc, 0, 0)
}

// comboBoxProc paints a drop-down list's closed face — the selected item, a
// chevron, and the frame — whole, and redraws it as its state changes.
func comboBoxProc(hwnd windows.Handle, control *themedControl, message uint32, lParam uintptr, call func() uintptr) uintptr {
	switch message {
	case wmPaint:
		paintComboBox(hwnd, control)
		return 0
	case wmEraseBkgnd:
		return 1
	case wmSetFont:
		r := call()
		fitItemHeights(hwnd, roleComboBox)
		return r
	case wmMouseMove:
		themeMutex.Lock()
		entering := !control.hot
		control.hot = true
		themeMutex.Unlock()
		if entering {
			track := themeTrackMouse{size: uint32(unsafe.Sizeof(themeTrackMouse{})), flags: tmeLeave, track: uintptr(hwnd)}
			procThemeTrackMouseEvent.Call(uintptr(unsafe.Pointer(&track)))
			procInvalidateRect.Call(uintptr(hwnd), 0, 0)
		}
	case wmMouseLeave:
		themeMutex.Lock()
		control.hot = false
		themeMutex.Unlock()
		procInvalidateRect.Call(uintptr(hwnd), 0, 0)
	case wmSetFocus, wmKillFocus, wmEnable:
		r := call()
		procInvalidateRect.Call(uintptr(hwnd), 0, 0)
		return r
	}
	return call()
}

func paintComboBox(hwnd windows.Handle, control *themedControl) {
	var paint themePaintStruct
	dc, _, _ := procThemeBeginPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&paint)))
	if dc == 0 {
		return
	}
	defer procThemeEndPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&paint)))
	var client themeRect
	procThemeGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&client)))
	buffer, ok := newPaintBuffer(dc, client.width(), client.height())
	if !ok {
		return
	}
	defer buffer.release()
	p := CurrentPalette()
	focus, _, _ := procGetFocus.Call()
	dropped, _, _ := procSendMessageW.Call(uintptr(hwnd), cbGetDroppedState, 0, 0)
	themeMutex.Lock()
	hot := control.hot
	themeMutex.Unlock()
	fill, border, width := fieldColors(hwnd, windows.Handle(focus) == hwnd || dropped != 0, hot)
	c := buffer.canvas
	c.fill(backgroundBehind(hwnd))
	c.filledBox(shape{width: float64(c.width), height: float64(c.height), radius: scaleFloat(hwnd, 4)}, fill, border, width)
	textColor, chevronColor := p.Text, p.TextMuted
	if enabled, _, _ := procThemeIsWindowEnabled.Call(uintptr(hwnd)); enabled == 0 {
		textColor, chevronColor = p.TextDisabled, p.TextDisabled
	}
	chevron := scaleFor(hwnd, 30)
	padding := scaleFor(hwnd, 10)
	var text string
	if selected, _, _ := procSendMessageW.Call(uintptr(hwnd), cbGetCurSel, 0, 0); int32(selected) >= 0 {
		text = itemText(hwnd, cbGetLBTextLen, cbGetLBText, int(int32(selected)))
	}
	bounds := themeRect{Left: padding, Right: int32(c.width) - chevron, Bottom: int32(c.height)}
	buffer.text(text, controlFont(hwnd), textColor, bounds, dtSingleLine|dtVCenter|dtNoPrefix|dtEndEllipsis)
	if font := iconFont(scaleFor(hwnd, 10)); font != 0 {
		arrow := themeRect{Left: int32(c.width) - chevron, Right: int32(c.width) - scaleFor(hwnd, 6), Bottom: int32(c.height)}
		buffer.text("", uintptr(font), chevronColor, arrow, dtSingleLine|dtVCenter|dtCenter|dtNoPrefix)
	}
	buffer.copyTo(dc, 0, 0)
}

// listBoxProc outlines a list box in the theme's frame once Windows has
// drawn its edge and scroll bar, and sizes its rows to its font.
func listBoxProc(hwnd windows.Handle, message uint32, call func() uintptr) uintptr {
	switch message {
	case wmNCPaint:
		r := call()
		paintListFrame(hwnd)
		return r
	case wmSetFont:
		r := call()
		fitItemHeights(hwnd, roleListBox)
		return r
	case wmSetFocus, wmKillFocus, wmEnable:
		r := call()
		procThemeRedrawWindow.Call(uintptr(hwnd), 0, 0, rdwFrame|rdwInvalidate)
		return r
	}
	return call()
}

// paintListFrame paints over a list box's edge — the band between its window
// and its client area on the side without the scroll bar — with the theme's
// frame.
func paintListFrame(hwnd windows.Handle) {
	window, client, ok := windowAndClient(hwnd)
	if !ok {
		return
	}
	edge := client.Left
	if edge <= 0 {
		return
	}
	dc, _, _ := procThemeGetWindowDC.Call(uintptr(hwnd))
	if dc == 0 {
		return
	}
	defer procThemeReleaseDC.Call(uintptr(hwnd), dc)
	buffer, ok := newPaintBuffer(dc, window.width(), window.height())
	if !ok {
		return
	}
	defer buffer.release()
	focus, _, _ := procGetFocus.Call()
	fill, border, width := fieldColors(hwnd, windows.Handle(focus) == hwnd, false)
	buffer.canvas.fill(backgroundBehind(hwnd))
	box := shape{width: float64(window.width()), height: float64(window.height()), radius: scaleFloat(hwnd, 4)}
	buffer.canvas.filledBox(box, fill, border, width)
	strips := []themeRect{
		{Right: window.Right, Bottom: edge},
		{Top: window.Bottom - edge, Right: window.Right, Bottom: window.Bottom},
		{Top: edge, Right: edge, Bottom: window.Bottom - edge},
		{Left: window.Right - edge, Top: edge, Right: window.Right, Bottom: window.Bottom - edge},
	}
	for _, strip := range strips {
		procThemeBitBlt.Call(dc, uintptr(strip.Left), uintptr(strip.Top), uintptr(strip.width()), uintptr(strip.height()), buffer.dc, uintptr(strip.Left), uintptr(strip.Top), srcCopy)
	}
}

// fitItemHeights sizes an owner-drawn list's rows, and a drop-down list's
// closed face, to the control's font: a row has room around its line, and a
// drop-down list is as tall as a text field.
func fitItemHeights(hwnd windows.Handle, role controlRole) {
	line := lineHeight(hwnd)
	if line <= 0 {
		return
	}
	row := uintptr(line + scaleFor(hwnd, 12))
	if role == roleListBox {
		procSendMessageW.Call(uintptr(hwnd), lbSetItemHeight, 0, row)
		return
	}
	procSendMessageW.Call(uintptr(hwnd), cbSetItemHeight, 0, row)
	// The closed face is the selection field's height and the edge around
	// it, whose size the control decides, so it is measured.
	target := fieldHeight(hwnd)
	selection := ^uintptr(0) // -1, the selection field
	procSendMessageW.Call(uintptr(hwnd), cbSetItemHeight, selection, uintptr(target))
	var bounds themeRect
	if r, _, _ := procThemeGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&bounds))); r != 0 {
		if extra := bounds.height() - target; extra > 0 && target > extra {
			procSendMessageW.Call(uintptr(hwnd), cbSetItemHeight, selection, uintptr(target-extra))
		}
	}
}

// fieldHeight is the height of a text field at the control's DPI, which a
// drop-down list's closed face matches.
func fieldHeight(hwnd windows.Handle) int32 {
	return scaleFor(hwnd, 28)
}

// FontFace is a typeface the application draws text in.
type FontFace uint8

const (
	// FontText is the face of body text and controls.
	FontText FontFace = iota
	// FontDisplay is the face of titles.
	FontDisplay
	// FontIcons is the Windows icon font the navigation and the drop-down
	// lists draw their glyphs in.
	FontIcons
)

// fontFaces lists each face's typefaces in order of preference: the Windows
// 11 typeface first, then the one an earlier Windows has.
var fontFaces = map[FontFace][]string{
	FontText:    {"Segoe UI Variable Text", "Segoe UI"},
	FontDisplay: {"Segoe UI Variable Display", "Segoe UI"},
	FontIcons:   {"Segoe Fluent Icons", "Segoe MDL2 Assets"},
}

var (
	resolvedFacesMutex sync.Mutex
	resolvedFaces      = map[FontFace]string{}
)

// NewFont creates a font of the face, pixelHeight pixels tall from the top of
// its tallest letter to the bottom of its lowest, of the weight (400 regular,
// 600 semibold). The caller deletes it.
func NewFont(pixelHeight, weight int32, face FontFace) (windows.Handle, error) {
	description := themeLogFont{
		Height:  -pixelHeight,
		Weight:  weight,
		CharSet: 1, // DEFAULT_CHARSET
		Quality: 5, // CLEARTYPE_QUALITY
	}
	name, err := windows.UTF16FromString(fontFaceName(face))
	if err != nil {
		return 0, err
	}
	copy(description.Face[:len(description.Face)-1], name)
	font, _, err := procThemeCreateFontIndirectW.Call(uintptr(unsafe.Pointer(&description)))
	if font == 0 {
		return 0, callError("CreateFontIndirectW", err)
	}
	return windows.Handle(font), nil
}

// fontFaceName returns the first of the face's typefaces that is installed,
// found once: Windows substitutes another typeface for one that is missing,
// and the font it creates then reports that typeface's name.
func fontFaceName(face FontFace) string {
	resolvedFacesMutex.Lock()
	defer resolvedFacesMutex.Unlock()
	if name, ok := resolvedFaces[face]; ok {
		return name
	}
	candidates := fontFaces[face]
	name := candidates[len(candidates)-1]
	for _, candidate := range candidates[:len(candidates)-1] {
		if fontInstalled(candidate) {
			name = candidate
			break
		}
	}
	resolvedFaces[face] = name
	return name
}

func fontInstalled(name string) bool {
	description := themeLogFont{Height: -12, Weight: 400, CharSet: 1}
	face, err := windows.UTF16FromString(name)
	if err != nil {
		return false
	}
	copy(description.Face[:len(description.Face)-1], face)
	font, _, _ := procThemeCreateFontIndirectW.Call(uintptr(unsafe.Pointer(&description)))
	if font == 0 {
		return false
	}
	defer procThemeDeleteObject.Call(font)
	dc, _, _ := procThemeGetDC.Call(0)
	if dc == 0 {
		return false
	}
	defer procThemeReleaseDC.Call(0, dc)
	previous, _, _ := procThemeSelectObject.Call(dc, font)
	defer procThemeSelectObject.Call(dc, previous)
	var actual [32]uint16
	procThemeGetTextFaceW.Call(dc, uintptr(len(actual)), uintptr(unsafe.Pointer(&actual[0])))
	return windows.UTF16ToString(actual[:]) == name
}

// iconFont returns the icon font at the pixel size, created once for the
// process, or 0 when it cannot be created.
func iconFont(size int32) windows.Handle {
	themeMutex.Lock()
	font, ok := iconFonts[size]
	themeMutex.Unlock()
	if ok {
		return font
	}
	created, err := NewFont(size, 400, FontIcons)
	if err != nil {
		return 0
	}
	themeMutex.Lock()
	defer themeMutex.Unlock()
	if existing, ok := iconFonts[size]; ok {
		procThemeDeleteObject.Call(uintptr(created))
		return existing
	}
	iconFonts[size] = created
	return created
}

// Paint answers WM_PAINT for a window that draws over its background: draw
// gets the device context of the part of the window to draw, which the
// theme has already filled with its surfaces.
func Paint(hwnd windows.Handle, draw func(dc uintptr)) {
	var paint themePaintStruct
	dc, _, _ := procThemeBeginPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&paint)))
	if dc == 0 {
		return
	}
	defer procThemeEndPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&paint)))
	draw(dc)
}

// PaintBrand draws the application's icon, size pixels square, and its name
// beside it in the font, at (x, y) of the device context, vertically
// centered on the icon. The window's title bar names the application too, so
// the drawing is decoration that no screen reader needs.
func PaintBrand(dc uintptr, icon windows.Handle, size int32, font windows.Handle, name string, x, y int32) {
	p := CurrentPalette()
	if icon != 0 {
		procThemeDrawIconEx.Call(dc, uintptr(x), uintptr(y), uintptr(icon), uintptr(size), uintptr(size), 0, 0, 3) // DI_NORMAL
	}
	previous := uintptr(0)
	if font != 0 {
		previous, _, _ = procThemeSelectObject.Call(dc, uintptr(font))
	}
	procThemeSetBkMode.Call(dc, themeTransparent)
	procThemeSetTextColor.Call(dc, p.Text.colorRef())
	bounds := themeRect{Left: x + size + size/3, Top: y, Right: x + 1<<14, Bottom: y + size}
	procThemeDrawTextW.Call(dc, uintptr(unsafe.Pointer(utf16(name))), ^uintptr(0), uintptr(unsafe.Pointer(&bounds)), dtSingleLine|dtVCenter|dtNoPrefix)
	if previous != 0 {
		procThemeSelectObject.Call(dc, previous)
	}
}

// fieldFill returns the fill of a field, drop-down list, or list box: the
// palette's, or for one that is disabled a fill halfway to the background
// behind it, which both its frame and the text area it paints itself take.
func fieldFill(hwnd windows.Handle, p Palette) Color {
	if enabled, _, _ := procThemeIsWindowEnabled.Call(uintptr(hwnd)); enabled == 0 {
		return backgroundBehind(hwnd).blend(p.Field, 0.5)
	}
	return p.Field
}
