//go:build windows

package app

import (
	"math"
	"runtime"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
	core "github.com/CyberCute-Studio/GameDevTools/internal/shell"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

var (
	procGetBkMode             = gdi32.NewProc("GetBkMode")
	procGetTextColor          = gdi32.NewProc("GetTextColor")
	commandTestShellClassOnce sync.Once
	commandTestShellClassErr  error
)

func TestFitStartupBounds(t *testing.T) {
	tests := []struct {
		name                  string
		x, y, width, height   int32
		work                  rect
		wantX, wantY          int32
		wantWidth, wantHeight int32
	}{
		{
			name: "negative monitor origin", x: 0, y: 0, width: 1920, height: 1080,
			work:  rect{Left: -1920, Top: -1080, Right: 0, Bottom: 0},
			wantX: -1920, wantY: -1080, wantWidth: 1920, wantHeight: 1080,
		},
		{
			name: "clamps to small work area", x: 100, y: 100, width: 1100, height: 700,
			work:  rect{Left: 0, Top: 0, Right: 800, Bottom: 600},
			wantX: 0, wantY: 0, wantWidth: 800, wantHeight: 600,
		},
		{
			name: "wide intermediate at right and bottom limits", x: math.MaxInt32 - 50, y: math.MaxInt32 - 50, width: 100, height: 100,
			work:  rect{Left: math.MaxInt32 - 1000, Top: math.MaxInt32 - 1000, Right: math.MaxInt32, Bottom: math.MaxInt32},
			wantX: math.MaxInt32 - 100, wantY: math.MaxInt32 - 100, wantWidth: 100, wantHeight: 100,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			x, y, width, height := fitStartupBounds(test.x, test.y, test.width, test.height, test.work)
			if x != test.wantX || y != test.wantY || width != test.wantWidth || height != test.wantHeight {
				t.Fatalf("fitStartupBounds() = (%d, %d, %d, %d), want (%d, %d, %d, %d)", x, y, width, height, test.wantX, test.wantY, test.wantWidth, test.wantHeight)
			}
		})
	}
}

// TestWorkspaceOffset pins how a window's saved bounds are turned from the
// workspace coordinates GetWindowPlacement gives into screen coordinates
// (recordWindow): by the offset of the monitor's work area from its corner,
// which a taskbar docked at its top or left makes, and which a taskbar at
// its bottom or right does not.
func TestWorkspaceOffset(t *testing.T) {
	primary := rect{Right: 1920, Bottom: 1080}
	secondary := rect{Left: 1920, Top: -200, Right: 3840, Bottom: 880}
	for _, test := range []struct {
		name          string
		monitor, work rect
		want          point
	}{
		{"taskbar at the bottom", primary, rect{Right: 1920, Bottom: 1032}, point{}},
		{"taskbar at the top", primary, rect{Top: 48, Right: 1920, Bottom: 1080}, point{Y: 48}},
		{"taskbar at the left", primary, rect{Left: 60, Right: 1920, Bottom: 1080}, point{X: 60}},
		{"secondary monitor without a taskbar", secondary, secondary, point{}},
		{"secondary monitor with a taskbar at the top", secondary, rect{Left: 1920, Top: -152, Right: 3840, Bottom: 880}, point{Y: 48}},
	} {
		if got := workspaceOffset(monitorInfo{Monitor: test.monitor, Work: test.work}); got != test.want {
			t.Errorf("%s: workspaceOffset = %+v, want %+v", test.name, got, test.want)
		}
	}
}

func TestStaticTextBrushUsesSystemColors(t *testing.T) {
	dc, _, err := procGetDC.Call(0)
	if dc == 0 {
		t.Fatalf("GetDC(desktop): %v", err)
	}
	defer procReleaseDC.Call(0, dc)
	previousColor, _, _ := procSetTextColor.Call(dc, 0x00010203)
	defer procSetTextColor.Call(dc, previousColor)
	previousMode, _, _ := procSetBkMode.Call(dc, transparent)
	defer procSetBkMode.Call(dc, previousMode)

	if brush := staticTextBrush(dc); brush == 0 {
		t.Fatal("staticTextBrush() returned a null brush")
	}
	wantColor, _, _ := procGetSysColor.Call(colorWindowText)
	gotColor, _, _ := procGetTextColor.Call(dc)
	if gotColor != wantColor {
		t.Fatalf("text color = %#x, want system COLOR_WINDOWTEXT %#x", gotColor, wantColor)
	}
	if gotMode, _, _ := procGetBkMode.Call(dc); gotMode != transparent {
		t.Fatalf("background mode = %d, want TRANSPARENT", gotMode)
	}
}

