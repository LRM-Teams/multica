package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/service"
	agentpkg "github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// diagnosisTopologicalSegmentIDs returns a deterministic causality-respecting
// order for the segment snapshot supplied to the diagnosis agent.
func diagnosisTopologicalSegmentIDs(dag service.AssembledDag) ([]string, error) {
	indegree := make(map[string]int, len(dag.Segments))
	children := make(map[string][]string, len(dag.Segments))
	for _, segment := range dag.Segments {
		if _, duplicate := indegree[segment.SegmentID]; duplicate {
			return nil, fmt.Errorf("duplicate segment %s", segment.SegmentID)
		}
		indegree[segment.SegmentID] = 0
	}
	for _, edge := range dag.Edges {
		if _, exists := indegree[edge.SrcSegmentID]; !exists {
			return nil, fmt.Errorf("unknown source segment %s", edge.SrcSegmentID)
		}
		if _, exists := indegree[edge.DstSegmentID]; !exists {
			return nil, fmt.Errorf("unknown destination segment %s", edge.DstSegmentID)
		}
		indegree[edge.DstSegmentID]++
		children[edge.SrcSegmentID] = append(children[edge.SrcSegmentID], edge.DstSegmentID)
	}
	ready := make([]string, 0, len(indegree))
	for _, segment := range dag.Segments {
		if indegree[segment.SegmentID] == 0 {
			ready = append(ready, segment.SegmentID)
		}
	}
	ordered := make([]string, 0, len(indegree))
	for len(ready) > 0 {
		segmentID := ready[0]
		ready = ready[1:]
		ordered = append(ordered, segmentID)
		for _, childID := range children[segmentID] {
			indegree[childID]--
			if indegree[childID] == 0 {
				ready = append(ready, childID)
			}
		}
	}
	if len(ordered) != len(dag.Segments) {
		return nil, fmt.Errorf("diagnosis DAG contains a cycle")
	}
	return ordered, nil
}

// DiagnoseEnvDispatchProject starts the opt-in persistent diagnosis flow only
// after a non-training dispatch is terminal and has an assembled dense DAG.
func (h *Handler) DiagnoseEnvDispatchProject(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	h.diagnoseEnvDispatchProject(w, r, chi.URLParam(r, "projectID"))
}

// DiagnoseEnvDispatchChannel is the message-dispatch facade for the project
// route. It resolves channel ownership before delegating.
func (h *Handler) DiagnoseEnvDispatchChannel(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace ID required")
		return
	}
	channelID := chi.URLParam(r, "channelID")
	if _, ok := parseUUIDOrBadRequest(w, channelID, "channelID"); !ok {
		return
	}
	projectID, _, err := h.resolveChannelProject(r.Context(), workspaceID, channelID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
		return
	}
	h.diagnoseEnvDispatchProject(w, r, projectID)
}

func (h *Handler) diagnoseEnvDispatchProject(w http.ResponseWriter, r *http.Request, projectID string) {
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace ID required")
		return
	}
	projectUUID, ok := parseUUIDOrBadRequest(w, projectID, "projectID")
	if !ok {
		return
	}
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspaceID")
	if !ok {
		return
	}
	if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{ID: projectUUID, WorkspaceID: workspaceUUID}); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusServiceUnavailable, "lookup project: "+err.Error())
			return
		}
		if _, lookupErr := h.Queries.GetProject(r.Context(), projectUUID); lookupErr != nil {
			if errors.Is(lookupErr, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
				return
			}
			writeError(w, http.StatusServiceUnavailable, "lookup project: "+lookupErr.Error())
			return
		}
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden"})
		return
	}
	cfg := service.LoadTrainingConfig()
	readiness, err := service.NewEnvDispatchService(newEnvDispatchDepsAdapter(h), envDispatchConcurrency()).GetDagReadiness(r.Context(), projectID, workspaceID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "lookup dispatch root: "+err.Error())
		return
	}
	if readiness != service.DagReadinessTerminal {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "dispatch_not_terminal"})
		return
	}

	dag, err := service.NewInteractionDAGService(h.Queries, nil, true).AssembleAssembledDag(r.Context(), projectID)
	if err != nil || !denseCover(dag) || len(dag.Segments) == 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "dag_not_ready"})
		return
	}
	segmentIDs, err := diagnosisTopologicalSegmentIDs(dag)
	if err != nil {
		writeError(w, http.StatusConflict, "invalid diagnosis DAG")
		return
	}
	rootTaskID, err := h.envDispatchRootTaskID(r.Context(), projectID, workspaceID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "lookup dispatch root: "+err.Error())
		return
	}

	runner, err := service.NewDiagnosisAgentRunner(service.DiagnosisAgentConfig{
		Provider: "pi", ExecutablePath: cfg.DiagnosisAgentPath, Model: cfg.DiagnosisAgentModel,
		Timeout: cfg.DiagnosisAgentTimeout, ScoreMax: cfg.DiagnosisAgentScoreMax,
		DAGStore: h.Queries, MessageStore: h.Queries,
	})
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "diagnosis_unavailable"})
		return
	}
	extensionRoot, err := os.MkdirTemp("", "multica-diagnosis-")
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "create diagnosis workspace")
		return
	}
	defer os.RemoveAll(extensionRoot)
	report, err := runner.DiagnoseOnDemand(r.Context(), projectID, rootTaskID, segmentIDs, service.DiagnosisOnDemandConfig{
		StateStore: service.NewDiagnosisStateStore(h.Queries), DAGWriter: diagnosisDAGWriterAdapter{store: h.Queries},
		MessagePager: h.Queries, PiRPC: agentpkg.NewPiRPCBackend(agentpkg.Config{ExecutablePath: cfg.DiagnosisAgentPath}), ExtensionRoot: extensionRoot,
	})
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "diagnosis_failed"})
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (h *Handler) envDispatchRootTaskID(ctx context.Context, projectID, workspaceID string) (string, error) {
	var taskID string
	err := h.DB.QueryRow(ctx, `SELECT root_task_id::text FROM env_dispatch_run WHERE project_id = $1 AND workspace_id = $2 AND root_task_id IS NOT NULL`, projectID, workspaceID).Scan(&taskID)
	return taskID, err
}

type diagnosisDAGWriterAdapter struct{ store service.InteractionDAGStore }

func (a diagnosisDAGWriterAdapter) UpsertDiagnosisStepReward(ctx context.Context, projectID, segmentID string, seq int32, score int, rationale string) error {
	return a.store.InsertInteractionDAGStepReward(ctx, db.InsertInteractionDAGStepRewardParams{SegmentID: segmentID, Seq: seq, Score: int32(score), Rationale: rationale})
}

func (a diagnosisDAGWriterAdapter) GetDiagnosisStepReward(ctx context.Context, projectID, segmentID string, seq int32) (int, string, bool, error) {
	rewards, err := a.store.ListInteractionDAGStepRewardsForProject(ctx, projectID)
	if err != nil {
		return 0, "", false, err
	}
	for _, reward := range rewards {
		if reward.SegmentID == segmentID && reward.Seq == seq {
			return int(reward.Score), reward.Rationale, true, nil
		}
	}
	return 0, "", false, nil
}

func (a diagnosisDAGWriterAdapter) CountDiagnosisStepRewards(ctx context.Context, projectID, segmentID string) (int, error) {
	rewards, err := a.store.ListInteractionDAGStepRewardsForProject(ctx, projectID)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, reward := range rewards {
		if reward.SegmentID == segmentID {
			count++
		}
	}
	return count, nil
}
