package imageresizer

import (
	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	app "github.com/CyberCute-Studio/GameDevTools/internal/shell"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

type Module struct {
	view *View
}

type View struct {
	manager *preset.Manager
	native  any
}

func New(repository *preset.Repository, value *settings.Settings) (*Module, error) {
	manager, err := preset.NewManager(repository, value)
	if err != nil {
		return nil, err
	}
	view := &View{manager: manager}
	view.native = preset.NewNativeManager(manager)
	return &Module{view: view}, nil
}

func (m *Module) ID() app.ModuleID {
	return app.ModuleImageResizer
}

func (m *Module) NavigationLabel() string {
	return ui.ImageResizerNavigation
}

func (m *Module) View() app.View {
	return m.view
}

func (v *View) NativeView() any {
	return v.native
}

func (v *View) Title() string {
	return ui.ImageResizerTitle
}

func (v *View) Description() string {
	return ui.ImageResizerDescription
}

func (v *View) PrimaryAction() string {
	return ui.StartResizeAction
}
