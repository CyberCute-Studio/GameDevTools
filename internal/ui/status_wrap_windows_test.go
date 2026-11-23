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