func TestCalculateShellLayout(t *testing.T) {
	t.Run("standard window keeps navigation beside content", func(t *testing.T) {
		plan := calculateShellLayout(rect{Right: 1100, Bottom: 700}, 96, 3, shellTextHeights{})
		if plan.compact {
			t.Fatal("standard window uses compact navigation")
		}
		if plan.virtualHeight != 700 {
			t.Fatalf("virtual height = %d, want 700", plan.virtualHeight)
		}
	})

	t.Run("high DPI uses full-width navigation and scrolling", func(t *testing.T) {
		client := rect{Right: 1920, Bottom: 970}
		plan := calculateShellLayout(client, 480, 3, shellTextHeights{})
		if !plan.compact {
			t.Fatal("high-DPI window retains clipped side navigation")
		}
		if plan.navWidth != plan.contentWidth {
			t.Fatalf("navigation width = %d, content width = %d", plan.navWidth, plan.contentWidth)
		}
		navBottom := plan.navY + 3*plan.navHeight + 2*40
		if navBottom > plan.virtualHeight-plan.margin {
			t.Fatalf("navigation bottom = %d, scrollable bottom = %d", navBottom, plan.virtualHeight-plan.margin)
		}
		if actionBottom := plan.actionY + 220; actionBottom > plan.virtualHeight-plan.margin {
			t.Fatalf("primary action bottom = %d, scrollable bottom = %d", actionBottom, plan.virtualHeight-plan.margin)
		}
		if plan.virtualHeight <= client.Bottom {
			t.Fatalf("virtual height = %d, want scrolling beyond client height %d", plan.virtualHeight, client.Bottom)
		}
	})

	t.Run("wrapped text contributes its measured height", func(t *testing.T) {
		text := shellTextHeights{title: 240, description: 900, note: 400}
		plan := calculateShellLayout(rect{Right: 1280, Bottom: 650}, 480, 3, text)
		if plan.titleHeight != text.title || plan.descriptionHeight != text.description || plan.noteHeight != text.note {
			t.Fatalf("layout text heights = %+v, want %+v", plan, text)
		}
		if noteBottom := plan.noteY + plan.noteHeight; noteBottom != plan.virtualHeight-plan.margin {
			t.Fatalf("note bottom = %d, scrollable bottom = %d", noteBottom, plan.virtualHeight-plan.margin)
		}
	})
}

func TestShellWindowProcRoutesNativeCommandsAndRelayouts(t *testing.T) {
	native := &recordingNativeView{}
	shell := newCommandTestShell(t, native)
	shellWindowProc(shell.hwnd, wmCommand, uintptr(206)|uintptr(1)<<16, 0)
	if native.commandID != 206 || native.notification != 1 {
		t.Fatalf("routed command = (%d, %d), want (206, 1)", native.commandID, native.notification)
	}
	if native.layouts == 0 {
		t.Fatal("native command did not trigger a layout")
	}
}

func TestShellWindowProcForwardsDisplayChanges(t *testing.T) {
	shell := newCommandTestShell(t, nil)
	monitor := display.NewMonitor()
	shell.displays = monitor
	calls := 0
	monitor.Subscribe(func() { calls++ })
	shellWindowProc(shell.hwnd, wmDisplayChange, 0, 0)
	if calls != 1 {
		t.Fatalf("display-change callback invocations = %d, want 1", calls)
	}
	// A scaling change reaches the same subscribers: the physical topology
	// is unchanged, but every DPI-derived measurement is invalidated
	// (SCR-054).
	shellWindowProc(shell.hwnd, wmDPIChanged, uintptr(96)|uintptr(96)<<16, 0)
	if calls != 2 {
		t.Fatalf("DPI-change callback invocations = %d, want 2", calls)
	}
	shell.displays = nil
	shellWindowProc(shell.hwnd, wmDisplayChange, 0, 0)
	shellWindowProc(shell.hwnd, wmDPIChanged, uintptr(96)|uintptr(96)<<16, 0)
	if calls != 2 {
		t.Fatalf("nil monitor panicked or renotified: %d calls", calls)
	}
}

