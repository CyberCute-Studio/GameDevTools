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

type Label struct {
	Control
}

func NewLabel(parent windows.Handle, text string) (*Label, error) {
	return newLabel(parent, text, ssNoPrefix)
}

func NewMnemonicLabel(parent windows.Handle, text string) (*Label, error) {
	return newLabel(parent, text, 0)
}

func newMnemonicLabel(parent windows.Handle, text string) (*Label, error) {
	return NewMnemonicLabel(parent, text)
}

func newLabel(parent windows.Handle, text string, style uintptr) (*Label, error) {
	hwnd, err := createControl(parent, "STATIC", text, wsChild|wsVisible|style, 0, 0)
	if err != nil {
		return nil, err
	}
	return &Label{Control{hwnd: hwnd}}, nil
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

func NewButton(parent windows.Handle, text string, options ButtonOptions) (*Button, error) {
	style := uintptr(wsChild | wsVisible | wsTabStop | bsNotify)
	if options.Primary {
		style |= bsDefPushButton
	}
	if options.Radio {
		style |= bsAutoRadioButton
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
	return &Button{Control{hwnd: hwnd}}, nil
}

// SetChecked checks or unchecks a radio button or check box.
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
	validation, err := NewLabel(parent, "")
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
	label, err := newMnemonicLabel(parent, field+" (px)")
	if err != nil {
		return nil, err
	}
	edit, err := createControl(parent, "EDIT", "", wsChild|wsVisible|wsTabStop|esAutoHScroll, wsExClientEdge, id)
	if err != nil {
		procDestroyWindow.Call(uintptr(label.Handle()))
		return nil, err
	}
	validation, err := NewLabel(parent, "")
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

type StatusArea struct {
	*Label

	parent windows.Handle
	timer  uintptr
}

func NewStatusArea(parent windows.Handle) (*StatusArea, error) {
	label, err := NewLabel(parent, "")
	if err != nil {
		return nil, err
	}
	return &StatusArea{Label: label, parent: parent}, nil
}

func (s *StatusArea) Show(notice Notice, timeout time.Duration) error {
	s.stopTimer()
	s.SetText(notice.Text())
	if notice.Text() == "" || timeout <= 0 {
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

func (s *StatusArea) Clear() {
	s.stopTimer()
	s.SetText("")
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

func Confirm(parent windows.Handle, title, message string) bool {
	answer, _, _ := procMessageBoxW.Call(
		uintptr(parent),
		uintptr(unsafe.Pointer(utf16(message))),
		uintptr(unsafe.Pointer(utf16(title))),
		mbYesNo|mbIconWarning|mbDefButton2,
	)
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
