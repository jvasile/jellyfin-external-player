const serverUrlInput = document.getElementById('serverUrl');
const saveBtn = document.getElementById('saveBtn');
const savedMsg = document.getElementById('savedMsg');
const configLink = document.getElementById('configLink');
const statusDot = document.getElementById('statusDot');
const statusText = document.getElementById('statusText');

// Update config link based on server URL
function updateConfigLink() {
    const serverUrl = serverUrlInput.value.replace(/\/+$/, '') || 'http://localhost:9999';
    configLink.href = serverUrl + '/config';
}

// Check server status
function checkServerStatus() {
    const serverUrl = serverUrlInput.value.replace(/\/+$/, '') || 'http://localhost:9999';

    statusDot.classList.remove('connected', 'disconnected');
    statusText.textContent = 'Checking...';

    fetch(`${serverUrl}/api/config`)
        .then(response => {
            if (response.ok) {
                statusDot.classList.add('connected');
                statusText.textContent = 'Server running';
            } else {
                statusDot.classList.add('disconnected');
                statusText.textContent = 'Server error';
            }
        })
        .catch(error => {
            statusDot.classList.add('disconnected');
            statusText.textContent = 'Server not running';
        });
}

// Load saved settings
chrome.runtime.sendMessage({ action: 'getSettings' }, function(response) {
    if (response && response.success) {
        serverUrlInput.value = response.serverUrl || 'http://localhost:9999';
    } else {
        serverUrlInput.value = 'http://localhost:9999';
    }
    updateConfigLink();
    checkServerStatus();
});

// Save settings
saveBtn.addEventListener('click', function() {
    const serverUrl = serverUrlInput.value.replace(/\/+$/, '');

    chrome.runtime.sendMessage({
        action: 'setSettings',
        serverUrl: serverUrl
    }, function(response) {
        if (response && response.success) {
            savedMsg.style.display = 'inline';
            setTimeout(() => {
                savedMsg.style.display = 'none';
            }, 2000);
            checkServerStatus();
        }
    });
});

// Update config link as user types
serverUrlInput.addEventListener('input', updateConfigLink);
