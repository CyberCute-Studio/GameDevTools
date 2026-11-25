//go:build windows

package ui

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/ui/uiatest"
)

var (
	procTestGetTextColor  = themeGDI32.NewProc("GetTextColor")
	procTestGetBkColor    = themeGDI32.NewProc("GetBkColor")
	procTestGetBkMode     = themeGDI32.NewProc("GetBkMode")
	procTestGetSysColorUI = user32.NewProc("GetSysColor")
)

const bcmGetIdealSize = 0x1601

// TestControlsUseCommonControls6 pins that the tests drive the Common
// Controls the application does (internal/ui/visualstyles): version 6, whose
// buttons answer BCM_GETIDEALSIZE and send the custom-draw notifications the
// theme paints them through. Version 5 does not know the message.
func TestControlsUseCommonControls6(t *testing.T) {
	parent := newTestParent(t)
	button, err := NewButton(parent, "Start Capture", ButtonOptions{ID: 7})
	if err != nil {
		t.Fatal(err)
	}
	var size struct{ cx, cy int32 }
	if r, _, _ := procSendMessageW.Call(uintptr(button.Handle()), bcmGetIdealSize, 0, uintptr(unsafe.Pointer(&size))); r == 0 {
		t.Fatal("the button does not answer BCM_GETIDEALSIZE: the Common Controls are not version 6")
	}
}

// withPalette runs the test with the dark palette, or a contrast theme's
// when highContrast, as if Windows had reported it, and chooses the palette
// again once the test ends.
func withPalette(t *testing.T, highContrast bool) {
	t.Helper()
	previous := highContrastActive
	highContrastActive = func() bool { return highContrast }
	refreshPalette()
	t.Cleanup(func() {
		highContrastActive = previous
		refreshPalette()
	})
}

// colorsOf sends the parent's WM_CTLCOLOR* message for the control through
// ThemeMessage and returns the text and background colors it set and its
// background mode.
func colorsOf(t *testing.T, parent, control windows.Handle, message uint32) (text, background Color, mode uintptr) {
	t.Helper()
	dc, _, _ := procThemeGetDC.Call(uintptr(parent))
	if dc == 0 {
		t.Fatal("GetDC failed")
	}
	defer procThemeReleaseDC.Call(uintptr(parent), dc)
	result, handled := ThemeMessage(parent, message, dc, uintptr(control))
	if !handled || result == 0 {
		t.Fatalf("message %#x: handled %v, brush %#x", message, handled, result)
	}
	textRef, _, _ := procTestGetTextColor.Call(dc)
	backgroundRef, _, _ := procTestGetBkColor.Call(dc)
	mode, _, _ = procTestGetBkMode.Call(dc)
	return colorFromRef(textRef), colorFromRef(backgroundRef), mode
}

// TestLabelsTakeTheirStylesColor pins the colors labels are drawn in: content
// in the text color, a field's label and a hint muted, a validation message
// as an error, and a status area's notice in its severity's color, all
// transparent over the surface behind them.
func TestLabelsTakeTheirStylesColor(t *testing.T) {
	withPalette(t, false)
	parent := newTestParent(t)
	p := CurrentPalette()
	body, err := NewLabel(parent, "Screenshot_yyyyMMdd.png")
	if err != nil {
		t.Fatal(err)
	}
	fieldLabel, err := NewMnemonicLabel(parent, "&Output folder")
	if err != nil {
		t.Fatal(err)
	}
	hint, err := NewLabel(parent, "Drop PNG or JPEG files")
	if err != nil {
		t.Fatal(err)
	}
	hint.SetStyle(TextMuted)
	input, err := NewTextInput(parent, "&JPEG quality", 3)
	if err != nil {
		t.Fatal(err)
	}
	status, err := NewStatusArea(parent)
	if err != nil {
		t.Fatal(err)
	}
	status.Update(Notice{Kind: NoticeWarning, Message: "The shortcut is held."})
	for _, test := range []struct {
		name    string
		control windows.Handle
		want    Color
	}{
		{"content", body.Handle(), p.Text},
		{"field label", fieldLabel.Handle(), p.TextMuted},
		{"hint", hint.Handle(), p.TextMuted},
		{"validation message", input.Error.Handle(), p.Error},
		{"warning", status.Handle(), p.Warning},
	} {
		text, background, mode := colorsOf(t, parent, test.control, wmCtlColorStatic)
		if text != test.want {
			t.Errorf("%s: text %06x, want %06x", test.name, text, test.want)
		}
		if background != p.Window || mode != themeTransparent {
			t.Errorf("%s: background %06x mode %d, want %06x transparent", test.name, background, mode, p.Window)
		}
	}
	status.Update(Notice{Kind: NoticeError, Message: "Could not save."})
	if text, _, _ := colorsOf(t, parent, status.Handle(), wmCtlColorStatic); text != p.Error {
		t.Errorf("an error notice's text is %06x, want %06x", text, p.Error)
	}
	status.Clear()
	if text, _, _ := colorsOf(t, parent, status.Handle(), wmCtlColorStatic); text != p.Text {
		t.Errorf("a cleared notice's text is %06x, want %06x", text, p.Text)
	}
}

