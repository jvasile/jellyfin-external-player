//go:build !windows

package main

import "os/exec"

// hideWindow is a no-op on Unix
func hideWindow(cmd *exec.Cmd) {
	// Nothing to do on Unix
}

// noConsole is a no-op on Unix
func noConsole(cmd *exec.Cmd) {
	// Nothing to do on Unix
}

// Default player path for Unix
const defaultMpvPath = "mpv"

// fixPlayerPath is a no-op on Unix
func fixPlayerPath(path string) string {
	return path
}

// logToStderr returns true on Unix (terminal available)
func logToStderr() bool {
	return true
}

// focusProcessWindow is a no-op on Unix
func focusProcessWindow(pid int) bool {
	// Nothing to do on Unix - window managers handle focus
	return true
}

// findMpv looks for mpv on Unix (just checks PATH)
func findMpv() string {
	if path, err := exec.LookPath("mpv"); err == nil {
		return path
	}
	return ""
}
