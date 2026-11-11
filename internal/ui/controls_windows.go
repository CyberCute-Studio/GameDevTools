//go:build windows

package ui

import (
	"fmt"
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
	hwnd, err := createControl(parent, "STATIC", text, wsChild|wsVisible, 0, 0)
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
	label, err := NewLabel(parent, field+" (px)")
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
		field: field,
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

func (s *StatusArea) Show(notice Notice, timeout time.Duration) {
	s.stopTimer()
	s.SetText(notice.Text())
	if timeout <= 0 {
		return
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
	s.timer, _, _ = procSetTimer.Call(uintptr(s.parent), timerID, uintptr(milliseconds), 0)
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
