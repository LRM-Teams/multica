package computer

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/diagnosticlog"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const workspaceDaemonControlBusyCode = "control_busy"

// WorkspaceDaemonIdentity fences every WorkspaceDaemon-to-Computer request by the process-reported
// daemonInstanceId and OS process identity the Computer supervises.
type WorkspaceDaemonIdentity struct {
	WorkspaceID      string `json:"workspaceId"`
	DaemonInstanceID string `json:"daemonInstanceId"`
	PID              int    `json:"pid"`
}

func (identity WorkspaceDaemonIdentity) Validate() error {
	if strings.TrimSpace(identity.WorkspaceID) == "" || strings.TrimSpace(identity.DaemonInstanceID) == "" || identity.PID < 1 {
		return errors.New("WorkspaceDaemon control identity is incomplete")
	}
	return nil
}

// ComputerControlCallbacks are adapters into machine services. ComputerCore owns
// authentication, generation fencing, and request routing; it never
// imports WorkspaceDaemon execution details.
type ComputerControlCallbacks struct {
	Current         func(WorkspaceDaemonIdentity) bool
	RuntimeSet      func(context.Context, WorkspaceDaemonIdentity, json.RawMessage, string, time.Time) error
	Diagnostic      func(context.Context, WorkspaceDaemonIdentity, string, diagnosticlog.Event) error
	MachineActions  func(context.Context, WorkspaceDaemonIdentity, json.RawMessage) error
	ComputerUpgrade func(context.Context, WorkspaceDaemonIdentity, json.RawMessage) error
	WorkDigest      func(context.Context, WorkspaceDaemonIdentity, protocol.ComputerWorkDigestPayload) (protocol.WorkDigest, error)
	WorkJournal     func(context.Context, WorkspaceDaemonIdentity, protocol.ComputerWorkJournalPayload) (bool, error)
	Released        func(WorkspaceDaemonIdentity)
}

// ComputerControl is ComputerCore's local control interface for WorkspaceDaemons.
type ComputerControl struct {
	token     string
	callbacks ComputerControlCallbacks
}

// RegisterLocalControlHandlers adds ComputerCore-owned operations to an IPC registry.
func (control *ComputerControl) RegisterLocalControlHandlers(registry *LocalControlRegistry) {
	control.RegisterRPCHandlers(registry)
}

func NewComputerControl(token string, callbacks ComputerControlCallbacks) *ComputerControl {
	return &ComputerControl{
		token: strings.TrimSpace(token), callbacks: callbacks,
	}
}

// RegisterRPCHandlers exposes the same authenticated child operations on the
// production framed transport. Payload validation and generation fencing are
// performed before invoking the callback; the payload itself is intentionally
// open because these callbacks own its domain schema.
func (control *ComputerControl) RegisterRPCHandlers(registry *LocalControlRegistry) {
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
	register(LocalControlWorkspaceDiagnosticsOperation, func(ctx context.Context, headers map[string]string, raw json.RawMessage) (any, error) {
		var request diagnosticControlRequest
		if err := decode(headers, raw, &request); err != nil {
			return nil, err
		}
		if !control.current(request.Identity) || strings.TrimSpace(request.WorkspaceID) != strings.TrimSpace(request.Identity.WorkspaceID) {
			return nil, errors.New("diagnostic identity is invalid")
		}
		if control.callbacks.Diagnostic == nil {
			return nil, errors.New("Computer diagnostic aggregation failed")
		}
		return nil, control.callbacks.Diagnostic(ctx, request.Identity, request.WorkspaceID, request.Event)
	})
	register(LocalControlComputerControlOperation, control.rpcRawCallback(registry, LocalControlComputerControlOperation, control.callbacks.MachineActions))
	register(LocalControlRunnerReadyOperation, control.rpcRuntimeSet)
	register(LocalControlWorkDigestOperation, control.rpcWorkDigest)
	register(LocalControlWorkJournalOperation, control.rpcWorkJournal)
}

