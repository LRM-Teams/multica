package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/workgraph"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (h *Handler) CreateAgentWorkGraph(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	if h.WorkGraph == nil {
		writeError(w, http.StatusServiceUnavailable, "work graph unavailable")
		return
	}
	var input workgraph.CreateInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	input.WorkspaceID = p.WorkspaceID
	input.ActorType = "agent"
	input.ActorID = p.AgentID
	if key := r.Header.Get("Idempotency-Key"); key != "" {
		input.IdempotencyKey = key
	}
	result, err := h.WorkGraph.Create(r.Context(), input)
	if err != nil {
		writeWorkGraphError(w, err)
		return
	}
	h.publishAgentWorkGraphUpdated(r, p.WorkspaceID, p.AgentID, result.Graph.ID)
	writeJSON(w, http.StatusCreated, result)
}

func (h *Handler) GetAgentWorkGraph(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	if h.WorkGraph == nil {
		writeError(w, http.StatusServiceUnavailable, "work graph unavailable")
		return
	}
	if err := h.WorkGraph.AuthorizeAgent(r.Context(), p.WorkspaceID, chi.URLParam(r, "graphId"), p.AgentID, "", workgraph.AccessRead); err != nil {
		writeWorkGraphError(w, err)
		return
	}
	graph, err := h.WorkGraph.Get(r.Context(), p.WorkspaceID, chi.URLParam(r, "graphId"))
	if err != nil {
		writeWorkGraphError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, graph)
}

func (h *Handler) ReconcileAgentWorkGraph(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	if err := h.WorkGraph.AuthorizeAgent(r.Context(), p.WorkspaceID, chi.URLParam(r, "graphId"), p.AgentID, "", workgraph.AccessCoordinate); err != nil {
		writeWorkGraphError(w, err)
		return
	}
	ids, err := h.WorkGraph.ReconcileReady(r.Context(), p.WorkspaceID, chi.URLParam(r, "graphId"))
	if err != nil {
		writeWorkGraphError(w, err)
		return
	}
	h.publishAgentWorkGraphUpdated(r, p.WorkspaceID, p.AgentID, chi.URLParam(r, "graphId"))
	writeJSON(w, http.StatusOK, map[string]any{"newly_ready": ids})
}

