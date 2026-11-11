package preset

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/CyberCute-Studio/GameDevTools/internal/shell"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

const (
	MinDimension        = 1
	MaxDimension        = 16384
	MaxResizeMegapixels = 100
)

type ResizeMode string

const (
	ResizeFit        ResizeMode = "fit"
	ResizeStretch    ResizeMode = "stretch"
	ResizeCropToFill ResizeMode = "crop-to-fill"
)

var (
	ErrBuiltIn       = errors.New("built-in presets cannot be edited or deleted")
	ErrNotFound      = errors.New("preset not found")
	ErrDuplicateName = errors.New("preset name already exists in this module")
)

type Preset struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Width      int        `json:"width"`
	Height     int        `json:"height"`
	ResizeMode ResizeMode `json:"resizeMode,omitempty"`
	BuiltIn    bool       `json:"-"`
}

type Library struct {
	module     shell.ModuleID
	builtIns   []Preset
	custom     []Preset
	selectedID string
}

func BuiltIns(module shell.ModuleID) ([]Preset, error) {
	var presets []Preset
	switch module {
	case shell.ModuleScreenshot:
		presets = []Preset{
			{ID: "builtin.screenshot.hd", Name: "HD", Width: 1280, Height: 720, BuiltIn: true},
			{ID: "builtin.screenshot.full-hd", Name: "Full HD", Width: 1920, Height: 1080, BuiltIn: true},
			{ID: "builtin.screenshot.qhd", Name: "QHD", Width: 2560, Height: 1440, BuiltIn: true},
			{ID: "builtin.screenshot.4k-uhd", Name: "4K UHD", Width: 3840, Height: 2160, BuiltIn: true},
		}
	case shell.ModuleImageResizer:
		presets = []Preset{
			{ID: "builtin.image-resizer.16x16", Name: "16 × 16", Width: 16, Height: 16, ResizeMode: ResizeFit, BuiltIn: true},
			{ID: "builtin.image-resizer.32x32", Name: "32 × 32", Width: 32, Height: 32, ResizeMode: ResizeFit, BuiltIn: true},
			{ID: "builtin.image-resizer.64x64", Name: "64 × 64", Width: 64, Height: 64, ResizeMode: ResizeFit, BuiltIn: true},
			{ID: "builtin.image-resizer.128x128", Name: "128 × 128", Width: 128, Height: 128, ResizeMode: ResizeFit, BuiltIn: true},
			{ID: "builtin.image-resizer.256x256", Name: "256 × 256", Width: 256, Height: 256, ResizeMode: ResizeFit, BuiltIn: true},
			{ID: "builtin.image-resizer.512x512", Name: "512 × 512", Width: 512, Height: 512, ResizeMode: ResizeFit, BuiltIn: true},
			{ID: "builtin.image-resizer.1024x1024", Name: "1024 × 1024", Width: 1024, Height: 1024, ResizeMode: ResizeFit, BuiltIn: true},
			{ID: "builtin.image-resizer.1920x1080", Name: "1920 × 1080", Width: 1920, Height: 1080, ResizeMode: ResizeFit, BuiltIn: true},
		}
	default:
		return nil, fmt.Errorf("unsupported preset module %q", module)
	}
	return clonePresets(presets), nil
}

func NewLibrary(module shell.ModuleID, custom []Preset) (*Library, error) {
	builtIns, err := BuiltIns(module)
	if err != nil {
		return nil, err
	}
	library := &Library{module: module, builtIns: builtIns, custom: make([]Preset, 0, len(custom)), selectedID: builtIns[0].ID}
	for _, candidate := range custom {
		if candidate.BuiltIn {
			return nil, fmt.Errorf("custom preset %q is marked built-in", candidate.ID)
		}
		validated, err := validatePreset(module, candidate)
		if err != nil {
			return nil, err
		}
		if err := library.checkUnique(validated, ""); err != nil {
			return nil, err
		}
		library.custom = append(library.custom, validated)
	}
	return library, nil
}

func (l *Library) Module() shell.ModuleID {
	return l.module
}

func (l *Library) Presets() []Preset {
	return clonePresets(append(append(make([]Preset, 0, len(l.builtIns)+len(l.custom)), l.builtIns...), l.custom...))
}

func (l *Library) Custom() []Preset {
	return clonePresets(l.custom)
}

func (l *Library) Get(id string) (Preset, bool) {
	for _, candidate := range l.Presets() {
		if candidate.ID == id {
			return candidate, true
		}
	}
	return Preset{}, false
}

func (l *Library) SelectedID() string {
	return l.selectedID
}

