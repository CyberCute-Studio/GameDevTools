// Package resize scales an upright Image Resizer source to a preset's
// dimensions with the preset's fitting mode.
//
// Plan computes the output geometry from the source dimensions alone:
// Stretch scales each axis independently to exactly the target (RSZ-010);
// Fit scales uniformly by min(targetWidth/sourceWidth,
// targetHeight/sourceHeight), rounds each output dimension to the nearest
// whole pixel — halves round up — and clamps it to at least one pixel
// (RSZ-011, RSZ-012); and Crop to Fill scales uniformly until both target
// dimensions are covered, rounding the same way, then crops the excess
// equally from opposite sides, removing an odd leftover pixel from the right
// or bottom (RSZ-013, RSZ-014). Every mode enlarges as readily as it reduces;
// there is no upscale prevention (RSZ-015).
//
// Resize resamples the pixels with a separable Catmull-Rom cubic filter,
// widened in proportion to the reduction when downscaling so every source
// pixel contributes, which suits both enlargement and reduction (RSZ-017).
// Colors are filtered with premultiplied alpha, so fully transparent pixels
// lend no color to their neighbors, and an output pixel whose alpha rounds to
// zero is stored as transparent black. The stored sRGB values are filtered
// directly, as most image tools do, rather than converted to linear light;
// YCbCr and CMYK pixels, and the color of NYCbCrA ones, are first converted
// to 8-bit RGB by the standard library's conversion, the one the decoder
// applies when it reorients a JPEG, so reorienting a picture does not
// change the colors the filter starts from. The filter's overshoot near
// sharp edges is clamped. Crop to Fill resamples only the cropped region,
// applying exactly the filter weights that scaling the whole source and
// cropping afterwards would.
//
// Results are deterministic (RSZ-017): the same source and target produce
// bit-identical output on every platform the package compiles for — every
// 64-bit one — and in every run. Sample positions are exact rational
// numbers, the filter weights are computed with float64 operations kept from
// fusing into platform-dependent multiply-adds and are quantized to fixed
// point by rounding their running total, which keeps each within one unit of
// its exact value, and the pixels are filtered in integer arithmetic on one
// goroutine.
//
// Resize allocates the output image, which the target's PRESET-006 limit of
// 100 megapixels bounds at 800,000,000 bytes (8 bytes a pixel at 16 bits per
// channel with alpha), plus working memory that does not grow with the
// source's area. The weight tables grow with the source's dimensions, which
// MaxSourceDimension bounds, and the filter streams the source a row at a
// time, keeping no more working rows than a fixed budget holds, so the
// working memory — weight tables and rows together — stays under
// MaxWorkingBytes, 128 MiB, in the worst case and is a few megabytes for
// typical sources, within the bound WorkingBytes gives for a source's
// dimensions. Resize reads the source but never retains or modifies it, and
// callers run it off the GUI thread (TECH-005); a context cancels it between
// source rows, and before it allocates the output. A panic during a resize,
// such as one a malformed source image's own methods raise, is returned as
// that resize's error, an unexpected failure (KindFailed), rather than
// unwinding into the caller (NFR-021).
package resize

import (
	"context"
	"errors"
	"fmt"
	"image"

	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
)

// MaxSourceDimension bounds each source axis. The filter weights for an axis
// grow with its length, so the bound keeps them, and every other working
// buffer, to a fixed size whatever the source's shape. It is far beyond the
// 65,535-pixel limit of JPEG and any practical PNG; the decoder's memory
// bound already limits the source's area.
const MaxSourceDimension = 1 << 20

// MaxWorkingBytes bounds the working memory Resize needs beyond the output
// image: the weight tables of both axes, the scratch that builds them, and
// the working rows, whatever the source's shape and the target. WorkingBytes
// bounds it for one source's dimensions.
const MaxWorkingBytes = 128 << 20

