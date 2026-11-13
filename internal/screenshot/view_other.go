//go:build !windows

package screenshot

import (
	"github.com/CyberCute-Studio/GameDevTools/internal/display"
	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
)

func NewNativeView(manager *preset.Manager, options *OptionsManager, displays *display.Monitor, capture CaptureFunc) any {
	return nil
}
