//go:build windows

package app

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot/execute"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot/overlay"
	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	core "github.com/CyberCute-Studio/GameDevTools/internal/shell"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui/uiatest"
)

var (
	procGetWindow       = user32.NewProc("GetWindow")
	procGetClassNameW   = user32.NewProc("GetClassNameW")
	procGetWindowTextW  = user32.NewProc("GetWindowTextW")
	procPeekMessageW    = user32.NewProc("PeekMessageW")
	procGetActiveWindow = user32.NewProc("GetActiveWindow")
	procEnableWindow    = user32.NewProc("EnableWindow")
)

const (
	gwChild            = 5
	gwHwndNext         = 2
	gwlStyle           = ^uintptr(15)
	wsTabStop          = 0x00010000
	ssNoPrefix         = 0x00000080
	wmKeyDown          = 0x0100
	wmSysChar          = 0x0106
	wmChar             = 0x0102
	vkReturn           = 0x0d
	emSetSel           = 0x00b1
	lbGetCount         = 0x018b
	vkTab              = 0x09
	vkUp               = 0x26
	vkDown             = 0x28
	waActive           = 1
	swShowNoActivate   = 4
	pmRemove           = 0x0001
	bmClick            = 0x00f5
	altDownContextCode = 1 << 29
)

// auditShell is the real main window with every production module, built on
// a temporary settings file, as the keyboard and accessibility audit
// exercises it (QLT-01).
type auditShell struct {
	*nativeShell
	capture *screenshot.OptionsManager
	resize  *imageresizer.OptionsManager
	// path is the settings file the window was opened on, in its own
	// temporary folder, which also holds the default output folders.
	path string
}

func newAuditShell(t *testing.T) *auditShell {
	t.Helper()
	return newAuditShellOn(t, nil)
}

// newAuditShellOn builds the audit's main window on a settings file that
// holds data, or on no settings file when data is nil.
func newAuditShellOn(t *testing.T, data []byte) *auditShell {
	t.Helper()
	return newAuditShellWhile(t, data, nil)
}

// newAuditShellWhile builds the audit's main window as newAuditShellOn does,
// with hold, when it is not nil, holding the settings file while the session
// opens it — as a sync client or a virus scanner holding it open does — and
// releasing it with the function it returns. The window's note shows the
// startup warnings, and takes the restored ones, as runNative gives them.
func newAuditShellWhile(t *testing.T, data []byte, hold func(t *testing.T, path string) (release func())) *auditShell {
	t.Helper()
	return newAuditShellFrom(t, data, hold, nil)
}

// newAuditShellFrom builds the audit's main window as newAuditShellWhile
// does, with folders, when it is not nil, giving the default output folders
// startup resolves, and why one could not be, from the window's own
// temporary folder.
func newAuditShellFrom(t *testing.T, data []byte, hold func(t *testing.T, path string) (release func()), folders func(dir string) defaultFolders) *auditShell {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if data != nil {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	release := func() {}
	if hold != nil {
		release = hold(t, path)
	}
	defaults := defaultFolders{
		screenshot: filepath.Join(dir, "Screenshots"),
		resizer:    filepath.Join(dir, "Resized"),
	}
	if folders != nil {
		defaults = folders(dir)
	}
	state, err := openSession(settings.Paths{ConfigFile: path}, nil, defaults)
	release()
	if err != nil {
		t.Fatal(err)
	}
	// A combination nothing else uses, so the view's global registration
	// cannot take the developer's own Ctrl+Alt+S while the test runs. A
	// session whose saving recovery disabled keeps it in memory.
	shortcut, err := screenshot.ParseShortcut("Ctrl+Alt+Shift+F24")
	if err != nil {
		t.Fatal(err)
	}
	if err := state.capture.SetShortcut(shortcut); err != nil && !errors.Is(err, screenshot.ErrPersistence) {
		t.Fatal(err)
	}
	monitor := display.NewMonitor()
	executor := execute.NewService(monitor)
	navigator, err := newNavigator(state, monitor, executor.FullDisplay, executor.Rectangle, overlay.NewController(monitor))
	if err != nil {
		t.Fatal(err)
	}
	shell, err := newNativeShell(navigator, state.store, state.value, nil, monitor, state.startupWarnings())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		procDestroyWindow.Call(uintptr(shell.hwnd))
		// WM_DESTROY posts the quit message a real message loop ends on;
		// it is drained so it cannot reach a later test on this thread.
		var message msg
		for {
			if available, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0, pmRemove); available == 0 {
				break
			}
		}
		shell.waitForBackgroundWork()
	})
	procShowWindow.Call(uintptr(shell.hwnd), swShowNoActivate)
	return &auditShell{nativeShell: shell, capture: state.capture, resize: state.resize, path: path}
}

