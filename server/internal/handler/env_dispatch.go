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
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/cloudruntime"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/internal/util/stackerr"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// EnvDispatchRequest is the body of POST /api/v1/env-dispatch (spec §6.3).
type EnvDispatchRequest struct {
	Mode                   string   `json:"mode"`
	EnvID                  string   `json:"env_id"`
	SourceTaskID           string   `json:"source_task_id,omitempty"`
	Domain                 string   `json:"domain,omitempty"`
	DispatchType           string   `json:"dispatch_type"`
	GroupSize              int      `json:"group_size"`
	AgentID                string   `json:"agent_id"`
	OnlineTrainableAgents  []string `json:"online_trainable_agents"`
	OfflineTrainableAgents []string `json:"offline_trainable_agents"`
	QuietWindowMS          int      `json:"quiet_window_ms"`
	TotalTimeoutSeconds    int      `json:"total_timeout_seconds"`
	CriticAgentID          string   `json:"critic_agent_id,omitempty"`
	IdempotencyKey         string   `json:"idempotency_key,omitempty"`
	// SharedSandbox optionally requests that all agents of each rollout (sample)
	// share one sandbox + one daemon + one working directory. Omitted (nil) or
	// false preserves the current per-agent isolation; the pointer distinguishes
	// an omitted JSON field from an explicit false, matching training_mode.
	SharedSandbox *bool `json:"shared_sandbox,omitempty"`
	// Template optionally overrides the server's default self_play sandbox
	// template (MULTICA_DEFAULT_SELF_PLAY_TEMPLATE) for the auto-created default
	// base env. Only consulted when env_id is empty and no default is configured
	// (scratch self_play). 1..64 chars when set.
	Template    string                        `json:"template,omitempty"`
	Issue       *IssueDispatchInput           `json:"issue,omitempty"`
	Message     *MessageDispatchInput         `json:"message,omitempty"`
	PerAgentEnv map[string]PerAgentEnvRequest `json:"per_agent_env,omitempty"`
	Audit       *EnvDispatchAuditRequest      `json:"audit,omitempty"`
	// StageFiles optionally materializes workspace-relative text files into the
	// dispatched environment after provision. Additive eval seam: omitted on
	// training dispatches. Paths must be relative and must not contain "..".
	StageFiles []EnvDispatchStagedFile `json:"stage_files,omitempty"`
	// Environment is an optional benchmark-neutral recipe. Image/services are
	// accepted for client compatibility; only inlined Files are applied as
	// staged workspace files. Docker image names are not MultiCA templates.
	Environment *EnvDispatchEnvironment `json:"environment,omitempty"`
}

// EnvDispatchStagedFile is one workspace-relative text file to materialize
// into a dispatched environment (POST /env-dispatch stage_files, and
// POST .../files).
type EnvDispatchStagedFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// EnvDispatchEnvironment is the additive `environment` object on
// POST /api/v1/env-dispatch. Files are staged; Image and Services are
// recorded only as client-compatible fields.
type EnvDispatchEnvironment struct {
	Image    string                  `json:"image,omitempty"`
	Files    []EnvDispatchStagedFile `json:"files,omitempty"`
	Services []string                `json:"services,omitempty"`
}

// EnvDispatchAuditRequest is the opt-in audit correlation request. The
// server owns the correlation ID and deadline; callers may only enable audit
// recording and select its reclamation window.
type EnvDispatchAuditRequest struct {
	Enabled                  bool `json:"enabled"`
	ReclamationWindowSeconds int  `json:"reclamation_window_seconds"`
}

// ExternalModelRuntimeRequest carries a caller-supplied external model provider
// configuration for one squad member's isolated sandbox. APIKey is a secret; it
// is mapped to a service-layer value at the boundary and never retained by the
// handler-layer pointer.
type ExternalModelRuntimeRequest struct {
	Provider string `json:"provider,omitempty"`
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
	Model    string `json:"model"`
}

