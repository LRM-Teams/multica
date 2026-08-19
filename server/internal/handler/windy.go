package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/logger"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	windyAgentName                 = "Wendy"
	windyAgentTemplate             = "windy_hr"
	windyDescription               = "Personal HR and team-building lead for organizing Multica agents and teams."
	windyMaxDraftNameLen           = 80
	windyMaxDraftTextLen           = 20000
	windyMaxDraftListSize          = 32
	windyMaxInitialContextFiles    = 16
	windyMaxInitialContextTextLen  = 20000
	windyMaxInitialContextTotalLen = 64000
)

const windyAvatarURL = "https://cdn.leagent.me/agent-avatars/v3/agent-11.png"

const windyInstructions = `Role

You are Wendy, the user's personal HR and team-building lead for Multica. Your mission is to help the user build and organize useful human-agent teams. You focus on HR — recruiting, team design, and workspace setup — not on running the work.

Core Goals

- Do not do concrete implementation work yourself. By default, stay focused on HR and team building. If the runtime brief lists channels where you are a group manager, also carry the manager responsibilities for those channels; that current channel membership role is the only source of this duty.
- In group channels where you are not a manager, reply only to HR/team-building needs: recruiting agents, drafting or creating agent roles, removing/firing agents, team design, permissions/ownership advice, or other personnel/organization recommendations.
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
- If the user asks for one employee, draft one agent. Draft multiple agents only when the user asks for a team or the work clearly needs distinct roles.
- When the scope is unclear, ask whether they want one employee or a small team instead of guessing.
- Let specialization emerge when the user is unsure.
- Use channels for workstreams and threads/tasks for execution.

Agent Recruiting Behavior

When the user describes a goal, prepare human-confirmable agent:create Proposal Messages instead of asking them to manually write prompts. Each proposal contains a permanent Agent name and a short description; the human picks computer/runtime/model and edits instructions in Create Agent Dialog. Choose a short, meaningful lowercase ASCII name with letters, digits, or hyphens that matches the role.

Before preparing, do a light HR intake when important context is missing. Ask 3-6 focused questions about business/project background, goals, inputs/outputs, current workflow, collaborators, permission boundaries, quality bar, and no-go areas. Do not over-interview when the user already gave enough detail.

Hire path:

1. multica action prepare --target <channel> --name <permanent-name> [--description <desc>] --output json
2. Human confirms in CreateAgentDialog.

When the user wants agents in a specific group channel, do not silently create or place them there yourself. After the agents exist, use the Multica CLI to add them explicitly to the channel the user asked for. The command is: multica channel member add --target <channel> <agent> [<agent>...]. Here <channel> is the requested group and <agent> entries are the created agents, usually found by their display names. Only do this when the user explicitly asked for that channel; otherwise leave them unassigned.

Avatar: leave avatar empty on proposals unless product later adds avatar to the action payload. The Multica UI/server assigns a preset on create; humans can change it in the dialog.

Do not silently create agents. Always let the user confirm by clicking a create card or creation action.

Project And Channel Behavior

- For casual chat: suggest a general channel with no project binding.
- For one clear project: suggest a project channel with that project as default.
- For multiple projects: recommend separate project channels unless the user explicitly wants one multi-project room.
- For code tasks: ensure the task has a project, repo, branch/workspace policy, and review gate.
- Wendy is user-scoped. Do not present yourself as a project manager for one project.

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

// windyInstructionsCapabilityMarker detects stale Wendy personas that predate
// channel-member role based manager duties and need a one-shot refresh.
const windyInstructionsCapabilityMarker = "current channel membership role is the only source"

// windyInstructionsAvatarDraftMarker detects Wendy personas that still teach
// retired agent draft create hire path instead of multica action prepare CLI.
const windyInstructionsAvatarDraftMarker = "multica action prepare"

type WindyResponse struct {
	Agent AgentResponse `json:"agent"`
	DMID  string        `json:"dm_id,omitempty"`
}

type AgentCreationDraftResponse struct {
	ID                string            `json:"id"`
	WorkspaceID       string            `json:"workspace_id"`
	CreatedByAgentID  *string           `json:"created_by_agent_id,omitempty"`
	TargetUserID      string            `json:"target_user_id"`
	Name              string            `json:"name"`
	Description       string            `json:"description"`
	Instructions      string            `json:"instructions"`
	AvatarURL         *string           `json:"avatar_url,omitempty"`
	ProjectID         *string           `json:"project_id,omitempty"`
	ChannelID         *string           `json:"channel_id,omitempty"`
	CanExecuteCode    bool              `json:"can_execute_code"`
	SuggestedChannels []string          `json:"suggested_channels"`
	RecommendedTools  []string          `json:"recommended_tools"`
	InitialNotes      map[string]string `json:"initial_notes,omitempty"`
	InitialMemory     map[string]string `json:"initial_memory,omitempty"`
	Status            string            `json:"status"`
	UsedAgentID       *string           `json:"used_agent_id,omitempty"`
	CreatedAt         string            `json:"created_at"`
	UpdatedAt         string            `json:"updated_at"`
	UsedAt            *string           `json:"used_at,omitempty"`
}

type CreateAgentDraftRequest struct {
	Name              string            `json:"name"`
	Description       string            `json:"description"`
	Instructions      string            `json:"instructions"`
	AvatarURL         *string           `json:"avatar_url"`
	ProjectID         *string           `json:"project_id"`
	ChannelID         *string           `json:"channel_id"`
	CanExecuteCode    bool              `json:"can_execute_code"`
	SuggestedChannels []string          `json:"suggested_channels"`
	RecommendedTools  []string          `json:"recommended_tools"`
	InitialNotes      map[string]string `json:"initial_notes"`
	InitialMemory     map[string]string `json:"initial_memory"`
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
	if member.Role != "owner" {
		writeError(w, http.StatusForbidden, "only the workspace owner may set up the onboarding agent")
		return
	}
	// A completed Setup is idempotent even if its Runtime has since gone
	// offline or a retry carries stale form values. The structured binding is
	// the completion record, so return it before validating creation inputs.
	if boundID, err := h.Queries.GetWorkspaceOnboardingAgentID(r.Context(), wsUUID); err == nil && boundID.Valid {
		agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{ID: boundID, WorkspaceID: wsUUID})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load Wendy")
			return
		}
		dmID := ""
		if ch, ok := h.ensureWindyDM(r, workspaceID, parseUUID(userID), agent.ID); ok {
			dmID = ch.ID
		}
		resp := agentToResponse(agent)
		redactAgentResponseForActor(&resp, "member")
		writeJSON(w, http.StatusOK, WindyResponse{Agent: resp, DMID: dmID})
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to inspect Wendy setup")
		return
	}
	var setup struct {
		RuntimeID     string `json:"runtime_id"`
		Model         string `json:"model"`
		ThinkingLevel string `json:"thinking_level"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&setup); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	setup.RuntimeID = strings.TrimSpace(setup.RuntimeID)
	setup.Model = strings.TrimSpace(setup.Model)
	setup.ThinkingLevel = strings.TrimSpace(setup.ThinkingLevel)
	if setup.RuntimeID == "" {
		writeError(w, http.StatusBadRequest, "runtime_id is required")
		return
	}
	if setup.Model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}

	runtime, ok := h.pickWindyRuntime(w, r, wsUUID, setup.RuntimeID)
	if !ok {
		return
	}
	if !agent.IsKnownThinkingValue(runtime.Provider, setup.ThinkingLevel) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("thinking_level %q is not a recognised value for runtime %q", setup.ThinkingLevel, runtime.Provider))
		return
	}
	agent, created, err := h.provisionOnboardingAgent(r.Context(), wsUUID, parseUUID(userID), runtime, setup.Model, setup.ThinkingLevel)
	if err != nil {
		slog.Warn("ensure Wendy failed", append(logger.RequestAttrs(r), "error", err, "workspace_id", workspaceID)...)
		writeError(w, http.StatusInternalServerError, "failed to create Wendy")
		return
	}
	if created {
		h.refreshAgentSkillSuggestions(r.Context(), agent)
		if runtime.Status == "online" {
			h.TaskService.ReconcileAgentStatus(r.Context(), agent.ID)
			agent, _ = h.Queries.GetAgent(r.Context(), agent.ID)
		}
		resp := agentToResponse(agent)
		h.publishAgentVisibilityEvent(protocol.EventAgentCreated, workspaceID, "member", userID, agent, map[string]any{"agent": broadcastAgentResponse(resp)})
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

// provisionOnboardingAgent is the setup commit boundary. The workspace row lock
// serializes retries while the shared Agent creation path creates the Agent,
// then this role-specific layer binds it as onboarding and writes welcome
// messages. The Agent's name is presentation only and never identifies the
// onboarding role.
func (h *Handler) provisionOnboardingAgent(ctx context.Context, workspaceID, creatorID pgtype.UUID, runtime db.AgentRuntime, model string, thinkingLevel string) (db.Agent, bool, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return db.Agent{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := db.New(tx)

	var boundID pgtype.UUID
	if err := tx.QueryRow(ctx, `SELECT onboarding_agent_id FROM workspace WHERE id = $1 FOR UPDATE`, workspaceID).Scan(&boundID); err != nil {
		return db.Agent{}, false, err
	}
	if boundID.Valid {
		agent, err := qtx.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: boundID, WorkspaceID: workspaceID})
		if err != nil {
			return db.Agent{}, false, err
		}
		if err := ensureAgentGeneralMembership(ctx, tx, workspaceID, agent.ID); err != nil {
			return db.Agent{}, false, err
		}
		if err := h.ensureOnboardingAgentWelcomeTx(ctx, tx, workspaceID, agent.ID); err != nil {
			return db.Agent{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return db.Agent{}, false, err
		}
		return agent, false, nil
	}

	thinking := pgtype.Text{}
	if thinkingLevel != "" {
		thinking = pgtype.Text{String: thinkingLevel, Valid: true}
	}
	agent, err := h.createAgentManagedTx(ctx, tx, qtx, workspaceID, db.CreateAgentParams{
		WorkspaceID: workspaceID, Description: windyDescription, Instructions: windyInstructions,
		AvatarUrl: pgtype.Text{String: windyAvatarURL, Valid: true}, AvatarSource: agentAvatarSourceAssigned,
		RuntimeMode: runtime.RuntimeMode, RuntimeConfig: []byte("{}"), RuntimeID: runtime.ID,
		MaxConcurrentTasks: 6, OwnerID: creatorID, CustomEnv: []byte("{}"), CustomArgs: []byte("[]"),
		Model: pgtype.Text{String: model, Valid: true}, ThinkingLevel: thinking,
	}, windyAgentName)
	if err != nil {
		return db.Agent{}, false, err
	}
	if err := qtx.SetWorkspaceOnboardingAgentID(ctx, db.SetWorkspaceOnboardingAgentIDParams{ID: workspaceID, OnboardingAgentID: agent.ID}); err != nil {
		return db.Agent{}, false, err
	}
	if err := h.ensureOnboardingAgentWelcomeTx(ctx, tx, workspaceID, agent.ID); err != nil {
		return db.Agent{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.Agent{}, false, err
	}
	return agent, true, nil
}

func (h *Handler) pickWindyRuntime(w http.ResponseWriter, r *http.Request, workspaceID pgtype.UUID, runtimeID string) (db.AgentRuntime, bool) {
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
		// pickWindyRuntime is only called after EnsureWindy already resolved
		// the member; re-load for the visibility gate so a private foreign
		// runtime cannot be forced through an explicit runtime_id.
		userID, ok := requireUserID(w, r)
		if !ok {
			return db.AgentRuntime{}, false
		}
		member, err := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{
			UserID:      parseUUID(userID),
			WorkspaceID: workspaceID,
		})
		if err != nil {
			writeError(w, http.StatusForbidden, "not a member of this workspace")
			return db.AgentRuntime{}, false
		}
		if !canUseRuntimeForAgent(member, runtime) {
			writeError(w, http.StatusForbidden, "this runtime is private; only its owner or a workspace admin can create agents on it")
			return db.AgentRuntime{}, false
		}
		// Task #123 L1: explicit runtime_id still must be heartbeat-fresh.
		// Otherwise a client can bind Wendy to a dead "online" row for ~150s.
		if !runtimeIsPickableOnline(runtime, time.Now()) {
			writeError(w, http.StatusUnprocessableEntity, "runtime is offline or heartbeat is stale")
			return db.AgentRuntime{}, false
		}
		return runtime, true
	}

	writeError(w, http.StatusBadRequest, "runtime_id is required")
	return db.AgentRuntime{}, false
}

