// Package app implements the application, following the dependency injection pattern.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"servo/internal/build"
	"servo/internal/ops"
	"servo/internal/platform/database"
	"servo/internal/platform/database/config"
	// --- BEGIN UPDATE CHECK ---
	"servo/internal/platform/release"
	// --- END UPDATE CHECK ---
	"servo/internal/platform/secrets"
	// --- BEGIN REMOTE UPDATE ---
	"servo/internal/types"
	// --- END REMOTE UPDATE ---
	"servo/internal/ui"
	"servo/pkg/x"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Data-Corruption/lmdb-go/wrap"
	"github.com/Data-Corruption/stdx/xhttp"
	"github.com/Data-Corruption/stdx/xlog"
	"github.com/urfave/cli/v3"
	"golang.org/x/mod/semver"
)

type CleanupFunc func() error

/*
App represents the application, following the dependency injection pattern.

It provides:
  - build-time variables
  - injected services
  - lifecycle management
  - update handlers and migration synchronization
*/
type App struct {
	// injected services, etc.

	DB          *wrap.DB
	Log         *xlog.Logger
	Server      *xhttp.Server  // primary TLS dashboard listener
	ProxyServer *xhttp.Server  // loopback HTTP listener for local reverse proxies (nil when disabled)
	Secrets     *secrets.Store // self-signed TLS cert (+ any app secret material)
	UI          *ui.UI
	Ops         *ops.Runner    // single-flight game server operation runner
	Poller      *ops.Poller    // TTL-cached game status for the dashboard
	Sched       *ops.Scheduler // daily restart/backup window; Run() only in service mode
	BaseURL     string // e.g., "https://localhost:8829"
	UserAgent  string // e.g., "Mozilla/5.0 (compatible; <Name>/1.2.3; +<ContactURL>)"
	StorageDir string // (e.g., ~/.<Name>)
	RuntimeDir string // (e.g., XDG_RUNTIME_DIR/<Name>, fallback to /tmp/<Name>-USER)
	TempDir    string // (e.g., StorageDir/tmp)
	// game server dirs (all under StorageDir)
	DriversDir     string // driver executables, installed over SSH
	DriverDataDir  string // SERVO_DATA_DIR handed to drivers
	BackupsDir     string // SERVO_BACKUP_DIR, retention-pruned
	BackgroundsDir string // uploaded login/dashboard background images
	// --- BEGIN UPDATE CHECK ---
	ReleaseSource release.ReleaseSource
	// --- END UPDATE CHECK ---
	// TestMode is baked in via `build.sh --test` (local builds only). It
	// bypasses HTTP auth, isolates storage/runtime in "-test" dirs, and
	// forces debug logging.
	TestMode  bool
	buildInfo build.BuildInfo // read-only

	// lifecycle management

	cleanup       []CleanupFunc
	cleanupOnce   sync.Once
	postCleanup   CleanupFunc
	postCleanupMu sync.Mutex
	// --- BEGIN REMOTE UPDATE ---
	uOnce sync.Once // prep update only once before exiting
	// --- END REMOTE UPDATE ---
	// Inside commands, you can use <-a.Context.Done() to check for cancellation.
	// You don't need to do this for the example service, the http server
	// wrapper has its own signal listener.
	Context context.Context
}

func New(buildInfo build.BuildInfo) *App {
	return &App{
		buildInfo: buildInfo,
	}
}

func (a *App) BuildInfo() build.BuildInfo {
	return a.buildInfo
}

