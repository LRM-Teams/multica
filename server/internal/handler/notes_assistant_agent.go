package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	notesAssistantAgentName        = "notes-assistant"
	notesAssistantAgentDisplayName = "笔记助手"
	notesAssistantAgentTemplate    = "notes-assistant"
	// Detects personas that predate selective-read wake contract.
	notesAssistantInstructionsCapabilityMarker = "Standalone Agent Chat"
)

// EnsureNotesAssistantAgentResponse is returned by POST /api/agents/notes-assistant.
type EnsureNotesAssistantAgentResponse struct {
	Agent                 *AgentResponse `json:"agent,omitempty"`
	Created               bool           `json:"created"`
	Aligned               bool           `json:"aligned"`
	NeedsSetup            bool           `json:"needs_setup"`
	OnboardingAvailable   bool           `json:"onboarding_available"`
	SetupHint             bool           `json:"setup_hint"`
}

type ensureNotesAssistantAgentRequest struct {
	CloneOnboarding bool   `json:"clone_onboarding"`
	RuntimeID       string `json:"runtime_id"`
	Model           string `json:"model"`
}

// EnsureNotesAssistantAgent idempotently provisions the Workspace Notes
// Assistant (「笔记助手」 / name notes-assistant) for the Notes page bubble.
//
// POST {}:
//   - existing (active) → return (refresh stale instructions)
//   - missing → needs_setup (no create). UI shows the create card.
//
// POST {clone_onboarding:true}: restore archived or create from Wendy.
// POST {runtime_id,model}: restore archived or create with explicit runtime.
func (h *Handler) EnsureNotesAssistantAgent(w http.ResponseWriter, r *http.Request) {
	if rejectAgentOnHumanRoute(w, r, "EnsureNotesAssistantAgent") {
		return
	}
	ownerID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	member, ok := h.requireManageAgents(w, r, workspaceID, "workspace not found")
	if !ok {
		return
	}

	var req ensureNotesAssistantAgentRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}

	onboardingRuntime, onboardingModel, onboardingOK, onboardingErr := h.resolveOnboardingRuntimeModel(r.Context(), wsUUID)
	if onboardingErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve onboarding agent")
		return
	}

	if agent, found, err := h.findNotesAssistantAgent(r.Context(), wsUUID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list agents")
		return
	} else if found {
		agent = h.refreshNotesAssistantInstructionsIfStale(r.Context(), agent)
		aligned := false
		if req.CloneOnboarding {
			if !onboardingOK {
				writeError(w, http.StatusBadRequest, "onboarding agent is not configured")
				return
			}
			if !canUseRuntimeForAgent(member, onboardingRuntime) {
				writeError(w, http.StatusForbidden, "this runtime is private; only its owner or a workspace admin can move agents onto it")
				return
			}
			updated, alignErr := h.alignNotesAssistantToRuntime(r.Context(), agent, onboardingRuntime, onboardingModel)
			if alignErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to align notes assistant runtime: "+alignErr.Error())
				return
			}
			agent = updated
			aligned = true
		}
		resp := agentToResponse(agent)
		redactAgentResponseForActor(&resp, "member")
		writeJSON(w, http.StatusOK, EnsureNotesAssistantAgentResponse{
			Agent:               &resp,
			Created:             false,
			Aligned:             aligned,
			OnboardingAvailable: onboardingOK,
			SetupHint:           false,
		})
		return
	}

	runtimeID := strings.TrimSpace(req.RuntimeID)
	model := strings.TrimSpace(req.Model)

	// Soft probe: missing agent → ask the human to create (do not auto-provision).
	if !req.CloneOnboarding && runtimeID == "" && model == "" {
		writeJSON(w, http.StatusOK, EnsureNotesAssistantAgentResponse{
			NeedsSetup:          true,
			OnboardingAvailable: onboardingOK,
			SetupHint:           true,
		})
		return
	}

	var runtime db.AgentRuntime
	switch {
	case req.CloneOnboarding:
		if !onboardingOK {
			writeJSON(w, http.StatusOK, EnsureNotesAssistantAgentResponse{
				NeedsSetup:          true,
				OnboardingAvailable: false,
				SetupHint:           true,
			})
			return
		}
		if !canUseRuntimeForAgent(member, onboardingRuntime) {
			writeError(w, http.StatusForbidden, "this runtime is private; only its owner or a workspace admin can create agents on it")
			return
		}
		runtime = onboardingRuntime
		model = onboardingModel
	case runtimeID != "" && model != "":
		if err := service.RequireAgentModel(model); err != nil {
			writeError(w, http.StatusBadRequest, "model is required")
			return
		}
		runtimeUUID, ok := parseUUIDOrBadRequest(w, runtimeID, "runtime_id")
		if !ok {
			return
		}
		rt, err := h.Queries.GetAgentRuntimeForWorkspace(r.Context(), db.GetAgentRuntimeForWorkspaceParams{
			ID:          runtimeUUID,
			WorkspaceID: wsUUID,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid runtime_id")
			return
		}
		if !canUseRuntimeForAgent(member, rt) {
			writeError(w, http.StatusForbidden, "this runtime is private; only its owner or a workspace admin can create agents on it")
			return
		}
		runtime = rt
	default:
		writeJSON(w, http.StatusOK, EnsureNotesAssistantAgentResponse{
			NeedsSetup:          true,
			OnboardingAvailable: onboardingOK,
			SetupHint:           true,
		})
		return
	}

	if err := service.RequireAgentModel(model); err != nil {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}

	// Prefer restoring an archived 笔记助手 (human "deleted" it) over failing
	// UNIQUE(workspace_id, name) when creating a second row.
	if restored, okRestore, restoreErr := h.restoreArchivedNotesAssistant(r.Context(), wsUUID, runtime, model); restoreErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to restore notes assistant: "+restoreErr.Error())
		return
	} else if okRestore {
		resp := agentToResponse(restored)
		redactAgentResponseForActor(&resp, "member")
		writeJSON(w, http.StatusOK, EnsureNotesAssistantAgentResponse{
			Agent:               &resp,
			Created:             true,
			OnboardingAvailable: onboardingOK,
			SetupHint:           true,
		})
		return
	}

	tmpl, found := agentTemplates.Get(notesAssistantAgentTemplate)
	if !found {
		writeError(w, http.StatusInternalServerError, "notes-assistant template missing")
		return
	}

	createParams := db.CreateAgentParams{
		WorkspaceID:        wsUUID,
		Name:               notesAssistantAgentName,
		DisplayName:        notesAssistantAgentDisplayName,
		Description:        tmpl.Description,
		Instructions:       tmpl.Instructions,
		RuntimeMode:        runtime.RuntimeMode,
		RuntimeConfig:      []byte("{}"),
		RuntimeID:          runtime.ID,
		MaxConcurrentTasks: 6,
		OwnerID:            parseUUID(ownerID),
		CustomEnv:          []byte("{}"),
		CustomArgs:         []byte("[]"),
		Model:              pgtype.Text{String: model, Valid: true},
	}
	applyCreateAgentAvatar(&createParams, resolvedAgentAvatar{})

	created, err := h.createAgentManagedCommit(r.Context(), wsUUID, createParams, notesAssistantAgentDisplayName)
	if err != nil {
		if identityUniqueViolation(err, "agent_workspace_name_unique") {
			if restored, okRestore, restoreErr := h.restoreArchivedNotesAssistant(r.Context(), wsUUID, runtime, model); restoreErr == nil && okRestore {
				resp := agentToResponse(restored)
				redactAgentResponseForActor(&resp, "member")
				writeJSON(w, http.StatusOK, EnsureNotesAssistantAgentResponse{
					Agent:               &resp,
					Created:             true,
					OnboardingAvailable: onboardingOK,
					SetupHint:           true,
				})
				return
			}
			if agent, found, findErr := h.findNotesAssistantAgent(r.Context(), wsUUID); findErr == nil && found {
				agent = h.refreshNotesAssistantInstructionsIfStale(r.Context(), agent)
				resp := agentToResponse(agent)
				redactAgentResponseForActor(&resp, "member")
				writeJSON(w, http.StatusOK, EnsureNotesAssistantAgentResponse{
					Agent:               &resp,
					Created:             false,
					OnboardingAvailable: onboardingOK,
				})
				return
			}
			writeError(w, http.StatusConflict, "name is already in use")
			return
		}
		if errors.Is(err, errIdentityHandleInvalid) {
			writeError(w, http.StatusBadRequest, "name must be 1-32 lowercase letters, digits, or hyphens")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create notes assistant agent: "+err.Error())
		return
	}

	resp := agentToResponse(created)
	redactAgentResponseForActor(&resp, "member")
	writeJSON(w, http.StatusCreated, EnsureNotesAssistantAgentResponse{
		Agent:               &resp,
		Created:             true,
		OnboardingAvailable: onboardingOK,
		SetupHint:           true,
	})
}

