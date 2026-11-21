// Package output delivers finished screenshot captures to the user's two
// destinations: the Windows clipboard and a PNG or JPEG file in the
// configured output folder.
//
// Deliver always attempts both destinations and reports them independently
// (SCR-060): a clipboard failure never discards the saved file, and a save
// failure never discards a successful clipboard copy. The clipboard is
// served first and always receives the original captured pixels, never the
// output of JPEG encoding (SCR-062).
//
// Files are named Screenshot_yyyyMMdd_HHmmssfff with the format's
// extension (SCR-064). An existing file is never overwritten: a numeric
// suffix is appended until an unused name is found (SCR-065). Every file
// is encoded into a temporary file inside the destination folder and moved
// to its final name only after encoding succeeds, so a failed write cannot
// leave a seemingly valid partial image behind (SCR-066, NFR-023).
//
// Failures are reported as *Error values with a Kind, so callers can
// distinguish validation, folder, encoding, write, and clipboard failures
// per UX-010.
//
// All functions are safe to call from any goroutine. They never touch the
// GUI thread, so callers run them off the GUI thread and deliver results
// through the shell dispatcher, per TECH-005.
package output

import (
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"time"

	"github.com/CyberCute-Studio/GameDevTools/internal/pathform"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot"
)

// timestampLayout renders the date and time portion of the SCR-064
// filename pattern Screenshot_yyyyMMdd_HHmmssfff in local time. The
// millisecond portion is appended separately: Go's fractional-second
// layout verbs require a decimal point the pattern does not have.
const timestampLayout = "20060102_150405"

// maxSuffixAttempts bounds the collision search so an unexpected repeated
// filesystem failure cannot spin forever.
const maxSuffixAttempts = 9999

// Kind classifies an output failure so callers can present the right
// recovery path per UX-010.
type Kind int

const (
	// KindValidation means the request itself is invalid: a nil or empty
	// image, or capture options that fail validation.
	KindValidation Kind = iota
	// KindFolder means the output folder could not be created or used.
	KindFolder
	// KindEncode means the image could not be encoded as PNG or JPEG.
	KindEncode
	// KindWrite means writing, syncing, or finalizing the file failed.
	KindWrite
	// KindClipboard means placing the image on the Windows clipboard
	// failed.
	KindClipboard
)

