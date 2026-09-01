package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	issueDispatchLeaseTTL = 30 * time.Second
	issueDispatchBatch    = 100

	// maxConcurrentRunsPerGoal bounds how many Issues of one Goal may hold an
	// active execution claim at the same time. Decomposition fans out and a
	// dispatched worker can decompose further, so without a per-Goal ceiling
	// effective concurrency (and spend) grows multiplicatively with graph
	// depth. Deferred Issues keep no claim, so the 5s recovery scan
	// (IssueExecutionReconcileJob -> RecoverMissing) re-reconciles them as
	// soon as a sibling Run releases its slot.
	maxConcurrentRunsPerGoal = 8
	// maxExecutionAttemptsPerIssue is the runaway circuit breaker: an Issue
	// that has burned this many execution attempts stops consuming Runs until
	// the manager intervenes. Generous on purpose — it exists to stop
	// crash/retry loops from spending forever, not to police normal retries.
	maxExecutionAttemptsPerIssue = 50
)

// IssueExecutionReconcileOptions describes why canonical Issue execution is
// being reconsidered. Invalidate is reserved for changes to the work contract
// (assignee, title, description, acceptance criteria, or runnable class), not
// for incidental row updates.
type IssueExecutionReconcileOptions struct {
	TriggerKind       string
	Invalidate        bool
	NewAttempt        bool
	ForceFreshSession bool
	ParentRunID       pgtype.UUID
	PreserveRunID     pgtype.UUID
	// KeepTerminalRunID excludes the authenticated provider turn from
	// supersession while a completion report moves its Issue to in_review.
	// The claim/owner lease are still released; daemon terminal callbacks remain
	// the sole owner of the real Run/execution outcome.
	KeepTerminalRunID pgtype.UUID
	// KeepCompletionReportRunID protects the report being inserted by the
	// completion transaction itself. Every other pending report is superseded
	// when its Issue work contract/revision is invalidated.
	KeepCompletionReportRunID pgtype.UUID
	DeliveryAttempt           int32
	MaxAttempts               int32
}

// IssueExecutionReconcileOutcome contains only post-commit side effects. The
// canonical claim, revision, cancellations, and outbox intent are already
// durable when callers publish this value.
type IssueExecutionReconcileOutcome struct {
	WorkspaceID pgtype.UUID
	IssueID     pgtype.UUID
	RunID       pgtype.UUID
	Cancelled   []db.AgentInboxEvent
	Dispatch    bool
}

// IssueExecutionService owns the canonical Issue -> logical Run boundary.
// Provider executions remain a one-to-many child of the inbox Run and are not
// minted here.
type IssueExecutionService struct {
	Queries     *db.Queries
	TxStarter   TxStarter
	TaskService *TaskService
}

func NewIssueExecutionService(q *db.Queries, tx TxStarter, tasks *TaskService) *IssueExecutionService {
	return &IssueExecutionService{Queries: q, TxStarter: tx, TaskService: tasks}
}

func newPGUUID() pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.New(), Valid: true}
}

func issueExecutionStatusRunnable(status string) bool {
	return status == "todo" || status == "in_progress"
}

func issueExecutionHasAcceptanceCriteria(raw []byte) bool {
	var criteria []string
	if len(raw) == 0 || json.Unmarshal(raw, &criteria) != nil || len(criteria) == 0 {
		return false
	}
	for _, criterion := range criteria {
		if strings.TrimSpace(criterion) == "" {
			return false
		}
	}
	return true
}

func issueExecutionPayload(opts IssueExecutionReconcileOptions) ([]byte, string, error) {
	payload, err := json.Marshal(map[string]any{
		"force_fresh_session": opts.ForceFreshSession,
		"parent_run_id":       util.UUIDToString(opts.ParentRunID),
		"delivery_attempt":    opts.DeliveryAttempt,
		"max_attempts":        opts.MaxAttempts,
		"version":             1,
	})
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(payload)
	return payload, hex.EncodeToString(digest[:]), nil
}

