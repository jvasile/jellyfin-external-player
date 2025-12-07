//go:build !windows

package main

// showFatalError does nothing on Unix (errors go to stderr which is visible)
func showFatalError(msg string) {
	// No-op on Linux/Unix - terminal shows errors
}
