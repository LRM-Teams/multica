package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// requestError is returned by postJSON/getJSON when the server responds with an error status.
type requestError struct {
	Method     string
	Path       string
	StatusCode int
	Body       string
}

func (e *requestError) Error() string {
	return fmt.Sprintf("%s %s returned %d: %s", e.Method, e.Path, e.StatusCode, e.Body)
}

// isWorkspaceNotFoundError returns true if the error is a 404 with "workspace not found" body.
func isWorkspaceNotFoundError(err error) bool {
	var reqErr *requestError
	if !errors.As(err, &reqErr) {
		return false
	}
	if reqErr.StatusCode != http.StatusNotFound {
		return false
	}
	return strings.Contains(strings.ToLower(reqErr.Body), "workspace not found")
}

// isTaskNotFoundError returns true if the error is a 404 with "task not found"
// body. The daemon uses this to detect that a task was deleted server-side
// (issue removed, agent reassigned, ...) while the local agent was still
// running, so it can interrupt the agent rather than letting it keep
// emitting tool calls against a dead task.
func isTaskNotFoundError(err error) bool {
	var reqErr *requestError
	if !errors.As(err, &reqErr) {
		return false
	}
	if reqErr.StatusCode != http.StatusNotFound {
		return false
	}
	return strings.Contains(strings.ToLower(reqErr.Body), "task not found")
}

// isUnauthorizedError returns true if the error is a 401 from the server.
// Used by the token-renewal loop to surface a clear "re-login required"
// message instead of a generic transport-level retry.
func isUnauthorizedError(err error) bool {
	var reqErr *requestError
	if !errors.As(err, &reqErr) {
		return false
	}
	return reqErr.StatusCode == http.StatusUnauthorized
}

// isAgentNotBoundToRuntimeError returns true if the error is a 403 with
// "agent is not bound to this runtime" body (server/internal/handler/agent_credential.go:256).
// This fires when an agent has been reassigned to a different runtime (a
// normal, supported operation — agent.runtime_id is user-editable) but this
// daemon's local state still reflects the old binding. Unlike a transient
// auth failure, retrying with the same local state can never succeed: the
// daemon must stop treating this agent as its own rather than looping on
// the same request.
func isAgentNotBoundToRuntimeError(err error) bool {
	var reqErr *requestError
	if !errors.As(err, &reqErr) {
		return false
	}
	if reqErr.StatusCode != http.StatusForbidden {
		return false
	}
	return strings.Contains(strings.ToLower(reqErr.Body), "agent is not bound to this runtime")
}

// isRuntimeTransitionInProgressError returns true if the error is a 403
// with "runtime_transition_in_progress" body (task #38,
// server/internal/handler/agent_credential.go's runtimeTransitionInProgressReason).
// Deliberately checked before isAgentNotBoundToRuntimeError's broader
// "agent is not bound" substring elsewhere in the caller's dispatch, since
// this body never contains that phrase — the two are mutually exclusive by
// construction, not just by check ordering.
func isRuntimeTransitionInProgressError(err error) bool {
	var reqErr *requestError
	if !errors.As(err, &reqErr) {
		return false
	}
	if reqErr.StatusCode != http.StatusForbidden {
		return false
	}
	return strings.Contains(strings.ToLower(reqErr.Body), "runtime_transition_in_progress")
}

func isInvalidDaemonTokenError(err error) bool {
	var reqErr *requestError
	if !errors.As(err, &reqErr) {
		return false
	}
	if reqErr.StatusCode != http.StatusUnauthorized {
		return false
	}
	return strings.Contains(strings.ToLower(reqErr.Body), "invalid daemon token")
}

// isRuntimeNotFoundError returns true if the error is a 404 with "runtime not
// found" body. The daemon uses this to detect that the runtime row was deleted
// server-side (UI Delete, 7-day offline GC) while the daemon was still
// heartbeating against the dead UUID, so it can prune the stale runtime from
// its local state and re-register instead of looping on the dead ID forever.
//
// Server-side, this body is paired with pgx.ErrNoRows specifically (other DB
// errors return 500), so a transient DB hiccup cannot make the daemon
// self-cleanup.
func isRuntimeNotFoundError(err error) bool {
	var reqErr *requestError
	if !errors.As(err, &reqErr) {
		return false
	}
	if reqErr.StatusCode != http.StatusNotFound {
		return false
	}
	return strings.Contains(strings.ToLower(reqErr.Body), "runtime not found")
}

