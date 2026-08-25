package handler

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// ---------------------------------------------------------------------------
// Remote restart request store (task #43)
// ---------------------------------------------------------------------------
//
// The server cannot reach the daemon directly (it's behind the user's NAT and
// only polls the server), so "restart this daemon" uses the same
// pending-request pattern as the CLI update / model list / local skill
// flows: a frontend POST creates a pending request, the daemon pops it on the
// next heartbeat and calls triggerRestart() locally. There is no completion
// callback — after a successful restart the daemon process exits and a new
// one re-registers, which is itself the success signal; GetRestart exists so
// the UI can show "delivered, waiting for the daemon to come back" in the
// meantime.
//
// Like ModelListStore/LocalSkillListStore, this MUST stay coherent across API
// replicas in multi-node deployments (Multica Cloud) — POST and the
// heartbeat pop can land on different nodes. The in-memory implementation
// below is the NewHandler default (fine for single-node/self-host); router.go
// overrides it with RedisRestartStore when Redis is configured, mirroring
// ModelListStore's wiring (see #2009 for the regression this pattern exists
// to avoid).

// RestartStatus represents the lifecycle of a remote restart request.
type RestartStatus string

const (
	RestartPending   RestartStatus = "pending"
	RestartDelivered RestartStatus = "delivered"
	RestartTimeout   RestartStatus = "timeout"
)

// restartPendingTimeout bounds how long a restart request waits for a
// heartbeat to claim it before the UI should stop polling and tell the human
// the daemon looks offline (there's no point restarting something that isn't
// heartbeating in the first place).
const restartPendingTimeout = 120 * time.Second

// restartStoreRetention keeps a terminal request around long enough for one
// last UI poll to observe the transition.
const restartStoreRetention = 5 * time.Minute

// RestartRequest represents a pending or delivered daemon restart request.
type RestartRequest struct {
	ID          string        `json:"id"`
	RuntimeID   string        `json:"runtime_id"`
	Status      RestartStatus `json:"status"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	DeliveredAt *time.Time    `json:"delivered_at,omitempty"`
}

// RestartStore is the cross-cutting state for the remote-restart flow.
type RestartStore interface {
	Create(ctx context.Context, runtimeID string) (*RestartRequest, error)
	Get(ctx context.Context, id string) (*RestartRequest, error)
	HasPending(ctx context.Context, runtimeID string) (bool, error)
	PopPending(ctx context.Context, runtimeID string) (*RestartRequest, error)
}

func applyRestartTimeout(req *RestartRequest, now time.Time) bool {
	if req.Status == RestartPending && now.Sub(req.CreatedAt) > restartPendingTimeout {
		req.Status = RestartTimeout
		req.UpdatedAt = now
		return true
	}
	return false
}

// InMemoryRestartStore is the NewHandler default and the deterministic
// unit-test implementation. Production multi-node deployments use
// RedisRestartStore (wired in router.go) instead.
type InMemoryRestartStore struct {
	mu       sync.Mutex
	requests map[string]*RestartRequest // keyed by restart ID
}

func NewInMemoryRestartStore() *InMemoryRestartStore {
	return &InMemoryRestartStore{requests: make(map[string]*RestartRequest)}
}

func (s *InMemoryRestartStore) Create(_ context.Context, runtimeID string) (*RestartRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	req := &RestartRequest{
		ID:        randomID(),
		RuntimeID: runtimeID,
		Status:    RestartPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.requests[req.ID] = req
	copy := *req
	return &copy, nil
}

func (s *InMemoryRestartStore) Get(_ context.Context, id string) (*RestartRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	req, ok := s.requests[id]
	if !ok {
		return nil, nil
	}
	applyRestartTimeout(req, time.Now())
	copy := *req
	return &copy, nil
}

func (s *InMemoryRestartStore) HasPending(_ context.Context, runtimeID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for _, req := range s.requests {
		if req.RuntimeID != runtimeID {
			continue
		}
		applyRestartTimeout(req, now)
		if req.Status == RestartPending {
			return true, nil
		}
	}
	return false, nil
}

func (s *InMemoryRestartStore) PopPending(_ context.Context, runtimeID string) (*RestartRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var oldest *RestartRequest
	for _, req := range s.requests {
		if req.RuntimeID != runtimeID {
			continue
		}
		applyRestartTimeout(req, now)
		if req.Status != RestartPending {
			continue
		}
		if oldest == nil || req.CreatedAt.Before(oldest.CreatedAt) {
			oldest = req
		}
	}
	if oldest == nil {
		return nil, nil
	}
	oldest.Status = RestartDelivered
	oldest.DeliveredAt = &now
	oldest.UpdatedAt = now
	copy := *oldest
	return &copy, nil
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// InitiateRestart creates a new remote restart request for a runtime
// (protected route, called by frontend). Same permission bar as
// Machine upgrades do not change this boundary: only the computer owner may
// restart it (Frank 2026-08-03).
func (h *Handler) InitiateRestart(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	runtimeUUID, ok := parseUUIDOrBadRequest(w, runtimeID, "runtime_id")
	if !ok {
		return
	}

	rt, err := h.Queries.GetAgentRuntime(r.Context(), runtimeUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found")
		return
	}

	member, ok := h.requireWorkspaceMember(w, r, uuidToString(rt.WorkspaceID), "runtime not found")
	if !ok {
		return
	}
	rtOwnerID, _ := h.resolveRuntimeOwnerQuery(r.Context(), rt)
	if !canOwnRuntime(member, rt, rtOwnerID) {
		writeError(w, http.StatusForbidden, "only the computer owner can restart this runtime")
		return
	}

	restart, err := h.RestartStore.Create(r.Context(), uuidToString(rt.ID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create restart request: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, restart)
}

// GetRestart returns the status of a restart request (protected route,
// called by frontend for polling).
func (h *Handler) GetRestart(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	runtimeUUID, ok := parseUUIDOrBadRequest(w, runtimeID, "runtime_id")
	if !ok {
		return
	}

	rt, err := h.Queries.GetAgentRuntime(r.Context(), runtimeUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found")
		return
	}
	member, ok := h.requireWorkspaceMember(w, r, uuidToString(rt.WorkspaceID), "runtime not found")
	if !ok {
		return
	}
	rtOwnerID, _ := h.resolveRuntimeOwnerQuery(r.Context(), rt)
	if !canOwnRuntime(member, rt, rtOwnerID) {
		writeError(w, http.StatusForbidden, "only the computer owner can inspect this restart request")
		return
	}

	restartID := chi.URLParam(r, "restartId")
	restart, err := h.RestartStore.Get(r.Context(), restartID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load restart request: "+err.Error())
		return
	}
	if restart == nil || restart.RuntimeID != uuidToString(rt.ID) {
		writeError(w, http.StatusNotFound, "restart request not found")
		return
	}

	writeJSON(w, http.StatusOK, restart)
}
