package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// EnvCheckpointStatus is the terminal save status of an env checkpoint.
type EnvCheckpointStatus string

const (
	EnvCheckpointSavePending  EnvCheckpointStatus = "pending"
	EnvCheckpointSaveComplete EnvCheckpointStatus = "complete"
	EnvCheckpointSaveFailed   EnvCheckpointStatus = "failed"
	EnvCheckpointSaveTimedOut EnvCheckpointStatus = "timed_out"
)

// EnvCheckpointSaveMode is how a checkpoint captured its environment.
// pause_in_place stops the source instances and resumes them later, so the
// checkpoint can be materialized exactly once. snapshot captures an immutable
// savepoint and leaves the source running, so the checkpoint can be
// materialized into any number of independent lanes.
type EnvCheckpointSaveMode string

const (
	SaveModePauseInPlace EnvCheckpointSaveMode = "pause_in_place"
	SaveModeSnapshot     EnvCheckpointSaveMode = "snapshot"
)

// ResumeTrigger names the in-flight agent task/runtime to re-engage on resume.
// Captured at checkpoint-create time (server-side resolved from the project's
// in-flight task) and executed by ResumeFromCheckpoint after the sandbox
// containers resume, so a resumed rollout continues its task from the
// checkpointed state instead of stalling on a sandbox with no agent runtime.
type ResumeTrigger struct {
	TaskID        string `json:"task_id"`
	RuntimeID     string `json:"runtime_id"`
	AgentID       string `json:"agent_id"`
	IssueID       string `json:"issue_id,omitempty"`
	ChatSessionID string `json:"chat_session_id,omitempty"`
	ProjectID     string `json:"project_id"`
	Kind          string `json:"kind"` // "issue" | "chat"
}

// TriggerStatus reports whether ResumeFromCheckpoint re-engaged the agent runtime.
type TriggerStatus string

const (
	TriggerExecuted      TriggerStatus = "executed"
	TriggerSkippedLegacy TriggerStatus = "skipped_legacy"
	TriggerFailed        TriggerStatus = "failed"
)

// InFlightTaskResolver resolves a project's in-flight (running/dispatched)
// agent tasks at checkpoint-create time so the resume-trigger descriptor can be
// populated server-side (the caller does not know multica-internal task ids).
type InFlightTaskResolver interface {
	ListInFlightTasksForProject(ctx context.Context, workspaceID, projectID string) ([]ResumeTrigger, error)
}

// LaneRef identifies one materialized lane for forked continuation. The zero
// value means "no lane", which is same-runtime continuation.
type LaneRef struct {
	// LaneKey identifies the lane for idempotency. It is not an env id: in
	// fan-out it is an anchor plus an ordinal, so the env id travels separately.
	LaneKey         string
	LaneEnvID       string
	InstanceID      string
	ProjectID       string
	RuntimeID       string
	AgentID         string
	ChannelID       string
	ChatSessionID   string
	SourceMessageID string
	// SharedWorkdirEnvID, when non-empty, is the sample env whose shared
	// workdir the enqueued run anchors to (research D5); empty keeps the
	// per-agent workdir root.
	SharedWorkdirEnvID string
}

// ContinuationRequest is the uniform input of the continuation seam. Trigger
// describes same-runtime continuation and is unused by forked continuation,
// which is described entirely by Lane.
type ContinuationRequest struct {
	Trigger     ResumeTrigger
	Lane        LaneRef
	WorkspaceID string
	ActorUserID string
	// Index is the caller's rollout index, passed through to the enqueue seam.
	Index int
}

// ContinuationOutcome is the uniform result of the continuation seam, so a
// failed continuation after a successful restore stays visible as a partial
// resume instead of being reported as an outright failure.
type ContinuationOutcome struct {
	Status  TriggerStatus
	TaskID  string
	LaneKey string
}

