//go:build windows

package screenshot

import (
	"errors"
	"fmt"
	"strconv"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// optionsCommandBase keeps capture-option command identifiers clear of the
// navigation commands (100), the Screenshot preset manager (200), and the
// Image Resizer preset manager (300).
const optionsCommandBase = 400

const (
	modeFreeCommand = iota + 1
	modePresetCommand
	modeFullCommand
	formatCommand
	qualityCommand
	folderCommand
	browseCommand
)

type NativeView struct {
	options *OptionsManager
	presets *preset.NativeManager
	parent  windows.Handle
	browse  func(windows.Handle, string) (string, bool, error)

	modeLabel    *ui.Label
	modeFree     *ui.Button
	modePreset   *ui.Button
	modeFull     *ui.Button
	formatLabel  *ui.Label
	format       *ui.ComboBox
	quality      *ui.TextInput
	folderLabel  *ui.Label
	folder       *ui.ReadOnlyText
	browseButton *ui.Button
	patternLabel *ui.Label
	pattern      *ui.Label
	status       *ui.StatusArea
}

func NewNativeView(manager *preset.Manager, options *OptionsManager) *NativeView {
	return &NativeView{
		options: options,
		presets: preset.NewNativeManager(manager),
		browse:  ui.BrowseForFolder,
	}
}

func (n *NativeView) NativeCreate(parent windows.Handle) error {
	n.parent = parent
	base := int32(optionsCommandBase)
	var err error
	if n.modeLabel, err = ui.NewMnemonicLabel(parent, ui.CaptureModeLabel); err != nil {
		return err
	}
	if n.modeFree, err = ui.NewButton(parent, ui.CaptureModeFreeRectangle, ui.ButtonOptions{ID: base + modeFreeCommand, Group: true, Radio: true}); err != nil {
		return err
	}
	if n.modePreset, err = ui.NewButton(parent, ui.CaptureModePresetRectangle, ui.ButtonOptions{ID: base + modePresetCommand, Radio: true}); err != nil {
		return err
	}
	if n.modeFull, err = ui.NewButton(parent, ui.CaptureModeFullDisplay, ui.ButtonOptions{ID: base + modeFullCommand, Radio: true}); err != nil {
		return err
	}
	if n.formatLabel, err = ui.NewMnemonicLabel(parent, ui.OutputFormatLabel); err != nil {
		return err
	}
	if n.format, err = ui.NewComboBox(parent, base+formatCommand); err != nil {
		return err
	}
	n.format.Add(ui.ImageFormatPNG)
	n.format.Add(ui.ImageFormatJPEG)
	if n.quality, err = ui.NewTextInput(parent, ui.JPEGQualityLabel, base+qualityCommand); err != nil {
		return err
	}
	if n.folderLabel, err = ui.NewMnemonicLabel(parent, ui.OutputFolderLabel); err != nil {
		return err
	}
	if n.folder, err = ui.NewReadOnlyText(parent, base+folderCommand); err != nil {
		return err
	}
	if n.browseButton, err = ui.NewButton(parent, ui.BrowseFolderAction, ui.ButtonOptions{ID: base + browseCommand, Group: true}); err != nil {
		return err
	}
	if n.patternLabel, err = ui.NewLabel(parent, ui.FilenamePatternLabel); err != nil {
		return err
	}
	if n.pattern, err = ui.NewLabel(parent, ""); err != nil {
		return err
	}
	if n.status, err = ui.NewStatusArea(parent); err != nil {
		return err
	}
	if err := n.presets.NativeCreate(parent); err != nil {
		return err
	}
	n.refresh()
	return nil
}

func (n *NativeView) NativeFont(font windows.Handle) {
	n.modeLabel.SetFont(font)
	for _, button := range []*ui.Button{n.modeFree, n.modePreset, n.modeFull, n.browseButton} {
		button.SetFont(font)
	}
	n.formatLabel.SetFont(font)
	n.format.SetFont(font)
	n.quality.SetFont(font)
	n.folderLabel.SetFont(font)
	n.folder.SetFont(font)
	n.patternLabel.SetFont(font)
	n.pattern.SetFont(font)
	n.status.SetFont(font)
	n.presets.NativeFont(font)
}

func (n *NativeView) NativeShow(visible bool) {
	n.modeLabel.Show(visible)
	for _, button := range []*ui.Button{n.modeFree, n.modePreset, n.modeFull, n.browseButton} {
		button.Show(visible)
	}
	n.formatLabel.Show(visible)
	n.format.Show(visible)
	n.quality.Show(visible)
	n.folderLabel.Show(visible)
	n.folder.Show(visible)
	n.patternLabel.Show(visible)
	n.pattern.Show(visible)
	n.status.Label.Show(visible)
	n.presets.NativeShow(visible)
}

func (n *NativeView) NativeHeight(width int32, scale func(int32) int32, measure func(string, int32) int32) int32 {
	own, _, _ := n.optionsSectionHeight(width, scale, measure)
	return own + n.presets.NativeHeight(width, scale, measure)
}

func (n *NativeView) NativeLayout(x, y, width int32, scale func(int32) int32, measure func(string, int32) int32) {
	_, metrics, statusHeight := n.optionsSectionHeight(width, scale, measure)
	n.modeLabel.Move(x, y, width, scale(20))
	radioY := y + scale(24)
	if stackedOptionsLayout(width, scale) {
		n.modeFree.Move(x, radioY, width, scale(26))
		n.modePreset.Move(x, radioY+scale(26), width, scale(26))
		n.modeFull.Move(x, radioY+2*scale(26), width, scale(26))
		y = radioY + 3*scale(26)
	} else {
		column := max(int32(1), width/3)
		n.modeFree.Move(x, radioY, column, scale(26))
		n.modePreset.Move(x+column, radioY, column, scale(26))
		n.modeFull.Move(x+2*column, radioY, max(int32(1), width-2*column), scale(26))
		y = radioY + scale(26)
	}
	y += scale(12)
	n.formatLabel.Move(x, y, width, scale(20))
	n.format.Move(x, y+scale(24), width, scale(200))
	y += scale(66)
	n.quality.Move(x, y, width, metrics)
	y += metrics.Height() + scale(12)
	n.folderLabel.Move(x, y, width, scale(20))
	browseWidth := scale(120)
	gap := scale(8)
	n.folder.Move(x, y+scale(24), max(int32(1), width-browseWidth-gap), scale(28))
	n.browseButton.Move(x+width-browseWidth, y+scale(24), browseWidth, scale(28))
	y += scale(24) + scale(28) + scale(12)
	n.patternLabel.Move(x, y, width, scale(20))
	n.pattern.Move(x, y+scale(24), width, scale(20))
	y += scale(24) + scale(20) + scale(12)
	n.status.Move(x, y, width, statusHeight)
	y += statusHeight + scale(16)
	n.presets.NativeLayout(x, y, width, scale, measure)
}

func (n *NativeView) optionsSectionHeight(width int32, scale func(int32) int32, measure func(string, int32) int32) (int32, ui.DimensionMetrics, int32) {
	metrics := ui.DimensionMetrics{LabelHeight: scale(20), EditHeight: scale(28), ErrorHeight: scale(40), Gap: scale(4)}
	metrics.ErrorHeight = max(metrics.ErrorHeight, measure(n.quality.Error.Text(), width))
	radioRows := int32(1)
	if stackedOptionsLayout(width, scale) {
		radioRows = 3
	}
	height := scale(24) + radioRows*scale(26) + scale(12)
	height += scale(66)
	height += metrics.Height() + scale(12)
	height += scale(24) + scale(28) + scale(12)
	height += scale(24) + scale(20) + scale(12)
	statusHeight := max(scale(48), measure(n.status.Text(), width))
	return height + statusHeight + scale(16), metrics, statusHeight
}

func stackedOptionsLayout(width int32, scale func(int32) int32) bool {
	return width < scale(460)
}

func (n *NativeView) NativeCommand(id int32, notification uint16) bool {
	if n.quality.HandleCommand(id, notification) {
		if notification == ui.NotificationEditKillFocus {
			n.commitQuality()
		}
		return true
	}
	switch id - optionsCommandBase {
	case modeFreeCommand:
		n.selectMode(notification, ModeFreeRectangle)
	case modePresetCommand:
		n.selectMode(notification, ModePresetRectangle)
	case modeFullCommand:
		n.selectMode(notification, ModeFullDisplay)
	case formatCommand:
		if notification != 1 {
			return true
		}
		format := FormatPNG
		label := ui.ImageFormatPNG
		if n.format.Selected() == 1 {
			format = FormatJPEG
			label = ui.ImageFormatJPEG
		}
		err := n.options.SetFormat(format)
		n.refreshFormat()
		n.presentResult(err, fmt.Sprintf(ui.OutputFormatSelected, label))
	case browseCommand:
		if notification != 0 {
			return true
		}
		folder, chosen, err := n.browse(n.parent, ui.BrowseFolderDialogTitle)
		if err != nil {
			_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.OptionsOperationFailed, err)}, 0)
			return true
		}
		if !chosen {
			return true
		}
		err = n.options.SetOutputFolder(folder)
		if err == nil || errors.Is(err, ErrPersistence) {
			n.folder.SetText(n.options.Options().OutputFolder)
		}
		n.presentResult(err, fmt.Sprintf(ui.OutputFolderSaved, folder))
	default:
		return n.presets.NativeCommand(id, notification)
	}
	return true
}

