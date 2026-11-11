//go:build windows

package app

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"

	core "github.com/CyberCute-Studio/GameDevTools/internal/shell"
)

const (
	shellClassName = "GameDevToolsMainWindow"
	navCommandBase = 100
)

type nativeShell struct {
	navigator   *core.Navigator
	dispatcher  *core.DispatchQueue
	hwnd        windows.Handle
	dpi         uint32
	navHeading  windows.Handle
	navButtons  map[core.ModuleID]windows.Handle
	navIDs      map[uint16]core.ModuleID
	title       windows.Handle
	description windows.Handle
	action      windows.Handle
	note        windows.Handle
	headingFont windows.Handle
	bodyFont    windows.Handle
}

var shellStates = map[windows.Handle]*nativeShell{}

func runShell(navigator *core.Navigator) error {
	if err := registerShellClass(); err != nil {
		return err
	}
	shell := &nativeShell{
		navigator:  navigator,
		dpi:        96,
		navButtons: make(map[core.ModuleID]windows.Handle),
		navIDs:     make(map[uint16]core.ModuleID),
	}
	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(utf16(shellClassName))),
		uintptr(unsafe.Pointer(utf16("GameDevTools"))),
		wsOverlappedWindow|wsClipChildren|wsVisible,
		cwUseDefault, cwUseDefault, 1100, 700,
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
	shell.layout()
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
	var err error
	s.navHeading, err = s.createControl("STATIC", "TOOLS", wsChild|wsVisible, 0)
	if err != nil {
		return err
	}
	for i, module := range s.navigator.Modules() {
		style := uintptr(wsChild | wsVisible | wsTabStop | bsAutoRadioButton)
		if i == 0 {
			style |= wsGroup
		}
		id := uint16(navCommandBase + i)
		button, err := s.createControl("BUTTON", module.NavigationLabel(), style, id)
		if err != nil {
			return err
		}
		s.navButtons[module.ID()] = button
		s.navIDs[id] = module.ID()
	}
	s.title, err = s.createControl("STATIC", "", wsChild|wsVisible, 0)
	if err != nil {
		return err
	}
	s.description, err = s.createControl("STATIC", "", wsChild|wsVisible, 0)
	if err != nil {
		return err
	}
	s.action, err = s.createControl("BUTTON", "", wsChild|wsVisible|wsDisabled|wsGroup|wsTabStop, 0)
	if err != nil {
		return err
	}
	s.note, err = s.createControl("STATIC", "Placeholder view. No files or images are changed.", wsChild|wsVisible, 0)
	if err != nil {
		return err
	}
	return nil
}

func (s *nativeShell) createControl(className, text string, style uintptr, id uint16) (windows.Handle, error) {
	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(utf16(className))),
		uintptr(unsafe.Pointer(utf16(text))),
		style,
		0, 0, 0, 0,
		uintptr(s.hwnd), uintptr(id), 0, 0,
	)
	if hwnd == 0 {
		return 0, callError(fmt.Sprintf("CreateWindowExW(%s)", className), err)
	}
	return windows.Handle(hwnd), nil
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
	for _, control := range []windows.Handle{s.navHeading, s.description, s.action, s.note} {
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

func (s *nativeShell) layout() {
	var client rect
	r, _, _ := procGetClientRect.Call(uintptr(s.hwnd), uintptr(unsafe.Pointer(&client)))
	if r == 0 {
		return
	}
	margin := s.scale(32)
	navWidth := s.scale(210)
	navHeight := s.scale(44)
	moveWindow(s.navHeading, margin, margin, navWidth, s.scale(24))
	y := margin + s.scale(48)
	for _, module := range s.navigator.Modules() {
		moveWindow(s.navButtons[module.ID()], margin, y, navWidth, navHeight)
		y += navHeight + s.scale(8)
	}
	contentX := margin + navWidth + s.scale(40)
	contentWidth := client.Right - contentX - margin
	if contentWidth < 1 {
		contentWidth = 1
	}
	moveWindow(s.title, contentX, margin, contentWidth, s.scale(48))
	moveWindow(s.description, contentX, margin+s.scale(60), contentWidth, s.scale(90))
	moveWindow(s.action, contentX, margin+s.scale(166), s.scale(190), s.scale(44))
	noteY := client.Bottom - margin - s.scale(28)
	if noteY < margin {
		noteY = margin
	}
	moveWindow(s.note, contentX, noteY, contentWidth, s.scale(28))
}

func (s *nativeShell) scale(value int32) int32 {
	return int32((int64(value)*int64(s.dpi) + 48) / 96)
}

func shellWindowProc(hwnd windows.Handle, message uint32, wParam uintptr, lParam unsafe.Pointer) uintptr {
	shell := shellStates[hwnd]
	switch message {
	case wmCommand:
		if shell != nil && highWord(wParam) == 0 {
			if id, exists := shell.navIDs[lowWord(wParam)]; exists {
				if shell.navigator.Select(id) == nil {
					shell.updateView()
				}
				return 0
			}
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
		procSetBkMode.Call(wParam, transparent)
		brush, _, _ := procGetSysColorBrush.Call(colorWindow)
		return brush
	case wmAppDispatch:
		if shell != nil {
			shell.dispatcher.Drain()
			return 0
		}
	case wmClose:
		procDestroyWindow.Call(uintptr(hwnd))
		return 0
	case wmDestroy:
		if shell != nil {
			delete(shellStates, hwnd)
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
