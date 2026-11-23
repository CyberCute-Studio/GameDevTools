//go:build windows

package ui

import (
	"runtime"
	"slices"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestImagePickerFilterIsATerminatedPairList pins the form GetOpenFileNameW
// reads: a label, its pattern, each ending in a NUL, and one more NUL.
func TestImagePickerFilterIsATerminatedPairList(t *testing.T) {
	filter, err := imagePickerFilter()
	if err != nil {
		t.Fatal(err)
	}
	if len(filter) < 2 || filter[len(filter)-1] != 0 || filter[len(filter)-2] != 0 {
		t.Fatalf("the filter does not end in two NULs: %v", filter)
	}
	parts := slices.Collect(func(yield func(string) bool) {
		start := 0
		for i, unit := range filter[:len(filter)-1] {
			if unit == 0 {
				if !yield(windows.UTF16ToString(filter[start:i])) {
					return
				}
				start = i + 1
			}
		}
	})
	want := []string{ImageFileFilterLabel + " (*.png; *.jpg; *.jpeg)", "*.png;*.jpg;*.jpeg"}
	if !slices.Equal(parts, want) {
		t.Fatalf("filter strings = %q, want %q", parts, want)
	}
}

// TestPickImageFilesOpensTheRealPicker opens the real common dialog, which
// the view's tests replace with a stand-in, and cancels it. The picker runs on
// a thread of its own, so the test fails at its limit even if a shell call
// blocks that thread and the picker never returns.
func TestPickImageFilesOpensTheRealPicker(t *testing.T) {
	const title = "CyberCute Dev Tools picker test"
	const idCancel = 2
	const limit = 30 * time.Second
	type outcome struct {
		paths  []string
		picked bool
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		// Never unlocked, so the thread ends with this goroutine.
		runtime.LockOSThread()
		paths, picked, err := PickImageFiles(0, title)
		done <- outcome{paths, picked, err}
	}()
	closePicker := func() bool {
		dialog, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(utf16("#32770"))), uintptr(unsafe.Pointer(utf16(title))))
		if dialog != 0 {
			procPostMessageW.Call(dialog, wmCommand, idCancel, 0)
		}
		return dialog != 0
	}
	returned := false
	// A picker left open would disturb the package's later tests.
	t.Cleanup(func() {
		for range 40 {
			if returned {
				return
			}
			closePicker()
			select {
			case <-done:
				returned = true
			case <-time.After(50 * time.Millisecond):
			}
		}
	})
	opened := false
	deadline := time.After(limit)
	for {
		select {
		case result := <-done:
			returned = true
			if result.err != nil {
				t.Fatalf("the picker could not be opened: %v", result.err)
			}
			if !opened {
				t.Fatal("PickImageFiles returned without showing the picker")
			}
			if result.picked || result.paths != nil {
				t.Fatalf("a cancelled picker returned %v, %v", result.paths, result.picked)
			}
			return
		case <-deadline:
			t.Fatalf("the picker was still open after %v (found: %v)", limit, opened)
		case <-time.After(50 * time.Millisecond):
		}
		opened = closePicker() || opened
	}
}

func TestListBoxMultiSelect(t *testing.T) {
	parent := newTestParent(t)
	list, err := NewListBox(parent, 42)
	if err != nil {
		t.Fatal(err)
	}
	style, _, _ := procGetWindowLongPtrW.Call(uintptr(list.Handle()), ^uintptr(15))
	if style&wsTabStop == 0 {
		t.Fatal("list box is not keyboard focusable")
	}

	list.Add("one.png — Ready")
	list.Add("two.bmp — Unsupported format")
	list.Add("three.jpg — Ready")
	if list.Count() != 3 {
		t.Fatalf("count = %d, want 3", list.Count())
	}
	if got := list.ItemText(1); got != "two.bmp — Unsupported format" {
		t.Fatalf("item 1 = %q", got)
	}
	if indices := list.SelectedIndices(); len(indices) != 0 {
		t.Fatalf("selection = %v, want none", indices)
	}

	list.SetSelected(0, true)
	list.SetSelected(2, true)
	indices := list.SelectedIndices()
	if len(indices) != 2 || indices[0] != 0 || indices[1] != 2 {
		t.Fatalf("selection = %v, want [0 2]", indices)
	}
	list.SetSelected(0, false)
	if indices := list.SelectedIndices(); len(indices) != 1 || indices[0] != 2 {
		t.Fatalf("selection after clearing = %v, want [2]", indices)
	}

	list.Reset()
	if list.Count() != 0 {
		t.Fatalf("count after reset = %d", list.Count())
	}
	if indices := list.SelectedIndices(); len(indices) != 0 {
		t.Fatalf("selection after reset = %v", indices)
	}
}

func TestParsePickedPaths(t *testing.T) {
	pack := func(parts ...string) []uint16 {
		var buffer []uint16
		for _, part := range parts {
			wide, err := windows.UTF16FromString(part)
			if err != nil {
				t.Fatal(err)
			}
			buffer = append(buffer, wide...)
		}
		return append(buffer, 0)
	}

	if paths := parsePickedPaths(pack()); paths != nil {
		t.Fatalf("empty buffer = %v", paths)
	}
	single := parsePickedPaths(pack(`C:\Images\one.png`))
	if len(single) != 1 || single[0] != `C:\Images\one.png` {
		t.Fatalf("single selection = %v", single)
	}
	multi := parsePickedPaths(pack(`C:\Images`, "one.png", "two.jpg", "three.jpeg"))
	if len(multi) != 3 || multi[0] != `C:\Images\one.png` || multi[1] != `C:\Images\two.jpg` || multi[2] != `C:\Images\three.jpeg` {
		t.Fatalf("multi selection = %v", multi)
	}
	// Go 1.27's filepath.Join would move this folder to the current drive.
	share := parsePickedPaths(pack(`\\server\..\dir`, "one.png", "two.png"))
	if len(share) != 2 || share[0] != `\\server\..\dir\one.png` || share[1] != `\\server\..\dir\two.png` {
		t.Fatalf("selection under a share = %v", share)
	}
}