// control returns the visible control the description names, as describe
// gives it, in the module shown.
func (a *auditShell) control(t *testing.T, want string) windows.Handle {
	t.Helper()
	for _, child := range a.children() {
		if visible, _, _ := procIsWindowVisible.Call(uintptr(child)); visible == 0 {
			continue
		}
		if a.describe(t, []windows.Handle{child})[0] == want {
			return child
		}
	}
	t.Fatalf("no visible control is %q", want)
	return 0
}

// pump dispatches the messages waiting for the window's thread, as the
// message loop does, until done reports true, failing after timeout.
func (a *auditShell) pump(t *testing.T, timeout time.Duration, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatal("timed out pumping the window's messages")
		}
		var message msg
		for {
			available, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0, pmRemove)
			if available == 0 {
				break
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// selectModule switches to the module as clicking its navigation button
// does.
func (a *auditShell) selectModule(t *testing.T, id core.ModuleID) {
	t.Helper()
	for command, module := range a.navIDs {
		if module == id {
			shellWindowProc(a.hwnd, wmCommand, uintptr(command), 0)
			if a.navigator.Current().ID() != id {
				t.Fatalf("selecting %s left %s current", id, a.navigator.Current().ID())
			}
			return
		}
	}
	t.Fatalf("no navigation button for %s", id)
}

// press hands a key to the shell's message loop from the focused control.
func (a *auditShell) press(message uint32, key uintptr) {
	focused := a.focused()
	m := msg{HWND: focused, Message: message, WParam: key}
	if message == wmSysChar {
		m.LParam = altDownContextCode
	}
	if a.filterModuleMessage(&m) {
		return
	}
	procIsDialogMessageW.Call(uintptr(a.hwnd), uintptr(unsafe.Pointer(&m)))
}

// focused returns the control with the keyboard focus, coming back to the
// window first if it lost the foreground meanwhile. The tests of other
// packages run alongside in their own processes and bring their capture
// toolbars to the foreground, and a window that loses the foreground leaves
// its thread with no active window and no focus. Focusing the window
// activates it again, and restoreFocus gives the focus back to the control
// that had it, as it does when the user comes back to the window.
func (a *auditShell) focused() windows.Handle {
	if active, _, _ := procGetActiveWindow.Call(); windows.Handle(active) != a.hwnd {
		procSetFocus.Call(uintptr(a.hwnd))
	}
	hwnd, _, _ := procGetFocus.Call()
	return windows.Handle(hwnd)
}

// children returns the shell's direct child controls in tab (z) order.
func (a *auditShell) children() []windows.Handle {
	var result []windows.Handle
	child, _, _ := procGetWindow.Call(uintptr(a.hwnd), gwChild)
	for child != 0 {
		result = append(result, windows.Handle(child))
		child, _, _ = procGetWindow.Call(child, gwHwndNext)
	}
	return result
}

func className(hwnd windows.Handle) string {
	buffer := make([]uint16, 64)
	procGetClassNameW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	return windows.UTF16ToString(buffer)
}

func windowText(hwnd windows.Handle) string {
	buffer := make([]uint16, 1024)
	procGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	return windows.UTF16ToString(buffer)
}

func style(hwnd windows.Handle) uintptr {
	value, _, _ := procGetWindowLongPtrW.Call(uintptr(hwnd), gwlStyle)
	return value
}

func usable(hwnd windows.Handle) bool {
	visible, _, _ := procIsWindowVisible.Call(uintptr(hwnd))
	enabled, _, _ := procIsWindowEnabled.Call(uintptr(hwnd))
	return visible != 0 && enabled != 0
}

// tabWalk presses Tab from the focused control until the focus comes back
// to it, and returns every control it reached in order.
func (a *auditShell) tabWalk(t *testing.T) []windows.Handle {
	t.Helper()
	start := a.focused()
	walk := []windows.Handle{start}
	for range 200 {
		a.press(wmKeyDown, vkTab)
		if a.focused() == start {
			return walk
		}
		walk = append(walk, a.focused())
	}
	t.Fatal("Tab did not come back to where it started")
	return nil
}

// backwardWalk follows the order Shift+Tab takes from the focused control
// until it comes back to it, and returns every control on the way. It asks
// the dialog manager for each previous tab stop, as Shift+Tab does, without
// moving the focus: holding Shift for the test would mean setting the
// thread's key state, which is not the test's to keep, and tabWalk already
// shows that moving through the tab stops changes nothing.
func (a *auditShell) backwardWalk(t *testing.T) []windows.Handle {
	t.Helper()
	start := a.focused()
	walk := []windows.Handle{start}
	current := start
	for range 200 {
		previous, _, _ := procGetNextDlgTabItem.Call(uintptr(a.hwnd), uintptr(current), 1)
		current = windows.Handle(previous)
		if current == start {
			return walk
		}
		walk = append(walk, current)
	}
	t.Fatal("Shift+Tab did not come back to where it started")
	return nil
}

func (a *auditShell) describe(t *testing.T, controls []windows.Handle) []string {
	t.Helper()
	names, err := uiatest.Names(controls...)
	if err != nil {
		t.Fatal(err)
	}
	described := make([]string, len(controls))
	for i, control := range controls {
		described[i] = className(control) + " " + names[i]
	}
	return described
}

// describeFocused returns the class and name of the focused control.
func (a *auditShell) describeFocused(t *testing.T) string {
	t.Helper()
	return a.describe(t, []windows.Handle{a.focused()})[0]
}

// typeText types into the focused edit control as the keyboard does, one
// character at a time.
func (a *auditShell) typeText(text string) {
	for _, character := range text {
		procSendMessageW.Call(uintptr(a.focused()), wmChar, uintptr(character), 0)
	}
}

// tabTo presses Tab until the focused control is the one described, failing
// if a full round of the tab order passes without reaching it.
func (a *auditShell) tabTo(t *testing.T, want string) {
	t.Helper()
	for range 60 {
		if a.describeFocused(t) == want {
			return
		}
		a.press(wmKeyDown, vkTab)
	}
	t.Fatalf("Tab never reached %q", want)
}

// expectedTabOrder is every control Tab reaches in each module, from the
// module's navigation button, in its layout order: the class and the name a
// screen reader announces for it (UX-006, AC-A03). Controls that do not
// apply yet are disabled and skipped, such as Open Folder before a capture,
// JPEG quality while PNG is selected, and Remove Selected without a
// selection; the keyboard-only workflow tests reach them once they apply.
var expectedTabOrder = map[core.ModuleID][]string{
	core.ModuleScreenshot: {
		"Button Screenshot",
		"Button Start Capture",
		"Button Free Rectangle",
		"ComboBox Save format",
		"Edit Global shortcut",
		"Edit Output folder",
		"Button Browse…",
		"ComboBox Presets",
		"Edit Name",
		"Edit Width (px)",
		"Edit Height (px)",
		"Button Create Preset",
		"Button Duplicate",
	},
	core.ModuleImageResizer: {
		"Button Image Resizer",
		"Button Start Resize",
		"ListBox Source images",
		"Edit Full path",
		"Button Add Images…",
		"Edit Output folder",
		"Button Browse…",
		"ComboBox Output format",
		"Edit JPEG quality (1–100)",
		"Edit JPEG transparency background (#RRGGBB)",
		"Button Rename",
		"ComboBox Presets",
		"Edit Name",
		"Edit Width (px)",
		"Edit Height (px)",
		"ComboBox Fitting mode",
		"Button Create Preset",
		"Button Duplicate",
	},
	core.ModuleSettings: {
		"Button Settings",
		"Button Restore Default Settings",
	},
}

// TestTabReachesEveryControlInLayoutOrder walks each module with Tab and
// Shift+Tab as a keyboard-only user would (UX-006, AC-A03): every enabled
// control is reached, in layout order, with the name a screen reader
// announces, and passing through changes nothing — not the module, which
// Tab once switched by focusing each navigation radio button in turn, and
// not the capture mode or collision policy, which it switched the same way.
func TestTabReachesEveryControlInLayoutOrder(t *testing.T) {
	shell := newAuditShell(t)
	mode, policy := shell.capture.Options().Mode, shell.resize.Options().Collision
	for _, id := range []core.ModuleID{core.ModuleScreenshot, core.ModuleImageResizer, core.ModuleSettings} {
		shell.selectModule(t, id)
		procSetFocus.Call(uintptr(shell.navButtons[id].Handle()))
		walk := shell.tabWalk(t)
		if got, want := shell.describe(t, walk), expectedTabOrder[id]; strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s tab order:\n%s\nwant:\n%s", id, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		if current := shell.navigator.Current().ID(); current != id {
			t.Fatalf("tabbing through %s switched to %s", id, current)
		}
		reached := map[windows.Handle]bool{}
		for _, control := range walk {
			if !usable(control) {
				t.Errorf("%s: Tab reached a hidden or disabled control %q", id, windowText(control))
			}
			reached[control] = true
		}
		for _, control := range shell.children() {
			if usable(control) && style(control)&wsTabStop != 0 && !reached[control] {
				t.Errorf("%s: Tab never reached the %s %q", id, className(control), windowText(control))
			}
		}
		backward := shell.backwardWalk(t)
		if len(backward) != len(walk) {
			t.Errorf("%s: Shift+Tab reached %d controls, Tab %d", id, len(backward), len(walk))
		}
		for i := 1; i < min(len(walk), len(backward)); i++ {
			if backward[i] != walk[len(walk)-i] {
				t.Errorf("%s: Shift+Tab step %d reached %q, want %q", id, i, windowText(backward[i]), windowText(walk[len(walk)-i]))
				break
			}
		}
	}
	if got := shell.capture.Options().Mode; got != mode {
		t.Errorf("tabbing changed the capture mode to %v", got)
	}
	if got := shell.resize.Options().Collision; got != policy {
		t.Errorf("tabbing changed the collision policy to %v", got)
	}
}

