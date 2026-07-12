package settings

import (
	"encoding/json"
	// --- BEGIN REMOTE UPDATE ---
	"errors"
	// --- END REMOTE UPDATE ---
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"servo/internal/app"
	"servo/internal/driver"
	"servo/internal/platform/database/config"
	"servo/internal/platform/http/cookies"
	"servo/internal/platform/http/middleware"
	"servo/internal/types"
	"servo/pkg/crypto"
	"strings"
	"time"

	"github.com/Data-Corruption/stdx/xhttp"
	"github.com/go-chi/chi/v5"
)

// Themes are the DaisyUI built-ins offered by the forced-theme setting
// (input.css bundles `themes: all`).
var Themes = []string{
	"light", "dark", "cupcake", "bumblebee", "emerald", "corporate",
	"synthwave", "retro", "cyberpunk", "valentine", "halloween", "garden",
	"forest", "aqua", "lofi", "pastel", "fantasy", "wireframe", "black",
	"luxury", "dracula", "cmyk", "autumn", "business", "acid", "lemonade",
	"night", "coffee", "winter", "dim", "nord", "sunset", "caramellatte",
	"abyss", "silk",
}

var validThemes = func() map[string]bool {
	m := make(map[string]bool, len(Themes))
	for _, t := range Themes {
		m[t] = true
	}
	return m
}()

func Register(a *app.App, r chi.Router) {
	r.Get("/settings", handleGetSettings(a))
	r.Post("/settings", handleUpdateSettings(a))
	r.Post("/settings/stop", handleStop(a))
	r.Post("/settings/restart", handleRestart(a))
	r.Get("/settings/restart-status", handleRestartStatus(a))
	r.Post("/settings/driver/activate", handleActivateDriver(a))
	r.Post("/settings/background", handleUploadBackground(a))
	r.Post("/settings/background/clear", handleClearBackground(a))
}

