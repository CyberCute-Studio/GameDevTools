package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/CyberCute-Studio/GameDevTools/internal/shell"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

const CurrentVersion = 1

// staleTemporaryAge is how old a temporary settings file must be before Load
// removes it as one an interrupted save left behind. A save keeps its
// temporary file only for the moments between creating it and renaming it
// over the settings file, so a file this old belongs to no save in flight,
// even one by an instance in another of the user's sessions, which shares
// the per-user settings directory. The age is a day rather than moments
// because it compares the file's time, which the file's server sets when
// the directory is redirected to a network share, with this computer's
// clock, and the two can differ by minutes; a file an interrupted save left
// holds only settings and waits harmlessly until then.
const staleTemporaryAge = 24 * time.Hour

// ErrNotRestored reports that Restore wrote nothing: the restored settings
// could not be made or were invalid, or the current settings file could not
// be read or backed up.
var ErrNotRestored = errors.New("the default settings were not restored")

// ErrPathUnavailable reports that a save had no settings file to write, as
// the per-user settings path could not be determined: the session's
// settings stay in memory until the application closes.
var ErrPathUnavailable = errors.New("settings path is unavailable")

// notRestored is why Restore wrote nothing, marked as ErrNotRestored without
// repeating that in its text, which the caller's message says already.
type notRestored struct{ err error }

func (e notRestored) Error() string { return e.err.Error() }

func (e notRestored) Unwrap() []error { return []error{ErrNotRestored, e.err} }

type Paths struct {
	ConfigFile string
	LogFile    string
}

type Settings struct {
	Version int                        `json:"version"`
	Shell   ShellSettings              `json:"shell"`
	Modules map[string]json.RawMessage `json:"modules,omitempty"`
}

type ShellSettings struct {
	SelectedModule string         `json:"selectedModule"`
	Window         WindowSettings `json:"window"`
}

type WindowSettings struct {
	X           int32 `json:"x"`
	Y           int32 `json:"y"`
	Width       int32 `json:"width"`
	Height      int32 `json:"height"`
	Maximized   bool  `json:"maximized"`
	HasPosition bool  `json:"hasPosition"`
}

type Migration interface {
	FromVersion() int
	Migrate(json.RawMessage) (json.RawMessage, error)
}

type ModuleValidator func(json.RawMessage) error

type LoadResult struct {
	Settings Settings
	Warnings []string
}

type Store struct {
	path       string
	migrations map[int]Migration
	validators map[string][]ModuleValidator
	blocked    error
	// unloaded is whether the settings file holds settings the session never
	// took: Load could not read or decode the file and started the session
	// from the defaults in their place, and no save has replaced it since.
	// Restore then derives the restored settings from the file's, once they
	// decode, so what the restore keeps — the preset libraries — is what the
	// file stores rather than the defaults the session started from.
	unloaded bool
	now      func() time.Time
	// readBack reads a backup back to verify it; tests substitute it.
	readBack func(string) ([]byte, error)
}

func LocalPaths() (Paths, error) {
	configRoot, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("locate per-user configuration directory: %w", err)
	}
	logRoot, err := os.UserCacheDir()
	if err != nil {
		return Paths{}, fmt.Errorf("locate per-user log directory: %w", err)
	}
	return Paths{
		ConfigFile: filepath.Join(configRoot, "GameDevTools", "settings.json"),
		LogFile:    filepath.Join(logRoot, "GameDevTools", "Logs", "gamedevtools.log"),
	}, nil
}

func Default() Settings {
	return Settings{
		Version: CurrentVersion,
		Shell: ShellSettings{
			SelectedModule: string(shell.ModuleScreenshot),
			Window: WindowSettings{
				Width:  1100,
				Height: 700,
			},
		},
		Modules: make(map[string]json.RawMessage),
	}
}

func NewStore(paths Paths, migrations ...Migration) *Store {
	store := &Store{
		path:       paths.ConfigFile,
		migrations: make(map[int]Migration),
		validators: make(map[string][]ModuleValidator),
		now:        time.Now,
		readBack:   os.ReadFile,
	}
	for _, migration := range migrations {
		if migration != nil {
			store.migrations[migration.FromVersion()] = migration
		}
	}
	return store
}

// RegisterModuleValidator appends a validator for a module settings section.
// Multiple validators per module compose so independent section owners, such
// as the preset library and capture options, each validate their own keys.
// A nil validator removes every validator registered for the module.
func (s *Store) RegisterModuleValidator(name string, validator ModuleValidator) {
	if validator == nil {
		delete(s.validators, name)
		return
	}
	s.validators[name] = append(s.validators[name], validator)
}