// TestFieldsAndListsTakeTheFieldFill pins that a text field, a read-only
// field, and a list draw their text on the field fill, and a disabled field
// on the fill its frame is painted in too.
func TestFieldsAndListsTakeTheFieldFill(t *testing.T) {
	withPalette(t, false)
	parent := newTestParent(t)
	p := CurrentPalette()
	edit, err := NewEdit(parent, 4)
	if err != nil {
		t.Fatal(err)
	}
	path, err := NewReadOnlyText(parent, 5)
	if err != nil {
		t.Fatal(err)
	}
	list, err := NewListBox(parent, 6)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		control windows.Handle
		message uint32
	}{
		{"field", edit.Handle(), wmCtlColorEdit},
		{"read-only field", path.Handle(), wmCtlColorStatic},
		{"list", list.Handle(), wmCtlColorListBox},
	} {
		text, background, _ := colorsOf(t, parent, test.control, test.message)
		if text != p.Text || background != p.Field {
			t.Errorf("%s: %06x on %06x, want %06x on %06x", test.name, text, background, p.Text, p.Field)
		}
	}
	edit.Enable(false)
	_, background, _ := colorsOf(t, parent, edit.Handle(), wmCtlColorStatic)
	if want := p.Window.blend(p.Field, 0.5); background != want {
		t.Errorf("disabled field: background %06x, want %06x", background, want)
	}
	if fill, _, _ := fieldColors(edit.Handle(), false, false); fill != background {
		t.Errorf("disabled field: frame fill %06x, text area %06x", fill, background)
	}
}

// TestSurfacesChooseTheBackgroundBehindAControl pins that a control in a
// region of another surface, such as the navigation's heading in the
// sidebar, is drawn over that surface.
func TestSurfacesChooseTheBackgroundBehindAControl(t *testing.T) {
	withPalette(t, false)
	parent := newTestParent(t)
	p := CurrentPalette()
	inside, err := NewLabel(parent, "TOOLS")
	if err != nil {
		t.Fatal(err)
	}
	outside, err := NewLabel(parent, "Screenshot")
	if err != nil {
		t.Fatal(err)
	}
	inside.Move(10, 10, 80, 20)
	outside.Move(150, 10, 80, 20)
	SetSurfaces(parent, []SurfaceRegion{{Right: 120, Bottom: 300, Surface: SurfaceSidebar}})
	if _, background, _ := colorsOf(t, parent, inside.Handle(), wmCtlColorStatic); background != p.Sidebar {
		t.Errorf("in the sidebar: background %06x, want %06x", background, p.Sidebar)
	}
	if _, background, _ := colorsOf(t, parent, outside.Handle(), wmCtlColorStatic); background != p.Window {
		t.Errorf("beside the sidebar: background %06x, want %06x", background, p.Window)
	}
}

// customDraw sends a button's prepaint notification through ThemeMessage
// with a memory device context and returns the answer.
func customDraw(t *testing.T, parent windows.Handle, button *Button) uintptr {
	t.Helper()
	screen, _, _ := procThemeGetDC.Call(0)
	defer procThemeReleaseDC.Call(0, screen)
	dc, _, _ := procThemeCreateCompatibleDC.Call(screen)
	if dc == 0 {
		t.Fatal("CreateCompatibleDC failed")
	}
	defer procThemeDeleteDC.Call(dc)
	draw := themeNMCustomDraw{
		hdr:       themeNMHdr{hwndFrom: uintptr(button.Handle()), code: nmCustomDraw},
		drawStage: cddsPrePaint,
		hdc:       dc,
		rc:        themeRect{Right: 120, Bottom: 32},
	}
	result, handled := ThemeMessage(parent, wmNotify, 0, uintptr(unsafe.Pointer(&draw)))
	if !handled {
		t.Fatal("the button's custom draw was not handled")
	}
	return result
}

