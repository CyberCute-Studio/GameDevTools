//go:build windows

package screenshot

import (
	"errors"
	"fmt"
	"strconv"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	core "github.com/CyberCute-Studio/GameDevTools/internal/shell"
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
	displayCommand
	openFolderCommand
)

type NativeView struct {
	options *OptionsManager
	presets *preset.NativeManager
	parent  windows.Handle
	browse  func(windows.Handle, string) (string, bool, error)

	displays    *display.Monitor
	capture     CaptureFunc
	dispatcher  core.Dispatcher
	reveal      func(string) error
	displayList []display.Display
	displayErr  error
	inProgress  bool
	savedPath   string
	shown       bool

	modeLabel    *ui.Label
	modeFree     *ui.Button
	modePreset   *ui.Button
	modeFull     *ui.Button
	displayLabel *ui.Label
	displayCombo *ui.ComboBox
	formatLabel  *ui.Label
	format       *ui.ComboBox
	quality      *ui.TextInput
	folderLabel  *ui.Label
	folder       *ui.ReadOnlyText
	browseButton *ui.Button
	patternLabel *ui.Label
	pattern      *ui.Label
	status       *ui.StatusArea
	openFolder   *ui.Button
}

func NewNativeView(manager *preset.Manager, options *OptionsManager, displays *display.Monitor, capture CaptureFunc) *NativeView {
	return &NativeView{
		options:  options,
		presets:  preset.NewNativeManager(manager),
		browse:   ui.BrowseForFolder,
		displays: displays,
		capture:  capture,
		reveal:   ui.RevealInFolder,
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
	if n.displayLabel, err = ui.NewMnemonicLabel(parent, ui.DisplayLabel); err != nil {
		return err
	}
	if n.displayCombo, err = ui.NewComboBox(parent, base+displayCommand); err != nil {
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
	if n.openFolder, err = ui.NewButton(parent, ui.OpenFolderAction, ui.ButtonOptions{ID: base + openFolderCommand, Group: true, Disabled: true}); err != nil {
		return err
	}
	if err := n.presets.NativeCreate(parent); err != nil {
		return err
	}
	if n.displays != nil {
		n.displays.Subscribe(n.refreshDisplays)
	}
	// refresh clears the status area, so it runs before refreshDisplays:
	// a display-enumeration failure at startup must stay visible.
	n.refresh()
	n.refreshDisplays()
	return nil
}

// NativeDispatcher receives the shell's GUI-thread dispatcher. Capture
// completions are posted through it so result presentation always runs on
// the GUI thread.
func (n *NativeView) NativeDispatcher(dispatcher core.Dispatcher) {
	n.dispatcher = dispatcher
}

func (n *NativeView) NativeFont(font windows.Handle) {
	n.modeLabel.SetFont(font)
	for _, button := range []*ui.Button{n.modeFree, n.modePreset, n.modeFull, n.browseButton, n.openFolder} {
		button.SetFont(font)
	}
	n.displayLabel.SetFont(font)
	n.displayCombo.SetFont(font)
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
	n.shown = visible
	n.modeLabel.Show(visible)
	for _, button := range []*ui.Button{n.modeFree, n.modePreset, n.modeFull, n.browseButton, n.openFolder} {
		button.Show(visible)
	}
	n.showDisplayPicker(visible && n.options.Options().Mode == ModeFullDisplay)
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

func (n *NativeView) showDisplayPicker(visible bool) {
	n.displayLabel.Show(visible)
	n.displayCombo.Show(visible)
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
	if n.options.Options().Mode == ModeFullDisplay {
		n.displayLabel.Move(x, y, width, scale(20))
		n.displayCombo.Move(x, y+scale(24), width, scale(200))
		y += scale(66)
	}
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
	folderWidth := scale(120)
	n.status.Move(x, y, max(int32(1), width-folderWidth-gap), statusHeight)
	n.openFolder.Move(x+width-folderWidth, y+(statusHeight-scale(28))/2, folderWidth, scale(28))
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
	if n.options.Options().Mode == ModeFullDisplay {
		height += scale(66)
	}
	height += scale(66)
	height += metrics.Height() + scale(12)
	height += scale(24) + scale(28) + scale(12)
	height += scale(24) + scale(20) + scale(12)
	statusHeight := max(scale(48), measure(n.status.Text(), width-scale(128)))
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
	case displayCommand:
		// The selection lives in the control and is read at capture time;
		// nothing commits on selection change.
		return true
	case openFolderCommand:
		if notification != 0 || n.savedPath == "" {
			return true
		}
		if err := n.reveal(n.savedPath); err != nil {
			_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.OpenFolderFailed, err)}, 0)
		}
	default:
		return n.presets.NativeCommand(id, notification)
	}
	return true
}

func (n *NativeView) NativeDestroy() {
	n.status.Destroy()
	n.presets.NativeDestroy()
}

