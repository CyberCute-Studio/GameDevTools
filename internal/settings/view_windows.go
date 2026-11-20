//go:build windows

package settings

import (
	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// restoreDefaultsCommand identifies Restore Default Settings, clear of the
// other modules' command ranges.
const restoreDefaultsCommand = 801

// NativeView is the Settings module's view: Restore Default Settings, which
// asks first and then restores the default settings for the running
// application (SET-005) through the function the shell hands it
// (NativeRestoreHandler), and the status area that reports the outcome.
type NativeView struct {
	parent  windows.Handle
	confirm func(windows.Handle, string, string) bool
	restore func() ui.Notice
	// destroyed is set once NativeDestroy has run: the end of the Windows
	// session destroys the window while the restore's question is open
	// (WM_ENDSESSION), and the answer must then change nothing.
	destroyed bool

	description *ui.Label
	restoreAll  *ui.Button
	status      *ui.StatusArea
}

func newNativeView() *NativeView {
	return &NativeView{confirm: ui.Confirm}
}

// NativeRestoreHandler receives the shell's restore of the default
// settings, which Restore Default Settings runs once the user confirms it.
func (n *NativeView) NativeRestoreHandler(restore func() ui.Notice) {
	n.restore = restore
}

func (n *NativeView) NativeCreate(parent windows.Handle) error {
	n.parent = parent
	var err error
	if n.description, err = ui.NewLabel(parent, ui.SettingsRestoreDescription); err != nil {
		return err
	}
	if n.restoreAll, err = ui.NewButton(parent, ui.RestoreDefaultsAction, ui.ButtonOptions{ID: restoreDefaultsCommand, Group: true}); err != nil {
		return err
	}
	if n.status, err = ui.NewStatusArea(parent); err != nil {
		return err
	}
	return nil
}

func (n *NativeView) NativeFont(font windows.Handle) {
	n.description.SetFont(font)
	n.restoreAll.SetFont(font)
	n.status.SetFont(font)
}

func (n *NativeView) NativeShow(visible bool) {
	n.description.Show(visible)
	n.restoreAll.Show(visible)
	n.status.Label.Show(visible)
}

func (n *NativeView) NativeHeight(width int32, scale func(int32) int32, measure func(string, int32) int32) int32 {
	description, status := n.heights(width, scale, measure)
	return description + scale(12) + scale(36) + scale(12) + status
}

func (n *NativeView) NativeLayout(x, y, width int32, scale func(int32) int32, measure func(string, int32) int32) {
	description, status := n.heights(width, scale, measure)
	n.description.Move(x, y, width, description)
	y += description + scale(12)
	n.restoreAll.Move(x, y, min(scale(260), width), scale(36))
	y += scale(36) + scale(12)
	n.status.Move(x, y, width, status)
}

// heights measures the description and the status area at width, the
// status area keeping room for two lines while it is empty, as the other
// modules' status areas do.
func (n *NativeView) heights(width int32, scale func(int32) int32, measure func(string, int32) int32) (int32, int32) {
	return max(scale(20), measure(ui.SettingsRestoreDescription, width)), max(scale(48), measure(n.status.Text(), width))
}

func (n *NativeView) NativeCommand(id int32, notification uint16) bool {
	if id != restoreDefaultsCommand {
		return false
	}
	if notification == 0 {
		n.restoreDefaults()
	}
	return true
}

// restoreDefaults asks the user to confirm Restore Default Settings, its
// safe answer the default (UX-002), and then restores the default settings
// and shows the outcome, which a screen reader announces.
func (n *NativeView) restoreDefaults() {
	if n.restore == nil {
		return
	}
	if !n.confirm(n.parent, ui.RestoreDefaultsTitle, ui.RestoreDefaultsConfirmation) || n.destroyed {
		return
	}
	_ = n.status.Show(n.restore(), 0)
}

func (n *NativeView) NativeDestroy() {
	n.destroyed = true
	n.status.Destroy()
}
