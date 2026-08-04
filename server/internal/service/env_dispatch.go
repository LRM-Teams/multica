package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/util/stackerr"
)

// EnvMode enumerates the reset modes (spec §4.2).
type EnvMode string

const (
	EnvModeScratch EnvMode = "scratch"
	EnvModeBranch  EnvMode = "branch"
)

// EnvModeBase is the mode for a freshly booted env (POST /api/v1/env). A base
// env has no project and is the reset source for scratch dispatch.
const EnvModeBase EnvMode = "base"

// DefaultTrainingReward is the reward stamped on a training_dispatch row when a
// dispatch requests training (spec §4.1). Task 8 wires this to a configurable
// TRAINING_DEFAULT_REWARD; for now it is a fixed default.
const DefaultTrainingReward = 1.0

// EnvDomain enumerates the dispatch domains (spec §4.2). Required on dispatch;
// each domain pins a dispatch_type (swe_lego⇒issue, self_play⇒message).
type EnvDomain string

const (
	EnvDomainSweLego  EnvDomain = "swe_lego"
	EnvDomainSelfPlay EnvDomain = "self_play"
)

// EnvDispatchType enumerates the dispatch types.
type EnvDispatchType string

const (
	EnvDispatchIssue   EnvDispatchType = "issue"
	EnvDispatchMessage EnvDispatchType = "message"
)

// EnvDispatchInput is the service-layer input for the unified dispatch.
// EnvDispatchInput is the service-layer input for the unified dispatch.
type EnvDispatchInput struct {
	WorkspaceID     string
	UserID          string // creator/actor
	Mode            EnvMode
	EnvID           string    // base env (scratch) or state env (branch)
	SourceProjectID string    // branch only: the single project on EnvID (1:1 invariant), resolved by the handler
	SourceTaskID    string    // durable reusable sampling identity; resolved before idempotency/fan-out
	Domain          EnvDomain // required
	DispatchType    EnvDispatchType
	GroupSize       int
	AgentID         string
	TrainAgentID    string // optional training target (spec §4.1); empty ⇒ no training session
	CriticAgentID   string // optional critic for trained agent (sub-project E): evaluates the trained agent's output; empty ⇒ unchanged behavior
	IdempotencyKey  string // optional; dedupes retries (spec §7.7)

	// TrainingMode is the explicit training vs non-training switch (Task 1).
	// true requires TrainAgentID; false forbids TrainAgentID and CriticAgentID.
	// The handler dereferences the request pointer and never passes an absent
	// value (nil is rejected at the HTTP boundary).
	TrainingMode bool

	// SharedSandbox is the resolved per-rollout sandbox sharing switch: when
	// true, all agents of each rollout (sample) share one sandbox + one daemon +
	// one working directory. The handler resolves the optional request pointer
	// (nil ⇒ false), so the service only ever sees an explicit boolean.
	SharedSandbox bool

	// PerAgentEnvSpecs optionally assigns individual squad agents to sandbox
	// templates or base environments while preserving a shared Multica entity
	// subtree. Empty preserves existing default/shared sandbox behavior.
	PerAgentEnvSpecs []PerAgentEnvSpec

	// Issue dispatch (required for scratch+swe_lego; forbidden for
	// branch+swe_lego where the copied issue is reused).
	Issue *IssueInput

	// Message dispatch (required for self_play).
	Message *MessageInput

	// DefaultBaseTemplate is the sandbox template used when the service
	// auto-creates the workspace default self_play base env (empty env_id, no
	// default configured). Resolved by the handler from the request's optional
	// template override or the server's MULTICA_DEFAULT_SELF_PLAY_TEMPLATE
	// default. Empty ⇒ "default". Unused when env_id is explicit or a default is
	// already configured.
	DefaultBaseTemplate string

	// TaskTemplateID is the ready, sanitized Cube template produced for a
	// scratch SWE-Lego issue source. It is internal dispatch state, never a
	// caller-selected template, and takes precedence for every rollout sandbox.
	TaskTemplateID string

	// InstanceBackedBase is resolved by Dispatch after GetEnv: true when the base
	// env's sandbox is a sandbox_instance (e.g. an auto-created default env), so
	// forking uses the sandbox_instance backend instead of the Fleet fork path.
	// Probed once per dispatch via the lifecycle creator; false for Fleet-booted
	// base envs and when no lifecycle is injected.
	InstanceBackedBase bool

	// MessageRoster is resolved once before reset fan-out. It is internal
	// dispatch state, not an HTTP request field.
	MessageRoster       MessageRoster
	BranchMessageSource *ValidatedBranchMessageSource
	SourceTask          *SourceTask

	// Audit is an explicit, per-request opt-in for the identifier- and
	// state-only dispatch lifecycle ledger. Nil and Enabled=false preserve the
	// ordinary zero-write dispatch path even when an audit storage dependency is
	// installed.
	Audit *EnvDispatchAuditRequest
}

// PerAgentEnvSpec assigns one squad agent to a sandbox template or base
// environment. All agents still share the same Multica entity subtree.
// Runtime, when set, overrides the agent's provider model for a non-training
// scratch message dispatch (the sandbox starts with the caller's external
// model instead of the agent's configured runtime).
type PerAgentEnvSpec struct {
	AgentID   string
	Template  string
	BaseEnvID string
	Runtime   *ExternalModelRuntime
}

// ExternalModelRuntime is a caller-supplied external model provider
// configuration used to start an isolated sandbox for a non-training scratch
// message agent. APIKey is a secret: it must never be serialized into
// SandboxInstanceRef, HTTP responses, errors, or structured logs.
type ExternalModelRuntime struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
	Model    string `json:"model"`
}

// ResolvedPerAgentSandboxPolicy is the internal, resolved per-agent sandbox
// policy: the sandbox template plus an optional external model runtime. It is
// the canonical value consumed by channel binding creation and provisioning,
// and is never serialized into response DTOs - SandboxInstanceRef carries only
// the non-secret template. The runtime, when present, is persisted only in the
// env-agent binding's sandbox_config (via the handler codec).
type ResolvedPerAgentSandboxPolicy struct {
	Template string
	Runtime  *ExternalModelRuntime
	// Shared marks the binding as belonging to a shared_sandbox rollout: the
	// sample's agents attach to the rollout's single shared sandbox/runtime
	// instead of claiming per-agent sandboxes (research D3). Persisted on the
	// binding's sandbox_config as "shared".
	Shared bool
}

// NormalizeExternalModelRuntime trims whitespace and validates that the
// runtime has all three fields and an absolute HTTP(S) base_url. It returns a
// new normalized value, or an error whose message names fields but never
// formats runtime values. A nil input returns (nil, nil).
func NormalizeExternalModelRuntime(in *ExternalModelRuntime) (*ExternalModelRuntime, error) {
	if in == nil {
		return nil, nil
	}
	out := &ExternalModelRuntime{
		Provider: strings.ToLower(strings.TrimSpace(in.Provider)),
		BaseURL:  strings.TrimSpace(in.BaseURL),
		APIKey:   strings.TrimSpace(in.APIKey),
		Model:    strings.TrimSpace(in.Model),
	}
	// Preserve compatibility with callers created before provider selection was
	// explicit. The sandbox runtime historically interpreted this flat shape as
	// OpenAI-compatible.
	if out.Provider == "" {
		out.Provider = "openai"
	}
	if out.BaseURL == "" || out.APIKey == "" || out.Model == "" {
		return nil, fmt.Errorf("base_url, api_key, and model are required")
	}
	parsed, err := url.Parse(out.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("base_url must be an absolute HTTP(S) URL")
	}
	return out, nil
}

type IssueInput struct {
	Title              string
	Description        string
	AcceptanceCriteria []string
	FailToPass         []string
	PassToPass         []string
}

type MessageInput struct {
	Content string
}

type MessageRoster struct {
	LeaderID string
	AgentIDs []string
}

// EnvDispatchAgentProvisionInput identifies the channel member whose isolated
// runtime, sandbox, and channel chat session must be created.
type EnvDispatchAgentProvisionInput struct {
	WorkspaceID, UserID, EnvID, ProjectID, ChannelID, AgentID string
	SourceSandboxInstanceID                                   string
	SandboxConfig                                             json.RawMessage
}

type EnvDispatchAgentProvisionResult struct {
	AgentID, SandboxInstanceID, RuntimeID, DaemonID, ChatSessionID string
}

// ChannelRunInput carries the explicit binding IDs that must be used for an
// EnvDispatch channel task. In particular, RuntimeID is never inferred from
// the agent's shared default runtime.
type ChannelRunInput struct {
	AgentID, ChannelID, ProjectID, EnvID, ChatSessionID string
	SandboxInstanceID, RuntimeID, SourceMessageID       string
	// SharedWorkdirEnvID, when non-empty, is the sample env whose single
	// shared working directory the daemon must anchor this run to (research
	// D5, FR-008). Empty keeps the per-agent workdir root.
	SharedWorkdirEnvID string
}

type AgentSandboxStatus struct {
	Status            string `json:"status"`
	SandboxInstanceID string `json:"sandbox_instance_id,omitempty"`
	RuntimeID         string `json:"runtime_id,omitempty"`
}

// EnvRollout is one element of the response array (spec §6.3).
type EnvRollout struct {
	RunID          string // unique per rollout; never used as DAG/cleanup identity
	SourceTaskID   string // durable reusable task identity
	ChannelID      string
	LeaderRunID    string
	AgentSandboxes map[string]AgentSandboxStatus
	EnvID          string // always a new env_id (branch always forks, incl. N=1)
	ProjectID      string
	IssueID        string // empty iff dispatch_type=message
	ChatSessionID  string // empty iff dispatch_type=issue
	AgentRunID     string // empty if dispatch failed (partial rollout)
	Error          string // empty if rollout succeeded
	// Stack is the origin goroutine stack (stackerr.StackOf) for the error in
	// Error, when the failure came from an adapter call. Surfaced per-rollout in
	// the 500 (all-failed) response. Empty on success or for leaf logic errors.
	Stack []byte

	// SandboxRefs carries structured sandbox_instance refs for save/resume-capable
	// (sandbox_instance-backed) rollouts. Empty for Fleet-backed rollouts.
	SandboxRefs []SandboxInstanceRef
	// AgentSandboxRefs maps agent_id -> its sandbox_instance ref when per-agent
	// env specs are used. Empty for default/shared sandbox assignment.
	AgentSandboxRefs map[string]SandboxInstanceRef

	// channelMessageIDs carries the copied channel's source->destination message
	// ID map from reset into dispatch, so a branch can remap the source trigger's
	// source_message_id / thread_root_message_id onto the destination channel.
	// Unexported: it is not part of the idempotency-replay JSON.
	channelMessageIDs map[string]string
}

// EnvDispatchResult wraps the rollouts slice.
type EnvDispatchResult struct {
	ChannelID string
	ProjectID string // single project for the dispatch (group_size=1: the rollout's project)
	Rollouts  []EnvRollout
	// Audit is set only when the configured audit storage created a real run.
	// It is never derived from caller input.
	Audit *EnvDispatchAuditReport
}

