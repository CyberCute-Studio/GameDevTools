//go:build windows

package preset

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/shell"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

const (
	presetListCommand = iota + 1
	presetNameCommand
	presetWidthCommand
	presetHeightCommand
	presetModeCommand
	presetCreateCommand
	presetUpdateCommand
	presetDuplicateCommand
	presetDeleteCommand
)

type NativeManager struct {
	manager *Manager
	parent  windows.Handle
	confirm func(windows.Handle, string, string) bool

	listLabel *ui.Label
	list      *ui.ComboBox
	name      *ui.TextInput
	width     *ui.DimensionInput
	height    *ui.DimensionInput
	modeLabel *ui.Label
	mode      *ui.ComboBox
	create    *ui.Button
	update    *ui.Button
	duplicate *ui.Button
	remove    *ui.Button
	status    *ui.StatusArea
	listed    []Preset
}

func NewNativeManager(manager *Manager) *NativeManager {
	return &NativeManager{manager: manager, confirm: ui.Confirm}
}

// SelectedPreset returns the preset the library has selected; the Screenshot
// module sizes its Preset Rectangle capture frame from its exact dimensions
// (SCR-020).
func (n *NativeManager) SelectedPreset() (Preset, bool) {
	return n.manager.library.Get(n.manager.library.SelectedID())
}

func (n *NativeManager) NativeCreate(parent windows.Handle) error {
	n.parent = parent
	base := int32(200)
	if n.manager.library.Module() == shell.ModuleImageResizer {
		base = 300
	}
	var err error
	if n.listLabel, err = ui.NewMnemonicLabel(parent, ui.PresetLibraryLabel); err != nil {
		return err
	}
	if n.list, err = ui.NewComboBox(parent, base+presetListCommand); err != nil {
		return err
	}
	if n.name, err = ui.NewTextInput(parent, ui.PresetNameLabel, base+presetNameCommand); err != nil {
		return err
	}
	if n.width, err = ui.NewDimensionInput(parent, ui.PresetWidthLabel, base+presetWidthCommand); err != nil {
		return err
	}
	if n.height, err = ui.NewDimensionInput(parent, ui.PresetHeightLabel, base+presetHeightCommand); err != nil {
		return err
	}
	if n.manager.library.Module() == shell.ModuleImageResizer {
		if n.modeLabel, err = ui.NewMnemonicLabel(parent, ui.PresetResizeModeLabel); err != nil {
			return err
		}
		if n.mode, err = ui.NewComboBox(parent, base+presetModeCommand); err != nil {
			return err
		}
		for _, label := range []string{ui.ResizeModeFit, ui.ResizeModeStretch, ui.ResizeModeCropToFill} {
			n.mode.Add(label)
		}
	}
	if n.create, err = ui.NewButton(parent, ui.PresetCreateAction, ui.ButtonOptions{ID: base + presetCreateCommand, Group: true}); err != nil {
		return err
	}
	if n.update, err = ui.NewButton(parent, ui.PresetRenameAction, ui.ButtonOptions{ID: base + presetUpdateCommand}); err != nil {
		return err
	}
	if n.duplicate, err = ui.NewButton(parent, ui.PresetDuplicateAction, ui.ButtonOptions{ID: base + presetDuplicateCommand}); err != nil {
		return err
	}
	if n.remove, err = ui.NewButton(parent, ui.PresetDeleteAction, ui.ButtonOptions{ID: base + presetDeleteCommand}); err != nil {
		return err
	}
	if n.status, err = ui.NewStatusArea(parent); err != nil {
		return err
	}
	n.refresh()
	return nil
}

func (n *NativeManager) NativeFont(font windows.Handle) {
	n.listLabel.SetFont(font)
	n.list.SetFont(font)
	n.name.SetFont(font)
	n.width.SetFont(font)
	n.height.SetFont(font)
	if n.modeLabel != nil {
		n.modeLabel.SetFont(font)
		n.mode.SetFont(font)
	}
	for _, button := range []*ui.Button{n.create, n.update, n.duplicate, n.remove} {
		button.SetFont(font)
	}
	n.status.SetFont(font)
}

