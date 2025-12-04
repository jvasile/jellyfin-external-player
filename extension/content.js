// Content script injected into Emby/Jellyfin pages
// This file is shared between the browser extension and userscript

(function() {
    'use strict';

    const KIOSK_SERVER = 'http://localhost:9999';

    // ==PLAY_FUNCTION_START==
    // Send play request to local kiosk server via background script
    function playInExternalPlayer(path) {
        chrome.runtime.sendMessage({
            action: 'play',
            path: path
        }, function(response) {
            if (response && response.success) {
                console.log('Embyfin Kiosk: Playing in external player');
            } else {
                console.error('Embyfin Kiosk: Failed to play', response);
                alert('Embyfin Kiosk: Could not connect to local server. Is embyfin-kiosk.exe running?');
            }
        });
    }
    // ==PLAY_FUNCTION_END==

    // Check if this looks like an Emby/Jellyfin page
    function isEmbyfinPage() {
        return document.querySelector('.skinHeader') !== null ||
               document.querySelector('#indexPage') !== null ||
               window.location.pathname.includes('/web/') ||
               typeof window.ApiClient !== 'undefined';
    }

    // Detect which platform we're on
    function detectPlatform() {
        if (window.ApiClient && window.ApiClient.serverInfo) {
            const serverInfo = window.ApiClient.serverInfo();
            if (serverInfo && serverInfo.ServerName) {
                if (serverInfo.ServerName.toLowerCase().includes('jellyfin')) {
                    return 'jellyfin';
                }
            }
        }
        if (document.querySelector('meta[name="application-name"][content*="Jellyfin"]')) {
            return 'jellyfin';
        }
        if (document.querySelector('meta[name="application-name"][content*="Emby"]')) {
            return 'emby';
        }
        if (window.location.hostname.includes('jellyfin')) {
            return 'jellyfin';
        }
        return 'emby';
    }

    // Get the API base path
    function getApiBase() {
        const platform = detectPlatform();
        return platform === 'jellyfin' ? '' : '/emby';
    }

    // Get authentication params
    function getAuthParams() {
        // Check both window and unsafeWindow (for userscript sandboxing)
        const win = (typeof unsafeWindow !== 'undefined') ? unsafeWindow : window;
        const params = [];
        if (win.ApiClient) {
            const token = win.ApiClient.accessToken ? win.ApiClient.accessToken() : null;
            if (token) {
                params.push(`api_key=${encodeURIComponent(token)}`);
            } else if (win.ApiClient._serverInfo && win.ApiClient._serverInfo.AccessToken) {
                params.push(`api_key=${encodeURIComponent(win.ApiClient._serverInfo.AccessToken)}`);
            }
            // Add UserId if available
            const userId = win.ApiClient.getCurrentUserId ? win.ApiClient.getCurrentUserId() : null;
            if (userId) {
                params.push(`UserId=${encodeURIComponent(userId)}`);
            }
        }
        return params.join('&');
    }

    // Fetch item details from API
    async function getItemPath(itemId) {
        const win = (typeof unsafeWindow !== 'undefined') ? unsafeWindow : window;
        const apiBase = getApiBase();
        const userId = win.ApiClient && win.ApiClient.getCurrentUserId ? win.ApiClient.getCurrentUserId() : null;
        const token = win.ApiClient && win.ApiClient.accessToken ? win.ApiClient.accessToken() : null;

        if (!userId || !token) {
            throw new Error('Not authenticated');
        }

        const url = `${window.location.origin}${apiBase}/Users/${userId}/Items/${itemId}?api_key=${encodeURIComponent(token)}`;

        const response = await fetch(url);
        if (!response.ok) {
            throw new Error(`API request failed: ${response.status}`);
        }

        const data = await response.json();
        return data.Path;
    }

    // Extract item ID from URL or element
    function extractItemId(element) {
        if (element.dataset && element.dataset.id) {
            return element.dataset.id;
        }

        let parent = element.closest('[data-id]');
        if (parent && parent.dataset.id) {
            return parent.dataset.id;
        }

        const urlMatch = window.location.hash.match(/id=([a-f0-9]+)/i);
        if (urlMatch) {
            return urlMatch[1];
        }

        if (element.href) {
            const hrefMatch = element.href.match(/id=([a-f0-9]+)/i);
            if (hrefMatch) {
                return hrefMatch[1];
            }
        }

        return null;
    }

    // Check if this is a playable item
    function isPlayableItem() {
        const hash = window.location.hash;
        return hash.includes('/movie') ||
               hash.includes('/episode') ||
               hash.includes('/video') ||
               hash.includes('type=Movie') ||
               hash.includes('type=Episode');
    }

    // Handle play button click
    async function handlePlayClick(event) {
        let itemId = extractItemId(event.target);

        if (!itemId) {
            const urlMatch = window.location.hash.match(/id=([a-f0-9]+)/i);
            if (urlMatch) {
                itemId = urlMatch[1];
            }
        }

        if (!itemId) {
            console.log('Embyfin Kiosk: Could not find item ID');
            return;
        }

        try {
            const path = await getItemPath(itemId);
            if (path) {
                event.preventDefault();
                event.stopPropagation();
                console.log('Embyfin Kiosk: Playing', path);
                playInExternalPlayer(path);
            }
        } catch (err) {
            console.error('Embyfin Kiosk: Error getting item path', err);
        }
    }

    // Add click listeners to play buttons
    function attachPlayListeners() {
        const playSelectors = [
            '.btnPlay',
            '.playButton',
            'button[data-action="play"]',
            '.detailButton-primary',
            '[data-action="resume"]',
            '.btnResume'
        ];

        document.addEventListener('click', function(event) {
            const target = event.target.closest(playSelectors.join(','));
            if (target) {
                handlePlayClick(event);
            }
        }, true);
    }

    // Keyboard shortcut: Press 'k' to play current item
    function attachKeyboardShortcut() {
        document.addEventListener('keydown', async function(event) {
            if (event.key === 'k' && !event.target.matches('input, textarea')) {
                const urlMatch = window.location.hash.match(/id=([a-f0-9]+)/i);
                if (urlMatch && isPlayableItem()) {
                    try {
                        const path = await getItemPath(urlMatch[1]);
                        if (path) {
                            console.log('Embyfin Kiosk: Playing via keyboard shortcut', path);
                            playInExternalPlayer(path);
                        }
                    } catch (err) {
                        console.error('Embyfin Kiosk: Error', err);
                    }
                }
            }
        });
    }

    // Initialize
    function init() {
        if (!isEmbyfinPage()) {
            return;
        }
        console.log('Embyfin Kiosk: Initializing, platform:', detectPlatform());
        attachPlayListeners();
        attachKeyboardShortcut();
    }

    // Wait for page to be ready
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        // Delay slightly to ensure Emby/Jellyfin JS has loaded
        setTimeout(init, 1000);
    }
})();