// EnvDispatchDeps is the seam between the service and the DB + cloud runtime.
// Production wires this to real queries + cloudRuntimeProxy; tests inject a fake.
type EnvDispatchDeps interface {
	// Environment operations
	GetEnv(ctx context.Context, envID, workspaceID string) (Env, error)
	CreateEnv(ctx context.Context, workspaceID string, sandboxIDs []string, parentEnvID string, mode EnvMode, domain EnvDomain) (envID string, err error)
	// SetEnvSandboxes attaches the execution sandboxes after an EnvDispatch
	// message environment has been reserved. The reservation is necessary
	// because channel bindings need env_id before they can provision a sandbox.
	SetEnvSandboxes(ctx context.Context, envID, workspaceID string, sandboxIDs []string) error
	DeleteEnv(ctx context.Context, envID, workspaceID string) error

	// Sandbox operations (proxy to cloud-runtime/Fleet)
	ForkSandbox(ctx context.Context, sourceSandboxID string, idx int) (sandboxID string, err error)
	DeleteSandbox(ctx context.Context, sandboxID string) error
	BootSandbox(ctx context.Context, imageRef string) (sandboxID string, err error) // for POST /api/v1/env

	// Project operations
	GetProjectByEnvID(ctx context.Context, envID, workspaceID string) (projectID string, err error) // branch: resolve source env → its single project (1:1 invariant)
	CreateProject(ctx context.Context, workspaceID, name, envID string) (projectID string, err error)
	// CopyProjectSubtree deep-copies issues + chat sessions + messages under a
	// new project bound to envID; returns source→copied ID maps so dispatch can
	// target the copied issue (branch+swe_lego) or copied session (branch+self_play).
	CopyProjectSubtree(ctx context.Context, sourceProjectID, workspaceID, envID string) (newProjectID string, issueIDMap, chatSessionIDMap map[string]string, err error)
	DeleteProject(ctx context.Context, projectID, workspaceID string) error
	ResolveMessageRoster(ctx context.Context, workspaceID, agentID string) (MessageRoster, error)
	CreateEnvDispatchChannel(ctx context.Context, workspaceID, userID, projectID, envID string, roster MessageRoster, specs map[string]ResolvedPerAgentSandboxPolicy) (channelID string, err error)
	DeleteChannel(ctx context.Context, workspaceID, channelID string) error
	ProvisionEnvDispatchAgent(ctx context.Context, in EnvDispatchAgentProvisionInput) (EnvDispatchAgentProvisionResult, error)
	CreateChannelMessage(ctx context.Context, channelID, workspaceID, userID, content string) (messageID string, err error)
	EnqueueEnvDispatchChannelRun(ctx context.Context, workspaceID, userID string, in ChannelRunInput, idx int) (runID string, err error)

	// LinkEnvDispatchTrainingSession links the binding's persisted training
	// session to the real derived-agent task ID after the task is enqueued, so
	// DAG assembly maps the session to the actual agent run (AC-4). Best-effort:
	// a no-op when the (envID, agentID) binding is not a training binding
	// (training_session_id NULL). Called after EnqueueEnvDispatchChannelRun.
	LinkEnvDispatchTrainingSession(ctx context.Context, envID, agentID, projectID, runID, issueID string) error
	SaveCollaborationTrigger(ctx context.Context, envID string, trigger EnvCollaborationTrigger) error
	ValidateBranchMessageSource(ctx context.Context, workspaceID, envID, projectID string, roster MessageRoster) (ValidatedBranchMessageSource, error)
	CopyEnvDispatchChannel(ctx context.Context, workspaceID, sourceChannelID, destinationProjectID, destinationEnvID string) (ChannelCopyMap, error)

	// Issue operations
	ListIssuesByProject(ctx context.Context, projectID, workspaceID string) ([]IssueRow, error)
	CreateIssue(ctx context.Context, projectID, workspaceID, creatorID, title, description string, acceptanceCriteria, failToPass, passToPass []string) (issueID string, err error)

	// Chat operations
	CreateChatSession(ctx context.Context, projectID, workspaceID, agentID, creatorID string) (sessionID string, err error)
	CreateChatMessage(ctx context.Context, sessionID, role, content string) (messageID string, err error)
	// ListChatSessionsByProject returns the chat session ids under a project.
	// Used to enforce the branch+self_play "exactly one session" rule (§7.4).
	ListChatSessionsByProject(ctx context.Context, projectID, workspaceID string) ([]string, error)

	// Agent run
	// runtimeID, when non-empty, overrides the task's runtime_id: routes the
	// task to a pre-created sandbox runtime R' (Phase 2) instead of the
	// agent/session/leader runtime. Empty preserves the current behavior.
	EnqueueAgentRun(ctx context.Context, workspaceID, actorUserID, agentID, issueID, chatSessionID, sandboxInstanceID, envID, runtimeID string, idx int) (runID string, err error)

	// GetDefaultSelfPlayEnv resolves the per-workspace default self_play base
	// env used when a scratch+self_play dispatch is called with an empty
	// env_id. Returns ("", nil) or an error when unconfigured (spec D2/D3).
	GetDefaultSelfPlayEnv(ctx context.Context, workspaceID string) (envID string, err error)

	// SetDefaultSelfPlayEnv persists envID as the workspace's default self_play
	// base env, but only when the column is still NULL - the first of N
	// concurrent auto-create writers wins, the rest are no-ops. The caller
	// re-reads GetDefaultSelfPlayEnv to pick up the canonical winner and clean
	// up any env it created but did not win the race for.
	SetDefaultSelfPlayEnv(ctx context.Context, workspaceID, envID string) error

	// PrecreateAgentRuntime inserts an offline agent_runtime row (R') keyed by a
	// freshly-generated daemon_id, for the given agent's provider, owned by
	// ownerUserID. Returns the runtime id (R') and the daemon_id. The in-sandbox
	// daemon booted with MULTICA_DAEMON_ID=<daemon_id> adopts R' on register
	// (UpsertAgentRuntime ON CONFLICT (workspace_id, daemon_id, provider)). Used
	// by daemon-enabled sandbox rollouts so the task can carry runtime_id=R'
	// immediately - no deferred binding. agentID must be a real agent (the
	// provider is resolved from it); empty agentID returns an error.
	PrecreateAgentRuntime(ctx context.Context, workspaceID, ownerUserID, agentID string) (runtimeID, daemonID string, err error)

	// DeleteAgentRuntime deletes a pre-created agent_runtime row (R'). Used to
	// reclaim R' when a daemon-enabled rollout fails before its task is created
	// (sandbox create failure, or any pre-enqueue/enqueue failure) so the offline
	// row does not linger. Safe only when no task references R' (the task FK is
	// ON DELETE CASCADE) - callers must ensure the task was never created.
	DeleteAgentRuntime(ctx context.Context, workspaceID, runtimeID string) error

	// Idempotency ledger (spec §7.7). GetIdempotentResponse returns ok=false
	// when the key is unseen; SaveIdempotentResponse persists the response for
	// replay. Both are workspace-scoped.
	GetIdempotentResponse(ctx context.Context, workspaceID, key string) (EnvDispatchResult, bool, error)
	SaveIdempotentResponse(ctx context.Context, workspaceID, key string, res EnvDispatchResult) error

	// SaveTrainingDispatch persists the training intent for a rollout project
	// (spec §4.1): one row per rollout project when a train_agent_id is set, so
	// the later session-open hook can resolve the training target + default
	// reward by project_id. Keyed by projectID (upsert on conflict).
	SaveTrainingDispatch(ctx context.Context, projectID, workspaceID, trainAgentID, criticAgentID string, defaultReward float64) error

	// Source-task operations. Scratch resolves the request identity before
	// idempotency; branch inherits it from the source project's durable run.
	GetSourceTask(ctx context.Context, sourceTaskID, workspaceID string) (SourceTask, error)
	GetEnvDispatchRunSourceTask(ctx context.Context, projectID, workspaceID string) (SourceTask, error)

	// CreateEnvDispatchRunWithSource persists one unique durable rollout run
	// after a project exists. project_id remains the DAG and cleanup identity.
	CreateEnvDispatchRunWithSource(ctx context.Context, projectID, workspaceID string, trainingMode bool, sourceTaskID string, sampleIndex int) (runID string, err error)
	SetEnvDispatchRunLocalTargets(ctx context.Context, projectID, workspaceID, localIssueID, localChannelID string) error

	// CreateEnvDispatchRun persists the legacy durable dispatch root row for a project
	// (spec: durable dispatch identity independent of training_dispatch). One row
	// per project, keyed by project_id, carrying workspace_id and training_mode.
	// root_task_id starts NULL and is bound later via BindEnvDispatchRootTask.
	// Created after the project exists. Best-effort: a creation failure is
	// recorded on the rollout but does not fail the dispatch; /dag treats a
	// missing row as in_progress (no root task yet).
	CreateEnvDispatchRun(ctx context.Context, projectID, workspaceID string, trainingMode bool) error

	// BindEnvDispatchRootTask binds the enqueued leader task as the dispatch
	// root (env_dispatch_run.root_task_id = rootTaskID). Called immediately after
	// the leader task is enqueued (consumes EnvRollout.LeaderRunID / AgentRunID).
	// Best-effort: a binding failure is recorded but does not fail the dispatch;
	// /dag treats an unbound root as in_progress until the binding succeeds.
	BindEnvDispatchRootTask(ctx context.Context, projectID, rootTaskID string) error

	// GetEnvDispatchRootTaskStatus resolves the status of the dispatch's bound
	// root task for the /dag readiness decision. Readiness is derived
	// EXCLUSIVELY from this row, not from training_dispatch. Returns
	// pgx.ErrNoRows when no env_dispatch_run row exists or root_task_id is NULL
	// (rollout not started / root not enqueued); the caller treats both as
	// in_progress. Otherwise returns the agent_inbox_event.status of the bound
	// root task.
	GetEnvDispatchRootTaskStatus(ctx context.Context, projectID, workspaceID string) (string, error)

	// ValidateAgentInWorkspace reports whether agentID is a member of the
	// workspace. Returns a typed error when the agent is unknown or
	// unauthorized; nil when the agent is a member. Used by per-agent env
	// spec validation (§5) to reject unknown agents before any rollout state
	// is created.
	ValidateAgentInWorkspace(ctx context.Context, workspaceID, agentID string) error

	// ResolvePerAgentEnvSpec validates that the spec's template or base_env_id
	// is known and authorized for the workspace, and returns the resolved
	// per-agent sandbox policy (template + optional external model runtime).
	// InstanceID is left empty - the caller creates the sandbox_instance via
	// SandboxInstanceCreator. Returns a typed error when the template/base_env_id
	// is unknown or unauthorized, or when the runtime is malformed.
	ResolvePerAgentEnvSpec(ctx context.Context, workspaceID string, spec PerAgentEnvSpec) (ResolvedPerAgentSandboxPolicy, error)
}

// rootTaskTerminalStatuses is the terminal subset of the canonical inbox
// lifecycle. Outcome details live in terminal_outcome; acked and suppressed
// both mean the root can no longer make progress.
var rootTaskTerminalStatuses = map[string]bool{
	"acked":      true,
	"suppressed": true,
}

// DagReadiness is the /dag readiness decision derived EXCLUSIVELY from the
// env_dispatch_run root task, independent of training_dispatch.
type DagReadiness int

const (
	// DagReadinessInProgress: no env_dispatch_run row exists, no root task is
	// bound, or the bound root task is non-terminal. The handler returns
	// 202 {"status":"in_progress"} so AReaL keeps polling.
	DagReadinessInProgress DagReadiness = iota
	// DagReadinessTerminal: the bound root task is terminal
	// (completed/failed/cancelled). The handler proceeds to DAG assembly to
	// decide 200 {"status":"failed"} (non-dense coverage) vs 200 + assembled DAG
	// (dense coverage).
	DagReadinessTerminal
)

// GetDagReadiness derives the /dag readiness exclusively from the
// env_dispatch_run root task, independent of training_dispatch. Returns
// DagReadinessInProgress when no run exists, no root task is bound, or the root
// task is non-terminal. Returns DagReadinessTerminal when the root task is
// terminal, signaling the handler to assemble the DAG for the 200 decision.
// A non-ErrNoRows lookup error is surfaced to the caller (handler -> 503).
func (s *EnvDispatchService) GetDagReadiness(ctx context.Context, projectID, workspaceID string) (DagReadiness, error) {
	status, err := s.deps.GetEnvDispatchRootTaskStatus(ctx, projectID, workspaceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DagReadinessInProgress, nil
		}
		return DagReadinessInProgress, err
	}
	if !rootTaskTerminalStatuses[status] {
		return DagReadinessInProgress, nil
	}
	return DagReadinessTerminal, nil
}

// EnvCollaborationTrigger is the persisted source of truth for a branch to
// resume a channel collaboration without reusing a source runtime or task.
type EnvCollaborationTrigger struct {
	AgentID, Kind, ChannelID, ProjectID, ChatSessionID, SourceMessageID string
	ThreadRootMessageID                                                 *string
	TaskID, RuntimeID                                                   string
}

// ValidatedBranchMessageSource is checked before reset fan-out. It prevents a
// branch from creating any resources until its source trigger and roster are
// known to be safe to resume.
type ValidatedBranchMessageSource struct {
	SourceEnvID, SourceProjectID, SourceChannelID string
	Roster                                        MessageRoster
	Trigger                                       EnvCollaborationTrigger
	// TriggerSourceSandboxInstanceID is the source env's ready sandbox_instance
	// for the trigger agent (empty when the source binding was not ready). The
	// branch dispatch passes it to ProvisionEnvDispatchAgent so the trigger
	// agent's sandbox is cloned from the source state instead of created from
	// the saved policy. Empty ⇒ first-mention-style creation.
	TriggerSourceSandboxInstanceID string
}

