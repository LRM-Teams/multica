package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Engine struct {
	store              *PostgresStore
	dispatcher         Dispatcher
	projector          Projector
	clock              Clock
	leaseDuration      time.Duration
	leaseRenewInterval time.Duration
	v6Agents           AgentLifecycleAdapter
	v6Inbox            InboxDispatchAdapter
	retrieval          RetrievalAdapter
}

const (
	reconcileLeaseDuration      = 45 * time.Second
	reconcileLeaseRenewInterval = 15 * time.Second
)

func NewEngine(store *PostgresStore, dispatcher Dispatcher, projector Projector) ResearchRun {
	return newEngine(store, dispatcher, projector)
}

func (e *Engine) ProjectionV6Snapshot(ctx context.Context, request V6ProjectionPageRequest) (V6ProjectionSnapshot, error) {
	if e == nil || e.store == nil {
		return V6ProjectionSnapshot{}, ErrV6DirectorUnavailable
	}
	return e.store.ProjectionV6Snapshot(ctx, request)
}

func (e *Engine) ProjectionV6Slice(ctx context.Context, request V6ProjectionSliceRequest) (V6ProjectionSnapshot, error) {
	if e == nil || e.store == nil {
		return V6ProjectionSnapshot{}, ErrV6DirectorUnavailable
	}
	return e.store.ProjectionV6Slice(ctx, request)
}

func (e *Engine) ProjectionV6Deltas(ctx context.Context, request V6ProjectionDeltaRequest) (V6ProjectionDeltaPage, error) {
	if e == nil || e.store == nil {
		return V6ProjectionDeltaPage{}, ErrV6DirectorUnavailable
	}
	return e.store.ProjectionV6Deltas(ctx, request)
}

func (e *Engine) ProjectionV6NodeDetail(ctx context.Context, workspaceID, runID, snapshotID, nodeID, view string) (V6ProjectionNodeDetail, error) {
	if e == nil || e.store == nil {
		return V6ProjectionNodeDetail{}, ErrV6DirectorUnavailable
	}
	return e.store.ProjectionV6NodeDetail(ctx, workspaceID, runID, snapshotID, nodeID, view)
}

func (e *Engine) ProjectionV6WorkActivity(ctx context.Context, workspaceID, runID, workItemID string) (V6WorkActivity, error) {
	if e == nil || e.store == nil {
		return V6WorkActivity{}, ErrV6DirectorUnavailable
	}
	return e.store.ProjectionV6WorkActivity(ctx, workspaceID, runID, workItemID)
}

func NewEngineWithReportStorage(store *PostgresStore, dispatcher Dispatcher, projector Projector, reportStorage ReportPackageStorage) ResearchRun {
	store.reportStorage = reportStorage
	return newEngine(store, dispatcher, projector)
}

func NewEngineWithReportAdapters(store *PostgresStore, dispatcher Dispatcher, projector Projector, reportStorage ReportPackageStorage, renderer ReportRenderAdapter, frameAncestors []string) ResearchRun {
	store.reportStorage, store.reportRenderer = reportStorage, renderer
	store.reportFrameAncestors = append([]string(nil), frameAncestors...)
	return newEngine(store, dispatcher, projector)
}

// NewEngineWithV6Adapters wires external effects without granting adapters any
// canonical-store mutation capability. V6 remains gated by supported-version
// policy until the activation slice.
func NewEngineWithV6Adapters(store *PostgresStore, dispatcher Dispatcher, projector Projector, agents AgentLifecycleAdapter, inbox InboxDispatchAdapter) ResearchRun {
	engine := newEngine(store, dispatcher, projector)
	engine.v6Agents = agents
	engine.v6Inbox = inbox
	return engine
}

