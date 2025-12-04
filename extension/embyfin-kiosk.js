// Embyfin Kiosk - Main script
// Loaded dynamically by the userscript stub

(function() {
    'use strict';

    const KIOSK_SERVER = window.EMBYFIN_KIOSK_SERVER || 'http://localhost:9998';

    // Modal state
    let modalElement = null;
    let statusElement = null;
    let pollInterval = null;
    let currentItemId = null;
    let lastKnownPosition = 0;
    let lastKnownDuration = 0;

    // Create and show the modal overlay
    function showModal(message) {
        if (modalElement) return; // Already showing

        modalElement = document.createElement('div');
        modalElement.id = 'embyfin-kiosk-modal';
        modalElement.innerHTML = `
            <style>
                #embyfin-kiosk-modal {
                    position: fixed;
                    top: 0;
                    left: 0;
                    right: 0;
                    bottom: 0;
                    background: rgba(0, 0, 0, 0.85);
                    z-index: 999999;
                    display: flex;
                    align-items: center;
                    justify-content: center;
                }
                #embyfin-kiosk-modal .modal-box {
                    background: #1a1a1a;
                    border: 1px solid #333;
                    border-radius: 8px;
                    padding: 40px 60px;
                    text-align: center;
                    color: #fff;
                    font-family: system-ui, sans-serif;
                    max-width: 500px;
                }
                #embyfin-kiosk-modal .modal-title {
                    font-size: 24px;
                    margin-bottom: 20px;
                }
                #embyfin-kiosk-modal .modal-status {
                    font-size: 16px;
                    color: #aaa;
                    margin-bottom: 30px;
                }
                #embyfin-kiosk-modal .modal-hint {
                    font-size: 13px;
                    color: #666;
                }
                #embyfin-kiosk-modal .modal-error {
                    color: #ff6b6b;
                }
                #embyfin-kiosk-modal .spinner {
                    width: 40px;
                    height: 40px;
                    border: 3px solid #333;
                    border-top-color: #00a4dc;
                    border-radius: 50%;
                    animation: spin 1s linear infinite;
                    margin: 0 auto 20px;
                }
                @keyframes spin {
                    to { transform: rotate(360deg); }
                }
            </style>
            <div class="modal-box">
                <div class="spinner"></div>
                <div class="modal-title">Playing in External Player</div>
                <div class="modal-status">${message}</div>
                <div class="modal-hint">Press <strong>Escape</strong> to stop playback and return to Emby</div>
            </div>
        `;
        document.body.appendChild(modalElement);
        statusElement = modalElement.querySelector('.modal-status');

        // Listen for escape key
        document.addEventListener('keydown', handleModalKeydown);

        // Start polling for status
        startStatusPolling();
    }

    // Update the modal status message
    function updateModalStatus(message, isError) {
        if (statusElement) {
            statusElement.textContent = message;
            if (isError) {
                statusElement.classList.add('modal-error');
            } else {
                statusElement.classList.remove('modal-error');
            }
        }
    }

    // Hide and cleanup the modal
    function hideModal() {
        if (pollInterval) {
            clearInterval(pollInterval);
            pollInterval = null;
        }
        document.removeEventListener('keydown', handleModalKeydown);
        if (modalElement) {
            modalElement.remove();
            modalElement = null;
            statusElement = null;
        }
    }

    // Handle escape key to stop playback
    function handleModalKeydown(event) {
        if (event.key === 'Escape') {
            event.preventDefault();
            stopPlayback();
        }
    }

    // Stop the external player
    function stopPlayback() {
        updateModalStatus('Stopping playback...');
        fetch(KIOSK_SERVER + '/api/stop', { method: 'POST' })
            .then(() => hideModal())
            .catch(() => hideModal());
    }

    // Poll the server for playback status
    function startStatusPolling() {
        pollInterval = setInterval(() => {
            fetch(KIOSK_SERVER + '/api/status')
                .then(response => response.json())
                .then(status => {
                    if (!status.playing) {
                        // Playback ended - server reports progress to Emby
                        hideModal();
                    } else {
                        // Track position for progress reporting
                        if (status.position !== undefined) {
                            lastKnownPosition = status.position;
                        }
                        if (status.duration !== undefined) {
                            lastKnownDuration = status.duration;
                        }

                        if (status.position !== undefined && status.duration !== undefined) {
                            const posMin = Math.floor(status.position / 60);
                            const posSec = Math.floor(status.position % 60);
                            const durMin = Math.floor(status.duration / 60);
                            const durSec = Math.floor(status.duration % 60);
                            const posStr = `${posMin}:${posSec.toString().padStart(2, '0')}`;
                            const durStr = `${durMin}:${durSec.toString().padStart(2, '0')}`;
                            updateModalStatus(`Playing... ${posStr} / ${durStr}`);
                        } else if (status.position !== undefined) {
                            const mins = Math.floor(status.position / 60);
                            const secs = Math.floor(status.position % 60);
                            updateModalStatus(`Playing... ${mins}:${secs.toString().padStart(2, '0')}`);
                        }
                    }
                })
                .catch(() => {
                    // Server not responding, assume playback stopped
                    hideModal();
                });
        }, 1000);
    }

    // Send play request to local kiosk server
    function playInExternalPlayer(path, itemId, isResume) {
        // Track item for progress reporting
        currentItemId = itemId;
        lastKnownPosition = 0;
        lastKnownDuration = 0;

        // Show modal immediately
        showModal('Launching player...');

        // Get Emby credentials for progress reporting
        const apiBase = getApiBase();
        const serverUrl = window.location.origin + apiBase;
        const userId = window.ApiClient && window.ApiClient.getCurrentUserId ? window.ApiClient.getCurrentUserId() : '';
        const token = window.ApiClient && window.ApiClient.accessToken ? window.ApiClient.accessToken() : '';

        let url = KIOSK_SERVER + '/api/play?path=' + encodeURIComponent(path);
        if (itemId) url += '&itemId=' + encodeURIComponent(itemId);
        if (serverUrl) url += '&serverUrl=' + encodeURIComponent(serverUrl);
        if (userId) url += '&userId=' + encodeURIComponent(userId);
        if (token) url += '&token=' + encodeURIComponent(token);
        if (isResume) url += '&resume=1';

        fetch(url)
            .then(response => {
                if (response.ok) {
                    console.log('Embyfin Kiosk: Playing in external player');
                    updateModalStatus('Playing...');
                } else {
                    console.error('Embyfin Kiosk: Server error', response.status);
                    updateModalStatus('Server error: ' + response.status, true);
                    setTimeout(hideModal, 3000);
                }
            })
            .catch(error => {
                console.error('Embyfin Kiosk: Failed to connect', error);
                updateModalStatus('Could not connect to server. Is embyfin-kiosk.exe running?', true);
                setTimeout(hideModal, 3000);
            });
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
        const apiBase = getApiBase();
        const userId = window.ApiClient && window.ApiClient.getCurrentUserId ? window.ApiClient.getCurrentUserId() : null;
        const token = window.ApiClient && window.ApiClient.accessToken ? window.ApiClient.accessToken() : null;

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
    async function handlePlayClick(event, isResume) {
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
                console.log('Embyfin Kiosk: Playing', path, 'isResume:', isResume);
                playInExternalPlayer(path, itemId, isResume);
            }
        } catch (err) {
            console.error('Embyfin Kiosk: Error getting item path', err);
        }
    }

    // Add click listeners to play buttons
    function attachPlayListeners() {
        const resumeSelectors = [
            '[data-action="resume"]',
            '.btnResume'
        ];
        const playSelectors = [
            '.btnPlay',
            '.playButton',
            'button[data-action="play"]',
            '.detailButton-primary',
            ...resumeSelectors
        ];

        document.addEventListener('click', function(event) {
            const target = event.target.closest(playSelectors.join(','));
            if (target) {
                // btnResume -> resume, btnPlay -> play from beginning
                const isResume = event.target.closest(resumeSelectors.join(',')) !== null;
                handlePlayClick(event, isResume);
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
                            playInExternalPlayer(path, urlMatch[1]);
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
                        // Check for resume position - if startPositionTicks > 0, it's a resume
                        let startPositionTicks = 0;
                        let isResume = false;
                        if (options && options.startPositionTicks && options.startPositionTicks > 0) {
                            startPositionTicks = options.startPositionTicks;
                            isResume = true;
                        }
                        console.log('Embyfin Kiosk: startPositionTicks =', startPositionTicks, 'isResume =', isResume);

                        // Dispatch event for userscript to handle
                        const event = new CustomEvent('embyfin-kiosk-play', {
                            detail: { itemId: itemId, startPositionTicks: startPositionTicks, isResume: isResume }
                        });
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
            const startPositionTicks = e.detail.startPositionTicks || 0;
            const isResume = e.detail.isResume || false;
            console.log('Embyfin Kiosk: Received play event for', itemId, 'startPositionTicks:', startPositionTicks, 'isResume:', isResume);
            try {
                const path = await getItemPath(itemId);
                if (path) {
                    console.log('Embyfin Kiosk: Playing externally', path);
                    playInExternalPlayer(path, itemId, isResume);
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
