package researchrun

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type faultingSubmissionStore struct {
	*submissionStoreStub
	fault error
}

func seedV6RecoveryWorkItem(t *testing.T, run *transactionRecoveryRun, status string, expires time.Time) (string, string) {
	t.Helper()
	membershipID, workItemID := uuid.NewString(), uuid.NewString()
	missionHash := "sha256:" + strings.Repeat("1", 64)
	if _, err := run.pool.Exec(run.ctx, `
		INSERT INTO research_team_membership (
		 id,workspace_id,session_id,agent_id,membership_generation,mission_prompt,mission_hash,mission_revision,state
		) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'test',$5,1,'working')
	`, membershipID, run.fixture.workspaceID, run.fixture.sessionID, run.fixture.agentID, missionHash); err != nil {
		t.Fatal(err)
	}
	if _, err := run.pool.Exec(run.ctx, `
		INSERT INTO research_work_item (
		 id,workspace_id,session_id,kind,status,assigned_agent_id,goal_version,idempotency_key,
		 lease_token,lease_expires_at,payload_schema_id,state_version
		) VALUES ($1::uuid,$2::uuid,$3::uuid,'research',$4,$5::uuid,1,$6,$7::uuid,$8,'schema',1)
	`, workItemID, run.fixture.workspaceID, run.fixture.sessionID, status, run.fixture.agentID,
		"test:"+workItemID, uuid.NewString(), expires); err != nil {
		t.Fatal(err)
	}
	return membershipID, workItemID
}

func seedV6RecoveryAttempt(t *testing.T, run *transactionRecoveryRun, membershipID, workItemID string) string {
	t.Helper()
	attemptID, manifestID := uuid.NewString(), uuid.NewString()
	manifestHash, dispatchKey := "sha256:"+strings.Repeat("3", 64), "dispatch:"+uuid.NewString()
	tx, err := run.pool.Begin(run.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(run.ctx)
	if _, err = tx.Exec(run.ctx, `INSERT INTO research_work_item_attempt (
		id,workspace_id,session_id,work_item_id,attempt_number,assigned_agent_id,membership_id,
		dispatch_key,manifest_id,manifest_hash,status,manifest
	) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,$5::uuid,$6::uuid,$7,$8::uuid,$9,'running','{}'::jsonb)`,
		attemptID, run.fixture.workspaceID, run.fixture.sessionID, workItemID, run.fixture.agentID, membershipID, dispatchKey, manifestID, manifestHash); err != nil {
		t.Fatal(err)
	}
	hash, err := ArtifactContentHash(ArtifactKindAttempt, v6WorkAttemptArtifactContent(workItemID, 1, run.fixture.agentID, membershipID, dispatchKey, manifestID, manifestHash))
	if err != nil {
		t.Fatal(err)
	}
	goalVersion := int32(run.goalVersion)
	planVersion := int32(run.planVersion)
	if err = registerArtifactPassportTx(run.ctx, tx, registerArtifactPassportInput{
		WorkspaceID: run.fixture.workspaceID, SessionID: run.fixture.sessionID, EntityID: attemptID,
		Kind: ArtifactKindAttempt, ProvenanceCompleteness: ArtifactProvenanceComplete,
		GoalVersion: &goalVersion, PlanVersion: &planVersion,
		SchemaName: string(ArtifactKindAttempt), SchemaVersion: OrchestratorVersionV6,
		AccessLevel: ArtifactAccessRaw, HashOrigin: ArtifactHashOriginProduction, ContentHash: hash,
	}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(run.ctx); err != nil {
		t.Fatal(err)
	}
	return attemptID
}

func TestClaimV6WorkItemTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpV6WorkItemClaim, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		_, workItemID := seedV6RecoveryWorkItem(t, run, "ready", time.Now().Add(-time.Minute))
		input := ClaimV6WorkItemInput{WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID, WorkItemID: workItemID,
			ExpectedStateVersion: 1, LeaseToken: uuid.NewString(), Now: time.Now(), LeaseDuration: time.Minute}
		invoke := func() error { _, err := run.store.ClaimV6WorkItem(run.ctx, input); return err }
		status := func() string {
			var value string
			if err := run.pool.QueryRow(run.ctx, `SELECT status FROM research_work_item WHERE id=$1::uuid`, workItemID).Scan(&value); err != nil {
				t.Fatal(err)
			}
			return value
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				if got := status(); got != "ready" {
					t.Fatalf("status=%s", got)
				}
			},
			assertCommitted: func() {
				if got := status(); got != "running" {
					t.Fatalf("status=%s", got)
				}
			},
			recover: func() error {
				_, err := run.store.ClaimV6WorkItem(run.ctx, input)
				if errors.Is(err, ErrWorkItemChanged) {
					return nil
				}
				return err
			},
		}
	})
}

