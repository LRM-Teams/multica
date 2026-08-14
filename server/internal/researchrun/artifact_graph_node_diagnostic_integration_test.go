package researchrun

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestGraphNodeTypedReferenceDiagnosticsDenyAndClear(t *testing.T) {
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
		LeadAgentID: fixture.agentID, Goal: "Graph relationship diagnostics",
		Title: "Graph diagnostics", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	var taskID, questionID string
	if err = pool.QueryRow(ctx, `
		SELECT task.id::text,question.id::text
		FROM research_task task
		JOIN research_question question
		  ON question.workspace_id=task.workspace_id AND question.session_id=task.session_id
		 AND question.id=task.question_id
		WHERE task.workspace_id=$1::uuid AND task.session_id=$2::uuid
		ORDER BY task.created_at,task.id LIMIT 1
	`, fixture.workspaceID, run.SessionID).Scan(&taskID, &questionID); err != nil {
		t.Fatal(err)
	}
	sourceID := uuid.NewString()
	seedDiagnosticArtifact(t, ctx, pool, fixture.workspaceID, run.SessionID, sourceID, ArtifactKindLegacySource, `
		INSERT INTO research_source(id,workspace_id,session_id,url,title,source_class)
		VALUES($1::uuid,$2::uuid,$3::uuid,'https://example.test/graph-diagnostic','source','primary')
	`, sourceID, fixture.workspaceID, run.SessionID)
	validPayload, _ := json.Marshal(map[string]any{
		"source_id": sourceID, "question_id": questionID,
		"details": map[string]any{"task_id": taskID},
	})
	validNodeID := uuid.NewString()
	seedDiagnosticArtifact(t, ctx, pool, fixture.workspaceID, run.SessionID, validNodeID, ArtifactKindGraphNode, `
		INSERT INTO research_graph_node(id,workspace_id,session_id,node_type,title,status,payload)
		VALUES($1::uuid,$2::uuid,$3::uuid,'finding','valid graph reference','active',$4::jsonb)
	`, validNodeID, fixture.workspaceID, run.SessionID, validPayload)

	brokenNodeID := uuid.NewString()
	seedDiagnosticArtifact(t, ctx, pool, fixture.workspaceID, run.SessionID, brokenNodeID, ArtifactKindGraphNode, `
		INSERT INTO research_graph_node(id,workspace_id,session_id,node_type,title,status,payload)
		VALUES($1::uuid,$2::uuid,$3::uuid,'finding','broken graph reference','active',
		       '{"source_id":"not-a-uuid"}'::jsonb)
	`, brokenNodeID, fixture.workspaceID, run.SessionID)
	var validCount, brokenCount int
	if err = pool.QueryRow(ctx, `
		SELECT
		  count(*) FILTER (WHERE owner_id=$3::uuid)::int,
		  count(*) FILTER (WHERE owner_id=$4::uuid AND reason_code='malformed_uuid')::int
		FROM research_artifact_migration_diagnostic
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND owner_kind='graph_node'
	`, fixture.workspaceID, run.SessionID, validNodeID, brokenNodeID).Scan(&validCount, &brokenCount); err != nil {
		t.Fatal(err)
	}
	if validCount != 0 || brokenCount != 1 {
		t.Fatalf("graph diagnostics valid=%d broken=%d", validCount, brokenCount)
	}
	if _, err = pool.Exec(ctx, `
		UPDATE research_graph_node SET payload=jsonb_build_object('source_id',$4::text)
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
	`, fixture.workspaceID, run.SessionID, brokenNodeID, sourceID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM research_artifact_migration_diagnostic
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid
		  AND owner_kind='graph_node' AND owner_id=$3::uuid
	`, fixture.workspaceID, run.SessionID, brokenNodeID).Scan(&brokenCount); err != nil {
		t.Fatal(err)
	}
	if brokenCount != 0 {
		t.Fatalf("repaired graph node retained %d diagnostics", brokenCount)
	}
}
