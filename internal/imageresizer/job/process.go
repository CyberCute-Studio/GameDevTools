// Package job runs Image Resizer work.
//
// Process takes one planned item end to end: it decodes the source with its
// EXIF orientation applied (RSZ-018), resizes it to the job's preset
// (RSZ-010 to RSZ-017), encodes the result in the item's planned format
// (RSZ-022, RSZ-028, RSZ-029), and saves it at the item's destination
// through a temporary file (RSZ-027, NFR-023). The source is opened
// read-only and never modified (NFR-020).
//
// Functions are safe to call from any goroutine and never touch the GUI;
// callers run them off the GUI thread and deliver results through the shell
// dispatcher, per TECH-005.
package job

import (
	"context"
	"image"
	"image/color"
	"io"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/decode"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/encode"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/output"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/resize"
)

// Settings are what a job applies to each of its items.
type Settings struct {
	// Target is the preset's dimensions and fitting mode.
	Target resize.Target
	// JPEGQuality is the quality JPEG results are encoded with, 1 to 100.
	JPEGQuality int
	// Background is the opaque color translucent pixels are flattened
	// against in JPEG results.
	Background color.RGBA
}

// encodeOptions are the encode options for a result in the given format.
func (s Settings) encodeOptions(format decode.Format) encode.Options {
	return encode.Options{Format: format, Quality: s.JPEGQuality, Background: s.Background}
}

// Process resizes one item of a plan and saves its result, returning the
// destination it saved. Only an item output.Write can save can be processed:
// an ActionCreate or ActionReplace item, as output.Item.CheckWrite checks. A
// deferred item Plan.Successor names, whose turn to create the name has
// come, is processed with its Action set to output.ActionCreate.
//
// The item, the settings, and the encode options for the item's format are
// checked before the source is read. Nothing holds the decoded source once it
// is resized, so the collector can reclaim it while the result is encoded and
// written.
//
// The error is the failing step's own: a *decode.Error for a source that
// cannot be read or decoded, a *resize.Error, an *encode.Error, or an
// *output.Error, including the KindValidation one for an item that saves no
// file and the KindChanged one for a destination that changed after the
// plan was made. A cancelled context fails with the step's KindCanceled
// error, which errors.Is reports as the context's error. As with
// output.Write, a destination returned alongside an error means the result
// was saved but a file the error names was left behind.
func Process(ctx context.Context, item output.Item, settings Settings) (string, error) {
	if err := item.CheckWrite(); err != nil {
		return "", err
	}
	if err := settings.Target.Validate(); err != nil {
		return "", err
	}
	options := settings.encodeOptions(item.Format)
	if err := options.Validate(); err != nil {
		return "", err
	}
	resized, err := resizeSource(ctx, item.Source, settings.Target)
	if err != nil {
		return "", err
	}
	return output.Write(ctx, item, func(w io.Writer) error {
		return encode.Encode(w, resized, options)
	})
}

// resizeSource decodes the source and resizes it. The decoded source is no
// longer reachable once it returns.
func resizeSource(ctx context.Context, path string, target resize.Target) (image.Image, error) {
	source, err := decode.Decode(ctx, path)
	if err != nil {
		return nil, err
	}
	return resize.Resize(ctx, source.Pixels, target)
}
