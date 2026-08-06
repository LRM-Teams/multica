package daemon

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

// HealthResponse is returned by the daemon's local health endpoint.
type HealthResponse struct {
	Status string `json:"status"`
	PID    int    `json:"pid"`
	// OS is the daemon's runtime.GOOS. The desktop app compares it against its
	// own host OS to detect a daemon it cannot manage — e.g. a Windows desktop
	// reaching a Linux daemon inside WSL2 over localhost forwarding. The
	// lifecycle CLI (`daemon start/stop`) acts on the host process namespace,
	// so a foreign-OS daemon can't be started/stopped by the app even though
	// /health is reachable. See #3916.
	OS              string            `json:"os"`
	Uptime          string            `json:"uptime"`
	DaemonID        string            `json:"daemon_id"`
	DeviceName      string            `json:"device_name"`
	ServerURL       string            `json:"server_url"`
	CLIVersion      string            `json:"cli_version"`
	ActiveTaskCount int64             `json:"active_task_count"`
	Agents          []string          `json:"agents"`
	Workspaces      []healthWorkspace `json:"workspaces"`
}

type healthWorkspace struct {
	ID       string   `json:"id"`
	Runtimes []string `json:"runtimes"`
}

// listenHealth binds the health port. Returns the listener or an error if
// another daemon is already running (port taken).
func (d *Daemon) listenHealth() (net.Listener, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", d.cfg.HealthPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("another daemon is already running on %s: %w", addr, err)
	}
	return ln, nil
}

// healthHandler returns the /health HTTP handler. Extracted from serveHealth
// so tests can exercise it without spinning up a listener.
func (d *Daemon) healthHandler(startedAt time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		var wsList []healthWorkspace
		for id, ws := range d.workspaces {
			wsList = append(wsList, healthWorkspace{
				ID:       id,
				Runtimes: ws.runtimeIDs,
			})
		}
		d.mu.Unlock()

		agents := make([]string, 0, len(d.cfg.Agents))
		for name := range d.cfg.Agents {
			agents = append(agents, name)
		}

		// "starting" until preflight (PAT renew + initial workspace sync +
		// runtime registration) completes; "running" once the daemon can
		// actually claim tasks. The health port is bound before preflight for
		// liveness/diagnostics, so callers must not treat a reachable endpoint
		// as ready — they gate on this status. Consumers that only know
		// "running" (older CLI/desktop) safely treat "starting" as not-ready.
		status := "starting"
		if d.ready.Load() {
			status = "running"
		}

		resp := HealthResponse{
			Status:          status,
			PID:             os.Getpid(),
			OS:              runtime.GOOS,
			Uptime:          time.Since(startedAt).Truncate(time.Second).String(),
			DaemonID:        d.cfg.DaemonID,
			DeviceName:      d.cfg.DeviceName,
			ServerURL:       d.cfg.ServerBaseURL,
			CLIVersion:      d.cfg.CLIVersion,
			ActiveTaskCount: d.activeTasks.Load(),
			Agents:          agents,
			Workspaces:      wsList,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

// shutdownHandler triggers a graceful daemon shutdown by cancelling the
// top-level context. Used by `multica daemon stop` so we don't depend on
// OS-signal delivery, which is unreliable on Windows once the daemon is
// spawned with DETACHED_PROCESS (no shared console with the stop caller).
// The listener is bound to 127.0.0.1 only, so only local processes can hit
// this endpoint.
func (d *Daemon) shutdownHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "shutting down"})
		if d.cancelFunc != nil {
			// Cancel asynchronously so the response flushes first; otherwise
			// srv.Close() races with the writer.
			go d.cancelFunc()
		}
	}
}

type localMachineUpgradeRequest struct {
	RequestID     string `json:"request_id"`
	TargetVersion string `json:"target_version"`
}

// localMachineUpgradeHandler is deliberately separate from /health. The
// bearer-like control secret lives in a 0600 profile file owned by the daemon
// user, which protects the loopback mutation path from browsers and unrelated
// local users. The daemon still creates the normal server-side canonical
// operation; this endpoint is only the single-writer local routing boundary.
func (d *Daemon) localMachineUpgradeHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		token := strings.TrimSpace(d.cfg.LocalControlToken)
		provided := strings.TrimSpace(r.Header.Get("X-Multica-Control-Token"))
		if token == "" || provided == "" || subtle.ConstantTimeCompare([]byte(token), []byte(provided)) != 1 {
			http.Error(w, "local control authentication failed", http.StatusUnauthorized)
			return
		}
		if d.client == nil || strings.TrimSpace(d.cfg.DaemonID) == "" || strings.TrimSpace(d.cfg.WorkspaceID) == "" {
			http.Error(w, "machine control unavailable", http.StatusServiceUnavailable)
			return
		}
		var request localMachineUpgradeRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		request.RequestID = strings.TrimSpace(request.RequestID)
		request.TargetVersion = strings.TrimSpace(request.TargetVersion)
		if request.RequestID == "" || request.TargetVersion == "" {
			http.Error(w, "request_id and target_version are required", http.StatusBadRequest)
			return
		}
		operation, err := d.client.CreateMachineUpgrade(r.Context(), d.cfg.WorkspaceID, d.cfg.DaemonID, request.RequestID, request.TargetVersion)
		if err != nil {
			var requestErr *requestError
			if errors.As(err, &requestErr) {
				http.Error(w, requestErr.Body, requestErr.StatusCode)
				return
			}
			http.Error(w, "create machine upgrade: "+err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(operation)
	}
}

