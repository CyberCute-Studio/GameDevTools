//go:build windows

package screenshot

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
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
	dismissProblemCommand
)

// globalShortcutHotkeyID is the RegisterHotKey identifier the shell window
// uses for the global capture shortcut; WM_HOTKEY carries it in wParam.
const globalShortcutHotkeyID = 1

var (
	viewUser32                  = windows.NewLazySystemDLL("user32.dll")
	procViewIsWindow            = viewUser32.NewProc("IsWindow")
	procViewIsWindowEnabled     = viewUser32.NewProc("IsWindowEnabled")
	procViewIsWindowVisible     = viewUser32.NewProc("IsWindowVisible")
	procViewIsIconic            = viewUser32.NewProc("IsIconic")
	procViewGetForegroundWindow = viewUser32.NewProc("GetForegroundWindow")
	procViewGetWindowRect       = viewUser32.NewProc("GetWindowRect")
	procViewWindowFromPoint     = viewUser32.NewProc("WindowFromPoint")
	procViewGetAncestor         = viewUser32.NewProc("GetAncestor")
	procViewGetClientRect       = viewUser32.NewProc("GetClientRect")
	procViewMapWindowPoints     = viewUser32.NewProc("MapWindowPoints")
)

// viewGARoot and viewGARootOwner are GetAncestor's GA_ROOT and GA_ROOTOWNER.
const (
	viewGARoot      = 2
	viewGARootOwner = 3
)

// controlCoverStep is the most pixels controlCovered leaves between two points
// it checks along a control, less than any window that could hide a message.
const controlCoverStep = 48

// viewBNDoubleClicked is the notification a BS_NOTIFY button sends for a second
// quick click, as dismissing several problems in a row makes.
const viewBNDoubleClicked = 5

type viewRect struct {
	Left, Top, Right, Bottom int32
}

// hotkeyRegistrar registers, suspends, and releases one global hotkey
// combination. The production implementation is Hotkey; tests substitute a
// fake.
type hotkeyRegistrar interface {
	Apply(Shortcut) error
	Suspend()
	Release()
}

// resultCard reports results while the view cannot be seen. The production
// implementation is ui.ResultCard; tests substitute a fake, as a real card
// is a topmost window that a capture test running beside them would see.
type resultCard interface {
	Report(notice ui.Notice, path string)
	ReportSetting(notice ui.Notice)
	ReportSettingBehind(notice ui.Notice)
	WithdrawSettings()
	WithdrawSetting(notice ui.Notice)
	Suspend()
	Resume()
	Reposition()
	DismissProblem()
	SetProblemsChanged(changed func(problems int))
	Destroy()
}

type NativeView struct {
	options  *OptionsManager
	presets  *preset.NativeManager
	parent   windows.Handle
	browse   func(windows.Handle, string) (string, bool, error)
	sessions *overlay.Controller

	displays *display.Monitor
	// listDisplays enumerates the displays; tests replace it to make
	// enumeration fail.
	listDisplays     func() ([]display.Display, error)
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
	// captureAttempts counts Start Capture actions, on the GUI thread, so a
	// late Reveal failure can tell that one has begun since.
	captureAttempts uint64
	// revealing holds the files whose reveal is running, under revealMu.
	revealMu  sync.Mutex
	revealing map[string]bool
	shown     bool
	font      windows.Handle
	// shortcutNotice is the status area's revision once
	// presentShortcutFailure last showed a failure there, which
	// NativeAnnounceNotice compares to tell whether a later notice replaced
	// it.
	shortcutNotice uint64
	// optionsNotice is the status area's revision once a notice about the
	// capture options was last shown there — an option change's outcome,
	// such as an output folder saved (presentResult), or Start Capture's
	// refusal of the options or the JPEG quality text (refuseOptions) — which
	// the restore of the default settings clears while it is still shown, as
	// the option it reports is replaced.
	optionsNotice uint64
	// card reports a global-shortcut capture's outcome while the status area
	// cannot be seen (SCR-060, SCR-069).
	card resultCard
	// fromShortcut is whether the global shortcut started the latest capture,
	// and watched whether the status area was in front of the user as it did.
	fromShortcut bool
	watched      bool
	// startupConflict is a shortcut conflict found while the view was created,
	// reported on the card once the window shows whether the view is visible.
	startupConflict ui.Notice
	// cardConflict is that conflict once it is on the card, which withdraws it
	// when the shortcut registers.
	cardConflict ui.Notice
	// cardProblems is how many problems a press can dismiss on the card, none
	// while it is hidden for a capture. The Dismiss Problem button is offered
	// while there are any.
	cardProblems int
	// foregroundWindow and windowAtPoint read the desktop; tests replace them,
	// as their windows are off every display, where nothing hits them, and
	// never in front.
	foregroundWindow func() windows.Handle
	windowAtPoint    func(x, y int32) uintptr
	// windowAlive reports whether the shell window still exists, which ends a
	// capture's retried completion post.
	windowAlive func() bool

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
	// dismissProblem is the keyboard's way to dismiss a problem on the result
	// card, which never takes the focus.
	dismissProblem *ui.Button
}

