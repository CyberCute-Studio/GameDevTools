// Package decode reads Image Resizer sources. It identifies PNG and JPEG
// content by its file signature rather than its extension (RSZ-003),
// decodes it with the Go standard library decoders, applies the JPEG EXIF
// orientation so dimensions and pixels are upright (RSZ-018), and reports
// every failure as a typed *Error so corrupt, unsupported, unreadable, and
// oversized sources are identified distinctly (RSZ-004).
//
// Functions are safe to call from any goroutine and never touch the GUI;
// callers run them off the GUI thread and deliver results through the shell
// dispatcher, per TECH-005. Sources are opened read-only and are never
// modified (RSZ-009). A context cancels a decode at the next read, so a
// caller can abandon a large source promptly.
//
// Metadata other than the normalized orientation — the rest of EXIF, ICC
// profiles, comments, and timestamps — is not read or preserved.
package decode

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
)

// Format identifies decoded source content.
type Format uint8

const (
	// FormatUnknown means the content was not recognized as PNG or JPEG.
	FormatUnknown Format = iota
	FormatPNG
	FormatJPEG
)

func (f Format) String() string {
	switch f {
	case FormatPNG:
		return "PNG"
	case FormatJPEG:
		return "JPEG"
	}
	return "unknown"
}

// MaxDecodedBytes bounds the pixel memory one decoded source may need. The
// estimate counts 4 bytes per pixel, or 8 for sources with 16 bits per
// channel, so an 8-bit source is limited to 2^28 pixels — the same bound the
// Screenshot module applies to one capture. Larger sources are rejected with
// a KindTooLarge error from their declared dimensions, before any pixel
// memory is allocated, so a crafted header cannot exhaust memory.
const MaxDecodedBytes = 1 << 30

// Kind classifies a decode failure so the source list can present the
// matching status and explanation (RSZ-004).
type Kind uint8

const (
	// KindRead means the file could not be opened or read: it is missing,
	// locked, a folder, or the read failed part-way.
	KindRead Kind = iota
	// KindUnsupported means the content is not PNG or JPEG data, whatever
	// its extension, or it is PNG or JPEG data that uses a feature the
	// standard library decoder does not implement, such as arithmetic-coded
	// or 12-bit JPEG.
	KindUnsupported
	// KindCorrupt means the content is recognizably PNG or JPEG but is
	// damaged, truncated, or empty.
	KindCorrupt
	// KindTooLarge means the declared dimensions would need more than
	// MaxDecodedBytes of pixel memory.
	KindTooLarge
	// KindCanceled means the context ended before decoding finished.
	KindCanceled
)

