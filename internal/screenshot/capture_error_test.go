package screenshot

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot/capture"
)

func TestCaptureFailureMessage(t *testing.T) {
	fallback := "The display could not be captured: %v"

	// Untyped errors, such as a window-hide failure, keep the caller's
	// flavor-specific wording.
	untyped := errors.New("window could not be hidden")
	if got := captureFailureMessage(fallback, untyped); got != fmt.Sprintf(fallback, untyped) {
		t.Fatalf("untyped error message = %q", got)
	}

	// A disconnected display or a mid-capture layout change asks for a
	// fresh look at the connected displays rather than reporting a generic
	// failure (UX-010).
	displayErr := &capture.Error{Kind: capture.KindDisplay, Op: "find display", Err: errors.New(`display \\.\DISPLAY2 is no longer connected`)}
	got := captureFailureMessage(fallback, displayErr)
	if !strings.Contains(got, "no longer connected") || !strings.Contains(got, "Check the connected displays") {
		t.Fatalf("display-kind message = %q", got)
	}

	// A native graphics failure names the operation and suggests windowed
	// mode, the recovery for full-screen exclusive applications (PRD 9.8).
	nativeErr := &capture.Error{Kind: capture.KindNative, Op: "BitBlt", Err: errors.New("access denied")}
	got = captureFailureMessage(fallback, nativeErr)
	if !strings.Contains(got, "access denied") || !strings.Contains(got, "windowed mode") {
		t.Fatalf("native-kind message = %q", got)
	}

	// Invalid capture geometry is a validation failure, distinct from a
	// capture failure (UX-010).
	validationErr := &capture.Error{Kind: capture.KindValidation, Op: "validate region", Err: errors.New("capture rectangle is empty or inverted")}
	got = captureFailureMessage(fallback, validationErr)
	if !strings.HasPrefix(got, "Validation error: ") || !strings.Contains(got, "empty or inverted") {
		t.Fatalf("validation-kind message = %q", got)
	}

	// A wrapped typed error is still recognized.
	wrapped := fmt.Errorf("capture display: %w", displayErr)
	if got := captureFailureMessage(fallback, wrapped); !strings.Contains(got, "Check the connected displays") {
		t.Fatalf("wrapped display-kind message = %q", got)
	}
}
