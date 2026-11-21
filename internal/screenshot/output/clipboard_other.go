//go:build !windows

package output

import (
	"errors"
	"image"
)

// setClipboardImage is a Windows-only capability; the production
// application only runs on Windows. The error is typed KindClipboard so
// callers classify it the same way as a native Windows clipboard failure.
func setClipboardImage(img *image.RGBA) error {
	return kindError(KindClipboard, "set clipboard image",
		errors.New("clipboard image output is available on Windows only"))
}
