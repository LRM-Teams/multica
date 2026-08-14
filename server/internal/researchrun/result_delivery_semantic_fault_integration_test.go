package researchrun

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type deliverySemanticRollbackState struct {
	reports         int
	reportClaims    int
	decisions       int
	results         int
	passports       int
	versions        int
	inputReferences int
	lifecycleEvents int
	policyMutations int
	acceptedEvents  int
	attemptStatus   string
	attemptHash     string
	taskStatus      string
}

func loadDeliverySemanticRollbackState(t *testing.T, ctx context.Context, fx acceptanceRaceFixture) deliverySemanticRollbackState {
	t.Helper()
	var state deliverySemanticRollbackState
	if err := fx.pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*)::int FROM research_report WHERE workspace_id=$1::uuid AND session_id=$2::uuid),
		  (SELECT count(*)::int FROM research_report_claim WHERE workspace_id=$1::uuid AND session_id=$2::uuid),
		  (SELECT count(*)::int FROM research_decision WHERE workspace_id=$1::uuid AND session_id=$2::uuid),
		  (SELECT count(*)::int FROM research_result_artifact WHERE workspace_id=$1::uuid AND session_id=$2::uuid),
		  (SELECT count(*)::int FROM research_artifact_passport WHERE workspace_id=$1::uuid AND session_id=$2::uuid),
		  (SELECT count(*)::int FROM research_artifact_version WHERE workspace_id=$1::uuid AND session_id=$2::uuid),
		  (SELECT count(*)::int FROM research_artifact_input_reference WHERE workspace_id=$1::uuid AND session_id=$2::uuid),
		  (SELECT count(*)::int FROM research_artifact_lifecycle_event WHERE workspace_id=$1::uuid AND session_id=$2::uuid),
		  (SELECT count(*)::int FROM research_artifact_policy_mutation WHERE workspace_id=$1::uuid AND session_id=$2::uuid),
		  (SELECT count(*)::int FROM research_run_event WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND event_type='task_result_accepted'),
		  attempt.status, COALESCE(attempt.result_hash, ''), task.status
		FROM research_task_attempt attempt
		JOIN research_task task ON task.id=$4::uuid AND task.session_id=attempt.session_id
		WHERE attempt.id=$3::uuid AND attempt.session_id=$2::uuid
	`, fx.fixture.workspaceID, fx.run.SessionID, fx.attempt.ID, fx.task.ID).Scan(
		&state.reports, &state.reportClaims, &state.decisions, &state.results,
		&state.passports, &state.versions, &state.inputReferences,
		&state.lifecycleEvents, &state.policyMutations, &state.acceptedEvents,
		&state.attemptStatus, &state.attemptHash, &state.taskStatus,
	); err != nil {
		t.Fatal(err)
	}
	return state
}

func assertDeliverySemanticFaultRollback(
	t *testing.T,
	ctx context.Context,
	fx acceptanceRaceFixture,
	point researchTxFaultPoint,
) {
	t.Helper()
	baseline := loadDeliverySemanticRollbackState(t, ctx, fx)
	injected := errors.New("injected " + string(point))
	fault := &oneShotResearchTxFault{operation: txOpResultAccept, point: point, err: injected}
	fx.store.txFaultHook = fault.hook
	_, err := fx.store.AcceptResult(ctx, fx.input)
	fx.store.txFaultHook = nil
	if !errors.Is(err, injected) || !fault.fired {
		t.Fatalf("AcceptResult point=%s err=%v fired=%v", point, err, fault.fired)
	}
	after := loadDeliverySemanticRollbackState(t, ctx, fx)
	if after != baseline {
		t.Fatalf("partial delivery acceptance survived point=%s\nafter=%+v\nbaseline=%+v", point, after, baseline)
	}
}

func TestAcceptResultSemanticFaultsRollbackCompleteReportAcceptance(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	for _, point := range []researchTxFaultPoint{txResultAfterReport, txResultAfterReportClaim} {
		point := point
		t.Run(string(point), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
			defer cancel()
			pool, err := pgxpool.New(ctx, databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			fx := setupReportAcceptanceRaceFixture(t, ctx, pool)
			defer cleanupResearchRunFixture(pool, fx.fixture)
			assertDeliverySemanticFaultRollback(t, ctx, fx, point)
		})
	}
}

func TestAcceptResultSemanticFaultRollsBackCompleteEvaluationAcceptance(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fx := setupEvaluationAcceptanceRaceFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fx.fixture)
	assertDeliverySemanticFaultRollback(t, ctx, fx, txResultAfterEvaluation)
}