// Client handles HTTP communication with the Multica server daemon API.
type Client struct {
	baseURL string
	client  *http.Client

	tokenMu         sync.RWMutex
	token           string
	workspaceTokens map[string]daemonAuthToken
	runtimeTokens   map[string]daemonAuthToken

	// Identity headers sent on every request as X-Client-*. Populated by
	// SetIdentity(); empty values are simply omitted.
	platform string
	version  string
	os       string
}

// NewClient creates a new daemon API client.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL:         baseURL,
		client:          &http.Client{Timeout: 30 * time.Second},
		workspaceTokens: make(map[string]daemonAuthToken),
		runtimeTokens:   make(map[string]daemonAuthToken),
		platform:        "daemon",
		os:              normalizeGOOS(runtime.GOOS),
	}
}

// normalizeGOOS maps Go's runtime.GOOS values to the protocol vocabulary
// used by X-Client-OS / client_os ("macos" / "windows" / "linux").
func normalizeGOOS(goos string) string {
	switch goos {
	case "darwin":
		return "macos"
	case "windows":
		return "windows"
	case "linux":
		return "linux"
	default:
		return goos
	}
}

// SetVersion records the daemon's CLI version, sent as X-Client-Version.
// Called by Daemon.Run after config is loaded.
func (c *Client) SetVersion(v string) {
	c.version = v
}

// setIdentityHeaders attaches X-Client-Platform/Version/OS to req when set.
func (c *Client) setIdentityHeaders(req *http.Request) {
	if c.platform != "" {
		req.Header.Set("X-Client-Platform", c.platform)
	}
	if c.version != "" {
		req.Header.Set("X-Client-Version", c.version)
	}
	if c.os != "" {
		req.Header.Set("X-Client-OS", c.os)
	}
}

// SetToken sets the auth token for authenticated requests.
func (c *Client) SetToken(token string) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	c.token = token
}

// Token returns the current auth token.
func (c *Client) Token() string {
	c.tokenMu.RLock()
	defer c.tokenMu.RUnlock()
	return c.token
}

type daemonAuthToken struct {
	token     string
	expiresAt time.Time
}

const daemonAuthTokenRefreshWindow = time.Hour

func (tok daemonAuthToken) available(now time.Time) bool {
	return tok.token != "" && !tok.expiresAt.IsZero() && now.Before(tok.expiresAt)
}

func (c *Client) SetWorkspaceDaemonToken(workspaceID, token string, expiresAt time.Time) {
	if workspaceID == "" || token == "" {
		return
	}
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	c.workspaceTokens[workspaceID] = daemonAuthToken{token: token, expiresAt: expiresAt}
}

func (c *Client) SetRuntimeDaemonToken(runtimeID, token string, expiresAt time.Time) {
	if runtimeID == "" || token == "" {
		return
	}
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	c.runtimeTokens[runtimeID] = daemonAuthToken{token: token, expiresAt: expiresAt}
}

func (c *Client) ClearWorkspaceDaemonToken(workspaceID string) {
	if workspaceID == "" {
		return
	}
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	delete(c.workspaceTokens, workspaceID)
}

func (c *Client) ClearRuntimeDaemonToken(runtimeID string) {
	if runtimeID == "" {
		return
	}
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	delete(c.runtimeTokens, runtimeID)
}

func (c *Client) WorkspaceDaemonTokenNeedsRefresh(workspaceID string, now time.Time) bool {
	c.tokenMu.RLock()
	defer c.tokenMu.RUnlock()
	tok, ok := c.workspaceTokens[workspaceID]
	if !ok || tok.token == "" {
		return false
	}
	if tok.expiresAt.IsZero() {
		return true
	}
	return !now.Add(daemonAuthTokenRefreshWindow).Before(tok.expiresAt)
}

func (c *Client) WorkspaceDaemonTokenAvailable(workspaceID string, now time.Time) bool {
	c.tokenMu.RLock()
	defer c.tokenMu.RUnlock()
	tok, ok := c.workspaceTokens[workspaceID]
	if !ok {
		return false
	}
	return tok.available(now)
}

func (c *Client) tokenSnapshot() string {
	c.tokenMu.RLock()
	defer c.tokenMu.RUnlock()
	return c.token
}

func (c *Client) tokenForWorkspace(workspaceID string) string {
	c.tokenMu.RLock()
	defer c.tokenMu.RUnlock()
	if tok, ok := c.workspaceTokens[workspaceID]; ok && tok.available(time.Now()) {
		return tok.token
	}
	return c.token
}

func (c *Client) tokenForRuntime(runtimeID string) string {
	c.tokenMu.RLock()
	defer c.tokenMu.RUnlock()
	if tok, ok := c.runtimeTokens[runtimeID]; ok && tok.available(time.Now()) {
		return tok.token
	}
	return c.token
}

