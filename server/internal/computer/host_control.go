package computer

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/diagnosticlog"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const bindingChildControlBusyCode = "control_busy"

// BindingChildIdentity fences every child-to-Host request by the immutable
// managed runner start identity and OS process identity the Computer supervises.
type BindingChildIdentity struct {
	WorkspaceID   string `json:"workspaceId"`
	StartIdentity string `json:"startIdentity"`
	PID           int    `json:"pid"`
}

func (identity BindingChildIdentity) Validate() error {
	if strings.TrimSpace(identity.WorkspaceID) == "" || strings.TrimSpace(identity.StartIdentity) == "" || identity.PID < 1 {
		return errors.New("Binding child control identity is incomplete")
	}
	return nil
}

// HostControlCallbacks are adapters into machine services. The Computer owns
// authentication, generation fencing, capacity, and request routing; it never
// imports the Binding execution package.
type HostControlCallbacks struct {
	Current         func(BindingChildIdentity) bool
	RuntimeSet      func(context.Context, BindingChildIdentity, json.RawMessage, string, time.Time) error
	Diagnostic      func(context.Context, BindingChildIdentity, string, diagnosticlog.Event) error
	MachineActions  func(context.Context, BindingChildIdentity, json.RawMessage) error
	ComputerUpgrade func(context.Context, BindingChildIdentity, json.RawMessage) error
	WorkDigest      func(context.Context, BindingChildIdentity, protocol.ComputerWorkDigestPayload) (protocol.WorkDigest, error)
	WorkJournal     func(context.Context, BindingChildIdentity, protocol.ComputerWorkJournalPayload) (bool, error)
	Released        func(BindingChildIdentity)
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

// RegisterLocalControlHandlers adds the Host-owned operations to an IPC
// registry. The process runner uses this to compose the service registry.
func (control *HostControl) RegisterLocalControlHandlers(registry *LocalControlRegistry) {
	control.RegisterRPCHandlers(registry)
}

func NewHostControl(token string, capacity *ProcessCapacity, callbacks HostControlCallbacks) *HostControl {
	return &HostControl{
		token: strings.TrimSpace(token), capacity: capacity, callbacks: callbacks,
		grants: make(map[BindingChildIdentity]map[string]ProcessCapacityGrant),
	}
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
	register(LocalControlWorkspaceCapacityOperation, func(ctx context.Context, headers map[string]string, raw json.RawMessage) (any, error) {
		var request capacityControlRequest
		if err := decode(headers, raw, &request); err != nil {
			return nil, err
		}
		if !control.current(request.Identity) {
			return nil, errors.New("inactive managed runner process")
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
	register(LocalControlWorkspaceDiagnosticsOperation, func(ctx context.Context, headers map[string]string, raw json.RawMessage) (any, error) {
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
	register(LocalControlComputerControlOperation, control.rpcRawCallback(registry, LocalControlComputerControlOperation, control.callbacks.MachineActions))
	register(LocalControlRunnerReadyOperation, control.rpcRuntimeSet)
	register(LocalControlWorkDigestOperation, control.rpcWorkDigest)
	register(LocalControlWorkJournalOperation, control.rpcWorkJournal)
}

func (control *HostControl) rpcWorkDigest(ctx context.Context, headers map[string]string, raw json.RawMessage) (any, error) {
	var request struct {
		Identity BindingChildIdentity               `json:"identity"`
		Command  protocol.ComputerWorkDigestPayload `json:"command"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	if err := control.decodeRPCIdentity(headers, raw, &request.Identity); err != nil {
		return nil, err
	}
	if control.callbacks.WorkDigest == nil {
		return nil, errors.New("Computer work journal is unavailable")
	}
	return control.callbacks.WorkDigest(ctx, request.Identity, request.Command)
}

func (control *HostControl) rpcWorkJournal(ctx context.Context, headers map[string]string, raw json.RawMessage) (any, error) {
	var request struct {
		Identity BindingChildIdentity                `json:"identity"`
		Command  protocol.ComputerWorkJournalPayload `json:"command"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	if err := control.decodeRPCIdentity(headers, raw, &request.Identity); err != nil {
		return nil, err
	}
	if control.callbacks.WorkJournal == nil {
		return nil, errors.New("Computer work journal is unavailable")
	}
	enabled, err := control.callbacks.WorkJournal(ctx, request.Identity, request.Command)
	if err != nil {
		return nil, err
	}
	return map[string]bool{"enabled": enabled}, nil
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
		err := callback(ctx, request.Identity, request.Payload)
		if errors.Is(err, ErrComputerControlBusy) {
			return nil, withLocalControlCode(bindingChildControlBusyCode, err)
		}
		return nil, err
	}
}

func (control *HostControl) decodeRPCIdentity(headers map[string]string, raw json.RawMessage, identity *BindingChildIdentity) error {
	if !control.authorizeHeaders(headers) {
		return errors.New("local control authentication failed")
	}
	if err := json.Unmarshal(raw, &struct {
		Identity *BindingChildIdentity `json:"identity"`
	}{Identity: identity}); err != nil || identity.Validate() != nil {
		return errors.New("inactive managed runner process")
	}
	if !control.current(*identity) {
		return errors.New("inactive managed runner process")
	}
	return nil
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

type capacityControlRequest struct {
	Identity    BindingChildIdentity `json:"identity"`
	Operation   string               `json:"operation"`
	WorkspaceID string               `json:"workspaceId,omitempty"`
	AgentID     string               `json:"agentId,omitempty"`
	RuntimeID   string               `json:"runtimeId,omitempty"`
	LaunchID    string               `json:"launchId,omitempty"`
	Grant       ProcessCapacityGrant `json:"grant,omitempty"`
}

type capacityControlResponse struct {
	Grant    ProcessCapacityGrant `json:"grant,omitempty"`
	Admitted bool                 `json:"admitted,omitempty"`
	Active   bool                 `json:"active,omitempty"`
}

type diagnosticControlRequest struct {
	Identity    BindingChildIdentity `json:"identity"`
	WorkspaceID string               `json:"workspaceId"`
	Event       diagnosticlog.Event  `json:"event"`
}

type rawControlRequest struct {
	Identity BindingChildIdentity `json:"identity"`
	Payload  json.RawMessage      `json:"payload"`
}

type runtimeSetControlRequest struct {
	Identity             BindingChildIdentity `json:"identity"`
	Runtimes             json.RawMessage      `json:"runtimes"`
	DaemonToken          string               `json:"daemonToken"`
	DaemonTokenExpiresAt string               `json:"daemonTokenExpiresAt"`
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

// HostControlClient is the Binding child's typed process adapter.
type HostControlClient struct {
	token    string
	identity BindingChildIdentity
	control  *localControlClient
	initErr  error
}

func NewHostControlClient(endpoint, token string, identity BindingChildIdentity) *HostControlClient {
	controlClient, err := localControlClientFor(endpoint, 5*time.Second)
	return &HostControlClient{token: strings.TrimSpace(token), identity: identity, control: controlClient, initErr: err}
}

func (client *HostControlClient) AcquireCapacity(ctx context.Context, request ProcessCapacityRequest) (ProcessCapacityGrant, bool, error) {
	var response capacityControlResponse
	err := client.post(ctx, LocalControlWorkspaceCapacityOperation, capacityControlRequest{
		Identity: client.identity, Operation: "acquire", WorkspaceID: request.WorkspaceID,
		AgentID: request.AgentID, RuntimeID: request.RuntimeID, LaunchID: request.LaunchID,
	}, &response)
	return response.Grant, response.Admitted, err
}

func (client *HostControlClient) CapacityActive(ctx context.Context, grant ProcessCapacityGrant) (bool, error) {
	var response capacityControlResponse
	err := client.post(ctx, LocalControlWorkspaceCapacityOperation, capacityControlRequest{Identity: client.identity, Operation: "active", Grant: grant}, &response)
	return response.Active, err
}

func (client *HostControlClient) CancelCapacity(ctx context.Context, grant ProcessCapacityGrant) error {
	return client.post(ctx, LocalControlWorkspaceCapacityOperation, capacityControlRequest{Identity: client.identity, Operation: "cancel", Grant: grant}, &capacityControlResponse{})
}

func (client *HostControlClient) ReleaseCapacity(ctx context.Context, grant ProcessCapacityGrant) error {
	return client.post(ctx, LocalControlWorkspaceCapacityOperation, capacityControlRequest{Identity: client.identity, Operation: "release", Grant: grant}, &capacityControlResponse{})
}

func (client *HostControlClient) RecordDiagnostic(ctx context.Context, workspaceID string, event diagnosticlog.Event) error {
	return client.post(ctx, LocalControlWorkspaceDiagnosticsOperation, diagnosticControlRequest{Identity: client.identity, WorkspaceID: workspaceID, Event: event}, nil)
}

func (client *HostControlClient) ForwardComputerControl(ctx context.Context, actions any) error {
	return client.postRaw(ctx, LocalControlComputerControlOperation, actions)
}

func (client *HostControlClient) RequestComputerUpgrade(ctx context.Context, command any) error {
	return client.postRaw(ctx, LocalControlUpgradeStartOperation, command)
}

func (client *HostControlClient) HarvestWorkDigest(ctx context.Context, command protocol.ComputerWorkDigestPayload) (protocol.WorkDigest, error) {
	if err := command.Validate(); err != nil {
		return protocol.WorkDigest{}, err
	}
	var digest protocol.WorkDigest
	if err := client.post(ctx, LocalControlWorkDigestOperation, struct {
		Identity BindingChildIdentity               `json:"identity"`
		Command  protocol.ComputerWorkDigestPayload `json:"command"`
	}{Identity: client.identity, Command: command}, &digest); err != nil {
		return protocol.WorkDigest{}, err
	}
	return digest, nil
}

func (client *HostControlClient) SetWorkJournalEnabled(ctx context.Context, command protocol.ComputerWorkJournalPayload) (bool, error) {
	if err := command.Validate(); err != nil {
		return false, err
	}
	var out struct {
		Enabled bool `json:"enabled"`
	}
	if err := client.post(ctx, LocalControlWorkJournalOperation, struct {
		Identity BindingChildIdentity                `json:"identity"`
		Command  protocol.ComputerWorkJournalPayload `json:"command"`
	}{Identity: client.identity, Command: command}, &out); err != nil {
		return false, err
	}
	return out.Enabled, nil
}

func (client *HostControlClient) ReportRuntimeSet(ctx context.Context, runtimes any, daemonToken, expiresAt string) error {
	return client.post(ctx, LocalControlRunnerReadyOperation, struct {
		Identity             BindingChildIdentity `json:"identity"`
		Runtimes             any                  `json:"runtimes"`
		DaemonToken          string               `json:"daemonToken"`
		DaemonTokenExpiresAt string               `json:"daemonTokenExpiresAt"`
	}{client.identity, runtimes, daemonToken, expiresAt}, nil)
}

func (client *HostControlClient) postRaw(ctx context.Context, operation string, payload any) error {
	return client.post(ctx, operation, struct {
		Identity BindingChildIdentity `json:"identity"`
		Payload  any                  `json:"payload"`
	}{client.identity, payload}, nil)
}

func (client *HostControlClient) post(ctx context.Context, operation string, input, output any) error {
	if client == nil || client.initErr != nil || client.control == nil || client.token == "" || client.identity.Validate() != nil {
		return errors.New("Binding Host control client is not configured")
	}
	var raw json.RawMessage
	if err := client.control.Call(ctx, operation, map[string]string{
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
		return fmt.Errorf("decode Binding Host control %s: %w", operation, err)
	}
	return nil
}
