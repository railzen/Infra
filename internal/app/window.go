package app

import (
	"math"
	"unsafe"

	"github.com/wailsapp/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"

	"intra/internal/config"
)

var theApp *App // the window procedure needs a way back to the App

func (a *App) scale() float64 { return float64(dpiOf(a.hwnd)) / 96 }

func (a *App) barPx() int32 { return int32(math.Round(barHeightDIP * a.scale())) }

func hInstance() uintptr {
	h, _, _ := pGetModuleHandleW.Call(0)
	return h
}

func appIcon(size int) uintptr {
	// resource "APP" is the icon group embedded by tools/genicon
	h, _, _ := pLoadImageW.Call(hInstance(), ptr(u16("APP")), 1 /*IMAGE_ICON*/, uintptr(size), uintptr(size), 0)
	return h
}

func (a *App) createWindow() error {
	theApp = a
	r, g, b := a.bgColor()
	brush, _, _ := pCreateSolidBrush.Call(uintptr(r) | uintptr(g)<<8 | uintptr(b)<<16)
	cursor, _, _ := pLoadCursorW.Call(0, 32512 /*IDC_ARROW*/)
	wc := wndClassEx{
		Style:      csHRedraw | csVRedraw,
		WndProc:    windows.NewCallback(mainWndProc),
		Instance:   hInstance(),
		Icon:       appIcon(32),
		IconSm:     appIcon(16),
		Cursor:     cursor,
		Background: brush,
		ClassName:  u16(a.className()),
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if atom, _, err := pRegisterClassExW.Call(ptr(&wc)); atom == 0 {
		return err
	}
	const cwUseDefault = 0x80000000
	h, _, err := pCreateWindowExW.Call(0, ptr(u16(a.className())), ptr(u16("Intra")),
		wsOverlappedWindow|wsClipChildren,
		cwUseDefault, cwUseDefault, cwUseDefault, cwUseDefault, 0, 0, hInstance(), 0)
	if h == 0 {
		return err
	}
	a.hwnd = h
	if a.dark {
		setDwmAttr(h, 20 /*DWMWA_USE_IMMERSIVE_DARK_MODE*/, 1)
	}
	return nil
}

// restorePlacement shows the window, either where it was last closed or
// centered on the primary monitor's work area.
func (a *App) restorePlacement() {
	if ws := a.cfg.Window; a.cfg.RememberWindow && ws != nil && ws.W > 200 && ws.H > 150 && a.onScreen(ws) {
		wp := windowPlacement{
			ShowCmd:        swShowNormal,
			NormalPosition: rect{int32(ws.X), int32(ws.Y), int32(ws.X + ws.W), int32(ws.Y + ws.H)},
		}
		if ws.Maximized {
			wp.ShowCmd = swShowMaximized
		}
		wp.Length = uint32(unsafe.Sizeof(wp))
		pSetWindowPlacement.Call(a.hwnd, ptr(&wp))
		return
	}
	s := a.scale()
	w, h := int32(1280*s), int32(820*s)
	mon, _, _ := pMonitorFromWindow.Call(a.hwnd, 1 /*MONITOR_DEFAULTTOPRIMARY*/)
	mi := monitorInfo{}
	mi.Size = uint32(unsafe.Sizeof(mi))
	pGetMonitorInfoW.Call(mon, ptr(&mi))
	wk := mi.Work
	w, h = min(w, wk.W()-int32(40*s)), min(h, wk.H()-int32(40*s))
	x, y := wk.Left+(wk.W()-w)/2, wk.Top+(wk.H()-h)/2
	pSetWindowPos.Call(a.hwnd, 0, uintptr(x), uintptr(y), uintptr(w), uintptr(h), swpNoZOrder|swpFrameChanged)
	pShowWindow.Call(a.hwnd, swShowNormal)
}

// onScreen reports whether the saved window's title area is on any monitor.
func (a *App) onScreen(ws *config.WindowState) bool {
	pt := point{int32(ws.X + ws.W/2), int32(ws.Y + 10)}
	m, _, _ := pMonitorFromPoint.Call(uintptr(uint32(pt.X))|uintptr(uint32(pt.Y))<<32, 0 /*MONITOR_DEFAULTTONULL*/)
	return m != 0
}

func (a *App) rememberPlacement() {
	if !a.cfg.RememberWindow {
		return
	}
	wp := windowPlacement{}
	wp.Length = uint32(unsafe.Sizeof(wp))
	if r, _, _ := pGetWindowPlacement.Call(a.hwnd, ptr(&wp)); r == 0 {
		return
	}
	n := wp.NormalPosition
	a.cfg.Window = &config.WindowState{X: int(n.Left), Y: int(n.Top), W: int(n.W()), H: int(n.H()),
		Maximized: wp.ShowCmd == swShowMaximized || (wp.ShowCmd == swShowMinimized && wp.Flags&2 != 0)}
}

// layout positions the two webviews: the UI view owns the title bar strip,
// and the whole client area while a page (settings, favorites…) is shown.
func (a *App) layout() {
	if a.ui == nil || a.ui.GetController() == nil {
		return
	}
	cr := clientRect(a.hwnd)
	bar := a.barPx()
	full := a.page != "" || a.cur == nil
	if full {
		a.ui.SetSize(edge.Rect{Left: 0, Top: 0, Right: cr.Right, Bottom: cr.Bottom})
	} else {
		a.ui.SetSize(edge.Rect{Left: 0, Top: 0, Right: cr.Right, Bottom: bar})
	}
	if a.site != nil && a.site.GetController() != nil {
		a.site.SetSize(edge.Rect{Left: 0, Top: bar, Right: cr.Right, Bottom: max(cr.Bottom, bar)})
		if full {
			a.site.Hide()
		} else {
			a.site.Show()
		}
	}
}

func (a *App) setRasterScale() {
	s := a.scale()
	for _, c := range []*edge.Chromium{a.ui, a.site} {
		putRasterScale(c, s)
	}
	if a.menu != nil {
		putRasterScale(a.menu.wv, s)
	}
}

// putRasterScale sets ICoreWebView2Controller3::put_RasterizationScale
// (vtable slot 29). go-webview2's wrapper passes the double incorrectly; Go's
// Windows syscall path mirrors the first arguments into XMM registers, so
// passing the IEEE-754 bits through a raw call delivers the right value.
func putRasterScale(c *edge.Chromium, s float64) {
	if c == nil || c.GetController() == nil {
		return
	}
	c3 := c.GetController().GetICoreWebView2Controller3()
	if c3 == nil {
		return
	}
	comCall(uintptr(unsafe.Pointer(c3)), 29, uintptr(math.Float64bits(s)))
}

func (a *App) notifyMoved() {
	for _, c := range []*edge.Chromium{a.ui, a.site} {
		if c != nil {
			c.NotifyParentWindowPositionChanged()
		}
	}
}

func mainWndProc(hwnd, m, wp, lp uintptr) uintptr {
	a := theApp
	if a != nil && a.closing && m != wmDestroy {
		// the webview controllers are being torn down; any call into them now
		// fails, and go-webview2 terminates the process on such failures
		r, _, _ := pDefWindowProcW.Call(hwnd, m, wp, lp)
		return r
	}
	switch m {
	case wmNCCalcSize:
		if wp == 0 {
			break
		}
		// Keep the standard left/right/bottom resize frame but give the
		// caption area to the client, like Electron's titleBarStyle "hidden".
		p := (*nccalcsizeParams)(unsafe.Pointer(lp))
		top := p.Rgrc[0].Top
		r, _, _ := pDefWindowProcW.Call(hwnd, m, wp, lp)
		if r != 0 {
			return r
		}
		p.Rgrc[0].Top = top
		if isZoomed(hwnd) {
			dpi := dpiOf(hwnd)
			p.Rgrc[0].Top += metricForDpi(smCyFrame, dpi) + metricForDpi(smCxPaddedBordr, dpi)
		}
		return 0
	case wmNCHitTest:
		r, _, _ := pDefWindowProcW.Call(hwnd, m, wp, lp)
		if r == htClient && !isZoomed(hwnd) {
			var pt point
			pt.X, pt.Y = loword(lp), hiword(lp)
			wr := windowRect(hwnd)
			if pt.Y < wr.Top+metricForDpi(smCyFrame, dpiOf(hwnd)) {
				return htTop
			}
		}
		return r
	case wmSize:
		if a != nil {
			a.layout()
			if a.menu != nil {
				a.menu.hide()
			}
			a.push()
		}
	case wmMove, wmMoving:
		if a != nil {
			a.notifyMoved()
			if a.menu != nil && m == wmMoving {
				a.menu.hide()
			}
		}
	case wmActivate:
		if a != nil {
			other := lp
			active := wp&0xffff != waInactive
			if !active && a.menu != nil && other == a.menu.hwnd {
				active = true // the favorites dropdown keeps the window looking active
			}
			if active != a.active {
				a.active = active
				a.push()
			}
			if wp&0xffff != waInactive && a.page == "" {
				a.focusSite()
			}
		}
	case wmGetMinMaxInfo:
		mi := (*minMaxInfo)(unsafe.Pointer(lp))
		s := float64(dpiOf(hwnd)) / 96
		mi.MinTrackSize = point{int32(720 * s), int32(480 * s)}
		return 0
	case wmDpiChanged:
		r := (*rect)(unsafe.Pointer(lp))
		pSetWindowPos.Call(hwnd, 0, uintptr(r.Left), uintptr(r.Top), uintptr(r.W()), uintptr(r.H()), swpNoZOrder|swpNoActivate)
		if a != nil {
			a.setRasterScale()
			a.layout()
		}
		return 0
	case wmSettingChange:
		if a != nil && lp != 0 && windows.UTF16PtrToString((*uint16)(unsafe.Pointer(lp))) == "ImmersiveColorSet" {
			a.dark = systemDarkMode()
			v := int32(0)
			if a.dark {
				v = 1
			}
			setDwmAttr(hwnd, 20, v)
		}
	case wmAppCall:
		if a != nil {
			a.runQueue()
		}
		return 0
	case wmClose:
		if a != nil {
			a.rememberPlacement()
			a.closing = true
			for _, c := range []*edge.Chromium{a.ui, a.site} {
				if c != nil {
					c.ShuttingDown()
				}
			}
			if a.menu != nil && a.menu.wv != nil {
				a.menu.wv.ShuttingDown()
			}
			a.saver.flush(a.cfg.Clone())
		}
		pDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, m, wp, lp)
	return r
}

// ---------------------------------------------------------------- window actions

func cursorLParam() uintptr {
	var pt point
	pGetCursorPos.Call(ptr(&pt))
	return uintptr(uint16(pt.X)) | uintptr(uint16(pt.Y))<<16
}

func (a *App) beginNCDrag(hit uintptr) {
	pReleaseCapture.Call()
	pPostMessageW.Call(a.hwnd, wmNCLButtonDown, hit, cursorLParam())
}

func (a *App) toggleMaximize() {
	if isZoomed(a.hwnd) {
		pShowWindow.Call(a.hwnd, swRestore)
	} else {
		pShowWindow.Call(a.hwnd, swShowMaximized)
	}
}

func (a *App) showSystemMenu() {
	menu, _, _ := pGetSystemMenu.Call(a.hwnd, 0)
	if menu == 0 {
		return
	}
	const mfByCommand, mfEnabled, mfGrayed = 0, 0, 1
	zoomed := isZoomed(a.hwnd)
	set := func(cmd uintptr, on bool) {
		f := uintptr(mfByCommand | mfGrayed)
		if on {
			f = mfByCommand | mfEnabled
		}
		pEnableMenuItem.Call(menu, cmd, f)
	}
	set(scRestore, zoomed)
	set(scMove, !zoomed)
	set(scSize, !zoomed)
	set(scMinimize, true)
	set(scMaximize, !zoomed)
	set(scClose, true)
	pSetMenuDefaultItem.Call(menu, scClose, 0)
	var pt point
	pGetCursorPos.Call(ptr(&pt))
	const tpmReturnCmd, tpmRightButton = 0x0100, 0x0002
	cmd, _, _ := pTrackPopupMenu.Call(menu, tpmReturnCmd|tpmRightButton, uintptr(pt.X), uintptr(pt.Y), 0, a.hwnd, 0)
	if cmd != 0 {
		pPostMessageW.Call(a.hwnd, wmSysCommand, cmd, 0)
	}
}

