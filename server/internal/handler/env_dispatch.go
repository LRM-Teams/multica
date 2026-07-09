package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/cloudruntime"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// EnvDispatchRequest is the body of POST /api/v1/env-dispatch (spec §6.3).
type EnvDispatchRequest struct {
	Mode           string                        `json:"mode"`
	EnvID          string                        `json:"env_id"`
	Domain         string                        `json:"domain,omitempty"`
	DispatchType   string                        `json:"dispatch_type"`
	GroupSize      int                           `json:"group_size"`
	AgentID        string                        `json:"agent_id"`
	SquadID        string                        `json:"squad_id,omitempty"`
	TrainAgentID   string                        `json:"train_agent_id,omitempty"`
	CriticAgentID  string                        `json:"critic_agent_id,omitempty"`
	IdempotencyKey string                        `json:"idempotency_key,omitempty"`
	Issue          *IssueDispatchInput           `json:"issue,omitempty"`
	Message        *MessageDispatchInput         `json:"message,omitempty"`
	PerAgentEnv    map[string]PerAgentEnvRequest `json:"per_agent_env,omitempty"`
}

// PerAgentEnvRequest carries one squad member's sandbox template or base env
// intent. The map key in EnvDispatchRequest is the agent_id.
type PerAgentEnvRequest struct {
	Template  string `json:"template,omitempty"`
	BaseEnvID string `json:"base_env_id,omitempty"`
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
	ProjectID string               `json:"project_id"`
	Rollouts  []EnvRolloutResponse `json:"rollouts"`
}

type EnvRolloutResponse struct {
	EnvID            string                                `json:"env_id"`
	ProjectID        string                                `json:"project_id"`
	IssueID          string                                `json:"issue_id,omitempty"`
	ChatSessionID    string                                `json:"chat_session_id,omitempty"`
	AgentRunID       string                                `json:"agent_run_id,omitempty"`
	Error            string                                `json:"error,omitempty"`
	SandboxRefs      []service.SandboxInstanceRef          `json:"sandbox_refs,omitempty"`
	AgentSandboxRefs map[string]service.SandboxInstanceRef `json:"agent_sandbox_refs,omitempty"`
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

	// UUID-shape validation (spec §6.3). Do it here so malformed IDs return a
	// 400 instead of panicking deep in the adapter (parseUUID is MustParseUUID).
	// env_id/agent_id may be empty now (empty env_id resolves a per-workspace
	// default for scratch self_play; agent_id is optional when squad_id is set),
	// so only shape-check them when present. The service enforces the
	// conditional-required rules.
	if req.EnvID != "" {
		if _, ok := parseUUIDOrBadRequest(w, req.EnvID, "env_id"); !ok {
			return
		}
	}
	if req.AgentID != "" {
		if _, ok := parseUUIDOrBadRequest(w, req.AgentID, "agent_id"); !ok {
			return
		}
	}
	if req.SquadID != "" {
		if _, ok := parseUUIDOrBadRequest(w, req.SquadID, "squad_id"); !ok {
			return
		}
	}
	if req.TrainAgentID != "" {
		if _, ok := parseUUIDOrBadRequest(w, req.TrainAgentID, "train_agent_id"); !ok {
			return
		}
	}
	if req.CriticAgentID != "" {
		if _, ok := parseUUIDOrBadRequest(w, req.CriticAgentID, "critic_agent_id"); !ok {
			return
		}
	}
	if req.IdempotencyKey != "" {
		if _, err := util.ParseUUID(req.IdempotencyKey); err != nil {
			writeError(w, http.StatusBadRequest, "idempotency_key must be a valid UUID")
			return
		}
	}

	svc := service.NewEnvDispatchService(newEnvDispatchDepsAdapter(h), envDispatchConcurrency())
	if lc := newEnvSandboxLifecycleService(h); lc != nil {
		svc = svc.WithSandboxLifecycle(lc)
	}
	res, err := svc.Dispatch(r.Context(), service.EnvDispatchInput{
		WorkspaceID: workspaceID, UserID: userID,
		Mode: service.EnvMode(req.Mode), EnvID: req.EnvID,
		Domain:       service.EnvDomain(req.Domain),
		DispatchType: service.EnvDispatchType(req.DispatchType),
		GroupSize:    req.GroupSize, AgentID: req.AgentID,
		SquadID:          req.SquadID,
		TrainAgentID:     req.TrainAgentID,
		CriticAgentID:    req.CriticAgentID,
		IdempotencyKey:   req.IdempotencyKey,
		Issue:            mapIssueInput(req.Issue),
		Message:          mapMessageInput(req.Message),
		PerAgentEnvSpecs: mapPerAgentEnvSpecs(req.PerAgentEnv),
	})
	if err != nil {
		writeEnvDispatchError(w, err, res)
		return
	}
	writeJSON(w, http.StatusCreated, EnvDispatchResponse{ProjectID: res.ProjectID, Rollouts: mapRollouts(res.Rollouts)})
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
	if _, ok := parseUUIDOrBadRequest(w, projectID, "projectID"); !ok {
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
		writeJSON(w, http.StatusInternalServerError, EnvDispatchResponse{ProjectID: res.ProjectID, Rollouts: mapRollouts(res.Rollouts)})
	case strings.Contains(msg, "validation_failed"):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "validation_failed", "message": msg})
	case strings.Contains(msg, "not_implemented"):
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "not_implemented", "message": msg})
	case errors.Is(err, pgx.ErrNoRows):
		// A bare GetEnv miss: env_id does not exist / not in workspace. (The
		// source-project-resolve path wraps its ErrNoRows in "validation_failed"
		// and is handled above, so this only fires for the top-level env lookup.)
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found", "message": msg})
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

