package settings

import (
	app "github.com/CyberCute-Studio/GameDevTools/internal/shell"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

type Module struct {
	view *View
}

type View struct{}

func New() *Module {
	return &Module{view: &View{}}
}

func (m *Module) ID() app.ModuleID {
	return app.ModuleSettings
}

func (m *Module) NavigationLabel() string {
	return ui.SettingsNavigation
}

func (m *Module) View() app.View {
	return m.view
}

func (v *View) Title() string {
	return ui.SettingsTitle
}

func (v *View) Description() string {
	return ui.SettingsDescription
}

func (v *View) PrimaryAction() string {
	return ""
}