func TestShellWindowProcScrollsFocusedComboIntoView(t *testing.T) {
	shell := newCommandTestShell(t, &recordingNativeView{height: 1000})
	combo, err := ui.NewComboBox(shell.hwnd, 201)
	if err != nil {
		t.Fatal(err)
	}
	combo.Move(0, 500, 200, 200)
	info := scrollInfo{CbSize: uint32(unsafe.Sizeof(scrollInfo{})), FMask: sifRange | sifPage, NMax: 1000, NPage: 200}
	procSetScrollInfo.Call(uintptr(shell.hwnd), sbVert, uintptr(unsafe.Pointer(&info)), 1)
	combo.Focus()
	procSendMessageW.Call(uintptr(shell.hwnd), wmCommand, uintptr(201)|uintptr(cbnSetFocus)<<16, uintptr(combo.Handle()))
	if shell.scrollY == 0 {
		t.Fatal("combo focus did not scroll the control into view")
	}
}

func TestShellWindowProcScrollsFocusedButtonIntoView(t *testing.T) {
	shell := newCommandTestShell(t, &recordingNativeView{height: 1000})
	button, err := ui.NewButton(shell.hwnd, "Create Preset", ui.ButtonOptions{ID: 206})
	if err != nil {
		t.Fatal(err)
	}
	button.Move(0, 500, 200, 36)
	info := scrollInfo{CbSize: uint32(unsafe.Sizeof(scrollInfo{})), FMask: sifRange | sifPage, NMax: 1000, NPage: 200}
	procSetScrollInfo.Call(uintptr(shell.hwnd), sbVert, uintptr(unsafe.Pointer(&info)), 1)
	const bnSetFocusNotification = 6
	procSendMessageW.Call(uintptr(shell.hwnd), wmCommand, uintptr(206)|uintptr(bnSetFocusNotification)<<16, uintptr(button.Handle()))
	if shell.scrollY == 0 {
		t.Fatal("button focus did not scroll the control into view")
	}
}

type commandTestView struct{}

func (commandTestView) Title() string         { return "Test" }
func (commandTestView) Description() string   { return "Test view" }
func (commandTestView) PrimaryAction() string { return "" }

type commandTestModule struct{}

func (commandTestModule) ID() core.ModuleID       { return core.ModuleScreenshot }
func (commandTestModule) NavigationLabel() string { return "Test" }
func (commandTestModule) View() core.View         { return commandTestView{} }

type recordingNativeView struct {
	commandID    int32
	notification uint16
	layouts      int
	height       int32
}

func (*recordingNativeView) NativeCreate(windows.Handle) error { return nil }
func (*recordingNativeView) NativeFont(windows.Handle)         {}
func (*recordingNativeView) NativeShow(bool)                   {}
func (n *recordingNativeView) NativeHeight(int32, func(int32) int32, func(string, int32) int32) int32 {
	return n.height
}
func (n *recordingNativeView) NativeLayout(int32, int32, int32, func(int32) int32, func(string, int32) int32) {
	n.layouts++
}
func (n *recordingNativeView) NativeCommand(id int32, notification uint16) bool {
	n.commandID = id
	n.notification = notification
	return true
}
func (*recordingNativeView) NativeDestroy() {}

// hotkeyRecordingView records NativeHotkey calls so the shell's WM_HOTKEY
// routing can be observed.
type hotkeyRecordingView struct {
	recordingNativeView
	hotkeyID int32
	calls    int
	handled  bool
}

func (n *hotkeyRecordingView) NativeHotkey(id int32) bool {
	n.calls++
	n.hotkeyID = id
	return n.handled
}

// TestIconPixelsFollowTheDPI pins the sizes the window asks for at the common
// display scales, each of which the packaged icon holds or is scaled down to.
func TestIconPixelsFollowTheDPI(t *testing.T) {
	for _, test := range []struct {
		base uintptr
		dpi  uint32
		want uintptr
	}{
		{16, 96, 16}, {32, 96, 32},
		{16, 120, 20}, {32, 120, 40},
		{16, 144, 24}, {32, 144, 48},
		{16, 192, 32}, {32, 192, 64},
		{16, 288, 48}, {32, 288, 96},
	} {
		if got := iconPixels(test.base, test.dpi); got != test.want {
			t.Errorf("iconPixels(%d, %d) = %d, want %d", test.base, test.dpi, got, test.want)
		}
	}
}

