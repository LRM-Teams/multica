package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type AgentLifecycleActionKind string

const (
	agentLifecycleRestart             AgentLifecycleActionKind = "restart"
	agentLifecycleResetSessionRestart AgentLifecycleActionKind = "reset_session_restart"
	agentLifecycleFullResetRestart    AgentLifecycleActionKind = "full_reset_restart"
)

const (
	agentLifecycleScheduled = "scheduled"
	agentLifecycleRunning   = "running"
	agentLifecycleSucceeded = "succeeded"
	agentLifecycleFailed    = "failed"

	agentLifecycleImmediate       = "immediate"
	agentLifecycleAfterCurrentRun = "after_current_run"

	// agentLifecycleSucceededActivityEventType is the single user-facing
	// Activity timeline row written when a lifecycle op finishes successfully
	// (Parker 2026-08-02: Frank wants "I clicked restart" visible; only the
	// success fact — not a running+succeeded pair). Must stay on the
	// /activity/events allowlist and FE isNarrativeActivityEvent allowlist.
	agentLifecycleSucceededActivityEventType = "agent_lifecycle_succeeded"
)

type AgentLifecycleOperation struct {
	ID            string                   `json:"id"`
	AgentID       string                   `json:"agent_id"`
	RuntimeID     *string                  `json:"runtime_id"`
	ActionKind    AgentLifecycleActionKind `json:"action_kind"`
	Status        string                   `json:"status"`
	ExecutionMode string                   `json:"execution_mode"`
	Step          string                   `json:"step,omitempty"`
	ReasonCode    string                   `json:"reason_code,omitempty"`
	CreatedAt     string                   `json:"created_at"`
	StartedAt     *string                  `json:"started_at"`
	FinishedAt    *string                  `json:"finished_at"`
}

type AgentLifecyclePreflight struct {
	Actions         map[AgentLifecycleActionKind]AgentLifecycleActionPreflight `json:"actions"`
	ActiveOperation *AgentLifecycleOperation                                   `json:"active_operation"`
	// ProviderCapabilities is the FE-facing projection of
	// agent.ProviderCapabilities for this agent's runtime provider.
	// Gate the restart button on provider_capabilities.force_restart — do
	// not hardcode a provider allow-list. Older servers omit the object;
	// treat missing as all-false.
	ProviderCapabilities ProviderCapabilitiesWire `json:"provider_capabilities"`
}

type AgentLifecycleActionPreflight struct {
	Supported      bool   `json:"supported"`
	DisabledReason string `json:"disabled_reason,omitempty"`
	ExecutionMode  string `json:"execution_mode"`
}

type createAgentLifecycleRequest struct {
	ActionKind AgentLifecycleActionKind `json:"action_kind"`
}

// GetAgentLifecycle returns the server-authoritative capability and scheduling
// mode for the lifecycle dialog. Clients must not infer active/idle from the
// presentation-oriented agent status field.
func (h *Handler) GetAgentLifecycle(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentLifecycleTarget(w, r)
	if !ok {
		return
	}
	preflight, err := h.agentLifecyclePreflight(r.Context(), agent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent lifecycle state")
		return
	}
	writeJSON(w, http.StatusOK, preflight)
}

