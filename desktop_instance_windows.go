package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"unsafe"

	winapi "golang.org/x/sys/windows"
)

// Only one launcher may own a data directory; an auto-started instance can be
// restored by opening the EXE again instead of starting a competing server.
func desktopInstance(directory string) (func(), bool, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, false, err
	}
	digest := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(absolute))))
	name, err := winapi.UTF16PtrFromString(`Local\VelinWebSSH_` + hex.EncodeToString(digest[:16]))
	if err != nil {
		return nil, false, err
	}
	handle, err := winapi.CreateMutex(nil, false, name)
	if errors.Is(err, winapi.ERROR_ALREADY_EXISTS) {
		_ = winapi.CloseHandle(handle)
		user32 := winapi.NewLazySystemDLL("user32.dll")
		title, _ := winapi.UTF16PtrFromString("VelinWebSSH")
		window, _, _ := user32.NewProc("FindWindowW").Call(0, uintptr(unsafe.Pointer(title)))
		if window != 0 {
			_, _, _ = user32.NewProc("ShowWindow").Call(window, 9)
			_, _, _ = user32.NewProc("SetForegroundWindow").Call(window)
		}
		return func() {}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return func() { _ = winapi.CloseHandle(handle) }, true, nil
}
