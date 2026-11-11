package shell

import (
	"errors"
	"testing"
)

type testView struct {
	title string
	state int
}

func (v *testView) Title() string         { return v.title }
func (v *testView) Description() string   { return "Description" }
func (v *testView) PrimaryAction() string { return "Run" }

type testModule struct {
	id    ModuleID
	label string
	view  *testView
}

func (m *testModule) ID() ModuleID            { return m.id }
func (m *testModule) NavigationLabel() string { return m.label }
func (m *testModule) View() View              { return m.view }

func TestNavigatorRegisterAndSelect(t *testing.T) {
	navigator := NewNavigator()
	first := &testModule{id: ModuleScreenshot, label: "Screenshot", view: &testView{title: "First"}}
	second := &testModule{id: ModuleImageResizer, label: "Image Resizer", view: &testView{title: "Second"}}
	for _, module := range []*testModule{first, second} {
		if err := navigator.Register(module); err != nil {
			t.Fatalf("Register(%q) error = %v", module.ID(), err)
		}
	}
	if got := navigator.Current(); got != first {
		t.Fatalf("initial Current() = %v, want first module", got)
	}
	if got := len(navigator.Modules()); got != 2 {
		t.Fatalf("len(Modules()) = %d, want 2", got)
	}
	if err := navigator.Select(second.ID()); err != nil {
		t.Fatalf("Select(second) error = %v", err)
	}
	if got := navigator.Current(); got != second {
		t.Fatalf("Current() = %v, want second module", got)
	}
}

func TestNavigatorPreservesModuleViewState(t *testing.T) {
	navigator := NewNavigator()
	first := &testModule{id: ModuleScreenshot, label: "Screenshot", view: &testView{title: "First"}}
	second := &testModule{id: ModuleImageResizer, label: "Image Resizer", view: &testView{title: "Second"}}
	_ = navigator.Register(first)
	_ = navigator.Register(second)
	first.view.state = 42
	for i := 0; i < 20; i++ {
		if err := navigator.Select(second.ID()); err != nil {
			t.Fatal(err)
		}
		if err := navigator.Select(first.ID()); err != nil {
			t.Fatal(err)
		}
	}
	if got := navigator.Current().View().(*testView).state; got != 42 {
		t.Fatalf("view state = %d, want 42", got)
	}
}

func TestNavigatorRejectsInvalidRegistration(t *testing.T) {
	tests := []struct {
		name   string
		module Module
	}{
		{name: "nil", module: nil},
		{name: "empty ID", module: &testModule{label: "Label", view: &testView{}}},
		{name: "empty label", module: &testModule{id: ModuleScreenshot, view: &testView{}}},
		{name: "nil view", module: &testModule{id: ModuleScreenshot, label: "Label"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := NewNavigator().Register(test.module); err == nil {
				t.Fatal("Register() error = nil, want validation error")
			}
		})
	}
}

func TestNavigatorRejectsDuplicateAndUnknownModules(t *testing.T) {
	navigator := NewNavigator()
	module := &testModule{id: ModuleScreenshot, label: "Screenshot", view: &testView{}}
	if err := navigator.Register(module); err != nil {
		t.Fatal(err)
	}
	if err := navigator.Register(module); err == nil {
		t.Fatal("duplicate Register() error = nil")
	}
	if err := navigator.Select(ModuleSettings); !errors.Is(err, ErrUnknownModule) {
		t.Fatalf("Select(unknown) error = %v, want ErrUnknownModule", err)
	}
	if got := navigator.Current(); got != module {
		t.Fatal("failed selection changed the current module")
	}
}

func TestNavigatorModulesReturnsCopy(t *testing.T) {
	navigator := NewNavigator()
	module := &testModule{id: ModuleScreenshot, label: "Screenshot", view: &testView{}}
	if err := navigator.Register(module); err != nil {
		t.Fatal(err)
	}
	modules := navigator.Modules()
	modules[0] = nil
	if navigator.Current() != module {
		t.Fatal("mutating Modules() result changed navigator")
	}
}