// TestShellWindowProcRoutesHotkeys covers the shell half of SCR-002: a
// WM_HOTKEY reaching the shell window is offered to the module views with
// its registration identifier, and a consumed hotkey triggers a relayout.
func TestShellWindowProcRoutesHotkeys(t *testing.T) {
	native := &hotkeyRecordingView{handled: true}
	shell := newCommandTestShell(t, native)
	shellWindowProc(shell.hwnd, wmHotkey, 1, 0)
	if native.calls != 1 || native.hotkeyID != 1 {
		t.Fatalf("routed hotkey = (%d calls, id %d), want (1, 1)", native.calls, native.hotkeyID)
	}
	if native.layouts == 0 {
		t.Fatal("consumed hotkey did not trigger a layout")
	}

	native.handled = false
	layouts := native.layouts
	shellWindowProc(shell.hwnd, wmHotkey, 7, 0)
	if native.calls != 2 || native.hotkeyID != 7 {
		t.Fatalf("unconsumed hotkey = (%d calls, id %d), want (2, 7)", native.calls, native.hotkeyID)
	}
	if native.layouts != layouts {
		t.Fatal("unconsumed hotkey triggered a layout")
	}
}

// TestShellWindowProcForwardsEditSetFocusToView pins the regression where
// the shell's scroll-into-view branch swallowed EN_SETFOCUS before the
// module view saw it: the Screenshot shortcut field suspends its global
// hotkey on that notification, so swallowing it kept the hotkey live while
// the user edited the combination.
func TestShellWindowProcForwardsEditSetFocusToView(t *testing.T) {
	native := &recordingNativeView{}
	shell := newCommandTestShell(t, native)
	edit, err := ui.NewTextInput(shell.hwnd, "Shortcut", 401)
	if err != nil {
		t.Fatal(err)
	}
	procSendMessageW.Call(uintptr(shell.hwnd), wmCommand, uintptr(401)|uintptr(enSetFocus)<<16, uintptr(edit.Edit.Handle()))
	if native.commandID != 401 || native.notification != enSetFocus {
		t.Fatalf("routed set-focus = (%d, %d), want (401, %d)", native.commandID, native.notification, enSetFocus)
	}
	if native.layouts == 0 {
		t.Fatal("consumed set-focus notification did not trigger a layout")
	}
}

// filterRecordingView records NativeFilterMessage calls so the shell's
// pre-dispatch hook can be observed.
type filterRecordingView struct {
	recordingNativeView
	messages int
	handled  bool
}

func (n *filterRecordingView) NativeFilterMessage(message unsafe.Pointer) bool {
	n.messages++
	return n.handled
}

func TestFilterModuleMessageDelegatesToNativeViews(t *testing.T) {
	native := &filterRecordingView{}
	shell := newCommandTestShell(t, native)

	var message msg
	if shell.filterModuleMessage(&message) {
		t.Fatal("filter claimed a message the view declined")
	}
	if native.messages != 1 {
		t.Fatalf("NativeFilterMessage calls = %d, want 1", native.messages)
	}
	native.handled = true
	if !shell.filterModuleMessage(&message) {
		t.Fatal("filter dropped a message the view handled")
	}
	if native.messages != 2 {
		t.Fatalf("NativeFilterMessage calls = %d, want 2", native.messages)
	}
}

// shutdownRecordingView records a NativeShutdown call and blocks until the
// test releases it, so waitForBackgroundWork can be observed waiting.
type shutdownRecordingView struct {
	recordingNativeView
	started chan struct{}
	release chan struct{}
}

func (n *shutdownRecordingView) NativeShutdown() {
	close(n.started)
	<-n.release
}

func TestWaitForBackgroundWorkWaitsForNativeShutdown(t *testing.T) {
	native := &shutdownRecordingView{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	shell := newCommandTestShell(t, native)

	done := make(chan struct{})
	go func() {
		shell.waitForBackgroundWork()
		close(done)
	}()
	select {
	case <-native.started:
	case <-time.After(5 * time.Second):
		t.Fatal("waitForBackgroundWork did not call NativeShutdown")
	}
	select {
	case <-done:
		t.Fatal("waitForBackgroundWork returned while NativeShutdown was blocked")
	case <-time.After(100 * time.Millisecond):
	}
	close(native.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("waitForBackgroundWork did not return after NativeShutdown finished")
	}
}

// TestWaitForBackgroundWorkWaitsForEveryViewAtOnce pins that the shutdown
// waits for every module view's background work at the same time, so a
// capture still delivering its outputs does not hold up the start of a
// cancelled job's bounded wait, and the exit takes the longest of the waits
// rather than their sum; it returns only once every view's work finished.
func TestWaitForBackgroundWorkWaitsForEveryViewAtOnce(t *testing.T) {
	views := []*shutdownRecordingView{
		{started: make(chan struct{}), release: make(chan struct{})},
		{started: make(chan struct{}), release: make(chan struct{})},
	}
	releases := make([]func(), len(views))
	for index, view := range views {
		var once sync.Once
		releases[index] = func() { once.Do(func() { close(view.release) }) }
		t.Cleanup(releases[index])
	}
	shell := newCommandTestShell(t, views[0])
	shell.nativeViews[core.ModuleImageResizer] = views[1]

	done := make(chan struct{})
	go func() {
		shell.waitForBackgroundWork()
		close(done)
	}()
	for index, view := range views {
		select {
		case <-view.started:
		case <-time.After(5 * time.Second):
			t.Fatalf("view %d's wait did not start while the other's was still running", index)
		}
	}
	releases[0]()
	select {
	case <-done:
		t.Fatal("waitForBackgroundWork returned while a view's work was still running")
	case <-time.After(100 * time.Millisecond):
	}
	releases[1]()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("waitForBackgroundWork did not return once every view's work finished")
	}
}