// mapPerAgentEnvSpecs converts the request's agent_id→spec map into the sorted
// service-layer slice. Map iteration order is non-deterministic, so keys are
// sorted for reproducible validation/creation order.
func mapPerAgentEnvSpecs(m map[string]PerAgentEnvRequest) []service.PerAgentEnvSpec {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]service.PerAgentEnvSpec, 0, len(m))
	for _, k := range keys {
		out = append(out, service.PerAgentEnvSpec{
			AgentID:   k,
			Template:  m[k].Template,
			BaseEnvID: m[k].BaseEnvID,
		})
	}
	return out
}

func mapRollouts(rs []service.EnvRollout) []EnvRolloutResponse {
	out := make([]EnvRolloutResponse, 0, len(rs))
	for _, r := range rs {
		out = append(out, EnvRolloutResponse{
			EnvID: r.EnvID, ProjectID: r.ProjectID, IssueID: r.IssueID,
			ChatSessionID: r.ChatSessionID, AgentRunID: r.AgentRunID, Error: r.Error,
			SandboxRefs:      r.SandboxRefs,
			AgentSandboxRefs: r.AgentSandboxRefs,
		})
	}
	return out
}

// envDispatchConcurrency reads ENV_DISPATCH_CONCURRENCY (default 8).
func envDispatchConcurrency() int {
	return 8
}

// newEnvDispatchDepsAdapter returns the production Deps adapter wired to real
// sqlc queries + cloud-runtime calls. When the handler has no Queries (test
// fixtures that only exercise validation paths), it falls back to a stub so
// construction does not crash; the service's validation gate happens before
// any Deps method is invoked, so handler validation tests stay green without
// a DB.
func newEnvDispatchDepsAdapter(h *Handler) service.EnvDispatchDeps {
	if h.Queries == nil {
		return &stubEnvDispatchDeps{}
	}
	return &envDispatchDepsAdapter{h: h}
}

// envDispatchDepsAdapter bridges service.EnvDispatchDeps to *Handler.Queries
// (DB) and *Handler.CloudRuntime (sandbox lifecycle). Each method maps the
// service's string IDs to pgtype.UUID via parseUUID (trusted: these IDs are
// either sqlc round-trips or already-validated request inputs by the time
// they reach the adapter).
type envDispatchDepsAdapter struct {
	h *Handler
}