type credentialProxyMessageCheckRequest struct {
	AgentID string `json:"agent_id"`
}

type credentialProxyMessageReadRequest struct {
	AgentID string `json:"agent_id"`

	WorkspaceID string `json:"workspace_id"`
	Target      string `json:"target"`
	Before      string `json:"before,omitempty"`
	After       string `json:"after,omitempty"`
	Around      string `json:"around,omitempty"`
	Limit       int    `json:"limit"`
}

func (d *Daemon) credentialProxyMessageCheckHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var request credentialProxyMessageCheckRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		request.AgentID = strings.TrimSpace(request.AgentID)
		if request.AgentID == "" {
			http.Error(w, "agent_id is required", http.StatusBadRequest)
			return
		}
		result, err := d.CredentialProxy().CheckMessages(request.AgentID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(result); err != nil && d.logger != nil {
			d.logger.Warn("write Credential Proxy message check response", "error", err)
		}
	}
}

func (d *Daemon) credentialProxyMessageReadHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var request credentialProxyMessageReadRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		request.AgentID = strings.TrimSpace(request.AgentID)
		request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
		request.Target = strings.TrimSpace(request.Target)
		if request.AgentID == "" || request.WorkspaceID == "" || request.Target == "" {
			http.Error(w, "agent_id, workspace_id, and target are required", http.StatusBadRequest)
			return
		}
		credential, ok := readCachedAgentCredentialForChat(d.cfg, request.WorkspaceID, request.AgentID, time.Now())
		if !ok {
			http.Error(w, "Agent credential is unavailable", http.StatusConflict)
			return
		}

		client := cli.NewAPIClient(d.cfg.ServerBaseURL, request.WorkspaceID, credential.Token)
		client.AgentID = request.AgentID
		upstreamRequest := map[string]any{
			"target": request.Target,
			"limit":  request.Limit,
		}
		if request.Before != "" {
			upstreamRequest["before"] = request.Before
		}
		if request.After != "" {
			upstreamRequest["after"] = request.After
		}
		if request.Around != "" {
			upstreamRequest["around"] = request.Around
		}
		ctx, cancel := cli.APIContext(r.Context())
		defer cancel()
		var response map[string]any
		if err := client.PostJSON(ctx, "/api/agent/messages/read", upstreamRequest, &response); err != nil {
			http.Error(w, "read messages through Credential Proxy: "+err.Error(), http.StatusBadGateway)
			return
		}
		contextTarget, _ := response["context_target"].(string)
		contextTarget = strings.TrimSpace(contextTarget)
		seenUpToSeq, _ := response["seenUpToSeq"].(float64)
		if contextTarget == "" || seenUpToSeq < 0 || seenUpToSeq != float64(int64(seenUpToSeq)) {
			http.Error(w, "invalid read response from server", http.StatusBadGateway)
			return
		}
		if seenUpToSeq > 0 {
			if err := d.CredentialProxy().RecordMessageRead(request.AgentID, contextTarget, int64(seenUpToSeq)); err != nil {
				http.Error(w, "persist message read boundary: "+err.Error(), http.StatusConflict)
				return
			}
		}
		// Context target and sequence are proxy-only facts. A Message command
		// never exposes a cursor-like read state to the Agent process.
		delete(response, "context_target")
		delete(response, "seenUpToSeq")
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil && d.logger != nil {
			d.logger.Warn("write Credential Proxy message read response", "error", err)
		}
	}
}

// serveHealth runs the health HTTP server on the given listener.
// Blocks until ctx is cancelled.
func (d *Daemon) serveHealth(ctx context.Context, ln net.Listener, startedAt time.Time) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", d.healthHandler(startedAt))
	mux.HandleFunc("/shutdown", d.shutdownHandler())
	mux.HandleFunc("/machine-upgrades", d.localMachineUpgradeHandler())
	mux.HandleFunc("/credential-proxy/messages/check", d.credentialProxyMessageCheckHandler())
	mux.HandleFunc("/credential-proxy/messages/read", d.credentialProxyMessageReadHandler())
	mux.HandleFunc("/credential-proxy/messages/send", d.credentialProxyMessageSendHandler())

	srv := &http.Server{Handler: mux}

	go func() {
		<-ctx.Done()
		srv.Close()
	}()

	d.logger.Info("health server listening", "addr", ln.Addr().String())
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		d.logger.Warn("health server error", "error", err)
	}
}