// NewEngineWithRuntimeAdapters wires every V6 external effect while retaining
// the report package boundary. Production uses this constructor so a process
// restart cannot silently drop either half of the Director runtime.
func NewEngineWithRuntimeAdapters(store *PostgresStore, dispatcher Dispatcher, projector Projector, reportStorage ReportPackageStorage, renderer ReportRenderAdapter, frameAncestors []string, agents AgentLifecycleAdapter, inbox InboxDispatchAdapter, retrieval RetrievalAdapter) ResearchRun {
	if store != nil {
		store.reportStorage, store.reportRenderer = reportStorage, renderer
		store.reportFrameAncestors = append([]string(nil), frameAncestors...)
	}
	engine := newEngine(store, dispatcher, projector)
	engine.v6Agents = agents
	engine.v6Inbox = inbox
	engine.retrieval = retrieval
	return engine
}

func newEngine(store *PostgresStore, dispatcher Dispatcher, projector Projector) *Engine {
	return &Engine{
		store: store, dispatcher: dispatcher, projector: projector, clock: systemClock{},
		leaseDuration: reconcileLeaseDuration, leaseRenewInterval: reconcileLeaseRenewInterval,
	}
}

func (e *Engine) Create(ctx context.Context, in StartInput) (Run, error) {
	if e == nil || e.store == nil || e.dispatcher == nil {
		return Run{}, errors.New("research run engine is unavailable")
	}
	run, _, err := e.store.CreateRun(ctx, in, DefaultRunConfig(in.DepthTier))
	if err != nil {
		return Run{}, err
	}
	if err = e.ReconcileSession(ctx, run.SessionID); err != nil {
		if errors.Is(err, ErrRunLeaseLost) {
			return run, nil
		}
		return run, err
	}
	return e.store.GetRun(ctx, run.SessionID, in.WorkspaceID)
}

func (e *Engine) PersistSourceIngestion(ctx context.Context, in PersistSourceIngestionInput) (PersistSourceIngestionResult, error) {
	if e == nil || e.store == nil {
		return PersistSourceIngestionResult{}, errors.New("research run engine is unavailable")
	}
	return e.store.PersistSourceIngestion(ctx, in)
}

func (e *Engine) RebuildCanonicalRun(ctx context.Context, sessionID, workspaceID string) (RebuiltCanonicalRun, error) {
	if e == nil || e.store == nil {
		return RebuiltCanonicalRun{}, errors.New("research run engine is unavailable")
	}
	return e.store.RebuildCanonicalRun(ctx, sessionID, workspaceID)
}

func (e *Engine) BootstrapV6(ctx context.Context, in V6BootstrapInput) (Run, error) {
	if e == nil || e.store == nil {
		return Run{}, errors.New("research run engine is unavailable")
	}
	run, sequence, err := e.store.BootstrapV6(ctx, in, DefaultRunConfig(in.DepthTier))
	if err != nil {
		return Run{}, err
	}
	if _, err = e.StartV6DirectorCycle(ctx, StartV6DirectorCycleInput{
		WorkspaceID: in.WorkspaceID, RunID: run.SessionID, TriggerKey: "bootstrap",
		FromSequence: sequence, ThroughSequence: sequence, ExpectedStateVersion: run.StateVersion,
		Now: e.clock.Now(),
	}); err != nil {
		return run, err
	}
	_, err = e.ReconcileV6Work(ctx, 32)
	if err != nil && !errors.Is(err, ErrRunLeaseLost) {
		return run, err
	}
	return e.store.GetRun(ctx, run.SessionID, in.WorkspaceID)
}

func (e *Engine) Start(ctx context.Context, in StartInput) (Run, error) {
	if e == nil || e.store == nil || e.dispatcher == nil {
		return Run{}, errors.New("research run engine is unavailable")
	}
	run, _, err := e.store.InitializeRun(ctx, in, DefaultRunConfig(in.DepthTier))
	if err != nil {
		return Run{}, err
	}
	if err = e.ReconcileSession(ctx, in.SessionID); err != nil {
		if errors.Is(err, ErrRunLeaseLost) {
			return run, nil
		}
		return run, err
	}
	return e.store.GetRun(ctx, in.SessionID, in.WorkspaceID)
}

