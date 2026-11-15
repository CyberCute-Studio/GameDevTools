//go:build !windows

package imageresizer

import "github.com/CyberCute-Studio/GameDevTools/internal/preset"

func NewNativeView(manager *preset.Manager, options *OptionsManager) any {
	return nil
}