func (n *NativeView) NativeDestroy() {
	n.status.Destroy()
	n.presets.NativeDestroy()
}

func (n *NativeView) selectMode(notification uint16, mode CaptureMode) {
	if notification != 0 || n.options.Options().Mode == mode {
		return
	}
	err := n.options.SetMode(mode)
	n.refreshMode()
	n.presentResult(err, fmt.Sprintf(ui.CaptureModeSelected, captureModeLabel(mode)))
}

func (n *NativeView) commitQuality() {
	quality, err := ParseJPEGQuality(n.quality.Edit.Text())
	if err != nil {
		n.quality.Error.SetText(ui.FormatError(ui.ErrorValidation, err))
		return
	}
	if quality == n.options.Options().JPEGQuality {
		n.quality.Error.SetText("")
		return
	}
	err = n.options.SetJPEGQuality(quality)
	if err == nil || errors.Is(err, ErrPersistence) {
		n.quality.Error.SetText("")
	}
	n.presentResult(err, fmt.Sprintf(ui.JPEGQualitySaved, quality))
}

func (n *NativeView) refresh() {
	options := n.options.Options()
	n.refreshMode()
	formatIndex := 0
	if options.Format == FormatJPEG {
		formatIndex = 1
	}
	n.format.Select(formatIndex)
	n.refreshFormat()
	n.quality.Edit.SetText(strconv.Itoa(options.JPEGQuality))
	n.quality.Error.SetText("")
	n.folder.SetText(options.OutputFolder)
	n.status.Clear()
}

