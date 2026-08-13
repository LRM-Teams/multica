package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestHashManifestEntriesDeterministic(t *testing.T) {
	entries := []artifactVersionCandidate{
		{
			ArtifactID:          "30000000-0000-4000-8000-000000000001",
			Version:             1,
			EligibilityRevision: 1,
			Representation:      "full",
			RepresentationHash:  "sha256:aaaa",
		},
		{
			ArtifactID:          "30000000-0000-4000-8000-000000000002",
			Version:             1,
			EligibilityRevision: 1,
			Representation:      "full",
			RepresentationHash:  "sha256:bbbb",
		},
	}
	first := hashManifestEntries(entries)
	second := hashManifestEntries([]artifactVersionCandidate{entries[1], entries[0]})
	if first != second {
		t.Fatalf("hash=%q want=%q", first, second)
	}
}

func TestDispatchManifestHashBindsAuthorizationScope(t *testing.T) {
	base := dispatchManifestHashInput{
		WorkspaceID:         "11111111-1111-1111-1111-111111111111",
		SessionID:           "22222222-2222-2222-2222-222222222222",
		AttemptID:           "33333333-3333-3333-3333-333333333333",
		TaskID:              "44444444-4444-4444-4444-444444444444",
		Purpose:             ArtifactPurposeTaskExecution,
		PolicyVersion:       LegacyV1V5CompatPolicy,
		PolicyWatermark:     7,
		ThroughStateVersion: 11,
		Entries: []artifactVersionCandidate{{
			VersionRowID:         "55555555-5555-5555-5555-555555555555",
			ArtifactID:           "66666666-6666-6666-6666-666666666666",
			Kind:                 ArtifactKindClaim,
			Version:              2,
			EligibilityRevision:  3,
			AccessLevel:          ArtifactAccessRaw,
			Lifecycle:            ArtifactLifecycleRegistered,
			Provenance:           ArtifactProvenanceComplete,
			VersionCount:         2,
			InputReferenceCount:  4,
			OutputReferenceCount: 5,
			Representation:       "full",
			RepresentationHash:   "sha256:representation",
		}},
	}
	want := hashDispatchManifest(base)

	tests := []struct {
		name   string
		mutate func(*dispatchManifestHashInput)
	}{
		{"workspace", func(in *dispatchManifestHashInput) { in.WorkspaceID = "different" }},
		{"session", func(in *dispatchManifestHashInput) { in.SessionID = "different" }},
		{"attempt", func(in *dispatchManifestHashInput) { in.AttemptID = "different" }},
		{"task", func(in *dispatchManifestHashInput) { in.TaskID = "different" }},
		{"purpose", func(in *dispatchManifestHashInput) { in.Purpose = ArtifactPurposeEvaluation }},
		{"policy version", func(in *dispatchManifestHashInput) { in.PolicyVersion = "different" }},
		{"policy watermark", func(in *dispatchManifestHashInput) { in.PolicyWatermark++ }},
		{"state watermark", func(in *dispatchManifestHashInput) { in.ThroughStateVersion++ }},
		{"version row", func(in *dispatchManifestHashInput) { in.Entries[0].VersionRowID = "different" }},
		{"selected version", func(in *dispatchManifestHashInput) { in.Entries[0].Version++ }},
		{"access", func(in *dispatchManifestHashInput) { in.Entries[0].AccessLevel = ArtifactAccessRedacted }},
		{"lifecycle", func(in *dispatchManifestHashInput) { in.Entries[0].Lifecycle = ArtifactLifecycleAccepted }},
		{"provenance", func(in *dispatchManifestHashInput) { in.Entries[0].Provenance = ArtifactProvenancePartial }},
		{"version count", func(in *dispatchManifestHashInput) { in.Entries[0].VersionCount++ }},
		{"input count", func(in *dispatchManifestHashInput) { in.Entries[0].InputReferenceCount++ }},
		{"output count", func(in *dispatchManifestHashInput) { in.Entries[0].OutputReferenceCount++ }},
		{"representation", func(in *dispatchManifestHashInput) { in.Entries[0].RepresentationHash = "different" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			changed := base
			changed.Entries = append([]artifactVersionCandidate(nil), base.Entries...)
			tc.mutate(&changed)
			if got := hashDispatchManifest(changed); got == want {
				t.Fatalf("hash did not bind %s", tc.name)
			}
		})
	}
}

