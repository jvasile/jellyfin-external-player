//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// hideWindow sets Windows-specific flags to completely hide a subprocess (for background server)
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}

// noConsole prevents console window but allows GUI windows to show (for mpv)
func noConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}

// Default player path for Windows (use .exe to avoid console launcher)
const defaultMpvPath = "mpv.exe"

// fixPlayerPath ensures we use .exe on Windows to avoid console launchers
func fixPlayerPath(path string) string {
	// If it's just "mpv" or "vlc", add .exe to avoid console launcher
	if path == "mpv" {
		return "mpv.exe"
	}
	return path
}

// logToStderr returns false on Windows GUI apps (no console)
func logToStderr() bool {
	return false
}