type AgentInboxEvent struct {
	ID               string `json:"id"`
	DeliveryID       string `json:"delivery_id"`
	LeaseToken       string `json:"lease_token"`
	LeaseExpiresAt   string `json:"lease_expires_at"`
	SeqTo            int64  `json:"seq_to"`
	Reason           string `json:"reason"`
	DeliveryMode     string `json:"delivery_mode"`
	ResponseMode     string `json:"response_mode"`
	ExecutionProfile string `json:"execution_profile"`
	RequiresWake     bool   `json:"requires_wake"`
	Task             *Task  `json:"task,omitempty"`
	RuntimeID        string `json:"-"`
}

func (c *Client) DrainAgentInbox(ctx context.Context, runtimeID string) (*AgentInboxEvent, error) {
	var resp struct {
		Events []AgentInboxEvent `json:"events"`
	}
	if err := c.postJSONWithToken(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/agent-inbox/drain", runtimeID), map[string]any{}, &resp, c.tokenForRuntime(runtimeID)); err != nil {
		return nil, err
	}
	if len(resp.Events) == 0 {
		return nil, nil
	}
	resp.Events[0].RuntimeID = runtimeID
	return &resp.Events[0], nil
}

type AgentCredentialResponse struct {
	ID             string  `json:"id"`
	AgentID        string  `json:"agent_id"`
	Prefix         string  `json:"token_prefix"`
	ExpiresAt      *string `json:"expires_at"`
	Token          string  `json:"token"`
	Reused         bool    `json:"reused"`
	RotationReason string  `json:"rotation_reason"`
}

