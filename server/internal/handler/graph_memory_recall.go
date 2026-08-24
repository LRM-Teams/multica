// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// RequestGraphMemoryRecall is the daemon's machine recall endpoint (spec
// §1/§3/§14): the workspace/daemon identity comes from the authenticated
// daemon capability — never from the request body — and the recall resolves
// fully server-side. A new recall answers 202 only after the ledger commit
// is durable (A25); replays follow the fail-closed matrix (A23): identical
// replays are idempotent 200s, conflicting/stale/finalized replays are 409s,
// and unknown or mismatched identities get not-found semantics — all with
// zero provider calls and zero ledger mutations.
func (h *Handler) RequestGraphMemoryRecall(w http.ResponseWriter, r *http.Request) {
	workspaceID := middleware.DaemonWorkspaceIDFromContext(r.Context())
	daemonID := middleware.DaemonIDFromContext(r.Context())
	if workspaceID == "" || daemonID == "" {
		// Machine endpoint: a specifically scoped daemon capability is
		// required (spec §14); a bare user token does not qualify.
		writeError(w, http.StatusForbidden, "daemon capability required")
		return
	}
	var req protocol.GraphMemoryRecallRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.TraceID) == "" || strings.TrimSpace(req.TaskID) == "" ||
		strings.TrimSpace(req.RuntimeID) == "" || strings.TrimSpace(req.Query) == "" {
		writeError(w, http.StatusBadRequest, "trace_id, task_id, runtime_id and query are required")
		return
	}
	svc := h.GraphMemoryRecall
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, "graph memory recall is not configured")
		return
	}

	plan, err := svc.Begin(r.Context(), service.GraphMemoryRecallRequest{
		WorkspaceID: workspaceID,
		DaemonID:    daemonID,
		TaskID:      req.TaskID,
		RuntimeID:   req.RuntimeID,
		Query:       req.Query,
		TraceID:     req.TraceID,
		// Caller hints are diagnostics only (A14).
		CallerGraphKind:    req.GraphKind,
		CallerGraphOwnerID: req.GraphOwnerID,
		CallerGraphVersion: req.GraphVersion,
		CallerTrainingMode: req.TrainingMode,
		CallerK:            req.K,
	})
	switch {
	case err == nil:
		// Resolved below.
	case errors.Is(err, service.ErrGraphMemoryRecallDisabled):
		// Graph memory off for this workspace/agent: a recall miss is data,
		// never a business-task failure (spec §1).
		writeJSON(w, http.StatusOK, map[string]string{"status": "disabled"})
		return
	case errors.Is(err, service.ErrGraphMemoryRecallNoScope):
		writeJSON(w, http.StatusOK, map[string]string{"status": "no_scope"})
		return
	case errors.Is(err, service.ErrGraphMemoryRecallIdentity):
		// Not-found semantics on identity denial, like private-channel
		// lineage reads (spec §16).
		writeError(w, http.StatusNotFound, "unknown or mismatched recall identity")
		return
	case errors.Is(err, service.ErrGraphMemoryRecallConflict):
		writeError(w, http.StatusConflict, "RECALL_CONFLICT")
		return
	case errors.Is(err, service.ErrGraphMemoryRecallFinalized):
		writeError(w, http.StatusConflict, "RECALL_FINALIZED")
		return
	default:
		writeError(w, http.StatusInternalServerError, "recall resolution failed")
		return
	}

	status := http.StatusAccepted // durable ledger commit happened inside Begin
	if plan.Replayed {
		status = http.StatusOK
	}
	injection := &service.GraphMemoryRecallInjection{Citations: []memorygraph.Citation{}, Version: plan.GraphVersion}
	if executor := h.GraphMemoryRecallExecutor; executor != nil {
		if plan.Replayed {
			injection, err = executor.LoadReplayInjection(r.Context(), plan)
		} else {
			injection, err = executor.Execute(r.Context(), plan)
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "recall execution failed")
			return
		}
		if injection == nil {
			injection = &service.GraphMemoryRecallInjection{Citations: []memorygraph.Citation{}, Version: plan.GraphVersion}
		}
	}
	writeJSON(w, status, map[string]any{
		"recall_id":     plan.RecallID,
		"trace_id":      plan.TraceID,
		"status":        "accepted",
		"replayed":      plan.Replayed,
		"k":             plan.K,
		"graph_kind":    plan.GraphKind,
		"graph_version": plan.GraphVersion,
		"found":         injection.Found,
		"summary":       injection.Summary,
		"citations":     injection.Citations,
		"rounds":        injection.Rounds,
		"injection":     injection.Content,
	})
}