// ReconcileTx must run in the same transaction as the Issue write that made
// the execution decision relevant. It serializes by locking the Issue row.
func (s *IssueExecutionService) ReconcileTx(ctx context.Context, tx pgx.Tx, issue db.Issue, opts IssueExecutionReconcileOptions) (IssueExecutionReconcileOutcome, error) {
	q := db.New(tx)
	state, err := q.GetIssueExecutionStateForUpdate(ctx, db.GetIssueExecutionStateForUpdateParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return IssueExecutionReconcileOutcome{}, fmt.Errorf("lock issue execution state: %w", err)
	}

	runnable := issueExecutionStatusRunnable(state.Status) &&
		state.AssigneeType.Valid && state.AssigneeType.String == "agent" && state.AssigneeID.Valid
	if runnable && state.ChannelGoalID.Valid {
		// Goal-scoped leaves require an explicit completion contract. This keeps
		// the scheduler from spending a Run on work the controller cannot verify.
		runnable = issueExecutionHasAcceptanceCriteria(state.AcceptanceCriteria)
	}
	if runnable {
		blocked, depErr := q.HasIncompleteIssueExecutionDependencies(ctx, state.ID)
		if depErr != nil {
			return IssueExecutionReconcileOutcome{}, fmt.Errorf("check issue dependencies: %w", depErr)
		}
		runnable = !blocked
	}
	if runnable {
		agent, agentErr := q.GetAgent(ctx, state.AssigneeID)
		runnable = agentErr == nil && !agent.ArchivedAt.Valid && agent.RuntimeID.Valid && agent.WorkspaceID == state.WorkspaceID
	}

	outcome := IssueExecutionReconcileOutcome{WorkspaceID: state.WorkspaceID, IssueID: state.ID}
	claim, claimErr := q.GetActiveIssueExecution(ctx, db.GetActiveIssueExecutionParams{
		WorkspaceID: state.WorkspaceID, IssueID: state.ID,
	})
	hasClaim := claimErr == nil
	if claimErr != nil && !errors.Is(claimErr, pgx.ErrNoRows) {
		return outcome, fmt.Errorf("load active issue execution: %w", claimErr)
	}
	if opts.PreserveRunID.Valid {
		preserved, preserveErr := s.preserveCurrentRunTx(ctx, q, tx, state, claim, hasClaim, opts.PreserveRunID)
		if preserveErr != nil {
			return outcome, preserveErr
		}
		if preserved {
			return outcome, nil
		}
	}

	claimMatches := hasClaim && runnable && claim.AgentID == state.AssigneeID &&
		claim.IssueExecutionRevision == state.ExecutionRevision
	if claimMatches && !opts.Invalidate && !opts.NewAttempt {
		return outcome, nil
	}

	if opts.Invalidate {
		if _, supersedeErr := tx.Exec(ctx, `
			UPDATE issue_completion_report
			SET review_status='superseded', updated_at=now()
			WHERE workspace_id=$1 AND issue_id=$2 AND review_status='pending'
			  AND ($3::uuid IS NULL OR run_id <> $3::uuid)`,
			state.WorkspaceID, state.ID, opts.KeepCompletionReportRunID); supersedeErr != nil {
			return outcome, fmt.Errorf("supersede stale completion reports: %w", supersedeErr)
		}
		advanced, advanceErr := q.AdvanceIssueExecutionRevision(ctx, db.AdvanceIssueExecutionRevisionParams{
			IssueID: state.ID, WorkspaceID: state.WorkspaceID,
			ExpectedExecutionRevision: state.ExecutionRevision,
		})
		if advanceErr != nil {
			return outcome, fmt.Errorf("advance issue execution revision: %w", advanceErr)
		}
		state.ExecutionRevision = advanced.ExecutionRevision
	}

	if hasClaim || opts.Invalidate || runnable {
		reason := "issue execution superseded"
		if !runnable {
			reason = "issue is not runnable"
		}
		if _, cancelErr := q.CancelIssueDispatchOutboxesForIssue(ctx, db.CancelIssueDispatchOutboxesForIssueParams{
			Reason: reason, WorkspaceID: state.WorkspaceID, IssueID: state.ID,
		}); cancelErr != nil {
			return outcome, fmt.Errorf("cancel issue dispatch intents: %w", cancelErr)
		}
		cancelled, cancelErr := q.CancelSupersededIssueRunEvents(ctx, db.CancelSupersededIssueRunEventsParams{
			Reason: pgtype.Text{String: reason, Valid: true}, WorkspaceID: state.WorkspaceID, IssueID: state.ID,
			KeepRunID: opts.KeepTerminalRunID,
		})
		if cancelErr != nil {
			return outcome, fmt.Errorf("cancel superseded issue runs: %w", cancelErr)
		}
		outcome.Cancelled = cancelled
		if s.TaskService != nil {
			for _, task := range cancelled {
				if err := s.TaskService.RecordTerminalTaskBoundaryTx(ctx, q, tx, task); err != nil {
					return outcome, fmt.Errorf("record superseded issue run terminal boundary: %w", err)
				}
			}
		}
		if _, deleteErr := q.DeleteActiveIssueExecutionForIssue(ctx, db.DeleteActiveIssueExecutionForIssueParams{
			WorkspaceID: state.WorkspaceID, IssueID: state.ID,
		}); deleteErr != nil {
			return outcome, fmt.Errorf("release active issue execution: %w", deleteErr)
		}
	}

	keepOwner := pgtype.UUID{}
	handoff := pgtype.UUID{}
	if runnable {
		keepOwner = state.AssigneeID
		handoff = state.AssigneeID
	}
	if _, releaseErr := q.ReleaseExecutorWorkOwnerLeaseForHandoff(ctx, db.ReleaseExecutorWorkOwnerLeaseForHandoffParams{
		HandoffTo: handoff, WorkspaceID: state.WorkspaceID, IssueID: state.ID, KeepOwnerAgentID: keepOwner,
	}); releaseErr != nil {
		return outcome, fmt.Errorf("release prior executor ownership: %w", releaseErr)
	}
	if !runnable {
		return outcome, nil
	}

	if state.ChannelGoalID.Valid {
		allowed, budgetErr := s.enforceGoalExecutionBudgetTx(ctx, tx, state)
		if budgetErr != nil {
			return outcome, budgetErr
		}
		if !allowed {
			return outcome, nil
		}
	}

	// Re-read the full row so a caller-provided pre-update value never leaks
	// into the snapshot/branch contract.
	current, err := q.GetIssue(ctx, state.ID)
	if err != nil {
		return outcome, fmt.Errorf("load current issue: %w", err)
	}
	if err := ensureExecutorWorkOwnerLeaseTx(ctx, tx, current, state.AssigneeID); err != nil {
		return outcome, err
	}
	attempt, err := q.AllocateIssueExecutionAttempt(ctx, db.AllocateIssueExecutionAttemptParams{
		IssueID: state.ID, WorkspaceID: state.WorkspaceID,
		ExpectedExecutionRevision: state.ExecutionRevision,
	})
	if err != nil {
		return outcome, fmt.Errorf("allocate issue execution attempt: %w", err)
	}
	runID := newPGUUID()
	if _, err = q.CreateActiveIssueExecution(ctx, db.CreateActiveIssueExecutionParams{
		WorkspaceID: state.WorkspaceID, IssueID: state.ID, RunID: runID,
		AgentID: state.AssigneeID, IssueExecutionRevision: state.ExecutionRevision,
		AttemptNumber: attempt.AttemptNumber,
	}); err != nil {
		return outcome, fmt.Errorf("claim active issue execution: %w", err)
	}
	payload, requestHash, err := issueExecutionPayload(opts)
	if err != nil {
		return outcome, err
	}
	trigger := opts.TriggerKind
	if trigger == "" {
		trigger = "reconcile"
	}
	dispatchKey := fmt.Sprintf("issue:%s:revision:%d:attempt:%d", util.UUIDToString(state.ID), state.ExecutionRevision, attempt.AttemptNumber)
	if _, err = q.CreateIssueDispatchOutbox(ctx, db.CreateIssueDispatchOutboxParams{
		WorkspaceID: state.WorkspaceID, IssueID: state.ID, RunID: runID,
		AgentID: state.AssigneeID, IssueExecutionRevision: state.ExecutionRevision,
		AttemptNumber: attempt.AttemptNumber, DispatchKey: dispatchKey,
		TriggerKind: trigger, RequestPayload: payload, RequestHash: requestHash,
	}); err != nil {
		return outcome, fmt.Errorf("persist issue dispatch intent: %w", err)
	}
	outcome.RunID = runID
	outcome.Dispatch = true
	return outcome, nil
}

