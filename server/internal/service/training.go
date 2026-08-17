// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/arealrl"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// arealProxyProvider / arealProxyModel are the fixed provider/model the trained
// runtime is launched with: `pi --provider areal --model areal-default`. The
// api_key and base_url are per-session (mint + deployment config).
const (
	arealProxyProvider = "openai"
	arealProxyModel    = "areal-default"
	// arealProxyContextKey is the top-level key under which the RL proxy config
	// is merged into agent_inbox_event.context.
	arealProxyContextKey = "areal_proxy"
)

// arealProxyConfig is the JSON object stored at context.areal_proxy on a
// trained task. The daemon execenv (Task 6) reads it at claim time to launch
// the runtime against the RL proxy. SessionID is retained so the close hook
// (Task 7) — which authenticates with the per-session proxy key (APIKey) — can
// correlate the trajectory.
type arealProxyConfig struct {
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	APIKey    string `json:"api_key"`
	BaseURL   string `json:"base_url"`
	SessionID string `json:"session_id"`
}

// trainingDispatchLookup resolves the per-project training intent persisted by
// env_dispatch (Task 3). *db.Queries satisfies it.
type trainingDispatchLookup interface {
	GetTrainingDispatchByProject(ctx context.Context, projectID pgtype.UUID) (db.TrainingDispatch, error)
}

// trainingTaskStore reads a task's current context (for the idempotency guard)
// and merges the areal_proxy config back in. *db.Queries satisfies it.
type trainingTaskStore interface {
	GetAgentTask(ctx context.Context, id pgtype.UUID) (db.AgentInboxEvent, error)
	MergeTaskArealProxyContext(ctx context.Context, arg db.MergeTaskArealProxyContextParams) error
}

// Compile-time guarantee that *db.Queries satisfies trainingTaskStore, so a
// generated-method signature drift fails at compile time now, not at U10 wiring.
var _ trainingTaskStore = (*db.Queries)(nil)

// arealSessionStarter opens an RL session for a task via the bridge. The real
// implementation is *arealrl.Client; tests inject a fake. The helper never
// constructs a real client — it is injected via TrainingSessionDeps.
type arealSessionStarter interface {
	// StartSession opens an RL session; sessionRef is the canonical session
	// reference (task id for the legacy training flow, source-binding id for
	// env-dispatch), forwarded to the bridge as "session_ref".
	StartSession(ctx context.Context, sessionRef, envID string) (arealrl.SessionCreds, error)
}

// arealSessionCloser finalizes an RL session by setting its reward via the
// bridge. The real implementation is *arealrl.Client; tests inject a fake.
//
// SetReward is the whole close protocol: AReaL v2 has no end_session route, and
// reclaims the session itself once the trajectory is exported.
type arealSessionCloser interface {
	SetReward(ctx context.Context, proxyKey string, reward float64) error
}

// criticTaskCreator creates critic tasks and reads existing ones for the
// critic-spawn hook's idempotency guard. *db.Queries satisfies it.
type criticTaskCreator interface {
	FindCriticTaskForTrained(ctx context.Context, trainedTaskID string) (db.AgentInboxEvent, error)
	CreateCriticTask(ctx context.Context, arg db.CreateCriticTaskParams) (db.AgentInboxEvent, error)
}

// TrainingSessionDeps bundles the collaborators the session-open/close hooks need.
// A nil *TrainingSessionDeps means training is not configured for this
// deployment, in which case the hook is a no-op. Construction/wiring of the
// real client + proxy URL is finalized in Task 8 (config).
// CheckpointTrigger creates an env checkpoint for a trained rollout's
// structural event. The training service calls it when a trained task reaches
// a terminal state that is eligible for checkpointing (not sweeper-failed,
// not autopilot, not a sandbox lifecycle job). Errors are logged but do not
// block the terminal routing flow.
type CheckpointTrigger interface {
	TriggerCheckpoint(ctx context.Context, task db.AgentInboxEvent, projectID pgtype.UUID) error
}

type TrainingSessionDeps struct {
	Lookup            trainingDispatchLookup
	Store             trainingTaskStore
	Creator           criticTaskCreator
	RL                arealSessionStarter
	Closer            arealSessionCloser
	ProxyURL          string
	DefaultReward     float64 // Fallback reward if training dispatch not available
	CheckpointTrigger CheckpointTrigger
	// DAG, when non-nil, records interaction-DAG segments + edges for trained
	// rollouts (U7.2). The service no-ops when its enabled flag is false
	// (INTERACTION_DAG_ENABLED), so a nil-vs-non-nil DAG is the first gate and
	// the enabled flag is the second. Recording is best-effort: a DAG recording
	// error is logged and never fails the session open/close. Production
	// construction (NewInteractionDAGService with the real arealrl client) is
	// wired in U10; U7.2 adds the field + the calls + tests construct it.
	DAG *InteractionDAGService
}

