package main

import (
	"archive/zip"
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

//go:embed extension/*
var extensionFS embed.FS

type PathMapping struct {
	Type    string `json:"type"`    // "prefix", "wildcard", or "regex"
	Match   string `json:"match"`   // pattern to match
	Replace string `json:"replace"` // replacement string
}

type PlayerConfig struct {
	Name string   `json:"name"`
	Path string   `json:"path"`
	Args []string `json:"args"`
}

type Config struct {
	Port          int                     `json:"port"`
	Player        string                  `json:"player"` // "mpv" or "vlc"
	Players       map[string]PlayerConfig `json:"players"`
	PathMappings  []PathMapping           `json:"path_mappings"`
	ServerURLs    []string                `json:"server_urls"`     // Emby/Jellyfin server URLs
	ServerURLsSet bool                    `json:"server_urls_set"` // true if user has explicitly set URLs
}

var (
	config     Config
	configPath string
	configMu   sync.RWMutex
)

func defaultConfig() Config {
	return Config{
		Port:   9999,
		Player: "mpv",
		Players: map[string]PlayerConfig{
			"mpv": {Name: "mpv", Path: "mpv", Args: []string{"--fs"}},
			"vlc": {Name: "VLC", Path: "vlc", Args: []string{"--fullscreen"}},
		},
		PathMappings: []PathMapping{
			{Type: "prefix", Match: "/mnt/jbod/007/media/Movies", Replace: `\\172.16.50.28\Movies`},
			{Type: "prefix", Match: "/mnt/jbod/007/media/TV", Replace: `\\172.16.50.28\TV`},
		},
		ServerURLs:    []string{}, // Will be populated by discovery
		ServerURLsSet: false,
	}
}

func loadConfig() error {
	configMu.Lock()
	defer configMu.Unlock()

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			config = defaultConfig()
			return saveConfigLocked()
		}
		return err
	}

	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}

	// Ensure players map exists
	if config.Players == nil {
		config.Players = defaultConfig().Players
	}

	return nil
}

func saveConfigLocked() error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0644)
}

func saveConfig() error {
	configMu.Lock()
	defer configMu.Unlock()
	return saveConfigLocked()
}

// wildcardToRegex converts a wildcard pattern to a regex
// * matches anything except /
// ** matches anything including /
func wildcardToRegex(pattern string) (*regexp.Regexp, error) {
	var result strings.Builder
	result.WriteString("^")

	i := 0
	for i < len(pattern) {
		if i+1 < len(pattern) && pattern[i] == '*' && pattern[i+1] == '*' {
			result.WriteString("(.*)")
			i += 2
		} else if pattern[i] == '*' {
			result.WriteString("([^/]*)")
			i++
		} else if strings.ContainsRune("[](){}+?.\\^$|", rune(pattern[i])) {
			result.WriteString("\\")
			result.WriteByte(pattern[i])
			i++
		} else {
			result.WriteByte(pattern[i])
			i++
		}
	}

	result.WriteString("$")
	return regexp.Compile(result.String())
}

// applyMapping applies a single mapping to a path
// Returns the transformed path and true if matched, or original path and false if not
func applyMapping(path string, mapping PathMapping) (string, bool) {
	switch mapping.Type {
	case "prefix":
		if strings.HasPrefix(path, mapping.Match) {
			return mapping.Replace + path[len(mapping.Match):], true
		}
		return path, false

	case "wildcard":
		re, err := wildcardToRegex(mapping.Match)
		if err != nil {
			log.Printf("Invalid wildcard pattern %q: %v", mapping.Match, err)
			return path, false
		}
		if re.MatchString(path) {
			// Replace {1}, {2}, etc. with captured groups
			matches := re.FindStringSubmatch(path)
			result := mapping.Replace
			for i := 1; i < len(matches); i++ {
				result = strings.ReplaceAll(result, fmt.Sprintf("{%d}", i), matches[i])
			}
			return result, true
		}
		return path, false

	case "regex":
		re, err := regexp.Compile(mapping.Match)
		if err != nil {
			log.Printf("Invalid regex pattern %q: %v", mapping.Match, err)
			return path, false
		}
		if re.MatchString(path) {
			return re.ReplaceAllString(path, mapping.Replace), true
		}
		return path, false

	default:
		// Treat unknown types as prefix for backwards compatibility
		if strings.HasPrefix(path, mapping.Match) {
			return mapping.Replace + path[len(mapping.Match):], true
		}
		return path, false
	}
}

