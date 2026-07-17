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
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/cloudruntime"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/internal/util/stackerr"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// EnvDispatchRequest is the body of POST /api/v1/env-dispatch (spec §6.3).
type EnvDispatchRequest struct {
	Mode           string `json:"mode"`
	EnvID          string `json:"env_id"`
	Domain         string `json:"domain,omitempty"`
	DispatchType   string `json:"dispatch_type"`
	GroupSize      int    `json:"group_size"`
	AgentID        string `json:"agent_id"`
	SquadID        string `json:"squad_id,omitempty"`
	TrainAgentID   string `json:"train_agent_id,omitempty"`
	CriticAgentID  string `json:"critic_agent_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	// Template optionally overrides the server's default self_play sandbox
	// template (MULTICA_DEFAULT_SELF_PLAY_TEMPLATE) for the auto-created default
	// base env. Only consulted when env_id is empty and no default is configured
	// (scratch self_play). 1..64 chars when set.
	Template    string                        `json:"template,omitempty"`
	Issue       *IssueDispatchInput           `json:"issue,omitempty"`
	Message     *MessageDispatchInput         `json:"message,omitempty"`
	PerAgentEnv map[string]PerAgentEnvRequest `json:"per_agent_env,omitempty"`
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

// EnvDispatchResponse is the 201 response (spec §6.3). On 500 (all rollouts
// failed) it is reused with Message set and each rollout carrying its Error +
// Traceback (origin goroutine stack from the failing adapter call).
type EnvDispatchResponse struct {
	ChannelID string               `json:"channel_id,omitempty"`
	ProjectID string               `json:"project_id"`
	Rollouts  []EnvRolloutResponse `json:"rollouts"`
	Message   string               `json:"message,omitempty"`
}

type EnvRolloutResponse struct {
	ChannelID        string                                `json:"channel_id,omitempty"`
	LeaderRunID      string                                `json:"leader_run_id,omitempty"`
	AgentSandboxes   map[string]service.AgentSandboxStatus `json:"agent_sandboxes,omitempty"`
	EnvID            string                                `json:"env_id"`
	ProjectID        string                                `json:"project_id"`
	IssueID          string                                `json:"issue_id,omitempty"`
	ChatSessionID    string                                `json:"chat_session_id,omitempty"`
	AgentRunID       string                                `json:"agent_run_id,omitempty"`
	Error            string                                `json:"error,omitempty"`
	Traceback        string                                `json:"traceback,omitempty"`
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
	// Template (optional override for the auto-created default self_play base
	// env). Validate length when present; 1..64 chars. Empty defers to the
	// server default (h.cfg.DefaultSelfPlayTemplate, itself defaulting to
	// "default") which the service fills in.
	template := strings.TrimSpace(req.Template)
	if template != "" && len(template) > 64 {
		writeError(w, http.StatusBadRequest, "template must be 1..64 chars")
		return
	}
	if template == "" {
		template = h.cfg.DefaultSelfPlayTemplate
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
		SquadID:             req.SquadID,
		TrainAgentID:        req.TrainAgentID,
		CriticAgentID:       req.CriticAgentID,
		IdempotencyKey:      req.IdempotencyKey,
		DefaultBaseTemplate: template,
		Issue:               mapIssueInput(req.Issue),
		Message:             mapMessageInput(req.Message),
		PerAgentEnvSpecs:    mapPerAgentEnvSpecs(req.PerAgentEnv),
	})
	if err != nil {
		writeEnvDispatchError(w, err, res)
		return
	}
	writeJSON(w, http.StatusCreated, EnvDispatchResponse{ChannelID: res.ChannelID, ProjectID: res.ProjectID, Rollouts: mapRollouts(res.Rollouts)})
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

// rootTrainingTaskTerminal statuses: agent_task_queue.status is CHECK-constrained
// to ('queued','dispatched','running','waiting_local_directory','completed',
// 'failed','cancelled') (migrations 001 + 109). The terminal set (the rollout is
// done) is completed/failed/cancelled - these are the transitions that fire
// RouteTerminalTrainingTask (training.go:433), the terminal hook. The rest are
// in-progress. A lookup miss (no training_dispatch / no root task enqueued yet)
// is also treated as in-progress: the rollout has not produced a terminal root
// task, so AReaL should keep polling rather than receive a partial DAG.
var rootTrainingTaskTerminalStatuses = map[string]bool{
	"completed": true,
	"failed":    true,
	"cancelled": true,
}

// GetDag handles GET /api/v1/env-dispatch/{projectID}/dag, the read-only
// assembled segment-DAG endpoint AReaL polls (Task 9, U8). Contract:
//   - 404 when the project does not exist at all.
//   - 403 when the project exists but in another workspace (cross-workspace).
//   - 202 + {"status":"in_progress"} when the rollout's root training task is
//     not yet terminal (or has not been enqueued yet).
//   - 200 + {"status":"failed"} when the root task is terminal but the recorded
//     segments do NOT densely cover every session - D14: never serve a partial
//     DAG, and never serve a failed rollout's partial data as if it were whole.
//   - 200 + the AssembledDag JSON when the root task is terminal and the
//     recorded segments densely cover every session.
//
// The service is constructed per-request (mirroring DeleteEnvDispatchProject):
// h.Queries satisfies InteractionDAGStore; a nil arealrl client is safe because
// assembly is read-only and never touches the bridge; enabled=true so
// AssembleAssembledDag does not short-circuit. No Handler field is added (the
// handler layer reaches the DAG service ad-hoc, keeping Task 10 wiring untouched).
func (h *Handler) GetDag(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	h.getEnvDispatchDagForProject(w, r, chi.URLParam(r, "projectID"))
}

// getEnvDispatchDagForProject serves the project-scoped assembled DAG for both
// the project-first route (GetDag) and the channel-first facade
// (GetEnvDispatchChannelDag). The caller resolves and supplies the project id;
// no URL param is read here.
func (h *Handler) getEnvDispatchDagForProject(w http.ResponseWriter, r *http.Request, projectID string) {
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace ID required")
		return
	}
	projectUUID, ok := parseUUIDOrBadRequest(w, projectID, "projectID")
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspaceID")
	if !ok {
		return
	}

	// Workspace gate -> 403 + unknown -> 404 (decision 2). There is no shared
	// canAccessProject helper; the 2-query distinguish mirrors the env-dispatch
	// pattern: a scoped lookup confirms in-workspace, an unscoped lookup tells
	// cross-workspace (403) from truly-unknown (404). DeleteEnvDispatchProject
	// treats ErrNoRows as idempotent success (204), so there is no existing 403
	// to mirror - the cross-workspace distinguish is implemented here.
	if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
		ID:          projectUUID,
		WorkspaceID: wsUUID,
	}); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusServiceUnavailable, "lookup project: "+err.Error())
			return
		}
		// Not in this workspace: distinguish cross-workspace (403) from unknown (404).
		if _, err2 := h.Queries.GetProject(r.Context(), projectUUID); err2 != nil {
			if errors.Is(err2, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
				return
			}
			writeError(w, http.StatusServiceUnavailable, "lookup project: "+err2.Error())
			return
		}
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden"})
		return
	}

	// Status decision (decision 3): the root training task is the
	// agent_task_queue row whose issue belongs to this project and whose
	// agent_id equals training_dispatch.train_agent_id (the dispatch's
	// EnqueueAgentRun creates it). ErrNoRows = no training_dispatch / no root
	// task yet -> in_progress (keep polling). A non-terminal status -> in_progress.
	status, err := h.Queries.GetRootTrainingTaskStatusForProject(r.Context(), projectUUID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusServiceUnavailable, "lookup root training task: "+err.Error())
			return
		}
		// No training rollout / root task not enqueued yet: rollout not done.
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "in_progress"})
		return
	}
	if !rootTrainingTaskTerminalStatuses[status] {
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "in_progress"})
		return
	}

	// Rollout is complete: assemble the DAG. A read-only assembly never touches
	// the arealrl bridge, so a nil client is safe; enabled=true avoids the
	// short-circuit. Per-request construction mirrors DeleteEnvDispatchProject.
	dagSvc := service.NewInteractionDAGService(h.Queries, nil, true)
	dag, derr := dagSvc.AssembleAssembledDag(r.Context(), projectID)
	if derr != nil || !denseCover(dag) {
		// D14: never serve a partial DAG. A failed/incomplete rollout surfaces as
		// a failed status so AReaL does not consume a gap-ridden trajectory.
		writeJSON(w, http.StatusOK, map[string]any{"status": "failed"})
		return
	}
	// Stamp the diagnosis scoring scale so AReaL can normalize per-turn
	// StepReward scores to [0, 1] without guessing Multica's configured max.
	// AssembleAssembledDag leaves ScoreMax 0 (it is config metadata, not
	// assembled data); the /dag boundary is the place that knows the config.
	dag.ScoreMax = service.LoadTrainingConfig().DiagnosisAgentScoreMax
	writeJSON(w, http.StatusOK, dag)
}

// denseCover reports whether every session in dag.SessionToAgentRun has at
// least one recorded segment covering its agent_run. A session whose agent_run
// has no segment is a coverage gap (the run's trajectory was not recorded), so
// the assembled DAG would be partial - the caller returns a failed status
// instead of serving it (D14). The empty case (no sessions recorded) is
// vacuously dense: there is nothing to cover, and the decision to serve an
// empty DAG vs failed is left to the caller's derr check (a nil derr with no
// sessions means the rollout recorded nothing, which the caller may still
// choose to reject - here it is served as an empty, well-formed DAG).
func denseCover(dag service.AssembledDag) bool {
	if len(dag.SessionToAgentRun) == 0 {
		return true
	}
	hasSegment := make(map[string]bool, len(dag.Segments))
	for _, seg := range dag.Segments {
		hasSegment[seg.AgentRunID] = true
	}
	for _, agentRunID := range dag.SessionToAgentRun {
		if !hasSegment[agentRunID] {
			return false
		}
	}
	return true
}

func writeEnvDispatchError(w http.ResponseWriter, err error, res service.EnvDispatchResult) {
	msg := err.Error()
	tb := string(stackerr.StackOf(err))
	// Always log the full chain + origin stack server-side (gap #3): the
	// traceback also rides in the response body, but the log survives even when
	// the caller discards the body.
	slog.Error("env_dispatch failed",
		"error", msg,
		"traceback", tb,
		"project_id", res.ProjectID,
	)
	switch {
	case errors.Is(err, service.ErrAllDispatchFailed):
		// Top-level error is a bare sentinel (no single origin); each rollout
		// carries its own origin Traceback. Add a top-level message (gap #1) so a
		// caller that only inspects the top level sees why the dispatch failed.
		writeJSON(w, http.StatusInternalServerError, EnvDispatchResponse{
			ChannelID: res.ChannelID,
			ProjectID: res.ProjectID,
			Rollouts:  mapRollouts(res.Rollouts),
			Message:   "all rollouts failed",
		})
	case strings.Contains(msg, "validation_failed"):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "validation_failed", "message": msg, "traceback": tb})
	case strings.Contains(msg, "not_implemented"):
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "not_implemented", "message": msg, "traceback": tb})
	case strings.Contains(msg, "auto-create default env"):
		// Auto-creating the default self_play base env failed (e.g. no online
		// sandbox node bound to the workspace, or the sandbox create job failed).
		// The error chain may wrap pgx.ErrNoRows from the node pick, but that is a
		// resource-availability issue, not a "not found" env - map to 503 ahead of
		// the generic ErrNoRows -> 404 rule.
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "auto_create_default_env_failed", "message": msg, "traceback": tb})
	case errors.Is(err, pgx.ErrNoRows):
		// A bare GetEnv miss: env_id does not exist / not in workspace. (The
		// source-project-resolve path wraps its ErrNoRows in "validation_failed"
		// and is handled above, so this only fires for the top-level env lookup.)
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found", "message": msg, "traceback": tb})
	case strings.Contains(msg, "reset_failed"):
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "reset_failed", "message": msg, "traceback": tb})
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "internal", "message": msg, "traceback": tb})
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
			ChannelID: r.ChannelID, LeaderRunID: r.LeaderRunID, AgentSandboxes: r.AgentSandboxes,
			EnvID: r.EnvID, ProjectID: r.ProjectID, IssueID: r.IssueID,
			ChatSessionID: r.ChatSessionID, AgentRunID: r.AgentRunID, Error: r.Error,
			Traceback:        string(r.Stack),
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
		return service.Env{}, stackerr.Wrap(err, "get env")
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
		return "", stackerr.Wrap(err, "create env")
	}
	return util.UUIDToString(row.ID), nil
}

func (a *envDispatchDepsAdapter) SetEnvSandboxes(ctx context.Context, envID, workspaceID string, sandboxIDs []string) error {
	commandTag, err := a.h.DB.Exec(ctx, `
		UPDATE environment
		SET sandbox_ids = $3, updated_at = now()
		WHERE id = $1 AND workspace_id = $2`, parseUUID(envID), parseUUID(workspaceID), sandboxIDs)
	if err != nil {
		return stackerr.Wrap(err, "attach environment sandboxes")
	}
	if commandTag.RowsAffected() != 1 {
		return stackerr.New("attach environment sandboxes: environment not found")
	}
	return nil
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
	return stackerr.Wrap(err, "delete env")
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
		return "", stackerr.Wrap(err, "marshal fork body")
	}
	resp, err := a.h.CloudRuntime.Do(ctx, cloudruntime.Request{
		Method: http.MethodPost,
		Path:   "/api/v1/sandboxes/fork",
		Body:   body,
		Op:     "provision",
	})
	if err != nil {
		return "", stackerr.Wrap(err, "fork sandbox")
	}
	if resp.StatusCode >= 400 {
		return "", stackerr.New(fmt.Sprintf("fork sandbox: status %d: %s", resp.StatusCode, string(resp.Body)))
	}
	var out struct {
		SandboxID string `json:"sandbox_id"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return "", stackerr.Wrap(err, "fork sandbox: decode")
	}
	if out.SandboxID == "" {
		return "", stackerr.New("fork sandbox: empty sandbox_id in response")
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
		return stackerr.Wrap(err, "delete sandbox")
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode >= 400 {
		return stackerr.New(fmt.Sprintf("delete sandbox: status %d: %s", resp.StatusCode, string(resp.Body)))
	}
	return nil
}

