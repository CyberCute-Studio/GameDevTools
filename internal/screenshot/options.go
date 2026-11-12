package screenshot

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

var ErrPersistence = errors.New("capture settings persistence failed")

type CaptureMode string

const (
	ModeFreeRectangle   CaptureMode = "free-rectangle"
	ModePresetRectangle CaptureMode = "preset-rectangle"
	ModeFullDisplay     CaptureMode = "full-display"
)

type ImageFormat string

const (
	FormatPNG  ImageFormat = "png"
	FormatJPEG ImageFormat = "jpeg"
)

const (
	MinJPEGQuality         = 1
	DefaultJPEGQuality     = 90
	MaxJPEGQuality         = 100
	DefaultFilenamePattern = "Screenshot_yyyyMMdd_HHmmssfff"
)

func (f ImageFormat) Extension() string {
	if f == FormatJPEG {
		return ".jpg"
	}
	return ".png"
}

type CaptureOptions struct {
	Mode         CaptureMode
	Format       ImageFormat
	JPEGQuality  int
	OutputFolder string
}

func DefaultOptions(outputFolder string) CaptureOptions {
	return CaptureOptions{
		Mode:         ModeFreeRectangle,
		Format:       FormatPNG,
		JPEGQuality:  DefaultJPEGQuality,
		OutputFolder: outputFolder,
	}
}

func (o CaptureOptions) Validate() error {
	switch o.Mode {
	case ModeFreeRectangle, ModePresetRectangle, ModeFullDisplay:
	default:
		return errors.New(ui.CaptureModeInvalid)
	}
	switch o.Format {
	case FormatPNG, FormatJPEG:
	default:
		return errors.New(ui.ImageFormatInvalid)
	}
	if o.JPEGQuality < MinJPEGQuality || o.JPEGQuality > MaxJPEGQuality {
		return errors.New(ui.JPEGQualityRange)
	}
	return validateOutputFolder(o.OutputFolder)
}

func (o CaptureOptions) FilenameExample() string {
	return DefaultFilenamePattern + o.Format.Extension()
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
	options  CaptureOptions
}

func NewOptionsManager(store *OptionsStore, value *settings.Settings) (*OptionsManager, error) {
	if store == nil {
		return nil, errors.New("capture options store is required")
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

func (m *OptionsManager) Options() CaptureOptions {
	return m.options
}

func (m *OptionsManager) SetMode(mode CaptureMode) error {
	updated := m.options
	updated.Mode = mode
	if err := updated.Validate(); err != nil {
		return err
	}
	m.options = updated
	return m.save()
}

func (m *OptionsManager) SetFormat(format ImageFormat) error {
	updated := m.options
	updated.Format = format
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
