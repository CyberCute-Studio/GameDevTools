// Package display provides the native display topology and physical-pixel
// coordinate services used by the Screenshot module. All geometry is
// expressed in physical pixels in the Windows virtual-desktop coordinate
// space, where the primary display's origin is (0,0) and secondary displays
// may use negative coordinates. Logical (96-DPI) values are converted
// explicitly; no implicit framework scaling is applied to output geometry.
package display

import (
	"errors"
	"sync"
)

// ReferenceDPI is the Windows 100% scaling level. Logical UI measurements
// are expressed against this reference.
const ReferenceDPI = 96

// ErrNoDisplays reports that no connected display is available.
var ErrNoDisplays = errors.New("no connected displays found")

// Point is a physical-pixel position in virtual-desktop coordinates.
type Point struct {
	X int32
	Y int32
}

// Rect is a physical-pixel rectangle in virtual-desktop coordinates. Right
// and Bottom are exclusive, matching the Win32 RECT convention.
type Rect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

func (r Rect) Width() int32 {
	return r.Right - r.Left
}

func (r Rect) Height() int32 {
	return r.Bottom - r.Top
}

func (r Rect) Empty() bool {
	return r.Right <= r.Left || r.Bottom <= r.Top
}

// Contains reports whether p lies inside r. Points on the Right or Bottom
// edge are outside, matching the exclusive-bound convention.
func (r Rect) Contains(p Point) bool {
	return p.X >= r.Left && p.X < r.Right && p.Y >= r.Top && p.Y < r.Bottom
}

// Display describes one connected display at its current native desktop
// pixel dimensions and orientation.
type Display struct {
	// Number is the deterministic 1-based picker label: the primary display
	// is always 1 and the remaining displays follow in device-name order.
	Number int
	// DeviceName is the Windows adapter identifier, for example
	// `\\.\DISPLAY1`. It is stable for a given adapter output.
	DeviceName string
	// Name is the monitor's friendly name where Windows provides it; it is
	// empty otherwise.
	Name string
	// Bounds is the display's desktop rectangle in physical pixels.
	Bounds Rect
	// WorkArea is Bounds minus system-reserved regions such as the taskbar.
	WorkArea Rect
	// Primary reports whether this is the primary display.
	Primary bool
	// DPIX and DPIY are the effective DPI values; 96 means 100% scaling.
	DPIX uint32
	DPIY uint32
}

// ScalePercent returns the horizontal scaling level relative to 96 DPI,
// for example 150 for 144 DPI.
func (d Display) ScalePercent() int {
	return int((uint64(d.DPIX)*100 + ReferenceDPI/2) / ReferenceDPI)
}

// VirtualDesktopBounds returns the union of every display's bounds. A
// capture rectangle may span displays but must fit within this rectangle.
// An empty input produces a zero rectangle.
func VirtualDesktopBounds(displays []Display) Rect {
	if len(displays) == 0 {
		return Rect{}
	}
	bounds := displays[0].Bounds
	for _, item := range displays[1:] {
		if item.Bounds.Left < bounds.Left {
			bounds.Left = item.Bounds.Left
		}
		if item.Bounds.Top < bounds.Top {
			bounds.Top = item.Bounds.Top
		}
		if item.Bounds.Right > bounds.Right {
			bounds.Right = item.Bounds.Right
		}
		if item.Bounds.Bottom > bounds.Bottom {
			bounds.Bottom = item.Bounds.Bottom
		}
	}
	return bounds
}

// DisplayContaining returns the display whose bounds contain p.
func DisplayContaining(displays []Display, p Point) (Display, bool) {
	for _, item := range displays {
		if item.Bounds.Contains(p) {
			return item, true
		}
	}
	return Display{}, false
}

// NearestDisplay returns the display closest to p, used when the point lies
// in a gap between display rectangles. Contained points have zero distance,
// so a containing display always wins.
func NearestDisplay(displays []Display, p Point) (Display, bool) {
	if len(displays) == 0 {
		return Display{}, false
	}
	nearest := displays[0]
	distance := distanceSquared(p, displays[0].Bounds)
	for _, item := range displays[1:] {
		if candidate := distanceSquared(p, item.Bounds); candidate < distance {
			nearest = item
			distance = candidate
		}
	}
	return nearest, true
}

func distanceSquared(p Point, r Rect) int64 {
	var dx, dy int64
	if p.X < r.Left {
		dx = int64(r.Left) - int64(p.X)
	} else if p.X >= r.Right {
		dx = int64(p.X) - int64(r.Right) + 1
	}
	if p.Y < r.Top {
		dy = int64(r.Top) - int64(p.Y)
	} else if p.Y >= r.Bottom {
		dy = int64(p.Y) - int64(r.Bottom) + 1
	}
	return dx*dx + dy*dy
}

// PrimaryDisplay returns the primary display, or the first display when the
// enumeration carries no primary flag.
func PrimaryDisplay(displays []Display) (Display, bool) {
	for _, item := range displays {
		if item.Primary {
			return item, true
		}
	}
	if len(displays) > 0 {
		return displays[0], true
	}
	return Display{}, false
}