func NewNativeView(manager *preset.Manager, options *OptionsManager, displays *display.Monitor, capture CaptureFunc, captureRectangle RectangleCaptureFunc, sessions *overlay.Controller) *NativeView {
	view := &NativeView{
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
	if displays != nil {
		view.listDisplays = displays.Displays
	}
	view.card = ui.NewResultCard(view.cardPlacement, view.revealFromCard)
	view.foregroundWindow = func() windows.Handle {
		foreground, _, _ := procViewGetForegroundWindow.Call()
		return windows.Handle(foreground)
	}
	view.windowAtPoint = func(x, y int32) uintptr {
		hit, _, _ := procViewWindowFromPoint.Call(uintptr(uint32(x)) | uintptr(uint32(y))<<32)
		return hit
	}
	view.windowAlive = func() bool {
		alive, _, _ := procViewIsWindow.Call(uintptr(view.parent))
		return alive != 0
	}
	return view
}

// cardPlacement puts the result card on the display under the pointer, where
// the user is working.
func (n *NativeView) cardPlacement() ui.CardPlacement {
	if n.displays == nil {
		return ui.CardPlacement{}
	}
	item, err := n.displays.CursorDisplay()
	if err != nil {
		return ui.CardPlacement{}
	}
	return ui.CardPlacement{
		Left: item.WorkArea.Left, Top: item.WorkArea.Top,
		Right: item.WorkArea.Right, Bottom: item.WorkArea.Bottom,
		DPI: item.DPIX,
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
	// The display label starts a group, ending the capture modes' radio
	// group at Full Display, so the arrow keys wrap between the modes
	// (ui.NewGroupLabel).
	if n.displayLabel, err = ui.NewGroupMnemonicLabel(parent, ui.DisplayLabel); err != nil {
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
	if n.dismissProblem, err = ui.NewButton(parent, ui.CardDismissProblemAction, ui.ButtonOptions{ID: base + dismissProblemCommand, Group: true, Disabled: true}); err != nil {
		return err
	}
	n.dismissProblem.Show(false)
	n.card.SetProblemsChanged(n.problemsChanged)
	if n.openFolder, err = ui.NewButton(parent, ui.OpenFolderAction, ui.ButtonOptions{ID: base + openFolderCommand, Group: true, Disabled: true}); err != nil {
		return err
	}
	if err := n.presets.NativeCreate(parent); err != nil {
		return err
	}
	if n.displays != nil {
		n.displays.Subscribe(func() {
			n.refreshDisplays()
			n.card.Reposition()
		})
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
		n.startupConflict = ui.Notice{Kind: ui.NoticeError, Message: ui.FormatError(ui.ErrorShortcutRegistration, err)}
	}
	n.refreshDisplays()
	return nil
}

// NativeDispatcher receives the shell's GUI-thread dispatcher. Capture
// completions are posted through it so result presentation always runs on
// the GUI thread. A shortcut conflict found at startup is reported through
// it too: the post runs once the message loop does, after the window has
// appeared with its remembered module, and the card shows the conflict unless
// the message beside the shortcut field can be seen (SCR-003, AC-S11).
func (n *NativeView) NativeDispatcher(dispatcher core.Dispatcher) {
	n.dispatcher = dispatcher
	if n.startupConflict.Message == "" || dispatcher == nil {
		return
	}
	notice := n.startupConflict
	n.startupConflict = ui.Notice{}
	_ = dispatcher.Post(func() {
		if !n.statusVisible() || n.controlCovered(n.shortcut.Error.Handle()) {
			n.cardConflict = notice
			n.card.ReportSetting(notice)
		}
	})
}

// statusVisible reports whether the status area is on a window that is shown
// and not minimized, with this view selected.
func (n *NativeView) statusVisible() bool {
	if !n.shown {
		return false
	}
	visible, _, _ := procViewIsWindowVisible.Call(uintptr(n.parent))
	iconic, _, _ := procViewIsIconic.Call(uintptr(n.parent))
	return visible != 0 && iconic == 0
}

// statusInSight reports whether the user can see the status area now: it is
// showing and no other window is over it. The foreground window cannot say,
// as a capture returns the main window without activating it.
func (n *NativeView) statusInSight() bool {
	return n.statusVisible() && !n.controlCovered(n.status.Handle())
}

// controlCovered reports whether part of a control is out of sight: scrolled
// outside the main window's client area, where the window there can still be
// the main window's own title bar, or under another top-level window. Points
// along its top, middle, and bottom are checked, corners included, as a window
// over any part of a message can hide what it is about.
func (n *NativeView) controlCovered(control windows.Handle) bool {
	var bounds, client viewRect
	if r, _, _ := procViewGetWindowRect.Call(uintptr(control), uintptr(unsafe.Pointer(&bounds))); r == 0 {
		return true
	}
	if r, _, _ := procViewGetClientRect.Call(uintptr(n.parent), uintptr(unsafe.Pointer(&client))); r == 0 {
		return true
	}
	procViewMapWindowPoints.Call(uintptr(n.parent), 0, uintptr(unsafe.Pointer(&client)), 2)
	right, bottom := bounds.Right-1, bounds.Bottom-1
	for _, y := range [...]int32{bounds.Top, bounds.Top + (bounds.Bottom-bounds.Top)/2, bottom} {
		for x := bounds.Left; ; x = min(x+controlCoverStep, right) {
			if x < client.Left || x >= client.Right || y < client.Top || y >= client.Bottom {
				return true
			}
			hit := n.windowAtPoint(x, y)
			if hit == 0 {
				return true
			}
			if root, _, _ := procViewGetAncestor.Call(hit, viewGARoot); windows.Handle(root) != n.parent {
				return true
			}
			if x >= right {
				break
			}
		}
	}
	return false
}

// cardNeeded reports whether a capture's outcome also goes on the result
// card: the global shortcut started it, and the user was not looking at this
// view then or no longer can, so the result would otherwise be written where
// nobody sees it (SCR-060, SCR-069).
func (n *NativeView) cardNeeded() bool {
	return n.fromShortcut && !(n.watched && n.statusInSight())
}

// reportOutcome shows a capture's outcome in the status area and, where
// cardNeeded says so, on the result card.
func (n *NativeView) reportOutcome(notice ui.Notice, path string) {
	if n.showOutcome(notice) {
		n.card.Report(notice, path)
	}
}

// showOutcome puts an outcome in the status area and reports whether it also
// goes on the result card. The desktop is asked once per outcome, as a window
// moving between two looks would leave the result announced nowhere. A result
// going on the card is written here quietly, as the card announces it and a
// screen reader would otherwise read it twice in a row.
func (n *NativeView) showOutcome(notice ui.Notice) bool {
	if n.cardNeeded() {
		n.status.Update(notice)
		return true
	}
	_ = n.status.Show(notice, 0)
	return false
}

// problemsChanged keeps the Dismiss Problem button in step with what a press
// can dismiss on the card. A button about to disappear hands the focus to the
// read-only Output Folder field, where a Space pressed once too often does
// nothing, as it would open the folder picker on Browse.
func (n *NativeView) problemsChanged(problems int) {
	n.cardProblems = problems
	if problems == 0 && n.dismissProblem.HasFocus() {
		n.folder.Focus()
	}
	n.dismissProblem.Enable(problems > 0)
	n.dismissProblem.Show(n.shown && problems > 0)
}

// NativeAnnounceNotice asks a screen reader to read the status area's
// notice, if any, once the view is first shown in the visible main window: a
// startup shortcut conflict or display failure is shown while the window, or
// the view, is still hidden, where its announcement goes unheard. A shortcut
// failure that a later notice replaced in the status area, as a display
// failure at startup in Full Display mode replaces a conflict, is still
// shown beside the field, and is announced from there.
func (n *NativeView) NativeAnnounceNotice() {
	n.status.Announce()
	if n.status.Revision() != n.shortcutNotice {
		n.shortcut.Error.Announce()
	}
}

func (n *NativeView) NativeFont(font windows.Handle) {
	n.font = font
	n.modeLabel.SetFont(font)
	for _, button := range []*ui.Button{n.modeFree, n.modePreset, n.modeFull, n.browseButton, n.openFolder, n.dismissProblem} {
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
	// The shell destroys the previous font while recreating this one, so an
	// active overlay session's toolbar must receive the new handle too, or
	// its controls keep painting through the destroyed one (SCR-054).
	if n.sessions != nil {
		n.sessions.SetFont(uintptr(font))
	}
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
	n.dismissProblem.Show(visible && n.cardProblems > 0)
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
	n.status.Move(x, y, width, statusHeight)
	buttonY := y + statusHeight + gap
	folderWidth := min(scale(120), max(int32(1), (width-gap)/2))
	dismissWidth := min(scale(statusDismissWidth), max(int32(1), width-folderWidth-gap))
	n.dismissProblem.Move(x+width-folderWidth-gap-dismissWidth, buttonY, dismissWidth, scale(28))
	n.openFolder.Move(x+width-folderWidth, buttonY, folderWidth, scale(28))
	y += statusRowHeight(statusHeight, scale) + scale(16)
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
	statusHeight := n.status.Height(width, scale, measure)
	return height + statusRowHeight(statusHeight, scale) + scale(16), quality, shortcut, statusHeight
}

// statusDismissWidth is the width at 96 DPI of the Dismiss Problem button.
const statusDismissWidth = 140

// statusRowHeight is the height of the status text and the row of buttons
// below it. The text has the content's whole width at every window size, as
// it can hold a saved path with no spaces, which takes more lines the narrower
// it is, and the row keeps room for Dismiss Problem, as a problem can reach
// the card with no layout to follow.
func statusRowHeight(textHeight int32, scale func(int32) int32) int32 {
	return textHeight + scale(8) + scale(28)
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
			n.presentResult(err, "")
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
		n.revealSaved()
	case dismissProblemCommand:
		if notification == 0 || notification == viewBNDoubleClicked {
			n.card.DismissProblem()
		}
	default:
		return n.presets.NativeCommand(id, notification)
	}
	return true
}

// revealSaved shows the saved file in File Explorer, only on the user's
// request. Looking at the file and handing it to the shell each block for as
// long as its drive does not respond, as a network share that went offline
// after the capture, so they run on a goroutine of their own, which must not
// freeze the window (TECH-005). A click while the same file's reveal is still
// running starts no second one, or a drive that recovers would open one
// Explorer window for each. A failure is shown through the dispatcher, only
// while this file is still the latest capture's and no capture has been
// started since, so that it cannot replace a newer capture's result, or the
// reason one ended without a result, as a cancelled or lost selection does.
func (n *NativeView) revealSaved() {
	path, attempt := n.savedPath, n.captureAttempts
	n.revealFile(path, func(err error) {
		if n.savedPath == path && n.captureAttempts == attempt {
			_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.OpenFolderFailed, err)}, 0)
		}
	})
}

