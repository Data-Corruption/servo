package ops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"servo/internal/platform/database"
	"servo/internal/platform/database/config"
	"servo/internal/types"

	"github.com/Data-Corruption/stdx/xlog"
)

// fixtureDriver logs each verb invocation to $SERVO_DATA_DIR/calls and
// simulates a stateful server via a $SERVO_DATA_DIR/online marker file.
const fixtureDriver = `
calls="$SERVO_DATA_DIR/calls"
echo "$@" >> "$calls"
case "$1" in
  describe) echo "DRIVER_API=1"; echo "NAME=Fixture"; echo "GAME=fixture" ;;
  deps) exit 0 ;;
  status) [ -f "$SERVO_DATA_DIR/online" ] && exit 0 || exit 3 ;;
  start) touch "$SERVO_DATA_DIR/online" ;;
  stop) rm -f "$SERVO_DATA_DIR/online" ;;
  install) echo "installed" ;;
  update) echo "updated" ;;
  backup)
    f="$SERVO_BACKUP_DIR/backup-$(date +%s%N).tar.gz"
    echo "fake archive" > "$f"
    echo "$f"
    ;;
  restore) echo "restored from $2" ;;
  *) exit 4 ;;
esac
`

type fixture struct {
	runner *Runner
	data   string
	backup string
}

func newFixture(t *testing.T, script string) *fixture {
	t.Helper()
	root := t.TempDir()
	drivers := filepath.Join(root, "drivers")
	data := filepath.Join(root, "data")
	backups := filepath.Join(root, "backups")
	for _, d := range []string{drivers, data, backups} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(drivers, "fixture.sh"), []byte("#!/bin/sh\n"+script), 0755); err != nil {
		t.Fatal(err)
	}

	logger, err := xlog.New(filepath.Join(root, "logs"), "error")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logger.Close() })

	db, err := database.New(filepath.Join(root, "db"), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := config.Update(db, func(cfg *types.Configuration) error {
		cfg.ActiveDriver = "fixture.sh"
		cfg.BackupRetention = 2
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	runner := New(context.Background(), db, logger, Paths{
		DriversDir: drivers,
		DataDir:    data,
		BackupsDir: backups,
		AppVersion: "vTEST",
	})
	return &fixture{runner: runner, data: data, backup: backups}
}

func (f *fixture) calls(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.data, "calls"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	return strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(data)), "\n", " "))
}

func (f *fixture) resetCalls() {
	os.Remove(filepath.Join(f.data, "calls"))
}

func (f *fixture) runOp(t *testing.T, op Op, args ...string) Result {
	t.Helper()
	done, err := f.runner.Start(op, args...)
	if err != nil {
		t.Fatalf("start %s: %v", op, err)
	}
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("op %s did not finish", op)
	}
	return *f.runner.Status().Last
}

func TestOpSequences(t *testing.T) {
	f := newFixture(t, fixtureDriver)

	res := f.runOp(t, OpStart)
	if !res.Success {
		t.Fatalf("start failed: %+v", res)
	}
	if got := f.calls(t); !strings.Contains(strings.Join(got, " "), "start") {
		t.Fatalf("calls = %v", got)
	}

	// backup while online: status → stop → backup → start
	f.resetCalls()
	res = f.runOp(t, OpBackup)
	if !res.Success {
		t.Fatalf("backup failed: %+v", res)
	}
	calls := strings.Join(f.calls(t), " ")
	if calls != "status stop backup start" {
		t.Fatalf("backup sequence = %q", calls)
	}

	// stop, then backup while offline: no stop/start around it
	f.runOp(t, OpStop)
	f.resetCalls()
	res = f.runOp(t, OpBackup)
	if !res.Success {
		t.Fatalf("offline backup failed: %+v", res)
	}
	calls = strings.Join(f.calls(t), " ")
	if calls != "status backup" {
		t.Fatalf("offline backup sequence = %q", calls)
	}

	// restart = stop start
	f.resetCalls()
	if res = f.runOp(t, OpRestart); !res.Success {
		t.Fatalf("restart failed: %+v", res)
	}
	if calls = strings.Join(f.calls(t), " "); calls != "stop start" {
		t.Fatalf("restart sequence = %q", calls)
	}
}