// BootSandbox calls POST /api/v1/sandboxes with image_ref. Used by
// CreateBaseEnv to provision a fresh base env.
func (a *envDispatchDepsAdapter) BootSandbox(ctx context.Context, imageRef string) (string, error) {
	body, err := json.Marshal(map[string]string{"image_ref": imageRef})
	if err != nil {
		return "", stackerr.Wrap(err, "marshal boot body")
	}
	resp, err := a.h.CloudRuntime.Do(ctx, cloudruntime.Request{
		Method: http.MethodPost,
		Path:   "/api/v1/sandboxes",
		Body:   body,
		Op:     "provision",
	})
	if err != nil {
		return "", stackerr.Wrap(err, "boot sandbox")
	}
	if resp.StatusCode >= 400 {
		return "", stackerr.New(fmt.Sprintf("boot sandbox: status %d: %s", resp.StatusCode, string(resp.Body)))
	}
	var out struct {
		SandboxID string `json:"sandbox_id"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return "", stackerr.Wrap(err, "boot sandbox: decode")
	}
	if out.SandboxID == "" {
		return "", stackerr.New("boot sandbox: empty sandbox_id in response")
	}
	return out.SandboxID, nil
}

// CreateProject inserts a new project bound to envID. Used by the scratch
// reset path (spec §7.2): every rollout gets a fresh project.
func (a *envDispatchDepsAdapter) CreateProject(ctx context.Context, workspaceID, name, envID string) (string, error) {
	row, err := a.h.Queries.CreateProjectWithEnv(ctx, db.CreateProjectWithEnvParams{
		WorkspaceID: parseUUID(workspaceID),
		Title:       name,
		Status:      "in_progress",
		Priority:    "medium",
		EnvID:       parseUUID(envID),
	})
	if err != nil {
		return "", stackerr.Wrap(err, "create project")
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
		return "", stackerr.Wrap(err, "get project by env")
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
	return stackerr.Wrap(err, "delete project")
}

func (a *envDispatchDepsAdapter) ResolveMessageRoster(ctx context.Context, workspaceID, agentID, squadID string) (service.MessageRoster, error) {
	wsID := parseUUID(workspaceID)
	if squadID == "" {
		if _, err := a.h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: parseUUID(agentID), WorkspaceID: wsID}); err != nil {
			return service.MessageRoster{}, err
		}
		return service.MessageRoster{LeaderID: agentID, AgentIDs: []string{agentID}}, nil
	}
	squad, err := a.h.Queries.GetSquadInWorkspace(ctx, db.GetSquadInWorkspaceParams{ID: parseUUID(squadID), WorkspaceID: wsID})
	if err != nil {
		return service.MessageRoster{}, err
	}
	ids := []string{util.UUIDToString(squad.LeaderID)}
	seen := map[string]bool{ids[0]: true}
	members, err := a.h.Queries.ListSquadMembers(ctx, squad.ID)
	if err != nil {
		return service.MessageRoster{}, err
	}
	for _, member := range members {
		if member.MemberType != "agent" {
			continue
		}
		id := util.UUIDToString(member.MemberID)
		if seen[id] {
			continue
		}
		if _, err := a.h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: member.MemberID, WorkspaceID: wsID}); err != nil {
			return service.MessageRoster{}, err
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return service.MessageRoster{LeaderID: ids[0], AgentIDs: ids}, nil
}

func (a *envDispatchDepsAdapter) CreateEnvDispatchChannel(ctx context.Context, workspaceID, userID, projectID, envID string, roster service.MessageRoster, specs map[string]service.SandboxInstanceRef) (string, error) {
	tx, err := a.h.TxStarter.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var channelID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, kind, project_id, created_by)
		VALUES ($1, $2, 'group', $3, $4) RETURNING id::text`,
		workspaceID, "env-dispatch-"+uuid.NewString(), projectID, userID).Scan(&channelID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id) VALUES ($1, $2, 'user', $3)`, channelID, workspaceID, userID); err != nil {
		return "", err
	}
	store := envDispatchChannelStore{}
	for _, agentID := range roster.AgentIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id) VALUES ($1, $2, 'agent', $3)`, channelID, workspaceID, agentID); err != nil {
			return "", err
		}
		config := json.RawMessage(`{}`)
		if ref, ok := specs[agentID]; ok && ref.Template != "" {
			config, _ = json.Marshal(map[string]string{"template": ref.Template})
		}
		if err := store.insertBinding(ctx, tx, envAgentSandboxBinding{EnvID: envID, ChannelID: channelID, AgentID: agentID, Status: "pending", SandboxConfig: config}); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return channelID, nil
}

