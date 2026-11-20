package preset

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	"github.com/CyberCute-Studio/GameDevTools/internal/shell"
)

const (
	CurrentVersion = 1
	sectionKey     = "presets"
)

type storedPresets struct {
	Version    int      `json:"version"`
	Custom     []Preset `json:"custom"`
	SelectedID string   `json:"selectedPresetId,omitempty"`
}

type Repository struct {
	module shell.ModuleID
	store  *settings.Store
}

// RegisterValidators registers both modules' preset library validators with
// the store, for a store that loads its file before, or without, building a
// repository for each module, as the module tests' restarted stores do. The
// application builds both repositories before it loads the file, and
// NewRepository registers each one's validator.
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
	stored, err := decodeSection(r.module, value.Modules[string(r.module)])
	if err != nil {
		return nil, err
	}
	library, err := NewLibrary(r.module, stored.Custom)
	if err != nil {
		return nil, err
	}
	if stored.SelectedID != "" {
		if err := library.Select(stored.SelectedID); err != nil {
			return nil, fmt.Errorf("validate selected preset: %w", err)
		}
	}
	return library, nil
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
	if _, exists := library.Get(library.selectedID); !exists {
		return fmt.Errorf("validate selected preset: %w", ErrNotFound)
	}
	section, err := encodeSection(r.module, value.Modules[string(r.module)], library.custom, library.selectedID)
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

func validateStoredUnicode(data []byte) error {
	if !utf8.Valid(data) {
		return errors.New("stored presets must contain valid UTF-8")
	}
	inString := false
	for index := 0; index < len(data); index++ {
		switch data[index] {
		case '"':
			inString = !inString
		case '\\':
			if !inString {
				continue
			}
			if index+1 >= len(data) {
				return errors.New("stored presets contain an incomplete escape")
			}
			if data[index+1] != 'u' {
				index++
				continue
			}
			if index+5 >= len(data) {
				return errors.New("stored presets contain an incomplete Unicode escape")
			}
			code, ok := parseHex4(data[index+2 : index+6])
			if !ok {
				return errors.New("stored presets contain an invalid Unicode escape")
			}
			if code >= 0xdc00 && code <= 0xdfff {
				return errors.New("stored presets contain an unpaired UTF-16 surrogate")
			}
			if code >= 0xd800 && code <= 0xdbff {
				if index+11 >= len(data) || data[index+6] != '\\' || data[index+7] != 'u' {
					return errors.New("stored presets contain an unpaired UTF-16 surrogate")
				}
				low, ok := parseHex4(data[index+8 : index+12])
				if !ok || low < 0xdc00 || low > 0xdfff {
					return errors.New("stored presets contain an unpaired UTF-16 surrogate")
				}
				index += 11
				continue
			}
			index += 5
		}
	}
	return nil
}

