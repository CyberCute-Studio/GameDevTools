//go:build windows

package screenshot

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// TestNativeViewRestoreDefaultsShowsAndRegistersTheDefaults pins the
// Screenshot half of Restore Default Settings (SET-005): the capture options
// return to their defaults in the controls, the default global shortcut is
// registered in place of the one the settings no longer name, and the
// status area keeps the last capture's result.
func TestNativeViewRestoreDefaultsShowsAndRegistersTheDefaults(t *testing.T) {
	view, fake := newHotkeyTestView(t, filepath.Join(t.TempDir(), "settings.json"))
	defaults := view.options.Options()
	shortcut, err := ParseShortcut("Ctrl+Shift+F9")
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func() error{
		func() error { return view.options.SetMode(ModeFullDisplay) },
		func() error { return view.options.SetFormat(FormatJPEG) },
		func() error { return view.options.SetJPEGQuality(55) },
		func() error { return view.options.SetOutputFolder(filepath.Join(t.TempDir(), "Other")) },
		func() error { return view.options.SetShortcut(shortcut) },
	} {
		if err := change(); err != nil {
			t.Fatal(err)
		}
	}
	if err := view.hotkeys.Apply(shortcut); err != nil {
		t.Fatal(err)
	}
	view.refresh()
	view.shortcut.Edit.SetText(shortcut.String())
	result := ui.Notice{Kind: ui.NoticeSuccess, Message: "Display captured."}
	_ = view.status.Show(result, 0)

	delete(view.options.settings.Modules, string(moduleID))
	if followUp := view.NativeRestoreDefaults(); followUp != "" {
		t.Fatalf("follow-up = %q, want none", followUp)
	}
	if got := view.options.Options(); got != defaults {
		t.Fatalf("options after the restore = %+v, want the defaults %+v", got, defaults)
	}
	if got := fake.applied[len(fake.applied)-1]; got != DefaultShortcut() {
		t.Fatalf("shortcut registered by the restore = %v, want the default", got)
	}
	if fake.released != 0 {
		t.Fatalf("Release calls = %d, want none when the default registers", fake.released)
	}
	if got := view.shortcut.Edit.Text(); got != DefaultShortcut().String() {
		t.Fatalf("shortcut field = %q, want the default", got)
	}
	if got := view.quality.Edit.Text(); got != "90" {
		t.Fatalf("JPEG quality field = %q, want 90", got)
	}
	if got := view.folder.Text(); got != defaults.OutputFolder {
		t.Fatalf("folder field = %q, want %q", got, defaults.OutputFolder)
	}
	if got := view.pattern.Text(); got != defaults.FilenameExample() {
		t.Fatalf("filename pattern = %q, want the PNG pattern %q", got, defaults.FilenameExample())
	}
	if got := view.status.Text(); got != result.Text() {
		t.Fatalf("status after the restore = %q, want the capture's result kept", got)
	}
}

// TestNativeViewRestoreDefaultsReportsAHeldDefaultShortcut pins that a
// default shortcut another application holds is reported as at startup,
// beside the field and in the status area, and returned for the Settings
// view; the combination the settings no longer name is released rather
// than left registered behind a field that shows the default.
func TestNativeViewRestoreDefaultsReportsAHeldDefaultShortcut(t *testing.T) {
	view, fake := newHotkeyTestView(t, filepath.Join(t.TempDir(), "settings.json"))
	shortcut, err := ParseShortcut("Ctrl+Shift+F9")
	if err != nil {
		t.Fatal(err)
	}
	if err := view.options.SetShortcut(shortcut); err != nil {
		t.Fatal(err)
	}
	fake.applyFunc = func(candidate Shortcut) error {
		if candidate == DefaultShortcut() {
			return shortcutConflictError(candidate)
		}
		return nil
	}

	delete(view.options.settings.Modules, string(moduleID))
	followUp := view.NativeRestoreDefaults()
	if !strings.Contains(followUp, ui.ErrorShortcutRegistrationLabel) || !strings.Contains(followUp, "Ctrl+Alt+S") {
		t.Fatalf("follow-up = %q, want the default shortcut's registration failure", followUp)
	}
	if fake.released != 1 {
		t.Fatalf("Release calls = %d, want the replaced combination released", fake.released)
	}
	if got := view.shortcut.Error.Text(); !strings.Contains(got, ui.ErrorShortcutRegistrationLabel) {
		t.Fatalf("field message = %q, want the registration failure", got)
	}
	if got := view.status.Text(); !strings.Contains(got, ui.ErrorShortcutRegistrationLabel) {
		t.Fatalf("status = %q, want the registration failure", got)
	}
}

// TestNativeViewRestoreDefaultsClearsAStaleShortcutFailure pins that a
// startup shortcut failure the restore resolved does not linger in the
// status area or beside the field.
func TestNativeViewRestoreDefaultsClearsAStaleShortcutFailure(t *testing.T) {
	view, fake := newHotkeyTestView(t, filepath.Join(t.TempDir(), "settings.json"))
	view.presentShortcutFailure(shortcutConflictError(DefaultShortcut()))
	fake.applyFunc = nil

	delete(view.options.settings.Modules, string(moduleID))
	if followUp := view.NativeRestoreDefaults(); followUp != "" {
		t.Fatalf("follow-up = %q, want none", followUp)
	}
	if got := view.status.Text(); got != "" {
		t.Fatalf("status = %q, want the resolved failure cleared", got)
	}
	if got := view.shortcut.Error.Text(); got != "" {
		t.Fatalf("field message = %q, want the resolved failure cleared", got)
	}
}

// TestNativeViewRestoreDefaultsWithoutADefaultFolder pins that a restore
// with no default output folder known returns the folder warning for the
// Settings view to report.
func TestNativeViewRestoreDefaultsWithoutADefaultFolder(t *testing.T) {
	view, _ := newHotkeyTestView(t, filepath.Join(t.TempDir(), "settings.json"))
	view.options.store.defaultFolder = ""
	delete(view.options.settings.Modules, string(moduleID))
	if followUp := view.NativeRestoreDefaults(); !strings.Contains(followUp, ui.CaptureDefaultFolderUnknown) {
		t.Fatalf("follow-up = %q, want the folder warning", followUp)
	}
}