// fkViolation23503 reports whether err is a PostgreSQL FK violation
// (SQLSTATE 23503). Used to surface ErrEnvInUse when ON DELETE RESTRICT
// rejects an environment delete.
func fkViolation23503(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// envRowToService converts a db.Environment row to the service-layer Env
// snapshot. ParentEnvID stays zero-valued for base envs.
func envRowToService(e db.Environment) service.Env {
	out := service.Env{
		ID:          util.UUIDToString(e.ID),
		WorkspaceID: util.UUIDToString(e.WorkspaceID),
		SandboxIDs:  e.SandboxIds,
		Mode:        service.EnvMode(e.Mode),
	}
	if e.ParentEnvID.Valid {
		out.ParentEnvID = util.UUIDToString(e.ParentEnvID)
	}
	if e.Domain.Valid {
		out.Domain = service.EnvDomain(e.Domain.String)
	}
	return out
}

// GetEnv resolves the env row, returning a service.Env snapshot. The service
// uses SandboxIDs to fork and ParentEnvID for fork provenance.
func (a *envDispatchDepsAdapter) GetEnv(ctx context.Context, envID, workspaceID string) (service.Env, error) {
	row, err := a.h.Queries.GetEnvironment(ctx, db.GetEnvironmentParams{
		ID:          parseUUID(envID),
		WorkspaceID: parseUUID(workspaceID),
	})
	if err != nil {
		return service.Env{}, fmt.Errorf("get env: %w", err)
	}
	return envRowToService(row), nil
}

// CreateEnv inserts an environment row with its sandbox set. parentEnvID is
// empty for base envs.
func (a *envDispatchDepsAdapter) CreateEnv(ctx context.Context, workspaceID string, sandboxIDs []string, parentEnvID string, mode service.EnvMode, domain service.EnvDomain) (string, error) {
	params := db.CreateEnvironmentParams{
		WorkspaceID: parseUUID(workspaceID),
		SandboxIds:  sandboxIDs,
		Mode:        string(mode),
	}
	if parentEnvID != "" {
		params.ParentEnvID = parseUUID(parentEnvID)
	}
	if domain != "" {
		params.Domain = pgtype.Text{String: string(domain), Valid: true}
	}
	row, err := a.h.Queries.CreateEnvironment(ctx, params)
	if err != nil {
		return "", fmt.Errorf("create env: %w", err)
	}
	return util.UUIDToString(row.ID), nil
}

// DeleteEnv deletes the env row. A FK violation (23503) is translated to
// service.ErrEnvInUse so the handler can map it to 409; the service layer
// also passes through ErrEnvInUse untouched. A missing row is treated as
// success so the rollback path stays idempotent.
func (a *envDispatchDepsAdapter) DeleteEnv(ctx context.Context, envID, workspaceID string) error {
	err := a.h.Queries.DeleteEnvironment(ctx, db.DeleteEnvironmentParams{
		ID:          parseUUID(envID),
		WorkspaceID: parseUUID(workspaceID),
	})
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if fkViolation23503(err) {
		return service.ErrEnvInUse
	}
	return fmt.Errorf("delete env: %w", err)
}

// ForkSandbox calls POST /api/v1/sandboxes/fork with the source sandbox id.
// idx is included in the request body so Fleet can label the fork per-rollout
// when desired; it is otherwise unused.
func (a *envDispatchDepsAdapter) ForkSandbox(ctx context.Context, sourceSandboxID string, idx int) (string, error) {
	body, err := json.Marshal(map[string]any{
		"source_sandbox_id": sourceSandboxID,
		"idx":               idx,
	})
	if err != nil {
		return "", fmt.Errorf("marshal fork body: %w", err)
	}
	resp, err := a.h.CloudRuntime.Do(ctx, cloudruntime.Request{
		Method: http.MethodPost,
		Path:   "/api/v1/sandboxes/fork",
		Body:   body,
		Op:     "provision",
	})
	if err != nil {
		return "", fmt.Errorf("fork sandbox: %w", err)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("fork sandbox: status %d: %s", resp.StatusCode, string(resp.Body))
	}
	var out struct {
		SandboxID string `json:"sandbox_id"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return "", fmt.Errorf("fork sandbox: decode: %w", err)
	}
	if out.SandboxID == "" {
		return "", fmt.Errorf("fork sandbox: empty sandbox_id in response")
	}
	return out.SandboxID, nil
}

// DeleteSandbox calls DELETE /api/v1/sandboxes/{id}. Idempotent: a 404 is
// treated as success so the rollback path tolerates races.
func (a *envDispatchDepsAdapter) DeleteSandbox(ctx context.Context, sandboxID string) error {
	resp, err := a.h.CloudRuntime.Do(ctx, cloudruntime.Request{
		Method: http.MethodDelete,
		Path:   "/api/v1/sandboxes/" + sandboxID,
		Op:     "terminate",
	})
	if err != nil {
		return fmt.Errorf("delete sandbox: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("delete sandbox: status %d: %s", resp.StatusCode, string(resp.Body))
	}
	return nil
}

// BootSandbox calls POST /api/v1/sandboxes with image_ref. Used by
// CreateBaseEnv to provision a fresh base env.
func (a *envDispatchDepsAdapter) BootSandbox(ctx context.Context, imageRef string) (string, error) {
	body, err := json.Marshal(map[string]string{"image_ref": imageRef})
	if err != nil {
		return "", fmt.Errorf("marshal boot body: %w", err)
	}
	resp, err := a.h.CloudRuntime.Do(ctx, cloudruntime.Request{
		Method: http.MethodPost,
		Path:   "/api/v1/sandboxes",
		Body:   body,
		Op:     "provision",
	})
	if err != nil {
		return "", fmt.Errorf("boot sandbox: %w", err)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("boot sandbox: status %d: %s", resp.StatusCode, string(resp.Body))
	}
	var out struct {
		SandboxID string `json:"sandbox_id"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return "", fmt.Errorf("boot sandbox: decode: %w", err)
	}
	if out.SandboxID == "" {
		return "", fmt.Errorf("boot sandbox: empty sandbox_id in response")
	}
	return out.SandboxID, nil
}

// CreateProject inserts a new project bound to envID. Used by the scratch
// reset path (spec §7.2): every rollout gets a fresh project.
func (a *envDispatchDepsAdapter) CreateProject(ctx context.Context, workspaceID, name, envID string) (string, error) {
	row, err := a.h.Queries.CreateProjectWithEnv(ctx, db.CreateProjectWithEnvParams{
		WorkspaceID: parseUUID(workspaceID),
		Title:       name,
		Status:      "active",
		Priority:    "medium",
		EnvID:       parseUUID(envID),
	})
	if err != nil {
		return "", fmt.Errorf("create project: %w", err)
	}
	return util.UUIDToString(row.ID), nil
}

// GetProjectByEnvID resolves the 1:1 env→project invariant (partial UNIQUE
// index on project(env_id)). Used by branch dispatch to find the source
// project whose subtree must be copied.
func (a *envDispatchDepsAdapter) GetProjectByEnvID(ctx context.Context, envID, workspaceID string) (string, error) {
	row, err := a.h.Queries.GetProjectByEnvID(ctx, db.GetProjectByEnvIDParams{
		EnvID:       parseUUID(envID),
		WorkspaceID: parseUUID(workspaceID),
	})
	if err != nil {
		return "", fmt.Errorf("get project by env: %w", err)
	}
	return util.UUIDToString(row.ID), nil
}

// DeleteProject deletes the project row; cascades to issues / chat / tasks
// via FK. Idempotent: a missing row is treated as success.
func (a *envDispatchDepsAdapter) DeleteProject(ctx context.Context, projectID, workspaceID string) error {
	err := a.h.Queries.DeleteProject(ctx, db.DeleteProjectParams{
		ID:          parseUUID(projectID),
		WorkspaceID: parseUUID(workspaceID),
	})
	if err == nil || errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return fmt.Errorf("delete project: %w", err)
}

// ListIssuesByProject returns all issues under a project. Used during
// CopyProjectSubtree to deep-copy the source project's issues.
func (a *envDispatchDepsAdapter) ListIssuesByProject(ctx context.Context, projectID, workspaceID string) ([]service.IssueRow, error) {
	rows, err := a.h.Queries.ListIssuesByProject(ctx, db.ListIssuesByProjectParams{
		ProjectID:   parseUUID(projectID),
		WorkspaceID: parseUUID(workspaceID),
	})
	if err != nil {
		return nil, fmt.Errorf("list issues by project: %w", err)
	}
	out := make([]service.IssueRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, issueRowToService(r))
	}
	return out, nil
}

func issueRowToService(r db.Issue) service.IssueRow {
	out := service.IssueRow{
		ID:        util.UUIDToString(r.ID),
		ProjectID: util.UUIDToString(r.ProjectID),
		Title:     r.Title,
	}
	if r.Description.Valid {
		out.Description = r.Description.String
	}
	return out
}

