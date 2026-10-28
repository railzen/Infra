// Package config keeps Intra's settings appended to the end of its own .exe,
// so copying the executable also copies bookmarks, CAs and preferences.
//
// Layout of the executable on disk:
//
//	[original PE image][gzip(JSON)][8 byte little-endian payload length][8 byte magic]
//
// Windows ignores trailing data after the PE image, so the program still runs.
package config

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const magic = "INTRACF1"

type DNSRecord struct {
	Host  string `json:"host"`
	Type  string `json:"type"` // "A" or "CNAME"
	Value string `json:"value"`
}

type Bookmark struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	URL     string      `json:"url"`
	Records []DNSRecord `json:"records"`
	// Window is where this link's window was last closed (machine specific, not exported).
	Window *WindowState `json:"window,omitempty"`
}

type CA struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	PEM         string `json:"pem"`
	Fingerprint string `json:"fingerprint"`
	NotAfter    string `json:"notAfter"`
	AddedAt     string `json:"addedAt"`
}

type WindowState struct {
	X, Y, W, H int
	Maximized  bool
}

type Config struct {
	Bookmarks []Bookmark `json:"bookmarks"`
	// DefaultPage: "home" (favorites overview), "last" (last opened bookmark) or a bookmark ID.
	DefaultPage    string       `json:"defaultPage"`
	RememberWindow bool         `json:"rememberWindow"`
	Window         *WindowState `json:"window,omitempty"`
	LastBookmark   string       `json:"lastBookmark,omitempty"`
	CAs            []CA         `json:"cas"`
}

func Default() *Config {
	return &Config{DefaultPage: "home", RememberWindow: true}
}

func (c *Config) Clone() *Config {
	b, _ := json.Marshal(c)
	out := &Config{}
	json.Unmarshal(b, out)
	return out
}

func (c *Config) Bookmark(id string) *Bookmark {
	for i := range c.Bookmarks {
		if c.Bookmarks[i].ID == id {
			return &c.Bookmarks[i]
		}
	}
	return nil
}

// Store reads and writes the configuration trailer of one executable file.
type Store struct {
	mu   sync.Mutex
	path string
}

func NewStore(exePath string) *Store {
	s := &Store{path: exePath}
	// left over from a previous save while that process was running
	os.Remove(s.OldPath())
	return s
}

func (s *Store) Path() string { return s.path }

// OldPath is where Save moves the running executable when it cannot be overwritten.
func (s *Store) OldPath() string { return s.path + ".old" }

// split returns the PE image and the payload (nil if none).
func split(data []byte) (image, payload []byte) {
	if len(data) < 16 || string(data[len(data)-8:]) != magic {
		return data, nil
	}
	n := binary.LittleEndian.Uint64(data[len(data)-16 : len(data)-8])
	if n > uint64(len(data)-16) {
		return data, nil
	}
	end := len(data) - 16
	return data[:end-int(n)], data[end-int(n) : end]
}

func (s *Store) Load() (*Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		return Default(), err
	}
	_, payload := split(data)
	if payload == nil {
		return Default(), nil
	}
	zr, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return Default(), err
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		return Default(), err
	}
	c := Default()
	if err := json.Unmarshal(raw, c); err != nil {
		return Default(), err
	}
	return c, nil
}

// Save rewrites the executable with a new trailer. A running .exe cannot be
// overwritten on Windows but it can be renamed, so when the target is locked
// the current file is moved aside to "<exe>.old" (removed on next start).
func (s *Store) Save(c *Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	image, _ := split(data)

	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	buf.Grow(len(image) + len(raw) + 64)
	buf.Write(image)
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	start := buf.Len()
	zw.Write(raw)
	zw.Close()
	var tail [16]byte
	binary.LittleEndian.PutUint64(tail[:8], uint64(buf.Len()-start))
	copy(tail[8:], magic)
	buf.Write(tail[:])

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o755); err != nil {
		return fmt.Errorf("无法写入 %s: %w", filepath.Dir(s.path), err)
	}
	if err := os.Rename(tmp, s.path); err == nil {
		return nil
	}
	old := s.OldPath()
	os.Remove(old)
	if err := os.Rename(s.path, old); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("无法替换程序文件: %w", err)
	}
	hideFile(old)
	if err := os.Rename(tmp, s.path); err != nil {
		// put the original back so the user is never left without an exe
		os.Rename(old, s.path)
		os.Remove(tmp)
		return errors.Join(errors.New("无法写入配置"), err)
	}
	return nil
}
