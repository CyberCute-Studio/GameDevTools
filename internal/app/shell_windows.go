//go:build windows

package app

import (
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/diagnostics"
	"github.com/CyberCute-Studio/GameDevTools/internal/display"
	"github.com/CyberCute-Studio/GameDevTools/internal/preset"
	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	core "github.com/CyberCute-Studio/GameDevTools/internal/shell"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

const (
	shellClassName = "GameDevToolsMainWindow"
	navCommandBase = 100
	// actionCommand identifies the primary action button; it stays clear of
	// the navigation commands and module command bases.
	actionCommand = 99
)

// endSessionWait bounds how long the end of the Windows session waits for
// the settings to be saved and the background work to finish (endSession),
// counted from WM_ENDSESSION's arrival. Windows lists an application that
// has not returned from WM_ENDSESSION within a few seconds as holding up the
// sign-out, restart, or shutdown, and may then end its process, so the wait
// stays under that: the save, a cancelled job's images in flight, and a
// capture delivering its outputs normally finish at once, and work that has
// not finished by then — a read or write blocked on an unresponsive network
// drive, such as a settings folder redirected to a share that has gone,
// which no wait would see through — is left to the process's end rather
// than holding the session's end up. It is a variable so tests can shorten
// it.
var endSessionWait = 3 * time.Second

type nativeShell struct {
	navigator   *core.Navigator
	dispatcher  *core.DispatchQueue
	store       *settings.Store
	settings    *settings.Settings
	logger      *diagnostics.Logger
	displays    *display.Monitor
	notice      string
	hwnd        windows.Handle
	dpi         uint32
	scrollY     int32
	navHeading  windows.Handle
	navButtons  map[core.ModuleID]*ui.Button
	navIDs      map[uint16]core.ModuleID
	nativeViews map[core.ModuleID]nativeView
	title       windows.Handle
	description windows.Handle
	action      *ui.Button
	note        *ui.StatusArea
	headingFont windows.Handle
	bodyFont    windows.Handle

	// acceptingDrops is whether the shell window and its children are
	// registered for file drops, as updateDropAcceptance last applied it.
	acceptingDrops bool

	// focus is the control that last had the keyboard focus, which
	// restoreFocus gives it back to when the window is activated again.
	focus windows.Handle
	// clickActivating is whether the window is being activated by a mouse
	// click, whose focus restoreFocus leaves to the click, as wmActivate
	// describes.
	clickActivating bool
	// keepScroll is whether a control's set-focus notification leaves the
	// view's scroll position alone, as it does while the focus is given back
	// after a click activation (wmAppRestoreFocus).
	keepScroll bool

	// announced holds the modules whose notice was announced once shown in
	// the visible window, as announceModuleNotice describes; it is nil until
	// the window first appears.
	announced map[core.ModuleID]bool

	// confirm asks the user a yes-or-no question, its safe answer No the
	// default, as ui.Confirm does, which answers No once the end of the
	// Windows session has destroyed the window under the question
	// (endSession); tests substitute it.
	confirm func(windows.Handle, string, string) bool
	// startup holds the warnings the startup notice is made of, by what
	// decides whether each still applies once the default settings are
	// restored (settleNotice) or the settings are saved (saveSettings).
	startup startupWarnings
	// backgroundWork is closed once every module view's background work has
	// finished (stopBackgroundWork); it is nil until the shell first waits
	// for the work, which it starts waiting for once: the end of the Windows
	// session waits inside WM_ENDSESSION for a while (endSession), and the
	// message loop's end then waits only for what is left.
	backgroundWork chan struct{}
	// sessionSaved is closed once the settings save the end of the Windows
	// session started has finished; it is nil until endSession starts it.
	// The message loop's end, should the process outlive WM_ENDSESSION,
	// waits for it (waitForBackgroundWork).
	sessionSaved chan struct{}
}

// startupWarnings are the warnings the shell's note shows as the
// application starts, kept by what decides whether each still applies once
// the settings are saved (saveSettings) or the default settings are
// restored (settleNotice): each is placed by what it is about where it is
// made, so neither need work out again which lines of a joined notice still
// apply.
type startupWarnings struct {
	// recovered is the warning that the malformed settings file the session
	// started from was backed up and the defaults took its place, naming the
	// backup, or "" (settings.LoadResult.Recovered). No save or restore
	// settles it: the backup, which holds that file's settings, stays where
	// it names.
	recovered string
	// unwritten is the warning that the settings the session started from —
	// the defaults that replaced a malformed file, or the migrated settings —
	// could not be written to the file, or "" (settings.LoadResult.Unwritten),
	// which any save that succeeds, the restore's included, settles.
	unwritten string
	// settingsFile are the other warnings about the settings file the
	// session started from — that it could not be located, read, or backed
	// up, so saving is disabled — which a restore that backs the file up, or
	// finds none to back up, settles, but for those about a settings path
	// there is not.
	settingsFile []string
	// folders returns the output folders' warnings as the options now hold
	// them, which a restore or the choice of a folder can change; nil
	// returns none.
	folders func() []string
	// other are the warnings the settings do not decide, such as diagnostic
	// logging being unavailable, which still apply once they are restored.
	other []string
}

// text returns the warnings that apply, one to a line: the settings file's,
// in the order loading the settings gave them, the output folders', and the
// others.
func (w startupWarnings) text() string {
	var warnings []string
	for _, warning := range []string{w.recovered, w.unwritten} {
		if warning != "" {
			warnings = append(warnings, warning)
		}
	}
	warnings = append(warnings, w.settingsFile...)
	if w.folders != nil {
		warnings = append(warnings, w.folders()...)
	}
	return strings.Join(append(warnings, w.other...), "\n")
}

type nativeView interface {
	NativeCreate(windows.Handle) error
	NativeFont(windows.Handle)
	NativeShow(bool)
	NativeHeight(int32, func(int32) int32, func(string, int32) int32) int32
	NativeLayout(int32, int32, int32, func(int32) int32, func(string, int32) int32)
	NativeCommand(int32, uint16) bool
	NativeDestroy()
}

type nativeViewProvider interface {
	NativeView() any
}

// nativeActionHandler is implemented by module views that run the shell's
// primary action button, such as the Screenshot module's Start Capture.
type nativeActionHandler interface {
	NativeAction()
}

// nativeActionAvailability is implemented by module views whose primary
// action is unavailable for a while, such as Start Resize while a resize job
// runs. The shell disables the primary action button while the view reports
// it unavailable, and re-reads the availability on every relayout, which
// follows every module command, action, and dispatched task. Before it
// disables the button while the button has the keyboard focus, the shell
// moves the focus to the next control in tab order, so the keyboard is never
// stranded on a disabled button; a view may move it first to a better place,
// as Start Resize moves it to Cancel Resize.
type nativeActionAvailability interface {
	NativeActionAvailable() bool
}

// nativeDispatcherAware is implemented by module views that post background
// results back to the GUI thread through the shell dispatcher.
type nativeDispatcherAware interface {
	NativeDispatcher(core.Dispatcher)
}

// nativeMessageFilter is implemented by module views that own windows outside
// the shell — the Screenshot overlay session — and must pre-handle their
// queued messages: Escape cancels the session, and the floating toolbar
// receives dialog navigation even though it is a popup.
type nativeMessageFilter interface {
	NativeFilterMessage(message unsafe.Pointer) bool
}

// nativeHotkeyHandler is implemented by module views that own a global
// hotkey registered on the shell window — the Screenshot global capture
// shortcut. WM_HOTKEY carries the registration identifier in wParam; the
// handler reports whether it consumed the hotkey.
type nativeHotkeyHandler interface {
	NativeHotkey(id int32) bool
}

// nativeNoticeAnnouncer is implemented by module views whose status area can
// take a notice while the main window, or the view, is still hidden, such as
// a global shortcut another application holds at startup. The shell asks a
// module's view to announce its notice the first time the module is shown
// in the visible window, as announceModuleNotice describes.
type nativeNoticeAnnouncer interface {
	NativeAnnounceNotice()
}

// nativeShutdown is implemented by module views with background work, such
// as an in-flight capture, that must finish delivering before the process
// exits. The shell calls NativeShutdown once the window is destroyed, on a
// goroutine of its own (stopBackgroundWork), and it waits for that work.
type nativeShutdown interface {
	NativeShutdown()
}

// nativeDropTarget is implemented by module views that accept dropped files
// — the Image Resizer source list (RSZ-002). The shell registers the window
// for drag-and-drop only while the active module implements it, so drops on
// other modules keep the no-drop cursor.
type nativeDropTarget interface {
	NativeDropFiles(paths []string)
}

// nativeDropAvailability is implemented by drop targets that refuse drops
// for a while, such as the Image Resizer source list while a resize job runs.
// The shell registers the window for drops only while the view reports them
// available, re-reading it on every relayout as it re-reads the primary
// action's availability, so a drop the view would refuse keeps the no-drop
// cursor instead of seeming to be accepted.
type nativeDropAvailability interface {
	NativeDropAvailable() bool
}

// nativeRestoreRequester is implemented by the Settings view, whose Restore
// Default Settings runs the shell's restore of the default settings
// (restoreDefaults) once the user confirms it.
type nativeRestoreRequester interface {
	NativeRestoreHandler(func() ui.Notice)
}

// nativeDefaultsRestorer is implemented by module views that show settings
// — the Screenshot capture options and the Image Resizer resize options —
// which show them again once the default settings are restored, and return
// what the user still has to do, such as choose a global shortcut another
// application holds, or "". An output folder that has no default is left to
// the shell's note, which gives the reason startup found (settleNotice).
type nativeDefaultsRestorer interface {
	NativeRestoreDefaults() string
}

// nativeCloseGuard is implemented by module views with work that closing the
// application cancels — the Image Resizer's running job. It returns the
// question closing asks first, or an empty message when closing loses
// nothing the user did not already ask to stop.
type nativeCloseGuard interface {
	NativeCloseConfirmation() (title, message string)
}

type shellLayout struct {
	compact           bool
	margin            int32
	navY              int32
	navWidth          int32
	navHeight         int32
	contentX          int32
	contentY          int32
	contentWidth      int32
	titleHeight       int32
	descriptionY      int32
	descriptionHeight int32
	actionY           int32
	moduleY           int32
	moduleHeight      int32
	noteY             int32
	noteHeight        int32
	virtualHeight     int32
}

type shellTextHeights struct {
	title        int32
	description  int32
	note         int32
	moduleHeight int32
}

var shellStates = map[windows.Handle]*nativeShell{}

// runShell runs the main window until it closes, its note showing the
// startup warnings that apply.
func runShell(navigator *core.Navigator, store *settings.Store, initial *settings.Settings, logger *diagnostics.Logger, displays *display.Monitor, startup startupWarnings) error {
	shell, err := newNativeShell(navigator, store, initial, logger, displays, startup)
	if err != nil {
		return err
	}
	hwnd := uintptr(shell.hwnd)
	showCommand := uintptr(swShow)
	if initial.Shell.Window.Maximized {
		showCommand = swShowMaximized
	}
	procShowWindow.Call(hwnd, showCommand)
	procUpdateWindow.Call(hwnd)
	shell.announceNotices()
	var message msg
	for {
		r, _, err := procGetMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(r) == -1 {
			return callError("GetMessageW", err)
		}
		if r == 0 {
			shell.waitForBackgroundWork()
			return nil
		}
		if shell.filterModuleMessage(&message) {
			continue
		}
		if handled, _, _ := procIsDialogMessageW.Call(hwnd, uintptr(unsafe.Pointer(&message))); handled != 0 {
			continue
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
	}
}

// newNativeShell creates the main window with its controls and every module's
// native view, laid out but not yet shown, its note showing the startup
// warnings.
func newNativeShell(navigator *core.Navigator, store *settings.Store, initial *settings.Settings, logger *diagnostics.Logger, displays *display.Monitor, startup startupWarnings) (*nativeShell, error) {
	if err := registerShellClass(); err != nil {
		return nil, err
	}
	shell := &nativeShell{
		navigator:   navigator,
		store:       store,
		settings:    initial,
		logger:      logger,
		displays:    displays,
		startup:     startup,
		notice:      startup.text(),
		dpi:         96,
		navButtons:  make(map[core.ModuleID]*ui.Button),
		navIDs:      make(map[uint16]core.ModuleID),
		nativeViews: make(map[core.ModuleID]nativeView),
		confirm:     ui.Confirm,
	}
	window := initial.Shell.Window
	x, y := uintptr(cwUseDefault), uintptr(cwUseDefault)
	if window.HasPosition {
		x, y = uintptr(uint32(window.X)), uintptr(uint32(window.Y))
	}
	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(utf16(shellClassName))),
		uintptr(unsafe.Pointer(utf16(ui.AppName))),
		wsOverlappedWindow|wsClipChildren|wsVScroll,
		x, y, uintptr(window.Width), uintptr(window.Height),
		0, 0, 0, 0,
	)
	if hwnd == 0 {
		return nil, callError("CreateWindowExW(main)", err)
	}
	shell.hwnd = windows.Handle(hwnd)
	shell.dispatcher = core.NewDispatchQueue(func() error {
		return postShellMessage(hwnd, wmAppDispatch)
	})
	shellStates[shell.hwnd] = shell
	if dpi, _, _ := procGetDpiForWindow.Call(hwnd); dpi != 0 {
		shell.dpi = uint32(dpi)
	}
	setWindowIcons(shell.hwnd, shell.dpi)
	if err := shell.createControls(); err != nil {
		procDestroyWindow.Call(hwnd)
		return nil, err
	}
	shell.updateFonts()
	shell.updateView()
	shell.fitInitialWindow()
	shell.layout()
	return shell, nil
}