// revealFromCard is the result card's Open Folder. The card may show an
// earlier capture's file, and a failure goes back on the card, where the user
// asked.
func (n *NativeView) revealFromCard(path string) {
	n.revealFile(path, func(err error) {
		n.card.Report(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.OpenFolderFailed, err)}, "")
	})
}

// revealFile reveals the file off the GUI thread, as revealSaved describes,
// and hands a failure to failed on the GUI thread.
func (n *NativeView) revealFile(path string, failed func(error)) {
	reveal := n.reveal
	if n.dispatcher == nil || !n.startReveal(path) {
		return
	}
	go func() {
		err := reveal(path)
		n.endReveal(path)
		if err != nil {
			n.post(func() { failed(err) })
		}
	}()
}

// startReveal records a reveal of the file, unless one is already running.
func (n *NativeView) startReveal(path string) bool {
	n.revealMu.Lock()
	defer n.revealMu.Unlock()
	if n.revealing[path] {
		return false
	}
	if n.revealing == nil {
		n.revealing = map[string]bool{}
	}
	n.revealing[path] = true
	return true
}

func (n *NativeView) endReveal(path string) {
	n.revealMu.Lock()
	defer n.revealMu.Unlock()
	delete(n.revealing, path)
}

func (n *NativeView) NativeDestroy() {
	if n.hotkeys != nil {
		n.hotkeys.Release()
	}
	if n.sessions != nil {
		n.sessions.Shutdown()
	}
	n.card.Destroy()
	n.status.Destroy()
	n.presets.NativeDestroy()
}

