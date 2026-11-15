package imageresizer

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

var ErrPersistence = errors.New("resize settings persistence failed")

type OutputFormat string

const (
	FormatKeepSource OutputFormat = "keep-source"
	FormatPNG        OutputFormat = "png"
	FormatJPEG       OutputFormat = "jpeg"
)

type CollisionPolicy string

const (
	CollisionRename    CollisionPolicy = "rename"
	CollisionSkip      CollisionPolicy = "skip"
	CollisionOverwrite CollisionPolicy = "overwrite"
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
	switch o.Format {
	case FormatKeepSource, FormatPNG, FormatJPEG:
	default:
		return errors.New(ui.ResizeFormatInvalid)
	}
	switch o.Collision {
	case CollisionRename, CollisionSkip, CollisionOverwrite:
	default:
		return errors.New(ui.CollisionInvalid)
	}
	if o.JPEGQuality < MinJPEGQuality || o.JPEGQuality > MaxJPEGQuality {
		return errors.New(ui.JPEGQualityRange)
	}
	if _, err := ParseBackground(o.Background); err != nil {
		return err
	}
	return validateOutputFolder(o.OutputFolder)
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

func validateOutputFolder(folder string) error {
	if folder == "" {
		return errors.New(ui.OutputFolderRequired)
	}
	if strings.ContainsRune(folder, 0) || strings.ContainsRune(folder, '�') {
		return errors.New(ui.OutputFolderInvalid)
	}
	if !filepath.IsAbs(folder) {
		return errors.New(ui.OutputFolderNotAbsolute)
	}
	return nil
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