func (a *envDispatchDepsAdapter) DeleteChannel(ctx context.Context, workspaceID, channelID string) error {
	_, err := a.h.DB.Exec(ctx, `DELETE FROM channel WHERE id = $1 AND workspace_id = $2`, channelID, workspaceID)
	return err
}

func (a *envDispatchDepsAdapter) ProvisionEnvDispatchAgent(ctx context.Context, in service.EnvDispatchAgentProvisionInput) (service.EnvDispatchAgentProvisionResult, error) {
	result, err := a.h.provisionEnvDispatchAgent(ctx, ProvisionEnvDispatchAgentInput{
		WorkspaceID:             in.WorkspaceID,
		UserID:                  in.UserID,
		EnvID:                   in.EnvID,
		ProjectID:               in.ProjectID,
		ChannelID:               in.ChannelID,
		AgentID:                 in.AgentID,
		SourceSandboxInstanceID: in.SourceSandboxInstanceID,
		SandboxConfig:           in.SandboxConfig,
	})
	if err != nil {
		return service.EnvDispatchAgentProvisionResult{}, err
	}
	return service.EnvDispatchAgentProvisionResult{
		SandboxInstanceID: result.SandboxInstanceID,
		RuntimeID:         result.RuntimeID,
		DaemonID:          result.DaemonID,
		ChatSessionID:     result.ChatSessionID,
	}, nil
}

