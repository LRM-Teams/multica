package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/logger"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	windyAgentName        = "Windy"
	legacyWindyAgentName  = "Joe"
	windyAgentTemplate    = "windy_hr"
	windyDescription      = "Personal HR for building and updating your Multica agent team."
	windyMaxDraftNameLen  = 80
	windyMaxDraftTextLen  = 20000
	windyMaxDraftListSize = 32
)

const windyAvatarURL = "/agent-avatars/human-11.jpg"

const windyInstructions = `Role

You are Windy, the user's personal HR and team-building lead for Multica. Your mission is to help this user start useful human-agent collaboration quickly by turning their real work into agents, channels, projects, and tasks.

Core Goals

- Help the user set up a practical agent team for real work.
- Understand what the user wants to accomplish before explaining Multica concepts.
- Recommend agents based on the user's actual goals, not from a fixed template.
- Help create practical channels mapped to real workflows.
- Optionally bind projects or repos when the user is doing project or code work.
- If the user has no clear idea, provide a few simple starter paths and one recommended next step.

What Multica Is

Multica is a workspace where humans and AI agents collaborate as persistent teammates. Agents can work in shared channels and threads, claim tasks, keep role context, hand off work, and participate in project boards.

Decision Principles

- Start from the user's existing work.
- Do not force every channel to be a project channel.
- If the user wants casual discussion, suggest general agents and general channels.
- If the user wants project collaboration, suggest a project channel and optional project binding.
- If the user wants code execution, require a project/repo and task-level workspaces.
- Recommend a small initial team first, usually 2-4 agents.
- Let specialization emerge when the user is unsure.
- Use channels for workstreams and threads/tasks for execution.

Agent Recruiting Behavior

When the user describes a goal, produce agent draft cards instead of asking them to manually write prompts. Each draft should include name, role summary, why it is useful, suggested channels, optional project binding, generated system instructions, recommended tools/capabilities, and whether it can execute code.

Use this exact markdown shape for a draft card so the UI can open a prefilled Create Agent page:

[Create Agent: <agent name>](multica://create-agent?name=<urlencoded name>&description=<urlencoded short description>&instructions=<urlencoded generated instructions>&visibility=private&can_execute_code=<true-or-false>)

Leave avatar_url empty unless the user explicitly provides an image. The Multica UI will assign a random human avatar automatically.

Do not silently create agents. Always let the user confirm by clicking a create card or creation action.

Project And Channel Behavior

- For casual chat: suggest a general channel with no project binding.
- For one clear project: suggest a project channel with that project as default.
- For multiple projects: recommend separate project channels unless the user explicitly wants one multi-project room.
- For code tasks: ensure the task has a project, repo, branch/workspace policy, and review gate.
- Windy is user-scoped. Do not present yourself as a project manager for one project.

Tone Principles

- Calm, practical, and reassuring.
- Reduce setup anxiety.
- No info dump.
- One actionable next step per turn.
- Use examples when the user is unsure.
- Be concrete: recommend a starter team, channel, or next action.
- Reply in the user's language.

Behavioral Invariant

Success is not a long onboarding conversation. Success means the user gets a useful first team, a practical channel, and a clear next step toward real collaboration.`

type WindyResponse struct {
	Agent AgentResponse `json:"agent"`
	DMID  string        `json:"dm_id,omitempty"`
}

type AgentCreationDraftResponse struct {
	ID                string   `json:"id"`
	WorkspaceID       string   `json:"workspace_id"`
	CreatedByAgentID  *string  `json:"created_by_agent_id,omitempty"`
	TargetUserID      string   `json:"target_user_id"`
	Name              string   `json:"name"`
	Description       string   `json:"description"`
	Instructions      string   `json:"instructions"`
	AvatarURL         *string  `json:"avatar_url,omitempty"`
	Visibility        string   `json:"visibility"`
	ProjectID         *string  `json:"project_id,omitempty"`
	ChannelID         *string  `json:"channel_id,omitempty"`
	CanExecuteCode    bool     `json:"can_execute_code"`
	SuggestedChannels []string `json:"suggested_channels"`
	RecommendedTools  []string `json:"recommended_tools"`
	Status            string   `json:"status"`
	UsedAgentID       *string  `json:"used_agent_id,omitempty"`
	CreatedAt         string   `json:"created_at"`
	UpdatedAt         string   `json:"updated_at"`
	UsedAt            *string  `json:"used_at,omitempty"`
}

