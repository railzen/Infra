package app

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"intra/internal/config"
	"intra/internal/netx"
)

type inMsg struct {
	T              string           `json:"t"`
	X, Y, H        float64
	Edge           string           `json:"edge"`
	Page           string           `json:"page"`
	Arg            string           `json:"arg"`
	ID             string           `json:"id"`
	Action         string           `json:"action"`
	Dir            int              `json:"dir"`
	Bookmark       *config.Bookmark `json:"bookmark"`
	Open           bool             `json:"open"`
	DefaultPage    string           `json:"defaultPage"`
	RememberWindow bool             `json:"rememberWindow"`
	Name           string           `json:"name"`
	Data           string           `json:"data"`
}

func (a *App) onMessage(fromMenu bool, raw string) {
	if a.closing {
		return
	}
	var m inMsg
	if json.Unmarshal([]byte(raw), &m) != nil {
		return
	}
	switch m.T {
	case "ready":
		if fromMenu {
			a.menu.ready = true
		} else {
			a.uiReady = true
		}
		a.push()

	// --- window chrome ---
	case "drag":
		a.beginNCDrag(htCaption)
	case "resize":
		hit := uintptr(htTop)
		switch m.Edge {
		case "tl":
			hit = 13 // HTTOPLEFT
		case "tr":
			hit = 14 // HTTOPRIGHT
		}
		a.beginNCDrag(hit)
	case "min":
		pShowWindow.Call(a.hwnd, swMinimize)
	case "max":
		a.toggleMaximize()
	case "close":
		pPostMessageW.Call(a.hwnd, wmClose, 0, 0)
	case "sysmenu":
		a.showSystemMenu()

	// --- dropdown ---
	case "menu":
		a.menu.toggle(m.X, m.Y)
	case "menuSize":
		a.menu.setHeight(m.H)
	case "menuClose":
		a.menu.hide()

	// --- navigation ---
	case "show":
		a.menu.hide()
		a.showPage(m.Page, m.Arg)
	case "open":
		a.menu.hide()
		a.openBookmark(m.ID)
	case "nav":
		if a.cur == nil || a.siteWV == 0 {
			return
		}
		switch m.Action {
		case "back":
			a.siteWV.GoBack()
		case "forward":
			a.siteWV.GoForward()
		case "reload":
			a.siteWV.Reload()
		case "stop":
			a.siteWV.Stop()
		}

	// --- data ---
	case "saveBookmark":
		a.saveBookmark(m.Bookmark, m.Open)
	case "deleteBookmark":
		a.deleteBookmark(m.ID)
	case "moveBookmark":
		a.moveBookmark(m.ID, m.Dir)
	case "settings":
		dp := m.DefaultPage
		if dp != "home" && dp != "last" && a.cfg.Bookmark(dp) == nil {
			dp = "home"
		}
		a.cfg.DefaultPage = dp
		a.cfg.RememberWindow = m.RememberWindow
		if !m.RememberWindow {
			a.cfg.Window = nil
		}
		a.save()
		a.push()
	case "addCA":
		a.addCA(m.Name, m.Data)
	case "deleteCA":
		a.deleteCA(m.ID)
	}
}

func newID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// validateBookmark mirrors the checks in the UI; the UI is not trusted to have run them.
func validateBookmark(b *config.Bookmark) error {
	b.Name = strings.TrimSpace(b.Name)
	b.URL = strings.TrimSpace(b.URL)
	if b.Name == "" {
		return fmt.Errorf("请输入名称")
	}
	u, err := url.Parse(normalizeURL(b.URL))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("网址格式不正确")
	}
	recs := b.Records[:0]
	for _, r := range b.Records {
		r.Host = strings.ToLower(strings.TrimSpace(r.Host))
		r.Value = strings.TrimSpace(r.Value)
		r.Type = strings.ToUpper(strings.TrimSpace(r.Type))
		if r.Host == "" && r.Value == "" {
			continue
		}
		switch r.Type {
		case "A":
			if net.ParseIP(r.Value) == nil {
				return fmt.Errorf("%s 的 A 记录值不是有效的 IP 地址", r.Host)
			}
		case "CNAME":
			if r.Value == "" || strings.ContainsAny(r.Value, " /:*") {
				return fmt.Errorf("%s 的 CNAME 值不是有效的主机名", r.Host)
			}
		default:
			return fmt.Errorf("不支持的记录类型 %q", r.Type)
		}
		if r.Host == "" || strings.ContainsAny(r.Host, " /:") {
			return fmt.Errorf("主机名 %q 格式不正确", r.Host)
		}
		recs = append(recs, r)
	}
	b.Records = recs
	return nil
}