func (n *NativeManager) NativeShow(visible bool) {
	n.listLabel.Show(visible)
	n.list.Show(visible)
	n.name.Show(visible)
	n.width.Show(visible)
	n.height.Show(visible)
	if n.modeLabel != nil {
		n.modeLabel.Show(visible)
		n.mode.Show(visible)
	}
	for _, button := range []*ui.Button{n.create, n.update, n.duplicate, n.remove} {
		button.Show(visible)
	}
	n.status.Label.Show(visible)
}

func (n *NativeManager) NativeHeight(width int32, scale func(int32) int32, measure func(string, int32) int32) int32 {
	nameMetrics, dimensionMetrics, statusHeight := n.metrics(width, scale, measure)
	stacked := stackedPresetLayout(width, scale)
	height := scale(66) + nameMetrics.Height() + scale(12) + dimensionMetrics.Height() + scale(12)
	if stacked {
		height += dimensionMetrics.Height() + scale(12)
	}
	if n.mode != nil {
		height += scale(66)
	}
	buttonHeight := scale(44) + scale(48)
	if stacked {
		buttonHeight += 2 * scale(44)
	}
	return height + buttonHeight + statusHeight
}

func (n *NativeManager) NativeLayout(x, y, width int32, scale func(int32) int32, measure func(string, int32) int32) {
	nameMetrics, dimensionMetrics, statusHeight := n.metrics(width, scale, measure)
	stacked := stackedPresetLayout(width, scale)
	n.listLabel.Move(x, y, width, scale(20))
	n.list.Move(x, y+scale(24), width, scale(200))
	y += scale(66)
	n.name.Move(x, y, width, nameMetrics)
	y += nameMetrics.Height() + scale(12)
	gap := scale(12)
	if stacked {
		n.width.Move(x, y, width, dimensionMetrics)
		y += dimensionMetrics.Height() + scale(12)
		n.height.Move(x, y, width, dimensionMetrics)
	} else {
		columnWidth := max(int32(1), (width-gap)/2)
		n.width.Move(x, y, columnWidth, dimensionMetrics)
		n.height.Move(x+columnWidth+gap, y, max(int32(1), width-columnWidth-gap), dimensionMetrics)
	}
	y += dimensionMetrics.Height() + scale(12)
	if n.mode != nil {
		n.modeLabel.Move(x, y, width, scale(20))
		n.mode.Move(x, y+scale(24), width, scale(200))
		y += scale(66)
	}
	if stacked {
		n.create.Move(x, y, width, scale(36))
		y += scale(44)
		n.update.Move(x, y, width, scale(36))
		y += scale(44)
		n.duplicate.Move(x, y, width, scale(36))
		y += scale(44)
		n.remove.Move(x, y, width, scale(36))
	} else {
		buttonWidth := max(int32(1), (width-gap)/2)
		n.create.Move(x, y, buttonWidth, scale(36))
		n.update.Move(x+buttonWidth+gap, y, max(int32(1), width-buttonWidth-gap), scale(36))
		y += scale(44)
		n.duplicate.Move(x, y, buttonWidth, scale(36))
		n.remove.Move(x+buttonWidth+gap, y, max(int32(1), width-buttonWidth-gap), scale(36))
	}
	y += scale(48)
	n.status.Move(x, y, width, statusHeight)
}

func (n *NativeManager) metrics(width int32, scale func(int32) int32, measure func(string, int32) int32) (ui.DimensionMetrics, ui.DimensionMetrics, int32) {
	baseMetrics := ui.DimensionMetrics{LabelHeight: scale(20), EditHeight: scale(28), ErrorHeight: scale(40), Gap: scale(4)}
	nameMetrics := baseMetrics
	nameMetrics.ErrorHeight = max(nameMetrics.ErrorHeight, measure(n.name.Error.Text(), width))
	dimensionWidth := width
	if !stackedPresetLayout(width, scale) {
		dimensionWidth = max(int32(1), (width-scale(12))/2)
	}
	dimensionMetrics := baseMetrics
	dimensionMetrics.ErrorHeight = max(dimensionMetrics.ErrorHeight, measure(n.width.Error.Text(), dimensionWidth))
	dimensionMetrics.ErrorHeight = max(dimensionMetrics.ErrorHeight, measure(n.height.Error.Text(), dimensionWidth))
	statusHeight := n.status.Height(width, scale, measure)
	return nameMetrics, dimensionMetrics, statusHeight
}

