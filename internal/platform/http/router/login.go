package router

import (
	"html/template"
	"net/http"
	"sprout/internal/app"
	"sprout/internal/platform/database/config"
	"sprout/internal/platform/http/cookies"
	"sprout/internal/platform/http/middleware"

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
		"Favicon": template.URL(`data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'><text x='50%' y='.9em' font-size='90' text-anchor='middle'>🌱</text></svg>`),
		"Title":   "Login",
		"Version": a.BuildInfo().Version,
	}
	// First-run hint: with no credentials the form is a dead end, so tell the
	// user how to create one instead.
	if cfg, err := config.View(a.DB); err == nil && len(cfg.Credentials) == 0 {
		data["NoCredentials"] = true
		data["AppName"] = a.BuildInfo().Name
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