func (e *Engine) SubmitResult(ctx context.Context, sessionID, workspaceID, taskID, attemptID, agentID, inboxTaskID string, raw json.RawMessage) (AcceptResultOutcome, error) {
	outcome, err := (resultAcceptanceModule{store: e.store}).Accept(ctx, resultSubmission{
		SessionID: sessionID, WorkspaceID: workspaceID, TaskID: taskID,
		AttemptID: attemptID, AgentID: agentID, InboxTaskID: inboxTaskID, Raw: raw,
	})
	if err != nil {
		return AcceptResultOutcome{}, err
	}
	if err = e.ReconcileSession(ctx, sessionID); err != nil {
		if errors.Is(err, ErrRunLeaseLost) {
			return outcome, nil
		}
		return outcome, fmt.Errorf("result accepted but run advancement failed: %w", err)
	}
	return outcome, nil
}

const defaultScreenedSourceFetchBytes int64 = 2 << 20

func (e *Engine) IngestPendingScreenedSources(ctx context.Context, limit int) (int, error) {
	if e == nil || e.store == nil || e.retrieval == nil || limit <= 0 {
		return 0, nil
	}
	pending, err := e.store.ListPendingScreenedSourceIngestions(ctx, limit)
	if err != nil {
		return 0, err
	}
	ingested := 0
	var errs []error
	for _, item := range pending {
		if item.MaximumContentSize <= 0 {
			item.MaximumContentSize = defaultScreenedSourceFetchBytes
		}
		if _, ingestErr := e.store.FetchAndIngestScreenedSource(ctx, item, e.retrieval); ingestErr != nil {
			errs = append(errs, ingestErr)
			continue
		}
		ingested++
	}
	return ingested, errors.Join(errs...)
}

func (e *Engine) ReconcileDue(ctx context.Context, limit int) (int, error) {
	v6Processed, v6Err := e.ReconcileV6Work(ctx, limit)
	ids, err := e.store.ListDueRunIDs(ctx, limit)
	if err != nil {
		return v6Processed, errors.Join(v6Err, err)
	}
	processed := 0
	errs := []error{}
	for _, sessionID := range ids {
		if err = e.ReconcileSession(ctx, sessionID); err != nil {
			if errors.Is(err, ErrRunLeaseLost) {
				continue
			}
			errs = append(errs, fmt.Errorf("research run %s: %w", sessionID, err))
			continue
		}
		processed++
	}
	return processed + v6Processed, errors.Join(append(errs, v6Err)...)
}

func (e *Engine) WorkManifest(ctx context.Context, access V6AttemptAccess) (V6WorkManifest, error) {
	return (workManifestModule{store: e.store}).Get(ctx, access)
}

func (e *Engine) WorkCatalog(ctx context.Context, in V6CatalogRequest) (V6CatalogPage, error) {
	return (workCatalogModule{store: e.store}).Get(ctx, in)
}

func (e *Engine) AcknowledgeWorkCatalog(ctx context.Context, in AcknowledgeV6CatalogInput) error {
	return (workCatalogModule{store: e.store}).Acknowledge(ctx, in)
}

func (e *Engine) SubmitV6Work(ctx context.Context, in V6SubmissionInput) (V6SubmissionOutcome, error) {
	return (v6SubmissionModule{store: e.store}).Submit(ctx, in)
}

func (e *Engine) ReconcileV6Work(ctx context.Context, limit int) (int, error) {
	if e == nil || e.store == nil {
		return 0, errors.New("research run engine is unavailable")
	}
	steering, steeringErr := e.store.ProcessV6SteeringTriggers(ctx, limit)
	proposals, proposalErr := e.store.ApplyReceivedV6DirectorProposals(ctx, limit)
	reports, reportErr := e.store.ApplyReceivedV6ReportPackages(ctx, limit)
	applied, applyErr := e.store.ApplyReceivedV6Submissions(ctx, limit)
	recovered, err := e.store.RecoverExpiredV6WorkItems(ctx, limit)
	cancelled, cancellationErr := cancelLostV6InboxTasks(ctx, e.store, e.dispatcher, limit)
	settled, settledCancellationErr := cancelSettledV6InboxTasks(ctx, e.store, e.dispatcher, limit)
	events, eventErr := e.store.ProcessV6EventTriggers(ctx, limit)
	prepared, prepareErr := e.store.PrepareV6Dispatches(ctx, limit)
	delivered, deliveryErr := (v6RuntimeModule{store: e.store, team: e.store, agents: e.v6Agents, inbox: e.v6Inbox, clock: e.clock}).Deliver(ctx, limit)
	ingested, ingestErr := e.IngestPendingScreenedSources(ctx, limit)
	return recovered + cancelled + settled + steering + proposals + reports + applied + events + prepared + delivered + ingested, errors.Join(err, cancellationErr, settledCancellationErr, steeringErr, proposalErr, reportErr, applyErr, eventErr, prepareErr, deliveryErr, ingestErr)
}

