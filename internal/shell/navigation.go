package shell

import (
	"errors"
	"fmt"
	"reflect"
)

type ModuleID string

const (
	ModuleScreenshot   ModuleID = "screenshot"
	ModuleImageResizer ModuleID = "image-resizer"
	ModuleSettings     ModuleID = "settings"
)

var ErrUnknownModule = errors.New("unknown module")

type View interface {
	Title() string
	Description() string
	PrimaryAction() string
}

type Module interface {
	ID() ModuleID
	NavigationLabel() string
	View() View
}

type Navigator struct {
	modules []Module
	byID    map[ModuleID]Module
	current ModuleID
}

func NewNavigator() *Navigator {
	return &Navigator{byID: make(map[ModuleID]Module)}
}

func (n *Navigator) Register(module Module) error {
	if isNil(module) {
		return errors.New("module is nil")
	}
	if module.ID() == "" {
		return errors.New("module ID is empty")
	}
	if module.NavigationLabel() == "" {
		return fmt.Errorf("module %q has an empty navigation label", module.ID())
	}
	if isNil(module.View()) {
		return fmt.Errorf("module %q has a nil view", module.ID())
	}
	if _, exists := n.byID[module.ID()]; exists {
		return fmt.Errorf("module %q is already registered", module.ID())
	}
	n.modules = append(n.modules, module)
	n.byID[module.ID()] = module
	if len(n.modules) == 1 {
		n.current = module.ID()
	}
	return nil
}

func (n *Navigator) Modules() []Module {
	return append([]Module(nil), n.modules...)
}

func (n *Navigator) Select(id ModuleID) error {
	if _, exists := n.byID[id]; !exists {
		return fmt.Errorf("%w: %q", ErrUnknownModule, id)
	}
	n.current = id
	return nil
}

func (n *Navigator) Current() Module {
	return n.byID[n.current]
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