// ResumeAgentRunner is the single continuation seam for re-engaging an agent
// after its environment is restored. Exactly one strategy is selected per
// resume, by the checkpoint's save mode. A nil strategy is a loud error for
// non-empty triggers (resume without one would silently no-op and return a
// handle AReaL cannot actually use).
type ResumeAgentRunner interface {
	Mode() EnvCheckpointSaveMode
	ResumeAgentRun(ctx context.Context, req ContinuationRequest) (ContinuationOutcome, error)
}

// ContinuationRegistry holds one strategy per save mode.
type ContinuationRegistry struct {
	SameRuntime ResumeAgentRunner
	Forked      ResumeAgentRunner
}

// For selects the single strategy for a save mode. An empty mode is a
// pre-change row and resolves to pause_in_place, so existing checkpoints keep
// their behavior.
func (r ContinuationRegistry) For(mode EnvCheckpointSaveMode) ResumeAgentRunner {
	if mode == SaveModeSnapshot {
		return r.Forked
	}
	return r.SameRuntime
}

// EnvCheckpointCreateInput is the service-layer input for creating a checkpoint.
type EnvCheckpointCreateInput struct {
	WorkspaceID   string
	ProjectID     string
	EventRef      string
	Kind          string
	SaveMode      EnvCheckpointSaveMode
	EnvIDMap      map[string]string
	SandboxRefs   []SandboxInstanceRef
	DBSnapshot    json.RawMessage
	ResumeTrigger json.RawMessage
	EntropyScore  *float64
	ActorUserID   string
	SaveTimeout   time.Duration
}

// EnvCheckpoint is a snapshot of an env_checkpoint row.
type EnvCheckpoint struct {
	ID            string
	WorkspaceID   string
	ProjectID     string
	EventRef      string
	Kind          string
	SaveMode      EnvCheckpointSaveMode
	EnvIDMap      map[string]string
	SandboxRefs   []SandboxInstanceRef
	DBSnapshot    json.RawMessage
	ResumeTrigger json.RawMessage
	EntropyScore  *float64
	SaveTimeoutMs int
	SaveStatus    EnvCheckpointStatus
	SaveError     string
	CreatedAt     time.Time
	UpdatedAt     time.Time

	// SourceChannelID is the conversation this checkpoint was taken from, which
	// standalone fan-out copies per lane. It has no column yet: the migration
	// that records it ships with the fan-out capability itself (design D8), so
	// every checkpoint written today reports none and fan-out is refused. The
	// rule is expressed against this field rather than as a temporary
	// unconditional refusal so the capability arrives by populating it, with no
	// guard to remember to remove.
	SourceChannelID string
}

// EnvCheckpointRepository is the persistence seam for env checkpoints.
type EnvCheckpointRepository interface {
	CreateCheckpoint(ctx context.Context, in EnvCheckpointCreateInput, status EnvCheckpointStatus, saveErr string) (EnvCheckpoint, error)
	UpdateCheckpointSaveStatus(ctx context.Context, checkpointID, workspaceID string, status EnvCheckpointStatus, saveErr string) (EnvCheckpoint, error)
	GetCheckpoint(ctx context.Context, checkpointID, workspaceID string) (EnvCheckpoint, error)
	ListCheckpoints(ctx context.Context, workspaceID, projectID string) ([]EnvCheckpoint, error)
	DeleteCheckpoint(ctx context.Context, checkpointID, workspaceID string) error
}

// SavepointReleaser schedules a savepoint's Cube template for deletion through
// the existing delete_template job. Releasing is separate from deleting the
// checkpoint row because the row is what records that the template exists: drop
// it first and the template is leaked with nothing left pointing at it.
type SavepointReleaser interface {
	ReleaseSavepoint(ctx context.Context, snapshotID, workspaceID, actorUserID string) error
}

// SandboxInstanceSaver saves (stops) a sandbox instance and blocks until the
// save reaches a terminal state or the context expires. The production adapter
// wraps EnvSandboxLifecycleService.Save (enqueue stop job) + status polling.
type SandboxInstanceSaver interface {
	Save(ctx context.Context, ref SandboxInstanceRef, actorUserID string) error
}

