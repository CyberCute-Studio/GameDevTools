//go:build windows

package app

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/CyberCute-Studio/GameDevTools/internal/diagnostics"
	"github.com/CyberCute-Studio/GameDevTools/internal/settings"
	core "github.com/CyberCute-Studio/GameDevTools/internal/shell"
	"github.com/CyberCute-Studio/GameDevTools/internal/ui"
)

const (
	shellClassName = "GameDevToolsMainWindow"
	navCommandBase = 100
)

type nativeShell struct {
	navigator     *core.Navigator
	dispatcher    *core.DispatchQueue
	store         *settings.Store
	settings      settings.Settings
	logger        *diagnostics.Logger
	startupNotice string
	notice        string
	hwnd          windows.Handle
	dpi           uint32
	scrollY       int32
	navHeading    windows.Handle
	navButtons    map[core.ModuleID]windows.Handle
	navIDs        map[uint16]core.ModuleID
	title         windows.Handle
	description   windows.Handle
	action        windows.Handle
	note          *ui.StatusArea
	headingFont   windows.Handle
	bodyFont      windows.Handle
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
	noteY             int32
	noteHeight        int32
	virtualHeight     int32
}

type shellTextHeights struct {
	title       int32
	description int32
	note        int32
}

var shellStates = map[windows.Handle]*nativeShell{}

func runShell(navigator *core.Navigator, store *settings.Store, initial settings.Settings, logger *diagnostics.Logger, notice string) error {
	if err := registerShellClass(); err != nil {
		return err
	}
	shell := &nativeShell{
		navigator:     navigator,
		store:         store,
		settings:      initial,
		logger:        logger,
		startupNotice: notice,
		notice:        notice,
		dpi:           96,
		navButtons:    make(map[core.ModuleID]windows.Handle),
		navIDs:        make(map[uint16]core.ModuleID),
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
		return callError("CreateWindowExW(main)", err)
	}
	shell.hwnd = windows.Handle(hwnd)
	shell.dispatcher = core.NewDispatchQueue(func() error {
		return postShellMessage(hwnd, wmAppDispatch)
	})
	shellStates[shell.hwnd] = shell
	if dpi, _, _ := procGetDpiForWindow.Call(hwnd); dpi != 0 {
		shell.dpi = uint32(dpi)
	}
	if err := shell.createControls(); err != nil {
		procDestroyWindow.Call(hwnd)
		return err
	}
	shell.updateFonts()
	shell.updateView()
	shell.fitInitialWindow()
	shell.layout()
	showCommand := uintptr(swShow)
	if initial.Shell.Window.Maximized {
		showCommand = swShowMaximized
	}
	procShowWindow.Call(hwnd, showCommand)
	procUpdateWindow.Call(hwnd)
	var message msg
	for {
		r, _, err := procGetMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(r) == -1 {
			return callError("GetMessageW", err)
		}
		if r == 0 {
			return nil
		}
		if handled, _, _ := procIsDialogMessageW.Call(hwnd, uintptr(unsafe.Pointer(&message))); handled != 0 {
			continue
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
	}
}

func registerShellClass() error {
	instance, _, err := procGetModuleHandleW.Call(0)
	if instance == 0 {
		return callError("GetModuleHandleW", err)
	}
	cursor, _, err := procLoadCursorW.Call(0, 32512)
	if cursor == 0 {
		return callError("LoadCursorW", err)
	}
	class := wndClassEx{
		LpfnWndProc:   windows.NewCallback(shellWindowProc),
		HInstance:     windows.Handle(instance),
		HCursor:       windows.Handle(cursor),
		HbrBackground: windows.Handle(colorWindow + 1),
		LpszClassName: utf16(shellClassName),
	}
	class.CbSize = uint32(unsafe.Sizeof(class))
	atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class)))
	if atom == 0 {
		return callError("RegisterClassExW", err)
	}
	return nil
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
		s.navButtons[module.ID()] = button.Handle()
		s.navIDs[id] = module.ID()
	}
	title, err := ui.NewLabel(s.hwnd, "")
	if err != nil {
		return err
	}
	s.title = title.Handle()
	description, err := ui.NewLabel(s.hwnd, "")
	if err != nil {
		return err
	}
	s.description = description.Handle()
	action, err := ui.NewButton(s.hwnd, "", ui.ButtonOptions{Primary: true, Group: true, Disabled: true})
	if err != nil {
		return err
	}
	s.action = action.Handle()
	s.note, err = ui.NewStatusArea(s.hwnd)
	if err != nil {
		return err
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
	for _, control := range []windows.Handle{s.navHeading, s.description, s.action, s.note.Handle()} {
		setControlFont(control, s.bodyFont)
	}
	for _, button := range s.navButtons {
		setControlFont(button, s.bodyFont)
	}
}

func (s *nativeShell) updateView() {
	current := s.navigator.Current()
	view := current.View()
	setWindowText(s.title, view.Title())
	setWindowText(s.description, view.Description())
	setWindowText(s.action, view.PrimaryAction())
	s.note.SetText(s.noteText())
	showAction := int32(swHide)
	if view.PrimaryAction() != "" {
		showAction = swShow
	}
	procShowWindow.Call(uintptr(s.action), uintptr(showAction))
	for id, button := range s.navButtons {
		checked := uintptr(0)
		if id == current.ID() {
			checked = bstChecked
		}
		procSendMessageW.Call(uintptr(button), bmSetCheck, checked, 0)
	}
}

