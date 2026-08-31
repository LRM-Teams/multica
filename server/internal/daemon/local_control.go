package daemon

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
)

type agentProxyAuthContextKey struct{}

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
	mux.Handle("/inbox", d.authenticateAgentProxyRequest(d.agentAppInboxHandler()))
	mux.Handle("/inbox/ack", d.authenticateAgentProxyRequest(d.agentAppInboxAckHandler()))
	d.registerCredentialProxyMessageRoutes(mux)
	mux.Handle("/api/", d.authenticateAgentProxyRequest(d.credentialProxyAgentAPIHandler()))
}

func (d *Daemon) authenticateAgentProxyRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		credential, err := d.authenticateAgentProxyToken(r.Header.Get(AgentProxyTokenHeader))
		if err != nil {
			http.Error(w, "invalid Agent Proxy credential", http.StatusUnauthorized)
			return
		}
		r.Header.Set("X-Agent-ID", credential.Inbox.AgentID)
		r.Header.Set("X-Workspace-ID", credential.Inbox.WorkspaceID)
		r.Header.Del(AgentProxyTokenHeader)
		ctx := context.WithValue(r.Context(), agentProxyAuthContextKey{}, credential)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func agentProxyServerCredential(r *http.Request) (cachedAgentCredential, bool) {
	credential, ok := r.Context().Value(agentProxyAuthContextKey{}).(authenticatedAgentProxy)
	if !ok || strings.TrimSpace(credential.ServerCredential.Token) == "" {
		return cachedAgentCredential{}, false
	}
	return credential.ServerCredential, true
}

func agentProxyRequestAuthenticated(r *http.Request) bool {
	_, ok := r.Context().Value(agentProxyAuthContextKey{}).(authenticatedAgentProxy)
	return ok
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