func (n *NativeView) refreshMode() {
	options := n.options.Options()
	n.modeFree.SetChecked(options.Mode == ModeFreeRectangle)
	n.modePreset.SetChecked(options.Mode == ModePresetRectangle)
	n.modeFull.SetChecked(options.Mode == ModeFullDisplay)
}

func (n *NativeView) refreshFormat() {
	options := n.options.Options()
	formatIndex := 0
	if options.Format == FormatJPEG {
		formatIndex = 1
	}
	n.format.Select(formatIndex)
	n.quality.Enable(options.Format == FormatJPEG)
	n.pattern.SetText(options.FilenameExample())
}

func (n *NativeView) presentResult(err error, success string) {
	if err == nil {
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeSuccess, Message: success}, 0)
		return
	}
	if errors.Is(err, ErrPersistence) {
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeWarning, Message: fmt.Sprintf(ui.OptionsSaveFailed, err)}, 0)
		return
	}
	_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.OptionsOperationFailed, err)}, 0)
}

func captureModeLabel(mode CaptureMode) string {
	switch mode {
	case ModePresetRectangle:
		return ui.CaptureModePresetRectangle
	case ModeFullDisplay:
		return ui.CaptureModeFullDisplay
	default:
		return ui.CaptureModeFreeRectangle
	}
}
