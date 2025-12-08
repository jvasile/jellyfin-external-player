// JF External Player - Jellyfin external player integration
// Intercepts video playback and redirects to external player (mpv)

(function() {
    'use strict';

    const KIOSK_SERVER = '{{KIOSK_SERVER}}';
    const DEBUG = {{DEBUG}};
    const PREF_KEY_PREFIX = 'jellyfin-external-player-';

    function debugLog(...args) {
        if (DEBUG) console.log('JF External Player:', ...args);
    }

    console.log('JF External Player: Script loaded. Server URL:', KIOSK_SERVER);

    // Modal state
    let modalElement = null;
    let statusElement = null;
    let pollInterval = null;
    let currentItemId = null;
    let lastKnownPosition = 0;
    let lastKnownDuration = 0;
    let bypassUntil = 0; // Timestamp until which we should not intercept

    // Create and show the modal overlay
    function showModal(message) {
        if (modalElement) return;

        modalElement = document.createElement('div');
        modalElement.id = 'jellyfin-external-player-modal';
        modalElement.innerHTML = `
            <style>
                #jellyfin-external-player-modal {
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
                #jellyfin-external-player-modal .modal-box {
                    background: #1a1a1a;
                    border: 1px solid #333;
                    border-radius: 8px;
                    padding: 40px 60px;
                    text-align: center;
                    color: #fff;
                    font-family: system-ui, sans-serif;
                    max-width: 500px;
                }
                #jellyfin-external-player-modal .modal-title {
                    font-size: 24px;
                    margin-bottom: 20px;
                }
                #jellyfin-external-player-modal .modal-status {
                    font-size: 16px;
                    color: #aaa;
                    margin-bottom: 30px;
                }
                #jellyfin-external-player-modal .modal-hint {
                    font-size: 13px;
                    color: #666;
                }
                #jellyfin-external-player-modal .modal-error {
                    color: #ff6b6b;
                }
                #jellyfin-external-player-modal .spinner {
                    width: 40px;
                    height: 40px;
                    border: 3px solid #333;
                    border-top-color: #00a4dc;
                    border-radius: 50%;
                    animation: jf-spin 1s linear infinite;
                    margin: 0 auto 20px;
                }
                @keyframes jf-spin {
                    to { transform: rotate(360deg); }
                }
            </style>
            <div class="modal-box">
                <div class="spinner"></div>
                <div class="modal-title">Playing in External Player</div>
                <div class="modal-status">${message}</div>
                <div class="modal-hint">Press <strong>Escape</strong> to stop playback and return to Jellyfin</div>
            </div>
        `;
        document.body.appendChild(modalElement);
        statusElement = modalElement.querySelector('.modal-status');

        document.addEventListener('keydown', handleModalKeydown, true);
        startStatusPolling();
    }

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

    function hideModal(stopPlayer = true) {
        debugLog('hideModal called, stopPlayer:', stopPlayer);
        if (pollInterval) {
            clearInterval(pollInterval);
            pollInterval = null;
        }
        document.removeEventListener('keydown', handleModalKeydown, true);
        if (modalElement) {
            modalElement.remove();
            modalElement = null;
            statusElement = null;
        }
        if (stopPlayer) {
            debugLog('Sending /api/stop request');
            fetch(KIOSK_SERVER + '/api/stop', { method: 'POST' })
                .then(() => debugLog('/api/stop succeeded'))
                .catch(err => debugLog('/api/stop failed:', err));
        }
    }

    function handleModalKeydown(event) {
        if (event.key === 'Escape') {
            debugLog('Escape key pressed');
            event.preventDefault();
            event.stopPropagation();
            stopPlayback();
        }
    }

    function stopPlayback() {
        debugLog('stopPlayback called');
        updateModalStatus('Stopping playback...');
        hideModal(); // hideModal() will call /api/stop
    }

    function startStatusPolling() {
        pollInterval = setInterval(() => {
            fetch(KIOSK_SERVER + '/api/status')
                .then(response => response.json())
                .then(status => {
                    if (!status.playing) {
                        debugLog('Status poll: playing=false, closing modal');
                        hideModal(false); // Player already stopped
                    } else {
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
                .catch(err => {
                    debugLog('Status poll failed:', err);
                    hideModal(false); // Server unreachable
                });
        }, 1000);
    }

    // Send play request to local kiosk server
    function playInExternalPlayer(path, itemId, isResume) {
        currentItemId = itemId;
        lastKnownPosition = 0;
        lastKnownDuration = 0;

        showModal('Launching player...');

        // Get Jellyfin credentials for progress reporting
        const serverUrl = window.location.origin;
        const userId = window.ApiClient && window.ApiClient.getCurrentUserId ? window.ApiClient.getCurrentUserId() : '';
        const token = window.ApiClient && window.ApiClient.accessToken ? window.ApiClient.accessToken() : '';

        // Construct stream URL as fallback when no path mapping matches
        const streamUrl = `${serverUrl}/Videos/${itemId}/stream?static=true&api_key=${encodeURIComponent(token)}`;

        let url = KIOSK_SERVER + '/api/play?path=' + encodeURIComponent(path);
        url += '&streamUrl=' + encodeURIComponent(streamUrl);
        if (itemId) url += '&itemId=' + encodeURIComponent(itemId);
        if (serverUrl) url += '&serverUrl=' + encodeURIComponent(serverUrl);
        if (userId) url += '&userId=' + encodeURIComponent(userId);
        if (token) url += '&token=' + encodeURIComponent(token);
        if (isResume) url += '&resume=1';

        fetch(url)
            .then(response => {
                if (response.ok) {
                    console.log('JF External Player: Playing in external player');
                    updateModalStatus('Playing...');
                } else {
                    console.error('JF External Player: Server error', response.status);
                    updateModalStatus('Server error: ' + response.status, true);
                    setTimeout(hideModal, 3000);
                }
            })
            .catch(error => {
                console.error('JF External Player: Failed to connect', error);
                updateModalStatus('Could not connect to server. Is jellyfin-external-player running?', true);
                setTimeout(hideModal, 3000);
            });
    }

    // Check if this looks like a Jellyfin page
    function isJellyfinPage() {
        return document.querySelector('.skinHeader') !== null ||
               document.querySelector('#indexPage') !== null ||
               window.location.pathname.includes('/web/') ||
               typeof window.ApiClient !== 'undefined';
    }

    // Video types we should intercept (everything else uses native Jellyfin)
    const VIDEO_TYPES = ['Movie', 'Episode', 'MusicVideo', 'Video', 'Trailer'];
    // Container types that should be expanded into playlists
    const CONTAINER_TYPES = ['Season', 'Series', 'Playlist', 'BoxSet'];

    // Fetch item details from Jellyfin API - returns {type, path} or null
    async function getItemInfo(itemId) {
        const userId = window.ApiClient && window.ApiClient.getCurrentUserId ? window.ApiClient.getCurrentUserId() : null;
        const token = window.ApiClient && window.ApiClient.accessToken ? window.ApiClient.accessToken() : null;

        if (!userId || !token) {
            throw new Error('Not authenticated');
        }

        const url = `${window.location.origin}/Users/${userId}/Items/${itemId}?api_key=${encodeURIComponent(token)}`;

        const response = await fetch(url);
        if (!response.ok) {
            throw new Error(`API request failed: ${response.status}`);
        }

        return await response.json();
    }

    // Legacy function for single video playback
    async function getItemPath(itemId) {
        const data = await getItemInfo(itemId);

        // Only intercept video types
        if (!VIDEO_TYPES.includes(data.Type)) {
            debugLog('Skipping non-video type:', data.Type, 'for item:', itemId);
            return null;
        }

        return data.Path;
    }

    // Fetch all child episodes for a container (Season, Series, etc.)
    async function getChildEpisodes(itemId, itemType) {
        const userId = window.ApiClient && window.ApiClient.getCurrentUserId ? window.ApiClient.getCurrentUserId() : null;
        const token = window.ApiClient && window.ApiClient.accessToken ? window.ApiClient.accessToken() : null;

        if (!userId || !token) {
            throw new Error('Not authenticated');
        }

        // Build query to get child items
        let url = `${window.location.origin}/Users/${userId}/Items?api_key=${encodeURIComponent(token)}`;
        url += `&ParentId=${encodeURIComponent(itemId)}`;
        url += `&IncludeItemTypes=Episode`;
        url += `&Recursive=true`;
        url += `&SortBy=SortName`;
        url += `&SortOrder=Ascending`;
        url += `&Fields=Path`;

        const response = await fetch(url);
        if (!response.ok) {
            throw new Error(`API request failed: ${response.status}`);
        }

        const data = await response.json();
        debugLog('Found', data.Items.length, 'episodes for', itemType, itemId);

        return data.Items.map(item => ({
            path: item.Path,
            itemId: item.Id,
            streamUrl: `${window.location.origin}/Videos/${item.Id}/stream?static=true&api_key=${encodeURIComponent(token)}`
        }));
    }

    // Play a playlist via the server
    async function playPlaylist(items, isResume) {
        if (!items || items.length === 0) {
            console.error('JF External Player: Empty playlist');
            return;
        }

        showModal(`Loading playlist (${items.length} items)...`);

        const serverUrl = window.location.origin;
        const userId = window.ApiClient && window.ApiClient.getCurrentUserId ? window.ApiClient.getCurrentUserId() : '';
        const token = window.ApiClient && window.ApiClient.accessToken ? window.ApiClient.accessToken() : '';

        // Ensure each item has a streamUrl fallback
        const itemsWithStream = items.map(item => ({
            ...item,
            streamUrl: item.streamUrl || `${serverUrl}/Videos/${item.itemId}/stream?static=true&api_key=${encodeURIComponent(token)}`
        }));

        const payload = {
            items: itemsWithStream,
            serverUrl: serverUrl,
            userId: userId,
            token: token,
            resume: isResume
        };

        try {
            const response = await fetch(KIOSK_SERVER + '/api/playlist', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(payload)
            });

            if (!response.ok) {
                throw new Error(`Server returned ${response.status}`);
            }

            const result = await response.json();
            debugLog('Playlist started:', result);
            updateModalStatus(`Playing playlist (${items.length} items)...`);
        } catch (err) {
            console.error('JF External Player: Playlist error:', err);
            updateModalStatus('Failed to start playlist: ' + err.message, true);
            setTimeout(() => hideModal(false), 3000);
        }
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

        const card = element.closest('.card');
        if (card) {
            if (card.dataset.id) return card.dataset.id;
            if (card.dataset.itemid) return card.dataset.itemid;
            if (card.dataset.itemId) return card.dataset.itemId;

            const inner = card.querySelector('[data-id], [data-itemid]');
            if (inner) {
                return inner.dataset.id || inner.dataset.itemid;
            }

            const img = card.querySelector('img[src*="/Items/"]');
            if (img) {
                const imgMatch = img.src.match(/\/Items\/(\d+)\//);
                if (imgMatch) {
                    return imgMatch[1];
                }
            }
        }

        const actionBtn = element.closest('[data-itemid], [data-id]');
        if (actionBtn) {
            return actionBtn.dataset.itemid || actionBtn.dataset.id;
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
    async function handlePlayClick(event, isResume) {
        // Check for bypass flag (re-triggered click for non-video)
        if (event.target.dataset && event.target.dataset.jfExternalBypass) {
            return; // Let native handler process this
        }

        let itemId = extractItemId(event.target);

        if (!itemId) {
            const urlMatch = window.location.hash.match(/id=([a-f0-9]+)/i);
            if (urlMatch) {
                itemId = urlMatch[1];
            }
        }

        if (!itemId) {
            return;
        }

        event.preventDefault();
        event.stopPropagation();
        event.stopImmediatePropagation();

        try {
            const itemInfo = await getItemInfo(itemId);
            debugLog('Item info:', itemInfo.Type, itemInfo.Name);

            if (VIDEO_TYPES.includes(itemInfo.Type)) {
                // Single video - play directly
                console.log('JF External Player: Playing', itemInfo.Path, 'isResume:', isResume);
                playInExternalPlayer(itemInfo.Path, itemId, isResume);
            } else if (CONTAINER_TYPES.includes(itemInfo.Type)) {
                // Container (Season, Series, etc.) - get child episodes and play as playlist
                debugLog('Expanding container:', itemInfo.Type);
                const episodes = await getChildEpisodes(itemId, itemInfo.Type);
                if (episodes.length > 0) {
                    console.log('JF External Player: Playing playlist of', episodes.length, 'episodes');
                    await playPlaylist(episodes, isResume);
                } else {
                    console.error('JF External Player: No episodes found in', itemInfo.Type);
                }
            } else {
                // Unknown type - let native Jellyfin handle it
                debugLog('Re-triggering click for native handling, type:', itemInfo.Type);
                const target = event.target;
                target.dataset.jfExternalBypass = 'true';
                target.click();
                delete target.dataset.jfExternalBypass;
            }
        } catch (err) {
            console.error('JF External Player: Error:', err);
        }
    }

    // Add click listeners to play buttons
    function attachPlayListeners() {
        const playSelectors = [
            '.btnPlay',
            '.btnReplay',
            '.btnResume',
            '.playButton',
            'button[data-action="play"]',
            '[data-action="resume"]',
            '.detailButton-primary',
            '.cardOverlayPlayButton',
            '.itemAction[data-action="play"]'
        ];

        document.addEventListener('click', function(event) {
            const target = event.target.closest(playSelectors.join(','));
            if (target) {
                // Use data-action attribute to determine resume vs play-from-start
                // data-action="resume" means resume, data-action="play" means from start
                const action = target.getAttribute('data-action');
                const isResume = action === 'resume';
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
                            console.log('JF External Player: Playing via keyboard shortcut', path);
                            playInExternalPlayer(path, urlMatch[1], true);
                        }
                    } catch (err) {
                        console.error('JF External Player: Error', err);
                    }
                }
            }
        });
    }

    // Hook into Jellyfin's playback system
    function hookPlaybackManager() {
        const script = document.createElement('script');
        script.textContent = `
        (function() {
            let hooked = false;

            function doHook(playbackManager) {
                if (hooked) return;
                const originalPlay = playbackManager.play.bind(playbackManager);
                playbackManager.play = async function(options) {
                    console.log('JF External Player: Intercepted PlaybackManager.play', options);

                    let itemId = null;
                    if (options && options.ids && options.ids.length > 0) {
                        itemId = options.ids[0];
                    } else if (options && options.items && options.items.length > 0) {
                        itemId = options.items[0].Id;
                    }

                    if (itemId) {
                        let startPositionTicks = options && options.startPositionTicks ? options.startPositionTicks : 0;
                        let isResume = true;
                        console.log('JF External Player: startPositionTicks =', startPositionTicks, 'isResume =', isResume);

                        const event = new CustomEvent('jellyfin-external-player-play', {
                            detail: { itemId: itemId, startPositionTicks: startPositionTicks, isResume: isResume }
                        });
                        document.dispatchEvent(event);
                        return;
                    }

                    return originalPlay(options);
                };
                hooked = true;
                console.log('JF External Player: Hooked PlaybackManager.play');
            }

            function tryHook() {
                if (window.PlaybackManager && window.PlaybackManager.play && !hooked) {
                    doHook(window.PlaybackManager);
                    return true;
                }
                return false;
            }

            if (tryHook()) {
                console.log('JF External Player: Hooked immediately');
            } else {
                if (typeof window.PlaybackManager === 'undefined' || window.PlaybackManager === null) {
                    let _pm = undefined;
                    Object.defineProperty(window, 'PlaybackManager', {
                        get: function() { return _pm; },
                        set: function(val) {
                            console.log('JF External Player: PlaybackManager being set to', val);
                            _pm = val;
                            if (val && val.play) doHook(val);
                        },
                        configurable: true
                    });
                    console.log('JF External Player: Installed PlaybackManager trap');
                }

                let attempts = 0;
                const interval = setInterval(() => {
                    if (tryHook()) {
                        console.log('JF External Player: Hooked via polling after', attempts, 'attempts');
                        clearInterval(interval);
                    } else if (++attempts > 60) {
                        console.log('JF External Player: Gave up polling for PlaybackManager');
                        clearInterval(interval);
                    }
                }, 500);
            }
        })();
        `;
        document.documentElement.appendChild(script);
        script.remove();

        document.addEventListener('jellyfin-external-player-play', async function(e) {
            const itemId = e.detail.itemId;
            const startPositionTicks = e.detail.startPositionTicks || 0;
            const isResume = e.detail.isResume !== false;
            console.log('JF External Player: Received play event for', itemId, 'startPositionTicks:', startPositionTicks, 'isResume:', isResume);
            try {
                const path = await getItemPath(itemId);
                if (path) {
                    console.log('JF External Player: Playing externally', path);
                    playInExternalPlayer(path, itemId, isResume);
                }
            } catch (err) {
                console.error('JF External Player: Error getting path', err);
            }
        });
    }

    // Intercept all video playback by overriding HTMLVideoElement.play
    function interceptVideoPlayback() {
        window.addEventListener('message', async function(e) {
            if (e.data && e.data.type === 'jellyfin-external-player-intercept') {
                const itemId = e.data.itemId;
                const src = e.data.src;
                console.log('JF External Player: Handling intercept, itemId:', itemId, 'src:', src);

                document.querySelectorAll('.videoPlayerContainer, .videoOsdBottom, .videoOsd').forEach(el => {
                    el.style.display = 'none';
                });

                history.back();

                if (itemId) {
                    try {
                        const path = await getItemPath(itemId);
                        console.log('JF External Player: Got path:', path);
                        if (path) {
                            playInExternalPlayer(path, itemId, true);
                        } else {
                            console.error('JF External Player: No path returned for item', itemId);
                        }
                    } catch (err) {
                        console.error('JF External Player: Error getting path:', err);
                    }
                } else {
                    console.error('JF External Player: No itemId found to play');
                }
            }
        });

        const script = document.createElement('script');
        script.textContent = `
        (function() {
            let intercepting = false;
            const originalPlay = HTMLVideoElement.prototype.play;

            HTMLVideoElement.prototype.play = function() {
                const src = this.src || '';
                console.log('JF External Player: Video.play() intercepted, src:', src);

                if (intercepting) {
                    return Promise.reject(new Error('Intercepted by JF External Player'));
                }

                // Check if this looks like Jellyfin video playback
                if (src.includes('blob:') || src.includes('/Videos/')) {
                    intercepting = true;

                    let itemId = null;

                    // From video src (e.g., /Videos/12345/stream)
                    const srcMatch = src.match(/\\/Videos\\/([0-9]+)[\\/\\?]/i);
                    if (srcMatch) itemId = srcMatch[1];

                    // From URL hash
                    if (!itemId) {
                        const urlMatch = window.location.hash.match(/(?:id|itemId)=([0-9]+)/i);
                        if (urlMatch) itemId = urlMatch[1];
                    }

                    // From image URLs on the page
                    if (!itemId) {
                        const img = document.querySelector('img[src*="/Items/"]');
                        if (img) {
                            const imgMatch = img.src.match(/\\/Items\\/(\\d+)\\//);
                            if (imgMatch) itemId = imgMatch[1];
                        }
                    }

                    // From PlaybackManager state
                    if (!itemId && window.PlaybackManager) {
                        try {
                            const nowPlaying = window.PlaybackManager.currentItem && window.PlaybackManager.currentItem();
                            if (nowPlaying && nowPlaying.Id) itemId = nowPlaying.Id;
                        } catch(e) {}
                    }

                    console.log('JF External Player: Intercepting playback, itemId:', itemId, 'src:', src, 'hash:', window.location.hash);

                    window.postMessage({
                        type: 'jellyfin-external-player-intercept',
                        itemId: itemId,
                        src: src
                    }, '*');

                    this.pause();
                    this.src = '';

                    setTimeout(() => { intercepting = false; }, 3000);

                    return Promise.reject(new Error('Intercepted by JF External Player'));
                }

                return originalPlay.call(this);
            };

            console.log('JF External Player: Installed video.play() interceptor');
        })();
        `;
        document.documentElement.appendChild(script);
        script.remove();
    }

    // Check if external player is enabled for this user
    function isEnabled() {
        const userId = window.ApiClient && window.ApiClient.getCurrentUserId ? window.ApiClient.getCurrentUserId() : 'default';
        const prefKey = PREF_KEY_PREFIX + userId;
        return localStorage.getItem(prefKey) !== 'false'; // default true
    }

    // Initialize
    async function init() {
        // Check user preference
        if (!isEnabled()) {
            console.log('JF External Player: Disabled by user preference');
            return;
        }

        // Check if kiosk server is running
        try {
            await fetch(KIOSK_SERVER + '/api/status');
        } catch {
            console.log('JF External Player: Server not available at', KIOSK_SERVER);
            return;
        }

        if (!isJellyfinPage()) {
            return;
        }

        console.log('JF External Player: Initializing');
        hookPlaybackManager();
        attachPlayListeners();
        attachKeyboardShortcut();
        interceptVideoPlayback();
    }

    // Wait for page to be ready
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        setTimeout(init, 1000);
    }
})();
