// ==UserScript==
// @name         Embyfin Kiosk
// @namespace    https://github.com/jvasile/embyfin-kiosk
// @version      1.0.0
// @description  Play Emby/Jellyfin videos in external player (mpv/VLC) via local server
// {{INCLUDE_LINES}}
// @grant        GM_xmlhttpRequest
// @grant        GM.xmlHttpRequest
// @grant        unsafeWindow
// @connect      localhost
// @connect      127.0.0.1
// ==/UserScript==

(function() {
    'use strict';

    const KIOSK_SERVER = 'http://localhost:{{PORT}}';

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
        // Direct data-id attribute
        if (element.dataset && element.dataset.id) {
            return element.dataset.id;
        }

        // Look for parent with data-id
        let parent = element.closest('[data-id]');
        if (parent && parent.dataset.id) {
            return parent.dataset.id;
        }

        // Look for the outer .card container (contains the image with item ID)
        const card = element.closest('.card');
        if (card) {
            // Check various attributes Emby uses
            if (card.dataset.id) return card.dataset.id;
            if (card.dataset.itemid) return card.dataset.itemid;
            if (card.dataset.itemId) return card.dataset.itemId;

            // Look for nested element with ID
            const inner = card.querySelector('[data-id], [data-itemid]');
            if (inner) {
                return inner.dataset.id || inner.dataset.itemid;
            }

            // Extract from image URL (e.g., /Items/62257/Images/Primary)
            const img = card.querySelector('img[src*="/Items/"]');
            if (img) {
                const imgMatch = img.src.match(/\/Items\/(\d+)\//);
                if (imgMatch) {
                    console.log('Embyfin Kiosk: Extracted item ID from image URL:', imgMatch[1]);
                    return imgMatch[1];
                }
            }
        }

        // Check for itemAction button attributes
        const actionBtn = element.closest('[data-itemid], [data-id]');
        if (actionBtn) {
            return actionBtn.dataset.itemid || actionBtn.dataset.id;
        }

        // From URL hash
        const urlMatch = window.location.hash.match(/id=([a-f0-9]+)/i);
        if (urlMatch) {
            return urlMatch[1];
        }

        // From href
        if (element.href) {
            const hrefMatch = element.href.match(/id=([a-f0-9]+)/i);
            if (hrefMatch) {
                return hrefMatch[1];
            }
        }

        // Log what we're looking at for debugging
        console.log('Embyfin Kiosk: Could not extract ID from element:', element, 'classes:', element.className);

        // Look for card and check for links or JS data inside
        const debugCard = element.closest('.card');
        if (debugCard) {
            // Check for anchor with item link
            const link = debugCard.querySelector('a[href*="id="]');
            if (link) {
                console.log('Embyfin Kiosk: Found link in card:', link.href);
            }
            // Check card's own properties
            console.log('Embyfin Kiosk: Card dataset:', debugCard.dataset);
            console.log('Embyfin Kiosk: Card item property:', debugCard.item);
            // Log inner HTML structure
            console.log('Embyfin Kiosk: Card innerHTML preview:', debugCard.innerHTML.substring(0, 500));
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
            return; // Already logged in extractItemId
        }

        // Prevent default immediately, before async operations
        event.preventDefault();
        event.stopPropagation();
        event.stopImmediatePropagation();

        try {
            const path = await getItemPath(itemId);
            if (path) {
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

    // Check if we're on the kiosk server pages (for detection)
    function isKioskPage() {
        return window.location.hostname === 'localhost' || window.location.hostname === '127.0.0.1';
    }

    // Signal presence to kiosk pages
    function signalPresence() {
        if (isKioskPage()) {
            window.embyfinKioskInstalled = true;
            document.dispatchEvent(new CustomEvent('embyfin-kiosk-installed'));
        }
    }

    // Hook into Emby/Jellyfin's playback system
    function hookPlaybackManager() {
        // Inject script directly into page to bypass sandbox restrictions
        const script = document.createElement('script');
        script.textContent = `
        (function() {
            let hooked = false;

            function doHook(playbackManager) {
                if (hooked) return;
                const originalPlay = playbackManager.play.bind(playbackManager);
                playbackManager.play = async function(options) {
                    console.log('Embyfin Kiosk: Intercepted PlaybackManager.play', options);

                    // Extract item ID from options
                    let itemId = null;
                    if (options && options.ids && options.ids.length > 0) {
                        itemId = options.ids[0];
                    } else if (options && options.items && options.items.length > 0) {
                        itemId = options.items[0].Id;
                    }

                    if (itemId) {
                        // Dispatch event for userscript to handle
                        const event = new CustomEvent('embyfin-kiosk-play', { detail: { itemId: itemId } });
                        document.dispatchEvent(event);
                        return; // Don't call original play
                    }

                    return originalPlay(options);
                };
                hooked = true;
                console.log('Embyfin Kiosk: Hooked PlaybackManager.play');
            }

            function tryHook() {
                if (window.PlaybackManager && window.PlaybackManager.play && !hooked) {
                    doHook(window.PlaybackManager);
                    return true;
                }
                return false;
            }

            // Check current state
            console.log('Embyfin Kiosk: PlaybackManager exists?', typeof window.PlaybackManager, window.PlaybackManager);

            // Try to hook immediately if it exists
            if (tryHook()) {
                console.log('Embyfin Kiosk: Hooked immediately');
            } else {
                // Use property trap if PlaybackManager not yet defined
                if (typeof window.PlaybackManager === 'undefined' || window.PlaybackManager === null) {
                    let _pm = undefined;
                    Object.defineProperty(window, 'PlaybackManager', {
                        get: function() { return _pm; },
                        set: function(val) {
                            console.log('Embyfin Kiosk: PlaybackManager being set to', val);
                            _pm = val;
                            if (val && val.play) doHook(val);
                        },
                        configurable: true
                    });
                    console.log('Embyfin Kiosk: Installed PlaybackManager trap');
                }

                // Polling backup
                let attempts = 0;
                const interval = setInterval(() => {
                    if (tryHook()) {
                        console.log('Embyfin Kiosk: Hooked via polling after', attempts, 'attempts');
                        clearInterval(interval);
                    } else if (++attempts > 60) {
                        console.log('Embyfin Kiosk: Gave up polling for PlaybackManager');
                        clearInterval(interval);
                    }
                }, 500);
            }
        })();
        `;
        document.documentElement.appendChild(script);
        script.remove();

        // Listen for play events from injected script
        document.addEventListener('embyfin-kiosk-play', async function(e) {
            const itemId = e.detail.itemId;
            console.log('Embyfin Kiosk: Received play event for', itemId);
            try {
                const path = await getItemPath(itemId);
                if (path) {
                    console.log('Embyfin Kiosk: Playing externally', path);
                    playInExternalPlayer(path);
                }
            } catch (err) {
                console.error('Embyfin Kiosk: Error getting path', err);
            }
        });
    }

    // Initialize
    function init() {
        signalPresence();
        if (!isEmbyfinPage()) {
            return;
        }
        console.log('Embyfin Kiosk: Initializing, platform:', detectPlatform());
        hookPlaybackManager();
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