func (h *Handler) findNotesAssistantAgent(ctx context.Context, workspaceID pgtype.UUID) (db.Agent, bool, error) {
	agents, err := h.Queries.ListAgents(ctx, workspaceID)
	if err != nil {
		return db.Agent{}, false, err
	}
	for _, agent := range agents {
		if agent.Name == notesAssistantAgentName {
			return agent, true, nil
		}
	}
	return db.Agent{}, false, nil
}

func (h *Handler) findArchivedNotesAssistantAgent(ctx context.Context, workspaceID pgtype.UUID) (db.Agent, bool, error) {
	var id pgtype.UUID
	err := h.DB.QueryRow(ctx, `
SELECT id FROM agent
WHERE workspace_id = $1
  AND name = $2
  AND archived_at IS NOT NULL
ORDER BY archived_at DESC
LIMIT 1`, workspaceID, notesAssistantAgentName).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Agent{}, false, nil
		}
		return db.Agent{}, false, err
	}
	agent, err := h.Queries.GetAgent(ctx, id)
	if err != nil {
		return db.Agent{}, false, err
	}
	return agent, true, nil
}

// restoreArchivedNotesAssistant un-archives the workspace 笔记助手 (if any) and
// rebinds it to the requested runtime/model. Returns ok=false when none archived.
func (h *Handler) restoreArchivedNotesAssistant(
	ctx context.Context,
	workspaceID pgtype.UUID,
	runtime db.AgentRuntime,
	model string,
) (db.Agent, bool, error) {
	archived, found, err := h.findArchivedNotesAssistantAgent(ctx, workspaceID)
	if err != nil || !found {
		return db.Agent{}, false, err
	}
	restored, err := h.Queries.RestoreAgent(ctx, archived.ID)
	if err != nil {
		return db.Agent{}, false, err
	}
	restored = h.refreshNotesAssistantInstructionsIfStale(ctx, restored)
	aligned, alignErr := h.alignNotesAssistantToRuntime(ctx, restored, runtime, model)
	if alignErr != nil {
		return restored, true, alignErr
	}
	return aligned, true, nil
}

