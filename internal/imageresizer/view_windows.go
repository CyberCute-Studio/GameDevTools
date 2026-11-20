//go:build windows

package imageresizer

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/decode"
	"github.com/CyberCute-Studio/GameDevTools/internal/imageresizer/job"
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
	// results carries finished content checks from the inspector's worker
	// to the GUI thread.
	results resultMailbox
	// checked tallies the content checks resolved since the list last had
	// nothing left to check, for the summary shown when checking finishes.
	checked checkTally
	// sourceNotice is the status area's revision right after the source list
	// last showed its notice. The finishing summary replaces only that
	// notice, or an area left empty, never a notice another part of the view
	// raised in the meantime.
	sourceNotice uint64
	// busyNotice is the status area's revision right after it last said that
	// the source list cannot change, or Start Resize run, while a job runs,
	// which finishJob clears while the area still shows it, or 0.
	busyNotice uint64

	// run runs a resize job, confirm asks for an Overwrite job's
	// confirmation, and openFolder opens the output folder on request; tests
	// substitute them.
	run        jobRunner
	confirm    func(windows.Handle, string, string) bool
	openFolder func(string) error
	// active is the running job, or nil while none runs.
	active *activeJob
	// jobs counts the job goroutines that have not returned, including those
	// of jobs the view let go, for NativeShutdown.
	jobs sync.WaitGroup
	// releaseDelay is how long a cancelled job may take to stop before the
	// view lets it go, jobReleaseDelay; tests shorten it.
	releaseDelay time.Duration
	// outputFolder is the folder Open Output Folder opens: the latest job's
	// that saved a result, once it saved one, or empty.
	outputFolder string

	jobStatus    *ui.StatusArea
	cancelButton *ui.Button
	openButton   *ui.Button

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

// checkTally marks the adds made since the list last had nothing left to
// check. The finishing summary restates their results, because small files
// resolve so quickly that the add notice itself is replaced at once. The
// items those adds listed are exactly the ones from first on, since IDs only
// grow and removal keeps the order, so the summary reads them — and only
// those still listed — from the inventory.
type checkTally struct {
	// first is the ID of the first item the adds listed, or 0 when no add
	// is being tallied.
	first uint64
	// duplicates holds, for each path the adds skipped as already listed,
	// the ID of the item listing it.
	duplicates []uint64
}

