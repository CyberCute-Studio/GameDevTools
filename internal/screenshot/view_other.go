//go:build !windows

package screenshot

import (
	"github.com/CyberCute-Studio/GameDevTools/internal/display"
	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot/overlay"
)

func NewNativeView(manager *preset.Manager, options *OptionsManager, displays *display.Monitor, capture CaptureFunc, sessions *overlay.Controller) any {
	return nil
}
