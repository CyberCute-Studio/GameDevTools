package imageresizer

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

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

const (
	MinJPEGQuality      = 1
	DefaultJPEGQuality  = 90
	MaxJPEGQuality      = 100
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

// validateOutputFolder applies the output planner's folder rules, so a
// folder the options accept is one a job can plan into.
func validateOutputFolder(folder string) error {
	switch err := output.ValidateFolder(folder); {
	case err == nil:
		return nil
	case errors.Is(err, output.ErrFolderEmpty):
		return errors.New(ui.OutputFolderRequired)
	case errors.Is(err, output.ErrFolderNotAbsolute):
		return errors.New(ui.OutputFolderNotAbsolute)
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
}

func NewOptionsManager(store *OptionsStore, value *settings.Settings) (*OptionsManager, error) {
	if store == nil {
		return nil, errors.New("resize options store is required")
	}
	if value == nil {
		return nil, errors.New("settings value is required")
	}
	options, err := store.Load(*value)
	if err != nil {
		return nil, err
	}
	return &OptionsManager{store: store, settings: value, options: options}, nil
}

func (m *OptionsManager) Options() ResizeOptions {
	return m.options
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

func (m *OptionsManager) save() error {
	if err := m.store.Save(m.settings, m.options); err != nil {
		return fmt.Errorf("%w: %w", ErrPersistence, err)
	}
	return nil
}
