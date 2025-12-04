// ==UserScript==
// @name         Embyfin Kiosk
// @namespace    https://github.com/jvasile/embyfin-kiosk
// @version      1.0.0
// @description  Play Emby/Jellyfin videos in external player (mpv/VLC) via local server
// {{INCLUDE_LINES}}
// @grant        none
// @run-at       document-start
// ==/UserScript==

(function() {
    'use strict';

    const KIOSK_SERVER = 'http://localhost:{{PORT}}';

    // Set server URL for the main script
    window.EMBYFIN_KIOSK_SERVER = KIOSK_SERVER;

    // Load the main script from the local server
    function loadMainScript() {
        const script = document.createElement('script');
        script.src = KIOSK_SERVER + '/embyfin-kiosk.js';
        script.onerror = function() {
            console.error('Embyfin Kiosk: Could not load script from ' + KIOSK_SERVER + '. Is the server running?');
        };
        document.head.appendChild(script);
    }

    // Wait for head to be available
    if (document.head) {
        loadMainScript();
    } else {
        document.addEventListener('DOMContentLoaded', loadMainScript);
    }
})();
