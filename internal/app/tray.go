package app

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wmTrayIcon = wmApp + 2

	nimAdd    = 0
	nimDelete = 2

	nifMessage = 1
	nifIcon    = 2
	nifTip     = 4

	trayCmdToggle   = 1
	trayCmdQuit     = 2
	trayCmdBookmark = 100 // + index into the bookmarks shown
)

var pShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")

// notifyIconData matches NOTIFYICONDATAW (976 bytes on amd64).
type notifyIconData struct {
	Size            uint32
	Wnd             uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GuidItem        windows.GUID
	BalloonIcon     uintptr
}

// createTray adds the notification-area icon. Its owner is a hidden top-level
// window rather than a message-only one: only top-level windows receive the
// TaskbarCreated broadcast, and the popup menu needs a window that can take
// the foreground.
func (a *App) createTray() error {
	cls := a.className() + "-tray"
	taskbarCreated, _, _ := pRegisterWindowMessageW.Call(ptr(u16("TaskbarCreated")))
	wc := wndClassEx{
		WndProc: windows.NewCallback(func(h, m, wp, lp uintptr) uintptr {
			switch {
			case m == wmTrayIcon:
				switch lp & 0xffff {
				case wmLButtonUp:
					a.post(a.toggleFromTray)
				case wmRButtonUp:
					a.trayMenu()
				}
				return 0
			case m == taskbarCreated && taskbarCreated != 0:
				a.addTrayIcon() // Explorer restarted and lost every icon
				return 0
			}
			r, _, _ := pDefWindowProcW.Call(h, m, wp, lp)
			return r
		}),
		Instance:  hInstance(),
		ClassName: u16(cls),
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if atom, _, err := pRegisterClassExW.Call(ptr(&wc)); atom == 0 {
		return err
	}
	h, _, err := pCreateWindowExW.Call(wsExToolWindow, ptr(u16(cls)), 0, wsPopup, 0, 0, 0, 0, 0, 0, hInstance(), 0)
	if h == 0 {
		return err
	}
	a.trayHwnd = h
	size := int32(16)
	if pGetSystemMetricsForDpi.Find() == nil {
		size = metricForDpi(smCxSmIcon, systemDPI())
	}
	a.trayIcon = appIcon(int(size))
	a.addTrayIcon()
	return nil
}

func (a *App) trayData() notifyIconData {
	nid := notifyIconData{Wnd: a.trayHwnd, ID: 1}
	nid.Size = uint32(unsafe.Sizeof(nid))
	return nid
}

func (a *App) addTrayIcon() {
	nid := a.trayData()
	nid.Flags = nifMessage | nifIcon | nifTip
	nid.CallbackMessage = wmTrayIcon
	nid.Icon = a.trayIcon
	copy(nid.Tip[:len(nid.Tip)-1], windows.StringToUTF16("Intra"))
	r, _, _ := pShellNotifyIconW.Call(nimAdd, ptr(&nid))
	a.trayShown = r != 0
}

func (a *App) removeTrayIcon() {
	if a.trayHwnd == 0 {
		return
	}
	nid := a.trayData()
	pShellNotifyIconW.Call(nimDelete, ptr(&nid))
	a.trayShown = false
}

// trayMenu shows the bookmarks, "show"/"hide" and "quit" at the pointer.
func (a *App) trayMenu() {
	menu, _, _ := pCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer pDestroyMenu.Call(menu)
	var ids []string
	for _, b := range a.cfg.Bookmarks {
		if !validOpenID(b.ID) || len(ids) >= 64 {
			continue
		}
		pAppendMenuW.Call(menu, mfString, trayCmdBookmark+uintptr(len(ids)), ptr(u16(strings.ReplaceAll(linkLabel(b), "&", "&&"))))
		ids = append(ids, b.ID)
	}
	if len(ids) > 0 {
		pAppendMenuW.Call(menu, mfSeparator, 0, 0)
	}
	toggle := "显示 Intra"
	if a.anyShowing() {
		toggle = "隐藏到托盘"
	}
	pAppendMenuW.Call(menu, mfString, trayCmdToggle, ptr(u16(toggle)))
	pAppendMenuW.Call(menu, mfString, trayCmdQuit, ptr(u16("退出")))
	pSetMenuDefaultItem.Call(menu, trayCmdToggle, 0)
	var pt point
	pGetCursorPos.Call(ptr(&pt))
	// without the foreground the menu stays open after a click elsewhere
	pSetForegroundWindow.Call(a.trayHwnd)
	const tpmReturnCmd, tpmRightButton = 0x0100, 0x0002
	cmd, _, _ := pTrackPopupMenu.Call(menu, tpmReturnCmd|tpmRightButton, uintptr(pt.X), uintptr(pt.Y), 0, a.trayHwnd, 0)
	pPostMessageW.Call(a.trayHwnd, wmNull, 0, 0)
	switch {
	case cmd == trayCmdToggle:
		a.post(a.toggleFromTray)
	case cmd == trayCmdQuit:
		a.post(a.quit)
	case cmd >= trayCmdBookmark && int(cmd-trayCmdBookmark) < len(ids):
		id := ids[cmd-trayCmdBookmark]
		a.post(func() { a.openFromExternal(id) })
	}
}

// anyShowing reports whether some window is on screen and not minimized.
func (a *App) anyShowing() bool {
	for _, w := range a.windows {
		if !w.closing && isVisible(w.hwnd) && !isIconic(w.hwnd) {
			return true
		}
	}
	return false
}

// toggleFromTray hides every window while one is showing, and otherwise
// brings them all back.
func (a *App) toggleFromTray() {
	if !a.anyShowing() {
		a.showFromTray()
		return
	}
	for _, w := range a.windows {
		if !w.closing && isVisible(w.hwnd) {
			w.hideToTray()
		}
	}
}

// showFromTray shows hidden windows in the state they were hidden in,
// restores minimized ones, and activates one of them.
func (a *App) showFromTray() {
	for _, w := range a.windows {
		if w.closing {
			continue
		}
		if !isVisible(w.hwnd) {
			pShowWindow.Call(w.hwnd, swShowNA)
		}
		// a window minimized into the tray comes back minimized
		if isIconic(w.hwnd) {
			pShowWindow.Call(w.hwnd, swShowNoActivate)
		}
	}
	if w := a.foreground(); w != nil && !w.closing {
		w.activate()
	}
}

// minimizesToTray reports whether a minimized window should leave the
// taskbar for the tray, which needs the tray icon to bring it back.
func (a *App) minimizesToTray() bool { return a.cfg.MinimizeToTray && a.trayShown }

func (a *App) quit() {
	if len(a.windows) == 0 {
		pPostQuitMessage.Call(0)
		return
	}
	for _, w := range append([]*Window(nil), a.windows...) {
		pPostMessageW.Call(w.hwnd, wmClose, 0, 0)
	}
}
