package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
)

type graphMemoryChannelModeResponse struct {
	WorkspaceID                   string `json:"workspace_id"`
	ChannelID                     string `json:"channel_id"`
	Override                      string `json:"override"`
	EffectiveMode                 string `json:"effective_mode"`
	Status                        string `json:"status"`
	BlockedReason                 string `json:"blocked_reason,omitempty"`
	AgentID                       string `json:"agent_id,omitempty"`
	RuntimeID                     string `json:"runtime_id,omitempty"`
	MemoryAgentRuntimeIDOverride  string `json:"memory_agent_runtime_id_override"`
	MemoryAgentModelOverride      string `json:"memory_agent_model_override"`
	MemoryAgentThinkingOverride   string `json:"memory_agent_thinking_override"`
	EffectiveMemoryAgentRuntimeID string `json:"effective_memory_agent_runtime_id"`
	EffectiveMemoryAgentModel     string `json:"effective_memory_agent_model"`
	EffectiveMemoryAgentThinking  string `json:"effective_memory_agent_thinking"`
}

type updateGraphMemoryChannelModeRequest struct {
	Override                     string          `json:"override"`
	MemoryAgentRuntimeIDOverride json.RawMessage `json:"memory_agent_runtime_id_override"`
	MemoryAgentModelOverride     json.RawMessage `json:"memory_agent_model_override"`
	MemoryAgentThinkingOverride  json.RawMessage `json:"memory_agent_thinking_override"`
}

func validGraphMemoryChannelOverride(value string) bool {
	return value == "inherit" || value == "inject" || value == "agent"
}

func decodeOptionalJSONString(raw json.RawMessage) (present bool, value *string, err error) {
	if len(raw) == 0 {
		return false, nil, nil
	}
	if string(raw) == "null" {
		return true, nil, nil
	}
	var decoded string
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return true, nil, err
	}
	return true, &decoded, nil
}

