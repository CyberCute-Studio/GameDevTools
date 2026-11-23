// Package overlay runs rectangle capture selection sessions: it hides the
// application window, presents the mode's selection surface plus a floating
// toolbar, and ends the session by capturing the selected rectangle or by
// cancelling. Free Rectangle sessions cover the virtual desktop with a
// dimmed input-capturing overlay on which the user draws, moves, and resizes
// the selection or types exact dimensions. Preset Rectangle sessions instead
// float an exact-size topmost frame whose border moves the selection while
// its click-through interior leaves the applications beneath interactive,
// with arrow-key nudges and typed dimensions for exact adjustment.
//
// All Controller methods run on the GUI thread. The session windows are
// unowned topmost popups, so they outlive the main window unless the
// controller removes them; Cancel, Complete, Shutdown, and a display change
// the session cannot survive all guarantee that removal (SCR-052, SCR-054,
// SCR-056). Every input-capturing surface is visible while it intercepts
// input — the Free Rectangle tint, the Preset Rectangle frame
// ring — so a session can never wedge the desktop behind an invisible
// surface (SCR-052).
package overlay

import (
	"errors"
	"fmt"
	"sync"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot/capture"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// ErrSessionActive rejects starting a second rectangle selection while one
// is already on screen (SCR-053).
var ErrSessionActive = errors.New(ui.SelectionSessionActive)

// ErrPresetTooLarge rejects a Preset Rectangle session whose frame cannot
// fit inside the virtual desktop (SCR-031). A frame larger than one
// individual display is still allowed while it fits the virtual desktop.
var ErrPresetTooLarge = errors.New(ui.PresetRectangleNoFit)

// Mode selects the session behavior: Free Rectangle draws and adjusts a
// selection by mouse and typed dimensions (SCR-010 through SCR-013); Preset
// Rectangle floats an exact-size frame that moves by its border, arrow-key
// nudges, and typed dimensions without exposing resize handles (SCR-020
// through SCR-030).
type Mode int

const (
	ModeFreeRectangle Mode = iota
	ModePresetRectangle
)

// Events reports how a session ended. The callbacks run on the GUI thread
// after the overlay windows are destroyed, so they can present results in
// the main window immediately.
type Events struct {
	// OnCapture receives the selected rectangle and the function that
	// restores the hidden main window. The handler owns restore from that
	// point: it must run it once the rectangle's pixels have been read —
	// restoring earlier would put the main window into the capture, and
	// never running it would leave the application hidden (SCR-044). The
	// production restore is idempotent. Keeping the window hidden until the
	// pixels are read lets the capture run off the GUI thread (TECH-005)
	// without the main window flashing back on screen first.
	OnCapture func(region display.Rect, restore func())
	// OnCancel runs after a cancelled session has been fully cleaned up:
	// the overlay windows are destroyed and the main window restored.
	OnCancel func()
	// OnSessionLost runs, instead of OnCancel, when the session had to end
	// without the user asking: a display topology change invalidated the
	// selection or made the new layout unreadable (SCR-054, AC-S12). The
	// reason is a user-facing explanation; the cleanup is complete before it
	// runs, so no invisible input-capturing surface remains (SCR-052). When
	// nil, OnCancel runs instead.
	OnSessionLost func(reason string)
}

// NativeSession is the platform surface behind one active session. The
// Windows implementation owns the tint overlay and the toolbar, tracks the
// selection the user draws, and reports every selection change through
// CreateRequest.OnSelection.
type NativeSession interface {
	// Close destroys every window the session owns. The controller calls it
	// exactly once per session.
	Close()
	// SetSelectionReady enables or disables the toolbar Capture command.
	SetSelectionReady(bool)
}

// CreateRequest carries everything the platform surface needs to appear.
type CreateRequest struct {
	// Mode selects free drawing or the preset frame behavior.
	Mode Mode
	// Monitor resolves the cursor display for toolbar placement and DPI.
	Monitor *display.Monitor
	// Bounds is the virtual-desktop rectangle the session works in. Free
	// Rectangle sessions cover it with the tint overlay; Preset Rectangle
	// sessions keep the frame inside it.
	Bounds display.Rect
	// Size is the Preset Rectangle frame's exact physical-pixel width (X)
	// and height (Y); it is zero in Free Rectangle sessions.
	Size display.Point
	// Font is the shell's body font handle for toolbar controls, or zero
	// to keep the system default.
	Font uintptr
	// OnSelection reports the live selection rectangle while the user
	// draws, moves, resizes, or types dimensions; an empty rectangle means
	// there is no selection and Capture stays disabled. Preset Rectangle
	// sessions report their initial frame through it at creation and never
	// report an empty rectangle.
	OnSelection func(display.Rect)
	// OnCapture and OnCancel route the toolbar commands into the
	// controller.
	OnCapture func()
	OnCancel  func()
}

// Controller owns the single active rectangle selection session. Monitor
// resolves the desktop bounds and toolbar anchor; DesktopBounds, Hide, and
// Create default to the production native services and are replaceable in
// tests. Hide hides the main window and returns the function that restores
// it; every session path runs that restore exactly once — Cancel and
// Shutdown run it directly, Complete hands it to the OnCapture handler so
// the window stays hidden until the capture has read the pixels (SCR-044).
type Controller struct {
	Monitor       *display.Monitor
	DesktopBounds func() (display.Rect, error)
	Hide          func(...uintptr) (restore func(), err error)
	Create        func(CreateRequest) (NativeSession, error)

	mu      sync.Mutex
	session *session
}

type session struct {
	native    NativeSession
	restore   func()
	events    Events
	bounds    display.Rect
	selection display.Rect
}

// NewController returns a Controller backed by the production window
// exclusion and native session windows. The controller subscribes to the
// monitor's display-change notifications, so an active session is
// revalidated whenever the display topology changes (SCR-054).
func NewController(monitor *display.Monitor) *Controller {
	controller := &Controller{
		Monitor:       monitor,
		DesktopBounds: monitor.VirtualDesktopBounds,
		Hide: func(windows ...uintptr) (func(), error) {
			exclusion, err := capture.Hide(windows...)
			if err != nil {
				return nil, err
			}
			return exclusion.Restore, nil
		},
		Create: createNative,
	}
	if monitor != nil {
		monitor.Subscribe(controller.DisplayChange)
	}
	return controller
}

// Active reports whether a selection session is on screen.
func (c *Controller) Active() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.session != nil
}

