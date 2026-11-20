//go:build windows

package ui

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var nextTimerID atomic.Uintptr

type Control struct {
	hwnd windows.Handle
}

func (c Control) Handle() windows.Handle {
	return c.hwnd
}

func (c Control) SetText(text string) {
	procSetWindowTextW.Call(uintptr(c.hwnd), uintptr(unsafe.Pointer(utf16(text))))
}

func (c Control) Text() string {
	length, _, _ := procGetWindowTextLengthW.Call(uintptr(c.hwnd))
	buffer := make([]uint16, int(length)+1)
	procGetWindowTextW.Call(uintptr(c.hwnd), uintptr(unsafe.Pointer(&buffer[0])), length+1)
	return windows.UTF16ToString(buffer)
}

func (c Control) SetFont(font windows.Handle) {
	procSendMessageW.Call(uintptr(c.hwnd), wmSetFont, uintptr(font), 1)
}

func (c Control) Move(x, y, width, height int32) {
	procMoveWindow.Call(uintptr(c.hwnd), uintptr(x), uintptr(y), uintptr(width), uintptr(height), 1)
}

func (c Control) Show(visible bool) {
	command := uintptr(swHide)
	if visible {
		command = swShow
	}
	procShowWindow.Call(uintptr(c.hwnd), command)
}

func (c Control) Enable(enabled bool) {
	procEnableWindow.Call(uintptr(c.hwnd), boolPointer(enabled))
}

func (c Control) Focus() {
	procSetFocus.Call(uintptr(c.hwnd))
}

// HasFocus reports whether the control has the keyboard focus, so a view
// that is about to disable it can move the focus on first instead of
// stranding the keyboard on a disabled control.
func (c Control) HasFocus() bool {
	focused, _, _ := procGetFocus.Call()
	return windows.Handle(focused) == c.hwnd
}

type Label struct {
	Control

	// live is whether the label is a UI Automation live region, as
	// NewLiveLabel describes.
	live bool
}

func NewLabel(parent windows.Handle, text string) (*Label, error) {
	return newLabel(parent, text, ssNoPrefix)
}

// NewLiveLabel creates a label for text that changes to report what
// happened — a result, a validation message, the reason an item cannot be
// used — and marks it as a polite UI Automation live region, so a screen
// reader announces each new text once it finishes what it is reading,
// without moving the keyboard focus. Static text is otherwise read only when
// the user navigates to it, so a result shown only there would go unheard.
// Marking is best effort: a label that cannot be marked, where the
// accessibility services are unavailable, still shows its text.
func NewLiveLabel(parent windows.Handle, text string) (*Label, error) {
	label, err := newLabel(parent, text, ssNoPrefix)
	if err != nil {
		return nil, err
	}
	label.live = markLive(label.hwnd) == nil
	return label, nil
}

// SetText replaces the label's text. A live label announces text that
// changed, so setting the same text again, as a relayout or a validation
// on every keystroke does, is not announced twice; clearing it is not
// announced.
func (l *Label) SetText(text string) {
	changed := l.live && text != "" && text != l.Text()
	l.Control.SetText(text)
	if changed {
		l.Announce()
	}
}

// SetTextQuietly replaces the label's text without announcing it, for a
// caller that announces the text itself (Announce) or whose text is not
// news: progress that changes faster than a screen reader could read it,
// instructions that return while the user types, or a message another live
// region already announces.
func (l *Label) SetTextQuietly(text string) {
	l.Control.SetText(text)
}

// Announce asks a screen reader to read a live label's current text now,
// whether or not it changed — as when the window that shows it appears, or
// a refused edit is refused again. It does nothing for other labels, or
// while the label is empty, as there is nothing to read.
func (l *Label) Announce() {
	if l.live && l.Text() != "" {
		announceLiveRegion(l.hwnd)
	}
}

