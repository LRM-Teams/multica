package handler

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// DaemonAgentRuntimeConfigResponse is the durable process configuration for
// one Agent resident on a daemon runtime. It deliberately has no task,
// delivery, lease, execution, or session identity: those are message-delivery
// facts and never process configuration.
type DaemonAgentRuntimeConfigResponse struct {
	WorkspaceID      string                  `json:"workspace_id"`
	RuntimeID        string                  `json:"runtime_id"`
	WorkspaceContext string                  `json:"workspace_context,omitempty"`
	Agent            *DaemonAgentRuntimeData `json:"agent"`
	// RuntimeEnv is the machine-default env injected before agent custom_env.
	RuntimeEnv map[string]string `json:"runtime_env,omitempty"`
}

// DaemonAgentRuntimeData is the stable Agent configuration exposed to a
// resident Message process. It is intentionally separate from TaskAgentData:
// no task claim or execution envelope is part of the Message runtime API.
type DaemonAgentRuntimeData struct {
	ID              string                   `json:"id"`
	Name            string                   `json:"name"`
	ManagedRole     string                   `json:"managed_role,omitempty"`
	ManagerChannels []ManagerChannelData     `json:"manager_channels,omitempty"`
	Instructions    string                   `json:"instructions"`
	Skills          []service.AgentSkillData `json:"skills,omitempty"`
	CustomEnv       map[string]string        `json:"custom_env,omitempty"`
	CustomArgs      []string                 `json:"custom_args,omitempty"`
	McpConfig       json.RawMessage          `json:"mcp_config,omitempty"`
	Model           string                   `json:"model,omitempty"`
	ThinkingLevel   string                   `json:"thinking_level,omitempty"`
}

// DaemonGetAgentRuntimeConfig serves the stable configuration required to
// create or refresh a resident Agent process. The daemon runtime binding is
// both the authorization boundary and the only placement proof.
func (h *Handler) DaemonGetAgentRuntimeConfig(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	runtime, ok := h.requireDaemonRuntimeAccess(w, r, runtimeID)
	if !ok {
		return
	}
	agentID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "agentId"), "agent_id")
	if !ok {
		return
	}
	agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
		ID: agentID, WorkspaceID: runtime.WorkspaceID,
	})
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "agent not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load agent")
		return
	}
	if !agent.RuntimeID.Valid || agent.RuntimeID != runtime.ID {
		writeError(w, http.StatusForbidden, "agent is not bound to this runtime")
		return
	}

	var customEnv map[string]string
	if len(agent.CustomEnv) > 0 {
		if err := json.Unmarshal(agent.CustomEnv, &customEnv); err != nil {
			writeError(w, http.StatusInternalServerError, "invalid agent custom_env")
			return
		}
	}
	var customArgs []string
	if len(agent.CustomArgs) > 0 {
		if err := json.Unmarshal(agent.CustomArgs, &customArgs); err != nil {
			writeError(w, http.StatusInternalServerError, "invalid agent custom_args")
			return
		}
	}
	var runtimeEnv map[string]string
	if len(runtime.CustomEnv) > 0 {
		if err := json.Unmarshal(runtime.CustomEnv, &runtimeEnv); err != nil {
			writeError(w, http.StatusInternalServerError, "invalid runtime custom_env")
			return
		}
	}

	skills := h.TaskService.LoadAgentSkills(r.Context(), agent.ID)
	skills = append(skills, h.builtinSkillsForAgent(r.Context(), agent)...)
	response := DaemonAgentRuntimeConfigResponse{
		WorkspaceID: util.UUIDToString(runtime.WorkspaceID),
		RuntimeID:   util.UUIDToString(runtime.ID),
		RuntimeEnv:  runtimeEnv,
		Agent: &DaemonAgentRuntimeData{
			ID:              util.UUIDToString(agent.ID),
			Name:            agentDisplayName(agent),
			ManagedRole:     agent.ManagedRole.String,
			ManagerChannels: h.agentManagerChannels(r.Context(), runtime.WorkspaceID, agent.ID),
			Instructions:    agent.Instructions,
			Skills:          skills,
			CustomEnv:       customEnv,
			CustomArgs:      customArgs,
			McpConfig:       json.RawMessage(agent.McpConfig),
			Model:           agent.Model.String,
			ThinkingLevel:   agent.ThinkingLevel.String,
		},
	}
	if workspace, err := h.Queries.GetWorkspace(r.Context(), runtime.WorkspaceID); err == nil && workspace.Context.Valid {
		response.WorkspaceContext = workspace.Context.String
	}
	writeJSON(w, http.StatusOK, response)
}
