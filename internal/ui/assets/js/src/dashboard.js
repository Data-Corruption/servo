// Dashboard
// Status/activity polling, operation buttons, and the backups list.
// Transport is plain polling: ~2s while an op runs, backing off to ~5s idle.

import { getJSON, postJSON } from './api.js';
import { showBanner, confirmAction, copyText } from './ui.js';

const POLL_ACTIVE_MS = 2000;
const POLL_IDLE_MS = 5000;

let logOffset = 0;
let wasRunning = false;
let pollTimer = null;

const $ = (id) => document.getElementById(id);

function flags() {
    const el = $('dashboard-flags');
    return {
        canRestore: el?.dataset.canRestore === '1',
        isAdmin: el?.dataset.isAdmin === '1',
    };
}

// ---- rendering (textContent only — driver output is untrusted) -------------

function renderGame(game) {
    const dot = $('status-dot');
    const text = $('status-text');
    if (!dot || !text) return;

    if (!game || !game.driver) {
        dot.className = 'status status-lg';
        text.textContent = 'no driver';
        return;
    }

    $('driver-name').textContent = game.driverInfo?.name || game.driver;

    const status = game.status || 'unknown';
    const dotClass = { online: 'status-success', offline: 'status-error' }[status] || 'status-warning';
    dot.className = `status status-lg ${dotClass} ${status === 'online' ? 'animate-pulse' : ''}`;
    text.textContent = status;

    // players
    const count = $('player-count');
    const list = $('players-list');
    if (game.playersSupported && status === 'online') {
        const players = game.players || [];
        count.textContent = `${players.length} online`;
        count.classList.remove('hidden');
        if (players.length > 0) {
            list.textContent = players.join(', ');
            list.classList.remove('hidden');
        } else {
            list.classList.add('hidden');
        }
    } else {
        count.classList.add('hidden');
        list.classList.add('hidden');
    }

    // versions + staleness
    $('stale-badge').classList.toggle('hidden', !game.stale);
    const parts = [];
    const info = game.driverInfo || {};
    if (game.serverVersion) {
        parts.push(`server ${game.serverVersion}${withTarget(info.targetServerVersion, game.serverVersion)}`);
    }
    if (game.containerVersion) {
        parts.push(`container ${game.containerVersion}${withTarget(info.targetContainerVersion, game.containerVersion)}`);
    }
    $('version-row').textContent = parts.join(' · ');
}

function withTarget(target, live) {
    return target && target !== live ? ` (driver targets ${target})` : '';
}

function renderActivity(op) {
    const running = !!op?.running;
    $('activity-idle')?.classList.toggle('hidden', running);
    $('activity-running')?.classList.toggle('hidden', !running);

    if (running) {
        $('activity-op').textContent = opLabel(op.op);
        $('activity-elapsed').textContent = formatElapsed(op.elapsedMs);
    }

    // last result
    const last = $('activity-last');
    if (last && op?.last && !running) {
        const l = op.last;
        const when = l.endedAt ? new Date(l.endedAt).toLocaleString() : '';
        last.textContent = l.success
            ? `Last: ${opLabel(l.op)} succeeded (${when})`
            : `Last: ${opLabel(l.op)} FAILED — ${l.detail || 'see log'} (${when})`;
        last.className = `text-xs ${l.success ? 'text-base-content/50' : 'text-error'}`;
        last.classList.remove('hidden');
    }

    // disable buttons while busy
    document.querySelectorAll('.op-btn').forEach(btn => { btn.disabled = running; });
}

function opLabel(op) {
    return {
        start: 'Starting server', stop: 'Stopping server', restart: 'Restarting server',
        update: 'Updating server', backup: 'Backing up', restore: 'Restoring backup',
        install: 'Installing server',
    }[op] || op || '';
}