func (s *Store) Load() LoadResult {
	result := LoadResult{Settings: Default()}
	if s.path == "" {
		result.Warnings = append(result.Warnings, ui.SettingsPathUnavailable)
		return result
	}
	s.removeStaleTemporaries()
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return result
	}
	if err != nil {
		s.blocked = err
		s.unloaded = true
		result.Warnings = append(result.Warnings, fmt.Sprintf(ui.SettingsReadFailed, s.path, err))
		return result
	}

	decoded, migrated, err := s.decode(data)
	if err != nil {
		s.unloaded = true
		backup, backupErr := s.backup(data, "corrupt")
		if backupErr != nil {
			s.blocked = backupErr
			result.Warnings = append(result.Warnings, fmt.Sprintf(ui.SettingsInvalidBackupFailed, s.path, err, backupErr))
			return result
		}
		result.Warnings = append(result.Warnings, fmt.Sprintf(ui.SettingsInvalidRecovered, err, backup))
		if saveErr := s.save(result.Settings); saveErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf(ui.DefaultSettingsWriteFailed, saveErr))
		}
		return result
	}

	result.Settings = decoded
	if migrated {
		backup, backupErr := s.backup(data, "migration")
		if backupErr != nil {
			s.blocked = backupErr
			result.Warnings = append(result.Warnings, fmt.Sprintf(ui.SettingsMigrationBackupFailed, backupErr, s.path))
			return result
		}
		if saveErr := s.save(decoded); saveErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf(ui.MigratedSettingsWriteFailed, saveErr, backup))
		}
	}
	return result
}

func (s *Store) Save(value Settings) error {
	return s.save(value)
}

// Restore replaces the settings file with the application's settings
// restored to their defaults (SET-005), which reset derives from the
// settings the restore replaces: current, the session's, or, while the
// session never took the file's (unloaded) and the file now decodes, the
// file's, so what the restore keeps is what the file stores. The file's
// current bytes are first copied to settings.json.reset-<timestamp>, as
// backup copies them, so the settings it replaces stay recoverable, and
// Restore returns the restored settings, for the session to take, and the
// copy's path, or "" when there was no file to copy. When reset fails or
// its settings would not save, or the file cannot be read or copied,
// nothing is written and the error wraps ErrNotRestored. Once the copy
// exists, a store whose saving Load disabled, because it could not read or
// back up the original, saves again: the original is preserved now, and the
// restore is the user's explicit request to replace it. A write that fails
// after the copy is returned as Save returns it, leaving the original in
// place beside its copy, as is the failure without a settings path,
// ErrPathUnavailable, where there is no file to copy.
func (s *Store) Restore(current Settings, reset func(replaced Settings) (Settings, error)) (Settings, string, error) {
	replaced := current
	var data []byte
	exists := false
	if s.path != "" {
		var err error
		data, err = os.ReadFile(s.path)
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			return Settings{}, "", notRestored{fmt.Errorf("read the current settings: %w", err)}
		default:
			exists = true
			if s.unloaded {
				// A file that still does not decode is kept only in its
				// copy, as recovery from a malformed file keeps it, and the
				// session's settings are the ones the restore replaces.
				if stored, _, err := s.decode(data); err == nil {
					replaced = stored
				}
			}
		}
	}
	value, err := reset(replaced)
	if err != nil {
		return Settings{}, "", notRestored{err}
	}
	if err := validate(value); err != nil {
		return Settings{}, "", notRestored{err}
	}
	if err := s.validateModules(value); err != nil {
		return Settings{}, "", notRestored{err}
	}
	if s.path == "" {
		// There is no file to replace: the session takes the restored
		// settings in memory, and the write fails as every save does.
		return value, "", s.save(value)
	}
	backup := ""
	if exists {
		if backup, err = s.backup(data, "reset"); err != nil {
			return Settings{}, "", notRestored{fmt.Errorf("back up the current settings: %w", err)}
		}
	}
	s.blocked = nil
	return value, backup, s.save(value)
}

// removeStaleTemporaries removes the temporary files that saves interrupted
// before their rename — by a crash, a forced exit, or the end of the
// Windows session — left in the settings directory, so they do not pile up
// there across launches. Only regular files named as save creates them, and
// older than staleTemporaryAge, are removed; a failure leaves the file for
// the next launch.
func (s *Store) removeStaleTemporaries() {
	directory := filepath.Dir(s.path)
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	cutoff := s.now().Add(-staleTemporaryAge)
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || !strings.HasPrefix(name, ".settings-") || !strings.HasSuffix(name, ".tmp") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(directory, name))
	}
}