// TestNavigationArrowKeysSwitchModules pins the navigation's keyboard model
// beside Tab: the arrow keys move between the modules, switching to each, and
// wrap from the last module to the first and back, as in any Windows radio
// group. The navigation's group ends at its last button, where the title
// starts the next; before, it ran on into the title and description, so Down
// from Settings and Up from Screenshot did nothing.
func TestNavigationArrowKeysSwitchModules(t *testing.T) {
	shell := newAuditShell(t)
	shell.selectModule(t, core.ModuleScreenshot)
	procSetFocus.Call(uintptr(shell.navButtons[core.ModuleScreenshot].Handle()))
	for _, step := range []struct {
		key  uintptr
		from core.ModuleID
		want core.ModuleID
	}{
		{vkDown, core.ModuleScreenshot, core.ModuleImageResizer},
		{vkUp, core.ModuleImageResizer, core.ModuleScreenshot},
		{vkUp, core.ModuleScreenshot, core.ModuleSettings},
		{vkDown, core.ModuleSettings, core.ModuleScreenshot},
	} {
		shell.press(wmKeyDown, step.key)
		if current := shell.navigator.Current().ID(); current != step.want || !shell.navButtons[step.want].HasFocus() {
			t.Fatalf("key %#x from %s: current %s, focus %q; want %s focused and shown", step.key, step.from, current, windowText(shell.focused()), step.want)
		}
	}
}