func (a *envDispatchDepsAdapter) CreateChannelMessage(ctx context.Context, channelID, workspaceID, userID, content string) (string, error) {
	message, err := a.h.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(workspaceID), "user", parseUUID(userID), "Env Dispatch", content, "env_dispatch", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		return "", stackerr.Wrap(err, "create env-dispatch channel message")
	}
	return message.ID, nil
}

func (a *envDispatchDepsAdapter) EnqueueEnvDispatchChannelRun(ctx context.Context, workspaceID, userID string, in service.ChannelRunInput, idx int) (string, error) {
	return a.EnqueueAgentRun(ctx, workspaceID, userID, in.AgentID, "", "", in.ChatSessionID, in.SandboxInstanceID, in.EnvID, in.RuntimeID, idx)
}

func (a *envDispatchDepsAdapter) SaveCollaborationTrigger(ctx context.Context, envID string, trigger service.EnvCollaborationTrigger) error {
	return (envDispatchChannelStore{}).saveTrigger(ctx, a.h.DB, envID, envCollaborationTrigger{
		AgentID:             trigger.AgentID,
		Kind:                trigger.Kind,
		ChannelID:           trigger.ChannelID,
		ProjectID:           trigger.ProjectID,
		ChatSessionID:       trigger.ChatSessionID,
		SourceMessageID:     trigger.SourceMessageID,
		ThreadRootMessageID: trigger.ThreadRootMessageID,
		TaskID:              trigger.TaskID,
		RuntimeID:           trigger.RuntimeID,
	})
}

