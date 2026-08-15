package researchrun

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDispatchManifestFreezesRealVersionAndReferenceCounts(t *testing.T) {
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

	fixture := setupPlanAcceptanceRaceFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fixture.fixture)
	if _, err = fixture.store.AcceptResult(ctx, fixture.input); err != nil {
		t.Fatalf("AcceptResult plan: %v", err)
	}
	tasks, err := fixture.store.ListTasks(ctx, fixture.run.SessionID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	var executable *Task
	for i := range tasks {
		if tasks[i].Status == TaskStatusReady {
			executable = &tasks[i]
			break
		}
	}
	if executable == nil {
		t.Fatalf("accepted plan produced no ready task: %+v", tasks)
	}
	attempt, _, err := fixture.store.CreateDispatchIntent(ctx, testDispatchIntentInput(
		t, ctx, fixture.store, fixture.run.SessionID, fixture.fixture.workspaceID,
		executable.ID, fixture.fixture.agentID,
	))
	if err != nil {
		t.Fatalf("CreateDispatchIntent discover: %v", err)
	}
	snapshot, err := fixture.store.TaskContextForAttempt(ctx, attempt.ID, fixture.fixture.workspaceID)
	if err != nil || snapshot.ArtifactProjection == nil {
		t.Fatalf("TaskContextForAttempt: projection=%+v err=%v", snapshot.ArtifactProjection, err)
	}

	var hasVersion, hasInputReference, hasOutputReference bool
	for _, item := range snapshot.ArtifactProjection.Items {
		hasVersion = hasVersion || item.VersionCount > 0
		hasInputReference = hasInputReference || item.InputReferenceCount > 0
		hasOutputReference = hasOutputReference || item.OutputReferenceCount > 0
	}
	if !hasVersion || !hasInputReference || !hasOutputReference {
		t.Fatalf("manifest did not freeze real relationship counts: versions=%v inputs=%v outputs=%v items=%+v",
			hasVersion, hasInputReference, hasOutputReference, snapshot.ArtifactProjection.Items)
	}
}

func TestArtifactCandidateQueryLoadsRelationshipCounts(t *testing.T) {
	source, err := os.ReadFile("artifact_context.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"AS version_count",
		"AS input_reference_count",
		"AS output_reference_count",
		"input_ref.input_version_id",
		"input_v.artifact_id)=(p.workspace_id,p.session_id,p.id)",
		"output_ref.consumer_version_id",
		"output_v.artifact_id)=(p.workspace_id,p.session_id,p.id)",
		"&candidate.VersionCount",
		"&candidate.InputReferenceCount",
		"&candidate.OutputReferenceCount",
	} {
		if !strings.Contains(string(source), required) {
			t.Fatalf("artifact candidate query does not freeze %q", required)
		}
	}
	manifestSource, err := os.ReadFile("artifact_manifest.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"func casArtifactRelationshipSelectionTx(",
		"versionCount != entry.VersionCount",
		"inputReferenceCount != entry.InputReferenceCount",
		"outputReferenceCount != entry.OutputReferenceCount",
		"artifact relationship projection CAS failed",
	} {
		if !strings.Contains(string(manifestSource), required) {
			t.Fatalf("artifact relationship CAS does not enforce %q", required)
		}
	}
}
