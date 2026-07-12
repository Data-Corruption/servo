package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"servo/internal/platform/database/config"
)

// BackupInfo describes one archive in the backups dir.
type BackupInfo struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

// ListBackups returns archives in dir, newest first. Subdirs and other
// non-regular entries are ignored.
func ListBackups(dir string) ([]BackupInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var backups []BackupInfo
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		backups = append(backups, BackupInfo{Name: e.Name(), Size: fi.Size(), ModTime: fi.ModTime()})
	}
	sort.Slice(backups, func(i, j int) bool { return backups[i].ModTime.After(backups[j].ModTime) })
	return backups, nil
}

// ResolveBackup validates that name is an entry of the backups dir (never a
// client-supplied path) and returns its absolute path.
func ResolveBackup(dir, name string) (string, error) {
	backups, err := ListBackups(dir)
	if err != nil {
		return "", err
	}
	for _, b := range backups {
		if b.Name == name {
			return filepath.Join(dir, name), nil
		}
	}
	return "", fmt.Errorf("backup %q not found", name)
}

// pruneBackups removes archives beyond the configured retention count,
// keeping the newest N.
func (r *Runner) pruneBackups() error {
	cfg, err := config.View(r.db)
	if err != nil {
		return err
	}
	keep := cfg.BackupRetention
	if keep <= 0 {
		return nil // retention disabled, keep everything
	}
	backups, err := ListBackups(r.paths.BackupsDir)
	if err != nil {
		return err
	}
	for _, b := range backups[min(keep, len(backups)):] {
		r.say("pruning old backup %s", b.Name)
		if err := os.Remove(filepath.Join(r.paths.BackupsDir, b.Name)); err != nil {
			return fmt.Errorf("prune %s: %w", b.Name, err)
		}
	}
	return nil
}