// trainingDefaultReward is the fallback reward when deps.DefaultReward is
// zero (e.g. tests constructing TrainingSessionDeps directly). Production
// code goes through NewTrainingSessionDeps, which sets DefaultReward from
// TRAINING_DEFAULT_REWARD (default 1.0).
const trainingDefaultReward = 1.0

// MaybeOpenTrainingSession is the public entry point invoked at every
// task-creation chokepoint (the Enqueue* family and the env_dispatch
// EnqueueAgentRun adapter). It delegates to the shared helper using the
// service's injected training deps; when training is unconfigured
// (s.Training == nil) it is a no-op.
func (s *TaskService) MaybeOpenTrainingSession(ctx context.Context, taskID, agentID, projectID, envID string) error {
	return maybeOpenTrainingSession(ctx, s.Training, taskID, agentID, projectID, envID)
}

// tryOpenTrainingSession is the in-service convenience wrapper used by the
// Enqueue* chokepoints: it derives taskID/agentID from the freshly-created task
// row + owning projectID and logs any error loudly (the task otherwise runs
// un-proxied, which must never be silent) without failing the enqueue.
func (s *TaskService) tryOpenTrainingSession(ctx context.Context, task db.AgentInboxEvent, projectID pgtype.UUID, envID string) {
	if s.Training == nil {
		return
	}
	if err := maybeOpenTrainingSession(
		ctx, s.Training,
		util.UUIDToString(task.ID),
		util.UUIDToString(task.AgentID),
		util.UUIDToString(projectID),
		envID,
	); err != nil {
		slog.Error("training session open failed",
			"task_id", util.UUIDToString(task.ID),
			"agent_id", util.UUIDToString(task.AgentID),
			"project_id", util.UUIDToString(projectID),
			"env_id", envID,
			"error", err,
		)
	}
}

// maybeOpenTrainingSession opens an RL proxy session and injects the areal_proxy
// config into the task's context when, and only when, the task is the training
// target for its project. It is safe to call at every task-creation chokepoint.
//
// Behavior:
//  1. deps == nil / no project -> no-op (training not configured for this task).
//  2. project has no training_dispatch row -> no-op (not a training project).
//  3. training_dispatch.train_agent_id != agentID -> no-op (not the target).
//  4. task already has context.areal_proxy -> no-op (idempotent / retry-safe).
//  5. otherwise: StartSession(sessionRef=taskID, envID) and merge context.areal_proxy.
//
// It returns (does NOT swallow) StartSession/persist errors so the caller can
// log + record. When a task IS a training target but the RL bridge is not
// configured, that is a loud error rather than a silent un-proxied run.
func maybeOpenTrainingSession(ctx context.Context, deps *TrainingSessionDeps, taskID, agentID, projectID, envID string) error {
	if deps == nil {
		return nil // training not configured for this deployment
	}
	if projectID == "" {
		return nil // no owning project -> cannot resolve a training target
	}
	projectUUID, err := util.ParseUUID(projectID)
	if err != nil {
		return nil // malformed project id -> not a training target
	}

	dispatch, err := deps.Lookup.GetTrainingDispatchByProject(ctx, projectUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // not a training project
		}
		return fmt.Errorf("training: lookup dispatch for project %s: %w", projectID, err)
	}
	if util.UUIDToString(dispatch.TrainAgentID) != agentID {
		return nil // this task's agent is not the training target
	}

	// From here the task IS a training target: a missing bridge dep is loud,
	// never a silent un-proxied run.
	if deps.RL == nil || deps.ProxyURL == "" {
		return fmt.Errorf(
			"training: task %s targets train_agent %s but the RL bridge is not configured (proxy_url/client missing)",
			taskID, agentID,
		)
	}

	taskUUID, err := util.ParseUUID(taskID)
	if err != nil {
		return fmt.Errorf("training: parse task id %q: %w", taskID, err)
	}

	// Idempotency: a task that already carries areal_proxy has an open session.
	task, err := deps.Store.GetAgentTask(ctx, taskUUID)
	if err != nil {
		return fmt.Errorf("training: load task %s: %w", taskID, err)
	}
	if hasArealProxyContext(task.Context) {
		return nil
	}

	creds, err := deps.RL.StartSession(ctx, taskID, envID)
	if err != nil {
		return fmt.Errorf("training: start_session for task %s: %w", taskID, err)
	}

	payload, err := json.Marshal(arealProxyConfig{
		Provider:  arealProxyProvider,
		Model:     arealProxyModel,
		APIKey:    creds.ProxyKey,
		BaseURL:   deps.ProxyURL,
		SessionID: creds.SessionID,
	})
	if err != nil {
		return fmt.Errorf("training: marshal areal_proxy for task %s: %w", taskID, err)
	}

	if err := deps.Store.MergeTaskArealProxyContext(ctx, db.MergeTaskArealProxyContextParams{
		ID:         taskUUID,
		ArealProxy: payload,
	}); err != nil {
		return fmt.Errorf("training: persist areal_proxy for task %s: %w", taskID, err)
	}

	// D10: record the {session_id -> agent_run_id (= task.ID), issue_id} mapping
	// for U8's DAG assembly. Fires only here - after the idempotency guard, so a
	// retry that finds an already-open session does NOT re-record. Best-effort:
	// the session is already open + persisted, so a recording failure degrades
	// assembly but must not fail the open (the run proceeds un-proxied of DAG
	// only, not of the RL session).
	if deps.DAG != nil {
		if err := deps.DAG.RecordSessionAgentRun(ctx, projectID, creds.SessionID, taskID, util.UUIDToString(task.IssueID)); err != nil {
			slog.Warn("training: record session->agent_run mapping failed",
				"task_id", taskID,
				"session_id", creds.SessionID,
				"issue_id", util.UUIDToString(task.IssueID),
				"error", err,
			)
		}
	}

	slog.Info("training session opened",
		"task_id", taskID,
		"agent_id", agentID,
		"project_id", projectID,
		"session_id", creds.SessionID,
	)
	return nil
}

