package app

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"

	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wailsapp/go-webview2/pkg/edge"
	"github.com/wailsapp/go-webview2/webviewloader"
	"golang.org/x/sys/windows"

	"intra/internal/config"
	"intra/internal/netx"
	"intra/ui"
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

type App struct {
	version    string
	exe        string
	key        string
	store      *config.Store
	cfg        *config.Config
	proxy      *netx.Proxy
	wv2Version string
	dataDir    string
	args       []string

	hwnd    uintptr
	ui      *edge.Chromium
	site    *edge.Chromium
	siteWV  webview
	uiReady bool
	menu    *popup

	page, pageArg string
	cur           *siteState
	active        bool
	dark          bool
	closing       bool

	qmu   sync.Mutex
	queue []func()

	saver *saver
}

// Run starts Intra and blocks until the main window is closed.
func Run(version string) {
	a := &App{version: version, active: true}
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
	exe, err := os.Executable()
	if err != nil {
		messageBox(0, "无法定位程序文件："+err.Error(), "Intra", 0x10)
		return
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	a.exe = exe
	sum := sha256.Sum256([]byte(strings.ToLower(exe)))
	a.key = hex.EncodeToString(sum[:])[:10]

	// One instance per executable copy: each copy has its own config and browser profile.
	mutex, err := windows.CreateMutex(nil, false, u16(`Local\Intra-`+a.key))
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

	a.store = config.NewStore(exe)
	a.cfg, err = a.store.Load()
	if err != nil {
		log.Printf("load config: %v", err)
	}
	a.saver = newSaver(a.store, func(err error) {
		a.post(func() { a.toast("保存配置失败："+err.Error(), "err") })
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
	a.dataDir = filepath.Join(local, "Intra", a.key)
	os.MkdirAll(a.dataDir, 0o755)
	a.args = []string{
		"--proxy-server=http://" + a.proxy.Addr(),
		"--proxy-bypass-list=<-loopback>",
		"--ignore-certificate-errors-spki-list=" + a.proxy.SPKIHash(),
		"--disable-quic",
	}

	a.dark = systemDarkMode()
	if err := a.createWindow(); err != nil {
		messageBox(0, "无法创建窗口："+err.Error(), "Intra", 0x10)
		return
	}
	a.restorePlacement()
	a.embed()
	a.startPage()
	a.menu = newPopup(a)

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

func (a *App) className() string { return "IntraWindow-" + a.key }

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

func (a *App) newChromium() *edge.Chromium {
	c := edge.NewChromium()
	c.DataPath = filepath.Join(a.dataDir, "WebView2")
	c.AdditionalBrowserArgs = a.args
	c.SetErrorCallback(func(err error) { log.Printf("webview2: %v", err) })
	return c
}

func (a *App) bgColor() (r, g, b uint8) {
	if a.dark {
		return 0x18, 0x18, 0x18
	}
	return 0xff, 0xff, 0xff
}

func (a *App) embed() {
	// --- UI: title bar + pages ---
	a.ui = a.newChromium()
	a.ui.MessageCallback = func(m string, _ *edge.ICoreWebView2, _ *edge.ICoreWebView2WebMessageReceivedEventArgs) {
		a.onMessage(false, m)
	}
	a.ui.Embed(a.hwnd)
	r, g, b := a.bgColor()
	a.ui.SetBackgroundColour(r, g, b, 255)
	lockDownUI(a.ui)
	a.ui.Init(`window.__INTRA_MODE="main"`)
	a.layout()
	a.ui.NavigateToString(ui.HTML)

	// --- site ---
	a.site = a.newChromium()
	a.site.Embed(a.hwnd)
	a.site.SetBackgroundColour(r, g, b, 255)
	if s, err := a.site.GetSettings(); err == nil {
		// Sites must not reach the bridge: go-webview2 terminates the process
		// on non-string web messages, and the site has no business talking to Go.
		s.PutIsWebMessageEnabled(false)
		s.PutAreHostObjectsAllowed(false)
		s.PutIsStatusBarEnabled(true)
	}
	a.site.Hide()
	a.siteWV = webviewOf(a.site)
	a.hookSite()
	a.setRasterScale()
	a.layout()
}

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

func (a *App) hookSite() {
	wv := a.siteWV
	if wv == 0 {
		return
	}
	vt := wv.vt()
	wv.on(vt.AddDocumentTitleChanged, func(_, _ uintptr) {
		if a.cur != nil {
			a.cur.Title = wv.Title()
			a.updateWindowTitle()
			a.push()
		}
	})
	wv.on(vt.AddHistoryChanged, func(_, _ uintptr) {
		if a.cur != nil {
			a.cur.CanBack, a.cur.CanFwd = wv.CanGoBack(), wv.CanGoForward()
			a.push()
		}
	})
	wv.on(vt.AddSourceChanged, func(_, _ uintptr) {
		if a.cur != nil {
			a.cur.URL = wv.Source()
		}
	})
	wv.on(vt.AddNavigationStarting, func(_, _ uintptr) {
		if a.cur != nil && !a.cur.Loading {
			a.cur.Loading = true
			a.push()
		}
	})
	a.site.NavigationCompletedCallback = func(_ *edge.ICoreWebView2, _ *edge.ICoreWebView2NavigationCompletedEventArgs) {
		if a.cur != nil {
			a.cur.Loading = false
			a.cur.CanBack, a.cur.CanFwd = wv.CanGoBack(), wv.CanGoForward()
			a.push()
		}
	}
	// keep window.open / target=_blank inside the app window
	wv.on(vt.AddNewWindowRequested, func(_, args uintptr) {
		uri := newWindowURI(args)
		newWindowHandled(args)
		low := strings.ToLower(uri)
		switch {
		case strings.HasPrefix(low, "http://"), strings.HasPrefix(low, "https://"):
			a.site.Navigate(uri)
		case uri != "" && !strings.HasPrefix(low, "javascript:") && !strings.HasPrefix(low, "file:"):
			shellOpen(uri)
		}
	})
}

// post runs fn on the UI thread.
func (a *App) post(fn func()) {
	a.qmu.Lock()
	a.queue = append(a.queue, fn)
	a.qmu.Unlock()
	pPostMessageW.Call(a.hwnd, wmAppCall, 0, 0)
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

// ---------------------------------------------------------------- state

func (a *App) startPage() {
	dp := a.cfg.DefaultPage
	if dp == "last" {
		dp = a.cfg.LastBookmark
	}
	if dp != "" && dp != "home" && a.cfg.Bookmark(dp) != nil {
		a.openBookmark(dp)
		return
	}
	a.showPage("home", "")
}

func (a *App) showPage(page, arg string) {
	if page == "" && a.cur == nil {
		page = "home"
	}
	a.page, a.pageArg = page, arg
	a.layout()
	a.push()
	if page == "" {
		a.focusSite()
	} else if a.ui != nil {
		a.ui.Focus()
	}
}

func (a *App) focusSite() {
	if a.site != nil && a.cur != nil && a.page == "" {
		a.site.Focus()
	}
}

func (a *App) openBookmark(id string) {
	b := a.cfg.Bookmark(id)
	if b == nil {
		return
	}
	a.cur = &siteState{ID: b.ID, Name: b.Name, URL: b.URL, Loading: true}
	a.applyRecords()
	a.page, a.pageArg = "", ""
	a.layout()
	a.site.Navigate(normalizeURL(b.URL))
	a.cfg.LastBookmark = id
	a.updateWindowTitle()
	a.push()
	a.focusSite()
}

func (a *App) closeSite() {
	a.cur = nil
	if a.site != nil {
		a.site.Navigate("about:blank")
	}
	a.applyRecords()
	a.updateWindowTitle()
}

func normalizeURL(u string) string {
	u = strings.TrimSpace(u)
	low := strings.ToLower(u)
	if !strings.HasPrefix(low, "http://") && !strings.HasPrefix(low, "https://") {
		u = "https://" + u
	}
	return u
}

func (a *App) updateWindowTitle() {
	t := "Intra"
	if a.cur != nil {
		name := a.cur.Name
		if a.cur.Title != "" {
			name = a.cur.Title
		}
		t = name + " - Intra"
	}
	pSetWindowTextW.Call(a.hwnd, ptr(u16(t)))
}

// applyRecords installs DNS records with the open bookmark taking precedence.
func (a *App) applyRecords() {
	var groups [][]netx.Record
	conv := func(b *config.Bookmark) []netx.Record {
		out := make([]netx.Record, 0, len(b.Records))
		for _, r := range b.Records {
			out = append(out, netx.Record{Host: r.Host, Type: r.Type, Value: r.Value})
		}
		return out
	}
	if a.cur != nil {
		if b := a.cfg.Bookmark(a.cur.ID); b != nil {
			groups = append(groups, conv(b))
		}
	}
	for i := range a.cfg.Bookmarks {
		b := &a.cfg.Bookmarks[i]
		if a.cur == nil || b.ID != a.cur.ID {
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
	Maximized bool       `json:"maximized"`
	Active    bool       `json:"active"`
	Info      struct {
		Version    string `json:"version"`
		RootCount  int    `json:"rootCount"`
		BundleDate string `json:"bundleDate"`
		ExePath    string `json:"exePath"`
		WebView2   string `json:"webview2"`
	} `json:"info"`
}

func (a *App) state() *stateMsg {
	s := &stateMsg{T: "state", Bookmarks: a.cfg.Bookmarks, CAs: []caView{}, Current: a.cur,
		Page: a.page, PageArg: a.pageArg, Active: a.active}
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
	s.MenuOpen = a.menu != nil && a.menu.open
	s.Maximized = a.hwnd != 0 && isZoomed(a.hwnd)
	s.Info.Version = a.version
	s.Info.RootCount = netx.BuiltinRootCount()
	s.Info.BundleDate = netx.BundleDate()
	s.Info.ExePath = a.exe
	s.Info.WebView2 = a.wv2Version
	return s
}

// push sends the full state to every ready front-end.
func (a *App) push() {
	if a.ui == nil || a.closing {
		return
	}
	j, err := json.Marshal(a.state())
	if err != nil {
		return
	}
	script := "window.intra&&intra.recv(" + string(j) + ")"
	if a.uiReady {
		a.ui.Eval(script)
	}
	if a.menu != nil && a.menu.ready {
		a.menu.wv.Eval(script)
	}
}

func (a *App) toast(text, kind string) {
	if !a.uiReady || a.closing {
		return
	}
	j, _ := json.Marshal(map[string]string{"t": "toast", "text": text, "kind": kind})
	a.ui.Eval("window.intra&&intra.recv(" + string(j) + ")")
}

func (a *App) save() { a.saver.save(a.cfg.Clone()) }

