package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestExportRoundTrip(t *testing.T) {
	c := Default()
	c.DefaultPage = "a"
	c.Window = &WindowState{X: 1, Y: 2, W: 3, H: 4}
	c.Bookmarks = []Bookmark{{ID: "a", Name: "OA", URL: "https://oa.corp",
		Records: []DNSRecord{{Host: "oa.corp", Type: "CNAME", Value: "lb.corp"}}, Window: &WindowState{W: 9}}}
	c.CAs = []CA{{ID: "x", Name: "Corp Root", PEM: "-----BEGIN CERTIFICATE-----\n..."}}
	data, err := c.Export()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(`"X"`)) || bytes.Contains(data, []byte(`"window"`)) {
		t.Fatal("window placement must not be exported")
	}
	e, err := ParseExport(append([]byte("\xEF\xBB\xBF"), data...))
	if err != nil {
		t.Fatal(err)
	}
	if e.Settings.DefaultPage != "a" || len(e.Bookmarks) != 1 || e.Bookmarks[0].Records[0].Value != "lb.corp" ||
		len(e.CAs) != 1 || e.CAs[0].Name != "Corp Root" {
		t.Fatalf("round trip: %+v", e)
	}
	if _, err := ParseExport([]byte(`{"bookmarks":[]}`)); err == nil {
		t.Fatal("foreign JSON must be rejected")
	}
}

func TestStoreRoundTrip(t *testing.T) {
	// the folder does not exist yet: Save must create it
	path := filepath.Join(t.TempDir(), AppDirName, "config.json")
	s := NewStore(path)
	c, err := s.Load()
	if !errors.Is(err, os.ErrNotExist) || c.DefaultPage != "home" {
		t.Fatalf("missing file: %+v %v", c, err)
	}
	c.Bookmarks = append(c.Bookmarks, Bookmark{ID: "a", Name: "OA", URL: "https://oa.corp",
		Records: []DNSRecord{{Host: "oa.corp", Type: "A", Value: "10.0.0.1"}}})
	for i := 0; i < 3; i++ {
		if err := s.Save(c); err != nil {
			t.Fatal(err)
		}
	}
	c2, err := NewStore(path).Load()
	if err != nil || len(c2.Bookmarks) != 1 || c2.Bookmarks[0].Records[0].Value != "10.0.0.1" {
		t.Fatalf("reload: %+v %v", c2, err)
	}
	// no temporary files left behind
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("expected only config.json, got %d entries", len(entries))
	}
}

func TestCorruptConfigIsKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte("{not json"), 0o644)
	s := NewStore(path)
	_, err := s.Load()
	var ce *CorruptError
	if !errors.As(err, &ce) {
		t.Fatalf("want CorruptError, got %v", err)
	}
	backup, err := s.Quarantine()
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(backup); string(b) != "{not json" {
		t.Fatal("backup must keep the original bytes")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("corrupt file should have been moved away")
	}
}