var (
	registerShellClassOnce sync.Once
	registerShellClassErr  error
)

// registerShellClass registers the main window's class once per process,
// however many shells are created, as tests create several.
func registerShellClass() error {
	registerShellClassOnce.Do(func() {
		registerShellClassErr = registerShellClassNow()
	})
	return registerShellClassErr
}

func registerShellClassNow() error {
	instance, _, err := procGetModuleHandleW.Call(0)
	if instance == 0 {
		return callError("GetModuleHandleW", err)
	}
	cursor, _, err := procLoadCursorW.Call(0, 32512)
	if cursor == 0 {
		return callError("LoadCursorW", err)
	}
	large, _, _ := procGetSystemMetrics.Call(smCxIcon)
	small, _, _ := procGetSystemMetrics.Call(smCxSmIcon)
	class := wndClassEx{
		LpfnWndProc:   windows.NewCallback(shellWindowProc),
		HInstance:     windows.Handle(instance),
		HIcon:         loadAppIcon(instance, large),
		HCursor:       windows.Handle(cursor),
		HbrBackground: windows.Handle(colorWindow + 1),
		LpszClassName: utf16(shellClassName),
		HIconSm:       loadAppIcon(instance, small),
	}
	class.CbSize = uint32(unsafe.Sizeof(class))
	atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class)))
	if atom == 0 {
		return callError("RegisterClassExW", err)
	}
	return nil
}

// appIcons holds the application icon at each pixel size loaded so far. They
// are not loaded with LR_SHARED, which LoadImage's documentation rules out for
// non-standard sizes such as a scaled 20 or 40 pixels. They last as long as
// the process: a class or window may hold any of them, and there are only as
// many as there are display scales in use.
var (
	appIconsMutex sync.Mutex
	appIcons      = make(map[uintptr]windows.Handle)
)

// loadAppIcon returns the executable's application icon at size pixels, or 0
// when the running program has no icon, as a test binary does, and the class
// then takes the default icon.
func loadAppIcon(instance, size uintptr) windows.Handle {
	appIconsMutex.Lock()
	defer appIconsMutex.Unlock()
	if icon, loaded := appIcons[size]; loaded {
		return icon
	}
	icon, _, _ := procLoadImageW.Call(instance, appIconResource, imageIcon, size, size, 0)
	// A failed load is not kept, so the next DPI change at this size retries.
	if icon != 0 {
		appIcons[size] = windows.Handle(icon)
	}
	return windows.Handle(icon)
}

// iconPixels is an icon's size in pixels at dpi, from its size at 100%.
func iconPixels(base uintptr, dpi uint32) uintptr {
	return (base*uintptr(dpi) + 48) / 96
}

// setWindowIcons gives the window the application icon at the sizes its DPI
// calls for. The class's icons are sized for the system DPI, which Windows
// would stretch on a display of another scale.
func setWindowIcons(hwnd windows.Handle, dpi uint32) {
	instance, _, _ := procGetModuleHandleW.Call(0)
	for _, icon := range []struct{ kind, size uintptr }{{iconSmall, 16}, {iconBig, 32}} {
		if handle := loadAppIcon(instance, iconPixels(icon.size, dpi)); handle != 0 {
			procSendMessageW.Call(uintptr(hwnd), wmSetIcon, icon.kind, uintptr(handle))
		}
	}
}

