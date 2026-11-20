//go:build windows

package app

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/CyberCute-Studio/GameDevTools/internal/diagnostics"
	"github.com/CyberCute-Studio/GameDevTools/internal/display"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer"
	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot/execute"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot/overlay"
	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	"github.com/CyberCute-Studio/GameDevTools/internal/shell"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// The shell finds a module view's optional hooks by interface assertion, so a
// signature that drifted would silently drop the hook. The Image Resizer view
// depends on its hooks — without the dispatcher, no content check or job
// event would ever reach the list; without the availability hooks, Start
// Resize and drops would stay enabled while a job runs; and without
// NativeShutdown, the process would exit without waiting for a cancelled
// job's images in flight to remove their temporary files; without the close
// guard, closing would cancel a running job without asking. The restore of
// the default settings depends on hooks too — without the Settings view's
// handler Restore Default Settings would do nothing, and without a module's
// restorer its controls, and the Screenshot global shortcut, would go on
// showing the settings the restore replaced — so their conformance is
// checked when the program compiles.
var (
	_ nativeView               = (*imageresizer.NativeView)(nil)
	_ nativeDispatcherAware    = (*imageresizer.NativeView)(nil)
	_ nativeActionHandler      = (*imageresizer.NativeView)(nil)
	_ nativeActionAvailability = (*imageresizer.NativeView)(nil)
	_ nativeDropTarget         = (*imageresizer.NativeView)(nil)
	_ nativeDropAvailability   = (*imageresizer.NativeView)(nil)
	_ nativeShutdown           = (*imageresizer.NativeView)(nil)
	_ nativeCloseGuard         = (*imageresizer.NativeView)(nil)
	_ nativeDefaultsRestorer   = (*imageresizer.NativeView)(nil)
	_ nativeDefaultsRestorer   = (*screenshot.NativeView)(nil)
	_ nativeView               = (*settings.NativeView)(nil)
	_ nativeRestoreRequester   = (*settings.NativeView)(nil)
)

func runNative() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := enablePerMonitorDPI(); err != nil {
		return err
	}
	// One instance per user session (APP-005): a second launch activates
	// the running instance's window and exits, so global shortcuts and
	// settings never conflict across instances.
	releaseInstance, activated, err := enforceSingleInstance()
	if err != nil {
		return err
	}
	if activated {
		return nil
	}
	defer releaseInstance()

	paths, pathErr := settings.LocalPaths()
	var folders defaultFolders
	folders.screenshot, folders.screenshotErr = screenshot.DefaultOutputFolder()
	folders.resizer, folders.resizerErr = imageresizer.DefaultOutputFolder()
	state, err := openSession(paths, pathErr, folders)
	if err != nil {
		return err
	}
	logger, loggerErr := diagnostics.Open(paths.LogFile)
	// The warnings the settings do not decide, which still apply once the
	// default settings are restored.
	var unsettled []string
	if loggerErr != nil {
		warning := fmt.Sprintf(ui.LoggingUnavailable, loggerErr)
		state.warnings = append(state.warnings, warning)
		unsettled = append(unsettled, warning)
	} else {
		defer logger.Close()
	}
	if len(state.warnings) > 0 && logger != nil {
		_ = logger.Record(diagnostics.Event{Level: diagnostics.LevelWarning, Operation: "settings.load", Code: "recovered-with-warnings"})
	}
	monitor := display.NewMonitor()
	executor := execute.NewService(monitor)
	navigator, err := newNavigator(state, monitor, executor.FullDisplay, executor.Rectangle, overlay.NewController(monitor))
	if err != nil {
		return err
	}
	return runShell(navigator, state.store, state.value, logger, monitor, strings.Join(state.warnings, "\n"), state.restoredNotice(unsettled))
}

// defaultFolders are the output folders the modules select by default, and
// why each could not be resolved, as startup resolves them through Windows.
type defaultFolders struct {
	screenshot    string
	screenshotErr error
	resizer       string
	resizerErr    error
}

// session is the application's persistent state as it starts: the settings
// store, with every section owner's validator registered, the settings
// loaded through it, which every module shares, the warnings loading them
// gave, the preset repositories and options managers built on them, and the
// default output folders they were built with.
type session struct {
	store             *settings.Store
	value             *settings.Settings
	warnings          []string
	screenshotPresets *preset.Repository
	resizerPresets    *preset.Repository
	capture           *screenshot.OptionsManager
	resize            *imageresizer.OptionsManager
	folders           defaultFolders
}

