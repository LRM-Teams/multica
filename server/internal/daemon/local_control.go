package daemon

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
)

// localControlAuthorized gates the loopback control surface shared by the
// binding child and its parent daemon.
func (d *Daemon) localControlAuthorized(r *http.Request) bool {
	token := strings.TrimSpace(d.cfg.LocalControlToken)
	provided := strings.TrimSpace(r.Header.Get("X-Multica-Control-Token"))
	return token != "" && provided != "" && subtle.ConstantTimeCompare([]byte(token), []byte(provided)) == 1
}

// registerLocalControlRoutes assembles the routes exposed by the binding
// child listener. Route implementations stay with their owning boundary.
func (d *Daemon) registerLocalControlRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/internal/agent-api/inbox", d.agentAppInboxHandler())
	mux.HandleFunc("/internal/agent-api/inbox/ack", d.agentAppInboxAckHandler())
	d.registerCredentialProxyMessageRoutes(mux)
	mux.HandleFunc("/api/", d.credentialProxyAgentAPIHandler())
}

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