func handleGetSettings(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, err := config.View(a.DB)
		if err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}

		drivers, err := driver.List(a.DriversDir)
		if err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}

		data := map[string]any{
			"CSS":     a.UI.CSS.URLPath,
			"JS":      a.UI.JS.URLPath,
			"Favicon": template.URL(`data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'><text x='50%' y='.9em' font-size='90' text-anchor='middle'>🛰️</text></svg>`),
			"Title":   "Settings",
			"Version": a.BuildInfo().Version,
			// --- BEGIN UPDATE CHECK ---
			"UpdateAvailable": cfg.UpdateAvailable && (a.BuildInfo().Version != "vX.X.X"),
			// --- END UPDATE CHECK ---
			//  config fields
			"LogLevel":  cfg.LogLevel,
			"UIBind":    cfg.UIBind,
			"ProxyBind": cfg.ProxyBind,
			// game server schedule
			"RestartTime":       cfg.RestartTime,
			"RestartEnabled":    cfg.RestartEnabled,
			"BackupsEnabled":    cfg.BackupsEnabled,
			"BackupRetention":   cfg.BackupRetention,
			"NotifyLeadMinutes": cfg.NotifyLeadMinutes,
			// appearance
			"ForcedTheme":    cfg.ForcedTheme,
			"Themes":         Themes,
			"BackgroundBlur": cfg.BackgroundBlur,
			"ContentAlign":   cfg.ContentAlign,
			// game server connection info
			"GameAddress":  cfg.GameAddress,
			"GamePassword": cfg.GamePassword,
			// driver management (admin-only sections)
			"IsAdmin":             middleware.SessionPerms(r).Has(types.PermAdmin),
			"Drivers":             drivers,
			"ActiveDriver":        cfg.ActiveDriver,
			"LoginBackground":     cfg.LoginBackground,
			"DashboardBackground": cfg.DashboardBackground,
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
		if err := middleware.RequirePerm(r, types.PermServoSettings); err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}

		// Parse body - all fields are optional
		var body struct {
			LogLevel  *string `json:"logLevel"`
			UIBind    *string `json:"uiBind"`
			ProxyBind *string `json:"proxyBind"`
			// game server schedule
			RestartTime       *string `json:"restartTime"`
			RestartEnabled    *bool   `json:"restartEnabled"`
			BackupsEnabled    *bool   `json:"backupsEnabled"`
			BackupRetention   *int    `json:"backupRetention"`
			NotifyLeadMinutes *int    `json:"notifyLeadMinutes"`
			// appearance
			ForcedTheme    *string `json:"forcedTheme"`
			BackgroundBlur *int    `json:"backgroundBlur"`
			ContentAlign   *string `json:"contentAlign"`
			// game server connection info
			GameAddress  *string `json:"gameAddress"`
			GamePassword *string `json:"gamePassword"`
		}
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&body); err != nil {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 400, Msg: "bad request", Err: err})
			return
		}

		// validate before touching config
		if body.RestartTime != nil {
			if _, err := time.Parse("15:04", *body.RestartTime); err != nil {
				xhttp.Error(r.Context(), w, &xhttp.Err{Code: 400, Msg: `restart time must be "HH:MM" (24h)`})
				return
			}
		}
		if body.BackupRetention != nil && *body.BackupRetention < 0 {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 400, Msg: "backup retention must be >= 0"})
			return
		}
		if body.NotifyLeadMinutes != nil && (*body.NotifyLeadMinutes < 0 || *body.NotifyLeadMinutes > 720) {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 400, Msg: "notify lead must be 0-720 minutes"})
			return
		}
		if body.ForcedTheme != nil && *body.ForcedTheme != "" && !validThemes[*body.ForcedTheme] {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 400, Msg: "unknown theme"})
			return
		}
		if body.BackgroundBlur != nil && (*body.BackgroundBlur < 0 || *body.BackgroundBlur > 30) {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 400, Msg: "blur must be 0-30 px"})
			return
		}
		if body.ContentAlign != nil {
			switch *body.ContentAlign {
			case "", "left", "center", "right":
			default:
				xhttp.Error(r.Context(), w, &xhttp.Err{Code: 400, Msg: "align must be left, center, or right"})
				return
			}
		}

		scheduleChanged := false
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
			if body.RestartTime != nil {
				cfg.RestartTime = *body.RestartTime
				scheduleChanged = true
			}
			if body.RestartEnabled != nil {
				cfg.RestartEnabled = *body.RestartEnabled
				scheduleChanged = true
			}
			if body.BackupsEnabled != nil {
				cfg.BackupsEnabled = *body.BackupsEnabled
				scheduleChanged = true
			}
			if body.BackupRetention != nil {
				cfg.BackupRetention = *body.BackupRetention
			}
			if body.NotifyLeadMinutes != nil {
				cfg.NotifyLeadMinutes = *body.NotifyLeadMinutes
				scheduleChanged = true
			}
			if body.ForcedTheme != nil {
				cfg.ForcedTheme = *body.ForcedTheme
			}
			if body.BackgroundBlur != nil {
				cfg.BackgroundBlur = *body.BackgroundBlur
			}
			if body.ContentAlign != nil {
				cfg.ContentAlign = *body.ContentAlign
			}
			if body.GameAddress != nil {
				cfg.GameAddress = *body.GameAddress
			}
			if body.GamePassword != nil {
				cfg.GamePassword = *body.GamePassword
			}
			return nil
		}); err != nil {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 500, Msg: "failed to update config", Err: err})
			return
		}

		if scheduleChanged {
			a.Sched.Poke()
		}

		w.WriteHeader(http.StatusOK)
	}
}

func handleStop(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := middleware.RequirePerm(r, types.PermServoControl); err != nil {
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
		if err := middleware.RequirePerm(r, types.PermServoControl); err != nil {
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

// handleActivateDriver activates a driver (admin only): describe validates
// the API version, deps must pass, then the filename is persisted. Selection
// is from the enumerated drivers dir only — never a client-supplied path.
func handleActivateDriver(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := middleware.RequirePerm(r, types.PermAdmin); err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}

		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 400, Msg: "driver name required"})
			return
		}

		path, err := driver.Resolve(a.DriversDir, body.Name)
		if err != nil {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 404, Msg: "driver not found", Err: err})
			return
		}
		env := driver.Env{
			DriverPath: path,
			BackupDir:  a.BackupsDir,
			DataDir:    a.DriverDataDir,
			AppVersion: a.BuildInfo().Version,
		}
		info, missing, err := driver.Validate(r.Context(), env)
		if err != nil {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 422, Msg: "driver validation failed: " + err.Error(), Err: err})
			return
		}
		if len(missing) > 0 {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 422, Msg: "missing dependencies: " + strings.Join(missing, ", ")})
			return
		}

		if _, err := config.Update(a.DB, func(cfg *types.Configuration) error {
			cfg.ActiveDriver = body.Name
			return nil
		}); err != nil {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 500, Msg: "failed to update config", Err: err})
			return
		}
		a.Poller.Invalidate()
		a.Log.Infof("driver activated: %s (%s)", body.Name, info.Name)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(info); err != nil {
			xhttp.Error(r.Context(), w, err)
		}
	}
}

