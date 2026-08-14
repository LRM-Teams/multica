package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

const maxResearchControlRequestBytes int64 = 256 << 10

func decodeResearchJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxResearchControlRequestBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return false
	}
	if int64(len(raw)) > maxResearchControlRequestBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "request body exceeds 256 KiB")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must contain exactly one JSON object")
		return false
	}
	return true
}

type steerResearchRunRequest struct {
	Goal               string          `json:"goal"`
	Reason             string          `json:"reason"`
	AllowRunningFinish bool            `json:"allow_running_finish"`
	Scope              json.RawMessage `json:"scope"`
	Audience           *string         `json:"audience"`
	Freshness          *string         `json:"freshness"`
	Language           *string         `json:"language"`
	SourcePolicy       json.RawMessage `json:"source_policy"`
	RunLimits          json.RawMessage `json:"run_limits"`
}

func (h *Handler) SteerResearchRun(w http.ResponseWriter, r *http.Request) {
	if h.ResearchRun == nil {
		writeError(w, http.StatusServiceUnavailable, "research run engine is unavailable")
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id"); !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	sessionID := chi.URLParam(r, "id")
	if _, ok = parseUUIDOrBadRequest(w, sessionID, "id"); !ok {
		return
	}
	var req steerResearchRunRequest
	if !decodeResearchJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Goal) == "" || len(strings.TrimSpace(req.Goal)) > 32<<10 {
		writeError(w, http.StatusBadRequest, "goal is required")
		return
	}
	if len(req.Reason) > 4096 || !validOptionalResearchJSONObject(req.Scope) || !validOptionalResearchJSONObject(req.SourcePolicy) || !validOptionalResearchJSONObject(req.RunLimits) {
		writeError(w, http.StatusBadRequest, "research contract fields are invalid")
		return
	}
	for _, field := range []*string{req.Audience, req.Freshness} {
		if field != nil && len(strings.TrimSpace(*field)) > 1024 {
			writeError(w, http.StatusBadRequest, "research contract text field exceeds 1024 bytes")
			return
		}
	}
	if req.Language != nil && len(strings.TrimSpace(*req.Language)) > 64 {
		writeError(w, http.StatusBadRequest, "language exceeds 64 bytes")
		return
	}
	run, err := h.ResearchRun.Steer(r.Context(), researchrun.SteerInput{
		SessionID:          sessionID,
		WorkspaceID:        workspaceID,
		UserID:             userID,
		Goal:               strings.TrimSpace(req.Goal),
		Reason:             strings.TrimSpace(req.Reason),
		AllowRunningFinish: req.AllowRunningFinish,
		Scope:              req.Scope,
		Audience:           trimOptionalString(req.Audience),
		Freshness:          trimOptionalString(req.Freshness),
		Language:           trimOptionalString(req.Language),
		SourcePolicy:       req.SourcePolicy,
		RunLimits:          req.RunLimits,
	})
	if err != nil {
		if errors.Is(err, researchrun.ErrRunNotFound) {
			writeError(w, http.StatusNotFound, "research session not found")
			return
		}
		if errors.Is(err, researchrun.ErrInvalidContract) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if errors.Is(err, researchrun.ErrInvalidTransition) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to steer research run")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": run})
}

func validOptionalResearchJSONObject(raw json.RawMessage) bool {
	if len(bytes.TrimSpace(raw)) == 0 {
		return true
	}
	var value map[string]any
	return json.Unmarshal(raw, &value) == nil && value != nil
}

func trimOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}

func (h *Handler) SubmitAgentResearchTaskResult(w http.ResponseWriter, r *http.Request) {
	if h.ResearchRun == nil {
		writeError(w, http.StatusServiceUnavailable, "research run engine is unavailable")
		return
	}
	principal, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	if principal.WorkspaceID != workspaceID {
		writeError(w, http.StatusForbidden, "access denied")
		return
	}
	workspaceUUID, valid := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !valid {
		return
	}
	if _, active := h.requireActiveFleetMember(w, r, workspaceUUID); !active {
		return
	}
	sessionID := chi.URLParam(r, "id")
	taskID := chi.URLParam(r, "taskId")
	attemptID := chi.URLParam(r, "attemptId")
	for field, value := range map[string]string{"id": sessionID, "task_id": taskID, "attempt_id": attemptID} {
		if _, valid = parseUUIDOrBadRequest(w, value, field); !valid {
			return
		}
	}
	inboxTaskID, allowed := h.resolveResearchAttemptInboxTaskID(w, r, principal, sessionID, taskID, attemptID)
	if !allowed {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, (2<<20)+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read result")
		return
	}
	if len(raw) > 2<<20 {
		writeError(w, http.StatusRequestEntityTooLarge, "research result exceeds 2 MiB")
		return
	}
	outcome, err := h.ResearchRun.SubmitResult(r.Context(), sessionID, workspaceID, taskID, attemptID, principal.AgentID, inboxTaskID, raw)
	if err != nil {
		switch {
		case errors.Is(err, researchrun.ErrInvalidResult):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, researchrun.ErrAttemptNotAssigned):
			writeError(w, http.StatusForbidden, "research attempt is not assigned to this task credential")
		case errors.Is(err, researchrun.ErrResultConflict), errors.Is(err, researchrun.ErrInvalidTransition), errors.Is(err, researchrun.ErrCapabilityUnavailable):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, researchrun.ErrRunNotFound):
			writeError(w, http.StatusNotFound, "research task attempt not found")
		default:
			if outcome.TaskID != "" {
				writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "outcome": outcome, "warning": err.Error()})
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to accept research result")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "outcome": outcome})
}

type researchInboxTaskContext struct {
	Type      string `json:"type"`
	SessionID string `json:"research_session_id"`
	TaskID    string `json:"research_task_id"`
	AttemptID string `json:"research_attempt_id"`
}

func (h *Handler) resolveResearchAttemptInboxTaskID(
	w http.ResponseWriter,
	r *http.Request,
	principal middleware.AgentPrincipal,
	sessionID, taskID, attemptID string,
) (string, bool) {
	if principal.ActorSource != "agent_credential" {
		inboxTaskID := strings.TrimSpace(principal.InboxEventID)
		if inboxTaskID == "" {
			inboxTaskID = strings.TrimSpace(principal.TaskID)
		}
		if inboxTaskID == "" {
			writeError(w, http.StatusForbidden, "task-bound agent credential required")
			return "", false
		}
		return inboxTaskID, true
	}

	event, _, ok := h.requireAgentCredentialActiveInboxDelivery(w, r)
	if !ok {
		return "", false
	}
	var binding researchInboxTaskContext
	if json.Unmarshal(event.Context, &binding) != nil ||
		binding.Type != "research_run_task" ||
		binding.SessionID != sessionID ||
		binding.TaskID != taskID ||
		binding.AttemptID != attemptID {
		writeError(w, http.StatusForbidden, "active inbox delivery does not match this research attempt")
		return "", false
	}
	return uuidToString(event.ID), true
}
