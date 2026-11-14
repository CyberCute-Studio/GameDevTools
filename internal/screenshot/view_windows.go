//go:build windows

package screenshot

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/display"
	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	"github.com/CyberCute-Studio/GameDevTools/internal/screenshot/overlay"
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
	shortcutCommand
	folderCommand
	browseCommand
	displayCommand
	openFolderCommand
)

// globalShortcutHotkeyID is the RegisterHotKey identifier the shell window
// uses for the global capture shortcut; WM_HOTKEY carries it in wParam.
const globalShortcutHotkeyID = 1

// hotkeyRegistrar registers, suspends, and releases one global hotkey
// combination. The production implementation is Hotkey; tests substitute a
// fake.
type hotkeyRegistrar interface {
	Apply(Shortcut) error
	Suspend()
	Release()
}

type NativeView struct {
	options  *OptionsManager
	presets  *preset.NativeManager
	parent   windows.Handle
	browse   func(windows.Handle, string) (string, bool, error)
	sessions *overlay.Controller

	displays         *display.Monitor
	capture          CaptureFunc
	captureRectangle RectangleCaptureFunc
	dispatcher       core.Dispatcher
	reveal           func(string) error
	hotkeyFactory    func(uintptr, int32) hotkeyRegistrar
	hotkeys          hotkeyRegistrar
	pending          sync.WaitGroup
	displayList      []display.Display
	displayErr       error
	inProgress       bool
	savedPath        string
	shown            bool
	font             windows.Handle

	modeLabel    *ui.Label
	modeFree     *ui.Button
	modePreset   *ui.Button
	modeFull     *ui.Button
	displayLabel *ui.Label
	displayCombo *ui.ComboBox
	formatLabel  *ui.Label
	format       *ui.ComboBox
	quality      *ui.TextInput
	shortcut     *ui.TextInput
	folderLabel  *ui.Label
	folder       *ui.ReadOnlyText
	browseButton *ui.Button
	patternLabel *ui.Label
	pattern      *ui.Label
	status       *ui.StatusArea
	openFolder   *ui.Button
}