func (l *Library) Select(id string) error {
	if _, exists := l.Get(id); !exists {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	l.selectedID = id
	return nil
}

func (l *Library) Create(name string, width, height int, mode ResizeMode) (Preset, error) {
	id, err := newCustomID()
	if err != nil {
		return Preset{}, err
	}
	candidate := Preset{ID: id, Name: name, Width: width, Height: height, ResizeMode: mode}
	validated, err := validatePreset(l.module, candidate)
	if err != nil {
		return Preset{}, err
	}
	if err := l.checkUnique(validated, ""); err != nil {
		return Preset{}, err
	}
	l.custom = append(l.custom, validated)
	return validated, nil
}

func (l *Library) Update(id, name string, width, height int, mode ResizeMode) error {
	index, err := l.customIndex(id)
	if err != nil {
		return err
	}
	candidate := Preset{ID: id, Name: name, Width: width, Height: height, ResizeMode: mode}
	validated, err := validatePreset(l.module, candidate)
	if err != nil {
		return err
	}
	if err := l.checkUnique(validated, id); err != nil {
		return err
	}
	l.custom[index] = validated
	return nil
}

func (l *Library) Rename(id, name string) error {
	current, exists := l.Get(id)
	if !exists {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	return l.Update(id, name, current.Width, current.Height, current.ResizeMode)
}

func (l *Library) Duplicate(id string) (Preset, error) {
	source, exists := l.Get(id)
	if !exists {
		return Preset{}, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	newID, err := newCustomID()
	if err != nil {
		return Preset{}, err
	}
	candidate := source
	candidate.ID = newID
	candidate.BuiltIn = false
	for suffix := 1; ; suffix++ {
		candidate.Name = source.Name + " copy"
		if suffix > 1 {
			candidate.Name = fmt.Sprintf("%s copy %d", source.Name, suffix)
		}
		if err := l.checkUnique(candidate, ""); err == nil {
			break
		} else if !errors.Is(err, ErrDuplicateName) {
			return Preset{}, err
		}
	}
	l.custom = append(l.custom, candidate)
	return candidate, nil
}

func (l *Library) Delete(id string) error {
	index, err := l.customIndex(id)
	if err != nil {
		return err
	}
	l.custom = append(l.custom[:index], l.custom[index+1:]...)
	if l.selectedID == id {
		l.selectedID = l.builtIns[0].ID
	}
	return nil
}

func (l *Library) customIndex(id string) (int, error) {
	for _, builtIn := range l.builtIns {
		if builtIn.ID == id {
			return -1, ErrBuiltIn
		}
	}
	for index, candidate := range l.custom {
		if candidate.ID == id {
			return index, nil
		}
	}
	return -1, fmt.Errorf("%w: %q", ErrNotFound, id)
}

func (l *Library) checkUnique(candidate Preset, replacingID string) error {
	for _, existing := range l.Presets() {
		if existing.ID == candidate.ID && existing.ID != replacingID {
			return fmt.Errorf("preset identifier %q is already used in module %q", candidate.ID, l.module)
		}
		if existing.ID != replacingID && strings.EqualFold(strings.TrimSpace(existing.Name), strings.TrimSpace(candidate.Name)) {
			return fmt.Errorf("%w: %q", ErrDuplicateName, candidate.Name)
		}
	}
	return nil
}

func validatePreset(module shell.ModuleID, candidate Preset) (Preset, error) {
	if candidate.ID == "" || candidate.ID != strings.TrimSpace(candidate.ID) {
		return Preset{}, errors.New("preset identifier must be non-empty and have no surrounding whitespace")
	}
	candidate.Name = strings.TrimSpace(candidate.Name)
	if candidate.Name == "" {
		return Preset{}, errors.New(ui.PresetNameRequired)
	}
	if candidate.Width < MinDimension || candidate.Width > MaxDimension {
		return Preset{}, fmt.Errorf(ui.PresetDimensionRange, "Width")
	}
	if candidate.Height < MinDimension || candidate.Height > MaxDimension {
		return Preset{}, fmt.Errorf(ui.PresetDimensionRange, "Height")
	}
	switch module {
	case shell.ModuleScreenshot:
		if candidate.ResizeMode != "" {
			return Preset{}, errors.New(ui.PresetScreenshotResizeMode)
		}
	case shell.ModuleImageResizer:
		switch candidate.ResizeMode {
		case ResizeFit, ResizeStretch, ResizeCropToFill:
		default:
			return Preset{}, errors.New(ui.PresetResizeMode)
		}
		if int64(candidate.Width)*int64(candidate.Height) > MaxResizeMegapixels*1_000_000 {
			return Preset{}, errors.New(ui.PresetResizePixelLimit)
		}
	default:
		return Preset{}, fmt.Errorf("unsupported preset module %q", module)
	}
	return candidate, nil
}

func clonePresets(presets []Preset) []Preset {
	return append([]Preset{}, presets...)
}

func newCustomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate preset identifier: %w", err)
	}
	return "custom." + hex.EncodeToString(value[:]), nil
}