func (e *Engine) AssignV6Director(ctx context.Context, in AssignV6DirectorInput) (V6DirectorAssignment, error) {
	return (directorModule{store: e.store}).Assign(ctx, in)
}
func (e *Engine) MarkV6DirectorUnavailable(ctx context.Context, in MarkV6DirectorUnavailableInput) (V6DirectorAssignment, error) {
	return (directorModule{store: e.store}).MarkUnavailable(ctx, in)
}
func (e *Engine) StartV6DirectorCycle(ctx context.Context, in StartV6DirectorCycleInput) (V6DirectorCycle, error) {
	return (directorBriefModule{store: e.store}).Start(ctx, in)
}
func (e *Engine) AddV6TeamMember(ctx context.Context, in AddV6TeamMemberInput) (V6TeamMember, error) {
	return (teamV6Module{store: e.store}).Add(ctx, in)
}
func (e *Engine) ArchiveV6TeamMember(ctx context.Context, in ArchiveV6TeamMemberInput) (V6TeamMember, error) {
	return (teamV6Module{store: e.store}).Archive(ctx, in)
}
func (e *Engine) RecordV6MatchDecision(ctx context.Context, in RecordV6MatchDecisionInput) (V6MatchDecision, error) {
	return (matchV6Module{store: e.store}).Record(ctx, in)
}
func (e *Engine) OpenV6Discussion(ctx context.Context, in OpenV6DiscussionInput) (V6Discussion, error) {
	return (discussionV6Module{store: e.store}).Open(ctx, in)
}
func (e *Engine) ApplyV6SteeringAssessment(ctx context.Context, in ApplyV6SteeringAssessmentInput) (V6SteeringAssessment, error) {
	return (steeringV6Module{store: e.store}).Apply(ctx, in)
}
func (e *Engine) DirectorBriefPage(ctx context.Context, access V6AttemptAccess, cursor string) (V6DirectorBriefPage, error) {
	return (directorBriefModule{store: e.store}).Page(ctx, access, cursor)
}
func (e *Engine) AcknowledgeDirectorBrief(ctx context.Context, in AcknowledgeV6DirectorBriefInput) error {
	return (directorBriefModule{store: e.store}).Acknowledge(ctx, in)
}