// NativeAction runs the shell's primary Start Capture action for the
// currently selected capture mode (SCR-001). Full Display capture runs here;
// the rectangle modes open the selection overlay session (SCR-050), whose
// Capture command captures the drawn rectangle end to end.
func (n *NativeView) NativeAction() {
	n.startCapture(false)
}

// startCapture starts a capture of the selected mode, recording whether the
// global shortcut started it, as reportOutcome needs.
func (n *NativeView) startCapture(fromShortcut bool) {
	n.captureAttempts++
	if n.inProgress {
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeWarning, Message: ui.CaptureInProgress}, 0)
		// The card is hidden until this capture ends, so it says what became
		// of the press rather than to wait. The running capture's own
		// reporting is not changed.
		if fromShortcut && !n.statusInSight() {
			n.card.Report(ui.Notice{Kind: ui.NoticeWarning, Message: ui.CaptureShortcutRefused}, "")
		}
		return
	}
	// This start supersedes a press refused behind a dialog, which would
	// otherwise return to the card under this capture's result.
	n.card.WithdrawSetting(shortcutDialogRefusal)
	// A start refused because a selection is on screen must not change how
	// that selection's outcome is reported.
	if n.sessions == nil || !n.sessions.Active() {
		n.fromShortcut = fromShortcut
		n.watched = false
		if fromShortcut && n.statusVisible() {
			n.watched = n.foregroundWindow() == n.parent
		}
	}
	// The quality field otherwise commits only when it loses focus, and
	// keyboard activation of Start Capture never moves focus; the capture
	// must not run with stale or invalid quality text. The refusal is shown
	// beside the field quietly, as the status area announces the same
	// message, which would otherwise be read twice in a row when the field's
	// message was not already shown. A PNG capture does not use the field,
	// which is disabled, so it neither reads nor commits the field's text.
	if current := n.options.Options(); current.Format == FormatJPEG {
		quality, err := ParseJPEGQuality(n.quality.Edit.Text())
		if err != nil {
			n.quality.Error.SetTextQuietly(ui.FormatError(ui.ErrorValidation, err))
			n.refuseOptions(err)
			return
		}
		// The commit bypasses commitQuality's success notice: the capture
		// result replaces it moments later, so the notice would only flash.
		// Failures still surface, and a non-persistence failure aborts the
		// capture because the new value never reached the options.
		if quality != current.JPEGQuality {
			if err := n.options.SetJPEGQuality(quality); err != nil {
				notice := optionNotice(err, "")
				onCard := n.showOutcome(notice)
				n.optionsNotice = n.status.Revision()
				if !errors.Is(err, ErrPersistence) {
					if onCard {
						n.card.ReportSetting(notice)
					}
					return
				}
				// The capture goes on and hides the card, which would cut off
				// this warning's announcement, so it joins the card after that,
				// and a selection cancelled with no result of its own still
				// announces it.
				if onCard {
					defer n.card.ReportSetting(notice)
				}
			}
		}
	}
	options := n.options.Options()
	if err := options.Validate(); err != nil {
		n.refuseOptions(err)
		return
	}
	if options.Mode != ModeFullDisplay {
		n.beginOverlay()
		return
	}
	// A stale enumeration failure must not wedge capture: retry it here,
	// the same way selectMode retries on mode selection, so a recovered
	// display service works without the user toggling the capture mode. Its
	// failure is reported once, by reportOutcome.
	_ = n.reloadDisplays()
	if n.displayErr != nil {
		n.reportOutcome(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.DisplayUnavailable, n.displayErr)}, "")
		return
	}
	deviceName := n.selectedDeviceName()
	if deviceName == "" {
		n.reportOutcome(ui.Notice{Kind: ui.NoticeError, Message: ui.DisplayRequired}, "")
		return
	}
	n.inProgress = true
	request := CaptureRequest{Window: uintptr(n.parent), DeviceName: deviceName, Options: options}
	// The card is hidden first so that it cannot appear in the image.
	n.card.Suspend()
	n.pending.Add(1)
	go func() {
		defer n.pending.Done()
		result := n.capture(request)
		n.postCompletion(func() { n.finishCapture(result, displayCaptureMessages) })
	}()
}

