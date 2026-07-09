package handler

import (
	"net/http"
	"strings"
)

type EvolutionMetricsResponse struct {
	UnitMetrics []EvolutionUnitMetricResponse `json:"unit_metrics"`
}

type EvolutionUnitMetricResponse struct {
	UnitID        *string `json:"unit_id,omitempty"`
	LocalUnitID   string  `json:"local_unit_id"`
	UnitType      string  `json:"unit_type"`
	Title         string  `json:"title"`
	InjectedCount int64   `json:"injected_count"`
	UsedCount     int64   `json:"used_count"`
	SuccessCount  int64   `json:"success_count"`
	FailureCount  int64   `json:"failure_count"`
	IgnoredCount  int64   `json:"ignored_count"`
	ConflictCount int64   `json:"conflict_count"`
	SuccessRate   float64 `json:"success_rate"`
	LastUsedAt    *string `json:"last_used_at,omitempty"`
}

func (h *Handler) GetEvolutionMetrics(w http.ResponseWriter, r *http.Request) {
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		workspaceID = strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	}
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return
	}
	unitType := strings.TrimSpace(r.URL.Query().Get("unit_type"))
	rows, err := h.DB.Query(r.Context(), `
		WITH feedback AS (
		  SELECT unit_type, unit_id, local_unit_id,
		         count(*) FILTER (WHERE event = 'injected') AS injected_count,
		         count(*) FILTER (WHERE event = 'used') AS used_count,
		         count(*) FILTER (WHERE event = 'success' OR outcome = 'success') AS success_count,
		         count(*) FILTER (WHERE event = 'failure' OR outcome = 'failure') AS failure_count,
		         count(*) FILTER (WHERE event = 'ignored') AS ignored_count,
		         count(*) FILTER (WHERE event = 'conflict') AS conflict_count,
		         max(created_at) FILTER (WHERE event IN ('used','success','failure')) AS last_used_at
		    FROM evolution_unit_feedback_event
		   WHERE workspace_id = $1
		     AND ($2 = '' OR unit_type = $2)
		   GROUP BY unit_type, unit_id, local_unit_id
		)
		SELECT COALESCE(f.unit_id::text, ''),
		       f.local_unit_id,
		       f.unit_type,
		       COALESCE(am.name, s.name, seu.title, f.local_unit_id, f.unit_type) AS title,
		       f.injected_count,
		       f.used_count,
		       f.success_count,
		       f.failure_count,
		       f.ignored_count,
		       f.conflict_count,
		       CASE WHEN (f.success_count + f.failure_count) > 0 THEN f.success_count::float8 / (f.success_count + f.failure_count) ELSE 0 END AS success_rate,
		       COALESCE(f.last_used_at::text, '')
		  FROM feedback f
		  LEFT JOIN agent_memory am ON am.id = f.unit_id
		  LEFT JOIN skill s ON s.id = f.unit_id
		  LEFT JOIN shared_evolution_unit seu ON seu.id = f.unit_id
		 ORDER BY f.used_count DESC, f.success_count DESC, f.last_used_at DESC NULLS LAST
		 LIMIT 100
	`, workspaceID, unitType)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load evolution metrics")
		return
	}
	defer rows.Close()
	var resp EvolutionMetricsResponse
	for rows.Next() {
		var item EvolutionUnitMetricResponse
		var unitID, lastUsedAt string
		if err := rows.Scan(&unitID, &item.LocalUnitID, &item.UnitType, &item.Title, &item.InjectedCount, &item.UsedCount, &item.SuccessCount, &item.FailureCount, &item.IgnoredCount, &item.ConflictCount, &item.SuccessRate, &lastUsedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to scan evolution metrics")
			return
		}
		if unitID != "" {
			item.UnitID = &unitID
		}
		if lastUsedAt != "" {
			item.LastUsedAt = &lastUsedAt
		}
		resp.UnitMetrics = append(resp.UnitMetrics, item)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load evolution metrics")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