// LinkExistingTrainingSession links a real derived-agent task to a training
// session that env-dispatch provisioning already opened before sandbox
// creation (AC-4). It persists the areal_proxy context on the task from the
// binding's recorded session key + the configured bridge URL and records the
// {session_id -> real task} mapping for DAG assembly -- WITHOUT calling
// StartSession (the session is reused, satisfying AC-4 retry identity + real
// task link). Idempotent: a task already carrying areal_proxy is a no-op.
//
// This is the env-dispatch counterpart to MaybeOpenTrainingSession (which opens
// a fresh session with task_id as session_ref for the legacy non-env-dispatch
// flow). The handler dispatches between the two based on whether the derived
// agent's binding already carries a training session.
func (s *TaskService) LinkExistingTrainingSession(ctx context.Context, taskID, agentID, projectID, envID, sessionID, sessionKey string) error {
	return linkExistingTrainingSession(ctx, s.Training, taskID, agentID, projectID, envID, sessionID, sessionKey)
}

func linkExistingTrainingSession(ctx context.Context, deps *TrainingSessionDeps, taskID, agentID, projectID, envID, sessionID, sessionKey string) error {
	if deps == nil || projectID == "" || sessionID == "" || sessionKey == "" {
		return nil // nothing to link (training unconfigured or no pre-opened session)
	}
	if deps.ProxyURL == "" {
		return fmt.Errorf("training: link existing session for task %s but the RL bridge is not configured", taskID)
	}
	taskUUID, err := util.ParseUUID(taskID)
	if err != nil {
		return fmt.Errorf("training: parse task id %q: %w", taskID, err)
	}
	task, err := deps.Store.GetAgentTask(ctx, taskUUID)
	if err != nil {
		return fmt.Errorf("training: load task %s: %w", taskID, err)
	}
	if hasArealProxyContext(task.Context) {
		return nil // idempotent: task already proxied
	}
	payload, err := json.Marshal(arealProxyConfig{
		Provider:  arealProxyProvider,
		Model:     arealProxyModel,
		APIKey:    sessionKey,
		BaseURL:   deps.ProxyURL,
		SessionID: sessionID,
	})
	if err != nil {
		return fmt.Errorf("training: marshal areal_proxy for task %s: %w", taskID, err)
	}
	if err := deps.Store.MergeTaskArealProxyContext(ctx, db.MergeTaskArealProxyContextParams{
		ID:         taskUUID,
		ArealProxy: payload,
	}); err != nil {
		return fmt.Errorf("training: persist areal_proxy for task %s: %w", taskID, err)
	}
	if deps.DAG != nil {
		if err := deps.DAG.LinkSessionTask(ctx, sessionID, projectID, taskID, util.UUIDToString(task.IssueID)); err != nil {
			slog.Warn("training: link session->real task failed",
				"task_id", taskID, "session_id", sessionID, "issue_id", util.UUIDToString(task.IssueID), "error", err)
		}
	}
	slog.Info("training session linked to real task",
		"task_id", taskID, "agent_id", agentID, "project_id", projectID, "session_id", sessionID)
	return nil
}

