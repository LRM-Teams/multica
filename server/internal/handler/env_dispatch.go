package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/service"
)

// EnvDispatchRequest is the body of POST /api/v1/env-dispatch (spec §6.3).
type EnvDispatchRequest struct {
	Mode           string                `json:"mode"`
	EnvID          string                `json:"env_id"`
	Domain         string                `json:"domain,omitempty"`
	DispatchType   string                `json:"dispatch_type"`
	GroupSize      int                   `json:"group_size"`
	AgentID        string                `json:"agent_id"`
	IdempotencyKey string                `json:"idempotency_key,omitempty"`
	Issue          *IssueDispatchInput   `json:"issue,omitempty"`
	Message        *MessageDispatchInput `json:"message,omitempty"`
}

type IssueDispatchInput struct {
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	FailToPass         []string `json:"fail_to_pass"`
	PassToPass         []string `json:"pass_to_pass"`
}

type MessageDispatchInput struct {
	Content string `json:"content"`
}

// EnvDispatchResponse is the 201 response (spec §6.3).
type EnvDispatchResponse struct {
	Rollouts []EnvRolloutResponse `json:"rollouts"`
}

type EnvRolloutResponse struct {
	EnvID         string `json:"env_id"`
	ProjectID     string `json:"project_id"`
	IssueID       string `json:"issue_id,omitempty"`
	ChatSessionID string `json:"chat_session_id,omitempty"`
	AgentRunID    string `json:"agent_run_id,omitempty"`
	Error         string `json:"error,omitempty"`
}

// EnvDispatch handles POST /api/v1/env-dispatch.
func (h *Handler) EnvDispatch(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace ID required")
		return
	}
	var req EnvDispatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	svc := service.NewEnvDispatchService(newEnvDispatchDepsAdapter(h), envDispatchConcurrency())
	res, err := svc.Dispatch(r.Context(), service.EnvDispatchInput{
		WorkspaceID: workspaceID, UserID: userID,
		Mode: service.EnvMode(req.Mode), EnvID: req.EnvID,
		Domain:       service.EnvDomain(req.Domain),
		DispatchType: service.EnvDispatchType(req.DispatchType),
		GroupSize:    req.GroupSize, AgentID: req.AgentID,
		IdempotencyKey: req.IdempotencyKey,
		Issue:          mapIssueInput(req.Issue),
		Message:        mapMessageInput(req.Message),
	})
	if err != nil {
		writeEnvDispatchError(w, err, res)
		return
	}
	writeJSON(w, http.StatusCreated, EnvDispatchResponse{Rollouts: mapRollouts(res.Rollouts)})
}

// DeleteEnvDispatchProject handles DELETE /api/v1/env-dispatch/{projectID}.
func (h *Handler) DeleteEnvDispatchProject(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace ID required")
		return
	}
	projectID := chi.URLParam(r, "projectID")
	if projectID == "" {
		writeError(w, http.StatusBadRequest, "projectID is required")
		return
	}
	svc := service.NewEnvDispatchService(newEnvDispatchDepsAdapter(h), 8)
	if err := svc.DeleteProject(r.Context(), projectID, workspaceID); err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeEnvDispatchError(w http.ResponseWriter, err error, res service.EnvDispatchResult) {
	msg := err.Error()
	switch {
	case errors.Is(err, service.ErrAllDispatchFailed):
		writeJSON(w, http.StatusInternalServerError, EnvDispatchResponse{Rollouts: mapRollouts(res.Rollouts)})
	case strings.Contains(msg, "validation_failed"):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "validation_failed", "message": msg})
	case strings.Contains(msg, "not_implemented"):
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "not_implemented", "message": msg})
	case strings.Contains(msg, "reset_failed"):
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "reset_failed", "message": msg})
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "internal", "message": msg})
	}
}

func mapIssueInput(i *IssueDispatchInput) *service.IssueInput {
	if i == nil {
		return nil
	}
	return &service.IssueInput{
		Title: i.Title, Description: i.Description,
		AcceptanceCriteria: i.AcceptanceCriteria, FailToPass: i.FailToPass, PassToPass: i.PassToPass,
	}
}