// attentionSession is implemented by native sessions that can draw the
// user's attention back to their on-screen selection surfaces.
type attentionSession interface {
	Attention()
}

// Attention brings the active session back to the user's attention without
// starting a new one: the global capture shortcut invokes it while a
// selection is on screen so the existing session is found rather than
// stacked under a second overlay (SCR-005). Sessions whose native surface
// has no attention affordance are left alone.
func (c *Controller) Attention() {
	c.mu.Lock()
	s := c.session
	c.mu.Unlock()
	if s == nil {
		return
	}
	if native, ok := s.native.(attentionSession); ok {
		native.Attention()
	}
}

// fontSession is implemented by native sessions whose toolbar controls hold
// a font the shell can replace.
type fontSession interface {
	SetFont(font uintptr)
}

// SetFont hands the shell's current body font to the active session's
// toolbar. The shell recreates its fonts when its DPI changes and destroys
// the old handles, so a session created with the previous font must receive
// the new one or its controls keep painting through a destroyed handle.
// Without an active session there is nothing to update.
func (c *Controller) SetFont(font uintptr) {
	c.mu.Lock()
	s := c.session
	c.mu.Unlock()
	if s == nil {
		return
	}
	if native, ok := s.native.(fontSession); ok {
		native.SetFont(font)
	}
}

// displayChangeSession is implemented by native sessions that can revalidate
// themselves against a changed virtual desktop: they resize and reposition
// their surfaces to the new bounds, keep a selection that still fits — its
// dimensions always, its coordinates where they remain valid — and report
// the revalidated selection through CreateRequest.OnSelection. False means
// the session cannot continue in the new layout, and the controller ends it
// with an explanation (SCR-054, AC-S12).
type displayChangeSession interface {
	DisplayChanged(bounds display.Rect) bool
}

