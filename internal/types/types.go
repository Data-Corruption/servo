package types

import (
	"fmt"
	"servo/internal/build"
	"time"
)

type Configuration struct {
	LogLevel string `json:"logLevel"`
	// UIBind is the self-signed HTTPS dashboard listener (e.g. ":8829").
	UIBind string `json:"uiBind"`
	// ProxyBind is the optional loopback-only plain HTTP listener for local
	// reverse proxies such as Caddy (default "127.0.0.1:8830"). Empty = disabled.
	ProxyBind string `json:"proxyBind"`

	// --- BEGIN UPDATE CHECK ---
	UpdateNotifications bool      `json:"updateNotifications"`
	LastUpdateCheck     time.Time `json:"lastUpdateCheck"`
	UpdateAvailable     bool      `json:"updateAvailable"`
	// --- END UPDATE CHECK ---

	// --- BEGIN REMOTE UPDATE ---
	// app version when update process was accepted. This is lazily used to determine if the update was successful after restart.
	PreUpdateVersion string `json:"preUpdateVersion"`
	// --- END REMOTE UPDATE ---

	// game server / driver
	// ActiveDriver is the filename of the active driver in the drivers dir.
	// Empty = none activated.
	ActiveDriver string `json:"activeDriver"`
	// RestartTime is the daily restart window as "HH:MM" (host-local time).
	RestartTime    string `json:"restartTime"`
	RestartEnabled bool   `json:"restartEnabled"`
	// BackupsEnabled makes the restart window take a backup (and enables
	// backup-only runs while the server is offline).
	BackupsEnabled bool `json:"backupsEnabled"`
	// BackupRetention is how many archives to keep, newest first.
	BackupRetention int `json:"backupRetention"`
	// NotifyLeadMinutes is how many minutes before the restart window players
	// are warned via the notify verb. 0 = no warning.
	NotifyLeadMinutes int `json:"notifyLeadMinutes"`
	// Uploaded background image filenames (within the backgrounds dir).
	// Empty = default background.
	LoginBackground     string `json:"loginBackground"`
	DashboardBackground string `json:"dashboardBackground"`
	// appearance
	// ForcedTheme pins a DaisyUI theme for everyone and hides the dark mode
	// toggle. Empty = per-user light/dark toggle.
	ForcedTheme string `json:"forcedTheme"`
	// BackgroundBlur is the background image blur radius in px (0 = sharp).
	BackgroundBlur int `json:"backgroundBlur"`
	// ContentAlign floats the content column left/center/right on wide
	// displays. Empty = center.
	ContentAlign string `json:"contentAlign"`
	// game server connection info surfaced on the dashboard (copy buttons)
	GameAddress  string `json:"gameAddress"`
	GamePassword string `json:"gamePassword"`

	// auth
	Credentials []Credential `json:"credentials"`
	// keep the browser session alive through restart
	SessionHash   string    `json:"sessionHash"`
	SessionExpiry time.Time `json:"sessionExpiry"`
	SessionPerms  Perm      `json:"sessionPerms"`
	// incremented on each service start (usually server listen or similar), used for detecting restarts
	StartCounter int `json:"startCounter"`
}

// Credential is a UI login credential. Passwords are stored Argon2id-hashed.
type Credential struct {
	Label    string `json:"label"`
	PassHash string `json:"passHash"`
	PassSalt string `json:"passSalt"`
	Perms    Perm   `json:"perms"`
}

func DefaultConfig() Configuration {
	return Configuration{
		LogLevel:  build.Info().DefaultLogLevel,
		UIBind:    fmt.Sprintf(":%d", build.Info().ServiceDefaultPort),
		ProxyBind: "127.0.0.1:8830",
		// --- BEGIN UPDATE CHECK ---
		UpdateNotifications: true,
		LastUpdateCheck:     time.Time{},
		// --- END UPDATE CHECK ---
		RestartTime:       "04:00",
		BackupRetention:   5,
		NotifyLeadMinutes: 5,
	}
}
