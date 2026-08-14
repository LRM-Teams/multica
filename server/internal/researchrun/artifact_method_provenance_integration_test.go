package researchrun

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAcceptedResearchMethodVersionBindsCanonicalDecisionAndAttempt(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	fixture := seedResearchRunFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fixture)
	store := NewPostgresStore(pool)
	attempt, inboxID, raw, run, task := setupRunningPlanAttempt(t, ctx, store, fixture)
	result, hash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, task, run.Config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AcceptResult(ctx, AcceptResultInput{
		SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
		InboxTaskID: inboxID, Raw: raw, Result: result, Hash: hash,
	}); err != nil {
		t.Fatal(err)
	}

	var (
		decisionID, decisionKind, actorType, actorID, rationale              string
		inputs, outcome                                                      []byte
		goalVersion, planVersion                                             int
		contentHash, hashOrigin, provenanceCompleteness, producedByAttemptID string
	)
	if err = pool.QueryRow(ctx, `
		SELECT decision.id::text, decision.decision_kind, decision.actor_type, decision.actor_id::text,
		       decision.goal_version, decision.plan_version, decision.inputs, decision.outcome,
		       decision.rationale, version.content_hash, version.hash_origin,
		       passport.provenance_completeness, version.produced_by_attempt_id::text
		FROM research_decision decision
		JOIN research_artifact_passport passport
		  ON (passport.workspace_id,passport.session_id,passport.id)=
		     (decision.workspace_id,decision.session_id,decision.id)
		JOIN research_artifact_version version
		  ON (version.workspace_id,version.session_id,version.artifact_id,version.version)=
		     (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
		WHERE decision.workspace_id=$1::uuid AND decision.session_id=$2::uuid
		  AND decision.decision_kind='research_method'
	`, fixture.workspaceID, run.SessionID).Scan(
		&decisionID, &decisionKind, &actorType, &actorID, &goalVersion, &planVersion, &inputs,
		&outcome, &rationale, &contentHash, &hashOrigin, &provenanceCompleteness,
		&producedByAttemptID,
	); err != nil {
		t.Fatal(err)
	}
	wantHash, err := ArtifactContentHash(ArtifactKindMethodDecision, map[string]any{
		"decision_kind": decisionKind,
		"actor_type":    actorType,
		"actor_id":      actorID,
		"goal_version":  goalVersion,
		"plan_version":  planVersion,
		"inputs":        json.RawMessage(inputs),
		"outcome":       json.RawMessage(outcome),
		"rationale":     rationale,
	})
	if err != nil {
		t.Fatal(err)
	}
	if contentHash != wantHash || hashOrigin != string(ArtifactHashOriginProduction) ||
		provenanceCompleteness != string(ArtifactProvenanceComplete) || producedByAttemptID != attempt.ID {
		t.Fatalf("method version hash=%q want=%q origin=%q provenance=%q attempt=%q wantAttempt=%q",
			contentHash, wantHash, hashOrigin, provenanceCompleteness, producedByAttemptID, attempt.ID)
	}
	var lineageCount int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int
		FROM research_artifact_input_reference reference
		JOIN research_artifact_version version
		  ON version.workspace_id=reference.workspace_id
		 AND version.session_id=reference.session_id
		 AND version.id=reference.consumer_version_id
		WHERE version.artifact_id=$1::uuid
		  AND reference.relation IN (
		    'decision_input_task','decision_input_attempt','decision_creator_task'
		  )
	`, decisionID).Scan(&lineageCount); err != nil {
		t.Fatal(err)
	}
	if lineageCount != 3 {
		t.Fatalf("method Decision lineage=%d want=3", lineageCount)
	}
}
