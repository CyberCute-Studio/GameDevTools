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
// modified (RSZ-009). A context cancels a decode at its next read of the
// source, and once more after decoding, before the orientation transform.
// The decoder's work after its last read — a progressive JPEG's final
// reconstruction and the color conversion — is not interruptible, so a
// cancelled decode of a large source can still spend that CPU time before it
// returns.
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
	"runtime/debug"
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

// MaxDecodedBytes bounds the pixel memory decoding one source may allocate.
// The estimate covers every pixel buffer the standard library decoders
// allocate, not only the decoded image: a PNG counts the decoder's pixel size
// for its color type — 1 byte for a palette or gray of up to 8 bits, 2 for
// 16-bit gray, and otherwise 4 bytes, or 8 at 16 bits per channel, which
// includes gray made transparent by a tRNS chunk — and twice that when
// Adam7-interlaced, so an 8-bit truecolor non-interlaced PNG is limited to
// 2^28 pixels — the same bound the Screenshot module applies to one capture;
// a JPEG counts its component planes at their sampling, a progressive
// frame's coefficient buffers, the converted image of an RGB or CMYK frame —
// including one a segment after its first scan switches to RGB — and the
// upright copy a reoriented source needs. Larger sources are rejected with a
// KindTooLarge error from their declared headers, before any pixel memory is
// allocated, so a crafted header cannot exhaust memory.
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
	// KindTooLarge means decoding the declared header would need more than
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

// errSignature is the cause of a KindCorrupt error for content that starts
// like PNG data but whose signature is damaged.
var errSignature = errors.New("the PNG file signature is damaged")

// Error describes a failed decode.
type Error struct {
	Kind Kind
	// Format is the detected content format; FormatUnknown when the
	// content was not recognized or could not be read.
	Format Format
	// Width and Height are the declared dimensions of a KindTooLarge
	// source, upright after its EXIF orientation like Info's; zero
	// otherwise.
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
	// Exchanging the axes is its own inverse.
	return i.Orientation.upright(i.Width, i.Height)
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

// releaseThreshold is the pixel memory a decode may allocate above which
// Inspect hands the discarded memory back before it returns.
const releaseThreshold = 64 << 20

// releaseMemory collects garbage and returns freed memory to the operating
// system; tests substitute it to observe when Inspect releases.
var releaseMemory = debug.FreeOSMemory

// Inspect fully decodes the source to validate its content (RSZ-003) and
// returns its upright dimensions, format, and alpha (RSZ-005) without
// keeping the pixels or applying the orientation transform. The pixels are
// garbage once it returns. The collector would reclaim them only when the
// heap next doubles, so back-to-back checks of large sources would hold two
// of them at once; after a large decode Inspect collects them before
// returning instead, so a caller checking sources one at a time keeps at
// most one decoded in memory (NFR-014).
func Inspect(ctx context.Context, path string) (Info, error) {
	info, _, allocated, err := load(ctx, path, false)
	if allocated >= releaseThreshold {
		releaseMemory()
	}
	return info, err
}

// Decode decodes the source and returns its pixels with the EXIF
// orientation applied, so the image is visually upright (RSZ-018).
func Decode(ctx context.Context, path string) (*Image, error) {
	info, pixels, _, err := load(ctx, path, true)
	if err != nil {
		return nil, err
	}
	return &Image{Info: info, Pixels: pixels}, nil
}

var (
	pngSignature = []byte("\x89PNG\r\n\x1a\n")
	// pngIdentifier opens the PNG signature: content that starts with it
	// but not with the whole signature is a damaged PNG.
	pngIdentifier = "\x89PNG"
	// jpegSignature is the SOI marker alone: the decoder accepts stray
	// bytes between it and the first segment, and the standard library
	// registers the JPEG format by these two bytes as well.
	jpegSignature = []byte{0xff, markerSOI}
)

// readBufferSize is the read granularity, which is also how often a decode
// observes cancellation.
const readBufferSize = 64 << 10

// header is what the scan before decoding learns: a JPEG's EXIF orientation
// and frame, or a PNG's IHDR layout.
type header struct {
	orientation Orientation
	jpeg        jpegFrame
	png         pngLayout
}

// load opens the source read-only and decodes it. Allocated is the pixel
// memory the decoders were allowed to allocate: zero when the source was
// rejected before decoding began, and the full allowance once it began,
// even if decoding then failed. It counts the upright copy only when keep
// asks for the pixels, since only then is the copy made.
func load(ctx context.Context, path string, keep bool) (info Info, pixels image.Image, allocated uint64, err error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Info{}, nil, 0, &Error{Kind: KindCanceled, Err: ctxErr}
	}
	// os.Open requests read access only: sources are never opened for
	// writing (RSZ-009).
	file, openErr := os.Open(path)
	if openErr != nil {
		return Info{}, nil, 0, &Error{Kind: KindRead, Err: openErr}
	}
	defer file.Close()
	if stat, statErr := file.Stat(); statErr != nil {
		return Info{}, nil, 0, &Error{Kind: KindRead, Err: statErr}
	} else if stat.IsDir() {
		return Info{}, nil, 0, &Error{Kind: KindRead, Err: errFolder}
	}
	return decodeSource(&sourceReader{ctx: ctx, file: file}, keep)
}

