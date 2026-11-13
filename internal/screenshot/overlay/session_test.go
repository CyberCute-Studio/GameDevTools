package overlay

import (
	"errors"
	"reflect"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
)

var testDesktop = display.Rect{Left: -1920, Top: 0, Right: 1920, Bottom: 1080}

// fakeNative records the controller's calls to the session surface.
type fakeNative struct {
	closed      int
	ready       []bool
	onSelection func(display.Rect)
	onCapture   func()
	onCancel    func()
}

func (f *fakeNative) Close()                       { f.closed++ }
func (f *fakeNative) SetSelectionReady(ready bool) { f.ready = append(f.ready, ready) }

// sessionLog records the order of hide, restore, create, and close calls so
// tests can assert the hide-before-overlay and close-before-restore
// sequences.
type sessionFixture struct {
	log       []string
	native    *fakeNative
	mode      Mode
	hideErr   error
	createErr error
}

func newFixtureController() (*Controller, *sessionFixture) {
	fixture := &sessionFixture{}
	controller := &Controller{
		Monitor: display.NewMonitor(),
		DesktopBounds: func() (display.Rect, error) {
			return testDesktop, nil
		},
	}
	controller.Hide = func(...uintptr) (func(), error) {
		fixture.log = append(fixture.log, "hide")
		if fixture.hideErr != nil {
			return nil, fixture.hideErr
		}
		return func() { fixture.log = append(fixture.log, "restore") }, nil
	}
	controller.Create = func(request CreateRequest) (NativeSession, error) {
		fixture.log = append(fixture.log, "create")
		if fixture.createErr != nil {
			return nil, fixture.createErr
		}
		fixture.mode = request.Mode
		fixture.native = &fakeNative{
			onSelection: request.OnSelection,
			onCapture:   request.OnCapture,
			onCancel:    request.OnCancel,
		}
		return fixture.native, nil
	}
	return controller, fixture
}

