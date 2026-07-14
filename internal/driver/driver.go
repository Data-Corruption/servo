// Package driver implements Servo's driver contract (Driver API v1).
//
// A driver is a single executable (POSIX sh in practice) invoked as
// `<driver> <verb> [args...]`. Servo communicates with it via argv,
// environment variables, exit codes, and stdout. This package is the only
// place that knows the contract; everything above it (job model, scheduler,
// HTTP) deals in Go types. See docs/DESIGN.md and docs/DRIVERS.md.
package driver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// SupportedAPI is the Driver API version this build of Servo speaks.
const SupportedAPI = 1

// Verbs. Required ones must exit 0/3/failure; optional ones may decline
// with ExitUnsupported.
const (
	VerbDescribe         = "describe"
	VerbDeps             = "deps"
	VerbStatus           = "status"
	VerbStart            = "start"
	VerbStop             = "stop"
	VerbInstall          = "install"
	VerbUpdate           = "update"
	VerbBackup           = "backup"
	VerbUninstall        = "uninstall"        // optional
	VerbRestore          = "restore"          // optional
	VerbNotify           = "notify"           // optional
	VerbPlayers          = "players"          // optional
	VerbVersion          = "version"          // optional
	VerbContainerVersion = "container-version" // optional
)

// Exit code conventions (LSB-flavored).
const (
	ExitOK          = 0
	ExitStopped     = 3 // status only: server is offline, not an error
	ExitUnsupported = 4 // optional verb declined by this driver
)

// Timeout tiers per verb. Fast verbs are polled and must never hang the
// daemon; start/stop wrap graceful game shutdowns; long verbs cover image
// pulls and archive churn.
const (
	fastTimeout      = 30 * time.Second
	stopStartTimeout = 10 * time.Minute
	longTimeout      = 60 * time.Minute
)

func timeoutFor(verb string) time.Duration {
	switch verb {
	case VerbStart, VerbStop:
		return stopStartTimeout
	case VerbInstall, VerbUpdate, VerbBackup, VerbRestore, VerbUninstall:
		return longTimeout
	default:
		return fastTimeout
	}
}

// Env is everything Servo provides to a driver invocation.
type Env struct {
	DriverPath string // absolute path to the driver executable
	BackupDir  string // SERVO_BACKUP_DIR
	DataDir    string // SERVO_DATA_DIR
	AppVersion string // SERVO_VERSION
}

// Run executes a driver verb, streaming combined stdout+stderr into out
// (which may be nil). It enforces the verb's timeout tier on top of ctx and
// kills the whole process group on cancellation so podman/tar children don't
// linger.
//
// The returned int is the driver's exit code. err is non-nil only for
// exec-level problems: the driver couldn't be started, was killed by a
// signal, or timed out (errors.Is(err, context.DeadlineExceeded)). A plain
// non-zero exit returns (code, nil) — interpretation is verb-specific and
// belongs to the caller.
func Run(ctx context.Context, env Env, out io.Writer, verb string, args ...string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeoutFor(verb))
	defer cancel()

	cmd := exec.CommandContext(ctx, env.DriverPath, append([]string{verb}, args...)...)
	cmd.Env = append(os.Environ(),
		"SERVO_BACKUP_DIR="+env.BackupDir,
		"SERVO_DATA_DIR="+env.DataDir,
		"SERVO_VERSION="+env.AppVersion,
	)
	// New process group so cancellation can take out the driver's children
	// too, not just the script itself.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			// negative pid targets the group; ESRCH just means already gone
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	if out != nil {
		cmd.Stdout = out
		cmd.Stderr = out
	}

	err := cmd.Run()
	if err == nil {
		return ExitOK, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return -1, fmt.Errorf("driver %q verb %q: %w", env.DriverPath, verb, ctxErr)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if code := exitErr.ExitCode(); code >= 0 {
			return code, nil
		}
		return -1, fmt.Errorf("driver %q verb %q killed: %w", env.DriverPath, verb, err)
	}
	return -1, fmt.Errorf("driver %q verb %q failed to run: %w", env.DriverPath, verb, err)
}