func (h *Handler) ensureWindyDM(r *http.Request, workspaceID string, userID, windyID pgtype.UUID) (ChannelResponse, bool) {
	canonical := dmCanonicalName("user", uuidToString(userID), "agent", uuidToString(windyID))
	if ch, found := h.findDMChannel(r.Context(), workspaceID, canonical); found {
		h.clearDMPeerHidden(r.Context(), workspaceID, uuidToString(userID), dmPeerRef{Type: "agent", ID: windyID})
		return ch, true
	}
	return h.createDMChannel(r.Context(), nil, workspaceID, uuidToString(userID), canonical, []dmMember{{memberType: "user", memberID: userID}, {memberType: "agent", memberID: windyID}})
}

var onboardingWelcomeV1 = []string{
	"Hi — I’m your Workspace Onboarding Agent. I can help turn the work you describe into a clear team of Agents.",
	"Tell me what you’re working on. I’ll discuss the role with you and prepare a Hiring Proposal for an Owner or Admin to review.",
}

func (h *Handler) ensureOnboardingAgentWelcomeTx(ctx context.Context, tx pgx.Tx, workspaceID, agentID pgtype.UUID) error {
	var generalID pgtype.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM channel WHERE workspace_id = $1 AND system_key = 'general'`, workspaceID).Scan(&generalID); err != nil {
		return err
	}
	var authorName string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(NULLIF(display_name, ''), name) FROM agent WHERE id = $1`, agentID).Scan(&authorName); err != nil {
		return err
	}
	for _, content := range onboardingWelcomeV1 {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (
				SELECT 1 FROM channel_message
				WHERE channel_id = $1 AND author_type = 'agent' AND author_id = $2
				  AND source = 'multica' AND content = $3 AND deleted_at IS NULL
			)`, generalID, agentID, content).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		// These server-owned templates contain no user-authored mention or
		// channel-reference syntax. Keep their insertion on the transaction's
		// connection: finalizeAgentChannelMessage queries through h.DB and can
		// deadlock with concurrent setup transactions waiting on the workspace
		// row lock when the connection pool is small.
		if _, err := insertChannelMessageWithPartsExec(ctx, tx, generalID, workspaceID, "agent", agentID, authorName, content, nil, "multica", nil, nil, pgtype.UUID{}, pgtype.UUID{}, nil, pgtype.UUID{}, nil, 0, channelMessageKindHint{}); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) GetAgentDraft(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
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
			description, instructions, avatar_url, project_id, channel_id,
			can_execute_code, suggested_channels, recommended_tools, initial_notes,
			initial_memory, status, used_agent_id, created_at, updated_at, used_at
		FROM agent_creation_draft
		WHERE id = $1 AND workspace_id = $2`,
		draftID, wsUUID)
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
	// Hire hard-cut (Frank/Parker): agents must use agent:create action prepare.
	// Human FE may still create drafts temporarily for URL-only multica:// links.
	if strings.TrimSpace(r.Header.Get("X-Agent-ID")) != "" {
		writeCodedError(w, http.StatusGone, "agent_draft_create_retired",
			"agent draft create is retired; use POST /api/agent/actions/prepare with action_type=agent:create")
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
	targetUserID := h.resolveAgentDraftTargetUserID(r, wsUUID, parseUUID(userID))

	draft, err := h.insertAgentDraft(r, wsUUID, targetUserID, createdBy, projectID, channelID, req)
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
	req.SuggestedChannels = cleanWindyStringList(req.SuggestedChannels, windyMaxDraftListSize)
	req.RecommendedTools = cleanWindyStringList(req.RecommendedTools, windyMaxDraftListSize)
	req.InitialNotes = cleanInitialContextMap(req.InitialNotes, allowedInitialNoteSeedPath)
	req.InitialMemory = cleanInitialContextMap(req.InitialMemory, allowedInitialMemorySeedPath)
	return true
}

