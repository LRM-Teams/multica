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

type agentRestartStorageKind string

const (
	agentRestartStorageRestart agentRestartStorageKind = "restart"
	agentRestartStorageSession agentRestartStorageKind = "reset_session_restart"
	agentRestartStorageFull    agentRestartStorageKind = "full_reset_restart"
)

type AgentRestartMode string

const (
	agentRestartModeRestart AgentRestartMode = "restart"
	agentRestartModeSession AgentRestartMode = "session"
	agentRestartModeFull    AgentRestartMode = "full"
)

const agentRestartCommandTimeout = 120 * time.Second

const (
	agentRestartRunning   = "running"
	agentRestartSucceeded = "succeeded"
	agentRestartFailed    = "failed"

	agentRestartImmediate = "immediate"
)

type AgentRestartOperation struct {
	ID          string                  `json:"id"`
	AgentID     string                  `json:"agent_id"`
	RuntimeID   *string                 `json:"runtime_id"`
	Mode        AgentRestartMode        `json:"mode"`
	storageKind agentRestartStorageKind `json:"-"`
	Status      string                  `json:"status"`
	Step        string                  `json:"step,omitempty"`
	ReasonCode  string                  `json:"reason_code,omitempty"`
	CreatedAt   string                  `json:"created_at"`
	StartedAt   *string                 `json:"started_at"`
	FinishedAt  *string                 `json:"finished_at"`
}

type AgentRestartPreflight struct {
	Actions         map[AgentRestartMode]AgentRestartModePreflight `json:"actions"`
	ActiveOperation *AgentRestartOperation                         `json:"active_operation"`
	// ProviderCapabilities is the FE-facing projection of
	// agent.ProviderCapabilities for this agent's runtime provider.
	// Gate the restart button on provider_capabilities.force_restart — do
	// not hardcode a provider allow-list. Older servers omit the object;
	// treat missing as all-false.
	ProviderCapabilities ProviderCapabilitiesWire `json:"provider_capabilities"`
}

type AgentRestartModePreflight struct {
	Supported      bool   `json:"supported"`
	DisabledReason string `json:"disabled_reason,omitempty"`
}

type createAgentRestartRequest struct {
	Mode AgentRestartMode `json:"mode"`
}

// GetAgentRestart returns the server-authoritative capability for Raft's
// three restart modes. Clients must not infer it from presentation state.
func (h *Handler) GetAgentRestart(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentRestartTarget(w, r)
	if !ok {
		return
	}
	preflight, err := h.agentRestartPreflight(r.Context(), agent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load Agent restart state")
		return
	}
	writeJSON(w, http.StatusOK, preflight)
}

