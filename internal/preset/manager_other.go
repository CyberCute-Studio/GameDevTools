//go:build !windows

package preset

func NewNativeManager(manager *Manager) any {
	return nil
}