type ChannelCopyMap struct {
	ChannelID  string
	MessageIDs map[string]string
}

// Env is a snapshot of an environment row.
type Env struct {
	ID          string
	WorkspaceID string
	// SandboxIDs holds one or more sandbox handles: an environment can host
	// many agents, each running in its own sandbox. Base envs carry a single
	// booted sandbox; branching an env forks every sandbox in the set.
	SandboxIDs  []string
	ParentEnvID string // empty for base
	Mode        EnvMode
	Domain      EnvDomain
}

// IssueRow is a snapshot of an issue row (subset needed by the service).
type IssueRow struct {
	ID          string
	ProjectID   string
	Title       string
	Description string
}

// EnvDispatchService orchestrates reset → dispatch (spec §7).
type EnvDispatchService struct {
	deps        EnvDispatchDeps
	concurrency int
	lifecycle   SandboxInstanceCreator // optional; nil ⇒ existing Fleet fork path
	// auditStorage is deliberately optional: ordinary env-dispatch requests do
	// not opt into audit correlation and must not incur audit persistence.
	auditStorage EnvDispatchAuditStorage
	// reclaimer is installed by the handler once the shared lifecycle cleanup
	// implementation is available. Keeping the dependency optional preserves
	// the current project-delete behavior until T020 delegates to it.
	reclaimer EnvDispatchReclaimer
}

// EnvDispatchReclamationRequest identifies one env-dispatch-owned scope for
// shared lifecycle cleanup. Exactly one of ProjectID and ChannelID is set by
// the caller. The Trigger is retained as a typed value so the reclaimer can
// create a matching audit obligation without inferring the delete path.
type EnvDispatchReclamationRequest struct {
	WorkspaceID string
	ProjectID   string
	ChannelID   string
	Trigger     EnvDispatchAuditObligationTrigger
}

// EnvDispatchReclaimer is the shared cleanup seam consumed by env-dispatch
// deletion entry points. Its implementation is intentionally deferred to the
// reclaimer task; a nil dependency leaves existing dispatch behavior intact.
type EnvDispatchReclaimer interface {
	Reclaim(ctx context.Context, request EnvDispatchReclamationRequest) error
}

// SandboxInstanceCreator creates a sandbox_instance-backed environment
// handle. *EnvSandboxLifecycleService satisfies it. When injected into
// EnvDispatchService, save/resume-capable (trained) rollouts create
// sandbox_instances instead of forking Fleet sandboxes.
type SandboxInstanceCreator interface {
	CreateSandboxInstance(ctx context.Context, in CreateSandboxInstanceInput, actorUserID string) (SandboxInstanceRef, error)
	// GetSandboxInstanceRef resolves the current ref (template, node, status)
	// for an existing sandbox_instance. Used by branch-from-template to derive
	// the source env's template when creating fresh sandbox_instances.
	GetSandboxInstanceRef(ctx context.Context, workspaceID, instanceID string) (SandboxInstanceRef, error)
	// DeleteSandboxInstance reclaims a sandbox_instance. Used by the auto-create
	// default-env path to clean up an instance when env creation fails or when a
	// concurrent writer lost the race to set the workspace default.
	DeleteSandboxInstance(ctx context.Context, ref SandboxInstanceRef, actorUserID string) error
}

func NewEnvDispatchService(deps EnvDispatchDeps, concurrency int) *EnvDispatchService {
	if concurrency < 1 {
		concurrency = 8
	}
	return &EnvDispatchService{deps: deps, concurrency: concurrency}
}

// WithSandboxLifecycle injects an optional sandbox_instance creator. When
// set, trained rollouts (train_agent_id present) create sandbox_instances and
// populate structured SandboxInstanceRefs; non-trained rollouts keep the
// Fleet fork path. Returns the service for chaining.
func (s *EnvDispatchService) WithSandboxLifecycle(lc SandboxInstanceCreator) *EnvDispatchService {
	s.lifecycle = lc
	return s
}

// WithAuditStorage injects the opt-in audit ledger. A nil storage disables
// audit recording, which is the default for ordinary dispatch construction.
func (s *EnvDispatchService) WithAuditStorage(storage EnvDispatchAuditStorage) *EnvDispatchService {
	s.auditStorage = storage
	return s
}

// WithReclaimer injects the shared env-dispatch lifecycle cleanup seam. A nil
// reclaimer preserves the current delete behavior until a caller explicitly
// provides the T018 implementation.
func (s *EnvDispatchService) WithReclaimer(reclaimer EnvDispatchReclaimer) *EnvDispatchService {
	s.reclaimer = reclaimer
	return s
}

// ErrAllDispatchFailed signals reset succeeded but every rollout's dispatch
// failed (spec §8 → 500). The returned result still carries rollouts[] so the
// caller can see the created envs/projects to clean up.
var ErrAllDispatchFailed = fmt.Errorf("dispatch_failed: all rollouts failed")

// Dispatch runs the unified dispatch flow.
func (s *EnvDispatchService) Dispatch(ctx context.Context, in EnvDispatchInput) (EnvDispatchResult, error) {
	// resume is an alias for branch (spec D1): normalize at the edge so there
	// is a single downstream code path and no new EnvMode.
	if in.Mode == "resume" {
		in.Mode = EnvModeBranch
	}

	if err := s.validate(in); err != nil {
		return EnvDispatchResult{}, err
	}
	// Scratch resolves its durable source task before an idempotency replay or
	// any fan-out. The local issue/message payload is materialized exclusively
	// from that immutable canonical source; request payloads are legacy-only
	// when source_task_id is absent.
	if in.Mode == EnvModeScratch && in.SourceTaskID != "" {
		source, err := s.deps.GetSourceTask(ctx, in.SourceTaskID, in.WorkspaceID)
		if err != nil {
			return EnvDispatchResult{}, fmt.Errorf("validation_failed: resolve source task: %w", err)
		}
		if err := materializeScratchSourceTask(&in, source); err != nil {
			return EnvDispatchResult{}, fmt.Errorf("validation_failed: %w", err)
		}
	}
	var messageRoster MessageRoster
	if in.DispatchType == EnvDispatchMessage {
		var err error
		messageRoster, err = s.deps.ResolveMessageRoster(ctx, in.WorkspaceID, in.AgentID)
		if err != nil {
			return EnvDispatchResult{}, fmt.Errorf("validation_failed: resolve message roster: %w", err)
		}
		in.MessageRoster = messageRoster
	}

	// DB-backed per-agent env spec validation (§5): reject unknown agents and
	// unknown/unauthorized env specs before any rollout state is created. No-op
	// when PerAgentEnvSpecs is empty, preserving current behavior.
	if err := s.validatePerAgentEnvSpecsDB(ctx, in); err != nil {
		return EnvDispatchResult{}, err
	}

	// Resolve the per-workspace default self_play base env when env_id is empty.
	// validate() guarantees this is only reachable for scratch+self_play. If a
	// default is configured, reuse it; otherwise auto-create one (D2) when a
	// sandbox lifecycle is available, so subsequent dispatches can fork it.
	if in.EnvID == "" {
		envID, err := s.ensureDefaultSelfPlayEnv(ctx, in)
		if err != nil {
			return EnvDispatchResult{}, err
		}
		in.EnvID = envID
	}

	env, err := s.deps.GetEnv(ctx, in.EnvID, in.WorkspaceID)
	if err != nil {
		return EnvDispatchResult{}, fmt.Errorf("get env: %w", err)
	}

	// Detect whether the base env's sandbox is a sandbox_instance (D3): an
	// auto-created default env (and any explicitly-passed instance-backed env_id)
	// must fork via the sandbox_instance backend, not the Fleet fork path. Probed
	// once per dispatch; a lookup miss means Fleet-backed (existing behavior).
	if s.lifecycle != nil && len(env.SandboxIDs) > 0 {
		if _, ierr := s.lifecycle.GetSandboxInstanceRef(ctx, in.WorkspaceID, env.SandboxIDs[0]); ierr == nil {
			in.InstanceBackedBase = true
		}
	}

	// Mode ↔ env kind cross-check (spec §6.3): scratch forks a base env;
	// branch forks a state env. Reject the mismatched combinations with a 400.
	if in.Mode == EnvModeScratch && env.Mode != EnvModeBase {
		return EnvDispatchResult{}, fmt.Errorf("validation_failed: scratch requires a base env (env_id is mode=%s)", env.Mode)
	}
	if in.Mode == EnvModeBranch && env.Mode == EnvModeBase {
		return EnvDispatchResult{}, fmt.Errorf("validation_failed: branch requires a state env (env_id is a base env)")
	}

	// Branch: resolve the single source project on this env (spec §7.2 step 0).
	// The 1:1 unique index guarantees exactly one; a base env has none → error.
	if in.Mode == EnvModeBranch && in.SourceProjectID == "" {
		pid, err := s.deps.GetProjectByEnvID(ctx, in.EnvID, in.WorkspaceID)
		if err != nil {
			return EnvDispatchResult{}, fmt.Errorf("validation_failed: resolve source project: %w", err)
		}
		in.SourceProjectID = pid
	}
	if in.Mode == EnvModeBranch && in.DispatchType == EnvDispatchMessage {
		validated, err := s.deps.ValidateBranchMessageSource(ctx, in.WorkspaceID, in.EnvID, in.SourceProjectID, messageRoster)
		if err != nil {
			return EnvDispatchResult{}, fmt.Errorf("validation_failed: branch message source: %w", err)
		}
		messageRoster = validated.Roster
		in.MessageRoster = validated.Roster
		in.BranchMessageSource = &validated
	}

	// Branch source shape (spec §7.4): v1 requires the source project to hold
	// exactly one dispatch target. Check upfront so zero/multiple yields a 400
	// before any fork/create work (and so dispatchOne never sees an empty
	// target for a branch, which would otherwise nil-deref on in.Issue).
	if in.Mode == EnvModeBranch {
		if err := s.validateBranchSource(ctx, in); err != nil {
			return EnvDispatchResult{}, err
		}
		source, err := s.deps.GetEnvDispatchRunSourceTask(ctx, in.SourceProjectID, in.WorkspaceID)
		if err != nil {
			return EnvDispatchResult{}, fmt.Errorf("validation_failed: resolve branch source task: %w", err)
		}
		if err := validateSourceTaskType(in.DispatchType, source); err != nil {
			return EnvDispatchResult{}, fmt.Errorf("validation_failed: %w", err)
		}
		if in.SourceTaskID != "" && in.SourceTaskID != source.ID {
			return EnvDispatchResult{}, fmt.Errorf("validation_failed: branch source_task_id must match the source project")
		}
		in.SourceTaskID = source.ID
		in.SourceTask = &source
	}

	// Idempotency replay runs only after the source task is resolved for either
	// scratch or branch dispatch, so replay never bypasses source authorization.
	if in.IdempotencyKey != "" {
		if prev, ok, err := s.deps.GetIdempotentResponse(ctx, in.WorkspaceID, in.IdempotencyKey); err != nil {
			return EnvDispatchResult{}, fmt.Errorf("idempotency lookup: %w", err)
		} else if ok {
			return prev, nil
		}
	}

	// Audit is intentionally started only after validation and source
	// resolution. This gives the run a trusted request scope while keeping
	// rejected inputs and ordinary (non-opted-in) dispatches write-free.
	ctx, audit := startEnvDispatchAudit(ctx, s.auditStorage, in)

	rollouts := make([]EnvRollout, in.GroupSize)
	sem := make(chan struct{}, s.concurrency)
	var wg sync.WaitGroup
	var resetErrs []error
	var resetErrMu sync.Mutex

	for i := 0; i < in.GroupSize; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			r, err := s.resetOne(ctx, in, env, messageRoster, idx)
			if err != nil {
				resetErrMu.Lock()
				resetErrs = append(resetErrs, fmt.Errorf("rollout %d reset: %w", idx, err))
				resetErrMu.Unlock()
				return
			}
			rollouts[idx] = r
		}(i)
	}
	wg.Wait()

	if len(resetErrs) > 0 {
		audit.recordEvent(ctx, EnvDispatchAuditEventCreationFailed, "", auditReasonResetFailed)
		audit.recordEvent(ctx, EnvDispatchAuditEventRollbackStarted, "", auditReasonResetRollback)
		// Reset failed for >=1 rollout -> roll back every rollout and return a
		// reset_failed error (handler -> 503). Reset is all-or-nothing. %w (not %v)
		// so the adapter-origin StackError survives Unwrap and the handler can render
		// its traceback; the literal "reset_failed:" prefix still drives the
		// handler's strings.Contains classification.
		for i, r := range rollouts {
			if r.ProjectID != "" || r.EnvID != "" {
				s.rollbackRollout(ctx, in.WorkspaceID, r)
				// Shared mode (US3/T024): the rolled-back sibling's pre-created
				// shared runtime R' must not linger either.
				s.reclaimSharedRuntimes(ctx, in, r.SandboxRefs)
			}
			rollouts[i] = EnvRollout{}
		}
		audit.complete(ctx, EnvDispatchAuditOutcomeFailed)
		return EnvDispatchResult{}, fmt.Errorf("reset_failed: %w", resetErrs[0])
	}

	// Persist the durable dispatch root row (spec: durable dispatch identity
	// independent of training_dispatch). One row per project, keyed by
	// project_id, carrying workspace_id + training_mode. root_task_id starts
	// NULL and is bound after the leader task is enqueued (below). Best-effort:
	// a creation failure is recorded on the rollout but does not fail the
	// dispatch; /dag treats a missing row as in_progress (no root task yet).
	for i := range rollouts {
		if rollouts[i].ProjectID == "" {
			continue
		}
		if in.SourceTaskID != "" {
			runID, err := s.deps.CreateEnvDispatchRunWithSource(ctx, rollouts[i].ProjectID, in.WorkspaceID, in.TrainingMode, in.SourceTaskID, i)
			if err != nil {
				rollouts[i].Error = fmt.Sprintf("create source-aware env_dispatch_run: %v", err)
				continue
			}
			rollouts[i].RunID = runID
			rollouts[i].SourceTaskID = in.SourceTaskID
			continue
		}
		if err := s.deps.CreateEnvDispatchRun(ctx, rollouts[i].ProjectID, in.WorkspaceID, in.TrainingMode); err != nil {
			rollouts[i].Error = fmt.Sprintf("create env_dispatch_run: %v", err)
		}
	}

	// Persist training intent per rollout project (spec §4.1) BEFORE dispatch:
	// the session-open hook (fired when the trained member's task is created
	// during dispatch) resolves the training target + default reward by
	// project_id, so the row must exist by the time dispatchOne enqueues the
	// run. Only when a train_agent_id is set; otherwise this is a no-op and
	// behavior is unchanged. Best-effort per rollout (a save failure is not
	// fatal to the dispatch; the open-hook simply finds no training row).
	if in.TrainAgentID != "" {
		for i := range rollouts {
			if rollouts[i].ProjectID == "" {
				continue
			}
			if err := s.deps.SaveTrainingDispatch(ctx, rollouts[i].ProjectID, in.WorkspaceID, in.TrainAgentID, in.CriticAgentID, DefaultTrainingReward); err != nil {
				// Non-fatal: record on the rollout so the caller can see the
				// training row was not persisted, but continue the dispatch.
				rollouts[i].Error = fmt.Sprintf("save training dispatch: %v", err)
			}
		}
	}

	// Dispatch phase: best-effort, per-rollout errors recorded in rollouts[i].Error.
	var dispatchWG sync.WaitGroup
	for i := 0; i < in.GroupSize; i++ {
		dispatchWG.Add(1)
		go func(idx int) {
			defer dispatchWG.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			s.dispatchOne(ctx, in, &rollouts[idx], idx)
		}(i)
	}
	dispatchWG.Wait()
	for _, rollout := range rollouts {
		if rollout.Error != "" {
			// Keep error contents out of the ledger: the stable reason code is
			// enough to establish that dispatch creation did not complete.
			audit.recordEvent(ctx, EnvDispatchAuditEventCreationFailed, "", auditReasonDispatchFailed)
		}
	}

	// Local targets are per-rollout materialization provenance, not reusable
	// sampling identities. Persist them only for source-aware runs after the
	// issue/channel has been created or copied.
	if in.SourceTaskID != "" {
		for i := range rollouts {
			if rollouts[i].ProjectID == "" {
				continue
			}
			if err := s.deps.SetEnvDispatchRunLocalTargets(ctx, rollouts[i].ProjectID, in.WorkspaceID, rollouts[i].IssueID, rollouts[i].ChannelID); err != nil && rollouts[i].Error == "" {
				rollouts[i].Error = fmt.Sprintf("set env_dispatch local targets: %v", err)
			}
		}
	}

	// Bind the enqueued leader task as the dispatch root (spec: bind
	// root_task_id immediately after enqueuing the leader task). The leader task
	// is the rollout's AgentRunID (set by every dispatchOne path: issue,
	// self_play, scratch-channel, and branch-channel). Best-effort: a binding
	// failure is recorded on the rollout but does not fail the dispatch; /dag
	// treats an unbound root as in_progress until the binding succeeds.
	for i := range rollouts {
		if rollouts[i].ProjectID == "" || rollouts[i].AgentRunID == "" {
			continue
		}
		if err := s.deps.BindEnvDispatchRootTask(ctx, rollouts[i].ProjectID, rollouts[i].AgentRunID); err != nil {
			if rollouts[i].Error == "" {
				rollouts[i].Error = fmt.Sprintf("bind env_dispatch root task: %v", err)
			}
		}
	}

	// Top-level project_id for the response: the single project the dispatch
	// produces (group_size=1 today). AReaL consumes one project_id via
	// GET /api/v1/env-dispatch/{projectID}/dag, so surface it directly rather
	// than forcing the client to dig it out of rollouts[0].
	projectID := ""
	if len(rollouts) > 0 {
		projectID = rollouts[0].ProjectID
	}
	channelID := ""
	if len(rollouts) > 0 {
		channelID = rollouts[0].ChannelID
	}
	result := EnvDispatchResult{ChannelID: channelID, ProjectID: projectID, Rollouts: rollouts}

	// Persist the idempotency response so a retry replays it (spec §7.7). Best-effort.
	if in.IdempotencyKey != "" {
		_ = s.deps.SaveIdempotentResponse(ctx, in.WorkspaceID, in.IdempotencyKey, result)
	}

	// Status rule (spec §6.3/§8): ≥1 dispatched → nil (201); all failed →
	// ErrAllDispatchFailed (handler → 500, body still carries rollouts[]).
	succeeded := 0
	for _, r := range rollouts {
		if r.AgentRunID != "" {
			succeeded++
		}
	}
	if succeeded == 0 {
		audit.complete(ctx, EnvDispatchAuditOutcomeFailed)
		result.Audit = audit.reportResult()
		return result, ErrAllDispatchFailed
	}
	audit.complete(ctx, EnvDispatchAuditOutcomeSucceeded)
	result.Audit = audit.reportResult()
	return result, nil
}

