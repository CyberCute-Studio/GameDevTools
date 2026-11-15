//go:build windows

package imageresizer

import (
	"errors"
	"fmt"
	"strconv"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// optionsCommandBase keeps resize-option command identifiers clear of the
// navigation commands (100), the Screenshot preset manager (200), the Image
// Resizer preset manager (300), and the Screenshot capture options (400).
const optionsCommandBase = 500

const (
	formatCommand = iota + 1
	qualityCommand
	backgroundCommand
	folderCommand
	browseCommand
	collisionRenameCommand
	collisionSkipCommand
	collisionOverwriteCommand
)

var formatOrder = []OutputFormat{FormatKeepSource, FormatPNG, FormatJPEG}

type NativeView struct {
	options *OptionsManager
	presets *preset.NativeManager
	parent  windows.Handle
	browse  func(windows.Handle, string) (string, bool, error)

	modeLabel       *ui.Label
	modeDescription *ui.Label
	folderLabel     *ui.Label
	folder          *ui.ReadOnlyText
	browseButton    *ui.Button
	formatLabel     *ui.Label
	format          *ui.ComboBox
	quality         *ui.TextInput
	background      *ui.TextInput
	collisionLabel  *ui.Label
	collisionRename *ui.Button
	collisionSkip   *ui.Button
	collisionOver   *ui.Button
	collisionNote   *ui.Label
	namingLabel     *ui.Label
	naming          *ui.Label
	status          *ui.StatusArea
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
	if n.modeLabel, err = ui.NewLabel(parent, ui.ResizeModeSummaryLabel); err != nil {
		return err
	}
	if n.modeDescription, err = ui.NewLabel(parent, ""); err != nil {
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
	if n.formatLabel, err = ui.NewMnemonicLabel(parent, ui.ResizeFormatLabel); err != nil {
		return err
	}
	if n.format, err = ui.NewComboBox(parent, base+formatCommand); err != nil {
		return err
	}
	n.format.Add(ui.ResizeFormatKeepSource)
	n.format.Add(ui.ImageFormatPNG)
	n.format.Add(ui.ImageFormatJPEG)
	if n.quality, err = ui.NewTextInput(parent, ui.JPEGQualityLabel, base+qualityCommand); err != nil {
		return err
	}
	if n.background, err = ui.NewTextInput(parent, ui.ResizeBackgroundLabel, base+backgroundCommand); err != nil {
		return err
	}
	if n.collisionLabel, err = ui.NewMnemonicLabel(parent, ui.ResizeCollisionLabel); err != nil {
		return err
	}
	if n.collisionRename, err = ui.NewButton(parent, ui.ResizeCollisionRename, ui.ButtonOptions{ID: base + collisionRenameCommand, Group: true, Radio: true}); err != nil {
		return err
	}
	if n.collisionSkip, err = ui.NewButton(parent, ui.ResizeCollisionSkip, ui.ButtonOptions{ID: base + collisionSkipCommand, Radio: true}); err != nil {
		return err
	}
	if n.collisionOver, err = ui.NewButton(parent, ui.ResizeCollisionOverwrite, ui.ButtonOptions{ID: base + collisionOverwriteCommand, Radio: true}); err != nil {
		return err
	}
	if n.collisionNote, err = ui.NewLabel(parent, ""); err != nil {
		return err
	}
	if n.namingLabel, err = ui.NewLabel(parent, ui.ResizeNamingLabel); err != nil {
		return err
	}
	if n.naming, err = ui.NewLabel(parent, ui.ResizeNamingBehavior); err != nil {
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
	n.modeDescription.SetFont(font)
	n.folderLabel.SetFont(font)
	n.folder.SetFont(font)
	n.browseButton.SetFont(font)
	n.formatLabel.SetFont(font)
	n.format.SetFont(font)
	n.quality.SetFont(font)
	n.background.SetFont(font)
	n.collisionLabel.SetFont(font)
	for _, button := range []*ui.Button{n.collisionRename, n.collisionSkip, n.collisionOver} {
		button.SetFont(font)
	}
	n.collisionNote.SetFont(font)
	n.namingLabel.SetFont(font)
	n.naming.SetFont(font)
	n.status.SetFont(font)
	n.presets.NativeFont(font)
}

func (n *NativeView) NativeShow(visible bool) {
	n.modeLabel.Show(visible)
	n.modeDescription.Show(visible)
	n.folderLabel.Show(visible)
	n.folder.Show(visible)
	n.browseButton.Show(visible)
	n.formatLabel.Show(visible)
	n.format.Show(visible)
	n.quality.Show(visible)
	n.background.Show(visible)
	n.collisionLabel.Show(visible)
	for _, button := range []*ui.Button{n.collisionRename, n.collisionSkip, n.collisionOver} {
		button.Show(visible)
	}
	n.collisionNote.Show(visible)
	n.namingLabel.Show(visible)
	n.naming.Show(visible)
	n.status.Label.Show(visible)
	n.presets.NativeShow(visible)
}

func (n *NativeView) NativeHeight(width int32, scale func(int32) int32, measure func(string, int32) int32) int32 {
	own, _, _, _ := n.optionsSectionHeight(width, scale, measure)
	return n.presets.NativeHeight(width, scale, measure) + scale(16) + own
}

func (n *NativeView) NativeLayout(x, y, width int32, scale func(int32) int32, measure func(string, int32) int32) {
	_, qualityMetrics, backgroundMetrics, statusHeight := n.optionsSectionHeight(width, scale, measure)
	n.presets.NativeLayout(x, y, width, scale, measure)
	y += n.presets.NativeHeight(width, scale, measure) + scale(16)
	n.modeLabel.Move(x, y, width, scale(20))
	modeHeight := max(scale(20), measure(n.modeDescription.Text(), width))
	n.modeDescription.Move(x, y+scale(24), width, modeHeight)
	y += scale(24) + modeHeight + scale(12)
	n.folderLabel.Move(x, y, width, scale(20))
	browseWidth := scale(120)
	gap := scale(8)
	n.folder.Move(x, y+scale(24), max(int32(1), width-browseWidth-gap), scale(28))
	n.browseButton.Move(x+width-browseWidth, y+scale(24), browseWidth, scale(28))
	y += scale(24) + scale(28) + scale(12)
	n.formatLabel.Move(x, y, width, scale(20))
	n.format.Move(x, y+scale(24), width, scale(200))
	y += scale(66)
	n.quality.Move(x, y, width, qualityMetrics)
	y += qualityMetrics.Height() + scale(12)
	n.background.Move(x, y, width, backgroundMetrics)
	y += backgroundMetrics.Height() + scale(12)
	n.collisionLabel.Move(x, y, width, scale(20))
	radioY := y + scale(24)
	if stackedOptionsLayout(width, scale) {
		n.collisionRename.Move(x, radioY, width, scale(26))
		n.collisionSkip.Move(x, radioY+scale(26), width, scale(26))
		n.collisionOver.Move(x, radioY+2*scale(26), width, scale(26))
		y = radioY + 3*scale(26)
	} else {
		column := max(int32(1), width/3)
		n.collisionRename.Move(x, radioY, column, scale(26))
		n.collisionSkip.Move(x+column, radioY, column, scale(26))
		n.collisionOver.Move(x+2*column, radioY, max(int32(1), width-2*column), scale(26))
		y = radioY + scale(26)
	}
	y += scale(4)
	noteHeight := max(scale(20), measure(n.collisionNote.Text(), width))
	n.collisionNote.Move(x, y, width, noteHeight)
	y += noteHeight + scale(12)
	n.namingLabel.Move(x, y, width, scale(20))
	namingHeight := max(scale(20), measure(n.naming.Text(), width))
	n.naming.Move(x, y+scale(24), width, namingHeight)
	y += scale(24) + namingHeight + scale(12)
	n.status.Move(x, y, width, statusHeight)
}

func (n *NativeView) optionsSectionHeight(width int32, scale func(int32) int32, measure func(string, int32) int32) (int32, ui.DimensionMetrics, ui.DimensionMetrics, int32) {
	// Each text input reserves its own error height, so a wrapped message
	// under one field does not inflate the row of the other.
	quality := ui.DimensionMetrics{LabelHeight: scale(20), EditHeight: scale(28), ErrorHeight: scale(40), Gap: scale(4)}
	quality.ErrorHeight = max(quality.ErrorHeight, measure(n.quality.Error.Text(), width))
	background := quality
	background.ErrorHeight = max(scale(40), measure(n.background.Error.Text(), width))
	radioRows := int32(1)
	if stackedOptionsLayout(width, scale) {
		radioRows = 3
	}
	height := scale(24) + max(scale(20), measure(n.modeDescription.Text(), width)) + scale(12)
	height += scale(24) + scale(28) + scale(12)
	height += scale(66)
	height += quality.Height() + scale(12)
	height += background.Height() + scale(12)
	height += scale(24) + radioRows*scale(26) + scale(4)
	height += max(scale(20), measure(n.collisionNote.Text(), width)) + scale(12)
	height += scale(24) + max(scale(20), measure(n.naming.Text(), width)) + scale(12)
	statusHeight := max(scale(48), measure(n.status.Text(), width))
	return height + statusHeight, quality, background, statusHeight
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
	if n.background.HandleCommand(id, notification) {
		if notification == ui.NotificationEditKillFocus {
			n.commitBackground()
		}
		return true
	}
	switch id - optionsCommandBase {
	case formatCommand:
		if notification != 1 {
			return true
		}
		index := n.format.Selected()
		if index < 0 || index >= len(formatOrder) {
			return true
		}
		format := formatOrder[index]
		err := n.options.SetFormat(format)
		n.refreshFormat()
		if format == FormatKeepSource {
			n.presentResult(err, ui.ResizeFormatKeepSelected)
		} else {
			n.presentResult(err, fmt.Sprintf(ui.ResizeFormatSelected, resizeFormatLabel(format)))
		}
	case browseCommand:
		if notification != 0 {
			return true
		}
		folder, chosen, err := n.browse(n.parent, ui.BrowseResizeFolderTitle)
		if err != nil {
			_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.ResizeOperationFailed, err)}, 0)
			return true
		}
		if !chosen {
			return true
		}
		err = n.options.SetOutputFolder(folder)
		if err == nil || errors.Is(err, ErrPersistence) {
			n.folder.SetText(n.options.Options().OutputFolder)
		}
		n.presentResult(err, fmt.Sprintf(ui.ResizeOutputFolderSaved, folder))
	case collisionRenameCommand:
		n.selectCollision(notification, CollisionRename)
	case collisionSkipCommand:
		n.selectCollision(notification, CollisionSkip)
	case collisionOverwriteCommand:
		n.selectCollision(notification, CollisionOverwrite)
	default:
		if n.presets.NativeCommand(id, notification) {
			// A selection, creation, or edit can change the preset the next
			// job would use, so its plain-language fitting-mode consequence
			// (RSZ-016) is refreshed after every preset operation.
			n.refreshModeDescription()
			return true
		}
		return false
	}
	return true
}

