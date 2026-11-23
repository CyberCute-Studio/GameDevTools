package overlay

import "github.com/CyberCute-Studio/GameDevTools/internal/display"

// zone identifies which part of an active selection a pointer position
// belongs to: one of the eight resize handles, the movable interior, or the
// dimmed area outside, where a press starts a new selection (SCR-012).
type zone int

const (
	zoneOutside zone = iota
	zoneInterior
	zoneNW
	zoneN
	zoneNE
	zoneE
	zoneSE
	zoneS
	zoneSW
	zoneW
)

// clampPoint pulls p inside the desktop, one pixel short of the exclusive
// Right and Bottom edges so a 1 × 1 rectangle started there still fits.
func clampPoint(p display.Point, desktop display.Rect) display.Point {
	return display.Point{
		X: min(max(p.X, desktop.Left), desktop.Right-1),
		Y: min(max(p.Y, desktop.Top), desktop.Bottom-1),
	}
}

// drawRect normalizes the two drag corners into a rectangle of at least
// 1 × 1 physical pixel (SCR-010). Both corners are clamped into the desktop
// first, so the result can never reach outside it, and the rectangle
// includes the pixels under both corners — matching the resize handles, so a
// corner-to-corner drag can still select the desktop's last row and column.
func drawRect(anchor, current display.Point, desktop display.Rect) display.Rect {
	anchor = clampPoint(anchor, desktop)
	current = clampPoint(current, desktop)
	return display.Rect{
		Left:   min(anchor.X, current.X),
		Top:    min(anchor.Y, current.Y),
		Right:  max(anchor.X, current.X) + 1,
		Bottom: max(anchor.Y, current.Y) + 1,
	}
}

// hitZone classifies a pointer position against the selection's resize
// handles and interior. Handles are grab×grab squares centered on the
// corners and edge midpoints and win over the interior, so even a 1 × 1
// selection stays adjustable. For selections smaller than the full grab
// reach the grab shrinks instead: three overlapping handle squares per edge
// would otherwise cover the whole rectangle and leave no interior to drag,
// making such a selection impossible to move (SCR-012).
func hitZone(selection display.Rect, p display.Point, grab int32) zone {
	if selection.Empty() {
		return zoneOutside
	}
	if smaller := min(selection.Width(), selection.Height()); 2*grab >= smaller {
		grab = max(smaller/4, 1)
	}
	half := grab / 2
	centerX := (selection.Left + selection.Right) / 2
	centerY := (selection.Top + selection.Bottom) / 2
	type handleAt struct {
		z zone
		x int32
		y int32
	}
	handles := []handleAt{
		{zoneNW, selection.Left, selection.Top},
		{zoneN, centerX, selection.Top},
		{zoneNE, selection.Right, selection.Top},
		{zoneE, selection.Right, centerY},
		{zoneSE, selection.Right, selection.Bottom},
		{zoneS, centerX, selection.Bottom},
		{zoneSW, selection.Left, selection.Bottom},
		{zoneW, selection.Left, centerY},
	}
	for _, h := range handles {
		if p.X >= h.x-half && p.X < h.x-half+grab && p.Y >= h.y-half && p.Y < h.y-half+grab {
			return h.z
		}
	}
	if selection.Contains(p) {
		return zoneInterior
	}
	return zoneOutside
}

// moveSelection shifts the selection by the drag delta without changing its
// dimensions, clamped so the whole rectangle stays inside the desktop.
func moveSelection(selection display.Rect, dx, dy int32, desktop display.Rect) display.Rect {
	width, height := selection.Width(), selection.Height()
	left := selection.Left + dx
	top := selection.Top + dy
	if width >= desktop.Width() {
		left = desktop.Left
	} else {
		left = min(max(left, desktop.Left), desktop.Right-width)
	}
	if height >= desktop.Height() {
		top = desktop.Top
	} else {
		top = min(max(top, desktop.Top), desktop.Bottom-height)
	}
	return display.Rect{Left: left, Top: top, Right: left + width, Bottom: top + height}
}

// resizeSelection drags one handle to the pointer position. The opposite
// edges stay anchored, every dimension keeps at least one physical pixel,
// and the result never reaches outside the desktop. Dragging a handle past
// its anchor edge pins the dimension at one pixel rather than flipping the
// rectangle.
func resizeSelection(selection display.Rect, z zone, p display.Point, desktop display.Rect) display.Rect {
	p = clampPoint(p, desktop)
	r := selection
	switch z {
	case zoneNW, zoneN, zoneNE:
		r.Top = min(p.Y, r.Bottom-1)
	}
	switch z {
	case zoneSW, zoneS, zoneSE:
		r.Bottom = max(p.Y+1, r.Top+1)
	}
	switch z {
	case zoneNW, zoneW, zoneSW:
		r.Left = min(p.X, r.Right-1)
	}
	switch z {
	case zoneNE, zoneE, zoneSE:
		r.Right = max(p.X+1, r.Left+1)
	}
	return r
}