func stackedPresetLayout(width int32, scale func(int32) int32) bool {
	return width < scale(320)
}

func (n *NativeManager) NativeCommand(id int32, notification uint16) bool {
	base := int32(200)
	if n.manager.library.Module() == shell.ModuleImageResizer {
		base = 300
	}
	localID := id - base
	if n.name.HandleCommand(id, notification) || n.width.HandleCommand(id, notification) || n.height.HandleCommand(id, notification) {
		return true
	}
	switch localID {
	case presetListCommand:
		if notification != 1 {
			return true
		}
		selected, exists := n.selected()
		if !exists {
			return true
		}
		err := n.manager.Select(selected.ID)
		n.refresh()
		n.presentResult(err, fmt.Sprintf(ui.PresetSelected, selected.Name))
	case presetCreateCommand:
		if notification != 0 {
			return true
		}
		name, width, height, mode, valid := n.readForm()
		if !valid {
			return true
		}
		created, err := n.manager.Create(name, width, height, mode)
		if err == nil || errors.Is(err, ErrPersistence) {
			n.refresh()
		}
		n.presentResult(err, fmt.Sprintf(ui.PresetCreated, created.Name))
	case presetUpdateCommand:
		if notification != 0 {
			return true
		}
		selected, exists := n.selected()
		if !exists {
			return true
		}
		name, width, height, mode, valid := n.readForm()
		if !valid {
			return true
		}
		err := n.manager.Update(selected.ID, name, width, height, mode)
		if err == nil || errors.Is(err, ErrPersistence) {
			n.refresh()
		}
		n.presentResult(err, fmt.Sprintf(ui.PresetUpdated, name))
	case presetDuplicateCommand:
		if notification != 0 {
			return true
		}
		selected, exists := n.selected()
		if !exists {
			return true
		}
		duplicated, err := n.manager.Duplicate(selected.ID)
		if err == nil || errors.Is(err, ErrPersistence) {
			n.refresh()
		}
		n.presentResult(err, fmt.Sprintf(ui.PresetDuplicated, duplicated.Name))
	case presetDeleteCommand:
		if notification != 0 {
			return true
		}
		selected, exists := n.selected()
		if !exists {
			return true
		}
		if !selected.BuiltIn && !n.confirm(n.parent, ui.PresetDeleteTitle, fmt.Sprintf(ui.PresetDeleteConfirmation, selected.Name)) {
			return true
		}
		err := n.manager.Delete(selected.ID)
		if err == nil || errors.Is(err, ErrPersistence) {
			n.refresh()
			n.list.Focus()
		}
		n.presentResult(err, fmt.Sprintf(ui.PresetDeleted, selected.Name))
	default:
		return false
	}
	return true
}

func (n *NativeManager) NativeDestroy() {
	if n.status != nil {
		n.status.Destroy()
	}
}

// Reload shows the preset library the shared settings value stores once the
// default settings are restored (SET-005), when it is not the library shown,
// as when the session started from the defaults because the settings file
// could not be read and the restore kept the file's presets. A library that
// did not change is left as it is shown, with the form's unsaved edits
// (APP-004). A library that cannot be loaded is reported in the status area
// and returned, for the restore's outcome to report too, as the status area
// is hidden while the Settings view is shown.
func (n *NativeManager) Reload() error {
	changed, err := n.manager.Reload()
	if err != nil {
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.PresetOperationFailed, err)}, 0)
		return err
	}
	if changed {
		n.refresh()
	}
	return nil
}

func (n *NativeManager) refresh() {
	n.listed = n.manager.library.Presets()
	n.list.Reset()
	selectedIndex := 0
	for index, candidate := range n.listed {
		suffix := ""
		if candidate.BuiltIn {
			suffix = ui.PresetBuiltInSuffix
		}
		n.list.Add(fmt.Sprintf(ui.PresetListFormat, candidate.Name, ui.FormatDimensions(candidate.Width, candidate.Height), suffix))
		if candidate.ID == n.manager.library.SelectedID() {
			selectedIndex = index
		}
	}
	n.list.Select(selectedIndex)
	n.loadForm(n.listed[selectedIndex])
}