// ListChatSessionsByProject returns the chat session ids under a project.
// Used by the service to enforce the branch+self_play "exactly one session"
// rule (§7.4).
func (a *envDispatchDepsAdapter) ListChatSessionsByProject(ctx context.Context, projectID, workspaceID string) ([]string, error) {
	rows, err := a.h.Queries.ListChatSessionsByProject(ctx, db.ListChatSessionsByProjectParams{
		ProjectID:   parseUUID(projectID),
		WorkspaceID: parseUUID(workspaceID),
	})
	if err != nil {
		return nil, fmt.Errorf("list chat sessions by project: %w", err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, util.UUIDToString(r.ID))
	}
	return out, nil
}

// CreateIssue inserts an issue with metadata pre-populated with
// acceptance_criteria / fail_to_pass / pass_to_pass (swe_lego). issueNumber
// is allocated via IncrementIssueCounter so the new issue gets a
// workspace-scoped number; position is set to 1.0 (the source project's
// issues are forked, not swimlane-ordered).
func (a *envDispatchDepsAdapter) CreateIssue(ctx context.Context, projectID, workspaceID, creatorID, title, description string, acceptanceCriteria, failToPass, passToPass []string) (string, error) {
	wsUUID := parseUUID(workspaceID)
	number, err := a.h.Queries.IncrementIssueCounter(ctx, wsUUID)
	if err != nil {
		return "", fmt.Errorf("increment issue counter: %w", err)
	}
	metaJSON, err := buildIssueMetadata(acceptanceCriteria, failToPass, passToPass)
	if err != nil {
		return "", err
	}
	desc := pgtype.Text{}
	if description != "" {
		desc = pgtype.Text{String: description, Valid: true}
	}
	row, err := a.h.Queries.CreateIssueWithMetadata(ctx, db.CreateIssueWithMetadataParams{
		WorkspaceID: wsUUID,
		Title:       title,
		Description: desc,
		Status:      "backlog",
		Priority:    "none",
		CreatorType: "member",
		CreatorID:   parseUUID(creatorID),
		Position:    1.0,
		Number:      number,
		ProjectID:   parseUUID(projectID),
		Metadata:    metaJSON,
	})
	if err != nil {
		return "", fmt.Errorf("create issue: %w", err)
	}
	return util.UUIDToString(row.ID), nil
}

// buildIssueMetadata assembles the JSONB metadata object for a swe_lego
// issue. Empty arrays serialize as `[]` so the metadata stays a stable shape
// downstream consumers can rely on.
func buildIssueMetadata(acceptanceCriteria, failToPass, passToPass []string) ([]byte, error) {
	if acceptanceCriteria == nil {
		acceptanceCriteria = []string{}
	}
	if failToPass == nil {
		failToPass = []string{}
	}
	if passToPass == nil {
		passToPass = []string{}
	}
	m := map[string]any{
		"acceptance_criteria": acceptanceCriteria,
		"fail_to_pass":        failToPass,
		"pass_to_pass":        passToPass,
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal issue metadata: %w", err)
	}
	return b, nil
}

// CreateChatSession creates a chat session bound to a project. Used by
// scratch+self_play (and CopyProjectSubtree via the SQL-level INSERT) for the
// new session under the freshly-created project.
func (a *envDispatchDepsAdapter) CreateChatSession(ctx context.Context, projectID, workspaceID, agentID, creatorID string) (string, error) {
	row, err := a.h.Queries.CreateChatSessionForProject(ctx, db.CreateChatSessionForProjectParams{
		WorkspaceID: parseUUID(workspaceID),
		ProjectID:   parseUUID(projectID),
		AgentID:     parseUUID(agentID),
		CreatorID:   parseUUID(creatorID),
		Title:       "env-dispatch",
	})
	if err != nil {
		return "", fmt.Errorf("create chat session: %w", err)
	}
	return util.UUIDToString(row.ID), nil
}

// CreateChatMessage appends a (role, content) message to a chat session. The
// dispatch path only ever inserts role='user' messages; assistant messages
// arrive later from the agent runtime. task_id / failure_reason / elapsed_ms
// are NULL on insert.
func (a *envDispatchDepsAdapter) CreateChatMessage(ctx context.Context, sessionID, role, content string) (string, error) {
	row, err := a.h.Queries.CreateChatMessage(ctx, db.CreateChatMessageParams{
		ChatSessionID: parseUUID(sessionID),
		Role:          role,
		Content:       content,
	})
	if err != nil {
		return "", fmt.Errorf("create chat message: %w", err)
	}
	return util.UUIDToString(row.ID), nil
}

// GetDefaultSelfPlayEnv resolves the workspace's configured default self_play
// base env. Returns "" (not an error) when the column is NULL/unset so the
// service can map an empty result to a 400 validation_failed. Any real query
// error is surfaced.
func (a *envDispatchDepsAdapter) GetDefaultSelfPlayEnv(ctx context.Context, workspaceID string) (string, error) {
	v, err := a.h.Queries.GetDefaultSelfPlayEnv(ctx, parseUUID(workspaceID))
	if err != nil {
		return "", fmt.Errorf("get default self_play env: %w", err)
	}
	if !v.Valid {
		return "", nil // not configured; service maps to 400
	}
	return util.UUIDToString(v), nil
}

