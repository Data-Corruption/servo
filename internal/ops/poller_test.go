package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const pollerFixtureDriver = `
case "$1" in
  describe)
    echo "DRIVER_API=1"
    echo "NAME=Poller Fixture"
    echo "GAME=fixture"
    ;;
  deps) exit 0 ;;
  status)
    [ -f "$SERVO_DATA_DIR/fail-status" ] && { echo "status unavailable" >&2; exit 1; }
    [ -f "$SERVO_DATA_DIR/online" ] && exit 0 || exit 3
    ;;
  players)
    [ -f "$SERVO_DATA_DIR/fail-players" ] && { echo "players unavailable" >&2; exit 1; }
    if [ -f "$SERVO_DATA_DIR/players" ]; then
      cat "$SERVO_DATA_DIR/players"
    fi
    ;;
  metrics)
    [ -f "$SERVO_DATA_DIR/fail-metrics" ] && { echo "metrics unavailable" >&2; exit 1; }
    echo "60 FPS"
    ;;
  version)
    [ -f "$SERVO_DATA_DIR/fail-version" ] && { echo "version unavailable" >&2; exit 1; }
    echo "v1.2.3"
    ;;
  container-version) exit 4 ;;
  *) exit 4 ;;
esac
`

func setupPollerFixture(t *testing.T) (*fixture, *Poller) {
	t.Helper()
	f := newFixture(t, pollerFixtureDriver)
	if err := os.MkdirAll(f.data, 0755); err != nil {
		t.Fatal(err)
	}
	writePollerFile(t, f, "online", "")
	return f, NewPoller(f.runner)
}

func writePollerFile(t *testing.T, f *fixture, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.data, name), []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
}

func removePollerFile(t *testing.T, f *fixture, name string) {
	t.Helper()
	if err := os.Remove(filepath.Join(f.data, name)); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestPollerUsesDaemonContext(t *testing.T) {
	_, poller := setupPollerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	state := poller.Game(ctx)
	if state.Status != "online" {
		t.Fatalf("status = %q, want online (error: %q)", state.Status, state.Error)
	}
	if state.ServerVersion != "v1.2.3" || state.Metrics != "60 FPS" {
		t.Fatalf("state missing detached probe data: %+v", state)
	}
}

func TestPollerPreservesStatusAndRetriesTransientFailure(t *testing.T) {
	f, poller := setupPollerFixture(t)
	writePollerFile(t, f, "players", "alice\n")

	first := poller.Game(context.Background())
	if first.Status != "online" || len(first.Players) != 1 || first.Metrics != "60 FPS" {
		t.Fatalf("initial state = %+v", first)
	}

	writePollerFile(t, f, "fail-status", "")
	poller.statusAt = poller.statusAt.Add(-statusTTL - 1)
	failed := poller.Game(context.Background())
	if failed.Status != "online" {
		t.Fatalf("transient failure replaced known status: %+v", failed)
	}
	if failed.Players != nil || failed.Metrics != "" {
		t.Fatalf("transient status failure retained live data: %+v", failed)
	}
	if failed.Error == "" {
		t.Fatal("transient failure was not surfaced")
	}
	failedAt := poller.statusAt

	removePollerFile(t, f, "fail-status")
	recovered := poller.Game(context.Background())
	if recovered.Error != "" || len(recovered.Players) != 1 || recovered.Metrics != "60 FPS" {
		t.Fatalf("state did not recover on immediate retry: %+v", recovered)
	}
	if !poller.statusAt.After(failedAt) {
		t.Fatal("successful retry did not advance fast TTL")
	}
}

func TestPollerDistinguishesEmptyAndUnavailablePlayers(t *testing.T) {
	f, poller := setupPollerFixture(t)

	empty := poller.Game(context.Background())
	if !empty.PlayersSupported || empty.Players == nil || len(empty.Players) != 0 {
		t.Fatalf("empty player list should be known and supported: %+v", empty)
	}
	emptyJSON, err := json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(emptyJSON, []byte(`"players":[]`)) {
		t.Fatalf("empty roster JSON must remain distinguishable: %s", emptyJSON)
	}

	writePollerFile(t, f, "fail-players", "")
	poller.statusAt = poller.statusAt.Add(-statusTTL - 1)
	unavailable := poller.Game(context.Background())
	if !unavailable.PlayersSupported || unavailable.Players != nil {
		t.Fatalf("failed player probe should be unavailable, not zero: %+v", unavailable)
	}
	unavailableJSON, err := json.Marshal(unavailable)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(unavailableJSON, []byte(`"players":null`)) {
		t.Fatalf("unavailable roster JSON must remain distinguishable: %s", unavailableJSON)
	}
	if unavailable.Error == "" {
		t.Fatal("player probe failure was not surfaced")
	}

	removePollerFile(t, f, "fail-players")
	writePollerFile(t, f, "fail-metrics", "")
	recoveredPlayers := poller.Game(context.Background())
	if recoveredPlayers.Players == nil || recoveredPlayers.Metrics != "" {
		t.Fatalf("metrics failure should not discard a valid empty roster: %+v", recoveredPlayers)
	}
}

func TestPollerRetriesFailedSlowProbeWithoutTTLDelay(t *testing.T) {
	f, poller := setupPollerFixture(t)
	writePollerFile(t, f, "fail-version", "")

	failed := poller.Game(context.Background())
	if failed.ServerVersion != "" || failed.Error == "" {
		t.Fatalf("expected failed version probe: %+v", failed)
	}
	failedAt := poller.versionsAt

	removePollerFile(t, f, "fail-version")
	recovered := poller.Game(context.Background())
	if recovered.ServerVersion != "v1.2.3" || recovered.Error != "" {
		t.Fatalf("slow probe did not recover immediately: %+v", recovered)
	}
	if !poller.versionsAt.After(failedAt) {
		t.Fatal("successful slow retry did not advance version TTL")
	}
}
