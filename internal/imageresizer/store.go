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

// Load returns the stored resize options, or the defaults when none are
// stored, and the stored output folder the default folder replaced, one only
// earlier builds accepted, or "" when it replaced none, so the caller can
// warn about it.
func (s *OptionsStore) Load(value settings.Settings) (ResizeOptions, string, error) {
	options := DefaultOptions(s.defaultFolder)
	found, err := decodeOptions(value.Modules[string(moduleID)])
	if err != nil {
		return ResizeOptions{}, "", err
	}
	if !found.exists {
		// The default folder is not validated here: when the Pictures folder
		// cannot be resolved the application must still start so the user can
		// choose an output folder, as the startup warning announces.
		return options, "", nil
	}
	// decodeOptions validated every stored field.
	stored := found.stored
	options.Format = stored.Format
	options.Collision = stored.Collision
	options.JPEGQuality = stored.JPEGQuality
	options.Background = stored.Background
	if found.legacyFolder {
		// The default folder, unvalidated as above, replaces a folder only
		// earlier builds accepted.
		return options, stored.OutputFolder, nil
	}
	options.OutputFolder = stored.OutputFolder
	return options, "", nil
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
	_, err := decodeOptions(section)
	return err
}

// decodedOptions is what decodeOptions finds in a settings section.
type decodedOptions struct {
	stored storedOptions
	// exists reports whether the section holds stored options.
	exists bool
	// legacyFolder reports whether their output folder is one only earlier
	// builds accepted, which the stored options keep but jobs refuse.
	legacyFolder bool
}

// decodeOptions decodes and validates the stored resize options.
func decodeOptions(section json.RawMessage) (decodedOptions, error) {
	if len(section) == 0 {
		return decodedOptions{}, nil
	}
	fields, err := decodeSectionFields(section)
	if err != nil {
		return decodedOptions{}, err
	}
	data, exists := fields[optionsKey]
	if !exists {
		return decodedOptions{}, nil
	}
	if _, err := decodeObjectFields("stored resize options", data); err != nil {
		return decodedOptions{}, err
	}
	found := decodedOptions{exists: true}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&found.stored); err != nil {
		return decodedOptions{}, fmt.Errorf("decode stored resize options: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return decodedOptions{}, errors.New("stored resize options contain trailing JSON values")
	}
	stored := found.stored
	if stored.Version != OptionsVersion {
		return decodedOptions{}, fmt.Errorf("unsupported resize options version %d", stored.Version)
	}
	options := ResizeOptions{
		Format:      stored.Format,
		Collision:   stored.Collision,
		JPEGQuality: stored.JPEGQuality,
		Background:  stored.Background,
	}
	if err := options.validateEncoding(); err != nil {
		return decodedOptions{}, fmt.Errorf("validate stored resize options: %w", err)
	}
	switch err := output.ValidateFolder(stored.OutputFolder); {
	case err == nil:
	case errors.Is(err, output.ErrFolderName), errors.Is(err, output.ErrFolderRoot):
		// Builds before output planning accepted any absolute folder path
		// in valid text, including one whose own name ends in a space or a
		// period, one with a folder name in its path that ends in a period,
		// one with a colon that names a file stream, and one that names only
		// a volume or a server, which jobs now refuse. Such a folder gives
		// way to the default folder on load, with a warning, rather than the
		// whole settings file failing validation and every module losing its
		// settings.
		found.legacyFolder = true
	default:
		return decodedOptions{}, fmt.Errorf("validate stored resize options: %w", folderMessage(err))
	}
	return found, nil
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