// NativeAction runs the shell's primary Start Capture action for the
// currently selected capture mode (SCR-001). Rectangle modes are delivered
// by later items; Full Display capture runs here.
func (n *NativeView) NativeAction() {
	options := n.options.Options()
	if options.Mode != ModeFullDisplay {
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeInfo, Message: ui.CaptureRectanglePending}, 0)
		return
	}
	if n.inProgress {
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeWarning, Message: ui.CaptureInProgress}, 0)
		return
	}
	// The quality field otherwise commits only when it loses focus, and
	// keyboard activation of Start Capture never moves focus; the capture
	// must not run with stale or invalid quality text.
	if _, err := ParseJPEGQuality(n.quality.Edit.Text()); err != nil {
		n.quality.Error.SetText(ui.FormatError(ui.ErrorValidation, err))
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: ui.FormatError(ui.ErrorValidation, err)}, 0)
		return
	}
	n.commitQuality()
	options = n.options.Options()
	if err := options.Validate(); err != nil {
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: ui.FormatError(ui.ErrorValidation, err)}, 0)
		return
	}
	if n.displayErr != nil {
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.DisplayUnavailable, n.displayErr)}, 0)
		return
	}
	deviceName := n.selectedDeviceName()
	if deviceName == "" {
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: ui.DisplayRequired}, 0)
		return
	}
	n.inProgress = true
	request := CaptureRequest{Window: uintptr(n.parent), DeviceName: deviceName, Options: options}
	go func() {
		result := n.capture(request)
		n.post(func() { n.finishCapture(result) })
	}()
}

// finishCapture presents one capture's outcome on the GUI thread. Clipboard
// and save outcomes are reported independently (SCR-060), and a saved file
// enables the Open Folder action (SCR-069).
func (n *NativeView) finishCapture(result CaptureResult) {
	n.inProgress = false
	n.savedPath = result.Path
	n.openFolder.Enable(result.Path != "")
	switch {
	case result.Err != nil:
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.CaptureFailed, result.Err)}, 0)
	case result.Clipboard == nil && result.Save == nil:
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeSuccess, Message: fmt.Sprintf(ui.CaptureSucceeded, result.Path)}, 0)
	case result.Save != nil && result.Clipboard == nil:
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeWarning, Message: fmt.Sprintf(ui.CaptureSaveFailed, result.Save)}, 0)
	case result.Clipboard != nil && result.Save == nil:
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeWarning, Message: fmt.Sprintf(ui.CaptureClipboardFailed, result.Path, result.Clipboard)}, 0)
	default:
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.CaptureOutputsFailed, result.Save, result.Clipboard)}, 0)
	}
}

func (n *NativeView) post(task func()) {
	if n.dispatcher == nil {
		task()
		return
	}
	// A failed post means the shell window is gone; the capture pipeline has
	// already restored the window state, so the result is dropped quietly.
	_ = n.dispatcher.Post(task)
}

func (n *NativeView) selectMode(notification uint16, mode CaptureMode) {
	if notification != 0 || n.options.Options().Mode == mode {
		return
	}
	err := n.options.SetMode(mode)
	n.refreshMode()
	if mode == ModeFullDisplay {
		n.refreshDisplays()
	}
	n.presentResult(err, fmt.Sprintf(ui.CaptureModeSelected, captureModeLabel(mode)))
}

// refreshDisplays repopulates the display picker from the current topology
// (SCR-041, SCR-043). A previous selection is kept while its display stays
// connected; otherwise the cursor display becomes the default. It runs on
// the GUI thread, including from the display-change subscription.
func (n *NativeView) refreshDisplays() {
	if n.displayCombo == nil {
		return
	}
	previous := n.selectedDeviceName()
	if n.displays == nil {
		n.displayErr = errors.New("the display service is unavailable")
		n.displayList = nil
		n.displayCombo.Reset()
		return
	}
	displays, err := n.displays.Displays()
	if err != nil {
		n.displayErr = err
		n.displayList = nil
		n.displayCombo.Reset()
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.DisplayUnavailable, err)}, 0)
		return
	}
	n.displayErr = nil
	n.displayList = displays
	n.displayCombo.Reset()
	for _, item := range displays {
		n.displayCombo.Add(displayListItem(item))
	}
	selected := -1
	for i, item := range displays {
		if item.DeviceName == previous {
			selected = i
			break
		}
	}
	if selected < 0 {
		if cursor, err := n.displays.CursorDisplay(); err == nil {
			for i, item := range displays {
				if item.DeviceName == cursor.DeviceName {
					selected = i
					break
				}
			}
		}
	}
	if selected < 0 && len(displays) > 0 {
		selected = 0
	}
	n.displayCombo.Select(selected)
}

func (n *NativeView) selectedDeviceName() string {
	index := n.displayCombo.Selected()
	if index < 0 || index >= len(n.displayList) {
		return ""
	}
	return n.displayList[index].DeviceName
}

func displayListItem(item display.Display) string {
	name := item.Name
	if name == "" {
		name = item.DeviceName
	}
	format := ui.DisplayListItem
	if item.Primary {
		format = ui.DisplayListItemPrimary
	}
	return fmt.Sprintf(format, item.Number, name, item.Bounds.Width(), item.Bounds.Height(), item.ScalePercent())
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
	n.showDisplayPicker(n.shown && options.Mode == ModeFullDisplay)
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
