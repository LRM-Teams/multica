package handler

import (
	"context"
	"encoding/json"
	"errors"
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
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	windyAgentName                 = "Wendy"
	legacyWindyAgentName           = "Windy"
	legacyJoeAgentName             = "Joe"
	windyAgentTemplate             = "windy_hr"
	windyDescription               = "Personal HR and team-building lead for organizing Multica agents and teams."
	windyMaxDraftNameLen           = 80
	windyMaxDraftTextLen           = 20000
	windyMaxDraftListSize          = 32
	windyMaxInitialContextFiles    = 16
	windyMaxInitialContextTextLen  = 20000
	windyMaxInitialContextTotalLen = 64000
)

const windyAvatarURL = "/agent-avatars/human-11.jpg"

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

When the user describes a goal, prepare human-confirmable agent:create Proposal Messages instead of asking them to manually write prompts. Each proposal is name + short description only; the human picks computer/runtime/model and edits instructions in Create Agent Dialog.

Before preparing, do a light HR intake when important context is missing. Ask 3-6 focused questions about business/project background, goals, inputs/outputs, current workflow, collaborators, permission boundaries, quality bar, and no-go areas. Do not over-interview when the user already gave enough detail.

Hire path:

1. multica action prepare --target <channel> --name <name> [--description <desc>] --output json
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
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}

	runtime, ok := h.pickWindyRuntime(w, r, wsUUID, parseUUID(userID))
	if !ok {
		return
	}
	agent, created, err := h.ensureWindyAgent(r, wsUUID, runtime)
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
		// Task #123 L1: explicit runtime_id still must be heartbeat-fresh.
		// Otherwise a client can bind Wendy to a dead "online" row for ~150s.
		if !runtimeIsPickableOnline(runtime, time.Now()) {
			writeError(w, http.StatusUnprocessableEntity, "runtime is offline or heartbeat is stale")
			return db.AgentRuntime{}, false
		}
		return runtime, true
	}

	runtimes, err := h.Queries.ListAgentRuntimes(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list runtimes")
		return db.AgentRuntime{}, false
	}
	if len(runtimes) == 0 {
		writeError(w, http.StatusBadRequest, "connect a runtime before creating Wendy")
		return db.AgentRuntime{}, false
	}
	now := time.Now()
	for _, rt := range runtimes {
		if rt.OwnerID.Valid && uuidToString(rt.OwnerID) == uuidToString(userID) && runtimeIsPickableOnline(rt, now) {
			return rt, true
		}
	}
	for _, rt := range runtimes {
		if runtimeIsPickableOnline(rt, now) {
			return rt, true
		}
	}
	// Fail closed: do not fall back to a ghost first row.
	writeError(w, http.StatusUnprocessableEntity, "no online runtime with a fresh heartbeat; start or reconnect a machine first")
	return db.AgentRuntime{}, false
}

