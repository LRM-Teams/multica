package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func seedRacingResearchMethod(
	t *testing.T,
	ctx context.Context,
	fx acceptanceRaceFixture,
	mutate func(*ResearchMethod),
) string {
	t.Helper()
	plan := e2eDeliveryPlan().Plan
	method := ResearchMethod{
		GoalVersion:             fx.run.GoalVersion,
		PlanVersion:             fx.run.PlanVersion,
		DecisionQuestion:        strings.TrimSpace(plan.Method.DecisionQuestion),
		MethodRationale:         strings.TrimSpace(plan.Method.MethodRationale),
		AnalysisMethods:         plan.Method.AnalysisMethods,
		EvidenceRequirements:    plan.Method.EvidenceRequirements,
		EvidenceStandards:       plan.Method.EvidenceStandards,
		InclusionCriteria:       plan.InclusionCriteria,
		ExclusionCriteria:       plan.ExclusionCriteria,
		SourceStrategy:          plan.SourceStrategy,
		CounterevidenceStrategy: plan.Method.CounterevidenceStrategy,
		StoppingConditions:      plan.Method.StoppingConditions,
		Uncertainties:           plan.Uncertainties,
		PlanningRisks:           plan.PlanningRisks,
		CreatedByTaskID:         fx.task.ID,
		CreatedByAgentID:        fx.fixture.agentID,
	}
	if mutate != nil {
		mutate(&method)
	}
	outcome, err := json.Marshal(method)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := json.Marshal(map[string]any{
		"attempt_id": fx.attempt.ID,
		"task_id":    fx.task.ID,
		"task_kind":  fx.task.Kind,
	})
	if err != nil {
		t.Fatal(err)
	}
	rationale := truncateBytes(method.MethodRationale, 8192)
	kind := artifactKindForDecision("research_method")
	contentHash, err := ArtifactContentHash(kind, map[string]any{
		"decision_kind": "research_method",
		"actor_type":    "agent",
		"actor_id":      fx.fixture.agentID,
		"goal_version":  fx.run.GoalVersion,
		"plan_version":  fx.run.PlanVersion,
		"inputs":        json.RawMessage(inputs),
		"outcome":       json.RawMessage(outcome),
		"rationale":     rationale,
	})
	if err != nil {
		t.Fatal(err)
	}

	tx, err := fx.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	decisionID := uuid.NewString()
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_decision (
		  id, workspace_id, session_id, decision_kind, actor_type, actor_id,
		  goal_version, plan_version, inputs, outcome, rationale
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 'research_method', 'agent', $4::uuid,
		          $5, $6, $7::jsonb, $8::jsonb, $9)
	`, decisionID, fx.fixture.workspaceID, fx.run.SessionID, fx.fixture.agentID,
		fx.run.GoalVersion, fx.run.PlanVersion, inputs, outcome, rationale); err != nil {
		t.Fatal(err)
	}
	access, err := deriveManifestOutputAccessTx(
		ctx, tx, fx.fixture.workspaceID, fx.run.SessionID, fx.attempt.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{
		WorkspaceID:            fx.fixture.workspaceID,
		SessionID:              fx.run.SessionID,
		EntityID:               decisionID,
		Kind:                   kind,
		SourceCreatedAt:        timePtr(time.Now()),
		ProvenanceCompleteness: ArtifactProvenanceComplete,
		GoalVersion:            int32Ptr(int32(fx.run.GoalVersion)),
		PlanVersion:            int32Ptr(int32(fx.run.PlanVersion)),
		AccessLevel:            access,
		HashOrigin:             ArtifactHashOriginProduction,
		ContentHash:            contentHash,
		ProducedByAttemptID:    fx.attempt.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return decisionID
}

func countResearchMethods(t *testing.T, ctx context.Context, fx acceptanceRaceFixture) int {
	t.Helper()
	var count int
	if err := fx.pool.QueryRow(ctx, `
		SELECT count(*)::int FROM research_decision
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
		  AND decision_kind = 'research_method'
	`, fx.fixture.workspaceID, fx.run.SessionID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestAcceptResultRaceFencesResearchMethodVersion(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	t.Run("conflicting_method", func(t *testing.T) {
		fx := setupPlanAcceptanceRaceFixture(t, ctx, pool)
		defer cleanupResearchRunFixture(pool, fx.fixture)
		invokeAcceptWithBeforeCommitFault(t, ctx, fx)
		assertAcceptanceRolledBack(t, ctx, fx)
		seedRacingResearchMethod(t, ctx, fx, func(method *ResearchMethod) {
			method.DecisionQuestion = "A conflicting decision question"
		})

		if _, err = fx.store.AcceptResult(ctx, fx.input); !errors.Is(err, ErrResultConflict) {
			t.Fatalf("AcceptResult after Method drift err=%v want ErrResultConflict", err)
		}
		assertAcceptanceRolledBack(t, ctx, fx)
		if count := countResearchMethods(t, ctx, fx); count != 1 {
			t.Fatalf("method count=%d want conflicting singleton", count)
		}
	})

	t.Run("identical_method", func(t *testing.T) {
		fx := setupPlanAcceptanceRaceFixture(t, ctx, pool)
		defer cleanupResearchRunFixture(pool, fx.fixture)
		invokeAcceptWithBeforeCommitFault(t, ctx, fx)
		assertAcceptanceRolledBack(t, ctx, fx)
		seedRacingResearchMethod(t, ctx, fx, nil)

		outcome, acceptErr := fx.store.AcceptResult(ctx, fx.input)
		if acceptErr != nil {
			t.Fatalf("AcceptResult with identical Method: %v", acceptErr)
		}
		if outcome.Replayed {
			t.Fatal("identical preexisting Method must not turn first acceptance into Attempt replay")
		}
		if count := countResearchMethods(t, ctx, fx); count != 1 {
			t.Fatalf("method count=%d want exact singleton reuse", count)
		}
	})
}
