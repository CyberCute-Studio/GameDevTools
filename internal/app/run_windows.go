//go:build windows

package app

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/CyberCute-Studio/GameDevTools/internal/diagnostics"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer"
	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot"
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

	paths, pathErr := settings.LocalPaths()
	store := settings.NewStore(paths)
	preset.RegisterValidators(store)
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
	if len(loaded.Warnings) > 0 && logger != nil {
		_ = logger.Record(diagnostics.Event{Level: diagnostics.LevelWarning, Operation: "settings.load", Code: "recovered-with-warnings"})
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
	screenshotModule, err := screenshot.New(screenshotRepository, &value)
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
	return runShell(navigator, store, &value, logger, strings.Join(loaded.Warnings, "\n"))
}