func (s *nativeShell) createControls() error {
	heading, err := ui.NewLabel(s.hwnd, ui.ToolsHeading)
	if err != nil {
		return err
	}
	s.navHeading = heading.Handle()
	for i, module := range s.navigator.Modules() {
		id := uint16(navCommandBase + i)
		button, err := ui.NewButton(s.hwnd, module.NavigationLabel(), ui.ButtonOptions{
			ID:    int32(id),
			Group: i == 0,
			Radio: true,
		})
		if err != nil {
			return err
		}
		s.navButtons[module.ID()] = button
		s.navIDs[id] = module.ID()
	}
	// The title starts a group, ending the navigation's radio group at its
	// last button, so the arrow keys wrap between the modules
	// (ui.NewGroupLabel).
	title, err := ui.NewGroupLabel(s.hwnd, "")
	if err != nil {
		return err
	}
	s.title = title.Handle()
	description, err := ui.NewLabel(s.hwnd, "")
	if err != nil {
		return err
	}
	s.description = description.Handle()
	if s.action, err = ui.NewButton(s.hwnd, "", ui.ButtonOptions{ID: actionCommand, Primary: true, Group: true, Disabled: true}); err != nil {
		return err
	}
	s.note, err = ui.NewStatusArea(s.hwnd)
	if err != nil {
		return err
	}
	for _, module := range s.navigator.Modules() {
		provider, ok := module.View().(nativeViewProvider)
		if !ok {
			continue
		}
		native, ok := provider.NativeView().(nativeView)
		if !ok {
			continue
		}
		if err := native.NativeCreate(s.hwnd); err != nil {
			return err
		}
		if aware, ok := native.(nativeDispatcherAware); ok {
			aware.NativeDispatcher(s.dispatcher)
		}
		if requester, ok := native.(nativeRestoreRequester); ok {
			requester.NativeRestoreHandler(s.restoreDefaults)
		}
		native.NativeShow(false)
		s.nativeViews[module.ID()] = native
	}
	return nil
}

func (s *nativeShell) updateFonts() {
	heading, headingErr := createFont(-s.scale(32), fwSemiBold)
	body, bodyErr := createFont(-s.scale(18), fwNormal)
	if headingErr == nil && bodyErr == nil {
		if s.headingFont != 0 {
			procDeleteObject.Call(uintptr(s.headingFont))
		}
		if s.bodyFont != 0 {
			procDeleteObject.Call(uintptr(s.bodyFont))
		}
		s.headingFont = heading
		s.bodyFont = body
	} else {
		if heading != 0 {
			procDeleteObject.Call(uintptr(heading))
		}
		if body != 0 {
			procDeleteObject.Call(uintptr(body))
		}
	}
	setControlFont(s.title, s.headingFont)
	for _, control := range []windows.Handle{s.navHeading, s.description, s.action.Handle(), s.note.Handle()} {
		setControlFont(control, s.bodyFont)
	}
	for _, button := range s.navButtons {
		setControlFont(button.Handle(), s.bodyFont)
	}
	for _, native := range s.nativeViews {
		native.NativeFont(s.bodyFont)
	}
}

func (s *nativeShell) updateView() {
	current := s.navigator.Current()
	view := current.View()
	setWindowText(s.title, view.Title())
	setWindowText(s.description, view.Description())
	s.action.SetText(view.PrimaryAction())
	s.showNote()
	s.action.Show(view.PrimaryAction() != "")
	s.refreshAction()
	// SetChecked keeps the current module's button the navigation's only
	// tab stop, so Tab passes the navigation without switching modules.
	for id, button := range s.navButtons {
		button.SetChecked(id == current.ID())
	}
	for id, native := range s.nativeViews {
		native.NativeShow(id == current.ID())
	}
	s.announceModuleNotice()
	s.updateDropAcceptance()
}

// showNote puts the shell's note on display. Only a warning is news. The
// placeholder note of a module without a native view belongs to its view, as
// the view's title and description do, so it is shown quietly: switching to
// the module would otherwise announce it on every visit.
func (s *nativeShell) showNote() {
	if s.notice != "" {
		s.note.SetText(s.noteText())
	} else {
		s.note.SetTextQuietly(s.noteText())
	}
}

// settleStartupNotice brings the note up to date after a module command, given
// the startup warnings as they stood before it, so that an output folder's
// warning leaves as soon as the user chooses a folder rather than go on saying
// captures are not saved. A note showing anything else, such as a failed
// save, stays. The command's relayout fits the note's new height.
func (s *nativeShell) settleStartupNotice(before string) {
	if s.notice != before {
		return
	}
	if after := s.startup.text(); after != before {
		s.notice = after
		s.showNote()
	}
}

// refreshAction enables the primary action button only while the current
// module has a native view that runs the action and, when it reports the
// action's availability, reports it available. A button it disables while
// the button has the keyboard focus first hands the focus to the next
// control in tab order (AC-A03), or, when there is none, to the current
// module's navigation button, and in a shell without one to the window
// itself, where Tab reaches the first control again.
func (s *nativeShell) refreshAction() {
	enabled := s.actionEnabled()
	if !enabled && s.action.HasFocus() {
		next, _, _ := procGetNextDlgTabItem.Call(uintptr(s.hwnd), uintptr(s.action.Handle()), 0)
		if next == 0 || windows.Handle(next) == s.action.Handle() {
			next = uintptr(s.currentNavButton())
		}
		if next == 0 {
			// The window's focus runs restoreFocus, which would give the
			// focus straight back to the button, still enabled.
			s.focus = 0
			next = uintptr(s.hwnd)
		}
		procSetFocus.Call(next)
	}
	s.action.Enable(enabled)
}

// announceNotices asks a screen reader to read the notices on display once
// the window first appears: the shell's startup warning, such as settings
// that were recovered, and the current module's own (announceModuleNotice).
// Each was announced as it was set, while the window was still hidden and no
// screen reader was reading it, and an unchanged text is not announced again.
// A placeholder note is not a notice, as updateView describes.
func (s *nativeShell) announceNotices() {
	if s.notice != "" {
		s.note.Announce()
	}
	s.announced = make(map[core.ModuleID]bool)
	s.announceModuleNotice()
}

// announceModuleNotice asks the current module's view to announce its notice
// (nativeNoticeAnnouncer) the first time the module is shown once the window
// has appeared. A module hidden at startup takes its startup notice, such as
// a global shortcut another application holds while Image Resizer is the
// remembered module, where no screen reader hears it, and showing the
// unchanged notice later announces nothing. Later visits announce nothing,
// so switching modules does not repeat a notice already heard.
func (s *nativeShell) announceModuleNotice() {
	id := s.navigator.Current().ID()
	if s.announced == nil || s.announced[id] {
		return
	}
	s.announced[id] = true
	if announcer, ok := s.nativeViews[id].(nativeNoticeAnnouncer); ok {
		announcer.NativeAnnounceNotice()
	}
}

// reportsFocus reports whether a WM_COMMAND notification from the control
// says it gained the keyboard focus. A list box's LBN_SETFOCUS shares its
// code, 4, with a combo box's CBN_KILLFOCUS, which arrives once the focus
// has left the combo box for another control, and a combo box's
// CBN_SETFOCUS shares its code, 3, with a list box's LBN_SELCANCEL, so those
// codes count only while the control holds the focus. The combo boxes are
// drop-down lists, which hold the focus themselves.
func reportsFocus(notification uint16, control uintptr) bool {
	if control == 0 {
		return false
	}
	switch notification {
	case bnSetFocus, enSetFocus:
		return true
	case cbnSetFocus, lbnSetFocus:
		focus, _, _ := procGetFocus.Call()
		return focus == control
	}
	return false
}

// currentNavButton returns the current module's navigation button, or 0
// when the shell has none.
func (s *nativeShell) currentNavButton() windows.Handle {
	if button, exists := s.navButtons[s.navigator.Current().ID()]; exists {
		return button.Handle()
	}
	return 0
}

