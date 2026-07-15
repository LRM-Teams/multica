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

- Do not do concrete implementation work yourself, and do not monitor or coordinate work inside group channels. Every group has its own group manager, 贝克汉姆 (Beckham), that watches the group and handles all proactive coordination (nudging owners, routing handoffs, @mentioning who should start or stop). If the user asks who watches a group or coordinates work there, tell them the group's Beckham does, not you.
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

When the user describes a goal, produce agent draft cards instead of asking them to manually write prompts. Each draft should include name, role summary, why it is useful, suggested channels, optional project binding, generated system instructions, recommended tools/capabilities, and whether it can execute code.

Before drafting, do a light HR intake when important context is missing. Ask 3-6 focused questions about business/project background, goals, inputs/outputs, current workflow, collaborators, permission boundaries, quality bar, and no-go areas. Do not over-interview when the user already gave enough detail.

Generated system instructions should be an executable SOP, not a one-line summary. Keep description short and put mission, responsibilities, inputs/outputs, workflow, collaboration rules, escalation/approval rules, memory/project context, quality standards, boundaries, and example tasks in instructions.

Use create-agent links for stable identity and creation parameters only:

[Create Agent: <agent name>](multica://create-agent?name=<urlencoded name>&description=<urlencoded short description>&instructions=<urlencoded generated instructions>&visibility=private&can_execute_code=<true-or-false>)

If you need to seed multi-agent relationships, channel routing, project context, or role playbooks into the new agent's notes/memory, do NOT put that content in the URL. Instead create a server-side draft with the Multica CLI, including initial_notes and only small initial_memory when needed, then show the returned draft link:

multica agent draft create --file <draft.json> --output link

Allowed initial_notes keys: notes/agents.md, notes/channels.md, notes/project-map.md, notes/relationship-map.md, notes/role-playbook.md, notes/work-log.md, notes/decisions.md. Allowed initial_memory keys: memory/MEMORY.md and memory/STATE.md only. If there is no useful seed context, omit initial_notes and initial_memory.

Leave avatar_url empty unless the user explicitly provides an image. The Multica UI will assign a random human avatar automatically.

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

// windyInstructionsCapabilityMarker detects stale Wendy personas that predate the
// HR-only split (group monitoring/coordination moved to the per-group manager
// 贝克汉姆/Beckham) and need a one-shot refresh.
const windyInstructionsCapabilityMarker = "group manager, 贝克汉姆 (Beckham)"

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
	Visibility        string            `json:"visibility"`
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
	Visibility        string            `json:"visibility"`
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
		slog.Warn("ensure Wendy failed", append(logger.RequestAttrs(r), "error", err, "workspace_id", workspaceID)...)
		writeError(w, http.StatusInternalServerError, "failed to create Wendy")
		return
	}
	if member.Role == "owner" {
		cancelled, err := h.bindWorkspaceRadarSupervisor(r.Context(), wsUUID, agent.ID)
		if err != nil {
			slog.Warn("bind Wendy workspace supervisor failed", append(logger.RequestAttrs(r), "error", err, "workspace_id", workspaceID, "agent_id", uuidToString(agent.ID))...)
			writeError(w, http.StatusInternalServerError, "failed to configure Wendy workspace supervision")
			return
		}
		if h.TaskService != nil && len(cancelled) > 0 {
			h.TaskService.BroadcastCancelledTasks(r.Context(), cancelled)
		}
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

func (h *Handler) bindWorkspaceRadarSupervisor(ctx context.Context, workspaceID, agentID pgtype.UUID) ([]db.AgentTaskQueue, error) {
	if h.TxStarter == nil {
		return nil, errors.New("workspace Radar transaction starter unavailable")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin workspace Radar supervisor bind: %w", err)
	}
	defer tx.Rollback(ctx)

	// Serialize all binds for one workspace, including the first insert where
	// workspace_radar_state has no row to lock yet.
	var lockedWorkspaceID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT id FROM workspace WHERE id = $1 FOR UPDATE
	`, workspaceID).Scan(&lockedWorkspaceID); err != nil {
		return nil, fmt.Errorf("lock workspace Radar bind: %w", err)
	}

	var previousAgentID pgtype.UUID
	hasPrevious := true
	if err := tx.QueryRow(ctx, `
		SELECT supervisor_agent_id
		FROM workspace_radar_state
		WHERE workspace_id = $1
		FOR UPDATE
	`, workspaceID).Scan(&previousAgentID); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("lock current workspace Radar supervisor: %w", err)
		}
		hasPrevious = false
	}
	if hasPrevious {
		var previousStillAuthorized bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1
			  FROM agent supervisor
			  JOIN member owner_member
			    ON owner_member.workspace_id = supervisor.workspace_id
			   AND owner_member.user_id = supervisor.owner_id
			   AND owner_member.role = 'owner'
			  WHERE supervisor.workspace_id = $1
			    AND supervisor.id = $2
			    AND supervisor.archived_at IS NULL
			)
		`, workspaceID, previousAgentID).Scan(&previousStillAuthorized); err != nil {
			return nil, fmt.Errorf("validate current workspace Radar supervisor: %w", err)
		}
		// EnsureWindy is called independently by every workspace owner. A valid
		// existing supervisor belongs to the workspace, not to the latest caller,
		// so repeated owner onboarding must not cancel work or reset scan state.
		if previousStillAuthorized {
			return nil, nil
		}
	}

	// The state table FK proves workspace membership, but the scheduling grant
	// additionally requires that this private Wendy still belongs to an owner.
	var authorizedAgentID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT supervisor.id
		FROM agent supervisor
		JOIN member owner_member
		  ON owner_member.workspace_id = supervisor.workspace_id
		 AND owner_member.user_id = supervisor.owner_id
		 AND owner_member.role = 'owner'
		WHERE supervisor.workspace_id = $1
		  AND supervisor.id = $2
		  AND supervisor.archived_at IS NULL
		FOR SHARE OF supervisor, owner_member
	`, workspaceID, agentID).Scan(&authorizedAgentID); err != nil {
		return nil, fmt.Errorf("authorize workspace Radar supervisor: %w", err)
	}

	qtx := h.Queries.WithTx(tx)
	cancelled := make([]db.AgentTaskQueue, 0)
	if hasPrevious && !radarUUIDsMatch(previousAgentID, agentID) {
		rows, err := tx.Query(ctx, `
			UPDATE agent_task_queue task
			SET status = 'cancelled',
			    completed_at = COALESCE(task.completed_at, now()),
			    error = COALESCE(NULLIF(task.error, ''), 'Workspace Radar supervisor was rebound'),
			    failure_reason = 'radar_supervisor_rebound'
			WHERE task.status IN ('queued', 'dispatched', 'running', 'waiting_local_directory')
			  AND task.agent_id = $2
			  AND task.context->>'type' = 'agent_radar'
			  AND EXISTS (
			    SELECT 1
			    FROM agent_radar_run run
			    WHERE run.task_id = task.id
			      AND run.id::text = task.context->>'radar_run_id'
			      AND run.workspace_id = $1
			      AND run.agent_id = $2
			      AND run.trigger_kind = 'scheduled'
			      AND run.cooldown_key = 'workspace_supervisor_radar'
			      AND run.status IN ('planned', 'queued', 'running', 'executing')
			  )
			RETURNING task.id
		`, workspaceID, previousAgentID)
		if err != nil {
			return nil, fmt.Errorf("cancel rebound workspace Radar tasks: %w", err)
		}
		var cancelledIDs []pgtype.UUID
		for rows.Next() {
			var taskID pgtype.UUID
			if err := rows.Scan(&taskID); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan cancelled workspace Radar task: %w", err)
			}
			cancelledIDs = append(cancelledIDs, taskID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("cancel rebound workspace Radar tasks: %w", err)
		}
		rows.Close()
		for _, taskID := range cancelledIDs {
			task, err := qtx.GetAgentTask(ctx, taskID)
			if err != nil {
				return nil, fmt.Errorf("load cancelled workspace Radar task: %w", err)
			}
			cancelled = append(cancelled, task)
		}

		if _, err := tx.Exec(ctx, `
			UPDATE agent_radar_run run
			SET status = 'cancelled',
			    error = CASE
			      WHEN run.error = '' THEN 'Workspace Radar supervisor was rebound'
			      ELSE run.error
			    END,
			    finished_at = COALESCE(run.finished_at, now()),
			    updated_at = now()
			WHERE run.workspace_id = $1
			  AND run.agent_id = $2
			  AND run.trigger_kind = 'scheduled'
			  AND run.cooldown_key = 'workspace_supervisor_radar'
			  AND run.status IN ('planned', 'queued', 'running', 'executing')
		`, workspaceID, previousAgentID); err != nil {
			return nil, fmt.Errorf("cancel rebound workspace Radar runs: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `
			INSERT INTO workspace_radar_state (
		  workspace_id,
		  supervisor_agent_id,
		  enabled,
		  next_due_at
		) VALUES ($1, $2, true, now())
		ON CONFLICT (workspace_id) DO UPDATE
		SET supervisor_agent_id = EXCLUDED.supervisor_agent_id,
		    enabled = true,
		    next_due_at = LEAST(workspace_radar_state.next_due_at, now()),
		    consecutive_failures = 0,
		    updated_at = now()
		`, workspaceID, agentID); err != nil {
		return nil, fmt.Errorf("persist workspace Radar supervisor: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit workspace Radar supervisor bind: %w", err)
	}
	return cancelled, nil
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
		writeError(w, http.StatusBadRequest, "connect a runtime before creating Wendy")
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
	agents, err := h.Queries.ListAllAgents(r.Context(), workspaceID)
	if err != nil {
		return db.Agent{}, false, err
	}
	if existing, ok := selectOwnedWindyAgent(agents, userID); ok {
		updated, err := h.restoreAndNormalizeWindyAgent(r, existing)
		if err != nil {
			return db.Agent{}, false, err
		}
		return updated, false, nil
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
	if (candidate.Visibility == "private") != (current.Visibility == "private") {
		return candidate.Visibility == "private"
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
	if agentDisplayName(updated) != windyAgentName || updated.Visibility != "private" {
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
	if restored {
		resp := agentToResponse(updated)
		h.publish(protocol.EventAgentStatus, uuidToString(updated.WorkspaceID), "member", requestUserID(r), map[string]any{"agent": broadcastAgentResponse(resp)})
	}
	return updated, nil
}

func (h *Handler) refreshWindyInstructionsIfStale(r *http.Request, agent db.Agent) (db.Agent, error) {
	if strings.Contains(agent.Instructions, windyInstructionsCapabilityMarker) {
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
		Visibility:  pgtype.Text{String: "private", Valid: true},
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
			description, instructions, avatar_url, visibility, project_id, channel_id,
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
			instructions, avatar_url, visibility, project_id, channel_id,
			can_execute_code, suggested_channels, recommended_tools, initial_notes,
			initial_memory
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING id, workspace_id, created_by_agent_id, target_user_id, name,
			description, instructions, avatar_url, visibility, project_id, channel_id,
			can_execute_code, suggested_channels, recommended_tools, initial_notes,
			initial_memory, status, used_agent_id, created_at, updated_at, used_at`,
		workspaceID, nullableUUID(createdByAgentID), targetUserID, req.Name, req.Description,
		req.Instructions, ptrToText(req.AvatarURL), req.Visibility, nullableUUID(projectID), nullableUUID(channelID),
		req.CanExecuteCode, suggestedChannels, recommendedTools, marshalStringMap(req.InitialNotes), marshalStringMap(req.InitialMemory))
	return scanAgentDraft(row)
}

