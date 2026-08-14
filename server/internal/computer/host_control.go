package computer

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/diagnosticlog"
)

const (
	bindingChildAttestPath              = "/binding-child/attest"
	bindingChildCapacityPath            = "/binding-child/capacity"
	bindingChildDiagnosticPath          = "/binding-child/diagnostics"
	bindingChildLifecycleDiagnosticPath = "/binding-child/lifecycle-diagnostics"
	bindingChildMachineActionsPath      = "/binding-child/machine-actions"
	bindingChildRuntimeSetPath          = "/binding-child/runtime-set"
)

// BindingChildIdentity fences every child-to-Host request by the immutable
// Binding, runner generation, and OS process identity the Computer supervises.
type BindingChildIdentity struct {
	WorkspaceID      string `json:"workspace_id"`
	RunnerGeneration int64  `json:"runner_generation"`
	PID              int    `json:"pid"`
}

func (identity BindingChildIdentity) Validate() error {
	if strings.TrimSpace(identity.WorkspaceID) == "" || identity.RunnerGeneration < 1 || identity.PID < 1 {
		return errors.New("Binding child control identity is incomplete")
	}
	return nil
}

// HostControlCallbacks are adapters into machine services. The Computer owns
// authentication, generation fencing, capacity, and request routing; it never
// imports the Binding execution package.
type HostControlCallbacks struct {
	Current             func(BindingChildIdentity) bool
	RuntimeSet          func(context.Context, BindingChildIdentity, json.RawMessage, string, time.Time) error
	Diagnostic          func(context.Context, BindingChildIdentity, string, diagnosticlog.Event) error
	LifecycleDiagnostic func(context.Context, BindingChildIdentity, json.RawMessage) error
	MachineActions      func(context.Context, BindingChildIdentity, json.RawMessage) error
	Released            func(BindingChildIdentity)
}

// HostControl is the Computer-owned local control Interface shared by all
// Binding children.
type HostControl struct {
	token     string
	capacity  *ProcessCapacity
	callbacks HostControlCallbacks

	mu     sync.Mutex
	grants map[BindingChildIdentity]map[string]ProcessCapacityGrant
}

func NewHostControl(token string, capacity *ProcessCapacity, callbacks HostControlCallbacks) *HostControl {
	return &HostControl{
		token: strings.TrimSpace(token), capacity: capacity, callbacks: callbacks,
		grants: make(map[BindingChildIdentity]map[string]ProcessCapacityGrant),
	}
}

func (control *HostControl) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc(bindingChildAttestPath, control.attestHandler())
	mux.HandleFunc(bindingChildCapacityPath, control.capacityHandler())
	mux.HandleFunc(bindingChildDiagnosticPath, control.diagnosticHandler())
	mux.HandleFunc(bindingChildLifecycleDiagnosticPath, control.lifecycleDiagnosticHandler())
	mux.HandleFunc(bindingChildMachineActionsPath, control.machineActionsHandler())
	mux.HandleFunc(bindingChildRuntimeSetPath, control.runtimeSetHandler())
}

func (control *HostControl) Release(identity BindingChildIdentity) {
	if control == nil {
		return
	}
	control.mu.Lock()
	owned := control.grants[identity]
	delete(control.grants, identity)
	control.mu.Unlock()
	if control.capacity != nil {
		for _, grant := range owned {
			control.capacity.Cancel(grant)
		}
	}
	if control.callbacks.Released != nil {
		control.callbacks.Released(identity)
	}
}

func (control *HostControl) current(identity BindingChildIdentity) bool {
	return control != nil && identity.Validate() == nil && control.callbacks.Current != nil && control.callbacks.Current(identity)
}

func (control *HostControl) authorized(r *http.Request) bool {
	if control == nil {
		return false
	}
	provided := strings.TrimSpace(r.Header.Get("X-Multica-Control-Token"))
	return control.token != "" && provided != "" && subtle.ConstantTimeCompare([]byte(control.token), []byte(provided)) == 1
}

