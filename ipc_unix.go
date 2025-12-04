//go:build !windows

package main

import (
	"net"
	"time"
)

// Connect to mpv IPC via Unix socket on Linux/macOS
func connectMpvIPC(pipePath string) (net.Conn, error) {
	return net.DialTimeout("unix", pipePath, 500*time.Millisecond)
}

// getMpvIPCPath returns the IPC socket path for mpv on Unix systems
func getMpvIPCPath() string {
	return "/tmp/jf-external-player-mpv.sock"
}