type CreateAgentDraftRequest struct {
	Name              string   `json:"name"`
	Description       string   `json:"description"`
	Instructions      string   `json:"instructions"`
	AvatarURL         *string  `json:"avatar_url"`
	Visibility        string   `json:"visibility"`
	ProjectID         *string  `json:"project_id"`
	ChannelID         *string  `json:"channel_id"`
	CanExecuteCode    bool     `json:"can_execute_code"`
	SuggestedChannels []string `json:"suggested_channels"`
	RecommendedTools  []string `json:"recommended_tools"`
}

func (h *Handler) EnsureWindy(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}

	runtime, ok := h.pickWindyRuntime(w, r, wsUUID, parseUUID(userID))
	if !ok {
		return
	}
	if !canUseRuntimeForAgent(member, runtime) {
		writeError(w, http.StatusForbidden, "this runtime is private; only its owner or a workspace admin can create agents on it")
		return
	}

	agent, created, err := h.ensureWindyAgent(r, wsUUID, parseUUID(userID), runtime)
	if err != nil {
		slog.Warn("ensure Windy failed", append(logger.RequestAttrs(r), "error", err, "workspace_id", workspaceID)...)
		writeError(w, http.StatusInternalServerError, "failed to create Windy")
		return
	}
	if created {
		h.refreshAgentSkillSuggestions(r.Context(), agent)
		if runtime.Status == "online" {
			h.TaskService.ReconcileAgentStatus(r.Context(), agent.ID)
			agent, _ = h.Queries.GetAgent(r.Context(), agent.ID)
		}
		resp := agentToResponse(agent)
		h.publish(protocol.EventAgentCreated, workspaceID, "member", userID, map[string]any{"agent": broadcastAgentResponse(resp)})
		obsmetrics.RecordEvent(h.Analytics, h.Metrics, analytics.AgentCreated(
			userID,
			workspaceID,
			uuidToString(agent.ID),
			runtime.Provider,
			runtime.RuntimeMode,
			windyAgentTemplate,
			false,
		))
	}

	dmID := ""
	if ch, ok := h.ensureWindyDM(r, workspaceID, parseUUID(userID), agent.ID); ok {
		dmID = ch.ID
	}

	resp := agentToResponse(agent)
	redactAgentResponseForActor(&resp, "member")
	writeJSON(w, http.StatusOK, WindyResponse{Agent: resp, DMID: dmID})
}

func (h *Handler) pickWindyRuntime(w http.ResponseWriter, r *http.Request, workspaceID, userID pgtype.UUID) (db.AgentRuntime, bool) {
	runtimeID := strings.TrimSpace(r.URL.Query().Get("runtime_id"))
	if runtimeID != "" {
		runtimeUUID, ok := parseUUIDOrBadRequest(w, runtimeID, "runtime_id")
		if !ok {
			return db.AgentRuntime{}, false
		}
		runtime, err := h.Queries.GetAgentRuntimeForWorkspace(r.Context(), db.GetAgentRuntimeForWorkspaceParams{ID: runtimeUUID, WorkspaceID: workspaceID})
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid runtime_id")
			return db.AgentRuntime{}, false
		}
		return runtime, true
	}

	runtimes, err := h.Queries.ListVisibleAgentRuntimes(r.Context(), db.ListVisibleAgentRuntimesParams{WorkspaceID: workspaceID, OwnerID: userID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list runtimes")
		return db.AgentRuntime{}, false
	}
	if len(runtimes) == 0 {
		writeError(w, http.StatusBadRequest, "connect a runtime before creating Windy")
		return db.AgentRuntime{}, false
	}
	for _, rt := range runtimes {
		if rt.OwnerID.Valid && uuidToString(rt.OwnerID) == uuidToString(userID) && rt.Status == "online" {
			return rt, true
		}
	}
	for _, rt := range runtimes {
		if rt.Status == "online" {
			return rt, true
		}
	}
	return runtimes[0], true
}

func (h *Handler) ensureWindyAgent(r *http.Request, workspaceID, userID pgtype.UUID, runtime db.AgentRuntime) (db.Agent, bool, error) {
	agents, err := h.Queries.ListAgents(r.Context(), workspaceID)
	if err != nil {
		return db.Agent{}, false, err
	}
	for _, existing := range agents {
		name := agentDisplayName(existing)
		if existing.OwnerID.Valid && uuidToString(existing.OwnerID) == uuidToString(userID) && (name == windyAgentName || name == legacyWindyAgentName) && existing.Visibility == "private" {
			return existing, false, nil
		}
	}

	created, err := h.createAgentWithIdentity(r.Context(), h.Queries, db.CreateAgentParams{
		WorkspaceID:        workspaceID,
		Description:        windyDescription,
		Instructions:       windyInstructions,
		AvatarUrl:          pgtype.Text{String: windyAvatarURL, Valid: true},
		RuntimeMode:        runtime.RuntimeMode,
		RuntimeConfig:      []byte("{}"),
		RuntimeID:          runtime.ID,
		Visibility:         "private",
		MaxConcurrentTasks: 6,
		OwnerID:            userID,
		CustomEnv:          []byte("{}"),
		CustomArgs:         []byte("[]"),
		McpConfig:          nil,
		Model:              pgtype.Text{},
		ThinkingLevel:      pgtype.Text{},
	}, windyAgentName, windyAgentName)
	if err != nil {
		return db.Agent{}, false, err
	}
	return created, true, nil
}

