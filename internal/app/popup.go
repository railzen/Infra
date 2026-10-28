package app

import (
	"math"
	"time"
	"unsafe"

	"github.com/wailsapp/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"

	"intra/ui"
)

const popupWidthDIP = 320

// popup is the favorites dropdown. It is a separate owned top-level window so
// it can overlap the site webview, which a DOM element in the bar could not.
type popup struct {
	a        *App
	hwnd     uintptr
	wv       *edge.Chromium
	ready    bool
	open     bool
	hiddenAt time.Time
	heightCSS float64
	anchor   point // client coordinates (physical px) of the menu button's bottom-left
}

var thePopup *popup

func newPopup(a *App) *popup {
	p := &popup{a: a, heightCSS: 120}
	thePopup = p
	r, g, b := a.bgColor()
	if a.dark {
		r, g, b = 0x21, 0x21, 0x21
	}
	brush, _, _ := pCreateSolidBrush.Call(uintptr(r) | uintptr(g)<<8 | uintptr(b)<<16)
	cls := a.className() + "-popup"
	wc := wndClassEx{
		Style:      csDropShadow,
		WndProc:    windows.NewCallback(popupWndProc),
		Instance:   hInstance(),
		Background: brush,
		ClassName:  u16(cls),
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(ptr(&wc))
	h, _, _ := pCreateWindowExW.Call(wsExToolWindow, ptr(u16(cls)), ptr(u16("Intra")),
		wsPopup|wsClipChildren, 0, 0, uintptr(popupWidthDIP*a.scale()), 200, a.hwnd, 0, hInstance(), 0)
	if h == 0 {
		return p
	}
	p.hwnd = h
	setDwmAttr(h, 33 /*DWMWA_WINDOW_CORNER_PREFERENCE*/, 2 /*DWMWCP_ROUND*/)

	p.wv = a.newChromium()
	p.wv.MessageCallback = func(m string, _ *edge.ICoreWebView2, _ *edge.ICoreWebView2WebMessageReceivedEventArgs) {
		a.onMessage(true, m)
	}
	p.wv.Embed(h)
	p.wv.SetBackgroundColour(r, g, b, 255)
	lockDownUI(p.wv)
	putRasterScale(p.wv, a.scale())
	p.wv.Init(`window.__INTRA_MODE="menu"`)
	p.wv.Resize()
	p.wv.NavigateToString(ui.HTML)
	return p
}

func (p *popup) setRasterScale(s float64) { putRasterScale(p.wv, s) }

func (p *popup) toggle(xCSS, yCSS float64) {
	if p.hwnd == 0 || !p.ready {
		return
	}
	if p.open {
		p.hide()
		return
	}
	// the mousedown that deactivated (and so closed) the popup also lands here
	if time.Since(p.hiddenAt) < 300*time.Millisecond {
		return
	}
	s := p.a.scale()
	p.anchor = point{int32(math.Round(xCSS * s)), int32(math.Round((yCSS + 4) * s))}
	p.open = true
	p.place()
	pShowWindow.Call(p.hwnd, swShow)
	pSetForegroundWindow.Call(p.hwnd)
	p.wv.Resize()
	p.wv.Show()
	p.wv.Focus()
	p.a.push()
}

func (p *popup) place() {
	s := p.a.scale()
	pt := p.anchor
	pClientToScreen.Call(p.a.hwnd, ptr(&pt))
	w := int32(popupWidthDIP * s)
	h := int32(math.Ceil(p.heightCSS * s))
	mon, _, _ := pMonitorFromWindow.Call(p.a.hwnd, 2 /*MONITOR_DEFAULTTONEAREST*/)
	mi := monitorInfo{}
	mi.Size = uint32(unsafe.Sizeof(mi))
	pGetMonitorInfoW.Call(mon, ptr(&mi))
	h = min(h, mi.Work.Bottom-pt.Y-int32(8*s))
	x := min(pt.X, mi.Work.Right-w-int32(4*s))
	pSetWindowPos.Call(p.hwnd, 0, uintptr(x), uintptr(pt.Y), uintptr(w), uintptr(max(h, int32(40*s))), swpNoActivate)
}

func (p *popup) setHeight(hCSS float64) {
	if hCSS <= 0 || hCSS == p.heightCSS {
		return
	}
	p.heightCSS = hCSS
	if p.open {
		p.place()
	}
}

func (p *popup) hide() {
	if p == nil || !p.open {
		return
	}
	p.open = false
	p.hiddenAt = time.Now()
	pShowWindow.Call(p.hwnd, swHide)
	p.wv.Hide()
	p.a.push()
}

func popupWndProc(hwnd, m, wp, lp uintptr) uintptr {
	p := thePopup
	if theApp != nil && theApp.closing {
		r, _, _ := pDefWindowProcW.Call(hwnd, m, wp, lp)
		return r
	}
	switch m {
	case wmActivate:
		if p != nil && wp&0xffff == waInactive {
			p.hide()
		}
	case wmSize:
		if p != nil && p.wv != nil {
			p.wv.Resize()
		}
	case wmClose:
		if p != nil {
			p.hide()
		}
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, m, wp, lp)
	return r
}