func (control *ComputerControl) rpcWorkDigest(ctx context.Context, headers map[string]string, raw json.RawMessage) (any, error) {
	var request struct {
		Identity WorkspaceDaemonIdentity            `json:"identity"`
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

func (control *ComputerControl) rpcWorkJournal(ctx context.Context, headers map[string]string, raw json.RawMessage) (any, error) {
	var request struct {
		Identity WorkspaceDaemonIdentity             `json:"identity"`
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

func (control *ComputerControl) rpcRawCallback(_ *LocalControlRegistry, _ string, callback func(context.Context, WorkspaceDaemonIdentity, json.RawMessage) error) LocalControlHandler {
	return func(ctx context.Context, headers map[string]string, raw json.RawMessage) (any, error) {
		var request rawControlRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		if err := control.decodeRPCIdentity(headers, raw, &request.Identity); err != nil {
			return nil, err
		}
		if callback == nil || len(request.Payload) == 0 {
			return nil, errors.New("WorkspaceDaemon control payload rejected")
		}
		err := callback(ctx, request.Identity, request.Payload)
		if errors.Is(err, ErrComputerControlBusy) {
			return nil, withLocalControlCode(workspaceDaemonControlBusyCode, err)
		}
		return nil, err
	}
}

func (control *ComputerControl) decodeRPCIdentity(headers map[string]string, raw json.RawMessage, identity *WorkspaceDaemonIdentity) error {
	if !control.authorizeHeaders(headers) {
		return errors.New("local control authentication failed")
	}
	if err := json.Unmarshal(raw, &struct {
		Identity *WorkspaceDaemonIdentity `json:"identity"`
	}{Identity: identity}); err != nil || identity.Validate() != nil {
		return errors.New("inactive WorkspaceDaemon process")
	}
	if !control.current(*identity) {
		return errors.New("inactive WorkspaceDaemon process")
	}
	return nil
}

func (control *ComputerControl) rpcRuntimeSet(ctx context.Context, headers map[string]string, raw json.RawMessage) (any, error) {
	var request runtimeSetControlRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	if err := control.decodeRPCIdentity(headers, raw, &request.Identity); err != nil {
		return nil, err
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(request.DaemonTokenExpiresAt))
	if err != nil || strings.TrimSpace(request.DaemonToken) == "" || !expiresAt.After(time.Now()) || len(request.Runtimes) == 0 {
		return nil, errors.New("WorkspaceDaemon Runtime credential is invalid")
	}
	if control.callbacks.RuntimeSet == nil {
		return nil, errors.New("WorkspaceDaemon Runtime set rejected")
	}
	return nil, control.callbacks.RuntimeSet(ctx, request.Identity, request.Runtimes, request.DaemonToken, expiresAt)
}

func (control *ComputerControl) authorizeHeaders(headers map[string]string) bool {
	provided := strings.TrimSpace(headers["X-Multica-Control-Token"])
	return control != nil && control.token != "" && provided != "" && subtle.ConstantTimeCompare([]byte(control.token), []byte(provided)) == 1
}

func (control *ComputerControl) Release(identity WorkspaceDaemonIdentity) {
	if control == nil {
		return
	}
	if control.callbacks.Released != nil {
		control.callbacks.Released(identity)
	}
}

func (control *ComputerControl) current(identity WorkspaceDaemonIdentity) bool {
	return control != nil && identity.Validate() == nil && control.callbacks.Current != nil && control.callbacks.Current(identity)
}

type diagnosticControlRequest struct {
	Identity    WorkspaceDaemonIdentity `json:"identity"`
	WorkspaceID string                  `json:"workspaceId"`
	Event       diagnosticlog.Event     `json:"event"`
}

type rawControlRequest struct {
	Identity WorkspaceDaemonIdentity `json:"identity"`
	Payload  json.RawMessage         `json:"payload"`
}

type upgradeStartControlRequest struct {
	Identity WorkspaceDaemonIdentity         `json:"identity"`
	Command  protocol.ComputerUpgradePayload `json:"command"`
}

type runtimeSetControlRequest struct {
	Identity             WorkspaceDaemonIdentity `json:"identity"`
	Runtimes             json.RawMessage         `json:"runtimes"`
	DaemonToken          string                  `json:"daemonToken"`
	DaemonTokenExpiresAt string                  `json:"daemonTokenExpiresAt"`
}

// ComputerControlClient is a WorkspaceDaemon's typed adapter to ComputerCore.
type ComputerControlClient struct {
	token    string
	identity WorkspaceDaemonIdentity
	control  *localControlClient
	initErr  error
}

func NewComputerControlClient(endpoint, token string, identity WorkspaceDaemonIdentity) *ComputerControlClient {
	controlClient, err := localControlClientFor(endpoint, 5*time.Second)
	return &ComputerControlClient{token: strings.TrimSpace(token), identity: identity, control: controlClient, initErr: err}
}

func (client *ComputerControlClient) RecordDiagnostic(ctx context.Context, workspaceID string, event diagnosticlog.Event) error {
	return client.post(ctx, LocalControlWorkspaceDiagnosticsOperation, diagnosticControlRequest{Identity: client.identity, WorkspaceID: workspaceID, Event: event}, nil)
}

func (client *ComputerControlClient) ForwardComputerControl(ctx context.Context, actions any) error {
	return client.postRaw(ctx, LocalControlComputerControlOperation, actions)
}

func (client *ComputerControlClient) RequestComputerUpgrade(ctx context.Context, command protocol.ComputerUpgradePayload) error {
	return client.post(ctx, LocalControlUpgradeStartOperation, upgradeStartControlRequest{
		Identity: client.identity, Command: command,
	}, nil)
}

func (client *ComputerControlClient) HarvestWorkDigest(ctx context.Context, command protocol.ComputerWorkDigestPayload) (protocol.WorkDigest, error) {
	if err := command.Validate(); err != nil {
		return protocol.WorkDigest{}, err
	}
	var digest protocol.WorkDigest
	if err := client.post(ctx, LocalControlWorkDigestOperation, struct {
		Identity WorkspaceDaemonIdentity            `json:"identity"`
		Command  protocol.ComputerWorkDigestPayload `json:"command"`
	}{Identity: client.identity, Command: command}, &digest); err != nil {
		return protocol.WorkDigest{}, err
	}
	return digest, nil
}

func (client *ComputerControlClient) SetWorkJournalEnabled(ctx context.Context, command protocol.ComputerWorkJournalPayload) (bool, error) {
	if err := command.Validate(); err != nil {
		return false, err
	}
	var out struct {
		Enabled bool `json:"enabled"`
	}
	if err := client.post(ctx, LocalControlWorkJournalOperation, struct {
		Identity WorkspaceDaemonIdentity             `json:"identity"`
		Command  protocol.ComputerWorkJournalPayload `json:"command"`
	}{Identity: client.identity, Command: command}, &out); err != nil {
		return false, err
	}
	return out.Enabled, nil
}

func (client *ComputerControlClient) ReportRuntimeSet(ctx context.Context, runtimes any, daemonToken, expiresAt string) error {
	return client.post(ctx, LocalControlRunnerReadyOperation, struct {
		Identity             WorkspaceDaemonIdentity `json:"identity"`
		Runtimes             any                     `json:"runtimes"`
		DaemonToken          string                  `json:"daemonToken"`
		DaemonTokenExpiresAt string                  `json:"daemonTokenExpiresAt"`
	}{client.identity, runtimes, daemonToken, expiresAt}, nil)
}

func (client *ComputerControlClient) postRaw(ctx context.Context, operation string, payload any) error {
	return client.post(ctx, operation, struct {
		Identity WorkspaceDaemonIdentity `json:"identity"`
		Payload  any                     `json:"payload"`
	}{client.identity, payload}, nil)
}

func (client *ComputerControlClient) post(ctx context.Context, operation string, input, output any) error {
	if client == nil || client.initErr != nil || client.control == nil || client.token == "" || client.identity.Validate() != nil {
		return errors.New("Computer control client is not configured")
	}
	var raw json.RawMessage
	if err := client.control.Call(ctx, operation, map[string]string{
		"Content-Type": "application/json", "X-Multica-Control-Token": client.token,
	}, input, &raw); err != nil {
		if strings.Contains(err.Error(), workspaceDaemonControlBusyCode) {
			return ErrComputerControlBusy
		}
		return err
	}
	if output == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, output); err != nil {
		return fmt.Errorf("decode Computer control %s: %w", operation, err)
	}
	return nil
}
