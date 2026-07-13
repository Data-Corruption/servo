package app

// --- BEGIN UPDATE SHARED ---
//
// release-url mechanics, used by both the "UPDATE CHECK" block
// (update_check.go) and the "REMOTE UPDATE" block (update_remote.go).
// Delete this file only when removing BOTH blocks.
//
// The release-url file is written by install.sh only for installs from the
// official release URL. Mirror installs (APP_RELEASE_URL override) don't get
// one, which disables update checking and remote updates for them by design —
// see docs/sprout/MIRRORING.md. A missing file surfaces as [ErrUpdatesDisabled] and
// callers treat it as a graceful no-op, not an error.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const releaseURLFileName = "release-url"

// ErrUpdatesDisabled indicates this install has no release-url file (e.g. it
// was installed from a mirror), so update checking / self-update is disabled.
var ErrUpdatesDisabled = errors.New("updates disabled: no release-url file (mirror install?)")

func releaseURLPath(storageDir string) string {
	return filepath.Join(storageDir, releaseURLFileName)
}

func normalizeReleaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimRight(trimmed, "/")
	if trimmed == "" {
		return "", fmt.Errorf("release URL is empty")
	}
	return trimmed + "/", nil
}

func loadReleaseURL(storageDir string) (string, error) {
	if storageDir == "" {
		return "", fmt.Errorf("storage directory is not set")
	}

	path := releaseURLPath(storageDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: missing %s", ErrUpdatesDisabled, path)
		}
		return "", fmt.Errorf("read release URL file: %w", err)
	}

	url, err := normalizeReleaseURL(string(data))
	if err != nil {
		return "", fmt.Errorf("invalid release URL in %s: %w", path, err)
	}
	return url, nil
}

func (a *App) releaseURL() (string, error) {
	return loadReleaseURL(a.StorageDir)
}

// --- END UPDATE SHARED ---
