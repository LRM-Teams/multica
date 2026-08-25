package researchrun

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestManifestEntryOrdinalFollowsCanonicalKindAndIDOrder(t *testing.T) {
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
		LeadAgentID: fixture.agentID, Goal: "Manifest entry order", Title: "Entry order",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	seedShadowEquivalenceArtifacts(t, ctx, pool, fixture.workspaceID, run.SessionID)
	tasks, err := store.ListTasks(ctx, run.SessionID, run.WorkspaceID)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	attempt, _, err := store.CreateDispatchIntent(ctx, input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	wantAttemptHash, err := ArtifactContentHash(ArtifactKindAttempt, attemptArtifactContent(attempt))
	if err != nil {
		t.Fatal(err)
	}
	var provenance, hashOrigin, contentHash string
	var artifactGoalVersion, artifactPlanVersion int
	if err = pool.QueryRow(ctx, `
		SELECT p.provenance_completeness, v.hash_origin, v.content_hash,
		       v.goal_version, v.plan_version
		FROM research_artifact_passport p
		JOIN research_artifact_version v
		  ON (v.workspace_id, v.session_id, v.artifact_id, v.version) =
		     (p.workspace_id, p.session_id, p.id, p.current_version)
		WHERE p.workspace_id = $1::uuid AND p.session_id = $2::uuid
		  AND p.id = $3::uuid AND p.entity_kind = 'attempt'
	`, fixture.workspaceID, run.SessionID, attempt.ID).Scan(
		&provenance, &hashOrigin, &contentHash, &artifactGoalVersion, &artifactPlanVersion,
	); err != nil {
		t.Fatal(err)
	}
	if provenance != string(ArtifactProvenanceComplete) || hashOrigin != string(ArtifactHashOriginProduction) {
		t.Fatalf("attempt provenance=%q hash_origin=%q", provenance, hashOrigin)
	}
	if contentHash != wantAttemptHash {
		t.Fatalf("attempt content_hash=%q want=%q", contentHash, wantAttemptHash)
	}
	if artifactGoalVersion != input.Request.Task.GoalVersion || artifactPlanVersion != input.Request.Task.PlanVersion {
		t.Fatalf("attempt goal/plan=%d/%d want=%d/%d", artifactGoalVersion, artifactPlanVersion,
			input.Request.Task.GoalVersion, input.Request.Task.PlanVersion)
	}

	type orderedEntry struct {
		ordinal    int
		kind       string
		artifactID string
	}
	rows, err := pool.Query(ctx, `
		SELECT e.ordinal, p.entity_kind, v.artifact_id::text
		FROM research_artifact_context_entry e
		JOIN research_artifact_context_manifest m
		  ON m.workspace_id = e.workspace_id
		 AND m.session_id = e.session_id
		 AND m.id = e.manifest_id
		JOIN research_artifact_version v
		  ON v.workspace_id = e.workspace_id
		 AND v.session_id = e.session_id
		 AND v.id = e.artifact_version_id
		JOIN research_artifact_passport p
		  ON p.workspace_id = v.workspace_id
		 AND p.session_id = v.session_id
		 AND p.id = v.artifact_id
		WHERE m.workspace_id = $1::uuid
		  AND m.session_id = $2::uuid
		  AND m.attempt_id = $3::uuid
		ORDER BY e.ordinal
	`, fixture.workspaceID, run.SessionID, attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var entries []orderedEntry
	for rows.Next() {
		var entry orderedEntry
		if err = rows.Scan(&entry.ordinal, &entry.kind, &entry.artifactID); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 {
		t.Fatalf("entries=%d want at least 2 for order assertion", len(entries))
	}
	sorted := append([]orderedEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].kind != sorted[j].kind {
			return sorted[i].kind < sorted[j].kind
		}
		return sorted[i].artifactID < sorted[j].artifactID
	})
	for i := range entries {
		if entries[i].kind != sorted[i].kind || entries[i].artifactID != sorted[i].artifactID {
			t.Fatalf("ordinal %d=%+v want canonical %+v", i, entries[i], sorted[i])
		}
		if entries[i].ordinal != i {
			t.Fatalf("ordinal gap at index %d: got %d want %d", i, entries[i].ordinal, i)
		}
	}
}

func TestManifestEntryRepresentationBytesAreFrozenAndHashBound(t *testing.T) {
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
		LeadAgentID: fixture.agentID, Goal: "Representation bytes", Title: "Representation bytes",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	seedShadowEquivalenceArtifacts(t, ctx, pool, fixture.workspaceID, run.SessionID)
	tasks, err := store.ListTasks(ctx, run.SessionID, run.WorkspaceID)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	attempt, _, err := store.CreateDispatchIntent(ctx, input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT e.representation_bytes, v.content_hash, e.representation_hash, p.entity_kind
		FROM research_artifact_context_entry e
		JOIN research_artifact_context_manifest m
		  ON m.workspace_id = e.workspace_id
		 AND m.session_id = e.session_id
		 AND m.id = e.manifest_id
		JOIN research_artifact_version v
		  ON v.workspace_id = e.workspace_id
		 AND v.session_id = e.session_id
		 AND v.id = e.artifact_version_id
		JOIN research_artifact_passport p
		  ON p.workspace_id = v.workspace_id
		 AND p.session_id = v.session_id
		 AND p.id = v.artifact_id
		WHERE m.workspace_id = $1::uuid
		  AND m.session_id = $2::uuid
		  AND m.attempt_id = $3::uuid
	`, fixture.workspaceID, run.SessionID, attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	found := false
	for rows.Next() {
		found = true
		var reprBytes []byte
		var contentHash, reprHash, kind string
		if err = rows.Scan(&reprBytes, &contentHash, &reprHash, &kind); err != nil {
			t.Fatal(err)
		}
		switch ArtifactEntityKind(kind) {
		case ArtifactKindContractRevision, ArtifactKindMethodDecision, ArtifactKindQuestion, ArtifactKindTask, ArtifactKindAttempt, ArtifactKindGraphNode, ArtifactKindLegacySource, ArtifactKindResearchMessage, ArtifactKindProductRoundDecision:
			var decoded struct {
				Order int            `json:"order"`
				Value map[string]any `json:"value"`
			}
			if err = json.Unmarshal(reprBytes, &decoded); err != nil || len(decoded.Value) == 0 {
				t.Fatalf("%s representation is not frozen ordered JSON: %q err=%v", kind, reprBytes, err)
			}
		case ArtifactKindRunSession, ArtifactKindSourceSnapshot, ArtifactKindObservation, ArtifactKindClaim, ArtifactKindReportRevision:
			var decoded map[string]any
			if err = json.Unmarshal(reprBytes, &decoded); err != nil {
				t.Fatalf("%s representation is not frozen wire JSON: %q err=%v", kind, reprBytes, err)
			}
			if kind == string(ArtifactKindRunSession) && decoded["session_id"] != run.SessionID {
				t.Fatalf("run session representation session_id=%v want=%s", decoded["session_id"], run.SessionID)
			}
			if kind != string(ArtifactKindRunSession) && decoded["id"] == "" {
				t.Fatalf("%s representation has no id: %q", kind, reprBytes)
			}
		default:
			if string(reprBytes) != contentHash {
				t.Fatalf("legacy %s representation_bytes=%q content_hash=%q", kind, reprBytes, contentHash)
			}
		}
		wantHash := contentHashFromPayload(reprBytes)
		if reprHash != wantHash {
			t.Fatalf("representation_hash=%q want=%q", reprHash, wantHash)
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected manifest entries with representation bytes")
	}
	before, err := store.TaskContextForAttempt(ctx, attempt.ID, fixture.workspaceID)
	if err != nil {
		t.Fatalf("TaskContextForAttempt before mutation: %v", err)
	}
	if len(before.Questions) == 0 || len(before.Tasks) == 0 || len(before.Attempts) == 0 {
		t.Fatalf("incomplete frozen durable context: questions=%d tasks=%d attempts=%d", len(before.Questions), len(before.Tasks), len(before.Attempts))
	}
	if _, err = pool.Exec(ctx, `UPDATE research_contract_revision SET goal='live-mutated-contract' WHERE workspace_id=$1::uuid AND session_id=$2::uuid`, fixture.workspaceID, run.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_question SET question='live-mutated-question' WHERE workspace_id=$1::uuid AND session_id=$2::uuid`, fixture.workspaceID, run.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_task SET objective='live-mutated-task' WHERE workspace_id=$1::uuid AND session_id=$2::uuid`, fixture.workspaceID, run.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_task_attempt SET diagnostics='live-mutated-attempt' WHERE workspace_id=$1::uuid AND session_id=$2::uuid`, fixture.workspaceID, run.SessionID); err != nil {
		t.Fatal(err)
	}
	after, err := store.TaskContextForAttempt(ctx, attempt.ID, fixture.workspaceID)
	if err != nil {
		t.Fatalf("TaskContextForAttempt after mutation: %v", err)
	}
	if !reflect.DeepEqual(before.Contract, after.Contract) || !reflect.DeepEqual(before.Method, after.Method) ||
		!reflect.DeepEqual(before.Questions, after.Questions) || !reflect.DeepEqual(before.Tasks, after.Tasks) ||
		!reflect.DeepEqual(before.Attempts, after.Attempts) {
		t.Fatalf("task-bound durable context changed after live mutation\nbefore=%+v\nafter=%+v", before, after)
	}

	if _, err = pool.Exec(ctx, `UPDATE research_session SET title='live-mutated-title', goal='live-mutated-goal' WHERE workspace_id=$1::uuid AND id=$2::uuid`, fixture.workspaceID, run.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_fleet_member SET status='archived' WHERE workspace_id=$1::uuid AND fleet_id=$2::uuid AND agent_id=$3::uuid`, fixture.workspaceID, fixture.fleetID, fixture.agentID); err != nil {
		t.Fatal(err)
	}
	frozen, err := store.TaskContextForAttempt(ctx, attempt.ID, fixture.workspaceID)
	if err != nil {
		t.Fatalf("TaskContextForAttempt: %v", err)
	}
	if frozen.Run.Title != "Representation bytes" || frozen.Run.Goal != "Representation bytes" {
		t.Fatalf("task-bound Run used live mutation: title=%q goal=%q", frozen.Run.Title, frozen.Run.Goal)
	}
	if len(frozen.PrincipalHeader) == 0 || frozen.PrincipalHeader[0].Status == "archived" {
		t.Fatalf("task-bound principal header used live roster: %+v", frozen.PrincipalHeader)
	}
}

func TestDispatchFailsWhenPassportLifecycleWithdrawnBeforeDispatch(t *testing.T) {
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
		LeadAgentID: fixture.agentID, Goal: "Withdrawn lifecycle gate", Title: "Withdrawn lifecycle",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID, run.WorkspaceID)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	claimID := uuid.NewString()
	seedIntegrationClaimArtifact(t, ctx, pool, fixture.workspaceID, run.SessionID, claimID, "withdraw-dispatch-claim", "withdrawn before dispatch")
	if _, err = (artifactLifecycleModule{store: store}).Change(ctx, artifactLifecycleChange{
		OperationID: uuid.NewString(), WorkspaceID: fixture.workspaceID, SessionID: run.SessionID,
		ArtifactID: claimID, Kind: artifactLifecycleWithdraw, Reason: "withdrawn before dispatch",
	}); err != nil {
		t.Fatalf("withdraw before dispatch: %v", err)
	}

	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	attempt, _, err := store.CreateDispatchIntent(ctx, input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent with withdrawn omission should succeed: %v", err)
	}
	var included bool
	if err = pool.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1
		  FROM research_artifact_context_entry e
		  JOIN research_artifact_context_manifest m
		    ON m.workspace_id = e.workspace_id
		   AND m.session_id = e.session_id
		   AND m.id = e.manifest_id
		  JOIN research_artifact_version v
		    ON v.workspace_id = e.workspace_id
		   AND v.session_id = e.session_id
		   AND v.id = e.artifact_version_id
		  WHERE m.attempt_id = $1::uuid AND v.artifact_id = $2::uuid
		)
	`, attempt.ID, claimID).Scan(&included); err != nil {
		t.Fatal(err)
	}
	if included {
		t.Fatal("withdrawn claim must not appear in manifest entries")
	}
	var versions, lifecycleEvents, mutations int
	if err = pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*)::int FROM research_artifact_version
		   WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND artifact_id=$3::uuid),
		  (SELECT count(*)::int FROM research_artifact_lifecycle_event
		   WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND artifact_id=$3::uuid),
		  (SELECT count(*)::int FROM research_artifact_policy_mutation
		   WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND artifact_id=$3::uuid
		     AND mutation_kind='lifecycle')
	`, fixture.workspaceID, run.SessionID, claimID).Scan(&versions, &lifecycleEvents, &mutations); err != nil {
		t.Fatal(err)
	}
	if versions != 1 || lifecycleEvents != 1 || mutations != 1 {
		t.Fatalf("withdrawal audit versions=%d events=%d mutations=%d want 1/1/1", versions, lifecycleEvents, mutations)
	}
}
