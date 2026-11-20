//go:build windows

package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot"
	core "github.com/CyberCute-Studio/GameDevTools/internal/shell"
)

// TestStartingWorkKeepsUnsavedEditsAndTheOtherModule pins APP-004 on the
// real main window: starting a capture, which hides the window for its
// selection, and starting a resize job keep the unsaved edits in both
// modules' preset forms and the other module's state — the resize list
// through a capture, and the capture options through a job.
func TestStartingWorkKeepsUnsavedEditsAndTheOtherModule(t *testing.T) {
	shell := newAuditShell(t)
	directory := filepath.Dir(shell.path)
	list := addSources(t, shell, writePNG(t, filepath.Join(directory, "Sources", "photo.png"), 320, 240))
	// The fields are found while their module is shown, and read wherever
	// the window is: the selection hides it, and its controls with it.
	type edit struct {
		field   windows.Handle
		unsaved string
	}
	var edits []edit
	for _, module := range []struct {
		id          core.ModuleID
		name, width string
	}{
		{core.ModuleImageResizer, "Unsaved resize preset", "300"},
		{core.ModuleScreenshot, "Unsaved capture preset", "1000"},
	} {
		shell.selectModule(t, module.id)
		for _, field := range []edit{{shell.control(t, "Edit Name"), module.name}, {shell.control(t, "Edit Width (px)"), module.width}} {
			setWindowText(field.field, field.unsaved)
			edits = append(edits, field)
		}
	}
	if err := shell.capture.SetMode(screenshot.ModeFreeRectangle); err != nil {
		t.Fatal(err)
	}
	capture := shell.capture.Options()
	checkEdits := func(when string) {
		t.Helper()
		for _, field := range edits {
			if got := windowText(field.field); got != field.unsaved {
				t.Fatalf("%s: a preset field holds %q, want the unsaved %q", when, got, field.unsaved)
			}
		}
		if rows := listRows(list); len(rows) != 1 || !strings.HasPrefix(rows[0], "photo.png") {
			t.Fatalf("%s: resize list = %q, want the image kept", when, rows)
		}
	}

	shell.nativeViews[core.ModuleScreenshot].(nativeActionHandler).NativeAction()
	toolbars := threadWindows("GameDevToolsRectangleToolbar")
	if len(toolbars) != 1 {
		t.Fatalf("Start Capture opened %d toolbar(s), want a selection session", len(toolbars))
	}
	checkEdits("during the selection")
	procSendMessageW.Call(uintptr(toolbars[0]), wmClose, 0, 0)
	shell.pump(t, 10*time.Second, func() bool { return sessionWindows() == 0 })
	if visible, _, _ := procIsWindowVisible.Call(uintptr(shell.hwnd)); visible == 0 {
		t.Fatal("the main window was not shown again once the selection was cancelled")
	}
	checkEdits("after the cancelled selection")

	shell.selectModule(t, core.ModuleImageResizer)
	resizer := shell.nativeViews[core.ModuleImageResizer]
	resizer.(nativeActionHandler).NativeAction()
	running := func() bool {
		_, message := resizer.(nativeCloseGuard).NativeCloseConfirmation()
		return message != ""
	}
	if !running() {
		t.Fatal("Start Resize started no job")
	}
	shell.pump(t, 60*time.Second, func() bool { return !running() })
	checkEdits("after the resize job")
	if got := shell.capture.Options(); got != capture {
		t.Fatalf("capture options after the resize job = %+v, want %+v", got, capture)
	}
}