func newCommandTestShell(t *testing.T, native nativeView) *nativeShell {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	commandTestShellClassOnce.Do(func() {
		commandTestShellClassErr = registerShellClass()
	})
	if commandTestShellClassErr != nil {
		t.Fatal(commandTestShellClassErr)
	}
	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(utf16(shellClassName))),
		uintptr(unsafe.Pointer(utf16("GameDevTools shell command test"))),
		wsOverlappedWindow,
		0, 0, 400, 200,
		0, 0, 0, 0,
	)
	if hwnd == 0 {
		t.Fatalf("CreateWindowExW(test shell): %v", err)
	}
	navigator := core.NewNavigator()
	if err := navigator.Register(commandTestModule{}); err != nil {
		t.Fatal(err)
	}
	note, err := ui.NewStatusArea(windows.Handle(hwnd))
	if err != nil {
		t.Fatal(err)
	}
	action, err := ui.NewButton(windows.Handle(hwnd), "", ui.ButtonOptions{ID: actionCommand, Primary: true, Group: true, Disabled: true})
	if err != nil {
		t.Fatal(err)
	}
	shell := &nativeShell{
		navigator:   navigator,
		hwnd:        windows.Handle(hwnd),
		dpi:         96,
		navButtons:  make(map[core.ModuleID]*ui.Button),
		navIDs:      make(map[uint16]core.ModuleID),
		nativeViews: make(map[core.ModuleID]nativeView),
		action:      action,
		note:        note,
		// A test that expects a question substitutes its own answer; any
		// other would block on a real message box.
		confirm: func(_ windows.Handle, title, message string) bool {
			t.Errorf("unexpected question %q: %q", title, message)
			return false
		},
	}
	if native != nil {
		shell.nativeViews[core.ModuleScreenshot] = native
	}
	shellStates[shell.hwnd] = shell
	t.Cleanup(func() {
		delete(shellStates, shell.hwnd)
		note.Destroy()
		procDestroyWindow.Call(hwnd)
	})
	return shell
}

// dropFilesHeader mirrors the Win32 DROPFILES structure: the offset of the
// double-NUL-terminated, wide-character path list that follows it.
type dropFilesHeader struct {
	pFiles uint32
	x      int32
	y      int32
	fNC    int32
	fWide  int32
}

var (
	procGlobalAlloc   = kernel32.NewProc("GlobalAlloc")
	procGlobalLock    = kernel32.NewProc("GlobalLock")
	procGlobalUnlock  = kernel32.NewProc("GlobalUnlock")
	procRtlMoveMemory = kernel32.NewProc("RtlMoveMemory")
)