// DisplayChange revalidates the active session after the display topology
// changed: a display disconnected or reconnected, a resolution or scaling
// changed, or the layout moved (SCR-054). It runs on the GUI thread through
// the monitor's subscription. The session survives whenever its selection
// still fits the new virtual desktop; otherwise it ends with an explanation
// instead of leaving a stale overlay up (AC-S12). A topology that cannot be
// enumerated at all is treated the same way, because the session can no
// longer prove its surface matches the desktop (SCR-052). Sessions with
// display-change support receive every readable notification, even one whose
// physical topology is unchanged: a scaling or work-area change keeps the
// virtual-desktop rectangle identical while still invalidating the session's
// DPI-derived measurements, and only the native session can tell that from a
// truly empty notification. Sessions without display-change support are
// ended when the topology moved — left up, they would cover a desktop they
// no longer match — and left alone when it did not, because their surface
// still matches it.
func (c *Controller) DisplayChange() {
	c.mu.Lock()
	s := c.session
	c.mu.Unlock()
	if s == nil {
		return
	}
	bounds, err := c.DesktopBounds()
	if err != nil || bounds.Empty() {
		c.endDisplayChangeSession(s, ui.CaptureSessionDisplayGone)
		return
	}
	native, ok := s.native.(displayChangeSession)
	if !ok {
		if bounds != s.bounds {
			c.endDisplayChangeSession(s, ui.CaptureSessionDisplayLost)
		}
		return
	}
	if !native.DisplayChanged(bounds) {
		c.endDisplayChangeSession(s, ui.CaptureSessionDisplayLost)
		return
	}
	c.mu.Lock()
	if c.session == s {
		c.session.bounds = bounds
	}
	c.mu.Unlock()
}

// endDisplayChangeSession ends a session a display change invalidated. The
// cleanup matches Cancel — the overlay windows are destroyed and the main
// window restored, so no invisible input-capturing surface survives — but
// the event carries the reason so the user learns why the selection went
// away and that starting again is safe (AC-S12).
func (c *Controller) endDisplayChangeSession(s *session, reason string) {
	c.mu.Lock()
	if c.session != s {
		c.mu.Unlock()
		return
	}
	c.session = nil
	c.mu.Unlock()
	s.native.Close()
	s.restore()
	if s.events.OnSessionLost != nil {
		s.events.OnSessionLost(reason)
		return
	}
	if s.events.OnCancel != nil {
		s.events.OnCancel()
	}
}

