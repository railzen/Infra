package app

import (
	"crypto/x509"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"github.com/wailsapp/go-webview2/pkg/edge"
	"github.com/wailsapp/go-webview2/webviewloader"
	"golang.org/x/sys/windows"

	"intra/internal/config"
	"intra/internal/netx"
)

const (
	barHeightDIP = 36 // Codex titleBarOverlay height
	wmAppCall    = wmApp + 1
)

type siteState struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	CanBack bool   `json:"canBack"`
	CanFwd  bool   `json:"canFwd"`
	Loading bool   `json:"loading"`
}

// App holds what all windows share: configuration, the proxy and saving.
type App struct {
	version    string
	store      *config.Store
	cfg        *config.Config
	proxy      *netx.Proxy
	wv2Version string
	dataDir    string
	args       []string

	msgHwnd uintptr // message-only window that runs posted functions
	windows []*Window

	qmu   sync.Mutex
	queue []func()

	saver *saver
}

var theApp *App // window procedures need a way back to the App

// Run starts Intra and blocks until the last window is closed.
func Run(version string) {
	a := &App{version: version}
	theApp = a
	// Per-monitor v2 DPI awareness (also declared in the manifest); without it
	// Windows bitmap-stretches the whole window on high-DPI displays.
	if pSetProcessDpiAwarenessCx.Find() == nil {
		pSetProcessDpiAwarenessCx.Call(^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (-4)
	}
	if p := os.Getenv("INTRA_LOG"); p != "" {
		if f, err := os.Create(p); err == nil {
			log.SetOutput(f)
		}
	}

	// All copies share one config file, so only one Intra runs per user session.
	mutex, err := windows.CreateMutex(nil, false, u16(`Local\`+config.AppDirName))
	if err == windows.ERROR_ALREADY_EXISTS {
		activateExisting(a.className())
		return
	}
	defer windows.CloseHandle(mutex)

	ver, err := webviewloader.GetAvailableCoreWebView2BrowserVersionString("")
	if err != nil || ver == "" {
		if messageBox(0, "未检测到 Microsoft Edge WebView2 运行时。\n\nWindows 11 已自带此组件；Windows 10 需要先安装。是否打开下载页面？", "Intra", 0x34) == 6 {
			shellOpen("https://go.microsoft.com/fwlink/p/?LinkId=2124703")
		}
		return
	}
	a.wv2Version = ver

	path, err := config.DefaultPath()
	if err != nil {
		messageBox(0, "无法定位配置文件夹："+err.Error(), "Intra", 0x10)
		return
	}
	a.store = config.NewStore(path)
	a.cfg, err = a.store.Load()
	var corrupt *config.CorruptError
	switch {
	case errors.As(err, &corrupt):
		// keep the unreadable file for the user instead of overwriting it
		backup, qerr := a.store.Quarantine()
		msg := "配置文件无法读取，已使用空白配置启动。\n\n" + err.Error()
		if qerr == nil {
			msg += "\n\n原文件已保留为：\n" + backup
		}
		messageBox(0, msg, "Intra", 0x30)
	case err != nil && !errors.Is(err, os.ErrNotExist):
		log.Printf("load config: %v", err)
	}
	a.saver = newSaver(a.store, func(err error) {
		a.post(func() {
			if w := a.foreground(); w != nil {
				w.toast("保存配置失败："+err.Error(), "err")
			}
		})
	})
	a.applyTrust()
	a.applyRecords()

	a.proxy, err = netx.Start()
	if err != nil {
		messageBox(0, "无法启动本地代理："+err.Error(), "Intra", 0x10)
		return
	}
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		local = os.TempDir()
	}
	a.dataDir = filepath.Join(local, config.AppDirName)
	os.MkdirAll(a.dataDir, 0o755)
	a.args = []string{
		"--proxy-server=http://" + a.proxy.Addr(),
		"--proxy-bypass-list=<-loopback>",
		"--ignore-certificate-errors-spki-list=" + a.proxy.SPKIHash(),
		"--disable-quic",
	}

	if err := a.createMessageWindow(); err != nil {
		messageBox(0, "无法创建窗口："+err.Error(), "Intra", 0x10)
		return
	}
	if err := a.registerWindowClass(); err != nil {
		messageBox(0, "无法创建窗口："+err.Error(), "Intra", 0x10)
		return
	}
	start := a.startBookmark()
	w, err := a.newWindow(nil, start)
	if err != nil {
		messageBox(0, "无法创建窗口："+err.Error(), "Intra", 0x10)
		return
	}
	if start != "" {
		w.openBookmark(start)
	} else {
		w.showPage("home", "")
	}

	var m msg
	for {
		r, _, _ := pGetMessageW.Call(ptr(&m), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(ptr(&m))
		pDispatchMessageW.Call(ptr(&m))
	}
}

func (a *App) className() string { return config.AppDirName + "-Window" }

func activateExisting(class string) {
	h, _, _ := pFindWindowW.Call(ptr(u16(class)), 0)
	if h == 0 {
		return
	}
	if isIconic(h) {
		pShowWindow.Call(h, swRestore)
	}
	pSetForegroundWindow.Call(h)
}

// createMessageWindow makes the message-only window that post() targets, so
// queued work keeps running no matter which top-level windows exist.
func (a *App) createMessageWindow() error {
	cls := a.className() + "-msg"
	wc := wndClassEx{
		WndProc:   windows.NewCallback(func(h, m, wp, lp uintptr) uintptr {
			if m == wmAppCall {
				a.runQueue()
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
	const hwndMessage = ^uintptr(2) // HWND_MESSAGE (-3)
	h, _, err := pCreateWindowExW.Call(0, ptr(u16(cls)), 0, 0, 0, 0, 0, 0, hwndMessage, 0, hInstance(), 0)
	if h == 0 {
		return err
	}
	a.msgHwnd = h
	return nil
}

// post runs fn on the UI thread, outside of any webview callback.
func (a *App) post(fn func()) {
	a.qmu.Lock()
	a.queue = append(a.queue, fn)
	a.qmu.Unlock()
	pPostMessageW.Call(a.msgHwnd, wmAppCall, 0, 0)
}

func (a *App) runQueue() {
	a.qmu.Lock()
	q := a.queue
	a.queue = nil
	a.qmu.Unlock()
	for _, fn := range q {
		fn()
	}
}

func (a *App) newChromium() *edge.Chromium {
	c := edge.NewChromium()
	c.DataPath = filepath.Join(a.dataDir, "WebView2")
	c.AdditionalBrowserArgs = a.args
	c.SetErrorCallback(func(err error) { log.Printf("webview2: %v", err) })
	return c
}

// bgColor is the white the UI is drawn on, used before a webview paints.
func (a *App) bgColor() (r, g, b uint8) { return 0xff, 0xff, 0xff }

func lockDownUI(c *edge.Chromium) {
	s, err := c.GetSettings()
	if err != nil {
		return
	}
	s.PutAreDefaultContextMenusEnabled(false)
	s.PutIsZoomControlEnabled(false)
	s.PutIsStatusBarEnabled(false)
	s.PutAreDevToolsEnabled(os.Getenv("INTRA_DEVTOOLS") != "")
	if s3 := s.GetICoreWebView2Settings3(); s3 != nil {
		s3.PutAreBrowserAcceleratorKeysEnabled(false)
	}
}

// ---------------------------------------------------------------- windows

// open shows a bookmark requested from window `from`: a window still on the
// home page loads it in place; a window already showing a site gets a new
// window; a bookmark already open somewhere brings that window forward.
func (a *App) open(from *Window, id string) {
	b := a.cfg.Bookmark(id)
	if b == nil {
		return
	}
	for _, w := range a.windows {
		if w.cur != nil && w.cur.ID == id && !w.closing {
			if w == from {
				w.showPage("", "")
			}
			w.activate()
			return
		}
	}
	if from.cur == nil {
		a.openHere(from, id)
		return
	}
	a.openNew(from, id)
}

// openHere loads a bookmark in window w, replacing whatever it shows. The
// window first saves its placement for the page it leaves, then takes on the
// link's own size and position.
func (a *App) openHere(w *Window, id string) {
	b := a.cfg.Bookmark(id)
	if b == nil {
		return
	}
	if w.cur != nil && w.cur.ID == id {
		w.showPage("", "")
		return
	}
	w.rememberPlacement()
	w.applyPlacement(b.Window)
	w.openBookmark(id)
}

// openNew opens a bookmark in a new window cascaded from `from`.
func (a *App) openNew(from *Window, id string) {
	if a.cfg.Bookmark(id) == nil {
		return
	}
	// creating webviews pumps messages; do it outside the current webview callback
	a.post(func() {
		w, err := a.newWindow(from, id)
		if err != nil {
			from.toast("无法打开新窗口："+err.Error(), "err")
			return
		}
		w.openBookmark(id)
	})
}

func (a *App) closeWindow(w *Window) {
	w.rememberPlacement()
	w.closing = true
	for i, x := range a.windows {
		if x == w {
			a.windows = append(a.windows[:i], a.windows[i+1:]...)
			break
		}
	}
	closeController(w.ui)
	closeController(w.site)
	if w.menu != nil {
		closeController(w.menu.wv)
	}
	a.applyRecords()
	if len(a.windows) == 0 {
		a.saver.flush(a.cfg.Clone())
	} else {
		a.save()
	}
}

// foreground returns the active window, or any window.
func (a *App) foreground() *Window {
	for _, w := range a.windows {
		if w.active {
			return w
		}
	}
	if len(a.windows) > 0 {
		return a.windows[0]
	}
	return nil
}

func (a *App) pushAll() {
	for _, w := range a.windows {
		w.push()
	}
}

// ---------------------------------------------------------------- shared state

func normalizeURL(u string) string {
	u = strings.TrimSpace(u)
	low := strings.ToLower(u)
	if !strings.HasPrefix(low, "http://") && !strings.HasPrefix(low, "https://") {
		u = "https://" + u
	}
	return u
}

// applyRecords installs DNS records; bookmarks open in windows take precedence.
func (a *App) applyRecords() {
	var groups [][]netx.Record
	conv := func(b *config.Bookmark) []netx.Record {
		out := make([]netx.Record, 0, len(b.Records))
		for _, r := range b.Records {
			out = append(out, netx.Record{Host: r.Host, Type: r.Type, Value: r.Value})
		}
		return out
	}
	open := map[string]bool{}
	for _, w := range a.windows {
		if w.cur == nil || w.closing || open[w.cur.ID] {
			continue
		}
		if b := a.cfg.Bookmark(w.cur.ID); b != nil {
			open[b.ID] = true
			groups = append(groups, conv(b))
		}
	}
	for i := range a.cfg.Bookmarks {
		if b := &a.cfg.Bookmarks[i]; !open[b.ID] {
			groups = append(groups, conv(b))
		}
	}
	netx.SetRecords(groups)
}

func (a *App) applyTrust() {
	var certs []*x509.Certificate
	for _, ca := range a.cfg.CAs {
		cs, err := netx.ParseCertificates([]byte(ca.PEM))
		if err == nil {
			certs = append(certs, cs...)
		}
	}
	netx.SetExtraRoots(certs)
}

type caView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	NotAfter    string `json:"notAfter"`
	AddedAt     string `json:"addedAt"`
}

type stateMsg struct {
	T         string            `json:"t"`
	Bookmarks []config.Bookmark `json:"bookmarks"`
	CAs       []caView          `json:"cas"`
	Settings  struct {
		DefaultPage    string `json:"defaultPage"`
		RememberWindow bool   `json:"rememberWindow"`
	} `json:"settings"`
	Current   *siteState `json:"current"`
	Page      string     `json:"page"`
	PageArg   string     `json:"pageArg"`
	MenuOpen  bool       `json:"menuOpen"`
	MenuKind  string     `json:"menuKind"`
	Maximized bool       `json:"maximized"`
	Active    bool       `json:"active"`
	Info      struct {
		Version    string `json:"version"`
		RootCount  int    `json:"rootCount"`
		BundleDate string `json:"bundleDate"`
		ConfigPath string `json:"configPath"`
		WebView2   string `json:"webview2"`
	} `json:"info"`
}

// baseState fills the fields every window shares.
func (a *App) baseState() *stateMsg {
	s := &stateMsg{T: "state", Bookmarks: a.cfg.Bookmarks, CAs: []caView{}}
	if s.Bookmarks == nil {
		s.Bookmarks = []config.Bookmark{}
	}
	for i := range s.Bookmarks {
		if s.Bookmarks[i].Records == nil {
			s.Bookmarks[i].Records = []config.DNSRecord{}
		}
	}
	for _, c := range a.cfg.CAs {
		s.CAs = append(s.CAs, caView{c.ID, c.Name, c.Fingerprint, c.NotAfter, c.AddedAt})
	}
	s.Settings.DefaultPage = a.cfg.DefaultPage
	s.Settings.RememberWindow = a.cfg.RememberWindow
	s.Info.Version = a.version
	s.Info.RootCount = netx.BuiltinRootCount()
	s.Info.BundleDate = netx.BundleDate()
	s.Info.ConfigPath = a.store.Path()
	s.Info.WebView2 = a.wv2Version
	return s
}

func (a *App) save() { a.saver.save(a.cfg.Clone()) }