const maxBackgroundBytes = 8 << 20 // 8 MiB

// allowed background image types → canonical extension
var backgroundTypes = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/webp": ".webp",
	"image/gif":  ".gif",
}

// sniffImageExt returns the canonical extension for a supported image type
// based on the file's leading bytes. AVIF needs a manual check because
// http.DetectContentType doesn't know ISO-BMFF brands.
func sniffImageExt(head []byte) (string, bool) {
	if ext, ok := backgroundTypes[http.DetectContentType(head)]; ok {
		return ext, true
	}
	// The major brand is usually "avif"/"avis" but some encoders use "mif1"
	// and only list avif among the compatible brands, so scan the whole ftyp
	// box header region.
	if len(head) >= 12 && string(head[4:8]) == "ftyp" {
		brands := string(head[8:min(len(head), 32)])
		if strings.Contains(brands, "avif") || strings.Contains(brands, "avis") {
			return ".avif", true
		}
	}
	return "", false
}

// backgroundTarget validates the login/dashboard discriminator used by the
// upload, clear, and serve endpoints.
func backgroundTarget(s string) (string, bool) {
	if s == "login" || s == "dashboard" {
		return s, true
	}
	return "", false
}

// handleUploadBackground stores an uploaded background image (admin only).
// The file is size-capped, content-sniffed, and stored under a
// server-generated name — client filenames never touch the filesystem.
func handleUploadBackground(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := middleware.RequirePerm(r, types.PermAdmin); err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxBackgroundBytes)
		if err := r.ParseMultipartForm(maxBackgroundBytes); err != nil {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 400, Msg: "upload too large or malformed (8 MiB max)", Err: err})
			return
		}

		target, ok := backgroundTarget(r.FormValue("target"))
		if !ok {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 400, Msg: `target must be "login" or "dashboard"`})
			return
		}

		file, _, err := r.FormFile("image")
		if err != nil {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 400, Msg: "image file required", Err: err})
			return
		}
		defer file.Close()

		// sniff real content type from the first 512 bytes
		head := make([]byte, 512)
		n, _ := io.ReadFull(file, head)
		ext, ok := sniffImageExt(head[:n])
		if !ok {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 415, Msg: "unsupported image type (avif, png, jpeg, webp, gif)"})
			return
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}

		// write to a temp file then rename over the target atomically
		name := target + ext
		tmp, err := os.CreateTemp(a.BackgroundsDir, "upload-*")
		if err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}
		defer os.Remove(tmp.Name())
		if _, err := io.Copy(tmp, file); err != nil {
			tmp.Close()
			xhttp.Error(r.Context(), w, err)
			return
		}
		if err := tmp.Close(); err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}
		if err := os.Rename(tmp.Name(), filepath.Join(a.BackgroundsDir, name)); err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}

		if _, err := config.Update(a.DB, func(cfg *types.Configuration) error {
			old := cfg.LoginBackground
			if target == "dashboard" {
				old = cfg.DashboardBackground
				cfg.DashboardBackground = name
			} else {
				cfg.LoginBackground = name
			}
			// remove a stale file left behind by an extension change
			if old != "" && old != name {
				os.Remove(filepath.Join(a.BackgroundsDir, old))
			}
			return nil
		}); err != nil {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 500, Msg: "failed to update config", Err: err})
			return
		}

		w.WriteHeader(http.StatusOK)
	}
}

// handleClearBackground removes an uploaded background (admin only),
// reverting to the default look.
func handleClearBackground(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := middleware.RequirePerm(r, types.PermAdmin); err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}

		var body struct {
			Target string `json:"target"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 400, Msg: "bad request", Err: err})
			return
		}
		target, ok := backgroundTarget(body.Target)
		if !ok {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 400, Msg: `target must be "login" or "dashboard"`})
			return
		}

		if _, err := config.Update(a.DB, func(cfg *types.Configuration) error {
			name := &cfg.LoginBackground
			if target == "dashboard" {
				name = &cfg.DashboardBackground
			}
			if *name != "" {
				os.Remove(filepath.Join(a.BackgroundsDir, *name))
				*name = ""
			}
			return nil
		}); err != nil {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 500, Msg: "failed to update config", Err: err})
			return
		}
		w.WriteHeader(http.StatusOK)
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