func (a *App) saveBookmark(b *config.Bookmark, open bool) {
	if b == nil {
		return
	}
	if err := validateBookmark(b); err != nil {
		a.toast(err.Error(), "err")
		return
	}
	if existing := a.cfg.Bookmark(b.ID); b.ID != "" && existing != nil {
		*existing = *b
	} else {
		b.ID = newID()
		a.cfg.Bookmarks = append(a.cfg.Bookmarks, *b)
	}
	if a.cur != nil && a.cur.ID == b.ID {
		a.cur.Name = b.Name
		a.updateWindowTitle()
	}
	a.applyRecords()
	a.save()
	a.toast("已保存", "ok")
	if open {
		a.openBookmark(b.ID)
		return
	}
	a.showPage("bookmarks", "")
}

func (a *App) deleteBookmark(id string) {
	out := a.cfg.Bookmarks[:0]
	for _, b := range a.cfg.Bookmarks {
		if b.ID != id {
			out = append(out, b)
		}
	}
	a.cfg.Bookmarks = out
	if a.cfg.DefaultPage == id {
		a.cfg.DefaultPage = "home"
	}
	if a.cfg.LastBookmark == id {
		a.cfg.LastBookmark = ""
	}
	if a.cur != nil && a.cur.ID == id {
		a.closeSite()
	}
	a.applyRecords()
	a.save()
	a.toast("已删除", "ok")
	a.showPage("bookmarks", "")
}

func (a *App) moveBookmark(id string, dir int) {
	bs := a.cfg.Bookmarks
	for i := range bs {
		if bs[i].ID == id {
			j := i + dir
			if j >= 0 && j < len(bs) {
				bs[i], bs[j] = bs[j], bs[i]
				a.save()
				a.push()
			}
			return
		}
	}
}

func (a *App) addCA(name, data string) {
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		a.toast("无法读取文件", "err")
		return
	}
	certs, err := netx.ParseCertificates(raw)
	if err != nil {
		a.toast(name+"："+err.Error(), "err")
		return
	}
	added, dup := 0, 0
	for _, c := range certs {
		fp := netx.Fingerprint(c)
		exists := false
		for _, ca := range a.cfg.CAs {
			if ca.Fingerprint == fp {
				exists = true
				break
			}
		}
		if exists {
			dup++
			continue
		}
		a.cfg.CAs = append(a.cfg.CAs, config.CA{
			ID:          strings.ReplaceAll(fp, ":", "")[:16],
			Name:        netx.DisplayName(c),
			PEM:         netx.CertToPEM(c),
			Fingerprint: fp,
			NotAfter:    c.NotAfter.Local().Format("2006-01-02"),
			AddedAt:     time.Now().Format("2006-01-02"),
		})
		added++
	}
	if added == 0 {
		a.toast("证书已存在", "err")
		return
	}
	a.applyTrust()
	a.save()
	a.push()
	msg := fmt.Sprintf("已添加 %d 个 CA 证书", added)
	if dup > 0 {
		msg += fmt.Sprintf("（%d 个已存在）", dup)
	}
	a.toast(msg, "ok")
}

func (a *App) deleteCA(id string) {
	out := a.cfg.CAs[:0]
	for _, c := range a.cfg.CAs {
		if c.ID != id {
			out = append(out, c)
		}
	}
	a.cfg.CAs = out
	a.applyTrust()
	a.save()
	a.push()
	a.toast("已删除证书", "ok")
}
