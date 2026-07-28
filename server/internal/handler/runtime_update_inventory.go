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
//
// Naming is observational only (Parker): we report "status age over threshold",
// not a judgment that a runtime is "stuck".
//
// ready_to_apply_over_threshold: counts of that status will be removed with
// task #815 clean-cut (do not reference a merged PR number for the sunset).
type UpdateInventoryDiagnostic struct {
	Kind                      string         `json:"kind"` // always "inventory_diagnostic"
	StatusAgeOverMinutes      int            `json:"status_age_over_minutes"`
	AsOf                      time.Time      `json:"as_of"`
	RunningOverThreshold      int            `json:"running_over_threshold"`
	ReadyToApplyOverThreshold int            `json:"ready_to_apply_over_threshold"` // removed with task #815 clean-cut
	CLIVersionDistribution    map[string]int `json:"cli_version_distribution"`
	EligibleRuntimeCount      int            `json:"eligible_runtime_count"`
	Notes                     string         `json:"notes"`
}

const updateInventoryDiagnosticNotes = "Read-only inventory/diagnostic. Status-age counts only (how long a runtime has been in running/ready_to_apply past the threshold). Not a safety guard; no auto-remediation. ready_to_apply_over_threshold is temporary inventory of a status deleted by task #815 clean-cut."

// ComputeUpdateInventoryDiagnostic aggregates two read-only views from
// in-memory slices (unit-testable without DB).
//
// Counts: latest update per runtime in {running, ready_to_apply} whose age
// clock exceeds threshold (status age, not "stuck").
// age clock: running → RunStartedAt if set else UpdatedAt; ready → UpdatedAt.
func ComputeUpdateInventoryDiagnostic(
	now time.Time,
	statusAgeOver time.Duration,
	latestByRuntime map[string]*UpdateRequest,
	cliVersions []string,
) UpdateInventoryDiagnostic {
	if statusAgeOver < time.Minute {
		statusAgeOver = time.Minute
	}
	var runningOver, readyOver int
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
		if now.Sub(start) < statusAgeOver {
			continue
		}
		if req.Status == UpdateRunning {
			runningOver++
		} else {
			readyOver++
		}
	}

	dist := map[string]int{}
	for _, v := range cliVersions {
		key := normalizeInventoryCLIVersion(v)
		dist[key]++
	}

	return UpdateInventoryDiagnostic{
		Kind:                      "inventory_diagnostic",
		StatusAgeOverMinutes:      int(statusAgeOver / time.Minute),
		AsOf:                      now.UTC(),
		RunningOverThreshold:      runningOver,
		ReadyToApplyOverThreshold: readyOver,
		CLIVersionDistribution:    dist,
		EligibleRuntimeCount:      len(cliVersions),
		Notes:                     updateInventoryDiagnosticNotes,
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
// Query: status_age_over_minutes (default 30, min 1, max 1440).
// Auth: workspace member.
func (h *Handler) GetWorkspaceUpdateInventoryDiagnostic(w http.ResponseWriter, r *http.Request) {
	workspaceID := chi.URLParam(r, "id")
	if _, ok := h.requireWorkspaceMember(w, r, workspaceID, "workspace not found"); !ok {
		return
	}

	ageMin := 30
	// Prefer observational name; accept legacy stuck_over_minutes as alias during PR only if needed — do not document stuck.
	raw := strings.TrimSpace(r.URL.Query().Get("status_age_over_minutes"))
	if raw == "" {
		raw = strings.TrimSpace(r.URL.Query().Get("in_status_over_minutes"))
	}
	if raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 1440 {
			writeError(w, http.StatusBadRequest, "status_age_over_minutes must be an integer 1..1440")
			return
		}
		ageMin = n
	}
	statusAgeOver := time.Duration(ageMin) * time.Minute

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

	out := ComputeUpdateInventoryDiagnostic(time.Now(), statusAgeOver, latest, cliVersions)
	writeJSON(w, http.StatusOK, out)
}
