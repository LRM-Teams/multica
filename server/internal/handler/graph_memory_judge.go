// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ReportGraphMemoryJudge receives a graph-memory recall report from the
// daemon and kicks the server-side async judge + delayed-reward flow
// (design §5.3, review P0-2). The daemon has no DB access for the judge's
// downstream history and no RL bridge configuration, so judging runs
// server-side; this endpoint is the narrow channel between the two. The
// response is always 202 once the payload validates: judging is async and
// best-effort, and the daemon's recall path never blocks on it.
func (h *Handler) ReportGraphMemoryJudge(w http.ResponseWriter, r *http.Request) {
	var req protocol.GraphMemoryJudgeKickPayload
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.TraceID) == "" || strings.TrimSpace(req.TaskID) == "" || strings.TrimSpace(req.Query) == "" {
		writeError(w, http.StatusBadRequest, "trace_id, task_id and query are required")
		return
	}
	svc := h.GraphMemoryJudge
	if svc == nil {
		// Judge service not wired (e.g. tests): accept and drop silently.
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "dropped"})
		return
	}

	judgeReq := service.GraphMemoryJudgeRequest{
		TraceID: req.TraceID,
		TaskID:  req.TaskID,
		Query:   req.Query,
		Summary: req.Summary,
		NodeIDs: req.NodeIDs,
		Rounds:  req.Rounds,
		Version: req.Version,
	}
	for _, run := range req.AgentRuns {
		judgeReq.AgentRuns = append(judgeReq.AgentRuns, memorygraph.ExploreRun{
			RunID:  run.RunID,
			Seed:   run.Seed,
			Found:  run.Found,
			Rounds: run.Rounds,
			Error:  run.Error,
		})
	}

	go func() {
		// Detached from the request: judging outlives the response. The
		// judge timeout plus a grace margin bounds the goroutine.
		ctx, cancel := context.WithTimeout(context.Background(), svc.JudgeTimeout()+time.Minute)
		defer cancel()
		if err := svc.JudgeRecall(ctx, judgeReq); err != nil {
			slog.Warn("graph memory judge failed", "trace_id", judgeReq.TraceID, "task_id", judgeReq.TaskID, "error", err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}