// IsTrainingTarget reports whether agentID is the training target for the given
// project. Env-dispatch provisioning (AC-4) uses this to decide whether to open
// a training session before sandbox creation.
func (s *TaskService) IsTrainingTarget(ctx context.Context, projectID, agentID string) bool {
	if s.Training == nil || projectID == "" || agentID == "" {
		return false
	}
	projectUUID, err := util.ParseUUID(projectID)
	if err != nil {
		return false
	}
	dispatch, err := s.Training.Lookup.GetTrainingDispatchByProject(ctx, projectUUID)
	if err != nil {
		return false
	}
	return util.UUIDToString(dispatch.TrainAgentID) == agentID
}

// OpenEnvDispatchTrainingSession opens one RL session for an env-dispatch
// training source BEFORE sandbox creation (AC-4), using the persistent binding
// ID as session_ref. Returns the session id + proxy key for the sandbox config
// and binding persistence. The session is reused on retry via the binding's
// persisted identity (LinkExistingTrainingSession links the real task later).
func (s *TaskService) OpenEnvDispatchTrainingSession(ctx context.Context, envID, bindingID string) (arealrl.SessionCreds, error) {
	if s.Training == nil || s.Training.RL == nil {
		return arealrl.SessionCreds{}, fmt.Errorf("training: RL bridge not configured for env-dispatch session")
	}
	return s.Training.RL.StartSession(ctx, bindingID, envID)
}

// TrainingProxyURL returns the configured AReaL bridge URL (the areal-default
// runtime base_url) for env-dispatch training sandbox config (AC-4). Empty when
// training is unconfigured.
func (s *TaskService) TrainingProxyURL() string {
	if s.Training == nil {
		return ""
	}
	return s.Training.ProxyURL
}

// hasArealProxyContext reports whether the task context JSONB already carries a
// non-null areal_proxy sub-object.
func hasArealProxyContext(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	v, ok := m[arealProxyContextKey]
	return ok && len(v) > 0 && string(v) != "null"
}

// extractArealProxyConfig parses the areal proxy config from task context.
func extractArealProxyConfig(raw []byte) (*arealProxyConfig, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, false
	}
	v, ok := m[arealProxyContextKey]
	if !ok || len(v) == 0 || string(v) == "null" {
		return nil, false
	}
	var cfg arealProxyConfig
	if err := json.Unmarshal(v, &cfg); err != nil {
		return nil, false
	}
	return &cfg, true
}

// maybeCloseTrainingSession closes an RL session and sets reward when the task
// has an areal_proxy config. It is safe to call on any terminal task state.
// Errors are logged, not propagated — the task is already terminal.
//
// NOTE: runtime_sweeper.FailStaleTasks uses raw SQL to transition stale tasks
// to failed, bypassing FailTask — so timeout-killed tasks will NOT auto-close
// their RL session. A reaper that sweeps orphaned sessions is future hardening,
// out of D scope.
func maybeCloseTrainingSession(ctx context.Context, deps *TrainingSessionDeps, task db.AgentInboxEvent, projectID pgtype.UUID) {
	if deps == nil || deps.Closer == nil {
		return
	}

	// Extract proxy config from task context
	cfg, ok := extractArealProxyConfig(task.Context)
	if !ok {
		return
	}

	// Resolve reward: try training dispatch first, fall back to configured default or 1.0
	reward := 1.0
	if deps.DefaultReward != 0 {
		reward = deps.DefaultReward
	}
	if deps.Lookup != nil {
		dispatch, err := deps.Lookup.GetTrainingDispatchByProject(ctx, projectID)
		if err == nil {
			reward = dispatch.DefaultReward
		} else if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("training: failed to load training dispatch for close hook",
				"task_id", util.UUIDToString(task.ID),
				"project_id", util.UUIDToString(projectID),
				"error", err,
			)
		}
	}

	// Set reward (best effort)
	if err := deps.Closer.SetReward(ctx, cfg.APIKey, reward); err != nil {
		slog.Warn("training: failed to set reward",
			"task_id", util.UUIDToString(task.ID),
			"session_id", cfg.SessionID,
			"reward", reward,
			"error", err,
		)
	}

	slog.Info("training session closed",
		"task_id", util.UUIDToString(task.ID),
		"project_id", util.UUIDToString(projectID),
		"session_id", cfg.SessionID,
		"reward", reward,
	)
}