// ensureDefaultSelfPlayEnv resolves the base env to fork for a scratch+self_play
// dispatch with an empty env_id. If the workspace already has a configured
// default self_play base env, it is reused. Otherwise the service auto-creates
// one: it creates a sandbox_instance from the configured template, inserts a
// mode='base' env row, and persists it as the workspace default. The set is
// conditional (only-if-NULL) so the first of N concurrent writers wins; the
// service re-reads the canonical default and cleans up its own env when it lost
// the race. Requires an injected sandbox lifecycle creator; without one (test
// fixtures / no DB) it returns the legacy "not configured" validation error so
// behavior is unchanged there.
func (s *EnvDispatchService) ensureDefaultSelfPlayEnv(ctx context.Context, in EnvDispatchInput) (string, error) {
	if envID, err := s.deps.GetDefaultSelfPlayEnv(ctx, in.WorkspaceID); err == nil && envID != "" {
		return envID, nil
	}
	if s.lifecycle == nil {
		return "", fmt.Errorf("validation_failed: default self-play env not configured")
	}
	template := in.DefaultBaseTemplate
	if template == "" {
		template = "default"
	}
	ref, err := s.lifecycle.CreateSandboxInstance(ctx, CreateSandboxInstanceInput{
		WorkspaceID: in.WorkspaceID,
		Template:    template,
	}, in.UserID)
	if err != nil {
		return "", fmt.Errorf("auto-create default env: boot sandbox: %w", err)
	}
	envID, err := s.deps.CreateEnv(ctx, in.WorkspaceID, []string{ref.InstanceID}, "", EnvModeBase, EnvDomainSelfPlay)
	if err != nil {
		_ = s.lifecycle.DeleteSandboxInstance(ctx, ref, in.UserID) // best-effort: reclaim the pending instance
		return "", fmt.Errorf("auto-create default env: create env: %w", err)
	}
	// Conditional set: first concurrent writer wins. Ignore the error (best-
	// effort) and re-read to pick up the canonical default.
	_ = s.deps.SetDefaultSelfPlayEnv(ctx, in.WorkspaceID, envID)
	winner, err := s.deps.GetDefaultSelfPlayEnv(ctx, in.WorkspaceID)
	if err != nil || winner == "" {
		// Nothing persisted as the default; still proceed with the env we created
		// so this dispatch can fork it (a later dispatch will retry the set).
		return envID, nil
	}
	if winner != envID {
		// Lost the race: another writer's env became the default. Clean up ours
		// (env row + the sandbox_instance we created) and fork the winner instead.
		_ = s.deps.DeleteEnv(ctx, envID, in.WorkspaceID)
		_ = s.lifecycle.DeleteSandboxInstance(ctx, ref, in.UserID)
		return winner, nil
	}
	return envID, nil
}

// materializeScratchSourceTask validates that source type matches dispatch type
// and replaces the inline payload with the source's canonical immutable data.
func materializeScratchSourceTask(in *EnvDispatchInput, source SourceTask) error {
	if err := validateSourceTaskType(in.DispatchType, source); err != nil {
		return err
	}
	var err error
	switch in.DispatchType {
	case EnvDispatchIssue:
		in.Issue, err = source.IssueInput()
	case EnvDispatchMessage:
		in.Message, err = source.MessageInput()
	}
	if err != nil {
		return fmt.Errorf("materialize source task: %w", err)
	}
	in.SourceTaskID = source.ID
	in.SourceTask = &source
	return nil
}

func validateSourceTaskType(dispatchType EnvDispatchType, source SourceTask) error {
	expected := SourceTaskIssue
	if dispatchType == EnvDispatchMessage {
		expected = SourceTaskMessage
	}
	if source.Type != expected {
		return fmt.Errorf("source task type %q does not match dispatch_type %q", source.Type, dispatchType)
	}
	return nil
}

// validate implements the §6.3 validation table (the subset that's
// service-level; UUID-shape validation lives in the handler).
func (s *EnvDispatchService) validate(in EnvDispatchInput) error {
	if in.Mode != EnvModeScratch && in.Mode != EnvModeBranch {
		return fmt.Errorf("validation_failed: mode must be scratch or branch")
	}
	if in.DispatchType != EnvDispatchIssue && in.DispatchType != EnvDispatchMessage {
		return fmt.Errorf("validation_failed: dispatch_type must be issue or message")
	}
	if in.GroupSize < 1 || in.GroupSize > 64 {
		return fmt.Errorf("validation_failed: group_size must be in [1, 64]")
	}
	if in.AgentID == "" {
		return fmt.Errorf("validation_failed: agent_id is required")
	}
	// training_mode (Task 1): false forbids training IDs; true requires
	// train_agent_id. The handler rejects an omitted training_mode before
	// constructing EnvDispatchInput, so the service only sees an explicit
	// boolean.
	if !in.TrainingMode && (in.TrainAgentID != "" || in.CriticAgentID != "") {
		return fmt.Errorf("validation_failed: training_mode=false forbids train_agent_id and critic_agent_id")
	}
	if in.TrainingMode && in.TrainAgentID == "" {
		return fmt.Errorf("validation_failed: training_mode=true requires train_agent_id")
	}
	// train_agent_id (spec §4.1): the training target. Allowed only when it
	// equals agent_id (single-agent training). Empty ⇒ today's behavior
	// exactly (no new error). DB membership resolution is enforced later,
	// not here.
	if in.TrainAgentID != "" && in.TrainAgentID != in.AgentID {
		return fmt.Errorf("validation_failed: train_agent_id must equal agent_id (single-agent training)")
	}
	// critic_agent_id (sub-project E): the critic that evaluates the trained agent.
	// Requires train_agent_id. Must differ from train_agent_id and agent_id.
	if in.CriticAgentID != "" {
		if in.TrainAgentID == "" {
			return fmt.Errorf("validation_failed: critic_agent_id requires train_agent_id")
		}
		if in.CriticAgentID == in.TrainAgentID {
			return fmt.Errorf("validation_failed: critic_agent_id must differ from train_agent_id")
		}
		if in.CriticAgentID == in.AgentID {
			return fmt.Errorf("validation_failed: critic_agent_id must differ from agent_id")
		}
	}
	if in.Domain != EnvDomainSweLego && in.Domain != EnvDomainSelfPlay {
		return fmt.Errorf("validation_failed: domain is required (swe_lego or self_play)")
	}
	if in.Domain == EnvDomainSweLego && in.DispatchType == EnvDispatchMessage {
		return fmt.Errorf("validation_failed: swe_lego domain is issue-only")
	}
	if in.Domain == EnvDomainSelfPlay && in.DispatchType == EnvDispatchIssue {
		return fmt.Errorf("not_implemented: self_play + issue dispatch")
	}
	if in.Mode == EnvModeBranch && in.Domain == EnvDomainSweLego && in.Issue != nil {
		return fmt.Errorf("validation_failed: issue must not be supplied for branch+swe_lego (copied issue is reused)")
	}
	if in.Mode == EnvModeScratch && in.Domain == EnvDomainSweLego && in.Issue == nil && in.SourceTaskID == "" {
		return fmt.Errorf("validation_failed: issue required for scratch+swe_lego")
	}
	if in.DispatchType == EnvDispatchMessage && (in.Message == nil || in.Message.Content == "") && in.SourceTaskID == "" {
		return fmt.Errorf("validation_failed: message.content required")
	}
	// env_id may be empty ONLY for scratch+self_play (resolves the workspace
	// default base env); every other combination requires an explicit env_id.
	if in.EnvID == "" {
		if in.Mode != EnvModeScratch || in.Domain != EnvDomainSelfPlay {
			return fmt.Errorf("validation_failed: env_id is required except for scratch self_play")
		}
	}
	// shared_sandbox is accepted for every mode (scratch/branch, including
	// resume-normalized inputs), both dispatch types, and the single-agent
	// degenerate case; it carries no rejection rules of its own.
	if err := validatePerAgentEnvSpecsShape(in); err != nil {
		return err
	}
	return nil
}

