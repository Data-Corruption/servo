//go:build linux

package app

// --- BEGIN REMOTE UPDATE ---
//
// This file is the "REMOTE UPDATE" block: the app downloading, verifying, and
// executing its own install/update script. Some deployments consider this a
// liability (it's remote code execution by design); deleting this block turns
// the app into check-and-notify only — users update manually by re-running
// install.sh.
//
// To remove remote update (keeping check-and-notify):
//   1. Delete this file.
//   2. Delete the fenced "REMOTE UPDATE" blocks in:
//        internal/app/app.go            (uOnce field, PreUpdateVersion write in DB cleanup)
//        internal/types/types.go        (PreUpdateVersion)
//        internal/app/commands/update.go  (default action; replace with manual-update instructions)
//        internal/app/commands/service.go (update-logs cheat-sheet line)
//        internal/platform/http/router/settings/settings.go (update branch of restart + "updated" status)
//        internal/ui/templates/settings.html (restart-update checkbox)
//        internal/ui/assets/js/src/server.js (update-checkbox read + "updated" poll handling)
//        scripts/install.sh             (release-url write/backup/rollback blocks)
//   3. To remove ALL update functionality, also delete the "UPDATE CHECK"
//      block (see update_check.go) and then the "UPDATE SHARED" block
//      (internal/app/release_url.go).

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sprout/internal/platform/database/config"
	"sprout/internal/types"
	"syscall"
	"time"

	"github.com/Data-Corruption/lmdb-go/wrap"
)

const UpdateTimeout = 10 * time.Minute // max time for update process

// updatePipeline builds the shell pipeline that downloads the install script,
// verifies its cosign keyless signature against the identity baked into this
// binary at build time, and only then executes it. This means a compromised or
// modified release host (e.g. a mirror) cannot feed us an arbitrary script.
func (a *App) updatePipeline(releaseURL string) (string, error) {
	if a.buildInfo.CertIdentity == "" || a.buildInfo.OidcIssuer == "" {
		return "", fmt.Errorf("remote update unavailable: no cosign identity baked into this build")
	}
	return fmt.Sprintf(`set -eu
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -sSfL -o "$tmp/install.sh" %q
curl -sSfL -o "$tmp/install.sh.cosign.bundle" %q
cosign verify-blob --bundle "$tmp/install.sh.cosign.bundle" --certificate-identity %q --certificate-oidc-issuer %q "$tmp/install.sh"
sh "$tmp/install.sh"`,
		releaseURL+"install.sh",
		releaseURL+"install.sh.cosign.bundle",
		a.buildInfo.CertIdentity,
		a.buildInfo.OidcIssuer,
	), nil
}

// DeferUpdate prepares the install/update script to be run on exit.
// It will prep the update regardless of if an update is available or not.
// You should exit soon after calling this.
// Calling either DeferUpdate or DetachUpdate more than once does nothing.
// Only the first call will have any effect.
// When the install has no release-url file (mirror install), it returns [ErrUpdatesDisabled].
func (a *App) DeferUpdate() error {
	var rErr error
	a.uOnce.Do(func() {
		if err := uPrep(a.buildInfo.Version, a.DB); err != nil {
			rErr = err
			return
		}

		releaseURL, err := a.releaseURL()
		if err != nil {
			rErr = err
			return
		}

		// prepare update command
		pipeline, err := a.updatePipeline(releaseURL)
		if err != nil {
			rErr = err
			return
		}
		a.Log.Debugf("Prepared update, command: %s", pipeline)

		a.SetPostCleanup(func() error {
			rCtx, rCancel := context.WithTimeout(a.Context, UpdateTimeout)
			defer rCancel()

			cmd := exec.CommandContext(rCtx, "sh", "-c", pipeline)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			return cmd.Run()
		})
	})
	return rErr
}

// DetachUpdate starts the install/update script as a detached process.
// It does so regardless of if an update is available or not.
// After calling this, the process will soon be closed externally by the install/update script.
// Calling either DeferUpdate or DetachUpdate more than once does nothing.
// Only the first call will have any effect.
// When the install has no release-url file (mirror install), it returns [ErrUpdatesDisabled].
func (a *App) DetachUpdate() error {
	var rErr error
	a.uOnce.Do(func() {
		if err := uPrep(a.buildInfo.Version, a.DB); err != nil {
			rErr = err
			return
		}

		releaseURL, err := a.releaseURL()
		if err != nil {
			rErr = err
			return
		}

		// prepare update command
		name := a.buildInfo.Name
		pipeline, err := a.updatePipeline(releaseURL)
		if err != nil {
			rErr = err
			return
		}
		logPath := filepath.Join(a.StorageDir, "update.log")
		a.Log.Debugf("Prepared detached update: command: %s, logPath: %s", pipeline, logPath)

		// run update (install/update script will close this process)
		if err := runUpdateDetached(a.buildInfo.ServiceEnabled, name, pipeline, logPath); err != nil {
			rErr = err
			return
		}
	})
	return rErr
}

// uPrep prepares the update by setting updateAvailable to false and updateFollowup to the current version.
// After restart, updateFollowup will be used to lazily infer if an update was successful.
func uPrep(version string, db *wrap.DB) error {
	// double check version string
	if version == "" {
		return fmt.Errorf("failed to get appVersion")
	}
	if version == "vX.X.X" {
		return ErrDevBuild
	}
	// set updateAvailable to false since we're updating
	if _, err := config.Update(db, func(cfg *types.Configuration) error {
		cfg.UpdateAvailable = false
		return nil
	}); err != nil {
		return fmt.Errorf("failed to update updateAvailable in config: %w", err)
	}
	return nil
}

func runUpdateDetached(serviceEnabled bool, name, pipeline, logPath string) error {
	if serviceEnabled {
		// Run as transient systemd service (like a service but one-off and
		// configured via cmdline args). Assuming this is run from in the daemon,
		// we need this to survive the parent process (service) exiting, which
		// will kill the c group, including any child processes. Even those started
		// using `cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}`. The service
		// needs to exit because the install script updates the unit file, etc.

		lCtx, lCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer lCancel()

		unitName := fmt.Sprintf("%s-update-%s", name, time.Now().Format("20060102-150405"))
		runtime := fmt.Sprintf("RuntimeMaxSec=%ds", int(UpdateTimeout.Seconds()))
		syslogIdent := fmt.Sprintf("SyslogIdentifier=%s-update", name)

		cmd := exec.CommandContext(
			lCtx,
			"systemd-run",
			"--user",
			"--unit="+unitName,
			"--quiet",
			"--no-block", // fully detached
			"-p", "StandardOutput=journal",
			"-p", "StandardError=journal",
			"-p", syslogIdent,
			"-p", runtime, // apply timeout
			"-p", "KillSignal=SIGINT",
			"-p", "TimeoutStopSec=30s", // graceful shutdown time
			"/bin/sh", "-c", pipeline,
		)
		return cmd.Run()
	} else {
		// Not under threat of c group being killed, so just use setsid
		// with shell-managed logging. escape logPath to be safe.
		pipelineWithLogging := fmt.Sprintf("( %s ) >> %q 2>&1", pipeline, logPath)
		cmd := exec.Command("sh", "-c", pipelineWithLogging)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("failed to start detached update: %w", err)
		}
		// release resources so the parent doesn't track the child (prevents zombies)
		if err := cmd.Process.Release(); err != nil {
			return fmt.Errorf("failed to release process: %w", err)
		}
		return nil
	}
}

// --- END REMOTE UPDATE ---
