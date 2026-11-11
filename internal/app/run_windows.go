//go:build windows

package app

import (
	"runtime"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot"
	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	"github.com/CyberCute-Studio/GameDevTools/internal/shell"
)

func runNative() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := enablePerMonitorDPI(); err != nil {
		return err
	}
	navigator := shell.NewNavigator()
	for _, module := range []shell.Module{screenshot.New(), imageresizer.New(), settings.New()} {
		if err := navigator.Register(module); err != nil {
			return err
		}
	}
	return runShell(navigator)
}
