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
	shellWindowProc(shell.hwnd, wmCommand, uintptr(206)|uintptr(1)<<16, nil)
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
	shellWindowProc(shell.hwnd, wmDisplayChange, 0, nil)
	if calls != 1 {
		t.Fatalf("display-change callback invocations = %d, want 1", calls)
	}
	shell.displays = nil
	shellWindowProc(shell.hwnd, wmDisplayChange, 0, nil)
	if calls != 1 {
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

// TestShellWindowProcRoutesHotkeys covers the shell half of SCR-002: a
// WM_HOTKEY reaching the shell window is offered to the module views with
// its registration identifier, and a consumed hotkey triggers a relayout.
func TestShellWindowProcRoutesHotkeys(t *testing.T) {
	native := &hotkeyRecordingView{handled: true}
	shell := newCommandTestShell(t, native)
	shellWindowProc(shell.hwnd, wmHotkey, 1, nil)
	if native.calls != 1 || native.hotkeyID != 1 {
		t.Fatalf("routed hotkey = (%d calls, id %d), want (1, 1)", native.calls, native.hotkeyID)
	}
	if native.layouts == 0 {
		t.Fatal("consumed hotkey did not trigger a layout")
	}

	native.handled = false
	layouts := native.layouts
	shellWindowProc(shell.hwnd, wmHotkey, 7, nil)
	if native.calls != 2 || native.hotkeyID != 7 {
		t.Fatalf("unconsumed hotkey = (%d calls, id %d), want (2, 7)", native.calls, native.hotkeyID)
	}
	if native.layouts != layouts {
		t.Fatal("unconsumed hotkey triggered a layout")
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
	shell := &nativeShell{
		navigator:   navigator,
		hwnd:        windows.Handle(hwnd),
		dpi:         96,
		navButtons:  make(map[core.ModuleID]windows.Handle),
		navIDs:      make(map[uint16]core.ModuleID),
		nativeViews: make(map[core.ModuleID]nativeView),
		note:        note,
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