// EnqueueAgentRun enqueues an agent task. issueID set → CreateAgentTask
// (issue-bound; chat_session_id NULL). chatSessionID set → CreateChatTask
// (chat-bound; issue_id NULL). Both paths need runtime_id, resolved via
// GetAgent (issue path) or GetChatSession (chat path).
//
// initiator_user_id is left NULL: the service's EnqueueAgentRun signature
// carries workspaceID/agentID/issue|chat but not the dispatch's UserID, and
// threading UserID through would require an interface change touching the
// service fake. The column is nullable, and the daemon resolves ownership
// via agent_id + chat_session_id, so NULL is a safe intermediate state.
// A follow-up task should extend the interface to pass UserID explicitly.
//
// squadID threads through for team dispatch. The issue-path squad branch
// (assignee=squad + leader task) and the chat-path squad branch (leader
// resolution + {"squad_id"} context hint) are implemented below. When
// squadID == "" both paths behave exactly as the current single-agent path.
func (a *envDispatchDepsAdapter) EnqueueAgentRun(ctx context.Context, workspaceID, agentID, squadID, issueID, chatSessionID, sandboxID, envID string, idx int) (string, error) {
	switch {
	case issueID != "":
		var agentUUID pgtype.UUID
		if agentID != "" {
			agentUUID = parseUUID(agentID)
		}
		isLeaderTask := pgtype.Bool{}
		if squadID != "" {
			// Squad dispatch: stamp the issue with assignee_type='squad' and
			// enqueue the squad LEADER's task with is_leader_task=true so the
			// leader-task ownership rules apply. The agent_id is resolved to
			// the squad leader (agentID is empty for squad dispatch).
			squad, err := a.h.Queries.GetSquadInWorkspace(ctx, db.GetSquadInWorkspaceParams{
				ID:          parseUUID(squadID),
				WorkspaceID: parseUUID(workspaceID),
			})
			if err != nil {
				return "", fmt.Errorf("get squad: %w", err)
			}
			if err := a.h.Queries.SetIssueAssignee(ctx, db.SetIssueAssigneeParams{
				ID:           parseUUID(issueID),
				AssigneeType: pgtype.Text{String: "squad", Valid: true},
				AssigneeID:   parseUUID(squadID),
				WorkspaceID:  parseUUID(workspaceID),
			}); err != nil {
				return "", fmt.Errorf("set issue assignee to squad: %w", err)
			}
			agentUUID = squad.LeaderID
			isLeaderTask = pgtype.Bool{Bool: true, Valid: true}
		}
		agent, err := a.h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID:          agentUUID,
			WorkspaceID: parseUUID(workspaceID),
		})
		if err != nil {
			return "", fmt.Errorf("get agent for run: %w", err)
		}
		task, err := a.h.Queries.CreateAgentTask(ctx, db.CreateAgentTaskParams{
			AgentID:      agentUUID,
			RuntimeID:    agent.RuntimeID,
			IssueID:      parseUUID(issueID),
			Priority:     envDispatchTaskPriority,
			IsLeaderTask: isLeaderTask,
		})
		if err != nil {
			return "", fmt.Errorf("create agent task: %w", err)
		}
		// Training session-open chokepoint (spec §4.3): a single-agent training
		// dispatch (train_agent_id == agent_id) creates the trained task HERE,
		// not via a later @mention. Resolve the owning project via the issue.
		a.maybeOpenTrainingSession(ctx, util.UUIDToString(task.ID), util.UUIDToString(agentUUID), a.issueProjectID(ctx, issueID), envID)
		return util.UUIDToString(task.ID), nil
	case chatSessionID != "":
		session, err := a.h.Queries.GetChatSession(ctx, parseUUID(chatSessionID))
		if err != nil {
			return "", fmt.Errorf("get chat session for run: %w", err)
		}
		params := db.CreateChatTaskParams{
			RuntimeID:     session.RuntimeID,
			Priority:      envDispatchTaskPriority,
			ChatSessionID: session.ID,
		}
		if squadID != "" {
			// Squad dispatch: run the chat task on the squad LEADER and stamp
			// the task context with a {"squad_id": ...} hint. The daemon claim
			// path consumes that hint to inject the squad-leader briefing when
			// the leader claims the task. The leader's runtime_id (not the
			// session's) is used so the task is delivered to the leader.
			squad, err := a.h.Queries.GetSquadInWorkspace(ctx, db.GetSquadInWorkspaceParams{
				ID:          parseUUID(squadID),
				WorkspaceID: parseUUID(workspaceID),
			})
			if err != nil {
				return "", fmt.Errorf("get squad: %w", err)
			}
			leader, err := a.h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
				ID:          squad.LeaderID,
				WorkspaceID: parseUUID(workspaceID),
			})
			if err != nil {
				return "", fmt.Errorf("get squad leader: %w", err)
			}
			contextJSON, err := json.Marshal(map[string]string{"squad_id": squadID})
			if err != nil {
				return "", fmt.Errorf("marshal squad chat context: %w", err)
			}
			params.AgentID = squad.LeaderID
			params.RuntimeID = leader.RuntimeID
			params.Context = contextJSON
		} else {
			params.AgentID = parseUUID(agentID)
		}
		task, err := a.h.Queries.CreateChatTask(ctx, params)
		if err != nil {
			return "", fmt.Errorf("create chat task: %w", err)
		}
		// Training session-open chokepoint (spec §4.3): chat-bound dispatch.
		// Project resolves via the chat session (seam 1e).
		a.maybeOpenTrainingSession(ctx, util.UUIDToString(task.ID), util.UUIDToString(params.AgentID), util.UUIDToString(session.ProjectID), envID)
		return util.UUIDToString(task.ID), nil
	default:
		return "", fmt.Errorf("enqueue agent run: issueID or chatSessionID required")
	}
}

// envDispatchTaskPriority is the priority assigned to every env-dispatch
// enqueued task. Existing callers of CreateAgentTask default to 5; we mirror
// that convention so dispatch tasks coexist with manual triggers without
// jumping the queue.
const envDispatchTaskPriority int32 = 5