func (h *Handler) ensureWindyDM(r *http.Request, workspaceID string, userID, windyID pgtype.UUID) (ChannelResponse, bool) {
	canonical := dmCanonicalName("user", uuidToString(userID), "agent", uuidToString(windyID))
	if ch, found := h.findDMChannel(r.Context(), workspaceID, canonical); found {
		h.clearDMPeerHidden(r.Context(), workspaceID, uuidToString(userID), dmPeerRef{Type: "agent", ID: windyID})
		return ch, true
	}
	return h.createDMChannel(r.Context(), nil, workspaceID, uuidToString(userID), canonical, []dmMember{{memberType: "user", memberID: userID}, {memberType: "agent", memberID: windyID}})
}

func (h *Handler) GetAgentDraft(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	draftID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "draftId"), "draft id")
	if !ok {
		return
	}
	row := h.DB.QueryRow(r.Context(), `
		SELECT id, workspace_id, created_by_agent_id, target_user_id, name,
			description, instructions, avatar_url, visibility, project_id, channel_id,
			can_execute_code, suggested_channels, recommended_tools, status,
			used_agent_id, created_at, updated_at, used_at
		FROM agent_creation_draft
		WHERE id = $1 AND workspace_id = $2 AND target_user_id = $3`,
		draftID, wsUUID, parseUUID(userID))
	draft, err := scanAgentDraft(row)
	if err != nil {
		writeError(w, http.StatusNotFound, "agent draft not found")
		return
	}
	writeJSON(w, http.StatusOK, draft)
}

func (h *Handler) CreateAgentDraft(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}

	var req CreateAgentDraftRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !h.validateAgentDraftRequest(w, &req) {
		return
	}

	var projectID pgtype.UUID
	if req.ProjectID != nil && strings.TrimSpace(*req.ProjectID) != "" {
		parsed, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(*req.ProjectID), "project_id")
		if !ok {
			return
		}
		projectID = parsed
	}
	var channelID pgtype.UUID
	if req.ChannelID != nil && strings.TrimSpace(*req.ChannelID) != "" {
		parsed, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(*req.ChannelID), "channel_id")
		if !ok {
			return
		}
		channelID = parsed
	}

	createdBy := pgtype.UUID{}
	if raw := r.Header.Get("X-Agent-ID"); strings.TrimSpace(raw) != "" {
		if u, err := parseAgentActorHeader(raw); err == nil {
			createdBy = u
		}
	}

	draft, err := h.insertAgentDraft(r, wsUUID, parseUUID(userID), createdBy, projectID, channelID, req)
	if err != nil {
		slog.Warn("create agent draft failed", append(logger.RequestAttrs(r), "error", err, "workspace_id", workspaceID)...)
		writeError(w, http.StatusInternalServerError, "failed to create agent draft")
		return
	}
	writeJSON(w, http.StatusCreated, draft)
}

func (h *Handler) validateAgentDraftRequest(w http.ResponseWriter, req *CreateAgentDraftRequest) bool {
	req.Name = strings.TrimSpace(req.Name)
	req.Description = strings.TrimSpace(req.Description)
	req.Instructions = strings.TrimSpace(req.Instructions)
	req.Visibility = strings.TrimSpace(req.Visibility)
	if req.Visibility == "" {
		req.Visibility = "private"
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return false
	}
	if utf8.RuneCountInString(req.Name) > windyMaxDraftNameLen {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("name must be %d characters or fewer", windyMaxDraftNameLen))
		return false
	}
	if utf8.RuneCountInString(req.Description) > maxAgentDescriptionLength {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("description must be %d characters or fewer", maxAgentDescriptionLength))
		return false
	}
	if utf8.RuneCountInString(req.Instructions) > windyMaxDraftTextLen {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("instructions must be %d characters or fewer", windyMaxDraftTextLen))
		return false
	}
	if req.Visibility != "workspace" && req.Visibility != "private" {
		writeError(w, http.StatusBadRequest, "visibility must be workspace or private")
		return false
	}
	req.SuggestedChannels = cleanWindyStringList(req.SuggestedChannels, windyMaxDraftListSize)
	req.RecommendedTools = cleanWindyStringList(req.RecommendedTools, windyMaxDraftListSize)
	return true
}

