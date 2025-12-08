# jellyfin-external-player

Intercepts Jellyfin video playback and launches mpv instead of using the web player.

## Features

- Plays media files directly in mpv
- Resume support - continues from where you left off
- Progress reporting back to Jellyfin
- Path mapping for NFS/SMB shares
- Playlist support for seasons/series
- Auto-focuses mpv window on Windows

## Requirements

- Go 1.21+
- mpv
- A userscript manager (Tampermonkey, Violentmonkey, etc.)

## Building

```bash
make
```

## Installation

### From source

```bash
sudo make install
```

This installs the binary and man page to `/usr/local`.

### Debian package

```bash
# Signed with default GPG key
make deb

# Unsigned (for users without GPG key)
make deb DEB_SIGN=no

# Signed with specific key
make deb DEB_SIGN=YOURKEYID

sudo dpkg -i ../jellyfin-external-player_*.deb
```

### Userscript

1. Start the server: `jellyfin-external-player`
2. Open http://localhost:9998/install
3. Click "Install Userscript"

### Systemd (optional)

To start automatically when you log in to a graphical session:

```bash
# If installed from source:
make install-service

# If installed from .deb:
systemctl --user enable jellyfin-external-player

# Then start it:
systemctl --user start jellyfin-external-player
```

## Configuration

Open http://localhost:9998/config to configure:

- **Path mappings** - Transform server paths to local paths (e.g., NFS to SMB)
- **Debug logging** - Enable verbose output

Config is stored in:
- Linux: `~/.config/jellyfin-external-player/config.json`
- Windows: `%APPDATA%\jellyfin-external-player\config.json`

### Path Mapping Example

If Jellyfin sees files at `nfs://192.168.1.10/media/Movies/...` but your Windows machine accesses them via `\\192.168.1.10\Movies\...`:

| Type   | Match                              | Replace              |
|--------|------------------------------------|-----------------------|
| prefix | `nfs://192.168.1.10/media/Movies`  | `\\192.168.1.10\Movies` |

## How It Works

1. The server runs on localhost:9998
2. A userscript injects JavaScript into Jellyfin pages
3. When you click play, the JS intercepts the request and calls the local server
4. The server launches mpv with the translated file path
5. Playback position is reported back to Jellyfin when the player closes

## Documentation

See the man page for detailed usage:

```bash
man jellyfin-external-player
```

## License

GPLv3 - see [LICENSE](LICENSE)