// NativeHotkey runs the global capture shortcut for the currently selected
// capture mode (SCR-002), exactly like the Start Capture button (SCR-001).
// While a rectangle selection session is on screen, the shortcut brings
// attention to that session instead of stacking a second overlay (SCR-005).
// While one of the application's modal dialogs is open it captures nothing,
// as dialogForbidsCapture explains. The shell routes WM_HOTKEY here; unknown
// identifiers are not consumed.
func (n *NativeView) NativeHotkey(id int32) bool {
	if id != globalShortcutHotkeyID || n.hotkeys == nil {
		return false
	}
	if n.sessions != nil && n.sessions.Active() {
		n.sessions.Attention()
		return true
	}
	// A capture already running keeps startCapture's refusal, as the main
	// window is back, and a dialog can be open, while its file is written.
	if !n.inProgress && n.dialogForbidsCapture() {
		if n.statusInSight() && n.foregroundIsOwn() {
			_ = n.status.Show(shortcutDialogRefusal, 0)
		} else {
			n.status.Update(shortcutDialogRefusal)
			n.card.ReportSettingBehind(shortcutDialogRefusal)
		}
		return true
	}
	n.startCapture(true)
	return true
}

// shortcutDialogRefusal answers the shortcut while a dialog is open. On the
// card it is a withdrawable entry, which the next capture to start removes,
// and it waits behind a success there rather than ending it.
var shortcutDialogRefusal = ui.Notice{Kind: ui.NoticeWarning, Message: ui.CaptureShortcutDialogOpen}

