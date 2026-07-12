package settings

import (
	"encoding/json"
	// --- BEGIN REMOTE UPDATE ---
	"errors"
	// --- END REMOTE UPDATE ---
	"fmt"
	"html/template"
	"net/http"
	"os/exec"
	"sprout/internal/app"
	"sprout/internal/platform/database/config"
	"sprout/internal/platform/http/cookies"
	"sprout/internal/platform/http/middleware"
	"sprout/internal/types"
	"sprout/pkg/crypto"
	"time"

	"github.com/Data-Corruption/stdx/xhttp"
	"github.com/go-chi/chi/v5"
)

func Register(a *app.App, r chi.Router) {
	r.Get("/", handleGetSettings(a))
	r.Post("/settings", handleUpdateSettings(a))
	r.Post("/settings/stop", handleStop(a))
	r.Post("/settings/restart", handleRestart(a))
	r.Get("/settings/restart-status", handleRestartStatus(a))
}

func handleGetSettings(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, err := config.View(a.DB)
		if err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}

		data := map[string]any{
			"CSS":     a.UI.CSS.URLPath,
			"JS":      a.UI.JS.URLPath,
			"Favicon": template.URL(`data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'><text x='50%' y='.9em' font-size='90' text-anchor='middle'>🌱</text></svg>`),
			"Title":   "Settings",
			"Version": a.BuildInfo().Version,
			// --- BEGIN UPDATE CHECK ---
			"UpdateAvailable": cfg.UpdateAvailable && (a.BuildInfo().Version != "vX.X.X"),
			// --- END UPDATE CHECK ---
			//  config fields
			"LogLevel":  cfg.LogLevel,
			"UIBind":    cfg.UIBind,
			"ProxyBind": cfg.ProxyBind,
		}
		if err := a.UI.Execute(w, "settings.html", data); err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}
	}
}

func handleUpdateSettings(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := middleware.RequirePerm(r, types.PermSettings); err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}

		// Parse body - all fields are optional
		var body struct {
			LogLevel  *string `json:"logLevel"`
			UIBind    *string `json:"uiBind"`
			ProxyBind *string `json:"proxyBind"`
		}
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&body); err != nil {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 400, Msg: "bad request", Err: err})
			return
		}

		// Update only the fields that were provided
		if _, err := config.Update(a.DB, func(cfg *types.Configuration) error {
			if body.LogLevel != nil {
				cfg.LogLevel = *body.LogLevel
			}
			if body.UIBind != nil {
				cfg.UIBind = *body.UIBind
			}
			if body.ProxyBind != nil {
				cfg.ProxyBind = *body.ProxyBind
			}
			return nil
		}); err != nil {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 500, Msg: "failed to update config", Err: err})
			return
		}

		w.WriteHeader(http.StatusOK)
	}
}

func handleStop(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := middleware.RequirePerm(r, types.PermServerControl); err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)

		if a.BuildInfo().ServiceEnabled && a.BuildInfo().Version != "vX.X.X" {
			// Use systemd-run to create a transient unit that survives our process dying.
			// This ensures the stop command completes and logs reliably.
			go func() {
				serviceName := a.BuildInfo().Name + ".service"
				unitName := fmt.Sprintf("%s-stop-%s", a.BuildInfo().Name, time.Now().Format("20060102-150405"))
				syslogIdent := fmt.Sprintf("SyslogIdentifier=%s-stop", a.BuildInfo().Name)

				cmd := exec.CommandContext(
					a.Context,
					"systemd-run",
					"--user",
					"--unit="+unitName,
					"--quiet",
					"--no-block",
					"-p", "StandardOutput=journal",
					"-p", "StandardError=journal",
					"-p", syslogIdent,
					"systemctl", "--user", "stop", serviceName,
				)
				if err := cmd.Run(); err != nil {
					a.Log.Errorf("failed to start stop unit: %v", err)
				}
			}()
		} else {
			go a.Server.Shutdown()
		}
	}
}

// prepRestart resets the restart detector and persists the caller's session
// so the browser that triggered the restart stays logged in afterwards. The
// auth middleware restores it on the next start.
func prepRestart(a *app.App, r *http.Request) error {
	token := cookies.Read(r, middleware.SessionCookieName)
	var hashedToken string
	var expiry time.Time
	if token != "" {
		hashedToken = crypto.Hash(token)
		expiry = time.Now().Add(middleware.SessionDuration)
	}
	_, err := config.Update(a.DB, func(cfg *types.Configuration) error {
		cfg.StartCounter = 0
		if hashedToken != "" {
			cfg.SessionHash = hashedToken
			cfg.SessionExpiry = expiry
			cfg.SessionPerms = middleware.SessionPerms(r)
		}
		return nil
	})
	return err
}

func handleRestart(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := middleware.RequirePerm(r, types.PermServerControl); err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}

		// parse body
		var body struct {
			Update bool `json:"update"`
		}
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&body); err != nil {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 400, Msg: "bad request", Err: err})
			return
		}

		a.Log.Debug("Restart requested.")

		// --- BEGIN REMOTE UPDATE ---
		// skip update if dev build
		doUpdate := body.Update && a.BuildInfo().Version != "vX.X.X"
		a.Log.Debugf("Restart update requested: %t, doUpdate: %t", body.Update, doUpdate)
		// --- END REMOTE UPDATE ---

		// reset StartCounter (post migrate restart will increment) and carry
		// the session across the restart
		if err := prepRestart(a, r); err != nil {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 500, Msg: "failed to update config", Err: err})
			return
		}

		w.WriteHeader(http.StatusAccepted)

		// do the restart
		// --- BEGIN REMOTE UPDATE ---
		if doUpdate {
			// detach update will close us externally
			if err := a.DetachUpdate(); err != nil {
				if errors.Is(err, app.ErrUpdatesDisabled) {
					a.Log.Warnf("update requested but disabled on this install: %v", err)
					go a.Server.Shutdown() // plain restart instead
				} else {
					a.Log.Errorf("failed to detach update: %v", err)
				}
			}
			return
		}
		// --- END REMOTE UPDATE ---
		// otherwise we need to close ourselves
		go a.Server.Shutdown()
	}
}

func handleRestartStatus(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, err := config.View(a.DB)
		if err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}

		restarted := cfg.StartCounter > 0
		resp := map[string]bool{"restarted": restarted}
		a.Log.Debugf("Restart status check: StartCounter=%d, Restarted=%t", cfg.StartCounter, restarted)

		// --- BEGIN REMOTE UPDATE ---
		updated := cfg.PreUpdateVersion != "" && cfg.PreUpdateVersion != a.BuildInfo().Version
		resp["updated"] = updated
		a.Log.Debugf("Restart status check: PreUpdateVersion=%q, CurrentVersion=%q, Updated=%t",
			cfg.PreUpdateVersion, a.BuildInfo().Version, updated)
		// --- END REMOTE UPDATE ---

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			xhttp.Error(r.Context(), w, err)
		}
	}
}