func validGraphMemoryAgentConfigText(value string, maxRunes int) bool {
	if len([]rune(value)) > maxRunes {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func (h *Handler) loadGraphMemoryChannelModeResponse(ctx context.Context, workspaceID string, channelID pgtype.UUID) (graphMemoryChannelModeResponse, error) {
	var response graphMemoryChannelModeResponse
	var memoryType, workspaceMode string
	err := h.DB.QueryRow(ctx, `
		SELECT c.workspace_id::text,c.id::text,c.graph_memory_mode_override,p.memory_type,p.graph_memory_mode,
		       COALESCE(a.status,'inactive'),COALESCE(a.blocked_reason,''),COALESCE(a.agent_id::text,''),COALESCE(a.runtime_id::text,''),
		       COALESCE(c.graph_memory_agent_runtime_id_override::text,''),
		       COALESCE(c.graph_memory_agent_model_override,''),
		       COALESCE(c.graph_memory_agent_thinking_override,''),
		       COALESCE(COALESCE(c.graph_memory_agent_runtime_id_override,p.memory_agent_runtime_id)::text,''),
		       CASE WHEN c.graph_memory_agent_runtime_id_override IS NOT NULL
		         THEN COALESCE(c.graph_memory_agent_model_override,'') ELSE COALESCE(p.memory_agent_model,'') END,
		       CASE WHEN c.graph_memory_agent_runtime_id_override IS NOT NULL
		         THEN COALESCE(c.graph_memory_agent_thinking_override,'') ELSE COALESCE(p.memory_agent_thinking,'') END
		FROM channel c JOIN graph_memory_profile p ON p.workspace_id=c.workspace_id
		LEFT JOIN graph_memory_channel_agent a ON a.channel_id=c.id
		WHERE c.id=$1 AND c.workspace_id=$2`, channelID, parseUUID(workspaceID)).Scan(
		&response.WorkspaceID, &response.ChannelID, &response.Override, &memoryType, &workspaceMode,
		&response.Status, &response.BlockedReason, &response.AgentID, &response.RuntimeID,
		&response.MemoryAgentRuntimeIDOverride, &response.MemoryAgentModelOverride, &response.MemoryAgentThinkingOverride,
		&response.EffectiveMemoryAgentRuntimeID, &response.EffectiveMemoryAgentModel, &response.EffectiveMemoryAgentThinking,
	)
	if err != nil {
		return graphMemoryChannelModeResponse{}, err
	}
	response.EffectiveMode = service.EffectiveGraphMemoryMode(memoryType, workspaceMode, response.Override)
	return response, nil
}

func (h *Handler) GetGraphMemoryChannelMode(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	response, err := h.loadGraphMemoryChannelModeResponse(r.Context(), workspaceID, channelID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "channel or graph memory profile not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load channel graph memory mode")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) UpdateGraphMemoryChannelMode(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return
	}
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	var req updateGraphMemoryChannelModeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Override = strings.ToLower(strings.TrimSpace(req.Override))
	if !validGraphMemoryChannelOverride(req.Override) {
		writeError(w, http.StatusBadRequest, "override must be 'inherit', 'inject', or 'agent'")
		return
	}
	runtimePresent, runtimeValue, err := decodeOptionalJSONString(req.MemoryAgentRuntimeIDOverride)
	if err != nil {
		writeError(w, http.StatusBadRequest, "memory_agent_runtime_id_override must be a string or null")
		return
	}
	modelPresent, modelValue, err := decodeOptionalJSONString(req.MemoryAgentModelOverride)
	if err != nil {
		writeError(w, http.StatusBadRequest, "memory_agent_model_override must be a string or null")
		return
	}
	thinkingPresent, thinkingValue, err := decodeOptionalJSONString(req.MemoryAgentThinkingOverride)
	if err != nil {
		writeError(w, http.StatusBadRequest, "memory_agent_thinking_override must be a string or null")
		return
	}
	if !runtimePresent && (modelPresent || thinkingPresent) {
		writeError(w, http.StatusBadRequest, "runtime override must be supplied with model/thinking override")
		return
	}

	var runtimeOverride pgtype.UUID
	var modelOverride, thinkingOverride pgtype.Text
	if runtimePresent && runtimeValue != nil {
		if !modelPresent || modelValue == nil || strings.TrimSpace(*modelValue) == "" {
			writeError(w, http.StatusBadRequest, "a non-empty model override is required with a runtime override")
			return
		}
		parsed, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(*runtimeValue), "memory_agent_runtime_id_override")
		if !ok {
			return
		}
		model := strings.TrimSpace(*modelValue)
		thinking := ""
		if thinkingPresent && thinkingValue != nil {
			thinking = strings.TrimSpace(*thinkingValue)
		}
		if !validGraphMemoryAgentConfigText(model, 512) || !validGraphMemoryAgentConfigText(thinking, 128) {
			writeError(w, http.StatusBadRequest, "memory agent model/thinking override is invalid")
			return
		}
		if err := h.DB.QueryRow(r.Context(), `
			SELECT id FROM agent_runtime
			WHERE id=$1 AND workspace_id=$2 AND provider='pi' AND status='online'`,
			parsed, parseUUID(workspaceID)).Scan(&runtimeOverride); errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "memory agent runtime override must be an online Pi runtime in this workspace")
			return
		} else if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to validate memory agent runtime override")
			return
		}
		modelOverride = pgtype.Text{String: model, Valid: true}
		thinkingOverride = pgtype.Text{String: thinking, Valid: true}
	} else if runtimePresent {
		if modelValue != nil && strings.TrimSpace(*modelValue) != "" || thinkingValue != nil && strings.TrimSpace(*thinkingValue) != "" {
			writeError(w, http.StatusBadRequest, "clearing runtime override requires empty model/thinking overrides")
			return
		}
	}

	tag, err := h.DB.Exec(r.Context(), `
		UPDATE channel SET
		  graph_memory_mode_override=$3,
		  graph_memory_agent_runtime_id_override=CASE WHEN $4 THEN $5::uuid ELSE graph_memory_agent_runtime_id_override END,
		  graph_memory_agent_model_override=CASE WHEN $4 THEN $6::text ELSE graph_memory_agent_model_override END,
		  graph_memory_agent_thinking_override=CASE WHEN $4 THEN $7::text ELSE graph_memory_agent_thinking_override END,
		  updated_at=now()
		WHERE id=$1 AND workspace_id=$2 AND kind='group'`,
		channelID, parseUUID(workspaceID), req.Override, runtimePresent,
		runtimeOverride, modelOverride, thinkingOverride)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update channel graph memory mode")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "group channel not found")
		return
	}
	if h.GraphMemoryAgentControl == nil {
		writeError(w, http.StatusServiceUnavailable, "graph memory agent control plane is unavailable")
		return
	}
	if _, err := h.GraphMemoryAgentControl.ReconcileChannel(r.Context(), workspaceID, uuidToString(channelID)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reconcile channel graph memory mode")
		return
	}
	response, err := h.loadGraphMemoryChannelModeResponse(r.Context(), workspaceID, channelID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load reconciled channel graph memory mode")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) ResetGraphMemoryChannelAgent(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return
	}
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if h.GraphMemoryAgentControl == nil {
		writeError(w, http.StatusServiceUnavailable, "graph memory agent control plane is unavailable")
		return
	}
	if err := h.GraphMemoryAgentControl.ResetState(r.Context(), workspaceID, uuidToString(channelID)); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
}

func (h *Handler) reconcileGraphMemoryWorkspaceChannels(ctx context.Context, workspaceID string) {
	if h.GraphMemoryAgentControl == nil {
		return
	}
	rows, err := h.DB.Query(ctx, `SELECT id::text FROM channel WHERE workspace_id=$1::uuid AND kind='group' AND system_key IS NULL`, workspaceID)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var channelID string
		if rows.Scan(&channelID) == nil {
			_, _ = h.GraphMemoryAgentControl.ReconcileChannel(ctx, workspaceID, channelID)
		}
	}
}