func (s *IssueExecutionService) preserveCurrentRunTx(
	ctx context.Context,
	q *db.Queries,
	tx pgx.Tx,
	state db.GetIssueExecutionStateForUpdateRow,
	claim db.ActiveIssueExecution,
	hasClaim bool,
	runID pgtype.UUID,
) (bool, error) {
	event, err := q.GetAgentInboxEvent(ctx, runID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if !event.IssueID.Valid || event.IssueID != state.ID || event.AgentID != state.AssigneeID ||
		event.Reason != "issue" || event.TriggerCommentID.Valid || event.Status != "draining" {
		return false, nil
	}
	if hasClaim {
		if claim.RunID == runID {
			return true, nil
		}
		// During cutover a legacy assignment Run may already be executing while
		// a newly-created canonical Run is still queued. The authenticated
		// executing Run wins: retire only the prior claim/Run, then adopt the
		// caller below. Never cancel all Issue events here, because that would
		// suppress the caller we are preserving.
		if _, cancelErr := q.CancelIssueDispatchOutbox(ctx, db.CancelIssueDispatchOutboxParams{
			Reason: "replaced by current executing run", WorkspaceID: state.WorkspaceID,
			IssueID: state.ID, RunID: claim.RunID,
		}); cancelErr != nil && !errors.Is(cancelErr, pgx.ErrNoRows) {
			return false, fmt.Errorf("cancel replaced dispatch intent: %w", cancelErr)
		}
		cancelled, cancelErr := q.CancelAgentTask(ctx, claim.RunID)
		if cancelErr != nil && !errors.Is(cancelErr, pgx.ErrNoRows) {
			return false, fmt.Errorf("cancel replaced canonical run: %w", cancelErr)
		}
		if cancelErr == nil && s.TaskService != nil {
			if err := s.TaskService.RecordTerminalTaskBoundaryTx(ctx, q, tx, cancelled); err != nil {
				return false, fmt.Errorf("record replaced canonical run terminal boundary: %w", err)
			}
		}
		if _, deleteErr := q.DeleteActiveIssueExecutionForIssue(ctx, db.DeleteActiveIssueExecutionForIssueParams{
			WorkspaceID: state.WorkspaceID, IssueID: state.ID,
		}); deleteErr != nil {
			return false, fmt.Errorf("release replaced active issue execution: %w", deleteErr)
		}
	}
	attempt, err := q.AllocateIssueExecutionAttempt(ctx, db.AllocateIssueExecutionAttemptParams{
		IssueID: state.ID, WorkspaceID: state.WorkspaceID,
		ExpectedExecutionRevision: state.ExecutionRevision,
	})
	if err != nil {
		return false, fmt.Errorf("allocate adopted run attempt: %w", err)
	}
	if _, err = q.CreateActiveIssueExecution(ctx, db.CreateActiveIssueExecutionParams{
		WorkspaceID: state.WorkspaceID, IssueID: state.ID, RunID: runID,
		AgentID: event.AgentID, IssueExecutionRevision: state.ExecutionRevision,
		AttemptNumber: attempt.AttemptNumber,
	}); err != nil {
		return false, fmt.Errorf("claim adopted current run: %w", err)
	}
	if !event.IssueRunKind.Valid {
		if _, err = q.BindCanonicalIssueRunEvent(ctx, db.BindCanonicalIssueRunEventParams{
			RunID: runID, WorkspaceID: state.WorkspaceID,
		}); err != nil {
			return false, fmt.Errorf("bind adopted current run: %w", err)
		}
	} else if event.IssueRunKind.String != "canonical" ||
		event.IssueExecutionRevision.Int64 != state.ExecutionRevision ||
		event.IssueExecutionAttemptNumber.Int64 != attempt.AttemptNumber {
		return false, errors.New("current run canonical binding does not match adopted claim")
	}
	if _, err = q.ActivateIssueExecution(ctx, db.ActivateIssueExecutionParams{
		WorkspaceID: state.WorkspaceID, IssueID: state.ID, RunID: runID,
		IssueExecutionRevision: state.ExecutionRevision,
	}); err != nil {
		return false, fmt.Errorf("activate adopted current run: %w", err)
	}
	return true, nil
}

func ensureExecutorWorkOwnerLeaseTx(ctx context.Context, tx pgx.Tx, issue db.Issue, agentID pgtype.UUID) error {
	branch := defaultCanonicalBranch(issue, agentID)
	tag, err := tx.Exec(ctx, `
		INSERT INTO work_owner_lease (
			workspace_id, issue_id, owner_agent_id, role, canonical_branch, status, expires_at
		) VALUES ($1, $2, $3, 'executor', $4, 'active', $5)
		ON CONFLICT (issue_id) WHERE status = 'active' AND role = 'executor'
		DO UPDATE SET expires_at = EXCLUDED.expires_at, updated_at = now()
		WHERE work_owner_lease.workspace_id = EXCLUDED.workspace_id
		  AND work_owner_lease.owner_agent_id = EXCLUDED.owner_agent_id`,
		issue.WorkspaceID, issue.ID, agentID, branch, time.Now().UTC().Add(workOwnerLeaseDefaultTTL))
	if err != nil {
		return fmt.Errorf("acquire executor ownership: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrWorkOwnerLeaseConflict
	}
	return nil
}

// Reconcile opens a standalone transaction for recovery callbacks and status
// paths that do not already own one.
func (s *IssueExecutionService) Reconcile(ctx context.Context, workspaceID, issueID pgtype.UUID, opts IssueExecutionReconcileOptions) (IssueExecutionReconcileOutcome, error) {
	if s == nil || s.TxStarter == nil {
		return IssueExecutionReconcileOutcome{}, errors.New("issue execution transaction starter unavailable")
	}
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return IssueExecutionReconcileOutcome{}, err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	issue, err := q.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: issueID, WorkspaceID: workspaceID})
	if err != nil {
		return IssueExecutionReconcileOutcome{}, err
	}
	outcome, err := s.ReconcileTx(ctx, tx, issue, opts)
	if err != nil {
		return IssueExecutionReconcileOutcome{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return IssueExecutionReconcileOutcome{}, err
	}
	s.PublishOutcome(ctx, outcome)
	return outcome, nil
}

// UpdateIssue persists the Issue mutation and execution reconciliation in one
// transaction. Callers may publish the Issue response only after it returns.
func (s *IssueExecutionService) UpdateIssue(ctx context.Context, params db.UpdateIssueParams, workspaceID pgtype.UUID, opts IssueExecutionReconcileOptions) (db.Issue, error) {
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return db.Issue{}, err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	issue, err := q.UpdateIssue(ctx, params)
	if err != nil {
		return db.Issue{}, err
	}
	if issue.WorkspaceID != workspaceID {
		return db.Issue{}, errors.New("issue workspace changed during update")
	}
	outcome, err := s.ReconcileTx(ctx, tx, issue, opts)
	if err != nil {
		return db.Issue{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return db.Issue{}, err
	}
	s.PublishOutcome(ctx, outcome)
	return issue, nil
}

// UpdateStatus is the transaction-safe counterpart for internal writers such
// as dependency unlock, GitHub merge, and failure recovery.
func (s *IssueExecutionService) UpdateStatus(ctx context.Context, issue db.Issue, status string, opts IssueExecutionReconcileOptions) (db.Issue, error) {
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return db.Issue{}, err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	updated, err := q.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
		ID: issue.ID, Status: status, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return db.Issue{}, err
	}
	classChanged := issueExecutionStatusRunnable(issue.Status) != issueExecutionStatusRunnable(updated.Status)
	opts.Invalidate = opts.Invalidate || classChanged
	outcome, err := s.ReconcileTx(ctx, tx, updated, opts)
	if err != nil {
		return db.Issue{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return db.Issue{}, err
	}
	s.PublishOutcome(ctx, outcome)
	return updated, nil
}

func (s *IssueExecutionService) PublishOutcome(ctx context.Context, outcome IssueExecutionReconcileOutcome) {
	if s == nil || s.TaskService == nil {
		return
	}
	if len(outcome.Cancelled) > 0 {
		s.TaskService.BroadcastCancelledTasks(ctx, outcome.Cancelled)
	}
	if outcome.Dispatch {
		if _, err := s.DispatchRun(ctx, outcome.WorkspaceID, outcome.RunID); err != nil {
			slog.Warn("dispatch canonical issue run after commit failed", "issue_id", util.UUIDToString(outcome.IssueID), "error", err)
		}
	}
}

// DispatchRun synchronously delivers one known Run intent. It is used by
// request paths that must return the newly-created Run rather than whichever
// global outbox row happens to be oldest.
func (s *IssueExecutionService) DispatchRun(ctx context.Context, workspaceID, runID pgtype.UUID) (*db.AgentInboxEvent, error) {
	leaseToken := newPGUUID()
	outbox, err := s.Queries.ClaimIssueDispatchOutboxByRun(ctx, db.ClaimIssueDispatchOutboxByRunParams{
		LeaseToken:     leaseToken,
		LeaseExpiresAt: pgtype.Timestamptz{Time: time.Now().UTC().Add(issueDispatchLeaseTTL), Valid: true},
		WorkspaceID:    workspaceID,
		RunID:          runID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if event, lookupErr := s.Queries.GetCanonicalIssueRunEvent(ctx, db.GetCanonicalIssueRunEventParams{
				WorkspaceID: workspaceID, RunID: runID,
			}); lookupErr == nil {
				return &event, nil
			}
		}
		return nil, err
	}
	if err := s.deliverOutbox(ctx, outbox, leaseToken); err != nil {
		delay := time.Duration(outbox.DeliveryAttempts) * time.Second
		if delay < time.Second {
			delay = time.Second
		}
		if delay > time.Minute {
			delay = time.Minute
		}
		_, _ = s.Queries.RescheduleIssueDispatchOutbox(ctx, db.RescheduleIssueDispatchOutboxParams{
			NextDeliveryAt: pgtype.Timestamptz{Time: time.Now().UTC().Add(delay), Valid: true},
			LastError:      err.Error(), WorkspaceID: outbox.WorkspaceID, OutboxID: outbox.ID, LeaseToken: leaseToken,
		})
		return nil, err
	}
	event, err := s.Queries.GetAgentInboxEvent(ctx, runID)
	if err != nil {
		return nil, err
	}
	return &event, nil
}

// DispatchPending claims due outbox rows globally. Each delivery is committed
// atomically with event creation, claim activation, and outbox acknowledgement.
func (s *IssueExecutionService) DispatchPending(ctx context.Context, limit int32) (int, error) {
	if limit <= 0 || limit > issueDispatchBatch {
		limit = issueDispatchBatch
	}
	leaseToken := newPGUUID()
	claimed, err := s.Queries.ClaimIssueDispatchOutboxGlobal(ctx, db.ClaimIssueDispatchOutboxGlobalParams{
		LeaseToken:     leaseToken,
		LeaseExpiresAt: pgtype.Timestamptz{Time: time.Now().UTC().Add(issueDispatchLeaseTTL), Valid: true},
		ClaimLimit:     limit,
	})
	if err != nil {
		return 0, err
	}
	delivered := 0
	for _, outbox := range claimed {
		if err := s.deliverOutbox(ctx, outbox, leaseToken); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				_, _ = s.Reconcile(ctx, outbox.WorkspaceID, outbox.IssueID, IssueExecutionReconcileOptions{TriggerKind: "stale_dispatch_recovery"})
				continue
			}
			delay := time.Duration(outbox.DeliveryAttempts) * time.Second
			if delay > time.Minute {
				delay = time.Minute
			}
			_, _ = s.Queries.RescheduleIssueDispatchOutbox(ctx, db.RescheduleIssueDispatchOutboxParams{
				NextDeliveryAt: pgtype.Timestamptz{Time: time.Now().UTC().Add(delay), Valid: true},
				LastError:      err.Error(), WorkspaceID: outbox.WorkspaceID, OutboxID: outbox.ID, LeaseToken: leaseToken,
			})
			continue
		}
		delivered++
	}
	return delivered, nil
}

