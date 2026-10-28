package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// Saving rewrites Intra.exe, which Windows only allows by first renaming the
// running image to Intra.exe.old. The running process cannot delete its own
// image, so on exit it starts the new Intra.exe in this helper mode: it waits
// for the old process to end, deletes the .old file and quits without a window.

const cleanupFlag = "--cleanup-old"

// runCleanupHelper handles "Intra.exe --cleanup-old <pid> <path>". It reports
// whether the process was started in helper mode.
func runCleanupHelper(args []string) bool {
	if len(args) < 4 || args[1] != cleanupFlag {
		return false
	}
	pid, err := strconv.ParseUint(args[2], 10, 32)
	if err != nil {
		return true
	}
	exe, err := os.Executable()
	if err != nil {
		return true
	}
	// only ever delete this executable's own .old file
	target := filepath.Clean(args[3])
	if !strings.EqualFold(target, filepath.Clean(exe)+".old") {
		return true
	}
	if h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid)); err == nil {
		windows.WaitForSingleObject(h, 30_000)
		windows.CloseHandle(h)
	}
	for i := 0; i < 50; i++ {
		err := os.Remove(target)
		if err == nil || os.IsNotExist(err) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return true
}

// spawnCleanup starts the helper if this session left an .old file behind.
func (a *App) spawnCleanup() {
	old := a.store.OldPath()
	if _, err := os.Stat(old); err != nil {
		return
	}
	if _, err := os.Stat(a.exe); err != nil {
		return
	}
	cmd := exec.Command(a.exe, cleanupFlag, strconv.Itoa(os.Getpid()), old)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if cmd.Start() == nil {
		cmd.Process.Release()
	}
}
