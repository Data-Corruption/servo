// Theme Management
// Default light/dark switching with localStorage and system preference
// support. When the server forces a theme (settings), page_start renders
// data-theme + data-theme-forced on <html> and this module stands down.

const LIGHT_THEME = 'light';
const DARK_THEME = 'dark';
const THEME_KEY = 'SERVO_THEME';

function themeForced() {
    return document.documentElement.hasAttribute('data-theme-forced');
}

/** Get current theme, defaulting to system preference */
export function getTheme() {
    return localStorage.getItem(THEME_KEY) ||
        (window.matchMedia?.('(prefers-color-scheme: dark)').matches ? DARK_THEME : LIGHT_THEME);
}

/** Check if current theme is dark */
export function isDarkTheme() {
    return getTheme() === DARK_THEME;
}

/** Update the theme toggle checkbox state */
function updateThemeToggle() {
    const toggle = document.getElementById('theme-toggle');
    if (toggle) {
        toggle.checked = isDarkTheme();
    }
}

/** Set theme and update UI */
export function setTheme(theme) {
    localStorage.setItem(THEME_KEY, theme);
    document.documentElement.setAttribute('data-theme', theme);
    updateThemeToggle();
}

/** Toggle between light and dark themes */
export function toggleTheme() {
    setTheme(isDarkTheme() ? LIGHT_THEME : DARK_THEME);
}

/** Initialize theme on page load */
export function initTheme() {
    if (themeForced()) return;
    const loadTheme = getTheme();
    document.documentElement.setAttribute('data-theme', loadTheme);
    localStorage.setItem(THEME_KEY, loadTheme);
}

/** Setup toggle after DOM is loaded */
export function setupThemeToggle() {
    if (themeForced()) return;
    const toggle = document.getElementById('theme-toggle');
    updateThemeToggle();
    if (toggle) {
        toggle.addEventListener('change', toggleTheme);
    }
}