func (c *Client) EnsureAgentCredential(ctx context.Context, runtimeID, agentID, credentialID string) (*AgentCredentialResponse, error) {
	var resp AgentCredentialResponse
	body := map[string]any{}
	if credentialID != "" {
		body["credential_id"] = credentialID
	}
	if err := c.postJSONWithToken(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/agents/%s/credential", runtimeID, agentID), body, &resp, c.tokenForRuntime(runtimeID)); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) ReportProgress(ctx context.Context, taskID, summary string, step, total int) error {
	return c.postJSON(ctx, fmt.Sprintf("/api/daemon/tasks/%s/progress", taskID), map[string]any{
		"summary": summary,
		"step":    step,
		"total":   total,
	}, nil)
}

// TaskMessageData represents a single agent execution message for batch reporting.
type TaskMessageData struct {
	Seq     int            `json:"seq"`
	Type    string         `json:"type"`
	Tool    string         `json:"tool,omitempty"`
	CallID  string         `json:"call_id,omitempty"`
	Content string         `json:"content,omitempty"`
	Lineage string         `json:"lineage,omitempty"`
	Input   map[string]any `json:"input,omitempty"`
	Output  string         `json:"output,omitempty"`
}

func (c *Client) ReportTaskMessages(ctx context.Context, taskID string, messages []TaskMessageData) error {
	return c.postJSON(ctx, fmt.Sprintf("/api/daemon/tasks/%s/messages", taskID), map[string]any{
		"messages": messages,
	}, nil)
}

func (c *Client) ReportAgentInboxMessages(ctx context.Context, lease AgentInboxLease, messages []TaskMessageData) error {
	if len(messages) == 0 {
		return nil
	}
	return c.postJSONWithToken(ctx, fmt.Sprintf("/api/daemon/agent-inbox/events/%s/messages", lease.ID), map[string]any{
		"delivery_id": lease.DeliveryID,
		"lease_token": lease.LeaseToken,
		"messages":    messages,
	}, nil, c.tokenForRuntime(lease.RuntimeID))
}

type AgentInboxCompletionReceipt struct {
	OK              bool   `json:"ok"`
	AckedSeq        int64  `json:"acked_seq"`
	TerminalOutcome string `json:"terminal_outcome"`
	ResumeUnsafe    bool   `json:"resume_unsafe"`
}

func (c *Client) CompleteAgentInboxEvent(ctx context.Context, lease AgentInboxLease, result TaskResult) (AgentInboxCompletionReceipt, error) {
	body := map[string]any{
		"delivery_id": lease.DeliveryID,
		"lease_token": lease.LeaseToken,
		"output":      result.Comment,
	}
	if result.ExecutionID != "" {
		body["execution_id"] = result.ExecutionID
	}
	if len(result.Usage) > 0 {
		body["usage"] = result.Usage
	}
	if result.Action != "" {
		body["action"] = result.Action
	}
	if result.Target != "" {
		body["target"] = result.Target
	}
	if result.Type != "" {
		body["type"] = result.Type
	}
	if len(result.Parts) > 0 {
		body["parts"] = result.Parts
	}
	if result.Reaction != nil {
		body["reaction"] = result.Reaction
	}
	if result.SessionID != "" {
		body["session_id"] = result.SessionID
	}
	if result.WorkDir != "" {
		body["work_dir"] = result.WorkDir
	}
	if result.OutputSuppressedReason != "" {
		body["output_suppressed_reason"] = result.OutputSuppressedReason
	}
	if result.ChannelOnboardingDecision != "" {
		body["channel_onboarding_decision"] = result.ChannelOnboardingDecision
	}
	if result.TransportAttempted {
		body["transport_attempted"] = true
	}
	if result.RuntimeStats != nil {
		body["runtime_stats"] = result.RuntimeStats
	}
	if len(result.InternalOutput) > 0 {
		body["internal_output"] = result.InternalOutput
	}
	var receipt AgentInboxCompletionReceipt
	err := c.postJSONWithRetryToken(ctx, fmt.Sprintf("/api/daemon/agent-inbox/events/%s/complete", lease.ID), body, &receipt, defaultTerminalRetrySchedule, c.tokenForRuntime(lease.RuntimeID))
	return receipt, err
}

// StartAgentInboxExecution persists the daemon-minted provider-run UUID before
// calling the provider. delivery_id remains only the active transport lease.
func (c *Client) StartAgentInboxExecution(ctx context.Context, lease AgentInboxLease, executionID string) error {
	return c.postJSONWithRetryToken(ctx, fmt.Sprintf("/api/daemon/agent-inbox/events/%s/execution", lease.ID), map[string]any{
		"delivery_id":  lease.DeliveryID,
		"lease_token":  lease.LeaseToken,
		"execution_id": executionID,
	}, nil, defaultTerminalRetrySchedule, c.tokenForRuntime(lease.RuntimeID))
}

func (c *Client) ReportAgentInboxUsage(ctx context.Context, lease AgentInboxLease, executionID string, usage []TaskUsageEntry) error {
	if len(usage) == 0 {
		return nil
	}
	return c.postJSONWithRetryToken(ctx, fmt.Sprintf("/api/daemon/agent-inbox/events/%s/usage", lease.ID), map[string]any{
		"delivery_id":  lease.DeliveryID,
		"lease_token":  lease.LeaseToken,
		"execution_id": executionID,
		"usage":        usage,
	}, nil, defaultTerminalRetrySchedule, c.tokenForRuntime(lease.RuntimeID))
}

func (c *Client) FailAgentInboxEvent(ctx context.Context, lease AgentInboxLease, errMsg, sessionID, workDir, failureReason, reasonCode string) error {
	body := map[string]any{
		"delivery_id": lease.DeliveryID,
		"lease_token": lease.LeaseToken,
		"error":       errMsg,
	}
	if sessionID != "" {
		body["session_id"] = sessionID
	}
	if workDir != "" {
		body["work_dir"] = workDir
	}
	if failureReason != "" {
		body["failure_reason"] = failureReason
	}
	if reasonCode != "" {
		body["reason_code"] = reasonCode
	}
	return c.postJSONWithRetryToken(ctx, fmt.Sprintf("/api/daemon/agent-inbox/events/%s/fail", lease.ID), body, nil, defaultTerminalRetrySchedule, c.tokenForRuntime(lease.RuntimeID))
}

func (c *Client) RenewAgentInboxEvent(ctx context.Context, lease AgentInboxLease) error {
	body := map[string]any{
		"delivery_id": lease.DeliveryID,
		"lease_token": lease.LeaseToken,
	}
	return c.postJSONWithToken(ctx, fmt.Sprintf("/api/daemon/agent-inbox/events/%s/renew", lease.ID), body, nil, c.tokenForRuntime(lease.RuntimeID))
}

func (c *Client) AckAgentInboxEvent(ctx context.Context, lease AgentInboxLease) error {
	body := map[string]any{
		"delivery_id":    lease.DeliveryID,
		"lease_token":    lease.LeaseToken,
		"seen_up_to_seq": lease.SeqTo,
	}
	return c.postJSONWithToken(ctx, fmt.Sprintf("/api/daemon/agent-inbox/events/%s/ack", lease.ID), body, nil, c.tokenForRuntime(lease.RuntimeID))
}

// PinTaskSession persists the provider CLI resume token (TEXT
// agent_inbox_event.session_id) and work_dir on the task row mid-flight so a
// daemon crash doesn't lose --resume. Not the Multica agent_session /
// agent_session_id inbox wake/drain UUID (task #109).
func (c *Client) PinTaskSession(ctx context.Context, taskID, sessionID, workDir string) error {
	if sessionID == "" && workDir == "" {
		return nil
	}
	body := map[string]any{}
	if sessionID != "" {
		body["session_id"] = sessionID
	}
	if workDir != "" {
		body["work_dir"] = workDir
	}
	return c.postJSON(ctx, fmt.Sprintf("/api/daemon/tasks/%s/session", taskID), body, nil)
}

// RecoverOrphans tells the server to fail any dispatched/running tasks the
// previous daemon process for this runtime left behind. The server will
// auto-retry eligible tasks.
func (c *Client) RecoverOrphans(ctx context.Context, runtimeID string) error {
	return c.postJSONWithToken(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/recover-orphans", runtimeID), map[string]any{}, nil, c.tokenForRuntime(runtimeID))
}

// GetTaskStatus returns the current status of a task. Used by the daemon to
// detect terminal/interruption signals (cancelled, failed, completed, or a
// 404 task-not-found) while a task is executing.
func (c *Client) GetTaskStatus(ctx context.Context, taskID string) (string, error) {
	var resp struct {
		Status string `json:"status"`
	}
	if err := c.getJSON(ctx, fmt.Sprintf("/api/daemon/tasks/%s/status", taskID), &resp); err != nil {
		return "", err
	}
	return resp.Status, nil
}

// HeartbeatResponse, PendingUpdate, etc. alias the wire types so HTTP and WS
// heartbeat paths share a single type and a single decoder shape. Aliases
// (rather than wrappers) keep call sites unchanged.
type (
	HeartbeatResponse       = protocol.DaemonHeartbeatAckPayload
	PendingUpdate           = protocol.DaemonHeartbeatPendingUpdate
	PendingMachineUpgrade   = protocol.DaemonHeartbeatPendingMachineUpgrade
	PendingModelList        = protocol.DaemonHeartbeatPendingModelList
	PendingLocalSkills      = protocol.DaemonHeartbeatPendingLocalSkills
	PendingLocalSkillImport = protocol.DaemonHeartbeatPendingLocalSkillImport
	PendingMemoryCuration   = protocol.DaemonHeartbeatPendingMemoryCuration
	PendingRestart          = protocol.DaemonHeartbeatPendingRestart
	PendingAgentStartIntent = protocol.DaemonHeartbeatPendingAgentStartIntent
)

func (c *Client) SendHeartbeat(
	ctx context.Context,
	runtimeID, activeMemoryCurationRunID string,
	updateObservation *protocol.DaemonUpdateObservation,
) (*HeartbeatResponse, error) {
	var resp HeartbeatResponse
	if err := c.postJSONWithToken(ctx, "/api/daemon/heartbeat", map[string]any{
		"runtime_id":                    runtimeID,
		"supports_batch_import":         true,
		"supports_memory_curation":      true,
		"active_memory_curation_run_id": activeMemoryCurationRunID,
		"auto_update":                   updateObservation,
	}, &resp, c.tokenForRuntime(runtimeID)); err != nil {
		return nil, err
	}
	return &resp, nil
}

// MachineUpgradeReceipt is the immutable server acceptance snapshot needed by
// the daemon before any local release mutation.
type MachineUpgradeReceipt struct {
	ID                 string   `json:"id"`
	RequestedTarget    string   `json:"requested_target"`
	ResolvedTarget     *string  `json:"resolved_target,omitempty"`
	Phase              string   `json:"phase"`
	AcceptedGeneration *string  `json:"accepted_generation,omitempty"`
	AcceptedRuntimeIDs []string `json:"accepted_runtime_ids,omitempty"`
}

// MachineUpgradeControlOperation is the minimal canonical operation receipt
// needed by the owner-only local control surface. It intentionally does not
// duplicate handler types across the daemon package boundary.
type MachineUpgradeControlOperation struct {
	ID              string `json:"id"`
	DaemonID        string `json:"daemon_id"`
	RequestedTarget string `json:"requested_target"`
	Phase           string `json:"phase"`
}

func (c *Client) CreateMachineUpgrade(ctx context.Context, workspaceID, daemonID, requestID, targetVersion string) (*MachineUpgradeControlOperation, error) {
	var operation MachineUpgradeControlOperation
	err := c.postJSONWithTokenAndHeaders(ctx, fmt.Sprintf("/api/daemons/%s/upgrades", daemonID), map[string]string{
		"request_id":     requestID,
		"target_version": targetVersion,
	}, &operation, c.tokenSnapshot(), map[string]string{"X-Workspace-ID": workspaceID})
	if err != nil {
		return nil, err
	}
	return &operation, nil
}

// AcceptMachineUpgrade records that this daemon process accepted the claimed
// machine operation. The following full registration is the attestation; the
// server will not complete the operation until every captured sibling does so.
func (c *Client) AcceptMachineUpgrade(ctx context.Context, runtimeID, upgradeID, generationID, cliVersion, resolvedTarget string) (*MachineUpgradeReceipt, error) {
	var receipt MachineUpgradeReceipt
	err := c.postJSONWithToken(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/machine-upgrades/%s/accept", runtimeID, upgradeID), map[string]any{
		"generation_id":   generationID,
		"cli_version":     cliVersion,
		"resolved_target": resolvedTarget,
	}, &receipt, c.tokenForRuntime(runtimeID))
	if err != nil {
		return nil, err
	}
	return &receipt, nil
}

func (c *Client) ReportMachineUpgradeProgress(ctx context.Context, runtimeID, upgradeID, phase, errorCode, errorMessage string) error {
	return c.postJSONWithToken(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/machine-upgrades/%s/progress", runtimeID, upgradeID), map[string]any{
		"phase": phase, "error_code": errorCode, "error_message": errorMessage,
	}, nil, c.tokenForRuntime(runtimeID))
}