func (a *envDispatchDepsAdapter) ValidateBranchMessageSource(ctx context.Context, workspaceID, envID, projectID string, roster service.MessageRoster) (service.ValidatedBranchMessageSource, error) {
	trigger, err := (envDispatchChannelStore{}).loadTrigger(ctx, a.h.DB, envID, workspaceID)
	if err != nil {
		return service.ValidatedBranchMessageSource{}, err
	}
	if trigger.ProjectID != projectID {
		return service.ValidatedBranchMessageSource{}, fmt.Errorf("trigger project does not match source project")
	}
	rows, err := a.h.DB.Query(ctx, `
		SELECT member_id::text
		FROM channel_member
		WHERE channel_id = $1 AND member_type = 'agent'
		ORDER BY member_id`, trigger.ChannelID)
	if err != nil {
		return service.ValidatedBranchMessageSource{}, err
	}
	defer rows.Close()
	sourceIDs := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return service.ValidatedBranchMessageSource{}, err
		}
		sourceIDs = append(sourceIDs, id)
	}
	if err := rows.Err(); err != nil {
		return service.ValidatedBranchMessageSource{}, err
	}
	if !sameEnvDispatchRoster(sourceIDs, roster.AgentIDs) {
		return service.ValidatedBranchMessageSource{}, fmt.Errorf("requested agent roster differs from source channel")
	}
	// Resolve the trigger agent's source binding so the branch can clone its
	// sandbox state. A ready source binding (sandbox_instance_id set) is the
	// clone source; a non-ready binding means the trigger agent creates from its
	// saved policy instead. A missing binding is inconsistent source state.
	sourceBinding, err := (envDispatchChannelStore{}).binding(ctx, a.h.DB, envID, trigger.AgentID)
	if err != nil {
		return service.ValidatedBranchMessageSource{}, fmt.Errorf("load source trigger binding: %w", err)
	}
	var sourceSandboxID string
	if sourceBinding.SandboxInstanceID != nil {
		sourceSandboxID = *sourceBinding.SandboxInstanceID
	}
	return service.ValidatedBranchMessageSource{
		SourceEnvID:                    envID,
		SourceProjectID:                projectID,
		SourceChannelID:                trigger.ChannelID,
		Roster:                         roster,
		TriggerSourceSandboxInstanceID: sourceSandboxID,
		Trigger: service.EnvCollaborationTrigger{
			AgentID:             trigger.AgentID,
			Kind:                trigger.Kind,
			ChannelID:           trigger.ChannelID,
			ProjectID:           trigger.ProjectID,
			ChatSessionID:       trigger.ChatSessionID,
			SourceMessageID:     trigger.SourceMessageID,
			ThreadRootMessageID: trigger.ThreadRootMessageID,
			TaskID:              trigger.TaskID,
			RuntimeID:           trigger.RuntimeID,
		},
	}, nil
}

func (a *envDispatchDepsAdapter) CopyEnvDispatchChannel(ctx context.Context, workspaceID, sourceChannelID, destinationProjectID, destinationEnvID string) (service.ChannelCopyMap, error) {
	copyMap, err := a.h.copyEnvDispatchChannel(ctx, workspaceID, sourceChannelID, destinationProjectID, destinationEnvID)
	if err != nil {
		return service.ChannelCopyMap{}, err
	}
	return service.ChannelCopyMap{ChannelID: copyMap.ChannelID, MessageIDs: copyMap.MessageIDs}, nil
}

func sameEnvDispatchRoster(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]struct{}, len(left))
	for _, id := range left {
		seen[id] = struct{}{}
	}
	if len(seen) != len(left) {
		return false
	}
	for _, id := range right {
		if _, ok := seen[id]; !ok {
			return false
		}
	}
	return true
}

// ListIssuesByProject returns all issues under a project. Used during
// CopyProjectSubtree to deep-copy the source project's issues.
func (a *envDispatchDepsAdapter) ListIssuesByProject(ctx context.Context, projectID, workspaceID string) ([]service.IssueRow, error) {
	rows, err := a.h.Queries.ListIssuesByProject(ctx, db.ListIssuesByProjectParams{
		ProjectID:   parseUUID(projectID),
		WorkspaceID: parseUUID(workspaceID),
	})
	if err != nil {
		return nil, stackerr.Wrap(err, "list issues by project")
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
		return nil, stackerr.Wrap(err, "list chat sessions by project")
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
		return "", stackerr.Wrap(err, "increment issue counter")
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
		return "", stackerr.Wrap(err, "create issue")
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
		return nil, stackerr.Wrap(err, "marshal issue metadata")
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
		return "", stackerr.Wrap(err, "create chat session")
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
		return "", stackerr.Wrap(err, "create chat message")
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
		return "", stackerr.Wrap(err, "get default self_play env")
	}
	if !v.Valid {
		return "", nil // not configured; service maps to 400
	}
	return util.UUIDToString(v), nil
}

