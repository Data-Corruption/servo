package server

import (
	"fmt"
	"net"
	"net/http"
	"sprout/internal/app"
	"sprout/internal/platform/database/config"
	"sprout/internal/types"
	"sprout/pkg/sdnotify"
	"strings"

	"github.com/Data-Corruption/stdx/xhttp"
)

// New builds the dashboard's two HTTP listeners, both serving the same handler:
//   - a self-signed HTTPS listener on cfg.UIBind (the dashboard), and
//   - an optional loopback-only plain HTTP listener on cfg.ProxyBind, for a
//     local reverse proxy (e.g. Caddy) to terminate TLS in front of the app.
//
// Only the HTTPS server runs lifecycle hooks (systemd readiness, start
// counter). The caller runs the proxy server's Listen loop and shuts both down.
func New(a *app.App, cfg *types.Configuration, handler http.Handler) error {
	var err error
	a.Server, err = xhttp.NewServer(&xhttp.ServerConfig{
		Addr:        cfg.UIBind,
		UseTLS:      true,
		TLSCertPath: a.Secrets.CertPath(),
		TLSKeyPath:  a.Secrets.KeyPath(),
		Handler:     handler,
		AfterListen: func() {
			fmt.Println("Listening on", a.BaseURL) // for user
			status := fmt.Sprintf("Listening on %s", a.Server.Addr())
			if err := sdnotify.Ready(status); err != nil {
				a.Log.Warnf("sd_notify READY failed: %v", err)
			}
			if _, err := config.Update(a.DB, func(c *types.Configuration) error {
				c.StartCounter++
				return nil
			}); err != nil {
				a.Log.Errorf("failed to increment start counter: %v", err)
			}
		},
		OnShutdown: func() {
			if err := sdnotify.Stopping("Shutting down"); err != nil {
				a.Log.Debugf("sd_notify STOPPING failed: %v", err)
			}
			fmt.Println("shutting down, cleaning up resources ...")
		},
	})
	if err != nil {
		return err
	}

	proxyBind := strings.TrimSpace(cfg.ProxyBind)
	if proxyBind == "" {
		return nil
	}
	if err := validateLoopbackBind(proxyBind); err != nil {
		return err
	}
	a.ProxyServer, err = xhttp.NewServer(&xhttp.ServerConfig{
		Addr:    proxyBind,
		UseTLS:  false,
		Handler: handler,
	})
	return err
}

// validateLoopbackBind rejects a proxy bind that is not loopback-only. The
// plain HTTP listener must never be reachable off-host.
func validateLoopbackBind(bind string) error {
	host, _, err := net.SplitHostPort(bind)
	if err != nil {
		return &xhttp.Err{Code: http.StatusBadRequest, Msg: fmt.Sprintf("invalid proxy bind %q (want host:port)", bind), Err: err}
	}
	host = strings.TrimSpace(host)
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return &xhttp.Err{Code: http.StatusBadRequest, Msg: fmt.Sprintf("proxy bind host %q must be loopback (127.0.0.1, ::1, or localhost)", host)}
	}
	return nil
}
