package netx

import (
	"crypto/x509"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"
)

func classify(host string, err error) (title, desc string) {
	var unk x509.UnknownAuthorityError
	var hn x509.HostnameError
	var inv x509.CertificateInvalidError
	var de *DialError
	switch {
	case errors.As(err, &unk):
		return "证书不受信任",
			"该网站的证书不是由 Intra 信任的公共 CA 签发。Intra 只信任 Mozilla 公共根证书和你手动导入的 CA，不会使用 Windows 系统证书库。如果这是企业内部 CA，请在「设置 → 证书」中导入它的根证书。"
	case errors.As(err, &hn):
		return "证书与域名不匹配",
			fmt.Sprintf("服务器返回的证书不包含 %s。请检查网址，或确认 DNS 记录指向了正确的服务器。", host)
	case errors.As(err, &inv):
		if inv.Reason == x509.Expired {
			return "证书已过期或尚未生效", "服务器证书不在有效期内，也可能是本机时间不正确。"
		}
		return "证书无效", "服务器证书无法用于此网站。"
	case errors.As(err, &de) && !de.Connect:
		return "无法解析域名",
			fmt.Sprintf("找不到 %s 的地址。可以在收藏夹中为它添加 A 或 CNAME 记录。", host)
	case errors.As(err, &de):
		return "无法连接到服务器", fmt.Sprintf("已解析 %s，但连接失败。请检查网络或 DNS 记录中的地址是否可达。", host)
	case strings.Contains(err.Error(), "remote error: tls"):
		return "服务器拒绝了安全连接",
			fmt.Sprintf("目标服务器不接受 %s 的 HTTPS 连接。如果为它配置了 A 或 CNAME 记录，请确认目标服务器确实托管了这个域名。", host)
	}
	return "无法打开此网站", "与服务器通信时发生错误。"
}

func writeErrorPage(w http.ResponseWriter, host string, err error) {
	title, desc := classify(host, err)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusBadGateway)
	fmt.Fprintf(w, errorTemplate, html.EscapeString(title), html.EscapeString(title),
		html.EscapeString(host), html.EscapeString(desc), html.EscapeString(err.Error()))
}

const errorTemplate = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>%s</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>
:root{--bg:#fff;--fg:#1a1c1f;--muted:#5d5d5d;--faint:#8f8f8f;--border:#0000001a;--soft:#00000008;color-scheme:light}
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:grid;place-items:center;background:var(--bg);color:var(--fg);
font:14px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI","Microsoft YaHei UI",sans-serif;padding:24px}
main{max-width:520px;width:100%%}
.ic{width:40px;height:40px;border-radius:10px;border:1px solid var(--border);display:grid;place-items:center;margin-bottom:20px;color:var(--muted)}
h1{font-size:20px;font-weight:600;margin:0 0 4px}
.host{color:var(--faint);font-size:13px;margin:0 0 16px}
p{color:var(--muted);margin:0 0 20px}
pre{margin:0;padding:12px 14px;border:1px solid var(--border);background:var(--soft);border-radius:10px;
font:12px/1.5 ui-monospace,"Cascadia Mono",Consolas,monospace;color:var(--faint);white-space:pre-wrap;word-break:break-all}
button{margin-top:20px;height:32px;padding:0 14px;border-radius:9999px;border:1px solid var(--border);background:transparent;color:var(--fg);font:inherit;font-size:13px;cursor:pointer}
button:hover{background:var(--soft)}
</style></head><body><main>
<div class="ic"><svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3l9 16H3z"/><path d="M12 10v4"/><path d="M12 17.5v.01"/></svg></div>
<h1>%s</h1><div class="host">%s</div><p>%s</p><pre>%s</pre>
<button onclick="location.reload()">重新加载</button>
</main></body></html>`
