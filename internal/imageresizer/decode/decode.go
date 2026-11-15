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
// modified (RSZ-009). Each pass over a source — the header scan, the header
// parse, and the decode — sees exactly the bytes the passes before it
// checked, so a file rewritten during its check fails with ErrChanged instead
// of being decoded from a header that was never checked. A context cancels a
// decode at its next read of the source, and once more after decoding,
// before the orientation transform.
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
	"hash/maphash"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"runtime/debug"
	"runtime/metrics"
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
// allocated, so a crafted header cannot exhaust memory; and since the decode
// pass must read the same bytes the check read, a header rewritten after the
// check cannot either.
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

// ErrChanged is the cause of a KindRead error for a file whose content
// changed between two passes of its decode.
var ErrChanged = errors.New("the file changed while it was being read")

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
	// the decoder's concrete image type; a reoriented grayscale JPEG is an
	// *image.Gray, and any other reoriented JPEG an opaque *image.RGBA.
	Pixels image.Image
}

// releaseThreshold is the memory a decode must have allocated for Inspect to
// hand the discarded memory back before it returns.
const releaseThreshold = 64 << 20

// releaseMemory collects garbage and returns freed memory to the operating
// system; tests substitute it to observe when Inspect releases.
var releaseMemory = debug.FreeOSMemory

// heapAllocsMetric is the runtime's running total of bytes allocated on the
// heap.
const heapAllocsMetric = "/gc/heap/allocs:bytes"

