package overlay

import (
	"errors"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// displaysAfterRemoval is testDesktop's layout once a display in its middle
// has gone: the displays that remain span the same virtual-desktop rectangle,
// so the bounds alone cannot show the change.
func displaysAfterRemoval() ([]display.Rect, error) {
	return []display.Rect{
		{Left: -1920, Top: 0, Right: -1500, Bottom: 1080},
		{Left: 0, Top: 0, Right: 1920, Bottom: 1080},
	}, nil
}

// onRemovedDisplay lies wholly on the part of testDesktop no display covers
// in displaysAfterRemoval.
var onRemovedDisplay = display.Rect{Left: -1000, Top: 100, Right: -500, Bottom: 400}

// TestDisplayChangeEndsASelectionLeftOnNoDisplay is a display removed from
// inside an unchanged virtual desktop: the selection on it can no longer be
// seen or captured, so the session ends with the reason (SCR-054, AC-S12).
func TestDisplayChangeEndsASelectionLeftOnNoDisplay(t *testing.T) {
	controller, fixture := newFixtureController()
	var reason string
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{
		OnSessionLost: func(got string) { reason = got },
	}); err != nil {
		t.Fatal(err)
	}
	controller.SetSelection(onRemovedDisplay)
	controller.DisplayAreas = displaysAfterRemoval

	controller.DisplayChange()

	if controller.Active() || fixture.native.closed != 1 {
		t.Fatalf("session after its display went: active=%v closed=%d", controller.Active(), fixture.native.closed)
	}
	if reason != ui.CaptureSessionDisplayLost {
		t.Fatalf("reason = %q", reason)
	}
}

// TestDisplayChangeEndsAnUnsupportedSessionLeftOnNoDisplay is the same removal
// for a session surface with no display-change support, which an unchanged
// virtual desktop otherwise leaves alone.
func TestDisplayChangeEndsAnUnsupportedSessionLeftOnNoDisplay(t *testing.T) {
	controller, _ := newFixtureController()
	plain := &plainNative{}
	controller.Create = func(CreateRequest) (NativeSession, error) { return plain, nil }
	lost := 0
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{
		OnSessionLost: func(string) { lost++ },
	}); err != nil {
		t.Fatal(err)
	}
	controller.SetSelection(onRemovedDisplay)
	controller.DisplayAreas = displaysAfterRemoval

	controller.DisplayChange()

	if controller.Active() || plain.closed != 1 || lost != 1 {
		t.Fatalf("active=%v closed=%d lost=%d", controller.Active(), plain.closed, lost)
	}
}

// TestDisplayChangeKeepsASelectionADisplayStillShows pins what the check must
// leave alone: a selection on or partly on a remaining display, and a session
// with no selection yet.
func TestDisplayChangeKeepsASelectionADisplayStillShows(t *testing.T) {
	tests := []struct {
		name      string
		selection display.Rect
	}{
		{name: "no selection"},
		{name: "on a display", selection: display.Rect{Left: 10, Top: 10, Right: 110, Bottom: 110}},
		{name: "partly on a display", selection: display.Rect{Left: -1600, Top: 10, Right: -1000, Bottom: 110}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controller, fixture := newFixtureController()
			if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{}); err != nil {
				t.Fatal(err)
			}
			controller.SetSelection(test.selection)
			controller.DisplayAreas = displaysAfterRemoval

			controller.DisplayChange()

			if !controller.Active() || fixture.native.closed != 0 {
				t.Fatalf("active=%v closed=%d", controller.Active(), fixture.native.closed)
			}
		})
	}
}

// TestDisplayChangeEndsASelectionWhenDisplaysCannotBeRead pins that a selection
// the session can no longer place on a display ends the session, as an
// unreadable layout does for the bounds (SCR-052).
func TestDisplayChangeEndsASelectionWhenDisplaysCannotBeRead(t *testing.T) {
	controller, fixture := newFixtureController()
	var reason string
	if err := controller.Begin(42, 0, ModeFreeRectangle, display.Point{}, Events{
		OnSessionLost: func(got string) { reason = got },
	}); err != nil {
		t.Fatal(err)
	}
	controller.SetSelection(display.Rect{Left: 10, Top: 10, Right: 110, Bottom: 110})
	controller.DisplayAreas = func() ([]display.Rect, error) {
		return nil, errors.New("enumeration failed")
	}

	controller.DisplayChange()

	if controller.Active() || fixture.native.closed != 1 {
		t.Fatalf("active=%v closed=%d", controller.Active(), fixture.native.closed)
	}
	if reason != ui.CaptureSessionDisplayGone {
		t.Fatalf("reason = %q", reason)
	}
}
