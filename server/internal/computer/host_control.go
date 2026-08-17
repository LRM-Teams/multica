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
	bindingChildCapacityPath            = "/binding-child/capacity"
	bindingChildDiagnosticPath          = "/binding-child/diagnostics"
	bindingChildLifecycleDiagnosticPath = "/binding-child/lifecycle-diagnostics"
	bindingChildMachineActionsPath      = "/binding-child/machine-actions"
	bindingChildRuntimeSetPath          = "/binding-child/runtime-set"
	bindingChildPrepareUpgradePath      = "/binding-child/prepare-upgrade"
	bindingChildComputerUpgradePath     = "/binding-child/computer-upgrade"
	bindingChildControlBusyCode         = "control_busy"
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
	PrepareUpgrade      func(context.Context, BindingChildIdentity, json.RawMessage) (any, error)
	ComputerUpgrade     func(context.Context, BindingChildIdentity, json.RawMessage) error
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
	mux.HandleFunc(bindingChildCapacityPath, control.capacityHandler())
	mux.HandleFunc(bindingChildDiagnosticPath, control.diagnosticHandler())
	mux.HandleFunc(bindingChildLifecycleDiagnosticPath, control.lifecycleDiagnosticHandler())
	mux.HandleFunc(bindingChildMachineActionsPath, control.machineActionsHandler())
	mux.HandleFunc(bindingChildPrepareUpgradePath, control.prepareUpgradeHandler())
	mux.HandleFunc(bindingChildComputerUpgradePath, control.rawHandler(control.callbacks.ComputerUpgrade))
	mux.HandleFunc(bindingChildRuntimeSetPath, control.runtimeSetHandler())
}