// MaybeCloseTrainingSession is the public entry point invoked at terminal task
// transitions (complete/fail/cancel). It delegates to the shared helper using
// the service's injected training deps.
func (s *TaskService) MaybeCloseTrainingSession(ctx context.Context, task db.AgentInboxEvent) {
	projectID := pgtype.UUID{}
	if task.IssueID.Valid {
		if issue, err := s.Queries.GetIssue(ctx, task.IssueID); err == nil {
			projectID = issue.ProjectID
		}
	}
	maybeCloseTrainingSession(ctx, s.Training, task, projectID)
}

// maybeSweepIdleTrainingSessions checks whether all training agents in the
// project are idle (no non-terminal inbox events remain).  When idle, it sweeps
// any remaining open segments (close_segment) and ends the RL session.
//
// T10: triggered when a leaf task completes without @-mentions.  If all agents
// are now idle (conversation naturally ended), close remaining segments and end
// sessions — the training episode is complete.
func maybeSweepIdleTrainingSessions(ctx context.Context, deps *TrainingSessionDeps, q *db.Queries, task db.AgentInboxEvent, projectID pgtype.UUID) error {
	if deps == nil {
		return nil
	}
	if !projectID.Valid {
		return nil
	}
	activeCount, err := q.CountActiveTrainingTasks(ctx, projectID)
	if err != nil {
		return fmt.Errorf("training: count active tasks: %w", err)
	}
	if activeCount > 0 {
		return nil // other agents still working — not idle yet
	}
	// All trainable agents are idle.  Close the triggering task's session
	// (the sweep target) as the final bookend.
	cfg, ok := extractArealProxyConfig(task.Context)
	if !ok {
		return nil
	}
	// Close any still-open segment (one that wasn't closed by a delegation
	// handoff).  Best-effort: errors are logged but don't block session close.
	if deps.DAG != nil && deps.DAG.Enabled() {
		runID := util.UUIDToString(task.ID)
		if existing, segErr := deps.DAG.SegmentIDForAgentRun(ctx, runID); segErr != nil || existing == "" {
			if _, _, clsErr := deps.DAG.CloseSegmentForEvent(ctx, util.UUIDToString(projectID), cfg.SessionID, cfg.APIKey, "", nil); clsErr != nil {
				slog.Warn("training: idle-sweep segment close failed",
					"task_id", runID,
					"session_id", cfg.SessionID,
					"error", clsErr,
				)
			}
		}
	}
	// agent_inbox_event has no "completed" status — the enum is
	// pending/draining/acked/failed/suppressed and success lives in
	// terminal_outcome, so the old status comparison always scored 0.
	reward := 0.0
	if task.Status == "acked" && task.TerminalOutcome.String != "failed" {
		reward = 1.0
	}
	if rErr := deps.Closer.SetReward(ctx, cfg.APIKey, reward); rErr != nil {
		return fmt.Errorf("training: idle-sweep set_reward: %w", rErr)
	}
	slog.Info("training session swept (all agents idle)",
		"task_id", util.UUIDToString(task.ID),
		"project_id", util.UUIDToString(projectID),
		"session_id", cfg.SessionID,
	)
	return nil
}

// RouteTerminalTrainingTask is the terminal-transition routing hook invoked at
// complete/fail/cancel. When the terminating task is itself a critic task
// (carries context.critic_of), T8 closes the trained session using the critic's
// parsed reward. Otherwise, when the owning project has a training_dispatch row
// with a critic_agent_id, it spawns a critic task (deferring the RL session
// close to critic-terminal — T8). If no critic is configured, it falls back to
// D's close hook (SetReward(default)). Safe to call on any
// terminal task; errors are logged, not propagated — the task is already
// terminal.
// maybeTriggerCheckpoint fires the checkpoint trigger for a trained rollout
// structural event. It skips autopilot tasks, sweeper-failed tasks (stale /
// runtime-offline / timeout), and sandbox lifecycle jobs — these are not
// structural events that should produce a checkpoint. Errors are logged but
// do not block the terminal routing flow.
func maybeTriggerCheckpoint(ctx context.Context, deps *TrainingSessionDeps, task db.AgentInboxEvent, projectID pgtype.UUID) {
	if deps == nil || deps.CheckpointTrigger == nil {
		return
	}
	if task.AutopilotRunID.Valid {
		return
	}
	if task.FailureReason.Valid {
		reason := task.FailureReason.String
		if strings.Contains(reason, "queued_expired") ||
			strings.Contains(reason, "runtime_offline") ||
			strings.Contains(reason, "timeout") ||
			strings.Contains(reason, "stale") {
			return
		}
	}
	if hasSandboxLifecycleContext(task.Context) {
		return
	}
	if err := deps.CheckpointTrigger.TriggerCheckpoint(ctx, task, projectID); err != nil {
		slog.Warn("training: checkpoint trigger failed",
			"task_id", util.UUIDToString(task.ID),
			"project_id", util.UUIDToString(projectID),
			"error", err,
		)
	}
}