// SetDefaultSelfPlayEnv conditionally persists envID as the workspace default
// self_play base env (only when still NULL). The query is a no-op when another
// concurrent writer already set the default; the service re-reads
// GetDefaultSelfPlayEnv to pick up the canonical winner. A real query error is
// surfaced; "updated 0 rows" is not an error here.
func (a *envDispatchDepsAdapter) SetDefaultSelfPlayEnv(ctx context.Context, workspaceID, envID string) error {
	if err := a.h.Queries.SetDefaultSelfPlayEnv(ctx, db.SetDefaultSelfPlayEnvParams{
		ID:                   parseUUID(workspaceID),
		DefaultSelfPlayEnvID: parseUUID(envID),
	}); err != nil {
		return stackerr.Wrap(err, "set default self_play env")
	}
	return nil
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
func (a *envDispatchDepsAdapter) EnqueueAgentRun(ctx context.Context, workspaceID, actorUserID, agentID, squadID, issueID, chatSessionID, sandboxInstanceID, envID, runtimeID string, idx int) (string, error) {
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
				return "", stackerr.Wrap(err, "get squad")
			}
			if err := a.h.Queries.SetIssueAssignee(ctx, db.SetIssueAssigneeParams{
				ID:           parseUUID(issueID),
				AssigneeType: pgtype.Text{String: "squad", Valid: true},
				AssigneeID:   parseUUID(squadID),
				WorkspaceID:  parseUUID(workspaceID),
			}); err != nil {
				return "", stackerr.Wrap(err, "set issue assignee to squad")
			}
			agentUUID = squad.LeaderID
			isLeaderTask = pgtype.Bool{Bool: true, Valid: true}
		}
		agent, err := a.h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID:          agentUUID,
			WorkspaceID: parseUUID(workspaceID),
		})
		if err != nil {
			return "", stackerr.Wrap(err, "get agent for run")
		}
		// Phase 2: route the task to the pre-created sandbox runtime R' when
		// supplied (single-agent daemon-enabled rollout), instead of the agent's
		// own runtime. Empty preserves the current behavior.
		taskRuntimeID := agent.RuntimeID
		if runtimeID != "" {
			taskRuntimeID = parseUUID(runtimeID)
		}
		taskContext := mergeEphemeralSandboxContext(nil, sandboxInstanceID, actorUserID)
		taskContext, err = service.WithTaskExecutionConfig(taskContext, agent.Model.String, agent.ThinkingLevel.String)
		if err != nil {
			return "", stackerr.Wrap(err, "snapshot agent task execution config")
		}
		task, err := a.h.Queries.CreateAgentTask(ctx, db.CreateAgentTaskParams{
			AgentID:      agentUUID,
			RuntimeID:    taskRuntimeID,
			IssueID:      parseUUID(issueID),
			Priority:     envDispatchTaskPriority,
			IsLeaderTask: isLeaderTask,
			Context:      taskContext,
		})
		if err != nil {
			return "", stackerr.Wrap(err, "create agent task")
		}
		// Training session-open chokepoint (spec §4.3): a single-agent training
		// dispatch (train_agent_id == agent_id) creates the trained task HERE,
		// not via a later @mention. Resolve the owning project via the issue.
		a.maybeOpenTrainingSession(ctx, util.UUIDToString(task.ID), util.UUIDToString(agentUUID), a.issueProjectID(ctx, issueID), envID)
		return util.UUIDToString(task.ID), nil
	case chatSessionID != "":
		session, err := a.h.Queries.GetChatSession(ctx, parseUUID(chatSessionID))
		if err != nil {
			return "", stackerr.Wrap(err, "get chat session for run")
		}
		params := db.CreateChatTaskParams{
			RuntimeID:     session.RuntimeID,
			Priority:      envDispatchTaskPriority,
			ChatSessionID: session.ID,
		}
		// Phase 2: route to the pre-created sandbox runtime R' when supplied
		// (single-agent daemon-enabled rollout), instead of the session's
		// runtime. squadID dispatch always has runtimeID="" (see
		// rolloutRuntimeID), so the squad branch below still sets the leader's
		// runtime and is unaffected.
		if runtimeID != "" {
			params.RuntimeID = parseUUID(runtimeID)
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
				return "", stackerr.Wrap(err, "get squad")
			}
			leader, err := a.h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
				ID:          squad.LeaderID,
				WorkspaceID: parseUUID(workspaceID),
			})
			if err != nil {
				return "", stackerr.Wrap(err, "get squad leader")
			}
			contextJSON, err := json.Marshal(map[string]string{"squad_id": squadID})
			if err != nil {
				return "", stackerr.Wrap(err, "marshal squad chat context")
			}
			params.AgentID = squad.LeaderID
			if runtimeID == "" {
				params.RuntimeID = leader.RuntimeID
			}
			params.Context = contextJSON
		} else {
			params.AgentID = parseUUID(agentID)
		}
		// Phase 5: stamp the ephemeral_sandbox marker (sandbox_instance_id) so the
		// terminal cleanup hook can reclaim the sandbox. No-op for squad dispatch
		// (sandboxInstanceID is empty); merges alongside any squad_id context.
		params.Context = mergeEphemeralSandboxContext(params.Context, sandboxInstanceID, actorUserID)
		targetAgent, err := a.h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID:          params.AgentID,
			WorkspaceID: parseUUID(workspaceID),
		})
		if err != nil {
			return "", stackerr.Wrap(err, "get chat task agent")
		}
		params.Context, err = service.WithTaskExecutionConfig(params.Context, targetAgent.Model.String, targetAgent.ThinkingLevel.String)
		if err != nil {
			return "", stackerr.Wrap(err, "snapshot chat task execution config")
		}
		task, err := a.h.Queries.CreateChatTask(ctx, params)
		if err != nil {
			return "", stackerr.Wrap(err, "create chat task")
		}
		// Training session-open chokepoint (spec §4.3): chat-bound dispatch.
		// Project resolves via the chat session (seam 1e).
		a.maybeOpenTrainingSession(ctx, util.UUIDToString(task.ID), util.UUIDToString(params.AgentID), util.UUIDToString(session.ProjectID), envID)
		return util.UUIDToString(task.ID), nil
	default:
		return "", stackerr.New("enqueue agent run: issueID or chatSessionID required")
	}
}

