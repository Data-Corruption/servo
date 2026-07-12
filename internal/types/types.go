package types

import (
	"fmt"
	"sprout/internal/build"
	"time"
)

type Configuration struct {
	LogLevel string `json:"logLevel"`
	// UIBind is the self-signed HTTPS dashboard listener (e.g. ":8484").
	UIBind string `json:"uiBind"`
	// ProxyBind is the optional loopback-only plain HTTP listener for local
	// reverse proxies such as Caddy (e.g. "127.0.0.1:8485"). Empty = disabled.
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
		LogLevel: build.Info().DefaultLogLevel,
		UIBind:   fmt.Sprintf(":%d", build.Info().ServiceDefaultPort),
		// --- BEGIN UPDATE CHECK ---
		UpdateNotifications: true,
		LastUpdateCheck:     time.Time{},
		// --- END UPDATE CHECK ---
	}
}
