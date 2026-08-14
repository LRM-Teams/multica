package handler

import (
	"net/http"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// AgentWorkspaceInfoResponse is the narrow, read-only operational overview
// used by `multica workspace info` under an AgentPrincipal. It deliberately
// excludes agent instructions/runtime configuration and runtime ownership or
// metadata; those belong to human control-plane surfaces.
type AgentWorkspaceInfoResponse struct {
	Workspace WorkspaceResponse                    `json:"workspace"`
	Agents    []AgentWorkspaceInfoAgentResponse    `json:"agents"`
	Computers []AgentWorkspaceInfoComputerResponse `json:"computers"`
	Tasks     []AgentWorkspaceInfoTaskResponse     `json:"tasks"`
}

type AgentWorkspaceInfoAgentResponse struct {
	ID                   string  `json:"id"`
	Name                 string  `json:"name"`
	DisplayName          string  `json:"display_name"`
	Status               string  `json:"status"`
	RuntimeID            string  `json:"runtime_id,omitempty"`
	RuntimeName          string  `json:"runtime_name,omitempty"`
	RuntimeStatus        string  `json:"runtime_status,omitempty"`
	RuntimeDisplayStatus string  `json:"runtime_display_status,omitempty"`
	ArchivedAt           *string `json:"archived_at,omitempty"`
}

type AgentWorkspaceInfoComputerResponse struct {
	ID                string                                `json:"id"`
	Name              string                                `json:"name"`
	DisplayName       string                                `json:"display_name,omitempty"`
	Provider          string                                `json:"provider,omitempty"`
	RuntimeMode       string                                `json:"runtime_mode,omitempty"`
	Status            string                                `json:"status"`
	RuntimeHealth     string                                `json:"runtime_health,omitempty"`
	CurrentVersion    *string                               `json:"current_version,omitempty"`
	UpdateState       string                                `json:"update_state,omitempty"`
	UpdateError       *string                               `json:"update_error,omitempty"`
	AutoUpdate        *AgentWorkspaceInfoAutoUpdateResponse `json:"auto_update,omitempty"`
	DeviceName        string                                `json:"device_name,omitempty"`
	ComputerConnected bool                                  `json:"computer_connected"`
}

type AgentWorkspaceInfoAutoUpdateResponse struct {
	ErrorMessage *string `json:"error_message,omitempty"`
}

type AgentWorkspaceInfoTaskResponse struct {
	AgentID       string  `json:"agent_id"`
	Status        string  `json:"status"`
	CompletedAt   *string `json:"completed_at,omitempty"`
	Error         *string `json:"error,omitempty"`
	FailureReason string  `json:"failure_reason,omitempty"`
}

// GetAgentWorkspaceInfo returns the Agent-native equivalent of Raft's
// ordinary-agent-readable server overview. Authorization comes only from the
// immutable AgentPrincipal workspace stamp; an owner user is never borrowed as
// a viewer identity.
func (h *Handler) GetAgentWorkspaceInfo(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	workspaceID, workspaceOK := p.WorkspaceUUID()
	agentID, agentOK := p.AgentUUID()
	if !workspaceOK || !agentOK {
		writeError(w, http.StatusForbidden, "access denied")
		return
	}

	workspace, err := h.Queries.GetWorkspace(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	caller, err := h.Queries.GetAgent(r.Context(), agentID)
	if err != nil || uuidToString(caller.WorkspaceID) != p.WorkspaceID {
		writeError(w, http.StatusForbidden, "access denied")
		return
	}

	var agents []db.Agent
	if r.URL.Query().Get("include_archived") == "true" {
		agents, err = h.Queries.ListAllAgents(r.Context(), workspaceID)
	} else {
		agents, err = h.Queries.ListAgents(r.Context(), workspaceID)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list agents")
		return
	}
	fleetAgentIDs, err := h.Queries.ListActiveResearchFleetMemberAgentIDsByWorkspace(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load research fleet membership")
		return
	}
	fleetAgents := make(map[string]struct{}, len(fleetAgentIDs))
	for _, id := range fleetAgentIDs {
		fleetAgents[uuidToString(id)] = struct{}{}
	}
	agentDetails := make([]AgentResponse, 0, len(agents))
	visibleAgentIDs := make(map[string]struct{}, len(agents))
	for _, agent := range agents {
		agentID := uuidToString(agent.ID)
		if _, hidden := fleetAgents[agentID]; hidden {
			continue
		}
		agentDetails = append(agentDetails, agentToResponse(agent))
		visibleAgentIDs[agentID] = struct{}{}
	}
	h.attachAgentRuntimeNames(r.Context(), agentDetails)
	agentOverview := make([]AgentWorkspaceInfoAgentResponse, 0, len(agentDetails))
	for _, agent := range agentDetails {
		agentOverview = append(agentOverview, AgentWorkspaceInfoAgentResponse{
			ID:                   agent.ID,
			Name:                 agent.Name,
			DisplayName:          agent.DisplayName,
			Status:               agent.Status,
			RuntimeID:            agent.RuntimeID,
			RuntimeName:          agent.RuntimeName,
			RuntimeStatus:        agent.RuntimeStatus,
			RuntimeDisplayStatus: agent.RuntimeDisplayStatus,
			ArchivedAt:           agent.ArchivedAt,
		})
	}

	allRuntimes, err := h.Queries.ListAgentRuntimes(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list runtimes")
		return
	}
	// Agent-native visibility is public Computers plus the caller's own bound
	// Computer. OwnerUserID is audit lineage, not a human viewer identity, so
	// it must never unlock that human's other private Computers.
	runtimes := make([]db.AgentRuntime, 0, len(allRuntimes))
	for _, runtime := range allRuntimes {
		if runtime.Visibility == "public" || uuidToString(runtime.ID) == uuidToString(caller.RuntimeID) {
			runtimes = append(runtimes, runtime)
		}
	}
	runtimeDetails := h.agentRuntimeResponsesForList(r.Context(), runtimes)
	computers := make([]AgentWorkspaceInfoComputerResponse, 0, len(runtimeDetails))
	for _, runtime := range runtimeDetails {
		var autoUpdate *AgentWorkspaceInfoAutoUpdateResponse
		if runtime.AutoUpdate != nil {
			autoUpdate = &AgentWorkspaceInfoAutoUpdateResponse{ErrorMessage: runtime.AutoUpdate.ErrorMessage}
		}
		computers = append(computers, AgentWorkspaceInfoComputerResponse{
			ID:                runtime.ID,
			Name:              runtime.Name,
			DisplayName:       runtime.DisplayName,
			Provider:          runtime.Provider,
			RuntimeMode:       runtime.RuntimeMode,
			Status:            runtime.Status,
			RuntimeHealth:     runtime.RuntimeHealth,
			CurrentVersion:    runtime.CurrentVersion,
			UpdateState:       runtime.UpdateState,
			UpdateError:       runtime.UpdateError,
			AutoUpdate:        autoUpdate,
			DeviceName:        runtime.DeviceName,
			ComputerConnected: runtime.ComputerConnected,
		})
	}

	taskRows, err := h.Queries.ListWorkspaceAgentTaskSnapshot(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list agent task snapshot")
		return
	}
	tasks := make([]AgentWorkspaceInfoTaskResponse, 0, len(taskRows))
	for _, task := range taskRows {
		agentID := uuidToString(task.AgentID)
		if _, visible := visibleAgentIDs[agentID]; !visible {
			continue
		}
		tasks = append(tasks, AgentWorkspaceInfoTaskResponse{
			AgentID:       agentID,
			Status:        task.Status,
			CompletedAt:   timestampToPtr(task.CompletedAt),
			Error:         textToPtr(task.Error),
			FailureReason: task.FailureReason.String,
		})
	}

	writeJSON(w, http.StatusOK, AgentWorkspaceInfoResponse{
		Workspace: workspaceToResponse(workspace),
		Agents:    agentOverview,
		Computers: computers,
		Tasks:     tasks,
	})
}