// RegisterRPCHandlers exposes the same authenticated child operations on the
// production framed transport. Payload validation and generation fencing are
// performed before invoking the callback; the payload itself is intentionally
// open because these callbacks own its domain schema.
func (control *HostControl) RegisterRPCHandlers(registry *LocalControlRegistry) {
	if control == nil || registry == nil {
		return
	}
	register := func(operation string, handler LocalControlHandler) {
		if err := registry.Register(operation, handler); err != nil && !strings.Contains(err.Error(), "already registered") {
			panic(err)
		}
	}
	decode := func(headers map[string]string, raw json.RawMessage, target any) error {
		if !control.authorizeHeaders(headers) {
			return errors.New("local control authentication failed")
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		return decoder.Decode(target)
	}
	register("workspace-capacity", func(ctx context.Context, headers map[string]string, raw json.RawMessage) (any, error) {
		var request capacityControlRequest
		if err := decode(headers, raw, &request); err != nil {
			return nil, err
		}
		if !control.current(request.Identity) {
			return nil, errors.New("inactive Binding child generation")
		}
		if request.WorkspaceID != "" && strings.TrimSpace(request.WorkspaceID) != strings.TrimSpace(request.Identity.WorkspaceID) {
			return nil, errors.New("capacity request belongs to another Workspace")
		}
		if control.capacity == nil {
			return nil, errors.New("Host capacity admission is unavailable")
		}
		response := capacityControlResponse{}
		switch request.Operation {
		case "acquire":
			if control.launchOwnedByOther(request.Identity, strings.TrimSpace(request.LaunchID)) {
				return nil, errors.New("capacity grant identity conflict")
			}
			response.Grant, response.Admitted = control.capacity.Acquire(ProcessCapacityRequest{WorkspaceID: request.Identity.WorkspaceID, AgentID: strings.TrimSpace(request.AgentID), RuntimeID: strings.TrimSpace(request.RuntimeID), LaunchID: strings.TrimSpace(request.LaunchID)})
			if response.Grant.LaunchID == "" || !control.track(request.Identity, response.Grant) {
				return nil, errors.New("capacity grant identity conflict")
			}
		case "cancel":
			if !control.owns(request.Identity, request.Grant) {
				return nil, errors.New("capacity grant belongs to another Binding child")
			}
			control.capacity.Cancel(request.Grant)
			control.untrack(request.Identity, request.Grant)
		case "release":
			if !control.owns(request.Identity, request.Grant) {
				return nil, errors.New("capacity grant belongs to another Binding child")
			}
			control.capacity.Release(request.Grant)
			control.untrack(request.Identity, request.Grant)
		case "active":
			response.Active = control.owns(request.Identity, request.Grant) && control.capacity.Active(request.Grant)
		default:
			return nil, errors.New("unsupported capacity operation")
		}
		return response, nil
	})
	register("workspace-diagnostics", func(ctx context.Context, headers map[string]string, raw json.RawMessage) (any, error) {
		var request diagnosticControlRequest
		if err := decode(headers, raw, &request); err != nil {
			return nil, err
		}
		if !control.current(request.Identity) || strings.TrimSpace(request.WorkspaceID) != strings.TrimSpace(request.Identity.WorkspaceID) {
			return nil, errors.New("diagnostic identity is invalid")
		}
		if control.callbacks.Diagnostic == nil {
			return nil, errors.New("Host diagnostic aggregation failed")
		}
		return nil, control.callbacks.Diagnostic(ctx, request.Identity, request.WorkspaceID, request.Event)
	})
	register("runner-status", control.rpcRawCallback(registry, "runner-status", control.callbacks.LifecycleDiagnostic))
	register("runner-attestation", control.rpcRawCallback(registry, "runner-attestation", control.callbacks.MachineActions))
	register("runner-drain", control.rpcPrepareUpgrade)
	register("runner-ready", control.rpcRuntimeSet)
}

func (control *HostControl) rpcRawCallback(_ *LocalControlRegistry, _ string, callback func(context.Context, BindingChildIdentity, json.RawMessage) error) LocalControlHandler {
	return func(ctx context.Context, headers map[string]string, raw json.RawMessage) (any, error) {
		var request rawControlRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		if err := control.decodeRPCIdentity(headers, raw, &request.Identity); err != nil {
			return nil, err
		}
		if callback == nil || len(request.Payload) == 0 {
			return nil, errors.New("Binding child control payload rejected")
		}
		return nil, callback(ctx, request.Identity, request.Payload)
	}
}

func (control *HostControl) decodeRPCIdentity(headers map[string]string, raw json.RawMessage, identity *BindingChildIdentity) error {
	if !control.authorizeHeaders(headers) {
		return errors.New("local control authentication failed")
	}
	if err := json.Unmarshal(raw, &struct {
		Identity *BindingChildIdentity `json:"identity"`
	}{Identity: identity}); err != nil || identity.Validate() != nil {
		return errors.New("inactive Binding child generation")
	}
	if !control.current(*identity) {
		return errors.New("inactive Binding child generation")
	}
	return nil
}

func (control *HostControl) rpcPrepareUpgrade(ctx context.Context, headers map[string]string, raw json.RawMessage) (any, error) {
	var request struct {
		Identity BindingChildIdentity `json:"identity"`
		Payload  json.RawMessage      `json:"payload"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	if err := control.decodeRPCIdentity(headers, raw, &request.Identity); err != nil {
		return nil, err
	}
	if control.callbacks.PrepareUpgrade == nil {
		return nil, errors.New("Binding child control payload rejected")
	}
	return control.callbacks.PrepareUpgrade(ctx, request.Identity, request.Payload)
}

func (control *HostControl) rpcRuntimeSet(ctx context.Context, headers map[string]string, raw json.RawMessage) (any, error) {
	var request runtimeSetControlRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	if err := control.decodeRPCIdentity(headers, raw, &request.Identity); err != nil {
		return nil, err
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(request.DaemonTokenExpiresAt))
	if err != nil || strings.TrimSpace(request.DaemonToken) == "" || !expiresAt.After(time.Now()) || len(request.Runtimes) == 0 {
		return nil, errors.New("Binding child Runtime credential is invalid")
	}
	if control.callbacks.RuntimeSet == nil {
		return nil, errors.New("Binding child Runtime set rejected")
	}
	return nil, control.callbacks.RuntimeSet(ctx, request.Identity, request.Runtimes, request.DaemonToken, expiresAt)
}

func (control *HostControl) authorizeHeaders(headers map[string]string) bool {
	provided := strings.TrimSpace(headers["X-Multica-Control-Token"])
	return control != nil && control.token != "" && provided != "" && subtle.ConstantTimeCompare([]byte(control.token), []byte(provided)) == 1
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

func (control *HostControl) prepareUpgradeHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Identity BindingChildIdentity `json:"identity"`
			Payload  json.RawMessage      `json:"payload"`
		}
		if !control.begin(w, r, &request) {
			return
		}
		if !control.current(request.Identity) {
			http.Error(w, "inactive Binding child generation", http.StatusConflict)
			return
		}
		if control.callbacks.PrepareUpgrade == nil {
			http.Error(w, "Binding child control payload rejected", http.StatusBadRequest)
			return
		}
		prepared, err := control.callbacks.PrepareUpgrade(r.Context(), request.Identity, request.Payload)
		if err != nil {
			if errors.Is(err, ErrComputerControlBusy) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]string{"code": bindingChildControlBusyCode})
				return
			}
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(prepared)
	}
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
		if len(request.Payload) == 0 || callback == nil {
			http.Error(w, "Binding child control payload rejected", http.StatusBadRequest)
			return
		}
		if err := callback(r.Context(), request.Identity, request.Payload); err != nil {
			if errors.Is(err, ErrComputerControlBusy) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]string{"code": bindingChildControlBusyCode})
				return
			}
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
	control  *localControlClient
	initErr  error
}

func NewHostControlClient(endpoint, token string, identity BindingChildIdentity) *HostControlClient {
	controlClient, baseURL, err := localControlClientFor(endpoint, 5*time.Second)
	return &HostControlClient{baseURL: baseURL, token: strings.TrimSpace(token), identity: identity, control: controlClient, initErr: err}
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

func (client *HostControlClient) RequestComputerUpgrade(ctx context.Context, command any) error {
	return client.postRaw(ctx, bindingChildComputerUpgradePath, command)
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
	if client == nil || client.initErr != nil || client.control == nil || client.token == "" || client.identity.Validate() != nil {
		return errors.New("Binding Host control client is not configured")
	}
	operation := localControlOperationForPath(path)
	if operation == "" {
		return fmt.Errorf("unknown Binding Host control operation for %s", path)
	}
	var raw json.RawMessage
	if err := client.control.callAt(ctx, operation, path, map[string]string{
		"Content-Type": "application/json", "X-Multica-Control-Token": client.token,
	}, input, &raw); err != nil {
		if strings.Contains(err.Error(), bindingChildControlBusyCode) {
			return ErrComputerControlBusy
		}
		return err
	}
	if output == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, output); err != nil {
		return fmt.Errorf("decode Binding Host control %s: %w", path, err)
	}
	return nil
}