func (k Kind) String() string {
	switch k {
	case KindRead:
		return "read"
	case KindUnsupported:
		return "unsupported"
	case KindCorrupt:
		return "corrupt"
	case KindTooLarge:
		return "too large"
	case KindCanceled:
		return "canceled"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// ErrUnrecognized is the cause of a KindUnsupported error whose content is
// neither PNG nor JPEG data.
var ErrUnrecognized = errors.New("content is not PNG or JPEG image data")

// ErrEmpty is the cause of a KindCorrupt error for an empty file.
var ErrEmpty = errors.New("the file is empty")

// errFolder is the cause of a KindRead error for a path that names a folder.
var errFolder = errors.New("the path is a folder, not a file")

// Error describes a failed decode.
type Error struct {
	Kind Kind
	// Format is the detected content format; FormatUnknown when the
	// content was not recognized or could not be read.
	Format Format
	// Width and Height are the declared dimensions of a KindTooLarge
	// source, in stored (pre-orientation) pixels; zero otherwise.
	Width, Height int
	Err           error
}

func (e *Error) Error() string {
	switch e.Kind {
	case KindRead:
		return fmt.Sprintf("read source: %v", e.Err)
	case KindTooLarge:
		return fmt.Sprintf("decode %s: %v", e.Format, e.Err)
	case KindCanceled:
		return fmt.Sprintf("decode canceled: %v", e.Err)
	}
	if e.Format == FormatUnknown {
		return fmt.Sprintf("decode source: %v", e.Err)
	}
	return fmt.Sprintf("decode %s: %v", e.Format, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// Info describes a decoded source. Width and Height are the upright
// dimensions after the EXIF orientation is applied, which are the
// dimensions every resize calculation must use (RSZ-018).
type Info struct {
	Format        Format
	Width, Height int
	// Orientation is the EXIF orientation read from a JPEG source, or
	// OrientationNormal when the source carries none. PNG sources are
	// always OrientationNormal.
	Orientation Orientation
	// HasAlpha reports whether the decoded pixels carry an alpha channel:
	// a PNG with an alpha channel, a transparency chunk, or a palette with
	// translucent entries. JPEG sources never have alpha.
	HasAlpha bool
}

// StoredSize returns the dimensions as stored in the file, before the
// orientation is applied.
func (i Info) StoredSize() (width, height int) {
	if i.Orientation.SwapsAxes() {
		return i.Height, i.Width
	}
	return i.Width, i.Height
}

// Image is a decoded source with its orientation applied.
type Image struct {
	Info
	// Pixels is upright, with bounds starting at (0, 0) and exactly
	// Width × Height pixels. A source that needs no reorientation keeps
	// the decoder's concrete image type; a reoriented JPEG is an opaque
	// *image.RGBA.
	Pixels image.Image
}

// Inspect fully decodes the source to validate its content (RSZ-003) and
// returns its upright dimensions, format, and alpha (RSZ-005) without
// keeping the pixels or applying the orientation transform.
func Inspect(ctx context.Context, path string) (Info, error) {
	info, _, err := load(ctx, path, false)
	return info, err
}

// Decode decodes the source and returns its pixels with the EXIF
// orientation applied, so the image is visually upright (RSZ-018).
func Decode(ctx context.Context, path string) (*Image, error) {
	info, pixels, err := load(ctx, path, true)
	if err != nil {
		return nil, err
	}
	return &Image{Info: info, Pixels: pixels}, nil
}

var (
	pngSignature  = []byte("\x89PNG\r\n\x1a\n")
	jpegSignature = []byte{0xff, 0xd8, 0xff}
)

// readBufferSize is the read granularity, which is also how often a decode
// observes cancellation.
const readBufferSize = 64 << 10

func load(ctx context.Context, path string, keep bool) (info Info, pixels image.Image, err error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Info{}, nil, &Error{Kind: KindCanceled, Err: ctxErr}
	}
	// os.Open requests read access only: sources are never opened for
	// writing (RSZ-009).
	file, openErr := os.Open(path)
	if openErr != nil {
		return Info{}, nil, &Error{Kind: KindRead, Err: openErr}
	}
	defer file.Close()
	if stat, statErr := file.Stat(); statErr != nil {
		return Info{}, nil, &Error{Kind: KindRead, Err: statErr}
	} else if stat.IsDir() {
		return Info{}, nil, &Error{Kind: KindRead, Err: errFolder}
	}
	source := &sourceReader{ctx: ctx, file: file}
	format := FormatUnknown
	// The standard library decoders are fuzzed, but a decoder panic on an
	// unforeseen input must still be contained as this source's failure
	// rather than taking down the batch (NFR-021). A runtime out-of-memory
	// condition cannot be recovered, which is why the declared dimensions
	// are bounded before any pixel memory is allocated.
	defer func() {
		if recovered := recover(); recovered != nil {
			info, pixels = Info{}, nil
			err = &Error{Kind: KindCorrupt, Format: format, Err: fmt.Errorf("decoder failure: %v", recovered)}
		}
	}()

	format, err = sniff(source)
	if err != nil {
		return Info{}, nil, err
	}
	orientation := OrientationNormal
	if format == FormatJPEG {
		if err := source.rewind(); err != nil {
			return Info{}, nil, source.classify(format, err)
		}
		orientation = readOrientation(bufio.NewReaderSize(source, readBufferSize))
	}
	if err := source.rewind(); err != nil {
		return Info{}, nil, source.classify(format, err)
	}
	config, err := decodeConfig(format, bufio.NewReaderSize(source, readBufferSize))
	if err != nil {
		return Info{}, nil, source.classify(format, err)
	}
	if err := checkLimit(format, config); err != nil {
		return Info{}, nil, err
	}
	if err := source.rewind(); err != nil {
		return Info{}, nil, source.classify(format, err)
	}
	decoded, err := decodeImage(format, bufio.NewReaderSize(source, readBufferSize))
	if err != nil {
		return Info{}, nil, source.classify(format, err)
	}
	bounds := decoded.Bounds()
	if bounds.Dx() != config.Width || bounds.Dy() != config.Height {
		return Info{}, nil, &Error{Kind: KindCorrupt, Format: format, Err: fmt.Errorf("decoded %d × %d pixels, but the header declares %d × %d", bounds.Dx(), bounds.Dy(), config.Width, config.Height)}
	}
	width, height := orientation.upright(config.Width, config.Height)
	info = Info{
		Format:      format,
		Width:       width,
		Height:      height,
		Orientation: orientation,
		HasAlpha:    format == FormatPNG && modelHasAlpha(decoded.ColorModel()),
	}
	if keep {
		pixels = orient(decoded, orientation)
	}
	return info, pixels, nil
}

// sniff identifies the content from its signature, so a renamed file is
// decoded as what it actually contains (RSZ-003).
func sniff(source *sourceReader) (Format, error) {
	var header [8]byte
	n, err := io.ReadFull(source, header[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return FormatUnknown, source.classify(FormatUnknown, err)
	}
	prefix := header[:n]
	switch {
	case n == 0:
		return FormatUnknown, &Error{Kind: KindCorrupt, Err: ErrEmpty}
	case n == len(pngSignature) && string(prefix) == string(pngSignature):
		return FormatPNG, nil
	case n >= len(jpegSignature) && string(prefix[:len(jpegSignature)]) == string(jpegSignature):
		return FormatJPEG, nil
	case n < len(pngSignature) && string(pngSignature[:n]) == string(prefix):
		// The file ends inside the PNG signature.
		return FormatPNG, &Error{Kind: KindCorrupt, Format: FormatPNG, Err: io.ErrUnexpectedEOF}
	case n < len(jpegSignature) && string(jpegSignature[:n]) == string(prefix):
		return FormatJPEG, &Error{Kind: KindCorrupt, Format: FormatJPEG, Err: io.ErrUnexpectedEOF}
	}
	return FormatUnknown, &Error{Kind: KindUnsupported, Err: ErrUnrecognized}
}

func decodeConfig(format Format, reader io.Reader) (image.Config, error) {
	if format == FormatPNG {
		return png.DecodeConfig(reader)
	}
	return jpeg.DecodeConfig(reader)
}

func decodeImage(format Format, reader io.Reader) (image.Image, error) {
	if format == FormatPNG {
		return png.Decode(reader)
	}
	return jpeg.Decode(reader)
}

// checkLimit rejects sources whose declared dimensions would need more than
// MaxDecodedBytes of pixel memory.
func checkLimit(format Format, config image.Config) error {
	if config.Width <= 0 || config.Height <= 0 {
		return &Error{Kind: KindCorrupt, Format: format, Err: fmt.Errorf("invalid dimensions %d × %d", config.Width, config.Height)}
	}
	perPixel := uint64(4)
	switch config.ColorModel {
	case color.RGBA64Model, color.NRGBA64Model, color.Gray16Model, color.Alpha16Model:
		perPixel = 8
	}
	if uint64(config.Width)*uint64(config.Height)*perPixel > MaxDecodedBytes {
		return &Error{
			Kind:   KindTooLarge,
			Format: format,
			Width:  config.Width,
			Height: config.Height,
			Err:    fmt.Errorf("%d × %d pixels need more than the %d MiB decoding limit", config.Width, config.Height, MaxDecodedBytes>>20),
		}
	}
	return nil
}

// modelHasAlpha reports whether PNG pixels decoded with this model carry an
// alpha channel. The PNG decoder uses the NRGBA models for alpha and
// transparency-chunk sources and the RGBA models only for opaque truecolor,
// so only the non-premultiplied models and translucent palettes count.
func modelHasAlpha(model color.Model) bool {
	switch model {
	case color.NRGBAModel, color.NRGBA64Model, color.AlphaModel, color.Alpha16Model:
		return true
	}
	if palette, ok := model.(color.Palette); ok {
		for _, entry := range palette {
			if _, _, _, alpha := entry.RGBA(); alpha != 0xffff {
				return true
			}
		}
	}
	return false
}

// sourceReader reads the source file, observes cancellation at every read,
// and remembers a genuine read failure, so a decoder error caused by the
// file system is reported as KindRead rather than as damaged content.
type sourceReader struct {
	ctx     context.Context
	file    *os.File
	readErr error
}

func (s *sourceReader) Read(buffer []byte) (int, error) {
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := s.file.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) && s.readErr == nil {
		s.readErr = err
	}
	return n, err
}

func (s *sourceReader) rewind() error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if _, err := s.file.Seek(0, io.SeekStart); err != nil {
		if s.readErr == nil {
			s.readErr = err
		}
		return err
	}
	return nil
}

// classify maps a decoder or reader failure to its Kind. Cancellation wins
// over everything, a recorded file-system failure over the decoder's view
// of it, and an explicit unsupported-feature report over damage; every
// other failure — format errors, checksum mismatches, and truncation —
// means the content is damaged.
func (s *sourceReader) classify(format Format, err error) *Error {
	if ctxErr := s.ctx.Err(); ctxErr != nil {
		return &Error{Kind: KindCanceled, Format: format, Err: ctxErr}
	}
	if s.readErr != nil {
		return &Error{Kind: KindRead, Format: format, Err: s.readErr}
	}
	var pngUnsupported png.UnsupportedError
	var jpegUnsupported jpeg.UnsupportedError
	if errors.As(err, &pngUnsupported) || errors.As(err, &jpegUnsupported) {
		return &Error{Kind: KindUnsupported, Format: format, Err: err}
	}
	return &Error{Kind: KindCorrupt, Format: format, Err: err}
}