func (h *Handler) InvalidateAgentWorkGraphNode(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	if err := h.WorkGraph.AuthorizeAgent(r.Context(), p.WorkspaceID, chi.URLParam(r, "graphId"), p.AgentID, "", workgraph.AccessCoordinate); err != nil {
		writeWorkGraphError(w, err)
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil || body.Reason == "" {
		writeError(w, http.StatusBadRequest, "reason is required")
		return
	}
	ids, err := h.WorkGraph.InvalidateFrom(r.Context(), p.WorkspaceID, chi.URLParam(r, "graphId"), chi.URLParam(r, "nodeId"), body.Reason)
	if err != nil {
		writeWorkGraphError(w, err)
		return
	}
	h.publishAgentWorkGraphUpdated(r, p.WorkspaceID, p.AgentID, chi.URLParam(r, "graphId"))
	writeJSON(w, http.StatusOK, map[string]any{"affected_nodes": ids})
}

func (h *Handler) UpdateAgentWorkGraphNode(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	var in workgraph.NodeUpdateInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	in.WorkspaceID = p.WorkspaceID
	in.GraphID = chi.URLParam(r, "graphId")
	in.NodeID = chi.URLParam(r, "nodeId")
	if err := h.WorkGraph.AuthorizeAgent(r.Context(), p.WorkspaceID, in.GraphID, p.AgentID, in.NodeID, workgraph.AccessExecute); err != nil {
		// Coordinators may repair state, but ordinary participants may mutate
		// only the worker node assigned to them.
		if coordinatorErr := h.WorkGraph.AuthorizeAgent(r.Context(), p.WorkspaceID, in.GraphID, p.AgentID, "", workgraph.AccessCoordinate); coordinatorErr != nil {
			writeWorkGraphError(w, err)
			return
		}
	}
	out, err := h.WorkGraph.UpdateNode(r.Context(), in)
	if err != nil {
		writeWorkGraphError(w, err)
		return
	}
	h.publishAgentWorkGraphUpdated(r, p.WorkspaceID, p.AgentID, in.GraphID)
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) AddAgentWorkGraphArtifact(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	var in workgraph.ArtifactInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	in.WorkspaceID = p.WorkspaceID
	in.GraphID = chi.URLParam(r, "graphId")
	if err := h.WorkGraph.AuthorizeAgent(r.Context(), p.WorkspaceID, in.GraphID, p.AgentID, in.ProducerNodeID, workgraph.AccessExecute); err != nil {
		writeWorkGraphError(w, err)
		return
	}
	out, err := h.WorkGraph.AddArtifact(r.Context(), in)
	if err != nil {
		writeWorkGraphError(w, err)
		return
	}
	h.publishAgentWorkGraphUpdated(r, p.WorkspaceID, p.AgentID, in.GraphID)
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) AddAgentWorkGraphVerification(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	var in workgraph.VerificationInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	in.WorkspaceID = p.WorkspaceID
	in.GraphID = chi.URLParam(r, "graphId")
	if err := h.WorkGraph.AuthorizeAgent(r.Context(), p.WorkspaceID, in.GraphID, p.AgentID, in.VerifierNodeID, workgraph.AccessVerify); err != nil {
		writeWorkGraphError(w, err)
		return
	}
	id, err := h.WorkGraph.AddVerification(r.Context(), in)
	if err != nil {
		writeWorkGraphError(w, err)
		return
	}
	h.publishAgentWorkGraphUpdated(r, p.WorkspaceID, p.AgentID, in.GraphID)
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (h *Handler) ReviseAgentWorkGraph(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	var in workgraph.ReviseInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	in.WorkspaceID = p.WorkspaceID
	in.GraphID = chi.URLParam(r, "graphId")
	in.ActorType = "agent"
	in.ActorID = p.AgentID
	if err := h.WorkGraph.AuthorizeAgent(r.Context(), p.WorkspaceID, in.GraphID, p.AgentID, "", workgraph.AccessCoordinate); err != nil {
		writeWorkGraphError(w, err)
		return
	}
	out, err := h.WorkGraph.Revise(r.Context(), in)
	if err != nil {
		writeWorkGraphError(w, err)
		return
	}
	h.publishAgentWorkGraphUpdated(r, p.WorkspaceID, p.AgentID, in.GraphID)
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) ListAgentWorkGraphEpochs(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	graphID := chi.URLParam(r, "graphId")
	if err := h.WorkGraph.AuthorizeAgent(r.Context(), p.WorkspaceID, graphID, p.AgentID, "", workgraph.AccessRead); err != nil {
		writeWorkGraphError(w, err)
		return
	}
	epochs, err := h.WorkGraph.ListEpochs(r.Context(), p.WorkspaceID, graphID)
	if err != nil {
		writeWorkGraphError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"epochs": epochs})
}

func (h *Handler) StartAgentWorkGraphEpoch(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	graphID := chi.URLParam(r, "graphId")
	if err := h.WorkGraph.AuthorizeAgent(r.Context(), p.WorkspaceID, graphID, p.AgentID, "", workgraph.AccessCoordinate); err != nil {
		writeWorkGraphError(w, err)
		return
	}
	var in workgraph.StartEpochInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	in.WorkspaceID, in.GraphID, in.ActorAgentID = p.WorkspaceID, graphID, p.AgentID
	out, err := h.WorkGraph.StartEpoch(r.Context(), in)
	if err != nil {
		writeWorkGraphError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) FinishAgentWorkGraphEpoch(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	graphID := chi.URLParam(r, "graphId")
	if err := h.WorkGraph.AuthorizeAgent(r.Context(), p.WorkspaceID, graphID, p.AgentID, "", workgraph.AccessCoordinate); err != nil {
		writeWorkGraphError(w, err)
		return
	}
	var in workgraph.FinishEpochInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	in.WorkspaceID, in.GraphID, in.EpochID, in.ActorAgentID = p.WorkspaceID, graphID, chi.URLParam(r, "epochId"), p.AgentID
	out, err := h.WorkGraph.FinishEpoch(r.Context(), in)
	if err != nil {
		writeWorkGraphError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) GetWorkGraph(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	if h.WorkGraph == nil {
		writeError(w, http.StatusServiceUnavailable, "work graph unavailable")
		return
	}
	graph, err := h.WorkGraph.Get(r.Context(), ctxWorkspaceID(r.Context()), chi.URLParam(r, "graphId"))
	if err != nil {
		writeWorkGraphError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, graph)
}

func writeWorkGraphError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, workgraph.ErrGraphForbidden):
		writeError(w, http.StatusForbidden, "work graph access denied")
	case errors.Is(err, workgraph.ErrInvalidGraph):
		writeError(w, http.StatusBadRequest, "invalid work graph")
	case errors.Is(err, workgraph.ErrGraphConflict), errors.Is(err, workgraph.ErrIdempotencyConflict):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "work graph operation failed")
	}
}

func (h *Handler) publishAgentWorkGraphUpdated(r *http.Request, workspaceID, agentID, graphID string) {
	var channelID string
	err := h.DB.QueryRow(r.Context(), `SELECT cg.channel_id::text FROM work_graph g JOIN channel_goal cg ON g.anchor_kind='channel_goal' AND cg.id=g.anchor_id WHERE g.workspace_id=$1::uuid AND g.id=$2::uuid`, workspaceID, graphID).Scan(&channelID)
	if err == nil {
		h.publish(protocol.EventChannelUpdated, workspaceID, "agent", agentID, map[string]any{"id": channelID, "work_graph_id": graphID})
	}
}
