// Package middleware implements session-based HTTP auth for the dashboard:
// in-memory sessions keyed by SHA256 of the cookie token, sliding 30-minute
// expiry, Argon2id-verified credentials from LMDB config, and rate limiting.
// Sized for a few users on a dashboard-style UI.
package middleware

import (
	"context"
	"fmt"
	"net/http"
	"sprout/internal/app"
	"sprout/internal/build"
	"sprout/internal/platform/database/config"
	"sprout/internal/platform/http/cookies"
	"sprout/internal/types"
	"sprout/pkg/crypto"
	"strings"
	"sync"
	"time"

	"github.com/Data-Corruption/stdx/xhttp"
	"golang.org/x/time/rate"
)

// SessionCookieName is derived from the app name, e.g. "sprout_session".
// Falls back to a bare "session" in raw `go build` binaries where the name
// ldflag is unset.
var SessionCookieName = func() string {
	if n := build.Info().Name; n != "" {
		return n + "_session"
	}
	return "session"
}()

const SessionDuration = 30 * time.Minute

type session struct {
	Expiry time.Time
	Perms  types.Perm
}

// sessionAuth is the per-request authorization payload stored in the context.
type sessionAuth struct {
	Perms types.Perm
}

type authKeyType struct{}

var authKey authKeyType

var (
	sessions = make(map[string]session)
	mu       sync.RWMutex

	globalLimiter = rate.NewLimiter(rate.Limit(5), 10)
	authLimiter   = rate.NewLimiter(rate.Limit(0.25), 3)
)

// NewSession validates the password against stored credentials and mints a
// session token. On failure it returns a user-facing error message alongside
// the error.
func NewSession(a *app.App, password string) (string, string, error) {
	cfg, err := config.View(a.DB)
	if err != nil {
		return "", "internal error", err
	}

	var matched *types.Credential
	for i := range cfg.Credentials {
		if crypto.ComparePasswords(password, cfg.Credentials[i].PassHash, cfg.Credentials[i].PassSalt) {
			matched = &cfg.Credentials[i]
			break
		}
	}
	if matched == nil {
		if err := authLimiter.Wait(context.Background()); err != nil {
			return "", "too many requests", err
		}
		return "", "invalid password", fmt.Errorf("invalid password")
	}

	token, err := crypto.GenRandomString(32)
	if err != nil {
		return "", "internal error", err
	}
	hashedToken := crypto.Hash(token)

	mu.Lock()
	defer mu.Unlock()

	for k, s := range sessions {
		if s.Expiry.Before(time.Now()) {
			delete(sessions, k)
		}
		if k == hashedToken {
			return "", "session already exists", fmt.Errorf("session already exists")
		}
	}

	sessions[hashedToken] = session{
		Expiry: time.Now().Add(SessionDuration),
		Perms:  matched.Perms,
	}

	return token, "", nil
}

// SessionPerms returns the permissions associated with the current request's session.
func SessionPerms(r *http.Request) types.Perm {
	if s, ok := r.Context().Value(authKey).(sessionAuth); ok {
		return s.Perms
	}
	return 0
}

// RequirePerm returns an HTTP 403 error if the request session lacks the given permission.
func RequirePerm(r *http.Request, p types.Perm) error {
	if !SessionPerms(r).Has(p) {
		return &xhttp.Err{Code: http.StatusForbidden, Msg: "insufficient permissions"}
	}
	return nil
}

// TestAuth returns middleware that grants PermAdmin to every request,
// bypassing session cookies and rate limiting. Only used in test-mode builds
// (build.sh --test).
func TestAuth() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), authKey, sessionAuth{Perms: types.PermAdmin})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Auth returns the session-auth middleware. At construction it restores a
// session persisted across restart (see the settings restart handler), so the
// browser that triggered a restart stays logged in.
func Auth(a *app.App) func(http.Handler) http.Handler {
	if cfg, err := config.View(a.DB); err == nil {
		if cfg.SessionHash != "" && cfg.SessionExpiry.After(time.Now()) {
			mu.Lock()
			sessions[cfg.SessionHash] = session{
				Expiry: cfg.SessionExpiry,
				Perms:  cfg.SessionPerms,
			}
			mu.Unlock()
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := globalLimiter.Wait(context.Background()); err != nil {
				http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
				return
			}

			if r.URL.Path == "/login" || strings.HasPrefix(r.URL.Path, "/assets/") {
				next.ServeHTTP(w, r)
				return
			}

			token := cookies.Read(r, SessionCookieName)
			if token == "" {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			hashedToken := crypto.Hash(token)

			mu.RLock()
			s, ok := sessions[hashedToken]
			mu.RUnlock()
			if !ok || s.Expiry.Before(time.Now()) {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			newExpiry := time.Now().Add(SessionDuration)
			mu.Lock()
			sessions[hashedToken] = session{Expiry: newExpiry, Perms: s.Perms}
			mu.Unlock()

			ctx := context.WithValue(r.Context(), authKey, sessionAuth{Perms: s.Perms})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