func NewNativeView(manager *preset.Manager, options *OptionsManager, displays *display.Monitor, capture CaptureFunc, captureRectangle RectangleCaptureFunc, sessions *overlay.Controller) *NativeView {
	return &NativeView{
		options:          options,
		presets:          preset.NewNativeManager(manager),
		browse:           ui.BrowseForFolder,
		displays:         displays,
		capture:          capture,
		captureRectangle: captureRectangle,
		reveal:           ui.RevealInFolder,
		hotkeyFactory: func(window uintptr, id int32) hotkeyRegistrar {
			return newHotkey(window, id)
		},
		sessions: sessions,
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
	if n.shortcut, err = ui.NewTextInput(parent, ui.GlobalShortcutLabel, base+shortcutCommand); err != nil {
		return err
	}
	n.shortcut.Edit.SetText(n.options.Options().Shortcut.String())
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
	// Register the global capture shortcut between the two refreshes. The
	// status area holds a single notice, so a startup registration conflict
	// (SCR-003, AC-S11) is reported beside the field and takes the status
	// area, which a later display-enumeration failure then claims for
	// itself — the conflict stays visible beside the field either way,
	// while the display failure has no other home.
	n.hotkeys = n.hotkeyFactory(uintptr(parent), globalShortcutHotkeyID)
	if err := n.hotkeys.Apply(n.options.Options().Shortcut); err != nil {
		n.presentShortcutFailure(err)
	}
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
	n.font = font
	n.modeLabel.SetFont(font)
	for _, button := range []*ui.Button{n.modeFree, n.modePreset, n.modeFull, n.browseButton, n.openFolder} {
		button.SetFont(font)
	}
	n.displayLabel.SetFont(font)
	n.displayCombo.SetFont(font)
	n.formatLabel.SetFont(font)
	n.format.SetFont(font)
	n.quality.SetFont(font)
	n.shortcut.SetFont(font)
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
	n.shortcut.Show(visible)
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
	own, _, _, _ := n.optionsSectionHeight(width, scale, measure)
	return own + n.presets.NativeHeight(width, scale, measure)
}

func (n *NativeView) NativeLayout(x, y, width int32, scale func(int32) int32, measure func(string, int32) int32) {
	_, qualityMetrics, shortcutMetrics, statusHeight := n.optionsSectionHeight(width, scale, measure)
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
	n.quality.Move(x, y, width, qualityMetrics)
	y += qualityMetrics.Height() + scale(12)
	n.shortcut.Move(x, y, width, shortcutMetrics)
	y += shortcutMetrics.Height() + scale(12)
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

func (n *NativeView) optionsSectionHeight(width int32, scale func(int32) int32, measure func(string, int32) int32) (int32, ui.DimensionMetrics, ui.DimensionMetrics, int32) {
	// Each text input reserves its own error height, so a wrapped message
	// under one field does not inflate the row of the other.
	quality := ui.DimensionMetrics{LabelHeight: scale(20), EditHeight: scale(28), ErrorHeight: scale(40), Gap: scale(4)}
	quality.ErrorHeight = max(quality.ErrorHeight, measure(n.quality.Error.Text(), width))
	shortcut := quality
	shortcut.ErrorHeight = max(scale(40), measure(n.shortcut.Error.Text(), width))
	radioRows := int32(1)
	if stackedOptionsLayout(width, scale) {
		radioRows = 3
	}
	height := scale(24) + radioRows*scale(26) + scale(12)
	if n.options.Options().Mode == ModeFullDisplay {
		height += scale(66)
	}
	height += scale(66)
	height += quality.Height() + scale(12)
	height += shortcut.Height() + scale(12)
	height += scale(24) + scale(28) + scale(12)
	height += scale(24) + scale(20) + scale(12)
	statusHeight := max(scale(48), measure(n.status.Text(), width-scale(120)-scale(8)))
	return height + statusHeight + scale(16), quality, shortcut, statusHeight
}

func stackedOptionsLayout(width int32, scale func(int32) int32) bool {
	return width < scale(460)
}

func (n *NativeView) NativeCommand(id int32, notification uint16) bool {
	if id == optionsCommandBase+shortcutCommand {
		switch notification {
		case ui.NotificationEditSetFocus:
			// While the user edits the combination, pressing the active
			// shortcut must not start a capture out from under the edit.
			if n.hotkeys != nil {
				n.hotkeys.Suspend()
			}
		case ui.NotificationEditKillFocus:
			n.commitShortcut()
		case ui.NotificationEditChange:
			n.shortcut.Error.SetText("")
		}
		return true
	}
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
	if n.hotkeys != nil {
		n.hotkeys.Release()
	}
	if n.sessions != nil {
		n.sessions.Shutdown()
	}
	n.status.Destroy()
	n.presets.NativeDestroy()
}

// NativeAction runs the shell's primary Start Capture action for the
// currently selected capture mode (SCR-001). Full Display capture runs here;
// the rectangle modes open the selection overlay session (SCR-050), whose
// Capture command captures the drawn rectangle end to end.
func (n *NativeView) NativeAction() {
	if n.inProgress {
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeWarning, Message: ui.CaptureInProgress}, 0)
		return
	}
	// The quality field otherwise commits only when it loses focus, and
	// keyboard activation of Start Capture never moves focus; the capture
	// must not run with stale or invalid quality text.
	quality, err := ParseJPEGQuality(n.quality.Edit.Text())
	if err != nil {
		n.quality.Error.SetText(ui.FormatError(ui.ErrorValidation, err))
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: ui.FormatError(ui.ErrorValidation, err)}, 0)
		return
	}
	options := n.options.Options()
	// The commit bypasses commitQuality's success notice: the capture
	// result replaces it moments later, so the notice would only flash.
	// Failures still surface, and a non-persistence failure aborts the
	// capture because the new value never reached the options.
	if quality != options.JPEGQuality {
		if err := n.options.SetJPEGQuality(quality); err != nil {
			n.presentResult(err, "")
			if !errors.Is(err, ErrPersistence) {
				return
			}
		}
	}
	options = n.options.Options()
	if err := options.Validate(); err != nil {
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: ui.FormatError(ui.ErrorValidation, err)}, 0)
		return
	}
	if options.Mode != ModeFullDisplay {
		n.beginOverlay()
		return
	}
	// A stale enumeration failure must not wedge capture: retry it here,
	// the same way selectMode retries on mode selection, so a recovered
	// display service works without the user toggling the capture mode.
	n.refreshDisplays()
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
	n.pending.Add(1)
	go func() {
		defer n.pending.Done()
		result := n.capture(request)
		n.post(func() { n.finishCapture(result, displayCaptureMessages) })
	}()
}