func NewNativeView(manager *preset.Manager, options *OptionsManager) *NativeView {
	view := &NativeView{
		options:      options,
		presets:      preset.NewNativeManager(manager),
		inventory:    NewInventory(),
		browse:       ui.BrowseForFolder,
		pickFiles:    ui.PickImageFiles,
		run:          job.Run,
		confirm:      ui.Confirm,
		openFolder:   ui.OpenFolder,
		releaseDelay: jobReleaseDelay,
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
	// The job section comes first, directly below the shell's Start Resize
	// button, in layout and in tab order.
	if err := n.createJobControls(parent); err != nil {
		return err
	}
	var err error
	// The hint is created ahead of the label, though laid out below it, so
	// the label directly precedes the list: Windows names a list box after
	// the static text just before it, which a screen reader announces with
	// the list, and the label's mnemonic focuses the next tab stop.
	if n.sourceHint, err = ui.NewLabel(parent, ui.SourceImagesHint); err != nil {
		return err
	}
	if n.sourceLabel, err = ui.NewMnemonicLabel(parent, ui.SourceImagesLabel); err != nil {
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
	// The detail line says why the selected image cannot be used or how its
	// resize ended, which its row abbreviates, so a screen reader announces
	// it as the selection moves.
	if n.pathDetail, err = ui.NewLiveLabel(parent, ""); err != nil {
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
	// The note starts a group, ending the collision policies' radio group at
	// Overwrite, so the arrow keys wrap between the policies
	// (ui.NewGroupLabel).
	if n.collisionNote, err = ui.NewGroupLabel(parent, ""); err != nil {
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
	n.jobStatus.SetFont(font)
	n.cancelButton.SetFont(font)
	n.openButton.SetFont(font)
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
	n.jobStatus.Label.Show(visible)
	n.cancelButton.Show(visible)
	n.openButton.Show(visible)
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
	return n.jobSectionHeight(width, scale, measure) + scale(16) + n.sourcesSectionHeight(width, scale, measure) + scale(16) + n.presets.NativeHeight(width, scale, measure) + scale(16) + own
}

func (n *NativeView) NativeLayout(x, y, width int32, scale func(int32) int32, measure func(string, int32) int32) {
	_, qualityMetrics, backgroundMetrics, statusHeight := n.optionsSectionHeight(width, scale, measure)
	y = n.layoutJobSection(x, y, width, scale, measure) + scale(16)
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
	if n.jobCommand(id, notification) {
		return true
	}
	switch id - sourcesCommandBase {
	case addImagesCommand:
		// The picker does not open while a job runs; addSources checks again
		// once it closes, as its modal loop dispatches messages.
		if notification != 0 || n.refuseWhileRunning() {
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
		if notification == 0 {
			n.removeSelectedSources()
		}
		return true
	case clearListCommand:
		if notification == 0 {
			n.clearSources()
		}
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
// worker stops at its next read instead of decoding for a closing window,
// and cancels a running job, whose items in flight stop at their next safe
// point and keep every completed result (RSZ-045). Content checks are not
// waited for: a check only reads its source, so nothing is lost when the
// process exits around one, and waiting for the worker could hold the
// process — and its single-instance lock — for as long as an open or read
// on a slow network share or an online-only file blocks. NativeShutdown
// waits, within a bound, for the job. Any result a worker or the job still
// finishes fails to post harmlessly against the destroyed window, and the
// post is not retried, as closing the inspector marks the view destroyed.
func (n *NativeView) NativeDestroy() {
	n.inspector.Close()
	if n.active != nil {
		n.active.cancel()
	}
	n.status.Destroy()
	n.jobStatus.Destroy()
	n.presets.NativeDestroy()
}

// destroyed reports whether NativeDestroy has run, which closes the
// inspector, so a refused post of a drain task, which can then only fail
// again, is not retried.
func (n *NativeView) destroyed() bool {
	return n.inspector.Closed()
}

// refuseWhileRunning explains, while a job runs, that the source list cannot
// change, as its rows are the job's items, and reports whether it did, as
// refuseBusy describes. Every change to the list — Add Images, a drop,
// Remove Selected, and Clear List — goes through addSources,
// removeSelectedSources, or clearSources, which each refuse it first.
func (n *NativeView) refuseWhileRunning() bool {
	return n.refuseBusy(ui.ImagesBusy)
}

// refuseBusy explains in the status area, while a job runs, why what was
// asked cannot run until the job ends — a change to the source list, or
// Start Resize again — and reports whether it did. The job section keeps
// the job's progress, and the job's end clears the explanation while the
// status area still shows it.
func (n *NativeView) refuseBusy(message string) bool {
	if n.active == nil {
		return false
	}
	n.showSourceNotice(ui.Notice{Kind: ui.NoticeWarning, Message: message})
	n.busyNotice = n.status.Revision()
	return true
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
	n.refreshOptions()
	n.refreshModeDescription()
	n.status.Clear()
}

// refreshOptions shows the resize options in their controls, clearing the
// JPEG fields' messages.
func (n *NativeView) refreshOptions() {
	options := n.options.Options()
	n.refreshFormat()
	n.quality.Edit.SetText(strconv.Itoa(options.JPEGQuality))
	n.quality.Error.SetText("")
	n.background.Edit.SetText(options.Background)
	n.background.Error.SetText("")
	n.folder.SetText(options.OutputFolder)
	n.refreshCollision()
}

// NativeRestoreDefaults shows the resize options the restored default
// settings hold (SET-005). The source list, its status, a running job and
// its progress, the selected preset, and the preset form are kept (APP-004):
// a job runs on the options and preset it started with. The preset library
// is shown again only when the restore kept another than the one shown, as
// when the session never loaded the settings file it replaced
// (preset.NativeManager.Reload), and the fitting mode's description then
// describes the preset it selects (RSZ-016). A library or options that could
// not be shown are returned for the Settings view to report with the
// restore's outcome; an output folder that has no default is left to the
// shell's note, which gives the reason startup found.
func (n *NativeView) NativeRestoreDefaults() string {
	var followUps []string
	if err := n.presets.Reload(); err != nil {
		followUps = append(followUps, fmt.Sprintf(ui.PresetOperationFailed, err))
	}
	n.refreshModeDescription()
	if err := n.options.Reload(); err != nil {
		message := fmt.Sprintf(ui.ResizeOperationFailed, err)
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: message}, 0)
		followUps = append(followUps, message)
	} else {
		n.refreshOptions()
	}
	return strings.Join(followUps, " ")
}

// NativeCloseConfirmation returns the question closing the application asks
// first while a resize job runs, as closing cancels it, or "" for the
// message when no job runs or the user already cancelled it, and nothing
// that has not been asked for would be lost.
func (n *NativeView) NativeCloseConfirmation() (title, message string) {
	if n.active == nil || n.active.cancelling {
		return "", ""
	}
	return ui.ResizeCloseTitle, ui.ResizeCloseConfirmation
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

// NativeDropAvailable reports drops unavailable while a job runs, as the
// source list cannot change then, so the shell keeps the no-drop cursor
// instead of seeming to accept a drop the view would refuse.
func (n *NativeView) NativeDropAvailable() bool {
	return n.active == nil
}

// NativeDropFiles handles files and folders dropped on the shell window
// while the Image Resizer module is active (RSZ-002, RSZ-008). The shell
// enables drag-and-drop only while the active module accepts it, and so
// never while a job runs; a drop that arrives then all the same is refused,
// as addSources refuses every add then.
func (n *NativeView) NativeDropFiles(paths []string) {
	n.addSources(paths)
}

// addSources adds the paths to the source list and queues their content
// checks, unless a job runs (refuseWhileRunning).
func (n *NativeView) addSources(paths []string) {
	if n.refuseWhileRunning() {
		return
	}
	first := n.inventory.Len()
	summary := n.inventory.Add(paths)
	n.refreshSources()
	n.inspector.Enqueue(summary.Inspect)
	if n.inventory.Checking() > 0 {
		// An add that starts checking listed a file to check, and Add only
		// appends, so the items it listed follow the earlier ones.
		if n.checked.first == 0 && first < n.inventory.Len() {
			n.checked.first = n.inventory.Item(first).ID
		}
		n.checked.duplicates = append(n.checked.duplicates, summary.DuplicateOf...)
	}
	n.list.Focus()
	n.showSourceNotice(summarizeAdd(summary))
}

// showSourceNotice shows a source-list notice and remembers it, so the
// finishing summary knows the status area still belongs to the list.
func (n *NativeView) showSourceNotice(notice ui.Notice) {
	_ = n.status.Show(notice, 0)
	n.sourceNotice = n.status.Revision()
}

// postRetryDelay paces the retries of a refused drain post.
const postRetryDelay = 100 * time.Millisecond

// deliverInspection runs on the inspector's worker goroutine and hands the
// result to the GUI thread. Results are batched: a drain task is posted only
// when none is outstanding, so however quickly checks finish, the view adds
// at most one message to the GUI thread's queue. Without a dispatcher — the
// shell always provides one — the result is dropped: the inventory must
// never be touched off the GUI thread.
func (n *NativeView) deliverInspection(result InspectionResult) {
	if n.dispatcher == nil || !n.results.put(result) {
		return
	}
	n.postDrain(n.applyInspections)
}

// postDrain posts a mailbox's drain task. A refused post — the GUI thread's
// message queue is momentarily full — is retried after a pause, since nothing
// may come to post it again: the drain task stays outstanding meanwhile, as
// drainSlot describes, so the retry carries everything queued in the
// meantime, and what the mailbox holds would otherwise never reach the view,
// leaving items Checking, or a job unfinished, for good. Once the view is
// destroyed a post can only fail, so the retries stop.
func (n *NativeView) postDrain(drain func()) {
	if err := n.dispatcher.Post(drain); err == nil || n.destroyed() {
		return
	}
	time.AfterFunc(postRetryDelay, func() { n.postDrain(drain) })
}

// applyInspections resolves the delivered content checks into their items on
// the GUI thread. Only their rows are rewritten, so the user's selection and
// scroll position survive background updates; the detail below the full path
// is refreshed when it describes one of them. When nothing is left to check,
// the outcome of the checks is summarized (RSZ-004).
func (n *NativeView) applyInspections() {
	resolved := false
	refreshDetail := false
	selection := n.list.SelectedIndices()
	for _, result := range n.results.take() {
		index, ok := n.inventory.Resolve(result)
		if !ok {
			continue
		}
		resolved = true
		n.list.SetItemText(index, n.inventory.Item(index).RowText())
		if len(selection) > 0 && selection[0] == index {
			refreshDetail = true
		}
	}
	if refreshDetail {
		n.refreshSourceSelection()
	}
	if !resolved || n.inventory.Checking() > 0 {
		return
	}
	summary, _ := n.finishChecks()
	// A notice raised elsewhere in the view since the list's own — a failed
	// settings save, say — stays visible; each row still shows its outcome.
	if n.status.Revision() == n.sourceNotice || n.status.Text() == "" {
		n.showSourceNotice(summary)
	}
}

// finishChecks ends the tally once nothing is left to check: it returns the
// summary of the checks resolved since the list last had nothing to check,
// and whether any of them is still listed to summarize. Every item the adds
// queued for a check has resolved by then.
func (n *NativeView) finishChecks() (ui.Notice, bool) {
	tally := n.checked
	n.checked = checkTally{}
	var added AddSummary
	ready, failed := 0, 0
	for index := 0; index < n.inventory.Len(); index++ {
		item := n.inventory.Item(index)
		switch {
		case tally.first == 0 || item.ID < tally.first:
			// No add is tallied, or the item was listed before them.
		case item.Inspected:
			added.Added++
			if item.Status == StatusReady {
				ready++
			} else {
				failed++
			}
		case item.Status == StatusUnsupported:
			added.Unsupported++
		default:
			added.Inaccessible++
		}
	}
	for _, id := range tally.duplicates {
		if _, listed := n.inventory.IndexOf(id); listed {
			added.Duplicates++
		}
	}
	return summarizeChecks(added, ready, failed), ready+failed > 0
}

// summarizeChecks builds the notice shown when the last pending content
// check resolves: the add results since the list was last fully checked,
// counting only the images still listed, then how many of those are ready
// and how many cannot be resized.
func summarizeChecks(added AddSummary, ready, failed int) ui.Notice {
	message, warn := describeAdd(added)
	if message != "" {
		message += " "
	}
	message += fmt.Sprintf(ui.ImagesChecked, ready+failed, ready)
	if failed > 0 {
		message += fmt.Sprintf(ui.ImagesCheckedFailed, failed)
		warn = true
	}
	kind := ui.NoticeSuccess
	if warn {
		kind = ui.NoticeWarning
	}
	return ui.Notice{Kind: kind, Message: message}
}

// removeSelectedSources removes the selected items and abandons their
// pending content checks, unless a job runs (refuseWhileRunning).
func (n *NativeView) removeSelectedSources() {
	if n.refuseWhileRunning() {
		return
	}
	indices := n.list.SelectedIndices()
	if len(indices) == 0 {
		n.showSourceNotice(ui.Notice{Kind: ui.NoticeInfo, Message: ui.ImagesRemoveNone})
		return
	}
	wasChecking := n.inventory.Checking() > 0
	n.inspector.Drop(n.inventory.Remove(indices))
	n.refreshSources()
	n.list.Focus()
	notice := ui.Notice{Kind: ui.NoticeSuccess, Message: fmt.Sprintf(ui.ImagesRemoved, len(indices))}
	if wasChecking && n.inventory.Checking() == 0 {
		// Removing the last items still being checked finishes checking, so
		// the outcome of the other checks is summarized ahead of the
		// removal, as if the last check had resolved.
		if summary, listed := n.finishChecks(); listed {
			notice = ui.Notice{Kind: summary.Kind, Message: summary.Message + " " + notice.Message}
		}
	}
	n.showSourceNotice(notice)
}

// clearSources empties the source list and abandons every pending content
// check, unless a job runs (refuseWhileRunning).
func (n *NativeView) clearSources() {
	if n.refuseWhileRunning() {
		return
	}
	n.inventory.Clear()
	n.inspector.DropAll()
	n.checked = checkTally{}
	n.refreshSources()
	// Clearing disables the Clear button itself, so keyboard focus moves to
	// the list instead of being stranded on a disabled control.
	n.list.Focus()
	n.showSourceNotice(ui.Notice{Kind: ui.NoticeSuccess, Message: ui.ImagesCleared})
}

// summarizeAdd builds the batch result notice: every add reports what was
// added and why other files were not, so unsupported or unreadable files
// never fail silently (RSZ-004).
func summarizeAdd(summary AddSummary) ui.Notice {
	message, warn := describeAdd(summary)
	if message == "" {
		return ui.Notice{Kind: ui.NoticeInfo, Message: ui.ImagesAddEmpty}
	}
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

// refreshSourceSelection shows the selected item's path and detail and
// enables the source list's commands for the selection, announcing a changed
// detail, as when the selection moves or the selected image's content check
// ends (showSourceSelection).
func (n *NativeView) refreshSourceSelection() {
	n.showSourceSelection(true)
}

// showSourceSelection is refreshSourceSelection, where announce is whether
// the detail line announces a changed detail (ui.NewLiveLabel). A resize
// job's changes to it are shown quietly, as the job announces only its
// start, its Cancel, and its summary (showJobProgress): the selected image's
// Pending, Processing, and final state would otherwise queue beside them,
// its final state just ahead of the summary.
func (n *NativeView) showSourceSelection(announce bool) {
	setDetail := n.pathDetail.SetTextQuietly
	if announce {
		setDetail = n.pathDetail.SetText
	}
	selection := n.list.SelectedIndices()
	if len(selection) == 0 {
		n.path.SetText("")
		setDetail("")
	} else {
		item := n.inventory.Item(selection[0])
		n.path.SetText(item.Path)
		setDetail(item.DetailText())
	}
	// The source list cannot change while a job runs, as its rows are the
	// job's items.
	idle := n.active == nil
	n.addButton.Enable(idle)
	n.removeButton.Enable(idle && len(selection) > 0)
	n.clearButton.Enable(idle && n.inventory.Len() > 0)
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