// newTestDrop packs paths into a real HDROP so the shell's DragQueryFileW
// parsing is exercised against the same payload Explorer produces. The
// shell's DragFinish releases the handle, so the test must not free it.
func newTestDrop(t *testing.T, paths []string) uintptr {
	t.Helper()
	var names []uint16
	for _, path := range paths {
		wide, err := windows.UTF16FromString(path)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, wide...)
	}
	names = append(names, 0)
	header := dropFilesHeader{pFiles: uint32(unsafe.Sizeof(dropFilesHeader{})), fWide: 1}
	payload := make([]byte, unsafe.Sizeof(header)+uintptr(len(names))*2)
	payload[0] = byte(header.pFiles)
	payload[16] = byte(header.fWide)
	for index, unit := range names {
		payload[int(unsafe.Sizeof(header))+2*index] = byte(unit)
		payload[int(unsafe.Sizeof(header))+2*index+1] = byte(unit >> 8)
	}
	const gmemMoveableZeroInit = 0x0042
	handle, _, err := procGlobalAlloc.Call(gmemMoveableZeroInit, uintptr(len(payload)))
	if handle == 0 {
		t.Fatalf("GlobalAlloc(HDROP): %v", err)
	}
	base, _, err := procGlobalLock.Call(handle)
	if base == 0 {
		t.Fatalf("GlobalLock(HDROP): %v", err)
	}
	procRtlMoveMemory.Call(base, uintptr(unsafe.Pointer(&payload[0])), uintptr(len(payload)))
	procGlobalUnlock.Call(handle)
	return handle
}

// dropRecordingView records NativeDropFiles calls so the shell's
// WM_DROPFILES routing can be observed.
type dropRecordingView struct {
	recordingNativeView
	paths []string
}

func (n *dropRecordingView) NativeDropFiles(paths []string) {
	n.paths = append(n.paths, paths...)
}

// TestShellWindowProcRoutesDroppedFiles covers the shell half of RSZ-002: a
// WM_DROPFILES reaching the shell window is unpacked and delivered to the
// active module when it accepts drops, with the drop handle released either
// way.
func TestShellWindowProcRoutesDroppedFiles(t *testing.T) {
	native := &dropRecordingView{}
	shell := newCommandTestShell(t, native)
	shellWindowProc(shell.hwnd, wmDropFiles, newTestDrop(t, []string{`C:\Images\a.png`, `C:\Images\b.jpg`}), 0)
	if len(native.paths) != 2 || native.paths[0] != `C:\Images\a.png` || native.paths[1] != `C:\Images\b.jpg` {
		t.Fatalf("dropped paths = %v", native.paths)
	}
	if native.layouts == 0 {
		t.Fatal("accepted drop did not trigger a layout")
	}

	layouts := native.layouts
	shellWindowProc(shell.hwnd, wmDropFiles, newTestDrop(t, nil), 0)
	if len(native.paths) != 2 {
		t.Fatalf("empty drop delivered paths: %v", native.paths)
	}
	if native.layouts != layouts {
		t.Fatal("empty drop triggered a layout")
	}
}

func TestShellWindowProcIgnoresDropsForNonTargets(t *testing.T) {
	native := &recordingNativeView{}
	shell := newCommandTestShell(t, native)
	shellWindowProc(shell.hwnd, wmDropFiles, newTestDrop(t, []string{`C:\Images\a.png`}), 0)
	if native.layouts != 0 {
		t.Fatal("drop for a module without drop support triggered a layout")
	}
}

// TestChildDropForwardsToShellHandler pins the delivery half of RSZ-002:
// WM_DROPFILES goes to the window under the cursor, which over a module view
// is a child control, so the child's subclass must forward the drop to the
// shell's own routing for "drop anywhere on the view" to hold.
func TestChildDropForwardsToShellHandler(t *testing.T) {
	native := &dropRecordingView{}
	shell := newCommandTestShell(t, native)
	child, err := ui.NewButton(shell.hwnd, "Child", ui.ButtonOptions{ID: 209})
	if err != nil {
		t.Fatal(err)
	}
	shell.updateDropAcceptance()
	procSendMessageW.Call(uintptr(child.Handle()), wmDropFiles, newTestDrop(t, []string{`C:\Images\a.png`}), 0)
	if len(native.paths) != 1 || native.paths[0] != `C:\Images\a.png` {
		t.Fatalf("dropped paths through child = %v", native.paths)
	}
	if native.layouts == 0 {
		t.Fatal("child drop did not trigger a layout")
	}
}

// TestShellWindowProcScrollsFocusedListIntoView covers the source list
// (RSZ-005): a list box reports LBN_SETFOCUS rather than the button, edit,
// or combo notification, and tabbing to it must scroll it into view like
// every other control.
func TestShellWindowProcScrollsFocusedListIntoView(t *testing.T) {
	shell := newCommandTestShell(t, &recordingNativeView{height: 1000})
	list, err := ui.NewListBox(shell.hwnd, 601)
	if err != nil {
		t.Fatal(err)
	}
	list.Move(0, 500, 200, 140)
	info := scrollInfo{CbSize: uint32(unsafe.Sizeof(scrollInfo{})), FMask: sifRange | sifPage, NMax: 1000, NPage: 200}
	procSetScrollInfo.Call(uintptr(shell.hwnd), sbVert, uintptr(unsafe.Pointer(&info)), 1)
	list.Focus()
	procSendMessageW.Call(uintptr(shell.hwnd), wmCommand, uintptr(601)|uintptr(lbnSetFocus)<<16, uintptr(list.Handle()))
	if shell.scrollY == 0 {
		t.Fatal("list focus did not scroll the control into view")
	}
}