// ClearLive removes a live label's live-region marking, after which it is a
// plain label. Call it before destroying a live label that does not live as
// long as the process, as a capture session's toolbar does: oleacc.dll keeps
// the marking by window handle, so the markings of destroyed windows would
// otherwise build up, and a later window given the same handle would be
// reported as a live region.
func (l *Label) ClearLive() {
	if l.live && unmarkLive(l.hwnd) == nil {
		l.live = false
	}
}

func NewMnemonicLabel(parent windows.Handle, text string) (*Label, error) {
	return newLabel(parent, text, 0)
}

// NewGroupLabel creates a label that starts a group (WS_GROUP), for the label
// laid out directly after a radio group (UX-006). The dialog manager moves
// the arrow keys through the run of controls from a group's first button to
// the next control that starts a group, wrapping at its ends, and stop at a
// label, so a group that ran on into the labels and fields after it would not
// wrap: Down from its last button would stop at the label after it, and Up
// from its first button would stop at the run's last control or, when that
// is a field, move the focus there, selecting nothing.
func NewGroupLabel(parent windows.Handle, text string) (*Label, error) {
	return newLabel(parent, text, ssNoPrefix|wsGroup)
}

// NewGroupMnemonicLabel is NewMnemonicLabel for a label that starts a group,
// as NewGroupLabel describes.
func NewGroupMnemonicLabel(parent windows.Handle, text string) (*Label, error) {
	return newLabel(parent, text, wsGroup)
}

func newMnemonicLabel(parent windows.Handle, text string) (*Label, error) {
	return NewMnemonicLabel(parent, text)
}

func newLabel(parent windows.Handle, text string, style uintptr) (*Label, error) {
	hwnd, err := createControl(parent, "STATIC", text, wsChild|wsVisible|style, 0, 0)
	if err != nil {
		return nil, err
	}
	return &Label{Control: Control{hwnd: hwnd}}, nil
}

type ButtonOptions struct {
	ID       int32
	Primary  bool
	Radio    bool
	Group    bool
	Disabled bool
}

type Button struct {
	Control
}

// NewButton creates a push button, or with Radio an automatic radio button.
// A radio group is the run of radio buttons from one created with Group to
// the next control created with Group, which must directly follow its last
// button, as NewGroupLabel describes. Its first button is the group's tab
// stop until SetChecked checks one, as SetChecked describes.
func NewButton(parent windows.Handle, text string, options ButtonOptions) (*Button, error) {
	style := uintptr(wsChild | wsVisible | wsTabStop | bsNotify)
	if options.Primary {
		style |= bsDefPushButton
	}
	if options.Radio {
		style |= bsAutoRadioButton
		if !options.Group {
			style &^= wsTabStop
		}
	}
	if options.Group {
		style |= wsGroup
	}
	if options.Disabled {
		style |= wsDisabled
	}
	hwnd, err := createControl(parent, "BUTTON", text, style, 0, options.ID)
	if err != nil {
		return nil, err
	}
	return &Button{Control: Control{hwnd: hwnd}}, nil
}

// SetChecked checks or unchecks a radio button or check box. BM_SETCHECK
// also makes a radio button a tab stop exactly while it is checked — Windows
// sets WS_TABSTOP on the radio button it checks and clears it on one it
// unchecks — so Tab enters the group at the selected button and leaves at
// the next control, while the arrow keys move the selection within the
// group (UX-006). That matters because the dialog manager stops Tab at every
// tab stop, and a radio button that gains the focus unchecked reports a click
// (BN_CLICKED): tabbing through a group of tab stops would select each button
// in turn, and a group label's mnemonic, which focuses the next tab stop,
// would select the group's first button. Call it for every button of a
// group, as refreshing a group's selection does, so no unchecked button keeps
// the tab stop it was created with.
func (b *Button) SetChecked(checked bool) {
	procSendMessageW.Call(uintptr(b.hwnd), bmSetCheck, boolPointer(checked), 0)
}

type ComboBox struct {
	Control
}

