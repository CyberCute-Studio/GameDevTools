package imageresizer

import (
	"errors"
	"fmt"
	"image/color"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/encode"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/output"
	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

var ErrPersistence = errors.New("resize settings persistence failed")

// OutputFormat and CollisionPolicy are defined by the output planner, which
// interprets them, so the planner needs nothing from this package and the
// view can start jobs that plan their outputs.
type OutputFormat = output.Format

const (
	FormatKeepSource = output.FormatKeepSource
	FormatPNG        = output.FormatPNG
	FormatJPEG       = output.FormatJPEG
)

type CollisionPolicy = output.Collision

const (
	CollisionRename    = output.CollisionRename
	CollisionSkip      = output.CollisionSkip
	CollisionOverwrite = output.CollisionOverwrite
)

// The JPEG quality range is the encoder's, so a quality the options accept is
// one a job can encode with (RSZ-029).
const (
	MinJPEGQuality      = encode.MinQuality
	DefaultJPEGQuality  = 90
	MaxJPEGQuality      = encode.MaxQuality
	DefaultBackground   = "#FFFFFF"
	backgroundHexDigits = 6
)

type ResizeOptions struct {
	Format       OutputFormat
	Collision    CollisionPolicy
	JPEGQuality  int
	Background   string
	OutputFolder string
}

func DefaultOptions(outputFolder string) ResizeOptions {
	return ResizeOptions{
		Format:       FormatKeepSource,
		Collision:    CollisionRename,
		JPEGQuality:  DefaultJPEGQuality,
		Background:   DefaultBackground,
		OutputFolder: outputFolder,
	}
}

func (o ResizeOptions) Validate() error {
	if err := o.validateEncoding(); err != nil {
		return err
	}
	return validateOutputFolder(o.OutputFolder)
}

// validateEncoding checks every option but the output folder.
func (o ResizeOptions) validateEncoding() error {
	if !o.Format.Valid() {
		return errors.New(ui.ResizeFormatInvalid)
	}
	if !o.Collision.Valid() {
		return errors.New(ui.CollisionInvalid)
	}
	if o.JPEGQuality < MinJPEGQuality || o.JPEGQuality > MaxJPEGQuality {
		return errors.New(ui.JPEGQualityRange)
	}
	_, err := ParseBackground(o.Background)
	return err
}

// ParseJPEGQuality converts the quality field text into the supported 1–100
// range.
func ParseJPEGQuality(input string) (int, error) {
	value := strings.TrimSpace(input)
	if value == "" {
		return 0, errors.New(ui.JPEGQualityRange)
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, errors.New(ui.JPEGQualityRange)
		}
	}
	quality, err := strconv.Atoi(value)
	if err != nil || quality < MinJPEGQuality || quality > MaxJPEGQuality {
		return 0, errors.New(ui.JPEGQualityRange)
	}
	return quality, nil
}

// ParseBackground converts the background field text into its canonical
// #RRGGBB form used when transparent pixels are flattened into JPEG output
// (RSZ-028).
func ParseBackground(input string) (string, error) {
	value := strings.TrimSpace(input)
	if len(value) != backgroundHexDigits+1 || value[0] != '#' {
		return "", errors.New(ui.ResizeBackgroundInvalid)
	}
	for _, character := range value[1:] {
		switch {
		case character >= '0' && character <= '9':
		case character >= 'a' && character <= 'f':
		case character >= 'A' && character <= 'F':
		default:
			return "", errors.New(ui.ResizeBackgroundInvalid)
		}
	}
	return "#" + strings.ToUpper(value[1:]), nil
}

// BackgroundColor converts a background in the #RRGGBB form ParseBackground
// accepts into the opaque color a job flattens translucent pixels against
// in JPEG results (RSZ-028).
func BackgroundColor(background string) (color.RGBA, error) {
	value, err := ParseBackground(background)
	if err != nil {
		return color.RGBA{}, err
	}
	rgb, err := strconv.ParseUint(value[1:], 16, 32)
	if err != nil {
		return color.RGBA{}, errors.New(ui.ResizeBackgroundInvalid)
	}
	return color.RGBA{R: uint8(rgb >> 16), G: uint8(rgb >> 8), B: uint8(rgb), A: 0xff}, nil
}

// defaultFolder is the default output folder (RSZ-021) under the Pictures
// folder the system knows for the user, known, or, when that cannot be
// located (knownErr) or gives a folder the output planner's folder rules
// refuse, under the Pictures folder in the user's profile, which home
// locates. When neither gives a folder, the error says why: the known
// Pictures folder's refusal, else the profile's, else why the folders could
// not be located.
func defaultFolder(known string, knownErr error, home func() (string, error)) (string, error) {
	var refused error
	if knownErr == nil && known != "" {
		folder, err := defaultFolderIn(known)
		if err == nil {
			return folder, nil
		}
		refused = err
	}
	profile, homeErr := home()
	if homeErr == nil {
		folder, err := defaultFolderIn(filepath.Join(profile, "Pictures"))
		if err == nil {
			return folder, nil
		}
		if refused == nil {
			refused = err
		}
	}
	switch {
	case refused != nil:
		return "", refused
	case knownErr != nil:
		return "", fmt.Errorf("locate Pictures folder: %w", knownErr)
	}
	return "", fmt.Errorf("locate Pictures folder: %w", homeErr)
}