// PerAgentEnvRequest carries one squad member's sandbox template or base env
// intent. The map key in EnvDispatchRequest is the agent_id.
type PerAgentEnvRequest struct {
	Template  string                       `json:"template,omitempty"`
	BaseEnvID string                       `json:"base_env_id,omitempty"`
	Runtime   *ExternalModelRuntimeRequest `json:"runtime,omitempty"`
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
type EnvDispatchRunAgentResponse struct {
	SourceAgentID    string `json:"source_agent_id"`
	ExecutionAgentID string `json:"execution_agent_id"`
	TrainingMode     string `json:"training_mode"`
}

type EnvDispatchResponse struct {
	RunID                     string                        `json:"run_id,omitempty"`
	ChannelID                 string                        `json:"channel_id,omitempty"`
	ProjectID                 string                        `json:"project_id"`
	Status                    string                        `json:"status,omitempty"`
	Rollouts                  []EnvRolloutResponse          `json:"rollouts"`
	QuietWindowMS             int                           `json:"quiet_window_ms"`
	TotalTimeoutSeconds       int                           `json:"total_timeout_seconds"`
	InitialMessageSubmittedAt time.Time                     `json:"initial_message_submitted_at,omitempty"`
	RunAgents                 []EnvDispatchRunAgentResponse `json:"run_agents"`
	Message                   string                        `json:"message,omitempty"`
	Audit                     *EnvDispatchAuditResponse     `json:"audit,omitempty"`
}

// FrozenRunDAGResponse is the sanitized, immutable public view of a completed
// mixed-RL run. It intentionally projects only the frozen DAG data; raw
// provider payloads, credentials, SSE, and materialized tensors never cross
// this boundary. Associations are nested under segments as provider_calls per
// the frozen-DAG contract; a top-level associations list is never emitted.
type FrozenRunDAGResponse struct {
	RunID         string                                `json:"run_id"`
	ProjectID     string                                `json:"project_id"`
	WorkspaceID   string                                `json:"workspace_id"`
	Status        string                                `json:"status"`
	RunStatus     string                                `json:"run_status"`
	SnapshotID    string                                `json:"snapshot_id"`
	SnapshotHash  string                                `json:"snapshot_hash"`
	SchemaVersion string                                `json:"schema_version"`
	FrozenAt      time.Time                             `json:"frozen_at"`
	CaptureGaps   []FrozenRunDAGCaptureGapResponse      `json:"capture_gaps"`
	RunAgents     []service.FrozenDAGRunAgentRecord     `json:"run_agents"`
	ProviderCalls []service.FrozenDAGProviderCallRecord `json:"provider_calls"`
	Segments      []FrozenRunDAGSegmentResponse         `json:"segments"`
	Edges         []service.CausalEdgeRecord            `json:"edges"`
}

// FrozenRunDAGSegmentResponse nests call associations under each segment.
type FrozenRunDAGSegmentResponse struct {
	SegmentID         string                            `json:"segment_id"`
	RunAgentID        string                            `json:"run_agent_id"`
	Kind              string                            `json:"kind"`
	CanonicalActionID string                            `json:"canonical_action_id,omitempty"`
	SegmentOrdinal    int64                             `json:"segment_ordinal"`
	Reward            *float64                          `json:"reward,omitempty"`
	RewardSource      string                            `json:"reward_source,omitempty"`
	ProviderCalls     []FrozenRunDAGAssociationResponse `json:"provider_calls"`
}

// FrozenRunDAGAssociationResponse is the contract-facing call association.
type FrozenRunDAGAssociationResponse struct {
	CallID          string `json:"call_id"`
	CallOrdinal     int64  `json:"call_ordinal"`
	AssociationKind string `json:"association_kind"`
}

// FrozenRunDAGCaptureGapResponse is the sanitized capture-gap locator.
type FrozenRunDAGCaptureGapResponse struct {
	RunAgentID string `json:"run_agent_id"`
	TurnID     string `json:"turn_id"`
	Reason     string `json:"reason"`
}

// FrozenRunDAGPollingResponse is returned while a mixed run is not yet frozen.
type FrozenRunDAGPollingResponse struct {
	RunID               string `json:"run_id"`
	Status              string `json:"status"`
	QuietCandidateSince any    `json:"quiet_candidate_since"`
	DeadlineAt          any    `json:"deadline_at"`
}

func (response EnvDispatchResponse) MarshalJSON() ([]byte, error) {
	type responseAlias EnvDispatchResponse
	if response.QuietWindowMS == 0 {
		response.QuietWindowMS = 2000
	}
	if response.TotalTimeoutSeconds == 0 {
		response.TotalTimeoutSeconds = 3300
	}
	if response.RunAgents == nil {
		response.RunAgents = []EnvDispatchRunAgentResponse{}
	}
	return json.Marshal(responseAlias(response))
}

// EnvDispatchAuditResponse is the public locator for a server-owned audit
// report. It deliberately does not echo audit request fields or accept caller
// controlled identifiers.
type EnvDispatchAuditResponse struct {
	AuditID             string    `json:"audit_id"`
	ReportURL           string    `json:"report_url"`
	ReclamationDeadline time.Time `json:"reclamation_deadline"`
}

// envDispatchAuditResponseFromReport projects the response locator from the
// stored server record. T012 attaches this only when an audit run exists.
func envDispatchAuditResponseFromReport(report service.EnvDispatchAuditReport) *EnvDispatchAuditResponse {
	return &EnvDispatchAuditResponse{
		AuditID:             report.AuditID,
		ReportURL:           "/api/v1/env-dispatch/audits/" + report.AuditID,
		ReclamationDeadline: report.ReclamationDeadline,
	}
}

type EnvRolloutResponse struct {
	RunID            string                                `json:"run_id,omitempty"`
	SourceTaskID     string                                `json:"source_task_id,omitempty"`
	ChannelID        string                                `json:"channel_id,omitempty"`
	LeaderRunID      string                                `json:"leader_run_id,omitempty"`
	AgentSandboxes   map[string]service.AgentSandboxStatus `json:"agent_sandboxes,omitempty"`
	EnvID            string                                `json:"env_id"`
	ProjectID        string                                `json:"project_id"`
	IssueID          string                                `json:"issue_id,omitempty"`
	ChatSessionID    string                                `json:"-"`
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
	req := EnvDispatchRequest{QuietWindowMS: 2000, TotalTimeoutSeconds: 3300}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		for _, removed := range []string{"training_mode", "train_agent_id"} {
			if strings.Contains(err.Error(), `unknown field "`+removed+`"`) {
				writeError(w, http.StatusBadRequest, removed+" was removed; use online_trainable_agents and offline_trainable_agents")
				return
			}
		}
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	// UUID-shape validation (spec §6.3). Do it here so malformed IDs return a
	// 400 instead of panicking deep in the adapter (parseUUID is MustParseUUID).
	// env_id/agent_id may be empty now (empty env_id resolves a per-workspace
	// default for scratch self_play), so only shape-check them when present.
	// The service enforces the conditional-required rules.
	if req.EnvID != "" {
		if _, ok := parseUUIDOrBadRequest(w, req.EnvID, "env_id"); !ok {
			return
		}
	}
	if req.SourceTaskID != "" {
		if _, ok := parseUUIDOrBadRequest(w, req.SourceTaskID, "source_task_id"); !ok {
			return
		}
	}
	if req.AgentID != "" {
		if _, ok := parseUUIDOrBadRequest(w, req.AgentID, "agent_id"); !ok {
			return
		}
	}
	for field, agentIDs := range map[string][]string{
		"online_trainable_agents":  req.OnlineTrainableAgents,
		"offline_trainable_agents": req.OfflineTrainableAgents,
	} {
		for _, agentID := range agentIDs {
			if _, ok := parseUUIDOrBadRequest(w, agentID, field); !ok {
				return
			}
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

	staged, err := collectEnvDispatchStagedFiles(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	svc := newEnvDispatchService(h, envDispatchConcurrency())
	// Branch dispatch continues a running source, which it can only do from a
	// captured savepoint now that the live clone is gone.
	if provider := newBranchSavepointProvider(h); provider != nil {
		svc = svc.WithBranchSavepoints(provider)
	}
	res, err := svc.Dispatch(r.Context(), service.EnvDispatchInput{
		WorkspaceID: workspaceID, UserID: userID,
		Mode: service.EnvMode(req.Mode), EnvID: req.EnvID,
		SourceTaskID: req.SourceTaskID,
		Domain:       service.EnvDomain(req.Domain),
		DispatchType: service.EnvDispatchType(req.DispatchType),
		GroupSize:    req.GroupSize, AgentID: req.AgentID,
		OnlineTrainableAgents:  append([]string(nil), req.OnlineTrainableAgents...),
		OfflineTrainableAgents: append([]string(nil), req.OfflineTrainableAgents...),
		QuietWindowMS:          req.QuietWindowMS,
		TotalTimeoutSeconds:    req.TotalTimeoutSeconds,
		CriticAgentID:          req.CriticAgentID,
		IdempotencyKey:         req.IdempotencyKey,
		SharedSandbox:          req.SharedSandbox != nil && *req.SharedSandbox,
		DefaultBaseTemplate:    template,
		Issue:                  mapIssueInput(req.Issue),
		Message:                mapMessageInput(req.Message),
		PerAgentEnvSpecs:       mapPerAgentEnvSpecs(req.PerAgentEnv),
		Audit:                  mapEnvDispatchAuditRequest(req.Audit),
	})
	if err != nil {
		writeEnvDispatchError(w, err, res)
		return
	}
	if len(staged) > 0 {
		if stageErr := h.stageEnvDispatchFiles(r.Context(), workspaceID, res.ProjectID, res.ChannelID, staged); stageErr != nil {
			slog.Error("env-dispatch stage_files failed after provision",
				"project_id", res.ProjectID,
				"channel_id", res.ChannelID,
				"error", stageErr,
			)
		}
	}
	writeJSON(w, http.StatusCreated, envDispatchSuccessResponse(res))
}

func envDispatchSuccessResponse(res service.EnvDispatchResult) EnvDispatchResponse {
	runID := ""
	if len(res.Rollouts) > 0 {
		runID = res.Rollouts[0].RunID
	}
	return EnvDispatchResponse{
		RunID:                     runID,
		ChannelID:                 res.ChannelID,
		ProjectID:                 res.ProjectID,
		Status:                    "running",
		Rollouts:                  mapRollouts(res.Rollouts),
		QuietWindowMS:             res.QuietWindowMS,
		TotalTimeoutSeconds:       res.TotalTimeoutSeconds,
		InitialMessageSubmittedAt: res.InitialMessageSubmittedAt,
		RunAgents:                 mapMixedDispatchRunAgents(res.RunAgents),
		Audit:                     envDispatchAuditResponseFromResult(res),
	}
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
	inProgress, err := h.dispatchDiagnosisInProgress(r.Context(), projectID, workspaceID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "lookup diagnosis run")
		return
	}
	if inProgress {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "diagnosis_in_progress"})
		return
	}
	svc := newEnvDispatchService(h, 8)
	if err := svc.DeleteProject(r.Context(), projectID, workspaceID); err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetDag handles GET /api/v1/env-dispatch/{projectID}/dag, the read-only
// assembled segment-DAG endpoint AReaL polls (Task 9, U8). Contract:
//   - 404 when the project does not exist at all.
//   - 403 when the project exists but in another workspace (cross-workspace).
//   - 202 + {"status":"in_progress"} when the dispatch root task
//     (env_dispatch_run.root_task_id) is not yet terminal, not yet bound, or no
//     env_dispatch_run row exists. Readiness is derived EXCLUSIVELY from
//     env_dispatch_run, independent of training_dispatch.
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

// GetFrozenRunDAG serves the run-scoped mixed-RL frozen snapshot. Unlike the
// legacy project DAG, readiness is derived solely from the mixed run lifecycle
// and never from a root task or a dense session-cover assumption.
func (h *Handler) GetFrozenRunDAG(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace ID required")
		return
	}
	runID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "runID"), "runID")
	if !ok {
		return
	}
	run, err := h.Queries.GetMixedRLRun(r.Context(), runID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "lookup mixed run: "+err.Error())
		return
	}
	if run.WorkspaceID != parseUUID(workspaceID) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden"})
		return
	}
	if run.Status != "completed" && run.Status != "failed_timeout" {
		w.Header().Set("Retry-After", "1")
		writeJSON(w, http.StatusAccepted, frozenRunDAGPollingResponse(run))
		return
	}
	dag, err := service.NewMixedRLFreezeService(h.Queries, h.TxStarter).GetFrozenRunDAG(
		r.Context(), runID, run.FrozenSnapshotID.String,
	)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "read frozen mixed run DAG: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, frozenRunDAGResponse(dag))
}

func timeValueForJSON(value pgtype.Timestamptz) any {
	if !value.Valid {
		return nil
	}
	return value.Time
}

func frozenRunDAGPollingResponse(run db.EnvDispatchRun) FrozenRunDAGPollingResponse {
	return FrozenRunDAGPollingResponse{
		RunID:               run.RunID.String(),
		Status:              run.Status,
		QuietCandidateSince: timeValueForJSON(run.QuietCandidateSince),
		DeadlineAt:          timeValueForJSON(run.TimeoutDeadlineAt),
	}
}