func NewComboBox(parent windows.Handle, id int32) (*ComboBox, error) {
	hwnd, err := createControl(parent, "COMBOBOX", "", wsChild|wsVisible|wsTabStop|cbsDropDownList|cbsNoIntegralHeight, 0, id)
	if err != nil {
		return nil, err
	}
	return &ComboBox{Control{hwnd: hwnd}}, nil
}

func (c *ComboBox) Add(text string) {
	procSendMessageW.Call(uintptr(c.hwnd), cbAddString, 0, uintptr(unsafe.Pointer(utf16(text))))
}

func (c *ComboBox) Reset() {
	procSendMessageW.Call(uintptr(c.hwnd), cbResetContent, 0, 0)
}

func (c *ComboBox) Select(index int) {
	procSendMessageW.Call(uintptr(c.hwnd), cbSetCurSel, uintptr(index), 0)
}

func (c *ComboBox) Selected() int {
	index, _, _ := procSendMessageW.Call(uintptr(c.hwnd), cbGetCurSel, 0, 0)
	return int(int32(index))
}

// Count returns the number of items in the combo box's list, as itemCount
// describes.
func (c *ComboBox) Count() int {
	return itemCount(c.hwnd, cbGetCount)
}

// ItemText returns the text of the item at index, as itemText describes.
func (c *ComboBox) ItemText(index int) string {
	return itemText(c.hwnd, cbGetLBTextLen, cbGetLBText, index)
}

// itemCount returns the number of items a list or combo box holds, by its
// count message (LB_GETCOUNT, CB_GETCOUNT), or 0 when the control reports
// that it cannot count them (LB_ERR, CB_ERR), so a caller can size a slice
// by it.
func itemCount(hwnd windows.Handle, countMessage uintptr) int {
	count, _, _ := procSendMessageW.Call(uintptr(hwnd), countMessage, 0, 0)
	return max(0, int(int32(count)))
}

// itemText returns the text of a list or combo box item at index, by its
// length and text messages (LB_GETTEXTLEN and LB_GETTEXT, CB_GETLBTEXTLEN
// and CB_GETLBTEXT), or "" for an index out of range or an item that cannot
// be read. Call it on the thread that owns the control, as every control is
// driven: the text message takes no buffer size, so the buffer is sized by
// the length message, and on the owning thread nothing can change the item
// between the two messages, which go straight to its window procedure; from
// another thread a change dispatched between them could overrun the buffer.
func itemText(hwnd windows.Handle, lengthMessage, textMessage uintptr, index int) string {
	length, _, _ := procSendMessageW.Call(uintptr(hwnd), lengthMessage, uintptr(index), 0)
	if int32(length) <= 0 {
		return ""
	}
	buffer := make([]uint16, int(length)+1)
	procSendMessageW.Call(uintptr(hwnd), textMessage, uintptr(index), uintptr(unsafe.Pointer(&buffer[0])))
	return windows.UTF16ToString(buffer)
}

type TextInput struct {
	Label *Label
	Edit  Control
	Error *Label

	id int32
}

func NewTextInput(parent windows.Handle, field string, id int32) (*TextInput, error) {
	label, err := newMnemonicLabel(parent, field)
	if err != nil {
		return nil, err
	}
	edit, err := createControl(parent, "EDIT", "", wsChild|wsVisible|wsTabStop|esAutoHScroll, wsExClientEdge, id)
	if err != nil {
		procDestroyWindow.Call(uintptr(label.Handle()))
		return nil, err
	}
	validation, err := NewLiveLabel(parent, "")
	if err != nil {
		procDestroyWindow.Call(uintptr(edit))
		procDestroyWindow.Call(uintptr(label.Handle()))
		return nil, err
	}
	return &TextInput{Label: label, Edit: Control{hwnd: edit}, Error: validation, id: id}, nil
}

func (t *TextInput) SetFont(font windows.Handle) {
	t.Label.SetFont(font)
	t.Edit.SetFont(font)
	t.Error.SetFont(font)
}

