package ops

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"servo/internal/driver"
)

// GameState is the cached dashboard view of the game server.
type GameState struct {
	Driver     string      `json:"driver"`     // active driver filename, "" = none
	DriverInfo driver.Info `json:"driverInfo"` // from describe

	Status  string `json:"status"` // online / offline / unknown
	Players []string `json:"players,omitempty"`
	PlayersSupported bool `json:"playersSupported"`

	// live versions (empty when unsupported / unknown)
	ServerVersion    string `json:"serverVersion"`
	ContainerVersion string `json:"containerVersion"`
	// soft staleness: driver's target version differs from live
	Stale bool `json:"stale"`

	CheckedAt time.Time `json:"checkedAt"`
	Error     string    `json:"error,omitempty"` // last probe error, informational
}

const (
	statusTTL  = 5 * time.Second
	versionTTL = 10 * time.Minute
)

// Poller lazily refreshes game state with TTL caching: fast facts (status,
// players) every few seconds, slow facts (describe, versions) every few
// minutes. Refreshes are skipped while an op runs — the server is likely
// mid-bounce, and the activity panel is the interesting thing then anyway.
type Poller struct {
	runner *Runner

	mu         sync.Mutex
	state      GameState
	statusAt   time.Time
	versionsAt time.Time
}

func NewPoller(r *Runner) *Poller {
	return &Poller{runner: r}
}

// Invalidate drops the cache, e.g. after driver activation.
func (p *Poller) Invalidate() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state = GameState{}
	p.statusAt = time.Time{}
	p.versionsAt = time.Time{}
}

// Game returns the current cached state, refreshing expired parts unless an
// operation is running (stale data beats probing a bouncing server).
func (p *Poller) Game(ctx context.Context) GameState {
	p.mu.Lock()
	defer p.mu.Unlock()

	env, err := p.runner.Env()
	if err != nil {
		if errors.Is(err, ErrNoDriver) {
			return GameState{}
		}
		p.state.Error = err.Error()
		return p.state
	}

	if p.runner.Busy() {
		return p.state
	}

	now := time.Now()
	p.state.Error = ""

	if now.Sub(p.versionsAt) > versionTTL {
		p.refreshSlow(ctx, env)
		p.versionsAt = now
	}
	if now.Sub(p.statusAt) > statusTTL {
		p.refreshFast(ctx, env)
		p.statusAt = now
	}
	p.state.CheckedAt = now
	return p.state
}

func (p *Poller) refreshSlow(ctx context.Context, env driver.Env) {
	info, err := driver.Describe(ctx, env)
	if err != nil {
		p.state.Error = err.Error()
		return
	}
	p.state.DriverInfo = info

	p.state.ServerVersion = optionalOutput(ctx, env, driver.VerbVersion, &p.state.Error)
	p.state.ContainerVersion = optionalOutput(ctx, env, driver.VerbContainerVersion, &p.state.Error)

	p.state.Stale = (info.TargetServerVersion != "" && p.state.ServerVersion != "" && info.TargetServerVersion != p.state.ServerVersion) ||
		(info.TargetContainerVersion != "" && p.state.ContainerVersion != "" && info.TargetContainerVersion != p.state.ContainerVersion)
}

func (p *Poller) refreshFast(ctx context.Context, env driver.Env) {
	// Env() succeeded, so ActiveDriver is set; reflect the filename
	p.state.Driver = filepath.Base(env.DriverPath)

	status, err := driver.GetStatus(ctx, env)
	if err != nil {
		p.state.Status = driver.StatusUnknown.String()
		p.state.Error = err.Error()
		return
	}
	p.state.Status = status.String()

	if status != driver.StatusOnline {
		p.state.Players = nil
		return
	}
	players, err := driver.Players(ctx, env)
	switch {
	case errors.Is(err, driver.ErrUnsupported):
		p.state.PlayersSupported = false
		p.state.Players = nil
	case err != nil:
		p.state.Error = err.Error()
	default:
		p.state.PlayersSupported = true
		p.state.Players = players
	}
}

// optionalOutput runs an optional info verb; unsupported or failing verbs
// yield "" (versions are best-effort, never gates).
func optionalOutput(ctx context.Context, env driver.Env, verb string, errOut *string) string {
	out, err := driver.RunOptional(ctx, env, verb)
	if err != nil {
		if !errors.Is(err, driver.ErrUnsupported) && *errOut == "" {
			*errOut = err.Error()
		}
		return ""
	}
	return out
}
