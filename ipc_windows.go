//go:build windows

package main

import (
	"net"

	"github.com/Microsoft/go-winio"
)

// Connect to mpv IPC via named pipe on Windows
func connectMpvIPC(pipePath string) (net.Conn, error) {
	return winio.DialPipe(pipePath, nil)
}

// getMpvIPCPath returns the IPC path for mpv on Windows
func getMpvIPCPath() string {
	return `\\.\pipe\jf-external-player-mpv`
}
