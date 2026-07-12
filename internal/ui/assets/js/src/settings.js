// Settings Wiring
// DOMContentLoaded initialization for all settings controls

import { handleSelect, handleTextInput, handleToggle } from './forms.js';
import { postJSON } from './api.js';
import { findStatus, showPending, showSuccess, showError } from './ui.js';

/** Show restart required notice */
function showRestartNotice() {
    const notice = document.getElementById('restart-required-notice');
    if (notice) notice.classList.remove('hidden');
}

// portFromBind extracts the numeric port from a host:port bind string.
function portFromBind(bind) {
    const i = (bind || '').lastIndexOf(':');
    if (i < 0) return 0;
    return parseInt(bind.slice(i + 1), 10) || 0;
}

function privilegedPortWarning() {
    const v = portFromBind(document.getElementById('settings-ui-bind')?.value);
    const notice = document.getElementById('privileged-port-notice');
    if (notice) notice.classList.toggle('hidden', !(v > 0 && v < 1024));
}

/** Wire up settings */
function wireSettings() {
    handleSelect('settings-log-level', '/settings', 'logLevel', showRestartNotice);
    handleTextInput('settings-ui-bind', '/settings', 'uiBind', 800, { onSuccess() { showRestartNotice(); privilegedPortWarning(); } });
    handleTextInput('settings-proxy-bind', '/settings', 'proxyBind', 800, { onSuccess: showRestartNotice });

    // game server schedule (applied live, no daemon restart needed)
    handleToggle('settings-restart-enabled', '/settings', 'restartEnabled');
    handleTextInput('settings-restart-time', '/settings', 'restartTime', 800, {
        validate: (v) => /^([01]\d|2[0-3]):[0-5]\d$/.test(v) ? null : 'Use HH:MM (24h)',
    });
    handleToggle('settings-backups-enabled', '/settings', 'backupsEnabled');
    handleTextInput('settings-backup-retention', '/settings', 'backupRetention', 800);
    handleTextInput('settings-notify-lead', '/settings', 'notifyLeadMinutes', 800);

    // appearance + connection info
    handleSelect('settings-forced-theme', '/settings', 'forcedTheme', () => window.location.reload());
    handleSelect('settings-content-align', '/settings', 'contentAlign');
    handleTextInput('settings-game-address', '/settings', 'gameAddress', 800);
    handleTextInput('settings-game-password', '/settings', 'gamePassword', 800);
    wireBlurSlider();
}

/** Blur slider: live px label while dragging, save on release */
function wireBlurSlider() {
    const slider = document.getElementById('settings-background-blur');
    const label = document.getElementById('background-blur-value');
    if (!slider) return;

    const status = findStatus(slider);
    slider.addEventListener('input', () => {
        if (label) label.textContent = `${slider.value}px`;
    });
    slider.addEventListener('change', async () => {
        showPending(status);
        try {
            await postJSON('/settings', { backgroundBlur: parseInt(slider.value, 10) || 0 });
            showSuccess(status);
        } catch (e) {
            showError(status, e.message);
        }
    });
}

/** Driver activation (admin section) */
function wireDriverActivation() {
    const btn = document.getElementById('driver-activate-btn');
    const select = document.getElementById('driver-select');
    if (!btn || !select) return;

    const status = findStatus(select);
    btn.addEventListener('click', async () => {
        if (!select.value) return;
        showPending(status);
        btn.disabled = true;
        try {
            const info = await postJSON('/settings/driver/activate', { name: select.value });
            showSuccess(status);
            const active = document.getElementById('driver-active-label');
            if (active) active.textContent = `Active: ${select.value} — ${info?.name || ''}`;
        } catch (e) {
            showError(status, e.message);
        } finally {
            btn.disabled = false;
        }
    });
}

/** Background image upload/clear (admin section) */
function wireBackground(target) {
    const input = document.getElementById(`bg-${target}-file`);
    const clearBtn = document.getElementById(`bg-${target}-clear`);
    const preview = document.getElementById(`bg-${target}-preview`);
    if (!input) return;

    const status = findStatus(input);
    input.addEventListener('change', async () => {
        const file = input.files?.[0];
        if (!file) return;
        showPending(status);
        try {
            const form = new FormData();
            form.append('target', target);
            form.append('image', file);
            const res = await fetch('/settings/background', { method: 'POST', body: form });
            if (res.status === 401) {
                window.location.href = '/login';
                return;
            }
            if (!res.ok) throw new Error(await res.text() || `HTTP ${res.status}`);
            showSuccess(status);
            if (preview) {
                preview.src = `/bg/${target}?v=${Date.now()}`; // cache-bust
                preview.classList.remove('hidden');
            }
        } catch (e) {
            showError(status, e.message);
        } finally {
            input.value = '';
        }
    });

    clearBtn?.addEventListener('click', async () => {
        showPending(status);
        try {
            await postJSON('/settings/background/clear', { target });
            showSuccess(status);
            preview?.classList.add('hidden');
        } catch (e) {
            showError(status, e.message);
        }
    });
}

/** Initialize all settings on DOMContentLoaded */
export function initSettings() {
    wireSettings();
    privilegedPortWarning();
    wireDriverActivation();
    wireBackground('login');
    wireBackground('dashboard');
}
