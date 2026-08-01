package handler

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// ---------------------------------------------------------------------------
// Manual single-agent restart request store (task #42①)
// ---------------------------------------------------------------------------
//
// Narrower than the daemon-wide RestartStore (task #43): this restarts one
// agent's provider process on its host daemon's canonical resident pool,
// without restarting the daemon itself and without resetting the agent's
// conversation session (only the process is replaced; PriorSessionID is
// untouched). Same pending-request pattern as RestartStore/UpdateStore — the
// server can't reach the daemon directly, so a frontend POST creates a
// pending request the daemon claims on its next heartbeat.
//
// Must stay coherent across API replicas in multi-node deployments; the
// in-memory implementation below is the NewHandler default, and router.go
// overrides it with RedisAgentRestartStore when Redis is configured (see
// #2009 for the regression class this pattern exists to avoid).

// AgentRestartStatus represents the lifecycle of a manual agent-restart
// request.
type AgentRestartStatus string

const (
	AgentRestartPending   AgentRestartStatus = "pending"
	AgentRestartDelivered AgentRestartStatus = "delivered"
	AgentRestartTimeout   AgentRestartStatus = "timeout"
)

// agentRestartPendingTimeout mirrors restartPendingTimeout: bounds how long a
// request waits for a heartbeat to claim it.
const agentRestartPendingTimeout = 120 * time.Second

// agentRestartStoreRetention mirrors restartStoreRetention.
const agentRestartStoreRetention = 5 * time.Minute

// AgentRestartRequest represents a pending or delivered single-agent restart
// request.
type AgentRestartRequest struct {
	ID          string             `json:"id"`
	AgentID     string             `json:"agent_id"`
	RuntimeID   string             `json:"runtime_id"`
	Status      AgentRestartStatus `json:"status"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
	DeliveredAt *time.Time         `json:"delivered_at,omitempty"`
}

// AgentRestartStore is the cross-cutting state for the manual single-agent
// restart flow. Pending lookups are scoped by RuntimeID (the daemon claims
// everything pending for the machine it's heartbeating from in one pass),
// but requests and Get lookups are identified by their own ID.
type AgentRestartStore interface {
	Create(ctx context.Context, agentID, runtimeID string) (*AgentRestartRequest, error)
	Get(ctx context.Context, id string) (*AgentRestartRequest, error)
	HasPending(ctx context.Context, runtimeID string) (bool, error)
	PopAllPending(ctx context.Context, runtimeID string) ([]*AgentRestartRequest, error)
}

func applyAgentRestartTimeout(req *AgentRestartRequest, now time.Time) bool {
	if req.Status == AgentRestartPending && now.Sub(req.CreatedAt) > agentRestartPendingTimeout {
		req.Status = AgentRestartTimeout
		req.UpdatedAt = now
		return true
	}
	return false
}

// InMemoryAgentRestartStore is the NewHandler default and the deterministic
// unit-test implementation. Production multi-node deployments use
// RedisAgentRestartStore (wired in router.go) instead.
type InMemoryAgentRestartStore struct {
	mu       sync.Mutex
	requests map[string]*AgentRestartRequest // keyed by request ID
}

func NewInMemoryAgentRestartStore() *InMemoryAgentRestartStore {
	return &InMemoryAgentRestartStore{requests: make(map[string]*AgentRestartRequest)}
}

func (s *InMemoryAgentRestartStore) Create(_ context.Context, agentID, runtimeID string) (*AgentRestartRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	req := &AgentRestartRequest{
		ID:        randomID(),
		AgentID:   agentID,
		RuntimeID: runtimeID,
		Status:    AgentRestartPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.requests[req.ID] = req
	copy := *req
	return &copy, nil
}

func (s *InMemoryAgentRestartStore) Get(_ context.Context, id string) (*AgentRestartRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	req, ok := s.requests[id]
	if !ok {
		return nil, nil
	}
	applyAgentRestartTimeout(req, time.Now())
	copy := *req
	return &copy, nil
}

func (s *InMemoryAgentRestartStore) HasPending(_ context.Context, runtimeID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for _, req := range s.requests {
		if req.RuntimeID != runtimeID {
			continue
		}
		applyAgentRestartTimeout(req, now)
		if req.Status == AgentRestartPending {
			return true, nil
		}
	}
	return false, nil
}

// PopAllPending claims every pending request for runtimeID in one pass — a
// single heartbeat delivers restarts for however many of the runtime's
// agents currently have one queued, rather than one per heartbeat cycle.
func (s *InMemoryAgentRestartStore) PopAllPending(_ context.Context, runtimeID string) ([]*AgentRestartRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var claimed []*AgentRestartRequest
	for _, req := range s.requests {
		if req.RuntimeID != runtimeID {
			continue
		}
		applyAgentRestartTimeout(req, now)
		if req.Status != AgentRestartPending {
			continue
		}
		req.Status = AgentRestartDelivered
		req.DeliveredAt = &now
		req.UpdatedAt = now
		copy := *req
		claimed = append(claimed, &copy)
	}
	return claimed, nil
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// InitiateAgentRestart creates a new manual restart request for one agent's
// provider process (protected route, called by frontend). Same permission
// bar as agent management generally: only the agent owner or a workspace
// admin/owner may restart it.
func (h *Handler) InitiateAgentRestart(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	agent, ok := h.loadAgentForUser(w, r, agentID)
	if !ok {
		return
	}
	if !h.canManageAgent(w, r, agent) {
		return
	}
	runtimeID := uuidToString(agent.RuntimeID)
	if runtimeID == "" {
		writeError(w, http.StatusConflict, "agent has no runtime assigned")
		return
	}

	restart, err := h.AgentRestartStore.Create(r.Context(), uuidToString(agent.ID), runtimeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create agent restart request: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, restart)
}

// GetAgentRestart returns the status of a manual agent-restart request
// (protected route, called by frontend for polling).
func (h *Handler) GetAgentRestart(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	agent, ok := h.loadAgentForUser(w, r, agentID)
	if !ok {
		return
	}
	if !h.canManageAgent(w, r, agent) {
		return
	}

	restartID := chi.URLParam(r, "restartId")
	restart, err := h.AgentRestartStore.Get(r.Context(), restartID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent restart request: "+err.Error())
		return
	}
	if restart == nil || restart.AgentID != uuidToString(agent.ID) {
		writeError(w, http.StatusNotFound, "restart request not found")
		return
	}

	writeJSON(w, http.StatusOK, restart)
}
