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

	Status           string   `json:"status"` // online / offline / unknown
	Players          []string `json:"players"`
	PlayersSupported bool     `json:"playersSupported"`
	Metrics          string   `json:"metrics,omitempty"`

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
// operation is running (stale data beats probing a bouncing server). Probes
// use the runner's daemon context: a browser refresh must not cancel and
// poison shared state that subsequent requests consume.
func (p *Poller) Game(_ context.Context) GameState {
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
	probeCtx := p.runner.ctx
	slowDue := now.Sub(p.versionsAt) > versionTTL
	fastDue := now.Sub(p.statusAt) > statusTTL
	if slowDue || fastDue {
		p.state.Error = ""
	}

	if slowDue && p.refreshSlow(probeCtx, env) {
		p.versionsAt = now
	}
	if fastDue {
		statusChecked, complete := p.refreshFast(probeCtx, env)
		if statusChecked {
			p.state.CheckedAt = now
		}
		if complete {
			p.statusAt = now
		}
	}
	return p.state
}

func (p *Poller) refreshSlow(ctx context.Context, env driver.Env) bool {
	info, err := driver.Describe(ctx, env)
	if err != nil {
		p.recordError(err)
		return false
	}
	p.state.DriverInfo = info

	complete := true
	if value, ok := optionalOutput(ctx, env, driver.VerbVersion, &p.state.Error); ok {
		p.state.ServerVersion = value
	} else {
		complete = false
	}
	if value, ok := optionalOutput(ctx, env, driver.VerbContainerVersion, &p.state.Error); ok {
		p.state.ContainerVersion = value
	} else {
		complete = false
	}

	p.state.Stale = (info.TargetServerVersion != "" && p.state.ServerVersion != "" && info.TargetServerVersion != p.state.ServerVersion) ||
		(info.TargetContainerVersion != "" && p.state.ContainerVersion != "" && info.TargetContainerVersion != p.state.ContainerVersion)
	return complete
}

// refreshFast reports whether status itself was checked and whether all fast
// probes completed. A transient optional-probe failure retains the status but
// leaves the TTL expired so the next dashboard poll retries it.
func (p *Poller) refreshFast(ctx context.Context, env driver.Env) (statusChecked, complete bool) {
	// Env() succeeded, so ActiveDriver is set; reflect the filename
	p.state.Driver = filepath.Base(env.DriverPath)

	status, err := driver.GetStatus(ctx, env)
	if err != nil {
		if p.state.Status == "" {
			p.state.Status = driver.StatusUnknown.String()
		}
		p.state.Players = nil
		p.state.Metrics = ""
		p.recordError(err)
		return false, false
	}
	p.state.Status = status.String()

	if status != driver.StatusOnline {
		p.state.Players = nil
		p.state.Metrics = ""
		return true, true
	}

	complete = true
	players, err := driver.Players(ctx, env)
	switch {
	case errors.Is(err, driver.ErrUnsupported):
		p.state.PlayersSupported = false
		p.state.Players = nil
	case err != nil:
		p.state.Players = nil
		p.recordError(err)
		complete = false
	default:
		p.state.PlayersSupported = true
		p.state.Players = players
	}

	metrics, err := driver.RunOptional(ctx, env, driver.VerbMetrics)
	switch {
	case errors.Is(err, driver.ErrUnsupported):
		p.state.Metrics = ""
	case err != nil:
		p.state.Metrics = ""
		p.recordError(err)
		complete = false
	default:
		p.state.Metrics = metrics
	}
	return true, complete
}

// optionalOutput runs an optional info verb; unsupported or failing verbs
// yield "". The bool is false only for a real failure, allowing the caller to
// retry without waiting through the slow-data TTL.
func optionalOutput(ctx context.Context, env driver.Env, verb string, errOut *string) (string, bool) {
	out, err := driver.RunOptional(ctx, env, verb)
	if err != nil {
		if !errors.Is(err, driver.ErrUnsupported) && *errOut == "" {
			*errOut = err.Error()
		}
		return "", errors.Is(err, driver.ErrUnsupported)
	}
	return out, true
}

func (p *Poller) recordError(err error) {
	if err != nil && p.state.Error == "" {
		p.state.Error = err.Error()
	}
}