func (s *IssueExecutionService) deliverOutbox(ctx context.Context, outbox db.IssueDispatchOutbox, leaseToken pgtype.UUID) error {
	issue, err := s.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: outbox.IssueID, WorkspaceID: outbox.WorkspaceID})
	if err != nil {
		return err
	}
	agent, err := s.Queries.GetAgent(ctx, outbox.AgentID)
	if err != nil {
		return err
	}
	snapshot, err := buildIssueAssignmentSnapshotWithQueries(ctx, s.Queries, issue)
	if err != nil {
		return err
	}
	var taskContext []byte
	taskContext, err = withIssueAssignmentSnapshot(taskContext, snapshot)
	if err != nil {
		return err
	}
	taskContext, err = WithTaskExecutionConfig(taskContext, agent.Model.String, agent.ThinkingLevel.String)
	if err != nil {
		return err
	}
	var payload struct {
		ForceFreshSession bool   `json:"force_fresh_session"`
		ParentRunID       string `json:"parent_run_id"`
		DeliveryAttempt   int32  `json:"delivery_attempt"`
		MaxAttempts       int32  `json:"max_attempts"`
	}
	if err := json.Unmarshal(outbox.RequestPayload, &payload); err != nil {
		return err
	}
	var parentRunID pgtype.UUID
	if payload.ParentRunID != "" {
		parentRunID, err = util.ParseUUID(payload.ParentRunID)
		if err != nil {
			return fmt.Errorf("decode parent run id: %w", err)
		}
	}

	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	row, err := q.CreateCanonicalIssueRunEvent(ctx, db.CreateCanonicalIssueRunEventParams{
		OutboxID: outbox.ID, WorkspaceID: outbox.WorkspaceID, LeaseToken: leaseToken,
		TaskContext: taskContext, Priority: priorityToInt(issue.Priority),
		ForceFreshSession: payload.ForceFreshSession,
		ParentRunID:       parentRunID,
		DeliveryAttempt:   payload.DeliveryAttempt,
		MaxAttempts:       payload.MaxAttempts,
	})
	if err != nil {
		return err
	}
	if _, err = q.ActivateIssueExecution(ctx, db.ActivateIssueExecutionParams{
		WorkspaceID: outbox.WorkspaceID, IssueID: outbox.IssueID, RunID: outbox.RunID,
		IssueExecutionRevision: outbox.IssueExecutionRevision,
	}); err != nil {
		return err
	}
	if _, err = q.MarkIssueDispatchOutboxDelivered(ctx, db.MarkIssueDispatchOutboxDeliveredParams{
		WorkspaceID: outbox.WorkspaceID, OutboxID: outbox.ID, RunID: outbox.RunID, LeaseToken: leaseToken,
	}); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	event, err := s.Queries.GetAgentInboxEvent(ctx, row.ID)
	if err != nil {
		return err
	}
	if s.TaskService != nil {
		s.TaskService.broadcastTaskEvent(ctx, protocol.EventTaskQueued, event)
		s.TaskService.NotifyTaskEnqueued(ctx, event)
	}
	return nil
}

