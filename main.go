package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

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
	Port         int                     `json:"port"`
	Player       string                  `json:"player"` // "mpv" or "vlc"
	Players      map[string]PlayerConfig `json:"players"`
	PathMappings []PathMapping           `json:"path_mappings"`
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

	http.HandleFunc("/api/play", playHandler)
	http.HandleFunc("/api/config", configAPIHandler)
	http.HandleFunc("/config", configPageHandler)

	addr := fmt.Sprintf("127.0.0.1:%d", config.Port)
	log.Printf("Starting server on %s", addr)
	log.Printf("Config page: http://%s/config", addr)
	log.Printf("Play endpoint: http://%s/api/play?path=...", addr)

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(err)
	}
}
