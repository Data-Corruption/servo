package router

import (
	"html/template"
	"net/http"
	"servo/internal/app"
	"servo/internal/platform/database/config"
	"servo/internal/platform/http/cookies"
	"servo/internal/platform/http/middleware"

	"github.com/Data-Corruption/stdx/xhttp"
	"github.com/go-chi/chi/v5"
)

func RegisterLoginRoutes(a *app.App, r chi.Router) {
	r.Get("/login", handleGetLogin(a))
	r.Post("/login", handlePostLogin(a))
}

func loginData(a *app.App) map[string]any {
	data := map[string]any{
		"CSS":     a.UI.CSS.URLPath,
		"JS":      a.UI.JS.URLPath,
		"Favicon": template.URL(`data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'><text x='50%' y='.9em' font-size='90' text-anchor='middle'>🛰️</text></svg>`),
		"Title":   "Login",
		// defaults in case the config read below fails
		"ForcedTheme":    "",
		"BackgroundBlur": 0,
		"ContentAlign":   "",
	}
	if cfg, err := config.View(a.DB); err == nil {
		// First-run hint: with no credentials the form is a dead end, so tell
		// the user how to create one instead.
		if len(cfg.Credentials) == 0 {
			data["NoCredentials"] = true
			data["AppName"] = a.BuildInfo().Name
		}
		data["HasBackground"] = cfg.LoginBackground != ""
		data["ForcedTheme"] = cfg.ForcedTheme
		data["BackgroundBlur"] = cfg.BackgroundBlur
		data["ContentAlign"] = cfg.ContentAlign
	}
	return data
}

func handleGetLogin(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := a.UI.Execute(w, "login.html", loginData(a)); err != nil {
			xhttp.Error(r.Context(), w, err)
			return
		}
	}
}

func handlePostLogin(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		password := r.FormValue("password")

		token, errMsg, err := middleware.NewSession(a, password)
		if err != nil {
			data := loginData(a)
			data["Error"] = errMsg
			w.WriteHeader(http.StatusUnauthorized)
			if err := a.UI.Execute(w, "login.html", data); err != nil {
				xhttp.Error(r.Context(), w, err)
			}
			return
		}

		secureCookie := a.BuildInfo().Version != "vX.X.X"
		cookies.Set(w, middleware.SessionCookieName, token, "/", middleware.SessionDuration, secureCookie)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}
