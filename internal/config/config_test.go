package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

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