func (k Kind) String() string {
	switch k {
	case KindValidation:
		return "validation"
	case KindFolder:
		return "folder"
	case KindEncode:
		return "encode"
	case KindWrite:
		return "write"
	case KindClipboard:
		return "clipboard"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Error describes a failed output operation.
type Error struct {
	Kind Kind
	Op   string
	Err  error
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %v", e.Op, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

func kindError(kind Kind, op string, err error) *Error {
	return &Error{Kind: kind, Op: op, Err: err}
}

// Result reports the two independent outcomes of one delivery (SCR-060).
// A nil Clipboard or Save field means that destination succeeded. Path is
// the final saved file. It is set whenever the file was fully written and
// finalized, including when Save reports a non-nil error from cleaning up
// the temporary file afterward.
type Result struct {
	Path      string
	Clipboard error
	Save      error
}

// OK reports whether both destinations succeeded.
func (r Result) OK() bool {
	return r.Clipboard == nil && r.Save == nil
}

// Deliver copies img to the Windows clipboard and saves it as PNG or JPEG
// in the configured output folder. The clipboard is served first with the
// original captured pixels, so JPEG encoding can never degrade the
// clipboard image (SCR-062). Both destinations are always attempted: the
// failure of one never suppresses the other (SCR-060).
//
// An invalid image or invalid options fail both destinations with a
// KindValidation error without touching either one.
func Deliver(img *image.RGBA, options screenshot.CaptureOptions) Result {
	if err := validateRequest(img, options); err != nil {
		return Result{Clipboard: err, Save: err}
	}
	result := Result{}
	if err := setClipboardImage(0, img); err != nil {
		result.Clipboard = err
	}
	path, err := save(img, options, time.Now())
	result.Path = path
	if err != nil {
		result.Save = err
	}
	return result
}

func validateRequest(img *image.RGBA, options screenshot.CaptureOptions) error {
	if img == nil || img.Bounds().Empty() {
		return kindError(KindValidation, "validate image",
			errors.New("the captured image is empty"))
	}
	if err := options.Validate(); err != nil {
		return kindError(KindValidation, "validate capture options", err)
	}
	return nil
}

// encode writes img to writer in the requested format using the configured
// JPEG quality (SCR-061, SCR-067).
func encode(writer io.Writer, img *image.RGBA, options screenshot.CaptureOptions) error {
	switch options.Format {
	case screenshot.FormatPNG:
		if err := png.Encode(writer, img); err != nil {
			return kindError(KindEncode, "encode PNG", err)
		}
	case screenshot.FormatJPEG:
		if err := jpeg.Encode(writer, img, &jpeg.Options{Quality: options.JPEGQuality}); err != nil {
			return kindError(KindEncode, "encode JPEG", err)
		}
	default:
		return kindError(KindValidation, "encode",
			fmt.Errorf("unsupported image format %q", options.Format))
	}
	return nil
}

// save encodes img into a temporary file inside the destination folder and
// finalizes it under a collision-free timestamped name. The final name is
// claimed with a hard link, which the filesystem refuses to create over an
// existing file, so a save racing another writer can never silently
// overwrite it (SCR-065). On any failure the temporary file is removed, so
// no partial image remains at a final or temporary path (SCR-066).
func save(img *image.RGBA, options screenshot.CaptureOptions, now time.Time) (string, error) {
	return saveWithEncoder(img, options, now, encode)
}

// saveWithEncoder is save with the encoder injectable, so tests can
// exercise encode failures and partial-write cleanup.
func saveWithEncoder(img *image.RGBA, options screenshot.CaptureOptions, now time.Time,
	encoder func(io.Writer, *image.RGBA, screenshot.CaptureOptions) error,
) (string, error) {
	folder := options.OutputFolder
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return "", kindError(KindFolder, "create output folder", err)
	}
	info, err := os.Stat(folder)
	if err != nil {
		return "", kindError(KindFolder, "open output folder", err)
	}
	if !info.IsDir() {
		return "", kindError(KindFolder, "open output folder",
			fmt.Errorf("%s is not a folder", folder))
	}
	temporary, err := os.CreateTemp(folder, ".gamedevtools-*.tmp")
	if err != nil {
		return "", kindError(KindWrite, "create temporary file", err)
	}
	temporaryPath := temporary.Name()
	// The final name is claimed only after encoding succeeded; every return
	// before then must remove the temporary file.
	succeeded := false
	defer func() {
		if !succeeded {
			os.Remove(temporaryPath)
		}
	}()
	if err := encoder(temporary, img, options); err != nil {
		temporary.Close()
		return "", err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return "", kindError(KindWrite, "flush temporary file", err)
	}
	if err := temporary.Close(); err != nil {
		return "", kindError(KindWrite, "close temporary file", err)
	}
	base := fmt.Sprintf("Screenshot_%s%03d", now.Format(timestampLayout), now.Nanosecond()/int(time.Millisecond))
	for suffix := 1; suffix <= maxSuffixAttempts; suffix++ {
		name := base
		if suffix > 1 {
			name = fmt.Sprintf("%s_%d", base, suffix)
		}
		final := pathform.Join(folder, name+options.Format.Extension())
		if err := os.Link(temporaryPath, final); err != nil {
			if os.IsExist(err) {
				continue
			}
			return "", kindError(KindWrite, "finalize saved file", err)
		}
		if err := os.Remove(temporaryPath); err != nil {
			// The final file already exists and is complete; an orphaned
			// temporary file is reported without hiding the successful
			// save.
			succeeded = true
			return final, kindError(KindWrite, "remove temporary file", err)
		}
		succeeded = true
		return final, nil
	}
	return "", kindError(KindWrite, "choose filename",
		fmt.Errorf("no unused filename found after %d attempts for %s", maxSuffixAttempts,
			base+options.Format.Extension()))
}
