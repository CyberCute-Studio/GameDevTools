//go:build windows

package ui

import (
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	cardClassName = "CyberCuteDevToolsResultCard"

	// Sizes at 96 DPI.
	cardLogicalWidth   = 380
	cardLogicalInset   = 12
	cardLogicalGap     = 8
	cardLogicalMargin  = 16
	cardLogicalButtonW = 112
	cardLogicalButtonH = 28
	cardLogicalFont    = 18

	cardOpenCommand    = 1
	cardDismissCommand = 2
	cardTimerID        = 1

	cardWMDestroy        = 0x0002
	cardWMClose          = 0x0010
	cardWMCommand        = 0x0111
	cardWMTimer          = 0x0113
	cardWMCtlColorStatic = 0x0138
	cardWMMouseActivate  = 0x0021
	cardWMNCDestroy      = 0x0082
	cardWMLButtonDown    = 0x0201
	cardWMLButtonUp      = 0x0202
	cardWMLButtonDblClk  = 0x0203
	cardWMMouseLeave     = 0x02a3
	cardTMELeave         = 0x00000002
	cardBMSetState       = 0x00f3
	cardMANoActivate     = 3
	cardBNClicked        = 0
	cardBNDoubleClicked  = 5
	cardGWLPWndProc      = ^uintptr(3) // -4 as a uintptr

	cardWSPopup          = 0x80000000
	cardWSBorder         = 0x00800000
	cardWSExTopmost      = 0x00000008
	cardWSExToolWindow   = 0x00000080
	cardWSExNoActivate   = 0x08000000
	cardSSEditControl    = 0x00002000
	cardHwndTopmost      = ^uintptr(0)
	cardSWPNoActivate    = 0x0010
	cardSWPShowWindow    = 0x0040
	cardDTWordBreak      = 0x00000010
	cardDTCalcRect       = 0x00000400
	cardDTNoPrefix       = 0x00000800
	cardDTEditControl    = 0x00002000
	cardColorWindow      = 5
	cardColorWindowText  = 8
	cardTransparent      = 1
	cardIDCArrow         = 32512
	cardFontNormal       = 400
	cardDefaultCharset   = 1
	cardClearTypeQuality = 5
)

var (
	cardKernel32 = windows.NewLazySystemDLL("kernel32.dll")
	cardGDI32    = windows.NewLazySystemDLL("gdi32.dll")

	procCardRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCardDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procCardLoadCursorW         = user32.NewProc("LoadCursorW")
	procCardSetWindowPos        = user32.NewProc("SetWindowPos")
	procCardGetSysColor         = user32.NewProc("GetSysColor")
	procCardGetSysColorBrush    = user32.NewProc("GetSysColorBrush")
	procCardGetDC               = user32.NewProc("GetDC")
	procCardReleaseDC           = user32.NewProc("ReleaseDC")
	procCardDrawTextW           = user32.NewProc("DrawTextW")
	procCardSetWindowLongPtrW   = user32.NewProc("SetWindowLongPtrW")
	procCardCallWindowProcW     = user32.NewProc("CallWindowProcW")
	procCardTrackMouseEvent     = user32.NewProc("TrackMouseEvent")
	procCardGetModuleHandleW    = cardKernel32.NewProc("GetModuleHandleW")
	procCardCreateFontIndirectW = cardGDI32.NewProc("CreateFontIndirectW")
	procCardSelectObject        = cardGDI32.NewProc("SelectObject")
	procCardDeleteObject        = cardGDI32.NewProc("DeleteObject")
	procCardSetBkMode           = cardGDI32.NewProc("SetBkMode")
	procCardSetTextColor        = cardGDI32.NewProc("SetTextColor")

	registerCardOnce sync.Once
	registerCardErr  error
	// cardStates finds a card from its window on the GUI thread.
	cardStates = map[windows.Handle]*ResultCard{}
	// cardButtons finds a card button's press state on the GUI thread.
	cardButtons        = map[windows.Handle]*cardButton{}
	cardButtonCallback = sync.OnceValue(func() uintptr { return windows.NewCallback(cardButtonProc) })
)