// LogicalToPhysical converts a logical 96-DPI measurement to physical pixels
// at the given DPI, rounding half away from zero. A zero DPI is treated as
// the 96-DPI reference.
func LogicalToPhysical(value int32, dpi uint32) int32 {
	if dpi == 0 {
		dpi = ReferenceDPI
	}
	return mulDiv(value, dpi, ReferenceDPI)
}

// PhysicalToLogical converts physical pixels to a logical 96-DPI
// measurement at the given DPI, rounding half away from zero. A zero DPI is
// treated as the 96-DPI reference.
func PhysicalToLogical(value int32, dpi uint32) int32 {
	if dpi == 0 {
		dpi = ReferenceDPI
	}
	return mulDiv(value, ReferenceDPI, dpi)
}

// LogicalRectToPhysical converts a logical rectangle to physical pixels.
func LogicalRectToPhysical(r Rect, dpi uint32) Rect {
	return Rect{
		Left:   LogicalToPhysical(r.Left, dpi),
		Top:    LogicalToPhysical(r.Top, dpi),
		Right:  LogicalToPhysical(r.Right, dpi),
		Bottom: LogicalToPhysical(r.Bottom, dpi),
	}
}

// PhysicalRectToLogical converts a physical-pixel rectangle to logical
// 96-DPI measurements.
func PhysicalRectToLogical(r Rect, dpi uint32) Rect {
	return Rect{
		Left:   PhysicalToLogical(r.Left, dpi),
		Top:    PhysicalToLogical(r.Top, dpi),
		Right:  PhysicalToLogical(r.Right, dpi),
		Bottom: PhysicalToLogical(r.Bottom, dpi),
	}
}

func mulDiv(value int32, multiplier uint32, divisor uint32) int32 {
	product := int64(value) * int64(multiplier)
	half := int64(divisor) / 2
	if product >= 0 {
		return int32((product + half) / int64(divisor))
	}
	return int32((product - half) / int64(divisor))
}

// Monitor tracks the connected-display topology. It is safe for concurrent
// use; subscription callbacks always run synchronously on the thread that
// calls HandleDisplayChange, which must be the GUI thread.
type Monitor struct {
	mu          sync.Mutex
	subscribers []func()
}

func NewMonitor() *Monitor {
	return &Monitor{}
}

// Displays enumerates the currently connected displays with fresh native
// data on every call and assigns deterministic picker numbers.
func (m *Monitor) Displays() ([]Display, error) {
	displays, err := rawDisplays()
	if err != nil {
		return nil, err
	}
	numberDisplays(displays)
	return displays, nil
}

// VirtualDesktopBounds returns the union of all current display bounds.
func (m *Monitor) VirtualDesktopBounds() (Rect, error) {
	displays, err := m.Displays()
	if err != nil {
		return Rect{}, err
	}
	return VirtualDesktopBounds(displays), nil
}

// PrimaryDisplay returns the current primary display.
func (m *Monitor) PrimaryDisplay() (Display, error) {
	displays, err := m.Displays()
	if err != nil {
		return Display{}, err
	}
	item, ok := PrimaryDisplay(displays)
	if !ok {
		return Display{}, ErrNoDisplays
	}
	return item, nil
}

// CursorDisplay returns the display the cursor currently rests on, falling
// back to the nearest display when the cursor sits in a gap between display
// rectangles during a topology change.
func (m *Monitor) CursorDisplay() (Display, error) {
	position, err := cursorPosition()
	if err != nil {
		return Display{}, err
	}
	displays, err := m.Displays()
	if err != nil {
		return Display{}, err
	}
	if item, ok := DisplayContaining(displays, position); ok {
		return item, nil
	}
	item, ok := NearestDisplay(displays, position)
	if !ok {
		return Display{}, ErrNoDisplays
	}
	return item, nil
}

// Subscribe registers a display-change callback. Callbacks run in
// subscription order on the GUI thread inside the window procedure and must
// return promptly.
func (m *Monitor) Subscribe(callback func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subscribers = append(m.subscribers, callback)
}

// HandleDisplayChange notifies subscribers that the display topology may
// have changed. The shell window procedure calls it for every
// WM_DISPLAYCHANGE message; subscribers re-enumerate through this monitor
// to observe the new topology.
func (m *Monitor) HandleDisplayChange() {
	m.mu.Lock()
	subscribers := append([]func(){}, m.subscribers...)
	m.mu.Unlock()
	for _, callback := range subscribers {
		callback()
	}
}

// numberDisplays sorts displays so the primary display is first, followed
// by the rest in device-name order, and assigns 1-based picker numbers.
func numberDisplays(displays []Display) {
	for i := 1; i < len(displays); i++ {
		for j := i; j > 0 && displayBefore(displays[j], displays[j-1]); j-- {
			displays[j], displays[j-1] = displays[j-1], displays[j]
		}
	}
	for i := range displays {
		displays[i].Number = i + 1
	}
}

func displayBefore(a, b Display) bool {
	if a.Primary != b.Primary {
		return a.Primary
	}
	return a.DeviceName < b.DeviceName
}
