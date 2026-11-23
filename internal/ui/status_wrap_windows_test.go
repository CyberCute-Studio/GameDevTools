//go:build windows

package ui

import "testing"

// TestStatusAreaBreaksATokenWiderThanItself pins that a saved path with no
// spaces wraps inside the status area, where a plain static control would cut
// it off at the right edge.
func TestStatusAreaBreaksATokenWiderThanItself(t *testing.T) {
	parent := newTestParent(t)
	status, err := NewStatusArea(parent)
	if err != nil {
		t.Fatal(err)
	}
	style, _, _ := procGetWindowLongPtrW.Call(uintptr(status.Handle()), ^uintptr(15))
	if style&ssEditControl == 0 {
		t.Fatalf("status style = %#x, want SS_EDITCONTROL so a long path wraps", style)
	}
	if style&ssNoPrefix == 0 {
		t.Fatalf("status style = %#x, want SS_NOPREFIX kept", style)
	}
}

// TestFieldMessagesBreakATokenWiderThanThemselves pins that a field's message,
// which a layout sizes by the same measure as the status area, wraps a long
// token rather than leaving the measured height blank.
func TestFieldMessagesBreakATokenWiderThanThemselves(t *testing.T) {
	parent := newTestParent(t)
	text, err := NewTextInput(parent, "Name", 90)
	if err != nil {
		t.Fatal(err)
	}
	dimension, err := NewDimensionInput(parent, "Width", 91)
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range []*Label{text.Error, dimension.Error} {
		style, _, _ := procGetWindowLongPtrW.Call(uintptr(label.Handle()), ^uintptr(15))
		if style&ssEditControl == 0 {
			t.Fatalf("message style = %#x, want SS_EDITCONTROL", style)
		}
	}
}