func TestDispatchManifestHashCanonicalizesEntryOrder(t *testing.T) {
	first := artifactVersionCandidate{VersionRowID: "v1", ArtifactID: "b", Kind: ArtifactKindClaim, Version: 1, EligibilityRevision: 1, Representation: "full", RepresentationHash: "h1"}
	second := artifactVersionCandidate{VersionRowID: "v2", ArtifactID: "a", Kind: ArtifactKindObservation, Version: 1, EligibilityRevision: 1, Representation: "full", RepresentationHash: "h2"}
	base := dispatchManifestHashInput{WorkspaceID: "w", SessionID: "s", AttemptID: "a", TaskID: "t", Purpose: ArtifactPurposeTaskExecution, PolicyVersion: "p", PolicyWatermark: 1, ThroughStateVersion: 2, Entries: []artifactVersionCandidate{first, second}}
	reversed := base
	reversed.Entries = []artifactVersionCandidate{second, first}
	if hashDispatchManifest(base) != hashDispatchManifest(reversed) {
		t.Fatal("manifest hash must use canonical entry order")
	}
}

func TestVerifyManifestPromptShadow(t *testing.T) {
	live := RunSnapshot{
		Sources:      []SourceSnapshotView{{ID: "s1"}, {ID: "s2"}},
		Observations: []Observation{{ID: "o1"}},
		Claims:       []Claim{{ID: "c1"}},
	}
	filtered := filterRunSnapshotByManifest(live, map[string]struct{}{"s1": {}, "o1": {}, "c1": {}})
	livePrompt := "ledger: 2 source snapshots, 1 observations, 1 claims"
	manifestPrompt := "ledger: 1 source snapshots, 1 observations, 1 claims"
	if err := verifyManifestPromptShadow(livePrompt, manifestPrompt, live, filtered); err != nil {
		t.Fatalf("expected filter delta to allow prompt change: %v", err)
	}
	if err := verifyManifestPromptShadow("same", "different", live, live); err == nil {
		t.Fatal("expected prompt shadow mismatch")
	}
}

func TestLegacyManifestVisibleArtifactIDsMatchPlan(t *testing.T) {
	module := NewArtifactContextModule()
	candidates := []artifactVersionCandidate{
		{
			ArtifactID:  "30000000-0000-4000-8000-000000000001",
			Kind:        ArtifactKindClaim,
			Lifecycle:   ArtifactLifecycleRegistered,
			Provenance:  ArtifactProvenancePartial,
			AccessLevel: ArtifactAccessRaw,
		},
		{
			ArtifactID:  "30000000-0000-4000-8000-000000000002",
			Kind:        ArtifactKindSourceSnapshot,
			Lifecycle:   ArtifactLifecycleWithdrawn,
			Provenance:  ArtifactProvenancePartial,
			AccessLevel: ArtifactAccessRaw,
		},
	}
	clearance := defaultTaskExecutionClearance()
	purpose := manifestPurposeForTask()
	liveIDs := make(map[string]struct{})
	for _, candidate := range candidates {
		admitted, _ := module.policy.LegacyAdmissionAllowed(
			candidate.Kind, candidate.Lifecycle, candidate.Provenance,
		)
		if !admitted {
			continue
		}
		allowed, _ := module.policy.CanReadNormal(
			clearance, candidate.AccessLevel, purpose, false,
		)
		if !allowed {
			continue
		}
		liveIDs[candidate.ArtifactID] = struct{}{}
	}
	if _, ok := liveIDs["30000000-0000-4000-8000-000000000001"]; !ok {
		t.Fatal("expected claim in legacy visible set")
	}
	if _, ok := liveIDs["30000000-0000-4000-8000-000000000002"]; ok {
		t.Fatal("withdrawn artifact must not appear in legacy visible set")
	}
}