func (a *App) Init(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	a.TestMode = a.buildInfo.TestMode

	// paths. Test-mode builds use isolated dirs (~/.<name>-test) so a dev build
	// never touches a real install's data: test mode disables HTTP auth, which
	// would otherwise expose the real DB and secrets unauthenticated.
	pathName := a.buildInfo.Name
	if a.TestMode {
		pathName += "-test"
	}
	var err error
	if a.StorageDir, err = getStoragePath(pathName); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(a.StorageDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create storage dir: %w", err)
	}
	if a.RuntimeDir, err = getRuntimePath(pathName); err != nil {
		return nil, err
	}
	if a.TestMode {
		fmt.Printf("test mode: using isolated storage dir %s\n", a.StorageDir)
		// Test mode is the hook point for app-specific test-only diagnostics
		// (e.g. printing secrets, protocol tracers). Add them here so they can
		// never run against a real install's data.
	}
	a.TempDir = filepath.Join(a.StorageDir, "tmp")
	if err := os.MkdirAll(a.TempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	// game server dirs
	a.DriversDir = filepath.Join(a.StorageDir, "drivers")
	a.DriverDataDir = filepath.Join(a.StorageDir, "driver-data")
	a.BackupsDir = filepath.Join(a.StorageDir, "backups")
	a.BackgroundsDir = filepath.Join(a.StorageDir, "backgrounds")
	for _, dir := range []string{a.DriversDir, a.DriverDataDir, a.BackupsDir, a.BackgroundsDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create %s: %w", dir, err)
		}
	}
	// control-plane secrets (dashboard TLS cert) live outside the config blob
	// in their own protected directory.
	if a.Secrets, err = secrets.New(a.StorageDir, a.buildInfo.Name); err != nil {
		return nil, fmt.Errorf("failed to initialize secrets store: %w", err)
	}
	if cmd.String("log") == "debug" || a.TestMode {
		fmt.Println("StorageDir:", a.StorageDir)
		fmt.Println("RuntimeDir:", a.RuntimeDir)
		fmt.Println("TempDir:", a.TempDir)
	}

	// migration guard before touching anything
	if !cmd.Bool("migrate") {
		if err := a.mguard(); err != nil {
			return ctx, fmt.Errorf("failed to setup migration guard: %w", err)
		}
	} else {
		// migrate flag set, we are the migrator instance, proceed without guard
		fmt.Printf("%s version %s\n", a.buildInfo.Name, a.buildInfo.Version)
	}

	// logger
	a.Log, err = xlog.New(filepath.Join(a.StorageDir, "logs"), "debug")
	if err != nil {
		return ctx, fmt.Errorf("failed to initialize logger: %w", err)
	}
	a.AddCleanup(a.Log.Close)

	a.Log.Debugf("Starting %s, version: %s, storage path: %s, runtime path: %s",
		a.buildInfo.Name, a.buildInfo.Version, a.StorageDir, a.RuntimeDir)

	// database
	if a.DB, err = database.New(filepath.Join(a.StorageDir, "db"), a.Log); err != nil {
		return ctx, fmt.Errorf("failed to initialize database: %w", err)
	}
	a.AddCleanup(func() error {
		// --- BEGIN REMOTE UPDATE ---
		// store PreUpdateVersion on shutdown, unless we are the migrator instance
		if !cmd.Bool("migrate") {
			if _, err := config.Update(a.DB, func(cfg *types.Configuration) error {
				cfg.PreUpdateVersion = a.buildInfo.Version
				return nil
			}); err != nil {
				a.Log.Errorf("failed to set PreUpdateVersion on shutdown: %v", err)
			}
		}
		// --- END REMOTE UPDATE ---
		a.DB.Close()
		return nil
	})
	a.Log.Debug("Database initialized")

	// get config
	cfg, err := config.View(a.DB)
	if err != nil {
		return ctx, fmt.Errorf("failed to view config: %w", err)
	}

	// override port (useful for testing); maps onto the HTTPS bind
	if oPort := cmd.Int("port"); oPort != 0 {
		cfg.UIBind = fmt.Sprintf(":%d", oPort)
	}

	// calculate BaseURL (simplified, just for local cli)
	a.BaseURL = BindToBaseURL(cfg.UIBind)
	a.Log.Debugf("Base URL: %s", a.BaseURL)

	// set UserAgent
	mmVer := strings.TrimPrefix(semver.MajorMinor(a.buildInfo.Version), "v")
	a.UserAgent = fmt.Sprintf("Mozilla/5.0 (compatible; %s/%s; +%s)", a.buildInfo.Name, mmVer, a.buildInfo.ContactURL)

	// set log level; test-mode builds always log at debug for deep test visibility
	logLevel := x.Ternary(cmd.IsSet("log"), cmd.String("log"), cfg.LogLevel)
	if a.TestMode {
		logLevel = "debug"
	}
	if err := a.Log.SetLevel(logLevel); err != nil {
		return ctx, fmt.Errorf("failed to set log level: %w", err)
	}
	// put logger into context
	ctx = xlog.IntoContext(ctx, a.Log)

	// store context for use in update checking, etc.
	a.Context = ctx

	// load frontend
	if a.UI, err = ui.New(); err != nil {
		return ctx, fmt.Errorf("failed to load UI: %w", err)
	}

	// game server op runner + cached status poller
	a.Ops = ops.New(ctx, a.DB, a.Log, ops.Paths{
		DriversDir: a.DriversDir,
		DataDir:    a.DriverDataDir,
		BackupsDir: a.BackupsDir,
		AppVersion: a.buildInfo.Version,
	})
	a.Poller = ops.NewPoller(a.Ops)
	a.Sched = ops.NewScheduler(a.Ops)

	// --- BEGIN UPDATE CHECK ---
	// initialize release source for update checking
	a.ReleaseSource = &release.GenericReleaseSource{}

	// update checking
	if err := a.startAutoChecker(cfg); err != nil {
		return ctx, fmt.Errorf("failed to start auto checker: %w", err)
	}
	// --- END UPDATE CHECK ---

	return ctx, nil
}