// openSession opens the settings at paths and builds the modules'
// persistent state on them, as the application starts. When pathErr says
// the per-user paths could not be located, it starts from the defaults in
// memory. Every validator is registered before the file is loaded — the
// preset repositories register the preset libraries', and the options
// stores the capture and resize options' — so malformed preset, capture, or
// resize data follows the store's backup and recovery like the rest of the
// file (PRESET-008, SET-006). The warnings include an output folder that
// could not be resolved, so the user is asked to choose one (folderWarnings).
func openSession(paths settings.Paths, pathErr error, folders defaultFolders) (*session, error) {
	store := settings.NewStore(paths)
	state := &session{store: store, folders: folders}
	var err error
	if state.screenshotPresets, err = preset.NewRepository(shell.ModuleScreenshot, store); err != nil {
		return nil, err
	}
	if state.resizerPresets, err = preset.NewRepository(shell.ModuleImageResizer, store); err != nil {
		return nil, err
	}
	captureStore, err := screenshot.NewOptionsStore(store, folders.screenshot)
	if err != nil {
		return nil, err
	}
	resizeStore, err := imageresizer.NewOptionsStore(store, folders.resizer)
	if err != nil {
		return nil, err
	}
	loaded := settings.LoadResult{Settings: settings.Default()}
	if pathErr == nil {
		loaded = store.Load()
	} else {
		loaded.Warnings = append(loaded.Warnings, fmt.Sprintf(ui.LocalSettingsUnavailable, pathErr))
	}
	state.value, state.warnings = &loaded.Settings, loaded.Warnings
	if state.capture, err = screenshot.NewOptionsManager(captureStore, state.value); err != nil {
		return nil, err
	}
	if state.resize, err = imageresizer.NewOptionsManager(resizeStore, state.value); err != nil {
		return nil, err
	}
	state.warnings = append(state.warnings, state.folderWarnings()...)
	return state, nil
}

// folderWarnings returns the warnings the output folders give as the
// options now hold them: the Screenshot default folder that could not be
// resolved while no folder is chosen, and the Image Resizer's folder
// warning, such as a stored folder the default one replaced
// (imageresizer.OptionsManager.FolderWarning).
func (state *session) folderWarnings() []string {
	var warnings []string
	if state.folders.screenshotErr != nil && state.capture.Options().OutputFolder == "" {
		warnings = append(warnings, fmt.Sprintf(ui.OutputFolderUnavailable, state.folders.screenshotErr))
	}
	if warning := state.resize.FolderWarning(state.folders.resizerErr); warning != "" {
		warnings = append(warnings, warning)
	}
	return warnings
}

// restoredNotice returns the shell's startup notice once the default
// settings are restored with the settings file backed up (settleNotice):
// the output folders' warnings the restored options give, and unsettled,
// the startup warnings the settings do not decide, such as diagnostic
// logging being unavailable. The warnings about the file the session
// started from no longer apply then.
func (state *session) restoredNotice(unsettled []string) func() string {
	return func() string {
		return strings.Join(append(state.folderWarnings(), unsettled...), "\n")
	}
}

// newNavigator builds the Screenshot, Image Resizer, and Settings modules on
// the session, Screenshot with the given capture services, and registers
// them for navigation in that order with the remembered module selected.
func newNavigator(state *session, monitor *display.Monitor, capture screenshot.CaptureFunc, captureRectangle screenshot.RectangleCaptureFunc, sessions *overlay.Controller) (*shell.Navigator, error) {
	screenshotModule, err := screenshot.New(state.screenshotPresets, state.capture, state.value, monitor, capture, captureRectangle, sessions)
	if err != nil {
		return nil, err
	}
	resizerModule, err := imageresizer.New(state.resizerPresets, state.resize, state.value)
	if err != nil {
		return nil, err
	}
	navigator := shell.NewNavigator()
	for _, module := range []shell.Module{screenshotModule, resizerModule, settings.New()} {
		if err := navigator.Register(module); err != nil {
			return nil, err
		}
	}
	if err := navigator.Select(shell.ModuleID(state.value.Shell.SelectedModule)); err != nil {
		return nil, err
	}
	return navigator, nil
}