func (t *TextInput) Move(x, y, width int32, metrics DimensionMetrics) {
	t.Label.Move(x, y, width, metrics.LabelHeight)
	t.Edit.Move(x, y+metrics.LabelHeight+metrics.Gap, width, metrics.EditHeight)
	t.Error.Move(x, y+metrics.LabelHeight+metrics.Gap+metrics.EditHeight+metrics.Gap, width, metrics.ErrorHeight)
}

func (t *TextInput) Show(visible bool) {
	t.Label.Show(visible)
	t.Edit.Show(visible)
	t.Error.Show(visible)
}

func (t *TextInput) Enable(enabled bool) {
	t.Edit.Enable(enabled)
}

func (t *TextInput) Focus() {
	t.Edit.Focus()
}

func (t *TextInput) HandleCommand(id int32, notification uint16) bool {
	if id != t.id || (notification != enChange && notification != enKillFocus) {
		return false
	}
	t.Error.SetText("")
	return true
}

type DimensionMetrics struct {
	LabelHeight int32
	EditHeight  int32
	ErrorHeight int32
	Gap         int32
}

func (m DimensionMetrics) Height() int32 {
	return m.LabelHeight + m.EditHeight + m.ErrorHeight + 2*m.Gap
}

type DimensionInput struct {
	Label *Label
	Edit  Control
	Error *Label

	field string
	id    int32
}

func NewDimensionInput(parent windows.Handle, field string, id int32) (*DimensionInput, error) {
	label, err := newMnemonicLabel(parent, field+DimensionUnitSuffix)
	if err != nil {
		return nil, err
	}
	edit, err := createControl(parent, "EDIT", "", wsChild|wsVisible|wsTabStop|esAutoHScroll, wsExClientEdge, id)
	if err != nil {
		procDestroyWindow.Call(uintptr(label.Handle()))
		return nil, err
	}
	validation, err := NewLiveLabel(parent, "")
	if err != nil {
		procDestroyWindow.Call(uintptr(edit))
		procDestroyWindow.Call(uintptr(label.Handle()))
		return nil, err
	}
	return &DimensionInput{
		Label: label,
		Edit:  Control{hwnd: edit},
		Error: validation,
		field: strings.ReplaceAll(field, "&", ""),
		id:    id,
	}, nil
}

func (d *DimensionInput) SetFont(font windows.Handle) {
	d.Label.SetFont(font)
	d.Edit.SetFont(font)
	d.Error.SetFont(font)
}

func (d *DimensionInput) Move(x, y, width int32, metrics DimensionMetrics) {
	d.Label.Move(x, y, width, metrics.LabelHeight)
	d.Edit.Move(x, y+metrics.LabelHeight+metrics.Gap, width, metrics.EditHeight)
	d.Error.Move(x, y+metrics.LabelHeight+metrics.Gap+metrics.EditHeight+metrics.Gap, width, metrics.ErrorHeight)
}

func (d *DimensionInput) Show(visible bool) {
	d.Label.Show(visible)
	d.Edit.Show(visible)
	d.Error.Show(visible)
}

func (d *DimensionInput) Focus() {
	d.Edit.Focus()
}

func (d *DimensionInput) Enable(enabled bool) {
	d.Edit.Enable(enabled)
}

func (d *DimensionInput) Validate() (int, error) {
	value, err := ParseDimension(d.field, d.Edit.Text())
	if err != nil {
		d.Error.SetText(FormatError(ErrorValidation, err))
		return 0, err
	}
	d.Error.SetText("")
	return value, nil
}

func (d *DimensionInput) HandleCommand(id int32, notification uint16) bool {
	if id != d.id || (notification != enChange && notification != enKillFocus) {
		return false
	}
	d.Validate()
	return true
}

