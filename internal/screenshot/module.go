package screenshot

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
	return app.ModuleScreenshot
}

func (m *Module) NavigationLabel() string {
	return ui.ScreenshotNavigation
}

func (m *Module) View() app.View {
	return m.view
}

func (v *View) Title() string {
	return ui.ScreenshotTitle
}

func (v *View) Description() string {
	return ui.ScreenshotDescription
}

func (v *View) PrimaryAction() string {
	return ui.StartCaptureAction
}