// TestRadioGroupsWrapAtTheirEnds pins that the arrow keys keep to the capture
// modes and the collision policies (UX-006): Up from a group's first button
// selects its last and Down from its last selects its first. Each group ends
// at its last button, where the label after it starts the next group; before,
// each ran on into the labels and fields after it, so Up from Free Rectangle
// moved the focus to Output folder and Up from Rename to Fitting mode,
// selecting nothing, and Down from the last button did nothing. The display
// label, which ends the capture modes' group, keeps its access key.
func TestRadioGroupsWrapAtTheirEnds(t *testing.T) {
	shell := newAuditShell(t)
	shell.selectModule(t, core.ModuleScreenshot)
	procSetFocus.Call(uintptr(shell.navButtons[core.ModuleScreenshot].Handle()))
	shell.tabTo(t, "Button Free Rectangle")
	shell.press(wmKeyDown, vkUp)
	if got, mode := shell.describeFocused(t), shell.capture.Options().Mode; got != "Button Full Display" || mode != screenshot.ModeFullDisplay {
		t.Fatalf("Up from Free Rectangle focused %q with mode %v, want Full Display focused and selected", got, mode)
	}
	shell.press(wmKeyDown, vkDown)
	if got, mode := shell.describeFocused(t), shell.capture.Options().Mode; got != "Button Free Rectangle" || mode != screenshot.ModeFreeRectangle {
		t.Fatalf("Down from Full Display focused %q with mode %v, want Free Rectangle focused and selected", got, mode)
	}
	shell.press(wmKeyDown, vkUp)
	shell.layout()
	shell.press(wmSysChar, 'd')
	if got := shell.describeFocused(t); got != "ComboBox Display" {
		t.Fatalf("Alt+D focused %q, want the display picker", got)
	}

	shell.selectModule(t, core.ModuleImageResizer)
	procSetFocus.Call(uintptr(shell.navButtons[core.ModuleImageResizer].Handle()))
	shell.tabTo(t, "Button Rename")
	shell.press(wmKeyDown, vkUp)
	if got, policy := shell.describeFocused(t), shell.resize.Options().Collision; got != "Button Overwrite" || policy != imageresizer.CollisionOverwrite {
		t.Fatalf("Up from Rename focused %q with policy %v, want Overwrite focused and selected", got, policy)
	}
	shell.press(wmKeyDown, vkDown)
	if got, policy := shell.describeFocused(t), shell.resize.Options().Collision; got != "Button Rename" || policy != imageresizer.CollisionRename {
		t.Fatalf("Down from Overwrite focused %q with policy %v, want Rename focused and selected", got, policy)
	}
}

