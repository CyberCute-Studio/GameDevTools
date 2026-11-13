// Package overlay runs rectangle capture selection sessions: it hides the
// application window, covers the virtual desktop with a visibly dimmed
// input-capturing surface plus a floating toolbar, and ends the session by
// capturing the selected rectangle or by cancelling. Free Rectangle sessions
// draw the selection by dragging, adjust it with move and resize handles or
// typed dimensions, and deliver it to the capture pipeline; the exact preset
// frame is implemented by the Preset Rectangle item.
//
// All Controller methods run on the GUI thread. The session windows are
// unowned topmost popups, so they outlive the main window unless the
// controller removes them; Cancel, Complete, and Shutdown guarantee that
// removal (SCR-052, SCR-056). The overlay always carries a visible tint
// while it intercepts input, so a session can never wedge the desktop
// behind an invisible surface (SCR-052).
package overlay

import (
	"errors"
	"fmt"
	"sync"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot/capture"
)

// ErrSessionActive rejects starting a second rectangle selection while one
// is already on screen (SCR-053).
var ErrSessionActive = errors.New("a rectangle selection session is already active")

// Mode selects the session behavior: Free Rectangle draws and adjusts a
// selection by mouse and typed dimensions (SCR-010 through SCR-013); Preset
// Rectangle's exact-size frame arrives with its own item and the session
// only offers Cancel until then.
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
	// Mode selects free drawing or the later preset frame behavior.
	Mode Mode
	// Monitor resolves the cursor display for toolbar placement and DPI.
	Monitor *display.Monitor
	// Bounds is the virtual-desktop rectangle the tint overlay covers.
	Bounds display.Rect
	// Font is the shell's body font handle for toolbar controls, or zero
	// to keep the system default.
	Font uintptr
	// OnSelection reports the live selection rectangle while the user
	// draws, moves, resizes, or types dimensions; an empty rectangle means
	// there is no selection and Capture stays disabled.
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
	selection display.Rect
}

// NewController returns a Controller backed by the production window
// exclusion and native session windows.
func NewController(monitor *display.Monitor) *Controller {
	return &Controller{
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
}

// Active reports whether a selection session is on screen.
func (c *Controller) Active() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.session != nil
}

// Begin hides the main window and shows the selection overlay and toolbar
// (SCR-050). Only one session may be active (SCR-053). If the overlay
// cannot be created, the main window is restored before the error returns,
// so a failed start never leaves the app hidden or an input-capturing
// surface behind (SCR-052).
func (c *Controller) Begin(window uintptr, font uintptr, mode Mode, events Events) error {
	if c.Monitor == nil || c.DesktopBounds == nil {
		return errors.New("the display service is unavailable")
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
		return errors.New("the virtual desktop has no area to select from")
	}
	restore, err := c.Hide(window)
	if err != nil {
		return fmt.Errorf("hide the main window: %w", err)
	}
	native, err := c.Create(CreateRequest{
		Mode:        mode,
		Monitor:     c.Monitor,
		Bounds:      bounds,
		Font:        font,
		OnSelection: c.SetSelection,
		OnCapture:   c.completeFromToolbar,
		OnCancel:    c.Cancel,
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
	c.session = &session{native: native, restore: restore, events: events}
	c.mu.Unlock()
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
