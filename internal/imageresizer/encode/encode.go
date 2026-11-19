// Package encode writes a resized Image Resizer result as PNG or JPEG.
//
// PNG output is lossless and preserves alpha (RSZ-028): the standard library
// encoder stores each image type the resizer produces at its own depth and
// kind — grayscale, opaque color, or color with alpha, at 8 or 16 bits per
// channel — and writes an alpha channel whenever a pixel is translucent. An
// image whose every pixel is opaque is stored without one, which loses no
// transparency.
//
// JPEG output is lossy and has no alpha channel, so a translucent pixel is
// flattened against the job's background color, which defaults to white
// (RSZ-028): its color is blended with the background in proportion to its
// alpha, so a fully transparent pixel becomes the background and an opaque
// one keeps its color. 16-bit samples are rounded to the nearest 8-bit value,
// and the quality is the job's 1–100 setting (RSZ-029). A grayscale result,
// which has no alpha, stays a one-component grayscale JPEG. A result with
// alpha is flattened in color, as the background may be any color, so a gray
// source with transparency, which the decoder and the resizer carry as color
// with alpha, gives a color JPEG. The blend and the rounding are integer
// arithmetic, so the same image and options give the same bytes on every
// platform.
//
// No metadata is written: no EXIF, so no orientation tag either, as the
// pixels are already upright (RSZ-018), and no ICC profile, comments, or
// timestamps (PRD 10.6).
//
// Encode takes ownership of the image it is given. To keep memory bounded
// (NFR-014) — a 100-megapixel result occupies up to 800 MB — JPEG output
// flattens and narrows the pixels in the image's own memory rather than in a
// copy, so the image must not be used once Encode returns. PNG output does not
// change the image.
//
// Functions are safe to call from any goroutine and never touch the GUI;
// callers run them off the GUI thread (TECH-005). A panic while encoding, such
// as one a malformed image raises, is returned as a KindFailed error rather
// than unwinding into the caller (NFR-021).
package encode

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/decode"
)

const (
	// MinQuality and MaxQuality bound the JPEG quality setting (RSZ-029).
	MinQuality = 1
	MaxQuality = 100
	// MaxJPEGDimension is the widest and tallest image a JPEG can hold.
	MaxJPEGDimension = 1<<16 - 1
)

// Kind classifies an encode failure.
type Kind uint8

const (
	// KindInvalid means the options or the image cannot be encoded: an
	// unsupported format, a JPEG quality outside 1–100, a background that is
	// not opaque, an image without pixels, or one too large for JPEG.
	KindInvalid Kind = iota
	// KindWrite means writing the encoded bytes failed. The error wraps the
	// writer's own error.
	KindWrite
	// KindFailed means the encoder failed for a reason of its own, or
	// panicked, which a malformed image or a defect in the encoder can cause
	// and which cannot be told apart. A panic wraps ErrUnexpected.
	KindFailed
)

