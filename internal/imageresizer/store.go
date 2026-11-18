package imageresizer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/output"
	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	"github.com/CyberCute-Studio/GameDevTools/internal/shell"
)

const (
	OptionsVersion = 1
	optionsKey     = "resize"
)

const moduleID = shell.ModuleImageResizer

type storedOptions struct {
	Version      int             `json:"version"`
	Format       OutputFormat    `json:"format"`
	Collision    CollisionPolicy `json:"collision"`
	JPEGQuality  int             `json:"jpegQuality"`
	Background   string          `json:"background"`
	OutputFolder string          `json:"outputFolder"`
}

type OptionsStore struct {
	store         *settings.Store
	defaultFolder string
}

func NewOptionsStore(store *settings.Store, defaultFolder string) (*OptionsStore, error) {
	if store == nil {
		return nil, errors.New("resize options settings store is required")
	}
	options := &OptionsStore{store: store, defaultFolder: defaultFolder}
	store.RegisterModuleValidator(string(moduleID), validateOptionsSection)
	return options, nil
}

func (s *OptionsStore) Load(value settings.Settings) (ResizeOptions, error) {
	options := DefaultOptions(s.defaultFolder)
	stored, exists, err := decodeOptions(value.Modules[string(moduleID)])
	if err != nil {
		return ResizeOptions{}, err
	}
	if !exists {
		// The default folder is not validated here: when the Pictures folder
		// cannot be resolved the application must still start so the user can
		// choose an output folder, as the startup warning announces.
		return options, nil
	}
	options.Format = stored.Format
	options.Collision = stored.Collision
	options.JPEGQuality = stored.JPEGQuality
	options.Background = stored.Background
	if stored.OutputFolder == "" {
		// decodeOptions dropped a folder only earlier builds accepted, so
		// the default folder replaces it, unvalidated as above.
		return options, options.validateEncoding()
	}
	options.OutputFolder = stored.OutputFolder
	return options, options.Validate()
}

func (s *OptionsStore) Save(value *settings.Settings, options ResizeOptions) error {
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
	if _, err := decodeObjectFields("stored resize options", data); err != nil {
		return storedOptions{}, false, err
	}
	var stored storedOptions
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&stored); err != nil {
		return storedOptions{}, false, fmt.Errorf("decode stored resize options: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return storedOptions{}, false, errors.New("stored resize options contain trailing JSON values")
	}
	if stored.Version != OptionsVersion {
		return storedOptions{}, false, fmt.Errorf("unsupported resize options version %d", stored.Version)
	}
	options := ResizeOptions{
		Format:      stored.Format,
		Collision:   stored.Collision,
		JPEGQuality: stored.JPEGQuality,
		Background:  stored.Background,
	}
	if err := options.validateEncoding(); err != nil {
		return storedOptions{}, false, fmt.Errorf("validate stored resize options: %w", err)
	}
	if errors.Is(output.ValidateFolder(stored.OutputFolder), output.ErrFolderName) {
		// Builds before output planning accepted a folder path with a name
		// that ends in a space or a period, which jobs now refuse. Such a
		// folder is dropped, and Load puts the default folder in its place,
		// rather than the whole settings file failing validation and every
		// module losing its settings.
		stored.OutputFolder = ""
	} else if err := validateOutputFolder(stored.OutputFolder); err != nil {
		return storedOptions{}, false, fmt.Errorf("validate stored resize options: %w", err)
	}
	return stored, true, nil
}

func encodeOptions(section json.RawMessage, options ResizeOptions) (json.RawMessage, error) {
	fields, err := decodeSectionFields(section)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(storedOptions{
		Version:      OptionsVersion,
		Format:       options.Format,
		Collision:    options.Collision,
		JPEGQuality:  options.JPEGQuality,
		Background:   options.Background,
		OutputFolder: options.OutputFolder,
	})
	if err != nil {
		return nil, fmt.Errorf("encode stored resize options: %w", err)
	}
	fields[optionsKey] = data
	section, err = json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("encode image resizer settings section: %w", err)
	}
	return section, nil
}

func decodeSectionFields(section json.RawMessage) (map[string]json.RawMessage, error) {
	return decodeObjectFields("image resizer settings section", section)
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