// enforceGoalExecutionBudgetTx applies the per-Goal execution budget before a
// new attempt is allocated. Two limits with different failure semantics:
//
//   - Attempts: an Issue past maxExecutionAttemptsPerIssue is a permanent
//     stall, so it surfaces a durable budget_exhausted controller event
//     (deduplicated while one is pending) and the manager decides — fix the
//     contract, reassign, cancel, or recreate — instead of the scheduler
//     retrying forever.
//   - Concurrency: a Goal at maxConcurrentRunsPerGoal is ordinary
//     backpressure, healed within seconds by the recovery scan once a slot
//     frees, so deferral is silent. Two parallel reconciles can each observe
//     cap-1 and both dispatch; a transient overshoot of one is acceptable for
//     a spend bound and not worth a serialized counter.
func (s *IssueExecutionService) enforceGoalExecutionBudgetTx(ctx context.Context, tx pgx.Tx, state db.GetIssueExecutionStateForUpdateRow) (bool, error) {
	if state.ExecutionAttemptSequence >= maxExecutionAttemptsPerIssue {
		if _, err := tx.Exec(ctx, `
			INSERT INTO goal_controller_event(workspace_id, goal_id, event_kind, source_kind, source_id, payload)
			SELECT $1, $2, 'budget_exhausted', 'issue', $3,
			       jsonb_build_object('attempts', $4::bigint, 'limit', $5::bigint)
			WHERE NOT EXISTS (
				SELECT 1 FROM goal_controller_event
				WHERE workspace_id=$1 AND goal_id=$2 AND event_kind='budget_exhausted'
				  AND source_id=$3 AND status='pending'
			)`, state.WorkspaceID, state.ChannelGoalID, state.ID,
			state.ExecutionAttemptSequence, int64(maxExecutionAttemptsPerIssue)); err != nil {
			return false, fmt.Errorf("record issue attempt budget exhaustion: %w", err)
		}
		slog.Warn("issue execution attempt budget exhausted",
			"issue_id", util.UUIDToString(state.ID),
			"goal_id", util.UUIDToString(state.ChannelGoalID),
			"attempts", state.ExecutionAttemptSequence)
		return false, nil
	}

	var activeSiblings int64
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM active_issue_execution claim
		JOIN issue sibling
		  ON sibling.workspace_id=claim.workspace_id AND sibling.id=claim.issue_id
		WHERE claim.workspace_id=$1 AND sibling.channel_goal_id=$2 AND claim.issue_id<>$3`,
		state.WorkspaceID, state.ChannelGoalID, state.ID).Scan(&activeSiblings); err != nil {
		return false, fmt.Errorf("count active goal executions: %w", err)
	}
	if activeSiblings >= maxConcurrentRunsPerGoal {
		slog.Debug("issue execution deferred by goal concurrency budget",
			"issue_id", util.UUIDToString(state.ID),
			"goal_id", util.UUIDToString(state.ChannelGoalID),
			"active_runs", activeSiblings)
		return false, nil
	}
	return true, nil
}

// RecoverMissing scans only runnable Agent-assigned Issues without a canonical
// claim. It closes the crash window for legacy/direct SQL writers and for a
// process that died before its post-commit dispatcher kick.
func (s *IssueExecutionService) RecoverMissing(ctx context.Context, limit int32) (int, error) {
	if limit <= 0 || limit > issueDispatchBatch {
		limit = issueDispatchBatch
	}
	issues, err := s.Queries.ListRunnableIssuesMissingExecution(ctx, limit)
	if err != nil {
		return 0, err
	}
	reconciled := 0
	for _, issue := range issues {
		outcome, reconcileErr := s.Reconcile(ctx, issue.WorkspaceID, issue.ID, IssueExecutionReconcileOptions{TriggerKind: "recovery_scan"})
		if reconcileErr != nil {
			slog.Warn("recover missing issue execution failed", "issue_id", util.UUIDToString(issue.ID), "error", reconcileErr)
			continue
		}
		if outcome.Dispatch {
			reconciled++
		}
	}
	delivered, err := s.DispatchPending(ctx, limit)
	return reconciled + delivered, err
}
