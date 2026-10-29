package app

import (
	"testing"
	"unsafe"
)

func TestParseOpenArg(t *testing.T) {
	if got := parseOpenArg([]string{"--open", "abc"}); got != "abc" {
		t.Fatalf("separate flag: %q", got)
	}
	if got := parseOpenArg([]string{"--open=abc"}); got != "abc" {
		t.Fatalf("joined flag: %q", got)
	}
	if got := parseOpenArg([]string{"--other", "abc"}); got != "" {
		t.Fatalf("unrelated: %q", got)
	}
	if !validOpenID("ab") || validOpenID("") || validOpenID("../x") || validOpenID("ab cd") {
		t.Fatal("id check")
	}
}

func TestBookmarkCommandMapping(t *testing.T) {
	a := &App{bookmarkCmds: []string{"aa", "bb"}}
	if id, ok := a.bookmarkForCommand(scBookmarkBase); !ok || id != "aa" {
		t.Fatalf("first: %q %v", id, ok)
	}
	// Windows ORs the low 4 bits into WM_SYSCOMMAND.
	if id, ok := a.bookmarkForCommand(scBookmarkBase + 16 + 3); !ok || id != "bb" {
		t.Fatalf("second: %q %v", id, ok)
	}
	if _, ok := a.bookmarkForCommand(scClose); ok {
		t.Fatal("system command must not open a bookmark")
	}
}

func TestCopyDataLayout(t *testing.T) {
	if unsafe.Sizeof(copyDataStruct{}) != 24 {
		t.Fatalf("COPYDATASTRUCT size %d", unsafe.Sizeof(copyDataStruct{}))
	}
	var cd copyDataStruct
	if unsafe.Offsetof(cd.Ptr) != 16 {
		t.Fatalf("lpData offset %d", unsafe.Offsetof(cd.Ptr))
	}
}
