// Package dashboard renders the main page: game server status, control
// buttons, the activity panel, and the backups list. All live data arrives
// via the /api/status poll; this handler only supplies the static shell and
// permission flags.
package dashboard

import (
	"html/template"
	"net/http"

	"servo/internal/app"
	"servo/internal/platform/database/config"
	"servo/internal/platform/http/middleware"
	"servo/internal/types"

	"github.com/Data-Corruption/stdx/xhttp"
	"github.com/go-chi/chi/v5"
)

func Register(a *app.App, r chi.Router) {
	r.Get("/", handleGetDashboard(a))
}

func handleGetDashboard(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, err := config.View(a.DB)
		if err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}

		perms := middleware.SessionPerms(r)
		data := map[string]any{
			"CSS":     a.UI.CSS.URLPath,
			"JS":      a.UI.JS.URLPath,
			"Favicon": template.URL(`data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'><text x='50%' y='.9em' font-size='90' text-anchor='middle'>🛰️</text></svg>`),
			"Title":   "Servo",

			"CanControl": perms.Has(types.PermGameControl),
			"CanBackup":  perms.Has(types.PermGameBackup),
			"CanRestore": perms.Has(types.PermGameRestore),
			"IsAdmin":    perms.Has(types.PermAdmin),

			"HasDriver":      cfg.ActiveDriver != "",
			"HasBackground":  cfg.DashboardBackground != "",
			"ForcedTheme":    cfg.ForcedTheme,
			"BackgroundBlur": cfg.BackgroundBlur,
			"ContentAlign":   cfg.ContentAlign,
			"GameAddress":    cfg.GameAddress,
			"GamePassword":   cfg.GamePassword,
		}
		if err := a.UI.Execute(w, "dashboard.html", data); err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}
	}
}
