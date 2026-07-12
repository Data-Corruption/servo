package router

import (
	"net/http"
	"net/url"
	"servo/internal/app"
	"servo/internal/platform/http/middleware"
	"servo/internal/platform/http/router/api"
	"servo/internal/platform/http/router/dashboard"
	"servo/internal/platform/http/router/settings"
	"strings"

	"github.com/Data-Corruption/stdx/xlog"
	"github.com/go-chi/chi/v5"
)

func New(a *app.App) *chi.Mux {
	r := chi.NewRouter()

	// inject logger into request context so we can use xhttp.Error() handler
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(xlog.IntoContext(r.Context(), a.Log)))
		})
	})

	// basic security hardening
	if a.BuildInfo().Version != "vX.X.X" && strings.HasPrefix(a.BaseURL, "https://") {
		r.Use(httpsRedirect)
	}
	r.Use(securityHeaders)

	// session auth. Test-mode builds skip login and grant admin to every
	// request so automated tests can hit the API directly.
	if a.TestMode {
		r.Use(middleware.TestAuth())
	} else {
		r.Use(csrfGuard)
		r.Use(middleware.Auth(a))
	}

	// serve embedded assets with cache busting
	r.Get("/assets/*", a.UI.ServeAsset)

	// register routes
	RegisterLoginRoutes(a, r)
	dashboard.Register(a, r)
	settings.Register(a, r)
	api.Register(a, r)

	return r
}

// csrfGuard rejects cross-origin state-changing requests. For unsafe methods it
// requires a same-origin Origin header, and for JSON APIs with a body it
// requires an application/json content type. Safe methods pass untouched.
func csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin == "" {
			http.Error(w, "missing Origin header", http.StatusForbidden)
			return
		}
		if u, err := url.Parse(origin); err != nil || u.Host != r.Host {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}
		// JSON endpoints must receive JSON; /login is the only form-encoded
		// POST and background image uploads are multipart. The Origin check
		// above is the actual CSRF gate — this is defense in depth.
		if r.URL.Path != "/login" && r.ContentLength != 0 {
			ct := r.Header.Get("Content-Type")
			if !strings.HasPrefix(ct, "application/json") && !strings.HasPrefix(ct, "multipart/form-data") {
				http.Error(w, "expected application/json", http.StatusUnsupportedMediaType)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'self'")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		next.ServeHTTP(w, r)
	})
}

func httpsRedirect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Forwarded-Proto") == "http" || (r.TLS == nil && r.Header.Get("X-Forwarded-Proto") == "") {
			if r.Host != "localhost" && r.Host != "127.0.0.1" && r.Host != "" {
				target := "https://" + r.Host + r.URL.RequestURI()
				http.Redirect(w, r, target, http.StatusSeeOther)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
