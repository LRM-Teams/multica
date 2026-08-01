package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// ---------------------------------------------------------------------------
// Agent lifecycle operation dispatch (task #52)
// ---------------------------------------------------------------------------
//
// /api/agents/{id}/lifecycle (agent_lifecycle.go) already builds a complete,
// tested operation record (idempotency key, action_kind, status machine) and
// the daemon already has a complete, tested executor (daemon/agent_lifecycle.go)
// that can carry it out. Neither side was ever connected — CreateAgentLifecycleOperation
// wrote a "running" row that nothing ever picked up, so the frontend restart
// button (task #633) never had anything real behind it (task #52's origin:
// Nash found this while building task #42①'s standalone restart endpoint,
// which duplicated agentLifecycleExecutor.invalidateRuntime's exact call to
// canonicalAgentRuntimePool.invalidateSession without knowing this existed).
//
// This is the missing transport, following the same pending-request-per-heartbeat
// pattern as UpdateStore/RestartStore/ModelListStore: CreateAgentLifecycleOperation
// creates a dispatch entry keyed by runtime_id (reusing the operation's own ID,
// not minting a new one — the daemon reports its result back against that same
// ID), the daemon's heartbeat claims it and calls agentLifecycleExecutor.Execute,
// then reports success/failure back via ReportAgentLifecycleOperationResult so
// CreateAgentLifecycleOperation's DB row transitions to succeeded/failed.
//
// Scope note: only the "immediate" execution path (operation created with
// status=running) is wired here. Operations scheduled "after_current_run"
// (status=scheduled, the agent was mid-turn at request time) still have no
// trigger that dispatches them once the blocking turn ends — that gap
// pre-dates this PR (nothing dispatched anything before) and isn't
// introduced by it, but it is a known, explicitly-flagged remaining gap, not
// a silent one.

// AgentLifecycleDispatchStatus represents the lifecycle of a dispatch entry.
type AgentLifecycleDispatchStatus string

const (
	AgentLifecycleDispatchPending   AgentLifecycleDispatchStatus = "pending"
	AgentLifecycleDispatchDelivered AgentLifecycleDispatchStatus = "delivered"
	AgentLifecycleDispatchTimeout   AgentLifecycleDispatchStatus = "timeout"
)

// agentLifecycleDispatchPendingTimeout mirrors restartPendingTimeout: bounds
// how long a dispatch waits for a heartbeat to claim it.
const agentLifecycleDispatchPendingTimeout = 120 * time.Second

// agentLifecycleDispatchStoreRetention mirrors restartStoreRetention.
const agentLifecycleDispatchStoreRetention = 5 * time.Minute

// AgentLifecycleDispatch is one queued "go run this lifecycle operation"
// instruction for a daemon to claim on its next heartbeat.
type AgentLifecycleDispatch struct {
	OperationID string                       `json:"operation_id"`
	AgentID     string                       `json:"agent_id"`
	RuntimeID   string                       `json:"runtime_id"`
	WorkspaceID string                       `json:"workspace_id"`
	ActionKind  string                       `json:"action_kind"`
	Status      AgentLifecycleDispatchStatus `json:"status"`
	CreatedAt   time.Time                    `json:"created_at"`
	UpdatedAt   time.Time                    `json:"updated_at"`
	DeliveredAt *time.Time                   `json:"delivered_at,omitempty"`
}

// AgentLifecycleDispatchStore is the cross-cutting state for the dispatch
// flow. Pending lookups are scoped by RuntimeID (one heartbeat claims
// everything pending for that machine in one pass, since several agents can
// share one runtime).
type AgentLifecycleDispatchStore interface {
	Create(ctx context.Context, operationID, agentID, runtimeID, workspaceID, actionKind string) (*AgentLifecycleDispatch, error)
	HasPending(ctx context.Context, runtimeID string) (bool, error)
	PopAllPending(ctx context.Context, runtimeID string) ([]*AgentLifecycleDispatch, error)
}

func applyAgentLifecycleDispatchTimeout(d *AgentLifecycleDispatch, now time.Time) bool {
	if d.Status == AgentLifecycleDispatchPending && now.Sub(d.CreatedAt) > agentLifecycleDispatchPendingTimeout {
		d.Status = AgentLifecycleDispatchTimeout
		d.UpdatedAt = now
		return true
	}
	return false
}

