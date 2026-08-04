// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// diagnosisRunAPIMaxBody is the hard cap on request bodies, matching the
// loopback tool server (diagnosisToolServerMaxBody).
const diagnosisRunAPIMaxBody = 256 * 1024

// diagnosisRunSegmentLookup is the narrow DAG read surface the run API needs
// to rebuild frozen segment targets from the database.
type diagnosisRunSegmentLookup interface {
	GetInteractionDAGSegmentByID(ctx context.Context, segmentID string) (db.GetInteractionDAGSegmentByIDRow, error)
}

// diagnosisRunAPIDeps carries the run-scoped dependencies for the network
// diagnosis tool surface (spec 005). Unlike the loopback tool server, which
// holds frozen targets and a session cursor key in process memory, the
// network API reconstructs both per request: targets come from the DAG rows,
// the cursor key is derived from the run's persisted capability token hash,
// and all progress state lives in DiagnosisStateStore. The shared endpoint
// logic lives in service (diagnosis_run_ops.go) and is reused by both
// surfaces.
type diagnosisRunAPIDeps struct {
	state     service.DiagnosisRunAPIStore
	pager     service.DiagnosisMessagePager
	dagWriter service.DiagnosisDAGWriter
	segments  diagnosisRunSegmentLookup
	// taskContextFn resolves the root-task goal/gold for a run; a closure so
	// tests can stub the workspace-scoped lookup without a database.
	taskContextFn func(ctx context.Context, run service.DiagnosisRunCheckpoint) (service.TaskContext, error)
	// reclaimFn enqueues the sandbox delete when a sandbox-mode run reaches a
	// terminal state through this API (spec 005 T015). Nil in tests and for
	// handlers without sandbox wiring.
	reclaimFn func(ctx context.Context, run service.DiagnosisRunCheckpoint)
}

func (h *Handler) diagnosisRunAPIDeps() diagnosisRunAPIDeps {
	return diagnosisRunAPIDeps{
		state:         service.NewDiagnosisStateStore(h.Queries),
		pager:         h.Queries,
		dagWriter:     diagnosisDAGWriterAdapter{store: h.Queries},
		segments:      h.Queries,
		taskContextFn: h.diagnosisRunTaskContext,
		reclaimFn:     h.reclaimDiagnosisRunSandbox,
	}
}

// diagnosisRunTaskContext resolves the workspace from the run's project and
// reuses the existing workspace-scoped GetTaskContext logic.
func (h *Handler) diagnosisRunTaskContext(ctx context.Context, run service.DiagnosisRunCheckpoint) (service.TaskContext, error) {
	project, err := h.Queries.GetProject(ctx, parseUUID(run.ProjectID))
	if err != nil {
		return service.TaskContext{}, err
	}
	return service.GetTaskContext(ctx, h.Queries, project.WorkspaceID, run.TaskID)
}

func diagnosisRunFromRequest(w http.ResponseWriter, r *http.Request) (service.DiagnosisRunCheckpoint, bool) {
	run, ok := middleware.DiagnosisRunFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, "diagnosis run not authenticated")
		return service.DiagnosisRunCheckpoint{}, false
	}
	return service.DiagnosisRunCheckpoint{
		RunID:               run.RunID,
		ProjectID:           run.ProjectID,
		TaskID:              run.TaskID,
		TopologyHash:        run.TopologyHash,
		OrderedSegmentIDs:   run.OrderedSegmentIDs,
		Status:              service.DiagnosisRunStatus(run.Status),
		CapabilityTokenHash: run.CapabilityTokenHash,
		ExecutionMode:       run.ExecutionMode,
		SandboxInstanceID:   run.SandboxInstanceID,
	}, true
}

func readDiagnosisRunBody(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	return io.ReadAll(io.LimitReader(r.Body, diagnosisRunAPIMaxBody))
}

func decodeDiagnosisRunBody(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := readDiagnosisRunBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_body")
		return false
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json")
		return false
	}
	return true
}

// ── POST /api/v1/diagnosis-runs/{runID}/get-segment-messages ──

type diagnosisRunGetSegmentMessagesRequest struct {
	SegmentID string `json:"segment_id"`
	Cursor    string `json:"cursor,omitempty"`
}

func (h *Handler) DiagnosisRunGetSegmentMessages(w http.ResponseWriter, r *http.Request) {
	h.diagnosisRunAPIDeps().getSegmentMessages(w, r)
}