// foregroundIsOwn reports whether the window in front is the main window or a
// dialog it owns. Otherwise the user is in another application, and a status
// area in sight on another display is not where they are looking.
func (n *NativeView) foregroundIsOwn() bool {
	root, _, _ := procViewGetAncestor.Call(uintptr(n.foregroundWindow()), viewGARootOwner)
	return windows.Handle(root) == n.parent
}

// dialogForbidsCapture reports whether a modal dialog of the application is
// open, which disables the main window it belongs to. The dialog's own message
// loop still delivers the shortcut, but hiding the main window leaves the
// dialog on screen, where it would appear in the image (SCR-044), and that
// loop does not run the overlay's keyboard handling, so Escape could not
// cancel a selection (SCR-052).
func (n *NativeView) dialogForbidsCapture() bool {
	enabled, _, _ := procViewIsWindowEnabled.Call(uintptr(n.parent))
	return enabled == 0
}

// NativeShutdown blocks until an in-flight capture has finished delivering
// its outputs. The shell calls it only once the window is destroyed, on a
// goroutine of its own, so the worker never waits on the GUI thread here;
// its final dispatcher post fails against the destroyed window, and
// postCompletion then stops trying.
// NativeDestroy has already shut down any overlay session, so Shutdown here,
// the idempotent backstop, finds none to close.
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
// was captured (SCR-051); a session a display change invalidates ends with
// the reason as a warning (SCR-054); a completed one hands the selected
// rectangle and the main-window restore to startRectangleCapture.
func (n *NativeView) beginOverlay() {
	if n.sessions == nil {
		n.reportOutcome(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.CaptureOverlayFailed, errors.New(ui.OverlayServiceUnavailable))}, "")
		return
	}
	mode := overlay.ModeFreeRectangle
	size := display.Point{}
	if n.options.Options().Mode == ModePresetRectangle {
		mode = overlay.ModePresetRectangle
		selected, exists := n.presets.SelectedPreset()
		if !exists {
			n.reportOutcome(ui.Notice{Kind: ui.NoticeError, Message: ui.FormatError(ui.ErrorValidation, errors.New(ui.PresetSelectionRequired))}, "")
			return
		}
		size = display.Point{X: int32(selected.Width), Y: int32(selected.Height)}
	}
	// The card stays hidden for the whole session, so that it cannot appear
	// in the image, and returns when the session or its capture has ended.
	n.card.Suspend()
	err := n.sessions.Begin(uintptr(n.parent), uintptr(n.font), mode, size, overlay.Events{
		OnCapture: n.startRectangleCapture,
		OnCancel: func() {
			_ = n.status.Show(ui.Notice{Kind: ui.NoticeInfo, Message: ui.CaptureSelectionEnded}, 0)
			n.card.Resume()
		},
		OnSessionLost: func(reason string) {
			n.reportOutcome(ui.Notice{Kind: ui.NoticeWarning, Message: reason}, "")
			n.card.Resume()
		},
	})
	switch {
	case err == nil:
	case errors.Is(err, overlay.ErrPresetTooLarge):
		n.reportOutcome(ui.Notice{Kind: ui.NoticeError, Message: ui.FormatError(ui.ErrorValidation, err)}, "")
	case errors.Is(err, overlay.ErrSessionActive):
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeWarning, Message: ui.CaptureSessionActive}, 0)
	default:
		n.reportOutcome(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.CaptureOverlayFailed, err)}, "")
	}
	// A session already on screen keeps the card hidden.
	if err != nil && !errors.Is(err, overlay.ErrSessionActive) {
		n.card.Resume()
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
		n.reportOutcome(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.SelectionCaptureFailed, errors.New(ui.CaptureServiceUnavailable))}, "")
		n.card.Resume()
		return
	}
	options := n.options.Options()
	n.inProgress = true
	n.pending.Add(1)
	go func() {
		defer n.pending.Done()
		result := n.captureRectangle(region, restore, options)
		n.postCompletion(func() { n.finishCapture(result, selectionCaptureMessages) })
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
	n.reportOutcome(captureNotice(result, messages), result.Path)
	n.card.Resume()
}