func (e *Engine) ReconcileSession(ctx context.Context, sessionID string) (retErr error) {
	if e == nil || e.store == nil || e.dispatcher == nil {
		return errors.New("research run engine is unavailable")
	}
	leaseDuration := e.leaseDuration
	if leaseDuration <= 0 {
		leaseDuration = reconcileLeaseDuration
	}
	leaseRenewInterval := e.leaseRenewInterval
	if leaseRenewInterval <= 0 || leaseRenewInterval >= leaseDuration {
		leaseRenewInterval = min(reconcileLeaseRenewInterval, leaseDuration/3)
	}
	if leaseRenewInterval <= 0 {
		leaseRenewInterval = time.Nanosecond
	}
	token := uuid.NewString()
	run, lease, claimed, err := e.store.ClaimRun(ctx, sessionID, token, leaseDuration)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	reconcileCtx, cancelReconcile := context.WithCancel(ctx)
	reconcileCtx = withRunLease(reconcileCtx, lease)
	heartbeatDone := make(chan struct{})
	heartbeatErr := make(chan error, 1)
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(leaseRenewInterval)
		defer ticker.Stop()
		current := lease
		for {
			select {
			case <-reconcileCtx.Done():
				return
			case <-ticker.C:
				var renewErr error
				current, renewErr = e.store.RenewRunLease(reconcileCtx, current, leaseDuration)
				if renewErr != nil {
					if reconcileCtx.Err() != nil {
						return
					}
					heartbeatErr <- fmt.Errorf("renew research run lease: %w", renewErr)
					cancelReconcile()
					return
				}
			}
		}
	}()
	ctx = reconcileCtx
	next := e.clock.Now().Add(15 * time.Second)
	defer func() {
		cancelReconcile()
		<-heartbeatDone
		select {
		case heartbeatFailure := <-heartbeatErr:
			retErr = errors.Join(retErr, heartbeatFailure)
		default:
		}
		lastError := ""
		if retErr != nil {
			lastError = retErr.Error()
		}
		if releaseErr := e.store.ReleaseRun(context.WithoutCancel(ctx), lease, next, lastError); releaseErr != nil {
			if !errors.Is(releaseErr, ErrRunLeaseLost) || !errors.Is(retErr, ErrRunLeaseLost) {
				retErr = errors.Join(retErr, fmt.Errorf("release research run lease: %w", releaseErr))
			}
		}
	}()
	if run.OrchestratorVersion == OrchestratorVersionV6 {
		next = e.clock.Now().Add(time.Hour)
		return e.projectPending(ctx, sessionID)
	}
	pendingCancellations, cancelErr := e.cancelPendingAttempts(ctx, run, "research_run_"+string(run.Status))
	if cancelErr != nil {
		next = e.clock.Now().Add(10 * time.Second)
		return cancelErr
	}
	if pendingCancellations {
		next = e.clock.Now().Add(10 * time.Second)
		return e.projectPending(ctx, sessionID)
	}
	if run.Status != RunStatusRunning {
		next = e.clock.Now().Add(time.Hour)
		return e.projectPending(ctx, sessionID)
	}
	if run.InitializedAt != nil && run.Config.MaxRunSeconds > 0 && e.clock.Now().After(run.InitializedAt.Add(time.Duration(run.Config.MaxRunSeconds)*time.Second)) {
		return e.failureModule().HandleBudgetExhaustion(ctx, run, "wall_time", fmt.Sprintf("run exceeded %d seconds", run.Config.MaxRunSeconds))
	}

	if err = e.executionModule().SyncAttempts(ctx, sessionID); err != nil {
		return e.failureModule().HandleDispatchFailure(ctx, sessionID, err)
	}

	tasks, err := e.store.ListTasks(ctx, sessionID)
	if err != nil {
		return err
	}
	attempts, err := e.store.ListAttempts(ctx, sessionID)
	if err != nil {
		return err
	}
	members, err := e.store.ListFleetMembers(ctx, sessionID, run.WorkspaceID)
	if err != nil {
		return err
	}
	dispatchOutcome, err := e.dispatchReady(ctx, run, tasks, attempts, members)
	if err != nil {
		return e.failureModule().HandleDispatchFailure(ctx, sessionID, err)
	}
	if dispatchNext, wait := nextReconcileAfterDispatch(e.clock.Now(), dispatchOutcome, hasExecutingCurrentWork(run, tasks)); wait {
		next = dispatchNext
		return e.projectPending(ctx, sessionID)
	}
	if hasUnfinishedCurrentWork(run, tasks) {
		// Delivery Gate findings such as plan_incomplete and tasks_incomplete
		// describe expected intermediate state while current-plan work remains.
		// They must not create remediation work before that work has run.
		next = e.clock.Now().Add(10 * time.Second)
		return e.projectPending(ctx, sessionID)
	}

	gateOutcome, err := e.gateModule().Advance(ctx, run, tasks)
	if err != nil {
		return err
	}
	if !gateOutcome.RemediationCreated {
		if gateOutcome.NextReconcileAfter > 0 {
			next = e.clock.Now().Add(gateOutcome.NextReconcileAfter)
		}
		return nil
	}
	if err = e.executionModule().ActivateReadyTasks(ctx, sessionID); err != nil {
		return err
	}
	tasks, err = e.store.ListTasks(ctx, sessionID)
	if err != nil {
		return err
	}
	attempts, err = e.store.ListAttempts(ctx, sessionID)
	if err != nil {
		return err
	}
	secondDispatch, err := e.dispatchReady(ctx, run, tasks, attempts, members)
	if err != nil {
		return e.failureModule().HandleDispatchFailure(ctx, sessionID, err)
	}
	next = e.clock.Now().Add(10 * time.Second)
	if dispatchNext, wait := nextReconcileAfterDispatch(e.clock.Now(), secondDispatch, false); wait {
		next = dispatchNext
	}
	return e.projectPending(ctx, sessionID)
}