// NewEdit creates a bare single-line edit control for compact surfaces such
// as the selection toolbar, where TextInput's stacked label and error labels
// do not fit; validation presentation stays with the caller.
func NewEdit(parent windows.Handle, id int32) (*Control, error) {
	hwnd, err := createControl(parent, "EDIT", "", wsChild|wsVisible|wsTabStop|esAutoHScroll, wsExClientEdge, id)
	if err != nil {
		return nil, err
	}
	return &Control{hwnd: hwnd}, nil
}

// ReadOnlyText displays a value such as a chosen path. It is keyboard
// focusable and horizontally scrollable so long values stay readable, but it
// cannot be edited.
type ReadOnlyText struct {
	Control
}

func NewReadOnlyText(parent windows.Handle, id int32) (*ReadOnlyText, error) {
	hwnd, err := createControl(parent, "EDIT", "", wsChild|wsVisible|wsTabStop|esAutoHScroll|esReadOnly, wsExClientEdge, id)
	if err != nil {
		return nil, err
	}
	return &ReadOnlyText{Control{hwnd: hwnd}}, nil
}

// ListBox is a multi-select list control: extended selection (Shift/Ctrl)
// with change notifications, used for item inventories such as the Image
// Resizer source list. Item data stays with the caller, indexed by position.
type ListBox struct {
	Control
}

func NewListBox(parent windows.Handle, id int32) (*ListBox, error) {
	hwnd, err := createControl(parent, "LISTBOX", "", wsChild|wsVisible|wsTabStop|wsVScroll|lbsNotify|lbsExtendedSel|lbsNoIntegralHeight, wsExClientEdge, id)
	if err != nil {
		return nil, err
	}
	return &ListBox{Control{hwnd: hwnd}}, nil
}

func (l *ListBox) Add(text string) {
	procSendMessageW.Call(uintptr(l.hwnd), lbAddString, 0, uintptr(unsafe.Pointer(utf16(text))))
}

func (l *ListBox) Reset() {
	procSendMessageW.Call(uintptr(l.hwnd), lbResetContent, 0, 0)
}

// Count returns the number of items in the list, as itemCount describes.
func (l *ListBox) Count() int {
	return itemCount(l.hwnd, lbGetCount)
}

// ItemText returns the text of the item at index, as itemText describes.
func (l *ListBox) ItemText(index int) string {
	return itemText(l.hwnd, lbGetTextLen, lbGetText, index)
}

// SetItemText replaces one item's text in place. The list box has no
// set-text message, so the item is deleted and reinserted; its selection,
// the caret (focus) item, the extended-selection anchor, and the scroll
// position are restored around that, so a background status update never
// disturbs what the user selected or is looking at. Out-of-range indexes are
// ignored.
//
// Setting the caret raises an accessibility focus event even when the
// caret does not move, which makes a screen reader announce the focused row
// again. So the selection is restored with LB_SELITEMRANGE, which leaves the
// caret alone, and the caret and anchor only where the reinsertion moved
// them: rewriting any other row raises no focus event, and rewriting the
// focused row raises one, announcing its new text.
func (l *ListBox) SetItemText(index int, text string) {
	if index < 0 || index >= l.Count() {
		return
	}
	hwnd := uintptr(l.hwnd)
	selected, _, _ := procSendMessageW.Call(hwnd, lbGetSel, uintptr(index), 0)
	caret, _, _ := procSendMessageW.Call(hwnd, lbGetCaretIndex, 0, 0)
	anchor, _, _ := procSendMessageW.Call(hwnd, lbGetAnchorIndex, 0, 0)
	top, _, _ := procSendMessageW.Call(hwnd, lbGetTopIndex, 0, 0)
	procSendMessageW.Call(hwnd, wmSetRedraw, 0, 0)
	procSendMessageW.Call(hwnd, lbDeleteString, uintptr(index), 0)
	procSendMessageW.Call(hwnd, lbInsertString, uintptr(index), uintptr(unsafe.Pointer(utf16(text))))
	if int32(selected) > 0 {
		if index <= 0xffff {
			// The range is packed into 16-bit halves.
			procSendMessageW.Call(hwnd, lbSelItemRange, 1, uintptr(index)|uintptr(index)<<16)
		} else {
			// Beyond the 16-bit range LB_SETSEL moves the caret, which is
			// then restored below.
			procSendMessageW.Call(hwnd, lbSetSel, 1, uintptr(index))
		}
	}
	if now, _, _ := procSendMessageW.Call(hwnd, lbGetAnchorIndex, 0, 0); int32(anchor) >= 0 && now != anchor {
		procSendMessageW.Call(hwnd, lbSetAnchorIndex, anchor, 0)
	}
	if now, _, _ := procSendMessageW.Call(hwnd, lbGetCaretIndex, 0, 0); int32(caret) >= 0 && now != caret {
		procSendMessageW.Call(hwnd, lbSetCaretIndex, caret, 0)
	}
	procSendMessageW.Call(hwnd, lbSetTopIndex, top, 0)
	procSendMessageW.Call(hwnd, wmSetRedraw, 1, 0)
	procInvalidateRect.Call(hwnd, 0, 1)
}

