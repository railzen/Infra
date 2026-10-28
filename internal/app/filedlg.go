package app

import (
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	comdlg32          = windows.NewLazySystemDLL("comdlg32.dll")
	pGetSaveFileNameW = comdlg32.NewProc("GetSaveFileNameW")
	pGetOpenFileNameW = comdlg32.NewProc("GetOpenFileNameW")
)

type openFileName struct {
	StructSize    uint32
	Owner         uintptr
	Instance      uintptr
	Filter        *uint16
	CustomFilter  *uint16
	MaxCustFilter uint32
	FilterIndex   uint32
	File          *uint16
	MaxFile       uint32
	FileTitle     *uint16
	MaxFileTitle  uint32
	InitialDir    *uint16
	Title         *uint16
	Flags         uint32
	FileOffset    uint16
	FileExtension uint16
	DefExt        *uint16
	CustData      uintptr
	Hook          uintptr
	TemplateName  *uint16
	PvReserved    uintptr
	DwReserved    uint32
	FlagsEx       uint32
}

const (
	ofnOverwritePrompt = 0x00000002
	ofnNoChangeDir     = 0x00000008
	ofnPathMustExist   = 0x00000800
	ofnFileMustExist   = 0x00001000
	ofnExplorer        = 0x00080000
)

// jsonFilter is "JSON 文件 (*.json)\0*.json\0所有文件\0*.*\0\0" as UTF-16.
func jsonFilter() *uint16 {
	var b []uint16
	for _, s := range []string{"JSON 文件 (*.json)", "*.json", "所有文件", "*.*"} {
		b = append(b, utf16.Encode([]rune(s))...)
		b = append(b, 0)
	}
	b = append(b, 0)
	return &b[0]
}

// fileDialog shows the common Save or Open dialog; it returns "" on cancel.
func fileDialog(owner uintptr, save bool, title, defaultName string) string {
	buf := make([]uint16, 1024)
	copy(buf, utf16.Encode([]rune(defaultName)))
	ofn := openFileName{
		Owner:       owner,
		Filter:      jsonFilter(),
		FilterIndex: 1,
		File:        &buf[0],
		MaxFile:     uint32(len(buf)),
		Title:       u16(title),
		DefExt:      u16("json"),
		Flags:       ofnExplorer | ofnNoChangeDir | ofnPathMustExist,
	}
	ofn.StructSize = uint32(unsafe.Sizeof(ofn))
	proc := pGetOpenFileNameW
	if save {
		proc = pGetSaveFileNameW
		ofn.Flags |= ofnOverwritePrompt
	} else {
		ofn.Flags |= ofnFileMustExist
	}
	if r, _, _ := proc.Call(ptr(&ofn)); r == 0 {
		return ""
	}
	return windows.UTF16ToString(buf)
}
