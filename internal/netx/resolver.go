package netx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"time"
)

// Record is a user defined DNS record. Type is "A" or "CNAME".
type Record struct {
	Host  string
	Type  string
	Value string
}

type recordSet struct {
	exact    map[string][]Record
	wildcard map[string][]Record // key: suffix without "*." e.g. "corp.local"
}

var records atomic.Pointer[recordSet]

func init() { SetRecords(nil) }

func normHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}

// SetRecords replaces the active record table. Each group is one bookmark's
// records, in priority order: a host defined by an earlier group is ignored in
// later groups, so callers put the active bookmark first.
func SetRecords(groups [][]Record) {
	rs := &recordSet{exact: map[string][]Record{}, wildcard: map[string][]Record{}}
	owner := map[string]int{}     // host -> group that defined it
	kind := map[string]string{}   // host -> first record type seen
	for gi, list := range groups {
		for _, r := range list {
			h := normHost(r.Host)
			t := strings.ToUpper(strings.TrimSpace(r.Type))
			v := normHost(r.Value)
			if h == "" || v == "" || (t != "A" && t != "CNAME") {
				continue
			}
			if g, ok := owner[h]; ok && g != gi {
				continue
			}
			// within a group a host is either one CNAME or a set of A records
			if k, ok := kind[h]; ok && (k != t || t == "CNAME") {
				continue
			}
			owner[h], kind[h] = gi, t
			r = Record{Host: h, Type: t, Value: v}
			if strings.HasPrefix(h, "*.") {
				rs.wildcard[h[2:]] = append(rs.wildcard[h[2:]], r)
			} else {
				rs.exact[h] = append(rs.exact[h], r)
			}
		}
	}
	records.Store(rs)
}

func (rs *recordSet) lookup(host string) []Record {
	if r, ok := rs.exact[host]; ok {
		return r
	}
	// most specific wildcard wins: a.b.corp.local tries b.corp.local, corp.local, local
	for i := strings.IndexByte(host, '.'); i >= 0; {
		suffix := host[i+1:]
		if r, ok := rs.wildcard[suffix]; ok {
			return r
		}
		j := strings.IndexByte(suffix, '.')
		if j < 0 {
			break
		}
		i += j + 1
	}
	return nil
}

// Resolve resolves host to IP addresses, consulting custom records first and
// following CNAME chains through custom records before falling back to system DNS.
func Resolve(ctx context.Context, host string) ([]net.IP, error) {
	h := normHost(host)
	if ip := net.ParseIP(strings.Trim(h, "[]")); ip != nil {
		return []net.IP{ip}, nil
	}
	rs := records.Load()
	cur := h
	for depth := 0; depth < 12; depth++ {
		recs := rs.lookup(cur)
		if len(recs) == 0 {
			break
		}
		if recs[0].Type == "CNAME" {
			cur = recs[0].Value
			if ip := net.ParseIP(cur); ip != nil {
				return []net.IP{ip}, nil
			}
			continue
		}
		var ips []net.IP
		for _, r := range recs {
			if ip := net.ParseIP(r.Value); ip != nil {
				ips = append(ips, ip)
			}
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("自定义 A 记录 %s 的值不是有效的 IP 地址", cur)
		}
		return ips, nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, cur)
	if err != nil {
		if cur != h {
			return nil, fmt.Errorf("解析 CNAME 目标 %s 失败: %w", cur, err)
		}
		return nil, err
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		ips = append(ips, a.IP)
	}
	return ips, nil
}

// Dial connects to addr ("host:port") using Resolve. IPv4 addresses are tried first.
func Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := Resolve(ctx, host)
	if err != nil {
		return nil, &DialError{Host: host, Err: err}
	}
	ordered := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		if ip.To4() != nil {
			ordered = append(ordered, ip)
		}
	}
	for _, ip := range ips {
		if ip.To4() == nil {
			ordered = append(ordered, ip)
		}
	}
	d := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	var lastErr error
	for _, ip := range ordered {
		c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
		if err == nil {
			return c, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用的地址")
	}
	return nil, &DialError{Host: host, Err: lastErr, Connect: true}
}

// DialError distinguishes resolution failures from connection failures for the error page.
type DialError struct {
	Host    string
	Err     error
	Connect bool
}

func (e *DialError) Error() string { return e.Err.Error() }
func (e *DialError) Unwrap() error { return e.Err }