// restoreFocus gives the keyboard focus that reached the shell window itself
// to a control (UX-006, AC-A03). Windows focuses a plain window, not the
// control inside it that had the focus, whenever the window is activated —
// at startup, on Alt+Tab or a click on its title bar, and when the user
// returns to it after a capture, which shows it again without activating it
// (capture.Exclusion.Restore) — and a window holding the focus shows no focus
// indicator, while Tab would start again from the first control. So the
// control that last had the focus gets it back while it can take it. One
// still shown that can no longer take it — Cancel Resize, disabled as a job
// finished while the window was inactive, when its own hand-off could not
// see the focus — hands it to the next control in tab order, as a control
// disabled while it holds the focus does (AC-A03). Otherwise, as at startup
// or with its module hidden, the current module's navigation button gets it.
func (s *nativeShell) restoreFocus() {
	target := s.focus
	if !s.canTakeFocus(target) && s.shown(target) {
		next, _, _ := procGetNextDlgTabItem.Call(uintptr(s.hwnd), uintptr(target), 0)
		target = windows.Handle(next)
	}
	if !s.canTakeFocus(target) {
		target = s.currentNavButton()
	}
	if target != 0 {
		procSetFocus.Call(uintptr(target))
	}
}

// shown reports whether the control is still one of the window's controls
// and visible, as its module is shown.
func (s *nativeShell) shown(control windows.Handle) bool {
	if control == 0 {
		return false
	}
	if child, _, _ := procIsChild.Call(uintptr(s.hwnd), uintptr(control)); child == 0 {
		return false
	}
	visible, _, _ := procIsWindowVisible.Call(uintptr(control))
	return visible != 0
}

// canTakeFocus reports whether restoreFocus may give the control the focus:
// it is shown, enabled, and not a radio button that is unchecked, which
// would report a click on gaining the focus and change the selection the
// user made.
func (s *nativeShell) canTakeFocus(control windows.Handle) bool {
	if !s.shown(control) {
		return false
	}
	if enabled, _, _ := procIsWindowEnabled.Call(uintptr(control)); enabled == 0 {
		return false
	}
	if code, _, _ := procSendMessageW.Call(uintptr(control), wmGetDlgCode, 0, 0); code&dlgcRadioButton != 0 {
		checked, _, _ := procSendMessageW.Call(uintptr(control), bmGetCheck, 0, 0)
		return checked&bstChecked != 0
	}
	return true
}

func (s *nativeShell) actionEnabled() bool {
	current := s.navigator.Current()
	native, exists := s.nativeViews[current.ID()]
	return exists && current.View().PrimaryAction() != "" && hookAvailable[nativeActionHandler](native, nativeActionAvailability.NativeActionAvailable)
}

// hookAvailable reports whether the native view implements the hook H and,
// when it reports the hook's availability as the optional A, reports it
// available, as available asks.
func hookAvailable[H, A any](native nativeView, available func(A) bool) bool {
	if _, implements := native.(H); !implements {
		return false
	}
	if availability, reports := native.(A); reports {
		return available(availability)
	}
	return true
}

func (s *nativeShell) noteText() string {
	if s.notice != "" {
		return ui.Notice{Kind: ui.NoticeWarning, Message: s.notice}.Text()
	}
	if _, exists := s.nativeViews[s.navigator.Current().ID()]; exists {
		return ""
	}
	return ui.PlaceholderNote
}

func (s *nativeShell) fitInitialWindow() {
	width := s.scale(1100)
	height := s.scale(700)
	if s.settings.Shell.Window.HasPosition {
		width = s.settings.Shell.Window.Width
		height = s.settings.Shell.Window.Height
	}
	flags := uintptr(swpNoZOrder | swpNoActivate | swpNoMove)
	var x, y int32
	var current rect
	if r, _, _ := procGetWindowRect.Call(uintptr(s.hwnd), uintptr(unsafe.Pointer(&current))); r != 0 {
		x = current.Left
		y = current.Top
		flags &^= swpNoMove
		monitor, _, _ := procMonitorFromWindow.Call(uintptr(s.hwnd), monitorDefaultToNearest)
		if monitor != 0 {
			info := monitorInfo{CbSize: uint32(unsafe.Sizeof(monitorInfo{}))}
			if r, _, _ := procGetMonitorInfoW.Call(monitor, uintptr(unsafe.Pointer(&info))); r != 0 {
				x, y, width, height = fitStartupBounds(x, y, width, height, info.Work)
			}
		}
	}
	procSetWindowPos.Call(uintptr(s.hwnd), 0, uintptr(x), uintptr(y), uintptr(width), uintptr(height), flags)
}

func fitStartupBounds(x, y, width, height int32, work rect) (int32, int32, int32, int32) {
	workWidth := int64(work.Right) - int64(work.Left)
	workHeight := int64(work.Bottom) - int64(work.Top)
	if int64(width) > workWidth {
		width = int32(workWidth)
	}
	if int64(height) > workHeight {
		height = int32(workHeight)
	}
	if int64(x)+int64(width) > int64(work.Right) {
		x = int32(int64(work.Right) - int64(width))
	}
	if int64(y)+int64(height) > int64(work.Bottom) {
		y = int32(int64(work.Bottom) - int64(height))
	}
	if x < work.Left {
		x = work.Left
	}
	if y < work.Top {
		y = work.Top
	}
	return x, y, width, height
}

func calculateShellLayout(client rect, dpi uint32, moduleCount int, text shellTextHeights) shellLayout {
	scale := func(value int32) int32 {
		return int32((int64(value)*int64(dpi) + 48) / 96)
	}
	if text.title <= 0 {
		text.title = scale(48)
	}
	if text.description <= 0 {
		text.description = scale(90)
	}
	if text.note <= 0 {
		text.note = scale(28)
	}
	plan := shellLayout{
		margin:            scale(32),
		navY:              scale(80),
		navWidth:          scale(210),
		navHeight:         scale(44),
		contentY:          scale(32),
		titleHeight:       text.title,
		descriptionHeight: text.description,
		moduleHeight:      text.moduleHeight,
		noteHeight:        text.note,
	}
	plan.contentX = plan.margin + plan.navWidth + scale(40)
	plan.compact = client.Right < scale(524) || client.Right-plan.contentX-plan.margin < scale(190)
	if plan.compact {
		plan.margin = scale(16)
		plan.contentX = plan.margin
		plan.contentY = plan.margin
		plan.contentWidth = max(int32(1), client.Right-2*plan.margin)
		plan.navWidth = plan.contentWidth
		plan.actionY = plan.contentY + plan.titleHeight + scale(12)
		plan.navY = plan.actionY + scale(44) + scale(16)
		navBottom := plan.navY + int32(moduleCount)*plan.navHeight + int32(moduleCount-1)*scale(8)
		plan.descriptionY = navBottom + scale(16)
		plan.moduleY = plan.descriptionY + plan.descriptionHeight + scale(12)
		plan.noteY = plan.moduleY + plan.moduleHeight
		if plan.moduleHeight > 0 {
			plan.noteY += scale(12)
		}
		plan.virtualHeight = plan.noteY + plan.noteHeight + plan.margin
	} else {
		plan.contentWidth = max(scale(190), client.Right-plan.contentX-plan.margin)
		plan.descriptionY = plan.contentY + plan.titleHeight + scale(12)
		plan.actionY = plan.descriptionY + plan.descriptionHeight + scale(16)
		plan.moduleY = plan.actionY + scale(44)
		if plan.moduleHeight > 0 {
			plan.moduleY += scale(16)
		}
		plan.noteY = plan.moduleY + plan.moduleHeight + scale(12)
		navBottom := plan.navY + int32(moduleCount)*plan.navHeight + int32(moduleCount-1)*scale(8)
		contentBottom := plan.noteY + plan.noteHeight
		if client.Bottom >= contentBottom+plan.margin && client.Bottom >= navBottom+plan.margin {
			plan.noteY = client.Bottom - plan.margin - plan.noteHeight
			plan.virtualHeight = client.Bottom
		} else {
			plan.virtualHeight = max(contentBottom, navBottom) + plan.margin
		}
	}
	return plan
}