// CreateAgentLifecycleOperation records one idempotent restart/reset request.
// The client supplies no runtime or filesystem path. Those bindings are always
// resolved from the locked agent row by the server.
func (h *Handler) CreateAgentLifecycleOperation(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentLifecycleTarget(w, r)
	if !ok {
		return
	}
	idempotencyKey, ok := parseLifecycleIdempotencyKey(w, r)
	if !ok {
		return
	}
	var req createAgentLifecycleRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !validAgentLifecycleAction(req.ActionKind) {
		writeError(w, http.StatusBadRequest, "invalid action_kind")
		return
	}
	if h.TxStarter == nil {
		writeError(w, http.StatusServiceUnavailable, "agent lifecycle is unavailable")
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create agent lifecycle operation")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()

	var lockedAgent db.Agent
	lockedAgent, err = scanAgentForLifecycle(tx.QueryRow(r.Context(), `
		SELECT id, workspace_id, runtime_id, owner_id
		FROM agent
		WHERE id = $1
		  AND archived_at IS NULL
		FOR UPDATE
	`, agent.ID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "agent not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create agent lifecycle operation")
		return
	}

	existing, err := getAgentLifecycleOperationByKey(r.Context(), tx, lockedAgent.ID, idempotencyKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create agent lifecycle operation")
		return
	}
	if existing != nil {
		if existing.ActionKind != req.ActionKind {
			writeError(w, http.StatusConflict, "Idempotency-Key is already bound to another operation")
			return
		}
		writeJSON(w, http.StatusAccepted, existing)
		return
	}

	runtime, supported, reason, err := h.agentLifecycleRuntimeSupport(r.Context(), lockedAgent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create agent lifecycle operation")
		return
	}
	if !supported {
		writeError(w, http.StatusConflict, reason)
		return
	}

	active, err := agentLifecycleHasActiveRun(r.Context(), tx, lockedAgent.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create agent lifecycle operation")
		return
	}
	if active && req.ActionKind == agentLifecycleFullResetRestart {
		writeError(w, http.StatusConflict, "agent_active")
		return
	}
	executionMode := agentLifecycleImmediate
	status := agentLifecycleRunning
	var startedAt any = time.Now()
	// Plain restart (task #62) always goes immediate, even while active: a
	// busy agent is exactly the stuck-agent case restart exists to recover,
	// and "scheduled" never fires on its own once created (there is no
	// trigger that promotes it once the blocking turn ends — see the design
	// doc). reset_session_restart and full_reset_restart keep the existing
	// scheduled/rejected behavior below; extending force-interrupt to them
	// is separate, tracked work, not done here.
	if active && req.ActionKind != agentLifecycleRestart {
		executionMode = agentLifecycleAfterCurrentRun
		status = agentLifecycleScheduled
		startedAt = nil
	}
	actorID, ok := parseUUIDOrBadRequest(w, requestUserID(r), "user_id")
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	op, err := scanAgentLifecycleOperation(tx.QueryRow(r.Context(), `
		INSERT INTO agent_lifecycle_operation (
			workspace_id, agent_id, runtime_id, actor_user_id,
			idempotency_key, action_kind, status, execution_mode,
			step, started_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING
			id, agent_id, runtime_id, action_kind, status, execution_mode,
			step, reason_code, created_at, started_at, finished_at
	`, lockedAgent.WorkspaceID, lockedAgent.ID, runtime.ID, actorID,
		idempotencyKey, string(req.ActionKind), status, executionMode,
		agentLifecycleInitialStep(status), startedAt))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeError(w, http.StatusConflict, "an agent lifecycle operation is already active")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create agent lifecycle operation")
		return
	}

	// Do not write a create-time Activity row. /activity/events excludes
	// event_kind=system, and a "running" flash for a ~seconds restart is
	// noise (Parker: one success row only). Success is recorded in
	// ReportAgentLifecycleOperationResult.
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create agent lifecycle operation")
		return
	}
	// Dispatch only the immediate case here. A "scheduled" (after_current_run)
	// operation still has no trigger that dispatches it once the blocking
	// turn ends — a known, pre-existing gap (see the package doc comment in
	// agent_lifecycle_dispatch.go), not something this handler silently
	// drops: it simply doesn't claim to solve it yet.
	if status == agentLifecycleRunning && h.AgentLifecycleDispatchStore != nil {
		if _, err := h.AgentLifecycleDispatchStore.Create(
			r.Context(), op.ID, uuidToString(lockedAgent.ID), uuidToString(runtime.ID),
			uuidToString(lockedAgent.WorkspaceID), string(req.ActionKind),
		); err != nil {
			// The operation row is already committed; a dispatch failure
			// here must not roll that back or the client sees a phantom
			// "running" operation nobody will ever act on. Log-and-continue:
			// the operation sits at status=running until GetAgentLifecycleOperation
			// polling eventually shows it stuck, which is visible, not silent.
			slog.Warn("failed to create agent lifecycle dispatch", "operation_id", op.ID, "error", err)
		}
	}
	writeJSON(w, http.StatusAccepted, op)
}

func (h *Handler) GetAgentLifecycleOperation(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentLifecycleTarget(w, r)
	if !ok {
		return
	}
	operationID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "operationId"), "operation_id")
	if !ok {
		return
	}
	op, err := getAgentLifecycleOperation(r.Context(), h.DB, agent.ID, operationID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent lifecycle operation")
		return
	}
	if op == nil {
		writeError(w, http.StatusNotFound, "agent lifecycle operation not found")
		return
	}
	writeJSON(w, http.StatusOK, op)
}