// Savepoint is one immutable snapshot record owned by a checkpoint.
type Savepoint struct {
	SnapshotID     string
	CubeSnapshotID string
	InstanceID     string
	Status         string // creating | ready | failed | deleting
}

// SavepointCreator creates a savepoint from a live sandbox instance through the
// existing create_template job and blocks until the snapshot record reaches a
// terminal state. Unlike SandboxInstanceSaver, the source instance is left
// running, which is what lets a snapshot-mode checkpoint be materialized more
// than once.
type SavepointCreator interface {
	CreateSavepoint(ctx context.Context, ref SandboxInstanceRef, checkpointID, actorUserID string) (Savepoint, error)
}

// ErrSavepointFailed reports a savepoint that reached a non-ready terminal
// state, which fails the whole checkpoint save.
var ErrSavepointFailed = errors.New("savepoint_failed")

// SandboxInstanceResumer resumes a previously-saved sandbox instance. The
// production adapter wraps EnvSandboxLifecycleService.Resume (enqueue resume
// job). Unlike Save, Resume is fire-and-forget: the job is enqueued and the
// caller returns a continuation handle for AReaL to poll.
type SandboxInstanceResumer interface {
	Resume(ctx context.Context, ref SandboxInstanceRef, actorUserID string) error
}

// ProjectSnapshotReader captures an inline JSONB snapshot of a project subtree
// (issues, chat sessions, messages) for checkpoint storage.
type ProjectSnapshotReader interface {
	CaptureProjectSnapshot(ctx context.Context, workspaceID, projectID string) (json.RawMessage, error)
}

// ResumeFromCheckpointInput is the service-layer input for resuming a
// checkpoint. LaneCount is how many independent continuations to materialize:
// pause_in_place can only ever produce one, since materializing it consumes the
// paused instances, while snapshot can produce any number from its savepoints.
//
// LaneKeyAnchor must be stable across retries of the same logical request, since
// lane keys derive from it and the per-lane unique index is what makes a retried
// resume idempotent rather than duplicative.
type ResumeFromCheckpointInput struct {
	WorkspaceID   string
	CheckpointID  string
	ActorUserID   string
	LaneCount     int
	LaneKeyAnchor string
}

// ResumeLane is one materialized continuation of a snapshot-mode checkpoint.
type ResumeLane struct {
	LaneKey       string
	Status        string // provisioning | ready | failed
	InstanceID    string
	ProjectID     string
	RuntimeID     string
	TaskID        string
	EnvID         string
	ChatSessionID string
	TriggerStatus TriggerStatus
	Error         string
}

// ResumeStatus summarizes a fan-out resume. It is empty for pause_in_place,
// which has a single outcome already carried by TriggerStatus and no lanes to
// summarize.
type ResumeStatus string

const (
	// ResumeCompleted means every requested lane is ready and triggered.
	ResumeCompleted ResumeStatus = "completed"
	// ResumePartial means at least one lane is usable and at least one is not.
	// The caller decides whether that is enough, which is why the per-lane
	// detail is reported rather than collapsed.
	ResumePartial ResumeStatus = "partial"
)

// ResumeFromCheckpointResult is the continuation handle returned by
// ResumeFromCheckpoint. AReaL uses RolloutHandle to re-enter the rollout;
// TriggerStatus reports whether the agent runtime was re-engaged. Lanes and
// Status are additive and stay empty for pause_in_place, which has nothing to fan
// out, so its result is unchanged.
type ResumeFromCheckpointResult struct {
	CheckpointID  string
	ProjectID     string
	EnvIDMap      map[string]string
	SandboxRefs   []SandboxInstanceRef
	RolloutHandle string
	TriggerStatus TriggerStatus
	Lanes         []ResumeLane
	Status        ResumeStatus
}

