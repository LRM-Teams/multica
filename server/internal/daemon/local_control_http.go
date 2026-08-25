package daemon

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
)

// localControlAuthorized authenticates owner-only requests on the daemon's
// loopback control surface. Credential Proxy handlers use their own Agent
// credential flow and do not depend on this check.
func (d *Daemon) localControlAuthorized(r *http.Request) bool {
	token := strings.TrimSpace(d.cfg.LocalControlToken)
	provided := strings.TrimSpace(r.Header.Get("X-Multica-Control-Token"))
	return token != "" && provided != "" && subtle.ConstantTimeCompare([]byte(token), []byte(provided)) == 1
}

// serveLocalHTTP owns lifecycle and diagnostics for daemon-local HTTP
// listeners; route registration stays with the component that serves it.
func (d *Daemon) serveLocalHTTP(ctx context.Context, ln net.Listener, handler http.Handler, name string) {
	srv := &http.Server{Handler: handler}

	go func() {
		<-ctx.Done()
		srv.Close()
	}()

	d.logger.Info(name+" listening", "addr", ln.Addr().String())
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		d.logger.Warn(name+" error", "error", err)
	}
}
