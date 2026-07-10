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

// ResumeAgentRunner re-activates an existing in-flight task against its resumed
// agent_runtime. Mirrors SandboxInstanceResumer injection. A nil runner is a
// loud error for non-empty triggers (resume without a runner would silently
// no-op and return a handle AReaL cannot actually use).
type ResumeAgentRunner interface {
	ResumeAgentRun(ctx context.Context, trigger ResumeTrigger) error
}

// EnvCheckpointCreateInput is the service-layer input for creating a checkpoint.
type EnvCheckpointCreateInput struct {
	WorkspaceID   string
	ProjectID     string
	EventRef      string
	Kind          string
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
}

// EnvCheckpointRepository is the persistence seam for env checkpoints.
type EnvCheckpointRepository interface {
	CreateCheckpoint(ctx context.Context, in EnvCheckpointCreateInput, status EnvCheckpointStatus, saveErr string) (EnvCheckpoint, error)
	UpdateCheckpointSaveStatus(ctx context.Context, checkpointID, workspaceID string, status EnvCheckpointStatus, saveErr string) (EnvCheckpoint, error)
	GetCheckpoint(ctx context.Context, checkpointID, workspaceID string) (EnvCheckpoint, error)
	ListCheckpoints(ctx context.Context, workspaceID, projectID string) ([]EnvCheckpoint, error)
}

// SandboxInstanceSaver saves (stops) a sandbox instance and blocks until the
// save reaches a terminal state or the context expires. The production adapter
// wraps EnvSandboxLifecycleService.Save (enqueue stop job) + status polling.
type SandboxInstanceSaver interface {
	Save(ctx context.Context, ref SandboxInstanceRef, actorUserID string) error
}

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

// ResumeFromCheckpointResult is the continuation handle returned by
// ResumeFromCheckpoint. AReaL uses RolloutHandle to re-enter the rollout;
// TriggerStatus reports whether the agent runtime was re-engaged.
type ResumeFromCheckpointResult struct {
	CheckpointID  string
	ProjectID     string
	EnvIDMap      map[string]string
	SandboxRefs   []SandboxInstanceRef
	RolloutHandle string
	TriggerStatus TriggerStatus
}

// EnvCheckpointService orchestrates checkpoint creation, save, and retrieval.
type EnvCheckpointService struct {
	repo        EnvCheckpointRepository
	saver       SandboxInstanceSaver
	resumer     SandboxInstanceResumer
	snapshot    ProjectSnapshotReader
	inFlight    InFlightTaskResolver
	resumeAgent ResumeAgentRunner
}

func NewEnvCheckpointService(repo EnvCheckpointRepository, saver SandboxInstanceSaver, resumer SandboxInstanceResumer, snapshot ProjectSnapshotReader, inFlight InFlightTaskResolver, resumeAgent ResumeAgentRunner) *EnvCheckpointService {
	return &EnvCheckpointService{repo: repo, saver: saver, resumer: resumer, snapshot: snapshot, inFlight: inFlight, resumeAgent: resumeAgent}
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
		if err := s.saver.Save(saveCtx, ref, in.ActorUserID); err != nil {
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
func (s *EnvCheckpointService) ResumeFromCheckpoint(ctx context.Context, workspaceID, checkpointID, actorUserID string) (ResumeFromCheckpointResult, error) {
	if s.resumer == nil {
		return ResumeFromCheckpointResult{}, fmt.Errorf("validation_failed: resume is not configured (no sandbox resumer)")
	}
	cp, err := s.repo.GetCheckpoint(ctx, checkpointID, workspaceID)
	if err != nil {
		return ResumeFromCheckpointResult{}, fmt.Errorf("not found: %w", err)
	}
	if cp.SaveStatus != EnvCheckpointSaveComplete {
		return ResumeFromCheckpointResult{}, fmt.Errorf("validation_failed: checkpoint save_status is %s, must be complete to resume", cp.SaveStatus)
	}
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
	// Non-empty trigger requires a runner; nil is a loud error (would no-op and
	// return a handle AReaL cannot use). Sandboxes are already resumed above, so
	// this is a partial-resume error.
	if s.resumeAgent == nil {
		return ResumeFromCheckpointResult{}, fmt.Errorf("validation_failed: non-empty resume_trigger but no resume agent runner configured")
	}
	var trigger ResumeTrigger
	if err := json.Unmarshal(cp.ResumeTrigger, &trigger); err != nil {
		result.TriggerStatus = TriggerFailed
		return result, fmt.Errorf("unmarshal resume_trigger: %w", err)
	}
	if err := s.resumeAgent.ResumeAgentRun(ctx, trigger); err != nil {
		result.TriggerStatus = TriggerFailed
		return result, fmt.Errorf("resume agent run: %w", err)
	}
	result.TriggerStatus = TriggerExecuted
	return result, nil
}