func mapMessageInput(m *MessageDispatchInput) *service.MessageInput {
	if m == nil {
		return nil
	}
	return &service.MessageInput{Content: m.Content}
}

func mapRollouts(rs []service.EnvRollout) []EnvRolloutResponse {
	out := make([]EnvRolloutResponse, 0, len(rs))
	for _, r := range rs {
		out = append(out, EnvRolloutResponse{
			EnvID: r.EnvID, ProjectID: r.ProjectID, IssueID: r.IssueID,
			ChatSessionID: r.ChatSessionID, AgentRunID: r.AgentRunID, Error: r.Error,
		})
	}
	return out
}

// envDispatchConcurrency reads ENV_DISPATCH_CONCURRENCY (default 8).
func envDispatchConcurrency() int {
	return 8
}

// newEnvDispatchDepsAdapter returns the production Deps adapter. Task 8 wires
// real queries; until then, this returns a stub adapter that returns dummy values.
func newEnvDispatchDepsAdapter(h *Handler) service.EnvDispatchDeps {
	return &envDispatchDepsAdapter{h: h}
}

type envDispatchDepsAdapter struct {
	h *Handler
}

// All methods return stubs until Task 8 wires them to real queries.
func (a *envDispatchDepsAdapter) GetEnv(ctx context.Context, envID, workspaceID string) (service.Env, error) {
	return service.Env{}, nil
}
func (a *envDispatchDepsAdapter) CreateEnv(ctx context.Context, workspaceID, sandboxID, parentEnvID string, mode service.EnvMode, domain service.EnvDomain) (string, error) {
	return "stub-env", nil
}
func (a *envDispatchDepsAdapter) DeleteEnv(ctx context.Context, envID, workspaceID string) error {
	return nil
}
func (a *envDispatchDepsAdapter) ForkSandbox(ctx context.Context, src string, idx int) (string, error) {
	return "stub-fork", nil
}
func (a *envDispatchDepsAdapter) DeleteSandbox(ctx context.Context, sandboxID string) error {
	return nil
}
func (a *envDispatchDepsAdapter) BootSandbox(ctx context.Context, imageRef string) (string, error) {
	return "stub-boot", nil
}
func (a *envDispatchDepsAdapter) CreateProject(ctx context.Context, workspaceID, name, envID string) (string, error) {
	return "stub-project", nil
}
func (a *envDispatchDepsAdapter) CopyProjectSubtree(ctx context.Context, src, ws, envID string) (string, map[string]string, map[string]string, error) {
	return "stub-copy", map[string]string{}, map[string]string{}, nil
}
func (a *envDispatchDepsAdapter) GetProjectByEnvID(ctx context.Context, envID, ws string) (string, error) {
	return "stub-project", nil
}
func (a *envDispatchDepsAdapter) GetIdempotentResponse(ctx context.Context, ws, key string) (service.EnvDispatchResult, bool, error) {
	return service.EnvDispatchResult{}, false, nil
}
func (a *envDispatchDepsAdapter) SaveIdempotentResponse(ctx context.Context, ws, key string, res service.EnvDispatchResult) error {
	return nil
}
func (a *envDispatchDepsAdapter) DeleteProject(ctx context.Context, pid, ws string) error { return nil }
func (a *envDispatchDepsAdapter) ListIssuesByProject(ctx context.Context, pid, ws string) ([]service.IssueRow, error) {
	return nil, nil
}
func (a *envDispatchDepsAdapter) CreateIssue(ctx context.Context, pid, ws, creator, title, desc string, ac, f2p, p2p []string) (string, error) {
	return "stub-issue", nil
}
func (a *envDispatchDepsAdapter) CreateChatSession(ctx context.Context, pid, ws, agent, creator string) (string, error) {
	return "stub-session", nil
}
func (a *envDispatchDepsAdapter) CreateChatMessage(ctx context.Context, sid, role, content string) (string, error) {
	return "stub-msg", nil
}
func (a *envDispatchDepsAdapter) EnqueueAgentRun(ctx context.Context, ws, agent, issue, sess, sbx string, idx int) (string, error) {
	return "stub-run", nil
}