func (control *HostControl) begin(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	if !control.authorized(r) {
		http.Error(w, "local control authentication failed", http.StatusUnauthorized)
		return false
	}
	if err := decodeHostControlJSON(w, r, target); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func (control *HostControl) attestHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var identity BindingChildIdentity
		if !control.begin(w, r, &identity) {
			return
		}
		if !control.current(identity) {
			http.Error(w, "inactive Binding child generation", http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type capacityControlRequest struct {
	Identity    BindingChildIdentity `json:"identity"`
	Operation   string               `json:"operation"`
	WorkspaceID string               `json:"workspace_id,omitempty"`
	AgentID     string               `json:"agent_id,omitempty"`
	RuntimeID   string               `json:"runtime_id,omitempty"`
	LaunchID    string               `json:"launch_id,omitempty"`
	Grant       ProcessCapacityGrant `json:"grant,omitempty"`
}

type capacityControlResponse struct {
	Grant    ProcessCapacityGrant `json:"grant,omitempty"`
	Admitted bool                 `json:"admitted,omitempty"`
	Active   bool                 `json:"active,omitempty"`
}

func (control *HostControl) capacityHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request capacityControlRequest
		if !control.begin(w, r, &request) {
			return
		}
		if !control.current(request.Identity) {
			http.Error(w, "inactive Binding child generation", http.StatusConflict)
			return
		}
		if request.WorkspaceID != "" && strings.TrimSpace(request.WorkspaceID) != strings.TrimSpace(request.Identity.WorkspaceID) {
			http.Error(w, "capacity request belongs to another Workspace", http.StatusForbidden)
			return
		}
		if control.capacity == nil {
			http.Error(w, "Host capacity admission is unavailable", http.StatusServiceUnavailable)
			return
		}
		response := capacityControlResponse{}
		switch request.Operation {
		case "acquire":
			if control.launchOwnedByOther(request.Identity, strings.TrimSpace(request.LaunchID)) {
				http.Error(w, "capacity grant identity conflict", http.StatusConflict)
				return
			}
			response.Grant, response.Admitted = control.capacity.Acquire(ProcessCapacityRequest{
				WorkspaceID: request.Identity.WorkspaceID, AgentID: strings.TrimSpace(request.AgentID),
				RuntimeID: strings.TrimSpace(request.RuntimeID), LaunchID: strings.TrimSpace(request.LaunchID),
			})
			if response.Grant.LaunchID == "" || !control.track(request.Identity, response.Grant) {
				http.Error(w, "capacity grant identity conflict", http.StatusConflict)
				return
			}
		case "cancel":
			if !control.owns(request.Identity, request.Grant) {
				http.Error(w, "capacity grant belongs to another Binding child", http.StatusForbidden)
				return
			}
			control.capacity.Cancel(request.Grant)
			control.untrack(request.Identity, request.Grant)
		case "release":
			if !control.owns(request.Identity, request.Grant) {
				http.Error(w, "capacity grant belongs to another Binding child", http.StatusForbidden)
				return
			}
			control.capacity.Release(request.Grant)
			control.untrack(request.Identity, request.Grant)
		case "active":
			response.Active = control.owns(request.Identity, request.Grant) && control.capacity.Active(request.Grant)
		default:
			http.Error(w, "unsupported capacity operation", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}
}

type diagnosticControlRequest struct {
	Identity    BindingChildIdentity `json:"identity"`
	WorkspaceID string               `json:"workspace_id"`
	Event       diagnosticlog.Event  `json:"event"`
}

func (control *HostControl) diagnosticHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request diagnosticControlRequest
		if !control.begin(w, r, &request) {
			return
		}
		if !control.current(request.Identity) {
			http.Error(w, "inactive Binding child generation", http.StatusConflict)
			return
		}
		if strings.TrimSpace(request.WorkspaceID) != strings.TrimSpace(request.Identity.WorkspaceID) {
			http.Error(w, "diagnostic belongs to another Workspace", http.StatusForbidden)
			return
		}
		if control.callbacks.Diagnostic == nil || control.callbacks.Diagnostic(r.Context(), request.Identity, request.WorkspaceID, request.Event) != nil {
			http.Error(w, "Host diagnostic aggregation failed", http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type rawControlRequest struct {
	Identity BindingChildIdentity `json:"identity"`
	Payload  json.RawMessage      `json:"payload"`
}

func (control *HostControl) lifecycleDiagnosticHandler() http.HandlerFunc {
	return control.rawHandler(control.callbacks.LifecycleDiagnostic)
}

func (control *HostControl) machineActionsHandler() http.HandlerFunc {
	return control.rawHandler(control.callbacks.MachineActions)
}

func (control *HostControl) rawHandler(callback func(context.Context, BindingChildIdentity, json.RawMessage) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request rawControlRequest
		if !control.begin(w, r, &request) {
			return
		}
		if !control.current(request.Identity) {
			http.Error(w, "inactive Binding child generation", http.StatusConflict)
			return
		}
		if len(request.Payload) == 0 || callback == nil || callback(r.Context(), request.Identity, request.Payload) != nil {
			http.Error(w, "Binding child control payload rejected", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type runtimeSetControlRequest struct {
	Identity             BindingChildIdentity `json:"identity"`
	Runtimes             json.RawMessage      `json:"runtimes"`
	DaemonToken          string               `json:"daemon_token"`
	DaemonTokenExpiresAt string               `json:"daemon_token_expires_at"`
}

func (control *HostControl) runtimeSetHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request runtimeSetControlRequest
		if !control.begin(w, r, &request) {
			return
		}
		if !control.current(request.Identity) {
			http.Error(w, "inactive Binding child generation", http.StatusConflict)
			return
		}
		expiresAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(request.DaemonTokenExpiresAt))
		if err != nil || strings.TrimSpace(request.DaemonToken) == "" || !expiresAt.After(time.Now()) || len(request.Runtimes) == 0 {
			http.Error(w, "Binding child Runtime credential is invalid", http.StatusBadRequest)
			return
		}
		if control.callbacks.RuntimeSet == nil || control.callbacks.RuntimeSet(r.Context(), request.Identity, request.Runtimes, request.DaemonToken, expiresAt) != nil {
			http.Error(w, "Binding child Runtime set rejected", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (control *HostControl) launchOwnedByOther(identity BindingChildIdentity, launchID string) bool {
	if launchID == "" {
		return false
	}
	control.mu.Lock()
	defer control.mu.Unlock()
	for owner, grants := range control.grants {
		if _, ok := grants[launchID]; ok && owner != identity {
			return true
		}
	}
	return false
}

func (control *HostControl) track(identity BindingChildIdentity, grant ProcessCapacityGrant) bool {
	control.mu.Lock()
	defer control.mu.Unlock()
	for owner, grants := range control.grants {
		if current, ok := grants[grant.LaunchID]; ok && (owner != identity || current != grant) {
			return false
		}
	}
	grants := control.grants[identity]
	if grants == nil {
		grants = make(map[string]ProcessCapacityGrant)
		control.grants[identity] = grants
	}
	grants[grant.LaunchID] = grant
	return true
}

func (control *HostControl) owns(identity BindingChildIdentity, grant ProcessCapacityGrant) bool {
	control.mu.Lock()
	defer control.mu.Unlock()
	return control.grants[identity][grant.LaunchID] == grant && grant.LaunchID != ""
}

func (control *HostControl) untrack(identity BindingChildIdentity, grant ProcessCapacityGrant) {
	control.mu.Lock()
	grants := control.grants[identity]
	if grants[grant.LaunchID] == grant {
		delete(grants, grant.LaunchID)
	}
	if len(grants) == 0 {
		delete(control.grants, identity)
	}
	control.mu.Unlock()
}

func decodeHostControlJSON(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid Binding child control request")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("invalid Binding child control request")
	}
	return nil
}

// HostControlClient is the Binding child's typed process adapter.
type HostControlClient struct {
	baseURL  string
	token    string
	identity BindingChildIdentity
	http     *http.Client
}

func NewHostControlClient(baseURL, token string, identity BindingChildIdentity) *HostControlClient {
	return &HostControlClient{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), token: strings.TrimSpace(token), identity: identity,
		http: &http.Client{Timeout: 5 * time.Second},
	}
}

func (client *HostControlClient) Attest(ctx context.Context) error {
	return client.post(ctx, bindingChildAttestPath, client.identity, nil)
}

func (client *HostControlClient) AwaitAttest(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var lastErr error
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := client.Attest(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("attest Binding child to Host: %w: %v", ctx.Err(), lastErr)
		case <-ticker.C:
		}
	}
}

func (client *HostControlClient) AcquireCapacity(ctx context.Context, request ProcessCapacityRequest) (ProcessCapacityGrant, bool, error) {
	var response capacityControlResponse
	err := client.post(ctx, bindingChildCapacityPath, capacityControlRequest{
		Identity: client.identity, Operation: "acquire", WorkspaceID: request.WorkspaceID,
		AgentID: request.AgentID, RuntimeID: request.RuntimeID, LaunchID: request.LaunchID,
	}, &response)
	return response.Grant, response.Admitted, err
}

func (client *HostControlClient) CapacityActive(ctx context.Context, grant ProcessCapacityGrant) (bool, error) {
	var response capacityControlResponse
	err := client.post(ctx, bindingChildCapacityPath, capacityControlRequest{Identity: client.identity, Operation: "active", Grant: grant}, &response)
	return response.Active, err
}

func (client *HostControlClient) CancelCapacity(ctx context.Context, grant ProcessCapacityGrant) error {
	return client.post(ctx, bindingChildCapacityPath, capacityControlRequest{Identity: client.identity, Operation: "cancel", Grant: grant}, &capacityControlResponse{})
}

func (client *HostControlClient) ReleaseCapacity(ctx context.Context, grant ProcessCapacityGrant) error {
	return client.post(ctx, bindingChildCapacityPath, capacityControlRequest{Identity: client.identity, Operation: "release", Grant: grant}, &capacityControlResponse{})
}

func (client *HostControlClient) RecordDiagnostic(ctx context.Context, workspaceID string, event diagnosticlog.Event) error {
	return client.post(ctx, bindingChildDiagnosticPath, diagnosticControlRequest{Identity: client.identity, WorkspaceID: workspaceID, Event: event}, nil)
}

func (client *HostControlClient) RecordLifecycleDiagnostic(ctx context.Context, transition any) error {
	return client.postRaw(ctx, bindingChildLifecycleDiagnosticPath, transition)
}

func (client *HostControlClient) ForwardMachineActions(ctx context.Context, actions any) error {
	return client.postRaw(ctx, bindingChildMachineActionsPath, actions)
}

func (client *HostControlClient) ReportRuntimeSet(ctx context.Context, runtimes any, daemonToken, expiresAt string) error {
	return client.post(ctx, bindingChildRuntimeSetPath, struct {
		Identity             BindingChildIdentity `json:"identity"`
		Runtimes             any                  `json:"runtimes"`
		DaemonToken          string               `json:"daemon_token"`
		DaemonTokenExpiresAt string               `json:"daemon_token_expires_at"`
	}{client.identity, runtimes, daemonToken, expiresAt}, nil)
}

func (client *HostControlClient) postRaw(ctx context.Context, path string, payload any) error {
	return client.post(ctx, path, struct {
		Identity BindingChildIdentity `json:"identity"`
		Payload  any                  `json:"payload"`
	}{client.identity, payload}, nil)
}

func (client *HostControlClient) post(ctx context.Context, path string, input, output any) error {
	if client == nil || client.baseURL == "" || client.token == "" || client.identity.Validate() != nil {
		return errors.New("Binding Host control client is not configured")
	}
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Multica-Control-Token", client.token)
	response, err := client.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, response.Body)
		return fmt.Errorf("Binding Host control %s returned %s", path, response.Status)
	}
	if output == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(output); err != nil {
		return fmt.Errorf("decode Binding Host control %s: %w", path, err)
	}
	return nil
}
