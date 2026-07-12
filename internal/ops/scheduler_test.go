package ops

import (
	"context"
	"strings"
	"testing"
	"time"

	"servo/internal/platform/database/config"
	"servo/internal/types"
)

func TestNextOccurrence(t *testing.T) {
	now := time.Date(2026, 7, 12, 10, 30, 0, 0, time.Local)

	next, err := nextOccurrence("11:00", now)
	if err != nil {
		t.Fatal(err)
	}
	if next.Day() != 12 || next.Hour() != 11 || next.Minute() != 0 {
		t.Fatalf("future today: got %v", next)
	}

	next, err = nextOccurrence("04:00", now)
	if err != nil {
		t.Fatal(err)
	}
	if next.Day() != 13 || next.Hour() != 4 {
		t.Fatalf("tomorrow: got %v", next)
	}

	// exactly now rolls to tomorrow (strictly after)
	next, err = nextOccurrence("10:30", now)
	if err != nil {
		t.Fatal(err)
	}
	if next.Day() != 13 {
		t.Fatalf("exact now should roll over: got %v", next)
	}

	if _, err := nextOccurrence("25:99", now); err == nil {
		t.Fatal("bad time should error")
	}
}

// TestSchedulerFire exercises the window sequences directly (not the timing
// loop — sleeping until 4am in a test is not a thing).
func TestSchedulerFire(t *testing.T) {
	f := newFixture(t, fixtureDriver)
	sched := NewScheduler(f.runner)

	// online + backups on → status stop backup start (OpBackup preserves state)
	f.runOp(t, OpStart)
	setSchedule(t, f, true, true)
	f.resetCalls()
	sched.fire(mustCfg(t, f))
	if calls := strings.Join(f.calls(t), " "); calls != "status stop backup start" {
		t.Fatalf("online+backups = %q", calls)
	}

	// online + backups off → stop start (plus the scheduler's own status probe)
	setSchedule(t, f, true, false)
	f.resetCalls()
	sched.fire(mustCfg(t, f))
	if calls := strings.Join(f.calls(t), " "); calls != "status stop start" {
		t.Fatalf("online+nobackups = %q", calls)
	}

	// offline + backups off → status probe only, no restart
	f.runOp(t, OpStop)
	f.resetCalls()
	sched.fire(mustCfg(t, f))
	if calls := strings.Join(f.calls(t), " "); calls != "status" {
		t.Fatalf("offline+nobackups = %q", calls)
	}

	// offline + backups on → backup only
	setSchedule(t, f, true, true)
	f.resetCalls()
	sched.fire(mustCfg(t, f))
	if calls := strings.Join(f.calls(t), " "); calls != "status backup" {
		t.Fatalf("offline+backups = %q", calls)
	}
}

func TestSchedulerNotify(t *testing.T) {
	f := newFixture(t, fixtureDriver)
	sched := NewScheduler(f.runner)

	// offline: no notify call
	f.resetCalls()
	sched.notify(context.Background(), 5)
	if calls := strings.Join(f.calls(t), " "); strings.Contains(calls, "notify") {
		t.Fatalf("offline notify should be skipped: %q", calls)
	}

	// online: fixture declines notify (exit 4) — must not error/log fatal
	f.runOp(t, OpStart)
	f.resetCalls()
	sched.notify(context.Background(), 5)
	if calls := strings.Join(f.calls(t), " "); !strings.Contains(calls, "notify") {
		t.Fatalf("online notify should be attempted: %q", calls)
	}
}

func setSchedule(t *testing.T, f *fixture, restart, backups bool) {
	t.Helper()
	if _, err := config.Update(f.runner.db, func(cfg *types.Configuration) error {
		cfg.RestartEnabled = restart
		cfg.BackupsEnabled = backups
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func mustCfg(t *testing.T, f *fixture) *types.Configuration {
	t.Helper()
	cfg, err := config.View(f.runner.db)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
