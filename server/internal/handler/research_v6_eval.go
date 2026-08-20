package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/multica-ai/multica/server/internal/researcheval"
)

func defaultProductionMonitorPolicy() researcheval.ProductionMonitorPolicy {
	return researcheval.ProductionMonitorPolicy{
		MinimumSamples:           5,
		MinimumMeanQualityScore:  0.6,
		MinimumQualityPassRate:   0.6,
		MaximumP95CostUnits:      10_000,
		MaximumBudgetOverrunRate: 0.2,
	}
}

func (h *Handler) RecordResearchProductionEpisode(ctx context.Context, workspaceID, sessionID, strategyVersion string, costUnits, budgetUnits float64) error {
	if h.DB == nil || workspaceID == "" || sessionID == "" {
		return nil
	}
	if strategyVersion == "" {
		strategyVersion = "research-v5-default"
	}
	if budgetUnits <= 0 {
		budgetUnits = 1
	}
	if costUnits < 0 {
		costUnits = 0
	}
	_, err := h.DB.Exec(ctx, `
		INSERT INTO research_production_episode (
		  workspace_id, session_id, strategy_version, observed_at,
		  quality_score, quality_passed, quality_signal, cost_units, budget_units
		) VALUES ($1::uuid,$2::uuid,$3,now(),1,TRUE,'user_confirmed_delivery',$4,$5)
		ON CONFLICT (workspace_id, session_id) DO NOTHING
	`, workspaceID, sessionID, strategyVersion, costUnits, budgetUnits)
	return err
}

func (h *Handler) EvaluateResearchProductionWindows(ctx context.Context, workspaceID string) (researcheval.ProductionMonitorReport, error) {
	report := researcheval.ProductionMonitorReport{}
	if h.DB == nil {
		return report, nil
	}
	rows, err := h.DB.Query(ctx, `
		SELECT session_id::text, strategy_version, observed_at, quality_score, quality_passed, cost_units, budget_units
		FROM research_production_episode
		WHERE workspace_id = $1::uuid
		ORDER BY observed_at
	`, workspaceID)
	if err != nil {
		return report, err
	}
	defer rows.Close()
	observations := make([]researcheval.ProductionRunObservation, 0)
	for rows.Next() {
		var observation researcheval.ProductionRunObservation
		if err = rows.Scan(&observation.RunID, &observation.StrategyVersion, &observation.ObservedAt,
			&observation.QualityScore, &observation.QualityPassed, &observation.CostUnits, &observation.BudgetUnits); err != nil {
			return report, err
		}
		observations = append(observations, observation)
	}
	if err = rows.Err(); err != nil {
		return report, err
	}
	if len(observations) == 0 {
		return report, nil
	}
	// Evaluate one strategy at a time; mixed versions fail closed by design.
	byStrategy := map[string][]researcheval.ProductionRunObservation{}
	for _, observation := range observations {
		byStrategy[observation.StrategyVersion] = append(byStrategy[observation.StrategyVersion], observation)
	}
	policy := defaultProductionMonitorPolicy()
	var latest researcheval.ProductionMonitorReport
	for _, group := range byStrategy {
		evaluated, evalErr := researcheval.EvaluateProductionWindow(group, policy)
		if evalErr != nil {
			return report, evalErr
		}
		encoded, marshalErr := json.Marshal(evaluated)
		if marshalErr != nil {
			return report, marshalErr
		}
		if _, err = h.DB.Exec(ctx, `
			INSERT INTO research_production_window_report (workspace_id, strategy_version, sufficient_data, within_bounds, report)
			VALUES ($1::uuid,$2,$3,$4,$5::jsonb)
		`, workspaceID, evaluated.StrategyVersion, evaluated.SufficientData, evaluated.WithinBounds, encoded); err != nil {
			return report, err
		}
		latest = evaluated
	}
	return latest, nil
}

func (h *Handler) GetResearchV6ProductionWindow(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id"); !ok {
		return
	}
	report, err := h.EvaluateResearchProductionWindows(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to evaluate production window")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"report":         report,
		"llm_judge":      false,
		"quality_signal": "user_confirmed_delivery",
		"evaluated_at":   time.Now().UTC(),
	})
}

func (h *Handler) ProcessDueResearchProductionWindows(ctx context.Context, limit int) (int, error) {
	if h.DB == nil || limit <= 0 {
		return 0, nil
	}
	rows, err := h.DB.Query(ctx, `
		SELECT DISTINCT workspace_id::text FROM research_production_episode ORDER BY workspace_id LIMIT $1
	`, limit)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	processed := 0
	for rows.Next() {
		var workspaceID string
		if err = rows.Scan(&workspaceID); err != nil {
			return processed, err
		}
		if _, err = h.EvaluateResearchProductionWindows(ctx, workspaceID); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, rows.Err()
}
