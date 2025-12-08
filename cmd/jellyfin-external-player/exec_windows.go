//go:build windows

package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procGetCurrentThreadId       = kernel32.NewProc("GetCurrentThreadId")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procShowWindow               = user32.NewProc("ShowWindow")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procGetClassNameW            = user32.NewProc("GetClassNameW")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procAttachThreadInput        = user32.NewProc("AttachThreadInput")
	procBringWindowToTop         = user32.NewProc("BringWindowToTop")
	procSetFocus                 = user32.NewProc("SetFocus")
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

// focusProcessWindow finds a window belonging to the given PID and brings it to foreground
// Returns true if window was found and focused
func focusProcessWindow(pid int) bool {
	var targetHwnd uintptr
	targetPid := uint32(pid)

	// Callback for EnumWindows - try by PID first, then by class name
	callback := syscall.NewCallback(func(hwnd, lParam uintptr) uintptr {
		// Check if window is visible
		visible, _, _ := procIsWindowVisible.Call(hwnd)
		if visible == 0 {
			return 1 // Continue
		}

		// Try matching by PID
		var windowPid uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&windowPid)))
		if windowPid == targetPid {
			targetHwnd = hwnd
			return 0 // Stop enumeration
		}

		// Also try matching by class name "mpv"
		className := make([]uint16, 256)
		procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&className[0])), 256)
		if syscall.UTF16ToString(className) == "mpv" {
			targetHwnd = hwnd
			log.Printf("Found mpv window by class name (hwnd=%x, pid=%d)", hwnd, windowPid)
			return 0 // Stop enumeration
		}

		return 1 // Continue enumeration
	})

	procEnumWindows.Call(callback, 0)

	if targetHwnd != 0 {
		// Get current foreground window's thread
		fgHwnd, _, _ := procGetForegroundWindow.Call()
		fgThread, _, _ := procGetWindowThreadProcessId.Call(fgHwnd, 0)
		ourThread, _, _ := procGetCurrentThreadId.Call()

		// Attach to foreground thread to get permission to steal focus
		if fgThread != ourThread {
			procAttachThreadInput.Call(ourThread, fgThread, 1) // Attach
		}

		// SW_SHOW = 5
		procShowWindow.Call(targetHwnd, 5)
		procBringWindowToTop.Call(targetHwnd)
		procSetForegroundWindow.Call(targetHwnd)
		procSetFocus.Call(targetHwnd)

		// Detach from foreground thread
		if fgThread != ourThread {
			procAttachThreadInput.Call(ourThread, fgThread, 0) // Detach
		}

		log.Printf("Focused mpv window (hwnd=%x) using AttachThreadInput", targetHwnd)
		return true
	}
	log.Printf("Could not find window for pid %d or class 'mpv'", pid)
	return false
}