var (
	// ErrCheckpointNotResumable marks a permanent rejection: the checkpoint's
	// state can never satisfy this resume, so retrying is pointless. Callers
	// must be able to tell it apart from a transient failure.
	ErrCheckpointNotResumable = errors.New("checkpoint_not_resumable")
	// ErrLaneCountInvalid marks a bad request rather than a bad checkpoint.
	ErrLaneCountInvalid = errors.New("lane_count_invalid")
	// ErrCheckpointHasProvisioningLanes refuses deletion while a lane is still
	// materializing. Retrying once those lanes settle succeeds, so this is a
	// conflict rather than a permanent rejection.
	ErrCheckpointHasProvisioningLanes = errors.New("checkpoint_has_provisioning_lanes")
)

// EnvCheckpointService orchestrates checkpoint creation, save, and retrieval.
type EnvCheckpointService struct {
	repo          EnvCheckpointRepository
	saver         SandboxInstanceSaver
	resumer       SandboxInstanceResumer
	snapshot      ProjectSnapshotReader
	inFlight      InFlightTaskResolver
	continuations ContinuationRegistry
	savepoints    SavepointCreator

	// Fan-out seams, all three installed together by WithLanes.
	lanes           EnvCheckpointLaneRepository
	materializer    LaneMaterializer
	savepointReader SavepointReader

	// savepointReleaser is only needed to delete a snapshot checkpoint, so it
	// stays separate from WithLanes rather than forcing every caller that reads
	// lanes to supply one.
	savepointReleaser SavepointReleaser
}

// WithSavepointReleaser injects the seam that schedules a savepoint's Cube
// template for deletion.
func (s *EnvCheckpointService) WithSavepointReleaser(r SavepointReleaser) *EnvCheckpointService {
	s.savepointReleaser = r
	return s
}

func NewEnvCheckpointService(repo EnvCheckpointRepository, saver SandboxInstanceSaver, resumer SandboxInstanceResumer, snapshot ProjectSnapshotReader, inFlight InFlightTaskResolver, continuations ContinuationRegistry) *EnvCheckpointService {
	return &EnvCheckpointService{repo: repo, saver: saver, resumer: resumer, snapshot: snapshot, inFlight: inFlight, continuations: continuations}
}

// WithSavepointCreator injects the snapshot-mode savepoint seam. Without one,
// snapshot-mode create is refused as unconfigured rather than silently
// downgraded to pause_in_place, which would stop instances the caller expected
// to keep running. pause_in_place is unaffected.
func (s *EnvCheckpointService) WithSavepointCreator(c SavepointCreator) *EnvCheckpointService {
	s.savepoints = c
	return s
}