func TestCompleteV6WorkItemTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpV6WorkItemComplete, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		_, workItemID := seedV6RecoveryWorkItem(t, run, "ready", time.Now().Add(-time.Minute))
		token := uuid.NewString()
		if _, err := run.store.ClaimV6WorkItem(run.ctx, ClaimV6WorkItemInput{WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
			WorkItemID: workItemID, ExpectedStateVersion: 1, LeaseToken: token, Now: time.Now(), LeaseDuration: time.Minute}); err != nil {
			t.Fatal(err)
		}
		input := CompleteV6WorkItemInput{WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID, WorkItemID: workItemID, ExpectedStateVersion: 2, LeaseToken: token}
		invoke := func() error { _, err := run.store.CompleteV6WorkItem(run.ctx, input); return err }
		status := func() string {
			var value string
			if err := run.pool.QueryRow(run.ctx, `SELECT status FROM research_work_item WHERE id=$1::uuid`, workItemID).Scan(&value); err != nil {
				t.Fatal(err)
			}
			return value
		}
		return transactionRecoveryOperation{invoke: invoke,
			assertRolledBack: func() {
				if got := status(); got != "running" {
					t.Fatalf("status=%s", got)
				}
			},
			assertCommitted: func() {
				if got := status(); got != "succeeded" {
					t.Fatalf("status=%s", got)
				}
			},
			recover: func() error {
				_, err := run.store.CompleteV6WorkItem(run.ctx, input)
				if errors.Is(err, ErrWorkItemLeaseLost) {
					return nil
				}
				return err
			},
		}
	})
}

func TestRecoverV6WorkItemTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpV6WorkItemRecover, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
			t.Fatal(err)
		}
		membershipID, workItemID := seedV6RecoveryWorkItem(t, run, "running", time.Now().Add(-time.Minute))
		attemptID := seedV6RecoveryAttempt(t, run, membershipID, workItemID)
		inboxTaskID := uuid.NewString()
		if _, err := run.pool.Exec(run.ctx, `
			INSERT INTO agent_inbox_event (
			 id,workspace_id,agent_id,reason,requires_wake,status,seq_from,seq_to
			) VALUES ($1::uuid,$2::uuid,$3::uuid,'quick_create',true,'draining',0,0)
		`, inboxTaskID, run.fixture.workspaceID, run.fixture.agentID); err != nil {
			t.Fatal(err)
		}
		if _, err := run.pool.Exec(run.ctx, `UPDATE research_work_item_attempt SET inbox_task_id=$2::uuid WHERE id=$1::uuid`, attemptID, inboxTaskID); err != nil {
			t.Fatal(err)
		}
		invoke := func() error { _, err := run.store.RecoverExpiredV6WorkItems(run.ctx, 10); return err }
		status := func() string {
			var value string
			if err := run.pool.QueryRow(run.ctx, `SELECT status FROM research_work_item WHERE id=$1::uuid`, workItemID).Scan(&value); err != nil {
				t.Fatal(err)
			}
			return value
		}
		return transactionRecoveryOperation{invoke: invoke,
			assertRolledBack: func() {
				if got := status(); got != "running" {
					t.Fatalf("status=%s", got)
				}
				if ids, listErr := run.store.ListLostV6InboxTaskIDs(run.ctx, 10); listErr != nil || len(ids) != 0 {
					t.Fatalf("lost Inbox tasks=%v err=%v, want none", ids, listErr)
				}
			},
			assertCommitted: func() {
				if got := status(); got != "ready" {
					t.Fatalf("status=%s", got)
				}
				ids, listErr := run.store.ListLostV6InboxTaskIDs(run.ctx, 10)
				if listErr != nil || len(ids) != 1 || ids[0] != inboxTaskID {
					t.Fatalf("lost Inbox tasks=%v err=%v, want %s", ids, listErr, inboxTaskID)
				}
			},
			recover: invoke,
		}
	})
}

