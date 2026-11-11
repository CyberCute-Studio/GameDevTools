package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/CyberCute-Studio/GameDevTools/internal/shell"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

const CurrentVersion = 1

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

type LoadResult struct {
	Settings Settings
	Warnings []string
}

type Store struct {
	path       string
	migrations map[int]Migration
	blocked    error
	now        func() time.Time
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
		now:        time.Now,
	}
	for _, migration := range migrations {
		if migration != nil {
			store.migrations[migration.FromVersion()] = migration
		}
	}
	return store
}

func (s *Store) Load() LoadResult {
	result := LoadResult{Settings: Default()}
	if s.path == "" {
		result.Warnings = append(result.Warnings, ui.SettingsPathUnavailable)
		return result
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return result
	}
	if err != nil {
		s.blocked = err
		result.Warnings = append(result.Warnings, fmt.Sprintf(ui.SettingsReadFailed, s.path, err))
		return result
	}

	decoded, migrated, err := s.decode(data)
	if err != nil {
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

func (s *Store) save(value Settings) error {
	if s.blocked != nil {
		return fmt.Errorf("saving is disabled because settings recovery is incomplete: %w", s.blocked)
	}
	if s.path == "" {
		return errors.New("settings path is unavailable")
	}
	if err := validate(value); err != nil {
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
	return decoded, migrated, nil
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
		return path, nil
	}
}
