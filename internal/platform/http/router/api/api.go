// Package api implements the JSON endpoints backing the dashboard: operation
// triggers, the combined status poll (op state + game state + admin log
// tail), and backup listing/download.
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"servo/internal/app"
	"servo/internal/ops"
	"servo/internal/platform/database/config"
	"servo/internal/platform/http/middleware"
	"servo/internal/types"

	"github.com/Data-Corruption/stdx/xhttp"
	"github.com/go-chi/chi/v5"
)

func Register(a *app.App, r chi.Router) {
	r.Post("/api/op/{op}", handleOp(a))
	r.Get("/api/status", handleStatus(a))
	r.Get("/api/backups", handleListBackups(a))
	r.Get("/api/backups/{name}", handleDownloadBackup(a))
	r.Get("/bg/{target}", handleBackground(a))
}

// handleBackground serves the uploaded login/dashboard background image from
// disk. /bg/login is auth-exempt (see middleware) since the login page
// renders pre-auth; only the config-recorded filename is ever opened.
func handleBackground(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, err := config.View(a.DB)
		if err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}
		var name string
		switch chi.URLParam(r, "target") {
		case "login":
			name = cfg.LoginBackground
		case "dashboard":
			name = cfg.DashboardBackground
		default:
			http.NotFound(w, r)
			return
		}
		if name == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, filepath.Join(a.BackgroundsDir, name))
	}
}

// permFor maps an operation to the game.* permission tier that gates it.
func permFor(op ops.Op) types.Perm {
	switch op {
	case ops.OpBackup:
		return types.PermGameBackup
	case ops.OpRestore:
		return types.PermGameRestore
	default:
		return types.PermGameControl
	}
}

func handleOp(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		op := ops.Op(chi.URLParam(r, "op"))
		if !ops.ValidOps[op] {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 400, Msg: "unknown operation"})
			return
		}
		if err := middleware.RequirePerm(r, permFor(op)); err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}

		var args []string
		if op == ops.OpRestore {
			var body struct {
				Archive string `json:"archive"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Archive == "" {
				xhttp.Error(r.Context(), w, &xhttp.Err{Code: 400, Msg: "restore requires an archive name"})
				return
			}
			args = []string{body.Archive}
		}

		if _, err := a.Ops.Start(op, args...); err != nil {
			switch {
			case errors.Is(err, ops.ErrBusy):
				xhttp.Error(r.Context(), w, &xhttp.Err{Code: 409, Msg: err.Error()})
			case errors.Is(err, ops.ErrNoDriver):
				xhttp.Error(r.Context(), w, &xhttp.Err{Code: 409, Msg: "no active driver"})
			default:
				xhttp.Error(r.Context(), w, &xhttp.Err{Code: 500, Msg: "failed to start operation", Err: err})
			}
			return
		}
		a.Log.Infof("op %s started via UI", op)
		w.WriteHeader(http.StatusAccepted)
	}
}

// handleStatus is the dashboard's poll endpoint: op snapshot + game state,
// plus the live driver output tail for admins (offset-based, only new bytes).
func handleStatus(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"op":   a.Ops.Status(),
			"game": a.Poller.Game(r.Context()),
		}

		if middleware.SessionPerms(r).Has(types.PermAdmin) {
			offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
			logBytes, newOffset := a.Ops.Tail(offset)
			resp["log"] = string(logBytes)
			resp["offset"] = newOffset
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			xhttp.Error(r.Context(), w, err)
		}
	}
}

func handleListBackups(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := middleware.RequirePerm(r, types.PermGameBackup); err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}
		backups, err := ops.ListBackups(a.BackupsDir)
		if err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}
		if backups == nil {
			backups = []ops.BackupInfo{}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(backups); err != nil {
			xhttp.Error(r.Context(), w, err)
		}
	}
}

// handleDownloadBackup streams an archive as-is (drivers compress; no
// re-packing). The name is validated against the enumerated dir listing,
// never used as a client-supplied path.
func handleDownloadBackup(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := middleware.RequirePerm(r, types.PermGameBackup); err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}
		path, err := ops.ResolveBackup(a.BackupsDir, chi.URLParam(r, "name"))
		if err != nil {
			xhttp.Error(r.Context(), w, &xhttp.Err{Code: 404, Msg: "backup not found", Err: err})
			return
		}
		f, err := os.Open(path)
		if err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="`+chi.URLParam(r, "name")+`"`)
		http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
	}
}