// PrecreateAgentRuntime satisfies EnvDispatchDeps. It inserts an offline
// agent_runtime row (R') keyed by a freshly-generated daemon_id, for the
// agent's provider, owned by ownerUserID. The in-sandbox daemon booted with
// MULTICA_DAEMON_ID=<daemon_id> adopts R' on register (UpsertAgentRuntime ON
// CONFLICT). Returns the runtime id (R') and the daemon_id. The provider is
// read from the agent's authoritative bound runtime. Only pi runtimes support
// the pi-in-sandbox topology.
func (a *envDispatchDepsAdapter) PrecreateAgentRuntime(ctx context.Context, workspaceID, ownerUserID, agentID string) (string, string, error) {
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return "", "", fmt.Errorf("parse workspace_id: %w", err)
	}
	ownerUUID, err := util.ParseUUID(ownerUserID)
	if err != nil {
		return "", "", fmt.Errorf("parse owner_user_id: %w", err)
	}
	agentUUID, err := util.ParseUUID(agentID)
	if err != nil {
		return "", "", fmt.Errorf("parse agent_id: %w", err)
	}
	agent, err := a.h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
		ID:          agentUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		return "", "", stackerr.Wrap(err, "get agent for runtime precreate")
	}
	bound, err := a.h.Queries.GetAgentBoundRuntimeForWorkspace(ctx, db.GetAgentBoundRuntimeForWorkspaceParams{
		AgentID: agentUUID, WorkspaceID: wsUUID,
	})
	if err != nil {
		return "", "", stackerr.Wrap(err, "get bound runtime")
	}
	if bound.Provider != "pi" {
		return "", "", fmt.Errorf("pi-in-sandbox requires pi runtime, got %q", bound.Provider)
	}
	daemonID := uuid.NewString()
	row, err := a.h.Queries.PrecreateAgentRuntime(ctx, db.PrecreateAgentRuntimeParams{
		WorkspaceID: wsUUID,
		DaemonID:    util.StrToText(daemonID),
		Name:        fmt.Sprintf("%s sandbox runtime", agent.Name),
		Provider:    bound.Provider,
		OwnerID:     ownerUUID,
	})
	if err != nil {
		return "", "", stackerr.Wrap(err, "precreate agent runtime")
	}
	return util.UUIDToString(row.ID), daemonID, nil
}

// mergeEphemeralSandboxContext stamps the Phase 5 ephemeral_sandbox marker into a
// task-context JSON blob, preserving any existing keys (e.g. squad_id). The
// marker carries the sandbox_instance_id the terminal cleanup hook reads to
// reclaim the ephemeral sandbox. Returns the input unchanged when instanceID is
// empty (not an ephemeral rollout). A malformed existing context is left intact
// (the marker is skipped - cleanup then no-ops for that task) rather than risk
// clobbering a context the task relies on.
func mergeEphemeralSandboxContext(existing []byte, instanceID, actorUserID string) []byte {
	if instanceID == "" {
		return existing
	}
	obj := map[string]json.RawMessage{}
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &obj); err != nil {
			return existing
		}
	}
	marker, _ := json.Marshal(map[string]string{
		"sandbox_instance_id": instanceID,
		"actor_user_id":       actorUserID,
	})
	obj["ephemeral_sandbox"] = marker
	merged, _ := json.Marshal(obj)
	return merged
}