// ensureWindyAgent resolves the workspace's single onboarding agent via
// workspace.onboarding_agent_id, the persisted, name-independent binding
// (task #902 — authorization/lookup must never key off display name again).
//
// binding set   -> reuse the bound agent (restore/refresh as needed).
// binding unset -> look for the workspace owner's existing legacy-named
//
//	agent (Wendy/Windy/Joe) and adopt it; otherwise create a
//	fresh one. Either way, bind it with a conditional UPDATE
//	(SetWorkspaceOnboardingAgentID mirrors the
//	SetDefaultSelfPlayEnv "first writer wins" pattern): if a
//	concurrent ensure() already bound a different agent, that
//	binding wins, and an agent this call created (but did not
//	adopt) is archived so no orphan/duplicate is left behind.
//
// This intentionally does not touch or backfill any other workspace: an old
// workspace's pre-existing Wendy/Windy/Joe rows are left exactly as they are
// until (and unless) someone triggers ensure() there.
func (h *Handler) ensureWindyAgent(r *http.Request, workspaceID pgtype.UUID, runtime db.AgentRuntime) (db.Agent, bool, error) {
	ctx := r.Context()

	if boundID, err := h.Queries.GetWorkspaceOnboardingAgentID(ctx, workspaceID); err != nil {
		return db.Agent{}, false, err
	} else if boundID.Valid {
		agent, err := h.Queries.GetAgent(ctx, boundID)
		if err == nil {
			updated, err := h.restoreAndNormalizeWindyAgent(r, agent)
			if err != nil {
				return db.Agent{}, false, err
			}
			return updated, false, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return db.Agent{}, false, err
		}
		// The bound agent row is gone (should only happen via a direct DB
		// delete bypassing ON DELETE SET NULL timing); fall through and
		// re-bind below.
	}

	ownerID, err := h.Queries.GetFirstWorkspaceOwnerUserID(ctx, workspaceID)
	if err != nil {
		return db.Agent{}, false, err
	}

	// isWindyAgentName/selectOwnedWindyAgent judge by display name — this is
	// otherwise a banned pattern (never infer identity/role from a display
	// name; task #902 exists because a permission check did exactly that).
	// The one-time adoption below is the sole legitimate exception: with no
	// binding yet, there is no other signal to find a pre-existing Wendy the
	// owner already has. The moment a binding exists (the `if boundID.Valid`
	// branch above), lookups are by workspace.onboarding_agent_id only, and
	// name is never consulted again. Do not use this as a precedent for a
	// new name-based check elsewhere.
	agents, err := h.Queries.ListAllAgents(ctx, workspaceID)
	if err != nil {
		return db.Agent{}, false, err
	}
	candidate, adopted := selectOwnedWindyAgent(agents, ownerID)

	mine := candidate
	if !adopted {
		created, err := h.createAgentWithIdentity(ctx, h.Queries, db.CreateAgentParams{
			WorkspaceID:        workspaceID,
			Description:        windyDescription,
			Instructions:       windyInstructions,
			AvatarUrl:          pgtype.Text{String: windyAvatarURL, Valid: true},
			AvatarSource:       agentAvatarSourceAssigned,
			RuntimeMode:        runtime.RuntimeMode,
			RuntimeConfig:      []byte("{}"),
			RuntimeID:          runtime.ID,
			MaxConcurrentTasks: 6,
			OwnerID:            ownerID,
			CustomEnv:          []byte("{}"),
			CustomArgs:         []byte("[]"),
			McpConfig:          nil,
			Model:              pgTextModelForRuntime(runtime.Provider),
			ThinkingLevel:      pgtype.Text{},
		}, windyAgentName, windyAgentName)
		if err != nil {
			return db.Agent{}, false, err
		}
		mine = created
	}

	if err := h.Queries.SetWorkspaceOnboardingAgentID(ctx, db.SetWorkspaceOnboardingAgentIDParams{
		ID:                workspaceID,
		OnboardingAgentID: mine.ID,
	}); err != nil {
		return db.Agent{}, false, err
	}
	winnerID, err := h.Queries.GetWorkspaceOnboardingAgentID(ctx, workspaceID)
	if err != nil {
		return db.Agent{}, false, err
	}
	if !winnerID.Valid || uuidToString(winnerID) != uuidToString(mine.ID) {
		// Lost the race to a concurrent ensure() call. If we created a fresh
		// agent for this attempt, archive it rather than leaving an orphan
		// duplicate (the bug #902 exists to fix). An adopted candidate is a
		// pre-existing agent and is left untouched either way.
		if !adopted {
			if _, archiveErr := h.Queries.ArchiveAgent(ctx, db.ArchiveAgentParams{ID: mine.ID, ArchivedBy: ownerID}); archiveErr != nil {
				slog.Warn("archive losing onboarding agent failed", append(logger.RequestAttrs(r), "error", archiveErr, "workspace_id", uuidToString(workspaceID), "agent_id", uuidToString(mine.ID))...)
			}
		}
		winner, err := h.Queries.GetAgent(ctx, winnerID)
		if err != nil {
			return db.Agent{}, false, err
		}
		updated, err := h.restoreAndNormalizeWindyAgent(r, winner)
		if err != nil {
			return db.Agent{}, false, err
		}
		return updated, false, nil
	}

	if adopted {
		updated, err := h.restoreAndNormalizeWindyAgent(r, mine)
		if err != nil {
			return db.Agent{}, false, err
		}
		return updated, false, nil
	}
	return mine, true, nil
}

func selectOwnedWindyAgent(agents []db.Agent, userID pgtype.UUID) (db.Agent, bool) {
	var selected db.Agent
	found := false
	for _, agent := range agents {
		if !isOwnedWindyAgent(agent, userID) {
			continue
		}
		if !found || preferWindyAgent(agent, selected) {
			selected = agent
			found = true
		}
	}
	return selected, found
}

func isOwnedWindyAgent(agent db.Agent, userID pgtype.UUID) bool {
	if !agent.OwnerID.Valid || uuidToString(agent.OwnerID) != uuidToString(userID) {
		return false
	}
	return isWindyAgentName(agentDisplayName(agent))
}

func isWindyAgentName(name string) bool {
	switch name {
	case windyAgentName, legacyWindyAgentName, legacyJoeAgentName:
		return true
	default:
		return false
	}
}