func (k Kind) String() string {
	switch k {
	case KindInvalid:
		return "invalid"
	case KindWrite:
		return "write"
	case KindFailed:
		return "failed"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

var (
	// ErrFormat is the cause of a KindInvalid error for a format other than
	// PNG or JPEG.
	ErrFormat = errors.New("the output format is not PNG or JPEG")
	// ErrQuality is the cause of a KindInvalid error for a JPEG quality
	// outside 1–100.
	ErrQuality = errors.New("the JPEG quality must be between 1 and 100")
	// ErrBackground is the cause of a KindInvalid error for a JPEG
	// background color that is not opaque.
	ErrBackground = errors.New("the JPEG background color must be opaque")
	// ErrEmptyImage is the cause of a KindInvalid error for an image without
	// pixels.
	ErrEmptyImage = errors.New("the image has no pixels")
	// ErrTooLargeForJPEG is the cause of a KindInvalid error for an image
	// wider or taller than a JPEG can hold.
	ErrTooLargeForJPEG = fmt.Errorf("the image is wider or taller than the %d pixels a JPEG can hold", MaxJPEGDimension)
	// ErrUnexpected is the cause of a KindFailed error for a panic. The error
	// wraps it with the panic's value, and wraps that value too when it is an
	// error.
	ErrUnexpected = errors.New("unexpected failure")
)

// Error describes a failed encode.
type Error struct {
	Kind Kind
	// Format is the format being encoded.
	Format decode.Format
	Err    error
}

func (e *Error) Error() string {
	return fmt.Sprintf("encode %s: %v", e.Format, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// Options are what one result is encoded with.
type Options struct {
	// Format is decode.FormatPNG or decode.FormatJPEG.
	Format decode.Format
	// Quality is the JPEG quality, from MinQuality to MaxQuality. PNG output
	// ignores it.
	Quality int
	// Background is the opaque color JPEG output flattens translucent pixels
	// against. PNG output ignores it.
	Background color.RGBA
}

// Validate checks the options: the format, and for JPEG the quality and the
// background.
func (o Options) Validate() error {
	switch o.Format {
	case decode.FormatPNG:
		return nil
	case decode.FormatJPEG:
	default:
		return &Error{Kind: KindInvalid, Format: o.Format, Err: ErrFormat}
	}
	if o.Quality < MinQuality || o.Quality > MaxQuality {
		return &Error{Kind: KindInvalid, Format: o.Format, Err: ErrQuality}
	}
	if o.Background.A != 0xff {
		return &Error{Kind: KindInvalid, Format: o.Format, Err: ErrBackground}
	}
	return nil
}

// Encode writes img to w in the options' format. It takes ownership of img:
// JPEG output converts img's pixels in place, so img must not be used
// afterwards. A failure of w is returned as a KindWrite error wrapping w's
// own error.
func Encode(w io.Writer, img image.Image, options Options) (err error) {
	if err := options.Validate(); err != nil {
		return err
	}
	tracked := &trackedWriter{w: w}
	// The image is read through its own methods and slices, its Bounds
	// included, which a malformed image makes panic, and a defect in the
	// conversion or the encoder would panic too. Either is this result's
	// failure, contained here as the decoder and the resizer contain theirs
	// (NFR-021). A failure of w that the encoder met first is still reported
	// as the write failure it is.
	defer func() {
		recovered := recover()
		switch {
		case tracked.err != nil:
			err = &Error{Kind: KindWrite, Format: options.Format, Err: tracked.err}
		case recovered != nil:
			cause := fmt.Errorf("%w: %v", ErrUnexpected, recovered)
			if panicErr, ok := recovered.(error); ok {
				cause = fmt.Errorf("%w: %w", ErrUnexpected, panicErr)
			}
			err = &Error{Kind: KindFailed, Format: options.Format, Err: cause}
		}
	}()
	if img == nil || img.Bounds().Empty() {
		return &Error{Kind: KindInvalid, Format: options.Format, Err: ErrEmptyImage}
	}
	bounds := img.Bounds()
	if options.Format == decode.FormatJPEG && (bounds.Dx() > MaxJPEGDimension || bounds.Dy() > MaxJPEGDimension) {
		return &Error{Kind: KindInvalid, Format: options.Format, Err: ErrTooLargeForJPEG}
	}
	var encodeErr error
	if options.Format == decode.FormatPNG {
		encodeErr = png.Encode(tracked, img)
	} else {
		encodeErr = jpeg.Encode(tracked, jpegPixels(img, options.Background), &jpeg.Options{Quality: options.Quality})
	}
	if encodeErr != nil {
		return &Error{Kind: KindFailed, Format: options.Format, Err: encodeErr}
	}
	return nil
}

// trackedWriter remembers the first failure of the writer it wraps, so a
// write failure is reported as one whatever the encoder makes of it.
type trackedWriter struct {
	w   io.Writer
	err error
}

func (t *trackedWriter) Write(p []byte) (int, error) {
	n, err := t.w.Write(p)
	if err != nil && t.err == nil {
		t.err = err
	}
	return n, err
}

// jpegPixels returns img's pixels in a form the JPEG encoder stores without a
// conversion of its own: *image.Gray for grayscale, and an opaque
// *image.RGBA for color, whose rows the encoder reads directly. The image
// types the resizer produces are converted in their own memory: translucent
// pixels are flattened against the background and 16-bit samples rounded to
// 8 bits, and a 16-bit image's narrower rows are packed into the start of
// its pixel slice. A *image.YCbCr is stored as it is. Any other type is read
// through At into a new *image.RGBA.
func jpegPixels(img image.Image, background color.RGBA) image.Image {
	switch m := img.(type) {
	case *image.Gray, *image.YCbCr:
		return m
	case *image.Gray16:
		return narrowGray16(m)
	case *image.RGBA:
		return flattenRGBA(m, background)
	case *image.RGBA64:
		return narrowRGBA64(m, background)
	case *image.NRGBA:
		return flattenNRGBA(m, background)
	case *image.NRGBA64:
		return narrowNRGBA64(m, background)
	}
	return flattenColors(img, background)
}

// narrow8 rounds a 16-bit sample to the nearest 8-bit one.
func narrow8(sample uint32) uint8 {
	return uint8((sample*0xff + 0x7fff) / 0xffff)
}

// blend8 flattens an 8-bit straight-alpha sample against a background sample:
// value × alpha + background × (1 − alpha), rounded to the nearest step.
func blend8(value, background uint8, alpha uint32) uint8 {
	return uint8((uint32(value)*alpha + uint32(background)*(0xff-alpha) + 0x7f) / 0xff)
}

// over8 flattens an 8-bit premultiplied sample against a background sample:
// value + background × (1 − alpha), rounded to the nearest step and clamped,
// since a malformed premultiplied sample may exceed its alpha.
func over8(value, background uint8, alpha uint32) uint8 {
	return uint8(min((uint32(value)*0xff+uint32(background)*(0xff-alpha)+0x7f)/0xff, 0xff))
}

// wide is 0xffff², the scale of a 16-bit sample multiplied by a 16-bit alpha.
const wide = 0xffff * 0xffff

// blend16 flattens a 16-bit straight-alpha sample against an 8-bit background
// sample and rounds the result to the nearest 8-bit value in one step.
func blend16(value uint32, background uint8, alpha uint32) uint8 {
	sum := uint64(value)*uint64(alpha) + uint64(background)*0x101*uint64(0xffff-alpha)
	return uint8((sum*0xff + wide/2) / wide)
}

// over16 flattens a 16-bit premultiplied sample against an 8-bit background
// sample and rounds the result to the nearest 8-bit value in one step,
// clamped, since a malformed premultiplied sample may exceed its alpha.
func over16(value uint32, background uint8, alpha uint32) uint8 {
	sum := uint64(value)*0xffff + uint64(background)*0x101*uint64(0xffff-alpha)
	return uint8(min((sum*0xff+wide/2)/wide, 0xff))
}

func sample16(p []uint8) uint32 { return uint32(p[0])<<8 | uint32(p[1]) }

// narrowGray16 rounds each sample to 8 bits, packing the rows into the start
// of the image's pixel slice. Each packed row starts no later than the 16-bit
// row it is read from, and each sample is written no later than its own
// bytes, so nothing is overwritten before it is read.
func narrowGray16(m *image.Gray16) *image.Gray {
	width, height := m.Rect.Dx(), m.Rect.Dy()
	for y := range height {
		row := m.Pix[y*m.Stride : y*m.Stride+2*width]
		packed := m.Pix[y*width : y*width+width]
		for x := range width {
			packed[x] = narrow8(sample16(row[2*x:]))
		}
	}
	return &image.Gray{Pix: m.Pix[:width*height], Stride: width, Rect: m.Rect}
}

// flattenRGBA flattens the translucent pixels of a premultiplied image in
// place.
func flattenRGBA(m *image.RGBA, background color.RGBA) *image.RGBA {
	width, height := m.Rect.Dx(), m.Rect.Dy()
	for y := range height {
		row := m.Pix[y*m.Stride : y*m.Stride+4*width]
		for i := 0; i < len(row); i += 4 {
			alpha := uint32(row[i+3])
			if alpha == 0xff {
				continue
			}
			row[i] = over8(row[i], background.R, alpha)
			row[i+1] = over8(row[i+1], background.G, alpha)
			row[i+2] = over8(row[i+2], background.B, alpha)
			row[i+3] = 0xff
		}
	}
	return m
}

// flattenNRGBA flattens the translucent pixels of a straight-alpha image in
// place. Once every pixel is opaque, its straight and premultiplied forms are
// the same bytes, so the image is returned as an *image.RGBA over the same
// memory.
func flattenNRGBA(m *image.NRGBA, background color.RGBA) *image.RGBA {
	width, height := m.Rect.Dx(), m.Rect.Dy()
	for y := range height {
		row := m.Pix[y*m.Stride : y*m.Stride+4*width]
		for i := 0; i < len(row); i += 4 {
			alpha := uint32(row[i+3])
			if alpha == 0xff {
				continue
			}
			row[i] = blend8(row[i], background.R, alpha)
			row[i+1] = blend8(row[i+1], background.G, alpha)
			row[i+2] = blend8(row[i+2], background.B, alpha)
			row[i+3] = 0xff
		}
	}
	return &image.RGBA{Pix: m.Pix, Stride: m.Stride, Rect: m.Rect}
}

// narrowRGBA64 flattens a 16-bit premultiplied image and rounds it to 8 bits,
// packing the rows into the start of its pixel slice as narrowGray16 does.
func narrowRGBA64(m *image.RGBA64, background color.RGBA) *image.RGBA {
	width, height := m.Rect.Dx(), m.Rect.Dy()
	for y := range height {
		row := m.Pix[y*m.Stride : y*m.Stride+8*width]
		packed := m.Pix[y*4*width : (y+1)*4*width]
		for x := range width {
			p := row[8*x:]
			alpha := sample16(p[6:])
			// The samples are read before any byte of the pixel is written:
			// the packed pixel overlaps its own first bytes.
			r, g, b := sample16(p), sample16(p[2:]), sample16(p[4:])
			q := packed[4*x:]
			q[0] = over16(r, background.R, alpha)
			q[1] = over16(g, background.G, alpha)
			q[2] = over16(b, background.B, alpha)
			q[3] = 0xff
		}
	}
	return &image.RGBA{Pix: m.Pix[:4*width*height], Stride: 4 * width, Rect: m.Rect}
}

// narrowNRGBA64 flattens a 16-bit straight-alpha image and rounds it to 8
// bits, packing the rows into the start of its pixel slice as narrowGray16
// does.
func narrowNRGBA64(m *image.NRGBA64, background color.RGBA) *image.RGBA {
	width, height := m.Rect.Dx(), m.Rect.Dy()
	for y := range height {
		row := m.Pix[y*m.Stride : y*m.Stride+8*width]
		packed := m.Pix[y*4*width : (y+1)*4*width]
		for x := range width {
			p := row[8*x:]
			alpha := sample16(p[6:])
			r, g, b := sample16(p), sample16(p[2:]), sample16(p[4:])
			q := packed[4*x:]
			q[0] = blend16(r, background.R, alpha)
			q[1] = blend16(g, background.G, alpha)
			q[2] = blend16(b, background.B, alpha)
			q[3] = 0xff
		}
	}
	return &image.RGBA{Pix: m.Pix[:4*width*height], Stride: 4 * width, Rect: m.Rect}
}

// flattenColors reads an image of any other type through At into a new
// opaque *image.RGBA, flattening its premultiplied colors against the
// background. The resizer never produces such an image.
func flattenColors(img image.Image, background color.RGBA) *image.RGBA {
	bounds := img.Bounds()
	flat := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			var r, g, b, a uint32
			if c := img.At(x, y); c != nil {
				r, g, b, a = c.RGBA()
			} else {
				a = 0xffff
			}
			p := flat.Pix[flat.PixOffset(x, y):]
			p[0] = over16(r, background.R, a)
			p[1] = over16(g, background.G, a)
			p[2] = over16(b, background.B, a)
			p[3] = 0xff
		}
	}
	return flat
}
