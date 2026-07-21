package driver

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeDriver writes an executable shell script into a temp dir and returns
// its path.
func writeDriver(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test-driver.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func testEnv(path string) Env {
	return Env{DriverPath: path, BackupDir: "/tmp/b", DataDir: "/tmp/d", AppVersion: "vTEST"}
}

func TestRunExitCodes(t *testing.T) {
	path := writeDriver(t, `
case "$1" in
  status) exit 3 ;;
  players) exit 4 ;;
  start) echo started; exit 0 ;;
  stop) echo "boom" >&2; exit 1 ;;
esac
`)
	ctx := context.Background()
	env := testEnv(path)

	var buf bytes.Buffer
	code, err := Run(ctx, env, &buf, VerbStart)
	if err != nil || code != ExitOK {
		t.Fatalf("start: code=%d err=%v", code, err)
	}
	if got := strings.TrimSpace(buf.String()); got != "started" {
		t.Fatalf("start output = %q", got)
	}

	code, err = Run(ctx, env, nil, VerbStatus)
	if err != nil || code != ExitStopped {
		t.Fatalf("status: code=%d err=%v", code, err)
	}

	code, err = Run(ctx, env, nil, VerbPlayers)
	if err != nil || code != ExitUnsupported {
		t.Fatalf("players: code=%d err=%v", code, err)
	}

	buf.Reset()
	code, err = Run(ctx, env, &buf, VerbStop)
	if err != nil || code != 1 {
		t.Fatalf("stop: code=%d err=%v", code, err)
	}
	if !strings.Contains(buf.String(), "boom") {
		t.Fatalf("stderr not captured: %q", buf.String())
	}
}

func TestRunEnvAndArgs(t *testing.T) {
	path := writeDriver(t, `
if [ "$1" = "notify" ]; then
  echo "msg=$2 backup=$SERVO_BACKUP_DIR data=$SERVO_DATA_DIR ver=$SERVO_VERSION"
fi
`)
	var buf bytes.Buffer
	code, err := Run(context.Background(), testEnv(path), &buf, VerbNotify, "hello world")
	if err != nil || code != ExitOK {
		t.Fatalf("code=%d err=%v", code, err)
	}
	want := "msg=hello world backup=/tmp/b data=/tmp/d ver=vTEST"
	if got := strings.TrimSpace(buf.String()); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRunTimeout(t *testing.T) {
	path := writeDriver(t, `sleep 30`)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Run(ctx, testEnv(path), nil, VerbStatus)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("kill took too long: %v", elapsed)
	}
}

func TestRunMissingDriver(t *testing.T) {
	env := testEnv("/nonexistent/driver.sh")
	if _, err := Run(context.Background(), env, nil, VerbStatus); err == nil {
		t.Fatal("expected error for missing driver")
	}
}

func TestParseInfo(t *testing.T) {
	good := []byte(`
DRIVER_API=1
NAME=Palworld (Podman, Fedora)
GAME=palworld
CONTAINERIZED=true
TARGET_CONTAINER_VERSION=v1.2.3
FUTURE_KEY=ignored
`)
	info, err := ParseInfo(good)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "Palworld (Podman, Fedora)" || info.Game != "palworld" ||
		!info.Containerized || info.TargetContainerVersion != "v1.2.3" ||
		info.TargetServerVersion != "" {
		t.Fatalf("bad parse: %+v", info)
	}

	cases := map[string]string{
		"missing api":  "NAME=x\n",
		"future api":   "DRIVER_API=2\nNAME=x\n",
		"bad api":      "DRIVER_API=banana\nNAME=x\n",
		"missing name": "DRIVER_API=1\n",
		"bad bool":     "DRIVER_API=1\nNAME=x\nCONTAINERIZED=maybe\n",
	}
	for label, in := range cases {
		if _, err := ParseInfo([]byte(in)); err == nil {
			t.Errorf("%s: expected error", label)
		}
	}

	// garbage lines are skipped, not fatal
	if _, err := ParseInfo([]byte("DRIVER_API=1\nNAME=x\nthis is not a kv line\n")); err != nil {
		t.Errorf("garbage line should be ignored: %v", err)
	}
}

func TestDescribeDepsStatus(t *testing.T) {
	path := writeDriver(t, `
case "$1" in
  describe)
    echo "DRIVER_API=1"
    echo "NAME=Fixture"
    echo "GAME=fixture"
    ;;
  deps)
    echo "podman"
    echo "frobnicator"
    exit 1
    ;;
  status) exit 0 ;;
esac
`)
	ctx := context.Background()
	env := testEnv(path)

	info, missing, err := Validate(ctx, env)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "Fixture" {
		t.Fatalf("info = %+v", info)
	}
	if len(missing) != 2 || missing[0] != "podman" || missing[1] != "frobnicator" {
		t.Fatalf("missing = %v", missing)
	}

	status, err := GetStatus(ctx, env)
	if err != nil || status != StatusOnline {
		t.Fatalf("status=%v err=%v", status, err)
	}
}

func TestPlayersAndOptional(t *testing.T) {
	path := writeDriver(t, `
case "$1" in
  players)
    printf 'harmless warning\n' >&2
    printf 'alice\nbob\n'
    ;;
  metrics)
    printf 'another warning\n' >&2
    printf '60 FPS\n'
    ;;
  version) exit 4 ;;
esac
`)
	ctx := context.Background()
	env := testEnv(path)

	players, err := Players(ctx, env)
	if err != nil {
		t.Fatal(err)
	}
	if len(players) != 2 || players[0] != "alice" || players[1] != "bob" {
		t.Fatalf("players = %v", players)
	}

	metrics, err := RunOptional(ctx, env, VerbMetrics)
	if err != nil {
		t.Fatal(err)
	}
	if metrics != "60 FPS" {
		t.Fatalf("metrics = %q", metrics)
	}

	if _, err := RunOptional(ctx, env, VerbVersion); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

func TestCapturedFailureIncludesStderr(t *testing.T) {
	path := writeDriver(t, `
case "$1" in
  players)
    printf 'partial data\n'
    printf 'probe failed\n' >&2
    exit 1
    ;;
esac
`)
	_, err := Players(context.Background(), testEnv(path))
	if err == nil {
		t.Fatal("expected players error")
	}
	if got := err.Error(); !strings.Contains(got, "partial data") || !strings.Contains(got, "probe failed") {
		t.Fatalf("error missing captured output: %q", got)
	}
}

func TestListAndResolve(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(name string, mode os.FileMode) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), mode); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("b-driver.sh", 0755)
	mustWrite("a-driver.sh", 0755)
	mustWrite("notes.txt", 0644) // not executable
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0755); err != nil {
		t.Fatal(err)
	}

	names, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "a-driver.sh" || names[1] != "b-driver.sh" {
		t.Fatalf("names = %v", names)
	}

	if _, err := Resolve(dir, "a-driver.sh"); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(dir, "notes.txt"); err == nil {
		t.Fatal("non-executable should not resolve")
	}
	if _, err := Resolve(dir, "../etc/passwd"); err == nil {
		t.Fatal("path traversal should not resolve")
	}

	// missing dir is not an error, just empty
	names, err = List(filepath.Join(dir, "nope"))
	if err != nil || names != nil {
		t.Fatalf("missing dir: names=%v err=%v", names, err)
	}
}

func TestTemplateDriverConforms(t *testing.T) {
	// The shipped template must describe itself validly and pass deps.
	path, err := filepath.Abs("../../drivers/driver.template.sh")
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Skipf("template not found: %v", statErr)
	}
	env := testEnv(path)
	info, err := Describe(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if info.API != SupportedAPI {
		t.Fatalf("template API = %d", info.API)
	}
	missing, err := Deps(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("template deps missing: %v", missing)
	}
}