func (s *nativeShell) layout() {
	var client rect
	r, _, _ := procGetClientRect.Call(uintptr(s.hwnd), uintptr(unsafe.Pointer(&client)))
	if r == 0 {
		return
	}
	modules := s.navigator.Modules()
	basePlan := calculateShellLayout(client, s.dpi, len(modules), shellTextHeights{})
	view := s.navigator.Current().View()
	measureModuleText := func(text string, width int32) int32 {
		return s.measureText(text, width, s.bodyFont)
	}
	moduleHeight := int32(0)
	if native, exists := s.nativeViews[s.navigator.Current().ID()]; exists {
		moduleHeight = native.NativeHeight(basePlan.contentWidth, s.scale, measureModuleText)
	}
	plan := calculateShellLayout(client, s.dpi, len(modules), shellTextHeights{
		title:        s.measureText(view.Title(), basePlan.contentWidth, s.headingFont),
		description:  measureModuleText(view.Description(), basePlan.contentWidth),
		note:         measureModuleText(s.noteText(), basePlan.contentWidth),
		moduleHeight: moduleHeight,
	})
	info := scrollInfo{
		CbSize: uint32(unsafe.Sizeof(scrollInfo{})),
		FMask:  sifRange | sifPage | sifPos,
		NMax:   max(int32(0), plan.virtualHeight-1),
		NPage:  uint32(max(int32(0), client.Bottom)),
		NPos:   s.scrollY,
	}
	position, _, _ := procSetScrollInfo.Call(uintptr(s.hwnd), sbVert, uintptr(unsafe.Pointer(&info)), 1)
	s.scrollY = int32(position)
	headingVisibility := int32(swShow)
	if plan.compact {
		headingVisibility = swHide
	}
	procShowWindow.Call(uintptr(s.navHeading), uintptr(headingVisibility))
	moveWindow(s.navHeading, plan.margin, plan.margin-s.scrollY, plan.navWidth, s.scale(24))
	for i, module := range modules {
		if button, exists := s.navButtons[module.ID()]; exists {
			y := plan.navY + int32(i)*(plan.navHeight+s.scale(8))
			moveWindow(button.Handle(), plan.margin, y-s.scrollY, plan.navWidth, plan.navHeight)
		}
	}
	contentY := func(y int32) int32 { return y - s.scrollY }
	moveWindow(s.title, plan.contentX, contentY(plan.contentY), plan.contentWidth, plan.titleHeight)
	moveWindow(s.description, plan.contentX, contentY(plan.descriptionY), plan.contentWidth, plan.descriptionHeight)
	moveWindow(s.action.Handle(), plan.contentX, contentY(plan.actionY), min(s.scale(190), plan.contentWidth), s.scale(44))
	if native, exists := s.nativeViews[s.navigator.Current().ID()]; exists {
		native.NativeLayout(plan.contentX, contentY(plan.moduleY), plan.contentWidth, s.scale, measureModuleText)
	}
	s.note.Move(plan.contentX, contentY(plan.noteY), plan.contentWidth, plan.noteHeight)
	// Every module command, action, and dispatched task ends in a relayout,
	// so the primary action's and the drops' availability are re-read here;
	// the drop registration is only re-applied when it changed, as applying
	// it visits every child control.
	s.refreshAction()
	if s.dropsAvailable() != s.acceptingDrops {
		s.updateDropAcceptance()
	}
}

// waitForBackgroundWork lets in-flight module work, such as a capture still
// delivering its outputs, finish before the process exits, as the message
// loop ends (stopBackgroundWork). Each module bounds its own wait, a
// cancelled resize job's by imageresizer.JobShutdownWait, but for a capture,
// which is waited for until its outputs are delivered; the modules' waits
// run at once, so the longest of them is the whole wait. After the end of
// the Windows session it waits only for what endSession's wait left running,
// the settings save included, so the process — should it outlive
// WM_ENDSESSION, as when an installer's Restart Manager closes the
// application — releases its single-instance lock, which lets a new
// instance load the settings, only once that save has written them.
func (s *nativeShell) waitForBackgroundWork() {
	<-s.stopBackgroundWork()
	if s.sessionSaved != nil {
		<-s.sessionSaved
	}
}

// stopBackgroundWork starts waiting, once, for every module view's
// background work to finish (nativeShutdown), each view's on a goroutine of
// its own, so one view's wait does not hold another's up and a caller can
// stop waiting before they have (endSession), and returns the channel
// closed once they all have. The shell starts it only once the window is
// destroyed — after the message loop ended, or inside WM_ENDSESSION — so the
// work never waits on the GUI thread, its completion posts fail harmlessly
// against the destroyed window, and no module view has a window left to
// close: the window's destruction closed any capture session (NativeDestroy).
func (s *nativeShell) stopBackgroundWork() <-chan struct{} {
	if s.backgroundWork == nil {
		var stopped sync.WaitGroup
		for _, native := range s.nativeViews {
			if shutdown, ok := native.(nativeShutdown); ok {
				stopped.Add(1)
				go func() {
					defer stopped.Done()
					shutdown.NativeShutdown()
				}()
			}
		}
		done := make(chan struct{})
		s.backgroundWork = done
		go func() {
			stopped.Wait()
			close(done)
		}()
	}
	return s.backgroundWork
}

// handleDrop unpacks a WM_DROPFILES payload into paths and offers them to
// the active module when it is a drop target. The drop handle is always
// released, including when no module accepts it.
func (s *nativeShell) handleDrop(drop uintptr) bool {
	defer procDragFinish.Call(drop)
	native, exists := s.nativeViews[s.navigator.Current().ID()]
	if !exists {
		return false
	}
	target, ok := native.(nativeDropTarget)
	if !ok {
		return false
	}
	paths := dropPaths(drop)
	if len(paths) == 0 {
		return false
	}
	target.NativeDropFiles(paths)
	return true
}

// dropPaths reads every dropped path out of the drop handle.
func dropPaths(drop uintptr) []string {
	count, _, _ := procDragQueryFileW.Call(drop, ^uintptr(0), 0, 0)
	paths := make([]string, 0, int(count))
	for index := 0; index < int(count); index++ {
		length, _, _ := procDragQueryFileW.Call(drop, uintptr(index), 0, 0)
		if length == 0 {
			continue
		}
		buffer := make([]uint16, int(length)+1)
		procDragQueryFileW.Call(drop, uintptr(index), uintptr(unsafe.Pointer(&buffer[0])), length+1)
		paths = append(paths, windows.UTF16ToString(buffer))
	}
	return paths
}

// DragAcceptFiles registers a single window, and WM_DROPFILES goes to the
// window under the cursor — over a module view that is almost always a child
// control, not the shell background. So that a drop works anywhere on the
// view (RSZ-002), every child is subclassed once with forwardChildDrop and
// registered alongside the shell window. The map keeps each child's original
// window procedure until the child is destroyed; both callbacks are fixed
// package-level functions because the runtime never releases NewCallback
// functions.
var (
	dropForwardCallback  = windows.NewCallback(forwardChildDrop)
	childDropCallback    = windows.NewCallback(childDropVisit)
	dropForwardOriginals = map[windows.Handle]uintptr{}
	dropForwardAccept    uintptr
)

// forwardChildDrop is the window procedure installed on the shell's child
// controls: it forwards WM_DROPFILES to the shell window, which owns the
// drop routing and the HDROP lifetime, and defers every other message to the
// control's original procedure.
func forwardChildDrop(hwnd windows.Handle, message uint32, wParam uintptr, lParam uintptr) uintptr {
	if message == wmDropFiles {
		if root, _, _ := procGetAncestor.Call(uintptr(hwnd), gaRoot); root != 0 {
			r, _, _ := procSendMessageW.Call(root, wmDropFiles, wParam, 0)
			return r
		}
		procDragFinish.Call(wParam)
		return 0
	}
	previous := dropForwardOriginals[hwnd]
	if previous == 0 {
		r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
		return r
	}
	r, _, _ := procCallWindowProcW.Call(previous, uintptr(hwnd), uintptr(message), wParam, lParam)
	if message == wmDestroy {
		delete(dropForwardOriginals, hwnd)
	}
	return r
}

// childDropVisit subclasses one shell child on first sight and applies the
// current drop acceptance to it.
func childDropVisit(hwnd windows.Handle, _ uintptr) uintptr {
	if _, subclassed := dropForwardOriginals[hwnd]; !subclassed {
		previous, _, _ := procSetWindowLongPtrW.Call(uintptr(hwnd), gwlpWndProc, dropForwardCallback)
		if previous != 0 {
			dropForwardOriginals[hwnd] = previous
		}
	}
	procDragAcceptFiles.Call(uintptr(hwnd), dropForwardAccept)
	return 1
}

// updateDropAcceptance registers the shell window and every child control
// for file drops while the active module consumes them and, when it reports
// their availability, reports them available, so other modules, and a module
// that would refuse a drop, keep the no-drop cursor. Controls are created
// before the first updateView and never recreated, so the enumeration
// subclasses each child exactly once.
func (s *nativeShell) updateDropAcceptance() {
	s.acceptingDrops = s.dropsAvailable()
	accept := uintptr(0)
	if s.acceptingDrops {
		accept = 1
	}
	procDragAcceptFiles.Call(uintptr(s.hwnd), accept)
	dropForwardAccept = accept
	procEnumChildWindows.Call(uintptr(s.hwnd), childDropCallback, 0)
}

