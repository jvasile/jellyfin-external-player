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

// Default player paths for Unix
const (
	defaultMpvPath = "mpv"
	defaultVlcPath = "vlc"
)

// fixPlayerPath is a no-op on Unix
func fixPlayerPath(path string) string {
	return path
}

// logToStderr returns true on Unix (terminal available)
func logToStderr() bool {
	return true
}