// SetSelected selects or clears a single item without disturbing the other
// selections, so callers can restore the selection after rebuilding a list.
func (l *ListBox) SetSelected(index int, selected bool) {
	procSendMessageW.Call(uintptr(l.hwnd), lbSetSel, boolPointer(selected), uintptr(index))
}

// SelectedIndices returns the indexes of every selected item in ascending
// order.
func (l *ListBox) SelectedIndices() []int {
	count, _, _ := procSendMessageW.Call(uintptr(l.hwnd), lbGetSelCount, 0, 0)
	if int32(count) <= 0 {
		return nil
	}
	indices := make([]int32, int(count))
	actual, _, _ := procSendMessageW.Call(uintptr(l.hwnd), lbGetSelItems, count, uintptr(unsafe.Pointer(&indices[0])))
	result := make([]int, 0, int(int32(actual)))
	for _, index := range indices[:int(int32(actual))] {
		result = append(result, int(index))
	}
	return result
}

type StatusArea struct {
	*Label

	parent windows.Handle
	timer  uintptr
	// revision counts the notices shown and cleared.
	revision uint64
}

// Revision identifies the notice on display: it changes whenever Show,
// Update, or Clear replaces the notice, including when a timed notice
// expires, even if the new text equals the old. A caller that records the
// revision after showing its notice can later tell whether that notice is
// still the one shown.
func (s *StatusArea) Revision() uint64 {
	return s.revision
}

// NewStatusArea creates the status area on a live label (NewLiveLabel), so
// a screen reader announces every notice Show presents.
func NewStatusArea(parent windows.Handle) (*StatusArea, error) {
	label, err := NewLiveLabel(parent, "")
	if err != nil {
		return nil, err
	}
	return &StatusArea{Label: label, parent: parent}, nil
}

// Height returns the status area's height at width, as a view lays it out:
// its notice's, as measure measures text, and room for two lines while the
// notice is shorter or there is none, so the controls below it move only
// for a longer notice. scale scales a 96-DPI length to the window's DPI.
func (s *StatusArea) Height(width int32, scale func(int32) int32, measure func(string, int32) int32) int32 {
	return max(scale(48), measure(s.Text(), width))
}

// Show replaces the notice and announces it to a screen reader — even when
// its text equals the notice it replaces, as an identical result is still a
// new one — and, with a positive timeout, clears it once the timeout
// passes.
func (s *StatusArea) Show(notice Notice, timeout time.Duration) error {
	s.Update(notice)
	if notice.Text() == "" {
		return nil
	}
	s.Announce()
	if timeout <= 0 {
		return nil
	}
	milliseconds := timeout.Milliseconds()
	if milliseconds < 1 {
		milliseconds = 1
	}
	if milliseconds > int64(^uint32(0)) {
		milliseconds = int64(^uint32(0))
	}
	timerID := nextTimerID.Add(1)
	if timerID == 0 {
		timerID = nextTimerID.Add(1)
	}
	timer, _, err := procSetTimer.Call(uintptr(s.parent), timerID, uintptr(milliseconds), 0)
	if timer == 0 {
		return callError("SetTimer", err)
	}
	s.timer = timer
	return nil
}