// validatePerAgentEnvSpecsShape enforces the synchronous shape rules for
// per-agent env specs: every spec needs an agent_id, at most one of
// template/base_env_id (a runtime object is a valid third option for
// non-training scratch message dispatch), and no duplicate agents. When a
// runtime is present it must be on a scratch message dispatch that is not the
// training target, and the runtime must be well-formed (absolute HTTP(S)
// URL, all fields). DB-backed membership and env-spec resolution happen later
// in validatePerAgentEnvSpecsDB (ctx). Error messages name fields and agent
// IDs but never format runtime values.
func validatePerAgentEnvSpecsShape(in EnvDispatchInput) error {
	seen := make(map[string]struct{}, len(in.PerAgentEnvSpecs))
	for _, s := range in.PerAgentEnvSpecs {
		if s.AgentID == "" {
			return fmt.Errorf("validation_failed: per_agent_env agent_id is required")
		}
		hasTemplate := s.Template != ""
		hasBase := s.BaseEnvID != ""
		hasRuntime := s.Runtime != nil
		if !hasTemplate && !hasBase && !hasRuntime {
			return fmt.Errorf("validation_failed: per_agent_env spec for agent %s needs a template, base_env_id, or runtime", s.AgentID)
		}
		if hasTemplate && hasBase {
			return fmt.Errorf("validation_failed: per_agent_env spec for agent %s must set template or base_env_id, not both", s.AgentID)
		}
		if _, dup := seen[s.AgentID]; dup {
			return fmt.Errorf("validation_failed: per_agent_env agent_id %s is duplicated", s.AgentID)
		}
		seen[s.AgentID] = struct{}{}
		if hasRuntime {
			// A caller runtime is accepted only for non-training scratch
			// message dispatch; branch/issue/training targets fail before any
			// rollout resource is created.
			if in.Mode != EnvModeScratch || in.DispatchType != EnvDispatchMessage {
				return fmt.Errorf("validation_failed: per_agent_env runtime for agent %s is only allowed for scratch message dispatch", s.AgentID)
			}
			if in.TrainAgentID != "" && s.AgentID == in.TrainAgentID {
				return fmt.Errorf("validation_failed: per_agent_env runtime for agent %s is not allowed for the training target", s.AgentID)
			}
			if _, err := NormalizeExternalModelRuntime(s.Runtime); err != nil {
				return fmt.Errorf("validation_failed: per_agent_env spec for agent %s: %w", s.AgentID, err)
			}
		}
	}
	return nil
}

// validatePerAgentEnvSpecsDB runs the DB-backed per-agent env spec validation
// (§5): for each spec, verify the agent is a workspace/squad member and the
// template/base_env_id is known and authorized. Preserves current behavior
// when PerAgentEnvSpecs is empty (no DB calls). Called after the synchronous
// shape validation passes, before any rollout state is created.
func (s *EnvDispatchService) validatePerAgentEnvSpecsDB(ctx context.Context, in EnvDispatchInput) error {
	if len(in.PerAgentEnvSpecs) == 0 {
		return nil
	}
	for _, spec := range in.PerAgentEnvSpecs {
		if err := s.deps.ValidateAgentInWorkspace(ctx, in.WorkspaceID, spec.AgentID); err != nil {
			return fmt.Errorf("validation_failed: per_agent_env agent %s: %w", spec.AgentID, err)
		}
		if _, err := s.deps.ResolvePerAgentEnvSpec(ctx, in.WorkspaceID, spec); err != nil {
			return fmt.Errorf("validation_failed: per_agent_env spec for agent %s: %w", spec.AgentID, err)
		}
	}
	return nil
}

// validateBranchSource enforces the §7.4 v1 constraint that a branch source
// project contains exactly one dispatch target: one swe_lego issue (issue
// dispatch) or one chat session (message dispatch). Zero or multiple → 400.
func (s *EnvDispatchService) validateBranchSource(ctx context.Context, in EnvDispatchInput) error {
	switch in.Domain {
	case EnvDomainSweLego:
		issues, err := s.deps.ListIssuesByProject(ctx, in.SourceProjectID, in.WorkspaceID)
		if err != nil {
			return fmt.Errorf("validation_failed: list source issues: %w", err)
		}
		if len(issues) != 1 {
			return fmt.Errorf("validation_failed: branch+swe_lego requires the source project to have exactly one issue (found %d)", len(issues))
		}
	case EnvDomainSelfPlay:
		sessions, err := s.deps.ListChatSessionsByProject(ctx, in.SourceProjectID, in.WorkspaceID)
		if err != nil {
			return fmt.Errorf("validation_failed: list source chat sessions: %w", err)
		}
		if len(sessions) != 1 {
			return fmt.Errorf("validation_failed: branch+self_play requires the source project to have exactly one chat session (found %d)", len(sessions))
		}
	}
	return nil
}

// resetOne does the per-rollout reset (sandbox + env + project) per §7.2.
func (s *EnvDispatchService) resetOne(ctx context.Context, in EnvDispatchInput, sourceEnv Env, roster MessageRoster, idx int) (EnvRollout, error) {
	// sandbox_instance backend bridge (D7): save/resume-capable (trained)
	// rollouts create sandbox_instances via the injected lifecycle creator
	// instead of forking Fleet sandboxes, and carry structured SandboxRefs.
	// Non-trained rollouts (or no creator injected) keep the Fleet fork path.
	var forked []string
	var sandboxRefs []SandboxInstanceRef
	var agentSandboxRefs map[string]SandboxInstanceRef
	reserveMessageEnv := in.DispatchType == EnvDispatchMessage
	if reserveMessageEnv {
		// A message rollout's sandboxes belong to per-agent channel bindings
		// (the leader for scratch, the trigger agent for branch). Reserve the
		// env with no sandboxes so dispatch can attach the exact binding sandbox
		// after provisioning; never Fleet-fork the source sandbox set, which for
		// a message env is a sandbox_instance Fleet cannot fork.
		forked = []string{}
	} else if s.useSandboxInstanceBackend(in) {
		leaderID := in.AgentID
		if in.DispatchType == EnvDispatchMessage && roster.LeaderID != "" {
			leaderID = roster.LeaderID
		}
		refs, agentRefs, err := s.createSandboxInstanceRefs(ctx, in, sourceEnv, leaderID)
		if err != nil {
			return EnvRollout{}, fmt.Errorf("create sandbox_instance: %w", err)
		}
		for _, r := range refs {
			forked = append(forked, r.InstanceID)
			sandboxRefs = append(sandboxRefs, r)
		}
		agentSandboxRefs = agentRefs
	} else {
		// Branch always forks (spec §4.3): the source sandbox set is never reused
		// in place, so the source state stays re-branchable (MCTS). Scratch forks
		// the base. An env can hold many sandboxes (one per agent), so fork every
		// one and carry the full set into the new env row.
		f, err := s.forkAll(ctx, sourceEnv.SandboxIDs, idx)
		if err != nil {
			return EnvRollout{}, fmt.Errorf("fork sandbox: %w", err)
		}
		forked = f
		for _, sandboxID := range forked {
			envDispatchAuditFromContext(ctx).recordResource(ctx, EnvDispatchAuditResourceSandbox, sandboxID, "", "", "", "")
		}
	}
	mode := EnvModeScratch
	if in.Mode == EnvModeBranch {
		mode = EnvModeBranch
	}
	envID, err := s.deps.CreateEnv(ctx, in.WorkspaceID, forked, sourceEnv.ID, mode, in.Domain)
	if err != nil {
		s.deleteSandboxes(ctx, forked)
		s.reclaimSharedRuntimes(ctx, in, sandboxRefs)
		return EnvRollout{}, fmt.Errorf("create env: %w", err)
	}
	audit := envDispatchAuditFromContext(ctx)
	for _, sandboxID := range forked {
		audit.recordResource(ctx, EnvDispatchAuditResourceSandbox, sandboxID, envID, "", "", "")
	}
	for _, ref := range sandboxRefs {
		audit.recordSandboxRef(ctx, ref, envID, "")
	}
	// Project
	var projectID string
	var issueIDMap, chatSessionIDMap map[string]string
	if in.Mode == EnvModeScratch {
		name := fmt.Sprintf("env-dispatch-%s", envID) // unique, spec §7.2
		pid, err := s.deps.CreateProject(ctx, in.WorkspaceID, name, envID)
		if err != nil {
			s.rollbackRollout(ctx, in.WorkspaceID, EnvRollout{EnvID: envID})
			s.reclaimSharedRuntimes(ctx, in, sandboxRefs)
			return EnvRollout{}, fmt.Errorf("create project: %w", err)
		}
		projectID = pid
	} else {
		// branch — copy source project subtree (issues + chat sessions).
		// in.SourceProjectID is resolved by the handler from the 1:1 env→project.
		pid, imap, smap, err := s.deps.CopyProjectSubtree(ctx, in.SourceProjectID, in.WorkspaceID, envID)
		if err != nil {
			s.rollbackRollout(ctx, in.WorkspaceID, EnvRollout{EnvID: envID})
			s.reclaimSharedRuntimes(ctx, in, sandboxRefs)
			return EnvRollout{}, fmt.Errorf("copy project: %w", err)
		}
		projectID = pid
		issueIDMap = imap
		chatSessionIDMap = smap
	}

	r := EnvRollout{EnvID: envID, ProjectID: projectID, SandboxRefs: sandboxRefs, AgentSandboxRefs: agentSandboxRefs}
	for _, sandboxID := range forked {
		audit.recordResource(ctx, EnvDispatchAuditResourceSandbox, sandboxID, envID, projectID, "", "")
	}
	for _, ref := range sandboxRefs {
		audit.recordSandboxRef(ctx, ref, envID, projectID)
	}
	if in.DispatchType == EnvDispatchMessage && in.Mode == EnvModeBranch {
		if in.BranchMessageSource == nil {
			s.rollbackRollout(ctx, in.WorkspaceID, r)
			s.reclaimSharedRuntimes(ctx, in, r.SandboxRefs)
			return EnvRollout{}, fmt.Errorf("missing validated branch message source")
		}
		copyMap, err := s.deps.CopyEnvDispatchChannel(ctx, in.WorkspaceID, in.BranchMessageSource.SourceChannelID, projectID, envID)
		if err != nil {
			s.rollbackRollout(ctx, in.WorkspaceID, r)
			s.reclaimSharedRuntimes(ctx, in, r.SandboxRefs)
			return EnvRollout{}, fmt.Errorf("copy env-dispatch channel: %w", err)
		}
		r.ChannelID = copyMap.ChannelID
		envDispatchAuditFromContext(ctx).recordChannelBindings(ctx, copyMap.ChannelID, projectID, envID, roster)
		r.channelMessageIDs = copyMap.MessageIDs
		if sessionID := chatSessionIDMap[in.BranchMessageSource.Trigger.ChatSessionID]; sessionID != "" {
			r.ChatSessionID = sessionID
		}
	}
	if in.DispatchType == EnvDispatchMessage && in.Mode == EnvModeScratch {
		// Scratch message rollouts provision through per-agent channel bindings,
		// so the binding specs are the resolved per-agent sandbox policies. The
		// sandbox_instance backend (agentSandboxRefs) is never used for scratch
		// message dispatch (useSandboxInstanceBackend returns false), so binding
		// specs are built solely from PerAgentEnvSpecs. In shared mode every
		// roster binding is stamped shared (including members without an
		// explicit spec) so first-mention provisioning attaches to the sample's
		// shared runtime instead of claiming a per-agent sandbox.
		var bindingSpecs map[string]ResolvedPerAgentSandboxPolicy
		if len(in.PerAgentEnvSpecs) > 0 || in.SharedSandbox {
			bindingSpecs = make(map[string]ResolvedPerAgentSandboxPolicy, len(roster.AgentIDs))
			for _, spec := range in.PerAgentEnvSpecs {
				policy, err := s.deps.ResolvePerAgentEnvSpec(ctx, in.WorkspaceID, spec)
				if err != nil {
					s.rollbackRollout(ctx, in.WorkspaceID, r)
					return EnvRollout{}, fmt.Errorf("resolve channel sandbox policy: %w", err)
				}
				bindingSpecs[spec.AgentID] = policy
			}
			if in.SharedSandbox {
				for _, agentID := range roster.AgentIDs {
					policy := bindingSpecs[agentID]
					policy.Shared = true
					bindingSpecs[agentID] = policy
				}
			}
		}
		channelID, err := s.deps.CreateEnvDispatchChannel(ctx, in.WorkspaceID, in.UserID, projectID, envID, roster, bindingSpecs)
		if err != nil {
			s.rollbackRollout(ctx, in.WorkspaceID, r)
			return EnvRollout{}, fmt.Errorf("create env-dispatch channel: %w", err)
		}
		r.ChannelID = channelID
		envDispatchAuditFromContext(ctx).recordChannelBindings(ctx, channelID, projectID, envID, roster)
	}
	// Stash the single copied entity for dispatchOne (spec §7.4: exactly one).
	if in.Mode == EnvModeBranch {
		if in.Domain == EnvDomainSweLego {
			for _, newID := range issueIDMap {
				r.IssueID = newID
				break
			}
		} else if in.Domain == EnvDomainSelfPlay {
			for _, newID := range chatSessionIDMap {
				r.ChatSessionID = newID
				break
			}
		}
	}
	return r, nil
}