// decodeSource identifies, checks, and decodes an open source; load
// describes its results.
func decodeSource(source *sourceReader, keep bool) (info Info, pixels image.Image, allocated uint64, err error) {
	format := FormatUnknown
	// The standard library decoders are fuzzed, but a decoder panic on an
	// unforeseen input must still be contained as this source's failure
	// rather than taking down the batch (NFR-021). A runtime out-of-memory
	// condition cannot be recovered, which is why the pixel memory decoding
	// needs is bounded from the declared header before any is allocated.
	defer func() {
		if recovered := recover(); recovered != nil {
			info, pixels = Info{}, nil
			err = &Error{Kind: KindCorrupt, Format: format, Err: fmt.Errorf("decoder failure: %v", recovered)}
		}
	}()

	format, err = sniff(source)
	if err != nil {
		return Info{}, nil, 0, err
	}
	// One buffer serves the header scan, the header parse, and the decode.
	buffered := bufio.NewReaderSize(source, readBufferSize)
	if err := source.restart(buffered); err != nil {
		return Info{}, nil, 0, source.classify(format, err)
	}
	scanned := header{orientation: OrientationNormal}
	switch format {
	case FormatJPEG:
		scanned.orientation, scanned.jpeg = scanJPEG(buffered)
	case FormatPNG:
		// The PNG decoder reports dimensions whose pixel count overflows its
		// own arithmetic as an unsupported feature, so the declared size is
		// checked before it reads the header.
		if scanned.png = scanPNG(buffered); scanned.png.valid {
			layout := scanned.png
			if err := checkLimit(format, layout.width, layout.height, pngDecodeBytes(layout)); err != nil {
				return Info{}, nil, 0, err
			}
		}
	}
	// The scans treat a failed read like the end of the header, so without
	// this a transient failure would pass for a header without an
	// orientation, and the later passes would list the source as Ready with
	// dimensions that are not upright.
	if source.readErr != nil {
		return Info{}, nil, 0, source.classify(format, source.readErr)
	}
	if err := source.restart(buffered); err != nil {
		return Info{}, nil, 0, source.classify(format, err)
	}
	config, err := decodeConfig(format, buffered)
	if err != nil {
		return Info{}, nil, 0, source.classify(format, err)
	}
	if config.Width <= 0 || config.Height <= 0 {
		return Info{}, nil, 0, &Error{Kind: KindCorrupt, Format: format, Err: fmt.Errorf("invalid dimensions %d × %d", config.Width, config.Height)}
	}
	width, height := scanned.orientation.upright(config.Width, config.Height)
	need := decodeBytes(format, config, scanned)
	// The upright copy is counted whenever the orientation calls for it — by
	// Inspect too, which never makes it — so a source Inspect accepts is
	// never rejected by Decode.
	upright := uprightCopyBytes(config, scanned.orientation)
	if format == FormatJPEG && config.ColorModel == color.YCbCrModel {
		// The decoder still converts this frame to RGB if a segment
		// DecodeConfig did not read says so. Only a source that conversion
		// would push over the limit is walked to its end to settle whether
		// the decoder makes it; for any smaller one the limit holds either
		// way.
		conversion := rgbaBytes(config)
		if need+upright <= MaxDecodedBytes && need+upright+conversion > MaxDecodedBytes {
			if err := source.restart(buffered); err != nil {
				return Info{}, nil, 0, source.classify(format, err)
			}
			converts := jpegConvertsToRGB(buffered)
			if source.readErr != nil || source.ctx.Err() != nil {
				return Info{}, nil, 0, source.classify(format, source.readErr)
			}
			if converts {
				need += conversion
			}
		}
	}
	if err := checkLimit(format, width, height, need+upright); err != nil {
		return Info{}, nil, 0, err
	}
	if err := source.restart(buffered); err != nil {
		return Info{}, nil, 0, source.classify(format, err)
	}
	allocated = need
	if keep {
		allocated += upright
	}
	decoded, err := decodeImage(format, buffered)
	if err != nil {
		return Info{}, nil, allocated, source.classify(format, err)
	}
	// The decoder reads nothing after the end of the image data, so a
	// cancellation during its final reconstruction is observed here, before
	// the orientation transform makes another full pass.
	if ctxErr := source.ctx.Err(); ctxErr != nil {
		return Info{}, nil, allocated, &Error{Kind: KindCanceled, Format: format, Err: ctxErr}
	}
	bounds := decoded.Bounds()
	if bounds.Dx() != config.Width || bounds.Dy() != config.Height {
		return Info{}, nil, allocated, &Error{Kind: KindCorrupt, Format: format, Err: fmt.Errorf("decoded %d × %d pixels, but the header declares %d × %d", bounds.Dx(), bounds.Dy(), config.Width, config.Height)}
	}
	info = Info{
		Format:      format,
		Width:       width,
		Height:      height,
		Orientation: scanned.orientation,
		HasAlpha:    format == FormatPNG && modelHasAlpha(decoded.ColorModel()),
	}
	if keep {
		pixels = orient(decoded, scanned.orientation)
	}
	return info, pixels, allocated, nil
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
	case n >= len(pngIdentifier) && string(prefix[:len(pngIdentifier)]) == pngIdentifier:
		// The rest of the PNG signature exists to detect a file damaged by
		// a transfer that converted its line endings.
		return FormatPNG, &Error{Kind: KindCorrupt, Format: FormatPNG, Err: errSignature}
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
	ctx context.Context
	// file is the open source; an *os.File outside tests.
	file    io.ReadSeeker
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

// restart rewinds the file and discards what buffered holds, so the next
// pass through buffered reads the file from its first byte.
func (s *sourceReader) restart(buffered *bufio.Reader) error {
	if err := s.rewind(); err != nil {
		return err
	}
	buffered.Reset(s)
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