// TestShellWindowProcIgnoresAComboLosingFocus pins that a combo box's
// CBN_KILLFOCUS, whose code LBN_SETFOCUS shares, neither scrolls the combo
// box into view nor makes it the control restoreFocus gives the focus back
// to: the focus has already left it.
func TestShellWindowProcIgnoresAComboLosingFocus(t *testing.T) {
	shell := newCommandTestShell(t, &recordingNativeView{height: 1000})
	combo, err := ui.NewComboBox(shell.hwnd, 201)
	if err != nil {
		t.Fatal(err)
	}
	combo.Move(0, 500, 200, 200)
	info := scrollInfo{CbSize: uint32(unsafe.Sizeof(scrollInfo{})), FMask: sifRange | sifPage, NMax: 1000, NPage: 200}
	procSetScrollInfo.Call(uintptr(shell.hwnd), sbVert, uintptr(unsafe.Pointer(&info)), 1)
	const cbnKillFocus = 4
	procSendMessageW.Call(uintptr(shell.hwnd), wmCommand, uintptr(201)|uintptr(cbnKillFocus)<<16, uintptr(combo.Handle()))
	if shell.scrollY != 0 {
		t.Fatalf("a combo box losing the focus scrolled the view to %d", shell.scrollY)
	}
	if shell.focus == combo.Handle() {
		t.Fatal("a combo box losing the focus became the control to give the focus back to")
	}
}

// TestShellWindowProcIgnoresAListCancellingItsSelection pins the same for a
// list box's LBN_SELCANCEL, whose code CBN_SETFOCUS shares: a list box that
// does not hold the focus did not gain it.
func TestShellWindowProcIgnoresAListCancellingItsSelection(t *testing.T) {
	shell := newCommandTestShell(t, &recordingNativeView{height: 1000})
	list, err := ui.NewListBox(shell.hwnd, 601)
	if err != nil {
		t.Fatal(err)
	}
	list.Move(0, 500, 200, 140)
	info := scrollInfo{CbSize: uint32(unsafe.Sizeof(scrollInfo{})), FMask: sifRange | sifPage, NMax: 1000, NPage: 200}
	procSetScrollInfo.Call(uintptr(shell.hwnd), sbVert, uintptr(unsafe.Pointer(&info)), 1)
	const lbnSelCancel = 3
	procSendMessageW.Call(uintptr(shell.hwnd), wmCommand, uintptr(601)|uintptr(lbnSelCancel)<<16, uintptr(list.Handle()))
	if shell.scrollY != 0 {
		t.Fatalf("a list box cancelling its selection scrolled the view to %d", shell.scrollY)
	}
	if shell.focus == list.Handle() {
		t.Fatal("a list box cancelling its selection became the control to give the focus back to")
	}
}

type actionTestView struct{}

func (actionTestView) Title() string         { return "Action" }
func (actionTestView) Description() string   { return "Action view" }
func (actionTestView) PrimaryAction() string { return "Act" }

type actionTestModule struct{}

func (actionTestModule) ID() core.ModuleID       { return core.ModuleScreenshot }
func (actionTestModule) NavigationLabel() string { return "Action" }
func (actionTestModule) View() core.View         { return actionTestView{} }

// availabilityView runs the primary action and reports whether it is
// available, as the Image Resizer does for Start Resize while a job runs.
type availabilityView struct {
	recordingNativeView
	available bool
	actions   int
}

func (v *availabilityView) NativeAction()               { v.actions++ }
func (v *availabilityView) NativeActionAvailable() bool { return v.available }

