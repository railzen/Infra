package config

import (
	"bytes"
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

func TestTrailerRoundTrip(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "Intra.exe")
	image := bytes.Repeat([]byte("MZ-image-"), 1000)
	os.WriteFile(exe, image, 0o755)

	s := NewStore(exe)
	c, err := s.Load()
	if err != nil || c.DefaultPage != "home" {
		t.Fatalf("default: %+v %v", c, err)
	}
	c.Bookmarks = append(c.Bookmarks, Bookmark{ID: "a", Name: "OA", URL: "https://oa.corp",
		Records: []DNSRecord{{Host: "oa.corp", Type: "A", Value: "10.0.0.1"}}})
	for i := 0; i < 3; i++ { // repeated saves must not grow the image
		if err := s.Save(c); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(exe)
	img, payload := split(data)
	if !bytes.Equal(img, image) || payload == nil {
		t.Fatalf("image changed or payload missing")
	}
	c2, err := NewStore(exe).Load()
	if err != nil || len(c2.Bookmarks) != 1 || c2.Bookmarks[0].Records[0].Value != "10.0.0.1" {
		t.Fatalf("reload: %+v %v", c2, err)
	}
}