// revalidatedSelection checks a live selection against a changed virtual
// desktop after a display topology change (SCR-054). A selection that still
// fits keeps its exact dimensions and, where its coordinates remain valid,
// its position; a selection that reaches outside the new desktop is moved
// back inside without resizing. A selection whose dimensions exceed the new
// desktop cannot be kept: the second return value is false and the session
// must end with an explanation (AC-S12). An empty selection — nothing drawn
// yet — is always kept.
func revalidatedSelection(selection display.Rect, desktop display.Rect) (display.Rect, bool) {
	if selection.Empty() {
		return display.Rect{}, true
	}
	if selection.Width() > desktop.Width() || selection.Height() > desktop.Height() {
		return display.Rect{}, false
	}
	return moveSelection(selection, 0, 0, desktop), true
}

// sizedSelection applies typed width and height values to the selection,
// anchored at its top-left corner (SCR-013). The rectangle must keep at
// least one pixel and fit inside the desktop without moving; the caller
// reports a violation and keeps the current selection. The fit check runs
// in 64 bits by subtraction: ParseDimension accepts values up to int64's
// maximum, and even int64 addition of such a value to the selection's
// origin can overflow and wrap into a bogus rectangle that fits.
func sizedSelection(selection display.Rect, width, height int, desktop display.Rect) (display.Rect, bool) {
	if width < 1 || height < 1 {
		return display.Rect{}, false
	}
	if int64(width) > int64(desktop.Right)-int64(selection.Left) ||
		int64(height) > int64(desktop.Bottom)-int64(selection.Top) {
		return display.Rect{}, false
	}
	// The fit check bounds width and height by the distance to the desktop's
	// edges, so these sums stay well inside int64 and int32.
	return display.Rect{
		Left:   selection.Left,
		Top:    selection.Top,
		Right:  int32(int64(selection.Left) + int64(width)),
		Bottom: int32(int64(selection.Top) + int64(height)),
	}, true
}

// typedSelection places a Free Rectangle selection of typed width and height
// that has none yet (SCR-015): centered on the cursor display's work area and
// kept inside the virtual desktop, as presetPlacement places a preset frame.
// The rectangle must keep at least one pixel and fit inside the desktop; the
// fit check runs in 64 bits, as ParseDimension accepts values up to int64's
// maximum.
func typedSelection(anchor, desktop display.Rect, width, height int) (display.Rect, bool) {
	if width < 1 || height < 1 || int64(width) > int64(desktop.Width()) || int64(height) > int64(desktop.Height()) {
		return display.Rect{}, false
	}
	return presetPlacement(anchor, desktop, int32(width), int32(height)), true
}

// presetPlacement positions the Preset Rectangle frame's initial selection:
// centered on the cursor display's work area, then clamped so the whole
// rectangle stays inside the virtual desktop (SCR-026). The dimensions never
// change (SCR-020); the caller guarantees they fit the desktop.
func presetPlacement(anchor, desktop display.Rect, width, height int32) display.Rect {
	left := anchor.Left + (anchor.Width()-width)/2
	top := anchor.Top + (anchor.Height()-height)/2
	left = min(max(left, desktop.Left), desktop.Right-width)
	top = min(max(top, desktop.Top), desktop.Bottom-height)
	return display.Rect{Left: left, Top: top, Right: left + width, Bottom: top + height}
}

// toolbarPlacement picks the toolbar's position. Without a selection the
// toolbar is centered at the top of the anchor work area. With a selection
// it sits centered below the selection, or above it when there is no room
// below, so it does not cover the selected content where practical
// (SCR-025); every result is clamped into the virtual desktop so the
// toolbar stays fully reachable on every display arrangement, including
// negative-coordinate displays and selections touching a display edge.
func toolbarPlacement(desktop, anchor, selection display.Rect, width, height, margin int32) (x, y int32) {
	if !selection.Empty() {
		x = selection.Left + (selection.Width()-width)/2
		y = selection.Bottom + margin
		if y+height > desktop.Bottom {
			y = selection.Top - margin - height
		}
	} else {
		x = anchor.Left + (anchor.Width()-width)/2
		y = anchor.Top + margin
	}
	if x < desktop.Left {
		x = desktop.Left
	}
	if y < desktop.Top {
		y = desktop.Top
	}
	if x+width > desktop.Right {
		x = desktop.Right - width
	}
	if y+height > desktop.Bottom {
		y = desktop.Bottom - height
	}
	return x, y
}
