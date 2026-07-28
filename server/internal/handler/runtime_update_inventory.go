package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// UpdateInventoryDiagnostic is a read-only snapshot for ops/product.
// It is NOT a safety guard and must not drive auto remediation.
type UpdateInventoryDiagnostic struct {
	Kind                   string         `json:"kind"` // always "inventory_diagnostic"
	StuckOverMinutes       int            `json:"stuck_over_minutes"`
	AsOf                   time.Time      `json:"as_of"`
	StuckUpdateCounts      map[string]int `json:"stuck_update_counts"` // running, ready_to_apply only
	CLIVersionDistribution map[string]int `json:"cli_version_distribution"`
	EligibleRuntimeCount   int            `json:"eligible_runtime_count"`
	Notes                  string         `json:"notes"`
}

const updateInventoryDiagnosticNotes = "Read-only inventory/diagnostic. Does not write update state, add protocol fields, or auto-remediate. Not a safety guard."

// ComputeUpdateInventoryDiagnostic aggregates two read-only views from
// in-memory slices (unit-testable without DB).
//
// stuck: latest update per runtime in {running, ready_to_apply} whose age
// clock exceeds threshold.
// age clock: running → RunStartedAt if set else UpdatedAt; ready → UpdatedAt.
func ComputeUpdateInventoryDiagnostic(
	now time.Time,
	stuckOver time.Duration,
	latestByRuntime map[string]*UpdateRequest,
	cliVersions []string,
) UpdateInventoryDiagnostic {
	if stuckOver < time.Minute {
		stuckOver = time.Minute
	}
	stuck := map[string]int{
		string(UpdateRunning): 0,
		string(UpdateReady):   0,
	}
	for _, req := range latestByRuntime {
		if req == nil {
			continue
		}
		switch req.Status {
		case UpdateRunning, UpdateReady:
		default:
			continue
		}
		start := req.UpdatedAt
		if req.Status == UpdateRunning && req.RunStartedAt != nil {
			start = *req.RunStartedAt
		}
		if now.Sub(start) < stuckOver {
			continue
		}
		stuck[string(req.Status)]++
	}

	dist := map[string]int{}
	for _, v := range cliVersions {
		key := normalizeInventoryCLIVersion(v)
		dist[key]++
	}

	return UpdateInventoryDiagnostic{
		Kind:                   "inventory_diagnostic",
		StuckOverMinutes:       int(stuckOver / time.Minute),
		AsOf:                   now.UTC(),
		StuckUpdateCounts:      stuck,
		CLIVersionDistribution: dist,
		EligibleRuntimeCount:   len(cliVersions),
		Notes:                  updateInventoryDiagnosticNotes,
	}
}

func normalizeInventoryCLIVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "unknown"
	}
	return v
}

func cliVersionsFromRuntimes(runtimes []db.AgentRuntime) []string {
	out := make([]string, 0, len(runtimes))
	for _, rt := range runtimes {
		out = append(out, readRuntimeCLIVersion(rt.Metadata))
	}
	return out
}

// GetWorkspaceUpdateInventoryDiagnostic is GET
// /api/workspaces/{id}/runtimes/update-inventory-diagnostic
//
// Query: stuck_over_minutes (default 30, min 1, max 1440).
// Auth: workspace member.
func (h *Handler) GetWorkspaceUpdateInventoryDiagnostic(w http.ResponseWriter, r *http.Request) {
	workspaceID := chi.URLParam(r, "id")
	if _, ok := h.requireWorkspaceMember(w, r, workspaceID, "workspace not found"); !ok {
		return
	}

	stuckMin := 30
	if raw := strings.TrimSpace(r.URL.Query().Get("stuck_over_minutes")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 1440 {
			writeError(w, http.StatusBadRequest, "stuck_over_minutes must be an integer 1..1440")
			return
		}
		stuckMin = n
	}
	stuckOver := time.Duration(stuckMin) * time.Minute

	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}

	runtimes, err := h.Queries.ListAgentRuntimes(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list runtimes")
		return
	}

	// Eligible = all workspace agent_runtime rows (no archived_at column on runtime).
	cliVersions := cliVersionsFromRuntimes(runtimes)

	latest := map[string]*UpdateRequest{}
	if h.UpdateStore != nil {
		for _, rt := range runtimes {
			id := uuidToString(rt.ID)
			req, err := h.UpdateStore.LatestForRuntime(r.Context(), id)
			if err != nil || req == nil {
				continue
			}
			latest[id] = req
		}
	}

	out := ComputeUpdateInventoryDiagnostic(time.Now(), stuckOver, latest, cliVersions)
	writeJSON(w, http.StatusOK, out)
}
