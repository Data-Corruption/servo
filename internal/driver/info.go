package driver

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Info is the metadata a driver reports via the describe verb.
type Info struct {
	API                    int    `json:"api"`
	Name                   string `json:"name"`
	Game                   string `json:"game"`
	Containerized          bool   `json:"containerized"`
	TargetServerVersion    string `json:"targetServerVersion"`    // optional
	TargetContainerVersion string `json:"targetContainerVersion"` // optional
}

// ParseInfo parses describe output: KEY=VALUE lines, blank lines skipped,
// unknown keys ignored (forward compat). DRIVER_API and NAME are required.
func ParseInfo(output []byte) (Info, error) {
	var info Info
	seenAPI := false
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "DRIVER_API":
			api, err := strconv.Atoi(value)
			if err != nil {
				return info, fmt.Errorf("describe: bad DRIVER_API %q", value)
			}
			info.API = api
			seenAPI = true
		case "NAME":
			info.Name = value
		case "GAME":
			info.Game = value
		case "CONTAINERIZED":
			b, err := strconv.ParseBool(value)
			if err != nil {
				return info, fmt.Errorf("describe: bad CONTAINERIZED %q", value)
			}
			info.Containerized = b
		case "TARGET_SERVER_VERSION":
			info.TargetServerVersion = value
		case "TARGET_CONTAINER_VERSION":
			info.TargetContainerVersion = value
		}
	}
	if !seenAPI {
		return info, fmt.Errorf("describe: missing DRIVER_API")
	}
	if info.API != SupportedAPI {
		return info, fmt.Errorf("describe: driver speaks API v%d, this servo supports v%d", info.API, SupportedAPI)
	}
	if info.Name == "" {
		return info, fmt.Errorf("describe: missing NAME")
	}
	return info, nil
}

// Describe runs the describe verb and parses its output.
func Describe(ctx context.Context, env Env) (Info, error) {
	code, stdout, stderr, err := runCaptured(ctx, env, VerbDescribe)
	if err != nil {
		return Info{}, err
	}
	if code != ExitOK {
		return Info{}, exitError(VerbDescribe, code, stdout, stderr)
	}
	return ParseInfo([]byte(stdout))
}

// Deps runs the deps verb. It returns nil when all dependencies are present,
// or the list of missing tools (one per stdout line) when the driver reports
// failure.
func Deps(ctx context.Context, env Env) ([]string, error) {
	code, stdout, stderr, err := runCaptured(ctx, env, VerbDeps)
	if err != nil {
		return nil, err
	}
	if code == ExitOK {
		return nil, nil
	}
	var missing []string
	for _, line := range strings.Split(stdout, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			missing = append(missing, line)
		}
	}
	if len(missing) == 0 {
		detail := strings.TrimSpace(stderr)
		if detail == "" {
			missing = []string{fmt.Sprintf("deps exited %d without naming missing tools", code)}
		} else {
			missing = []string{fmt.Sprintf("deps exited %d without naming missing tools: %s", code, detail)}
		}
	}
	return missing, nil
}

// Status is the game server's liveness as reported by the status verb.
type Status int

const (
	StatusUnknown Status = iota
	StatusOnline
	StatusOffline
)

func (s Status) String() string {
	switch s {
	case StatusOnline:
		return "online"
	case StatusOffline:
		return "offline"
	default:
		return "unknown"
	}
}

// GetStatus runs the status verb: exit 0 = online, 3 = offline (LSB), any
// other exit is an error.
func GetStatus(ctx context.Context, env Env) (Status, error) {
	code, stdout, stderr, err := runCaptured(ctx, env, VerbStatus)
	if err != nil {
		return StatusUnknown, err
	}
	switch code {
	case ExitOK:
		return StatusOnline, nil
	case ExitStopped:
		return StatusOffline, nil
	default:
		return StatusUnknown, exitError(VerbStatus, code, stdout, stderr)
	}
}

// ErrUnsupported is returned by optional-verb helpers when the driver
// declines the verb with ExitUnsupported.
var ErrUnsupported = fmt.Errorf("verb not supported by driver")

// RunOptional runs an optional verb and returns its trimmed output.
// ErrUnsupported means the driver declined; callers hide the feature.
func RunOptional(ctx context.Context, env Env, verb string, args ...string) (string, error) {
	code, stdout, stderr, err := runCaptured(ctx, env, verb, args...)
	if err != nil {
		return "", err
	}
	switch code {
	case ExitOK:
		return strings.TrimSpace(stdout), nil
	case ExitUnsupported:
		return "", ErrUnsupported
	default:
		return "", exitError(verb, code, stdout, stderr)
	}
}

func runCaptured(ctx context.Context, env Env, verb string, args ...string) (int, string, string, error) {
	var stdout, stderr bytes.Buffer
	code, err := run(ctx, env, &stdout, &stderr, verb, args...)
	return code, stdout.String(), stderr.String(), err
}

func exitError(verb string, code int, stdout, stderr string) error {
	var detail []string
	if stdout = strings.TrimSpace(stdout); stdout != "" {
		detail = append(detail, stdout)
	}
	if stderr = strings.TrimSpace(stderr); stderr != "" {
		detail = append(detail, stderr)
	}
	if len(detail) == 0 {
		return fmt.Errorf("%s exited %d", verb, code)
	}
	return fmt.Errorf("%s exited %d: %s", verb, code, strings.Join(detail, "\n"))
}

// Players runs the players verb: one player name per line.
func Players(ctx context.Context, env Env) ([]string, error) {
	out, err := RunOptional(ctx, env, VerbPlayers)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return []string{}, nil
	}
	var players []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			players = append(players, line)
		}
	}
	return players, nil
}

// List enumerates driver candidates: regular executable files directly in
// dir, sorted by name. Anything else (subdirs, non-executables) is ignored.
func List(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		fi, err := e.Info()
		if err != nil || fi.Mode()&0111 == 0 {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// Resolve validates that name is an entry of the drivers dir (never a
// client-supplied path) and returns its absolute path.
func Resolve(dir, name string) (string, error) {
	names, err := List(dir)
	if err != nil {
		return "", err
	}
	for _, n := range names {
		if n == name {
			return filepath.Join(dir, name), nil
		}
	}
	return "", fmt.Errorf("driver %q not found in %s", name, dir)
}

// Validate performs the activation checks: describe (API + metadata) then
// deps. A non-empty missing list means activation must be refused.
func Validate(ctx context.Context, env Env) (Info, []string, error) {
	info, err := Describe(ctx, env)
	if err != nil {
		return Info{}, nil, err
	}
	missing, err := Deps(ctx, env)
	if err != nil {
		return info, nil, err
	}
	return info, missing, nil
}
