package researchrun

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCreateRunRootQuestionHasProductionProvenance(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fixture := seedResearchRunFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fixture)
	store := NewPostgresStore(pool)
	run, _, err := store.CreateRun(ctx, StartInput{
		WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID,
		LeadAgentID: fixture.agentID, Goal: "Question provenance", Title: "Question provenance",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}

	var questionID, parentID, producerTaskID, clientKey, kind, question string
	var required bool
	var priority, impact, uncertainty, novelty, coverage float64
	var goalVersion, planVersion int
	var storedHash, hashOrigin, provenance string
	if err = pool.QueryRow(ctx, `
		SELECT q.id::text, COALESCE(q.parent_question_id::text, ''), COALESCE(q.created_by_task_id::text, ''),
		       q.client_key, q.kind, q.question, q.required, q.priority, q.impact, q.uncertainty,
		       q.novelty, q.coverage, q.goal_version, q.plan_version,
		       v.content_hash, v.hash_origin, p.provenance_completeness
		FROM research_question q
		JOIN research_artifact_passport p ON (p.workspace_id, p.session_id, p.id) = (q.workspace_id, q.session_id, q.id)
		JOIN research_artifact_version v ON (v.workspace_id, v.session_id, v.artifact_id, v.version) =
		  (p.workspace_id, p.session_id, p.id, p.current_version)
		WHERE q.session_id = $1::uuid AND q.client_key = 'root'
	`, run.SessionID).Scan(
		&questionID, &parentID, &producerTaskID, &clientKey, &kind, &question, &required,
		&priority, &impact, &uncertainty, &novelty, &coverage, &goalVersion, &planVersion,
		&storedHash, &hashOrigin, &provenance,
	); err != nil {
		t.Fatal(err)
	}
	wantHash, err := ArtifactContentHash(ArtifactKindQuestion, questionArtifactContent(
		parentID, producerTaskID, clientKey, kind, question, required,
		priority, impact, uncertainty, novelty, coverage, goalVersion, planVersion,
	))
	if err != nil {
		t.Fatal(err)
	}
	if questionID == "" || storedHash != wantHash || hashOrigin != string(ArtifactHashOriginProduction) || provenance != string(ArtifactProvenanceComplete) {
		t.Fatalf("question=%q hash=%q want=%q origin=%q provenance=%q", questionID, storedHash, wantHash, hashOrigin, provenance)
	}
}
