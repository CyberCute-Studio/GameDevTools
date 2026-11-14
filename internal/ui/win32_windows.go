//go:build windows

package ui

import "golang.org/x/sys/windows"

const (
	wmSetFont = 0x0030

	wsChild    = 0x40000000
	wsVisible  = 0x10000000
	wsDisabled = 0x08000000
	wsTabStop  = 0x00010000
	wsGroup    = 0x00020000

	wsExClientEdge = 0x00000200

	bsNotify            = 0x00004000
	bsDefPushButton     = 0x00000001
	bsAutoRadioButton   = 0x00000009
	ssNoPrefix          = 0x00000080
	esAutoHScroll       = 0x00000080
	esReadOnly          = 0x00000800
	enChange            = 0x0300
	enKillFocus         = 0x0200
	enSetFocus          = 0x0100
	bmSetCheck          = 0x00f1
	cbsDropDownList     = 0x0003
	cbsNoIntegralHeight = 0x0400
	cbAddString         = 0x0143
	cbResetContent      = 0x014b
	cbGetCurSel         = 0x0147
	cbSetCurSel         = 0x014e
	cbnSelChange        = 1

	swHide = 0
	swShow = 5

	mbYesNo       = 0x0004
	mbIconWarning = 0x0030
	mbDefButton2  = 0x0100
	idYes         = 6
)

// NotificationEditKillFocus is the edit-control notification sent when the
// field loses keyboard focus, exported so feature modules can commit a field
// value at the same moment the shared input clears its validation text.
const NotificationEditKillFocus = enKillFocus

// NotificationEditSetFocus is the edit-control notification sent when the
// field gains keyboard focus, exported so feature modules can suspend side
// effects of the field's committed value — such as its global hotkey — while
// the user edits a replacement.
const NotificationEditSetFocus = enSetFocus

// NotificationEditChange is the edit-control notification sent when the
// field's text changes, exported so feature modules can clear stale
// validation text while the user types.
const NotificationEditChange = enChange

var (
	user32 = windows.NewLazySystemDLL("user32.dll")

	procCreateWindowExW      = user32.NewProc("CreateWindowExW")
	procDestroyWindow        = user32.NewProc("DestroyWindow")
	procSetWindowTextW       = user32.NewProc("SetWindowTextW")
	procGetWindowTextW       = user32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW = user32.NewProc("GetWindowTextLengthW")
	procSendMessageW         = user32.NewProc("SendMessageW")
	procMoveWindow           = user32.NewProc("MoveWindow")
	procShowWindow           = user32.NewProc("ShowWindow")
	procEnableWindow         = user32.NewProc("EnableWindow")
	procSetFocus             = user32.NewProc("SetFocus")
	procSetTimer             = user32.NewProc("SetTimer")
	procKillTimer            = user32.NewProc("KillTimer")
	procMessageBoxW          = user32.NewProc("MessageBoxW")
)
