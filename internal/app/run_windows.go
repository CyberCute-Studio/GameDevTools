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
	store := settings.NewStore(paths)
	preset.RegisterValidators(store)
	defaultFolder, folderErr := screenshot.DefaultOutputFolder()
	optionsStore, err := screenshot.NewOptionsStore(store, defaultFolder)
	if err != nil {
		return err
	}
	loaded := settings.LoadResult{Settings: settings.Default()}
	if pathErr == nil {
		loaded = store.Load()
	} else {
		loaded.Warnings = append(loaded.Warnings, fmt.Sprintf(ui.LocalSettingsUnavailable, pathErr))
	}
	logger, loggerErr := diagnostics.Open(paths.LogFile)
	if loggerErr != nil {
		loaded.Warnings = append(loaded.Warnings, fmt.Sprintf(ui.LoggingUnavailable, loggerErr))
	} else {
		defer logger.Close()
	}

	value := loaded.Settings
	screenshotRepository, err := preset.NewRepository(shell.ModuleScreenshot, store)
	if err != nil {
		return err
	}
	resizerRepository, err := preset.NewRepository(shell.ModuleImageResizer, store)
	if err != nil {
		return err
	}
	optionsManager, err := screenshot.NewOptionsManager(optionsStore, &value)
	if err != nil {
		return err
	}
	if folderErr != nil && optionsManager.Options().OutputFolder == "" {
		loaded.Warnings = append(loaded.Warnings, fmt.Sprintf(ui.OutputFolderUnavailable, folderErr))
	}
	if len(loaded.Warnings) > 0 && logger != nil {
		_ = logger.Record(diagnostics.Event{Level: diagnostics.LevelWarning, Operation: "settings.load", Code: "recovered-with-warnings"})
	}
	monitor := display.NewMonitor()
	executor := execute.NewService(monitor)
	sessions := overlay.NewController(monitor)
	screenshotModule, err := screenshot.New(screenshotRepository, optionsManager, &value, monitor, executor.FullDisplay, executor.Rectangle, sessions)
	if err != nil {
		return err
	}
	resizerModule, err := imageresizer.New(resizerRepository, &value)
	if err != nil {
		return err
	}
	navigator := shell.NewNavigator()
	for _, module := range []shell.Module{screenshotModule, resizerModule, settings.New()} {
		if err := navigator.Register(module); err != nil {
			return err
		}
	}
	if err := navigator.Select(shell.ModuleID(loaded.Settings.Shell.SelectedModule)); err != nil {
		return err
	}
	return runShell(navigator, store, &value, logger, monitor, strings.Join(loaded.Warnings, "\n"))
}
