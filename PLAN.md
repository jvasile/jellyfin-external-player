# Embyfin Kiosk - External Player Launcher

## Goal

Play videos from Emby/Jellyfin web UI using a local external player (mpv or VLC) via SMB, bypassing server-side transcoding and subtitle inlining.

**Supports both Emby and Jellyfin** - the browser extension detects which platform is running and uses the appropriate API.

## Architecture

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────┐
│  Browser        │     │  Local Server   │     │  mpv/VLC    │
│  (Emby/Jellyfin)│────▶│  (localhost)    │────▶│  (Windows)  │
│  + Extension    │     │                 │     │             │
└─────────────────┘     └─────────────────┘     └─────────────┘
        │                       │
        │ 1. Intercept click    │ 3. Translate path
        │ 2. Get item ID        │ 4. Launch player --fs
        ▼                       │
┌─────────────────┐             │
│  Emby/Jellyfin  │◀────────────┘
│  API            │  Fetch file path
└─────────────────┘
```

## Components

### 1. Browser Extension (Chrome/Firefox)

**Purpose:** Intercept play button clicks, fetch file path from API, send to local server.

**Location:** `extension/`

**Files:**
- `manifest.json` - Extension manifest (Manifest V3)
- `content.js` - Injected into Emby/Jellyfin pages, intercepts play clicks
- `background.js` - Service worker, handles requests to local server
- `popup.html/js` - Extension popup with server URL config

**Functionality:**
- Detect clicks on play buttons in Emby/Jellyfin web UI
- Extract item ID from the page URL or DOM
- Call Emby/Jellyfin API to get the file's disk path: `GET /Items/{id}`
- Send the path to local server: `GET http://localhost:9999/api/play?path=...`
- Prevent default playback behavior
- Keyboard shortcut: Press `K` on movie/episode page to play

**Extension Config (stored in browser):**
- Server URL (default: `http://localhost:9999`)

### 2. Local HTTP Server (Go)

**Purpose:** Receive play requests, translate paths, and launch player on Windows.

**Location:** `main.go`

**Endpoints:**
- `GET /api/play?path=<path>` - Translate path and launch player
- `GET /api/config` - Return current config as JSON
- `GET /config` - Web UI for configuration

**Functionality:**
- Listen on localhost:9999 (configurable)
- Translate paths using configurable mappings (prefix, wildcard, or regex)
- Launch selected player (mpv or VLC) with fullscreen flag
- Return success/error response

### 3. Configuration

**File:** `config.json` (same directory as executable)

```json
{
  "port": 9999,
  "player": "mpv",
  "players": {
    "mpv": {"name": "mpv", "path": "mpv", "args": ["--fs"]},
    "vlc": {"name": "VLC", "path": "vlc", "args": ["--fullscreen"]}
  },
  "path_mappings": [
    {
      "type": "prefix",
      "match": "/mnt/jbod/007/media/Movies",
      "replace": "\\\\172.16.50.28\\Movies"
    },
    {
      "type": "prefix",
      "match": "/mnt/jbod/007/media/TV",
      "replace": "\\\\172.16.50.28\\TV"
    }
  ]
}
```

## Path Mapping Types

| Type | Match Pattern | Replace Pattern | Description |
|------|---------------|-----------------|-------------|
| `prefix` | `/mnt/media/Movies` | `\\server\Movies` | Simple string prefix replacement |
| `wildcard` | `/mnt/*/media/*` | `\\{1}\{2}` | `*` = segment, `**` = any path, `{1}` = backreference |
| `regex` | `^/mnt/(.*?)/media/(.*)` | `\\$1\$2` | Full regex with `$1`, `$2` backreferences |

First matching rule wins.

## Security Considerations

- Server binds to localhost only (127.0.0.1)
- Player selection limited to configured players (no arbitrary execution)
- Path translation happens server-side

## Subtitle Handling

mpv/VLC auto-load subtitles:
- Embedded in MKV: automatically available
- Sidecar .srt files: auto-detected if same name as video

## Building

**Server:**
```bash
# Development (Linux)
go run .

# Build for Windows
GOOS=windows GOARCH=amd64 go build -o embyfin-kiosk.exe .
```

**Extension:**
1. Open Chrome → `chrome://extensions/`
2. Enable "Developer mode"
3. Click "Load unpacked" → select `extension/` folder

## Running

1. Copy `embyfin-kiosk.exe` to Windows
2. Run the executable (or add to startup)
3. Open `http://localhost:9999/config` to configure path mappings
4. Install the browser extension
5. Configure server URL in extension popup
6. Navigate to Emby/Jellyfin and click play (or press `K`)

## Files

```
embyfin-kiosk/
├── main.go              # Go server
├── go.mod               # Go module
├── config.json          # Server config (created on first run)
├── embyfin-kiosk.exe    # Windows binary
├── PLAN.md              # This file
└── extension/
    ├── manifest.json    # Extension manifest
    ├── content.js       # Content script (injected into pages)
    ├── background.js    # Service worker
    ├── popup.html       # Extension popup
    ├── popup.js         # Popup logic
    ├── icon48.png       # Extension icon
    └── icon128.png      # Extension icon
```

## Future Enhancements

- Track playback position and report back to Emby/Jellyfin
- Support for play queue (multiple episodes)
- Pause/resume control via local server API
- System tray icon for Windows
- Firefox Add-on store submission