func (n *NativeManager) loadForm(selected Preset) {
	n.name.Edit.SetText(selected.Name)
	n.width.Edit.SetText(strconv.Itoa(selected.Width))
	n.height.Edit.SetText(strconv.Itoa(selected.Height))
	n.name.Error.SetText("")
	n.width.Error.SetText("")
	n.height.Error.SetText("")
	if n.mode != nil {
		modeIndex := 0
		switch selected.ResizeMode {
		case ResizeStretch:
			modeIndex = 1
		case ResizeCropToFill:
			modeIndex = 2
		}
		n.mode.Select(modeIndex)
	}
	n.update.Enable(!selected.BuiltIn)
	n.remove.Enable(!selected.BuiltIn)
	n.status.Clear()
}

func (n *NativeManager) selected() (Preset, bool) {
	index := n.list.Selected()
	if index < 0 || index >= len(n.listed) {
		return Preset{}, false
	}
	return n.listed[index], true
}

func (n *NativeManager) readForm() (string, int, int, ResizeMode, bool) {
	name := strings.TrimSpace(n.name.Edit.Text())
	if name == "" {
		n.name.Error.SetText(ui.FormatError(ui.ErrorValidation, errors.New(ui.PresetNameRequired)))
		n.name.Focus()
		return "", 0, 0, "", false
	}
	n.name.Error.SetText("")
	shown := n.width.Error.Text()
	width, err := n.width.Validate()
	if err != nil {
		refuseDimension(n.width, shown)
		return "", 0, 0, "", false
	}
	if !ValidDimension(width) {
		n.width.Error.SetText(ui.FormatError(ui.ErrorValidation, fmt.Errorf(ui.PresetDimensionRange, ui.DimensionWidthName)))
		n.width.Focus()
		return "", 0, 0, "", false
	}
	shown = n.height.Error.Text()
	height, err := n.height.Validate()
	if err != nil {
		refuseDimension(n.height, shown)
		return "", 0, 0, "", false
	}
	if !ValidDimension(height) {
		n.height.Error.SetText(ui.FormatError(ui.ErrorValidation, fmt.Errorf(ui.PresetDimensionRange, ui.DimensionHeightName)))
		n.height.Focus()
		return "", 0, 0, "", false
	}
	var mode ResizeMode
	if n.mode != nil {
		switch n.mode.Selected() {
		case 0:
			mode = ResizeFit
		case 1:
			mode = ResizeStretch
		case 2:
			mode = ResizeCropToFill
		}
		if !WithinResizePixelLimit(width, height) {
			n.height.Error.SetText(ui.FormatError(ui.ErrorValidation, errors.New(ui.PresetResizePixelLimit)))
			n.height.Focus()
			return "", 0, 0, "", false
		}
	}
	return name, width, height, mode, true
}

// refuseDimension moves the focus to a dimension field whose text refused
// Create or Rename / Save and has a screen reader read the field's message
// (UX-003): a refusal is news each time the user asks, as each of the
// capture toolbar's refusals is. The field validates as it changes and as it
// loses the focus, so Validate here usually sets the message already shown,
// which a live label does not announce again; shown is the message before
// Validate, so a message Validate changed, and so announced, is not
// announced twice.
func refuseDimension(field *ui.DimensionInput, shown string) {
	field.Focus()
	if field.Error.Text() == shown {
		field.Error.Announce()
	}
}

func (n *NativeManager) presentResult(err error, success string) {
	if err == nil {
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeSuccess, Message: success}, 0)
		return
	}
	if errors.Is(err, ErrDuplicateName) {
		n.name.Error.SetText(ui.FormatError(ui.ErrorValidation, err))
		n.name.Focus()
		return
	}
	if errors.Is(err, ErrPersistence) {
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeWarning, Message: fmt.Sprintf(ui.PresetSaveFailed, err)}, 0)
		return
	}
	_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.PresetOperationFailed, err)}, 0)
}