// CopyProjectSubtree deep-copies the source project's issues + chat sessions
// + messages into a new project bound to envID. Returns source→new ID maps
// for issues and chat sessions so the service can target the copied entities
// during dispatch. The new project inherits the source's title with a
// "-fork" suffix so it is distinguishable in the UI.
//
// Forked issues record forked_from_issue_id (but NOT forked_at_seq /
// forked_at_task_id, since this is not a task-message branch point — those
// stay NULL). Forked chat sessions/messages are straight copies under the
// new project.
func (a *envDispatchDepsAdapter) CopyProjectSubtree(ctx context.Context, sourceProjectID, workspaceID, envID string) (string, map[string]string, map[string]string, error) {
	wsUUID := parseUUID(workspaceID)
	srcProjectID := parseUUID(sourceProjectID)

	src, err := a.h.Queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{
		ID:          srcProjectID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		return "", nil, nil, fmt.Errorf("get source project: %w", err)
	}

	newProject, err := a.h.Queries.CreateProjectWithEnv(ctx, db.CreateProjectWithEnvParams{
		WorkspaceID: wsUUID,
		Title:       src.Title + "-fork",
		Status:      src.Status,
		Priority:    src.Priority,
		EnvID:       parseUUID(envID),
	})
	if err != nil {
		return "", nil, nil, fmt.Errorf("create forked project: %w", err)
	}
	newProjectID := util.UUIDToString(newProject.ID)

	issueIDMap, err := a.copyProjectIssues(ctx, wsUUID, srcProjectID, newProject.ID)
	if err != nil {
		return "", nil, nil, err
	}

	chatSessionIDMap, err := a.copyProjectChatSessions(ctx, wsUUID, srcProjectID, newProject.ID)
	if err != nil {
		return "", nil, nil, err
	}

	return newProjectID, issueIDMap, chatSessionIDMap, nil
}