func TestFilterRunSnapshotByManifest(t *testing.T) {
	allowed := map[string]struct{}{
		"claim-1": {},
	}
	snapshot := RunSnapshot{
		Sources:      []SourceSnapshotView{{ID: "source-1"}, {ID: "source-2"}},
		Observations: []Observation{{ID: "obs-1"}},
		Claims:       []Claim{{ID: "claim-1"}, {ID: "claim-2"}},
	}
	filtered := filterRunSnapshotByManifest(snapshot, allowed)
	if len(filtered.Sources) != 0 || len(filtered.Observations) != 0 || len(filtered.Claims) != 1 {
		t.Fatalf("filtered=%+v", filtered)
	}
	if filtered.Claims[0].ID != "claim-1" {
		t.Fatalf("claim=%q", filtered.Claims[0].ID)
	}
}

func TestFilterRunSnapshotByEmptyManifestFailsClosed(t *testing.T) {
	snapshot := RunSnapshot{
		Sources:      []SourceSnapshotView{{ID: "source"}},
		Observations: []Observation{{ID: "observation"}},
		Claims:       []Claim{{ID: "claim"}},
	}
	filtered := filterRunSnapshotByManifest(snapshot, map[string]struct{}{})
	if len(filtered.Sources) != 0 || len(filtered.Observations) != 0 || len(filtered.Claims) != 0 {
		t.Fatalf("empty manifest exposed evidence: %+v", filtered)
	}
}

func TestCompareShadowManifestError(t *testing.T) {
	live := map[string]struct{}{"a": {}, "b": {}}
	manifest := manifestArtifactSet{
		ArtifactIDs: map[string]struct{}{"a": {}},
		Hash:        "sha256:abc",
	}
	if err := compareShadowManifestError(live, manifest); err == nil {
		t.Fatal("expected shadow mismatch error")
	}
	if err := compareShadowManifestError(live, manifestArtifactSet{ArtifactIDs: live, Hash: "sha256:abc"}); err != nil {
		t.Fatalf("expected match: %v", err)
	}
}

func TestNewArtifactContextModule(t *testing.T) {
	module := NewArtifactContextModule()
	if module.policy.ManifestOmissionReason(ArtifactDenyLifecycle) != "lifecycle" {
		t.Fatal("expected lifecycle omission reason")
	}
}

