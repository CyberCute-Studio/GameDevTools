package screenshot

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
	OptionsVersion = 1
	optionsKey     = "capture"
)

const moduleID = shell.ModuleScreenshot

type storedOptions struct {
	Version      int         `json:"version"`
	Mode         CaptureMode `json:"mode"`
	Format       ImageFormat `json:"format"`
	JPEGQuality  int         `json:"jpegQuality"`
	OutputFolder string      `json:"outputFolder"`
	Shortcut     string      `json:"shortcut"`
	// shortcut holds the parsed Shortcut text; it is filled by decodeOptions
	// and never serialized. Sections written before the field existed omit
	// the text and decode to the default shortcut; a present value must
	// parse, even an empty one.
	shortcut Shortcut
}

type OptionsStore struct {
	store         *settings.Store
	defaultFolder string
}

func NewOptionsStore(store *settings.Store, defaultFolder string) (*OptionsStore, error) {
	if store == nil {
		return nil, errors.New("capture options settings store is required")
	}
	options := &OptionsStore{store: store, defaultFolder: defaultFolder}
	store.RegisterModuleValidator(string(moduleID), validateOptionsSection)
	return options, nil
}

func (s *OptionsStore) Load(value settings.Settings) (CaptureOptions, error) {
	options := DefaultOptions(s.defaultFolder)
	stored, exists, err := decodeOptions(value.Modules[string(moduleID)])
	if err != nil {
		return CaptureOptions{}, err
	}
	if !exists {
		// The default folder is not validated here: when the Pictures folder
		// cannot be resolved the application must still start so the user can
		// choose an output folder, as the startup warning announces.
		return options, nil
	}
	options.Mode = stored.Mode
	options.Format = stored.Format
	options.JPEGQuality = stored.JPEGQuality
	options.OutputFolder = stored.OutputFolder
	options.Shortcut = stored.shortcut
	return options, options.Validate()
}

func (s *OptionsStore) Save(value *settings.Settings, options CaptureOptions) error {
	if value == nil {
		return errors.New("settings value is required")
	}
	if err := options.Validate(); err != nil {
		return err
	}
	section, err := encodeOptions(value.Modules[string(moduleID)], options)
	if err != nil {
		return err
	}
	if value.Modules == nil {
		value.Modules = make(map[string]json.RawMessage)
	}
	value.Modules[string(moduleID)] = section
	return s.store.Save(*value)
}

func validateOptionsSection(section json.RawMessage) error {
	_, _, err := decodeOptions(section)
	return err
}

func decodeOptions(section json.RawMessage) (storedOptions, bool, error) {
	if len(section) == 0 {
		return storedOptions{}, false, nil
	}
	fields, err := decodeSectionFields(section)
	if err != nil {
		return storedOptions{}, false, err
	}
	data, exists := fields[optionsKey]
	if !exists {
		return storedOptions{}, false, nil
	}
	objectFields, err := decodeObjectFields("stored capture options", data)
	if err != nil {
		return storedOptions{}, false, err
	}
	var stored storedOptions
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&stored); err != nil {
		return storedOptions{}, false, fmt.Errorf("decode stored capture options: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return storedOptions{}, false, errors.New("stored capture options contain trailing JSON values")
	}
	if stored.Version != OptionsVersion {
		return storedOptions{}, false, fmt.Errorf("unsupported capture options version %d", stored.Version)
	}
	stored.shortcut = DefaultShortcut()
	if _, present := objectFields["shortcut"]; present {
		shortcut, err := ParseShortcut(stored.Shortcut)
		if err != nil {
			return storedOptions{}, false, fmt.Errorf("validate stored capture options: %w", err)
		}
		stored.shortcut = shortcut
	}
	options := CaptureOptions{
		Mode:         stored.Mode,
		Format:       stored.Format,
		JPEGQuality:  stored.JPEGQuality,
		OutputFolder: stored.OutputFolder,
		Shortcut:     stored.shortcut,
	}
	if err := options.Validate(); err != nil {
		return storedOptions{}, false, fmt.Errorf("validate stored capture options: %w", err)
	}
	return stored, true, nil
}

func encodeOptions(section json.RawMessage, options CaptureOptions) (json.RawMessage, error) {
	fields, err := decodeSectionFields(section)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(storedOptions{
		Version:      OptionsVersion,
		Mode:         options.Mode,
		Format:       options.Format,
		JPEGQuality:  options.JPEGQuality,
		OutputFolder: options.OutputFolder,
		Shortcut:     options.Shortcut.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("encode stored capture options: %w", err)
	}
	fields[optionsKey] = data
	section, err = json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("encode screenshot settings section: %w", err)
	}
	return section, nil
}

func decodeSectionFields(section json.RawMessage) (map[string]json.RawMessage, error) {
	return decodeObjectFields("screenshot settings section", section)
}

func decodeObjectFields(object string, section json.RawMessage) (map[string]json.RawMessage, error) {
	if len(section) == 0 {
		return make(map[string]json.RawMessage), nil
	}
	trimmed := bytes.TrimSpace(section)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("%s must be a JSON object", object)
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
			return nil, fmt.Errorf("%s field name must be a string", object)
		}
		if _, exists := fields[name]; exists {
			return nil, fmt.Errorf("duplicate %s field %q", object, name)
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
		return nil, fmt.Errorf("%s contains trailing JSON values", object)
	}
	return fields, nil
}