function formatElapsed(ms) {
    const s = Math.floor((ms || 0) / 1000);
    return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${s % 60}s`;
}

function appendLog(chunk) {
    const pre = $('op-log');
    if (!pre || !chunk) return;
    pre.classList.remove('hidden');
    const atBottom = pre.scrollTop + pre.clientHeight >= pre.scrollHeight - 8;
    pre.textContent += chunk;
    // keep the DOM node bounded like the server-side ring buffer
    if (pre.textContent.length > 128 * 1024) {
        pre.textContent = pre.textContent.slice(-64 * 1024);
    }
    if (atBottom) pre.scrollTop = pre.scrollHeight;
}

// ---- backups ----------------------------------------------------------------

function formatSize(bytes) {
    if (bytes >= 1 << 30) return `${(bytes / (1 << 30)).toFixed(1)} GiB`;
    if (bytes >= 1 << 20) return `${(bytes / (1 << 20)).toFixed(1)} MiB`;
    if (bytes >= 1 << 10) return `${(bytes / (1 << 10)).toFixed(1)} KiB`;
    return `${bytes} B`;
}

async function refreshBackups() {
    const list = $('backups-list');
    const empty = $('backups-empty');
    if (!list) return; // no game.backup perm, card not rendered

    let backups;
    try {
        backups = await getJSON('/api/backups');
    } catch {
        return; // transient; next refresh will retry
    }

    list.replaceChildren();
    const has = backups && backups.length > 0;
    list.classList.toggle('hidden', !has);
    empty.classList.toggle('hidden', has);
    if (!has) return;

    const { canRestore } = flags();
    for (const b of backups) {
        const li = document.createElement('li');
        li.className = 'flex items-center justify-between gap-2 bg-base-300 rounded-lg px-3 py-2';

        const label = document.createElement('div');
        label.className = 'min-w-0';
        const name = document.createElement('div');
        name.className = 'text-sm font-mono truncate';
        name.textContent = b.name;
        const meta = document.createElement('div');
        meta.className = 'text-xs text-base-content/50';
        meta.textContent = `${formatSize(b.size)} · ${new Date(b.modTime).toLocaleString()}`;
        label.append(name, meta);

        const actions = document.createElement('div');
        actions.className = 'flex gap-2 shrink-0';

        const dl = document.createElement('a');
        dl.className = 'btn btn-ghost btn-xs';
        dl.textContent = 'Download';
        dl.href = `/api/backups/${encodeURIComponent(b.name)}`;
        actions.append(dl);

        if (canRestore) {
            const restore = document.createElement('button');
            restore.className = 'btn btn-warning btn-outline btn-xs op-btn';
            restore.textContent = 'Restore';
            restore.addEventListener('click', () => {
                confirmAction(
                    'Restore Backup',
                    `This will <b>overwrite the current server data</b> with the backup <code>${escapeForModal(b.name)}</code>. The server will be stopped during the restore.`,
                    () => startOp('restore', { archive: b.name }),
                    'btn-warning',
                );
            });
            actions.append(restore);
        }

        li.append(label, actions);
        list.append(li);
    }
}

function escapeForModal(value) {
    const div = document.createElement('div');
    div.textContent = value ?? '';
    return div.innerHTML;
}

// ---- polling ---------------------------------------------------------------

async function poll() {
    let data;
    try {
        const { isAdmin } = flags();
        const url = isAdmin ? `/api/status?offset=${logOffset}` : '/api/status';
        data = await getJSON(url);
    } catch {
        schedule(POLL_IDLE_MS);
        return;
    }

    renderGame(data.game);
    renderActivity(data.op);
    if (data.log !== undefined) {
        appendLog(data.log);
        logOffset = data.offset ?? logOffset;
    }

    const running = !!data.op?.running;
    if (wasRunning && !running) {
        // op just finished: refresh backups and surface the outcome
        refreshBackups();
        const last = data.op?.last;
        if (last) {
            showBanner(last.success ? 'success' : 'error',
                last.success ? `${opLabel(last.op)} finished` : `${opLabel(last.op)} failed: ${last.detail || 'see log'}`);
        }
    }
    wasRunning = running;
    schedule(running ? POLL_ACTIVE_MS : POLL_IDLE_MS);
}

function schedule(ms) {
    clearTimeout(pollTimer);
    pollTimer = setTimeout(poll, ms);
}

// ---- operations --------------------------------------------------------------

async function startOp(op, body = {}) {
    try {
        await postJSON(`/api/op/${op}`, body);
        showBanner('info', `${opLabel(op)}…`);
        schedule(300); // pick up the running state quickly
    } catch (err) {
        showBanner('error', err.message);
    }
}

// ---- init --------------------------------------------------------------------

/** Connection info copy buttons: copy and flash confirmation */
function wireCopyButtons() {
    document.querySelectorAll('.copy-btn[data-copy]').forEach(btn => {
        btn.addEventListener('click', async () => {
            const ok = await copyText(btn.dataset.copy);
            showBanner(ok ? 'success' : 'error', ok ? 'Copied to clipboard' : 'Copy failed', 2000);
        });
    });
}

export function initDashboard() {
    if (!$('dashboard-root')) return;

    wireCopyButtons();

    document.querySelectorAll('.op-btn[data-op]').forEach(btn => {
        const op = btn.dataset.op;
        if (op === 'stop') {
            btn.addEventListener('click', () => confirmAction(
                'Stop Server', 'Stop the game server? Connected players will be disconnected.',
                () => startOp('stop'), 'btn-error'));
        } else {
            btn.addEventListener('click', () => startOp(op));
        }
    });

    poll();
    refreshBackups();
}