// defaultFolderIn is the default output folder under a Pictures folder, or
// why it cannot be used when the output planner's folder rules refuse it. The
// Pictures folder's path is first resolved as Windows resolves it, so one
// that ends in a period or a space, which Windows drops, gives a folder in
// the Pictures folder Windows opens for it. A default no job could plan into,
// as under a Pictures folder whose path leads through a device or holds a
// colon, is reported as unavailable, as one that cannot be located is,
// rather than selected.
func defaultFolderIn(pictures string) (string, error) {
	folder := filepath.Join(resolveFolder(pictures), "GameDevTools", "Resized")
	if err := output.ValidateFolder(folder); err != nil {
		return "", fmt.Errorf("%s: %w", folder, err)
	}
	return folder, nil
}

// validateOutputFolder applies the output planner's folder rules, so a
// folder the options accept is one a job can plan into.
func validateOutputFolder(folder string) error {
	return folderMessage(output.ValidateFolder(folder))
}

// folderMessage turns an output.ValidateFolder failure into the message the
// view shows for it.
func folderMessage(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, output.ErrFolderEmpty):
		return errors.New(ui.OutputFolderRequired)
	case errors.Is(err, output.ErrFolderNotAbsolute):
		return errors.New(ui.OutputFolderNotAbsolute)
	case errors.Is(err, output.ErrFolderRoot):
		return errors.New(ui.OutputFolderRootInvalid)
	case errors.Is(err, output.ErrFolderName):
		return errors.New(ui.OutputFolderNameInvalid)
	default:
		return errors.New(ui.OutputFolderInvalid)
	}
}

type OptionsManager struct {
	store    *OptionsStore
	settings *settings.Settings
	options  ResizeOptions
	// replaced is the stored output folder that the default folder replaced
	// when the options were loaded, because only earlier builds accepted it
	// and jobs refuse it, or "" when no stored folder was replaced.
	replaced string
}

func NewOptionsManager(store *OptionsStore, value *settings.Settings) (*OptionsManager, error) {
	if store == nil {
		return nil, errors.New("resize options store is required")
	}
	if value == nil {
		return nil, errors.New("settings value is required")
	}
	options, replaced, err := store.Load(*value)
	if err != nil {
		return nil, err
	}
	return &OptionsManager{store: store, settings: value, options: options, replaced: replaced}, nil
}

func (m *OptionsManager) Options() ResizeOptions {
	return m.options
}

// FolderWarning returns the one startup warning about the output folder, or
// "" when there is none. defaultErr is why the default folder could not be
// resolved, or nil when it was. When the options were loaded with a stored
// folder that only earlier builds accepted, the warning names that folder,
// so the user learns why the folder changed before the next save forgets
// the old one, and says the default folder is selected in its place, or,
// when the default folder could not be resolved, that it is unavailable too,
// and why, so no folder is selected; without a replaced folder it says why
// the default folder is unavailable when no folder is selected. When no
// folder is selected but defaultErr is nil, as when the store was given no
// default folder and no error, the warning says the default folder's
// location is unknown rather than formatting a nil error. The user is asked
// to choose a folder once, whatever happened.
func (m *OptionsManager) FolderWarning(defaultErr error) string {
	if m.options.OutputFolder == "" && defaultErr == nil {
		defaultErr = errors.New(ui.ResizeDefaultFolderUnknown)
	}
	switch {
	case m.replaced != "" && m.options.OutputFolder != "":
		return fmt.Sprintf(ui.ResizeFolderReplaced, m.replaced)
	case m.replaced != "":
		return fmt.Sprintf(ui.ResizeFolderRefused, m.replaced, defaultErr)
	case m.options.OutputFolder == "":
		return fmt.Sprintf(ui.ResizeFolderUnavailable, defaultErr)
	}
	return ""
}

func (m *OptionsManager) SetFormat(format OutputFormat) error {
	updated := m.options
	updated.Format = format
	if err := updated.Validate(); err != nil {
		return err
	}
	m.options = updated
	return m.save()
}

func (m *OptionsManager) SetCollision(policy CollisionPolicy) error {
	updated := m.options
	updated.Collision = policy
	if err := updated.Validate(); err != nil {
		return err
	}
	m.options = updated
	return m.save()
}

func (m *OptionsManager) SetJPEGQuality(quality int) error {
	updated := m.options
	updated.JPEGQuality = quality
	if err := updated.Validate(); err != nil {
		return err
	}
	m.options = updated
	return m.save()
}

func (m *OptionsManager) SetBackground(background string) error {
	updated := m.options
	updated.Background = background
	if err := updated.Validate(); err != nil {
		return err
	}
	m.options = updated
	return m.save()
}

func (m *OptionsManager) SetOutputFolder(folder string) error {
	updated := m.options
	updated.OutputFolder = folder
	if err := updated.Validate(); err != nil {
		return err
	}
	m.options = updated
	return m.save()
}

// Reload replaces the options with those the shared settings value stores,
// or the defaults where it stores none, as restoring the default settings
// leaves them (SET-005), and forgets a stored folder the default folder
// replaced at startup unless the value still stores it. Nothing is saved:
// the restore saved the value.
func (m *OptionsManager) Reload() error {
	options, replaced, err := m.store.Load(*m.settings)
	if err != nil {
		return err
	}
	m.options, m.replaced = options, replaced
	return nil
}

func (m *OptionsManager) save() error {
	if err := m.store.Save(m.settings, m.options); err != nil {
		return fmt.Errorf("%w: %w", ErrPersistence, err)
	}
	return nil
}