// captureNotice words one capture's outcome. A save error with a saved path
// means the image was saved and only its temporary name could not be removed,
// so the capture is reported as saved, with that name as a warning.
func captureNotice(result CaptureResult, messages captureMessages) ui.Notice {
	saveErr, leftBehind := result.Save, ""
	if result.Path != "" && saveErr != nil {
		leftBehind = fmt.Sprintf(ui.CaptureTemporaryLeftBehind, saveErr)
		saveErr = nil
	}
	switch {
	case result.Err != nil:
		return ui.Notice{Kind: ui.NoticeError, Message: captureFailureMessage(messages.failed, result.Err)}
	case result.Clipboard == nil && saveErr == nil:
		kind := ui.NoticeSuccess
		if leftBehind != "" {
			kind = ui.NoticeWarning
		}
		return ui.Notice{Kind: kind, Message: fmt.Sprintf(messages.succeeded, result.Path) + leftBehind}
	case saveErr != nil && result.Clipboard == nil:
		return ui.Notice{Kind: ui.NoticeWarning, Message: fmt.Sprintf(messages.saveFailed, saveErr)}
	case result.Clipboard != nil && saveErr == nil:
		return ui.Notice{Kind: ui.NoticeWarning, Message: fmt.Sprintf(messages.clipboardFailed, result.Path, result.Clipboard) + leftBehind}
	default:
		return ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(messages.outputsFailed, saveErr, result.Clipboard)}
	}
}

func (n *NativeView) post(task func()) {
	if n.dispatcher == nil {
		task()
		return
	}
	// A reveal's failure is worth one try: losing it leaves nothing stuck.
	_ = n.dispatcher.Post(task)
}

// completionRetryDelay spaces the attempts to deliver a capture's result.
const completionRetryDelay = 20 * time.Millisecond

// postCompletion delivers a capture's result to the GUI thread. Only the
// result ends the capture and brings the result card back, so a post that
// fails while the window lives, as with a full message queue, is tried again.
// Once the window is gone the result has nowhere to go and is dropped, as the
// capture pipeline has already restored the window state, which lets
// NativeShutdown's wait end.
func (n *NativeView) postCompletion(task func()) {
	if n.dispatcher == nil {
		task()
		return
	}
	for n.dispatcher.Post(task) != nil {
		if !n.windowAlive() {
			return
		}
		time.Sleep(completionRetryDelay)
	}
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
// connected; otherwise the cursor display becomes the default. A failed
// enumeration says nothing about which displays are connected, so it keeps
// the list and the selection for the next enumeration to check, and displayErr
// refuses a capture until one succeeds (SCR-040). It runs on the GUI thread,
// including from the display-change subscription.
func (n *NativeView) refreshDisplays() {
	// The picker is hidden outside Full Display mode, so a failure notice
	// only makes sense there; NativeAction and selectMode surface displayErr
	// when the mode is selected.
	if err := n.reloadDisplays(); err != nil && n.options.Options().Mode == ModeFullDisplay {
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.DisplayUnavailable, err)}, 0)
	}
}

// reloadDisplays is refreshDisplays without its notice, returning a failed
// enumeration for the caller to report.
func (n *NativeView) reloadDisplays() error {
	if n.displayCombo == nil {
		return nil
	}
	previous := n.selectedDeviceName()
	if n.displays == nil {
		n.displayErr = errors.New(ui.DisplayServiceUnavailable)
		n.displayList = nil
		n.displayCombo.Reset()
		return nil
	}
	displays, err := n.listDisplays()
	if err != nil {
		n.displayErr = err
		return err
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
	return nil
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
	n.card.WithdrawSetting(n.cardConflict)
	n.shortcut.Error.SetText("")
	n.shortcut.Edit.SetText(shortcut.String())
	err = n.options.SetShortcut(shortcut)
	n.presentResult(err, fmt.Sprintf(ui.ShortcutSaved, shortcut))
}

// restoreHotkey re-registers the persisted combination after the shortcut
// field's focus suspension or a rejected edit. A failure here means the
// combination itself became unavailable — another application claimed it in
// the meantime — so the caller reports it like any registration failure.
// Success ends any earlier failure still shown in the status area.
func (n *NativeView) restoreHotkey(shortcut Shortcut) error {
	if n.hotkeys == nil {
		return nil
	}
	err := n.hotkeys.Apply(shortcut)
	if err == nil {
		n.card.WithdrawSetting(n.cardConflict)
		n.status.ClearShown(n.shortcutNotice)
	}
	return err
}

// presentShortcutFailure reports a registration problem beside the shortcut
// field and in the status area, categorized as a shortcut-registration
// failure (UX-010). The status area announces it, so the field shows it
// quietly: a screen reader would otherwise read the same failure twice in a
// row.
func (n *NativeView) presentShortcutFailure(err error) {
	message := ui.FormatError(ui.ErrorShortcutRegistration, err)
	n.shortcut.Error.SetTextQuietly(message)
	_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: message}, 0)
	n.shortcutNotice = n.status.Revision()
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
	n.refreshOptions()
	n.status.Clear()
}