// TestScreenshotOptionsKeyboardOnly configures a capture without a pointer
// (UX-006): the arrow keys choose the capture mode, whose display picker
// then joins the tab order, and the save format, whose JPEG quality field
// does; typed quality is committed when the focus moves on; and the capture
// mode label's mnemonic focuses the selected mode instead of selecting the
// group's first one.
func TestScreenshotOptionsKeyboardOnly(t *testing.T) {
	shell := newAuditShell(t)
	shell.selectModule(t, core.ModuleScreenshot)
	procSetFocus.Call(uintptr(shell.navButtons[core.ModuleScreenshot].Handle()))
	shell.tabTo(t, "Button Free Rectangle")
	shell.press(wmKeyDown, vkDown)
	shell.press(wmKeyDown, vkDown)
	if got := shell.capture.Options().Mode; got != screenshot.ModeFullDisplay {
		t.Fatalf("capture mode after two Down presses = %v, want Full Display", got)
	}
	shell.layout()
	shell.press(wmKeyDown, vkTab)
	if got := shell.describeFocused(t); got != "ComboBox Display" {
		t.Fatalf("Tab after the capture mode reached %q, want the display picker", got)
	}
	shell.press(wmKeyDown, vkTab)
	shell.press(wmKeyDown, vkDown)
	if got := shell.capture.Options().Format; got != screenshot.FormatJPEG {
		t.Fatalf("save format after Down = %v, want JPEG", got)
	}
	shell.layout()
	shell.press(wmKeyDown, vkTab)
	if got := shell.describeFocused(t); got != "Edit JPEG quality (1–100)" {
		t.Fatalf("Tab after the save format reached %q, want JPEG quality", got)
	}
	procSendMessageW.Call(uintptr(shell.focused()), emSetSel, 0, ^uintptr(0))
	shell.typeText("72")
	shell.press(wmKeyDown, vkTab)
	if got := shell.capture.Options().JPEGQuality; got != 72 {
		t.Fatalf("JPEG quality after typing 72 and Tab = %d, want 72", got)
	}

	shell.press(wmSysChar, 'c')
	if got := shell.describeFocused(t); got != "Button Full Display" {
		t.Fatalf("Alt+C focused %q, want the selected capture mode", got)
	}
	if got := shell.capture.Options().Mode; got != screenshot.ModeFullDisplay {
		t.Fatalf("Alt+C changed the capture mode to %v", got)
	}
}