// useSandboxInstanceBackend reports whether this rollout should create
// sandbox_instance-backed sandboxes (D7). True when a lifecycle creator is
// injected AND the rollout is save/resume-capable (train_agent_id present —
// trained rollouts are the checkpointing target). Non-trained rollouts keep
// the Fleet fork path.
func (s *EnvDispatchService) useSandboxInstanceBackend(in EnvDispatchInput) bool {
	// Scratch message dispatch provisions the leader through its channel binding
	// after the project and channel exist. Creating an instance here would
	// pre-create a second runtime and let the task bypass that binding.
	if in.DispatchType == EnvDispatchMessage && in.Mode == EnvModeScratch {
		return false
	}
	// train_agent_id keeps the existing D7 trained-rollout path. InstanceBackedBase
	// extends it to any dispatch forking a sandbox_instance-backed base env (e.g.
	// an auto-created default self_play env), so the non-trained self_play case
	// forks via the sandbox_instance backend instead of the Fleet fork path.
	return s.lifecycle != nil && (in.TrainAgentID != "" || in.InstanceBackedBase || in.TaskTemplateID != "")
}

// createSandboxInstanceRefs creates one sandbox_instance per per-agent env
// spec, or a single default sandbox_instance when no per-agent specs are set.
// In shared mode (SharedSandbox=true) it collapses the per-agent dimension:
// exactly one sandbox_instance + one pre-created runtime R' per rollout, with
// every per-agent ref pointing at it (research D1/D2). Branch (D7) creates
// from the source env's template; scratch creates from the requested template.
// v1 resolves the workspace/node via the lifecycle creator deps; production
// adapter wiring (node selection) is injected by the handler. Returns the flat
// slice (for SandboxIDs/SandboxRefs) and, when per-agent specs are used, an
// agent_id→ref map for AgentSandboxRefs.
func (s *EnvDispatchService) createSandboxInstanceRefs(ctx context.Context, in EnvDispatchInput, sourceEnv Env, runtimeAgentID string) ([]SandboxInstanceRef, map[string]SandboxInstanceRef, error) {
	if in.SharedSandbox {
		return s.createSharedSandboxInstanceRefs(ctx, in, sourceEnv, runtimeAgentID)
	}
	if len(in.PerAgentEnvSpecs) > 0 {
		refs := make([]SandboxInstanceRef, 0, len(in.PerAgentEnvSpecs))
		agentRefs := make(map[string]SandboxInstanceRef, len(in.PerAgentEnvSpecs))
		for _, spec := range in.PerAgentEnvSpecs {
			template := spec.Template
			if in.TaskTemplateID != "" {
				template = in.TaskTemplateID
			}
			// spec.Template is used directly; BaseEnvID → template resolution is
			// deferred to Step 6c (production adapter wiring). Shape validation
			// guarantees exactly one of Template/BaseEnvID is set.
			// Per-agent sandbox (typically a squad). DaemonEnabled boots a daemon
			// that registers its own runtime; pre-creating R' + routing is
			// deferred (single-agent default path above handles R' for now).
			ref, err := s.lifecycle.CreateSandboxInstance(ctx, CreateSandboxInstanceInput{
				WorkspaceID:   in.WorkspaceID,
				Template:      template,
				DaemonEnabled: true,
			}, in.UserID)
			if err != nil {
				return nil, nil, err
			}
			refs = append(refs, ref)
			agentRefs[spec.AgentID] = ref
		}
		return refs, agentRefs, nil
	}
	// No per-agent specs: create one default sandbox_instance for the rollout.
	template := "default"
	if len(sourceEnv.SandboxIDs) > 0 && (in.Mode == EnvModeBranch || in.InstanceBackedBase) {
		// Derive the template from the source env's first sandbox_instance
		// rather than a live fork. Branch (D7) does this to continue the copied
		// conversation in a fresh sandbox; an instance-backed scratch base env
		// (e.g. auto-created default) does it so rollouts reuse the default's
		// configured template. The source DB subtree is still copied by
		// CopyProjectSubtree for branch.
		sourceRef, err := s.lifecycle.GetSandboxInstanceRef(ctx, in.WorkspaceID, sourceEnv.SandboxIDs[0])
		if err != nil {
			return nil, nil, fmt.Errorf("resolve source sandbox template: %w", err)
		}
		if sourceRef.Template != "" {
			template = sourceRef.Template
		}
	}
	if in.TaskTemplateID != "" {
		template = in.TaskTemplateID
	}
	// Phase 2: for a single-agent rollout, pre-create the agent_runtime row R'
	// keyed by a fresh daemon_id and inject it as MULTICA_DAEMON_ID into the
	// sandbox runtime_env. The in-sandbox daemon adopts R' on register, and the
	// task is routed to R' (see dispatchOne) - runtime_id is deterministic at
	// dispatch time, no deferred binding.
	runtimeEnv := map[string]string{}
	var runtimeID, daemonID string
	if runtimeAgentID != "" {
		rid, did, err := s.deps.PrecreateAgentRuntime(ctx, in.WorkspaceID, in.UserID, runtimeAgentID)
		if err != nil {
			return nil, nil, fmt.Errorf("precreate agent runtime: %w", err)
		}
		runtimeID, daemonID = rid, did
		runtimeEnv["MULTICA_DAEMON_ID"] = daemonID
	}
	ref, err := s.lifecycle.CreateSandboxInstance(ctx, CreateSandboxInstanceInput{
		WorkspaceID:   in.WorkspaceID,
		Template:      template,
		DaemonEnabled: true,
		RuntimeEnv:    runtimeEnv,
	}, in.UserID)
	if err != nil {
		// Sandbox create failed after R' was pre-created: reclaim R' so the
		// offline row does not linger (best-effort; the runtime GC is backstop).
		if runtimeID != "" {
			_ = s.deps.DeleteAgentRuntime(ctx, in.WorkspaceID, runtimeID)
		}
		return nil, nil, err
	}
	ref.RuntimeID = runtimeID
	ref.DaemonID = daemonID
	envDispatchAuditFromContext(ctx).recordSandboxRef(ctx, ref, "", "")
	return []SandboxInstanceRef{ref}, nil, nil
}

// createSharedSandboxInstanceRefs provisions the sample's single shared
// execution environment (research D1/D2, FR-003/004/005): one sandbox_instance
// + one pre-created daemon runtime R' per rollout, with every roster agent's
// ref pointing at the same ref. group_size>1 rollouts stay isolated because
// the collapse happens strictly inside the per-rollout pipeline.
//
// Template resolution: a shared rollout runs every agent in one sandbox, so a
// single template must win — the runtime owner's spec template when present,
// else the first spec's, else the default branch/instance-backed derivation
// (per-agent base_env_id fan-out is meaningless under sharing and falls back
// to the same default). Returns the one-ref slice plus, when per-agent specs
// exist, an agent_id→ref map whose values are all the shared ref.
func (s *EnvDispatchService) createSharedSandboxInstanceRefs(ctx context.Context, in EnvDispatchInput, sourceEnv Env, runtimeAgentID string) ([]SandboxInstanceRef, map[string]SandboxInstanceRef, error) {
	template := "default"
	if len(sourceEnv.SandboxIDs) > 0 && (in.Mode == EnvModeBranch || in.InstanceBackedBase) {
		sourceRef, err := s.lifecycle.GetSandboxInstanceRef(ctx, in.WorkspaceID, sourceEnv.SandboxIDs[0])
		if err != nil {
			return nil, nil, fmt.Errorf("resolve source sandbox template: %w", err)
		}
		if sourceRef.Template != "" {
			template = sourceRef.Template
		}
	}
	if specTemplate := sharedRolloutSpecTemplate(in.PerAgentEnvSpecs, runtimeAgentID); specTemplate != "" {
		template = specTemplate
	}
	if in.TaskTemplateID != "" {
		template = in.TaskTemplateID
	}
	// Pre-create the shared agent_runtime R' keyed by a fresh daemon_id and
	// inject it as MULTICA_DAEMON_ID, exactly like the single-agent default
	// path: the in-sandbox daemon adopts R' on register and every agent run of
	// the sample is routed to it.
	runtimeEnv := map[string]string{}
	var runtimeID, daemonID string
	if runtimeAgentID != "" {
		rid, did, err := s.deps.PrecreateAgentRuntime(ctx, in.WorkspaceID, in.UserID, runtimeAgentID)
		if err != nil {
			return nil, nil, fmt.Errorf("precreate shared agent runtime: %w", err)
		}
		runtimeID, daemonID = rid, did
		runtimeEnv["MULTICA_DAEMON_ID"] = daemonID
	}
	ref, err := s.lifecycle.CreateSandboxInstance(ctx, CreateSandboxInstanceInput{
		WorkspaceID:   in.WorkspaceID,
		Template:      template,
		DaemonEnabled: true,
		RuntimeEnv:    runtimeEnv,
	}, in.UserID)
	if err != nil {
		// Sandbox create failed after R' was pre-created: reclaim R' so the
		// offline row does not linger (best-effort; the runtime GC is backstop).
		if runtimeID != "" {
			_ = s.deps.DeleteAgentRuntime(ctx, in.WorkspaceID, runtimeID)
		}
		return nil, nil, err
	}
	ref.RuntimeID = runtimeID
	ref.DaemonID = daemonID
	envDispatchAuditFromContext(ctx).recordSandboxRef(ctx, ref, "", "")
	var agentRefs map[string]SandboxInstanceRef
	if len(in.PerAgentEnvSpecs) > 0 {
		agentRefs = make(map[string]SandboxInstanceRef, len(in.PerAgentEnvSpecs))
		for _, spec := range in.PerAgentEnvSpecs {
			agentRefs[spec.AgentID] = ref
		}
	}
	return []SandboxInstanceRef{ref}, agentRefs, nil
}

