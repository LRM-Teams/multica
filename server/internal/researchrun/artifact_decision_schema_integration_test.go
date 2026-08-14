package researchrun

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDecisionRelationshipSchemaDiagnostics(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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
		LeadAgentID: fixture.agentID, Goal: "Decision schema diagnostics",
		Title: "Decision schema", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	var taskID, questionID string
	if err = pool.QueryRow(ctx, `
		SELECT task.id::text,question.id::text
		FROM research_task task JOIN research_question question
		  ON question.workspace_id=task.workspace_id AND question.session_id=task.session_id
		 AND question.id=task.question_id
		WHERE task.workspace_id=$1::uuid AND task.session_id=$2::uuid
		ORDER BY task.created_at,task.id LIMIT 1
	`, fixture.workspaceID, run.SessionID).Scan(&taskID, &questionID); err != nil {
		t.Fatal(err)
	}
	insertDecision := func(kind string, outcome string) string {
		t.Helper()
		id := uuid.NewString()
		seedDiagnosticArtifact(t, ctx, pool, fixture.workspaceID, run.SessionID, id, ArtifactKindEvaluationDecision, `
			INSERT INTO research_decision(
			  id,workspace_id,session_id,decision_kind,actor_type,
			  goal_version,plan_version,inputs,outcome,rationale
			) VALUES($1::uuid,$2::uuid,$3::uuid,$4,'system',1,1,'{}'::jsonb,$5::jsonb,'fixture')
		`, id, fixture.workspaceID, run.SessionID, kind, outcome)
		return id
	}
	validID := insertDecision("remediation_routing", `{"task_id":"`+taskID+`","question_id":"`+questionID+`"}`)
	malformedID := insertDecision("remediation_routing", `{"question_id":"not-a-uuid"}`)
	malformedArrayID := insertDecision("selective_steering", `{"impacted_branch_ids":["not-a-uuid"]}`)
	unknownID := insertDecision("future_decision", `{}`)

	for _, tc := range []struct {
		name, decisionID, reason string
		want                     int
	}{
		{name: "valid", decisionID: validID, want: 0},
		{name: "malformed outcome", decisionID: malformedID, reason: "malformed_uuid", want: 1},
		{name: "malformed relationship array", decisionID: malformedArrayID, reason: "malformed_uuid", want: 1},
		{name: "unknown kind", decisionID: unknownID, reason: "unknown_schema", want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var count int
			if err = pool.QueryRow(ctx, `
				SELECT count(*)::int FROM research_artifact_migration_diagnostic
				WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND owner_id=$3::uuid
				  AND owner_kind='evaluation_decision' AND ($4='' OR reason_code=$4)
			`, fixture.workspaceID, run.SessionID, tc.decisionID, tc.reason).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != tc.want {
				t.Fatalf("diagnostic count=%d want=%d", count, tc.want)
			}
		})
	}
}