// TestResizerSourcesKeyboardOnly works the source list and the collision
// policy without a pointer (UX-006). Files are added as a drop adds them —
// the image picker is a system dialog of its own — and are then selected
// with the arrow keys, removed with Remove Selected, and cleared with Clear
// List from the keyboard; the arrow keys choose the collision policy, and
// its label's mnemonic focuses the selected policy rather than selecting
// the group's first one.
func TestResizerSourcesKeyboardOnly(t *testing.T) {
	shell := newAuditShell(t)
	shell.selectModule(t, core.ModuleImageResizer)
	dir := t.TempDir()
	var paths []string
	for _, name := range []string{"first.png", "second.png", "third.png"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("not an image"), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	target, ok := shell.nativeViews[core.ModuleImageResizer].(nativeDropTarget)
	if !ok {
		t.Fatal("the Image Resizer view takes no drops")
	}
	target.NativeDropFiles(paths)
	shell.layout()

	procSetFocus.Call(uintptr(shell.navButtons[core.ModuleImageResizer].Handle()))
	shell.tabTo(t, "ListBox Source images")
	list := shell.focused()
	shell.press(wmKeyDown, vkDown)
	shell.layout()
	walk := strings.Join(shell.describe(t, shell.tabWalk(t)), "\n")
	for _, want := range []string{"Button Remove Selected", "Button Clear List"} {
		if !strings.Contains(walk, want) {
			t.Fatalf("with an image selected, Tab never reached %q:\n%s", want, walk)
		}
	}
	shell.tabTo(t, "Button Remove Selected")
	shell.press(wmKeyDown, vkReturn)
	shell.layout()
	if got := listCount(list); got != 2 {
		t.Fatalf("source list after Remove Selected from the keyboard holds %d image(s), want 2", got)
	}
	procSetFocus.Call(uintptr(list))
	shell.tabTo(t, "Button Clear List")
	shell.press(wmKeyDown, vkReturn)
	shell.layout()
	if got := listCount(list); got != 0 {
		t.Fatalf("source list after Clear List from the keyboard holds %d image(s), want none", got)
	}

	shell.press(wmSysChar, 'x')
	if got := shell.describeFocused(t); got != "Button Rename" {
		t.Fatalf("Alt+X focused %q, want the selected collision policy", got)
	}
	shell.press(wmKeyDown, vkDown)
	if got := shell.resize.Options().Collision; got != imageresizer.CollisionSkip {
		t.Fatalf("collision policy after Down = %v, want Skip", got)
	}
	procSetFocus.Call(uintptr(list))
	shell.press(wmSysChar, 'x')
	if got := shell.describeFocused(t); got != "Button Skip" {
		t.Fatalf("Alt+X focused %q, want the selected collision policy", got)
	}
	if got := shell.resize.Options().Collision; got != imageresizer.CollisionSkip {
		t.Fatalf("Alt+X changed the collision policy to %v", got)
	}
}

func listCount(list windows.Handle) int {
	count, _, _ := procSendMessageW.Call(uintptr(list), lbGetCount, 0, 0)
	return int(int32(count))
}

// TestMnemonicsAreUniqueInEachModule pins that no two visible controls in a
// module share an access key, so each Alt+letter reaches one control.
func TestMnemonicsAreUniqueInEachModule(t *testing.T) {
	shell := newAuditShell(t)
	for _, id := range []core.ModuleID{core.ModuleScreenshot, core.ModuleImageResizer, core.ModuleSettings} {
		shell.selectModule(t, id)
		seen := map[rune]string{}
		for _, control := range shell.children() {
			if visible, _, _ := procIsWindowVisible.Call(uintptr(control)); visible == 0 {
				continue
			}
			if className(control) == "Static" && style(control)&ssNoPrefix != 0 {
				continue
			}
			key, ok := mnemonic(windowText(control))
			if !ok {
				continue
			}
			if other, taken := seen[key]; taken {
				t.Errorf("%s: %q and %q share the access key %q", id, other, windowText(control), key)
			}
			seen[key] = windowText(control)
		}
	}
}

// mnemonic returns the access key a control's text marks with an
// ampersand, as Windows reads it: "&&" is a literal ampersand.
func mnemonic(text string) (rune, bool) {
	runes := []rune(text)
	for i := 0; i < len(runes)-1; i++ {
		if runes[i] != '&' {
			continue
		}
		if runes[i+1] == '&' {
			i++
			continue
		}
		return []rune(strings.ToLower(string(runes[i+1])))[0], true
	}
	return 0, false
}

// TestShellRestoresFocusOnActivation pins that activating the window gives
// the keyboard focus to a control (UX-006): Windows focuses the window
// itself on activation, which would leave no visible focus and restart Tab
// from the first control. The first activation focuses the current module's
// navigation button; a later one, the control that had the focus, unless it
// can no longer take it: one disabled meanwhile, as Cancel Resize is when a
// job finishes while the window is inactive, hands it to the next control
// in tab order (AC-A03), and one whose module is hidden, to the navigation.
func TestShellRestoresFocusOnActivation(t *testing.T) {
	shell := newAuditShell(t)
	shell.selectModule(t, core.ModuleScreenshot)
	activate := func() {
		procSendMessageW.Call(uintptr(shell.hwnd), wmActivate, waInactive, 0)
		procSetFocus.Call(0)
		procSendMessageW.Call(uintptr(shell.hwnd), wmActivate, waActive, 0)
	}
	activate()
	if !shell.navButtons[core.ModuleScreenshot].HasFocus() {
		t.Fatalf("the first activation focused %q, want the Screenshot navigation button", windowText(shell.focused()))
	}

	shell.tabTo(t, "Edit Global shortcut")
	shortcut := shell.focused()
	activate()
	if shell.focused() != shortcut {
		t.Fatalf("reactivating the window focused %q, want the global shortcut field it left", windowText(shell.focused()))
	}

	shell.tabTo(t, "Edit Output folder")
	folder := shell.focused()
	procSendMessageW.Call(uintptr(shell.hwnd), wmActivate, waInactive, 0)
	procSetFocus.Call(0)
	procEnableWindow.Call(uintptr(folder), 0)
	procSendMessageW.Call(uintptr(shell.hwnd), wmActivate, waActive, 0)
	procEnableWindow.Call(uintptr(folder), 1)
	if got := shell.describeFocused(t); got != "Button Browse…" {
		t.Fatalf("reactivating with the focused control disabled meanwhile focused %q, want the next control, Browse…", got)
	}

	shell.selectModule(t, core.ModuleImageResizer)
	activate()
	if !shell.navButtons[core.ModuleImageResizer].HasFocus() {
		t.Fatalf("reactivating with the focused control's module hidden focused %q, want the Image Resizer navigation button", windowText(shell.focused()))
	}
}

// TestShellLeavesAClickActivationsFocusToTheClick pins that a click that
// activates the window keeps the focus it gives (UX-006): the window is
// activated before the click reaches the control under the pointer, and
// giving the focus back to the control that had it first would run that
// control's focus handlers for nothing — the global shortcut field suspends
// the shortcut — or scroll it into view from under the pointer. Once the
// click is handled, the focus goes back only if the click left it on the
// window itself, as a click on the title bar or the scroll bar does, and
// without scrolling the view.
func TestShellLeavesAClickActivationsFocusToTheClick(t *testing.T) {
	shell := newAuditShell(t)
	shell.selectModule(t, core.ModuleScreenshot)
	shell.tabTo(t, "Button Browse…")
	browse := shell.focused()
	shell.tabTo(t, "Edit Global shortcut")
	shortcut := shell.focused()
	clickActivate := func() {
		procSendMessageW.Call(uintptr(shell.hwnd), wmActivate, waInactive, 0)
		procSetFocus.Call(0)
		procSendMessageW.Call(uintptr(shell.hwnd), wmActivate, waClickActive, 0)
	}
	// handleClick dispatches what the activation posted, as the message loop
	// does once the click itself has been delivered.
	handleClick := func() {
		var message msg
		for {
			if available, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&message)), uintptr(shell.hwnd), 0, 0, pmRemove); available == 0 {
				return
			}
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
		}
	}

	clickActivate()
	if focus, _, _ := procGetFocus.Call(); windows.Handle(focus) == shortcut {
		t.Fatal("a click activation gave the focus back to the shortcut field before the click was handled")
	}
	procSetFocus.Call(uintptr(browse))
	handleClick()
	if focus, _, _ := procGetFocus.Call(); windows.Handle(focus) != browse {
		t.Fatalf("after a click on Browse… activated the window, %q has the focus", windowText(windows.Handle(focus)))
	}

	clickActivate()
	handleClick()
	if focus, _, _ := procGetFocus.Call(); windows.Handle(focus) != browse {
		t.Fatalf("after a click on the title bar activated the window, %q has the focus, want Browse…, which had it", windowText(windows.Handle(focus)))
	}

	// A click on the scroll bar leaves the focus on the window too, and the
	// view keeps the scrolling the click made: the control given the focus
	// back is not scrolled into view.
	moveWindow(shell.hwnd, 0, 0, 720, 320)
	for _, delta := range []int32{-1 << 20, 1 << 20} {
		shell.scrollBy(delta)
		if !shell.controlInView(browse) {
			break
		}
	}
	if shell.controlInView(browse) {
		t.Fatal("the test cannot scroll Browse… out of a 320-pixel-tall window")
	}
	scrolled := shell.scrollY
	clickActivate()
	handleClick()
	if focus, _, _ := procGetFocus.Call(); windows.Handle(focus) != browse {
		t.Fatalf("after a click on the scroll bar activated the window, %q has the focus, want Browse…, which had it", windowText(windows.Handle(focus)))
	}
	if shell.scrollY != scrolled {
		t.Fatalf("giving the focus back after a click on the scroll bar scrolled the view from %d to %d", scrolled, shell.scrollY)
	}
}