func cleanInitialContextMap(in map[string]string, allowed func(string) bool) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := map[string]string{}
	total := 0
	keys := make([]string, 0, len(in))
	for key := range in {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		path := normalizeInitialContextPath(key)
		content := strings.TrimSpace(in[key])
		if path == "" || content == "" || !allowed(path) {
			continue
		}
		if utf8.RuneCountInString(content) > windyMaxInitialContextTextLen {
			content = string([]rune(content)[:windyMaxInitialContextTextLen])
		}
		total += utf8.RuneCountInString(content)
		if total > windyMaxInitialContextTotalLen {
			break
		}
		out[path] = content
		if len(out) >= windyMaxInitialContextFiles {
			break
		}
	}
	return out
}

func normalizeInitialContextPath(raw string) string {
	path := filepath.ToSlash(strings.TrimSpace(raw))
	path = strings.TrimPrefix(path, "/")
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." || strings.HasPrefix(path, "../") || path == ".." {
		return ""
	}
	if strings.HasPrefix(path, "notes/") || strings.HasPrefix(path, "memory/") {
		return path
	}
	if allowedInitialNoteSeedPath("notes/" + path) {
		return "notes/" + path
	}
	if allowedInitialMemorySeedPath("memory/" + path) {
		return "memory/" + path
	}
	return path
}

