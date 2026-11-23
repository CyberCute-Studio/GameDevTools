package screenshot

import (
	"errors"
	"fmt"

	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot/capture"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// captureFailureMessage builds the user-facing message for a failed capture.
// Typed capture errors are presented by kind so the user can act on them
// (UX-010, PRD 9.8): a display that disconnected or a layout that changed
// mid-capture asks for a fresh display selection, and a native graphics
// failure names the operation and suggests windowed mode, the recovery that
// applies to full-screen exclusive applications and games. Validation
// failures and untyped errors — such as a window-hide failure — keep the
// caller's flavor-specific wording.
func captureFailureMessage(fallback string, err error) string {
	var captureErr *capture.Error
	if !errors.As(err, &captureErr) {
		return fmt.Sprintf(fallback, err)
	}
	// A selection in a gap between displays is not a layout that changed.
	if errors.Is(captureErr, capture.ErrNotOnDisplay) {
		return ui.CaptureNotOnDisplay
	}
	switch captureErr.Kind {
	case capture.KindDisplay:
		return fmt.Sprintf(ui.CaptureDisplayChanged, captureErr)
	case capture.KindNative:
		return fmt.Sprintf(ui.CaptureGraphicsFailed, captureErr)
	case capture.KindValidation:
		return ui.FormatError(ui.ErrorValidation, captureErr)
	default:
		return fmt.Sprintf(fallback, err)
	}
}