// copyProjectIssues deep-copies every issue in the source project to the new
// project, recording fork provenance via forked_from_issue_id. Returns a
// source-issue-id → new-issue-id map.
func (a *envDispatchDepsAdapter) copyProjectIssues(ctx context.Context, wsUUID, srcProjectID, newProjectID pgtype.UUID) (map[string]string, error) {
	srcs, err := a.h.Queries.ListIssuesByProject(ctx, db.ListIssuesByProjectParams{
		ProjectID:   srcProjectID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		return nil, fmt.Errorf("list source issues: %w", err)
	}
	issueIDMap := make(map[string]string, len(srcs))
	for _, src := range srcs {
		number, err := a.h.Queries.IncrementIssueCounter(ctx, wsUUID)
		if err != nil {
			return nil, fmt.Errorf("increment issue counter: %w", err)
		}
		forked, err := a.h.Queries.CreateForkedIssue(ctx, db.CreateForkedIssueParams{
			WorkspaceID:        src.WorkspaceID,
			Title:              src.Title,
			Description:        src.Description,
			Status:             src.Status,
			Priority:           src.Priority,
			AssigneeType:       src.AssigneeType,
			AssigneeID:         src.AssigneeID,
			CreatorType:        src.CreatorType,
			CreatorID:          src.CreatorID,
			ParentIssueID:      src.ParentIssueID,
			AcceptanceCriteria: src.AcceptanceCriteria,
			ContextRefs:        src.ContextRefs,
			Position:           src.Position,
			StartDate:          src.StartDate,
			DueDate:            src.DueDate,
			Metadata:           src.Metadata,
			Number:             number,
			ProjectID:          newProjectID,
			ForkedFromIssueID:  src.ID,
			// forked_at_seq / forked_at_task_id intentionally left invalid:
			// this is a project-level fork, not a task-message branch point.
		})
		if err != nil {
			return nil, fmt.Errorf("create forked issue: %w", err)
		}
		issueIDMap[util.UUIDToString(src.ID)] = util.UUIDToString(forked.ID)
	}
	return issueIDMap, nil
}

// copyProjectChatSessions deep-copies every chat session + its messages from
// the source project to the new project. Returns a source-session-id →
// new-session-id map. We use CreateChatSessionForProject so the new session
// inherits the agent's runtime_id via the subquery, then re-link each
// session's messages via CreateChatMessage.
func (a *envDispatchDepsAdapter) copyProjectChatSessions(ctx context.Context, wsUUID, srcProjectID, newProjectID pgtype.UUID) (map[string]string, error) {
	srcs, err := a.h.Queries.ListChatSessionsByProject(ctx, db.ListChatSessionsByProjectParams{
		ProjectID:   srcProjectID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		return nil, fmt.Errorf("list source chat sessions: %w", err)
	}
	chatSessionIDMap := make(map[string]string, len(srcs))
	for _, src := range srcs {
		newSession, err := a.h.Queries.CreateChatSessionForProject(ctx, db.CreateChatSessionForProjectParams{
			WorkspaceID: wsUUID,
			ProjectID:   newProjectID,
			AgentID:     src.AgentID,
			CreatorID:   src.CreatorID,
			Title:       src.Title,
		})
		if err != nil {
			return nil, fmt.Errorf("create forked chat session: %w", err)
		}
		if err := a.copyChatMessages(ctx, src.ID, newSession.ID); err != nil {
			return nil, err
		}
		chatSessionIDMap[util.UUIDToString(src.ID)] = util.UUIDToString(newSession.ID)
	}
	return chatSessionIDMap, nil
}

// copyChatMessages copies every message from srcSessionID into newSessionID
// in created_at order. task_id / failure_reason / elapsed_ms are not carried
// over — the forked session starts with a clean task pointer; the new agent
// run will write fresh task IDs as it appends.
func (a *envDispatchDepsAdapter) copyChatMessages(ctx context.Context, srcSessionID, newSessionID pgtype.UUID) error {
	msgs, err := a.h.Queries.ListChatMessages(ctx, srcSessionID)
	if err != nil {
		return fmt.Errorf("list source chat messages: %w", err)
	}
	for _, m := range msgs {
		if _, err := a.h.Queries.CreateChatMessage(ctx, db.CreateChatMessageParams{
			ChatSessionID: newSessionID,
			Role:          m.Role,
			Content:       m.Content,
		}); err != nil {
			return fmt.Errorf("create forked chat message: %w", err)
		}
	}
	return nil
}

// GetIdempotentResponse looks up the env_dispatch_request row for the
// (workspace, key) pair. A missing row returns ok=false so the service
// proceeds with a fresh dispatch. The stored JSONB response is decoded into
// service.EnvDispatchResult; a decode failure is surfaced as an error rather
// than silently treated as a miss, since replaying a corrupt response would
// hand the caller stale IDs.
func (a *envDispatchDepsAdapter) GetIdempotentResponse(ctx context.Context, workspaceID, key string) (service.EnvDispatchResult, bool, error) {
	row, err := a.h.Queries.GetEnvDispatchRequest(ctx, db.GetEnvDispatchRequestParams{
		WorkspaceID:    parseUUID(workspaceID),
		IdempotencyKey: parseUUID(key),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return service.EnvDispatchResult{}, false, nil
		}
		return service.EnvDispatchResult{}, false, fmt.Errorf("get idempotent response: %w", err)
	}
	var res service.EnvDispatchResult
	if err := json.Unmarshal(row.Response, &res); err != nil {
		return service.EnvDispatchResult{}, false, fmt.Errorf("decode idempotent response: %w", err)
	}
	return res, true, nil
}

// SaveIdempotentResponse persists the dispatch response JSONB for replay on
// a retry with the same idempotency key. Best-effort per the spec; the
// service swallows the error so a ledger write failure does not fail the
// dispatch.
func (a *envDispatchDepsAdapter) SaveIdempotentResponse(ctx context.Context, workspaceID, key string, res service.EnvDispatchResult) error {
	body, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("marshal idempotent response: %w", err)
	}
	if err := a.h.Queries.CreateEnvDispatchRequest(ctx, db.CreateEnvDispatchRequestParams{
		WorkspaceID:    parseUUID(workspaceID),
		IdempotencyKey: parseUUID(key),
		Response:       body,
	}); err != nil {
		return fmt.Errorf("save idempotent response: %w", err)
	}
	return nil
}

// SaveTrainingDispatch persists the training intent for a rollout project
// (spec §4.1): one row per rollout project when a dispatch carries a
// train_agent_id, keyed by project_id (upsert on conflict) so the later
// session-open hook can resolve the training target + default reward.
func (a *envDispatchDepsAdapter) SaveTrainingDispatch(ctx context.Context, projectID, workspaceID, trainAgentID, criticAgentID string, defaultReward float64) error {
	params := db.CreateTrainingDispatchParams{
		ProjectID:     parseUUID(projectID),
		WorkspaceID:   parseUUID(workspaceID),
		TrainAgentID:  parseUUID(trainAgentID),
		DefaultReward: defaultReward,
	}
	if criticAgentID != "" {
		params.CriticAgentID = parseUUID(criticAgentID)
	}
	if err := a.h.Queries.CreateTrainingDispatch(ctx, params); err != nil {
		return fmt.Errorf("save training dispatch: %w", err)
	}
	return nil
}

// ValidateAgentInWorkspaceOrSquad reports whether agentID is a member of the
// squad (when squadID is set) or the workspace. Returns a typed error when the
// agent is unknown or unauthorized.
func (a *envDispatchDepsAdapter) ValidateAgentInWorkspaceOrSquad(ctx context.Context, workspaceID, squadID, agentID string) error {
	agentUUID, err := util.ParseUUID(agentID)
	if err != nil {
		return fmt.Errorf("parse agent_id: %w", err)
	}
	if squadID != "" {
		squadUUID, err := util.ParseUUID(squadID)
		if err != nil {
			return fmt.Errorf("parse squad_id: %w", err)
		}
		ok, err := a.h.Queries.IsSquadMember(ctx, db.IsSquadMemberParams{
			SquadID:    squadUUID,
			MemberType: "agent",
			MemberID:   agentUUID,
		})
		if err != nil {
			return fmt.Errorf("check squad membership: %w", err)
		}
		if !ok {
			return fmt.Errorf("agent %s is not a member of squad %s", agentID, squadID)
		}
		return nil
	}
	// No squad: validate the agent belongs to the workspace.
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return fmt.Errorf("parse workspace_id: %w", err)
	}
	if _, err := a.h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
		ID:          agentUUID,
		WorkspaceID: wsUUID,
	}); err != nil {
		return fmt.Errorf("agent %s is not a member of workspace %s", agentID, workspaceID)
	}
	return nil
}

