package main

import (
	"archive/zip"
	"bytes"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
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
	URLEncode     bool                    `json:"url_encode"`      // URL-encode path when passing to player
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
		Port:   9998,
		Player: "mpv",
		Players: map[string]PlayerConfig{
			"mpv": {Name: "mpv", Path: "mpv", Args: []string{"--fs"}},
			"vlc": {Name: "VLC", Path: "vlc", Args: []string{"--fullscreen"}},
		},
		PathMappings: []PathMapping{
			{Type: "prefix", Match: "", Replace: ""},
			{Type: "prefix", Match: "", Replace: ""},
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

// wildcardToRegex converts a wildcard pattern to a regex for prefix-style matching
// * matches anything except /
// ** matches anything including /
// The pattern matches the beginning of the path, and captures the remainder
func wildcardToRegex(pattern string) (*regexp.Regexp, error) {
	var result strings.Builder
	result.WriteString("^")

	i := 0
	for i < len(pattern) {
		if i+1 < len(pattern) && pattern[i] == '*' && pattern[i+1] == '*' {
			result.WriteString("(.*?)")
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

	// Capture the remainder of the path
	result.WriteString("(.*)")
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
		matches := re.FindStringSubmatch(path)
		if matches != nil {
			// Last capture group is the remainder of the path
			remainder := matches[len(matches)-1]
			// Replace {1}, {2}, etc. with captured groups (excluding remainder)
			result := mapping.Replace
			for i := 1; i < len(matches)-1; i++ {
				result = strings.ReplaceAll(result, fmt.Sprintf("{%d}", i), matches[i])
			}
			// Append the remainder with proper path separator
			if len(remainder) > 0 && !strings.HasSuffix(result, "/") && !strings.HasSuffix(result, `\`) {
				result += "/"
			}
			return result + remainder, true
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

	// Check for colons in SMB paths (indicates a problem)
	if strings.HasPrefix(translatedPath, `\\`) {
		// Find position after the server and share parts
		// \\server\share\rest\of\path
		parts := strings.SplitN(translatedPath[2:], `\`, 3)
		if len(parts) >= 3 && strings.Contains(parts[2], ":") {
			log.Printf("Warning: Colon in SMB path may cause issues: %s", translatedPath)
		}
	}

	configMu.RLock()
	playerKey := config.Player
	playerConfig, ok := config.Players[playerKey]
	configMu.RUnlock()

	if !ok {
		log.Printf("Unknown player %q, falling back to mpv", playerKey)
		playerConfig = PlayerConfig{Path: "mpv", Args: []string{"--fs"}}
	}

	// URL-encode if configured (helps with special characters in paths)
	configMu.RLock()
	urlEncode := config.URLEncode
	configMu.RUnlock()

	pathForPlayer := translatedPath
	if urlEncode {
		pathForPlayer = url.PathEscape(translatedPath)
	}

	args := append([]string{}, playerConfig.Args...)
	args = append(args, pathForPlayer)

	// Log the exact command line
	cmdLine := playerConfig.Path
	for _, arg := range args {
		if strings.Contains(arg, " ") {
			cmdLine += fmt.Sprintf(" %q", arg)
		} else {
			cmdLine += " " + arg
		}
	}
	log.Printf("Command: %s", cmdLine)

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

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

func selected(b bool) string {
	if b {
		return " selected"
	}
	return ""
}

func configPageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		configMu.RLock()
		currentPlayer := config.Player
		mappings := config.PathMappings
		urlEncode := config.URLEncode
		configMu.RUnlock()

		// Build mapping rows HTML
		var mappingRows strings.Builder
		for i, m := range mappings {
			mappingRows.WriteString(fmt.Sprintf(`
            <div class="mapping-row" data-index="%d">
                <select name="mapping_type_%d" class="mapping-type">
                    <option value="prefix"%s>prefix</option>
                    <option value="wildcard"%s>wildcard</option>
                    <option value="regex"%s>regex</option>
                </select>
                <input type="text" name="mapping_match_%d" value="%s" placeholder="Match pattern" class="mapping-match">
                <span class="arrow">&rarr;</span>
                <input type="text" name="mapping_replace_%d" value="%s" placeholder="Replace with" class="mapping-replace">
                <button type="button" class="remove-btn" onclick="removeMapping(this)">&times;</button>
            </div>`,
				i, i,
				selected(m.Type == "prefix"), selected(m.Type == "wildcard"), selected(m.Type == "regex"),
				i, escapeHTML(m.Match),
				i, escapeHTML(m.Replace)))
		}

		playerMpvSelected := ""
		playerVlcSelected := ""
		if currentPlayer == "vlc" {
			playerVlcSelected = " selected"
		} else {
			playerMpvSelected = " selected"
		}

		urlEncodeChecked := ""
		if urlEncode {
			urlEncodeChecked = " checked"
		}

		html := `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>Embyfin Kiosk Config</title>
    <style>
        body { font-family: system-ui, sans-serif; max-width: 900px; margin: 50px auto; padding: 20px; }
        h1 { margin-bottom: 30px; }
        h2 { margin-top: 0; margin-bottom: 15px; font-size: 18px; }
        label { display: block; margin-bottom: 5px; font-weight: 500; }
        select, input[type="text"] {
            padding: 8px;
            font-size: 14px;
            border: 1px solid #ccc;
            border-radius: 4px;
        }
        select { min-width: 100px; }
        .section {
            background: #f9fafb;
            padding: 20px;
            border-radius: 8px;
            margin-bottom: 20px;
        }
        .help { color: #666; font-size: 13px; margin-top: 10px; }
        .mapping-row {
            display: flex;
            align-items: center;
            gap: 10px;
            margin-bottom: 10px;
            flex-wrap: wrap;
        }
        .mapping-type { width: 100px; flex-shrink: 0; }
        .mapping-match, .mapping-replace { flex: 1; min-width: 200px; }
        .arrow { color: #666; font-size: 18px; }
        .remove-btn {
            background: #ef4444;
            color: white;
            border: none;
            border-radius: 4px;
            width: 30px;
            height: 30px;
            cursor: pointer;
            font-size: 18px;
            line-height: 1;
            padding: 0;
        }
        .remove-btn:hover { background: #dc2626; }
        .add-btn {
            background: #10b981;
            color: white;
            border: none;
            padding: 8px 16px;
            border-radius: 4px;
            cursor: pointer;
            margin-top: 10px;
        }
        .add-btn:hover { background: #059669; }
        .save-btn {
            background: #3b82f6;
            color: white;
            border: none;
            padding: 12px 24px;
            font-size: 16px;
            border-radius: 4px;
            cursor: pointer;
            margin-top: 20px;
        }
        .save-btn:hover { background: #2563eb; }
        .success { color: green; margin-left: 10px; }
        #mappingsContainer { margin-top: 15px; }
        .example { background: #f0f9ff; padding: 12px; border-radius: 4px; margin-top: 15px; font-size: 13px; }
        .example code { background: #e0f2fe; padding: 2px 6px; border-radius: 3px; }
        .tip { background: #f0fdf4; padding: 12px; border-radius: 4px; margin-top: 10px; font-size: 13px; color: #166534; }
        .warning { background: #fef3c7; border: 1px solid #f59e0b; color: #92400e; padding: 15px; border-radius: 8px; margin-top: 30px; }
        .warning a { color: #92400e; font-weight: 500; }
    </style>
</head>
<body>
    <h1>Embyfin Kiosk Configuration</h1>

    <form method="POST" id="configForm">
        <div class="section">
            <h2>Player</h2>
            <label for="player">Default Player</label>
            <select name="player" id="player">
                <option value="mpv"` + playerMpvSelected + `>mpv</option>
                <option value="vlc"` + playerVlcSelected + `>VLC</option>
            </select>
        </div>

        <div class="section">
            <h2>Options</h2>
            <label style="display: flex; align-items: center; gap: 8px; font-weight: normal;">
                <input type="checkbox" name="url_encode" value="1"` + urlEncodeChecked + `>
                URL-encode paths when passing to player (for paths with special characters)
            </label>
        </div>

        <div class="section">
            <h2>Path Mappings</h2>
            <p class="help" style="margin-top: 0;">Transform media paths from your server to Windows-accessible paths.</p>

            <div id="mappingsContainer">` + mappingRows.String() + `
            </div>

            <button type="button" class="add-btn" onclick="addMapping()">+ Add Mapping</button>

            <div class="tip">
                <strong>Tip:</strong> To find the path Emby uses, go to any video, click the three dots menu, then "Edit metadata". The file path is shown there.
                <a href="/help/mappings">See mapping examples &rarr;</a>
            </div>
        </div>

        <button type="submit" class="save-btn">Save Configuration</button>
        <span class="success" id="savedMsg" style="display: none;">Saved!</span>
    </form>

    <div id="installWarning" class="warning" style="display: none;">
        <strong>Warning!</strong> No browser extension or userscript detected.
        <a href="/install">Please install.</a>
    </div>

    <script>
        let mappingIndex = ` + fmt.Sprintf("%d", len(mappings)) + `;

        function addMapping() {
            const container = document.getElementById('mappingsContainer');
            const row = document.createElement('div');
            row.className = 'mapping-row';
            row.dataset.index = mappingIndex;
            row.innerHTML = ` + "`" + `
                <select name="mapping_type_${mappingIndex}" class="mapping-type">
                    <option value="prefix" selected>prefix</option>
                    <option value="wildcard">wildcard</option>
                    <option value="regex">regex</option>
                </select>
                <input type="text" name="mapping_match_${mappingIndex}" placeholder="Match pattern" class="mapping-match">
                <span class="arrow">&rarr;</span>
                <input type="text" name="mapping_replace_${mappingIndex}" placeholder="Replace with" class="mapping-replace">
                <button type="button" class="remove-btn" onclick="removeMapping(this)">&times;</button>
            ` + "`" + `;
            container.appendChild(row);
            mappingIndex++;
        }

        function removeMapping(btn) {
            btn.closest('.mapping-row').remove();
        }

        // Show saved message if redirected with ?saved=1
        if (window.location.search.includes('saved=1')) {
            document.getElementById('savedMsg').style.display = 'inline';
            setTimeout(() => {
                document.getElementById('savedMsg').style.display = 'none';
                history.replaceState(null, '', '/config');
            }, 3000);
        }

        // Check for extension/userscript
        (function checkInstalled() {
            document.addEventListener('embyfin-kiosk-installed', function() {
                document.getElementById('installWarning').style.display = 'none';
            });
            setTimeout(() => {
                if (!window.embyfinKioskInstalled) {
                    document.getElementById('installWarning').style.display = 'block';
                }
            }, 1000);
        })();
    </script>
</body>
</html>`
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
		return
	}

	if r.Method == "POST" {
		r.ParseForm()

		// Get player selection
		player := r.FormValue("player")
		if player != "mpv" && player != "vlc" {
			player = "mpv"
		}

		// Check if player is on PATH
		playerPath := player
		configMu.RLock()
		if pc, ok := config.Players[player]; ok && pc.Path != "" {
			playerPath = pc.Path
		}
		configMu.RUnlock()

		if _, err := exec.LookPath(playerPath); err != nil {
			http.Error(w, fmt.Sprintf("Player '%s' not found on PATH. Please install it or configure a custom path.", playerPath), http.StatusBadRequest)
			return
		}

		// Parse path mappings from form
		var mappings []PathMapping
		for i := 0; ; i++ {
			matchKey := fmt.Sprintf("mapping_match_%d", i)
			replaceKey := fmt.Sprintf("mapping_replace_%d", i)
			typeKey := fmt.Sprintf("mapping_type_%d", i)

			match := r.FormValue(matchKey)
			replace := r.FormValue(replaceKey)
			mappingType := r.FormValue(typeKey)

			// Check if this mapping exists (at least match or replace has a value)
			if match == "" && replace == "" {
				// Check if there might be gaps in indices
				if i > 100 { // Safety limit
					break
				}
				continue
			}

			if match != "" { // Only add if match pattern is provided
				if mappingType == "" {
					mappingType = "prefix"
				}
				mappings = append(mappings, PathMapping{
					Type:    mappingType,
					Match:   match,
					Replace: replace,
				})
			}
		}

		// Get URL encode checkbox
		urlEncode := r.FormValue("url_encode") == "1"

		configMu.Lock()
		config.Player = player
		config.PathMappings = mappings
		config.URLEncode = urlEncode
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

func helpMappingsHandler(w http.ResponseWriter, r *http.Request) {
	html := `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>Path Mapping Help - Embyfin Kiosk</title>
    <style>
        body { font-family: system-ui, sans-serif; max-width: 900px; margin: 50px auto; padding: 20px; line-height: 1.6; }
        h1 { margin-bottom: 10px; }
        h2 { margin-top: 30px; color: #333; }
        h3 { margin-top: 20px; color: #555; }
        code { background: #f1f5f9; padding: 2px 8px; border-radius: 4px; font-size: 14px; }
        pre { background: #f1f5f9; padding: 15px; border-radius: 8px; overflow-x: auto; }
        .example { background: #f9fafb; padding: 20px; border-radius: 8px; margin: 15px 0; }
        .example-title { font-weight: 600; margin-bottom: 10px; }
        .arrow { color: #666; }
        table { border-collapse: collapse; width: 100%; margin: 15px 0; }
        th, td { border: 1px solid #e5e7eb; padding: 10px; text-align: left; }
        th { background: #f9fafb; }
        a { color: #3b82f6; }
    </style>
</head>
<body>
    <h1>Path Mapping Help</h1>
    <p><a href="/config">&larr; Back to Configuration</a></p>

    <p>Path mappings transform media file paths from your Emby/Jellyfin server to Windows-accessible paths.</p>

    <h2>Mapping Types</h2>

    <h3>1. Prefix (Recommended)</h3>
    <p>Simple string replacement. If the path starts with the match pattern, replace that prefix.</p>
    <div class="example">
        <div class="example-title">Example: NFS path to Windows share</div>
        <table>
            <tr><th>Type</th><td><code>prefix</code></td></tr>
            <tr><th>Match</th><td><code>nfs://192.168.1.28/mnt/jbod/007/media/Movies</code></td></tr>
            <tr><th>Replace</th><td><code>\\172.16.50.28\Movies</code></td></tr>
        </table>
        <p>
            <code>nfs://192.168.1.28/mnt/jbod/007/media/Movies/Inception/Inception.mkv</code><br>
            <span class="arrow">&darr;</span><br>
            <code>\\172.16.50.28\Movies\Inception\Inception.mkv</code>
        </p>
    </div>

    <h3>2. Wildcard</h3>
    <p>Like prefix, but with wildcards in the pattern. The remainder of the path is automatically appended.</p>
    <table>
        <tr><th>Pattern</th><th>Matches</th></tr>
        <tr><td><code>*</code></td><td>Any characters except <code>/</code></td></tr>
        <tr><td><code>**</code></td><td>Any characters including <code>/</code></td></tr>
    </table>
    <p>Use <code>{1}</code>, <code>{2}</code>, etc. in the replacement to reference captured wildcards.</p>
    <div class="example">
        <div class="example-title">Example: Match any server IP</div>
        <table>
            <tr><th>Type</th><td><code>wildcard</code></td></tr>
            <tr><th>Match</th><td><code>nfs://*/mnt/jbod/007/media/Movies</code></td></tr>
            <tr><th>Replace</th><td><code>\\172.16.50.28\Movies</code></td></tr>
        </table>
        <p>
            <code>nfs://192.168.1.28/mnt/jbod/007/media/Movies/Inception/Inception.mkv</code><br>
            <span class="arrow">&darr;</span><br>
            <code>\\172.16.50.28\Movies/Inception/Inception.mkv</code>
        </p>
        <p><small>The <code>*</code> matches <code>192.168.1.28</code>. The rest of the path (<code>/Inception/Inception.mkv</code>) is automatically appended.</small></p>
    </div>
    <div class="example">
        <div class="example-title">Example: Match variable path depth with **</div>
        <table>
            <tr><th>Type</th><td><code>wildcard</code></td></tr>
            <tr><th>Match</th><td><code>nfs://**/media/Movies</code></td></tr>
            <tr><th>Replace</th><td><code>\\172.16.50.28\Movies</code></td></tr>
        </table>
        <p>
            <code>nfs://192.168.1.28/mnt/jbod/007/media/Movies/Inception/Inception.mkv</code><br>
            <span class="arrow">&darr;</span><br>
            <code>\\172.16.50.28\Movies/Inception/Inception.mkv</code>
        </p>
        <p><small>The <code>**</code> matches <code>192.168.1.28/mnt/jbod/007</code> (everything up to <code>/media/Movies</code>).</small></p>
    </div>

    <h3>3. Regex</h3>
    <p>Full regular expression support. Use <code>$1</code>, <code>$2</code>, etc. for backreferences.</p>
    <div class="example">
        <div class="example-title">Example: Complex pattern with regex</div>
        <table>
            <tr><th>Type</th><td><code>regex</code></td></tr>
            <tr><th>Match</th><td><code>nfs://[^/]+/mnt/jbod/\d+/media/(Movies|TV)/(.*)</code></td></tr>
            <tr><th>Replace</th><td><code>\\172.16.50.28\$1\$2</code></td></tr>
        </table>
        <p>
            <code>nfs://192.168.1.28/mnt/jbod/007/media/Movies/Inception/Inception.mkv</code><br>
            <span class="arrow">&darr;</span><br>
            <code>\\172.16.50.28\Movies\Inception/Inception.mkv</code>
        </p>
    </div>

    <h2>Tips</h2>
    <ul>
        <li>Start with <strong>prefix</strong> mappings - they're the simplest and fastest.</li>
        <li>Create separate mappings for Movies and TV if they're in different shares.</li>
        <li>Forward slashes in the remaining path are automatically converted to backslashes.</li>
        <li>Test your mappings by clicking play and checking the server console output.</li>
    </ul>

    <p style="margin-top: 40px;"><a href="/config">&larr; Back to Configuration</a></p>
</body>
</html>`
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
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

	configMu.RLock()
	port := config.Port
	configMu.RUnlock()

	var includeLines strings.Builder
	// Always include the kiosk server pages for detection
	includeLines.WriteString(fmt.Sprintf("// @include      http://localhost:%d/*\n", port))
	includeLines.WriteString(fmt.Sprintf("// @include      http://127.0.0.1:%d/*\n", port))
	if len(serverURLs) == 0 {
		// Fallback if no servers configured
		includeLines.WriteString("// @include      *://*/*\n")
	} else {
		for _, url := range serverURLs {
			includeLines.WriteString(fmt.Sprintf("// @include      %s\n", url))
		}
	}

	// Userscript header
	header := fmt.Sprintf(`// ==UserScript==
// @name         Embyfin Kiosk
// @namespace    https://github.com/jvasile/embyfin-kiosk
// @version      1.0.0
// @description  Play Emby/Jellyfin videos in external player (mpv/VLC) via local server
%s// @grant        GM_xmlhttpRequest
// @grant        GM.xmlHttpRequest
// @grant        unsafeWindow
// @connect      localhost
// @connect      127.0.0.1
// ==/UserScript==

`, includeLines.String())

	// Replace the play function - support both GM_xmlhttpRequest and GM.xmlHttpRequest
	gmFunction := `    // ==PLAY_FUNCTION_START==
    // Send play request to local kiosk server
    function playInExternalPlayer(path) {
        const url = KIOSK_SERVER + '/api/play?path=' + encodeURIComponent(path);
        const opts = {
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
        };
        // Support both Greasemonkey 4+ (GM.xmlHttpRequest) and older/Tampermonkey (GM_xmlhttpRequest)
        if (typeof GM !== 'undefined' && GM.xmlHttpRequest) {
            GM.xmlHttpRequest(opts);
        } else if (typeof GM_xmlhttpRequest !== 'undefined') {
            GM_xmlhttpRequest(opts);
        } else {
            console.error('Embyfin Kiosk: No GM_xmlhttpRequest available');
            alert('Embyfin Kiosk: Userscript manager not supported');
        }
    }
    // ==PLAY_FUNCTION_END==`

	// Replace the chrome.runtime.sendMessage version with fetch version
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

	// Replace hardcoded port with configured port (port already read above)
	script = strings.Replace(script,
		"const KIOSK_SERVER = 'http://localhost:9999'",
		fmt.Sprintf("const KIOSK_SERVER = 'http://localhost:%d'", port),
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
		http.Redirect(w, r, "/install/userscript?saved=1", http.StatusSeeOther)
		return
	}

	// Redirect base /install to /install/extension
	http.Redirect(w, r, "/install/extension", http.StatusSeeOther)
}

func installExtensionHandler(w http.ResponseWriter, r *http.Request) {
	html := `<!DOCTYPE html>
<html>
<head>
    <title>Install Extension - Embyfin Kiosk</title>
    <style>
        body { font-family: system-ui, sans-serif; max-width: 800px; margin: 50px auto; padding: 20px; line-height: 1.6; }
        h1 { margin-bottom: 30px; }
        h3 { margin-top: 20px; color: #555; }
        code { background: #f1f5f9; padding: 2px 8px; border-radius: 4px; font-size: 14px; }
        a { color: #3b82f6; }
        .browser-section { background: #f9fafb; padding: 20px; border-radius: 8px; margin: 20px 0; }
        .note { background: #fef3c7; padding: 15px; border-radius: 8px; margin: 20px 0; }
        .tabs { margin-bottom: 20px; }
        .tabs a { display: inline-block; padding: 10px 20px; margin-right: 10px; border: 2px solid #3b82f6; border-radius: 8px; text-decoration: none; }
        .tabs a.active { background: #3b82f6; color: white; }
        ol { padding-left: 20px; }
        li { margin-bottom: 10px; }
    </style>
</head>
<body>
    <h1>Install Embyfin Kiosk</h1>

    <div class="tabs">
        <a href="/install/extension" class="active">Browser Extension</a>
        <a href="/install/userscript">Userscript</a>
    </div>

    <p>Direct link: <code><a href="/extension.zip">http://localhost:9999/extension.zip</a></code></p>

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
        </div>
    </div>

    <h2>After Installation</h2>
    <ol>
        <li>Click the extension icon in your browser toolbar</li>
        <li>Verify the server URL is <code>http://localhost:9999</code></li>
        <li>Navigate to your Emby or Jellyfin server</li>
        <li>Click play on any movie or episode, or press <strong>K</strong></li>
    </ol>

    <p style="margin-top: 40px;"><a href="/config">Configuration</a></p>
</body>
</html>`
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(html))
}

func installUserscriptHandler(w http.ResponseWriter, r *http.Request) {
	// Handle POST to save server URLs
	if r.Method == "POST" {
		r.ParseForm()
		urls := r.Form["server_url"]
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
		http.Redirect(w, r, "/install/userscript?saved=1", http.StatusSeeOther)
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

	savedMsg := ""
	if r.URL.Query().Get("saved") == "1" {
		savedMsg = `<span style="color: green; margin-left: 10px;">Saved!</span>`
	}

	html := `<!DOCTYPE html>
<html>
<head>
    <title>Install Userscript - Embyfin Kiosk</title>
    <style>
        body { font-family: system-ui, sans-serif; max-width: 800px; margin: 50px auto; padding: 20px; line-height: 1.6; }
        h1 { margin-bottom: 30px; }
        h3 { margin-top: 20px; color: #555; }
        code { background: #f1f5f9; padding: 2px 8px; border-radius: 4px; font-size: 14px; }
        a { color: #3b82f6; }
        .browser-section { background: #f9fafb; padding: 20px; border-radius: 8px; margin: 20px 0; }
        .tabs { margin-bottom: 20px; }
        .tabs a { display: inline-block; padding: 10px 20px; margin-right: 10px; border: 2px solid #3b82f6; border-radius: 8px; text-decoration: none; }
        .tabs a.active { background: #3b82f6; color: white; }
        ol, ul { padding-left: 20px; }
        li { margin-bottom: 10px; }
        .url-input { width: 100%; padding: 8px; margin: 5px 0; border: 1px solid #ccc; border-radius: 4px; font-size: 14px; box-sizing: border-box; }
        .url-list { margin: 10px 0; }
        .add-url-btn { background: #e5e7eb; border: none; padding: 8px 16px; border-radius: 4px; cursor: pointer; margin-top: 5px; }
        .add-url-btn:hover { background: #d1d5db; }
        .save-btn { background: #3b82f6; color: white; border: none; padding: 10px 20px; border-radius: 4px; cursor: pointer; margin-top: 10px; }
        .save-btn:hover { background: #2563eb; }
        .discover-btn { background: #8b5cf6; color: white; border: none; padding: 10px 20px; border-radius: 4px; cursor: pointer; margin-bottom: 15px; }
        .discover-btn:hover { background: #7c3aed; }
        .discover-btn:disabled { background: #c4b5fd; cursor: wait; }
        .reset-btn { background: #6b7280; color: white; border: none; padding: 10px 20px; border-radius: 4px; cursor: pointer; margin-left: 10px; margin-bottom: 15px; }
        .reset-btn:hover { background: #4b5563; }
        #discoverStatus { margin-left: 10px; color: #666; }
        .install-btn { display: inline-block; padding: 12px 24px; background: #10b981; color: white; text-decoration: none; border-radius: 6px; font-size: 16px; }
        .install-btn:hover { background: #059669; color: white; }
    </style>
</head>
<body>
    <h1>Install Embyfin Kiosk</h1>

    <div class="tabs">
        <a href="/install/extension">Browser Extension</a>
        <a href="/install/userscript" class="active">Userscript</a>
    </div>

    <div class="browser-section">
        <h3>Step 1: Install a Userscript Manager</h3>
        <p>If you don't already have one:</p>
        <ul>
            <li><strong>Tampermonkey</strong> - Chrome, Firefox, Edge, Safari</li>
            <li><strong>Violentmonkey</strong> - Chrome, Firefox, Edge</li>
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
            ` + savedMsg + `
        </form>
    </div>

    <div class="browser-section">
        <h3>Step 3: Install the Userscript</h3>
        <a href="/embyfin-kiosk.user.js" class="install-btn">Install Userscript</a>
        <p style="margin-top: 10px; font-size: 13px; color: #666;">If you change the server URLs, reinstall the userscript to pick up the changes.</p>
        <div id="installStatus" style="margin-top: 15px;"></div>
    </div>

    <h2>After Installation</h2>
    <ol>
        <li>Navigate to your Emby or Jellyfin server</li>
        <li>Click play on any movie or episode, or press <strong>K</strong></li>
    </ol>

    <p style="margin-top: 40px;"><a href="/config">Configuration</a></p>

    <script>
        function addUrlInput() {
            const input = document.createElement('input');
            input.type = 'text';
            input.name = 'server_url';
            input.placeholder = 'http://myserver:8096/*';
            input.className = 'url-input';
            document.getElementById('urlList').appendChild(input);
        }

        async function discoverServers() {
            const btn = document.querySelector('.discover-btn');
            const status = document.getElementById('discoverStatus');

            btn.disabled = true;
            status.textContent = 'Scanning network...';
            status.style.color = '#666';

            try {
                const response = await fetch('/api/discover');
                const data = await response.json();
                const servers = data.servers || [];

                if (servers.length > 0) {
                    const urlList = document.getElementById('urlList');
                    const existing = new Set();
                    urlList.querySelectorAll('input').forEach(input => {
                        if (input.value) existing.add(input.value);
                    });

                    // Remove empty placeholder if present
                    const inputs = urlList.querySelectorAll('input');
                    if (inputs.length === 1 && !inputs[0].value) {
                        inputs[0].remove();
                    }

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
            } catch (err) {
                status.textContent = 'Discovery failed: ' + err.message;
                status.style.color = 'red';
            }

            btn.disabled = false;
            setTimeout(() => { status.textContent = ''; }, 5000);
        }

        // Check if startup auto-discovery is still running
        (async function checkStartupDiscovery() {
            const response = await fetch('/api/discover?status=1');
            const data = await response.json();

            if (data.status === 'scanning') {
                const status = document.getElementById('discoverStatus');
                status.textContent = 'Auto-discovery in progress...';
                status.style.color = '#666';
                setTimeout(async () => {
                    const r = await fetch('/api/discover?status=1');
                    const d = await r.json();
                    if (d.status === 'complete') {
                        status.textContent = '';
                        // Reload page to show discovered servers
                        if (d.servers && d.servers.length > 0) {
                            window.location.reload();
                        }
                    } else {
                        checkStartupDiscovery();
                    }
                }, 500);
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

        // Check if userscript is installed
        (function checkInstalled() {
            const statusDiv = document.getElementById('installStatus');
            // Listen for event from userscript
            document.addEventListener('embyfin-kiosk-installed', function() {
                statusDiv.innerHTML = '<span style="color: #10b981; font-weight: 500;">✓ Userscript is installed and active</span>';
            });
            // Also check window flag after a short delay
            setTimeout(() => {
                if (window.embyfinKioskInstalled) {
                    statusDiv.innerHTML = '<span style="color: #10b981; font-weight: 500;">✓ Userscript is installed and active</span>';
                }
            }, 500);
        })();
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

	// If just checking status (not starting new discovery)
	if r.URL.Query().Get("status") == "1" {
		discoveryMu.Lock()
		running := discoveryRunning
		discoveryMu.Unlock()

		configMu.RLock()
		urls := config.ServerURLs
		configMu.RUnlock()

		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  map[bool]string{true: "scanning", false: "complete"}[running],
			"servers": urls,
		})
		return
	}

	// Run discovery synchronously and return results
	servers := runDiscovery(false)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "complete",
		"servers": servers,
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
	// Parse command-line flags
	var portFlag int
	flag.IntVar(&portFlag, "port", 0, "Port to listen on (overrides config)")
	flag.Parse()

	// Determine config path (same directory as executable)
	exe, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	configPath = filepath.Join(filepath.Dir(exe), "config.json")

	if err := loadConfig(); err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Override port if specified on command line
	if portFlag > 0 {
		config.Port = portFlag
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
	http.HandleFunc("/help/mappings", helpMappingsHandler)
	http.HandleFunc("/install", installPageHandler)
	http.HandleFunc("/install/extension", installExtensionHandler)
	http.HandleFunc("/install/userscript", installUserscriptHandler)
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