// controlInView reports whether any of the control shows in the window's
// client area.
func (a *auditShell) controlInView(control windows.Handle) bool {
	var bounds, client rect
	procGetWindowRect.Call(uintptr(control), uintptr(unsafe.Pointer(&bounds)))
	procMapWindowPoints.Call(0, uintptr(a.hwnd), uintptr(unsafe.Pointer(&bounds)), 2)
	procGetClientRect.Call(uintptr(a.hwnd), uintptr(unsafe.Pointer(&client)))
	return bounds.Bottom > client.Top && bounds.Top < client.Bottom
}

// TestShellAnnouncesStartupNoticeOnceShown pins that the shell announces its
// note once the window appears (announceNotices): a startup warning is set
// while the window is still hidden, where its announcement goes unheard,
// and its unchanged text would never be announced again. An empty note
// announces nothing, as switching between the modules shows; the Settings
// view, a placeholder until QLT-02, now has its own controls and an empty
// note too (TestShellShowsAPlaceholderNoteQuietly covers a placeholder).
func TestShellAnnouncesStartupNoticeOnceShown(t *testing.T) {
	shell := newAuditShell(t)
	shell.selectModule(t, core.ModuleSettings)
	watch, err := uiatest.WatchAnnouncements()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(watch.Stop)

	shell.announceNotices()
	if got := shell.note.Text(); got != "" {
		t.Fatalf("the Settings view's note = %q, want none", got)
	}
	shell.selectModule(t, core.ModuleScreenshot)
	shell.selectModule(t, core.ModuleSettings)
	shell.selectModule(t, core.ModuleScreenshot)
	if got := watch.Count(shell.note.Handle()); got != 0 {
		t.Fatalf("an empty note raised %d announcements", got)
	}
	shell.notice = "The settings file was recovered."
	shell.updateView()
	shell.announceNotices()
	if got := watch.Count(shell.note.Handle()); got != 2 {
		t.Fatalf("note announcements after the warning was set and the window appeared = %d, want 2", got)
	}
}