// hasSandboxLifecycleContext reports whether the task context JSONB carries a
// sandbox_lifecycle marker, indicating the task is a sandbox lifecycle job
// (create/stop/resume/delete) rather than a trained rollout structural event.
func hasSandboxLifecycleContext(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	var ctx map[string]any
	if err := json.Unmarshal(raw, &ctx); err != nil {
		return false
	}
	_, ok := ctx["sandbox_lifecycle"]
	return ok
}

func (s *TaskService) RouteTerminalTrainingTask(ctx context.Context, task db.AgentInboxEvent) {
	if s.Training == nil {
		return
	}
	// T8: if this is a critic task, close the trained session from it. The
	// critic path parses {"reward": <float>} from the critic's output and
	// calls SetReward on the trained session. Returns false
	// (no-op) for non-critic tasks, so trained-terminal routing proceeds.
	if maybeCloseTrainingSessionFromCritic(ctx, s.Training, task) {
		return // closed via critic; skip trained-terminal routing
	}
	// Env-dispatch rollout tasks carry their project through chat_session, not
	// issue_id; resolving only the issue shape here left every rollout task
	// project-less and skipped the whole training route (T10 included).
	projectID, projErr := s.terminalTaskProjectID(ctx, task)
	if projErr != nil {
		slog.Warn("training: terminal-route project lookup failed",
			"task_id", util.UUIDToString(task.ID),
			"error", projErr,
		)
		projectID = pgtype.UUID{}
	}
	if !projectID.Valid {
		// No owning project → cannot resolve a training dispatch → D's close
		// fires (no-op when the task has no areal_proxy context).
		maybeCloseTrainingSession(ctx, s.Training, task, projectID)
		return
	}
	dispatch, err := s.Training.Lookup.GetTrainingDispatchByProject(ctx, projectID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("training: terminal-route dispatch lookup failed",
				"task_id", util.UUIDToString(task.ID),
				"project_id", util.UUIDToString(projectID),
				"error", err,
			)
		}
		// No dispatch (or lookup failed) → D's close fires.
		maybeCloseTrainingSession(ctx, s.Training, task, projectID)
		return
	}
	// Checkpoint trigger: fire for trained rollout structural events (§6.2).
	// Skips autopilot, sweeper-failed, and sandbox lifecycle tasks internally.
	maybeTriggerCheckpoint(ctx, s.Training, task, projectID)
	if !dispatch.CriticAgentID.Valid {
		// T10: when the terminal task has children (delegated via @-mentions),
		// close the session immediately (handoff point).  When it has no
		// children (leaf — finished without @-mentions), defer the close and
		// check whether all training agents are now idle; if so, sweep
		// remaining segments and end sessions for all.
		childCount, cErr := s.Queries.CountChildTasks(ctx, task.ID)
		if cErr != nil {
			slog.Warn("training: count child tasks failed", "task_id", util.UUIDToString(task.ID), "err", cErr)
		}
		if cErr != nil || childCount > 0 {
			// Delegated: close immediately (handoff point).
			maybeCloseTrainingSession(ctx, s.Training, task, projectID)
			return
		}
		// Leaf: check all-idle → sweep remaining segments + end sessions.
		if sweepErr := maybeSweepIdleTrainingSessions(ctx, s.Training, s.Queries, task, projectID); sweepErr != nil {
			slog.Warn("training: idle sweep failed",
				"task_id", util.UUIDToString(task.ID),
				"project_id", util.UUIDToString(projectID),
				"error", sweepErr)
		}
		return
	}
	// Critic configured → spawn (or skip if already exists). Spawn failure
	// is swallowed inside maybeSpawnCriticTask, which fires the D-close
	// fallback itself — so we do NOT double-close here on error.
	if err := maybeSpawnCriticTask(ctx, s.Training, task, dispatch, projectID); err != nil {
		slog.Warn("critic spawn routing failed",
			"task_id", util.UUIDToString(task.ID),
			"project_id", util.UUIDToString(projectID),
			"error", err,
		)
	}
}