func (h *Handler) loadAgentLifecycleTarget(w http.ResponseWriter, r *http.Request) (db.Agent, bool) {
	agentID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "agent_id")
	if !ok {
		return db.Agent{}, false
	}
	agent, err := h.Queries.GetAgent(r.Context(), agentID)
	if err != nil || agent.ArchivedAt.Valid {
		writeError(w, http.StatusNotFound, "agent not found")
		return db.Agent{}, false
	}
	if _, ok := h.requireWorkspaceRole(
		w, r, uuidToString(agent.WorkspaceID), "agent not found", "owner", "admin",
	); !ok {
		return db.Agent{}, false
	}
	return agent, true
}

func (h *Handler) agentLifecyclePreflight(ctx context.Context, target db.Agent) (AgentLifecyclePreflight, error) {
	runtime, supported, reason, err := h.agentLifecycleRuntimeSupport(ctx, target)
	if err != nil {
		return AgentLifecyclePreflight{}, err
	}
	active, err := agentLifecycleHasActiveRun(ctx, h.DB, target.ID)
	if err != nil {
		return AgentLifecyclePreflight{}, err
	}
	mode := agentLifecycleImmediate
	if active {
		mode = agentLifecycleAfterCurrentRun
	}
	activeOperation, err := getActiveAgentLifecycleOperation(ctx, h.DB, target.ID)
	if err != nil {
		return AgentLifecyclePreflight{}, err
	}
	actions := make(map[AgentLifecycleActionKind]AgentLifecycleActionPreflight, 3)
	for _, action := range []AgentLifecycleActionKind{
		agentLifecycleRestart,
		agentLifecycleResetSessionRestart,
		agentLifecycleFullResetRestart,
	} {
		actionSupported := supported
		actionReason := reason
		actionMode := mode
		if action == agentLifecycleFullResetRestart && active {
			actionSupported = false
			actionReason = "agent_active"
			actionMode = agentLifecycleImmediate
		}
		// Task #62: plain restart bypasses the busy-check entirely
		// (CreateAgentLifecycleOperation always dispatches it immediately,
		// even on an active agent) — the preview must match that, not the
		// shared "mode" default, or the confirmation dialog tells the user
		// their click will queue behind the current run when it will
		// actually interrupt it right away.
		if action == agentLifecycleRestart {
			actionMode = agentLifecycleImmediate
		}
		actions[action] = AgentLifecycleActionPreflight{
			Supported:      actionSupported,
			DisabledReason: actionReason,
			ExecutionMode:  actionMode,
		}
	}
	return AgentLifecyclePreflight{
		Actions:              actions,
		ActiveOperation:      activeOperation,
		ProviderCapabilities: providerCapabilitiesWire(runtime.Provider),
	}, nil
}

func (h *Handler) agentLifecycleRuntimeSupport(ctx context.Context, agent db.Agent) (db.AgentRuntime, bool, string, error) {
	if !agent.RuntimeID.Valid {
		return db.AgentRuntime{}, false, "agent_runtime_missing", nil
	}
	runtime, err := h.Queries.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{
		ID:          agent.RuntimeID,
		WorkspaceID: agent.WorkspaceID,
	})
	if err != nil {
		if isNotFound(err) {
			return db.AgentRuntime{}, false, "agent_runtime_missing", nil
		}
		return db.AgentRuntime{}, false, "", err
	}
	if runtime.Status != "online" ||
		!runtime.LastSeenAt.Valid ||
		time.Since(runtime.LastSeenAt.Time) >= agentHealthStaleThreshold {
		return runtime, false, "agent_runtime_offline", nil
	}
	if !agentLifecycleCapabilityPresent(runtimeCapabilities(runtimeMetadata(runtime))) {
		return runtime, false, "unsupported_runtime_capability", nil
	}
	return runtime, true, "", nil
}