// ResolvePerAgentEnvSpec validates that the spec's base_env_id is known and
// authorized (templates are pass-through; there is no template registry), and
// returns a ref carrying the resolved template. For BaseEnvID, the template is
// resolved from the env's first sandbox_instance; for Template, the spec's
// template is used directly.
func (a *envDispatchDepsAdapter) ResolvePerAgentEnvSpec(ctx context.Context, workspaceID string, spec service.PerAgentEnvSpec) (service.SandboxInstanceRef, error) {
	template := spec.Template
	if spec.BaseEnvID != "" {
		env, err := a.GetEnv(ctx, spec.BaseEnvID, workspaceID)
		if err != nil {
			return service.SandboxInstanceRef{}, fmt.Errorf("resolve base env %s: %w", spec.BaseEnvID, err)
		}
		// Resolve the template from the base env's first sandbox_instance, if
		// any; otherwise fall back to "default".
		if len(env.SandboxIDs) > 0 {
			if lcDeps := newEnvSandboxLifecycleDepsAdapter(a.h); lcDeps != nil {
				if ref, err := lcDeps.GetSandboxInstanceRef(ctx, workspaceID, env.SandboxIDs[0]); err == nil && ref.Template != "" {
					template = ref.Template
				}
			}
		}
		if template == "" {
			template = "default"
		}
	}
	return service.SandboxInstanceRef{Template: template, WorkspaceID: workspaceID}, nil
}

// maybeOpenTrainingSession fires the shared session-open hook for a task
// created at dispatch time. It delegates to TaskService (no-op when training is
// unconfigured) and logs any error loudly — a trained task must never run
// un-proxied silently. Errors are not propagated: the task row already exists.
func (a *envDispatchDepsAdapter) maybeOpenTrainingSession(ctx context.Context, taskID, agentID, projectID, envID string) {
	if a.h.TaskService == nil {
		return
	}
	if err := a.h.TaskService.MaybeOpenTrainingSession(ctx, taskID, agentID, projectID, envID); err != nil {
		slog.Error("training session open failed (env_dispatch)",
			"task_id", taskID, "agent_id", agentID, "project_id", projectID, "env_id", envID, "error", err)
	}
}

// issueProjectID resolves the owning project for an issue-bound dispatch task
// (seam 1e). Returns "" when the issue can't be loaded or has no project, in
// which case the session-open hook no-ops.
func (a *envDispatchDepsAdapter) issueProjectID(ctx context.Context, issueID string) string {
	issue, err := a.h.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		return ""
	}
	return util.UUIDToString(issue.ProjectID)
}

// stubEnvDispatchDeps is a no-op Deps implementation used when the handler
// is constructed without a *db.Queries (test fixtures that exercise only the
// validation paths). Every method returns a zero value / nil error; the
// service's validation gate runs before any of these are reached, so
// handler validation tests stay green without a DB.
type stubEnvDispatchDeps struct{}

func (s *stubEnvDispatchDeps) GetEnv(context.Context, string, string) (service.Env, error) {
	return service.Env{}, nil
}
func (s *stubEnvDispatchDeps) CreateEnv(context.Context, string, []string, string, service.EnvMode, service.EnvDomain) (string, error) {
	return "stub-env", nil
}
func (s *stubEnvDispatchDeps) DeleteEnv(context.Context, string, string) error { return nil }
func (s *stubEnvDispatchDeps) ForkSandbox(context.Context, string, int) (string, error) {
	return "stub-fork", nil
}
func (s *stubEnvDispatchDeps) DeleteSandbox(context.Context, string) error { return nil }
func (s *stubEnvDispatchDeps) BootSandbox(context.Context, string) (string, error) {
	return "stub-boot", nil
}
func (s *stubEnvDispatchDeps) CreateProject(context.Context, string, string, string) (string, error) {
	return "stub-project", nil
}
func (s *stubEnvDispatchDeps) CopyProjectSubtree(context.Context, string, string, string) (string, map[string]string, map[string]string, error) {
	return "stub-copy", map[string]string{}, map[string]string{}, nil
}
func (s *stubEnvDispatchDeps) GetProjectByEnvID(context.Context, string, string) (string, error) {
	return "stub-project", nil
}
func (s *stubEnvDispatchDeps) GetIdempotentResponse(context.Context, string, string) (service.EnvDispatchResult, bool, error) {
	return service.EnvDispatchResult{}, false, nil
}
func (s *stubEnvDispatchDeps) SaveIdempotentResponse(context.Context, string, string, service.EnvDispatchResult) error {
	return nil
}
func (s *stubEnvDispatchDeps) DeleteProject(context.Context, string, string) error { return nil }
func (s *stubEnvDispatchDeps) ListIssuesByProject(context.Context, string, string) ([]service.IssueRow, error) {
	return nil, nil
}
func (s *stubEnvDispatchDeps) ListChatSessionsByProject(context.Context, string, string) ([]string, error) {
	return nil, nil
}
func (s *stubEnvDispatchDeps) CreateIssue(context.Context, string, string, string, string, string, []string, []string, []string) (string, error) {
	return "stub-issue", nil
}
func (s *stubEnvDispatchDeps) CreateChatSession(context.Context, string, string, string, string) (string, error) {
	return "stub-session", nil
}
func (s *stubEnvDispatchDeps) CreateChatMessage(context.Context, string, string, string) (string, error) {
	return "stub-msg", nil
}
func (s *stubEnvDispatchDeps) EnqueueAgentRun(context.Context, string, string, string, string, string, string, string, int) (string, error) {
	return "stub-run", nil
}
func (s *stubEnvDispatchDeps) GetDefaultSelfPlayEnv(context.Context, string) (string, error) {
	return "stub-env", nil
}
func (s *stubEnvDispatchDeps) SaveTrainingDispatch(context.Context, string, string, string, string, float64) error {
	return nil
}
func (s *stubEnvDispatchDeps) ValidateAgentInWorkspaceOrSquad(context.Context, string, string, string) error {
	return nil
}
func (s *stubEnvDispatchDeps) ResolvePerAgentEnvSpec(context.Context, string, service.PerAgentEnvSpec) (service.SandboxInstanceRef, error) {
	return service.SandboxInstanceRef{}, nil
}