// dropsAvailable reports whether the active module consumes dropped files
// now: its native view is a drop target and, when it reports their
// availability, reports them available.
func (s *nativeShell) dropsAvailable() bool {
	native, exists := s.nativeViews[s.navigator.Current().ID()]
	return exists && hookAvailable[nativeDropTarget](native, nativeDropAvailability.NativeDropAvailable)
}

// filterModuleMessage lets module views pre-handle a queued message before
// the shell's own dialog processing. The Screenshot overlay session uses it
// for Escape cancellation and toolbar keyboard navigation while its topmost
// session windows own the screen and the shell window is hidden.
func (s *nativeShell) filterModuleMessage(message *msg) bool {
	for _, native := range s.nativeViews {
		if filter, ok := native.(nativeMessageFilter); ok && filter.NativeFilterMessage(unsafe.Pointer(message)) {
			return true
		}
	}
	return false
}

func (s *nativeShell) setScrollPosition(position int32) {
	info := scrollInfo{
		CbSize: uint32(unsafe.Sizeof(scrollInfo{})),
		FMask:  sifPos,
		NPos:   position,
	}
	actual, _, _ := procSetScrollInfo.Call(uintptr(s.hwnd), sbVert, uintptr(unsafe.Pointer(&info)), 1)
	s.scrollY = int32(actual)
	s.layout()
}

func (s *nativeShell) scrollBy(delta int32) {
	info := scrollInfo{CbSize: uint32(unsafe.Sizeof(scrollInfo{})), FMask: sifAll}
	if r, _, _ := procGetScrollInfo.Call(uintptr(s.hwnd), sbVert, uintptr(unsafe.Pointer(&info))); r == 0 {
		return
	}
	s.setScrollPosition(info.NPos + delta)
}

func (s *nativeShell) scroll(request uint16) {
	info := scrollInfo{CbSize: uint32(unsafe.Sizeof(scrollInfo{})), FMask: sifAll}
	if r, _, _ := procGetScrollInfo.Call(uintptr(s.hwnd), sbVert, uintptr(unsafe.Pointer(&info))); r == 0 {
		return
	}
	position := info.NPos
	page := int32(info.NPage)
	switch request {
	case sbLineUp:
		position -= s.scale(40)
	case sbLineDown:
		position += s.scale(40)
	case sbPageUp:
		position -= page
	case sbPageDown:
		position += page
	case sbTop:
		position = info.NMin
	case sbBottom:
		position = info.NMax
	case sbThumbPosition, sbThumbTrack:
		position = info.NTrackPos
	default:
		return
	}
	s.setScrollPosition(position)
}

func (s *nativeShell) ensureControlVisible(control windows.Handle) {
	var bounds rect
	if r, _, _ := procGetWindowRect.Call(uintptr(control), uintptr(unsafe.Pointer(&bounds))); r == 0 {
		return
	}
	procMapWindowPoints.Call(0, uintptr(s.hwnd), uintptr(unsafe.Pointer(&bounds)), 2)
	var client rect
	if r, _, _ := procGetClientRect.Call(uintptr(s.hwnd), uintptr(unsafe.Pointer(&client))); r == 0 {
		return
	}
	if bounds.Top < 0 {
		s.scrollBy(bounds.Top)
	} else if bounds.Bottom > client.Bottom {
		s.scrollBy(bounds.Bottom - client.Bottom)
	}
}

func (s *nativeShell) measureText(text string, width int32, font windows.Handle) int32 {
	if width <= 0 {
		return 0
	}
	dc, _, _ := procGetDC.Call(uintptr(s.hwnd))
	if dc == 0 {
		return 0
	}
	previous := uintptr(0)
	if font != 0 {
		previous, _, _ = procSelectObject.Call(dc, uintptr(font))
	}
	bounds := rect{Right: width}
	height, _, _ := procDrawTextW.Call(
		dc,
		uintptr(unsafe.Pointer(utf16(text))),
		^uintptr(0),
		uintptr(unsafe.Pointer(&bounds)),
		dtWordBreak|dtCalcRect|dtNoPrefix,
	)
	if previous != 0 {
		procSelectObject.Call(dc, previous)
	}
	procReleaseDC.Call(uintptr(s.hwnd), dc)
	return int32(height)
}

func (s *nativeShell) scale(value int32) int32 {
	return int32((int64(value)*int64(s.dpi) + 48) / 96)
}

// saveSettings saves the settings, with the window's current module and,
// when captureWindow, its placement (recordWindow), and shows the outcome in
// the note: the warning of a failed save, or the startup warnings that still
// apply — the warning that the settings the session started from could not
// be written is gone, as they now are, and an output folder's warning follows
// the options as they now are, as it does after the module command that
// chooses a folder (settleStartupNotice).
func (s *nativeShell) saveSettings(captureWindow bool) error {
	s.recordWindow(captureWindow)
	err := s.store.Save(*s.settings)
	if err != nil {
		s.notice = fmt.Sprintf(ui.SettingsSaveFailed, err)
		s.logEvent(diagnostics.LevelError, "settings.save", "write-failed")
	} else {
		s.startup.unwritten = ""
		s.notice = s.startup.text()
	}
	s.updateView()
	s.layout()
	return err
}

// recordWindow records the window's current module in the settings and,
// when captureWindow, the window's placement, which only a window that has
// not been destroyed has: its normal bounds, in the screen coordinates the
// window is created at as the application next starts, and whether it is
// maximized — for a minimized window, whether it is maximized as it is
// restored, as one minimized from maximized is, so that signing out, or
// closing it from the taskbar, while it is minimized keeps it maximized.
func (s *nativeShell) recordWindow(captureWindow bool) {
	s.settings.Shell.SelectedModule = string(s.navigator.Current().ID())
	if !captureWindow {
		return
	}
	window := &s.settings.Shell.Window
	zoomed, _, _ := procIsZoomed.Call(uintptr(s.hwnd))
	window.Maximized = zoomed != 0
	placement := windowPlacement{Length: uint32(unsafe.Sizeof(windowPlacement{}))}
	if r, _, _ := procGetWindowPlacement.Call(uintptr(s.hwnd), uintptr(unsafe.Pointer(&placement))); r == 0 {
		return
	}
	if placement.ShowCmd == swShowMinimized {
		window.Maximized = placement.Flags&wpfRestoreToMaximized != 0
	}
	normal := placement.NormalPosition
	width := normal.Right - normal.Left
	height := normal.Bottom - normal.Top
	if width < 320 || height < 240 {
		return
	}
	// GetWindowPlacement gives the normal bounds of a top-level window in
	// workspace coordinates, which are offset from screen coordinates by the
	// toolbars docked at the top or left of the monitor's work area, such
	// as a taskbar there; for a minimized window, MonitorFromWindow gives
	// the monitor of its normal bounds.
	var offset point
	if monitor, _, _ := procMonitorFromWindow.Call(uintptr(s.hwnd), monitorDefaultToNearest); monitor != 0 {
		info := monitorInfo{CbSize: uint32(unsafe.Sizeof(monitorInfo{}))}
		if r, _, _ := procGetMonitorInfoW.Call(monitor, uintptr(unsafe.Pointer(&info))); r != 0 {
			offset = workspaceOffset(info)
		}
	}
	window.X = normal.Left + offset.X
	window.Y = normal.Top + offset.Y
	window.Width = width
	window.Height = height
	window.HasPosition = true
}

// workspaceOffset returns what turns a monitor's workspace coordinates into
// screen coordinates: the offset of its work area from the monitor's
// top-left corner.
func workspaceOffset(info monitorInfo) point {
	return point{X: info.Work.Left - info.Monitor.Left, Y: info.Work.Top - info.Monitor.Top}
}

// restoredSettings returns the settings the restore of the default settings
// writes (SET-005): every setting at its default but for what the restore
// keeps. Those are each module's preset library — as replaced, the settings
// the restore replaces, stores it with its selected preset, joined by the
// custom presets of session, the running session's settings, that replaced
// does not hold (preset.KeptSection), as a custom preset is deleted only by
// its explicit Delete, unless inherited, the settings the session took from
// the legacy file and never wrote (settings.Store.Inherited), holds the same
// preset: replaced then lacks it because it was deleted there — and the
// running window's placement and current module, which are saved again as it
// closes.
func restoredSettings(session, replaced, inherited settings.Settings) (settings.Settings, error) {
	restored := settings.Default()
	restored.Shell = session.Shell
	modules := make(map[string]bool, len(replaced.Modules)+len(session.Modules))
	for name := range replaced.Modules {
		modules[name] = true
	}
	for name := range session.Modules {
		modules[name] = true
	}
	for name := range modules {
		section, err := preset.KeptSection(core.ModuleID(name), replaced.Modules[name], session.Modules[name], inherited.Modules[name])
		if err != nil {
			return settings.Settings{}, fmt.Errorf("module settings %q: %w", name, err)
		}
		if section != nil {
			restored.Modules[name] = section
		}
	}
	return restored, nil
}

