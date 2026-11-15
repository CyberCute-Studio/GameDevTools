//go:build windows

package imageresizer

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/decode"
	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	core "github.com/CyberCute-Studio/GameDevTools/internal/shell"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

// optionsCommandBase keeps resize-option command identifiers clear of the
// navigation commands (100), the Screenshot preset manager (200), the Image
// Resizer preset manager (300), and the Screenshot capture options (400).
const optionsCommandBase = 500

// sourcesCommandBase keeps the source-list command identifiers clear of the
// other command ranges.
const sourcesCommandBase = 600

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

const (
	sourceListCommand = iota + 1
	sourcePathCommand
	addImagesCommand
	removeSelectedCommand
	clearListCommand
)

var formatOrder = []OutputFormat{FormatKeepSource, FormatPNG, FormatJPEG}

type NativeView struct {
	options    *OptionsManager
	presets    *preset.NativeManager
	inventory  *Inventory
	inspector  *Inspector
	dispatcher core.Dispatcher
	parent     windows.Handle
	browse     func(windows.Handle, string) (string, bool, error)
	pickFiles  func(windows.Handle, string) ([]string, bool, error)
	// checked tallies the content checks resolved since the list last had
	// nothing left to check, for the summary shown when checking finishes.
	checked checkTally

	sourceLabel     *ui.Label
	sourceHint      *ui.Label
	list            *ui.ListBox
	pathLabel       *ui.Label
	path            *ui.ReadOnlyText
	pathDetail      *ui.Label
	addButton       *ui.Button
	removeButton    *ui.Button
	clearButton     *ui.Button
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

// checkTally accumulates, since the list last had nothing left to check,
// the add results whose checks are pending and the resolved content checks
// by outcome. The finishing summary restates the add results, because small
// files resolve so quickly that the add notice itself is replaced at once.
type checkTally struct {
	added  AddSummary
	ready  int
	failed int
}

func NewNativeView(manager *preset.Manager, options *OptionsManager) *NativeView {
	view := &NativeView{
		options:   options,
		presets:   preset.NewNativeManager(manager),
		inventory: NewInventory(),
		browse:    ui.BrowseForFolder,
		pickFiles: ui.PickImageFiles,
	}
	view.inspector = NewInspector(decode.Inspect, view.deliverInspection)
	return view
}

// NativeDispatcher receives the shell's GUI-thread dispatcher. Content-check
// results are posted through it, so the inventory and the source list are
// only ever updated on the GUI thread (TECH-005).
func (n *NativeView) NativeDispatcher(dispatcher core.Dispatcher) {
	n.dispatcher = dispatcher
}

func (n *NativeView) NativeCreate(parent windows.Handle) error {
	n.parent = parent
	base := int32(optionsCommandBase)
	sources := int32(sourcesCommandBase)
	var err error
	if n.sourceLabel, err = ui.NewMnemonicLabel(parent, ui.SourceImagesLabel); err != nil {
		return err
	}
	if n.sourceHint, err = ui.NewLabel(parent, ui.SourceImagesHint); err != nil {
		return err
	}
	if n.list, err = ui.NewListBox(parent, sources+sourceListCommand); err != nil {
		return err
	}
	if n.pathLabel, err = ui.NewMnemonicLabel(parent, ui.SourcePathLabel); err != nil {
		return err
	}
	if n.path, err = ui.NewReadOnlyText(parent, sources+sourcePathCommand); err != nil {
		return err
	}
	if n.pathDetail, err = ui.NewLabel(parent, ""); err != nil {
		return err
	}
	if n.addButton, err = ui.NewButton(parent, ui.AddImagesAction, ui.ButtonOptions{ID: sources + addImagesCommand, Group: true}); err != nil {
		return err
	}
	if n.removeButton, err = ui.NewButton(parent, ui.RemoveSelectedAction, ui.ButtonOptions{ID: sources + removeSelectedCommand, Disabled: true}); err != nil {
		return err
	}
	if n.clearButton, err = ui.NewButton(parent, ui.ClearListAction, ui.ButtonOptions{ID: sources + clearListCommand, Disabled: true}); err != nil {
		return err
	}
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
	n.sourceLabel.SetFont(font)
	n.sourceHint.SetFont(font)
	n.list.SetFont(font)
	n.pathLabel.SetFont(font)
	n.path.SetFont(font)
	n.pathDetail.SetFont(font)
	for _, button := range []*ui.Button{n.addButton, n.removeButton, n.clearButton} {
		button.SetFont(font)
	}
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
	n.sourceLabel.Show(visible)
	n.sourceHint.Show(visible)
	n.list.Show(visible)
	n.pathLabel.Show(visible)
	n.path.Show(visible)
	n.pathDetail.Show(visible)
	for _, button := range []*ui.Button{n.addButton, n.removeButton, n.clearButton} {
		button.Show(visible)
	}
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
	return n.sourcesSectionHeight(width, scale, measure) + scale(16) + n.presets.NativeHeight(width, scale, measure) + scale(16) + own
}

func (n *NativeView) NativeLayout(x, y, width int32, scale func(int32) int32, measure func(string, int32) int32) {
	_, qualityMetrics, backgroundMetrics, statusHeight := n.optionsSectionHeight(width, scale, measure)
	y = n.layoutSourcesSection(x, y, width, scale, measure) + scale(16)
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

// sourcesSectionHeight mirrors layoutSourcesSection: label, hint, list,
// full-path display, and the Add/Remove/Clear button row(s).
func (n *NativeView) sourcesSectionHeight(width int32, scale func(int32) int32, measure func(string, int32) int32) int32 {
	height := scale(24) + max(scale(20), measure(n.sourceHint.Text(), width)) + scale(8)
	height += scale(140) + scale(12)
	height += scale(24) + scale(28)
	if text := n.pathDetail.Text(); text != "" {
		height += scale(4) + max(scale(20), measure(text, width))
	}
	height += scale(12)
	if stackedOptionsLayout(width, scale) {
		return height + 3*scale(36) + 2*scale(8)
	}
	return height + scale(36)
}

func (n *NativeView) layoutSourcesSection(x, y, width int32, scale func(int32) int32, measure func(string, int32) int32) int32 {
	n.sourceLabel.Move(x, y, width, scale(20))
	hintHeight := max(scale(20), measure(n.sourceHint.Text(), width))
	n.sourceHint.Move(x, y+scale(24), width, hintHeight)
	y += scale(24) + hintHeight + scale(8)
	n.list.Move(x, y, width, scale(140))
	y += scale(140) + scale(12)
	n.pathLabel.Move(x, y, width, scale(20))
	n.path.Move(x, y+scale(24), width, scale(28))
	y += scale(24) + scale(28)
	if text := n.pathDetail.Text(); text != "" {
		detailHeight := max(scale(20), measure(text, width))
		n.pathDetail.Move(x, y+scale(4), width, detailHeight)
		y += scale(4) + detailHeight
	}
	y += scale(12)
	buttons := []*ui.Button{n.addButton, n.removeButton, n.clearButton}
	if stackedOptionsLayout(width, scale) {
		for _, button := range buttons {
			button.Move(x, y, width, scale(36))
			y += scale(36) + scale(8)
		}
		return y - scale(8)
	}
	column := max(int32(1), width/3)
	for index, button := range buttons {
		right := x + int32(index+1)*column
		if index == len(buttons)-1 {
			right = x + width
		}
		button.Move(x+int32(index)*column, y, right-x-int32(index)*column, scale(36))
	}
	return y + scale(36)
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
	switch id - sourcesCommandBase {
	case addImagesCommand:
		if notification != 0 {
			return true
		}
		paths, chosen, err := n.pickFiles(n.parent, ui.ImagePickerTitle)
		if err != nil {
			_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.ImagePickerFailed, err)}, 0)
			return true
		}
		if !chosen {
			return true
		}
		n.addSources(paths)
		return true
	case removeSelectedCommand:
		if notification != 0 {
			return true
		}
		n.removeSelectedSources()
		return true
	case clearListCommand:
		if notification != 0 {
			return true
		}
		n.inventory.Clear()
		n.inspector.DropAll()
		n.checked = checkTally{}
		n.refreshSources()
		// Clearing disables the Clear button itself, so keyboard focus moves
		// to the list instead of being stranded on a disabled control.
		n.list.Focus()
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeSuccess, Message: ui.ImagesCleared}, 0)
		return true
	case sourceListCommand:
		if notification == ui.NotificationListSelChange {
			n.refreshSourceSelection()
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

// NativeDestroy abandons the queued and in-flight content checks, so the
// worker stops at its next read instead of decoding for a closing window.
func (n *NativeView) NativeDestroy() {
	n.inspector.Close()
	n.status.Destroy()
	n.presets.NativeDestroy()
}

// NativeShutdown blocks until the content-check worker has exited. The shell
// calls it only after the message loop has ended and the window is
// destroyed, so the worker never waits on the GUI thread here; NativeDestroy
// has already cancelled its work, and any final dispatcher post fails
// harmlessly against the destroyed window.
func (n *NativeView) NativeShutdown() {
	n.inspector.Close()
	n.inspector.Wait()
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

// NativeDropFiles handles files and folders dropped on the shell window
// while the Image Resizer module is active (RSZ-002, RSZ-008). The shell
// enables drag-and-drop only while the active module accepts it.
func (n *NativeView) NativeDropFiles(paths []string) {
	n.addSources(paths)
}

func (n *NativeView) addSources(paths []string) {
	summary := n.inventory.Add(paths)
	n.refreshSources()
	n.inspector.Enqueue(summary.Inspect)
	if n.inventory.Checking() > 0 {
		n.checked.added.merge(summary)
	}
	n.list.Focus()
	_ = n.status.Show(summarizeAdd(summary), 0)
}

// deliverInspection runs on the inspector's worker goroutine and hands the
// result to the GUI thread. Without a dispatcher — the shell always provides
// one — or once the shell window is gone, the result is dropped: the
// inventory must never be touched off the GUI thread.
func (n *NativeView) deliverInspection(result InspectionResult) {
	if n.dispatcher == nil {
		return
	}
	_ = n.dispatcher.Post(func() {
		n.applyInspection(result)
	})
}

// applyInspection resolves one content check into its item on the GUI
// thread. Only that row is rewritten, so the user's selection and scroll
// position survive background updates; the detail below the full path is
// refreshed when the item is the one it describes. When nothing is left to
// check, the outcome of the checks is summarized (RSZ-004).
func (n *NativeView) applyInspection(result InspectionResult) {
	index, ok := n.inventory.Resolve(result)
	if !ok {
		return
	}
	item := n.inventory.Item(index)
	n.list.SetItemText(index, item.RowText())
	if item.Status == StatusReady {
		n.checked.ready++
	} else {
		n.checked.failed++
	}
	if selection := n.list.SelectedIndices(); len(selection) > 0 && selection[0] == index {
		n.refreshSourceSelection()
	}
	if n.inventory.Checking() > 0 {
		return
	}
	tally := n.checked
	n.checked = checkTally{}
	_ = n.status.Show(summarizeChecks(tally), 0)
}

// summarizeChecks builds the notice shown when the last pending content
// check resolves: the add results since the list was last fully checked,
// then how many images are ready and how many cannot be resized.
func summarizeChecks(tally checkTally) ui.Notice {
	message, warn := describeAdd(tally.added)
	if message != "" {
		message += " "
	}
	message += fmt.Sprintf(ui.ImagesChecked, tally.ready+tally.failed, tally.ready)
	if tally.failed > 0 {
		message += fmt.Sprintf(ui.ImagesCheckedFailed, tally.failed)
		warn = true
	}
	kind := ui.NoticeSuccess
	if warn {
		kind = ui.NoticeWarning
	}
	return ui.Notice{Kind: kind, Message: message}
}

func (n *NativeView) removeSelectedSources() {
	indices := n.list.SelectedIndices()
	if len(indices) == 0 {
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeInfo, Message: ui.ImagesRemoveNone}, 0)
		return
	}
	n.inspector.Drop(n.inventory.Remove(indices))
	if n.inventory.Checking() == 0 {
		n.checked = checkTally{}
	}
	n.refreshSources()
	n.list.Focus()
	_ = n.status.Show(ui.Notice{Kind: ui.NoticeSuccess, Message: fmt.Sprintf(ui.ImagesRemoved, len(indices))}, 0)
}