func TestCreateDispatchIntentPersistsManifestBoundOutbox(t *testing.T) {
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
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, fixture.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, fixture.userID)
	}()
	store := NewPostgresStore(pool)
	run, _, err := store.CreateRun(ctx, StartInput{
		WorkspaceID: fixture.workspaceID,
		FleetID:     fixture.fleetID,
		CreatedBy:   fixture.userID,
		LeadAgentID: fixture.agentID,
		Goal:        "Artifact passport dispatch binding",
		Title:       "Passport dispatch",
		DepthTier:   "standard",
		Language:    "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	var passportEnabled bool
	if err = pool.QueryRow(ctx, `
		SELECT artifact_passport_enabled FROM research_session WHERE id = $1::uuid
	`, run.SessionID).Scan(&passportEnabled); err != nil {
		t.Fatalf("load passport flag: %v", err)
	}
	if !passportEnabled {
		t.Fatal("expected artifact_passport_enabled after initialization")
	}
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	attempt, _, err := store.CreateDispatchIntent(ctx, input)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			t.Fatalf("CreateDispatchIntent: %v (constraint=%s detail=%s)", err, pgErr.ConstraintName, pgErr.Detail)
		}
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	var manifestID, manifestHash, requestHash string
	if err = pool.QueryRow(ctx, `
		SELECT manifest_id::text, manifest_hash, request_hash
		FROM research_dispatch_outbox
		WHERE attempt_id = $1::uuid
	`, attempt.ID).Scan(&manifestID, &manifestHash, &requestHash); err != nil {
		t.Fatalf("load outbox binding: %v", err)
	}
	if manifestID == "" || manifestHash == "" || requestHash == "" {
		t.Fatalf("outbox binding incomplete: manifest_id=%q manifest_hash=%q request_hash=%q", manifestID, manifestHash, requestHash)
	}
	var manifestAttemptID, grantID, grantPrincipalID, grantClearance string
	var grantRevision int64
	if err = pool.QueryRow(ctx, `
		SELECT m.attempt_id::text, g.id::text, g.principal_id::text,
		       g.normal_clearance, m.normal_grant_revision
		FROM research_artifact_context_manifest m
		JOIN research_artifact_policy_grant g
		  ON g.workspace_id = m.workspace_id
		 AND g.session_id = m.session_id
		 AND g.id = m.normal_grant_id
		WHERE m.id = $1::uuid
	`, manifestID).Scan(
		&manifestAttemptID, &grantID, &grantPrincipalID, &grantClearance, &grantRevision,
	); err != nil {
		t.Fatalf("load manifest row: %v", err)
	}
	if manifestAttemptID != attempt.ID {
		t.Fatalf("manifest attempt=%q want=%q", manifestAttemptID, attempt.ID)
	}
	if grantID == "" || grantPrincipalID != fixture.agentID || grantClearance != "raw" || grantRevision != 1 {
		t.Fatalf("manifest grant id=%q principal=%q clearance=%q revision=%d",
			grantID, grantPrincipalID, grantClearance, grantRevision)
	}
}