// heapAllocated returns the bytes allocated on the heap since the process
// started, or 0 if the runtime does not report them.
func heapAllocated() uint64 {
	sample := []metrics.Sample{{Name: heapAllocsMetric}}
	metrics.Read(sample)
	if sample[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return sample[0].Value.Uint64()
}

// Inspect fully decodes the source to validate its content (RSZ-003) and
// returns its upright dimensions, format, and alpha (RSZ-005) without
// keeping the pixels or applying the orientation transform. The pixels are
// garbage once it returns. The collector would reclaim them only when the
// heap next doubles, so back-to-back checks of large sources would hold two
// of them at once; when the decode actually allocated a large amount —
// whether it then succeeded or failed — Inspect collects it before returning
// instead, so a caller checking sources one at a time keeps at most one
// decoded in memory (NFR-014). A source rejected from its header, or that
// fails before the decoder allocates its pixels, skips the collection.
func Inspect(ctx context.Context, path string) (Info, error) {
	before := heapAllocated()
	info, _, err := load(ctx, path, false)
	if heapAllocated()-before >= releaseThreshold {
		releaseMemory()
	}
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
// observes cancellation, and the size of the segments each pass checks
// against the passes before it.
const readBufferSize = 64 << 10

// header is what the scan before decoding learns: a JPEG's EXIF orientation
// and frame, or a PNG's IHDR layout.
type header struct {
	orientation Orientation
	jpeg        jpegFrame
	png         pngLayout
}

// load opens the source read-only and decodes it. It applies the orientation
// transform and returns the pixels only when keep asks for them.
func load(ctx context.Context, path string, keep bool) (Info, image.Image, error) {
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
	return decodeSource(newSourceReader(ctx, file), keep)
}

// decodeSource identifies, checks, and decodes an open source; load
// describes its results.
func decodeSource(source *sourceReader, keep bool) (info Info, pixels image.Image, err error) {
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

	// One buffer serves every pass: the signature sniff and the header scan
	// that follows it, the header parse, and the decode.
	buffered := bufio.NewReaderSize(source, readBufferSize)
	format, err = sniff(source, buffered)
	if err != nil {
		return Info{}, nil, err
	}
	scanned := header{orientation: OrientationNormal}
	switch format {
	case FormatJPEG:
		scanned.orientation, scanned.jpeg = scanJPEG(buffered)
	case FormatPNG:
		scanned.png = scanPNG(buffered)
	}
	// The scans treat a failed read like the end of the header. Without this
	// a transient failure would pass for a JPEG header without an
	// orientation, so the later passes would list the source as Ready with
	// dimensions that are not upright, and a PNG scan cut short assumes a
	// transparency chunk, which could misreport the source as too large.
	if source.readErr != nil || source.ctx.Err() != nil {
		return Info{}, nil, source.classify(format, source.readErr)
	}
	if format == FormatPNG && scanned.png.valid {
		// The PNG decoder reports dimensions whose pixel count overflows its
		// own arithmetic as an unsupported feature, so the declared size is
		// checked before it reads the header.
		layout := scanned.png
		if err := checkLimit(format, layout.width, layout.height, pngDecodeBytes(layout)); err != nil {
			return Info{}, nil, err
		}
	}
	if err := source.restart(buffered); err != nil {
		return Info{}, nil, source.classify(format, err)
	}
	config, err := decodeConfig(format, buffered)
	if err != nil {
		return Info{}, nil, source.classify(format, err)
	}
	if config.Width <= 0 || config.Height <= 0 {
		return Info{}, nil, &Error{Kind: KindCorrupt, Format: format, Err: fmt.Errorf("invalid dimensions %d × %d", config.Width, config.Height)}
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
				return Info{}, nil, source.classify(format, err)
			}
			// A walk that ends before the end-of-image marker met data the
			// decoder fails on too, so the source is damaged, not too large.
			converts, walkErr := jpegConvertsToRGB(buffered)
			if walkErr != nil || source.readErr != nil || source.ctx.Err() != nil {
				return Info{}, nil, source.classify(format, walkErr)
			}
			if converts {
				need += conversion
			}
		}
	}
	// For a PNG this repeats the check above, and is the only one for a PNG
	// whose IHDR the scan could not read.
	if err := checkLimit(format, width, height, need+upright); err != nil {
		return Info{}, nil, err
	}
	if err := source.restart(buffered); err != nil {
		return Info{}, nil, source.classify(format, err)
	}
	decoded, err := decodeImage(format, buffered)
	if err != nil {
		return Info{}, nil, source.classify(format, err)
	}
	// The decoder reads nothing after the end of the image data, so a
	// cancellation during its final reconstruction is observed here, before
	// the orientation transform makes another full pass.
	if ctxErr := source.ctx.Err(); ctxErr != nil {
		return Info{}, nil, &Error{Kind: KindCanceled, Format: format, Err: ctxErr}
	}
	bounds := decoded.Bounds()
	if bounds.Dx() != config.Width || bounds.Dy() != config.Height {
		return Info{}, nil, &Error{Kind: KindCorrupt, Format: format, Err: fmt.Errorf("decoded %d × %d pixels, but the header declares %d × %d", bounds.Dx(), bounds.Dy(), config.Width, config.Height)}
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
	return info, pixels, nil
}

// sniff identifies the content from its signature, so a renamed file is
// decoded as what it actually contains (RSZ-003). It only peeks at the
// signature, so the header scan goes on to read from the first byte.
func sniff(source *sourceReader, buffered *bufio.Reader) (Format, error) {
	prefix, err := buffered.Peek(len(pngSignature))
	if err != nil && !errors.Is(err, io.EOF) {
		return FormatUnknown, source.classify(FormatUnknown, err)
	}
	n := len(prefix)
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

// sourceReader reads the source file for every pass of a decode. It observes
// cancellation at every read and remembers the first genuine read failure,
// so a decoder error caused by the file system is reported as KindRead
// rather than as damaged content, and every later read fails with it.
//
// Each pass checks the header the next one relies on, so the passes must
// see the same bytes: the decoder would otherwise allocate for a header
// rewritten after the limit was checked. The reader therefore reads the file
// in fixed segments, and hands out a segment an earlier pass also read only
// after checking it against that pass's hash of it; a segment that differs
// fails the decode with ErrChanged before any of its bytes reach a parser.
// The hash is seeded randomly for each reader, so its values cannot be
// predicted from the file content.
type sourceReader struct {
	ctx context.Context
	// file is the open source; an *os.File outside tests.
	file    io.ReadSeeker
	readErr error

	seed maphash.Seed
	// sums holds the hash of every segment a pass has read, in file order.
	sums []uint64
	// segment is the one being served, read into buffer; served counts its
	// bytes already handed out, and index is the position of the next
	// segment in the file. ended reports that segment reached the end of
	// the file.
	buffer  []byte
	segment []byte
	served  int
	index   int
	ended   bool
}

func newSourceReader(ctx context.Context, file io.ReadSeeker) *sourceReader {
	return &sourceReader{ctx: ctx, file: file, seed: maphash.MakeSeed()}
}

func (s *sourceReader) Read(buffer []byte) (int, error) {
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	if s.readErr != nil {
		return 0, s.readErr
	}
	if s.served == len(s.segment) {
		if s.ended {
			return 0, io.EOF
		}
		if err := s.next(); err != nil {
			return 0, err
		}
		if len(s.segment) == 0 {
			return 0, io.EOF
		}
	}
	n := copy(buffer, s.segment[s.served:])
	s.served += n
	return n, nil
}

// next reads the following segment of the file and checks it against what
// an earlier pass read there, or records it for the passes after this one.
func (s *sourceReader) next() error {
	if s.buffer == nil {
		s.buffer = make([]byte, readBufferSize)
	}
	n, err := io.ReadFull(s.file, s.buffer)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		s.readErr = err
		return err
	}
	s.segment, s.served, s.ended = s.buffer[:n], 0, n < len(s.buffer)
	sum := maphash.Bytes(s.seed, s.segment)
	if s.index == len(s.sums) {
		s.sums = append(s.sums, sum)
	} else if s.sums[s.index] != sum {
		s.readErr = ErrChanged
		return ErrChanged
	}
	s.index++
	return nil
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
	s.segment, s.served, s.index, s.ended = nil, 0, 0, false
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