// summarizeAdd builds the batch result notice: every add reports what was
// added and why other files were not, so unsupported or unreadable files
// never fail silently (RSZ-004).
func summarizeAdd(summary AddSummary) ui.Notice {
	if summary.Added == 0 && summary.Duplicates == 0 && summary.Unsupported == 0 && summary.Inaccessible == 0 {
		return ui.Notice{Kind: ui.NoticeInfo, Message: ui.ImagesAddEmpty}
	}
	message, warn := describeAdd(summary)
	if summary.Added > 0 {
		// The added files are still Checking; the outcome of their content
		// checks is summarized once the last one resolves.
		message += ui.ImagesAddedChecking
	}
	kind := ui.NoticeSuccess
	if warn {
		kind = ui.NoticeWarning
	}
	return ui.Notice{Kind: kind, Message: message}
}

// describeAdd states what one or more adds contributed, and whether any
// file was rejected up front. It is empty when nothing was offered.
func describeAdd(summary AddSummary) (string, bool) {
	if summary.Added == 0 && summary.Duplicates == 0 && summary.Unsupported == 0 && summary.Inaccessible == 0 {
		return "", false
	}
	var message strings.Builder
	fmt.Fprintf(&message, ui.ImagesAdded, summary.Added)
	if summary.Duplicates > 0 {
		fmt.Fprintf(&message, ui.ImagesAddedDuplicates, summary.Duplicates)
	}
	if summary.Unsupported > 0 {
		fmt.Fprintf(&message, ui.ImagesAddedUnsupported, summary.Unsupported)
	}
	if summary.Inaccessible > 0 {
		fmt.Fprintf(&message, ui.ImagesAddedInaccessible, summary.Inaccessible)
	}
	return message.String(), summary.Unsupported > 0 || summary.Inaccessible > 0
}

// refreshSources rebuilds the list from the inventory (RSZ-005): each row
// shows the file name, the upright source dimensions once its content has
// been checked, and its status, and the full path and status detail of the
// selected item appear below on demand.
func (n *NativeView) refreshSources() {
	n.list.Reset()
	for _, item := range n.inventory.Items() {
		n.list.Add(item.RowText())
	}
	n.refreshSourceSelection()
}

func (n *NativeView) refreshSourceSelection() {
	items := n.inventory.Items()
	selection := n.list.SelectedIndices()
	if len(selection) == 0 {
		n.path.SetText("")
		n.pathDetail.SetText("")
	} else {
		item := items[selection[0]]
		n.path.SetText(item.Path)
		n.pathDetail.SetText(item.DetailText())
	}
	n.removeButton.Enable(len(selection) > 0)
	n.clearButton.Enable(len(items) > 0)
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