func (s *Store) save(value Settings) error {
	if s.blocked != nil {
		return fmt.Errorf("saving is disabled because settings recovery is incomplete: %w", s.blocked)
	}
	if s.path == "" {
		return ErrPathUnavailable
	}
	if err := validate(value); err != nil {
		return err
	}
	if err := s.validateModules(value); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	data = append(data, '\n')
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".settings-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary settings file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary settings file: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary settings file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("flush temporary settings file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary settings file: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		return fmt.Errorf("replace settings file: %w", err)
	}
	s.unloaded = false
	return nil
}

func (s *Store) decode(data []byte) (Settings, bool, error) {
	var envelope struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return Settings{}, false, fmt.Errorf("decode version: %w", err)
	}
	if envelope.Version < 0 || envelope.Version > CurrentVersion {
		return Settings{}, false, fmt.Errorf("unsupported settings version %d", envelope.Version)
	}
	migrated := false
	for envelope.Version < CurrentVersion {
		migration, exists := s.migrations[envelope.Version]
		if !exists {
			return Settings{}, false, fmt.Errorf("no migration from settings version %d", envelope.Version)
		}
		var err error
		data, err = migration.Migrate(data)
		if err != nil {
			return Settings{}, false, fmt.Errorf("migrate settings version %d: %w", envelope.Version, err)
		}
		migrated = true
		if err := json.Unmarshal(data, &envelope); err != nil {
			return Settings{}, false, fmt.Errorf("decode migrated version: %w", err)
		}
		if envelope.Version != CurrentVersion && envelope.Version <= migration.FromVersion() {
			return Settings{}, false, errors.New("settings migration did not advance the version")
		}
	}
	var decoded Settings
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return Settings{}, false, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Settings{}, false, errors.New("settings contain trailing JSON values")
	}
	if decoded.Modules == nil {
		decoded.Modules = make(map[string]json.RawMessage)
	}
	if err := validate(decoded); err != nil {
		return Settings{}, false, err
	}
	if err := s.validateModules(decoded); err != nil {
		return Settings{}, false, err
	}
	return decoded, migrated, nil
}

func (s *Store) validateModules(value Settings) error {
	for name, validators := range s.validators {
		section, exists := value.Modules[name]
		if !exists {
			continue
		}
		for _, validator := range validators {
			if err := validator(section); err != nil {
				return fmt.Errorf("module settings %q are invalid: %w", name, err)
			}
		}
	}
	return nil
}

func validate(value Settings) error {
	if value.Version != CurrentVersion {
		return fmt.Errorf("unsupported settings version %d", value.Version)
	}
	switch shell.ModuleID(value.Shell.SelectedModule) {
	case shell.ModuleScreenshot, shell.ModuleImageResizer, shell.ModuleSettings:
	default:
		return fmt.Errorf("unknown selected module %q", value.Shell.SelectedModule)
	}
	if value.Shell.Window.Width < 320 || value.Shell.Window.Height < 240 {
		return errors.New("saved window dimensions are too small")
	}
	if value.Shell.Window.Width > 32767 || value.Shell.Window.Height > 32767 {
		return errors.New("saved window dimensions are too large")
	}
	for name, section := range value.Modules {
		if name == "" {
			return errors.New("module settings name is empty")
		}
		trimmed := bytes.TrimSpace(section)
		if !json.Valid(trimmed) {
			return fmt.Errorf("module settings %q are malformed", name)
		}
		if len(trimmed) == 0 || trimmed[0] != '{' {
			return fmt.Errorf("module settings %q must be a JSON object", name)
		}
	}
	return nil
}

// backup copies data, the settings file's bytes, to a new file beside it
// named for the reason and the time, and reads the copy back, so a copy the
// disk did not keep intact is reported as a failure rather than trusted
// while the original is replaced (SET-006).
func (s *Store) backup(data []byte, reason string) (string, error) {
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create backup directory: %w", err)
	}
	base := fmt.Sprintf("%s.%s-%s", s.path, reason, s.now().UTC().Format("20060102T150405.000000000Z"))
	for suffix := 0; ; suffix++ {
		path := base
		if suffix > 0 {
			path = fmt.Sprintf("%s-%d", base, suffix)
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := file.Write(data); err != nil {
			file.Close()
			os.Remove(path)
			return "", err
		}
		if err := file.Sync(); err != nil {
			file.Close()
			os.Remove(path)
			return "", err
		}
		if err := file.Close(); err != nil {
			os.Remove(path)
			return "", err
		}
		copied, err := s.readBack(path)
		if err != nil {
			os.Remove(path)
			return "", fmt.Errorf("verify backup %s: %w", path, err)
		}
		if !bytes.Equal(copied, data) {
			os.Remove(path)
			return "", fmt.Errorf("verify backup %s: the copy differs from the original", path)
		}
		return path, nil
	}
}