func (h *Handler) resolveOnboardingRuntimeModel(
	ctx context.Context,
	workspaceID pgtype.UUID,
) (db.AgentRuntime, string, bool, error) {
	boundID, err := h.Queries.GetWorkspaceOnboardingAgentID(ctx, workspaceID)
	if err != nil || !boundID.Valid {
		return db.AgentRuntime{}, "", false, nil
	}
	agent, err := h.Queries.GetAgent(ctx, boundID)
	if err != nil {
		return db.AgentRuntime{}, "", false, err
	}
	if agent.ArchivedAt.Valid {
		return db.AgentRuntime{}, "", false, nil
	}
	model := strings.TrimSpace(agent.Model.String)
	if !agent.Model.Valid || model == "" {
		return db.AgentRuntime{}, "", false, nil
	}
	runtime, err := h.Queries.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{
		ID:          agent.RuntimeID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return db.AgentRuntime{}, "", false, err
	}
	return runtime, model, true, nil
}

func (h *Handler) alignNotesAssistantToRuntime(
	ctx context.Context,
	agent db.Agent,
	runtime db.AgentRuntime,
	model string,
) (db.Agent, error) {
	if agent.RuntimeID == runtime.ID && agent.Model.Valid && agent.Model.String == model {
		return agent, nil
	}
	updated, err := h.Queries.UpdateAgent(ctx, db.UpdateAgentParams{
		ID:          agent.ID,
		RuntimeID:   runtime.ID,
		RuntimeMode: pgtype.Text{String: runtime.RuntimeMode, Valid: true},
		Model:       pgtype.Text{String: model, Valid: true},
	})
	if err != nil {
		return agent, err
	}
	if agent.RuntimeID != runtime.ID {
		_ = h.Queries.MarkAgentRuntimeReassigned(ctx, agent.ID)
	}
	return updated, nil
}

func (h *Handler) refreshNotesAssistantInstructionsIfStale(ctx context.Context, agent db.Agent) db.Agent {
	if agent.Name != notesAssistantAgentName {
		return agent
	}
	if strings.Contains(agent.Instructions, notesAssistantInstructionsCapabilityMarker) {
		return agent
	}
	tmpl, found := agentTemplates.Get(notesAssistantAgentTemplate)
	if !found || strings.TrimSpace(tmpl.Instructions) == "" {
		return agent
	}
	if !strings.Contains(tmpl.Instructions, notesAssistantInstructionsCapabilityMarker) {
		return agent
	}
	updated, err := h.Queries.UpdateAgent(ctx, db.UpdateAgentParams{
		ID:           agent.ID,
		Description:  pgtype.Text{String: tmpl.Description, Valid: true},
		Instructions: pgtype.Text{String: tmpl.Instructions, Valid: true},
	})
	if err != nil {
		return agent
	}
	return updated
}