// TestShellDisablesAnUnavailablePrimaryAction pins that the shell disables
// the primary action button while the module view reports the action
// unavailable, re-reading it on every relayout, such as the one that follows
// the action itself, and enables it again once it is available. A button it
// disables while the button has the keyboard focus first hands the focus on
// to the next control, so the keyboard is never stranded on it (AC-A03).
func TestShellDisablesAnUnavailablePrimaryAction(t *testing.T) {
	native := &availabilityView{available: true}
	shell := newCommandTestShell(t, native)
	navigator := core.NewNavigator()
	if err := navigator.Register(actionTestModule{}); err != nil {
		t.Fatal(err)
	}
	shell.navigator = navigator
	next, err := ui.NewButton(shell.hwnd, "Next", ui.ButtonOptions{ID: 210})
	if err != nil {
		t.Fatal(err)
	}
	isEnabled := func() bool {
		result, _, _ := procIsWindowEnabled.Call(uintptr(shell.action.Handle()))
		return result != 0
	}

	shell.layout()
	if !isEnabled() {
		t.Fatal("an available primary action was disabled")
	}
	native.available = false
	shell.action.Focus()
	shellWindowProc(shell.hwnd, wmCommand, uintptr(actionCommand), 0)
	if native.actions != 1 || isEnabled() {
		t.Fatalf("after the action: %d actions, enabled = %v; want 1 and disabled", native.actions, isEnabled())
	}
	if shell.action.HasFocus() || !next.HasFocus() {
		t.Fatal("the disabled primary action kept the keyboard focus instead of handing it to the next control")
	}
	native.available = true
	shell.layout()
	if !isEnabled() {
		t.Fatal("the primary action stayed disabled once it was available again")
	}

	// A view that does not run the action leaves it disabled whatever it
	// reports.
	shell.nativeViews[core.ModuleScreenshot] = &recordingNativeView{}
	shell.layout()
	if isEnabled() {
		t.Fatal("a primary action no view runs was enabled")
	}
}

// TestShellHandsAnUnavailableActionsFocusToTheWindow pins that a primary
// action disabled while it holds the focus, in a shell with no other control
// and no navigation button to take it, hands the focus to the window itself
// rather than keeping it, where the keyboard would be stranded (AC-A03).
func TestShellHandsAnUnavailableActionsFocusToTheWindow(t *testing.T) {
	native := &availabilityView{available: true}
	shell := newCommandTestShell(t, native)
	navigator := core.NewNavigator()
	if err := navigator.Register(actionTestModule{}); err != nil {
		t.Fatal(err)
	}
	shell.navigator = navigator
	shell.layout()
	native.available = false
	shell.action.Focus()
	shellWindowProc(shell.hwnd, wmCommand, uintptr(actionCommand), 0)
	if result, _, _ := procIsWindowEnabled.Call(uintptr(shell.action.Handle())); result != 0 {
		t.Fatal("the unavailable primary action stayed enabled")
	}
	if focus, _, _ := procGetFocus.Call(); windows.Handle(focus) != shell.hwnd {
		t.Fatalf("the disabled primary action handed the focus to %#x, want the window (%#x)", focus, uintptr(shell.hwnd))
	}
}

var procGetWindowLongPtrW = user32.NewProc("GetWindowLongPtrW")

// dropAvailabilityView is a drop target that reports whether it takes drops
// now, as the Image Resizer source list does while a resize job runs.
type dropAvailabilityView struct {
	dropRecordingView
	available bool
}

func (v *dropAvailabilityView) NativeDropAvailable() bool { return v.available }

// TestShellRegistersDropsOnlyWhileAvailable pins that the shell registers
// the window and its children for drops only while the drop target reports
// drops available, re-reading it on every relayout, so a drop the view would
// refuse keeps the no-drop cursor.
func TestShellRegistersDropsOnlyWhileAvailable(t *testing.T) {
	const gwlExStyle, wsExAcceptFiles = ^uintptr(19), 0x10
	native := &dropAvailabilityView{available: true}
	shell := newCommandTestShell(t, native)
	child, err := ui.NewButton(shell.hwnd, "Child", ui.ButtonOptions{ID: 211})
	if err != nil {
		t.Fatal(err)
	}
	accepts := func(hwnd windows.Handle) bool {
		style, _, _ := procGetWindowLongPtrW.Call(uintptr(hwnd), gwlExStyle)
		return style&wsExAcceptFiles != 0
	}

	shell.layout()
	if !accepts(shell.hwnd) || !accepts(child.Handle()) {
		t.Fatal("an available drop target was not registered for drops")
	}
	native.available = false
	shell.layout()
	if accepts(shell.hwnd) || accepts(child.Handle()) {
		t.Fatal("the window stayed registered for drops the view refuses")
	}
	native.available = true
	shell.layout()
	if !accepts(shell.hwnd) || !accepts(child.Handle()) {
		t.Fatal("the window was not registered again once drops were available")
	}
}
