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

// registerLocalControlRoutes owns the local HTTP surface. Message-specific
// handlers remain in credential_proxy_messages.go; this function only wires
// them into the child server alongside the other local-control endpoints.
func (d *Daemon) registerLocalControlRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/internal/agent-api/inbox", d.agentAppInboxHandler())
	mux.HandleFunc("/internal/agent-api/inbox/ack", d.agentAppInboxAckHandler())
	mux.HandleFunc("POST /credential-proxy/messages/check", d.credentialProxyMessageCheckHandler())
	mux.HandleFunc("POST /credential-proxy/messages/read", d.credentialProxyMessageReadHandler())
	mux.HandleFunc("POST /credential-proxy/messages/send", d.credentialProxyMessageSendHandler())
	mux.HandleFunc("POST /credential-proxy/messages/search", d.credentialProxyMessageSearchHandler())
	mux.HandleFunc("POST /credential-proxy/messages/resolve", d.credentialProxyMessageResolveHandler())
	mux.HandleFunc("POST /credential-proxy/messages/react", d.credentialProxyMessageReactHandler())
	mux.HandleFunc("POST "+MessageCoverageCommitPath, d.credentialProxyMessageCoverageCommitHandler())
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