func (d diagnosisRunAPIDeps) getSegmentMessages(w http.ResponseWriter, r *http.Request) {
	run, ok := diagnosisRunFromRequest(w, r)
	if !ok {
		return
	}
	var req diagnosisRunGetSegmentMessagesRequest
	if !decodeDiagnosisRunBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.SegmentID) == "" {
		writeError(w, http.StatusBadRequest, "missing_segment_id")
		return
	}
	if !service.SegmentInRun(run, req.SegmentID) {
		writeError(w, http.StatusNotFound, "unknown_segment")
		return
	}
	segment, err := d.segments.GetInteractionDAGSegmentByID(r.Context(), req.SegmentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "unknown_segment")
			return
		}
		writeError(w, http.StatusInternalServerError, "segment_lookup_error")
		return
	}
	if segment.ProjectID != run.ProjectID {
		writeError(w, http.StatusNotFound, "unknown_segment")
		return
	}
	target := service.DiagnosisSegmentTarget{
		SegmentID:  req.SegmentID,
		AgentRunID: segment.AgentRunID,
		StartSeq:   segment.StartSeq,
		EndSeq:     segment.EndSeq,
	}
	page, err := service.FetchDiagnosisSegmentPage(
		r.Context(), d.state, d.pager, service.DiagnosisRunCursorKey(run.CapabilityTokenHash), run.RunID, target, req.Cursor,
	)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrDiagnosisRunNotFound):
			writeError(w, http.StatusNotFound, "unknown_segment")
		case errors.Is(err, service.ErrDiagnosisStaleCursor), errors.Is(err, service.ErrDiagnosisInvalidTransition):
			writeError(w, http.StatusConflict, "stale_cursor")
		default:
			writeError(w, http.StatusInternalServerError, "page_error")
		}
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// ── POST /api/v1/diagnosis-runs/{runID}/record-step-rewards ──

type diagnosisRunStepRewardEntry struct {
	Seq       int    `json:"seq"`
	Score     int    `json:"score"`
	Rationale string `json:"rationale"`
}

type diagnosisRunRecordStepRewardsRequest struct {
	SegmentID string                        `json:"segment_id"`
	Rewards   []diagnosisRunStepRewardEntry `json:"rewards"`
}

type diagnosisRunRejectedReward struct {
	Seq    int    `json:"seq"`
	Reason string `json:"reason"`
}

type diagnosisRunRecordStepRewardsResponse struct {
	PersistedSeqs []int                        `json:"persisted_seqs"`
	MissingSeqs   []int                        `json:"missing_seqs,omitempty"`
	Rejected      []diagnosisRunRejectedReward `json:"rejected,omitempty"`
}

func (h *Handler) DiagnosisRunRecordStepRewards(w http.ResponseWriter, r *http.Request) {
	h.diagnosisRunAPIDeps().recordStepRewards(w, r)
}

func (d diagnosisRunAPIDeps) recordStepRewards(w http.ResponseWriter, r *http.Request) {
	run, ok := diagnosisRunFromRequest(w, r)
	if !ok {
		return
	}
	var req diagnosisRunRecordStepRewardsRequest
	if !decodeDiagnosisRunBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.SegmentID) == "" {
		writeError(w, http.StatusBadRequest, "missing_segment_id")
		return
	}
	if !service.SegmentInRun(run, req.SegmentID) {
		writeError(w, http.StatusNotFound, "unknown_segment")
		return
	}
	if len(req.Rewards) == 0 {
		writeError(w, http.StatusBadRequest, "no_rewards")
		return
	}

	entries := make([]service.DiagnosisStepRewardInput, 0, len(req.Rewards))
	for _, entry := range req.Rewards {
		entries = append(entries, service.DiagnosisStepRewardInput{Seq: entry.Seq, Score: entry.Score, Rationale: entry.Rationale})
	}
	outcome, err := service.RecordDiagnosisStepRewards(
		r.Context(), d.state, d.dagWriter, run.ProjectID, run.RunID, req.SegmentID, entries,
	)
	if err != nil {
		if errors.Is(err, service.ErrDiagnosisRunNotFound) {
			writeError(w, http.StatusNotFound, "unknown_segment")
			return
		}
		writeError(w, http.StatusInternalServerError, "reward_error")
		return
	}

	var rejected []diagnosisRunRejectedReward
	for _, rej := range outcome.Rejected {
		rejected = append(rejected, diagnosisRunRejectedReward{Seq: rej.Seq, Reason: rej.Reason})
	}
	writeJSON(w, http.StatusOK, diagnosisRunRecordStepRewardsResponse{
		PersistedSeqs: outcome.PersistedSeqs,
		MissingSeqs:   outcome.MissingSeqs,
		Rejected:      rejected,
	})
}

// ── GET /api/v1/diagnosis-runs/{runID}/diagnosis-progress ──

func (h *Handler) DiagnosisRunProgress(w http.ResponseWriter, r *http.Request) {
	h.diagnosisRunAPIDeps().diagnosisProgress(w, r)
}

func (d diagnosisRunAPIDeps) diagnosisProgress(w http.ResponseWriter, r *http.Request) {
	run, ok := diagnosisRunFromRequest(w, r)
	if !ok {
		return
	}
	segments, err := d.state.ListSegments(r.Context(), run.RunID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "progress_error")
		return
	}
	writeJSON(w, http.StatusOK, service.BuildDiagnosisRunProgress(run, segments))
}

// ── POST /api/v1/diagnosis-runs/{runID}/finish-segment ──

type diagnosisRunFinishSegmentRequest struct {
	SegmentID string `json:"segment_id"`
}

type diagnosisRunIncompleteCoverage struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

type diagnosisRunFinishSegmentResponse struct {
	Completed  bool                             `json:"completed"`
	Incomplete []diagnosisRunIncompleteCoverage `json:"incomplete,omitempty"`
}