// restoreDefaults restores the default settings for the running application
// (SET-005) once the Settings view has asked the user, and returns the
// outcome for the view to show. The restored settings keep the preset
// libraries of the settings they replace — the session's, or the settings
// file's when the session started from the defaults because it could not
// read the file (settings.Store.Restore), with the presets the session
// created then — and the file is backed up before they replace it; when it
// cannot be, nothing changes. Otherwise the session takes the restored
// settings, as it takes any setting change, whether or not they could be
// written (SET-004), and every module view that shows settings shows them
// again, its preset library included when the restore kept another, the
// follow-ups they return, such as a default shortcut another application
// holds, joined to the outcome; the shell's note is then brought up to date
// (settleNotice), with the output folders' warnings. Saved images, the
// resize list, and a running job are not touched.
func (s *nativeShell) restoreDefaults() ui.Notice {
	session := *s.settings
	inherited := s.store.Inherited()
	restored, backup, err := s.store.Restore(session, func(replaced settings.Settings) (settings.Settings, error) {
		return restoredSettings(session, replaced, inherited)
	})
	if errors.Is(err, settings.ErrNotRestored) {
		s.logEvent(diagnostics.LevelError, "settings.restore", "not-restored")
		return ui.Notice{Kind: ui.NoticeError, Message: fmt.Sprintf(ui.SettingsRestoreFailed, err)}
	}
	*s.settings = restored
	if err != nil {
		s.logEvent(diagnostics.LevelError, "settings.restore", "write-failed")
	} else {
		s.logEvent(diagnostics.LevelInfo, "settings.restore", "restored")
	}
	notice := ui.Notice{Kind: ui.NoticeSuccess, Message: ui.SettingsRestoredNoBackup}
	switch {
	case errors.Is(err, settings.ErrPathUnavailable):
		notice = ui.Notice{Kind: ui.NoticeWarning, Message: fmt.Sprintf(ui.SettingsRestoreInMemory, err)}
	case err != nil && backup != "":
		notice = ui.Notice{Kind: ui.NoticeWarning, Message: fmt.Sprintf(ui.SettingsRestoreSaveFailed, err, backup)}
	case err != nil:
		// There was no settings file, so none holds the previous settings
		// and nothing was backed up.
		notice = ui.Notice{Kind: ui.NoticeWarning, Message: fmt.Sprintf(ui.SettingsRestoreSaveFailedNoFile, err)}
	case backup != "":
		notice.Message = fmt.Sprintf(ui.SettingsRestored, backup)
	}
	for _, module := range s.navigator.Modules() {
		restorer, ok := s.nativeViews[module.ID()].(nativeDefaultsRestorer)
		if !ok {
			continue
		}
		if followUp := restorer.NativeRestoreDefaults(); followUp != "" {
			notice.Kind = ui.NoticeWarning
			notice.Message += fmt.Sprintf(ui.SettingsRestoreFollowUp, followUp)
		}
	}
	s.settleNotice(err)
	return notice
}

// settleNotice brings the shell's note up to date once the default settings
// are restored and the module views show them, err being the restore's
// failed write, if any. The settings file was then backed up, or there was
// none to back up, so the warnings about the file the session started from,
// such as saving disabled to protect it or settings that could not be
// written to it, no longer apply — but for a session without a settings
// path (settings.ErrPathUnavailable), whose warning about that still does,
// and whose outcome already says the restored settings cannot be saved. The
// warning naming the backup of a malformed file the session started without
// still applies, as that backup still holds the file's settings. The note
// becomes the warnings that apply, the output folders' as the restored
// options give them included (startupWarnings) — no default folder known,
// as at startup, with the reason startup gave, rather than a stored folder
// the default one replaced — or the failed write, as every failed save does
// (SET-004). It is shown at once and laid out with the Settings view's
// outcome, as every module command is.
func (s *nativeShell) settleNotice(err error) {
	pathless := errors.Is(err, settings.ErrPathUnavailable)
	if !pathless {
		s.startup.settingsFile, s.startup.unwritten = nil, ""
	}
	s.notice = s.startup.text()
	if err != nil && !pathless {
		s.notice = fmt.Sprintf(ui.SettingsSaveFailed, err)
	}
	s.updateView()
}

// confirmClose asks, before the window closes, about what closing would
// lose: first the work a module view's close guard names (nativeCloseGuard),
// a running resize job, and then, once the settings are saved with the
// window's placement, settings that could not be saved. Each question's
// default, No, keeps the application running, and a question the end of the
// Windows session interrupted by destroying the window (endSession) answers
// No, as ui.Confirm does, so the window is not closed again.
func (s *nativeShell) confirmClose() bool {
	for _, module := range s.navigator.Modules() {
		guard, ok := s.nativeViews[module.ID()].(nativeCloseGuard)
		if !ok {
			continue
		}
		title, message := guard.NativeCloseConfirmation()
		if message == "" {
			continue
		}
		if !s.ask(title, message) {
			return false
		}
	}
	if s.saveSettings(true) != nil {
		if !s.ask(ui.SettingsCloseTitle, ui.SettingsCloseConfirmation) {
			return false
		}
	}
	return true
}

// ask asks the user a yes-or-no question about the window, its safe answer
// No the default, and reports whether the answer was Yes.
func (s *nativeShell) ask(title, message string) bool {
	return s.confirm(s.hwnd, title, message)
}

// endSession shuts the application down as Windows ends the user's session —
// signing out, restarting, or shutting down — which may terminate the
// process as soon as WM_ENDSESSION returns, so nothing is left to the end of
// the message loop. Nothing is asked. The window's placement is recorded
// first, as only the window has it (recordWindow), the store's saves are
// held (settings.Store.Hold), and the window is then destroyed, which
// releases the global shortcut, closes any capture overlay, cancels a
// running resize job, and stops accepting work (NFR-022, AC-A05), as closing
// does, before anything is written, so a write that blocks holds none of
// that up: a field the destruction takes the focus from commits its edit to
// the settings, as on closing, but the save that commit makes is held,
// writing nothing. The settings, as they are once the window is destroyed,
// are then saved on a goroutine of their own, a failure only logged, as no
// one is left to read a warning, and the background work is waited for, as
// at the end of the message loop, so a cancelled job's images in flight
// remove their temporary files (NFR-023): both for no longer than
// endSessionWait from the message's arrival, so the session's end is not
// held up by a save or work that does not stop — as on a settings folder or
// an output folder on a network share that has gone — which the process's
// end then ends. A save so ended leaves the settings file as it was, as a
// save replaces the file only once its temporary file is written
// (settings.Store.Save). The save writes a copy of the settings, their
// module sections' map copied too, so nothing the GUI thread could still
// change reaches it, and the store's held saves write nothing, so the save
// needs no lock. The quit message the window's destruction posts still ends
// the message loop when the process outlives WM_ENDSESSION, and the wait
// there waits only for the background work and the save that are left
// (waitForBackgroundWork).
func (s *nativeShell) endSession() {
	deadline := time.NewTimer(endSessionWait)
	defer deadline.Stop()
	s.recordWindow(true)
	s.logEvent(diagnostics.LevelInfo, "app.end-session", "stopped")
	write := s.store.Hold()
	procDestroyWindow.Call(uintptr(s.hwnd))
	value := *s.settings
	value.Modules = maps.Clone(value.Modules)
	saved := make(chan struct{})
	s.sessionSaved = saved
	go func() {
		defer close(saved)
		if err := write(value); err != nil {
			s.logEvent(diagnostics.LevelError, "settings.save", "write-failed")
		}
	}()
	stopped := s.stopBackgroundWork()
	for saved != nil || stopped != nil {
		select {
		case <-saved:
			saved = nil
		case <-stopped:
			stopped = nil
		case <-deadline.C:
			return
		}
	}
}