// Create records a checkpoint candidate, saves each sandbox instance with the
// configured timeout, then persists the terminal save status. A save that
// exceeds the timeout records timed_out; a save error records failed; all
// saves completing records complete. The resume-trigger descriptor is resolved
// server-side from the project's in-flight task (D5) so the caller does not
// need to know multica-internal task ids.
func (s *EnvCheckpointService) Create(ctx context.Context, in EnvCheckpointCreateInput) (EnvCheckpoint, error) {
	if in.WorkspaceID == "" || in.ProjectID == "" {
		return EnvCheckpoint{}, fmt.Errorf("validation_failed: workspace_id and project_id are required")
	}
	if in.SaveTimeout <= 0 {
		return EnvCheckpoint{}, fmt.Errorf("validation_failed: save_timeout must be positive")
	}
	// Checkpoint save/resume only operates on sandbox_instance refs. A request
	// with no refs is a Fleet-only env, which is not checkpointable (D7).
	if len(in.SandboxRefs) == 0 {
		return EnvCheckpoint{}, fmt.Errorf("validation_failed: checkpoint requires sandbox_instance refs (Fleet-only envs are not checkpointable)")
	}
	// An absent mode is pause_in_place, so existing callers keep their
	// behavior. Normalizing here also keeps an unknown mode from reaching the
	// save_mode CHECK constraint as an opaque database error.
	if in.SaveMode == "" {
		in.SaveMode = SaveModePauseInPlace
	}
	switch in.SaveMode {
	case SaveModePauseInPlace, SaveModeSnapshot:
	default:
		return EnvCheckpoint{}, fmt.Errorf("validation_failed: save_mode must be pause_in_place or snapshot")
	}
	if in.SaveMode == SaveModeSnapshot && s.savepoints == nil {
		return EnvCheckpoint{}, fmt.Errorf("validation_failed: snapshot save_mode requires a savepoint creator")
	}

	snapshot, err := s.snapshot.CaptureProjectSnapshot(ctx, in.WorkspaceID, in.ProjectID)
	if err != nil {
		return EnvCheckpoint{}, fmt.Errorf("capture project snapshot: %w", err)
	}
	in.DBSnapshot = snapshot

	// Resolve the resume-trigger descriptor server-side from the project's
	// in-flight (running/dispatched) task. v1 captures a single descriptor
	// (group_size=1); a project with no in-flight task yields an empty trigger,
	// which degrades to sandbox-only resume (legacy) on ResumeFromCheckpoint.
	if s.inFlight != nil {
		triggers, err := s.inFlight.ListInFlightTasksForProject(ctx, in.WorkspaceID, in.ProjectID)
		if err != nil {
			return EnvCheckpoint{}, fmt.Errorf("resolve in-flight tasks: %w", err)
		}
		if len(triggers) > 0 {
			raw, err := json.Marshal(triggers[0])
			if err != nil {
				return EnvCheckpoint{}, fmt.Errorf("marshal resume_trigger: %w", err)
			}
			in.ResumeTrigger = raw
		}
	}

	cp, err := s.repo.CreateCheckpoint(ctx, in, EnvCheckpointSavePending, "")
	if err != nil {
		return EnvCheckpoint{}, fmt.Errorf("create checkpoint: %w", err)
	}

	saveCtx, cancel := context.WithTimeout(ctx, in.SaveTimeout)
	defer cancel()

	status := EnvCheckpointSaveComplete
	var saveErr string
	for _, ref := range in.SandboxRefs {
		var err error
		if in.SaveMode == SaveModeSnapshot {
			// One savepoint per source instance, owned by this checkpoint. The
			// source is left running.
			var sp Savepoint
			sp, err = s.savepoints.CreateSavepoint(saveCtx, ref, cp.ID, in.ActorUserID)
			if err == nil && sp.Status != "ready" {
				err = fmt.Errorf("%w: savepoint %s status %s", ErrSavepointFailed, sp.SnapshotID, sp.Status)
			}
		} else {
			err = s.saver.Save(saveCtx, ref, in.ActorUserID)
		}
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				status = EnvCheckpointSaveTimedOut
			} else {
				status = EnvCheckpointSaveFailed
			}
			saveErr = err.Error()
			break
		}
	}

	cp, err = s.repo.UpdateCheckpointSaveStatus(ctx, cp.ID, in.WorkspaceID, status, saveErr)
	if err != nil {
		return EnvCheckpoint{}, fmt.Errorf("update checkpoint save status: %w", err)
	}
	return cp, nil
}

// Get retrieves a single checkpoint by ID, scoped to the workspace.
func (s *EnvCheckpointService) Get(ctx context.Context, checkpointID, workspaceID string) (EnvCheckpoint, error) {
	return s.repo.GetCheckpoint(ctx, checkpointID, workspaceID)
}

// List returns checkpoints for a project, newest first, scoped to the workspace.
func (s *EnvCheckpointService) List(ctx context.Context, workspaceID, projectID string) ([]EnvCheckpoint, error) {
	return s.repo.ListCheckpoints(ctx, workspaceID, projectID)
}

