package preset

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	"github.com/CyberCute-Studio/GameDevTools/internal/shell"
)

const (
	CurrentVersion = 1
	sectionKey     = "presets"
)

type storedPresets struct {
	Version int      `json:"version"`
	Custom  []Preset `json:"custom"`
}

type Repository struct {
	module shell.ModuleID
	store  *settings.Store
}

func RegisterValidators(store *settings.Store) {
	for _, module := range []shell.ModuleID{shell.ModuleScreenshot, shell.ModuleImageResizer} {
		store.RegisterModuleValidator(string(module), validatorFor(module))
	}
}

func NewRepository(module shell.ModuleID, store *settings.Store) (*Repository, error) {
	if _, err := BuiltIns(module); err != nil {
		return nil, err
	}
	if store == nil {
		return nil, errors.New("preset settings store is required")
	}
	repository := &Repository{module: module, store: store}
	store.RegisterModuleValidator(string(module), validatorFor(module))
	return repository, nil
}

func (r *Repository) Load(value settings.Settings) (*Library, error) {
	custom, err := decodeSection(r.module, value.Modules[string(r.module)])
	if err != nil {
		return nil, err
	}
	return NewLibrary(r.module, custom)
}

func (r *Repository) Save(value *settings.Settings, library *Library) error {
	if value == nil {
		return errors.New("settings value is required")
	}
	if library == nil {
		return errors.New("preset library is required")
	}
	if library.module != r.module {
		return fmt.Errorf("preset library module %q does not match repository module %q", library.module, r.module)
	}
	if _, err := NewLibrary(r.module, library.custom); err != nil {
		return fmt.Errorf("validate preset library: %w", err)
	}
	section, err := encodeSection(r.module, value.Modules[string(r.module)], library.custom)
	if err != nil {
		return err
	}
	if value.Modules == nil {
		value.Modules = make(map[string]json.RawMessage)
	}
	value.Modules[string(r.module)] = section
	return r.store.Save(*value)
}

func validatorFor(module shell.ModuleID) settings.ModuleValidator {
	return func(section json.RawMessage) error {
		_, err := decodeSection(module, section)
		return err
	}
}

func decodeSection(module shell.ModuleID, section json.RawMessage) ([]Preset, error) {
	if len(section) == 0 {
		return []Preset{}, nil
	}
	fields, err := decodeSectionFields(section)
	if err != nil {
		return nil, err
	}
	data, exists := fields[sectionKey]
	if !exists {
		return []Preset{}, nil
	}
	var stored storedPresets
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&stored); err != nil {
		return nil, fmt.Errorf("decode stored presets: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("stored presets contain trailing JSON values")
	}
	if stored.Version != CurrentVersion {
		return nil, fmt.Errorf("unsupported preset version %d", stored.Version)
	}
	if stored.Custom == nil {
		return nil, errors.New("stored custom presets must be an array")
	}
	library, err := NewLibrary(module, stored.Custom)
	if err != nil {
		return nil, fmt.Errorf("validate stored presets: %w", err)
	}
	return library.Custom(), nil
}

func encodeSection(module shell.ModuleID, section json.RawMessage, custom []Preset) (json.RawMessage, error) {
	fields, err := decodeSectionFields(section)
	if err != nil {
		return nil, err
	}
	library, err := NewLibrary(module, custom)
	if err != nil {
		return nil, fmt.Errorf("validate presets for storage: %w", err)
	}
	data, err := json.Marshal(storedPresets{Version: CurrentVersion, Custom: library.Custom()})
	if err != nil {
		return nil, fmt.Errorf("encode stored presets: %w", err)
	}
	fields[sectionKey] = data
	section, err = json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("encode module preset section: %w", err)
	}
	return section, nil
}

func decodeSectionFields(section json.RawMessage) (map[string]json.RawMessage, error) {
	fields := make(map[string]json.RawMessage)
	if len(section) == 0 {
		return fields, nil
	}
	trimmed := bytes.TrimSpace(section)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, errors.New("module preset section must be a JSON object")
	}
	if err := json.Unmarshal(section, &fields); err != nil {
		return nil, fmt.Errorf("decode module preset section: %w", err)
	}
	return fields, nil
}
