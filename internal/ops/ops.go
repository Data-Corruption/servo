// Package ops implements Servo's job model: every driver interaction is an
// operation, and only one runs at a time (a mutex, not a queue). Long ops run
// async in a goroutine; combined driver output lands in a capped ring buffer
// the UI polls. The last result is persisted so it survives daemon restarts.
package ops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"servo/internal/driver"
	"servo/internal/platform/database"
	"servo/internal/platform/database/config"

	"github.com/Data-Corruption/lmdb-go/wrap"
	"github.com/Data-Corruption/stdx/xlog"
)

// Op is a user-facing operation. Ops compose driver verbs; they are not 1:1
// with them (restart = stop+start, backup = stop→backup→start, ...).
type Op string

const (
	OpStart     Op = "start"
	OpStop      Op = "stop"
	OpRestart   Op = "restart"
	OpInstall   Op = "install"
	OpUpdate    Op = "update"
	OpBackup    Op = "backup"
	OpRestore   Op = "restore"
	OpUninstall Op = "uninstall"
)

// ValidOps maps op names to the game.* permission tier they belong to,
// keyed for HTTP routing. Values are informational; the api package maps
// them to permission bits.
var ValidOps = map[Op]bool{
	OpStart: true, OpStop: true, OpRestart: true, OpInstall: true,
	OpUpdate: true, OpBackup: true, OpRestore: true, OpUninstall: true,
}

var (
	ErrBusy         = errors.New("an operation is already running")
	ErrNoDriver     = errors.New("no active driver")
	ErrServerOnline = errors.New("server is online")
)

const lastOpKey = "lastOp"

// Result is the persisted outcome of the most recent operation.
type Result struct {
	Op        Op        `json:"op"`
	Success   bool      `json:"success"`
	Detail    string    `json:"detail,omitempty"` // failure summary, empty on success
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt"`
	Tail      string    `json:"tail"` // last chunk of driver output
}

// Paths tells the runner where driver-related directories live. DataDir and
// BackupsDir are parent roots; each driver gets its own subdirectory of both,
// named after the driver file (see Runner.EnvFor).
type Paths struct {
	DriversDir string
	DataDir    string
	BackupsDir string
	AppVersion string
}

// Runner is the single-flight operation runner. One per daemon, owned by App.
type Runner struct {
	db    *wrap.DB
	log   *xlog.Logger
	paths Paths
	ctx   context.Context // daemon lifetime; ops are not tied to HTTP requests

	mu      sync.Mutex
	running bool
	op      Op
	started time.Time
	buf     *ring
	last    *Result
}

func New(ctx context.Context, db *wrap.DB, log *xlog.Logger, paths Paths) *Runner {
	r := &Runner{db: db, log: log, paths: paths, ctx: ctx, buf: newRing()}
	// load the persisted last result; missing is fine on first run
	if last, err := database.Get[Result](db, *database.StateDBI, []byte(lastOpKey), database.EncodingJSON); err == nil {
		r.last = last
	}
	return r
}

// Env resolves the active driver into a driver.Env, or ErrNoDriver.
func (r *Runner) Env() (driver.Env, error) {
	cfg, err := config.View(r.db)
	if err != nil {
		return driver.Env{}, err
	}
	return r.EnvFor(cfg.ActiveDriver)
}

// EnvFor resolves a driver name into a driver.Env. Every driver gets its own
// data and backup subdirectory (keyed by driver filename), created here so
// drivers and callers can rely on both existing.
func (r *Runner) EnvFor(name string) (driver.Env, error) {
	if name == "" {
		return driver.Env{}, ErrNoDriver
	}
	path, err := driver.Resolve(r.paths.DriversDir, name)
	if err != nil {
		return driver.Env{}, fmt.Errorf("%w: %v", ErrNoDriver, err)
	}
	env := driver.Env{
		DriverPath: path,
		BackupDir:  filepath.Join(r.paths.BackupsDir, name),
		DataDir:    filepath.Join(r.paths.DataDir, name),
		AppVersion: r.paths.AppVersion,
	}
	for _, dir := range []string{env.DataDir, env.BackupDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return driver.Env{}, fmt.Errorf("create driver dir: %w", err)
		}
	}
	return env, nil
}

// Busy reports whether an operation is currently running.
func (r *Runner) Busy() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// GuardSwitch reports whether activating driver name is allowed right now:
// ErrBusy while an operation runs, ErrServerOnline while a *different*
// active driver's server is online. A failed status probe on the outgoing
// driver never blocks the switch — a broken driver may be exactly why the
// operator is switching.
func (r *Runner) GuardSwitch(ctx context.Context, name string) error {
	if r.Busy() {
		return ErrBusy
	}
	cfg, err := config.View(r.db)
	if err != nil {
		return err
	}
	if cfg.ActiveDriver == "" || cfg.ActiveDriver == name {
		return nil
	}
	env, err := r.EnvFor(cfg.ActiveDriver)
	if err != nil {
		return nil // unresolvable outgoing driver can't be probed, allow
	}
	if status, err := driver.GetStatus(ctx, env); err == nil && status == driver.StatusOnline {
		return ErrServerOnline
	}
	return nil
}

// Snapshot is the poll payload for the activity panel.
type Snapshot struct {
	Running   bool    `json:"running"`
	Op        Op      `json:"op,omitempty"`
	ElapsedMs int64   `json:"elapsedMs,omitempty"`
	Last      *Result `json:"last,omitempty"`
}

func (r *Runner) Status() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := Snapshot{Running: r.running, Last: r.last}
	if r.running {
		s.Op = r.op
		s.ElapsedMs = time.Since(r.started).Milliseconds()
	}
	return s
}