// TestButtonsDrawThemselvesUnderAContrastTheme pins that the theme paints the
// buttons in the dark palette and leaves them to draw themselves, in the
// system colors, under a contrast theme, whose colors the palette then
// holds.
func TestButtonsDrawThemselvesUnderAContrastTheme(t *testing.T) {
	parent := newTestParent(t)
	push, err := NewButton(parent, "&Browse…", ButtonOptions{ID: 8})
	if err != nil {
		t.Fatal(err)
	}
	radio, err := NewButton(parent, "Free Rectangle", ButtonOptions{ID: 9, Radio: true, Group: true})
	if err != nil {
		t.Fatal(err)
	}
	navigation, err := NewButton(parent, "Screenshot", ButtonOptions{ID: 10, Radio: true, Group: true, Navigation: true, Icon: ''})
	if err != nil {
		t.Fatal(err)
	}
	// No subtests: they would run off the thread that owns the windows.
	withPalette(t, false)
	for _, button := range []*Button{push, radio, navigation} {
		if got := customDraw(t, parent, button); got != cdrfSkipDefault {
			t.Errorf("dark: %s: custom draw answered %#x, want CDRF_SKIPDEFAULT", button.Text(), got)
		}
	}
	highContrastActive = func() bool { return true }
	refreshPalette()
	p := CurrentPalette()
	window, _, _ := procTestGetSysColorUI.Call(colorWindowIndex)
	text, _, _ := procTestGetSysColorUI.Call(colorWindowTextIndex)
	if !p.HighContrast || p.Window != colorFromRef(window) || p.Text != colorFromRef(text) || p.Error != p.Text {
		t.Fatalf("the contrast theme's palette is %+v, want the system colors", p)
	}
	for _, button := range []*Button{push, radio, navigation} {
		if got := customDraw(t, parent, button); got != cdrfDoDefault {
			t.Errorf("contrast theme: %s: custom draw answered %#x, want CDRF_DODEFAULT", button.Text(), got)
		}
	}
}

// TestAWindowRedrawsItselfWhenThePaletteChanges pins how a contrast theme
// switched on reaches a window: the system's report of the change to the
// window chooses the palette again and moves the window to the new palette's
// generation, and a second report, to the same or another window, finds
// nothing changed.
func TestAWindowRedrawsItselfWhenThePaletteChanges(t *testing.T) {
	withPalette(t, false)
	window := newTestParent(t)
	ThemeWindow(window, WindowPopup)
	generation := func() uint64 {
		themeMutex.Lock()
		defer themeMutex.Unlock()
		return themedWindows[window].generation
	}
	before := generation()
	highContrastActive = func() bool { return true }
	if _, handled := ThemeMessage(window, wmSysColorChange, 0, 0); handled {
		t.Error("the change was kept from the window's own handling")
	}
	after := generation()
	if after == before || !CurrentPalette().HighContrast {
		t.Fatalf("generation %d → %d, high contrast %v: the window did not move to the new palette", before, after, CurrentPalette().HighContrast)
	}
	ThemeMessage(window, wmSettingChange, 0, 0)
	if again := generation(); again != after {
		t.Errorf("a second report moved the generation from %d to %d", after, again)
	}
	ThemeMessage(window, wmNCDestroy, 0, 0)
	themeMutex.Lock()
	_, kept := themedWindows[window]
	themeMutex.Unlock()
	if kept {
		t.Error("the window was kept once destroyed")
	}
}

// TestFieldTextIsPaddedAndCentered pins the padding the theme's frame gives a
// text field: its text keeps clear of the frame at the sides, and its line
// sits in the middle of the field's height.
func TestFieldTextIsPaddedAndCentered(t *testing.T) {
	parent := newTestParent(t)
	edit, err := NewEdit(parent, 11)
	if err != nil {
		t.Fatal(err)
	}
	font, err := NewFont(14, 400, FontText)
	if err != nil {
		t.Fatal(err)
	}
	defer procThemeDeleteObject.Call(uintptr(font))
	edit.SetFont(font)
	edit.Move(0, 0, 200, 32)
	window, client, ok := windowAndClient(edit.Handle())
	if !ok {
		t.Fatal("the field's rectangles could not be read")
	}
	line := lineHeight(edit.Handle())
	if line <= 0 {
		t.Fatal("the field's line height could not be measured")
	}
	if client.Left < 8 || window.Right-client.Right < 8 {
		t.Errorf("client %+v in window %+v: the sides are not padded", client, window)
	}
	if client.height() != line {
		t.Errorf("client height %d, want the line's %d", client.height(), line)
	}
	if above, below := client.Top, window.Bottom-client.Bottom; above-below > 1 || below-above > 1 {
		t.Errorf("the line is %d from the top and %d from the bottom", above, below)
	}
}

