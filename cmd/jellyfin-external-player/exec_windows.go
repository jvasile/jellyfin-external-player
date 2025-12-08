//go:build windows

package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

var (
	mpvPathCache   string
	mpvPathChecked bool
)

// fixPlayerPath ensures we use .exe on Windows to avoid console launchers
// and finds the actual player executable
func fixPlayerPath(path string) string {
	if path == "mpv" || path == "mpv.exe" {
		if !mpvPathChecked {
			mpvPathChecked = true
			mpvPathCache = findMpv()
			if mpvPathCache != "" {
				log.Printf("Found mpv at: %s", mpvPathCache)
			} else {
				log.Printf("Warning: mpv not found in common locations. Install via scoop (scoop install mpv) or set full path in config.")
			}
		}
		if mpvPathCache != "" {
			return mpvPathCache
		}
		return "mpv.exe"
	}
	return path
}

// findMpv looks for mpv.exe in common installation locations
func findMpv() string {
	// First try PATH (but skip WindowsApps to avoid Store console launcher)
	if path, err := exec.LookPath("mpv.exe"); err == nil {
		if !strings.Contains(strings.ToLower(path), "windowsapps") {
			return path
		}
	}

	// Check Scoop installation
	if home := os.Getenv("USERPROFILE"); home != "" {
		scoopPath := filepath.Join(home, "scoop", "apps", "mpv", "current", "mpv.exe")
		if _, err := os.Stat(scoopPath); err == nil {
			return scoopPath
		}
	}

	// Check Chocolatey
	if choco := os.Getenv("ChocolateyInstall"); choco != "" {
		chocoPath := filepath.Join(choco, "bin", "mpv.exe")
		if _, err := os.Stat(chocoPath); err == nil {
			return chocoPath
		}
	}

	// Check Program Files
	for _, pf := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		if pf != "" {
			pfPath := filepath.Join(pf, "mpv", "mpv.exe")
			if _, err := os.Stat(pfPath); err == nil {
				return pfPath
			}
		}
	}

	return ""
}

// logToStderr returns false on Windows GUI apps (no console)
func logToStderr() bool {
	return false
}