// ResumeFromCheckpoint loads a checkpoint, requires save_status == complete,
// resumes each sandbox instance, and returns a continuation handle. Incomplete
// (pending/timed_out/failed) checkpoints are rejected without enqueueing any
// resume jobs. A missing resumer is a loud error - resume without a resumer
// would silently no-op and return a handle AReaL cannot actually use.
//
// After the sandbox containers resume, the checkpoint's resume_trigger (if any)
// is executed via the ResumeAgentRunner seam to re-engage the agent runtime so
// the resumed rollout continues its in-flight task. An empty resume_trigger
// (pre-change / no in-flight task) degrades to sandbox-only resume and reports
// TriggerSkippedLegacy; a trigger failure reports TriggerFailed (partial
// resume: sandboxes resumed, agent runtime not re-engaged).
// Fan-out (LaneCount > 1) is only available to snapshot mode; pause_in_place is
// rejected with ErrLaneCountInvalid, since materializing it consumes the paused
// instances and so can only happen once.
// Delete releases the checkpoint's savepoints and removes the row; its lanes and
// savepoint ownership rows cascade with it.
//
// Two orderings matter. Savepoints are released *before* the row goes, because
// the row is the only record that those Cube templates exist -- delete it first
// and a failed release leaks them with nothing left to retry from. And deletion
// is refused while any lane is still provisioning: that lane row is the only
// record of a sandbox being built, so cascading it away would orphan the
// sandbox, which is what the lane status column exists to prevent (design D4).
func (s *EnvCheckpointService) Delete(ctx context.Context, workspaceID, checkpointID, actorUserID string) error {
	if workspaceID == "" || checkpointID == "" {
		return fmt.Errorf("validation_failed: workspace_id and checkpoint_id are required")
	}
	cp, err := s.repo.GetCheckpoint(ctx, checkpointID, workspaceID)
	if err != nil {
		return err
	}
	if s.lanes != nil {
		provisioning, err := s.lanes.CountProvisioningLanes(ctx, cp.ID, workspaceID)
		if err != nil {
			return fmt.Errorf("count provisioning lanes: %w", err)
		}
		if provisioning > 0 {
			return fmt.Errorf("%w: %d lane(s) still materializing", ErrCheckpointHasProvisioningLanes, provisioning)
		}
	}
	if err := s.releaseSavepoints(ctx, cp, workspaceID, actorUserID); err != nil {
		return err
	}
	return s.repo.DeleteCheckpoint(ctx, cp.ID, workspaceID)
}

// releaseSavepoints schedules every template this checkpoint owns for deletion.
// A pause_in_place checkpoint owns none, so it needs no releaser; a snapshot
// checkpoint that owns some and has no releaser is refused rather than deleted,
// since deleting it would leak them.
func (s *EnvCheckpointService) releaseSavepoints(ctx context.Context, cp EnvCheckpoint, workspaceID, actorUserID string) error {
	if s.savepointReader == nil {
		return nil
	}
	savepoints, err := s.savepointReader.ListSavepoints(ctx, cp.ID, workspaceID)
	if err != nil {
		return fmt.Errorf("list savepoints: %w", err)
	}
	if len(savepoints) == 0 {
		return nil
	}
	if s.savepointReleaser == nil {
		return fmt.Errorf("unconfigured: checkpoint %s owns %d savepoint(s) but no releaser is installed",
			cp.ID, len(savepoints))
	}
	for _, sp := range savepoints {
		if err := s.savepointReleaser.ReleaseSavepoint(ctx, sp.SnapshotID, workspaceID, actorUserID); err != nil {
			return fmt.Errorf("release savepoint %s: %w", sp.SnapshotID, err)
		}
	}
	return nil
}

