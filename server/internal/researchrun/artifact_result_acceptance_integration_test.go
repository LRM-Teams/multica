package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAcceptResultRejectsWhenManifestPolicyWatermarkAhead(t *testing.T) {
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
	if _, err = pool.Exec(ctx, `
		UPDATE research_artifact_context_manifest
		SET policy_watermark = policy_watermark + 5
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
	`, fixture.workspaceID, run.SessionID, attempt.ID); err != nil {
		t.Fatalf("inflate manifest watermark: %v", err)
	}

	result, hash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, task, run.Config)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AcceptResult(ctx, AcceptResultInput{
		SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
		InboxTaskID: inboxID, Raw: raw, Result: result, Hash: hash,
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("AcceptResult err=%v want ErrInvalidTransition", err)
	}
}

func TestAcceptResultAcceptsAfterUnrelatedPolicyWatermarkAdvance(t *testing.T) {
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
	var manifestWatermark int64
	if err = pool.QueryRow(ctx, `
		SELECT policy_watermark
		FROM research_artifact_context_manifest
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
	`, fixture.workspaceID, run.SessionID, attempt.ID).Scan(&manifestWatermark); err != nil {
		t.Fatalf("load dispatch manifest watermark: %v", err)
	}
	if _, err = pool.Exec(ctx, `
		UPDATE research_artifact_policy_state
		SET watermark = watermark + 1, updated_at = now()
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
	`, fixture.workspaceID, run.SessionID); err != nil {
		t.Fatalf("advance unrelated policy watermark: %v", err)
	}
	var advancedWatermark int64
	if err = pool.QueryRow(ctx, `
		SELECT watermark
		FROM research_artifact_policy_state
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
	`, fixture.workspaceID, run.SessionID).Scan(&advancedWatermark); err != nil {
		t.Fatalf("load unrelated advanced watermark: %v", err)
	}
	if advancedWatermark <= manifestWatermark {
		t.Fatalf("unrelated advance watermark=%d want > manifest=%d", advancedWatermark, manifestWatermark)
	}

	result, hash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, task, run.Config)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := store.AcceptResult(ctx, AcceptResultInput{
		SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
		InboxTaskID: inboxID, Raw: raw, Result: result, Hash: hash,
	})
	if err != nil {
		t.Fatalf("AcceptResult: %v", err)
	}
	if outcome.TaskID != task.ID {
		t.Fatalf("outcome=%+v", outcome)
	}
	var resultArtifacts int
	var acceptanceWatermark int64
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int, max(acceptance_policy_watermark)
		FROM research_result_artifact
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
	`, fixture.workspaceID, run.SessionID, attempt.ID).Scan(
		&resultArtifacts,
		&acceptanceWatermark,
	); err != nil {
		t.Fatal(err)
	}
	if resultArtifacts != 1 {
		t.Fatalf("result artifacts=%d want 1", resultArtifacts)
	}
	var finalWatermark int64
	if err = pool.QueryRow(ctx, `
		SELECT watermark
		FROM research_artifact_policy_state
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
	`, fixture.workspaceID, run.SessionID).Scan(&finalWatermark); err != nil {
		t.Fatalf("load final acceptance watermark: %v", err)
	}
	if acceptanceWatermark != finalWatermark || acceptanceWatermark != advancedWatermark+1 {
		t.Fatalf(
			"acceptance watermark=%d final=%d advanced=%d want acceptance=final=advanced+1",
			acceptanceWatermark,
			finalWatermark,
			advancedWatermark,
		)
	}
}

func TestAcceptResultRejectsWhenDispatchAuthorizationIsNoLongerActive(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	for _, tc := range []struct {
		name   string
		mutate func(context.Context, *testing.T, *pgxpool.Pool, researchRunFixture, Run, Attempt)
	}{
		{
			name: "fleet membership archived",
			mutate: func(ctx context.Context, t *testing.T, pool *pgxpool.Pool, fixture researchRunFixture, run Run, _ Attempt) {
				t.Helper()
				if _, err := pool.Exec(ctx, `
					UPDATE research_fleet_member SET status = 'archived', updated_at = now()
					WHERE workspace_id = $1::uuid AND fleet_id = $2::uuid AND agent_id = $3::uuid
				`, fixture.workspaceID, fixture.fleetID, fixture.agentID); err != nil {
					t.Fatalf("archive fleet membership: %v", err)
				}
			},
		},
		{
			name: "manifest grant revoked",
			mutate: func(ctx context.Context, t *testing.T, pool *pgxpool.Pool, fixture researchRunFixture, run Run, attempt Attempt) {
				t.Helper()
				tx, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(ctx)
				var grantID string
				var oldRevision, watermark int64
				if err = tx.QueryRow(ctx, `
					SELECT normal_grant_id::text, normal_grant_revision
					FROM research_artifact_context_manifest
					WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
				`, fixture.workspaceID, run.SessionID, attempt.ID).Scan(&grantID, &oldRevision); err != nil {
					t.Fatalf("load manifest grant: %v", err)
				}
				if err = tx.QueryRow(ctx, `
					UPDATE research_artifact_policy_state
					SET watermark = watermark + 1, updated_at = now()
					WHERE workspace_id = $1::uuid AND session_id = $2::uuid
					RETURNING watermark
				`, fixture.workspaceID, run.SessionID).Scan(&watermark); err != nil {
					t.Fatalf("reserve revocation watermark: %v", err)
				}
				if _, err = tx.Exec(ctx, `
					UPDATE research_artifact_policy_grant
					SET status = 'revoked', revision = revision + 1, revoked_at = now()
					WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
				`, fixture.workspaceID, run.SessionID, grantID); err != nil {
					t.Fatalf("revoke grant: %v", err)
				}
				if _, err = tx.Exec(ctx, `
					INSERT INTO research_artifact_policy_mutation (
					  workspace_id, session_id, watermark, mutation_kind, policy_grant_id,
					  old_grant_revision, new_grant_revision, old_grant_status, new_grant_status
					) VALUES ($1::uuid, $2::uuid, $3, 'grant_revoke', $4::uuid, $5, $6, 'active', 'revoked')
				`, fixture.workspaceID, run.SessionID, watermark, grantID, oldRevision, oldRevision+1); err != nil {
					t.Fatalf("record grant revocation: %v", err)
				}
				if err = tx.Commit(ctx); err != nil {
					t.Fatalf("commit grant revocation: %v", err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			tc.mutate(ctx, t, pool, fixture, run, attempt)
			result, hash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, task, run.Config)
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.AcceptResult(ctx, AcceptResultInput{
				SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
				InboxTaskID: inboxID, Raw: raw, Result: result, Hash: hash,
			})
			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("AcceptResult err=%v want ErrInvalidTransition", err)
			}
		})
	}
}

func TestAcceptResultReplayRequiresMatchingHash(t *testing.T) {
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
	input := AcceptResultInput{
		SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
		InboxTaskID: inboxID, Raw: raw, Result: result, Hash: hash,
	}
	if _, err = store.AcceptResult(ctx, input); err != nil {
		t.Fatalf("first AcceptResult: %v", err)
	}
	replayed, err := store.AcceptResult(ctx, input)
	if err != nil || !replayed.Replayed {
		t.Fatalf("replay err=%v outcome=%+v", err, replayed)
	}
	changed := result
	if changed.Plan != nil && len(changed.Plan.Tasks) > 0 {
		changed.Plan.Tasks[0].Objective = "changed objective for conflict test"
	}
	changedRaw, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	_, changedHash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, changedRaw, task, run.Config)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AcceptResult(ctx, AcceptResultInput{
		SessionID: input.SessionID, AttemptID: input.AttemptID, AgentID: input.AgentID,
		InboxTaskID: input.InboxTaskID, Raw: changedRaw, Result: changed, Hash: changedHash,
	})
	if !errors.Is(err, ErrResultConflict) {
		t.Fatalf("changed replay err=%v want ErrResultConflict", err)
	}
}

func TestAcceptResultReplayRequiresExactArtifactBinding(t *testing.T) {
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

	tests := []struct {
		name   string
		mutate func(*testing.T, *AcceptResultInput, researchRunFixture)
	}{
		{
			name: "agent lineage",
			mutate: func(_ *testing.T, input *AcceptResultInput, _ researchRunFixture) {
				input.AgentID = uuid.NewString()
			},
		},
		{
			name: "inbox lineage",
			mutate: func(_ *testing.T, input *AcceptResultInput, _ researchRunFixture) {
				input.InboxTaskID = uuid.NewString()
			},
		},
		{
			name: "manifest hash",
			mutate: func(t *testing.T, input *AcceptResultInput, fixture researchRunFixture) {
				if _, err := pool.Exec(ctx, `
					UPDATE research_artifact_context_manifest
					SET manifest_hash = $4
					WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
				`, fixture.workspaceID, input.SessionID, input.AttemptID,
					contentHashFromPayload([]byte("changed replay manifest"))); err != nil {
					t.Fatalf("change manifest hash: %v", err)
				}
			},
		},
		{
			name: "manifest id",
			mutate: func(t *testing.T, input *AcceptResultInput, fixture researchRunFixture) {
				command, err := pool.Exec(ctx, `
					UPDATE research_artifact_input_reference ref
					SET manifest_id = NULL
					FROM research_artifact_version rv, research_result_artifact r
					WHERE rv.workspace_id = r.workspace_id
					  AND rv.session_id = r.session_id
					  AND rv.artifact_id = r.id
					  AND ref.workspace_id = r.workspace_id
					  AND ref.session_id = r.session_id
					  AND ref.consumer_version_id = rv.id
					  AND ref.relation = 'acceptance_input'
					  AND r.workspace_id = $1::uuid AND r.session_id = $2::uuid
					  AND r.attempt_id = $3::uuid
				`, fixture.workspaceID, input.SessionID, input.AttemptID)
				if err != nil {
					t.Fatalf("change input manifest id: %v", err)
				}
				if command.RowsAffected() == 0 {
					t.Fatal("expected at least one accepted input reference")
				}
			},
		},
		{
			name: "resolved version set",
			mutate: func(t *testing.T, input *AcceptResultInput, fixture researchRunFixture) {
				command, err := pool.Exec(ctx, `
					DELETE FROM research_artifact_input_reference ref
					USING research_artifact_version rv, research_result_artifact r
					WHERE rv.workspace_id = r.workspace_id
					  AND rv.session_id = r.session_id
					  AND rv.artifact_id = r.id
					  AND ref.workspace_id = r.workspace_id
					  AND ref.session_id = r.session_id
					  AND ref.consumer_version_id = rv.id
					  AND ref.relation = 'acceptance_input'
					  AND r.workspace_id = $1::uuid AND r.session_id = $2::uuid
					  AND r.attempt_id = $3::uuid
				`, fixture.workspaceID, input.SessionID, input.AttemptID)
				if err != nil {
					t.Fatalf("remove resolved version binding: %v", err)
				}
				if command.RowsAffected() == 0 {
					t.Fatal("expected at least one accepted input reference")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := seedResearchRunFixture(t, ctx, pool)
			defer cleanupResearchRunFixture(pool, fixture)
			store := NewPostgresStore(pool)
			attempt, inboxID, raw, run, task := setupRunningPlanAttempt(t, ctx, store, fixture)
			result, hash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, task, run.Config)
			if err != nil {
				t.Fatal(err)
			}
			input := AcceptResultInput{
				SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
				InboxTaskID: inboxID, Raw: raw, Result: result, Hash: hash,
			}
			if _, err = store.AcceptResult(ctx, input); err != nil {
				t.Fatalf("first AcceptResult: %v", err)
			}
			tc.mutate(t, &input, fixture)
			if _, err = store.AcceptResult(ctx, input); !errors.Is(err, ErrResultConflict) {
				t.Fatalf("replay err=%v want ErrResultConflict", err)
			}
		})
	}
}

func TestAcceptResultRejectsWhenManifestEntryEligibilityAdvances(t *testing.T) {
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

	run, _, err := store.CreateRun(ctx, StartInput{
		WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID,
		LeadAgentID: fixture.agentID, Goal: "Affected eligibility gate", Title: "Affected eligibility",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	claimID := uuid.NewString()
	seedIntegrationClaimArtifact(t, ctx, pool, fixture.workspaceID, run.SessionID, claimID, "accept-eligibility-claim", "claim for accept eligibility")

	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	attempt, _, err := store.CreateDispatchIntent(ctx, input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	inboxID := seedIntegrationInboxEvent(t, ctx, pool, fixture.workspaceID, fixture.agentID)
	if _, _, err = store.AttachInboxTask(ctx, attempt.ID, inboxID); err != nil {
		t.Fatalf("AttachInboxTask: %v", err)
	}
	mutateIntegrationArtifactForCASTest(t, ctx, pool, `
		UPDATE research_artifact_passport
		SET eligibility_revision = eligibility_revision + 1
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
	`, fixture.workspaceID, run.SessionID, claimID)

	raw, err := json.Marshal(upgradeResultToV5(validV4PlanResult(t)))
	if err != nil {
		t.Fatal(err)
	}
	result, hash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, tasks[0], run.Config)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AcceptResult(ctx, AcceptResultInput{
		SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
		InboxTaskID: inboxID, Raw: raw, Result: result, Hash: hash,
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("AcceptResult err=%v want ErrInvalidTransition", err)
	}
	var attemptStatus string
	if err = pool.QueryRow(ctx, `
		SELECT status FROM research_task_attempt WHERE id = $1::uuid
	`, attempt.ID).Scan(&attemptStatus); err != nil {
		t.Fatal(err)
	}
	if attemptStatus != "dispatching" {
		t.Fatalf("attempt status=%q want dispatching after failed accept", attemptStatus)
	}
}

func TestAcceptResultRejectsWhenManifestEntryArtifactWithdrawn(t *testing.T) {
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

	run, _, err := store.CreateRun(ctx, StartInput{
		WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID,
		LeadAgentID: fixture.agentID, Goal: "Withdrawn in-flight accept", Title: "Withdrawn accept",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	claimID := uuid.NewString()
	seedIntegrationClaimArtifact(t, ctx, pool, fixture.workspaceID, run.SessionID, claimID, "accept-withdraw-claim", "claim withdrawn before accept")

	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	attempt, _, err := store.CreateDispatchIntent(ctx, input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	inboxID := seedIntegrationInboxEvent(t, ctx, pool, fixture.workspaceID, fixture.agentID)
	if _, _, err = store.AttachInboxTask(ctx, attempt.ID, inboxID); err != nil {
		t.Fatalf("AttachInboxTask: %v", err)
	}
	if _, err = store.WithdrawArtifact(ctx, WithdrawArtifactInput{
		WorkspaceID: fixture.workspaceID,
		SessionID:   run.SessionID,
		ArtifactID:  claimID,
		ActorType:   "user",
		ActorID:     fixture.userID,
		Reason:      "claim owner withdrew support",
	}); err != nil {
		t.Fatalf("WithdrawArtifact: %v", err)
	}

	raw, err := json.Marshal(upgradeResultToV5(validV4PlanResult(t)))
	if err != nil {
		t.Fatal(err)
	}
	result, hash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, tasks[0], run.Config)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AcceptResult(ctx, AcceptResultInput{
		SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
		InboxTaskID: inboxID, Raw: raw, Result: result, Hash: hash,
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("AcceptResult err=%v want ErrInvalidTransition", err)
	}
}

func TestAcceptResultRejectsWhenManifestEntryRepresentationChanges(t *testing.T) {
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

	run, _, err := store.CreateRun(ctx, StartInput{
		WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID,
		LeadAgentID: fixture.agentID, Goal: "Representation tamper", Title: "Representation tamper",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	claimID := uuid.NewString()
	seedIntegrationClaimArtifact(t, ctx, pool, fixture.workspaceID, run.SessionID, claimID, "accept-repr-claim", "claim for representation tamper")

	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	attempt, _, err := store.CreateDispatchIntent(ctx, input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	inboxID := seedIntegrationInboxEvent(t, ctx, pool, fixture.workspaceID, fixture.agentID)
	if _, _, err = store.AttachInboxTask(ctx, attempt.ID, inboxID); err != nil {
		t.Fatalf("AttachInboxTask: %v", err)
	}
	if _, err = pool.Exec(ctx, `
		UPDATE research_artifact_context_entry e
		SET representation_bytes = convert_to('sha256:tampered-representation', 'UTF8')
		FROM research_artifact_context_manifest m
		WHERE e.manifest_id = m.id
		  AND m.workspace_id = $1::uuid
		  AND m.session_id = $2::uuid
		  AND m.attempt_id = $3::uuid
	`, fixture.workspaceID, run.SessionID, attempt.ID); err != nil {
		t.Fatalf("tamper representation bytes: %v", err)
	}

	raw, err := json.Marshal(upgradeResultToV5(validV4PlanResult(t)))
	if err != nil {
		t.Fatal(err)
	}
	result, hash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, tasks[0], run.Config)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AcceptResult(ctx, AcceptResultInput{
		SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
		InboxTaskID: inboxID, Raw: raw, Result: result, Hash: hash,
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("AcceptResult err=%v want ErrInvalidTransition", err)
	}
}

func TestAcceptResultRejectsWhenManifestHashTampered(t *testing.T) {
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
	if _, err = pool.Exec(ctx, `
		UPDATE research_artifact_context_manifest
		SET manifest_hash = 'sha256:tampered-manifest-hash'
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
	`, fixture.workspaceID, run.SessionID, attempt.ID); err != nil {
		t.Fatalf("tamper manifest hash: %v", err)
	}

	result, hash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, task, run.Config)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AcceptResult(ctx, AcceptResultInput{
		SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
		InboxTaskID: inboxID, Raw: raw, Result: result, Hash: hash,
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("AcceptResult err=%v want ErrInvalidTransition", err)
	}
}

func setupRunningPlanAttempt(
	t *testing.T,
	ctx context.Context,
	store *PostgresStore,
	fixture researchRunFixture,
) (Attempt, string, json.RawMessage, Run, Task) {
	t.Helper()
	run, _, err := store.CreateRun(ctx, StartInput{
		WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID,
		LeadAgentID: fixture.agentID, Goal: "Acceptance integration", Title: "Acceptance",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	task := tasks[0]
	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, task.ID, fixture.agentID)
	attempt, _, err := store.CreateDispatchIntent(ctx, input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	inboxID := seedIntegrationInboxEvent(t, ctx, store.pool, fixture.workspaceID, fixture.agentID)
	if _, _, err = store.AttachInboxTask(ctx, attempt.ID, inboxID); err != nil {
		t.Fatalf("AttachInboxTask: %v", err)
	}
	raw, err := json.Marshal(upgradeResultToV5(validV4PlanResult(t)))
	if err != nil {
		t.Fatal(err)
	}
	return attempt, inboxID, raw, run, task
}
