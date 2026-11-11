package imageresizer

import app "github.com/CyberCute-Studio/GameDevTools/internal/shell"

type Module struct {
	view *View
}

type View struct{}

func New() *Module {
	return &Module{view: &View{}}
}

func (m *Module) ID() app.ModuleID {
	return app.ModuleImageResizer
}

func (m *Module) NavigationLabel() string {
	return "Image Resizer"
}

func (m *Module) View() app.View {
	return m.view
}

func (v *View) Title() string {
	return "Image Resizer"
}

func (v *View) Description() string {
	return "Resize PNG and JPEG images with reusable dimensions and fitting modes. Image selection and resize tools will be added by the Image Resizer module items."
}

func (v *View) PrimaryAction() string {
	return "Start Resize"
}
