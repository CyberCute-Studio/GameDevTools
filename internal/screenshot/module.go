package screenshot

import app "github.com/CyberCute-Studio/GameDevTools/internal/shell"

type Module struct {
	view *View
}

type View struct{}

func New() *Module {
	return &Module{view: &View{}}
}

func (m *Module) ID() app.ModuleID {
	return app.ModuleScreenshot
}

func (m *Module) NavigationLabel() string {
	return "Screenshot"
}

func (m *Module) View() app.View {
	return m.view
}

func (v *View) Title() string {
	return "Screenshot"
}

func (v *View) Description() string {
	return "Capture a free rectangle, an exact preset rectangle, or one full display. Capture tools will be added by the Screenshot module items."
}

func (v *View) PrimaryAction() string {
	return "Start Capture"
}