// workingOverhead covers what Resize allocates beyond the weight tables and
// the working rows WorkingBytes counts: the row readers and writer, and the
// small values that describe the resize.
const workingOverhead = 64 << 10

// WorkingBytes bounds the working memory Resize needs beyond the output
// image for a source of the given upright dimensions, of any kind of pixels:
// the weight tables of both axes, the scratch that builds them, the working
// rows of whichever order the filter takes, and an eighth more for the
// allocator's rounding. It never exceeds MaxWorkingBytes, which it returns
// for dimensions or a target Plan refuses. It is a few megabytes for typical
// sources; a batch adds it to each item's memory need (NFR-014).
func WorkingBytes(sourceWidth, sourceHeight int, target Target) uint64 {
	geometry, err := Plan(sourceWidth, sourceHeight, target)
	if err != nil {
		return MaxWorkingBytes
	}
	working := axisBytes(sourceWidth, geometry.ScaledWidth, geometry.Width) +
		axisBytes(sourceHeight, geometry.ScaledHeight, geometry.Height) +
		rowBytes(sourceWidth, sourceHeight, geometry)
	working += working/8 + workingOverhead
	return min(uint64(working), MaxWorkingBytes)
}

// A scaled axis and its crop offset reach 2^34 pixels, which only a 64-bit
// int holds, so the package fails to compile where int is narrower rather
// than miscomputing there. The application supports 64-bit Windows only
// (NFR-001, NFR-004).
const _ int = 1 << 62

// Kind classifies a resize failure.
type Kind uint8

const (
	// KindInvalid means the target is not a valid resize preset — an
	// unknown fitting mode, or dimensions outside the preset limits
	// (PRESET-006) — or the source has no pixels.
	KindInvalid Kind = iota
	// KindTooLarge means the source is wider or taller than
	// MaxSourceDimension.
	KindTooLarge
	// KindCanceled means the context ended before resizing finished.
	KindCanceled
	// KindFailed means the resize failed unexpectedly: it panicked, in a
	// malformed source image's own methods or from a defect in the resizer.
	// The two cannot be told apart — a standard image whose pixel slice is
	// shorter than its bounds fails inside the resizer's own reads — so the
	// error blames neither; it wraps ErrUnexpected and the panic's value.
	KindFailed
)