// sharedRolloutSpecTemplate picks the single winning template for a shared
// rollout: the runtime owner's spec first (its agent owns R'), then the first
// spec with a template. Empty when no spec carries a template.
func sharedRolloutSpecTemplate(specs []PerAgentEnvSpec, runtimeAgentID string) string {
	for _, spec := range specs {
		if spec.AgentID == runtimeAgentID && spec.Template != "" {
			return spec.Template
		}
	}
	for _, spec := range specs {
		if spec.Template != "" {
			return spec.Template
		}
	}
	return ""
}

// rolloutRuntimeID resolves the pre-created sandbox runtime R' (Phase 2) for a
// single-agent rollout, so the task is routed to the in-sandbox daemon's runtime
// instead of the agent/session/leader runtime. Returns "" when the rollout is
// not R'-bound (Fleet path, per-agent specs, or no sandbox ref), which
// preserves the current runtime routing in EnqueueAgentRun.
func rolloutRuntimeID(in EnvDispatchInput, r EnvRollout) string {
	if in.DispatchType != EnvDispatchMessage && in.AgentID == "" {
		return ""
	}
	if len(r.SandboxRefs) > 0 {
		return r.SandboxRefs[0].RuntimeID
	}
	if ref, ok := r.AgentSandboxRefs[in.AgentID]; ok {
		return ref.RuntimeID
	}
	return ""
}

// rolloutSandboxInstanceID resolves the ephemeral sandbox_instance id paired with
// R' (Phase 5). It is carried into EnqueueAgentRun so the handler can stamp
// context.ephemeral_sandbox on the task, which the terminal cleanup hook reads
// to reclaim the sandbox. Same single-agent gating as rolloutRuntimeID; returns
// "" when the rollout is not R'-bound.
func rolloutSandboxInstanceID(in EnvDispatchInput, r EnvRollout) string {
	if in.DispatchType != EnvDispatchMessage && in.AgentID == "" {
		return ""
	}
	if len(r.SandboxRefs) > 0 {
		return r.SandboxRefs[0].InstanceID
	}
	if ref, ok := r.AgentSandboxRefs[in.AgentID]; ok {
		return ref.InstanceID
	}
	return ""
}

// dispatchOne runs the dispatch phase for one rollout (§7.3). Best-effort:
// failures recorded in r.Error, no rollback.
func (s *EnvDispatchService) dispatchOne(ctx context.Context, in EnvDispatchInput, r *EnvRollout, idx int) {
	if in.DispatchType == EnvDispatchMessage && in.Mode == EnvModeScratch && r.ChannelID != "" {
		s.dispatchScratchChannelMessage(ctx, in, r, idx)
		return
	}
	if in.DispatchType == EnvDispatchMessage && in.Mode == EnvModeBranch && r.ChannelID != "" {
		s.dispatchBranchChannelMessage(ctx, in, r, idx)
		return
	}
	runtimeID := rolloutRuntimeID(in, *r)
	sandboxInstanceID := rolloutSandboxInstanceID(in, *r)
	// If this rollout's task is never created (any pre-enqueue step fails, or
	// enqueue itself fails), the pre-created runtime R' is orphaned - reclaim it
	// so the offline row does not linger. On success r.AgentRunID is set and R'
	// stays (the task is routed to it). Best-effort; the runtime GC is backstop.
	defer func() {
		if r.AgentRunID == "" && runtimeID != "" {
			_ = s.deps.DeleteAgentRuntime(ctx, in.WorkspaceID, runtimeID)
		}
	}()
	if in.DispatchType == EnvDispatchIssue {
		issueID := r.IssueID // branch+swe_lego: copied issue id
		if issueID == "" {
			// scratch+swe_lego — create the new issue. in.Issue is guaranteed
			// non-nil here: validate() requires it for scratch+swe_lego, and
			// validateBranchSource guarantees branch+swe_lego set r.IssueID
			// above (so we never reach this branch with a nil in.Issue).
			ii := in.Issue
			if ii == nil {
				r.Error = "internal: missing issue payload for issue dispatch"
				return
			}
			newID, err := s.deps.CreateIssue(ctx, r.ProjectID, in.WorkspaceID, in.UserID, ii.Title, ii.Description, ii.AcceptanceCriteria, ii.FailToPass, ii.PassToPass)
			if err != nil {
				r.Error = fmt.Sprintf("create issue: %v", err)
				r.Stack = stackerr.StackOf(err)
				return
			}
			issueID = newID
			r.IssueID = newID
		}
		runID, err := s.deps.EnqueueAgentRun(ctx, in.WorkspaceID, in.UserID, in.AgentID, issueID, "", sandboxInstanceID, r.EnvID, runtimeID, idx)
		if err != nil {
			r.Error = fmt.Sprintf("enqueue agent run: %v", err)
			r.Stack = stackerr.StackOf(err)
			return
		}
		r.AgentRunID = runID
		envDispatchAuditFromContext(ctx).recordTask(ctx, runID, r.EnvID, r.ProjectID, "")
		return
	}
	// message (self_play)
	sessionID := r.ChatSessionID // branch: the copied session (spec §7.4); empty for scratch
	if sessionID == "" {
		// scratch+self_play — new session bound to the new project
		newID, err := s.deps.CreateChatSession(ctx, r.ProjectID, in.WorkspaceID, in.AgentID, in.UserID)
		if err != nil {
			r.Error = fmt.Sprintf("create chat session: %v", err)
			r.Stack = stackerr.StackOf(err)
			return
		}
		sessionID = newID
		r.ChatSessionID = newID
		envDispatchAuditFromContext(ctx).recordSession(ctx, newID, r.EnvID, r.ProjectID, "")
	}
	// branch continues the copied conversation by appending; scratch starts fresh (spec §7.3).
	if _, err := s.deps.CreateChatMessage(ctx, sessionID, "user", in.Message.Content); err != nil {
		r.Error = fmt.Sprintf("create chat message: %v", err)
		r.Stack = stackerr.StackOf(err)
		return
	}
	runID, err := s.deps.EnqueueAgentRun(ctx, in.WorkspaceID, in.UserID, in.AgentID, "", sessionID, sandboxInstanceID, r.EnvID, runtimeID, idx)
	if err != nil {
		r.Error = fmt.Sprintf("enqueue agent run: %v", err)
		r.Stack = stackerr.StackOf(err)
		return
	}
	r.AgentRunID = runID
	envDispatchAuditFromContext(ctx).recordTask(ctx, runID, r.EnvID, r.ProjectID, "")
}

// dispatchScratchChannelMessage starts only the canonical roster leader. The
// other channel members retain pending bindings and are provisioned by the
// channel delivery hook on their first directed mention.
func (s *EnvDispatchService) dispatchScratchChannelMessage(ctx context.Context, in EnvDispatchInput, r *EnvRollout, idx int) {
	leaderID := in.MessageRoster.LeaderID
	if leaderID == "" {
		r.Error = "internal: missing message roster leader"
		return
	}
	provisioned, err := s.deps.ProvisionEnvDispatchAgent(ctx, EnvDispatchAgentProvisionInput{
		WorkspaceID:   in.WorkspaceID,
		UserID:        in.UserID,
		EnvID:         r.EnvID,
		ProjectID:     r.ProjectID,
		ChannelID:     r.ChannelID,
		AgentID:       leaderID,
		SandboxConfig: json.RawMessage(`{}`),
	})
	if err != nil {
		r.Error = fmt.Sprintf("provision channel leader: %v", err)
		r.Stack = stackerr.StackOf(err)
		return
	}
	defer func() {
		if r.AgentRunID == "" {
			_ = s.deps.DeleteAgentRuntime(ctx, in.WorkspaceID, provisioned.RuntimeID)
		}
	}()
	if err := s.deps.SetEnvSandboxes(ctx, r.EnvID, in.WorkspaceID, []string{provisioned.SandboxInstanceID}); err != nil {
		r.Error = fmt.Sprintf("attach channel leader sandbox: %v", err)
		r.Stack = stackerr.StackOf(err)
		return
	}
	r.ChatSessionID = provisioned.ChatSessionID
	envDispatchAuditFromContext(ctx).recordProvisionedBinding(ctx, provisioned, r.EnvID, r.ProjectID, r.ChannelID)
	r.AgentSandboxes = make(map[string]AgentSandboxStatus, len(in.MessageRoster.AgentIDs))
	for _, agentID := range in.MessageRoster.AgentIDs {
		status := AgentSandboxStatus{Status: "pending"}
		if in.SharedSandbox {
			// Shared mode (FR-005): every roster member reports the sample's
			// shared identifiers; lazily-attached members stay "pending" until
			// their first mention.
			status.SandboxInstanceID = provisioned.SandboxInstanceID
			status.RuntimeID = provisioned.RuntimeID
		}
		r.AgentSandboxes[agentID] = status
	}
	r.AgentSandboxes[leaderID] = AgentSandboxStatus{
		Status:            "ready",
		SandboxInstanceID: provisioned.SandboxInstanceID,
		RuntimeID:         provisioned.RuntimeID,
	}

	messageID, err := s.deps.CreateChannelMessage(ctx, r.ChannelID, in.WorkspaceID, in.UserID, in.Message.Content)
	if err != nil {
		r.Error = fmt.Sprintf("create channel message: %v", err)
		r.Stack = stackerr.StackOf(err)
		return
	}
	runID, err := s.deps.EnqueueEnvDispatchChannelRun(ctx, in.WorkspaceID, in.UserID, ChannelRunInput{
		AgentID:            provisioned.AgentID,
		ChannelID:          r.ChannelID,
		ProjectID:          r.ProjectID,
		EnvID:              r.EnvID,
		ChatSessionID:      provisioned.ChatSessionID,
		SandboxInstanceID:  provisioned.SandboxInstanceID,
		RuntimeID:          provisioned.RuntimeID,
		SourceMessageID:    messageID,
		SharedWorkdirEnvID: sharedWorkdirAnchor(in, *r),
	}, idx)
	if err != nil {
		r.Error = fmt.Sprintf("enqueue channel leader: %v", err)
		r.Stack = stackerr.StackOf(err)
		return
	}
	if err := s.deps.SaveCollaborationTrigger(ctx, r.EnvID, EnvCollaborationTrigger{
		AgentID:         leaderID,
		Kind:            "channel_message",
		ChannelID:       r.ChannelID,
		ProjectID:       r.ProjectID,
		ChatSessionID:   provisioned.ChatSessionID,
		SourceMessageID: messageID,
		TaskID:          runID,
		RuntimeID:       provisioned.RuntimeID,
	}); err != nil {
		r.Error = fmt.Sprintf("save collaboration trigger: %v", err)
		r.Stack = stackerr.StackOf(err)
		return
	}
	r.LeaderRunID = runID
	r.AgentRunID = runID
	envDispatchAuditFromContext(ctx).recordTask(ctx, runID, r.EnvID, r.ProjectID, r.ChannelID)
	// AC-4: link the binding's persisted training session (if any) to the real
	// task so DAG assembly maps session->agent_run. Best-effort; non-training
	// bindings no-op inside the dep, and link failure never fails the dispatch.
	_ = s.deps.LinkEnvDispatchTrainingSession(ctx, r.EnvID, leaderID, r.ProjectID, runID, "")
}