// TestThemedControlsAreForgottenWhenDestroyed pins that the theme lets go of a
// control as it is destroyed, before Windows can give its handle to another
// window.
func TestThemedControlsAreForgottenWhenDestroyed(t *testing.T) {
	parent := newTestParent(t)
	label, err := NewLabel(parent, "Gone")
	if err != nil {
		t.Fatal(err)
	}
	field, err := NewEdit(parent, 12)
	if err != nil {
		t.Fatal(err)
	}
	for _, control := range []windows.Handle{label.Handle(), field.Handle()} {
		if themed(control) == nil {
			t.Fatalf("%#x was not themed", control)
		}
		procDestroyWindow.Call(uintptr(control))
		if themed(control) != nil {
			t.Errorf("%#x is still themed once destroyed", control)
		}
	}
}

// TestOwnerDrawnListsKeepTheirStrings pins that the lists the theme draws
// keep their items' strings, which a screen reader and the views read back.
func TestOwnerDrawnListsKeepTheirStrings(t *testing.T) {
	parent := newTestParent(t)
	list, err := NewListBox(parent, 13)
	if err != nil {
		t.Fatal(err)
	}
	combo, err := NewComboBox(parent, 14)
	if err != nil {
		t.Fatal(err)
	}
	const gwlStyle = ^uintptr(15)
	for _, test := range []struct {
		control windows.Handle
		want    uintptr
	}{
		{list.Handle(), lbsOwnerDrawFixed | lbsHasStrings},
		{combo.Handle(), cbsOwnerDrawFixed | cbsHasStrings},
	} {
		style, _, _ := procGetWindowLongPtrW.Call(uintptr(test.control), gwlStyle)
		if style&test.want != test.want {
			t.Errorf("%#x: style %#x lacks %#x", test.control, style, test.want)
		}
	}
	list.Add("cover.png — 1920 × 1080 px — Ready")
	combo.Add("PNG")
	if got := list.ItemText(0); got != "cover.png — 1920 × 1080 px — Ready" {
		t.Errorf("list item = %q", got)
	}
	if got := combo.ItemText(0); got != "PNG" {
		t.Errorf("drop-down item = %q", got)
	}
}

func TestFontInstalledTellsAMissingTypeface(t *testing.T) {
	if !fontInstalled("Segoe UI") {
		t.Error("Segoe UI is reported missing")
	}
	if fontInstalled("No Such Typeface 7f3a") {
		t.Error("a missing typeface is reported installed")
	}
}

// TestThemedControlsKeepTheirRoles pins that drawing a control in the theme
// leaves it the control a screen reader knows (UX-006, screen-reader
// metadata): subclassed, custom-drawn, and owner-drawn, each still reports
// its own UI Automation control type.
func TestThemedControlsKeepTheirRoles(t *testing.T) {
	const (
		buttonType      = 50000 // UIA_ButtonControlTypeId
		comboBoxType    = 50003 // UIA_ComboBoxControlTypeId
		editType        = 50004 // UIA_EditControlTypeId
		listType        = 50008 // UIA_ListControlTypeId
		radioButtonType = 50013 // UIA_RadioButtonControlTypeId
		textType        = 50020 // UIA_TextControlTypeId
	)
	parent := newTestParent(t)
	procShowWindowTest.Call(uintptr(parent), swShowNoActivate)
	label, err := NewLabel(parent, "Filename pattern")
	if err != nil {
		t.Fatal(err)
	}
	push, err := NewButton(parent, "Browse", ButtonOptions{ID: 20})
	if err != nil {
		t.Fatal(err)
	}
	radio, err := NewButton(parent, "Free Rectangle", ButtonOptions{ID: 21, Radio: true, Group: true})
	if err != nil {
		t.Fatal(err)
	}
	navigation, err := NewButton(parent, "Screenshot", ButtonOptions{ID: 22, Radio: true, Group: true, Navigation: true, Icon: ''})
	if err != nil {
		t.Fatal(err)
	}
	field, err := NewEdit(parent, 23)
	if err != nil {
		t.Fatal(err)
	}
	path, err := NewReadOnlyText(parent, 24)
	if err != nil {
		t.Fatal(err)
	}
	combo, err := NewComboBox(parent, 25)
	if err != nil {
		t.Fatal(err)
	}
	list, err := NewListBox(parent, 26)
	if err != nil {
		t.Fatal(err)
	}
	controls := []windows.Handle{label.Handle(), push.Handle(), radio.Handle(), navigation.Handle(), field.Handle(), path.Handle(), combo.Handle(), list.Handle()}
	want := []int32{textType, buttonType, radioButtonType, radioButtonType, editType, editType, comboBoxType, listType}
	got, err := uiatest.ControlTypes(controls...)
	if err != nil {
		t.Fatal(err)
	}
	for i := range controls {
		if got[i] != want[i] {
			t.Errorf("control %d: control type %d, want %d", i, got[i], want[i])
		}
	}
}
