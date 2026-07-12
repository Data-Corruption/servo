// Server Actions
// Stop, restart, and restart polling functionality

import { blockClicks, unblockClicks, showError } from './ui.js';
import { postJSON } from './api.js';

/** Stop the server */
export function stopServer() {
    blockClicks();
    postJSON('/settings/stop', {})
        .then(() => {
            // Replace title and body, keeping stylesheets loaded
            document.title = 'Server Stopped';
            document.body.className = 'bg-base-100 min-h-screen flex items-center justify-center';
            document.body.innerHTML = `
                <div class="text-center">
                    <h1 class="text-2xl font-bold mb-2">Server Stopped</h1>
                    <p class="text-base-content/70">You can close this tab.</p>
                </div>
            `;
        })
        .catch(err => {
            unblockClicks();
            showError(`Error: ${err.message}`);
        });
}

/** Restart the server with options from the restart modal */
export function restartServer() {
    // --- BEGIN REMOTE UPDATE ---
    const updateRequested = document.getElementById('restart-update')?.checked ?? false;
    // --- END REMOTE UPDATE ---

    // Close the modal
    document.getElementById('restart-modal').close();

    blockClicks();
    postJSON('/settings/restart', { update: updateRequested })
        .then(() => {
            // Server is restarting, poll for it to come back
            setTimeout(() => pollForRestart(updateRequested), 3000);
        })
        .catch(err => {
            unblockClicks();
            showError(`Error: ${err.message}`);
        });
}

/** Poll for server restart completion */
export function pollForRestart(updateRequested = false) {
    const startTime = Date.now();
    const pollInterval = 3000;
    const timeout = 300000; // 5 minutes

    const check = () => {
        if (Date.now() - startTime > timeout) {
            unblockClicks();
            showError('Restart timed out. Please check logs or try again.');
            return;
        }

        fetch(`/settings/restart-status?t=${Date.now()}`)
            .then(res => res.json())
            .then(data => {
                if (data.restarted) {
                    // --- BEGIN REMOTE UPDATE ---
                    if (updateRequested && !data.updated) {
                        console.warn('Restart detected but not updated.', data);
                        unblockClicks();
                        showError('Restart completed, but the update did not apply. You may already be on the latest version, or the update failed.');
                        return;
                    }
                    // --- END REMOTE UPDATE ---
                    window.location.reload();
                } else {
                    setTimeout(check, pollInterval);
                }
            })
            .catch(() => {
                // Network error during polling - server might be restarting
                setTimeout(check, pollInterval);
            });
    };

    check();
}

/** Wire up server control buttons and modals */
export function initServerControls() {
    document.getElementById('settings-stop-btn')?.addEventListener('click', () => {
        document.getElementById('stop-modal')?.showModal();
    });
    document.getElementById('settings-restart-btn')?.addEventListener('click', () => {
        document.getElementById('restart-modal')?.showModal();
    });
    document.getElementById('stop-modal-confirm')?.addEventListener('click', stopServer);
    document.getElementById('restart-modal-confirm')?.addEventListener('click', restartServer);
}