// Begin hides the main window and shows the selection surface and toolbar
// (SCR-050). Only one session may be active (SCR-053). Preset Rectangle
// sessions take the frame's exact physical-pixel dimensions in size (X is
// the width, Y the height); a frame that cannot fit inside the virtual
// desktop is rejected with ErrPresetTooLarge before any window changes
// (SCR-031), as is an empty size. Free Rectangle sessions ignore size. If
// the overlay cannot be created, the main window is restored before the
// error returns, so a failed start never leaves the app hidden or an
// input-capturing surface behind (SCR-052).
func (c *Controller) Begin(window uintptr, font uintptr, mode Mode, size display.Point, events Events) error {
	if c.Monitor == nil || c.DesktopBounds == nil {
		return errors.New(ui.DisplayServiceUnavailable)
	}
	c.mu.Lock()
	busy := c.session != nil
	c.mu.Unlock()
	if busy {
		return ErrSessionActive
	}
	bounds, err := c.DesktopBounds()
	if err != nil {
		return fmt.Errorf("enumerate displays: %w", err)
	}
	if bounds.Empty() {
		return errors.New(ui.VirtualDesktopEmpty)
	}
	if mode == ModePresetRectangle {
		width, height := size.X, size.Y
		if width < 1 || height < 1 {
			return errors.New(ui.PresetRectangleEmpty)
		}
		if width > bounds.Width() || height > bounds.Height() {
			return fmt.Errorf("%w: "+ui.PresetRectangleTooLarge, ErrPresetTooLarge, width, height, bounds.Width(), bounds.Height())
		}
	}
	restore, err := c.Hide(window)
	if err != nil {
		return fmt.Errorf("hide the main window: %w", err)
	}
	// The native session can report an initial selection while it is being
	// created — Preset Rectangle's exact frame exists from the start
	// (SCR-020) — but the controller's session only exists once Create
	// returns. Buffer that report and drive it once the session is
	// installed, or Capture would stay disarmed and deliver an empty
	// rectangle.
	var reported display.Rect
	selectionReported := false
	native, err := c.Create(CreateRequest{
		Mode:    mode,
		Monitor: c.Monitor,
		Bounds:  bounds,
		Size:    size,
		Font:    font,
		OnSelection: func(region display.Rect) {
			reported = region
			selectionReported = true
			c.SetSelection(region)
		},
		OnCapture: c.completeFromToolbar,
		OnCancel:  c.Cancel,
	})
	if err != nil {
		restore()
		return fmt.Errorf("create the selection overlay: %w", err)
	}
	c.mu.Lock()
	if c.session != nil {
		// Defensive: a re-entrant Begin on the GUI thread must not leak the
		// freshly created surface or the hidden main window.
		c.mu.Unlock()
		native.Close()
		restore()
		return ErrSessionActive
	}
	c.session = &session{native: native, restore: restore, events: events, bounds: bounds}
	c.mu.Unlock()
	if selectionReported {
		c.SetSelection(reported)
	}
	return nil
}

// Cancel ends the active session without producing any output (SCR-051).
// It destroys the overlay windows, restores the main window, and then runs
// the OnCancel event. Cancelling without an active session is a no-op, so
// toolbar, keyboard, and shutdown paths can all request it safely.
func (c *Controller) Cancel() {
	s := c.take()
	if s == nil {
		return
	}
	s.native.Close()
	s.restore()
	if s.events.OnCancel != nil {
		s.events.OnCancel()
	}
}

// Complete ends the active session and delivers the selected rectangle to
// the OnCapture event. An empty region leaves the session running, matching
// the disabled Capture command. The overlay windows are destroyed before the
// event runs so the tint, border, handles, and toolbar can never reach the
// output (SCR-068), and the event receives the restore function: the main
// window stays hidden until the handler has read the rectangle's pixels off
// the GUI thread (SCR-044, TECH-005).
func (c *Controller) Complete(region display.Rect) {
	if region.Empty() {
		return
	}
	s := c.take()
	if s == nil {
		return
	}
	s.native.Close()
	if s.events.OnCapture != nil {
		s.events.OnCapture(region, s.restore)
		return
	}
	s.restore()
}

// SetSelection records the rectangle the Capture command will deliver and
// toggles that command's enabled state. The native session calls it through
// CreateRequest.OnSelection as the user draws, moves, resizes, or types the
// selection's dimensions.
func (c *Controller) SetSelection(region display.Rect) {
	c.mu.Lock()
	s := c.session
	if s != nil {
		s.selection = region
	}
	c.mu.Unlock()
	if s != nil {
		s.native.SetSelectionReady(!region.Empty())
	}
}

// Shutdown removes an active session without running its events. The
// application calls it while exiting, so overlays are destroyed and normal
// input is restored immediately (SCR-056); the cancelled-session notice is
// pointless once the main window is going away.
func (c *Controller) Shutdown() {
	s := c.take()
	if s == nil {
		return
	}
	s.native.Close()
	s.restore()
}

// completeFromToolbar routes the toolbar Capture command through Complete
// with the session's current selection.
func (c *Controller) completeFromToolbar() {
	c.mu.Lock()
	region := display.Rect{}
	active := c.session != nil
	if active {
		region = c.session.selection
	}
	c.mu.Unlock()
	if active {
		c.Complete(region)
	}
}

// take detaches the active session, if any, so exactly one cleanup path
// owns its windows and restore function.
func (c *Controller) take() *session {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.session
	c.session = nil
	return s
}