// maybeSpawnCriticTask creates a critic task for the trained agent's output
// when training_dispatch has a critic_agent_id. Replaces D's close hook for
// trained tasks when a critic is configured. Idempotent — a prior spawn
// (matched on context.critic_of.trained_task_id) is a no-op. On spawn
// failure, the error is swallowed and D's close hook fires (SetReward with
// the default reward) so the RL session is not left unrewarded.
//
// Returns nil in all expected cases (including spawn-failure fallback). An
// error is returned only for pre-spawn lookup failures that prevent the
// fallback from firing safely — the caller logs but does not double-close.
func maybeSpawnCriticTask(ctx context.Context, deps *TrainingSessionDeps, trained db.AgentInboxEvent, td db.TrainingDispatch, projectID pgtype.UUID) error {
	if deps == nil {
		return nil
	}
	if !td.CriticAgentID.Valid {
		// No critic configured → caller's D-close fallback fires.
		return nil
	}
	if deps.Creator == nil {
		// Critic configured but no creator wired (mis-configured deployment).
		// Fall back to D's close so the session isn't orphaned. Production
		// wiring (NewTrainingSessionDeps) sets Creator whenever Lookup is set,
		// so this path is defensive.
		maybeCloseTrainingSession(ctx, deps, trained, projectID)
		return nil
	}

	// Idempotency: skip if a critic task already exists for this trained task.
	existing, err := deps.Creator.FindCriticTaskForTrained(ctx, util.UUIDToString(trained.ID))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("training: find existing critic for task %s: %w", util.UUIDToString(trained.ID), err)
	}
	if existing.ID.Valid {
		// Already spawned — critic-terminal (T8) owns the eventual close.
		return nil
	}

	// Read the trained session creds from context.areal_proxy. If the trained
	// task has no proxy config, it never went through the open hook — nothing
	// to link to, so D's close fires (which itself no-ops without the proxy).
	cfg, ok := extractArealProxyConfig(trained.Context)
	if !ok {
		maybeCloseTrainingSession(ctx, deps, trained, projectID)
		return nil
	}

	// Build the critic_of linkage stored in the critic task's context JSONB.
	criticOf, err := json.Marshal(map[string]string{
		"trained_task_id": util.UUIDToString(trained.ID),
		"proxy_key":       cfg.APIKey,
		"session_id":      cfg.SessionID,
		"project_id":      util.UUIDToString(projectID),
	})
	if err != nil {
		return fmt.Errorf("training: marshal critic_of for task %s: %w", util.UUIDToString(trained.ID), err)
	}

	// Extract the trained task's literal output text from result JSONB
	// (TaskCompletedPayload.Output). For failed/cancelled tasks this is
	// empty — the critic still runs against whatever output survived.
	trainedOutput := ""
	if len(trained.Result) > 0 {
		var payload protocol.TaskCompletedPayload
		if err := json.Unmarshal(trained.Result, &payload); err == nil {
			trainedOutput = payload.Output
		}
	}

	criticCtx, err := json.Marshal(map[string]any{
		"critic_of":      json.RawMessage(criticOf),
		"trained_output": trainedOutput,
	})
	if err != nil {
		return fmt.Errorf("training: marshal critic context for task %s: %w", util.UUIDToString(trained.ID), err)
	}

	// Create the critic task as a peer (parent_task_id NOT set). issue_id is
	// inherited so the critic shows up on the same issue as the trained task.
	if _, err := deps.Creator.CreateCriticTask(ctx, db.CreateCriticTaskParams{
		AgentID:  td.CriticAgentID,
		IssueID:  trained.IssueID,
		Priority: trained.Priority,
		Context:  criticCtx,
	}); err != nil {
		// Spawn failed — fall back to D's close so the RL session is not
		// orphaned. The error is swallowed; the close is the user-visible
		// outcome (default reward).
		slog.Warn("critic spawn failed; closing with default reward",
			"trained_task_id", util.UUIDToString(trained.ID),
			"critic_agent_id", util.UUIDToString(td.CriticAgentID),
			"project_id", util.UUIDToString(projectID),
			"error", err,
		)
		maybeCloseTrainingSession(ctx, deps, trained, projectID)
		return nil
	}

	slog.Info("critic task spawned",
		"trained_task_id", util.UUIDToString(trained.ID),
		"critic_agent_id", util.UUIDToString(td.CriticAgentID),
		"project_id", util.UUIDToString(projectID),
		"session_id", cfg.SessionID,
	)
	return nil
}

