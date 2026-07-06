// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/arealrl"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// arealProxyProvider / arealProxyModel are the fixed provider/model the trained
// runtime is launched with: `pi --provider areal --model areal-default`. The
// api_key and base_url are per-session (mint + deployment config).
const (
	arealProxyProvider = "areal"
	arealProxyModel    = "areal-default"
	// arealProxyContextKey is the top-level key under which the RL proxy config
	// is merged into agent_task_queue.context.
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
	GetAgentTask(ctx context.Context, id pgtype.UUID) (db.AgentTaskQueue, error)
	MergeTaskArealProxyContext(ctx context.Context, arg db.MergeTaskArealProxyContextParams) error
}

// arealSessionStarter opens an RL session for a task via the bridge. The real
// implementation is *arealrl.Client; tests inject a fake. The helper never
// constructs a real client — it is injected via TrainingSessionDeps.
type arealSessionStarter interface {
	StartSession(ctx context.Context, taskID, envID string) (arealrl.SessionCreds, error)
}

// TrainingSessionDeps bundles the collaborators the session-open hook needs.
// A nil *TrainingSessionDeps means training is not configured for this
// deployment, in which case the hook is a no-op. Construction/wiring of the
// real client + proxy URL is finalized in Task 8 (config).
type TrainingSessionDeps struct {
	Lookup   trainingDispatchLookup
	Store    trainingTaskStore
	RL       arealSessionStarter
	ProxyURL string
}

// MaybeOpenTrainingSession is the public entry point invoked at every
// task-creation chokepoint (the Enqueue* family and the env_dispatch
// EnqueueAgentRun adapter). It delegates to the shared helper using the
// service's injected training deps; when training is unconfigured
// (s.Training == nil) it is a no-op.
func (s *TaskService) MaybeOpenTrainingSession(ctx context.Context, taskID, agentID, projectID string) error {
	return maybeOpenTrainingSession(ctx, s.Training, taskID, agentID, projectID)
}

// tryOpenTrainingSession is the in-service convenience wrapper used by the
// Enqueue* chokepoints: it derives taskID/agentID from the freshly-created task
// row + owning projectID and logs any error loudly (the task otherwise runs
// un-proxied, which must never be silent) without failing the enqueue.
func (s *TaskService) tryOpenTrainingSession(ctx context.Context, task db.AgentTaskQueue, projectID pgtype.UUID) {
	if s.Training == nil {
		return
	}
	if err := maybeOpenTrainingSession(
		ctx, s.Training,
		util.UUIDToString(task.ID),
		util.UUIDToString(task.AgentID),
		util.UUIDToString(projectID),
	); err != nil {
		slog.Error("training session open failed",
			"task_id", util.UUIDToString(task.ID),
			"agent_id", util.UUIDToString(task.AgentID),
			"project_id", util.UUIDToString(projectID),
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
//  5. otherwise: StartSession(taskID) and merge context.areal_proxy.
//
// It returns (does NOT swallow) StartSession/persist errors so the caller can
// log + record. When a task IS a training target but the RL bridge is not
// configured, that is a loud error rather than a silent un-proxied run.
func maybeOpenTrainingSession(ctx context.Context, deps *TrainingSessionDeps, taskID, agentID, projectID string) error {
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

	creds, err := deps.RL.StartSession(ctx, taskID, "")
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

	slog.Info("training session opened",
		"task_id", taskID,
		"agent_id", agentID,
		"project_id", projectID,
		"session_id", creds.SessionID,
	)
	return nil
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