func (s *nativeShell) noteText() string {
	if s.notice != "" {
		return ui.Notice{Kind: ui.NoticeWarning, Message: s.notice}.Text()
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
		plan.noteY = plan.descriptionY + plan.descriptionHeight + scale(12)
		plan.virtualHeight = plan.noteY + plan.noteHeight + plan.margin
	} else {
		plan.contentWidth = max(scale(190), client.Right-plan.contentX-plan.margin)
		plan.descriptionY = plan.contentY + plan.titleHeight + scale(12)
		plan.actionY = plan.descriptionY + plan.descriptionHeight + scale(16)
		plan.noteY = plan.actionY + scale(44) + scale(12)
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
	plan := calculateShellLayout(client, s.dpi, len(modules), shellTextHeights{
		title:       s.measureText(view.Title(), basePlan.contentWidth, s.headingFont),
		description: s.measureText(view.Description(), basePlan.contentWidth, s.bodyFont),
		note:        s.measureText(s.noteText(), basePlan.contentWidth, s.bodyFont),
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
		y := plan.navY + int32(i)*(plan.navHeight+s.scale(8))
		moveWindow(s.navButtons[module.ID()], plan.margin, y-s.scrollY, plan.navWidth, plan.navHeight)
	}
	contentY := func(y int32) int32 { return y - s.scrollY }
	moveWindow(s.title, plan.contentX, contentY(plan.contentY), plan.contentWidth, plan.titleHeight)
	moveWindow(s.description, plan.contentX, contentY(plan.descriptionY), plan.contentWidth, plan.descriptionHeight)
	moveWindow(s.action, plan.contentX, contentY(plan.actionY), min(s.scale(190), plan.contentWidth), s.scale(44))
	s.note.Move(plan.contentX, contentY(plan.noteY), plan.contentWidth, plan.noteHeight)
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

func (s *nativeShell) saveSettings(captureWindow bool) error {
	value := s.settings
	value.Shell.SelectedModule = string(s.navigator.Current().ID())
	if captureWindow {
		placement := windowPlacement{Length: uint32(unsafe.Sizeof(windowPlacement{}))}
		if r, _, _ := procGetWindowPlacement.Call(uintptr(s.hwnd), uintptr(unsafe.Pointer(&placement))); r != 0 {
			normal := placement.NormalPosition
			width := normal.Right - normal.Left
			height := normal.Bottom - normal.Top
			if width >= 320 && height >= 240 {
				value.Shell.Window.X = normal.Left
				value.Shell.Window.Y = normal.Top
				value.Shell.Window.Width = width
				value.Shell.Window.Height = height
				value.Shell.Window.HasPosition = true
			}
		}
		maximized, _, _ := procIsZoomed.Call(uintptr(s.hwnd))
		value.Shell.Window.Maximized = maximized != 0
	}
	s.settings = value
	err := s.store.Save(value)
	if err != nil {
		s.notice = fmt.Sprintf(ui.SettingsSaveFailed, err)
		if s.logger != nil {
			_ = s.logger.Record(diagnostics.Event{Level: diagnostics.LevelError, Operation: "settings.save", Code: "write-failed"})
		}
	} else {
		s.notice = s.startupNotice
	}
	s.updateView()
	s.layout()
	return err
}

func staticTextBrush(dc uintptr) uintptr {
	procSetBkMode.Call(dc, transparent)
	textColor, _, _ := procGetSysColor.Call(colorWindowText)
	procSetTextColor.Call(dc, textColor)
	brush, _, _ := procGetSysColorBrush.Call(colorWindow)
	return brush
}

func shellWindowProc(hwnd windows.Handle, message uint32, wParam uintptr, lParam unsafe.Pointer) uintptr {
	shell := shellStates[hwnd]
	switch message {
	case wmCommand:
		notification := highWord(wParam)
		if shell != nil && (notification == bnSetFocus || notification == enSetFocus) && lParam != nil {
			shell.ensureControlVisible(windows.Handle(lParam))
			return 0
		}
		if shell != nil && highWord(wParam) == 0 {
			if id, exists := shell.navIDs[lowWord(wParam)]; exists {
				if shell.navigator.Select(id) == nil {
					_ = shell.saveSettings(false)
				}
				if lParam != nil {
					shell.ensureControlVisible(windows.Handle(lParam))
				}
				return 0
			}
		}
	case wmVScroll:
		if shell != nil {
			shell.scroll(lowWord(wParam))
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
	case wmDPIChanged:
		if shell != nil {
			shell.dpi = uint32(lowWord(wParam))
			shell.updateFonts()
			if lParam != nil {
				suggested := (*rect)(lParam)
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
			return 0
		}
	case wmClose:
		if shell != nil && shell.saveSettings(true) != nil {
			if !ui.Confirm(hwnd, ui.SettingsCloseTitle, ui.SettingsCloseConfirmation) {
				return 0
			}
		}
		procDestroyWindow.Call(uintptr(hwnd))
		return 0
	case wmDestroy:
		if shell != nil {
			delete(shellStates, hwnd)
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
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, uintptr(lParam))
	return r
}
