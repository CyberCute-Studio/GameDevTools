//go:build windows

package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer"
	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot/execute"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot/overlay"
	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	core "github.com/CyberCute-Studio/GameDevTools/internal/shell"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui/uiatest"
)

var (
	procGetWindow       = user32.NewProc("GetWindow")
	procGetClassNameW   = user32.NewProc("GetClassNameW")
	procGetWindowTextW  = user32.NewProc("GetWindowTextW")
	procPeekMessageW    = user32.NewProc("PeekMessageW")
	procGetActiveWindow = user32.NewProc("GetActiveWindow")
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
}

func newAuditShell(t *testing.T) *auditShell {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	dir := t.TempDir()
	store := settings.NewStore(settings.Paths{ConfigFile: filepath.Join(dir, "settings.json")})
	preset.RegisterValidators(store)
	captureStore, err := screenshot.NewOptionsStore(store, filepath.Join(dir, "Screenshots"))
	if err != nil {
		t.Fatal(err)
	}
	resizeStore, err := imageresizer.NewOptionsStore(store, filepath.Join(dir, "Resized"))
	if err != nil {
		t.Fatal(err)
	}
	value := settings.Default()
	screenshotPresets, err := preset.NewRepository(core.ModuleScreenshot, store)
	if err != nil {
		t.Fatal(err)
	}
	resizerPresets, err := preset.NewRepository(core.ModuleImageResizer, store)
	if err != nil {
		t.Fatal(err)
	}
	capture, err := screenshot.NewOptionsManager(captureStore, &value)
	if err != nil {
		t.Fatal(err)
	}
	// A combination nothing else uses, so the view's global registration
	// cannot take the developer's own Ctrl+Alt+S while the test runs.
	shortcut, err := screenshot.ParseShortcut("Ctrl+Alt+Shift+F24")
	if err != nil {
		t.Fatal(err)
	}
	if err := capture.SetShortcut(shortcut); err != nil {
		t.Fatal(err)
	}
	resize, err := imageresizer.NewOptionsManager(resizeStore, &value)
	if err != nil {
		t.Fatal(err)
	}
	monitor := display.NewMonitor()
	executor := execute.NewService(monitor)
	screenshotModule, err := screenshot.New(screenshotPresets, capture, &value, monitor, executor.FullDisplay, executor.Rectangle, overlay.NewController(monitor))
	if err != nil {
		t.Fatal(err)
	}
	resizerModule, err := imageresizer.New(resizerPresets, resize, &value)
	if err != nil {
		t.Fatal(err)
	}
	navigator := core.NewNavigator()
	for _, module := range []core.Module{screenshotModule, resizerModule, settings.New()} {
		if err := navigator.Register(module); err != nil {
			t.Fatal(err)
		}
	}
	shell, err := newNativeShell(navigator, store, &value, nil, monitor, "")
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
	return &auditShell{nativeShell: shell, capture: capture, resize: resize}
}

// selectModule switches to the module as clicking its navigation button
// does.
func (a *auditShell) selectModule(t *testing.T, id core.ModuleID) {
	t.Helper()
	for command, module := range a.navIDs {
		if module == id {
			shellWindowProc(a.hwnd, wmCommand, uintptr(command), nil)
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
// beside Tab: the arrow keys move between the modules, switching to each, as
// in any Windows radio group.
func TestNavigationArrowKeysSwitchModules(t *testing.T) {
	shell := newAuditShell(t)
	shell.selectModule(t, core.ModuleScreenshot)
	procSetFocus.Call(uintptr(shell.navButtons[core.ModuleScreenshot].Handle()))
	shell.press(wmKeyDown, vkDown)
	if current := shell.navigator.Current().ID(); current != core.ModuleImageResizer || !shell.navButtons[core.ModuleImageResizer].HasFocus() {
		t.Fatalf("Down from Screenshot: current %s, focus %q; want Image Resizer focused and shown", current, windowText(shell.focused()))
	}
	shell.press(wmKeyDown, vkUp)
	if current := shell.navigator.Current().ID(); current != core.ModuleScreenshot || !shell.navButtons[core.ModuleScreenshot].HasFocus() {
		t.Fatalf("Up from Image Resizer: current %s, focus %q; want Screenshot focused and shown", current, windowText(shell.focused()))
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
// can no longer take it, as when its module is hidden.
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

	shell.selectModule(t, core.ModuleImageResizer)
	activate()
	if !shell.navButtons[core.ModuleImageResizer].HasFocus() {
		t.Fatalf("reactivating with the focused control's module hidden focused %q, want the Image Resizer navigation button", windowText(shell.focused()))
	}
}