// cardButton is a card button's press. A standard button takes the keyboard
// focus when pressed, and with it the activation of its window, whatever
// WM_MOUSEACTIVATE answered, so the card handles the mouse on its buttons
// itself. The card is never the foreground window, whose mouse capture alone
// follows the pointer off it, so a press ends when the pointer leaves the
// button rather than wait for a release that may go to another application.
type cardButton struct {
	parent   windows.Handle
	id       int32
	previous uintptr
	pressed  bool
	double   bool
}

type cardTrackMouse struct {
	Size      uint32
	Flags     uint32
	Track     windows.Handle
	HoverTime uint32
}

type cardWindowClass struct {
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

type cardLogFont struct {
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

type cardRect struct {
	Left, Top, Right, Bottom int32
}

// ResultCard is a small always-on-top card in the corner of a display that
// reports results while the view they belong to cannot be seen. It never
// takes the keyboard focus from the application in front, so it can appear
// while the user works elsewhere (PRD 9.1). It has no owner window, as an
// owned window would be hidden with a minimized main window, and it is not
// listed on the taskbar. It must be suspended before a capture so that it
// cannot appear in the image (SCR-068). All of its methods run on the GUI
// thread.
type ResultCard struct {
	place func() CardPlacement
	open  func(path string)
	// changed hears how many problems a press can dismiss after each change.
	changed func(problems int)

	queue     cardQueue
	suspended bool
	destroyed bool

	hwnd    windows.Handle
	message *Label
	count   *Label
	openBtn *Button
	dismiss *Button
	font    windows.Handle
	fontDPI uint32
	path    string
	// showing is the result on the card, none while it is hidden, so that a
	// press on a button ends when another takes its place and a success's
	// time on screen starts only once.
	showing CardEntry
	// unheard is whether a problem came in while the card was hidden and has
	// not been read out, even one identical to a problem already waiting or
	// one a success was then read out over.
	unheard bool
}

// NewResultCard returns a card that appears where place says, as of each
// time it is shown, and calls open for Open Folder. Its window is created the
// first time there is something to show.
func NewResultCard(place func() CardPlacement, open func(path string)) *ResultCard {
	return &ResultCard{place: place, open: open}
}

// Report adds a result and shows it, unless the card is suspended, when it
// waits for Resume. A success leaves by itself; a warning or error stays
// until it is dismissed.
func (c *ResultCard) Report(notice Notice, path string) {
	c.add(CardEntry{Notice: notice, Path: path})
}

// ReportSetting adds a problem with a setting, as Report does, for
// WithdrawSettings to remove.
func (c *ResultCard) ReportSetting(notice Notice) {
	c.add(CardEntry{Notice: notice, Setting: true})
}

func (c *ResultCard) add(entry CardEntry) {
	if c.destroyed {
		return
	}
	c.queue.push(entry)
	c.unheard = c.unheard || c.suspended && entry.lasting()
	c.present(true)
	c.tellProblems()
}

// WithdrawSettings removes the problems with a setting, once the default
// settings replaced what they report.
func (c *ResultCard) WithdrawSettings() {
	if c.queue.withdrawSettings() {
		c.present(false)
		c.tellProblems()
	}
}

// WithdrawSetting removes one problem with a setting, once that setting no
// longer has it, as a shortcut that has since registered.
func (c *ResultCard) WithdrawSetting(notice Notice) {
	if c.queue.withdrawSetting(notice) {
		c.present(false)
		c.tellProblems()
	}
}

// Suspend hides the card before a capture, keeping its problems. A success on
// it has been seen and ends here, or a capture that ends with no result of its
// own, as a cancelled selection does, would bring it back as if it were new.
func (c *ResultCard) Suspend() {
	if !c.suspended {
		c.suspended = true
		c.queue.expire()
	}
	c.hide()
	c.tellProblems()
}

// Resume shows the card again once a capture has ended, announcing it only
// if a result came in meanwhile: Suspend ended any earlier success, so a
// success here is that capture's, and present reads out an unheard problem.
func (c *ResultCard) Resume() {
	if !c.suspended {
		return
	}
	c.suspended = false
	c.present(c.queue.passing != nil)
	c.tellProblems()
}

// Reposition moves a card on screen to where it now belongs, as after a
// display was disconnected.
func (c *ResultCard) Reposition() {
	if c.hwnd != 0 && !c.suspended {
		c.present(false)
	}
}

// SetProblemsChanged names the function that hears how many problems a press
// can dismiss, so a view can offer a way to dismiss them from the keyboard.
func (c *ResultCard) SetProblemsChanged(changed func(problems int)) {
	c.changed = changed
}

// tellProblems reports the problems a press can dismiss: none while the card
// is hidden for a capture, as the main window is back before the capture's
// file is written and a press there would remove a problem nobody can see.
func (c *ResultCard) tellProblems() {
	if c.changed == nil || c.destroyed {
		return
	}
	problems := len(c.queue.problems)
	if c.suspended {
		problems = 0
	}
	c.changed(problems)
}

// DismissProblem removes the result on the card, for its Dismiss button and for
// a keyboard user, whom that button cannot reach. A success over the problems
// goes first, so that a problem is on the card before a press can remove it,
// and a card hidden for a capture keeps everything.
func (c *ResultCard) DismissProblem() {
	if c.suspended {
		return
	}
	c.queue.dismiss()
	c.present(true)
	c.tellProblems()
}

// Destroy removes the card and its window. Nothing can be shown afterwards.
func (c *ResultCard) Destroy() {
	c.destroyed = true
	if c.hwnd != 0 {
		procKillTimer.Call(uintptr(c.hwnd), cardTimerID)
		c.message.ClearLive()
		c.count.ClearLive()
		procDestroyWindow.Call(uintptr(c.hwnd))
		c.hwnd = 0
	}
	if c.font != 0 {
		procCardDeleteObject.Call(uintptr(c.font))
		c.font = 0
	}
}

func (c *ResultCard) hide() {
	if c.hwnd != 0 {
		c.cancelPress()
		procKillTimer.Call(uintptr(c.hwnd), cardTimerID)
		procShowWindow.Call(uintptr(c.hwnd), swHide)
		c.showing = CardEntry{}
	}
}

// cancelPress ends a press on the card's buttons, so that releasing the mouse
// cannot dismiss or open a result other than the one it was pressed on.
func (c *ResultCard) cancelPress() {
	if c.hwnd == 0 {
		return
	}
	for _, button := range []*Button{c.openBtn, c.dismiss} {
		state := cardButtons[button.hwnd]
		if state == nil || !state.pressed {
			continue
		}
		state.pressed = false
		procSendMessageW.Call(uintptr(button.hwnd), cardBMSetState, 0, 0)
	}
}

// present shows the current result, or hides the card when there is none. A
// card that cannot be created shows nothing: the view's own status area still
// holds the result.
func (c *ResultCard) present(announce bool) {
	entry, ok := c.queue.current()
	if c.suspended || c.destroyed || !ok {
		c.hide()
		return
	}
	if c.hwnd == 0 && c.create() != nil {
		return
	}
	changed := entry != c.showing
	if changed {
		c.cancelPress()
		c.showing = entry
	}
	placement := CardPlacement{}
	if c.place != nil {
		placement = c.place()
	}
	dpi := placement.DPI
	if dpi == 0 {
		dpi = 96
	}
	c.applyFont(dpi)
	scale := func(value int32) int32 {
		return int32((int64(value)*int64(dpi) + 48) / 96)
	}

	text := entry.Notice.Text()
	countText := ""
	if waiting := c.queue.waiting(); waiting > 0 {
		countText = fmt.Sprintf(CardEarlierResults, waiting)
	}
	c.path = entry.Path
	c.message.SetTextQuietly(text)
	c.count.SetTextQuietly(countText)
	c.count.Show(countText != "")
	c.openBtn.Show(entry.Path != "")

	width, inset, gap := scale(cardLogicalWidth), scale(cardLogicalInset), scale(cardLogicalGap)
	buttonWidth, buttonHeight := scale(cardLogicalButtonW), scale(cardLogicalButtonH)
	margin := scale(cardLogicalMargin)
	textWidth := width - 2*inset
	countHeight := int32(0)
	if countText != "" {
		countHeight = c.measure(countText, textWidth) + gap
	}
	fixed := inset + countHeight + gap + buttonHeight + inset
	messageHeight := c.measure(text, textWidth)
	if room := placement.Bottom - placement.Top - 2*margin - 2 - fixed; room > 0 {
		messageHeight = min(messageHeight, room)
	}
	height := fixed + messageHeight

	c.message.Move(inset, inset, textWidth, messageHeight)
	y := inset + messageHeight
	if countText != "" {
		c.count.Move(inset, y+gap, textWidth, countHeight-gap)
		y += countHeight
	}
	y += gap
	c.dismiss.Move(width-inset-buttonWidth, y, buttonWidth, buttonHeight)
	c.openBtn.Move(width-inset-2*buttonWidth-gap, y, buttonWidth, buttonHeight)

	// The window's border adds a pixel on each side of its client area.
	x, top := placement.position(width+2, height+2, margin)
	procCardSetWindowPos.Call(uintptr(c.hwnd), cardHwndTopmost,
		uintptr(uint32(x)), uintptr(uint32(top)), uintptr(uint32(width+2)), uintptr(uint32(height+2)),
		cardSWPNoActivate|cardSWPShowWindow)

	// A move or a withdrawn problem leaves a success's time running.
	if entry.lasting() {
		procKillTimer.Call(uintptr(c.hwnd), cardTimerID)
	} else if changed || announce {
		procSetTimer.Call(uintptr(c.hwnd), cardTimerID, uintptr(cardSuccessDuration.Milliseconds()), 0)
	}
	if entry.lasting() {
		announce = announce || c.unheard
		c.unheard = false
	}
	if announce {
		c.message.Announce()
		c.count.Announce()
	}
}

func (c *ResultCard) create() error {
	if err := registerCardClass(); err != nil {
		return err
	}
	hwnd, _, err := procCreateWindowExW.Call(
		cardWSExTopmost|cardWSExToolWindow|cardWSExNoActivate,
		uintptr(unsafe.Pointer(utf16(cardClassName))),
		uintptr(unsafe.Pointer(utf16(CardWindowTitle))),
		cardWSPopup|cardWSBorder,
		0, 0, 1, 1,
		0, 0, 0, 0,
	)
	if hwnd == 0 {
		return callError("CreateWindowExW(result card)", err)
	}
	card := windows.Handle(hwnd)
	fail := func(err error) error {
		if c.message != nil {
			c.message.ClearLive()
		}
		if c.count != nil {
			c.count.ClearLive()
		}
		procDestroyWindow.Call(hwnd)
		c.message, c.count = nil, nil
		return err
	}
	// The message may hold a long path with no spaces, which only an
	// edit-control style wraps.
	message, err := newLabel(card, "", ssNoPrefix|cardSSEditControl)
	if err != nil {
		return fail(err)
	}
	message.live = markLive(message.hwnd) == nil
	c.message = message
	if c.count, err = newLabel(card, "", ssNoPrefix); err != nil {
		return fail(err)
	}
	// The card never has the focus, so a screen reader hears of the problems
	// waiting behind this one only if the count is announced too.
	c.count.live = markLive(c.count.hwnd) == nil
	if c.openBtn, err = NewButton(card, OpenFolderAction, ButtonOptions{ID: cardOpenCommand}); err != nil {
		return fail(err)
	}
	if c.dismiss, err = NewButton(card, CardDismissAction, ButtonOptions{ID: cardDismissCommand}); err != nil {
		return fail(err)
	}
	if err := subclassCardButton(c.openBtn, card, cardOpenCommand); err != nil {
		return fail(err)
	}
	if err := subclassCardButton(c.dismiss, card, cardDismissCommand); err != nil {
		return fail(err)
	}
	c.hwnd = card
	cardStates[card] = c
	return nil
}

// applyFont gives the card's controls a font for the display it appears on.
func (c *ResultCard) applyFont(dpi uint32) {
	if c.font != 0 && c.fontDPI == dpi {
		return
	}
	description := cardLogFont{
		Height:  -int32((int64(cardLogicalFont)*int64(dpi) + 48) / 96),
		Weight:  cardFontNormal,
		CharSet: cardDefaultCharset,
		Quality: cardClearTypeQuality,
	}
	face, err := windows.UTF16FromString("Segoe UI")
	if err != nil {
		return
	}
	copy(description.Face[:], face)
	font, _, _ := procCardCreateFontIndirectW.Call(uintptr(unsafe.Pointer(&description)))
	if font == 0 {
		return
	}
	for _, control := range []Control{c.message.Control, c.count.Control, c.openBtn.Control, c.dismiss.Control} {
		control.SetFont(windows.Handle(font))
	}
	if c.font != 0 {
		procCardDeleteObject.Call(uintptr(c.font))
	}
	c.font = windows.Handle(font)
	c.fontDPI = dpi
}

// measure returns the height text takes at width in the card's font.
func (c *ResultCard) measure(text string, width int32) int32 {
	dc, _, _ := procCardGetDC.Call(uintptr(c.hwnd))
	if dc == 0 {
		return 0
	}
	defer procCardReleaseDC.Call(uintptr(c.hwnd), dc)
	if c.font != 0 {
		previous, _, _ := procCardSelectObject.Call(dc, uintptr(c.font))
		if previous != 0 {
			defer procCardSelectObject.Call(dc, previous)
		}
	}
	bounds := cardRect{Right: width}
	height, _, _ := procCardDrawTextW.Call(dc,
		uintptr(unsafe.Pointer(utf16(text))), ^uintptr(0),
		uintptr(unsafe.Pointer(&bounds)),
		cardDTCalcRect|cardDTWordBreak|cardDTNoPrefix|cardDTEditControl)
	return int32(height)
}

func subclassCardButton(button *Button, parent windows.Handle, id int32) error {
	state := &cardButton{parent: parent, id: id}
	cardButtons[button.hwnd] = state
	previous, _, err := procCardSetWindowLongPtrW.Call(uintptr(button.hwnd), cardGWLPWndProc, cardButtonCallback())
	if previous == 0 {
		delete(cardButtons, button.hwnd)
		return callError("SetWindowLongPtrW(result card button)", err)
	}
	state.previous = previous
	return nil
}

// cardButtonProc presses and releases a card button without the focus. A
// second quick click arrives as a double click and is reported as one, so
// Open Folder does not open twice and Dismiss still takes it.
func cardButtonProc(hwnd windows.Handle, message uint32, wParam uintptr, lParam uintptr) uintptr {
	button := cardButtons[hwnd]
	if button == nil || button.previous == 0 {
		r, _, _ := procCardDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
		return r
	}
	setPressed := func(pressed bool) {
		state := uintptr(0)
		if pressed {
			state = 1
		}
		procSendMessageW.Call(uintptr(hwnd), cardBMSetState, state, 0)
	}
	switch message {
	case cardWMLButtonDown, cardWMLButtonDblClk:
		button.pressed, button.double = true, message == cardWMLButtonDblClk
		track := cardTrackMouse{Flags: cardTMELeave, Track: hwnd}
		track.Size = uint32(unsafe.Sizeof(track))
		procCardTrackMouseEvent.Call(uintptr(unsafe.Pointer(&track)))
		setPressed(true)
		return 0
	case cardWMLButtonUp:
		if button.pressed {
			notification := uintptr(cardBNClicked)
			if button.double {
				notification = cardBNDoubleClicked
			}
			button.pressed = false
			setPressed(false)
			procSendMessageW.Call(uintptr(button.parent), cardWMCommand, uintptr(button.id)|notification<<16, uintptr(hwnd))
			return 0
		}
	case cardWMMouseLeave:
		if button.pressed {
			button.pressed = false
			setPressed(false)
		}
	case cardWMNCDestroy:
		delete(cardButtons, hwnd)
	}
	r, _, _ := procCardCallWindowProcW.Call(button.previous, uintptr(hwnd), uintptr(message), wParam, lParam)
	return r
}

func registerCardClass() error {
	registerCardOnce.Do(func() {
		instance, _, err := procCardGetModuleHandleW.Call(0)
		if instance == 0 {
			registerCardErr = callError("GetModuleHandleW", err)
			return
		}
		cursor, _, err := procCardLoadCursorW.Call(0, cardIDCArrow)
		if cursor == 0 {
			registerCardErr = callError("LoadCursorW", err)
			return
		}
		brush, _, err := procCardGetSysColorBrush.Call(cardColorWindow)
		if brush == 0 {
			registerCardErr = callError("GetSysColorBrush", err)
			return
		}
		class := cardWindowClass{
			LpfnWndProc:   windows.NewCallback(cardWindowProc),
			HInstance:     windows.Handle(instance),
			HCursor:       windows.Handle(cursor),
			HbrBackground: windows.Handle(brush),
			LpszClassName: utf16(cardClassName),
		}
		class.CbSize = uint32(unsafe.Sizeof(class))
		if atom, _, err := procCardRegisterClassExW.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
			registerCardErr = callError("RegisterClassExW(result card)", err)
		}
	})
	return registerCardErr
}

func cardWindowProc(hwnd windows.Handle, message uint32, wParam uintptr, lParam uintptr) uintptr {
	card := cardStates[hwnd]
	switch message {
	case cardWMMouseActivate:
		// A click on the card must not take the foreground from the
		// application the user is working in.
		return cardMANoActivate
	case cardWMCommand:
		if card != nil {
			notification := wParam >> 16
			switch wParam & 0xffff {
			case cardOpenCommand:
				// A click queued just before a capture hid the card can still
				// reach it; the Explorer window it would open could be in the
				// image (SCR-044).
				if notification == cardBNClicked && !card.suspended && card.path != "" && card.open != nil {
					card.open(card.path)
				}
				return 0
			case cardDismissCommand:
				// Dismissing several problems quickly clicks the same spot,
				// which a BS_NOTIFY button reports as a double click.
				if notification == cardBNClicked || notification == cardBNDoubleClicked {
					card.DismissProblem()
				}
				return 0
			}
		}
	case cardWMTimer:
		if card != nil && wParam == cardTimerID {
			procKillTimer.Call(uintptr(hwnd), cardTimerID)
			// A problem the success leaves showing was announced when it came,
			// unless it came during a capture, which present sees to.
			if card.queue.expire() {
				card.present(false)
			}
			return 0
		}
	case cardWMClose:
		if card != nil {
			card.DismissProblem()
		}
		return 0
	case cardWMCtlColorStatic:
		procCardSetBkMode.Call(wParam, cardTransparent)
		color, _, _ := procCardGetSysColor.Call(cardColorWindowText)
		procCardSetTextColor.Call(wParam, color)
		brush, _, _ := procCardGetSysColorBrush.Call(cardColorWindow)
		return brush
	case cardWMDestroy:
		delete(cardStates, hwnd)
		return 0
	}
	r, _, _ := procCardDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
	return r
}