func (h *Handler) DiagnosisRunFinishSegment(w http.ResponseWriter, r *http.Request) {
	h.diagnosisRunAPIDeps().finishSegment(w, r)
}

func (d diagnosisRunAPIDeps) finishSegment(w http.ResponseWriter, r *http.Request) {
	run, ok := diagnosisRunFromRequest(w, r)
	if !ok {
		return
	}
	var req diagnosisRunFinishSegmentRequest
	if !decodeDiagnosisRunBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.SegmentID) == "" {
		writeError(w, http.StatusBadRequest, "missing_segment_id")
		return
	}
	if !service.SegmentInRun(run, req.SegmentID) {
		writeError(w, http.StatusNotFound, "unknown_segment")
		return
	}

	if err := d.state.CompleteSegment(r.Context(), run.RunID, req.SegmentID); err != nil {
		// Surface the precise missing coverage.
		segCkpt, fetchErr := d.state.GetSegment(r.Context(), run.RunID, req.SegmentID)
		if fetchErr != nil {
			writeError(w, http.StatusInternalServerError, "finish_error")
			return
		}
		resp := diagnosisRunFinishSegmentResponse{Completed: false}
		if segCkpt.FetchedMessageCount < segCkpt.ExpectedMessageCount {
			resp.Incomplete = append(resp.Incomplete, diagnosisRunIncompleteCoverage{
				Code:   "missing_messages",
				Detail: err.Error(),
			})
		}
		if segCkpt.RewardCount < segCkpt.ExpectedRewardCount {
			resp.Incomplete = append(resp.Incomplete, diagnosisRunIncompleteCoverage{
				Code:   "missing_rewards",
				Detail: err.Error(),
			})
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	writeJSON(w, http.StatusOK, diagnosisRunFinishSegmentResponse{Completed: true})
}

// ── POST /api/v1/diagnosis-runs/{runID}/complete-diagnosis ──

type diagnosisRunIncompleteSegment struct {
	SegmentID string   `json:"segment_id"`
	Reasons   []string `json:"reasons"`
}

type diagnosisRunCompleteResponse struct {
	Status             string                          `json:"status"`
	IncompleteSegments []diagnosisRunIncompleteSegment `json:"incomplete_segments,omitempty"`
}

func (h *Handler) DiagnosisRunCompleteDiagnosis(w http.ResponseWriter, r *http.Request) {
	h.diagnosisRunAPIDeps().completeDiagnosis(w, r)
}

func (d diagnosisRunAPIDeps) completeDiagnosis(w http.ResponseWriter, r *http.Request) {
	run, ok := diagnosisRunFromRequest(w, r)
	if !ok {
		return
	}

	if err := d.state.CompleteRun(r.Context(), run.RunID, run.TopologyHash); err != nil {
		// Enumerate incomplete segments.
		segments, listErr := d.state.ListSegments(r.Context(), run.RunID)
		if listErr != nil {
			writeError(w, http.StatusInternalServerError, "complete_error")
			return
		}
		var incomplete []diagnosisRunIncompleteSegment
		for _, seg := range service.ListIncompleteDiagnosisSegments(segments) {
			incomplete = append(incomplete, diagnosisRunIncompleteSegment{
				SegmentID: seg.SegmentID,
				Reasons:   []string{seg.Reason},
			})
		}
		writeJSON(w, http.StatusConflict, diagnosisRunCompleteResponse{
			Status:             string(run.Status),
			IncompleteSegments: incomplete,
		})
		return
	}
	// Terminal transition of a sandbox-mode run: reclaim the dedicated
	// sandbox (best-effort, never blocks the response).
	if d.reclaimFn != nil {
		d.reclaimFn(r.Context(), run)
	}
	writeJSON(w, http.StatusOK, diagnosisRunCompleteResponse{Status: string(service.DiagnosisRunCompleted)})
}

// ── GET /api/v1/diagnosis-runs/{runID}/task-context ──

type diagnosisRunTaskContextResponse struct {
	Goal                 string `json:"goal"`
	GoalTruncated        bool   `json:"goal_truncated"`
	GoldContext          string `json:"gold_context"`
	GoldContextTruncated bool   `json:"gold_context_truncated"`
}

func (h *Handler) DiagnosisRunTaskContext(w http.ResponseWriter, r *http.Request) {
	h.diagnosisRunAPIDeps().taskContext(w, r)
}

func (d diagnosisRunAPIDeps) taskContext(w http.ResponseWriter, r *http.Request) {
	run, ok := diagnosisRunFromRequest(w, r)
	if !ok {
		return
	}
	tc, err := d.taskContextFn(r.Context(), run)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "task_context_not_found")
			return
		}
		writeError(w, http.StatusInternalServerError, "task_context_error")
		return
	}
	goal, goalTruncated, goldContext, goldTruncated := service.TruncateDiagnosisTaskContext(tc)
	writeJSON(w, http.StatusOK, diagnosisRunTaskContextResponse{
		Goal:                 goal,
		GoalTruncated:        goalTruncated,
		GoldContext:          goldContext,
		GoldContextTruncated: goldTruncated,
	})
}