// dispatchBranchChannelMessage resumes the source env's persisted collaboration
// trigger on the copied channel. Only the trigger-selected agent is woken: its
// sandbox is cloned from the source binding (when ready) or created from the
// saved policy. Non-triggered peers keep their pending bindings and are
// provisioned lazily on first mention. The request's message.content, when
// non-empty, is appended as nondispatching channel context before the
// continuation enqueue, without changing the trigger-selected agent.
func (s *EnvDispatchService) dispatchBranchChannelMessage(ctx context.Context, in EnvDispatchInput, r *EnvRollout, idx int) {
	if in.BranchMessageSource == nil {
		r.Error = "internal: missing validated branch message source"
		return
	}
	src := in.BranchMessageSource.Trigger
	if src.AgentID == "" {
		r.Error = "internal: branch collaboration trigger missing agent"
		return
	}

	// Remap the source trigger onto the copied destination entities. The source
	// task/runtime are never reused; new IDs are filled in after provisioning.
	dst := src
	dst.ChannelID = r.ChannelID
	dst.ProjectID = r.ProjectID
	if r.channelMessageIDs != nil {
		if mapped, ok := r.channelMessageIDs[src.SourceMessageID]; ok && mapped != "" {
			dst.SourceMessageID = mapped
		}
		if src.ThreadRootMessageID != nil {
			if mapped, ok := r.channelMessageIDs[*src.ThreadRootMessageID]; ok && mapped != "" {
				mappedCopy := mapped
				dst.ThreadRootMessageID = &mappedCopy
			}
		}
	}
	dst.ChatSessionID = ""
	dst.TaskID = ""
	dst.RuntimeID = ""

	// Append the request's message.content as nondispatching channel context
	// (the store's channel-message insert is a pure SQL insert: it dispatches no
	// channel events and triggers no mention routing). It must not change which
	// agent is woken; the trigger agent is provisioned and enqueued below.
	if in.Message != nil && in.Message.Content != "" {
		if _, err := s.deps.CreateChannelMessage(ctx, r.ChannelID, in.WorkspaceID, in.UserID, in.Message.Content); err != nil {
			r.Error = fmt.Sprintf("append branch channel message: %v", err)
			r.Stack = stackerr.StackOf(err)
			return
		}
	}

	provisioned, err := s.deps.ProvisionEnvDispatchAgent(ctx, EnvDispatchAgentProvisionInput{
		WorkspaceID:             in.WorkspaceID,
		UserID:                  in.UserID,
		EnvID:                   r.EnvID,
		ProjectID:               r.ProjectID,
		ChannelID:               r.ChannelID,
		AgentID:                 dst.AgentID,
		SourceSandboxInstanceID: in.BranchMessageSource.TriggerSourceSandboxInstanceID,
		SandboxConfig:           json.RawMessage(`{}`),
	})
	if err != nil {
		r.Error = fmt.Sprintf("provision branch trigger agent: %v", err)
		r.Stack = stackerr.StackOf(err)
		return
	}
	defer func() {
		if r.AgentRunID == "" {
			_ = s.deps.DeleteAgentRuntime(ctx, in.WorkspaceID, provisioned.RuntimeID)
		}
	}()
	if err := s.deps.SetEnvSandboxes(ctx, r.EnvID, in.WorkspaceID, []string{provisioned.SandboxInstanceID}); err != nil {
		r.Error = fmt.Sprintf("attach branch trigger sandbox: %v", err)
		r.Stack = stackerr.StackOf(err)
		return
	}
	runID, err := s.deps.EnqueueEnvDispatchChannelRun(ctx, in.WorkspaceID, in.UserID, ChannelRunInput{
		AgentID:            provisioned.AgentID,
		ChannelID:          r.ChannelID,
		ProjectID:          r.ProjectID,
		EnvID:              r.EnvID,
		ChatSessionID:      provisioned.ChatSessionID,
		SandboxInstanceID:  provisioned.SandboxInstanceID,
		RuntimeID:          provisioned.RuntimeID,
		SourceMessageID:    dst.SourceMessageID,
		SharedWorkdirEnvID: sharedWorkdirAnchor(in, *r),
	}, idx)
	if err != nil {
		r.Error = fmt.Sprintf("enqueue branch trigger: %v", err)
		r.Stack = stackerr.StackOf(err)
		return
	}
	dst.ChatSessionID = provisioned.ChatSessionID
	dst.TaskID = runID
	dst.RuntimeID = provisioned.RuntimeID
	if err := s.deps.SaveCollaborationTrigger(ctx, r.EnvID, dst); err != nil {
		r.Error = fmt.Sprintf("save branch collaboration trigger: %v", err)
		r.Stack = stackerr.StackOf(err)
		return
	}
	r.ChatSessionID = provisioned.ChatSessionID
	envDispatchAuditFromContext(ctx).recordProvisionedBinding(ctx, provisioned, r.EnvID, r.ProjectID, r.ChannelID)
	r.LeaderRunID = runID
	r.AgentRunID = runID
	envDispatchAuditFromContext(ctx).recordTask(ctx, runID, r.EnvID, r.ProjectID, r.ChannelID)
	r.AgentSandboxes = make(map[string]AgentSandboxStatus, len(in.MessageRoster.AgentIDs))
	for _, agentID := range in.MessageRoster.AgentIDs {
		status := AgentSandboxStatus{Status: "pending"}
		if in.SharedSandbox {
			// Shared mode (FR-005): every roster member reports the sample's
			// shared identifiers; lazily-attached members stay "pending" until
			// their first mention.
			status.SandboxInstanceID = provisioned.SandboxInstanceID
			status.RuntimeID = provisioned.RuntimeID
		}
		r.AgentSandboxes[agentID] = status
	}
	r.AgentSandboxes[dst.AgentID] = AgentSandboxStatus{
		Status:            "ready",
		SandboxInstanceID: provisioned.SandboxInstanceID,
		RuntimeID:         provisioned.RuntimeID,
	}
}

// sharedWorkdirAnchor returns the sample env whose single shared working
// directory all of the rollout's agents anchor to (research D5, FR-008), or ""
// for non-shared dispatches (the daemon then keeps per-agent workdir roots).
func sharedWorkdirAnchor(in EnvDispatchInput, r EnvRollout) string {
	if !in.SharedSandbox {
		return ""
	}
	return r.EnvID
}

// rollbackRollout cleans up a partially-created rollout (reset phase only).
// Order matters under ON DELETE RESTRICT: delete the project first (it
// references env_id), then the env row, then its sandboxes. Every rollout
// forks its own sandboxes, so this never touches a shared/source sandbox.
func (s *EnvDispatchService) rollbackRollout(ctx context.Context, workspaceID string, r EnvRollout) {
	audit := envDispatchAuditFromContext(ctx)
	if r.ChannelID != "" {
		audit.recordResource(ctx, EnvDispatchAuditResourceBinding, r.ChannelID, r.EnvID, r.ProjectID, r.ChannelID, "")
		_ = s.deps.DeleteChannel(ctx, workspaceID, r.ChannelID)
	}
	if r.ProjectID != "" {
		_ = s.deps.DeleteProject(ctx, r.ProjectID, workspaceID)
	}
	if r.EnvID != "" {
		env, err := s.deps.GetEnv(ctx, r.EnvID, workspaceID)
		_ = s.deps.DeleteEnv(ctx, r.EnvID, workspaceID)
		if err == nil {
			for _, sandboxID := range env.SandboxIDs {
				audit.recordResource(ctx, EnvDispatchAuditResourceSandbox, sandboxID, r.EnvID, r.ProjectID, r.ChannelID, "")
				audit.recordEventForResource(ctx, EnvDispatchAuditEventSandboxDeletionRequested, EnvDispatchAuditResourceSandbox, sandboxID, auditReasonRollbackDeleteRequested)
				if deleteErr := s.deps.DeleteSandbox(ctx, sandboxID); deleteErr != nil {
					audit.classifyResource(ctx, EnvDispatchAuditResourceSandbox, sandboxID, EnvDispatchAuditOwnerUnknown, EnvDispatchAuditClassificationInconclusive, EnvDispatchAuditEventObservationUnavailable, auditReasonDeleteObservationUnavailable)
					continue
				}
				audit.classifyResource(ctx, EnvDispatchAuditResourceSandbox, sandboxID, EnvDispatchAuditOwnerDeleted, EnvDispatchAuditClassificationReclaimed, EnvDispatchAuditEventReclaimed, auditReasonRollbackDeleted)
			}
		}
	}
}

// forkAll forks every sandbox in src, returning the new sandbox ids. On the
// first failure it best-effort deletes the sandboxes already forked so the
// reset does not leak them, then returns the error.
func (s *EnvDispatchService) forkAll(ctx context.Context, src []string, idx int) ([]string, error) {
	forked := make([]string, 0, len(src))
	for _, sid := range src {
		newID, err := s.deps.ForkSandbox(ctx, sid, idx)
		if err != nil {
			s.deleteSandboxes(ctx, forked)
			return nil, err
		}
		forked = append(forked, newID)
	}
	return forked, nil
}

// deleteSandboxes best-effort deletes every sandbox id; errors are ignored
// (rollback is best-effort and the runtime GC is the backstop, spec §7.6).
func (s *EnvDispatchService) deleteSandboxes(ctx context.Context, ids []string) {
	audit := envDispatchAuditFromContext(ctx)
	for _, sid := range ids {
		audit.recordResource(ctx, EnvDispatchAuditResourceSandbox, sid, "", "", "", "")
		audit.recordEventForResource(ctx, EnvDispatchAuditEventSandboxDeletionRequested, EnvDispatchAuditResourceSandbox, sid, auditReasonRollbackDeleteRequested)
		if err := s.deps.DeleteSandbox(ctx, sid); err != nil {
			audit.classifyResource(ctx, EnvDispatchAuditResourceSandbox, sid, EnvDispatchAuditOwnerUnknown, EnvDispatchAuditClassificationInconclusive, EnvDispatchAuditEventObservationUnavailable, auditReasonDeleteObservationUnavailable)
			continue
		}
		audit.classifyResource(ctx, EnvDispatchAuditResourceSandbox, sid, EnvDispatchAuditOwnerDeleted, EnvDispatchAuditClassificationReclaimed, EnvDispatchAuditEventReclaimed, auditReasonRollbackDeleted)
	}
}

// CreateBaseEnv boots a sandbox and creates a mode='base' env row.
func (s *EnvDispatchService) CreateBaseEnv(ctx context.Context, workspaceID, imageRef string) (envID, sandboxID string, err error) {
	if imageRef == "" || len(imageRef) > 256 {
		return "", "", fmt.Errorf("validation_failed: image_ref must be 1..256 chars")
	}
	sbx, err := s.deps.BootSandbox(ctx, imageRef)
	if err != nil {
		return "", "", fmt.Errorf("boot sandbox: %w", err)
	}
	eid, err := s.deps.CreateEnv(ctx, workspaceID, []string{sbx}, "", EnvModeBase, "")
	if err != nil {
		_ = s.deps.DeleteSandbox(ctx, sbx)
		return "", "", fmt.Errorf("create env: %w", err)
	}
	return eid, sbx, nil
}

// ErrEnvInUse signals a DELETE /api/v1/env against an env a project still
// references (ON DELETE RESTRICT). Handler maps it to 409.
var ErrEnvInUse = fmt.Errorf("env_in_use")

// DeleteEnv deletes the env row + its sandbox. Idempotent: a missing env
// returns a "not found" error which the handler maps to 404. Returns
// ErrEnvInUse (→ 409) if a project still references the env.
func (s *EnvDispatchService) DeleteEnv(ctx context.Context, envID, workspaceID string) error {
	env, err := s.deps.GetEnv(ctx, envID, workspaceID)
	if err != nil {
		return fmt.Errorf("not found: %w", err)
	}
	// Delete the env ROW first. Under ON DELETE RESTRICT this fails with a FK
	// violation if a project still references it — the adapter surfaces that as
	// ErrEnvInUse, and the sandbox is left untouched. Only once the row is gone
	// do we reclaim the sandbox.
	if err := s.deps.DeleteEnv(ctx, envID, workspaceID); err != nil {
		if errors.Is(err, ErrEnvInUse) {
			return ErrEnvInUse
		}
		return fmt.Errorf("delete env: %w", err)
	}
	s.deleteSandboxes(ctx, env.SandboxIDs) // idempotent on 404 in Fleet
	return nil
}

// DeleteProject deletes a project by ID (cascades to issues/chat/tasks).
// Idempotent: a missing project returns nil.
func (s *EnvDispatchService) DeleteProject(ctx context.Context, projectID, workspaceID string) error {
	if err := s.deps.DeleteProject(ctx, projectID, workspaceID); err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	return nil
}

// reclaimSharedRuntimes removes pre-created runtimes after reset fails before
// any task is enqueued. Shared rollouts expose the same runtime through every
// agent ref, so each non-empty runtime ID is deleted once. Non-shared cleanup
// remains owned by the existing single-rollout error paths.
func (s *EnvDispatchService) reclaimSharedRuntimes(ctx context.Context, in EnvDispatchInput, refs []SandboxInstanceRef) {
	if !in.SharedSandbox {
		return
	}
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if ref.RuntimeID == "" {
			continue
		}
		if _, ok := seen[ref.RuntimeID]; ok {
			continue
		}
		seen[ref.RuntimeID] = struct{}{}
		_ = s.deps.DeleteAgentRuntime(context.WithoutCancel(ctx), in.WorkspaceID, ref.RuntimeID)
	}
}