func frozenRunDAGResponse(dag service.FrozenDAGRecord) FrozenRunDAGResponse {
	associationsBySegment := make(map[string][]FrozenRunDAGAssociationResponse, len(dag.Segments))
	for _, association := range dag.Associations {
		associationsBySegment[association.SegmentID] = append(
			associationsBySegment[association.SegmentID],
			FrozenRunDAGAssociationResponse{
				CallID: association.ProviderCallID, CallOrdinal: association.CallOrdinal,
				AssociationKind: association.AssociationKind,
			},
		)
	}
	segments := make([]FrozenRunDAGSegmentResponse, 0, len(dag.Segments))
	for _, segment := range dag.Segments {
		nested := associationsBySegment[segment.SegmentID]
		if nested == nil {
			nested = []FrozenRunDAGAssociationResponse{}
		}
		item := FrozenRunDAGSegmentResponse{
			SegmentID: segment.SegmentID, RunAgentID: segment.RunAgentID.String(),
			Kind: segment.Kind, SegmentOrdinal: segment.SegmentOrdinal,
			RewardSource: segment.RewardSource, ProviderCalls: nested,
		}
		if segment.CanonicalActionID.Valid {
			item.CanonicalActionID = segment.CanonicalActionID.String()
		}
		if segment.Reward.Valid {
			reward := segment.Reward.Float64
			item.Reward = &reward
		}
		segments = append(segments, item)
	}
	gaps := make([]FrozenRunDAGCaptureGapResponse, 0, len(dag.CaptureGaps))
	for _, gap := range dag.CaptureGaps {
		gaps = append(gaps, FrozenRunDAGCaptureGapResponse{
			RunAgentID: gap.RunAgentID.String(), TurnID: gap.TurnID.String(), Reason: gap.Reason,
		})
	}
	return FrozenRunDAGResponse{
		RunID: dag.Run.RunID.String(), ProjectID: dag.Run.ProjectID.String(),
		WorkspaceID: dag.Run.WorkspaceID.String(), Status: dag.Run.Status, RunStatus: dag.Run.Status,
		SnapshotID: dag.Snapshot.SnapshotID, SnapshotHash: dag.Snapshot.SnapshotHash,
		SchemaVersion: dag.Snapshot.SchemaVersion, FrozenAt: dag.Run.FrozenAt,
		CaptureGaps: gaps, RunAgents: dag.RunAgents, ProviderCalls: dag.ProviderCalls,
		Segments: segments, Edges: dag.Edges,
	}
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

	// Readiness decision (spec: durable dispatch identity independent of
	// training_dispatch): derive the /dag readiness EXCLUSIVELY from the
	// env_dispatch_run root task, not from training_dispatch. The service's
	// GetDagReadiness wraps the GetEnvDispatchRootTaskStatus dep seam:
	//   - no env_dispatch_run / no root_task_id / non-terminal root ->
	//     DagReadinessInProgress -> 202 {"status":"in_progress"} (keep polling).
	//   - terminal root (completed/failed/cancelled) -> DagReadinessTerminal ->
	//     proceed to DAG assembly below (200 failed or 200 assembled DAG).
	svc := newEnvDispatchService(h, envDispatchConcurrency())
	readiness, err := svc.GetDagReadiness(r.Context(), projectID, workspaceID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "lookup dispatch root: "+err.Error())
		return
	}
	if readiness == service.DagReadinessInProgress {
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
	// Log only genuine server-side failures at Error (gap #3). The traceback is
	// the SERVER's origin stack, which is meaningless for a 4xx client error —
	// the server did nothing wrong, the caller sent a malformed request. The
	// RequestLogger already records every 4xx as a WARN with path+user_id, and
	// the response body carries the reason, so an extra per-request Error plus
	// traceback for validation failures only turns one runaway client into a
	// log flood (LRM-640: ~1k empty-content env-dispatch 400s/min). Reserve
	// Error for the cases where the server is actually at fault.
	isClientError := strings.Contains(msg, "validation_failed") || strings.Contains(msg, "not_implemented")
	if !isClientError {
		slog.Error("env_dispatch failed",
			"error", msg,
			"traceback", tb,
			"project_id", res.ProjectID,
		)
	}
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
			Audit:     envDispatchAuditResponseFromResult(res),
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

func mapEnvDispatchAuditRequest(request *EnvDispatchAuditRequest) *service.EnvDispatchAuditRequest {
	if request == nil || !request.Enabled {
		return nil
	}
	return &service.EnvDispatchAuditRequest{
		Enabled:           true,
		ReclamationWindow: time.Duration(request.ReclamationWindowSeconds) * time.Second,
	}
}

func envDispatchAuditResponseFromResult(result service.EnvDispatchResult) *EnvDispatchAuditResponse {
	if result.Audit == nil || result.Audit.AuditID == "" {
		return nil
	}
	return envDispatchAuditResponseFromReport(*result.Audit)
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
		spec := service.PerAgentEnvSpec{
			AgentID:   k,
			Template:  m[k].Template,
			BaseEnvID: m[k].BaseEnvID,
		}
		if r := m[k].Runtime; r != nil {
			// Allocate a new service runtime value; do not retain the
			// handler-layer pointer (boundary canonicalization).
			spec.Runtime = &service.ExternalModelRuntime{
				Provider: r.Provider,
				BaseURL:  r.BaseURL,
				APIKey:   r.APIKey,
				Model:    r.Model,
			}
		}
		out = append(out, spec)
	}
	return out
}

