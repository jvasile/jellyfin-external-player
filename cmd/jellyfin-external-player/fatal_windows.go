//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	user32         = syscall.NewLazyDLL("user32.dll")
	procMessageBox = user32.NewProc("MessageBoxW")
)

const (
	MB_OK          = 0x00000000
	MB_ICONERROR   = 0x00000010
	MB_SYSTEMMODAL = 0x00001000
)

// showFatalError displays a Windows message box with the error
func showFatalError(msg string) {
	title := "JF External Player - Error"

	// Convert to UTF-16
	titlePtr, _ := syscall.UTF16PtrFromString(title)
	msgPtr, _ := syscall.UTF16PtrFromString(msg)

	procMessageBox.Call(
		0,
		uintptr(unsafe.Pointer(msgPtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		uintptr(MB_OK|MB_ICONERROR|MB_SYSTEMMODAL),
	)
}
