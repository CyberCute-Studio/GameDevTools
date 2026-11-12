package capture

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
)

func TestValidateRegion(t *testing.T) {
	desktop := display.Rect{Left: -1920, Top: 0, Right: 1920, Bottom: 1080}
	tests := []struct {
		name   string
		region display.Rect
		want   *Kind
	}{
		{name: "full desktop", region: desktop},
		{name: "one pixel", region: display.Rect{Left: -1, Top: 0, Right: 0, Bottom: 1}},
		{name: "spans negative and positive coordinates", region: display.Rect{Left: -100, Top: 100, Right: 100, Bottom: 200}},
		{name: "empty", region: display.Rect{}, want: kindPtr(KindValidation)},
		{name: "inverted", region: display.Rect{Left: 10, Top: 10, Right: 5, Bottom: 8}, want: kindPtr(KindValidation)},
		{name: "outside left edge", region: display.Rect{Left: desktop.Left - 1, Top: 0, Right: 0, Bottom: 10}, want: kindPtr(KindValidation)},
		{name: "outside top edge", region: display.Rect{Left: 0, Top: desktop.Top - 1, Right: 10, Bottom: 10}, want: kindPtr(KindValidation)},
		{name: "outside right edge", region: display.Rect{Left: 0, Top: 0, Right: desktop.Right + 1, Bottom: 10}, want: kindPtr(KindValidation)},
		{name: "outside bottom edge", region: display.Rect{Left: 0, Top: 0, Right: 10, Bottom: desktop.Bottom + 1}, want: kindPtr(KindValidation)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateRegion(desktop, test.region)
			if test.want == nil {
				if err != nil {
					t.Fatalf("validateRegion(%v) = %v, want nil", test.region, err)
				}
				return
			}
			var captureErr *Error
			if !errors.As(err, &captureErr) {
				t.Fatalf("validateRegion(%v) = %T, want *Error", test.region, err)
			}
			if captureErr.Kind != *test.want {
				t.Fatalf("validateRegion(%v) kind = %v, want %v", test.region, captureErr.Kind, *test.want)
			}
		})
	}
}

func kindPtr(kind Kind) *Kind { return &kind }

func TestValidateRegionPixelLimit(t *testing.T) {
	// The pixel limit is evaluated against a desktop large enough to
	// contain the request, so only the limit itself decides.
	desktop := display.Rect{Left: 0, Top: 0, Right: 1 << 20, Bottom: 1 << 20}
	atLimit := display.Rect{Left: 0, Top: 0, Right: 16384, Bottom: 16384}
	if err := validateRegion(desktop, atLimit); err != nil {
		t.Fatalf("validateRegion at the %d-pixel limit = %v, want nil", maxPixels, err)
	}
	beyond := display.Rect{Left: 0, Top: 0, Right: 16385, Bottom: 16384}
	assertKind(t, validateRegion(desktop, beyond), KindValidation)
}

func TestFindDisplay(t *testing.T) {
	displays := []display.Display{
		{DeviceName: `\\.\DISPLAY1`, Bounds: display.Rect{Right: 1920, Bottom: 1080}, Primary: true},
		{DeviceName: `\\.\DISPLAY2`, Bounds: display.Rect{Left: 1920, Right: 4480, Bottom: 1440}},
	}

	item, err := findDisplay(displays, `\\.\DISPLAY2`)
	if err != nil {
		t.Fatalf("findDisplay returned %v", err)
	}
	if item.Bounds.Left != 1920 {
		t.Fatalf("findDisplay returned bounds %v, want left edge 1920", item.Bounds)
	}

	_, err = findDisplay(displays, `\\.\DISPLAY9`)
	assertKind(t, err, KindDisplay)

	_, err = findDisplay(displays, "")
	assertKind(t, err, KindValidation)
}

func assertKind(t *testing.T, err error, want Kind) {
	t.Helper()
	var captureErr *Error
	if !errors.As(err, &captureErr) {
		t.Fatalf("error = %T (%v), want *Error", err, err)
	}
	if captureErr.Kind != want {
		t.Fatalf("error kind = %v, want %v", captureErr.Kind, want)
	}
}

func TestErrorFormattingAndUnwrap(t *testing.T) {
	cause := errors.New("access denied")
	err := &Error{Kind: KindNative, Op: "BitBlt", Err: cause}
	if !strings.Contains(err.Error(), "BitBlt") || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("Error() = %q, want operation and cause", err.Error())
	}
	if !errors.Is(err, cause) {
		t.Fatal("errors.Is did not reach the wrapped cause")
	}
	var captureErr *Error
	if !errors.As(fmt.Errorf("capture: %w", err), &captureErr) || captureErr.Kind != KindNative {
		t.Fatal("errors.As did not recover the capture error kind")
	}
}

func TestKindString(t *testing.T) {
	if KindValidation.String() != "validation" || KindDisplay.String() != "display" || KindNative.String() != "native" {
		t.Fatalf("unexpected kind names: %v %v %v", KindValidation, KindDisplay, KindNative)
	}
	if got := Kind(42).String(); got != "Kind(42)" {
		t.Fatalf("unknown kind renders as %q", got)
	}
}
