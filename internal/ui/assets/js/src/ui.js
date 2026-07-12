// UI Utilities
// Click blocker, status indicators, and common UI helpers

let bannerTimer = null;

/** Show click blocker overlay */
export function blockClicks() {
    const blocker = document.getElementById('click-blocker');
    if (!blocker || blocker.open) return;
    blocker.showModal();
}

/** Hide click blocker overlay */
export function unblockClicks() {
    const blocker = document.getElementById('click-blocker');
    if (!blocker?.open) return;
    blocker.close();
}

/** Show a loading spinner on the status element */
export function showPending(statusEl) {
    if (!statusEl) return;
    statusEl.className = 'status loading loading-spinner loading-xs';
    statusEl.textContent = '';
    statusEl.dataset.errorMessage = '';
    statusEl.onclick = null;
}

/** Show a green circle that auto-hides after 2 seconds */
export function showSuccess(statusEl) {
    if (!statusEl) return;
    statusEl.className = 'status status-success';
    statusEl.dataset.errorMessage = '';
    statusEl.onclick = null;
    setTimeout(() => {
        if (statusEl.classList.contains('status-success')) {
            statusEl.className = 'status hidden';
        }
    }, 2000);
}

/** Find the status element relative to the input */
export function findStatus(input) {
    // For inline toggles (inside label), find sibling status span
    const label = input.closest('label');
    if (label) {
        const status = label.querySelector('.status');
        if (status) return status;
    }
    // For inputs with wrapper divs, find sibling
    const wrapper = input.closest('.flex');
    if (wrapper) {
        const status = wrapper.querySelector('.status');
        if (status) return status;
    }
    // Fallback: search in parent form-control
    const formControl = input.closest('.form-control');
    if (formControl) {
        return formControl.querySelector('.status');
    }
    return input.parentElement?.querySelector('.status') || null;
}

/**
 * Show error modal with message.
 * Dual signature: showError(message) or showError(statusEl, message) — the
 * latter also clears the pending spinner on the status element.
 */
export function showError(statusOrMsg, maybeMsg) {
    const message = maybeMsg !== undefined ? maybeMsg : statusOrMsg;
    const statusEl = maybeMsg !== undefined ? statusOrMsg : null;

    if (statusEl && statusEl.classList) {
        statusEl.className = 'status hidden';
    }

    const modal = document.getElementById('error-modal');
    const msgEl = document.getElementById('error-modal-message');
    if (modal && msgEl) {
        msgEl.textContent = message;
        modal.showModal();
    }
}

/** Show a transient banner (requires #dashboard-banner elements; falls back to the error modal) */
export function showBanner(kind, message, timeoutMs = 4000) {
    const banner = document.getElementById('dashboard-banner');
    const messageEl = document.getElementById('dashboard-banner-message');
    if (!banner || !messageEl) {
        if (kind === 'error') {
            showError(message);
        }
        return;
    }

    if (bannerTimer) {
        clearTimeout(bannerTimer);
        bannerTimer = null;
    }

    banner.className = `alert alert-${kind}`;
    messageEl.textContent = message;

    if (timeoutMs > 0) {
        bannerTimer = setTimeout(() => {
            banner.className = 'alert hidden';
        }, timeoutMs);
    }
}

/** Generic confirmation modal (requires #confirm-modal elements) */
export function confirmAction(title, message, onConfirm, btnClass = 'btn-primary') {
    const modal = document.getElementById('confirm-modal');
    const titleEl = document.getElementById('confirm-modal-title');
    const msgEl = document.getElementById('confirm-modal-message');
    const btn = document.getElementById('confirm-modal-btn');
    if (!modal || !titleEl || !msgEl || !btn) return;

    titleEl.textContent = title;
    msgEl.innerHTML = message;

    btn.className = `btn ${btnClass}`;
    btn.textContent = title;

    // clone to drop any previously attached confirm handlers
    const newBtn = btn.cloneNode(true);
    btn.parentNode.replaceChild(newBtn, btn);

    newBtn.addEventListener('click', async () => {
        modal.close();
        await onConfirm();
    });

    modal.showModal();
}

export function escapeHtml(value) {
    const div = document.createElement('div');
    div.textContent = value ?? '';
    return div.innerHTML;
}

export function escapeAttr(value) {
    return String(value ?? '')
        .replace(/&/g, '&amp;')
        .replace(/"/g, '&quot;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;');
}

export async function copyText(value) {
    if (!value) return false;
    try {
        await navigator.clipboard.writeText(value);
        return true;
    } catch {
        return false;
    }
}
