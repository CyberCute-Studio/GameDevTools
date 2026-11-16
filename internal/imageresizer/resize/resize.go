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
// lend no color to their neighbors, and the stored sRGB values are filtered
// directly, as most image tools do, rather than converted to linear light.
// The filter's overshoot near sharp edges is clamped. Crop to Fill resamples
// only the cropped region, applying exactly the filter weights that scaling
// the whole source and cropping afterwards would.
//
// Results are deterministic (RSZ-017): the same source and target produce
// bit-identical output on every platform and in every run. Sample positions
// are exact rational numbers, the filter weights are computed with float64
// operations kept from fusing into platform-dependent multiply-adds and are
// quantized to fixed point, and the pixels are filtered in integer
// arithmetic on one goroutine.
//
// Resize allocates the output image plus working memory that does not grow
// with the source's area. The weight tables grow with the source's
// dimensions, which MaxSourceDimension bounds, and the filter streams the
// source a row at a time, so the working memory stays under 128 MiB in the
// worst case and is a few megabytes for typical sources. Resize reads the
// source but never retains or modifies it, and callers run it off the GUI
// thread (TECH-005); a context cancels it between source rows.
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
)

func (k Kind) String() string {
	switch k {
	case KindInvalid:
		return "invalid"
	case KindTooLarge:
		return "too large"
	case KindCanceled:
		return "canceled"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

var (
	// ErrMode is the cause of a KindInvalid error for a fitting mode other
	// than Fit, Stretch, or Crop to Fill.
	ErrMode = errors.New("the fitting mode is not Fit, Stretch, or Crop to Fill")
	// ErrTargetDimension is the cause of a KindInvalid error for a target
	// width or height outside the preset range.
	ErrTargetDimension = fmt.Errorf("the target width and height must each be between %d and %d pixels", preset.MinDimension, preset.MaxDimension)
	// ErrTargetArea is the cause of a KindInvalid error for a target of more
	// than the preset pixel limit.
	ErrTargetArea = fmt.Errorf("the target must not exceed %d megapixels", preset.MaxResizeMegapixels)
	// ErrEmptySource is the cause of a KindInvalid error for a source
	// without pixels.
	ErrEmptySource = errors.New("the source has no pixels")
	// ErrSourceDimension is the cause of a KindTooLarge error.
	ErrSourceDimension = fmt.Errorf("the source is wider or taller than %d pixels", MaxSourceDimension)
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
// (PRESET-006): a known fitting mode, 1 to 16,384 pixels per axis, and at
// most 100 megapixels.
func (t Target) Validate() error {
	switch t.Mode {
	case preset.ResizeFit, preset.ResizeStretch, preset.ResizeCropToFill:
	default:
		return &Error{Kind: KindInvalid, Err: ErrMode}
	}
	if t.Width < preset.MinDimension || t.Width > preset.MaxDimension || t.Height < preset.MinDimension || t.Height > preset.MaxDimension {
		return &Error{Kind: KindInvalid, Err: ErrTargetDimension}
	}
	if int64(t.Width)*int64(t.Height) > preset.MaxResizeMegapixels*1_000_000 {
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
	// where the axis that overflows the target is longer.
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
// any translucent pixel gives an *image.NRGBA, or an *image.NRGBA64.
// Sources outside the standard library's image types are read through At.
// Resizing to the source's own dimensions changes no pixel beyond its
// conversion to the output type: a straight-alpha 8-bit, grayscale, or
// opaque source comes back exactly, a premultiplied one as its straight
// color rounded to the nearest step, and a translucent 16-bit one within
// the precision of its premultiplied form.
func Resize(ctx context.Context, source image.Image, target Target) (image.Image, error) {
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