func mapMixedDispatchRunAgents(agents []service.MixedDispatchRunAgent) []EnvDispatchRunAgentResponse {
	mapped := make([]EnvDispatchRunAgentResponse, 0, len(agents))
	for _, agent := range agents {
		mapped = append(mapped, EnvDispatchRunAgentResponse{
			SourceAgentID: agent.SourceAgentID, ExecutionAgentID: agent.ExecutionAgentID,
			TrainingMode: agent.TrainingMode,
		})
	}
	return mapped
}
func mapRollouts(rs []service.EnvRollout) []EnvRolloutResponse {
	out := make([]EnvRolloutResponse, 0, len(rs))
	for _, r := range rs {
		out = append(out, EnvRolloutResponse{
			RunID: r.RunID, SourceTaskID: r.SourceTaskID,
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

// newEnvDispatchService centralizes production dependency injection for every
// env-dispatch entry point. Audit storage and lifecycle reclamation are both
// optional: T012/T018 install their concrete implementations here, while a
// nil dependency keeps ordinary dispatches fully unchanged.
func newEnvDispatchService(h *Handler, concurrency int) *service.EnvDispatchService {
	svc := service.NewEnvDispatchService(newEnvDispatchDepsAdapter(h), concurrency).
		WithAuditStorage(newEnvDispatchAuditStorage(h)).
		WithReclaimer(newEnvDispatchReclaimer(h))
	if lc := newEnvSandboxLifecycleService(h); lc != nil {
		svc = svc.WithSandboxLifecycle(lc)
		if placement := newSweLegoTemplatePlacement(h, lc); placement != nil {
			svc = svc.WithSweLegoTemplateResolver(placement)
		}
	}
	return svc
}

// newEnvDispatchReclaimer is the handler-level injection point for the shared
// cleanup implementation. T018 supplies the concrete reclaimer; keeping this
// nil before then preserves current project/channel deletion semantics.
func newEnvDispatchReclaimer(_ *Handler) service.EnvDispatchReclaimer {
	return nil
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
	adapter := &envDispatchDepsAdapter{h: h}
	// Wire the env-dispatch run checker so interaction-dag seams can route
	// non-training env-dispatch tasks to local task_messages recording.
	if h.TaskService != nil {
		h.TaskService.EnvDispatchCheck = adapter
	}
	return adapter
}

// HasEnvDispatchRun reports whether the project has an env_dispatch_run row,
// indicating it was created via env-dispatch. Used by interaction-dag seams
// to gate local trajectory recording for non-training dispatch tasks.
func (a *envDispatchDepsAdapter) HasEnvDispatchRun(ctx context.Context, projectID string) (bool, error) {
	pid := parseUUID(projectID)
	var exists bool
	err := a.h.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM env_dispatch_run WHERE project_id = $1)", pid).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// envDispatchDepsAdapter bridges service.EnvDispatchDeps to *Handler.Queries
// (DB) and *Handler.CloudRuntime (sandbox lifecycle). Each method maps the
// service's string IDs to pgtype.UUID via parseUUID (trusted: these IDs are
// either sqlc round-trips or already-validated request inputs by the time
// they reach the adapter).
type mixedDispatchPiRunClient interface {
	RequestPreparePiRun(context.Context, protocol.PreparePiRunRequestPayload) (*protocol.PreparePiRunResponsePayload, error)
	RequestRevokePiRun(context.Context, protocol.RevokePiRunRequestPayload) error
}

type envDispatchDepsAdapter struct {
	h      *Handler
	piRuns mixedDispatchPiRunClient
}

func (a *envDispatchDepsAdapter) mixedDispatchPiRuns() mixedDispatchPiRunClient {
	if a.piRuns != nil {
		return a.piRuns
	}
	if a.h != nil {
		return a.h.DaemonHub
	}
	return nil
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
	// Task 8A: fence the project's canonical memory sources in the same
	// transaction as the business delete. The deps interface carries no
	// actor, so the route is the attributable principal.
	wsUUID := parseUUID(workspaceID)
	projUUID := parseUUID(projectID)
	tx, err := a.h.TxStarter.Begin(ctx)
	if err != nil {
		return stackerr.Wrap(err, "start retraction transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := a.h.fenceMemorySourcesTx(ctx, tx, wsUUID,
		[]service.MemorySourceRef{memorySourceRef(wsUUID, service.MemorySourceProject, projUUID)},
		"env_dispatch_cleanup", "project deleted via env dispatch cleanup"); err != nil {
		return stackerr.Wrap(err, "fence project memory")
	}
	err = a.h.Queries.WithTx(tx).DeleteProject(ctx, db.DeleteProjectParams{
		ID:          projUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return stackerr.Wrap(err, "delete project")
	}
	if err := tx.Commit(ctx); err != nil {
		return stackerr.Wrap(err, "commit project delete")
	}
	return nil
}

func (a *envDispatchDepsAdapter) ResolveMessageRoster(ctx context.Context, workspaceID, agentID string, specs []service.PerAgentEnvSpec) (service.MessageRoster, error) {
	wsID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return service.MessageRoster{}, fmt.Errorf("parse workspace_id: %w", err)
	}
	roster := service.MessageRoster{
		LeaderID: agentID,
		AgentIDs: make([]string, 0, 1+len(specs)),
	}
	seen := make(map[string]struct{}, 1+len(specs))
	members := make([]string, 0, 1+len(specs))
	members = append(members, agentID)
	for _, spec := range specs {
		members = append(members, spec.AgentID)
	}
	for _, memberID := range members {
		if _, duplicate := seen[memberID]; duplicate {
			continue
		}
		memberUUID, err := util.ParseUUID(memberID)
		if err != nil {
			return service.MessageRoster{}, fmt.Errorf("parse roster agent_id %q: %w", memberID, err)
		}
		if _, err := a.h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID: memberUUID, WorkspaceID: wsID,
		}); err != nil {
			return service.MessageRoster{}, fmt.Errorf("load roster agent %s in workspace: %w", memberID, err)
		}
		seen[memberID] = struct{}{}
		roster.AgentIDs = append(roster.AgentIDs, memberID)
	}
	return roster, nil
}

// ResolveMixedDispatchRoster performs the read-only production half of mixed
// dispatch preflight. It validates each source member against its authoritative
// bound Pi runtime and reports whether the server-owned online route or the
// source/external offline model configuration is complete. Provisioning and
// runtime readiness still occur later, before the canonical initial send.
func (a *envDispatchDepsAdapter) ResolveMixedDispatchRoster(ctx context.Context, workspaceID string, roster service.MessageRoster, specs []service.PerAgentEnvSpec) ([]service.MixedDispatchRosterAgent, error) {
	wsID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("parse workspace_id: %w", err)
	}
	specByAgent := make(map[string]service.PerAgentEnvSpec, len(specs))
	for _, spec := range specs {
		specByAgent[spec.AgentID] = spec
	}
	training := service.LoadTrainingConfig()
	onlineReady := strings.TrimSpace(training.BridgeStubURL) != "" && strings.TrimSpace(training.AdminAPIKey) != ""
	result := make([]service.MixedDispatchRosterAgent, 0, len(roster.AgentIDs))
	for _, sourceAgentID := range roster.AgentIDs {
		agentID, err := util.ParseUUID(sourceAgentID)
		if err != nil {
			return nil, fmt.Errorf("parse source agent_id: %w", err)
		}
		source, err := a.h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: agentID, WorkspaceID: wsID})
		if err != nil {
			return nil, fmt.Errorf("load mixed dispatch source agent %s: %w", sourceAgentID, err)
		}
		bound, err := a.h.Queries.GetAgentBoundRuntimeForWorkspace(ctx, db.GetAgentBoundRuntimeForWorkspaceParams{AgentID: agentID, WorkspaceID: wsID})
		if err != nil {
			return nil, fmt.Errorf("load mixed dispatch source runtime %s: %w", sourceAgentID, err)
		}
		offlineReady := source.Model.Valid && strings.TrimSpace(source.Model.String) != ""
		if !offlineReady && len(source.RuntimeConfig) > 0 {
			var runtimeConfig map[string]any
			if json.Unmarshal(source.RuntimeConfig, &runtimeConfig) == nil {
				model, _ := runtimeConfig["model"].(string)
				offlineReady = strings.TrimSpace(model) != ""
			}
		}
		if spec, ok := specByAgent[sourceAgentID]; ok && spec.Runtime != nil {
			if _, err := service.NormalizeExternalModelRuntime(spec.Runtime); err != nil {
				return nil, fmt.Errorf("offline runtime for agent %s: %w", sourceAgentID, err)
			}
			offlineReady = true
		}
		result = append(result, service.MixedDispatchRosterAgent{
			SourceAgentID: sourceAgentID,
			Provider:      bound.Provider,
			TargetPolicy:  "areal-default",
			Tokenizer:     "areal-default",
			OnlineReady:   onlineReady,
			OfflineReady:  offlineReady,
		})
	}
	return result, nil
}

const envDispatchChannelJoinSource = "env_dispatch"