func (c *Client) ReportMachineUpgradeRollback(ctx context.Context, runtimeID, upgradeID, generation, errorCode, errorMessage string) error {
	return c.postJSONWithToken(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/machine-upgrades/%s/progress", runtimeID, upgradeID), map[string]any{
		"phase":         "rollback_pending",
		"generation_id": generation,
		"error_code":    errorCode,
		"error_message": errorMessage,
	}, nil, c.tokenForRuntime(runtimeID))
}

// ReportUpdateResult sends the CLI update result back to the server.
func (c *Client) ReportUpdateResult(ctx context.Context, runtimeID, updateID string, result map[string]any) error {
	return c.postJSONWithToken(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/update/%s/result", runtimeID, updateID), result, nil, c.tokenForRuntime(runtimeID))
}

// ReportModelListResult sends the model-discovery result back to the server.
func (c *Client) ReportModelListResult(ctx context.Context, runtimeID, requestID string, result map[string]any) error {
	return c.postJSONWithToken(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/models/%s/result", runtimeID, requestID), result, nil, c.tokenForRuntime(runtimeID))
}

// ReportLocalSkillListResult sends the runtime-local-skill inventory back to the server.
func (c *Client) ReportLocalSkillListResult(ctx context.Context, runtimeID, requestID string, result map[string]any) error {
	return c.postJSONWithToken(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/local-skills/%s/result", runtimeID, requestID), result, nil, c.tokenForRuntime(runtimeID))
}

// ReportLocalSkillImportResult sends a runtime-local-skill bundle back to the server.
func (c *Client) ReportLocalSkillImportResult(ctx context.Context, runtimeID, requestID string, result map[string]any) error {
	return c.postJSONWithToken(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/local-skills/import/%s/result", runtimeID, requestID), result, nil, c.tokenForRuntime(runtimeID))
}

func (c *Client) ReportMemoryCurationResult(ctx context.Context, runtimeID, runID string, result map[string]any) error {
	return c.postJSONWithToken(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/memory-curation/%s/result", runtimeID, runID), result, nil, c.tokenForRuntime(runtimeID))
}

// ReportAgentLifecycleOperationResult sends the outcome of an agent lifecycle
// operation (task #52) back to the server so the operation record transitions
// out of running/scheduled into succeeded/failed.
func (c *Client) ReportAgentLifecycleOperationResult(ctx context.Context, runtimeID, operationID string, result map[string]any) error {
	return c.postJSONWithToken(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/agent-lifecycle/%s/result", runtimeID, operationID), result, nil, c.tokenForRuntime(runtimeID))
}

// ReportAgentStartIntent reports a Computer acceptance or a later runtime
// observation for a durable first-start delivery. Replays use the same
// dispatch id and lifecycle sequence, so a lost HTTP response is safe.
func (c *Client) ReportAgentStartIntent(ctx context.Context, runtimeID, startDispatchID string, result map[string]any) error {
	return c.postJSONWithToken(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/agent-start-intents/%s/report", runtimeID, startDispatchID), result, nil, c.tokenForRuntime(runtimeID))
}

// ReportAgentProviderCrashed tells the server an idle resident provider
// process for this agent was found dead (task #42② / Raft status ②).
// Best-effort: callers should log and continue on error.
func (c *Client) ReportAgentProviderCrashed(ctx context.Context, runtimeID, agentID string) error {
	return c.postJSONWithToken(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/agents/%s/crashed", runtimeID, agentID), map[string]any{}, nil, c.tokenForRuntime(runtimeID))
}

// ClearAgentProviderCrashed clears a prior ReportAgentProviderCrashed after
// the resident provider was successfully recreated or a lifecycle restart
// succeeded. Best-effort: callers should log and continue on error.
func (c *Client) ClearAgentProviderCrashed(ctx context.Context, runtimeID, agentID string) error {
	return c.postJSONWithToken(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/agents/%s/crashed/clear", runtimeID, agentID), map[string]any{}, nil, c.tokenForRuntime(runtimeID))
}

func (c *Client) SyncSharedSkills(ctx context.Context, runtimeID string, payload SharedSkillSyncPayload) (*SharedSkillSyncResult, error) {
	var result SharedSkillSyncResult
	if err := c.postJSONWithToken(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/shared-skills/sync", runtimeID), payload, &result, c.tokenForRuntime(runtimeID)); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) SyncEvolutionSubmissions(ctx context.Context, runtimeID string, payload EvolutionSubmissionSyncPayload) (*SharedSkillSyncResult, error) {
	var result SharedSkillSyncResult
	if err := c.postJSONWithToken(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/evolution/submissions", runtimeID), payload, &result, c.tokenForRuntime(runtimeID)); err != nil {
		return nil, err
	}
	return &result, nil
}

// WorkspaceInfo holds minimal workspace metadata returned by the API.
type WorkspaceInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// RenewTokenResponse mirrors handler.RenewPATResponse — kept loose (string +
// bool) because the daemon never parses the timestamp itself; it just logs it
// for operator visibility.
type RenewTokenResponse struct {
	ExpiresAt string `json:"expires_at"`
	Renewed   bool   `json:"renewed"`
}

// RenewToken asks the server to extend the daemon's current PAT in place when
// it's within the server-side renewal window. The server is authoritative on
// the threshold — the daemon doesn't know the token's expires_at locally —
// so this is safe to call on any cadence; the only thing extra calls cost is
// one round trip and one cheap SELECT.
func (c *Client) RenewToken(ctx context.Context) (*RenewTokenResponse, error) {
	var resp RenewTokenResponse
	if err := c.postJSON(ctx, "/api/tokens/current/renew", map[string]any{}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListWorkspaces fetches all workspaces the authenticated user belongs to.
func (c *Client) ListWorkspaces(ctx context.Context) ([]WorkspaceInfo, error) {
	var workspaces []WorkspaceInfo
	if err := c.getJSON(ctx, "/api/workspaces", &workspaces); err != nil {
		return nil, err
	}
	return workspaces, nil
}

func (c *Client) Deregister(ctx context.Context, runtimeIDs []string) error {
	return c.postJSON(ctx, "/api/daemon/deregister", map[string]any{
		"runtime_ids": runtimeIDs,
	}, nil)
}

// MarkStarting tells the server this daemon is up and about to probe its
// agent CLIs' versions (a step that can take ~20s on a cold cache), so any
// runtime rows already registered under this daemon_id can show "starting"
// instead of whatever they looked like before this process started. It is a
// no-op on the server for a daemon_id with no existing runtime rows (first
// registration ever), and best-effort by design — callers should log and
// continue on error rather than fail startup over it.
func (c *Client) MarkStarting(ctx context.Context, workspaceID, daemonID string) error {
	return c.postJSON(ctx, "/api/daemon/starting", map[string]any{
		"workspace_id": workspaceID,
		"daemon_id":    daemonID,
	}, nil)
}

// RegisterResponse holds the server's response to a daemon registration.
type RegisterResponse struct {
	Runtimes             []Runtime       `json:"runtimes"`
	Settings             json.RawMessage `json:"settings,omitempty"`
	DaemonToken          string          `json:"daemon_token,omitempty"`
	DaemonTokenExpiresAt string          `json:"daemon_token_expires_at,omitempty"`
	ServerCapabilities   []string        `json:"server_capabilities,omitempty"`
}

func (c *Client) Register(ctx context.Context, req map[string]any) (*RegisterResponse, error) {
	return c.RegisterForWorkspace(ctx, "", req)
}

func (c *Client) RegisterForWorkspace(ctx context.Context, workspaceID string, req map[string]any) (*RegisterResponse, error) {
	var resp RegisterResponse
	if err := c.postJSONWithToken(ctx, "/api/daemon/register", req, &resp, c.tokenForWorkspace(workspaceID)); err != nil {
		return nil, err
	}
	return &resp, nil
}

// defaultTerminalRetrySchedule is the backoff used by postJSONWithRetry for
// terminal task callbacks (CompleteTask / FailTask). N entries → N+1 attempts
// in the worst case (one immediate + N retries). Five backoffs totalling
// 124s is wide enough to ride out the short upstream blips we've seen
// (MUL-2780) without leaving the task stuck if the outage outlives the
// window.
var defaultTerminalRetrySchedule = []time.Duration{
	4 * time.Second,
	8 * time.Second,
	16 * time.Second,
	32 * time.Second,
	64 * time.Second,
}

// retrySleep is the sleep used between retry attempts. Pulled into a package
// variable so tests can swap in an instant sleep without rewriting the
// caller's schedule.
var retrySleep = func(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// isTransientError reports whether err looks like a hiccup that's likely to
// resolve on retry: connection / TLS / I/O errors at the transport layer
// (including client timeouts surfacing as context.DeadlineExceeded inside
// http.Client.Do), 5xx server responses, and 408/429 rate-limit-style 4xx
// codes. Other 4xx codes are treated as permanent — retrying a 400 (bad
// body) or 404 (task not found) only burns time.
//
// The caller is responsible for separately bailing on parent-context
// cancellation; this predicate cannot distinguish "the daemon is shutting
// down" from "the HTTP client timed out a single attempt" because both
// reach here as context errors wrapped by net/http.
func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	var reqErr *requestError
	if errors.As(err, &reqErr) {
		if reqErr.StatusCode >= 500 {
			return true
		}
		if reqErr.StatusCode == http.StatusRequestTimeout || reqErr.StatusCode == http.StatusTooManyRequests {
			return true
		}
		return false
	}
	return true
}

// postJSONWithRetry posts a JSON body with bounded exponential backoff,
// intended for "must reach the server" terminal callbacks (CompleteTask /
// FailTask). It retries transient errors per isTransientError and stops
// immediately on permanent 4xx responses so we don't burn the schedule on
// requests the server has already rejected.
//
// schedule controls the sleeps between attempts. With N entries the helper
// performs N+1 attempts in the worst case (one initial + N retries). The
// returned error is the last response from the server, so callers can still
// inspect it with isTransientError to decide whether to fall back to a
// different terminal call (e.g. complete → fail on permanent error only).
//
// The server-side CompleteTask / FailTask treat "already terminal" as an
// idempotent success (see service/task.go), so a duplicate replay from a
// retry is safe even if the server's prior response was lost in transit.
func (c *Client) postJSONWithRetry(ctx context.Context, path string, reqBody any, respBody any, schedule []time.Duration) error {
	return c.postJSONWithRetryToken(ctx, path, reqBody, respBody, schedule, c.tokenSnapshot())
}

func (c *Client) postJSONWithRetryToken(ctx context.Context, path string, reqBody any, respBody any, schedule []time.Duration, token string) error {
	var lastErr error
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return lastErr
			}
			return err
		}
		err := c.postJSONWithToken(ctx, path, reqBody, respBody, token)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isTransientError(err) {
			return err
		}
		if attempt >= len(schedule) {
			return err
		}
		if sleepErr := retrySleep(ctx, schedule[attempt]); sleepErr != nil {
			return err
		}
	}
}

func (c *Client) postJSON(ctx context.Context, path string, reqBody any, respBody any) error {
	return c.postJSONWithToken(ctx, path, reqBody, respBody, c.tokenSnapshot())
}

func (c *Client) postJSONWithToken(ctx context.Context, path string, reqBody any, respBody any, token string) error {
	return c.postJSONWithTokenAndHeaders(ctx, path, reqBody, respBody, token, nil)
}

func (c *Client) postJSONWithTokenAndHeaders(ctx context.Context, path string, reqBody any, respBody any, token string, headers map[string]string) error {
	var body io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for name, value := range headers {
		if value != "" {
			req.Header.Set(name, value)
		}
	}
	c.setIdentityHeaders(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &requestError{Method: http.MethodPost, Path: path, StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	if respBody == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(respBody)
}

func (c *Client) getJSON(ctx context.Context, path string, respBody any) error {
	return c.getJSONWithToken(ctx, path, respBody, c.tokenSnapshot())
}

func (c *Client) getJSONWithToken(ctx context.Context, path string, respBody any, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	c.setIdentityHeaders(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &requestError{Method: http.MethodGet, Path: path, StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	if respBody == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(respBody)
}
