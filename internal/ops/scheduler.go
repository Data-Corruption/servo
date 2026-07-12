package ops

import (
	"context"
	"errors"
	"fmt"
	"time"

	"servo/internal/driver"
	"servo/internal/platform/database/config"
	"servo/internal/types"
)

// Scheduler runs the daily restart window: at the configured HH:MM it
// restarts the game server, taking a backup first when backups are enabled
// (see the sequence table in docs/DESIGN.md). No cron syntax — it computes
// the next occurrence and sleeps, recomputing when poked (settings change)
// and after each run. It shares the Runner's single-flight mutex, so a
// scheduled window can never race a dashboard button press.
type Scheduler struct {
	runner *Runner
	wake   chan struct{}
	// pokedFlag records that the last sleep ended via Poke; only touched
	// from the Run goroutine.
	pokedFlag bool
}

func NewScheduler(r *Runner) *Scheduler {
	return &Scheduler{runner: r, wake: make(chan struct{}, 1)}
}

// Poke tells the scheduler to recompute its next run (call after settings
// changes). Never blocks.
func (s *Scheduler) Poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Run blocks until ctx is cancelled. Start it in a goroutine from the
// service command only — CLI invocations must not run scheduled windows.
func (s *Scheduler) Run(ctx context.Context) {
	log := s.runner.log
	for {
		cfg, err := config.View(s.runner.db)
		if err != nil {
			log.Errorf("scheduler: failed to read config: %v", err)
			if !s.sleep(ctx, time.Minute) {
				return
			}
			continue
		}

		if !cfg.RestartEnabled && !cfg.BackupsEnabled {
			// nothing to do; wait for a poke (or recheck hourly as a backstop)
			if !s.sleep(ctx, time.Hour) {
				return
			}
			continue
		}

		window, err := nextOccurrence(cfg.RestartTime, time.Now())
		if err != nil {
			log.Errorf("scheduler: bad restart time %q: %v", cfg.RestartTime, err)
			if !s.sleep(ctx, time.Hour) {
				return
			}
			continue
		}
		log.Infof("scheduler: next window at %s", window.Format(time.RFC1123))

		// warn players ahead of the window when configured and applicable
		lead := time.Duration(cfg.NotifyLeadMinutes) * time.Minute
		if lead > 0 && window.Sub(time.Now()) > lead {
			if !s.sleep(ctx, time.Until(window.Add(-lead))) {
				return
			}
			if s.poked() {
				continue // settings changed, recompute
			}
			s.notify(ctx, cfg.NotifyLeadMinutes)
		}

		if !s.sleep(ctx, time.Until(window)) {
			return
		}
		if s.poked() {
			continue
		}
		s.fire(cfg)
	}
}

// sleep waits for d (>=0). Returns false when ctx is done. A poke ends the
// sleep early; poked() reports it.
func (s *Scheduler) sleep(ctx context.Context, d time.Duration) bool {
	if d < 0 {
		d = 0
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-s.wake:
		s.pokedFlag = true
		return true
	case <-t.C:
		return true
	}
}

func (s *Scheduler) poked() bool {
	p := s.pokedFlag
	s.pokedFlag = false
	return p
}

// notify best-effort warns in-game players. Skipped when the runner is busy
// or the server is offline; unsupported verbs are silently fine.
func (s *Scheduler) notify(ctx context.Context, minutes int) {
	log := s.runner.log
	if s.runner.Busy() {
		return
	}
	env, err := s.runner.Env()
	if err != nil {
		return
	}
	status, err := driver.GetStatus(ctx, env)
	if err != nil || status != driver.StatusOnline {
		return
	}
	msg := fmt.Sprintf("Server restarting in %d minutes", minutes)
	if _, err := driver.RunOptional(ctx, env, driver.VerbNotify, msg); err != nil && !errors.Is(err, driver.ErrUnsupported) {
		log.Warnf("scheduler: notify failed: %v", err)
	}
}

// fire runs the window sequence:
//
//	server online,  backups on:  stop → backup → start  (OpBackup)
//	server online,  backups off: stop → start           (OpRestart)
//	server offline, backups on:  backup only            (OpBackup)
//	server offline, backups off: nothing
func (s *Scheduler) fire(cfg *types.Configuration) {
	log := s.runner.log

	if cfg.BackupsEnabled {
		// OpBackup preserves prior state, which is exactly the table above.
		s.startAndWait(OpBackup)
		return
	}

	// restart only: never start a server someone stopped on purpose
	env, err := s.runner.Env()
	if err != nil {
		log.Warnf("scheduler: window skipped: %v", err)
		return
	}
	status, err := driver.GetStatus(s.runner.ctx, env)
	if err != nil {
		log.Errorf("scheduler: window status probe failed: %v", err)
		return
	}
	if status != driver.StatusOnline {
		log.Infof("scheduler: server offline, window skipped")
		return
	}
	s.startAndWait(OpRestart)
}

func (s *Scheduler) startAndWait(op Op) {
	log := s.runner.log
	done, err := s.runner.Start(op)
	if err != nil {
		log.Warnf("scheduler: could not start %s: %v", op, err)
		return
	}
	log.Infof("scheduler: running %s window", op)
	<-done
}

// nextOccurrence returns the next time hhmm ("15:04", host-local) occurs
// strictly after now.
func nextOccurrence(hhmm string, now time.Time) (time.Time, error) {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		return time.Time{}, err
	}
	next := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next, nil
}