func TestBeginHidesMainWindowBeforeCreatingOverlay(t *testing.T) {
	controller, fixture := newFixtureController()
	if err := controller.Begin(42, 0, ModeFreeRectangle, Events{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fixture.log, []string{"hide", "create"}) {
		t.Fatalf("call order = %v, want [hide create] (SCR-050)", fixture.log)
	}
	if !controller.Active() {
		t.Fatal("session is not active after a successful Begin")
	}
}

func TestBeginRejectsConcurrentSession(t *testing.T) {
	controller, fixture := newFixtureController()
	if err := controller.Begin(42, 0, ModeFreeRectangle, Events{}); err != nil {
		t.Fatal(err)
	}
	err := controller.Begin(42, 0, ModeFreeRectangle, Events{})
	if !errors.Is(err, ErrSessionActive) {
		t.Fatalf("second Begin error = %v, want ErrSessionActive (SCR-053)", err)
	}
	if !reflect.DeepEqual(fixture.log, []string{"hide", "create"}) {
		t.Fatalf("call order after rejected Begin = %v, want [hide create]", fixture.log)
	}
	controller.Cancel()
	if !reflect.DeepEqual(fixture.log, []string{"hide", "create", "restore"}) {
		t.Fatalf("call order after Cancel = %v", fixture.log)
	}
}

func TestBeginFailureRestoresMainWindow(t *testing.T) {
	controller, fixture := newFixtureController()
	fixture.createErr = errors.New("window class unavailable")
	err := controller.Begin(42, 0, ModeFreeRectangle, Events{})
	if err == nil || !errors.Is(err, fixture.createErr) {
		t.Fatalf("Begin error = %v, want wrapped %v", err, fixture.createErr)
	}
	// SCR-052: a failed start must not leave the main window hidden or an
	// input-capturing surface behind.
	if !reflect.DeepEqual(fixture.log, []string{"hide", "create", "restore"}) {
		t.Fatalf("call order after failed Begin = %v, want [hide create restore]", fixture.log)
	}
	if controller.Active() {
		t.Fatal("session is active after a failed Begin")
	}
}

func TestBeginHideFailureSkipsOverlay(t *testing.T) {
	controller, fixture := newFixtureController()
	fixture.hideErr = errors.New("compositor unavailable")
	err := controller.Begin(42, 0, ModeFreeRectangle, Events{})
	if err == nil || !errors.Is(err, fixture.hideErr) {
		t.Fatalf("Begin error = %v, want wrapped %v", err, fixture.hideErr)
	}
	if !reflect.DeepEqual(fixture.log, []string{"hide"}) {
		t.Fatalf("call order after failed hide = %v, want [hide]", fixture.log)
	}
	if controller.Active() {
		t.Fatal("session is active after a failed hide")
	}
}

func TestBeginWithoutDisplayServiceFails(t *testing.T) {
	controller := &Controller{}
	if err := controller.Begin(42, 0, ModeFreeRectangle, Events{}); err == nil {
		t.Fatal("Begin succeeded without a display service")
	}
	controller, fixture := newFixtureController()
	controller.DesktopBounds = func() (display.Rect, error) {
		return display.Rect{}, errors.New("enumeration failed")
	}
	if err := controller.Begin(42, 0, ModeFreeRectangle, Events{}); err == nil {
		t.Fatal("Begin succeeded when the desktop bounds were unavailable")
	}
	if len(fixture.log) != 0 {
		t.Fatalf("call order after failed enumeration = %v, want no hide or create", fixture.log)
	}
	controller.DesktopBounds = func() (display.Rect, error) {
		return display.Rect{}, nil
	}
	if err := controller.Begin(42, 0, ModeFreeRectangle, Events{}); err == nil {
		t.Fatal("Begin succeeded with an empty virtual desktop")
	}
}

func TestCancelClosesOverlayBeforeRestoring(t *testing.T) {
	controller, fixture := newFixtureController()
	events := []string{}
	if err := controller.Begin(42, 0, ModeFreeRectangle, Events{OnCancel: func() { events = append(events, "cancel") }}); err != nil {
		t.Fatal(err)
	}
	controller.Cancel()
	if controller.Active() {
		t.Fatal("session is still active after Cancel")
	}
	if fixture.native.closed != 1 {
		t.Fatalf("native Close calls = %d, want 1", fixture.native.closed)
	}
	if !reflect.DeepEqual(fixture.log, []string{"hide", "create", "restore"}) {
		t.Fatalf("call order after Cancel = %v", fixture.log)
	}
	if !reflect.DeepEqual(events, []string{"cancel"}) {
		t.Fatalf("OnCancel events = %v, want one", events)
	}
	// Cancelling twice must not double-restore the main window or touch the
	// clipboard or output (SCR-051).
	controller.Cancel()
	if fixture.native.closed != 1 || len(fixture.log) != 3 || len(events) != 1 {
		t.Fatalf("second Cancel changed state: closed=%d log=%v events=%v", fixture.native.closed, fixture.log, events)
	}
	controller.Shutdown()
	if len(fixture.log) != 3 {
		t.Fatalf("Shutdown after Cancel restored again: %v", fixture.log)
	}
}

func TestCompleteDeliversSelection(t *testing.T) {
	controller, fixture := newFixtureController()
	var captured display.Rect
	captures := 0
	events := Events{OnCapture: func(region display.Rect, restore func()) {
		captures++
		captured = region
		// The handler owns the main-window restore so the pixels can be read
		// before the window returns (SCR-044).
		if !reflect.DeepEqual(fixture.log, []string{"hide", "create"}) {
			t.Errorf("call order when OnCapture runs = %v, want the restore still pending", fixture.log)
		}
		restore()
	}}
	if err := controller.Begin(42, 0, ModeFreeRectangle, events); err != nil {
		t.Fatal(err)
	}
	region := display.Rect{Left: 10, Top: 20, Right: 810, Bottom: 620}
	// The native session reports its live selection through the request's
	// OnSelection, which the controller wires to SetSelection.
	fixture.native.onSelection(region)
	if !reflect.DeepEqual(fixture.native.ready, []bool{true}) {
		t.Fatalf("SetSelectionReady calls = %v, want [true]", fixture.native.ready)
	}
	// The toolbar Capture command routes through the request's OnCapture.
	fixture.native.onCapture()
	if captures != 1 || captured != region {
		t.Fatalf("OnCapture = (%d, %v), want (1, %v)", captures, captured, region)
	}
	if controller.Active() {
		t.Fatal("session is still active after Complete")
	}
	if fixture.native.closed != 1 {
		t.Fatalf("native Close calls = %d, want 1", fixture.native.closed)
	}
	if !reflect.DeepEqual(fixture.log, []string{"hide", "create", "restore"}) {
		t.Fatalf("call order after Complete = %v", fixture.log)
	}
}

// TestCompleteTransfersRestoreToHandler pins the contract that the
// controller does not restore the main window itself when an OnCapture
// handler exists: the handler restores it after the capture has read the
// selection's pixels.
func TestCompleteTransfersRestoreToHandler(t *testing.T) {
	controller, fixture := newFixtureController()
	keptRestore := func() {}
	if err := controller.Begin(42, 0, ModeFreeRectangle, Events{
		OnCapture: func(_ display.Rect, restore func()) { keptRestore = restore },
	}); err != nil {
		t.Fatal(err)
	}
	controller.SetSelection(display.Rect{Left: 1, Top: 1, Right: 5, Bottom: 5})
	fixture.native.onCapture()
	if !reflect.DeepEqual(fixture.log, []string{"hide", "create"}) {
		t.Fatalf("call order after Complete = %v, want the restore left to the handler", fixture.log)
	}
	keptRestore()
	if !reflect.DeepEqual(fixture.log, []string{"hide", "create", "restore"}) {
		t.Fatalf("call order after the handler restores = %v", fixture.log)
	}
}

func TestCompleteWithoutHandlerRestoresWindow(t *testing.T) {
	controller, fixture := newFixtureController()
	if err := controller.Begin(42, 0, ModeFreeRectangle, Events{}); err != nil {
		t.Fatal(err)
	}
	controller.SetSelection(display.Rect{Left: 1, Top: 1, Right: 5, Bottom: 5})
	fixture.native.onCapture()
	if controller.Active() {
		t.Fatal("session is still active after Complete")
	}
	if !reflect.DeepEqual(fixture.log, []string{"hide", "create", "restore"}) {
		t.Fatalf("call order after handlerless Complete = %v, want an immediate restore", fixture.log)
	}
}

func TestCompleteWithEmptySelectionKeepsSession(t *testing.T) {
	controller, fixture := newFixtureController()
	captures := 0
	if err := controller.Begin(42, 0, ModeFreeRectangle, Events{OnCapture: func(display.Rect, func()) { captures++ }}); err != nil {
		t.Fatal(err)
	}
	fixture.native.onCapture()
	if !controller.Active() {
		t.Fatal("empty selection ended the session")
	}
	if captures != 0 {
		t.Fatalf("OnCapture fired %d times for an empty selection", captures)
	}
	if fixture.native.closed != 0 {
		t.Fatal("empty selection destroyed the session windows")
	}
	controller.Cancel()
}

func TestSetSelectionWithoutSessionIsNoop(t *testing.T) {
	controller, _ := newFixtureController()
	controller.SetSelection(display.Rect{Left: 0, Top: 0, Right: 5, Bottom: 5})
	if controller.Active() {
		t.Fatal("SetSelection started a session")
	}
}

func TestShutdownRemovesSessionSilently(t *testing.T) {
	controller, fixture := newFixtureController()
	cancelled := 0
	captured := 0
	events := Events{
		OnCancel:  func() { cancelled++ },
		OnCapture: func(display.Rect, func()) { captured++ },
	}
	if err := controller.Begin(42, 0, ModeFreeRectangle, events); err != nil {
		t.Fatal(err)
	}
	controller.Shutdown()
	if controller.Active() {
		t.Fatal("session is still active after Shutdown")
	}
	if fixture.native.closed != 1 {
		t.Fatalf("native Close calls = %d, want 1 (SCR-056)", fixture.native.closed)
	}
	if !reflect.DeepEqual(fixture.log, []string{"hide", "create", "restore"}) {
		t.Fatalf("call order after Shutdown = %v", fixture.log)
	}
	if cancelled != 0 || captured != 0 {
		t.Fatalf("Shutdown ran events: cancel=%d capture=%d, want none", cancelled, captured)
	}
	controller.Shutdown()
	if fixture.native.closed != 1 {
		t.Fatal("second Shutdown closed the windows again")
	}
}