// NativeHotkey runs the global capture shortcut for the currently selected
// capture mode (SCR-002), exactly like the Start Capture button (SCR-001).
// While a rectangle selection session is on screen, the shortcut brings
// attention to that session instead of stacking a second overlay (SCR-005).
// The shell routes WM_HOTKEY here; unknown identifiers are not consumed.
func (n *NativeView) NativeHotkey(id int32) bool {
	if id != globalShortcutHotkeyID || n.hotkeys == nil {
		return false
	}
	if n.sessions != nil && n.sessions.Active() {
		n.sessions.Attention()
		return true
	}
	n.NativeAction()
	return true
}

// NativeShutdown blocks until an in-flight capture has finished delivering
// its outputs. The shell calls it only after the message loop has ended and
// the window is destroyed, so the worker never waits on the GUI thread here;
// its final dispatcher post fails harmlessly against the destroyed window.
// NativeDestroy has already shut down any overlay session; Shutdown here is
// the idempotent backstop.
func (n *NativeView) NativeShutdown() {
	n.pending.Wait()
	if n.sessions != nil {
		n.sessions.Shutdown()
	}
}

// beginOverlay starts the rectangle selection session: the controller hides
// the main window before the session surface appears (SCR-050) and restores
// it on cancel, failure, and exit. Preset Rectangle mode sizes the session's
// frame from the selected preset's exact dimensions (SCR-020); a preset that
// cannot fit the virtual desktop is rejected with a validation notice before
// anything hides (SCR-031). A cancelled session only reports that nothing
// was captured (SCR-051); a completed one hands the selected rectangle and
// the main-window restore to startRectangleCapture.
func (n *NativeView) beginOverlay() {
	if n.sessions == nil {
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.CaptureOverlayFailed, errors.New("the overlay service is unavailable"))}, 0)
		return
	}
	mode := overlay.ModeFreeRectangle
	size := display.Point{}
	if n.options.Options().Mode == ModePresetRectangle {
		mode = overlay.ModePresetRectangle
		selected, exists := n.presets.SelectedPreset()
		if !exists {
			_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: ui.FormatError(ui.ErrorValidation, errors.New(ui.PresetSelectionRequired))}, 0)
			return
		}
		size = display.Point{X: int32(selected.Width), Y: int32(selected.Height)}
	}
	err := n.sessions.Begin(uintptr(n.parent), uintptr(n.font), mode, size, overlay.Events{
		OnCapture: n.startRectangleCapture,
		OnCancel: func() {
			_ = n.status.Show(ui.Notice{Kind: ui.NoticeInfo, Message: ui.CaptureSelectionEnded}, 0)
		},
	})
	switch {
	case err == nil:
	case errors.Is(err, overlay.ErrPresetTooLarge):
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: ui.FormatError(ui.ErrorValidation, err)}, 0)
	case errors.Is(err, overlay.ErrSessionActive):
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeWarning, Message: ui.CaptureSessionActive}, 0)
	default:
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.CaptureOverlayFailed, err)}, 0)
	}
}

// startRectangleCapture runs on the GUI thread when the overlay session's
// Capture command completes: the session windows are already destroyed and
// the main window is still hidden. The worker reads the selection's pixels
// off the GUI thread (TECH-005), restores the main window through the
// session's restore function as soon as the capture ends, and delivers the
// captured image to the clipboard and the output folder (SCR-014).
func (n *NativeView) startRectangleCapture(region display.Rect, restore func()) {
	if n.captureRectangle == nil {
		restore()
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.SelectionCaptureFailed, errors.New("the capture service is unavailable"))}, 0)
		return
	}
	options := n.options.Options()
	n.inProgress = true
	n.pending.Add(1)
	go func() {
		defer n.pending.Done()
		result := n.captureRectangle(region, restore, options)
		n.post(func() { n.finishCapture(result, selectionCaptureMessages) })
	}()
}

// NativeFilterMessage lets the shell's message loop pre-handle queued
// messages for the overlay session's windows: Escape cancels the session,
// and toolbar messages receive dialog navigation.
func (n *NativeView) NativeFilterMessage(message unsafe.Pointer) bool {
	if n.sessions == nil {
		return false
	}
	return n.sessions.FilterMessage(message)
}

// captureMessages carries the user-facing result wording of one capture
// flavor, so finishCapture presents rectangle and full display outcomes
// through the same independent-outcome matrix.
type captureMessages struct {
	failed          string
	succeeded       string
	saveFailed      string
	clipboardFailed string
	outputsFailed   string
}

