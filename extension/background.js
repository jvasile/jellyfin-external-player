// Background service worker - handles requests to local kiosk server

const DEFAULT_SERVER = 'http://localhost:9999';

// Get server URL from storage
async function getServerUrl() {
    try {
        const result = await chrome.storage.sync.get(['serverUrl']);
        return result.serverUrl || DEFAULT_SERVER;
    } catch (e) {
        return DEFAULT_SERVER;
    }
}

chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
    if (message.action === 'play') {
        getServerUrl().then(serverUrl => {
            // Path transformation happens server-side now
            const url = `${serverUrl}/api/play?path=${encodeURIComponent(message.path)}`;

            fetch(url)
                .then(response => response.json())
                .then(data => {
                    sendResponse({ success: true, data: data });
                })
                .catch(error => {
                    console.error('Embyfin Kiosk: Error', error);
                    sendResponse({ success: false, error: error.message });
                });
        });

        return true;
    }

    if (message.action === 'setSettings') {
        chrome.storage.sync.set({
            serverUrl: message.serverUrl
        }, () => {
            sendResponse({ success: true });
        });

        return true;
    }

    if (message.action === 'getSettings') {
        getServerUrl().then(serverUrl => {
            sendResponse({ success: true, serverUrl: serverUrl });
        });

        return true;
    }
});