// refreshOptions shows the capture options in their controls, clearing the
// quality field's message.
func (n *NativeView) refreshOptions() {
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
}

// NativeRestoreDefaults shows the capture options the restored default
// settings hold (SET-005) and registers their global shortcut in place of
// the one registered. A default shortcut another application holds is
// reported as a startup conflict is, beside the field and in the status
// area, and the combination the settings no longer name is released rather
// than left registered behind a field that shows another. The status area
// keeps its notice, such as the last capture's result, unless it shows a
// shortcut failure, an option change's outcome, or Start Capture's refusal
// of the options, about a setting the restore replaced. What the user still
// has to do — a shortcut to choose — or a library or options that could not
// be shown is returned for the Settings view to report with the restore's
// outcome; an output folder that has no default is left to the shell's
// note, which gives the reason startup found. The preset library is shown
// again only when the restore kept another than the one shown, as when the
// session never loaded the settings file it replaced
// (preset.NativeManager.Reload); otherwise the selected preset and the
// preset form's unsaved edits are not touched (APP-004).
func (n *NativeView) NativeRestoreDefaults() string {
	var followUps []string
	if err := n.presets.Reload(); err != nil {
		followUps = append(followUps, fmt.Sprintf(ui.PresetOperationFailed, err))
	}
	if err := n.options.Reload(); err != nil {
		message := fmt.Sprintf(ui.OptionsOperationFailed, err)
		_ = n.status.Show(ui.Notice{Kind: ui.NoticeError, Message: message}, 0)
		return strings.Join(append(followUps, message), " ")
	}
	n.refreshOptions()
	options := n.options.Options()
	n.shortcut.Edit.SetText(options.Shortcut.String())
	n.shortcut.Error.SetText("")
	n.card.WithdrawSettings()
	n.status.ClearShown(n.shortcutNotice, n.optionsNotice)
	if n.hotkeys != nil {
		if err := n.hotkeys.Apply(options.Shortcut); err != nil {
			n.hotkeys.Release()
			n.presentShortcutFailure(err)
			followUps = append(followUps, ui.FormatError(ui.ErrorShortcutRegistration, err))
		}
	}
	return strings.Join(followUps, " ")
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
	// PNG does not use the field, so a refusal of its text no longer applies.
	// Choosing JPEG again checks the text at the next commit or capture.
	if options.Format != FormatJPEG {
		n.quality.Error.SetText("")
	}
	n.pattern.SetText(options.FilenameExample())
}

// presentResult shows an option change's outcome in the status area and
// records it as the option notice (optionsNotice).
func (n *NativeView) presentResult(err error, success string) {
	_ = n.status.Show(optionNotice(err, success), 0)
	n.optionsNotice = n.status.Revision()
}

// optionNotice words an option change's outcome.
func optionNotice(err error, success string) ui.Notice {
	switch {
	case errors.Is(err, ErrPersistence):
		return ui.Notice{Kind: ui.NoticeWarning, Message: fmt.Sprintf(ui.OptionsSaveFailed, err)}
	case err != nil:
		return ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.OptionsOperationFailed, err)}
	}
	return ui.Notice{Kind: ui.NoticeSuccess, Message: success}
}

// refuseOptions shows Start Capture's refusal of the capture options, or of
// the JPEG quality text the options would take, in the status area, and
// records it as the option notice (optionsNotice), as the restore of the
// default settings replaces what it refuses.
func (n *NativeView) refuseOptions(err error) {
	notice := ui.Notice{Kind: ui.NoticeError, Message: ui.FormatError(ui.ErrorValidation, err)}
	onCard := n.showOutcome(notice)
	n.optionsNotice = n.status.Revision()
	if onCard {
		n.card.ReportSetting(notice)
	}
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