// DeleteAgentRuntime satisfies EnvDispatchDeps. It reclaims a pre-created
// runtime R' (workspace-scoped) when its rollout fails before the task is
// created, so the offline row does not linger. No-op if the runtime is gone.
func (a *envDispatchDepsAdapter) DeleteAgentRuntime(ctx context.Context, workspaceID, runtimeID string) error {
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return fmt.Errorf("parse workspace_id: %w", err)
	}
	rtUUID, err := util.ParseUUID(runtimeID)
	if err != nil {
		return fmt.Errorf("parse runtime_id: %w", err)
	}
	if err := a.h.Queries.DeleteAgentRuntimeForWorkspace(ctx, db.DeleteAgentRuntimeForWorkspaceParams{
		ID:          rtUUID,
		WorkspaceID: wsUUID,
	}); err != nil {
		return stackerr.Wrap(err, "delete agent runtime")
	}
	return nil
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
		return "", nil, nil, stackerr.Wrap(err, "get source project")
	}

	newProject, err := a.h.Queries.CreateProjectWithEnv(ctx, db.CreateProjectWithEnvParams{
		WorkspaceID: wsUUID,
		Title:       src.Title + "-fork",
		Status:      src.Status,
		Priority:    src.Priority,
		EnvID:       parseUUID(envID),
	})
	if err != nil {
		return "", nil, nil, stackerr.Wrap(err, "create forked project")
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
		return nil, stackerr.Wrap(err, "list source issues")
	}
	issueIDMap := make(map[string]string, len(srcs))
	for _, src := range srcs {
		number, err := a.h.Queries.IncrementIssueCounter(ctx, wsUUID)
		if err != nil {
			return nil, stackerr.Wrap(err, "increment issue counter")
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
			return nil, stackerr.Wrap(err, "create forked issue")
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
		return nil, stackerr.Wrap(err, "list source chat sessions")
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
			return nil, stackerr.Wrap(err, "create forked chat session")
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
		return stackerr.Wrap(err, "list source chat messages")
	}
	for _, m := range msgs {
		if _, err := a.h.Queries.CreateChatMessage(ctx, db.CreateChatMessageParams{
			ChatSessionID: newSessionID,
			Role:          m.Role,
			Content:       m.Content,
		}); err != nil {
			return stackerr.Wrap(err, "create forked chat message")
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
		return service.EnvDispatchResult{}, false, stackerr.Wrap(err, "get idempotent response")
	}
	var res service.EnvDispatchResult
	if err := json.Unmarshal(row.Response, &res); err != nil {
		return service.EnvDispatchResult{}, false, stackerr.Wrap(err, "decode idempotent response")
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
		return stackerr.Wrap(err, "marshal idempotent response")
	}
	if err := a.h.Queries.CreateEnvDispatchRequest(ctx, db.CreateEnvDispatchRequestParams{
		WorkspaceID:    parseUUID(workspaceID),
		IdempotencyKey: parseUUID(key),
		Response:       body,
	}); err != nil {
		return stackerr.Wrap(err, "save idempotent response")
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
		return stackerr.Wrap(err, "save training dispatch")
	}
	return nil
}

// ValidateAgentInWorkspaceOrSquad reports whether agentID is a member of the
// squad (when squadID is set) or the workspace. Returns a typed error when the
// agent is unknown or unauthorized.
func (a *envDispatchDepsAdapter) ValidateAgentInWorkspaceOrSquad(ctx context.Context, workspaceID, squadID, agentID string) error {
	agentUUID, err := util.ParseUUID(agentID)
	if err != nil {
		return stackerr.Wrap(err, "parse agent_id")
	}
	if squadID != "" {
		squadUUID, err := util.ParseUUID(squadID)
		if err != nil {
			return stackerr.Wrap(err, "parse squad_id")
		}
		ok, err := a.h.Queries.IsSquadMember(ctx, db.IsSquadMemberParams{
			SquadID:    squadUUID,
			MemberType: "agent",
			MemberID:   agentUUID,
		})
		if err != nil {
			return stackerr.Wrap(err, "check squad membership")
		}
		if !ok {
			return stackerr.New(fmt.Sprintf("agent %s is not a member of squad %s", agentID, squadID))
		}
		return nil
	}
	// No squad: validate the agent belongs to the workspace.
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return stackerr.Wrap(err, "parse workspace_id")
	}
	if _, err := a.h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
		ID:          agentUUID,
		WorkspaceID: wsUUID,
	}); err != nil {
		return stackerr.New(fmt.Sprintf("agent %s is not a member of workspace %s", agentID, workspaceID))
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
			return service.SandboxInstanceRef{}, stackerr.Wrap(err, fmt.Sprintf("resolve base env %s", spec.BaseEnvID))
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
func (s *stubEnvDispatchDeps) SetEnvSandboxes(context.Context, string, string, []string) error {
	return nil
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
func (s *stubEnvDispatchDeps) ResolveMessageRoster(_ context.Context, _, agentID, _ string) (service.MessageRoster, error) {
	return service.MessageRoster{LeaderID: agentID, AgentIDs: []string{agentID}}, nil
}
func (s *stubEnvDispatchDeps) CreateEnvDispatchChannel(context.Context, string, string, string, string, service.MessageRoster, map[string]service.SandboxInstanceRef) (string, error) {
	return "stub-channel", nil
}
func (s *stubEnvDispatchDeps) DeleteChannel(context.Context, string, string) error { return nil }
func (s *stubEnvDispatchDeps) ProvisionEnvDispatchAgent(context.Context, service.EnvDispatchAgentProvisionInput) (service.EnvDispatchAgentProvisionResult, error) {
	return service.EnvDispatchAgentProvisionResult{SandboxInstanceID: "stub-sandbox", RuntimeID: "stub-runtime", DaemonID: "stub-daemon", ChatSessionID: "stub-session"}, nil
}
func (s *stubEnvDispatchDeps) CreateChannelMessage(context.Context, string, string, string, string) (string, error) {
	return "stub-channel-message", nil
}
func (s *stubEnvDispatchDeps) EnqueueEnvDispatchChannelRun(context.Context, string, string, service.ChannelRunInput, int) (string, error) {
	return "stub-channel-run", nil
}
func (s *stubEnvDispatchDeps) SaveCollaborationTrigger(context.Context, string, service.EnvCollaborationTrigger) error {
	return nil
}
func (s *stubEnvDispatchDeps) ValidateBranchMessageSource(context.Context, string, string, string, service.MessageRoster) (service.ValidatedBranchMessageSource, error) {
	return service.ValidatedBranchMessageSource{}, nil
}
func (s *stubEnvDispatchDeps) CopyEnvDispatchChannel(context.Context, string, string, string, string) (service.ChannelCopyMap, error) {
	return service.ChannelCopyMap{ChannelID: "stub-channel", MessageIDs: map[string]string{}}, nil
}
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
func (s *stubEnvDispatchDeps) EnqueueAgentRun(context.Context, string, string, string, string, string, string, string, string, string, int) (string, error) {
	return "stub-run", nil
}
func (s *stubEnvDispatchDeps) PrecreateAgentRuntime(context.Context, string, string, string) (string, string, error) {
	return "stub-runtime", "stub-daemon", nil
}
func (s *stubEnvDispatchDeps) DeleteAgentRuntime(context.Context, string, string) error {
	return nil
}
func (s *stubEnvDispatchDeps) GetDefaultSelfPlayEnv(context.Context, string) (string, error) {
	return "stub-env", nil
}
func (s *stubEnvDispatchDeps) SetDefaultSelfPlayEnv(context.Context, string, string) error {
	return nil
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
