package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
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

// settingsFileFound marks a write that only creates the settings file finding
// one already there. Only the no-replace rename reports it: creating the
// folder before it also fails with an error matching os.ErrExist when a
// junction whose target is gone holds the folder's name, which says nothing
// about a settings file.
type settingsFileFound struct{ err error }

func (e settingsFileFound) Error() string { return e.err.Error() }

func (e settingsFileFound) Unwrap() error { return e.err }

// foundSettingsFile reports whether err is a create finding a settings file.
func foundSettingsFile(err error) bool {
	var found settingsFileFound
	return errors.As(err, &found)
}

// legacyFolderName is the per-user folder the application used before it was
// named CyberCute Dev Tools. Load reads the settings file there whenever the
// settings file does not exist, so deleting the new file alone brings the old
// settings back.
const legacyFolderName = "GameDevTools"

type Paths struct {
	ConfigFile string
	LogFile    string
	// LegacyConfigFile is the settings file of a build from before the
	// rename, which Load takes the settings from while ConfigFile does not
	// exist, leaving it in place. Empty when there is none to look for.
	LegacyConfigFile string
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

// LoadResult is the settings Load gives and every warning it gave about the
// settings file, in Warnings, two of which a caller that keeps showing them
// may need to tell from the rest, as a later save or restore settles each
// differently: Recovered and Unwritten.
type LoadResult struct {
	Settings Settings
	Warnings []string
	// Recovered is the warning, among Warnings, that the settings file was
	// malformed and was backed up, naming the backup, and that the defaults
	// took its place, or "". No later save or restore makes it untrue: the
	// backup, which alone holds that file's settings, stays where it names.
	Recovered string
	// Unwritten is the warning, among Warnings, that the settings Load gives
	// — the defaults that replaced a malformed file, or the migrated
	// settings — could not be written to the settings file, or "". The next
	// save that succeeds writes them, and it no longer applies.
	Unwritten string
}

type Store struct {
	path string
	// legacyPath is Paths.LegacyConfigFile.
	legacyPath string
	migrations map[int]Migration
	validators map[string][]ModuleValidator
	blocked    error
	// createOnly is whether Load found no settings file and nothing has
	// reached it since, whether or not the legacy file gave the settings.
	// Until something has, a write only creates that file, as its absence may
	// be a share that is reconnecting. A block Load leaves then protects the
	// legacy file, which stays in place whatever is written, so Restore can
	// lift it by creating the settings file.
	createOnly bool
	// inherited is the settings Load took from the legacy file while they
	// have not reached the settings file, or the zero value.
	inherited Settings
	// unloaded is whether the settings file holds settings the session never
	// took: Load could not read or decode the file and started the session
	// from the defaults in their place, and neither a save nor a restore
	// that backed the file up has replaced it since. Restore then derives
	// the restored settings from the file's, once they decode, so what the
	// restore keeps — the preset libraries — is what the file stores rather
	// than the defaults the session started from.
	unloaded bool
	// held is whether Save writes nothing, as Hold leaves it.
	held bool
	now  func() time.Time
	// readBack reads a backup back to verify it; tests substitute it.
	readBack func(string) ([]byte, error)
	// reachable is folderReachable; tests substitute it.
	reachable func(string) bool
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
		ConfigFile:       filepath.Join(configRoot, ui.AppName, "settings.json"),
		LogFile:          filepath.Join(logRoot, ui.AppName, "Logs", ui.AppFileName+".log"),
		LegacyConfigFile: filepath.Join(configRoot, legacyFolderName, "settings.json"),
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
		legacyPath: paths.LegacyConfigFile,
		migrations: make(map[int]Migration),
		validators: make(map[string][]ModuleValidator),
		now:        time.Now,
		readBack:   os.ReadFile,
		reachable:  folderReachable,
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
	if s.path == "" {
		return LoadResult{Settings: Default(), Warnings: []string{ui.SettingsPathUnavailable}}
	}
	s.removeStaleTemporaries()
	result, settled := s.load(true)
	if !settled {
		// The settings file exists after all, so it is what the session
		// loads, and the legacy file is left unadopted.
		s.blocked, s.unloaded, s.createOnly, s.inherited = nil, false, false, Settings{}
		result, _ = s.load(false)
	}
	return result
}

// load reads the settings file or, when adopt allows it and that is missing,
// the legacy file. It reports false when adopting the legacy file found that
// a settings file exists after all, having written nothing over it; the
// result is then to be discarded. With adopt false a missing settings file is
// a failed read, as one just found to exist has not really gone away.
func (s *Store) load(adopt bool) (LoadResult, bool) {
	result := LoadResult{Settings: Default()}
	data, err := os.ReadFile(s.path)
	source := s.path
	if adopt && errors.Is(err, os.ErrNotExist) {
		if s.legacyPath == "" {
			return result, true
		}
		// The first run after the rename takes the settings of the build
		// before it. The file stays where it is, so that build still works,
		// and is not read again once the settings file exists. One that
		// cannot be read blocks saving, as an unreadable settings file does,
		// because a save would create the settings file and so abandon it.
		// So does finding neither file when the folders cannot be reached:
		// a share that is down reports both missing, and a save once it
		// returns would replace the one file or abandon the other. A real
		// first run still only creates the file with its first write, as a
		// share can return just after the reads here.
		source, s.createOnly = s.legacyPath, true
		var gone bool
		data, gone, err = s.readLegacy()
		if gone {
			if _, statErr := os.Stat(s.path); !errors.Is(statErr, os.ErrNotExist) {
				return result, false
			}
			return result, true
		}
	}
	if err != nil {
		s.blocked = err
		s.unloaded = true
		message := ui.SettingsReadFailed
		if s.createOnly && errors.Is(err, os.ErrNotExist) {
			message = ui.SettingsFolderUnreachable
		}
		result.Warnings = append(result.Warnings, fmt.Sprintf(message, source, err))
		return result, true
	}

	decoded, migrated, err := s.decode(data)
	if err != nil {
		s.unloaded = true
		backup, backupErr := s.backup(data, "corrupt")
		if backupErr != nil {
			s.blocked = backupErr
			result.Warnings = append(result.Warnings, fmt.Sprintf(ui.SettingsInvalidBackupFailed, source, err, backupErr))
			return result, true
		}
		result.Recovered = fmt.Sprintf(ui.SettingsInvalidRecovered, err, backup)
		result.Warnings = append(result.Warnings, result.Recovered)
		if saveErr := s.saveLoaded(result.Settings); saveErr != nil {
			if s.createOnly && foundSettingsFile(saveErr) {
				os.Remove(backup)
				return result, false
			}
			result.Unwritten = fmt.Sprintf(ui.DefaultSettingsWriteFailed, saveErr)
			result.Warnings = append(result.Warnings, result.Unwritten)
		}
		return result, true
	}

	result.Settings = decoded
	if s.createOnly {
		// A copy of the sections, as the session's saves update the map it
		// is given, and these must stay what the legacy file held.
		s.inherited = decoded
		s.inherited.Modules = maps.Clone(decoded.Modules)
	}
	kept, backup := source, ""
	if migrated {
		var backupErr error
		backup, backupErr = s.backup(data, "migration")
		if backupErr != nil {
			s.blocked = backupErr
			result.Warnings = append(result.Warnings, fmt.Sprintf(ui.SettingsMigrationBackupFailed, backupErr, source))
			return result, true
		}
		kept = backup
	}
	if migrated || s.createOnly {
		if saveErr := s.saveLoaded(decoded); saveErr != nil {
			if s.createOnly && foundSettingsFile(saveErr) {
				if backup != "" {
					os.Remove(backup)
				}
				return result, false
			}
			result.Unwritten = fmt.Sprintf(ui.MigratedSettingsWriteFailed, saveErr, kept)
			result.Warnings = append(result.Warnings, result.Unwritten)
		}
	}
	return result, true
}

// readLegacy reads the legacy file. gone reports that it is really missing:
// its folder answers, and a second read, made after that answer in case the
// share returned between the two, still finds nothing. A missing file that is
// not gone is one reported missing from a folder that cannot be reached.
func (s *Store) readLegacy() (data []byte, gone bool, err error) {
	data, err = os.ReadFile(s.legacyPath)
	if !errors.Is(err, os.ErrNotExist) || !s.reachable(s.legacyPath) {
		return data, false, err
	}
	data, err = os.ReadFile(s.legacyPath)
	return data, errors.Is(err, os.ErrNotExist), err
}

// Inherited returns the settings the session took from the legacy file while
// they have not reached the settings file, or the zero Settings. A settings
// file found later that lacks one of their presets is taken to have had it
// deleted, so what Restore keeps is told from the presets the session created
// by these. That holds only for a settings file known to be written after the
// legacy file: an earlier build still runs and can add presets there, which
// an older settings file never held, and with the legacy file gone the
// session may hold the only copy of one. So unless the settings file is the
// newer of the two, nothing counts as inherited. A preset this keeps by
// mistake can be deleted again, where one it dropped could not be got back in
// the application.
func (s *Store) Inherited() Settings {
	if s.inherited.Modules == nil || !s.settingsFileIsNewer() {
		return Settings{}
	}
	return s.inherited
}

// settingsFileIsNewer reports whether both files can be examined and the
// settings file was written after the legacy file.
func (s *Store) settingsFileIsNewer() bool {
	legacy, err := os.Stat(s.legacyPath)
	if err != nil {
		return false
	}
	current, err := os.Stat(s.path)
	return err == nil && current.ModTime().After(legacy.ModTime())
}

// saveLoaded writes the settings Load settled on. While the settings file was
// found missing they only ever create it: its absence may be a share that is
// reconnecting, and replacing what is really there would lose it with no copy
// made.
func (s *Store) saveLoaded(value Settings) error {
	if !s.createOnly {
		return s.save(value)
	}
	if err := s.check(value); err != nil {
		return err
	}
	return s.create(value)
}

// Save writes value to the settings file, or, once Hold has held the
// store's saves, writes nothing and reports success.
func (s *Store) Save(value Settings) error {
	if s.held {
		return nil
	}
	return s.save(value)
}

// Hold holds every later Save, which then writes nothing and reports
// success, and returns the function that writes the settings as Save did
// before. The end of the Windows session holds the saves the main window's
// destruction makes — a field whose edit is committed as it loses the
// focus saves the options it changed — so that none of them can block the
// GUI thread inside WM_ENDSESSION on a settings folder that has stopped
// responding, and writes the settings once, as they then are, with the
// function returned, on a goroutine whose wait it bounds.
func (s *Store) Hold() (write func(Settings) error) {
	s.held = true
	return s.save
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
// restore is the user's explicit request to replace it. It saves again only
// then: an original such a store cannot find now is treated as one it
// cannot read, as a file the disk cannot reach for the moment, such as one
// on a network share that is down, is reported missing too, and the restore
// would otherwise let a later save replace it with no copy made. Once the
// copy exists, the session's settings are also the ones a later restore
// replaces, though the write fails: the session takes the restored
// settings, derived from the file's, and the copy keeps the file's bytes,
// so a later restore keeps the presets the session holds then — renamed,
// deleted, or created since — not the file's again. A write
// that fails after the copy is returned as Save returns
// it, leaving the original in place beside its copy, as is the failure
// without a settings path, ErrPathUnavailable, where there is no file to
// copy. A session that found no settings file at launch and has not written
// one, with none found now, is the exception to saving again only once a copy
// exists: the legacy file is not replaced, so the restore creates the
// settings file. When Load's block protects the legacy
// file, the restore first backs it up, and while it exists but still cannot
// be read, or is reported missing from a folder that cannot be reached, the
// restore changes nothing, as the presets the restore keeps may be in it and
// the new file would stop it ever being taken. Any restore that found no
// settings file creates the file without replacing one, so a settings file
// that was only reported missing is kept: when one turns out to exist, or a
// block cannot be lifted because the file cannot be created, nothing changes.
// Without a block, a file that cannot be created is a failed write, returned
// as Save returns it, and in a session that found no file at launch later
// saves still only create the file.
func (s *Store) Restore(current Settings, reset func(replaced Settings) (Settings, error)) (Settings, string, error) {
	replaced := current
	var data []byte
	exists, creating, loadedLegacy := false, false, false
	if s.path != "" {
		var err error
		data, err = os.ReadFile(s.path)
		switch {
		case errors.Is(err, os.ErrNotExist) && s.createOnly:
			creating = true
			if s.blocked != nil {
				legacy, gone, legacyErr := s.readLegacy()
				switch {
				case gone:
				case errors.Is(legacyErr, os.ErrNotExist):
					return Settings{}, "", notRestored{fmt.Errorf("reach the earlier build's settings at %s: %w; reconnect the drive or network share that holds them and try again", s.legacyPath, legacyErr)}
				case legacyErr != nil:
					return Settings{}, "", notRestored{fmt.Errorf("read the earlier build's settings at %s: %w; close whatever holds the file and try again, or move it out of that folder to go on without its presets", s.legacyPath, legacyErr)}
				default:
					data, exists, loadedLegacy = legacy, true, !s.unloaded
				}
			}
		case errors.Is(err, os.ErrNotExist) && s.blocked != nil:
			return Settings{}, "", notRestored{fmt.Errorf("read the current settings: %w", err)}
		case errors.Is(err, os.ErrNotExist):
			// A share that is reconnecting reports a file missing too, and no
			// copy of it has been made, so the restore must not replace it.
			creating = true
		case err != nil:
			return Settings{}, "", notRestored{fmt.Errorf("read the current settings: %w", err)}
		default:
			exists = true
		}
		if exists && !loadedLegacy && (s.unloaded || s.createOnly) {
			// A file that still does not decode is kept only in its copy, as
			// recovery from a malformed file keeps it, and the session's
			// settings are the ones the restore replaces. With createOnly the
			// session holds the legacy file's settings or the defaults, not
			// this file's. A legacy file the session did load, whose migration
			// backup failed, is left out: the session holds its settings with
			// the edits made since, which decoding it again would undo.
			if stored, _, err := s.decode(data); err == nil {
				replaced = stored
			}
		}
	}
	value, err := reset(replaced)
	if err != nil {
		return Settings{}, "", notRestored{err}
	}
	if err := s.check(value); err != nil {
		return Settings{}, "", notRestored{err}
	}
	if s.path == "" {
		// There is no file to replace: the session takes the restored
		// settings in memory, and the write fails as every save without a
		// settings path does.
		return value, "", ErrPathUnavailable
	}
	backup := ""
	if exists {
		if backup, err = s.backup(data, "reset"); err != nil {
			return Settings{}, "", notRestored{fmt.Errorf("back up the current settings: %w", err)}
		}
	}
	if creating {
		if err := s.create(value); err != nil {
			if s.blocked == nil && !foundSettingsFile(err) {
				if !s.createOnly {
					s.unloaded = false
				}
				return value, "", err
			}
			if backup != "" {
				os.Remove(backup)
			}
			return Settings{}, "", notRestored{fmt.Errorf("write the restored settings: %w", err)}
		}
		s.blocked, s.unloaded, s.createOnly, s.inherited = nil, false, false, Settings{}
		return value, backup, nil
	}
	s.blocked, s.unloaded, s.createOnly, s.inherited = nil, false, false, Settings{}
	return value, backup, s.write(value)
}

// folderReachable reports whether the nearest folder above path that exists
// answers, by listing or by refusing access: a profile may allow creating
// files in a folder it denies listing, and a refusal comes from the volume
// holding it. Windows reports a file on a share that is down as not found,
// the same as one that is really gone, so a missing file counts as gone only
// when that folder answers. The search goes up only past names that are really
// absent or are not folders: a junction or link whose target is offline stops
// it, so a folder on the local volume above the link does not vouch for the
// volume that is gone, and so does a network error, so a share that is down
// costs one wait rather than one for each folder on its path.
func folderReachable(path string) bool {
	for folder := filepath.Dir(path); ; folder = filepath.Dir(folder) {
		info, err := os.Stat(folder)
		switch {
		case err == nil && info.IsDir():
			return folderAnswers(folder)
		case err == nil:
		case errors.Is(err, os.ErrPermission):
			return true
		case !errors.Is(err, os.ErrNotExist) || networkPathMissing(err):
			return false
		default:
			if _, err := os.Lstat(folder); !errors.Is(err, os.ErrNotExist) {
				return false
			}
		}
		if filepath.Dir(folder) == folder {
			return false
		}
	}
}

// folderAnswers reports whether folder, asked for an entry, gives one, has
// none, or refuses access.
func folderAnswers(folder string) bool {
	file, err := os.Open(folder)
	if err != nil {
		return errors.Is(err, os.ErrPermission)
	}
	defer file.Close()
	_, err = file.Readdirnames(1)
	return err == nil || errors.Is(err, io.EOF) || errors.Is(err, os.ErrPermission)
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
	if err := s.check(value); err != nil {
		return err
	}
	err := s.write(value)
	if s.createOnly && foundSettingsFile(err) {
		// The file was only reported missing when Load looked for it. It
		// holds settings this session never took, so it is kept for the next
		// launch to load, and Restore can back it up.
		s.blocked, s.unloaded, s.createOnly = err, true, false
		return fmt.Errorf("saving is disabled because a settings file this session did not load exists: %w", err)
	}
	return err
}

// check validates value as every write requires: the shell's settings and
// every module section, with each section owner's validator.
func (s *Store) check(value Settings) error {
	if err := validate(value); err != nil {
		return err
	}
	return s.validateModules(value)
}

// write puts value, which check has accepted, in the settings file, replacing
// it, or only creating it while nothing has reached it since Load found it
// missing (createOnly).
func (s *Store) write(value Settings) error {
	if s.createOnly {
		return s.create(value)
	}
	return s.place(value, os.Rename)
}

// create writes value, which check has accepted, as a new settings file, and
// fails with an error foundSettingsFile reports if there already is one.
func (s *Store) create(value Settings) error {
	return s.place(value, func(from, to string) error {
		err := renameNoReplace(from, to)
		if errors.Is(err, os.ErrExist) {
			return settingsFileFound{err}
		}
		return err
	})
}

// place puts value at the settings path through a temporary file in the same
// folder, which rename moves there once the new settings are written in full.
func (s *Store) place(value Settings, rename func(from, to string) error) error {
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
	if err := rename(temporaryPath, s.path); err != nil {
		return fmt.Errorf("replace settings file: %w", err)
	}
	s.unloaded, s.createOnly, s.inherited = false, false, Settings{}
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
