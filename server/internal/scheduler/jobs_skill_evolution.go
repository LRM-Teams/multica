package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// JobNameSkillEvolutionReconciliation names the manual orchestrator
// reconciliation lane (plan Slice 3.3). FIRST MILESTONE: the job is NOT
// registered for periodic execution — spec §12.6 admits curator-created
// runs only, and automatic triggers wait until pattern quality, cost, and
// pollution metrics are stable (with a minimum-new-evidence floor). The
// function below is the whole surface: operators and tests call it
// directly; reconciliation never creates runs.
const JobNameSkillEvolutionReconciliation = "skill_evolution_reconciliation"

// skillEvolutionOutboxDrainLimit bounds the pattern-outbox drain per
// workspace sweep (Slice 3.1 dispatcher; the drain rides the
// reconciliation sweep because both are workspace-scoped catch-ups).
const skillEvolutionOutboxDrainLimit = 32

// SkillEvolutionReconciliationReport is the per-workspace outcome of one
// sweep: run decisions applied or observed, plus the outbox drain count.
type SkillEvolutionReconciliationReport struct {
	WorkspaceID            string
	Runs                   service.SkillEvolutionReconciliationSummary
	OutboxEventsDispatched int
}

// RunSkillEvolutionReconciliation sweeps every workspace that has active
// evolution runs: it applies ONLY the safety terminals (failed/stale) the
// reconciler mandates, then drains pending pattern-outbox events when the
// pattern feature gate is on. A workspace-level error aborts the sweep —
// reconciliation is fail-closed, never best-effort across tenants.
func RunSkillEvolutionReconciliation(
	ctx context.Context, pool *pgxpool.Pool, gates service.SkillEvolutionFeatureGates, phaseDeadline time.Duration,
) ([]SkillEvolutionReconciliationReport, error) {
	if pool == nil {
		return nil, fmt.Errorf("skill evolution reconciliation: pool is required")
	}
	workspaceIDs, err := db.New(pool).ListWorkspacesWithActiveSkillEvolutionRuns(ctx)
	if err != nil {
		return nil, fmt.Errorf("skill evolution reconciliation: list workspaces: %w", err)
	}

	ledger := service.NewPostgresSkillEvolutionLedger(pool)
	orchestrator := service.NewSkillEvolutionOrchestratorService(pool, ledger)
	var dispatcher *service.SkillEvolutionOutboxDispatcher
	if gates.PatternConsolidation {
		projection := service.NewSkillPatternProjectionService(pool, service.NewGraphMutationCoordinator(pool), gates)
		dispatcher = service.NewSkillEvolutionOutboxDispatcher(pool, ledger, projection)
	}

	reports := make([]SkillEvolutionReconciliationReport, 0, len(workspaceIDs))
	for _, workspaceID := range workspaceIDs {
		id := workspaceID.String()
		summary, err := orchestrator.ReconcileWorkspace(ctx, id, phaseDeadline)
		if err != nil {
			return reports, fmt.Errorf("skill evolution reconciliation: workspace %s: %w", id, err)
		}
		report := SkillEvolutionReconciliationReport{WorkspaceID: id, Runs: summary}
		if dispatcher != nil {
			dispatched, err := dispatcher.DispatchClaim(ctx, id, skillEvolutionOutboxDrainLimit)
			if err != nil {
				return reports, fmt.Errorf("skill evolution reconciliation: outbox %s: %w", id, err)
			}
			report.OutboxEventsDispatched = dispatched
		}
		reports = append(reports, report)
	}
	return reports, nil
}