type lostV6InboxTaskStoreStub struct {
	ids []string
	err error
}

func (s lostV6InboxTaskStoreStub) ListLostV6InboxTaskIDs(context.Context, int) ([]string, error) {
	return s.ids, s.err
}

type v6InboxCancellerStub struct {
	ids    []string
	reason string
	err    error
}

func (s *v6InboxCancellerStub) Cancel(_ context.Context, ids []string, reason string) error {
	s.ids, s.reason = append([]string(nil), ids...), reason
	return s.err
}

func TestCancelLostV6InboxTasksUsesSharedCancellationPath(t *testing.T) {
	canceller := &v6InboxCancellerStub{}
	count, err := cancelLostV6InboxTasks(context.Background(), lostV6InboxTaskStoreStub{ids: []string{"inbox-1", "inbox-2"}}, canceller, 10)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || len(canceller.ids) != 2 || canceller.ids[0] != "inbox-1" || canceller.ids[1] != "inbox-2" {
		t.Fatalf("cancelled=%d ids=%v", count, canceller.ids)
	}
	if canceller.reason != "research_v6_attempt_lease_expired" {
		t.Fatalf("cancel reason=%q", canceller.reason)
	}
}

func TestRecordV6SubmissionTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpV6SubmissionRecord, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		membershipID, workItemID := seedV6RecoveryWorkItem(t, run, "running", time.Now().Add(time.Minute))
		attemptID, requestID := seedV6RecoveryAttempt(t, run, membershipID, workItemID), uuid.NewString()
		raw := withValidV6SelfHash(t, readV6Fixture(t, filepath.Join("..", "..", "..", "docs", "research", "fixtures", "atomic-result-v6.example.json")), "content_hash")
		decoded, err := DecodeV6Contract(raw, V6ContractAtomicResultSubmission, acceptingV6SecondStage{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = run.pool.Exec(context.Background(), `DELETE FROM research_v6_work_submission WHERE workspace_id=$1::uuid`, run.fixture.workspaceID)
		})
		access := V6AttemptAccess{WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID, WorkItemID: workItemID, AttemptID: attemptID, AgentID: run.fixture.agentID}
		invoke := func() error { _, err := run.store.RecordV6Submission(run.ctx, access, decoded, requestID); return err }
		count := func() int {
			var value int
			if err := run.pool.QueryRow(run.ctx, `SELECT count(*)::int FROM research_v6_work_submission WHERE client_request_id=$1::uuid`, requestID).Scan(&value); err != nil {
				t.Fatal(err)
			}
			return value
		}
		return transactionRecoveryOperation{invoke: invoke,
			assertRolledBack: func() {
				if got := count(); got != 0 {
					t.Fatalf("count=%d", got)
				}
			},
			assertCommitted: func() {
				if got := count(); got != 1 {
					t.Fatalf("count=%d", got)
				}
			},
			recover: invoke,
		}
	})
}