func (a *App) Close() {
	a.cleanupOnce.Do(func() {
		// call cleanup funcs in reverse order
		for i := len(a.cleanup) - 1; i >= 0; i-- {
			if err := a.cleanup[i](); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to clean up: %v\n", err)
			}
		}
		// call post cleanup func if set
		a.postCleanupMu.Lock()
		defer a.postCleanupMu.Unlock()
		if a.postCleanup != nil {
			time.Sleep(500 * time.Millisecond) // not sure if i need this actually
			if err := a.postCleanup(); err != nil {
				fmt.Fprintf(os.Stderr, "Post cleanup failure: %v\n", err)
			}
		}
	})
}

func (a *App) AddCleanup(f func() error) {
	a.cleanup = append(a.cleanup, f)
}

var ErrPostCleanupSet = errors.New("post cleanup already set")

// SetPostCleanup sets the post cleanup func. It returns an error if it's already set.
func (a *App) SetPostCleanup(f func() error) error {
	a.postCleanupMu.Lock()
	defer a.postCleanupMu.Unlock()

	if a.postCleanup != nil {
		return ErrPostCleanupSet
	}

	a.postCleanup = f
	return nil
}

// getStoragePath calculates the storage path for the application (~/.appName).
func getStoragePath(appName string) (string, error) {
	// get home dir
	home, err := x.GetUserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "."+appName), nil
}

// getRuntimePath calculates the runtime path for the application.
// Prefers XDG_RUNTIME_DIR, falls back to /tmp/appName-USER.
func getRuntimePath(appName string) (string, error) {
	// prefer XDG_RUNTIME_DIR (typically /run/user/UID)
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		return filepath.Join(runtimeDir, appName), nil
	}

	// fallback for non-systemd systems
	// include username to avoid conflicts in shared /tmp
	username := os.Getenv("USER")
	if username == "" {
		u, err := user.Current()
		if err != nil {
			return "", fmt.Errorf("cannot determine current user: %w", err)
		}
		username = u.Username
	}

	return filepath.Join("/tmp", appName+"-"+username), nil
}

// BindToBaseURL turns a listen bind like ":8829" or "0.0.0.0:8829" into a
// human-facing dashboard URL ("https://localhost:8829"). The dashboard always
// serves self-signed HTTPS on this bind.
func BindToBaseURL(bind string) string {
	port := strconv.Itoa(build.Info().ServiceDefaultPort)
	if i := strings.LastIndex(bind, ":"); i >= 0 && i+1 < len(bind) {
		port = bind[i+1:]
	}
	return fmt.Sprintf("https://localhost:%s", port)
}