func scanAgentDraft(row rowScanner) (AgentCreationDraftResponse, error) {
	var id, workspaceID, createdByAgentID, targetUserID, projectID, channelID, usedAgentID pgtype.UUID
	var name, description, instructions, visibility, status string
	var avatarURL pgtype.Text
	var canExecuteCode bool
	var suggestedChannelsRaw, recommendedToolsRaw, initialNotesRaw, initialMemoryRaw []byte
	var createdAt, updatedAt, usedAt pgtype.Timestamptz
	if err := row.Scan(
		&id, &workspaceID, &createdByAgentID, &targetUserID, &name,
		&description, &instructions, &avatarURL, &visibility, &projectID, &channelID,
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
		Visibility:        visibility,
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
	err = h.DB.QueryRow(r.Context(), `
		WITH task AS (
			SELECT t.initiator_user_id, c.author_type AS comment_author_type, c.author_id AS comment_author_id
			FROM agent_task_queue t
			JOIN agent a ON a.id = t.agent_id AND a.workspace_id = $2
			LEFT JOIN comment c ON c.id = t.trigger_comment_id AND c.workspace_id = $2
			WHERE t.id = $1
		)
		SELECT candidate.user_id
		FROM task
		CROSS JOIN LATERAL (
			SELECT CASE
				WHEN task.initiator_user_id IS NOT NULL THEN task.initiator_user_id
				WHEN task.comment_author_type = 'member' THEN task.comment_author_id
				ELSE NULL
			END AS user_id
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
	err = h.DB.QueryRow(r.Context(), `
		WITH inbox AS (
			SELECT msg.author_type AS message_author_type,
				msg.author_id AS message_author_id,
				session.creator_id AS session_creator_id
			FROM agent_inbox_event e
			LEFT JOIN channel_message msg ON msg.id = e.source_message_id AND msg.workspace_id = e.workspace_id
			LEFT JOIN chat_session session ON session.id = e.chat_session_id
			WHERE e.id = $1 AND e.workspace_id = $2 AND e.agent_id = $3
		)
		SELECT candidate.user_id
		FROM inbox
		CROSS JOIN LATERAL (
			SELECT COALESCE(
				CASE WHEN inbox.message_author_type = 'user' THEN inbox.message_author_id END,
				inbox.session_creator_id
			) AS user_id
		) candidate
		JOIN member m ON m.workspace_id = $2 AND m.user_id = candidate.user_id
		WHERE candidate.user_id IS NOT NULL
		LIMIT 1`, eventID, workspaceID, agentID).Scan(&target)
	return target, err == nil && target.Valid
}

func (h *Handler) MarkAgentDraftUsed(r *http.Request, workspaceID, targetUserID string, draftID pgtype.UUID, usedAgentID pgtype.UUID) {
	if h == nil || h.DB == nil || !draftID.Valid || !usedAgentID.Valid || strings.TrimSpace(targetUserID) == "" {
		return
	}
	_, _ = h.DB.Exec(r.Context(), `
		UPDATE agent_creation_draft
		SET status = 'used', used_agent_id = $2, used_at = now(), updated_at = now()
		WHERE id = $1 AND workspace_id = $3 AND target_user_id = $4 AND status = 'draft'`,
		draftID, usedAgentID, parseUUID(workspaceID), parseUUID(targetUserID))
}

func (h *Handler) loadAgentDraftInitialContext(r *http.Request, workspaceID, targetUserID string, draftID pgtype.UUID) (map[string]string, map[string]string) {
	if h == nil || h.DB == nil || !draftID.Valid || strings.TrimSpace(targetUserID) == "" {
		return nil, nil
	}
	var initialNotesRaw, initialMemoryRaw []byte
	err := h.DB.QueryRow(r.Context(), `
		SELECT initial_notes, initial_memory
		FROM agent_creation_draft
		WHERE id = $1 AND workspace_id = $2 AND target_user_id = $3 AND status = 'draft'`,
		draftID, parseUUID(workspaceID), parseUUID(targetUserID)).Scan(&initialNotesRaw, &initialMemoryRaw)
	if err != nil {
		return nil, nil
	}
	return decodeStringMap(initialNotesRaw), decodeStringMap(initialMemoryRaw)
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