// maybeCloseTrainingSessionFromCritic closes the trained RL session when the
// terminating task is itself a critic task (carries context.critic_of). It
// reads proxy_key from context.critic_of and parses {"reward": <float>} from
// the LAST line of the critic's output (TaskCompletedPayload.Output stored in
// result JSONB). RL errors are logged, not propagated — the task is already
// terminal.
//
// Returns true when the critic path fired (session close attempted). Returns
// false (no-op) when:
//   - deps == nil (training not configured);
//   - the task has no context.critic_of (not a critic task — routing proceeds
//     with trained-terminal logic);
//   - context.critic_of is malformed (skip silently — the task is mis-tagged).
//
// The default reward fallback is deps.DefaultReward (set by NewTrainingSessionDeps
// from TRAINING_DEFAULT_REWARD, default 1.0). When DefaultReward is zero
// (e.g., tests constructing TrainingSessionDeps directly), the package constant
// trainingDefaultReward is used.
func maybeCloseTrainingSessionFromCritic(ctx context.Context, deps *TrainingSessionDeps, critic db.AgentInboxEvent) bool {
	if deps == nil || deps.Closer == nil {
		return false
	}

	// Read context.critic_of. If absent or malformed, this is not a critic
	// task — routing proceeds with trained-terminal logic.
	var payload struct {
		CriticOf json.RawMessage `json:"critic_of"`
	}
	if len(critic.Context) == 0 {
		return false
	}
	if err := json.Unmarshal(critic.Context, &payload); err != nil {
		return false // malformed context — not a critic task
	}
	if len(payload.CriticOf) == 0 || string(payload.CriticOf) == "null" {
		return false // no critic_of — not a critic task
	}
	var cof struct {
		TrainedTaskID string `json:"trained_task_id"`
		ProxyKey      string `json:"proxy_key"`
		SessionID     string `json:"session_id"`
		ProjectID     string `json:"project_id"`
	}
	if err := json.Unmarshal(payload.CriticOf, &cof); err != nil {
		return false // malformed critic_of — skip
	}
	if cof.ProxyKey == "" {
		// Critic task without a proxy_key linkage — nothing to close. Treat
		// as a non-critic task so routing proceeds (the trained session will
		// be closed by D's fallback if/when the trained task itself terminates).
		return false
	}

	// Extract the critic's output text from result JSONB
	// (TaskCompletedPayload.Output). For failed/cancelled critic tasks this is
	// empty — parseCriticReward falls back to the default reward.
	output := ""
	if len(critic.Result) > 0 {
		var resultPayload protocol.TaskCompletedPayload
		if err := json.Unmarshal(critic.Result, &resultPayload); err == nil {
			output = resultPayload.Output
		}
	}

	defaultReward := deps.DefaultReward
	if defaultReward == 0 {
		defaultReward = trainingDefaultReward
	}
	reward := parseCriticReward(output, defaultReward)

	// Set reward (best effort)
	if err := deps.Closer.SetReward(ctx, cof.ProxyKey, reward); err != nil {
		slog.Warn("critic close: SetReward failed",
			"critic_task_id", util.UUIDToString(critic.ID),
			"trained_task_id", cof.TrainedTaskID,
			"session_id", cof.SessionID,
			"reward", reward,
			"error", err,
		)
	}

	slog.Info("training session closed from critic",
		"critic_task_id", util.UUIDToString(critic.ID),
		"trained_task_id", cof.TrainedTaskID,
		"session_id", cof.SessionID,
		"reward", reward,
	)
	return true
}

// parseCriticReward extracts {"reward": <float>} from the LAST line of output.
// Returns defaultReward on any failure (no lines, JSON parse failure, missing
// reward field). Range-checked to [0.0, 1.0]; out-of-range values fall back to
// defaultReward. Standalone (not a method) for testability.
func parseCriticReward(output string, defaultReward float64) float64 {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return defaultReward
	}
	lines := strings.Split(trimmed, "\n")
	lastLine := strings.TrimSpace(lines[len(lines)-1])
	if lastLine == "" {
		return defaultReward
	}
	var parsed struct {
		Reward float64 `json:"reward"`
	}
	if err := json.Unmarshal([]byte(lastLine), &parsed); err != nil {
		return defaultReward
	}
	if parsed.Reward < 0.0 || parsed.Reward > 1.0 {
		return defaultReward
	}
	return parsed.Reward
}