func (s *EnvCheckpointService) ResumeFromCheckpoint(ctx context.Context, in ResumeFromCheckpointInput) (ResumeFromCheckpointResult, error) {
	if s.resumer == nil {
		return ResumeFromCheckpointResult{}, fmt.Errorf("validation_failed: resume is not configured (no sandbox resumer)")
	}
	// Validated before the checkpoint is even loaded, so a bad lane count can
	// never have a side effect.
	if in.LaneCount < 1 {
		return ResumeFromCheckpointResult{}, fmt.Errorf("validation_failed: %w: lane_count must be at least 1, got %d", ErrLaneCountInvalid, in.LaneCount)
	}
	cp, err := s.repo.GetCheckpoint(ctx, in.CheckpointID, in.WorkspaceID)
	if err != nil {
		return ResumeFromCheckpointResult{}, fmt.Errorf("not found: %w", err)
	}
	if cp.SaveStatus != EnvCheckpointSaveComplete {
		return ResumeFromCheckpointResult{}, fmt.Errorf("validation_failed: %w: save_status is %s, must be complete to resume", ErrCheckpointNotResumable, cp.SaveStatus)
	}
	// An empty save mode is a pre-change row, which resolves to pause_in_place
	// so existing checkpoints keep their behavior — including their inability to
	// fan out.
	mode := cp.SaveMode
	if mode == "" {
		mode = SaveModePauseInPlace
	}
	if mode == SaveModePauseInPlace && in.LaneCount > 1 {
		return ResumeFromCheckpointResult{}, fmt.Errorf("validation_failed: %w: pause_in_place cannot fan out (lane_count=%d)", ErrLaneCountInvalid, in.LaneCount)
	}
	if mode == SaveModeSnapshot {
		return s.resumeSnapshotLanes(ctx, cp, in)
	}
	return s.resumePauseInPlace(ctx, cp, in)
}

// resumeSnapshotLanes materializes one independent lane per requested lane from
// the checkpoint's savepoints. The savepoints are read once and reused by every
// lane: fanning out to N lanes must not take N snapshots, which is the whole
// point of paying for a savepoint at capture time.
func (s *EnvCheckpointService) resumeSnapshotLanes(ctx context.Context, cp EnvCheckpoint, in ResumeFromCheckpointInput) (ResumeFromCheckpointResult, error) {
	if s.lanes == nil || s.materializer == nil || s.savepointReader == nil {
		return ResumeFromCheckpointResult{}, fmt.Errorf("validation_failed: snapshot fan-out resume is not configured")
	}
	savepoints, err := s.savepointReader.ListSavepoints(ctx, cp.ID, in.WorkspaceID)
	if err != nil {
		return ResumeFromCheckpointResult{}, fmt.Errorf("list savepoints: %w", err)
	}
	// A snapshot-mode checkpoint with no savepoint can never be materialized, so
	// this is permanent rather than transient.
	if len(savepoints) == 0 {
		return ResumeFromCheckpointResult{}, fmt.Errorf("validation_failed: %w: checkpoint owns no savepoint", ErrCheckpointNotResumable)
	}
	// Design D8: every lane continues its own copy of the source conversation. A
	// checkpoint that did not record which conversation that was could only be
	// served from the source's own channel, putting every lane in one thread --
	// the opposite of independent continuations. A single lane is exempt because
	// its caller supplies the conversation (branch dispatch pre-seeds the lane
	// row, design D6).
	if in.LaneCount > 1 && cp.SourceChannelID == "" {
		return ResumeFromCheckpointResult{}, fmt.Errorf(
			"validation_failed: %w: checkpoint recorded no source conversation to fan out from",
			ErrCheckpointNotResumable)
	}
	result := ResumeFromCheckpointResult{
		CheckpointID:  cp.ID,
		ProjectID:     cp.ProjectID,
		EnvIDMap:      cp.EnvIDMap,
		SandboxRefs:   cp.SandboxRefs,
		RolloutHandle: fmt.Sprintf("resume:%s", cp.ID),
	}
	var trigger ResumeTrigger
	hasTrigger := len(cp.ResumeTrigger) > 0
	if hasTrigger {
		if err := json.Unmarshal(cp.ResumeTrigger, &trigger); err != nil {
			result.TriggerStatus = TriggerFailed
			return result, fmt.Errorf("unmarshal resume_trigger: %w", err)
		}
	}
	strategy := s.continuations.For(SaveModeSnapshot)

	usable, triggered := 0, 0
	var firstErr error
	for i := 0; i < in.LaneCount; i++ {
		lane, err := s.materializeLane(ctx, cp, in,
			laneKeyForOrdinal(in.LaneKeyAnchor, i), savepoints[0], trigger, hasTrigger, strategy, i)
		result.Lanes = append(result.Lanes, lane)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if lane.Status == LaneStatusReady {
			usable++
			if lane.TriggerStatus == TriggerExecuted {
				triggered++
			}
		}
	}

	// Nothing usable came out of it, so this is a failure and not a fan-out with
	// zero lanes. The first lane's cause is wrapped so a typed failure such as a
	// vanished savepoint stays recognizable.
	if usable == 0 {
		result.TriggerStatus = TriggerFailed
		result.Status = ResumePartial
		if firstErr != nil {
			return result, fmt.Errorf("resume: all %d requested lanes failed: %w", in.LaneCount, firstErr)
		}
		return result, fmt.Errorf("resume: all %d requested lanes failed", in.LaneCount)
	}
	if !hasTrigger {
		result.TriggerStatus = TriggerSkippedLegacy
		result.Status = ResumeCompleted
		if usable < len(result.Lanes) {
			result.Status = ResumePartial
		}
		return result, nil
	}
	// A partial fan-out is reported as such rather than as success: the caller
	// owns the decision about whether fewer lanes than it asked for is workable.
	if triggered < len(result.Lanes) {
		result.TriggerStatus = TriggerFailed
		result.Status = ResumePartial
		return result, nil
	}
	result.TriggerStatus = TriggerExecuted
	result.Status = ResumeCompleted
	return result, nil
}