// ResetAgent records one idempotent restart/reset request.
// The client supplies no runtime or filesystem path. Those bindings are always
// resolved from the locked agent row by the server.
func (h *Handler) ResetAgent(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentRestartTarget(w, r)
	if !ok {
		return
	}
	idempotencyKey, ok := parseRestartIdempotencyKey(w, r)
	if !ok {
		return
	}
	var req createAgentRestartRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	storageKind, validMode := agentRestartStorageForMode(req.Mode)
	if !validMode {
		writeError(w, http.StatusBadRequest, "invalid mode")
		return
	}
	if h.TxStarter == nil {
		writeError(w, http.StatusServiceUnavailable, "Agent restart is unavailable")
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create Agent restart operation")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()

	var lockedAgent db.Agent
	lockedAgent, err = scanAgentForRestart(tx.QueryRow(r.Context(), `
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
		writeError(w, http.StatusInternalServerError, "failed to create Agent restart operation")
		return
	}

	existing, err := getAgentRestartOperationByKey(r.Context(), tx, lockedAgent.ID, idempotencyKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create Agent restart operation")
		return
	}
	if existing != nil {
		if existing.storageKind != storageKind {
			writeError(w, http.StatusConflict, "Idempotency-Key is already bound to another operation")
			return
		}
		writeJSON(w, http.StatusAccepted, existing)
		return
	}

	runtime, supported, reason, err := h.agentRestartRuntimeSupport(r.Context(), lockedAgent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create Agent restart operation")
		return
	}
	if !supported {
		writeError(w, http.StatusConflict, reason)
		return
	}
	if req.Mode == agentRestartModeFull &&
		!workspaceRunnerResetCapabilityPresent(runtimeCapabilities(runtimeMetadata(runtime))) {
		writeError(w, http.StatusConflict, "unsupported_runtime_capability")
		return
	}

	// Raft exposes exactly three reset modes. Every accepted request starts at
	// the stop fence; there is no separate scheduling/execution-mode contract.
	executionMode := agentRestartImmediate
	status := agentRestartRunning
	var startedAt any = time.Now()
	actorID, ok := parseUUIDOrBadRequest(w, requestUserID(r), "user_id")
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	op, err := scanAgentRestartOperation(tx.QueryRow(r.Context(), `
		INSERT INTO agent_restart_operation (
			workspace_id, agent_id, runtime_id, actor_user_id,
			idempotency_key, action_kind, status, execution_mode,
			step, started_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING
			id, agent_id, runtime_id, action_kind, status, execution_mode,
			step, reason_code, created_at, started_at, finished_at
	`, lockedAgent.WorkspaceID, lockedAgent.ID, runtime.ID, actorID,
		idempotencyKey, string(storageKind), status, executionMode,
		agentRestartStepStopping, startedAt))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeError(w, http.StatusConflict, "an Agent restart operation is already active")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create Agent restart operation")
		return
	}

	// Restart operations are business state. The Runner reports any
	// user-visible Activity separately from this scheduling transaction.
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create Agent restart operation")
		return
	}
	// Product actions are server-owned state machines. The daemon sees only
	// Raft's discrete stop/reset-workspace/start commands and reports the
	// process facts that advance this durable operation.
	if runtime.DaemonID.Valid {
		state := restartStateFromOperation(op, uuidToString(lockedAgent.WorkspaceID), runtime.DaemonID.String)
		if err := h.beginAgentRestartOperation(r.Context(), state); err != nil {
			// Relay publication is not completion. Leave the operation running;
			// do not invent a terminal result, and do not redrive it later on
			// Runner ready.
			slog.Warn(
				"Agent Restart command not yet delivered",
				"workspace_id", uuidToString(lockedAgent.WorkspaceID),
				"computer_id", runtime.DaemonID.String,
				"runtime_id", uuidToString(runtime.ID),
				"agent_id", op.AgentID,
				"operation_id", op.ID,
				"mode", op.Mode,
				"step", op.Step,
				"error", err,
			)
		}
	}
	if latest, loadErr := getAgentRestartOperation(r.Context(), h.DB, lockedAgent.ID, parseUUID(op.ID)); loadErr == nil && latest != nil {
		op = latest
	}
	writeJSON(w, http.StatusAccepted, op)
}

// SweepTimedOutAgentRestartOperations is a business-record backstop, not a
// delivery scheduler. It only clears a visible running operation whose Runner
// status/reset facts never advanced it to a terminal state.
func SweepTimedOutAgentRestartOperations(ctx context.Context, exec dbExecutor) (int64, error) {
	if exec == nil {
		return 0, errors.New("database is unavailable")
	}
	commandTag, err := exec.Exec(ctx, `
		UPDATE agent_restart_operation
		SET status = 'failed', step = 'timeout',
		    reason_code = 'workspace Runner command did not finish before timeout',
		    finished_at = now()
		WHERE status = 'running'
		  AND started_at < now() - ($1 * interval '1 second')
	`, int64(agentRestartCommandTimeout/time.Second))
	if err != nil {
		return 0, err
	}
	return commandTag.RowsAffected(), nil
}

func (h *Handler) GetAgentRestartOperation(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentRestartTarget(w, r)
	if !ok {
		return
	}
	operationID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "operationId"), "operation_id")
	if !ok {
		return
	}
	op, err := getAgentRestartOperation(r.Context(), h.DB, agent.ID, operationID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load Agent restart operation")
		return
	}
	if op == nil {
		writeError(w, http.StatusNotFound, "Agent restart operation not found")
		return
	}
	writeJSON(w, http.StatusOK, op)
}

func (h *Handler) loadAgentRestartTarget(w http.ResponseWriter, r *http.Request) (db.Agent, bool) {
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

func (h *Handler) agentRestartPreflight(ctx context.Context, target db.Agent) (AgentRestartPreflight, error) {
	runtime, supported, reason, err := h.agentRestartRuntimeSupport(ctx, target)
	if err != nil {
		return AgentRestartPreflight{}, err
	}
	activeOperation, err := getActiveAgentRestartOperation(ctx, h.DB, target.ID)
	if err != nil {
		return AgentRestartPreflight{}, err
	}
	actions := make(map[AgentRestartMode]AgentRestartModePreflight, 3)
	resetWorkspaceSupported := supported && workspaceRunnerResetCapabilityPresent(runtimeCapabilities(runtimeMetadata(runtime)))
	for _, mode := range []AgentRestartMode{
		agentRestartModeRestart,
		agentRestartModeSession,
		agentRestartModeFull,
	} {
		actionSupported, actionReason := supported, reason
		if mode == agentRestartModeFull && supported && !resetWorkspaceSupported {
			actionSupported = false
			actionReason = "unsupported_runtime_capability"
		}
		actions[mode] = AgentRestartModePreflight{
			Supported:      actionSupported,
			DisabledReason: actionReason,
		}
	}
	return AgentRestartPreflight{
		Actions:              actions,
		ActiveOperation:      activeOperation,
		ProviderCapabilities: providerCapabilitiesWire(runtime.Provider),
	}, nil
}

func (h *Handler) agentRestartRuntimeSupport(ctx context.Context, agent db.Agent) (db.AgentRuntime, bool, string, error) {
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
	if !workspaceRunnerAgentProcessCapabilityPresent(runtimeCapabilities(runtimeMetadata(runtime))) {
		return runtime, false, "unsupported_runtime_capability", nil
	}
	return runtime, true, "", nil
}

func getActiveAgentRestartOperation(ctx context.Context, exec dbExecutor, agentID pgtype.UUID) (*AgentRestartOperation, error) {
	if exec == nil {
		return nil, errors.New("database is unavailable")
	}
	return scanAgentRestartOperation(exec.QueryRow(ctx, agentRestartOperationSelect+`
		WHERE agent_id = $1
		  AND status IN ('scheduled', 'running')
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, agentID))
}

func getAgentRestartOperation(ctx context.Context, exec dbExecutor, agentID, operationID pgtype.UUID) (*AgentRestartOperation, error) {
	if exec == nil {
		return nil, errors.New("database is unavailable")
	}
	return scanAgentRestartOperation(exec.QueryRow(ctx, agentRestartOperationSelect+`
		WHERE agent_id = $1
		  AND id = $2
	`, agentID, operationID))
}

func getAgentRestartOperationByKey(ctx context.Context, exec dbExecutor, agentID, idempotencyKey pgtype.UUID) (*AgentRestartOperation, error) {
	return scanAgentRestartOperation(exec.QueryRow(ctx, agentRestartOperationSelect+`
		WHERE agent_id = $1
		  AND idempotency_key = $2
	`, agentID, idempotencyKey))
}

const agentRestartOperationSelect = `
	SELECT
		id, agent_id, runtime_id, action_kind, status, execution_mode,
		step, reason_code, created_at, started_at, finished_at
	FROM agent_restart_operation
`

type restartScanner interface {
	Scan(dest ...any) error
}

func scanAgentRestartOperation(row restartScanner) (*AgentRestartOperation, error) {
	var (
		id, agentID, runtimeID                               pgtype.UUID
		storageKind, status, executionMode, step, reasonCode string
		createdAt, startedAt, finishedAt                     pgtype.Timestamptz
	)
	err := row.Scan(
		&id, &agentID, &runtimeID, &storageKind, &status, &executionMode,
		&step, &reasonCode, &createdAt, &startedAt, &finishedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	op := &AgentRestartOperation{
		ID:          uuidToString(id),
		AgentID:     uuidToString(agentID),
		RuntimeID:   uuidToPtr(runtimeID),
		storageKind: agentRestartStorageKind(storageKind),
		Mode:        agentRestartModeForStorage(agentRestartStorageKind(storageKind)),
		Status:      status,
		Step:        step,
		ReasonCode:  reasonCode,
		CreatedAt:   timestampToString(createdAt),
		StartedAt:   timestampToPtr(startedAt),
		FinishedAt:  timestampToPtr(finishedAt),
	}
	return op, nil
}

func scanAgentForRestart(row restartScanner) (db.Agent, error) {
	var agent db.Agent
	err := row.Scan(&agent.ID, &agent.WorkspaceID, &agent.RuntimeID, &agent.OwnerID)
	return agent, err
}

func parseRestartIdempotencyKey(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	raw := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if raw == "" {
		writeError(w, http.StatusBadRequest, "Idempotency-Key is required")
		return pgtype.UUID{}, false
	}
	return parseUUIDOrBadRequest(w, raw, "Idempotency-Key")
}

func agentRestartStorageForMode(mode AgentRestartMode) (agentRestartStorageKind, bool) {
	switch mode {
	case agentRestartModeRestart:
		return agentRestartStorageRestart, true
	case agentRestartModeSession:
		return agentRestartStorageSession, true
	case agentRestartModeFull:
		return agentRestartStorageFull, true
	default:
		return "", false
	}
}

func agentRestartModeForStorage(action agentRestartStorageKind) AgentRestartMode {
	switch action {
	case agentRestartStorageSession:
		return agentRestartModeSession
	case agentRestartStorageFull:
		return agentRestartModeFull
	default:
		return agentRestartModeRestart
	}
}

func workspaceRunnerAgentProcessCapabilityPresent(capabilities []string) bool {
	for _, capability := range capabilities {
		if capability == protocol.DaemonCapabilityWorkspaceRunnerAgentProcess {
			return true
		}
	}
	return false
}

func workspaceRunnerResetCapabilityPresent(capabilities []string) bool {
	for _, capability := range capabilities {
		if capability == protocol.DaemonCapabilityWorkspaceRunnerAgentReset {
			return true
		}
	}
	return false
}