// Update replaces the notice as Show does without a timeout, but does not
// announce it: for progress that changes faster than a screen reader could
// read it, such as a resize job's count for every image, whose start and
// end are announced through Show instead.
func (s *StatusArea) Update(notice Notice) {
	s.stopTimer()
	s.revision++
	s.SetTextQuietly(notice.Text())
}

// Clear removes the notice, as Update with no notice does.
func (s *StatusArea) Clear() {
	s.Update(Notice{})
}

// ClearShown removes the notice when it is still one of those whose
// revisions a caller recorded once it showed them (Revision), as when the
// setting a notice reports was replaced, and keeps any notice shown since.
// A revision of 0, recorded before anything was shown, matches none.
func (s *StatusArea) ClearShown(revisions ...uint64) {
	for _, revision := range revisions {
		if revision != 0 && revision == s.revision {
			s.Clear()
			return
		}
	}
}

func (s *StatusArea) HandleTimer(id uintptr) bool {
	if id == 0 || id != s.timer {
		return false
	}
	s.Clear()
	return true
}

func (s *StatusArea) Destroy() {
	s.stopTimer()
}

func (s *StatusArea) stopTimer() {
	if s.timer != 0 {
		procKillTimer.Call(uintptr(s.parent), s.timer)
		s.timer = 0
	}
}

// Confirm asks the user a yes-or-no question about the parent window, its
// safe answer No the default (UX-002), and reports whether the answer was
// Yes. The question's modal loop dispatches the window's messages, so the
// window can be destroyed while it is open — as the end of the Windows
// session destroys the main window (WM_ENDSESSION) — and the answer is then
// No, even one given just before, so the caller does nothing more with a
// window that is gone. Windows ends the question with no answer once its
// owner is destroyed; the parent is also checked once it returns, so every
// caller has that guarantee whatever the question's answer.
func Confirm(parent windows.Handle, title, message string) bool {
	answer, _, _ := procMessageBoxW.Call(
		uintptr(parent),
		uintptr(unsafe.Pointer(utf16(message))),
		uintptr(unsafe.Pointer(utf16(title))),
		mbYesNo|mbIconWarning|mbDefButton2,
	)
	if parent != 0 {
		if alive, _, _ := procIsWindow.Call(uintptr(parent)); alive == 0 {
			return false
		}
	}
	return answer == idYes
}

func createControl(parent windows.Handle, className, text string, style, extendedStyle uintptr, id int32) (windows.Handle, error) {
	hwnd, _, err := procCreateWindowExW.Call(
		extendedStyle,
		uintptr(unsafe.Pointer(utf16(className))),
		uintptr(unsafe.Pointer(utf16(text))),
		style,
		0, 0, 0, 0,
		uintptr(parent), uintptr(id), 0, 0,
	)
	if hwnd == 0 {
		if err == windows.ERROR_SUCCESS {
			return 0, fmt.Errorf("CreateWindowExW(%s) failed", className)
		}
		return 0, fmt.Errorf("CreateWindowExW(%s): %w", className, err)
	}
	return windows.Handle(hwnd), nil
}

func utf16(value string) *uint16 {
	result, err := windows.UTF16PtrFromString(value)
	if err != nil {
		panic(err)
	}
	return result
}

func boolPointer(value bool) uintptr {
	if value {
		return 1
	}
	return 0
}

func callError(operation string, err error) error {
	if err == windows.ERROR_SUCCESS {
		return fmt.Errorf("%s failed", operation)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