// resumePauseInPlace resumes the paused instances themselves. This is the
// pre-existing resume path, unchanged.
func (s *EnvCheckpointService) resumePauseInPlace(ctx context.Context, cp EnvCheckpoint, in ResumeFromCheckpointInput) (ResumeFromCheckpointResult, error) {
	workspaceID, actorUserID := in.WorkspaceID, in.ActorUserID
	for _, ref := range cp.SandboxRefs {
		if err := s.resumer.Resume(ctx, ref, actorUserID); err != nil {
			return ResumeFromCheckpointResult{}, fmt.Errorf("resume sandbox %s: %w", ref.InstanceID, err)
		}
	}
	result := ResumeFromCheckpointResult{
		CheckpointID:  cp.ID,
		ProjectID:     cp.ProjectID,
		EnvIDMap:      cp.EnvIDMap,
		SandboxRefs:   cp.SandboxRefs,
		RolloutHandle: fmt.Sprintf("resume:%s", cp.ID),
	}
	// Legacy / no in-flight task: sandbox-only resume, no agent re-engagement.
	if len(cp.ResumeTrigger) == 0 {
		result.TriggerStatus = TriggerSkippedLegacy
		return result, nil
	}
	// Non-empty trigger requires a strategy; nil is a loud error (would no-op
	// and return a handle AReaL cannot use). Sandboxes are already resumed
	// above, so this is a partial-resume error.
	strategy := s.continuations.For(cp.SaveMode)
	if strategy == nil {
		return ResumeFromCheckpointResult{}, fmt.Errorf("validation_failed: non-empty resume_trigger but no continuation strategy configured for save_mode %q", cp.SaveMode)
	}
	var trigger ResumeTrigger
	if err := json.Unmarshal(cp.ResumeTrigger, &trigger); err != nil {
		result.TriggerStatus = TriggerFailed
		return result, fmt.Errorf("unmarshal resume_trigger: %w", err)
	}
	outcome, err := strategy.ResumeAgentRun(ctx, ContinuationRequest{
		Trigger:     trigger,
		WorkspaceID: workspaceID,
		ActorUserID: actorUserID,
	})
	if err != nil {
		result.TriggerStatus = TriggerFailed
		return result, fmt.Errorf("resume agent run: %w", err)
	}
	result.TriggerStatus = outcome.Status
	return result, nil
}