func preferWindyAgent(candidate, current db.Agent) bool {
	if candidate.ArchivedAt.Valid != current.ArchivedAt.Valid {
		return !candidate.ArchivedAt.Valid
	}
	if candidate.RuntimeID.Valid != current.RuntimeID.Valid {
		return candidate.RuntimeID.Valid
	}
	candidateIsWendy := agentDisplayName(candidate) == windyAgentName
	currentIsWendy := agentDisplayName(current) == windyAgentName
	if candidateIsWendy != currentIsWendy {
		return candidateIsWendy
	}
	if candidate.UpdatedAt.Valid && current.UpdatedAt.Valid && !candidate.UpdatedAt.Time.Equal(current.UpdatedAt.Time) {
		return candidate.UpdatedAt.Time.After(current.UpdatedAt.Time)
	}
	if candidate.CreatedAt.Valid && current.CreatedAt.Valid && !candidate.CreatedAt.Time.Equal(current.CreatedAt.Time) {
		return candidate.CreatedAt.Time.After(current.CreatedAt.Time)
	}
	return uuidToString(candidate.ID) < uuidToString(current.ID)
}

func (h *Handler) restoreAndNormalizeWindyAgent(r *http.Request, agent db.Agent) (db.Agent, error) {
	updated := agent
	restored := false
	if agent.ArchivedAt.Valid {
		var err error
		updated, err = h.Queries.RestoreAgent(r.Context(), agent.ID)
		if err != nil {
			return db.Agent{}, err
		}
		restored = true
	}
	if agentDisplayName(updated) != windyAgentName {
		normalized, err := h.normalizeWindyAgent(r, updated)
		if err != nil {
			return db.Agent{}, err
		}
		updated = normalized
	}
	refreshed, err := h.refreshWindyInstructionsIfStale(r, updated)
	if err != nil {
		return db.Agent{}, err
	}
	updated = refreshed
	updated, err = h.ensureWindyAgentModel(r.Context(), updated)
	if err != nil {
		return db.Agent{}, err
	}
	if restored {
		resp := agentToResponse(updated)
		h.publish(protocol.EventAgentStatus, uuidToString(updated.WorkspaceID), "member", requestUserID(r), map[string]any{"agent": broadcastAgentResponse(resp)})
		if updated.RuntimeID.Valid {
			h.projectReminderOwnerStart(r.Context(), uuidToString(updated.ID), uuidToString(updated.RuntimeID))
		}
	}
	return updated, nil
}

func (h *Handler) ensureWindyAgentModel(ctx context.Context, agent db.Agent) (db.Agent, error) {
	if strings.TrimSpace(agent.Model.String) != "" {
		return agent, nil
	}
	if !agent.RuntimeID.Valid {
		return agent, fmt.Errorf("wendy agent %s has no runtime for model backfill", uuidToString(agent.ID))
	}
	runtime, err := h.Queries.GetAgentRuntime(ctx, agent.RuntimeID)
	if err != nil {
		return db.Agent{}, err
	}
	return ensureAgentHasExplicitModel(ctx, h.Queries, agent, runtime.Provider)
}

func (h *Handler) refreshWindyInstructionsIfStale(r *http.Request, agent db.Agent) (db.Agent, error) {
	if strings.Contains(agent.Instructions, windyInstructionsCapabilityMarker) &&
		strings.Contains(agent.Instructions, windyInstructionsAvatarDraftMarker) {
		return agent, nil
	}
	updated, err := h.Queries.UpdateAgent(r.Context(), db.UpdateAgentParams{
		ID:           agent.ID,
		Instructions: pgtype.Text{String: windyInstructions, Valid: true},
		Description:  pgtype.Text{String: windyDescription, Valid: true},
	})
	if err != nil {
		return db.Agent{}, err
	}
	resp := agentToResponse(updated)
	h.publishAgentVisibilityEvent(protocol.EventAgentStatus, uuidToString(updated.WorkspaceID), "member", requestUserID(r), updated, map[string]any{"agent": broadcastAgentResponse(resp)})
	return updated, nil
}

func (h *Handler) normalizeWindyAgent(r *http.Request, agent db.Agent) (db.Agent, error) {
	updated, err := h.Queries.UpdateAgent(r.Context(), db.UpdateAgentParams{
		ID:          agent.ID,
		DisplayName: pgtype.Text{String: windyAgentName, Valid: true},
	})
	if err != nil {
		return db.Agent{}, err
	}
	resp := agentToResponse(updated)
	h.publishAgentVisibilityEvent(protocol.EventAgentStatus, uuidToString(updated.WorkspaceID), "member", requestUserID(r), updated, map[string]any{"agent": broadcastAgentResponse(resp)})
	return updated, nil
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
	// owner so managers can re-schedule patrols (Beckham 2026-08-04 403).
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
	// owner so channel manager patrols can schedule without a human wake
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
