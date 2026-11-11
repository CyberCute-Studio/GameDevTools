package settings

import app "github.com/CyberCute-Studio/GameDevTools/internal/shell"

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
	return "Settings"
}

func (m *Module) View() app.View {
	return m.view
}

func (v *View) Title() string {
	return "Settings"
}

func (v *View) Description() string {
	return "Configure application preferences and module options. Versioned local settings will be added by FND-04."
}

func (v *View) PrimaryAction() string {
	return ""
}