// TestShellShowsAPlaceholderNoteQuietly pins that the placeholder note of a
// module without a native view belongs to its view, as its title and
// description do, and is shown without an announcement (updateView): it
// would otherwise be announced on every visit to the module.
func TestShellShowsAPlaceholderNoteQuietly(t *testing.T) {
	shell := newCommandTestShell(t, nil)
	watch, err := uiatest.WatchAnnouncements()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(watch.Stop)

	shell.updateView()
	shell.announceNotices()
	shell.updateView()
	if got := shell.note.Text(); got != ui.PlaceholderNote {
		t.Fatalf("a placeholder module's note = %q, want the placeholder note", got)
	}
	if got := watch.Count(shell.note.Handle()); got != 0 {
		t.Fatalf("the placeholder note raised %d announcements", got)
	}
}

// noticeProbe stands in for a module view's notice announcement
// (nativeNoticeAnnouncer), counting the shell's requests.
type noticeProbe struct {
	nativeView
	requests *int
}

func (p noticeProbe) NativeAnnounceNotice() {
	*p.requests++
}

// TestShellAnnouncesEachModulesNoticeWhenFirstShown pins that the shell asks
// a module to announce its notice the first time the module is shown in the
// visible window, and not again (announceModuleNotice): a module hidden at
// startup, such as Screenshot with Image Resizer remembered, takes its
// startup notice, a global shortcut another application holds, where no
// screen reader hears it.
func TestShellAnnouncesEachModulesNoticeWhenFirstShown(t *testing.T) {
	shell := newAuditShell(t)
	requests := 0
	screenshotView := shell.nativeViews[core.ModuleScreenshot]
	shell.nativeViews[core.ModuleScreenshot] = noticeProbe{nativeView: screenshotView, requests: &requests}
	t.Cleanup(func() { shell.nativeViews[core.ModuleScreenshot] = screenshotView })

	shell.selectModule(t, core.ModuleScreenshot)
	shell.selectModule(t, core.ModuleImageResizer)
	if requests != 0 {
		t.Fatalf("Screenshot was asked %d time(s) to announce its notice before the window appeared", requests)
	}
	shell.announceNotices()
	if requests != 0 {
		t.Fatalf("Screenshot, hidden as the window appeared, was asked %d time(s) to announce its notice", requests)
	}
	shell.selectModule(t, core.ModuleScreenshot)
	if requests != 1 {
		t.Fatalf("showing Screenshot first asked %d time(s) for its notice, want 1", requests)
	}
	shell.selectModule(t, core.ModuleImageResizer)
	shell.selectModule(t, core.ModuleScreenshot)
	if requests != 1 {
		t.Fatalf("showing Screenshot again asked %d time(s) in all for its notice, want still 1", requests)
	}
}