// logEvent records a diagnostic event when logging is available.
func (s *nativeShell) logEvent(level diagnostics.Level, operation, code string) {
	if s.logger != nil {
		_ = s.logger.Record(diagnostics.Event{Level: level, Operation: operation, Code: code})
	}
}

func staticTextBrush(dc uintptr) uintptr {
	procSetBkMode.Call(dc, transparent)
	textColor, _, _ := procGetSysColor.Call(colorWindowText)
	procSetTextColor.Call(dc, textColor)
	brush, _, _ := procGetSysColorBrush.Call(colorWindow)
	return brush
}

// shellWindowProc takes lParam as an integer because most messages carry a
// number there: WM_GETICON, sent once the class has an icon, carries the DPI.
// Declared as a pointer, a small value crashes the garbage collector when it
// scans this frame. Only WM_DPICHANGED's lParam is a pointer, to memory
// Windows owns.
func shellWindowProc(hwnd windows.Handle, message uint32, wParam uintptr, lParam uintptr) uintptr {
	shell := shellStates[hwnd]
	switch message {
	case wmCommand:
		notification := highWord(wParam)
		if shell != nil && reportsFocus(notification, lParam) {
			shell.focus = windows.Handle(lParam)
			if !shell.keepScroll {
				shell.ensureControlVisible(windows.Handle(lParam))
			}
			// Set-focus notifications also reach the owning module view: the
			// Screenshot shortcut field suspends its global hotkey while it is
			// being edited, which only works if this branch does not swallow
			// the notification.
			if native, exists := shell.nativeViews[shell.navigator.Current().ID()]; exists && native.NativeCommand(int32(lowWord(wParam)), notification) {
				shell.layout()
			}
			return 0
		}
		if shell != nil && notification == 0 && lowWord(wParam) == actionCommand {
			if native, exists := shell.nativeViews[shell.navigator.Current().ID()]; exists {
				if handler, actionable := native.(nativeActionHandler); actionable {
					handler.NativeAction()
					shell.layout()
					return 0
				}
			}
		}
		if shell != nil && highWord(wParam) == 0 {
			if id, exists := shell.navIDs[lowWord(wParam)]; exists {
				if shell.navigator.Select(id) == nil {
					_ = shell.saveSettings(false)
				}
				if lParam != 0 {
					shell.ensureControlVisible(windows.Handle(lParam))
				}
				return 0
			}
		}
		if shell != nil {
			if native, exists := shell.nativeViews[shell.navigator.Current().ID()]; exists {
				before := shell.startup.text()
				if native.NativeCommand(int32(lowWord(wParam)), notification) {
					shell.settleStartupNotice(before)
					shell.layout()
					return 0
				}
			}
		}
	case wmActivate:
		// The control holding the focus as the window is deactivated is the
		// one restoreFocus gives it back to; DefWindowProc then focuses the
		// window itself on the next activation, which WM_SETFOCUS turns into
		// that control's focus. A click activates the window before the click
		// reaches the control under the pointer, which then takes the focus
		// itself; giving the focus back first would run the earlier control's
		// focus handlers for nothing — suspending the global shortcut while
		// the shortcut field holds it — or scroll it into view from under the
		// pointer. So on a click activation the focus is given back once the
		// click has been handled, only if the click left it on the window
		// itself, as a click on the title bar, the scroll bar, or the
		// background does, and without scrolling the control into view
		// (wmAppRestoreFocus).
		if shell != nil {
			switch lowWord(wParam) {
			case waInactive:
				if focused, _, _ := procGetFocus.Call(); focused != 0 {
					if child, _, _ := procIsChild.Call(uintptr(hwnd), focused); child != 0 {
						shell.focus = windows.Handle(focused)
					}
				}
			case waClickActive:
				shell.clickActivating = true
				r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
				shell.clickActivating = false
				return r
			}
		}
	case wmSetFocus:
		if shell != nil {
			if shell.clickActivating {
				_ = postShellMessage(uintptr(hwnd), wmAppRestoreFocus)
			} else {
				shell.restoreFocus()
			}
			return 0
		}
	case wmAppRestoreFocus:
		// The click that activated the window may have been on its scroll
		// bar, whose scrolling the view must keep, so the control given the
		// focus back is not scrolled into view: a pointer, not the keyboard,
		// brought the user back.
		if shell != nil {
			if focused, _, _ := procGetFocus.Call(); windows.Handle(focused) == hwnd {
				shell.keepScroll = true
				shell.restoreFocus()
				shell.keepScroll = false
			}
			return 0
		}
	case wmVScroll:
		if shell != nil {
			shell.scroll(lowWord(wParam))
			return 0
		}
	case wmHotkey:
		// A module's global hotkey fires here regardless of which
		// application holds the foreground — including while the shell
		// window is hidden behind a capture session, because the message
		// loop keeps running. The owning view consumes its identifier.
		if shell != nil {
			for _, native := range shell.nativeViews {
				if handler, ok := native.(nativeHotkeyHandler); ok && handler.NativeHotkey(int32(wParam)) {
					shell.layout()
					return 0
				}
			}
			return 0
		}
	case wmDropFiles:
		// Dropped files go only to the active module and only when it
		// accepts them; DragAcceptFiles in updateView keeps the no-drop
		// cursor for every other module.
		if shell != nil {
			if shell.handleDrop(wParam) {
				shell.layout()
			}
			return 0
		}
	case wmMouseWheel:
		if shell != nil {
			delta := int32(int16(highWord(wParam)))
			pixels := int32(-int64(delta) * int64(shell.scale(40)) / 120)
			if pixels == 0 && delta != 0 {
				pixels = 1
				if delta > 0 {
					pixels = -1
				}
			}
			shell.scrollBy(pixels)
			return 0
		}
	case wmSize:
		if shell != nil {
			shell.layout()
			return 0
		}
	case wmDisplayChange:
		if shell != nil && shell.displays != nil {
			shell.displays.HandleDisplayChange()
			// Subscriber callbacks can change the status area's text;
			// remeasure so a fresh notice is not clipped.
			shell.layout()
			return 0
		}
	case wmDPIChanged:
		if shell != nil {
			shell.dpi = uint32(lowWord(wParam))
			setWindowIcons(hwnd, shell.dpi)
			shell.updateFonts()
			if lParam != 0 {
				suggested := *(**rect)(unsafe.Pointer(&lParam))
				procSetWindowPos.Call(
					uintptr(hwnd),
					0,
					uintptr(suggested.Left),
					uintptr(suggested.Top),
					uintptr(suggested.Right-suggested.Left),
					uintptr(suggested.Bottom-suggested.Top),
					swpNoZOrder|swpNoActivate,
				)
			}
			// A scaling change keeps the physical display topology identical
			// while invalidating every DPI-derived measurement, so it reaches
			// the same subscribers a topology change does — an active overlay
			// session revalidates its surfaces against the new scaling
			// (SCR-054). Subscriber callbacks can change the status area's
			// text; layout after them so a fresh notice is not clipped.
			if shell.displays != nil {
				shell.displays.HandleDisplayChange()
			}
			shell.layout()
			return 0
		}
	case wmCtlColorStatic:
		return staticTextBrush(wParam)
	case wmTimer:
		if shell != nil && shell.note.HandleTimer(wParam) {
			shell.updateView()
			shell.layout()
			return 0
		}
	case wmAppDispatch:
		if shell != nil {
			shell.dispatcher.Drain()
			shell.layout()
			return 0
		}
	case wmClose:
		if shell != nil && !shell.confirmClose() {
			return 0
		}
		procDestroyWindow.Call(uintptr(hwnd))
		return 0
	case wmQueryEndSession:
		// Signing out, restarting, or shutting down is never refused or held
		// up by a question: the work stops once the session ends.
		if shell != nil {
			return 1
		}
	case wmEndSession:
		// wParam is zero when the session goes on after all, as when another
		// application refused to end it.
		if shell != nil {
			if wParam != 0 {
				shell.endSession()
			}
			return 0
		}
	case wmDestroy:
		if shell != nil {
			delete(shellStates, hwnd)
			procDragAcceptFiles.Call(uintptr(hwnd), 0)
			for _, native := range shell.nativeViews {
				native.NativeDestroy()
			}
			if shell.note != nil {
				shell.note.Destroy()
			}
			if shell.headingFont != 0 {
				procDeleteObject.Call(uintptr(shell.headingFont))
			}
			if shell.bodyFont != 0 {
				procDeleteObject.Call(uintptr(shell.bodyFont))
			}
			procPostQuitMessage.Call(0)
		}
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
	return r
}