func cleanWindyStringList(in []string, max int) []string {
	out := make([]string, 0, len(in))
	for _, item := range in {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
		if len(out) >= max {
			break
		}
	}
	return out
}

func (h *Handler) insertAgentDraft(r *http.Request, workspaceID, targetUserID, createdByAgentID, projectID, channelID pgtype.UUID, req CreateAgentDraftRequest) (AgentCreationDraftResponse, error) {
	suggestedChannels, _ := json.Marshal(req.SuggestedChannels)
	recommendedTools, _ := json.Marshal(req.RecommendedTools)
	row := h.DB.QueryRow(r.Context(), `
		INSERT INTO agent_creation_draft (
			workspace_id, created_by_agent_id, target_user_id, name, description,
			instructions, avatar_url, visibility, project_id, channel_id,
			can_execute_code, suggested_channels, recommended_tools
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id, workspace_id, created_by_agent_id, target_user_id, name,
			description, instructions, avatar_url, visibility, project_id, channel_id,
			can_execute_code, suggested_channels, recommended_tools, status,
			used_agent_id, created_at, updated_at, used_at`,
		workspaceID, nullableUUID(createdByAgentID), targetUserID, req.Name, req.Description,
		req.Instructions, ptrToText(req.AvatarURL), req.Visibility, nullableUUID(projectID), nullableUUID(channelID),
		req.CanExecuteCode, suggestedChannels, recommendedTools)
	return scanAgentDraft(row)
}

func scanAgentDraft(row rowScanner) (AgentCreationDraftResponse, error) {
	var id, workspaceID, createdByAgentID, targetUserID, projectID, channelID, usedAgentID pgtype.UUID
	var name, description, instructions, visibility, status string
	var avatarURL pgtype.Text
	var canExecuteCode bool
	var suggestedChannelsRaw, recommendedToolsRaw []byte
	var createdAt, updatedAt, usedAt pgtype.Timestamptz
	if err := row.Scan(
		&id, &workspaceID, &createdByAgentID, &targetUserID, &name,
		&description, &instructions, &avatarURL, &visibility, &projectID, &channelID,
		&canExecuteCode, &suggestedChannelsRaw, &recommendedToolsRaw, &status,
		&usedAgentID, &createdAt, &updatedAt, &usedAt,
	); err != nil {
		return AgentCreationDraftResponse{}, err
	}
	return AgentCreationDraftResponse{
		ID:                uuidToString(id),
		WorkspaceID:       uuidToString(workspaceID),
		CreatedByAgentID:  uuidToPtr(createdByAgentID),
		TargetUserID:      uuidToString(targetUserID),
		Name:              name,
		Description:       description,
		Instructions:      instructions,
		AvatarURL:         textToPtr(avatarURL),
		Visibility:        visibility,
		ProjectID:         uuidToPtr(projectID),
		ChannelID:         uuidToPtr(channelID),
		CanExecuteCode:    canExecuteCode,
		SuggestedChannels: decodeStringList(suggestedChannelsRaw),
		RecommendedTools:  decodeStringList(recommendedToolsRaw),
		Status:            status,
		UsedAgentID:       uuidToPtr(usedAgentID),
		CreatedAt:         timestampToString(createdAt),
		UpdatedAt:         timestampToString(updatedAt),
		UsedAt:            timestampToPtr(usedAt),
	}, nil
}

func decodeStringList(raw []byte) []string {
	var out []string
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func parseAgentActorHeader(raw string) (pgtype.UUID, error) {
	var out pgtype.UUID
	err := out.Scan(strings.TrimSpace(raw))
	return out, err
}

func (h *Handler) MarkAgentDraftUsed(r *http.Request, workspaceID string, draftID pgtype.UUID, usedAgentID pgtype.UUID) {
	if h == nil || h.DB == nil || !draftID.Valid || !usedAgentID.Valid {
		return
	}
	_, _ = h.DB.Exec(r.Context(), `
		UPDATE agent_creation_draft
		SET status = 'used', used_agent_id = $2, used_at = now(), updated_at = now()
		WHERE id = $1 AND workspace_id = $3 AND status = 'draft'`,
		draftID, usedAgentID, parseUUID(workspaceID))
}

func extractDraftID(rawFields map[string]json.RawMessage) pgtype.UUID {
	var empty pgtype.UUID
	raw, ok := rawFields["draft_id"]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return empty
	}
	var draftID string
	if err := json.Unmarshal(raw, &draftID); err != nil || strings.TrimSpace(draftID) == "" {
		return empty
	}
	var out pgtype.UUID
	if err := out.Scan(strings.TrimSpace(draftID)); err != nil {
		return empty
	}
	return out
}