func nextReconcileAfterDispatch(now time.Time, outcome DispatchOutcome, executing bool) (time.Time, bool) {
	if outcome.Dispatched > 0 || executing {
		next := now.Add(10 * time.Second)
		if outcome.NextDispatchAt != nil && outcome.NextDispatchAt.Before(next) {
			next = *outcome.NextDispatchAt
		}
		return next, true
	}
	if !outcome.Waiting {
		return time.Time{}, false
	}
	if outcome.NextDispatchAt != nil {
		return *outcome.NextDispatchAt, true
	}
	return now.Add(5 * time.Minute), true
}

func hasExecutingCurrentWork(run Run, tasks []Task) bool {
	for _, task := range tasks {
		if task.GoalVersion != run.GoalVersion || task.PlanVersion != run.PlanVersion {
			continue
		}
		if task.Status == TaskStatusDispatching || task.Status == TaskStatusRunning {
			return true
		}
	}
	return false
}

func hasUnfinishedCurrentWork(run Run, tasks []Task) bool {
	for _, task := range tasks {
		if task.GoalVersion != run.GoalVersion || task.PlanVersion != run.PlanVersion {
			continue
		}
		switch task.Status {
		case TaskStatusPending, TaskStatusReady, TaskStatusDispatching, TaskStatusRunning:
			return true
		}
	}
	return false
}

func (e *Engine) Pause(ctx context.Context, sessionID, workspaceID, userID string) (Run, error) {
	run, _, _, err := e.store.Pause(ctx, sessionID, workspaceID, userID)
	if err != nil {
		return Run{}, err
	}
	return run, e.finishTerminalTransition(ctx, run, sessionID, "research_run_paused")
}

func (e *Engine) Resume(ctx context.Context, sessionID, workspaceID, userID string) (Run, error) {
	run, _, err := e.store.Resume(ctx, sessionID, workspaceID, userID)
	if err != nil {
		return Run{}, err
	}
	return run, reconcileHandoff(e.ReconcileSession(ctx, sessionID))
}

func (e *Engine) Cancel(ctx context.Context, sessionID, workspaceID, userID, reason string) (Run, error) {
	run, _, _, err := e.store.Cancel(ctx, sessionID, workspaceID, userID, reason)
	if err != nil {
		return Run{}, err
	}
	return run, e.finishTerminalTransition(ctx, run, sessionID, "research_run_cancelled")
}

func (e *Engine) Archive(ctx context.Context, sessionID, workspaceID, userID, reason string) (Run, error) {
	run, _, _, err := e.store.Archive(ctx, sessionID, workspaceID, userID, reason)
	if err != nil {
		return Run{}, err
	}
	return run, e.finishTerminalTransition(ctx, run, sessionID, "research_run_archived")
}

// finishTerminalTransition keeps a persisted terminal transition successful
// for V6 even when projection/reconcile is still catching up.
func (e *Engine) finishTerminalTransition(ctx context.Context, run Run, sessionID, reason string) error {
	if run.OrchestratorVersion == OrchestratorVersionV6 {
		_ = reconcileHandoff(e.ReconcileSession(ctx, sessionID))
		return nil
	}
	if _, err := e.cancelPendingAttempts(ctx, run, reason); err != nil {
		return err
	}
	return reconcileHandoff(e.ReconcileSession(ctx, sessionID))
}

func (e *Engine) Confirm(ctx context.Context, sessionID, workspaceID, userID string) (Run, error) {
	run, err := e.gateModule().Confirm(ctx, sessionID, workspaceID, userID)
	if err != nil {
		return Run{}, err
	}
	return run, reconcileHandoff(e.ReconcileSession(ctx, sessionID))
}