// Tail returns buffered driver output after offset and the new offset.
func (r *Runner) Tail(offset int64) ([]byte, int64) {
	return r.buf.ReadFrom(offset)
}

// Start launches an operation asynchronously. It fails fast (before the
// goroutine) when another op is running or no driver is active. The returned
// channel closes when the op completes — the scheduler blocks on it, HTTP
// handlers ignore it.
func (r *Runner) Start(op Op, args ...string) (<-chan struct{}, error) {
	env, err := r.Env()
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return nil, ErrBusy
	}
	r.running = true
	r.op = op
	r.started = time.Now()
	r.buf.Reset()
	r.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		r.run(op, args, env)
	}()
	return done, nil
}

// say writes a servo-side marker line into the op log, distinguishing
// orchestration from driver output.
func (r *Runner) say(format string, a ...any) {
	fmt.Fprintf(r.buf, "[servo] "+format+"\n", a...)
}

// run executes the op's verb sequence and records the result.
func (r *Runner) run(op Op, args []string, env driver.Env) {
	res := Result{Op: op, StartedAt: time.Now()}
	err := r.sequence(op, args, env)
	res.EndedAt = time.Now()
	res.Success = err == nil
	if err != nil {
		res.Detail = err.Error()
		r.say("FAILED: %v", err)
	} else {
		r.say("done")
	}
	res.Tail = r.buf.Tail(4096)

	if dbErr := database.Put(r.db, *database.StateDBI, []byte(lastOpKey), &res, database.EncodingJSON); dbErr != nil {
		r.log.Errorf("failed to persist op result: %v", dbErr)
	}
	r.log.Infof("op %s finished: success=%t detail=%q", op, res.Success, res.Detail)

	r.mu.Lock()
	r.running = false
	r.last = &res
	r.mu.Unlock()
}

// step runs one driver verb, streaming output into the ring buffer, and
// fails on any non-zero exit.
func (r *Runner) step(env driver.Env, verb string, args ...string) error {
	r.say("%s", strings.TrimSpace(verb+" "+strings.Join(args, " ")))
	code, err := driver.Run(r.ctx, env, r.buf, verb, args...)
	if err != nil {
		return err
	}
	if code != driver.ExitOK {
		return fmt.Errorf("%s exited %d", verb, code)
	}
	return nil
}

// sequence composes driver verbs per op. update/backup/restore preserve the
// server's prior state: they only start it afterwards if it was online before
// (never start a server someone stopped on purpose).
func (r *Runner) sequence(op Op, args []string, env driver.Env) error {
	switch op {
	case OpStart:
		return r.step(env, driver.VerbStart)
	case OpStop:
		return r.step(env, driver.VerbStop)
	case OpRestart:
		if err := r.step(env, driver.VerbStop); err != nil {
			return err
		}
		return r.step(env, driver.VerbStart)
	case OpInstall:
		return r.step(env, driver.VerbInstall)
	case OpUpdate:
		return r.stopDoStart(env, func() error {
			return r.step(env, driver.VerbUpdate)
		})
	case OpBackup:
		return r.stopDoStart(env, func() error {
			if err := r.step(env, driver.VerbBackup); err != nil {
				return err
			}
			return r.pruneBackups(env.BackupDir)
		})
	case OpRestore:
		if len(args) != 1 {
			return fmt.Errorf("restore requires an archive name")
		}
		archive, err := ResolveBackup(env.BackupDir, args[0])
		if err != nil {
			return err
		}
		return r.stopDoStart(env, func() error {
			return r.step(env, driver.VerbRestore, archive)
		})
	case OpUninstall:
		return r.uninstall(env)
	default:
		return fmt.Errorf("unknown op %q", op)
	}
}

// uninstall stops the server if needed, has the driver tear down everything
// it created outside $SERVO_DATA_DIR, then removes the driver's data dir.
// Backups are deliberately kept. The server is never restarted afterwards.
func (r *Runner) uninstall(env driver.Env) error {
	r.say("checking server status")
	status, err := driver.GetStatus(r.ctx, env)
	if err != nil {
		return fmt.Errorf("status probe failed: %w", err)
	}
	r.say("server is %s", status)
	if status == driver.StatusOnline {
		if err := r.step(env, driver.VerbStop); err != nil {
			return err
		}
	}

	r.say("%s", driver.VerbUninstall)
	code, err := driver.Run(r.ctx, env, r.buf, driver.VerbUninstall)
	if err != nil {
		return err
	}
	switch code {
	case driver.ExitOK:
	case driver.ExitUnsupported:
		return fmt.Errorf("driver does not support uninstall")
	default:
		return fmt.Errorf("uninstall exited %d", code)
	}

	r.say("removing driver data dir %s", env.DataDir)
	if err := os.RemoveAll(env.DataDir); err != nil {
		return fmt.Errorf("remove data dir: %w", err)
	}
	return nil
}

// stopDoStart probes server state, stops it if online, runs fn, and restores
// the prior state.
func (r *Runner) stopDoStart(env driver.Env, fn func() error) error {
	r.say("checking server status")
	status, err := driver.GetStatus(r.ctx, env)
	if err != nil {
		return fmt.Errorf("status probe failed: %w", err)
	}
	r.say("server is %s", status)

	wasOnline := status == driver.StatusOnline
	if wasOnline {
		if err := r.step(env, driver.VerbStop); err != nil {
			return err
		}
	}

	if err := fn(); err != nil {
		// Best effort: if we stopped the server and the middle step failed,
		// try to bring it back rather than leaving it down.
		if wasOnline {
			r.say("attempting to restart server after failure")
			if startErr := r.step(env, driver.VerbStart); startErr != nil {
				r.say("restart after failure also failed: %v", startErr)
			}
		}
		return err
	}

	if wasOnline {
		return r.step(env, driver.VerbStart)
	}
	return nil
}