func (k Kind) String() string {
	switch k {
	case KindInvalid:
		return "invalid"
	case KindTooLarge:
		return "too large"
	case KindCanceled:
		return "canceled"
	case KindFailed:
		return "failed"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

var (
	// ErrMode is the cause of a KindInvalid error for a fitting mode other
	// than Fit, Stretch, or Crop to Fill.
	ErrMode = errors.New("the fitting mode is not Fit, Stretch, or Crop to Fill")
	// ErrTargetDimension is the cause of a KindInvalid error for a target
	// width or height outside the preset range, preset.MinDimension to
	// preset.MaxDimension, written as the preset editor writes it.
	ErrTargetDimension = errors.New("the target width and height must each be between 1 and 16,384 pixels")
	// ErrTargetArea is the cause of a KindInvalid error for a target of more
	// than the preset pixel limit, preset.MaxResizeMegapixels, written as the
	// preset editor writes it.
	ErrTargetArea = errors.New("the target must not exceed 100 megapixels")
	// ErrEmptySource is the cause of a KindInvalid error for a source
	// without pixels.
	ErrEmptySource = errors.New("the source has no pixels")
	// ErrUnexpected is the cause of a KindFailed error. The error wraps it
	// with the panic's value, and wraps that value too when it is an error,
	// such as a runtime.Error.
	ErrUnexpected = errors.New("unexpected failure")
	// ErrSourceDimension is the cause of a KindTooLarge error: a source
	// wider or taller than MaxSourceDimension.
	ErrSourceDimension = errors.New("the source is wider or taller than 1,048,576 pixels")
)

// Error describes a failed resize.
type Error struct {
	Kind Kind
	Err  error
}

func (e *Error) Error() string {
	if e.Kind == KindCanceled {
		return fmt.Sprintf("resize canceled: %v", e.Err)
	}
	return fmt.Sprintf("resize: %v", e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// Target is what a resize produces: an Image Resizer preset's dimensions and
// fitting mode.
type Target struct {
	Width, Height int
	Mode          preset.ResizeMode
}

// Validate checks the target against the Image Resizer preset rules
// (PRESET-006): 1 to 16,384 pixels per axis, a known fitting mode, and at
// most 100 megapixels. It applies the preset library's own checks in the
// order the preset library applies them to a saved preset, so a target and
// a saved preset cannot be judged differently, and one that breaks several
// rules is reported for the same one.
func (t Target) Validate() error {
	if !preset.ValidDimension(t.Width) || !preset.ValidDimension(t.Height) {
		return &Error{Kind: KindInvalid, Err: ErrTargetDimension}
	}
	if !preset.ValidResizeMode(t.Mode) {
		return &Error{Kind: KindInvalid, Err: ErrMode}
	}
	if !preset.WithinResizePixelLimit(t.Width, t.Height) {
		return &Error{Kind: KindInvalid, Err: ErrTargetArea}
	}
	return nil
}

// Geometry places the output relative to the source.
type Geometry struct {
	// Width and Height are the output dimensions.
	Width, Height int
	// ScaledWidth and ScaledHeight are the dimensions the whole source is
	// scaled to. They equal Width and Height except under Crop to Fill,
	// where the axis that overflows the target is longer: up to
	// MaxSourceDimension × preset.MaxDimension = 2^34 pixels.
	ScaledWidth, ScaledHeight int
	// Crop is the region of the scaled source kept as the output: all of it
	// except under Crop to Fill. Its Min is the number of scaled columns
	// removed from the left and rows removed from the top.
	Crop image.Rectangle
}

// Plan computes the output geometry for a source of the given upright
// dimensions (RSZ-018); the package documentation describes each fitting
// mode. It validates the target first, then the source.
func Plan(sourceWidth, sourceHeight int, target Target) (Geometry, error) {
	if err := target.Validate(); err != nil {
		return Geometry{}, err
	}
	if sourceWidth <= 0 || sourceHeight <= 0 {
		return Geometry{}, &Error{Kind: KindInvalid, Err: ErrEmptySource}
	}
	if sourceWidth > MaxSourceDimension || sourceHeight > MaxSourceDimension {
		return Geometry{}, &Error{Kind: KindTooLarge, Err: ErrSourceDimension}
	}
	// Comparing the cross products compares targetWidth/sourceWidth with
	// targetHeight/sourceHeight exactly. Every product fits in int64: the
	// dimensions are bounded by MaxSourceDimension and preset.MaxDimension.
	sw, sh := int64(sourceWidth), int64(sourceHeight)
	tw, th := int64(target.Width), int64(target.Height)
	widthRatioSmaller := tw*sh <= th*sw
	var width, height, scaledWidth, scaledHeight int64
	switch target.Mode {
	case preset.ResizeStretch:
		width, height = tw, th
		scaledWidth, scaledHeight = tw, th
	case preset.ResizeFit:
		// The smaller ratio applies to both axes: the axis it comes from
		// lands exactly on the target, and the other is rounded, so it can
		// fall short of the target but never exceed it.
		if widthRatioSmaller {
			width, height = tw, max(1, roundDiv(sh*tw, sw))
		} else {
			width, height = max(1, roundDiv(sw*th, sh)), th
		}
		scaledWidth, scaledHeight = width, height
	case preset.ResizeCropToFill:
		// The larger ratio applies to both axes: the axis it comes from
		// lands exactly on the target, and the other is rounded, so it can
		// overflow the target but never fall short of it.
		width, height = tw, th
		if widthRatioSmaller {
			scaledWidth, scaledHeight = roundDiv(sw*th, sh), th
		} else {
			scaledWidth, scaledHeight = tw, roundDiv(sh*tw, sw)
		}
	}
	// Integer division puts the odd pixel of an odd excess on the right or
	// bottom.
	left, top := (scaledWidth-width)/2, (scaledHeight-height)/2
	return Geometry{
		Width:        int(width),
		Height:       int(height),
		ScaledWidth:  int(scaledWidth),
		ScaledHeight: int(scaledHeight),
		Crop:         image.Rect(int(left), int(top), int(left+width), int(top+height)),
	}, nil
}

// roundDiv returns numerator/denominator rounded to the nearest integer,
// halves up, for a non-negative numerator and positive denominator.
func roundDiv(numerator, denominator int64) int64 {
	return (2*numerator + denominator) / (2 * denominator)
}

// Resize scales the source to the target with its fitting mode and returns
// a new image with bounds starting at (0, 0). The output keeps the source's
// kind of pixels: a grayscale source gives an *image.Gray, or an
// *image.Gray16 at 16 bits per sample; an opaque color source gives an
// *image.RGBA, or an *image.RGBA64 at 16 bits per channel; and a source with
// a translucent pixel among those the filter reads gives an *image.NRGBA,
// or an *image.NRGBA64. The filter reads the whole source except the
// margins Crop to Fill removes beyond the filter's reach, so translucency
// only there adds no alpha channel. The image types the decoder returns,
// and *image.NYCbCrA, are read directly. Any other type is read through At,
// and its color model decides its depth and whether it is gray: only the
// standard library's 16-bit models keep 16 bits, and its gray models are
// filtered as gray. With any other model the image is opaque if every pixel
// the filter reads is opaque as At returns it; Resize reads those pixels
// once beforehand to check, stopping at the first translucent one. The
// image's own SubImage and Opaque methods are not asked, since a type that
// embeds a standard image and overrides At inherits them from the embedded
// image. A paletted image read directly is judged by the palette entries
// its pixels index, as Paletted.Opaque judges it, without Opaque's panics
// on a palette of over 256 entries or an indexed nil entry. An index past
// the end of the palette, a nil palette entry, and a nil color from At read
// as opaque black. A panic while resizing, such as one from the At method
// of a malformed image, is returned as a KindFailed error wrapping
// ErrUnexpected rather than unwinding into the caller.
// Resizing to the source's own dimensions changes no pixel beyond its
// conversion to the output type: a straight-alpha 8-bit, grayscale, or
// opaque source comes back exactly, a premultiplied one as its straight
// color rounded to the nearest step, and a translucent 16-bit one within
// the precision of its premultiplied form — except that a fully transparent
// pixel comes back as transparent black, whatever color it stored.
func Resize(ctx context.Context, source image.Image, target Target) (resized image.Image, err error) {
	// The source is read through its own methods, which can panic on a
	// malformed image, and a defect in the resizer would panic too. Either is
	// this resize's failure, so it is contained here, as the decoder contains
	// a decoder panic, rather than taking down the caller's batch (NFR-021).
	// The stack is not kept: the error is the per-item message the batch
	// reports, and the diagnostics log records no free-form text. A runtime
	// out-of-memory failure cannot be recovered, which is why the working
	// memory is bounded and the validated target bounds the output.
	defer func() {
		if recovered := recover(); recovered != nil {
			cause := fmt.Errorf("%w: %v", ErrUnexpected, recovered)
			if panicErr, ok := recovered.(error); ok {
				cause = fmt.Errorf("%w: %w", ErrUnexpected, panicErr)
			}
			resized, err = nil, &Error{Kind: KindFailed, Err: cause}
		}
	}()
	bounds := source.Bounds()
	geometry, err := Plan(bounds.Dx(), bounds.Dy(), target)
	if err != nil {
		return nil, err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, &Error{Kind: KindCanceled, Err: ctxErr}
	}
	return resample(ctx, source, geometry, orderAuto)
}
