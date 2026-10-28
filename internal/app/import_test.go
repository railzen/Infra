package app

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"intra/internal/config"
	"intra/internal/netx"
)

func testCA(t *testing.T, cn string) *x509.Certificate {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return c
}

func TestExportImportMerge(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "Intra.exe")
	os.WriteFile(exe, []byte("MZ"), 0o755)
	store := config.NewStore(exe)
	defer netx.SetRecords(nil)
	defer netx.SetExtraRoots(nil)

	// source machine
	src := config.Default()
	src.DefaultPage = "oa"
	src.RememberWindow = false
	src.Bookmarks = []config.Bookmark{
		{ID: "oa", Name: "OA 新", URL: "oa.corp", Records: []config.DNSRecord{{Host: "oa.corp", Type: "A", Value: "10.0.0.2"}}},
		{ID: "wiki", Name: "Wiki", URL: "https://wiki.corp"},
		{ID: "bad", Name: "Bad", URL: "https://x.corp", Records: []config.DNSRecord{{Host: "x.corp", Type: "A", Value: "not-an-ip"}}},
	}
	ca := testCA(t, "Corp Root")
	src.CAs = []config.CA{{Name: "Corp Root", PEM: netx.CertToPEM(ca)}, {Name: "junk", PEM: "garbage"}}
	data, err := src.Export()
	if err != nil {
		t.Fatal(err)
	}
	e, err := config.ParseExport(data)
	if err != nil {
		t.Fatal(err)
	}

	// destination already has "oa" (old name) and the same CA
	a := &App{store: store, cfg: config.Default()}
	a.saver = newSaver(store, func(err error) { t.Error(err) })
	a.cfg.Bookmarks = []config.Bookmark{{ID: "oa", Name: "OA 旧", URL: "https://oa.corp"}, {ID: "mail", Name: "Mail", URL: "https://mail.corp"}}
	a.addCert(ca)

	added, updated, cas, skipped := a.mergeImport(e)
	if added != 1 || updated != 1 || cas != 0 || skipped != 2 {
		t.Fatalf("added=%d updated=%d cas=%d skipped=%d", added, updated, cas, skipped)
	}
	if len(a.cfg.Bookmarks) != 3 || a.cfg.Bookmark("oa").Name != "OA 新" || a.cfg.Bookmark("mail") == nil || a.cfg.Bookmark("bad") != nil {
		t.Fatalf("bookmarks: %+v", a.cfg.Bookmarks)
	}
	if a.cfg.DefaultPage != "oa" || a.cfg.RememberWindow || len(a.cfg.CAs) != 1 {
		t.Fatalf("settings/cas: %+v", a.cfg)
	}
	// imported records are live in the resolver
	ips, err := netx.Resolve(t.Context(), "oa.corp")
	if err != nil || ips[0].String() != "10.0.0.2" {
		t.Fatalf("resolve: %v %v", ips, err)
	}
	// and persisted into the exe trailer
	a.saver.flush(a.cfg.Clone())
	saved, err := store.Load()
	if err != nil || len(saved.Bookmarks) != 3 {
		t.Fatalf("saved: %+v %v", saved, err)
	}
}

func TestOpenFileNameLayout(t *testing.T) {
	// sizeof(OPENFILENAMEW) on x64 with the Windows 2000+ tail fields
	if got := unsafe.Sizeof(openFileName{}); got != 152 {
		t.Fatalf("OPENFILENAMEW size = %d, want 152", got)
	}
}