func TestBackupRetentionPruning(t *testing.T) {
	f := newFixture(t, fixtureDriver)
	for i := 0; i < 4; i++ {
		if res := f.runOp(t, OpBackup); !res.Success {
			t.Fatalf("backup %d failed: %+v", i, res)
		}
		time.Sleep(10 * time.Millisecond) // distinct mtimes
	}
	backups, err := ListBackups(f.backup)
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 2 { // retention = 2 in fixture config
		t.Fatalf("expected 2 backups after pruning, got %d", len(backups))
	}
}

func TestRestoreValidatesArchive(t *testing.T) {
	f := newFixture(t, fixtureDriver)

	res := f.runOp(t, OpRestore, "nope.tar.gz")
	if res.Success {
		t.Fatal("restore of unknown archive should fail")
	}

	if res = f.runOp(t, OpBackup); !res.Success {
		t.Fatalf("backup failed: %+v", res)
	}
	backups, _ := ListBackups(f.backup)
	if len(backups) == 0 {
		t.Fatal("no backups")
	}
	f.resetCalls()
	if res = f.runOp(t, OpRestore, backups[0].Name); !res.Success {
		t.Fatalf("restore failed: %+v", res)
	}
	if calls := strings.Join(f.calls(t), " "); !strings.Contains(calls, "restore") {
		t.Fatalf("restore not called: %q", calls)
	}
}

func TestBusyAndFailureRecovery(t *testing.T) {
	f := newFixture(t, fixtureDriver+"\n") // base fixture

	// swap in a driver whose update fails, to test start-after-failure
	failing := strings.Replace(fixtureDriver, `update) echo "updated" ;;`, `update) echo "kaboom" >&2; exit 1 ;;`, 1)
	path := filepath.Join(filepath.Dir(f.data), "drivers", "fixture.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+failing), 0755); err != nil {
		t.Fatal(err)
	}

	f.runOp(t, OpStart)
	f.resetCalls()
	res := f.runOp(t, OpUpdate)
	if res.Success {
		t.Fatal("update should have failed")
	}
	// server was online: status → stop → update (fails) → start (recovery)
	calls := strings.Join(f.calls(t), " ")
	if calls != "status stop update start" {
		t.Fatalf("failure recovery sequence = %q", calls)
	}
	if !strings.Contains(res.Tail, "kaboom") {
		t.Fatalf("tail missing driver stderr: %q", res.Tail)
	}
}

func TestBusyRejection(t *testing.T) {
	slow := fixtureDriver
	slow = strings.Replace(slow, `install) echo "installed" ;;`, `install) sleep 2 ;;`, 1)
	f := newFixture(t, slow)

	done, err := f.runner.Start(OpInstall)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.runner.Start(OpStart); !errors.Is(err, ErrBusy) {
		t.Fatalf("expected ErrBusy, got %v", err)
	}
	if !f.runner.Busy() {
		t.Fatal("runner should report busy")
	}
	<-done
	if f.runner.Busy() {
		t.Fatal("runner should be idle")
	}
}

func TestNoDriver(t *testing.T) {
	f := newFixture(t, fixtureDriver)
	if _, err := config.Update(f.runner.db, func(cfg *types.Configuration) error {
		cfg.ActiveDriver = ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runner.Start(OpStart); !errors.Is(err, ErrNoDriver) {
		t.Fatalf("expected ErrNoDriver, got %v", err)
	}
}

func TestRingBuffer(t *testing.T) {
	b := newRing()
	fmt.Fprint(b, "hello ")
	fmt.Fprint(b, "world")

	out, offset := b.ReadFrom(0)
	if string(out) != "hello world" || offset != 11 {
		t.Fatalf("got %q offset %d", out, offset)
	}
	// incremental read
	fmt.Fprint(b, "!")
	out, offset = b.ReadFrom(offset)
	if string(out) != "!" || offset != 12 {
		t.Fatalf("incremental: got %q offset %d", out, offset)
	}
	// nothing new
	out, _ = b.ReadFrom(offset)
	if len(out) != 0 {
		t.Fatalf("expected empty, got %q", out)
	}

	// overflow keeps only the newest ringCap bytes
	big := bytes.Repeat([]byte("x"), ringCap+100)
	b.Write(big)
	out, _ = b.ReadFrom(0)
	if len(out) != ringCap {
		t.Fatalf("overflow: len=%d want %d", len(out), ringCap)
	}
}
