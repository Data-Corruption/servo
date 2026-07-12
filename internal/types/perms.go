package types

import (
	"fmt"
	"strings"
)

// Perm is a permission bitmask attached to credentials and sessions. Bits are
// split into two namespaces: `game.*` protects the game server (driver
// operations), `servo.*` protects the dashboard/daemon itself. Bits group by
// risk tier rather than one bit per verb (the pattern scales to 64 bits).
type Perm uint64

const (
	// PermGameControl covers routine game server operations: start, stop,
	// restart, update, notify.
	PermGameControl Perm = 1 << iota // game.control
	// PermGameBackup covers backup-now and downloading archives.
	PermGameBackup // game.backup
	// PermGameRestore covers restoring a backup — the one destructive action,
	// so it's its own bit, granted per credential as trust allows.
	PermGameRestore // game.restore
	// PermServoSettings covers the settings page: restart schedule, notify
	// lead time, backup toggle/retention, binds, log level.
	PermServoSettings // servo.settings
	// PermServoControl covers the daemon itself: stop, restart, self-update.
	PermServoControl // servo.control

	PermAdmin Perm = ^Perm(0)
)

var permNames = [...]struct {
	name string
	perm Perm
}{
	{"game.control", PermGameControl},
	{"game.backup", PermGameBackup},
	{"game.restore", PermGameRestore},
	{"servo.settings", PermServoSettings},
	{"servo.control", PermServoControl},
}

var PermNames map[string]Perm

func init() {
	PermNames = make(map[string]Perm, len(permNames)+1)
	for _, e := range permNames {
		PermNames[e.name] = e.perm
	}
	PermNames["admin"] = PermAdmin
}

func (p Perm) Has(required Perm) bool {
	return p&required == required
}

func (p Perm) String() string {
	if p == PermAdmin {
		return "admin"
	}
	var parts []string
	for _, e := range permNames {
		if p&e.perm != 0 {
			parts = append(parts, e.name)
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " ")
}

// ParsePerms parses a space-separated permission spec. Tokens are OR'd in;
// a leading '!' clears the bit instead. Example: "admin !game.restore"
// yields all permissions except PermGameRestore.
func ParsePerms(args []string) (Perm, error) {
	var p Perm
	for _, tok := range args {
		neg := false
		name := tok
		if strings.HasPrefix(tok, "!") {
			neg = true
			name = tok[1:]
		}
		bit, ok := PermNames[name]
		if !ok {
			return 0, fmt.Errorf("unknown permission %q", name)
		}
		if neg {
			p &^= bit
		} else {
			p |= bit
		}
	}
	return p, nil
}