func (n *NativeView) NativeDestroy() {
	n.status.Destroy()
	n.presets.NativeDestroy()
}

func (n *NativeView) selectCollision(notification uint16, policy CollisionPolicy) {
	if notification != 0 || n.options.Options().Collision == policy {
		return
	}
	err := n.options.SetCollision(policy)
	n.refreshCollision()
	n.presentResult(err, fmt.Sprintf(ui.ResizeCollisionSelected, collisionPolicyLabel(policy)))
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

func (n *NativeView) commitBackground() {
	background, err := ParseBackground(n.background.Edit.Text())
	if err != nil {
		n.background.Error.SetText(ui.FormatError(ui.ErrorValidation, err))
		return
	}
	if background == n.options.Options().Background {
		n.background.Error.SetText("")
		n.background.Edit.SetText(background)
		return
	}
	err = n.options.SetBackground(background)
	if err == nil || errors.Is(err, ErrPersistence) {
		n.background.Error.SetText("")
		n.background.Edit.SetText(background)
	}
	n.presentResult(err, fmt.Sprintf(ui.ResizeBackgroundSaved, background))
}

func (n *NativeView) refresh() {
	options := n.options.Options()
	n.refreshFormat()
	n.quality.Edit.SetText(strconv.Itoa(options.JPEGQuality))
	n.quality.Error.SetText("")
	n.background.Edit.SetText(options.Background)
	n.background.Error.SetText("")
	n.folder.SetText(options.OutputFolder)
	n.refreshCollision()
	n.refreshModeDescription()
	n.status.Clear()
}

func (n *NativeView) refreshFormat() {
	options := n.options.Options()
	formatIndex := 0
	for index, candidate := range formatOrder {
		if candidate == options.Format {
			formatIndex = index
		}
	}
	// The combo re-selects the stored format so a rejected change — the save
	// validation still fails while no output folder is set — cannot leave the
	// control showing a format that was never applied.
	n.format.Select(formatIndex)
	n.quality.Enable(options.Format != FormatPNG)
	n.background.Enable(options.Format != FormatPNG)
}

func (n *NativeView) refreshCollision() {
	options := n.options.Options()
	n.collisionRename.SetChecked(options.Collision == CollisionRename)
	n.collisionSkip.SetChecked(options.Collision == CollisionSkip)
	n.collisionOver.SetChecked(options.Collision == CollisionOverwrite)
	n.collisionNote.SetText(collisionPolicyDescription(options.Collision))
}

// refreshModeDescription presents the selected preset's fitting mode and its
// consequence in plain language before any job can start (RSZ-016), including
// the Stretch distortion warning (RSZ-010).
func (n *NativeView) refreshModeDescription() {
	selected, exists := n.presets.SelectedPreset()
	if !exists {
		n.modeDescription.SetText("")
		return
	}
	format := ui.ResizeModeFitDescription
	switch selected.ResizeMode {
	case preset.ResizeStretch:
		format = ui.ResizeModeStretchDescription
	case preset.ResizeCropToFill:
		format = ui.ResizeModeCropDescription
	}
	n.modeDescription.SetText(fmt.Sprintf(format, selected.Width, selected.Height))
}

func (n *NativeView) presentResult(err error, success string) {
	if err == nil {
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeSuccess, Message: success}, 0)
		return
	}
	if errors.Is(err, ErrPersistence) {
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeWarning, Message: fmt.Sprintf(ui.ResizeOptionsSaveFailed, err)}, 0)
		return
	}
	_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.ResizeOperationFailed, err)}, 0)
}

func resizeFormatLabel(format OutputFormat) string {
	if format == FormatJPEG {
		return ui.ImageFormatJPEG
	}
	return ui.ImageFormatPNG
}

func collisionPolicyLabel(policy CollisionPolicy) string {
	switch policy {
	case CollisionSkip:
		return ui.ResizeCollisionSkip
	case CollisionOverwrite:
		return ui.ResizeCollisionOverwrite
	default:
		return ui.ResizeCollisionRename
	}
}

func collisionPolicyDescription(policy CollisionPolicy) string {
	switch policy {
	case CollisionSkip:
		return ui.ResizeCollisionSkipDescription
	case CollisionOverwrite:
		return ui.ResizeCollisionOverwriteDescription
	default:
		return ui.ResizeCollisionRenameDescription
	}
}
