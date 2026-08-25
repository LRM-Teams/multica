package researchrun

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIngestPendingScreenedSourcesCreatesImmutableSnapshot(t *testing.T) {
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
		LeadAgentID: fixture.agentID, Goal: "Ingest screened source", Title: "Ingest screened source",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID, run.WorkspaceID)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	attempt, _, err := store.CreateDispatchIntent(ctx, testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID))
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_task_attempt SET status='running' WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, fixture.workspaceID, run.SessionID, attempt.ID); err != nil {
		t.Fatalf("mark attempt running: %v", err)
	}

	content := []byte("verified source text")
	hash := fmt.Sprintf("sha256:%x", sha256.Sum256(content))
	batch := validSearchLineageBatch()
	batch.WorkspaceID, batch.SessionID, batch.TaskID, batch.AttemptID = fixture.workspaceID, run.SessionID, tasks[0].ID, attempt.ID
	batch.Adapter = "web-v1"
	batch.Candidates[0].CanonicalURL = "http://example.com/source"
	batch.Candidates[0].CanonicalIdentity = "url:http://example.com/source"
	batch.Candidates[0].ContentHash = hash
	batch.Candidates[1].ContentHash = hash
	created, err := store.RecordSearchLineageBatch(ctx, batch)
	if err != nil {
		t.Fatalf("RecordSearchLineageBatch: %v", err)
	}

	adapter := &stubRetrievalAdapter{document: RetrievalDocument{
		MIME: "text/plain", Content: content, ContentHash: hash,
		Cost:   RetrievalCost{Requests: 1, OutputBytes: int64(len(content))},
		Safety: RetrievalSafety{RequestedURL: "http://example.com/source", FinalURL: "http://example.com/source", ResolvedAddresses: []string{"93.184.216.34"}, ScanDisposition: "safe", ResponseBytes: int64(len(content))},
	}}
	engine := newEngine(store, nil, nil)
	engine.retrieval = adapter
	processed, err := engine.IngestPendingScreenedSources(ctx, 8)
	if err != nil || processed != 1 || adapter.fetches != 1 {
		t.Fatalf("processed=%d fetches=%d err=%v", processed, adapter.fetches, err)
	}
	lineage, err := store.SourceSearchLineage(ctx, fixture.workspaceID, run.SessionID, mustSourceSnapshotID(t, ctx, pool, run.SessionID))
	if err != nil || lineage.IngestionKind != string(SourceIngestionScreenedRetrieval) || lineage.SourceCandidateID != created.CandidateIDs["primary"] {
		t.Fatalf("lineage=%+v err=%v", lineage, err)
	}

	replayed, err := engine.IngestPendingScreenedSources(ctx, 8)
	if err != nil || replayed != 0 || adapter.fetches != 1 {
		t.Fatalf("replayed=%d fetches=%d err=%v", replayed, adapter.fetches, err)
	}
}

func mustSourceSnapshotID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sessionID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM research_source_snapshot WHERE session_id=$1::uuid`, sessionID).Scan(&id); err != nil {
		t.Fatalf("source snapshot: %v", err)
	}
	return id
}