func (a *envDispatchDepsAdapter) CreateEnvDispatchChannel(ctx context.Context, workspaceID, userID, projectID, envID string, roster service.MessageRoster, specs map[string]service.ResolvedPerAgentSandboxPolicy) (string, error) {
	tx, err := a.h.TxStarter.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	channelID, err := createOrdinaryGroupWithOwnerTx(
		ctx, tx,
		parseUUID(workspaceID), parseUUID(userID),
		"env-dispatch-"+uuid.NewString(),
		nil, nil, parseUUID(projectID),
	)
	if err != nil {
		return "", err
	}
	store := envDispatchChannelStore{}
	systemActor := channelMemberSystemActor()
	if err := validateChannelMemberActorWithExec(ctx, tx, workspaceID, systemActor); err != nil {
		return "", err
	}
	for _, agentID := range roster.AgentIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO channel_member (
				channel_id, workspace_id, member_type, member_id,
				added_by_type, added_by_id, join_source
			) VALUES ($1, $2, 'agent', $3, $4, $5, $6)`,
			channelID, workspaceID, agentID,
			systemActor.Type, systemActor.ID, envDispatchChannelJoinSource,
		); err != nil {
			return "", err
		}
		config := json.RawMessage(`{}`)
		if policy, ok := specs[agentID]; ok {
			encoded, err := marshalEnvDispatchSandboxConfig(policy)
			if err != nil {
				return "", fmt.Errorf("encode binding sandbox config for agent %s: %w", agentID, err)
			}
			config = encoded
		}
		if err := store.insertBinding(ctx, tx, envAgentSandboxBinding{EnvID: envID, ChannelID: channelID, SourceAgentID: agentID, ModelConfigOwnerAgentID: agentID, Status: "pending", SandboxConfig: config}); err != nil {
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
	if a.h.envDispatchProvisionAgentTestHook != nil {
		return a.h.envDispatchProvisionAgentTestHook(ctx, in)
	}
	result, err := a.h.provisionEnvDispatchAgent(ctx, ProvisionEnvDispatchAgentInput{
		WorkspaceID:             in.WorkspaceID,
		UserID:                  in.UserID,
		EnvID:                   in.EnvID,
		ProjectID:               in.ProjectID,
		ChannelID:               in.ChannelID,
		AgentID:                 in.AgentID,
		SourceSandboxInstanceID: in.SourceSandboxInstanceID,
		SavepointTemplate:       in.SavepointTemplate,
		SandboxConfig:           in.SandboxConfig,
		TrainingMode:            in.TrainingMode,
		TargetPolicy:            in.TargetPolicy,
		Tokenizer:               in.Tokenizer,
	})
	if err != nil {
		return service.EnvDispatchAgentProvisionResult{}, err
	}
	return service.EnvDispatchAgentProvisionResult{
		AgentID:           result.AgentID,
		SandboxInstanceID: result.SandboxInstanceID,
		RuntimeID:         result.RuntimeID,
		DaemonID:          result.DaemonID,
		ChatSessionID:     result.ChatSessionID,
		AReALSessionID:    result.AReALSessionID,
	}, nil
}

func (a *envDispatchDepsAdapter) CreateChannelMessage(ctx context.Context, channelID, workspaceID, userID, content string) (string, error) {
	if a.h.envDispatchCreateMessageTestHook != nil {
		return a.h.envDispatchCreateMessageTestHook(ctx, channelID, workspaceID, userID, content)
	}
	ch, found := a.h.getChannel(ctx, workspaceID, parseUUID(channelID))
	if !found {
		return "", stackerr.New("create env-dispatch channel message: channel not found")
	}
	message, err := a.h.sendCanonicalChannelMessage(ctx, ch, SendChannelMessageRequest{Content: content}, userID)
	if err != nil {
		return "", stackerr.Wrap(err, "create env-dispatch channel message")
	}
	return message.ID, nil
}

func (a *envDispatchDepsAdapter) PersistMixedDispatchInitialMessage(ctx context.Context, channelID, workspaceID, userID, content string) (service.PreparedMixedDispatchMessage, error) {
	if a.h.envDispatchCreateMessageTestHook != nil {
		messageID, err := a.h.envDispatchCreateMessageTestHook(ctx, channelID, workspaceID, userID, content)
		if err != nil {
			return service.PreparedMixedDispatchMessage{}, err
		}
		return service.NewPreparedMixedDispatchMessage(messageID, time.Now().UTC(), nil), nil
	}
	ch, found := a.h.getChannel(ctx, workspaceID, parseUUID(channelID))
	if !found {
		return service.PreparedMixedDispatchMessage{}, stackerr.New("create env-dispatch channel message: channel not found")
	}
	result, err := a.h.prepareCanonicalChannelMessage(ctx, ch, SendChannelMessageRequest{Content: content}, userID)
	if err != nil {
		return service.PreparedMixedDispatchMessage{}, stackerr.Wrap(err, "create env-dispatch channel message")
	}
	return service.NewPreparedMixedDispatchMessage(result.Message.Message.ID, time.Now().UTC(), result.Acknowledge), nil
}

func (a *envDispatchDepsAdapter) ReclaimMixedDispatchProvision(ctx context.Context, workspaceID, userID, envID, sourceAgentID string, provisioned service.EnvDispatchAgentProvisionResult) error {
	var cleanupErrs []error
	if provisioned.AReALSessionID != "" {
		if err := reclaimMixedDispatchAReALSession(ctx, provisioned.AReALSessionID); err != nil {
			cleanupErrs = append(cleanupErrs, err)
		}
	}
	if provisioned.ChatSessionID != "" {
		if _, err := a.h.DB.Exec(ctx, `DELETE FROM channel_agent_session WHERE chat_session_id::text = $1`, provisioned.ChatSessionID); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("delete provisional channel session binding: %w", err))
		}
		if _, err := a.h.DB.Exec(ctx, `DELETE FROM chat_session WHERE id::text = $1`, provisioned.ChatSessionID); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("delete provisional chat session: %w", err))
		}
	}
	binding, bindingErr := (envDispatchChannelStore{}).binding(ctx, a.h.DB, envID, sourceAgentID)
	if bindingErr == nil && provisioned.AgentID != "" && provisioned.AgentID != sourceAgentID {
		if err := a.h.cleanupFailedEnvDispatchDerivedAgent(ctx, binding.ID, sourceAgentID, provisioned.AgentID); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("delete provisional derived agent: %w", err))
		}
	} else if bindingErr != nil && !errors.Is(bindingErr, pgx.ErrNoRows) {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("load provisional binding: %w", bindingErr))
	}
	if provisioned.SandboxInstanceID != "" {
		lifecycle := newEnvSandboxLifecycleService(a.h)
		if lifecycle == nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("delete provisional sandbox: lifecycle unavailable"))
		} else if err := lifecycle.Delete(ctx, service.SandboxInstanceRef{
			WorkspaceID: workspaceID, InstanceID: provisioned.SandboxInstanceID,
			RuntimeID: provisioned.RuntimeID, DaemonID: provisioned.DaemonID,
		}, userID); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("delete provisional sandbox: %w", err))
		}
	}
	if provisioned.RuntimeID != "" {
		if err := a.DeleteAgentRuntime(ctx, workspaceID, provisioned.RuntimeID); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("delete provisional runtime: %w", err))
		}
	}
	return errors.Join(cleanupErrs...)
}

// LinkEnvDispatchTrainingSession links the binding's persisted training session
// to the real derived-agent task ID after the task is enqueued (AC-4), so DAG
// assembly maps the session to the actual agent run. Best-effort no-op when the
// (envID, agentID) binding is not a training binding (training_session_id NULL)
// or has no binding; link failure is logged and never fails the dispatch.
func (a *envDispatchDepsAdapter) LinkEnvDispatchTrainingSession(ctx context.Context, envID, agentID, projectID, runID, issueID string) error {
	store := envDispatchChannelStore{}
	binding, err := store.binding(ctx, a.h.DB, envID, agentID)
	if err != nil {
		return nil // no binding / not env-dispatch: best-effort no-op
	}
	if binding.TrainingSessionID == nil || *binding.TrainingSessionID == "" {
		return nil // not a training binding
	}
	dagSvc := service.NewInteractionDAGService(a.h.Queries, nil, true)
	if err := dagSvc.LinkSessionTask(ctx, *binding.TrainingSessionID, projectID, runID, issueID); err != nil {
		slog.Warn("env-dispatch: link training session->real task failed",
			"env_id", envID, "agent_id", agentID, "run_id", runID,
			"session_id", *binding.TrainingSessionID, "error", err)
		return nil
	}
	slog.Info("env-dispatch training session linked to real task",
		"env_id", envID, "agent_id", agentID, "run_id", runID, "session_id", *binding.TrainingSessionID)
	return nil
}

func (a *envDispatchDepsAdapter) CreateMixedDispatchRun(ctx context.Context, projectID, workspaceID, sourceTaskID string, sampleIndex, quietWindowMS, totalTimeoutSeconds int) (string, error) {
	runUUID := uuid.New()
	runID := pgtype.UUID{Bytes: runUUID, Valid: true}
	var sourceTaskUUID pgtype.UUID
	if sourceTaskID != "" {
		sourceTaskUUID = parseUUID(sourceTaskID)
	}
	store := service.NewEnvDispatchRunStore(a.h.Queries)
	if _, err := store.CreateRun(ctx, service.CreateEnvDispatchRunInput{
		RunID: runID, ProjectID: parseUUID(projectID), WorkspaceID: parseUUID(workspaceID),
		SourceTaskID: sourceTaskUUID, SampleIndex: int32(sampleIndex), QuietWindowMS: int32(quietWindowMS), TotalTimeoutSeconds: int32(totalTimeoutSeconds),
	}); err != nil {
		return "", err
	}
	if _, err := store.TransitionStatus(ctx, runID, "provisioning", "preflight"); err != nil {
		return "", err
	}
	return runUUID.String(), nil
}

// envDispatchPreparePiRunSettleTimeout bounds how long the dispatch waits for a
// freshly provisioned daemon's WebSocket to settle before giving up on
// PreparePiRun. The daemon registers over REST (flipping its runtime online in
// the DB) BEFORE it dials the WebSocket, and WaitForOnlineSandboxRuntime only
// observes the DB row, so PreparePiRun can race the WS connect and reach the hub
// while no socket watches the runtime yet (ErrRuntimeOffline). The window is
// transient and self-heals in milliseconds-to-seconds, so the dispatch retries
// within this bound instead of failing the whole rollout. Vars (not consts) so
// tests can shrink the window.
var (
	envDispatchPreparePiRunSettleTimeout = 30 * time.Second
	envDispatchPreparePiRunRetryDelay    = 200 * time.Millisecond
)

func (a *envDispatchDepsAdapter) PrepareMixedDispatchRunAgent(ctx context.Context, runID string, runAgent service.MixedDispatchRunAgent) (service.MixedDispatchRunAgent, error) {
	if a.h.envDispatchPreparePiRunTestHook != nil {
		return a.h.envDispatchPreparePiRunTestHook(ctx, runID, runAgent)
	}
	client := a.mixedDispatchPiRuns()
	if client == nil {
		return service.MixedDispatchRunAgent{}, errors.New("prepare mixed Pi run: daemon lifecycle transport unavailable")
	}
	runAgent.RunAgentID = uuid.NewString()
	request := protocol.PreparePiRunRequestPayload{
		RequestID: uuid.NewString(), RuntimeID: runAgent.RuntimeID,
		AgentID: runAgent.ExecutionAgentID, RunID: runID, RunAgentID: runAgent.RunAgentID,
	}
	response, err := a.requestPreparePiRunWithSettle(ctx, client, request)
	if err != nil {
		return service.MixedDispatchRunAgent{}, fmt.Errorf("prepare mixed Pi run: %w", err)
	}
	runAgent.PiSessionID = response.SessionID
	runAgent.CaptureBoundary = response.CaptureBoundary
	return runAgent, nil
}

// requestPreparePiRunWithSettle calls client.RequestPreparePiRun, retrying while
// the hub reports no daemon connection for the runtime (ErrRuntimeOffline). This
// rides out the gap between the daemon's REST register (runtime online in the DB)
// and its WebSocket connect, which WaitForOnlineSandboxRuntime cannot observe. A
// non-offline error fails immediately, and the bounded settle deadline keeps a
// genuinely dead daemon from stalling the rollout.
func (a *envDispatchDepsAdapter) requestPreparePiRunWithSettle(ctx context.Context, client mixedDispatchPiRunClient, request protocol.PreparePiRunRequestPayload) (*protocol.PreparePiRunResponsePayload, error) {
	deadline := time.Now().Add(envDispatchPreparePiRunSettleTimeout)
	var lastErr error
	for {
		response, err := client.RequestPreparePiRun(ctx, request)
		if err == nil {
			return response, nil
		}
		if !errors.Is(err, daemonws.ErrRuntimeOffline) {
			return nil, err
		}
		lastErr = err
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("daemon WebSocket not connected within %s: %w", envDispatchPreparePiRunSettleTimeout, lastErr)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(envDispatchPreparePiRunRetryDelay):
		}
	}
}

func (a *envDispatchDepsAdapter) RevokeMixedDispatchRunAgent(ctx context.Context, runID string, runAgent service.MixedDispatchRunAgent) error {
	client := a.mixedDispatchPiRuns()
	if client == nil || runAgent.RunAgentID == "" {
		return nil
	}
	return client.RequestRevokePiRun(ctx, protocol.RevokePiRunRequestPayload{
		RequestID: uuid.NewString(), RuntimeID: runAgent.RuntimeID,
		AgentID: runAgent.ExecutionAgentID, RunID: runID, RunAgentID: runAgent.RunAgentID,
	})
}

func (a *envDispatchDepsAdapter) BindMixedDispatchRunAgent(ctx context.Context, runID string, agent service.MixedDispatchRunAgent) error {
	arealSessionID := agent.AReALSessionID
	_, err := service.NewEnvDispatchRunStore(a.h.Queries).BindRunAgent(ctx, service.BindEnvDispatchRunAgentInput{
		RunAgentID: parseUUID(agent.RunAgentID), RunID: parseUUID(runID), SourceAgentID: parseUUID(agent.SourceAgentID), ExecutionAgentID: parseUUID(agent.ExecutionAgentID), RuntimeID: parseUUID(agent.RuntimeID),
		PiSessionID: agent.PiSessionID, TrainingMode: agent.TrainingMode, AReALSessionID: arealSessionID, CaptureBoundary: agent.CaptureBoundary,
	})
	return err
}

func (a *envDispatchDepsAdapter) StartMixedDispatchRun(ctx context.Context, runID string, submittedAt time.Time) error {
	_, err := service.NewEnvDispatchRunStore(a.h.Queries).StartTimeout(ctx, parseUUID(runID), submittedAt)
	return err
}

func (a *envDispatchDepsAdapter) EnqueueEnvDispatchChannelRun(ctx context.Context, workspaceID, userID string, in service.ChannelRunInput, _ int) (string, error) {
	if workspaceID == "" || userID == "" || in.AgentID == "" || in.ChannelID == "" ||
		in.ProjectID == "" || in.ChatSessionID == "" || in.SandboxInstanceID == "" || in.RuntimeID == "" || in.SourceMessageID == "" {
		return "", stackerr.New("enqueue env-dispatch channel run: execution identity is required")
	}
	tx, err := a.h.TxStarter.Begin(ctx)
	if err != nil {
		return "", stackerr.Wrap(err, "begin env-dispatch channel run")
	}
	defer tx.Rollback(ctx)
	qtx := a.h.Queries.WithTx(tx)

	session, err := qtx.GetChatSession(ctx, parseUUID(in.ChatSessionID))
	if err != nil {
		return "", stackerr.Wrap(err, "get env-dispatch chat session for run")
	}
	if uuidToString(session.WorkspaceID) != workspaceID ||
		uuidToString(session.ProjectID) != in.ProjectID ||
		uuidToString(session.AgentID) != in.AgentID ||
		uuidToString(session.RuntimeID) != in.RuntimeID {
		return "", stackerr.New("enqueue env-dispatch channel run: session identity mismatch")
	}

	var prompt string
	if err := tx.QueryRow(ctx, `
SELECT content
FROM channel_message
WHERE id = $1 AND channel_id = $2 AND workspace_id = $3 AND deleted_at IS NULL`,
		parseUUID(in.SourceMessageID), parseUUID(in.ChannelID), parseUUID(workspaceID)).Scan(&prompt); err != nil {
		return "", stackerr.Wrap(err, "load env-dispatch channel prompt")
	}
	if strings.TrimSpace(prompt) == "" {
		return "", stackerr.New("enqueue env-dispatch channel run: channel prompt is empty")
	}

	// Frame the raw channel message as a group-chat prompt so the derived agent
	// replies INTO this env-dispatch channel (like a frontend channel message)
	// instead of guessing a dm:@<user> target — which would land the reply in a
	// DM the dispatcher never sees. See buildEnvDispatchChannelPrompt.
	var channelName string
	if err := tx.QueryRow(ctx, `
SELECT name FROM channel WHERE id = $1 AND workspace_id = $2`,
		parseUUID(in.ChannelID), parseUUID(workspaceID)).Scan(&channelName); err != nil {
		return "", stackerr.Wrap(err, "load env-dispatch channel name")
	}
	framedPrompt := buildEnvDispatchChannelPrompt(channelName, prompt)

	targetAgent, err := qtx.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
		ID:          parseUUID(in.AgentID),
		WorkspaceID: parseUUID(workspaceID),
	})
	if err != nil {
		return "", stackerr.Wrap(err, "get env-dispatch channel task agent")
	}
	// The env-dispatch DELETE endpoint owns this sandbox lifecycle. Retain the
	// marker for execution identity/retry metadata, but do not reclaim the
	// sandbox when the first channel task reaches a terminal state.
	taskContext := mergeEphemeralSandboxContext(nil, in.SandboxInstanceID, userID, false)
	// Shared_sandbox rollouts (research D5): stamp the sample env as the
	// shared-workdir anchor so the daemon routes this run's agents into the
	// sample's single working directory. Empty keeps the per-agent root.
	taskContext = mergeSharedWorkdirContext(taskContext, in.SharedWorkdirEnvID)
	taskContext, err = service.WithTaskExecutionConfig(taskContext, targetAgent.Model.String, targetAgent.ThinkingLevel.String)
	if err != nil {
		return "", stackerr.Wrap(err, "snapshot env-dispatch channel task execution config")
	}
	task, err := qtx.CreateChatTask(ctx, db.CreateChatTaskParams{
		AgentID:         parseUUID(in.AgentID),
		RuntimeID:       parseUUID(in.RuntimeID),
		Priority:        envDispatchTaskPriority,
		ChatSessionID:   parseUUID(in.ChatSessionID),
		InitiatorUserID: parseUUID(userID),
		Context:         taskContext,
	})
	if err != nil {
		return "", stackerr.Wrap(err, "create env-dispatch channel task")
	}
	// Insert the framed prompt as the agent's user-role message. This mirrors
	// the frontend channel wake (createChannelAgentPromptMessageWithDB persists
	// buildChannelMentionPrompt as content with no parts). parts is intentionally
	// left NULL so the framed content — not the raw channel text — is what the
	// agent reads; otherwise a copied text part would re-introduce the
	// unframed prompt and the agent would again default to a DM reply.
	tag, err := tx.Exec(ctx, `
INSERT INTO chat_message (
    chat_session_id, role, content, task_id, thread_id, trigger_depth
)
SELECT $1, 'user', $6, $2, COALESCE(thread_id, id::text), trigger_depth
FROM channel_message
WHERE id = $3 AND channel_id = $4 AND workspace_id = $5 AND deleted_at IS NULL`,
		parseUUID(in.ChatSessionID), task.ID, parseUUID(in.SourceMessageID),
		parseUUID(in.ChannelID), parseUUID(workspaceID), framedPrompt)
	if err != nil {
		return "", stackerr.Wrap(err, "create env-dispatch channel prompt")
	}
	if tag.RowsAffected() != 1 {
		return "", stackerr.New("create env-dispatch channel prompt: source message not found")
	}
	if err := tx.Commit(ctx); err != nil {
		return "", stackerr.Wrap(err, "commit env-dispatch channel run")
	}

	taskID := util.UUIDToString(task.ID)
	a.maybeOpenTrainingSession(ctx, taskID, in.AgentID, in.ProjectID, in.EnvID)
	return taskID, nil
}

// buildEnvDispatchChannelPrompt frames a raw env-dispatch channel message as a
// group-chat prompt so the derived agent replies INTO the env-dispatch channel
// (analogous to a frontend channel message) instead of guessing a
// `dm:@<human>` target and delivering its reply into a private DM the
// dispatcher never sees.
//
// Constraint: the derived execution agent is intentionally NOT a channel_member
// (ReplaceDispatchChannelMember keeps the source agent as the stable @ alias),
// so an explicit `multica message send --target #<channel>` is rejected by
// resolveChannelOutputTarget's membership check. The reliable, membership-
// independent route is the plain final answer: handleResolvedChannelChatDone
// resolves an empty output target to the origin channel and posts the agent's
// reply without a membership check. The prompt therefore steers the agent to
// answer directly in this channel and explicitly forbids DM / thread / target
// switching.
func buildEnvDispatchChannelPrompt(channelName, rawContent string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are participating in the Multica group chat #%s. Respond only as yourself.\n", channelName)
	b.WriteString(channelOutputContractInstruction)
	b.WriteString("\n")
	b.WriteString("Reply to the message below directly in THIS channel as your normal final answer. Do NOT send a direct message (DM) to anyone, do NOT open a new thread, and do NOT switch to a different channel or target — your final answer is delivered to this channel automatically.\n\n")
	b.WriteString("Current message to respond to:\n")
	fmt.Fprintf(&b, "Env Dispatch (user): %s", rawContent)
	return b.String()
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
func (a *envDispatchDepsAdapter) EnqueueAgentRun(ctx context.Context, workspaceID, actorUserID, agentID, issueID, chatSessionID, sandboxInstanceID, envID, runtimeID string, idx int) (string, error) {
	switch {
	case issueID != "":
		agentUUID := parseUUID(agentID)
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
			AgentID:   agentUUID,
			RuntimeID: taskRuntimeID,
			IssueID:   parseUUID(issueID),
			Priority:  envDispatchTaskPriority,
			Context:   taskContext,
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
			AgentID:       parseUUID(agentID),
		}
		// Phase 2: route to the pre-created sandbox runtime R' when supplied
		// (single-agent daemon-enabled rollout), instead of the session's
		// runtime.
		if runtimeID != "" {
			params.RuntimeID = parseUUID(runtimeID)
		}
		// Phase 5: stamp the ephemeral_sandbox marker (sandbox_instance_id) so
		// the terminal cleanup hook can reclaim the sandbox.
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
	// LRM-1570: ownership is machine-level; PrecreateAgentRuntime no longer
	// writes owner_id on the runtime row. The owner UUID parameter is retained
	// for the calling contract (env-dispatch seeds the binding that carries
	// ownership) but not stored here.
	_, err = util.ParseUUID(ownerUserID)
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
func mergeEphemeralSandboxContext(existing []byte, instanceID, actorUserID string, cleanupOnTerminal ...bool) []byte {
	if instanceID == "" {
		return existing
	}
	obj := map[string]json.RawMessage{}
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &obj); err != nil {
			return existing
		}
	}
	markerFields := map[string]any{
		"sandbox_instance_id": instanceID,
		"actor_user_id":       actorUserID,
	}
	if len(cleanupOnTerminal) > 0 {
		markerFields["cleanup_on_terminal"] = cleanupOnTerminal[0]
	}
	marker, _ := json.Marshal(markerFields)
	obj["ephemeral_sandbox"] = marker
	merged, _ := json.Marshal(obj)
	return merged
}

// mergeSharedWorkdirContext stamps the shared_sandbox workdir anchor into a
// task-context JSON blob, preserving any existing keys (e.g.
// ephemeral_sandbox). The daemon reads context.shared_workdir at claim time
// and anchors the run to the sample env's single shared working directory
// (research D5, FR-008). Returns the input unchanged when envID is empty
// (non-shared dispatch), keeping non-shared task contexts byte-identical.
func mergeSharedWorkdirContext(existing []byte, envID string) []byte {
	if envID == "" {
		return existing
	}
	obj := map[string]json.RawMessage{}
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &obj); err != nil {
			return existing
		}
	}
	marker, _ := json.Marshal(map[string]any{"env_id": envID})
	obj["shared_workdir"] = marker
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

	tx, err := a.h.TxStarter.Begin(ctx)
	if err != nil {
		return stackerr.Wrap(err, "begin agent runtime reclaim")
	}
	defer tx.Rollback(ctx)

	// A ready binding requires all three execution handles. Clear its runtime
	// reference before the FK's ON DELETE SET NULL action can violate that check.
	if _, err := tx.Exec(ctx, `
		UPDATE environment_agent_sandbox binding
		SET status = CASE WHEN status IN ('deleting', 'deleted') THEN status ELSE 'failed_retryable' END,
			sandbox_instance_id = NULL,
			runtime_id = NULL,
			daemon_id = NULL,
			updated_at = now()
		FROM environment env
		WHERE binding.env_id = env.id
		  AND env.workspace_id = $1
		  AND binding.runtime_id = $2`, wsUUID, rtUUID); err != nil {
		return stackerr.Wrap(err, "clear env-dispatch runtime binding")
	}
	if err := a.h.Queries.WithTx(tx).DeleteAgentRuntimeForWorkspace(ctx, db.DeleteAgentRuntimeForWorkspaceParams{
		ID:          rtUUID,
		WorkspaceID: wsUUID,
	}); err != nil {
		return stackerr.Wrap(err, "delete agent runtime")
	}
	if err := tx.Commit(ctx); err != nil {
		return stackerr.Wrap(err, "commit agent runtime reclaim")
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

// sourceTaskFromDB converts the generated immutable row into the service value
// consumed by dispatch. The database query is workspace scoped, so callers
// cannot materialize another workspace's source task.
func sourceTaskFromDB(row db.SourceTask) service.SourceTask {
	return service.SourceTask{
		ID: util.UUIDToString(row.ID), WorkspaceID: util.UUIDToString(row.WorkspaceID),
		Type: service.SourceTaskType(row.Type), Payload: row.Payload, ContentHash: row.ContentHash,
	}
}

func (a *envDispatchDepsAdapter) GetSourceTask(ctx context.Context, sourceTaskID, workspaceID string) (service.SourceTask, error) {
	row, err := a.h.Queries.GetSourceTaskForWorkspace(ctx, db.GetSourceTaskForWorkspaceParams{
		ID: parseUUID(sourceTaskID), WorkspaceID: parseUUID(workspaceID),
	})
	if err != nil {
		return service.SourceTask{}, stackerr.Wrap(err, "get source task")
	}
	return sourceTaskFromDB(row), nil
}

func (a *envDispatchDepsAdapter) GetEnvDispatchRunSourceTask(ctx context.Context, projectID, workspaceID string) (service.SourceTask, error) {
	row, err := a.h.Queries.GetEnvDispatchRunSourceTask(ctx, db.GetEnvDispatchRunSourceTaskParams{
		ProjectID: parseUUID(projectID), WorkspaceID: parseUUID(workspaceID),
	})
	if err != nil {
		return service.SourceTask{}, stackerr.Wrap(err, "get env dispatch run source task")
	}
	return sourceTaskFromDB(row), nil
}

func (a *envDispatchDepsAdapter) CreateEnvDispatchRunWithSource(ctx context.Context, projectID, workspaceID string, trainingMode bool, sourceTaskID string, sampleIndex int) (string, error) {
	row, err := a.h.Queries.CreateEnvDispatchRunWithSource(ctx, db.CreateEnvDispatchRunWithSourceParams{
		ProjectID: parseUUID(projectID), WorkspaceID: parseUUID(workspaceID), TrainingMode: trainingMode,
		SourceTaskID: parseUUID(sourceTaskID), SampleIndex: int32(sampleIndex),
	})
	if err != nil {
		return "", stackerr.Wrap(err, "create source-aware env_dispatch_run")
	}
	return util.UUIDToString(row.RunID), nil
}

func (a *envDispatchDepsAdapter) SetEnvDispatchRunLocalTargets(ctx context.Context, projectID, workspaceID, localIssueID, localChannelID string) error {
	params := db.SetEnvDispatchRunLocalTargetsParams{ProjectID: parseUUID(projectID), WorkspaceID: parseUUID(workspaceID)}
	if localIssueID != "" {
		params.LocalIssueID = parseUUID(localIssueID)
	}
	if localChannelID != "" {
		params.LocalChannelID = parseUUID(localChannelID)
	}
	if err := a.h.Queries.SetEnvDispatchRunLocalTargets(ctx, params); err != nil {
		return stackerr.Wrap(err, "set env dispatch local targets")
	}
	return nil
}

// CreateEnvDispatchRun persists the durable dispatch root row for a project
// (spec: durable dispatch identity independent of training_dispatch). One row
// per project, keyed by project_id. Created after the project exists.
func (a *envDispatchDepsAdapter) CreateEnvDispatchRun(ctx context.Context, projectID, workspaceID string, trainingMode bool) error {
	if err := a.h.Queries.CreateEnvDispatchRun(ctx, db.CreateEnvDispatchRunParams{
		ProjectID:    parseUUID(projectID),
		WorkspaceID:  parseUUID(workspaceID),
		TrainingMode: trainingMode,
	}); err != nil {
		return stackerr.Wrap(err, "create env_dispatch_run")
	}
	return nil
}

// BindEnvDispatchRootTask binds the enqueued leader task as the dispatch root
// (env_dispatch_run.root_task_id = rootTaskID), called immediately after the
// leader task is enqueued.
func (a *envDispatchDepsAdapter) BindEnvDispatchRootTask(ctx context.Context, projectID, rootTaskID string) error {
	if err := a.h.Queries.BindEnvDispatchRootTask(ctx, db.BindEnvDispatchRootTaskParams{
		ProjectID:  parseUUID(projectID),
		RootTaskID: parseUUID(rootTaskID),
	}); err != nil {
		return stackerr.Wrap(err, "bind env_dispatch root task")
	}
	return nil
}

// GetEnvDispatchRootTaskStatus resolves the status of the dispatch's bound root
// task for the /dag readiness decision. Readiness is derived EXCLUSIVELY from
// env_dispatch_run, not from training_dispatch.
func (a *envDispatchDepsAdapter) GetEnvDispatchRootTaskStatus(ctx context.Context, projectID, workspaceID string) (string, error) {
	return a.h.Queries.GetEnvDispatchRootTaskStatus(ctx, db.GetEnvDispatchRootTaskStatusParams{
		ProjectID:   parseUUID(projectID),
		WorkspaceID: parseUUID(workspaceID),
	})
}

// ValidateAgentInWorkspace reports whether agentID is a member of the
// workspace. Returns a typed error when the agent is unknown or unauthorized.
func (a *envDispatchDepsAdapter) ValidateAgentInWorkspace(ctx context.Context, workspaceID, agentID string) error {
	agentUUID, err := util.ParseUUID(agentID)
	if err != nil {
		return stackerr.Wrap(err, "parse agent_id")
	}
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
// returns the resolved per-agent sandbox policy. For BaseEnvID, the template is
// resolved from the env's first sandbox_instance; for Template, the spec's
// template is used directly; a runtime-only scratch policy resolves to
// "default". The runtime, when present, is normalized and validated here so the
// returned policy carries canonical trimmed values.
func (a *envDispatchDepsAdapter) ResolvePerAgentEnvSpec(ctx context.Context, workspaceID string, spec service.PerAgentEnvSpec) (service.ResolvedPerAgentSandboxPolicy, error) {
	template := spec.Template
	if spec.BaseEnvID != "" {
		env, err := a.GetEnv(ctx, spec.BaseEnvID, workspaceID)
		if err != nil {
			return service.ResolvedPerAgentSandboxPolicy{}, stackerr.Wrap(err, fmt.Sprintf("resolve base env %s", spec.BaseEnvID))
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
	}
	if template == "" {
		template = "default"
	}
	runtime, err := service.NormalizeExternalModelRuntime(spec.Runtime)
	if err != nil {
		return service.ResolvedPerAgentSandboxPolicy{}, stackerr.Wrap(err, "normalize per-agent runtime")
	}
	return service.ResolvedPerAgentSandboxPolicy{Template: template, Runtime: runtime}, nil
}

// maybeOpenTrainingSession fires the shared session-open hook for a task
// created at dispatch time. It delegates to TaskService (no-op when training is
// unconfigured) and logs any error loudly — a trained task must never run
// un-proxied silently. Errors are not propagated: the task row already exists.
func (a *envDispatchDepsAdapter) maybeOpenTrainingSession(ctx context.Context, taskID, agentID, projectID, envID string) {
	if a.h.TaskService == nil {
		return
	}
	// AC-4: if this task's agent is a derived env-dispatch agent whose binding
	// already carries a training session (opened before sandbox creation), link
	// the real task to that session instead of opening a new one. Falls back to
	// MaybeOpenTrainingSession (legacy task_id-as-session_ref open) otherwise.
	if envID != "" {
		sid, skey, found, lErr := envDispatchChannelStore{}.trainingSessionForDerivedAgent(ctx, a.h.DB, envID, agentID)
		if lErr == nil && found {
			if err := a.h.TaskService.LinkExistingTrainingSession(ctx, taskID, agentID, projectID, envID, sid, skey); err != nil {
				slog.Error("training session link failed (env_dispatch)",
					"task_id", taskID, "agent_id", agentID, "session_id", sid, "error", err)
			}
			return
		}
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
func (s *stubEnvDispatchDeps) ResolveMessageRoster(_ context.Context, _, agentID string, specs []service.PerAgentEnvSpec) (service.MessageRoster, error) {
	roster := service.MessageRoster{LeaderID: agentID, AgentIDs: []string{agentID}}
	seen := map[string]struct{}{agentID: {}}
	for _, spec := range specs {
		if _, duplicate := seen[spec.AgentID]; duplicate {
			continue
		}
		seen[spec.AgentID] = struct{}{}
		roster.AgentIDs = append(roster.AgentIDs, spec.AgentID)
	}
	return roster, nil
}

func (s *stubEnvDispatchDeps) ResolveMixedDispatchRoster(_ context.Context, _ string, roster service.MessageRoster, _ []service.PerAgentEnvSpec) ([]service.MixedDispatchRosterAgent, error) {
	result := make([]service.MixedDispatchRosterAgent, 0, len(roster.AgentIDs))
	for _, agentID := range roster.AgentIDs {
		result = append(result, service.MixedDispatchRosterAgent{
			SourceAgentID: agentID,
			Provider:      "pi",
			TargetPolicy:  "areal-default",
			Tokenizer:     "areal-default",
			OnlineReady:   true,
			OfflineReady:  true,
		})
	}
	return result, nil
}

func (s *stubEnvDispatchDeps) CreateMixedDispatchRun(context.Context, string, string, string, int, int, int) (string, error) {
	return "70000000-0000-4000-8000-000000000001", nil
}

func (s *stubEnvDispatchDeps) BindMixedDispatchRunAgent(context.Context, string, service.MixedDispatchRunAgent) error {
	return nil
}

func (s *stubEnvDispatchDeps) StartMixedDispatchRun(context.Context, string, time.Time) error {
	return nil
}
func (s *stubEnvDispatchDeps) CreateEnvDispatchChannel(context.Context, string, string, string, string, service.MessageRoster, map[string]service.ResolvedPerAgentSandboxPolicy) (string, error) {
	return "stub-channel", nil
}
func (s *stubEnvDispatchDeps) DeleteChannel(context.Context, string, string) error { return nil }
func (s *stubEnvDispatchDeps) ProvisionEnvDispatchAgent(context.Context, service.EnvDispatchAgentProvisionInput) (service.EnvDispatchAgentProvisionResult, error) {
	return service.EnvDispatchAgentProvisionResult{AgentID: "stub-derived-agent", SandboxInstanceID: "stub-sandbox", RuntimeID: "stub-runtime", DaemonID: "stub-daemon", ChatSessionID: "stub-session"}, nil
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
func (s *stubEnvDispatchDeps) EnqueueAgentRun(context.Context, string, string, string, string, string, string, string, string, int) (string, error) {
	return "stub-run", nil
}
func (s *stubEnvDispatchDeps) LinkEnvDispatchTrainingSession(context.Context, string, string, string, string, string) error {
	return nil
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
func (s *stubEnvDispatchDeps) GetSourceTask(_ context.Context, sourceTaskID, workspaceID string) (service.SourceTask, error) {
	return service.SourceTask{ID: sourceTaskID, WorkspaceID: workspaceID, Type: service.SourceTaskIssue, Payload: json.RawMessage(`{"title":"stub","description":"stub"}`)}, nil
}
func (s *stubEnvDispatchDeps) GetEnvDispatchRunSourceTask(context.Context, string, string) (service.SourceTask, error) {
	return service.SourceTask{}, pgx.ErrNoRows
}
func (s *stubEnvDispatchDeps) CreateEnvDispatchRunWithSource(context.Context, string, string, bool, string, int) (string, error) {
	return "stub-dispatch-run", nil
}
func (s *stubEnvDispatchDeps) SetEnvDispatchRunLocalTargets(context.Context, string, string, string, string) error {
	return nil
}
func (s *stubEnvDispatchDeps) CreateEnvDispatchRun(context.Context, string, string, bool) error {
	return nil
}
func (s *stubEnvDispatchDeps) BindEnvDispatchRootTask(context.Context, string, string) error {
	return nil
}
func (s *stubEnvDispatchDeps) GetEnvDispatchRootTaskStatus(context.Context, string, string) (string, error) {
	return "", pgx.ErrNoRows
}
func (s *stubEnvDispatchDeps) ValidateAgentInWorkspace(context.Context, string, string) error {
	return nil
}
func (s *stubEnvDispatchDeps) ResolvePerAgentEnvSpec(context.Context, string, service.PerAgentEnvSpec) (service.ResolvedPerAgentSandboxPolicy, error) {
	return service.ResolvedPerAgentSandboxPolicy{}, nil
}