func parseHex4(data []byte) (uint16, bool) {
	if len(data) != 4 {
		return 0, false
	}
	var value uint16
	for _, character := range data {
		value <<= 4
		switch {
		case character >= '0' && character <= '9':
			value |= uint16(character - '0')
		case character >= 'a' && character <= 'f':
			value |= uint16(character-'a') + 10
		case character >= 'A' && character <= 'F':
			value |= uint16(character-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}

func decodeSection(module shell.ModuleID, section json.RawMessage) (storedPresets, error) {
	empty := storedPresets{Version: CurrentVersion, Custom: []Preset{}}
	if len(section) == 0 {
		return empty, nil
	}
	fields, err := decodeSectionFields(section)
	if err != nil {
		return storedPresets{}, err
	}
	data, exists := fields[sectionKey]
	if !exists {
		return empty, nil
	}
	if err := validateStoredUnicode(data); err != nil {
		return storedPresets{}, err
	}
	storedFields, err := decodeObjectFields(data)
	if err != nil {
		return storedPresets{}, fmt.Errorf("decode stored preset fields: %w", err)
	}
	for field := range storedFields {
		switch field {
		case "version", "custom", "selectedPresetId":
		default:
			return storedPresets{}, fmt.Errorf("unknown stored preset field %q", field)
		}
	}
	if custom, exists := storedFields["custom"]; exists {
		if err := validateStoredPresetFields(custom); err != nil {
			return storedPresets{}, fmt.Errorf("decode stored custom presets: %w", err)
		}
	}
	var stored storedPresets
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&stored); err != nil {
		return storedPresets{}, fmt.Errorf("decode stored presets: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return storedPresets{}, errors.New("stored presets contain trailing JSON values")
	}
	if stored.Version != CurrentVersion {
		return storedPresets{}, fmt.Errorf("unsupported preset version %d", stored.Version)
	}
	if stored.Custom == nil {
		return storedPresets{}, errors.New("stored custom presets must be an array")
	}
	_, hasSelectedID := storedFields["selectedPresetId"]
	if hasSelectedID && stored.SelectedID == "" {
		return storedPresets{}, errors.New("stored selectedPresetId must be a non-empty string when present")
	}
	library, err := NewLibrary(module, stored.Custom)
	if err != nil {
		return storedPresets{}, fmt.Errorf("validate stored presets: %w", err)
	}
	if stored.SelectedID != "" {
		if err := library.Select(stored.SelectedID); err != nil {
			return storedPresets{}, fmt.Errorf("validate selected preset: %w", err)
		}
	}
	stored.Custom = library.Custom()
	return stored, nil
}

func validateStoredPresetFields(data json.RawMessage) error {
	var records []json.RawMessage
	if err := json.Unmarshal(data, &records); err != nil {
		return err
	}
	for _, record := range records {
		fields, err := decodeObjectFields(record)
		if err != nil {
			return err
		}
		for field := range fields {
			switch field {
			case "id", "name", "width", "height", "resizeMode":
			default:
				return fmt.Errorf("unknown custom preset field %q", field)
			}
		}
	}
	return nil
}

func encodeSection(module shell.ModuleID, section json.RawMessage, custom []Preset, selectedID string) (json.RawMessage, error) {
	fields, err := decodeSectionFields(section)
	if err != nil {
		return nil, err
	}
	library, err := NewLibrary(module, custom)
	if err != nil {
		return nil, fmt.Errorf("validate presets for storage: %w", err)
	}
	if err := library.Select(selectedID); err != nil {
		return nil, fmt.Errorf("validate selected preset: %w", err)
	}
	data, err := json.Marshal(storedPresets{Version: CurrentVersion, Custom: library.Custom(), SelectedID: selectedID})
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

// LibrarySection returns a module settings section that holds only the
// section's preset library — its custom presets and the module's selected
// preset — as stored, or nil when the section holds none. Restoring the
// default settings keeps it and resets the rest of the section (SET-005):
// custom presets are deleted only by their explicit Delete.
func LibrarySection(section json.RawMessage) (json.RawMessage, error) {
	fields, err := decodeSectionFields(section)
	if err != nil {
		return nil, err
	}
	library, exists := fields[sectionKey]
	if !exists {
		return nil, nil
	}
	kept, err := json.Marshal(map[string]json.RawMessage{sectionKey: library})
	if err != nil {
		return nil, fmt.Errorf("encode module preset section: %w", err)
	}
	return kept, nil
}

// KeptSection returns what restoring the default settings keeps of replaced,
// the module's section in the settings the restore replaces (SET-005): only
// its preset library, with its selected preset, as LibrarySection returns
// it, joined by the custom presets of session, the module's section in the
// running session's settings, that replaced does not hold, or nil when
// neither holds a library. The two differ when the
// session started from the defaults because the settings file could not be
// read and the restore keeps the file's presets: the presets the session
// created meanwhile, which could not be saved, are kept with them, as a
// custom preset is deleted only by its explicit Delete. One whose name a
// kept preset already uses is kept under the name Duplicate would give a
// copy of it; the selected preset stays replaced's.
func KeptSection(module shell.ModuleID, replaced, session json.RawMessage) (json.RawMessage, error) {
	kept, err := LibrarySection(replaced)
	if err != nil {
		return nil, err
	}
	running, err := decodeSection(module, session)
	if err != nil {
		return nil, fmt.Errorf("decode the session's presets: %w", err)
	}
	if len(running.Custom) == 0 {
		return kept, nil
	}
	stored, err := decodeSection(module, replaced)
	if err != nil {
		return nil, err
	}
	library, err := NewLibrary(module, stored.Custom)
	if err != nil {
		return nil, err
	}
	selected := stored.SelectedID
	if selected == "" {
		selected = library.SelectedID()
	}
	added := false
	for _, candidate := range running.Custom {
		if _, exists := library.Get(candidate.ID); exists {
			continue
		}
		if err := library.adopt(candidate); err != nil {
			return nil, fmt.Errorf("keep the session's preset %q: %w", candidate.Name, err)
		}
		added = true
	}
	if !added {
		return kept, nil
	}
	return encodeSection(module, nil, library.custom, selected)
}

func decodeSectionFields(section json.RawMessage) (map[string]json.RawMessage, error) {
	if len(section) == 0 {
		return make(map[string]json.RawMessage), nil
	}
	fields, err := decodeObjectFields(section)
	if err != nil {
		return nil, fmt.Errorf("decode module preset section: %w", err)
	}
	return fields, nil
}

func decodeObjectFields(data json.RawMessage) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, errors.New("preset data must be a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, ok := token.(string)
		if !ok {
			return nil, errors.New("preset object field name must be a string")
		}
		if _, exists := fields[name]; exists {
			return nil, fmt.Errorf("duplicate preset object field %q", name)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		fields[name] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("preset object contains trailing JSON values")
	}
	return fields, nil
}