func (e *Engine) Snapshot(ctx context.Context, sessionID, workspaceID string) (RunSnapshot, error) {
	run, err := e.store.GetRun(ctx, sessionID, workspaceID)
	if err != nil {
		return RunSnapshot{}, err
	}
	contract, err := e.store.GetCurrentContract(ctx, sessionID, workspaceID)
	if err != nil {
		return RunSnapshot{}, err
	}
	method, err := e.store.GetCurrentMethod(ctx, sessionID, workspaceID)
	if err != nil {
		return RunSnapshot{}, err
	}
	questions, err := e.store.ListQuestions(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	tasks, err := e.store.ListTasks(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	attempts, err := e.store.ListAttempts(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	sources, err := e.store.ListSourceSnapshots(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	observations, err := e.store.ListObservations(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	claims, err := e.store.ListClaims(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	gate, err := e.gateModule().Evaluate(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	snapshot := RunSnapshot{
		Run: run, Contract: contract, Method: method, Questions: questions, Tasks: tasks, Attempts: attempts,
		Sources: sources, Observations: observations, Claims: claims, Gate: gate,
	}
	passportEnabled, err := e.store.SessionArtifactPassportEnabled(ctx, sessionID, workspaceID)
	if err != nil {
		return RunSnapshot{}, err
	}
	if passportEnabled {
		projection, projectionErr := (artifactProjectionModule{store: e.store}).Load(
			ctx, workspaceID, sessionID, artifactProjectionScope{},
		)
		if projectionErr != nil {
			return RunSnapshot{}, projectionErr
		}
		snapshot.ArtifactProjection = &projection
	}
	normalizeRunSnapshot(&snapshot)
	return snapshot, nil
}

// SnapshotForProjection is the internal least-privilege read model used by
// graph projection. Projection needs durable graph identities and delivery
// readiness, but it must not read source/observation representations,
// evaluation-private context, frozen Attempt context, or Artifact contents.
func (e *Engine) SnapshotForProjection(ctx context.Context, sessionID, workspaceID string) (RunSnapshot, error) {
	if e == nil || e.store == nil {
		return RunSnapshot{}, errors.New("research run engine is unavailable")
	}
	return loadProjectionSnapshot(ctx, e.store, sessionID, workspaceID)
}

type projectionSnapshotStore interface {
	GetRun(context.Context, string, string) (Run, error)
	GetCurrentContract(context.Context, string, string) (ResearchContract, error)
	GetCurrentMethod(context.Context, string, string) (*ResearchMethod, error)
	ListQuestions(context.Context, string) ([]Question, error)
	ListTasks(context.Context, string) ([]Task, error)
	ListAttempts(context.Context, string) ([]Attempt, error)
	ListClaims(context.Context, string) ([]Claim, error)
	EvaluateGate(context.Context, string) (GateResult, error)
}

func loadProjectionSnapshot(ctx context.Context, store projectionSnapshotStore, sessionID, workspaceID string) (RunSnapshot, error) {
	run, err := store.GetRun(ctx, sessionID, workspaceID)
	if err != nil {
		return RunSnapshot{}, err
	}
	contract, err := store.GetCurrentContract(ctx, sessionID, workspaceID)
	if err != nil {
		return RunSnapshot{}, err
	}
	method, err := store.GetCurrentMethod(ctx, sessionID, workspaceID)
	if err != nil {
		return RunSnapshot{}, err
	}
	questions, err := store.ListQuestions(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	tasks, err := store.ListTasks(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	attempts, err := store.ListAttempts(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	claims, err := store.ListClaims(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	gate, err := store.EvaluateGate(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	// Projection is an internal read model: do not normalize nil slices here.
	// Empty Sources/Observations must stay nil so the projection surface stays
	// least-privilege. HTTP snapshots go through Engine.Snapshot instead.
	return RunSnapshot{
		Run: run, Contract: contract, Method: method, Questions: questions,
		Tasks: tasks, Attempts: attempts, Claims: claims, Gate: gate,
	}, nil
}

func (e *Engine) SnapshotForAttempt(ctx context.Context, sessionID, workspaceID, attemptID string) (RunSnapshot, error) {
	if e == nil || e.store == nil {
		return RunSnapshot{}, errors.New("research run engine is unavailable")
	}
	snapshot, err := e.store.TaskContextForAttempt(ctx, attemptID, workspaceID)
	if err != nil {
		return RunSnapshot{}, err
	}
	if snapshot.Run.SessionID != sessionID {
		return RunSnapshot{}, ErrRunNotFound
	}
	normalizeRunSnapshot(&snapshot)
	return snapshot, nil
}

func (e *Engine) ListFleetMembers(ctx context.Context, sessionID, workspaceID string) ([]FleetMember, error) {
	return e.store.ListFleetMembers(ctx, sessionID, workspaceID)
}

func (e *Engine) Steer(ctx context.Context, in SteerInput) (Run, error) {
	if isSelectiveSteer(in) {
		outcome, err := e.store.ApplySelectiveSteering(ctx, in)
		if err != nil {
			return Run{}, err
		}
		if len(outcome.Plan.CancelRunningTaskIDs) > 0 {
			if _, err = e.cancelPendingAttempts(ctx, outcome.Run, "research_selective_steering"); err != nil {
				return outcome.Run, err
			}
		}
		objective := "Replan only the explicitly affected Inquiry Branches: " + strings.Join(outcome.Plan.ImpactedBranchIDs, ", ") +
			". Preserve all other current-plan Tasks and every accepted Evidence artifact. Steering reason: " + strings.TrimSpace(in.Reason)
		if _, _, err = e.store.CreateControlTask(ctx, ControlTaskInput{
			SessionID: in.SessionID, Kind: TaskKindReplan, Objective: truncateBytes(objective, maxTaskObjectiveBytes),
			Capability: "lead", Priority: 1, Rationale: "The user selectively redirected canonical Inquiry branches.",
		}); err != nil {
			return outcome.Run, err
		}
		return outcome.Run, reconcileHandoff(e.ReconcileSession(ctx, in.SessionID))
	}
	run, _, _, err := e.store.Steer(ctx, in)
	if err != nil {
		return Run{}, err
	}
	if _, err = e.cancelPendingAttempts(ctx, run, "research_goal_steered"); err != nil {
		return run, err
	}
	if _, _, err = e.store.CreateControlTask(ctx, ControlTaskInput{
		SessionID: in.SessionID, Kind: TaskKindReplan,
		Objective:  "Create a new evidence-oriented plan for the revised user goal. Treat earlier-version artifacts as audit history only.",
		Capability: "lead", Priority: 1, Rationale: "The user changed the durable research goal.",
	}); err != nil {
		return run, err
	}
	return run, reconcileHandoff(e.ReconcileSession(ctx, in.SessionID))
}

// NodeCommand applies continue|fork|retry|reassign from a canvas node, then
// reconciles so ready tasks can dispatch (LRM-1413 / LRM-1408).
func (e *Engine) NodeCommand(ctx context.Context, in NodeCommandInput) (NodeCommandOutcome, error) {
	outcome, err := e.store.NodeCommand(ctx, in)
	if err != nil {
		return NodeCommandOutcome{}, err
	}
	if !outcome.Replayed {
		if recErr := e.ReconcileSession(ctx, in.SessionID); recErr != nil {
			if !errors.Is(recErr, ErrRunLeaseLost) {
				// Command already committed; surface reconcile failure without rolling back.
				return outcome, recErr
			}
		}
	}
	if outcome.Task != nil {
		if latest, getErr := e.store.GetTask(ctx, outcome.Task.ID, in.SessionID); getErr == nil {
			outcome.Task = &latest
			if aid := strings.TrimSpace(latest.AssignedAgentID); aid != "" {
				outcome.Assigned = &aid
			}
			outcome.Queued = latest.Status == TaskStatusReady || latest.Status == TaskStatusPending ||
				latest.Status == TaskStatusDispatching || latest.Status == TaskStatusRunning
		}
	}
	return outcome, nil
}

func reconcileHandoff(err error) error {
	if errors.Is(err, ErrRunLeaseLost) {
		return nil
	}
	return err
}