func translatePath(path string) string {
	configMu.RLock()
	defer configMu.RUnlock()

	for _, mapping := range config.PathMappings {
		if result, matched := applyMapping(path, mapping); matched {
			// Convert forward slashes to backslashes for Windows
			return strings.ReplaceAll(result, "/", `\`)
		}
	}

	// No match - just convert slashes
	return strings.ReplaceAll(path, "/", `\`)
}

func playHandler(w http.ResponseWriter, r *http.Request) {
	// Set CORS headers for browser requests
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "missing 'path' parameter", http.StatusBadRequest)
		return
	}

	translatedPath := translatePath(path)
	log.Printf("Playing: %s -> %s", path, translatedPath)

	configMu.RLock()
	playerKey := config.Player
	playerConfig, ok := config.Players[playerKey]
	configMu.RUnlock()

	if !ok {
		log.Printf("Unknown player %q, falling back to mpv", playerKey)
		playerConfig = PlayerConfig{Path: "mpv", Args: []string{"--fs"}}
	}

	args := append([]string{}, playerConfig.Args...)
	args = append(args, translatedPath)

	cmd := exec.Command(playerConfig.Path, args...)
	if err := cmd.Start(); err != nil {
		log.Printf("Error starting player: %v", err)
		http.Error(w, fmt.Sprintf("failed to start player: %v", err), http.StatusInternalServerError)
		return
	}

	// Don't wait for the player to finish
	go cmd.Wait()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "playing",
		"path":   translatedPath,
	})
}

func configPageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		configMu.RLock()
		configJSON, _ := json.MarshalIndent(config, "", "  ")
		configMu.RUnlock()

		html := `<!DOCTYPE html>
<html>
<head>
    <title>Embyfin Kiosk Config</title>
    <style>
        body { font-family: system-ui, sans-serif; max-width: 900px; margin: 50px auto; padding: 20px; }
        h1 { margin-bottom: 30px; }
        h2 { margin-top: 30px; margin-bottom: 15px; font-size: 18px; }
        label { display: block; margin-bottom: 5px; font-weight: 500; }
        select, input[type="text"] {
            padding: 8px;
            font-size: 14px;
            border: 1px solid #ccc;
            border-radius: 4px;
            margin-bottom: 15px;
        }
        select { min-width: 150px; }
        input[type="text"] { width: 100%; box-sizing: border-box; }
        textarea {
            width: 100%;
            height: 300px;
            font-family: monospace;
            font-size: 13px;
            padding: 10px;
            border: 1px solid #ccc;
            border-radius: 4px;
            box-sizing: border-box;
        }
        button {
            padding: 10px 20px;
            font-size: 16px;
            margin-top: 10px;
            cursor: pointer;
            background: #3b82f6;
            color: white;
            border: none;
            border-radius: 4px;
        }
        button:hover { background: #2563eb; }
        .success { color: green; margin-left: 10px; }
        .error { color: red; }
        .section {
            background: #f9fafb;
            padding: 20px;
            border-radius: 8px;
            margin-bottom: 20px;
        }
        .help { color: #666; font-size: 13px; margin-top: 5px; }
        .mapping-help {
            background: #f0f9ff;
            padding: 15px;
            border-radius: 4px;
            margin-bottom: 15px;
            font-size: 13px;
        }
        .mapping-help code {
            background: #e0f2fe;
            padding: 2px 6px;
            border-radius: 3px;
        }
    </style>
</head>
<body>
    <h1>Embyfin Kiosk Configuration</h1>

    <form method="POST">
        <div class="section">
            <h2>Player</h2>
            <label for="player">Default Player</label>
            <select name="player" id="player">
                <option value="mpv">mpv</option>
                <option value="vlc">VLC</option>
            </select>
            <p class="help">Player paths and arguments can be customized in the JSON below.</p>
        </div>

        <div class="section">
            <h2>Path Mappings</h2>
            <div class="mapping-help">
                <strong>Mapping Types:</strong><br>
                <code>prefix</code> - Simple string prefix replacement<br>
                <code>wildcard</code> - Use <code>*</code> (single segment) and <code>**</code> (any path), reference with <code>{1}</code>, <code>{2}</code><br>
                <code>regex</code> - Full regex with <code>$1</code>, <code>$2</code> backreferences
            </div>
        </div>

        <div class="section">
            <h2>Full Configuration (JSON)</h2>
            <textarea name="config" id="configJson">` + string(configJSON) + `</textarea>
        </div>

        <button type="submit">Save Configuration</button>
        <span class="success" id="savedMsg" style="display: none;">Saved!</span>
    </form>

    <p style="margin-top: 30px;">
        <a href="/install">Install Browser Extension</a>
    </p>

    <script>
        // Show saved message if redirected with ?saved=1
        if (window.location.search.includes('saved=1')) {
            document.getElementById('savedMsg').style.display = 'inline';
            setTimeout(() => {
                document.getElementById('savedMsg').style.display = 'none';
                history.replaceState(null, '', '/config');
            }, 3000);
        }

        // Sync player dropdown with JSON
        const playerSelect = document.getElementById('player');
        const configJson = document.getElementById('configJson');

        try {
            const cfg = JSON.parse(configJson.value);
            playerSelect.value = cfg.player || 'mpv';
        } catch (e) {}

        playerSelect.addEventListener('change', function() {
            try {
                const cfg = JSON.parse(configJson.value);
                cfg.player = this.value;
                configJson.value = JSON.stringify(cfg, null, 2);
            } catch (e) {
                alert('Invalid JSON in configuration');
            }
        });
    </script>
</body>
</html>`
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
		return
	}

	if r.Method == "POST" {
		r.ParseForm()
		newConfigJSON := r.FormValue("config")

		var newConfig Config
		if err := json.Unmarshal([]byte(newConfigJSON), &newConfig); err != nil {
			http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
			return
		}

		// Ensure players map exists
		if newConfig.Players == nil {
			newConfig.Players = defaultConfig().Players
		}

		configMu.Lock()
		config = newConfig
		err := saveConfigLocked()
		configMu.Unlock()

		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to save: %v", err), http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, "/config?saved=1", http.StatusSeeOther)
		return
	}
}

func configAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	configMu.RLock()
	defer configMu.RUnlock()

	json.NewEncoder(w).Encode(config)
}

func extensionDownloadHandler(w http.ResponseWriter, r *http.Request) {
	// Create a zip file in memory
	buf := new(bytes.Buffer)
	zipWriter := zip.NewWriter(buf)

	// Walk the embedded extension directory
	err := fs.WalkDir(extensionFS, "extension", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		// Read the file
		data, err := extensionFS.ReadFile(path)
		if err != nil {
			return err
		}

		// Create file in zip (strip "extension/" prefix)
		zipPath := strings.TrimPrefix(path, "extension/")
		f, err := zipWriter.Create(zipPath)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		return err
	})

	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create zip: %v", err), http.StatusInternalServerError)
		return
	}

	zipWriter.Close()

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=embyfin-kiosk-extension.zip")
	w.Write(buf.Bytes())
}

func userscriptHandler(w http.ResponseWriter, r *http.Request) {
	// Read content.js from embedded FS
	contentJS, err := extensionFS.ReadFile("extension/content.js")
	if err != nil {
		http.Error(w, "Failed to read content.js", http.StatusInternalServerError)
		return
	}

	// Build @match directives from config
	configMu.RLock()
	serverURLs := config.ServerURLs
	configMu.RUnlock()

	var matchLines strings.Builder
	if len(serverURLs) == 0 {
		// Fallback if no servers configured
		matchLines.WriteString("// @match        *://*/*\n")
	} else {
		for _, url := range serverURLs {
			matchLines.WriteString(fmt.Sprintf("// @match        %s\n", url))
		}
	}

	// Userscript header
	header := fmt.Sprintf(`// ==UserScript==
// @name         Embyfin Kiosk
// @namespace    https://github.com/jvasile/embyfin-kiosk
// @version      1.0.0
// @description  Play Emby/Jellyfin videos in external player (mpv/VLC) via local server
%s// @grant        GM_xmlhttpRequest
// @connect      localhost
// @connect      127.0.0.1
// ==/UserScript==

`, matchLines.String())

	// Replacement function using GM_xmlhttpRequest
	gmFunction := `    // ==PLAY_FUNCTION_START==
    // Send play request to local kiosk server via GM_xmlhttpRequest
    function playInExternalPlayer(path) {
        const url = KIOSK_SERVER + '/api/play?path=' + encodeURIComponent(path);
        GM_xmlhttpRequest({
            method: 'GET',
            url: url,
            onload: function(response) {
                if (response.status === 200) {
                    console.log('Embyfin Kiosk: Playing in external player');
                } else {
                    console.error('Embyfin Kiosk: Server error', response.status);
                    alert('Embyfin Kiosk: Server returned error ' + response.status);
                }
            },
            onerror: function(error) {
                console.error('Embyfin Kiosk: Failed to connect', error);
                alert('Embyfin Kiosk: Could not connect to local server. Is embyfin-kiosk.exe running?');
            }
        });
    }
    // ==PLAY_FUNCTION_END==`

	// Replace the chrome.runtime.sendMessage version with GM_xmlhttpRequest version
	script := string(contentJS)

	// Find and replace the function between markers
	startMarker := "    // ==PLAY_FUNCTION_START=="
	endMarker := "    // ==PLAY_FUNCTION_END=="

	startIdx := strings.Index(script, startMarker)
	endIdx := strings.Index(script, endMarker)

	if startIdx != -1 && endIdx != -1 {
		endIdx += len(endMarker)
		script = script[:startIdx] + gmFunction + script[endIdx:]
	}

	// Remove the extension-specific comment
	script = strings.Replace(script,
		"// Content script injected into Emby/Jellyfin pages\n// This file is shared between the browser extension and userscript",
		"// Embyfin Kiosk Userscript\n// Generated from extension/content.js",
		1)

	w.Header().Set("Content-Type", "application/javascript")
	w.Write([]byte(header + script))
}

func installPageHandler(w http.ResponseWriter, r *http.Request) {
	// Handle POST to save server URLs
	if r.Method == "POST" {
		r.ParseForm()
		urls := r.Form["server_url"]
		// Filter empty URLs
		var filtered []string
		for _, u := range urls {
			u = strings.TrimSpace(u)
			if u != "" {
				filtered = append(filtered, u)
			}
		}
		configMu.Lock()
		config.ServerURLs = filtered
		config.ServerURLsSet = true
		saveConfigLocked()
		configMu.Unlock()
		http.Redirect(w, r, "/install?saved=1#userscript-content", http.StatusSeeOther)
		return
	}

	// Get current server URLs
	configMu.RLock()
	serverURLs := config.ServerURLs
	configMu.RUnlock()

	// Build URL inputs
	var urlInputs strings.Builder
	if len(serverURLs) == 0 {
		urlInputs.WriteString(`<input type="text" name="server_url" placeholder="http://myserver:8096/*" class="url-input">`)
	} else {
		for _, u := range serverURLs {
			urlInputs.WriteString(fmt.Sprintf(`<input type="text" name="server_url" value="%s" class="url-input">`, u))
		}
	}

	html := `<!DOCTYPE html>
<html>
<head>
    <title>Install Embyfin Kiosk</title>
    <style>
        body { font-family: system-ui, sans-serif; max-width: 800px; margin: 50px auto; padding: 20px; line-height: 1.6; }
        h1 { margin-bottom: 30px; }
        h2 { margin-top: 30px; color: #333; }
        h3 { margin-top: 20px; color: #555; }
        .download-btn {
            display: inline-block;
            padding: 15px 30px;
            font-size: 18px;
            background: #3b82f6;
            color: white;
            text-decoration: none;
            border-radius: 8px;
            margin: 20px 0;
        }
        .download-btn:hover { background: #2563eb; }
        .download-btn.secondary {
            background: #10b981;
        }
        .download-btn.secondary:hover { background: #059669; }
        ol { padding-left: 20px; }
        li { margin-bottom: 10px; }
        code {
            background: #f1f5f9;
            padding: 2px 8px;
            border-radius: 4px;
            font-size: 14px;
        }
        .browser-section {
            background: #f9fafb;
            padding: 20px;
            border-radius: 8px;
            margin: 20px 0;
        }
        .note {
            background: #fef3c7;
            padding: 15px;
            border-radius: 8px;
            margin: 20px 0;
        }
        .method-tabs {
            display: flex;
            gap: 10px;
            margin-bottom: 20px;
        }
        .method-tab {
            padding: 10px 20px;
            border: 2px solid #3b82f6;
            border-radius: 8px;
            cursor: pointer;
            background: white;
            font-size: 16px;
        }
        .method-tab.active {
            background: #3b82f6;
            color: white;
        }
        .method-content { display: none; }
        .method-content.active { display: block; }
        .url-input {
            width: 100%;
            padding: 8px;
            margin: 5px 0;
            border: 1px solid #ccc;
            border-radius: 4px;
            font-size: 14px;
            box-sizing: border-box;
        }
        .url-list { margin: 10px 0; }
        .add-url-btn {
            background: #e5e7eb;
            border: none;
            padding: 8px 16px;
            border-radius: 4px;
            cursor: pointer;
            margin-top: 5px;
        }
        .add-url-btn:hover { background: #d1d5db; }
        .save-btn {
            background: #3b82f6;
            color: white;
            border: none;
            padding: 10px 20px;
            border-radius: 4px;
            cursor: pointer;
            margin-top: 10px;
        }
        .save-btn:hover { background: #2563eb; }
        .success { color: green; margin-left: 10px; }
        .discover-btn {
            background: #8b5cf6;
            color: white;
            border: none;
            padding: 10px 20px;
            border-radius: 4px;
            cursor: pointer;
            margin-bottom: 15px;
        }
        .discover-btn:hover { background: #7c3aed; }
        .discover-btn:disabled { background: #c4b5fd; cursor: wait; }
        .reset-btn {
            background: #6b7280;
            color: white;
            border: none;
            padding: 10px 20px;
            border-radius: 4px;
            cursor: pointer;
            margin-left: 10px;
            margin-bottom: 15px;
        }
        .reset-btn:hover { background: #4b5563; }
        #discoverStatus { margin-left: 10px; color: #666; }
    </style>
</head>
<body>
    <h1>Install Embyfin Kiosk</h1>

    <div class="method-tabs">
        <button class="method-tab active" onclick="showMethod('extension')">Browser Extension</button>
        <button class="method-tab" onclick="showMethod('userscript')">Userscript</button>
    </div>

    <div id="extension-content" class="method-content active">
        <a href="/extension.zip" class="download-btn">Download Extension</a>

        <div class="browser-section">
            <h3>Chrome / Edge / Brave</h3>
            <ol>
                <li>Download and extract the zip file</li>
                <li>Open <code>chrome://extensions</code> (or <code>edge://extensions</code>)</li>
                <li>Enable "Developer mode" (toggle in top right)</li>
                <li>Click "Load unpacked"</li>
                <li>Select the extracted folder</li>
            </ol>
        </div>

        <div class="browser-section">
            <h3>Firefox</h3>
            <ol>
                <li>Download and extract the zip file</li>
                <li>Open <code>about:debugging#/runtime/this-firefox</code></li>
                <li>Click "Load Temporary Add-on"</li>
                <li>Select any file in the extracted folder (e.g., manifest.json)</li>
            </ol>
            <div class="note">
                <strong>Note:</strong> Temporary add-ons are removed when Firefox closes.
                For permanent installation, the extension needs to be signed by Mozilla.
            </div>
        </div>

        <h2>After Installation</h2>
        <ol>
            <li>Click the extension icon in your browser toolbar</li>
            <li>Verify the server URL is <code>http://localhost:9999</code></li>
            <li>The status should show "Server running"</li>
            <li>Navigate to your Emby or Jellyfin server</li>
            <li>Click play on any movie or episode, or press <strong>K</strong></li>
        </ol>
    </div>

    <div id="userscript-content" class="method-content">
        <p>Userscripts work with a userscript manager like Tampermonkey, Violentmonkey, or Greasemonkey.</p>

        <div class="browser-section">
            <h3>Step 1: Install a Userscript Manager</h3>
            <p>If you don't already have one, install a userscript manager for your browser:</p>
            <ul>
                <li><strong>Tampermonkey</strong> - Available for Chrome, Firefox, Edge, Safari</li>
                <li><strong>Violentmonkey</strong> - Available for Chrome, Firefox, Edge</li>
                <li><strong>Greasemonkey</strong> - Firefox only</li>
            </ul>
        </div>

        <div class="browser-section">
            <h3>Step 2: Configure Server URLs</h3>
            <p>Enter the URLs of your Emby/Jellyfin servers, or discover them automatically.</p>
            <button type="button" class="discover-btn" onclick="discoverServers()">Discover Servers</button>
            <button type="button" class="reset-btn" onclick="resetToDiscovery()">Reset to Auto-Discovery</button>
            <span id="discoverStatus"></span>
            <form method="POST" id="urlForm">
                <div class="url-list" id="urlList">
                    ` + urlInputs.String() + `
                </div>
                <button type="button" class="add-url-btn" onclick="addUrlInput()">+ Add Another Server</button>
                <br>
                <button type="submit" class="save-btn">Save URLs</button>
                <span class="success" id="savedMsg" style="display: none;">Saved!</span>
            </form>
        </div>

        <div class="browser-section">
            <h3>Step 3: Install the Userscript</h3>
            <ol>
                <li>Click the install button below</li>
                <li>Your userscript manager should detect it and offer to install</li>
                <li>Click "Install" or "Confirm"</li>
            </ol>
            <a href="/embyfin-kiosk.user.js" class="download-btn secondary">Install Userscript</a>
        </div>

        <div class="note" style="background: #f0f9ff;">
            <strong>Tip:</strong> If you change the server URLs, reinstall the userscript to pick up the changes.
        </div>

        <h2>After Installation</h2>
        <ol>
            <li>Navigate to your Emby or Jellyfin server</li>
            <li>Click play on any movie or episode, or press <strong>K</strong></li>
        </ol>
    </div>

    <p style="margin-top: 40px;">
        <a href="/config">← Back to Configuration</a>
    </p>

    <script>
        function showMethod(method) {
            document.querySelectorAll('.method-tab').forEach(t => t.classList.remove('active'));
            document.querySelectorAll('.method-content').forEach(c => c.classList.remove('active'));
            document.querySelector('.method-tab[onclick*="' + method + '"]').classList.add('active');
            document.getElementById(method + '-content').classList.add('active');
        }

        function addUrlInput() {
            const input = document.createElement('input');
            input.type = 'text';
            input.name = 'server_url';
            input.placeholder = 'http://myserver:8096/*';
            input.className = 'url-input';
            document.getElementById('urlList').appendChild(input);
        }

        // Show saved message and switch to userscript tab if redirected with ?saved=1
        if (window.location.search.includes('saved=1')) {
            showMethod('userscript');
            document.getElementById('savedMsg').style.display = 'inline';
            setTimeout(() => {
                document.getElementById('savedMsg').style.display = 'none';
                history.replaceState(null, '', '/install');
            }, 3000);
        }

        async function discoverServers() {
            const btn = document.querySelector('.discover-btn');
            const status = document.getElementById('discoverStatus');

            btn.disabled = true;
            status.textContent = 'Scanning network...';
            status.style.color = '#666';

            // Start discovery
            await fetch('/api/discover');

            // Poll for results
            let attempts = 0;
            const maxAttempts = 8; // ~4 seconds

            const poll = async () => {
                attempts++;
                const response = await fetch('/api/discover');
                const data = await response.json();

                if (data.status === 'scanning' && attempts < maxAttempts) {
                    setTimeout(poll, 500);
                    return;
                }

                // Discovery complete
                const servers = data.servers || [];
                if (servers.length > 0) {
                    const urlList = document.getElementById('urlList');
                    const existing = new Set();
                    urlList.querySelectorAll('input').forEach(input => {
                        if (input.value) existing.add(input.value);
                    });

                    let added = 0;
                    servers.forEach(server => {
                        if (!existing.has(server.url)) {
                            const input = document.createElement('input');
                            input.type = 'text';
                            input.name = 'server_url';
                            input.value = server.url;
                            input.className = 'url-input';
                            input.title = server.name + ' (' + server.platform + ')';
                            urlList.appendChild(input);
                            added++;
                        }
                    });

                    status.textContent = 'Found ' + servers.length + ' server(s)' + (added < servers.length ? ', ' + (servers.length - added) + ' already listed' : '');
                    status.style.color = 'green';
                } else {
                    status.textContent = 'No servers found';
                    status.style.color = '#666';
                }

                btn.disabled = false;
                setTimeout(() => { status.textContent = ''; }, 5000);
            };

            setTimeout(poll, 500);
        }

        // Check if servers were auto-discovered on page load
        (async function checkAutoDiscovery() {
            const response = await fetch('/api/discover');
            const data = await response.json();
            if (data.servers && data.servers.length > 0) {
                const urlList = document.getElementById('urlList');
                const inputs = urlList.querySelectorAll('input');
                // Only auto-fill if the list is empty or has just the placeholder
                if (inputs.length === 0 || (inputs.length === 1 && !inputs[0].value)) {
                    if (inputs.length === 1) inputs[0].remove();
                    data.servers.forEach(server => {
                        const input = document.createElement('input');
                        input.type = 'text';
                        input.name = 'server_url';
                        input.value = server.url;
                        input.className = 'url-input';
                        input.title = server.name + ' (' + server.platform + ')';
                        urlList.appendChild(input);
                    });
                }
            }
        })();

        async function resetToDiscovery() {
            if (!confirm('This will clear your saved server URLs and re-scan the network. Continue?')) {
                return;
            }

            const status = document.getElementById('discoverStatus');
            status.textContent = 'Resetting...';
            status.style.color = '#666';

            // Clear the URL list
            const urlList = document.getElementById('urlList');
            urlList.innerHTML = '<input type="text" name="server_url" placeholder="http://myserver:8096/*" class="url-input">';

            // Call reset API
            await fetch('/api/discover/reset');

            // Now run discovery
            discoverServers();
        }
    </script>
</body>
</html>`
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	html := `<!DOCTYPE html>
<html>
<head>
    <title>Embyfin Kiosk</title>
    <style>
        body { font-family: system-ui, sans-serif; max-width: 600px; margin: 80px auto; padding: 20px; text-align: center; }
        h1 { margin-bottom: 10px; }
        .subtitle { color: #666; margin-bottom: 40px; }
        .links { display: flex; flex-direction: column; gap: 15px; align-items: center; }
        a {
            display: inline-block;
            padding: 12px 24px;
            background: #3b82f6;
            color: white;
            text-decoration: none;
            border-radius: 6px;
            min-width: 200px;
        }
        a:hover { background: #2563eb; }
        .status { margin-top: 40px; padding: 15px; background: #f0fdf4; border-radius: 8px; color: #166534; }
    </style>
</head>
<body>
    <h1>Embyfin Kiosk</h1>
    <p class="subtitle">External player launcher for Emby/Jellyfin</p>
    <div class="links">
        <a href="/install">Install Browser Extension</a>
        <a href="/config">Configuration</a>
    </div>
    <div class="status">Server running</div>
</body>
</html>`
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

type DiscoveredServer struct {
	Name     string `json:"name"`
	Address  string `json:"address"`
	URL      string `json:"url"`
	Platform string `json:"platform"` // "jellyfin" or "emby"
}

var (
	discoveryRunning bool
	discoveryMu      sync.Mutex
	lastDiscovery    []DiscoveredServer
)

// getBroadcastAddresses returns broadcast addresses for all network interfaces
func getBroadcastAddresses() []net.IP {
	var broadcasts []net.IP
	seen := make(map[string]bool)

	// Always include global broadcast
	broadcasts = append(broadcasts, net.IPv4bcast)
	seen["255.255.255.255"] = true

	interfaces, err := net.Interfaces()
	if err != nil {
		return broadcasts
	}

	for _, iface := range interfaces {
		// Skip loopback and down interfaces
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}

			ip := ipNet.IP.To4()
			if ip == nil {
				continue // Skip IPv6
			}

			// Calculate broadcast address: IP | ^Mask
			mask := ipNet.Mask
			if len(mask) != 4 {
				continue
			}

			broadcast := make(net.IP, 4)
			for i := 0; i < 4; i++ {
				broadcast[i] = ip[i] | ^mask[i]
			}

			broadcastStr := broadcast.String()
			if !seen[broadcastStr] {
				broadcasts = append(broadcasts, broadcast)
				seen[broadcastStr] = true
				log.Printf("Discovery: will try broadcast %s (from %s)", broadcastStr, iface.Name)
			}
		}
	}

	return broadcasts
}

// runDiscovery performs network discovery and optionally updates config
func runDiscovery(updateConfig bool) []DiscoveredServer {
	discoveryMu.Lock()
	if discoveryRunning {
		discoveryMu.Unlock()
		return nil
	}
	discoveryRunning = true
	discoveryMu.Unlock()

	defer func() {
		discoveryMu.Lock()
		discoveryRunning = false
		discoveryMu.Unlock()
	}()

	var servers []DiscoveredServer
	var mu sync.Mutex
	var wg sync.WaitGroup
	seen := make(map[string]bool) // Track seen server addresses to avoid duplicates

	// Get all broadcast addresses to try
	broadcasts := getBroadcastAddresses()

	// Discovery messages
	queries := []struct {
		message  string
		platform string
	}{
		{"Who is JellyfinServer?", "jellyfin"},
		{"who is EmbyServer?", "emby"},
	}

	for _, q := range queries {
		wg.Add(1)
		go func(message, platform string) {
			defer wg.Done()

			// Create UDP socket
			conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
			if err != nil {
				log.Printf("Discovery: failed to create socket: %v", err)
				return
			}
			defer conn.Close()

			// Set read deadline
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))

			// Send to all broadcast addresses
			for _, broadcastIP := range broadcasts {
				broadcastAddr := &net.UDPAddr{IP: broadcastIP, Port: 7359}
				_, err = conn.WriteToUDP([]byte(message), broadcastAddr)
				if err != nil {
					log.Printf("Discovery: failed to send to %s: %v", broadcastIP, err)
				}
			}

			// Listen for responses
			buf := make([]byte, 4096)
			for {
				n, addr, err := conn.ReadFromUDP(buf)
				if err != nil {
					break // Timeout or error
				}

				// Parse response (JSON)
				var response struct {
					Name      string `json:"Name"`
					Address   string `json:"Address"`
					LocalAddr string `json:"LocalAddress"`
				}
				if err := json.Unmarshal(buf[:n], &response); err != nil {
					continue
				}

				// Build URL - prefer LocalAddress if available
				serverURL := response.Address
				if response.LocalAddr != "" {
					serverURL = response.LocalAddr
				}
				if serverURL == "" {
					serverURL = fmt.Sprintf("http://%s:8096", addr.IP.String())
				}

				// Deduplicate by address
				mu.Lock()
				key := addr.IP.String() + "|" + platform
				if !seen[key] {
					seen[key] = true
					servers = append(servers, DiscoveredServer{
						Name:     response.Name,
						Address:  addr.IP.String(),
						URL:      serverURL + "/*",
						Platform: platform,
					})
					log.Printf("Discovery: found %s server %q at %s", platform, response.Name, serverURL)
				}
				mu.Unlock()
			}
		}(q.message, q.platform)
	}

	wg.Wait()

	discoveryMu.Lock()
	lastDiscovery = servers
	discoveryMu.Unlock()

	// Update config if requested and servers were found
	if updateConfig && len(servers) > 0 {
		configMu.Lock()
		if !config.ServerURLsSet {
			// Add discovered URLs to config (avoid duplicates)
			existing := make(map[string]bool)
			for _, u := range config.ServerURLs {
				existing[u] = true
			}
			for _, s := range servers {
				if !existing[s.URL] {
					config.ServerURLs = append(config.ServerURLs, s.URL)
					existing[s.URL] = true
				}
			}
			saveConfigLocked()
			log.Printf("Discovery: auto-configured %d server URL(s)", len(config.ServerURLs))
		}
		configMu.Unlock()
	}

	return servers
}

// startBackgroundDiscovery runs discovery in the background
func startBackgroundDiscovery() {
	go runDiscovery(true)
}

func discoverHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Check if discovery is already running
	discoveryMu.Lock()
	running := discoveryRunning
	cached := lastDiscovery
	discoveryMu.Unlock()

	if running {
		// Return cached results or empty while scanning
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "scanning",
			"servers": cached,
		})
		return
	}

	// Start async discovery
	go runDiscovery(false) // Don't auto-update config from manual trigger

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "started",
		"servers": cached,
	})
}

func resetDiscoveryHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Clear user-set flag and URLs, then start discovery
	configMu.Lock()
	config.ServerURLs = []string{}
	config.ServerURLsSet = false
	saveConfigLocked()
	configMu.Unlock()

	// Clear cached discovery results
	discoveryMu.Lock()
	lastDiscovery = nil
	discoveryMu.Unlock()

	// Start fresh discovery
	go runDiscovery(true)

	json.NewEncoder(w).Encode(map[string]string{
		"status": "reset",
	})
}

func main() {
	// Determine config path (same directory as executable)
	exe, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	configPath = filepath.Join(filepath.Dir(exe), "config.json")

	if err := loadConfig(); err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Auto-discover servers on startup if not configured by user
	if !config.ServerURLsSet {
		log.Printf("Server URLs not configured, starting network discovery...")
		startBackgroundDiscovery()
	}

	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/api/play", playHandler)
	http.HandleFunc("/api/config", configAPIHandler)
	http.HandleFunc("/api/discover", discoverHandler)
	http.HandleFunc("/api/discover/reset", resetDiscoveryHandler)
	http.HandleFunc("/config", configPageHandler)
	http.HandleFunc("/install", installPageHandler)
	http.HandleFunc("/extension.zip", extensionDownloadHandler)
	http.HandleFunc("/embyfin-kiosk.user.js", userscriptHandler)

	addr := fmt.Sprintf("127.0.0.1:%d", config.Port)
	log.Printf("Starting server on %s", addr)
	log.Printf("Config page: http://%s/config", addr)
	log.Printf("Play endpoint: http://%s/api/play?path=...", addr)

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(err)
	}
}