func agentLifecycleHasActiveRun(ctx context.Context, exec dbExecutor, agentID pgtype.UUID) (bool, error) {
	if exec == nil {
		return false, errors.New("database is unavailable")
	}
	var active bool
	err := exec.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1
				FROM agent_event_delivery delivery
				JOIN agent_session session ON session.id = delivery.agent_session_id
				WHERE session.agent_id = $1
				  AND delivery.status IN ('leased', 'processing')
				  AND delivery.lease_expires_at > now()
			)
			OR EXISTS (
				SELECT 1
				FROM agent_execution execution
				WHERE execution.agent_id = $1
				  AND execution.status = 'running'
			)
	`, agentID).Scan(&active)
	return active, err
}

func getActiveAgentLifecycleOperation(ctx context.Context, exec dbExecutor, agentID pgtype.UUID) (*AgentLifecycleOperation, error) {
	if exec == nil {
		return nil, errors.New("database is unavailable")
	}
	return scanAgentLifecycleOperation(exec.QueryRow(ctx, agentLifecycleOperationSelect+`
		WHERE agent_id = $1
		  AND status IN ('scheduled', 'running')
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, agentID))
}

func getAgentLifecycleOperation(ctx context.Context, exec dbExecutor, agentID, operationID pgtype.UUID) (*AgentLifecycleOperation, error) {
	if exec == nil {
		return nil, errors.New("database is unavailable")
	}
	return scanAgentLifecycleOperation(exec.QueryRow(ctx, agentLifecycleOperationSelect+`
		WHERE agent_id = $1
		  AND id = $2
	`, agentID, operationID))
}

func getAgentLifecycleOperationByKey(ctx context.Context, exec dbExecutor, agentID, idempotencyKey pgtype.UUID) (*AgentLifecycleOperation, error) {
	return scanAgentLifecycleOperation(exec.QueryRow(ctx, agentLifecycleOperationSelect+`
		WHERE agent_id = $1
		  AND idempotency_key = $2
	`, agentID, idempotencyKey))
}

const agentLifecycleOperationSelect = `
	SELECT
		id, agent_id, runtime_id, action_kind, status, execution_mode,
		step, reason_code, created_at, started_at, finished_at
	FROM agent_lifecycle_operation
`

type lifecycleScanner interface {
	Scan(dest ...any) error
}

func scanAgentLifecycleOperation(row lifecycleScanner) (*AgentLifecycleOperation, error) {
	var (
		id, agentID, runtimeID                              pgtype.UUID
		actionKind, status, executionMode, step, reasonCode string
		createdAt, startedAt, finishedAt                    pgtype.Timestamptz
	)
	err := row.Scan(
		&id, &agentID, &runtimeID, &actionKind, &status, &executionMode,
		&step, &reasonCode, &createdAt, &startedAt, &finishedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	op := &AgentLifecycleOperation{
		ID:            uuidToString(id),
		AgentID:       uuidToString(agentID),
		RuntimeID:     uuidToPtr(runtimeID),
		ActionKind:    AgentLifecycleActionKind(actionKind),
		Status:        status,
		ExecutionMode: executionMode,
		Step:          step,
		ReasonCode:    reasonCode,
		CreatedAt:     timestampToString(createdAt),
		StartedAt:     timestampToPtr(startedAt),
		FinishedAt:    timestampToPtr(finishedAt),
	}
	return op, nil
}

func scanAgentForLifecycle(row lifecycleScanner) (db.Agent, error) {
	var agent db.Agent
	err := row.Scan(&agent.ID, &agent.WorkspaceID, &agent.RuntimeID, &agent.OwnerID)
	return agent, err
}

func parseLifecycleIdempotencyKey(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	raw := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if raw == "" {
		writeError(w, http.StatusBadRequest, "Idempotency-Key is required")
		return pgtype.UUID{}, false
	}
	return parseUUIDOrBadRequest(w, raw, "Idempotency-Key")
}

func validAgentLifecycleAction(action AgentLifecycleActionKind) bool {
	switch action {
	case agentLifecycleRestart, agentLifecycleResetSessionRestart, agentLifecycleFullResetRestart:
		return true
	default:
		return false
	}
}

func agentLifecycleInitialStep(status string) string {
	if status == agentLifecycleScheduled {
		return "waiting_for_current_run"
	}
	return "starting"
}

func agentLifecycleCapabilityPresent(capabilities []string) bool {
	for _, capability := range capabilities {
		if capability == protocol.DaemonCapabilityAgentLifecycleActions {
			return true
		}
	}
	return false
}