var (
	displayCaptureMessages = captureMessages{
		failed:          ui.CaptureFailed,
		succeeded:       ui.CaptureSucceeded,
		saveFailed:      ui.CaptureSaveFailed,
		clipboardFailed: ui.CaptureClipboardFailed,
		outputsFailed:   ui.CaptureOutputsFailed,
	}
	selectionCaptureMessages = captureMessages{
		failed:          ui.SelectionCaptureFailed,
		succeeded:       ui.SelectionCaptureSucceeded,
		saveFailed:      ui.SelectionCaptureSaveFailed,
		clipboardFailed: ui.SelectionCaptureClipboardFailed,
		outputsFailed:   ui.SelectionCaptureOutputsFailed,
	}
)

// finishCapture presents one capture's outcome on the GUI thread. Clipboard
// and save outcomes are reported independently (SCR-060), and a saved file
// enables the Open Folder action (SCR-069).
func (n *NativeView) finishCapture(result CaptureResult, messages captureMessages) {
	n.inProgress = false
	n.savedPath = result.Path
	n.openFolder.Enable(result.Path != "")
	switch {
	case result.Err != nil:
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(messages.failed, result.Err)}, 0)
	case result.Clipboard == nil && result.Save == nil:
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeSuccess, Message: fmt.Sprintf(messages.succeeded, result.Path)}, 0)
	case result.Save != nil && result.Clipboard == nil:
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeWarning, Message: fmt.Sprintf(messages.saveFailed, result.Save)}, 0)
	case result.Clipboard != nil && result.Save == nil:
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeWarning, Message: fmt.Sprintf(messages.clipboardFailed, result.Path, result.Clipboard)}, 0)
	default:
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(messages.outputsFailed, result.Save, result.Clipboard)}, 0)
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
		if err == nil && n.displayErr != nil {
			// refreshDisplays already reported that the picker could not be
			// populated; a mode-selection success notice must not replace
			// that failure.
			return
		}
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
		// The picker is hidden outside Full Display mode, so a failure
		// notice only makes sense there; NativeAction and selectMode
		// surface displayErr when the mode is selected.
		if n.options.Options().Mode == ModeFullDisplay {
			_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.DisplayUnavailable, err)}, 0)
		}
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

// commitShortcut applies the shortcut field's text when the field loses
// focus. Invalid text is rejected with field-level validation and the
// previously registered combination is restored; a combination another
// application owns is reported as a registration conflict while the old
// shortcut keeps working, so the user can choose a replacement (SCR-003,
// SCR-004, AC-S11).
func (n *NativeView) commitShortcut() {
	if n.hotkeys == nil {
		return
	}
	current := n.options.Options().Shortcut
	shortcut, err := ParseShortcut(n.shortcut.Edit.Text())
	if err != nil {
		n.shortcut.Error.SetText(ui.FormatError(ui.ErrorValidation, err))
		if restoreErr := n.restoreHotkey(current); restoreErr != nil {
			n.presentShortcutFailure(restoreErr)
		}
		return
	}
	if shortcut == current {
		n.shortcut.Error.SetText("")
		n.shortcut.Edit.SetText(shortcut.String())
		if restoreErr := n.restoreHotkey(current); restoreErr != nil {
			n.presentShortcutFailure(restoreErr)
		}
		return
	}
	if err := n.hotkeys.Apply(shortcut); err != nil {
		// A failed registration leaves the old combination untouched, but
		// the field edit suspended it; bring it back so the working shortcut
		// keeps working while the user picks another (AC-S11). If the old
		// combination cannot be brought back either, that loss is the more
		// important message.
		if restoreErr := n.restoreHotkey(current); restoreErr != nil {
			n.presentShortcutFailure(restoreErr)
		} else {
			n.presentShortcutFailure(err)
		}
		return
	}
	n.shortcut.Error.SetText("")
	n.shortcut.Edit.SetText(shortcut.String())
	err = n.options.SetShortcut(shortcut)
	n.presentResult(err, fmt.Sprintf(ui.ShortcutSaved, shortcut))
}

// restoreHotkey re-registers the persisted combination after the shortcut
// field's focus suspension or a rejected edit. A failure here means the
// combination itself became unavailable — another application claimed it in
// the meantime — so the caller reports it like any registration failure.
func (n *NativeView) restoreHotkey(shortcut Shortcut) error {
	if n.hotkeys == nil {
		return nil
	}
	return n.hotkeys.Apply(shortcut)
}

// presentShortcutFailure reports a registration problem beside the shortcut
// field and in the status area, categorized as a shortcut-registration
// failure (UX-010).
func (n *NativeView) presentShortcutFailure(err error) {
	n.shortcut.Error.SetText(ui.FormatError(ui.ErrorShortcutRegistration, err))
	_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: ui.FormatError(ui.ErrorShortcutRegistration, err)}, 0)
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
