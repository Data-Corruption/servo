//go:build linux

package app

// --- BEGIN UPDATE CHECK ---
//
// This file is the "UPDATE CHECK" block: periodic + on-demand checking whether
// a newer release exists, and the config plumbing to notify the user. It does
// NOT download or run anything; that's the "REMOTE UPDATE" block
// (update_remote.go).
//
// To remove all update checking (and notifications):
//   1. Delete this file.
//   2. Delete the fenced "UPDATE CHECK" blocks in:
//        internal/app/app.go            (ReleaseSource field/init, startAutoChecker call)
//        internal/types/types.go        (UpdateNotifications, LastUpdateCheck, UpdateAvailable)
//        internal/app/commands/update.go  (--check / --notify; delete the whole file if
//                                          also removing REMOTE UPDATE)
//        internal/platform/http/router/settings/settings.go ("UpdateAvailable" template data)
//        internal/ui/templates/settings.html (update banner)
//   3. Delete internal/platform/release/ and internal/app/update_test.go.
//   4. If also removing REMOTE UPDATE (see update_remote.go), delete the
//      "UPDATE SHARED" block too (internal/app/release_url.go).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sprout/internal/platform/database/config"
	"sprout/internal/types"
	"sync"
	"time"

	"github.com/Data-Corruption/stdx/xhttp"
	"golang.org/x/mod/semver"
)

const UpdateCheckInterval = 24 * time.Hour // interval for update checks

var ErrDevBuild = &xhttp.Err{
	Code: http.StatusNotImplemented,
	Msg:  "development build detected, skipping...",
	Err:  fmt.Errorf("development build detected, skipping"),
}

// startAutoChecker starts a goroutine that checks for updates every [UpdateCheckInterval].
func (a *App) startAutoChecker(currentCfgCopy *types.Configuration) error {
	// if dev build, do nothing
	if a.buildInfo.Version == "vX.X.X" {
		return nil
	}

	// no release-url file means this install shouldn't self-manage updates
	// (e.g. mirror install) — silently disable checking.
	if _, err := a.releaseURL(); err != nil {
		if errors.Is(err, ErrUpdatesDisabled) {
			a.Log.Debugf("update checking disabled: %v", err)
			return nil
		}
		return err
	}

	// if update notifications are enabled, calculate initial delay for next check
	initialDelay := UpdateCheckInterval
	if currentCfgCopy.UpdateNotifications {
		// if last check was more than UpdateCheckInterval ago, do one right now
		if time.Since(currentCfgCopy.LastUpdateCheck) >= UpdateCheckInterval {
			var err error
			currentCfgCopy.UpdateAvailable, err = a.CheckForUpdate()
			if err != nil {
				a.Log.Errorf("Initial update check failed: %v", err) // may just be a network issue, so don't fail
			}
		} else {
			initialDelay = time.Until(currentCfgCopy.LastUpdateCheck.Add(UpdateCheckInterval))
		}
		// cli notification
		if currentCfgCopy.UpdateAvailable {
			fmt.Printf("Update available! Run '%s update' to update to the latest version.\n", a.buildInfo.Name)
		}
	}

	// start auto checker. on tick if update notifications are enabled, check for updates
	var acWaitGroup sync.WaitGroup
	acCloseChan := make(chan struct{})
	acWaitGroup.Add(1)
	go func() {
		defer acWaitGroup.Done()

		// handle initial delay interruptibly
		timer := time.NewTimer(initialDelay)
		select {
		case <-timer.C:
			// continue
		case <-acCloseChan:
			if !timer.Stop() {
				<-timer.C
			}
			return
		}

		// check helper
		check := func() {
			cfg, err := config.View(a.DB)
			if err != nil {
				a.Log.Errorf("failed to view config: %v", err)
				return
			}
			// the -1 minute is to account for the time between the tick firing and LastUpdateCheck being set.
			// otherwise, on every other tick, the check would be skipped.
			if cfg.UpdateNotifications && time.Since(cfg.LastUpdateCheck) >= UpdateCheckInterval-time.Minute {
				if _, err := a.CheckForUpdate(); err != nil {
					a.Log.Errorf("Update check failed: %v", err) // may just be a network issue
				}
			}
		}
		check() // do one after initial delay

		// start periodic checks
		ticker := time.NewTicker(UpdateCheckInterval)
		defer ticker.Stop()

		for {
			select {
			case <-acCloseChan:
				return
			case <-ticker.C:
				check()
			}
		}
	}()

	// ensure auto checker is stopped on cleanup
	a.AddCleanup(func() error {
		close(acCloseChan)
		acWaitGroup.Wait()
		return nil
	})

	return nil
}

// CheckForUpdate checks if there is a newer version of the application available and updates the config accordingly.
// It returns true if an update is available, false otherwise.
// When running a dev build (e.g. with `vX.X.X`), it returns false without checking.
// When the install has no release-url file (mirror install), it returns [ErrUpdatesDisabled].
func (a *App) CheckForUpdate() (bool, error) {
	if a.buildInfo.Version == "" {
		return false, fmt.Errorf("failed to get appVersion from context")
	}
	if a.buildInfo.Version == "vX.X.X" {
		return false, ErrDevBuild
	}

	lCtx, lCancel := context.WithTimeout(a.Context, 8*time.Second)
	defer lCancel()

	releaseURL, err := a.releaseURL()
	if err != nil {
		return false, err
	}

	latest, err := a.ReleaseSource.GetLatestVersion(lCtx, releaseURL)
	if err != nil {
		return false, err
	}

	updateAvailable := semver.Compare(latest, a.buildInfo.Version) > 0
	a.Log.Debugf("Latest version: %s, Current version: %s, Update available: %t", latest, a.buildInfo.Version, updateAvailable)

	// update config
	if _, err := config.Update(a.DB, func(cfg *types.Configuration) error {
		cfg.UpdateAvailable = updateAvailable
		cfg.LastUpdateCheck = time.Now()
		return nil
	}); err != nil {
		return false, fmt.Errorf("failed to update updateAvailable in config: %w", err)
	}

	return updateAvailable, nil
}

// --- END UPDATE CHECK ---