// InMemoryAgentLifecycleDispatchStore is the NewHandler default and the
// deterministic unit-test implementation. Production multi-node deployments
// use RedisAgentLifecycleDispatchStore (wired in router.go) instead.
type InMemoryAgentLifecycleDispatchStore struct {
	mu         sync.Mutex
	dispatches map[string]*AgentLifecycleDispatch // keyed by operation ID
}

func NewInMemoryAgentLifecycleDispatchStore() *InMemoryAgentLifecycleDispatchStore {
	return &InMemoryAgentLifecycleDispatchStore{dispatches: make(map[string]*AgentLifecycleDispatch)}
}

func (s *InMemoryAgentLifecycleDispatchStore) Create(_ context.Context, operationID, agentID, runtimeID, workspaceID, actionKind string) (*AgentLifecycleDispatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	d := &AgentLifecycleDispatch{
		OperationID: operationID,
		AgentID:     agentID,
		RuntimeID:   runtimeID,
		WorkspaceID: workspaceID,
		ActionKind:  actionKind,
		Status:      AgentLifecycleDispatchPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	s.dispatches[operationID] = d
	copy := *d
	return &copy, nil
}

func (s *InMemoryAgentLifecycleDispatchStore) HasPending(_ context.Context, runtimeID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for _, d := range s.dispatches {
		if d.RuntimeID != runtimeID {
			continue
		}
		applyAgentLifecycleDispatchTimeout(d, now)
		if d.Status == AgentLifecycleDispatchPending {
			return true, nil
		}
	}
	return false, nil
}

func (s *InMemoryAgentLifecycleDispatchStore) PopAllPending(_ context.Context, runtimeID string) ([]*AgentLifecycleDispatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var claimed []*AgentLifecycleDispatch
	for _, d := range s.dispatches {
		if d.RuntimeID != runtimeID {
			continue
		}
		applyAgentLifecycleDispatchTimeout(d, now)
		if d.Status != AgentLifecycleDispatchPending {
			continue
		}
		d.Status = AgentLifecycleDispatchDelivered
		d.DeliveredAt = &now
		d.UpdatedAt = now
		copy := *d
		claimed = append(claimed, &copy)
	}
	return claimed, nil
}

// ---------------------------------------------------------------------------
// Result reporting (daemon -> server)
// ---------------------------------------------------------------------------

type agentLifecycleResultRequest struct {
	Status     string `json:"status"`
	Step       string `json:"step"`
	ReasonCode string `json:"reason_code"`
}

// ReportAgentLifecycleOperationResult is the daemon-authenticated endpoint
// the daemon calls after agentLifecycleExecutor.Execute returns, updating
// the operation row's status/step/reason_code/finished_at. Protected by
// requireDaemonWorkspaceAccess, matching the other daemon-report endpoints
// (ReportMemoryCurationRunResult et al.), not the human owner/admin bar
// CreateAgentLifecycleOperation uses.
func (h *Handler) ReportAgentLifecycleOperationResult(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	runtimeUUID, ok := parseUUIDOrBadRequest(w, runtimeID, "runtime_id")
	if !ok {
		return
	}
	operationID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "operationId"), "operation_id")
	if !ok {
		return
	}
	rt, err := h.Queries.GetAgentRuntime(r.Context(), runtimeUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found")
		return
	}
	if !h.requireDaemonWorkspaceAccess(w, r, uuidToString(rt.WorkspaceID)) {
		return
	}

	var req agentLifecycleResultRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	status := strings.ToLower(strings.TrimSpace(req.Status))
	if status != agentLifecycleSucceeded && status != agentLifecycleFailed {
		writeError(w, http.StatusBadRequest, "status must be succeeded or failed")
		return
	}
	if h.DB == nil {
		writeError(w, http.StatusServiceUnavailable, "agent lifecycle is unavailable")
		return
	}

	tag, err := h.DB.Exec(r.Context(), `
		UPDATE agent_lifecycle_operation
		SET status = $1, step = $2, reason_code = $3, finished_at = now()
		WHERE id = $4
		  AND runtime_id = $5
		  AND status IN ('running', 'scheduled')
	`, status, req.Step, req.ReasonCode, operationID, runtimeUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record agent lifecycle result")
		return
	}
	if tag.RowsAffected() == 0 {
		// Not an error: the operation may have already timed out or been
		// reported by a prior (retried) heartbeat delivery. Idempotent no-op.
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "recorded"})
}