func TestDispatchOutboxPromptMatchesManifestBoundRequestHash(t *testing.T) {
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
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, fixture.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, fixture.userID)
	}()
	store := NewPostgresStore(pool)
	run, _, err := store.CreateRun(ctx, StartInput{
		WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID,
		LeadAgentID: fixture.agentID, Goal: "Outbox prompt binding", Title: "Outbox prompt",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	attempt, _, err := store.CreateDispatchIntent(ctx, input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	var payload []byte
	var storedHash string
	if err = pool.QueryRow(ctx, `
		SELECT request_payload, request_hash
		FROM research_dispatch_outbox
		WHERE attempt_id = $1::uuid
	`, attempt.ID).Scan(&payload, &storedHash); err != nil {
		t.Fatalf("load outbox payload: %v", err)
	}
	var request DispatchRequest
	if err = json.Unmarshal(payload, &request); err != nil {
		t.Fatalf("decode outbox payload: %v", err)
	}
	recomputed, err := HashDispatchRequest(request)
	if err != nil {
		t.Fatalf("HashDispatchRequest: %v", err)
	}
	if recomputed != storedHash {
		t.Fatalf("stored hash=%q recomputed=%q", storedHash, recomputed)
	}
	if request.Prompt == "" {
		t.Fatal("expected manifest-bound outbox prompt")
	}
	if request.Prompt == "test dispatch" {
		t.Fatal("expected manifest rebound to replace placeholder test dispatch prompt")
	}
}

func TestTaskContextForAttemptExcludesPostDispatchArtifacts(t *testing.T) {
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
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, fixture.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, fixture.userID)
	}()
	store := NewPostgresStore(pool)
	run, _, err := store.CreateRun(ctx, StartInput{
		WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID,
		LeadAgentID: fixture.agentID, Goal: "Frozen manifest reads", Title: "Frozen reads",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	attempt, _, err := store.CreateDispatchIntent(ctx, input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	liveBefore, err := store.TaskContext(ctx, tasks[0].ID, fixture.workspaceID)
	if err != nil {
		t.Fatalf("TaskContext before: %v", err)
	}
	sourceID := uuid.NewString()
	execIntegrationDomainInsert(t, ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, insertErr := tx.Exec(ctx, `
			INSERT INTO research_source_snapshot (
			  id, workspace_id, session_id, canonical_url, title, publisher, source_class,
			  evidence_traits, independence_key, retrieved_at, content_hash, snapshot_text, metadata,
			  verification_status
			) VALUES (
			  $1::uuid, $2::uuid, $3::uuid, 'https://example.test/late', 'Late source', 'example.test',
			  'primary', '{}'::text[], 'example.test', now(), 'sha256:late', 'late snapshot', '{}'::jsonb,
			  'verified'
			)
		`, sourceID, fixture.workspaceID, run.SessionID); insertErr != nil {
			return insertErr
		}
		backfillIntegrationArtifactPassport(t, ctx, tx, fixture.workspaceID, run.SessionID, sourceID, string(ArtifactKindSourceSnapshot), nil, nil)
		return nil
	})

	liveAfter, err := store.TaskContext(ctx, tasks[0].ID, fixture.workspaceID)
	if err != nil {
		t.Fatalf("TaskContext after: %v", err)
	}
	frozen, err := store.TaskContextForAttempt(ctx, attempt.ID, fixture.workspaceID)
	if err != nil {
		t.Fatalf("TaskContextForAttempt: %v", err)
	}
	if len(liveAfter.Sources) != len(liveBefore.Sources)+1 {
		t.Fatalf("live sources before=%d after=%d", len(liveBefore.Sources), len(liveAfter.Sources))
	}
	for _, source := range frozen.Sources {
		if source.ID == sourceID {
			t.Fatal("post-dispatch source leaked into manifest-filtered snapshot")
		}
	}
	if frozen.AttemptContext == nil || !frozen.AttemptContext.ManifestFiltered {
		t.Fatalf("attempt context=%+v", frozen.AttemptContext)
	}
}

func TestAcceptResultRequiresDispatchManifest(t *testing.T) {
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
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, fixture.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, fixture.userID)
	}()
	store := NewPostgresStore(pool)
	run, _, err := store.CreateRun(ctx, StartInput{
		WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID,
		LeadAgentID: fixture.agentID, Goal: "Acceptance manifest gate", Title: "Accept gate",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	attemptID := uuid.NewString()
	inboxID := uuid.NewString()
	if _, err = pool.Exec(ctx, `
		INSERT INTO agent_inbox_event (id, workspace_id, agent_id, reason, status)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'dm', 'draining')
	`, inboxID, fixture.workspaceID, fixture.agentID); err != nil {
		t.Fatalf("insert inbox event: %v", err)
	}
	execIntegrationDomainInsert(t, ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, insertErr := tx.Exec(ctx, `
			INSERT INTO research_task_attempt (
			  id, workspace_id, session_id, task_id, attempt_number, assigned_agent_id,
			  execution_adapter, provider, model, target_config_fingerprint,
			  agent_config_fingerprint, runtime_config_fingerprint, provider_config_fingerprint,
			  dispatch_key, status, inbox_task_id
			) VALUES (
			  $1::uuid, $2::uuid, $3::uuid, $4::uuid, 1, $5::uuid,
			  'agent_inbox', 'openai', 'test-model', 'cfg', 'cfg', 'cfg', 'cfg',
			  'research-test:missing-manifest', 'running', $6::uuid
			)
		`, attemptID, fixture.workspaceID, run.SessionID, tasks[0].ID, fixture.agentID, inboxID); insertErr != nil {
			return insertErr
		}
		backfillIntegrationArtifactPassport(t, ctx, tx, fixture.workspaceID, run.SessionID, attemptID, string(ArtifactKindAttempt), nil, nil)
		return nil
	})
	raw, err := json.Marshal(validPlanResult(t))
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AcceptResult(ctx, AcceptResultInput{
		SessionID: run.SessionID, AttemptID: attemptID, AgentID: fixture.agentID,
		InboxTaskID: inboxID, Raw: raw,
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("AcceptResult err=%v want ErrInvalidTransition", err)
	}
}