func allowedInitialNoteSeedPath(path string) bool {
	switch path {
	case "notes/agents.md", "notes/channels.md", "notes/project-map.md", "notes/relationship-map.md", "notes/role-playbook.md", "notes/work-log.md", "notes/decisions.md":
		return true
	default:
		return false
	}
}

func allowedInitialMemorySeedPath(path string) bool {
	switch path {
	case "memory/MEMORY.md", "memory/STATE.md":
		return true
	default:
		return false
	}
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
			instructions, avatar_url, project_id, channel_id,
			can_execute_code, suggested_channels, recommended_tools, initial_notes,
			initial_memory
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id, workspace_id, created_by_agent_id, target_user_id, name,
			description, instructions, avatar_url, project_id, channel_id,
			can_execute_code, suggested_channels, recommended_tools, initial_notes,
			initial_memory, status, used_agent_id, created_at, updated_at, used_at`,
		workspaceID, nullableUUID(createdByAgentID), targetUserID, req.Name, req.Description,
		req.Instructions, ptrToText(req.AvatarURL), nullableUUID(projectID), nullableUUID(channelID),
		req.CanExecuteCode, suggestedChannels, recommendedTools, marshalStringMap(req.InitialNotes), marshalStringMap(req.InitialMemory))
	return scanAgentDraft(row)
}

func scanAgentDraft(row rowScanner) (AgentCreationDraftResponse, error) {
	var id, workspaceID, createdByAgentID, targetUserID, projectID, channelID, usedAgentID pgtype.UUID
	var name, description, instructions, status string
	var avatarURL pgtype.Text
	var canExecuteCode bool
	var suggestedChannelsRaw, recommendedToolsRaw, initialNotesRaw, initialMemoryRaw []byte
	var createdAt, updatedAt, usedAt pgtype.Timestamptz
	if err := row.Scan(
		&id, &workspaceID, &createdByAgentID, &targetUserID, &name,
		&description, &instructions, &avatarURL, &projectID, &channelID,
		&canExecuteCode, &suggestedChannelsRaw, &recommendedToolsRaw, &initialNotesRaw,
		&initialMemoryRaw, &status, &usedAgentID, &createdAt, &updatedAt, &usedAt,
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
		ProjectID:         uuidToPtr(projectID),
		ChannelID:         uuidToPtr(channelID),
		CanExecuteCode:    canExecuteCode,
		SuggestedChannels: decodeStringList(suggestedChannelsRaw),
		RecommendedTools:  decodeStringList(recommendedToolsRaw),
		InitialNotes:      decodeStringMap(initialNotesRaw),
		InitialMemory:     decodeStringMap(initialMemoryRaw),
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

func decodeStringMap(raw []byte) map[string]string {
	var out map[string]string
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	if out == nil {
		out = map[string]string{}
	}
	return out
}

func marshalStringMap(in map[string]string) []byte {
	if in == nil {
		in = map[string]string{}
	}
	out, _ := json.Marshal(in)
	return out
}

func parseAgentActorHeader(raw string) (pgtype.UUID, error) {
	var out pgtype.UUID
	err := out.Scan(strings.TrimSpace(raw))
	return out, err
}

func (h *Handler) resolveAgentDraftTargetUserID(r *http.Request, workspaceID, fallbackUserID pgtype.UUID) pgtype.UUID {
	if h == nil || h.DB == nil || !fallbackUserID.Valid {
		return fallbackUserID
	}
	switch r.Header.Get("X-Actor-Source") {
	case "task_token":
		if target, ok := h.agentTaskInitiatorUserID(r, workspaceID); ok {
			return target
		}
	case "agent_inbox_token", "agent_credential":
		if target, ok := h.agentInboxInitiatorUserID(r, workspaceID); ok {
			return target
		}
	}
	return fallbackUserID
}

func (h *Handler) agentTaskInitiatorUserID(r *http.Request, workspaceID pgtype.UUID) (pgtype.UUID, bool) {
	var empty pgtype.UUID
	taskID, err := parseAgentActorHeader(r.Header.Get("X-Task-ID"))
	if err != nil || !taskID.Valid || !workspaceID.Valid {
		return empty, false
	}
	var target pgtype.UUID
	// Prefer turn/human initiator; if the wake has no human author (reminder
	// re-fire, agent-authored anchor, channel-only), fall back to the agent
	// owner so the Agent can use its ordinary Reminder capability.
	err = h.DB.QueryRow(r.Context(), `
		WITH task AS (
			SELECT t.initiator_user_id,
			       c.author_type AS comment_author_type,
			       c.author_id AS comment_author_id,
			       a.owner_id AS agent_owner_id
			FROM agent_inbox_event t
			JOIN agent a ON a.id = t.agent_id AND a.workspace_id = $2 AND a.archived_at IS NULL
			LEFT JOIN comment c ON c.id = t.trigger_comment_id AND c.workspace_id = $2
			WHERE t.id = $1
		)
		SELECT candidate.user_id
		FROM task
		CROSS JOIN LATERAL (
			SELECT COALESCE(
				task.initiator_user_id,
				CASE WHEN task.comment_author_type IN ('member', 'user') THEN task.comment_author_id END,
				task.agent_owner_id,
				(SELECT m.user_id FROM member m
				 WHERE m.workspace_id = $2 AND m.role = 'owner'
				 ORDER BY m.created_at ASC NULLS LAST, m.user_id ASC
				 LIMIT 1)
			) AS user_id
		) candidate
		JOIN member m ON m.workspace_id = $2 AND m.user_id = candidate.user_id
		WHERE candidate.user_id IS NOT NULL
		LIMIT 1`, taskID, workspaceID).Scan(&target)
	return target, err == nil && target.Valid
}

func (h *Handler) agentInboxInitiatorUserID(r *http.Request, workspaceID pgtype.UUID) (pgtype.UUID, bool) {
	var empty pgtype.UUID
	eventID, err := parseAgentActorHeader(r.Header.Get("X-Agent-Inbox-Event-ID"))
	if err != nil || !eventID.Valid || !workspaceID.Valid {
		return empty, false
	}
	agentID, err := parseAgentActorHeader(r.Header.Get("X-Agent-ID"))
	if err != nil || !agentID.Valid {
		return empty, false
	}
	var target pgtype.UUID
	// Prefer human source-message author / chat creator; fall back to agent
	// owner so an ordinary Reminder can be scheduled without a human wake
	// author (reminder re-anchor 403: "reminder initiator is not available").
	err = h.DB.QueryRow(r.Context(), `
		WITH inbox AS (
			SELECT msg.author_type AS message_author_type,
				msg.author_id AS message_author_id,
				session.creator_id AS session_creator_id,
				a.owner_id AS agent_owner_id
			FROM agent_inbox_event e
			JOIN agent a ON a.id = e.agent_id AND a.workspace_id = e.workspace_id AND a.archived_at IS NULL
			LEFT JOIN channel_message msg ON msg.id = e.source_message_id AND msg.workspace_id = e.workspace_id
			LEFT JOIN chat_session session ON session.id = e.chat_session_id
			WHERE e.id = $1 AND e.workspace_id = $2 AND e.agent_id = $3
		)
		SELECT candidate.user_id
		FROM inbox
		CROSS JOIN LATERAL (
			SELECT COALESCE(
				CASE WHEN inbox.message_author_type IN ('user', 'member') THEN inbox.message_author_id END,
				inbox.session_creator_id,
				inbox.agent_owner_id,
				(SELECT m.user_id FROM member m
				 WHERE m.workspace_id = $2 AND m.role = 'owner'
				 ORDER BY m.created_at ASC NULLS LAST, m.user_id ASC
				 LIMIT 1)
			) AS user_id
		) candidate
		JOIN member m ON m.workspace_id = $2 AND m.user_id = candidate.user_id
		WHERE candidate.user_id IS NOT NULL
		LIMIT 1`, eventID, workspaceID, agentID).Scan(&target)
	return target, err == nil && target.Valid
}

func (h *Handler) MarkAgentDraftUsed(r *http.Request, workspaceID, _targetUserID string, draftID pgtype.UUID, usedAgentID pgtype.UUID) {
	if h == nil || h.DB == nil || !draftID.Valid || !usedAgentID.Valid {
		return
	}
	// Match GetAgentDraft / loadAgentDraftForCreate: any workspace member who
	// can materialize the hiring card may consume it. Do not gate on
	// target_user_id — Wendy may stamp the wrong target when inbox initiator
	// resolution fails (LRM-444).
	_, _ = h.DB.Exec(r.Context(), `
		UPDATE agent_creation_draft
		SET status = 'used', used_agent_id = $2, used_at = now(), updated_at = now()
		WHERE id = $1 AND workspace_id = $3 AND status = 'draft'`,
		draftID, usedAgentID, parseUUID(workspaceID))
}

type agentDraftSeed struct {
	InitialNotes  map[string]string
	InitialMemory map[string]string
	AvatarURL     pgtype.Text
}

// agentDraftLookup codes for CreateAgent(draft_id=...).
const (
	agentDraftLookupOK          = ""
	agentDraftLookupNotFound    = "agent_draft_not_found"
	agentDraftLookupAlreadyUsed = "agent_draft_already_used"
)

// loadAgentDraftForCreate loads a still-open hiring draft by id+workspace.
// Intentionally does NOT filter by target_user_id: GetAgentDraft is already
// workspace-visible, and Create must match that contract (LRM-444).
func (h *Handler) loadAgentDraftForCreate(r *http.Request, workspaceID string, draftID pgtype.UUID) (agentDraftSeed, string) {
	if h == nil || h.DB == nil || !draftID.Valid || strings.TrimSpace(workspaceID) == "" {
		return agentDraftSeed{}, agentDraftLookupNotFound
	}
	var initialNotesRaw, initialMemoryRaw []byte
	var avatarURL pgtype.Text
	var status string
	err := h.DB.QueryRow(r.Context(), `
		SELECT initial_notes, initial_memory, avatar_url, status
		FROM agent_creation_draft
		WHERE id = $1 AND workspace_id = $2`,
		draftID, parseUUID(workspaceID)).Scan(&initialNotesRaw, &initialMemoryRaw, &avatarURL, &status)
	if err != nil {
		return agentDraftSeed{}, agentDraftLookupNotFound
	}
	if status != "draft" {
		return agentDraftSeed{}, agentDraftLookupAlreadyUsed
	}
	return agentDraftSeed{
		InitialNotes:  decodeStringMap(initialNotesRaw),
		InitialMemory: decodeStringMap(initialMemoryRaw),
		AvatarURL:     avatarURL,
	}, agentDraftLookupOK
}

// loadAgentDraftSeed is kept for call sites that only need the happy-path seed.
// Prefer loadAgentDraftForCreate when callers must distinguish used vs missing.
func (h *Handler) loadAgentDraftSeed(r *http.Request, workspaceID, _targetUserID string, draftID pgtype.UUID) (agentDraftSeed, bool) {
	seed, code := h.loadAgentDraftForCreate(r, workspaceID, draftID)
	return seed, code == agentDraftLookupOK
}

func extractDraftID(rawFields map[string]json.RawMessage) (pgtype.UUID, bool, error) {
	var empty pgtype.UUID
	raw, ok := rawFields["draft_id"]
	if !ok {
		return empty, false, nil
	}
	if len(raw) == 0 || string(raw) == "null" {
		return empty, true, fmt.Errorf("draft_id must be a UUID")
	}
	var draftID string
	if err := json.Unmarshal(raw, &draftID); err != nil || strings.TrimSpace(draftID) == "" {
		return empty, true, fmt.Errorf("draft_id must be a UUID")
	}
	var out pgtype.UUID
	if err := out.Scan(strings.TrimSpace(draftID)); err != nil {
		return empty, true, fmt.Errorf("draft_id must be a UUID")
	}
	return out, true, nil
}