func TestAcknowledgeV6CatalogTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpV6CatalogAcknowledge, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		membershipID, workItemID := seedV6RecoveryWorkItem(t, run, "running", time.Now().Add(time.Minute))
		attemptID := seedV6RecoveryAttempt(t, run, membershipID, workItemID)
		pageHash := "sha256:" + strings.Repeat("4", 64)
		if _, err := run.pool.Exec(run.ctx, `INSERT INTO research_work_catalog_page (
			workspace_id,session_id,work_item_attempt_id,catalog_view,tier,through_event_sequence,page_key,ordinal,content_hash,page
		) VALUES ($1::uuid,$2::uuid,$3::uuid,'same_tier','S',0,'same-tier:0:0',0,$4,'[]'::jsonb)`,
			run.fixture.workspaceID, run.fixture.sessionID, attemptID, pageHash); err != nil {
			t.Fatal(err)
		}
		input := AcknowledgeV6CatalogInput{V6AttemptAccess: V6AttemptAccess{WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
			WorkItemID: workItemID, AttemptID: attemptID, AgentID: run.fixture.agentID}, ClientRequestID: uuid.NewString(), PageKey: "same-tier:0:0", PageHash: pageHash}
		invoke := func() error { return run.store.AcknowledgeV6WorkCatalog(run.ctx, input) }
		reviewed := func() bool {
			var value bool
			if err := run.pool.QueryRow(run.ctx, `SELECT reviewed_at IS NOT NULL FROM research_work_catalog_page WHERE work_item_attempt_id=$1::uuid`, attemptID).Scan(&value); err != nil {
				t.Fatal(err)
			}
			return value
		}
		return transactionRecoveryOperation{invoke: invoke,
			assertRolledBack: func() {
				if reviewed() {
					t.Fatal("page reviewed after rollback")
				}
			},
			assertCommitted: func() {
				if !reviewed() {
					t.Fatal("page not reviewed")
				}
			},
			recover: invoke,
		}
	})
}

func (s *faultingSubmissionStore) RecordV6Submission(ctx context.Context, access V6AttemptAccess, decoded DecodedV6Contract, requestID string) (V6SubmissionOutcome, error) {
	if s.fault == nil {
		return s.submissionStoreStub.RecordV6Submission(ctx, access, decoded, requestID)
	}
	fault := s.fault
	s.fault = nil
	if errors.Is(fault, ErrCommitOutcomeUnknown) {
		_, _ = s.submissionStoreStub.RecordV6Submission(ctx, access, decoded, requestID)
	}
	return V6SubmissionOutcome{}, fault
}

func TestV6SubmissionRecoveryMatrix(t *testing.T) {
	raw := withValidV6SelfHash(t, readV6Fixture(t, filepath.Join("..", "..", "..", "docs", "research", "fixtures", "atomic-result-v6.example.json")), "content_hash")
	access := V6AttemptAccess{
		WorkspaceID: "00000000-0000-4000-8000-000000000002", RunID: "00000000-0000-4000-8000-000000000003",
		WorkItemID: "00000000-0000-4000-8000-000000000202", AttemptID: "00000000-0000-4000-8000-000000000204",
		AgentID: "00000000-0000-4000-8000-000000000205",
	}
	binding := V6SubmissionBinding{
		ManifestID: "00000000-0000-4000-8000-000000000201", ManifestHash: "sha256:3333333333333333333333333333333333333333333333333333333333333333",
		ExpectedKind: V6ContractAtomicResultSubmission, TaskSchemaID: "research.production_benchmark.v1",
		TaskSchema: []byte(`{"type":"object","required":["benchmark_matrix"]}`),
	}
	for _, test := range []struct {
		name  string
		fault error
	}{
		{name: "before commit rollback", fault: errors.New("before commit")},
		{name: "after commit outcome unknown", fault: ErrCommitOutcomeUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &faultingSubmissionStore{submissionStoreStub: &submissionStoreStub{binding: binding, seen: map[string]V6SubmissionOutcome{}}, fault: test.fault}
			module := v6SubmissionModule{store: store}
			if _, err := module.Submit(context.Background(), V6SubmissionInput{V6AttemptAccess: access, Raw: raw}); err == nil {
				t.Fatal("faulted submission succeeded")
			}
			outcome, err := module.Submit(context.Background(), V6SubmissionInput{V6AttemptAccess: access, Raw: raw})
			if err != nil {
				t.Fatal(err)
			}
			wantReplay := errors.Is(test.fault, ErrCommitOutcomeUnknown)
			if outcome.Replayed != wantReplay {
				t.Fatalf("replayed=%v want=%v", outcome.Replayed, wantReplay)
			}
		})
	}
}
