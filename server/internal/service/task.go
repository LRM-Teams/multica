package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/memoryscope"
	"github.com/multica-ai/multica/server/internal/mention"
	"github.com/multica-ai/multica/server/internal/messageparts"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/redact"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

// EphemeralSandboxCleaner is an optional seam injected into TaskService so the
// terminal cleanup hook can reclaim an env-dispatch ephemeral sandbox instance
// after the last task on its pre-created runtime R' has terminated. Nil = no-op
// (sandbox cleanup not configured for this deployment). Wired by the handler to
// the env-sandbox-lifecycle adapter's DeleteSandboxInstance.
type EphemeralSandboxCleaner interface {
	// DeleteSandboxInstance stops and deletes the Cube sandbox instance (via
	// sandboxd delete job). It is safe to call on an already-deleted instance
	// (not-found = success). Best-effort; errors are logged but never fail the
	// terminal flow.
	DeleteSandboxInstance(ctx context.Context, workspaceID, instanceID string) error
}

type EphemeralRetryResources struct {
	RuntimeID   pgtype.UUID
	Context     []byte
	WorkspaceID string
	SandboxRef  SandboxInstanceRef
	ActorUserID string
}

type EphemeralSandboxManager interface {
	PrepareRetry(context.Context, db.AgentInboxEvent) (*EphemeralRetryResources, error)
	Reclaim(context.Context, *EphemeralRetryResources) error
	Cleanup(context.Context, db.AgentInboxEvent) error
}

type CanonicalChannelMessage struct {
	ID                  pgtype.UUID
	WorkspaceID         pgtype.UUID
	ChannelID           pgtype.UUID
	ThreadRootMessageID pgtype.UUID
	ThreadID            pgtype.Text
	AuthorType          string
	Seq                 int64
}

type TaskService struct {
	Queries   *db.Queries
	TxStarter TxStarter
	Hub       *realtime.Hub
	Bus       *events.Bus
	Analytics analytics.Client
	Metrics   *obsmetrics.BusinessMetrics
	Wakeup    TaskWakeupNotifier

	// OnChildTaskCreated is an optional callback fired when a retry child
	// task is created (subagent lifecycle). When set, it receives the parent
	// and child task so the caller can record activity events. Failures in
	// the callback are logged but never block task processing.
	OnChildTaskCreated func(ctx context.Context, parent, child db.AgentInboxEvent)
	// OnTaskCompleted is an optional best-effort callback fired only after a
	// task completion transaction commits successfully.
	OnTaskCompleted func(ctx context.Context, task db.AgentInboxEvent)
	// PrepareCanonicalChannelMessageCommit extends a canonical visible message
	// transaction with handler-owned dependent state. The returned callback is
	// invoked only after the same transaction commits successfully.
	PrepareCanonicalChannelMessageCommit func(
		ctx context.Context,
		exec db.DBTX,
		message CanonicalChannelMessage,
	) (afterCommit func(context.Context), err error)

	// Training, when non-nil, enables the RL session-open hook at task
	// creation (see maybeOpenTrainingSession). Nil = training not configured
	// for this deployment; the hook is then a no-op. Wired in Task 8 (config).
	Training *TrainingSessionDeps

	// EnvDispatchCheck, when non-nil, checks whether a project was created via
	// env-dispatch. The interaction-dag seams use this to decide between AReaL
	// bridge recording (training mode, areal_proxy present) and local
	// task_messages recording (non-training mode, env-dispatch project without
	// proxy). Nil = no env-dispatch awareness (no-op for non-proxy tasks).
	// Wired by the handler in env_dispatch.go (newEnvDispatchDepsAdapter).
	EnvDispatchCheck EnvDispatchRunChecker

	// EphemeralSandboxCleaner, when non-nil, enables the Phase 5 sandbox
	// cleanup hook at task terminal. Nil = no-op (sandbox cleanup not
	// configured). Wired by the handler to the env-sandbox-lifecycle adapter.
	EphemeralSandboxCleaner EphemeralSandboxCleaner
	EphemeralSandboxManager EphemeralSandboxManager

	analyticsContextMu    sync.Mutex
	analyticsContextCache map[string]analytics.TaskContext
	analyticsContextOrder []string
}

type TaskWakeupNotifier interface {
	NotifyTaskAvailable(runtimeID, taskID string)
}

// triggerSummaryMaxLen caps the snapshot length so the row stays cheap to
// transmit (it ends up in every task list response). 200 is enough for a
// recognisable preview of a one-paragraph comment.
const triggerSummaryMaxLen = 200

// truncateForSummary returns s shortened to maxRunes, with a trailing
// `…` when truncated. Operates on runes (not bytes) so multibyte characters
// — Chinese / emoji — count as one each. Strips surrounding whitespace
// first so a leading newline doesn't waste budget.
func truncateForSummary(s string, maxRunes int) string {
	// strings.Builder + Grow avoids the O(N²) realloc cycle of `+=` in
	// a loop. Grow uses byte length, which is an upper bound for the
	// rune-equivalent output (replacing \n/\r/\t with space is byte-equal
	// for ASCII whitespace), so we never reallocate.
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\n', '\r', '\t':
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	rs := []rune(strings.TrimSpace(b.String()))
	if len(rs) <= maxRunes {
		return string(rs)
	}
	return string(rs[:maxRunes]) + "…"
}

const (
	taskAnalyticsContextCacheMax = 4096
	// claimResponseRecoveryWindow must exceed daemon client.Timeout for
	// inbox drain (30s) plus execution start (30s) plus scheduling slack, so
	// an in-flight StartTask cannot be reclaimed and double-dispatched.
	claimResponseRecoveryWindow = 90 * time.Second
)

// buildCommentTriggerSummary fetches the comment content and truncates
// it for storage on the task row. Returns an invalid pgtype.Text when
// the comment is missing (deleted / wrong workspace / etc) so the column
// stays NULL — front-end falls back to a structural label in that case.
func (s *TaskService) buildCommentTriggerSummary(ctx context.Context, commentID pgtype.UUID) pgtype.Text {
	return s.buildCommentTriggerSummaryWithQueries(ctx, s.Queries, commentID)
}

func (s *TaskService) buildCommentTriggerSummaryWithQueries(ctx context.Context, q *db.Queries, commentID pgtype.UUID) pgtype.Text {
	if !commentID.Valid {
		return pgtype.Text{}
	}
	comment, err := q.GetComment(ctx, commentID)
	if err != nil {
		return pgtype.Text{}
	}
	summary := truncateForSummary(comment.Content, triggerSummaryMaxLen)
	if summary == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: summary, Valid: true}
}

// BuildCommentTriggerSummaryForTask is the transactional counterpart used by
// callers that create a visible comment and retarget an already-queued task in
// the same transaction.
func (s *TaskService) BuildCommentTriggerSummaryForTask(ctx context.Context, q *db.Queries, commentID pgtype.UUID) pgtype.Text {
	return s.buildCommentTriggerSummaryWithQueries(ctx, q, commentID)
}

func NewTaskService(q *db.Queries, tx TxStarter, hub *realtime.Hub, bus *events.Bus, wakeups ...TaskWakeupNotifier) *TaskService {
	var wakeup TaskWakeupNotifier
	if len(wakeups) > 0 {
		wakeup = wakeups[0]
	}
	return &TaskService{Queries: q, TxStarter: tx, Hub: hub, Bus: bus, Wakeup: wakeup}
}

// WithTraining sets the TrainingSessionDeps for the TaskService.
// Returns the TaskService to enable builder-style chaining.
func (s *TaskService) WithTraining(deps *TrainingSessionDeps) *TaskService {
	s.Training = deps
	return s
}

var trivialDoneMarkers = []string{
	"done",
	"готово",
	"готова",
	"сделано",
	"完成",
	"完了",
}

func isTrivialDoneOutput(output string) bool {
	normalized := strings.TrimSpace(strings.ToLower(output))
	normalized = strings.Trim(normalized, ".!！。… ")
	for _, marker := range trivialDoneMarkers {
		if normalized == marker {
			return true
		}
	}
	return false
}

func (s *TaskService) captureTaskQueued(ctx context.Context, task db.AgentInboxEvent) {
	if s.Metrics != nil {
		source, runtimeMode, _ := s.taskMetricsContext(ctx, task)
		s.Metrics.RecordTaskEnqueued(source, runtimeMode)
	}
}

func (s *TaskService) captureTaskDispatched(ctx context.Context, task db.AgentInboxEvent) {
	if s.Metrics != nil {
		source, runtimeMode, _ := s.taskMetricsContext(ctx, task)
		s.Metrics.RecordTaskDispatched(util.UUIDToString(task.ID), source, runtimeMode, taskQueueWaitSeconds(task))
	}
}

func (s *TaskService) AnalyticsContextForTask(ctx context.Context, task db.AgentInboxEvent) analytics.TaskContext {
	return s.taskAnalyticsContext(ctx, task)
}

func (s *TaskService) captureTaskStarted(ctx context.Context, task db.AgentInboxEvent) {
	if s.Metrics != nil {
		source, runtimeMode, provider := s.taskMetricsContext(ctx, task)
		s.Metrics.RecordTaskStarted(source, runtimeMode, provider)
	}
}

func (s *TaskService) captureTaskCompleted(ctx context.Context, task db.AgentInboxEvent) {
	if s.Metrics != nil {
		source, runtimeMode, _ := s.taskMetricsContext(ctx, task)
		s.Metrics.RecordTaskTerminal(util.UUIDToString(task.ID), source, runtimeMode, task.Status, taskRunSeconds(task), taskTotalSeconds(task), task.Attempt)
	}
}

func (s *TaskService) captureTaskFailed(ctx context.Context, task db.AgentInboxEvent) {
	failureReason := taskFailureReason(task)
	if s.Metrics != nil {
		source, runtimeMode, _ := s.taskMetricsContext(ctx, task)
		s.Metrics.RecordTaskTerminal(util.UUIDToString(task.ID), source, runtimeMode, task.Status, taskRunSeconds(task), taskTotalSeconds(task), task.Attempt)
		s.Metrics.RecordTaskFailed(source, runtimeMode, failureReason)
	}
}

func (s *TaskService) captureTaskCancelled(ctx context.Context, task db.AgentInboxEvent) {
	if s.Metrics != nil {
		source, runtimeMode, _ := s.taskMetricsContext(ctx, task)
		s.Metrics.RecordTaskTerminal(util.UUIDToString(task.ID), source, runtimeMode, task.Status, taskRunSeconds(task), taskTotalSeconds(task), task.Attempt)
	}
	// Revoke any mat_ task tokens minted for this task. Cancellation is
	// a terminal transition, so the running agent process no longer
	// needs to call back; eagerly deleting the token closes the
	// window where a compromised process could keep authenticating
	// against the API until the 24h expiry. Failure is non-fatal — the
	// expiry / FK cascade are the durable guards. MUL-2600.
	if err := s.Queries.DeleteTaskTokensByTask(ctx, task.ID); err != nil {
		slog.Warn("cancel task: failed to revoke task tokens",
			"task_id", util.UUIDToString(task.ID), "error", err)
	}
	// Inbox-scoped mat_ credentials carry the same canonical task/event ID.
	// Revoke them at the same terminal seam so every cancellation path closes
	// both accepted task-scoped credential sources. CancelTaskWithResult skips
	// this hook for an already-terminal task, preserving idempotent retries.
	if err := s.Queries.DeleteAgentInboxTokensByEvent(ctx, task.ID); err != nil {
		slog.Warn("cancel task: failed to revoke agent inbox tokens",
			"task_id", util.UUIDToString(task.ID), "error", err)
	}
}

func (s *TaskService) CaptureTaskUsage(ctx context.Context, task db.AgentInboxEvent, provider, model string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int64) {
	if s.Metrics == nil {
		return
	}
	source, runtimeMode, _ := s.taskMetricsContext(ctx, task)
	s.Metrics.RecordLLMUsage(source, runtimeMode, provider, model, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens)
}

func (s *TaskService) CaptureQueuedExpiredTasks(ctx context.Context, tasks []db.AgentInboxEvent) {
	if s.Metrics == nil {
		return
	}
	for _, task := range tasks {
		source, runtimeMode, _ := s.taskMetricsContext(ctx, task)
		s.Metrics.RecordTaskQueuedExpired(source, runtimeMode)
	}
}

func (s *TaskService) CaptureLeaseExpiredTasks(ctx context.Context, tasks []db.AgentInboxEvent) {
	if s.Metrics == nil {
		return
	}
	for _, task := range tasks {
		source, _, _ := s.taskMetricsContext(ctx, task)
		s.Metrics.RecordTaskLeaseExpired(source)
	}
}

func (s *TaskService) cachedTaskAnalyticsContext(task db.AgentInboxEvent) (analytics.TaskContext, bool) {
	key := taskAnalyticsContextKey(task)
	if key == "" {
		return analytics.TaskContext{}, false
	}
	s.analyticsContextMu.Lock()
	defer s.analyticsContextMu.Unlock()
	if s.analyticsContextCache == nil {
		return analytics.TaskContext{}, false
	}
	tc, ok := s.analyticsContextCache[key]
	return tc, ok
}

func (s *TaskService) storeTaskAnalyticsContext(task db.AgentInboxEvent, tc analytics.TaskContext) {
	if tc.WorkspaceID == "" {
		return
	}
	key := taskAnalyticsContextKey(task)
	if key == "" {
		return
	}
	s.analyticsContextMu.Lock()
	defer s.analyticsContextMu.Unlock()
	if s.analyticsContextCache == nil {
		s.analyticsContextCache = make(map[string]analytics.TaskContext)
	}
	if _, ok := s.analyticsContextCache[key]; !ok {
		s.analyticsContextOrder = append(s.analyticsContextOrder, key)
		if len(s.analyticsContextOrder) > taskAnalyticsContextCacheMax {
			oldest := s.analyticsContextOrder[0]
			s.analyticsContextOrder = s.analyticsContextOrder[1:]
			delete(s.analyticsContextCache, oldest)
		}
	}
	s.analyticsContextCache[key] = tc
}

func taskAnalyticsContextKey(task db.AgentInboxEvent) string {
	taskID := util.UUIDToString(task.ID)
	if taskID == "" {
		return ""
	}
	return strings.Join([]string{
		taskID,
		util.UUIDToString(task.RuntimeID),
		util.UUIDToString(task.IssueID),
		util.UUIDToString(task.ChatSessionID),
		util.UUIDToString(task.AutopilotRunID),
	}, "|")
}

func (s *TaskService) taskMetricsContext(ctx context.Context, task db.AgentInboxEvent) (source, runtimeMode, provider string) {
	tc := s.taskAnalyticsContext(ctx, task)
	source = "other"
	switch {
	case task.ChatSessionID.Valid:
		source = "chat"
	case task.IssueID.Valid:
		if tc.Source == analytics.SourceAutopilot {
			source = "autopilot_issue"
		} else {
			source = "issue"
		}
	case task.AutopilotRunID.Valid:
		source = "autopilot"
	default:
		if _, ok := s.parseQuickCreateContext(task); ok {
			source = "quick_create"
		} else if tc.Source != "" {
			source = tc.Source
		}
	}
	return source, tc.RuntimeMode, tc.Provider
}

func (s *TaskService) taskAnalyticsContext(ctx context.Context, task db.AgentInboxEvent) analytics.TaskContext {
	if tc, ok := s.cachedTaskAnalyticsContext(task); ok {
		return tc
	}
	tc := analytics.TaskContext{
		AgentID: util.UUIDToString(task.AgentID),
		TaskID:  util.UUIDToString(task.ID),
		Source:  analytics.SourceManual,
	}
	if task.IssueID.Valid {
		tc.IssueID = util.UUIDToString(task.IssueID)
	}
	if task.ChatSessionID.Valid {
		tc.ChatSessionID = util.UUIDToString(task.ChatSessionID)
		tc.Source = analytics.SourceChat
	}
	if task.AutopilotRunID.Valid {
		tc.AutopilotRunID = util.UUIDToString(task.AutopilotRunID)
		tc.Source = analytics.SourceAutopilot
	}

	if task.RuntimeID.Valid {
		if rt, err := s.Queries.GetAgentRuntime(ctx, task.RuntimeID); err == nil {
			tc.WorkspaceID = util.UUIDToString(rt.WorkspaceID)
			tc.RuntimeMode = rt.RuntimeMode
			tc.Provider = rt.Provider
		}
	}
	if tc.WorkspaceID == "" || tc.RuntimeMode == "" {
		if agent, err := s.Queries.GetAgent(ctx, task.AgentID); err == nil {
			if tc.WorkspaceID == "" {
				tc.WorkspaceID = util.UUIDToString(agent.WorkspaceID)
			}
			if tc.RuntimeMode == "" {
				tc.RuntimeMode = agent.RuntimeMode
			}
		}
	}

	if task.IssueID.Valid {
		if issue, err := s.Queries.GetIssue(ctx, task.IssueID); err == nil {
			tc.WorkspaceID = util.UUIDToString(issue.WorkspaceID)
			if issue.CreatorType == "member" {
				tc.UserID = util.UUIDToString(issue.CreatorID)
			}
			if issue.OriginType.Valid {
				switch issue.OriginType.String {
				case "autopilot":
					// Autopilot tables dropped (LRM-1051); keep analytics source stamp only.
					tc.Source = analytics.SourceAutopilot
				case "quick_create":
					tc.Source = analytics.SourceManual
				}
			}
		}
	}
	if task.ChatSessionID.Valid {
		if cs, err := s.Queries.GetChatSession(ctx, task.ChatSessionID); err == nil {
			tc.WorkspaceID = util.UUIDToString(cs.WorkspaceID)
			tc.UserID = util.UUIDToString(cs.CreatorID)
		}
	}
	// Autopilot tables dropped (LRM-1051); historical AutopilotRunID is orphan UUID only.
	if qc, ok := s.parseQuickCreateContext(task); ok {
		tc.WorkspaceID = qc.WorkspaceID
		tc.UserID = qc.RequesterID
		tc.Source = analytics.SourceManual
	}
	s.storeTaskAnalyticsContext(task, tc)
	return tc
}

func taskQueueWaitSeconds(task db.AgentInboxEvent) float64 {
	return durationSeconds(task.CreatedAt, task.DispatchedAt)
}

func taskRunSeconds(task db.AgentInboxEvent) float64 {
	return durationSeconds(task.StartedAt, task.CompletedAt)
}

func taskTotalSeconds(task db.AgentInboxEvent) float64 {
	return durationSeconds(task.CreatedAt, task.CompletedAt)
}

func durationSeconds(start, end pgtype.Timestamptz) float64 {
	if !start.Valid || !end.Valid {
		return -1
	}
	seconds := end.Time.Sub(start.Time).Seconds()
	if seconds < 0 {
		return 0
	}
	return seconds
}

func taskFailureReason(task db.AgentInboxEvent) string {
	if task.FailureReason.Valid && task.FailureReason.String != "" {
		return task.FailureReason.String
	}
	return "agent_error"
}

func taskErrorType(reason string) string {
	switch reason {
	case "runtime_offline", "runtime_recovery":
		return "runtime"
	case "timeout", "codex_semantic_inactivity", "grok_first_turn_no_progress":
		return "timeout"
	case taskfailure.ReasonAgentContextOverflow.String():
		return "agent_error"
	case "iteration_limit", "agent_fallback_message":
		return "agent_output"
	case "cancelled", "user_cancelled":
		return "cancelled"
	default:
		return "agent_error"
	}
}

// EnqueueTaskForIssue creates a queued task for an agent-assigned issue.
// Assignment-triggered tasks snapshot the visible issue read-model at enqueue
// time. Comment-triggered tasks keep their existing trigger-body + cursor
// contract and do not duplicate issue/comment context into the task.
func (s *TaskService) EnqueueTaskForIssue(ctx context.Context, issue db.Issue, triggerCommentID ...pgtype.UUID) (db.AgentInboxEvent, error) {
	var commentID pgtype.UUID
	if len(triggerCommentID) > 0 {
		commentID = triggerCommentID[0]
	}
	return s.enqueueIssueTask(ctx, issue, commentID, false)
}

// enqueueIssueTask is the shared implementation behind EnqueueTaskForIssue
// and the manual rerun path. forceFreshSession=true marks the task so the
// daemon claim handler skips the (agent_id, issue_id) resume lookup — the
// user already judged the prior output bad, a fresh agent session is the
// expected behavior.
func (s *TaskService) enqueueIssueTask(ctx context.Context, issue db.Issue, triggerCommentID pgtype.UUID, forceFreshSession bool) (db.AgentInboxEvent, error) {
	if !issue.AssigneeID.Valid {
		slog.Error("task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "error", "issue has no assignee")
		return db.AgentInboxEvent{}, fmt.Errorf("issue has no assignee")
	}

	agent, err := s.Queries.GetAgent(ctx, issue.AssigneeID)
	if err != nil {
		slog.Error("task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "error", err)
		return db.AgentInboxEvent{}, fmt.Errorf("load agent: %w", err)
	}
	if agent.ArchivedAt.Valid {
		slog.Debug("task enqueue skipped: agent is archived", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agent.ID))
		return db.AgentInboxEvent{}, fmt.Errorf("agent is archived")
	}
	if agent.WorkspaceID != issue.WorkspaceID {
		slog.Error("task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agent.ID), "error", "agent workspace does not match issue")
		return db.AgentInboxEvent{}, fmt.Errorf("agent workspace does not match issue")
	}
	if !agent.RuntimeID.Valid {
		slog.Error("task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "error", "agent has no runtime")
		return db.AgentInboxEvent{}, fmt.Errorf("agent has no runtime")
	}
	var taskContext []byte
	if !triggerCommentID.Valid {
		snapshot, err := s.buildIssueAssignmentSnapshot(ctx, issue)
		if err != nil {
			slog.Error("task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "error", err)
			return db.AgentInboxEvent{}, fmt.Errorf("snapshot issue assignment: %w", err)
		}
		taskContext, err = withIssueAssignmentSnapshot(taskContext, snapshot)
		if err != nil {
			return db.AgentInboxEvent{}, fmt.Errorf("encode issue assignment snapshot: %w", err)
		}
	}
	taskContext, err = WithTaskExecutionConfig(taskContext, agent.Model.String, agent.ThinkingLevel.String)
	if err != nil {
		return db.AgentInboxEvent{}, fmt.Errorf("snapshot issue task execution config: %w", err)
	}

	task, err := s.Queries.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID:           issue.AssigneeID,
		RuntimeID:         agent.RuntimeID,
		IssueID:           issue.ID,
		Priority:          priorityToInt(issue.Priority),
		TriggerCommentID:  triggerCommentID,
		TriggerSummary:    s.buildCommentTriggerSummary(ctx, triggerCommentID),
		ForceFreshSession: pgtype.Bool{Bool: forceFreshSession, Valid: forceFreshSession},
		Context:           taskContext,
	})
	if err != nil {
		slog.Error("task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "error", err)
		return db.AgentInboxEvent{}, fmt.Errorf("create task: %w", err)
	}

	slog.Info("task enqueued",
		"task_id", util.UUIDToString(task.ID),
		"issue_id", util.UUIDToString(issue.ID),
		"agent_id", util.UUIDToString(issue.AssigneeID),
		"force_fresh_session", forceFreshSession,
	)
	// Training session-open chokepoint (spec §4.3, seam 1a/1e): the task row now
	// exists with a known agent_id + owning project (issue.ProjectID). No-op
	// unless this project/agent is the training target.
	s.tryOpenTrainingSession(ctx, task, issue.ProjectID, "")
	// Order matters: broadcast first, notify daemon second. notifyTaskAvailable
	// kicks an in-process channel that the daemon picks up over HTTP and
	// claims; the claim path then emits its own task:dispatch. Doing the
	// queued broadcast afterwards risks the dispatch event reaching clients
	// before the queued one (rare but unsafe-by-construction). Publishing
	// in the desired observe-order makes correctness independent of timing.
	s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
	s.interruptInFlightIssueTasksForFollowup(ctx, task)
	s.NotifyTaskEnqueued(ctx, task)
	return task, nil
}

func (s *TaskService) interruptInFlightIssueTasksForFollowup(ctx context.Context, task db.AgentInboxEvent) {
	if !task.TriggerCommentID.Valid || !task.IssueID.Valid {
		return
	}
	cancelled, err := s.Queries.CancelInFlightTasksByIssueAndAgent(ctx, db.CancelInFlightTasksByIssueAndAgentParams{
		IssueID: task.IssueID,
		AgentID: task.AgentID,
		ID:      task.ID,
	})
	if err != nil {
		slog.Warn("follow-up interrupt failed", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(task.IssueID), "agent_id", util.UUIDToString(task.AgentID), "error", err)
		return
	}
	s.broadcastFollowupInterruptedTasks(ctx, cancelled)
}

func (s *TaskService) interruptInFlightChatTasksForFollowup(ctx context.Context, task db.AgentInboxEvent) {
	if !task.ChatSessionID.Valid {
		return
	}
	cancelled, err := s.Queries.CancelInFlightChatTasksBySessionAndAgent(ctx, db.CancelInFlightChatTasksBySessionAndAgentParams{
		ChatSessionID: task.ChatSessionID,
		AgentID:       task.AgentID,
		ID:            task.ID,
	})
	if err != nil {
		slog.Warn("chat follow-up interrupt failed", "task_id", util.UUIDToString(task.ID), "chat_session_id", util.UUIDToString(task.ChatSessionID), "agent_id", util.UUIDToString(task.AgentID), "error", err)
		return
	}
	s.broadcastFollowupInterruptedTasks(ctx, cancelled)
}

func (s *TaskService) broadcastFollowupInterruptedTasks(ctx context.Context, tasks []db.AgentInboxEvent) {
	for _, t := range tasks {
		slog.Info("task interrupted by newer guidance", "task_id", util.UUIDToString(t.ID), "issue_id", util.UUIDToString(t.IssueID), "chat_session_id", util.UUIDToString(t.ChatSessionID), "agent_id", util.UUIDToString(t.AgentID))
		s.finalizeCancelledTask(ctx, t)
		s.ReconcileAgentStatus(ctx, t.AgentID)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, t)
	}
}

// EnqueueTaskForMention creates a queued task for a mentioned agent on an issue.
// Unlike EnqueueTaskForIssue, this takes an explicit agent ID rather than
// deriving it from the issue assignee.
func (s *TaskService) EnqueueTaskForMention(ctx context.Context, issue db.Issue, agentID pgtype.UUID, triggerCommentID pgtype.UUID) (db.AgentInboxEvent, error) {
	return s.enqueueMentionTask(ctx, issue, agentID, triggerCommentID, false, false)
}

// CreateMentionTaskRow validates the target agent and inserts a normal mention
// task through q without publishing realtime, training, cancellation, or daemon
// wake side effects. Transactional callers must invoke PublishMentionTaskQueued
// only after the transaction commits.
func (s *TaskService) CreateMentionTaskRow(ctx context.Context, q *db.Queries, issue db.Issue, agentID pgtype.UUID, triggerCommentID pgtype.UUID) (db.AgentInboxEvent, error) {
	return s.createMentionTaskRow(ctx, q, issue, agentID, triggerCommentID, false, false)
}

func (s *TaskService) enqueueMentionTask(ctx context.Context, issue db.Issue, agentID pgtype.UUID, triggerCommentID pgtype.UUID, isLeader bool, forceFreshSession bool) (db.AgentInboxEvent, error) {
	task, err := s.createMentionTaskRow(ctx, s.Queries, issue, agentID, triggerCommentID, isLeader, forceFreshSession)
	if err != nil {
		return db.AgentInboxEvent{}, err
	}
	return s.publishMentionTaskQueued(ctx, task, issue, triggerCommentID), nil
}

func (s *TaskService) createMentionTaskRow(ctx context.Context, q *db.Queries, issue db.Issue, agentID pgtype.UUID, triggerCommentID pgtype.UUID, isLeader bool, forceFreshSession bool) (db.AgentInboxEvent, error) {
	agent, err := q.GetAgent(ctx, agentID)
	if err != nil {
		slog.Error("mention task enqueue failed: agent not found", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID), "error", err)
		return db.AgentInboxEvent{}, fmt.Errorf("load agent: %w", err)
	}
	if agent.ArchivedAt.Valid {
		slog.Debug("mention task enqueue skipped: agent is archived", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID))
		return db.AgentInboxEvent{}, fmt.Errorf("agent is archived")
	}
	if !agent.RuntimeID.Valid {
		slog.Error("mention task enqueue failed: agent has no runtime", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID))
		return db.AgentInboxEvent{}, fmt.Errorf("agent has no runtime")
	}
	if err := RequireAgentModel(agent.Model.String); err != nil {
		slog.Error("mention task enqueue failed: agent model required", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID))
		return db.AgentInboxEvent{}, err
	}
	taskContext, err := WithTaskExecutionConfig(nil, agent.Model.String, agent.ThinkingLevel.String)
	if err != nil {
		return db.AgentInboxEvent{}, fmt.Errorf("snapshot mention task execution config: %w", err)
	}

	task, err := q.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID:           agentID,
		RuntimeID:         agent.RuntimeID,
		IssueID:           issue.ID,
		Priority:          priorityToInt(issue.Priority),
		TriggerCommentID:  triggerCommentID,
		TriggerSummary:    s.buildCommentTriggerSummaryWithQueries(ctx, q, triggerCommentID),
		IsLeaderTask:      pgtype.Bool{Bool: isLeader, Valid: isLeader},
		ForceFreshSession: pgtype.Bool{Bool: forceFreshSession, Valid: forceFreshSession},
		Context:           taskContext,
	})
	if err != nil {
		slog.Error("mention task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID), "error", err)
		return db.AgentInboxEvent{}, fmt.Errorf("create task: %w", err)
	}
	return task, nil
}

// PublishMentionTaskQueued emits all post-commit effects for a task inserted by
// CreateMentionTaskRow. It must never be called before the creating transaction
// commits, because it can wake the daemon and cancel older in-flight work.
func (s *TaskService) PublishMentionTaskQueued(ctx context.Context, task db.AgentInboxEvent, issue db.Issue, triggerCommentID pgtype.UUID) {
	s.publishMentionTaskQueuedWithInterruptPolicy(ctx, task, issue, triggerCommentID, true)
}

func (s *TaskService) publishMentionTaskQueued(ctx context.Context, task db.AgentInboxEvent, issue db.Issue, triggerCommentID pgtype.UUID) db.AgentInboxEvent {
	return s.publishMentionTaskQueuedWithInterruptPolicy(ctx, task, issue, triggerCommentID, true)
}

// PublishMentionTaskQueuedPreservingInFlight publishes a visible supervisory
// follow-up without cancelling work the target agent has already started. The
// new task remains queued behind the running task and carries the new comment
// as its trigger.
func (s *TaskService) PublishMentionTaskQueuedPreservingInFlight(ctx context.Context, task db.AgentInboxEvent, issue db.Issue, triggerCommentID pgtype.UUID) {
	s.publishMentionTaskQueuedWithInterruptPolicy(ctx, task, issue, triggerCommentID, false)
}

func (s *TaskService) publishMentionTaskQueuedWithInterruptPolicy(ctx context.Context, task db.AgentInboxEvent, issue db.Issue, triggerCommentID pgtype.UUID, interruptInFlight bool) db.AgentInboxEvent {
	isLeader := task.IsLeaderTask
	slog.Info("mention task enqueued", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(task.AgentID), "is_leader_task", isLeader)
	// Training session-open chokepoint (spec §4.3): leader @mention delegation
	// of a teammate — the trained member's task is typically created here.
	s.tryOpenTrainingSession(ctx, task, issue.ProjectID, "")
	// Delegation event seam (D11): the trained parent that posted the trigger
	// comment delegates to this child. Close the parent's segment and link the
	// child to its parent so the delegation edge is recorded at the child's
	// close. No-op when there is no trained parent (plain user mention).
	if parent, ok := s.discoverDelegationParent(ctx, issue.ID, triggerCommentID, task.ID); ok {
		projectID := util.UUIDToString(issue.ProjectID)
		s.closeSegmentForDelegation(ctx, parent, projectID, s.leanEnvSnapshot(ctx, issue.ProjectID))
		if err := s.Queries.SetTaskParentTaskID(ctx, db.SetTaskParentTaskIDParams{ID: task.ID, ParentTaskID: parent.ID}); err != nil {
			slog.Warn("interaction_dag: set child parent_task_id failed", "child", util.UUIDToString(task.ID), "err", err)
		} else {
			task.ParentTaskID = parent.ID
		}
	}
	// See EnqueueTaskForIssue for ordering rationale.
	s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
	if interruptInFlight {
		s.interruptInFlightIssueTasksForFollowup(ctx, task)
	}
	s.NotifyTaskEnqueued(ctx, task)
	return task
}

// QuickCreateContext is the JSON payload stored on a quick-create task's
// context column. The daemon detects this variant via Type == "quick_create"
// and switches to the quick-create prompt template; the completion path
// uses RequesterID + WorkspaceID to write the inbox notification.
//
// ProjectID is the optional project the user picked in the modal. When
// non-empty the daemon claim handler resolves the project's title +
// resources, and the prompt template instructs the agent to pass
// `--project <uuid>` so the new issue lands in that project.
type QuickCreateContext struct {
	Type          string   `json:"type"`
	Prompt        string   `json:"prompt"`
	RequesterID   string   `json:"requester_id"`
	WorkspaceID   string   `json:"workspace_id"`
	ProjectID     string   `json:"project_id,omitempty"`
	AttachmentIDs []string `json:"attachment_ids,omitempty"`
	// Source carries the visible chat/channel/DM thread context that opened
	// the quick-create flow. It is written into the task context so the
	// daemon can instruct the agent to copy that context into the issue
	// artifact, and so completion can return a human-readable link to the
	// same source thread.
	Source *protocol.QuickCreateSourceContext `json:"source,omitempty"`
	// ParentIssueID is the optional UUID of the parent issue the new issue
	// should be filed under. Set when the user opens the modal from "Add
	// sub issue" on an existing issue; the daemon claim handler resolves the
	// parent's identifier and the prompt template instructs the agent to
	// pass `--parent <uuid>` so the sub-issue relationship is preserved
	// across the manual→agent mode flip.
	ParentIssueID string `json:"parent_issue_id,omitempty"`
}

// QuickCreateContextType marks a task as a quick-create job.
const QuickCreateContextType = "quick_create"

// EnqueueQuickCreateTask creates a queued task that has no issue / chat /
// autopilot link — the user's natural-language prompt is stored in the
// task's context JSONB and the agent is expected to translate it into a
// `multica issue create` call. Pre-validates that the agent is reachable
// (not archived, has a runtime) so the API can reject up-front rather than
// queue a task no one will ever claim.
//
// projectID is optional (zero-valued pgtype.UUID when the user didn't pick
// one). The handler is responsible for validating it belongs to the same
// workspace before passing it in.
//
// parentIssueID is optional (zero-valued pgtype.UUID when the user didn't
// open the modal from "Add sub issue"). The handler is responsible for
// validating it belongs to the same workspace before passing it in.
func (s *TaskService) EnqueueQuickCreateTask(ctx context.Context, workspaceID, requesterID pgtype.UUID, agentID pgtype.UUID, prompt string, projectID, parentIssueID pgtype.UUID, attachmentIDs []pgtype.UUID, source *protocol.QuickCreateSourceContext) (db.AgentInboxEvent, error) {
	agent, err := s.Queries.GetAgent(ctx, agentID)
	if err != nil {
		return db.AgentInboxEvent{}, fmt.Errorf("load agent: %w", err)
	}
	if agent.ArchivedAt.Valid {
		return db.AgentInboxEvent{}, fmt.Errorf("agent is archived")
	}
	if !agent.RuntimeID.Valid {
		return db.AgentInboxEvent{}, fmt.Errorf("agent has no runtime")
	}

	payload := QuickCreateContext{
		Type:        QuickCreateContextType,
		Prompt:      prompt,
		RequesterID: util.UUIDToString(requesterID),
		WorkspaceID: util.UUIDToString(workspaceID),
	}
	if projectID.Valid {
		payload.ProjectID = util.UUIDToString(projectID)
	}
	if parentIssueID.Valid {
		payload.ParentIssueID = util.UUIDToString(parentIssueID)
	}
	if len(attachmentIDs) > 0 {
		payload.AttachmentIDs = make([]string, 0, len(attachmentIDs))
		for _, id := range attachmentIDs {
			if id.Valid {
				payload.AttachmentIDs = append(payload.AttachmentIDs, util.UUIDToString(id))
			}
		}
	}
	if source != nil {
		sourceCopy := *source
		sourceCopy.AttachmentIDs = append([]string(nil), source.AttachmentIDs...)
		payload.Source = &sourceCopy
		// Also stamp source-chat images onto AttachmentIDs so the daemon CLI
		// env auto-binds them on issue create (LRM-731).
		seen := make(map[string]struct{}, len(payload.AttachmentIDs)+len(sourceCopy.AttachmentIDs))
		for _, id := range payload.AttachmentIDs {
			seen[id] = struct{}{}
		}
		for _, id := range sourceCopy.AttachmentIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			payload.AttachmentIDs = append(payload.AttachmentIDs, id)
		}
	}
	contextJSON, err := json.Marshal(payload)
	if err != nil {
		return db.AgentInboxEvent{}, fmt.Errorf("marshal quick-create context: %w", err)
	}
	contextJSON, err = WithTaskExecutionConfig(contextJSON, agent.Model.String, agent.ThinkingLevel.String)
	if err != nil {
		return db.AgentInboxEvent{}, fmt.Errorf("snapshot quick-create execution config: %w", err)
	}

	task, err := s.Queries.CreateQuickCreateTask(ctx, db.CreateQuickCreateTaskParams{
		AgentID:   agentID,
		RuntimeID: agent.RuntimeID,
		Priority:  priorityToInt("high"),
		Context:   contextJSON,
	})
	if err != nil {
		return db.AgentInboxEvent{}, fmt.Errorf("create quick-create task: %w", err)
	}

	slog.Info("quick-create task enqueued",
		"task_id", util.UUIDToString(task.ID),
		"agent_id", util.UUIDToString(agentID),
		"requester_id", util.UUIDToString(requesterID),
		"workspace_id", util.UUIDToString(workspaceID),
		"project_id", payload.ProjectID,
		"parent_issue_id", payload.ParentIssueID,
	)
	// Training session-open chokepoint (spec §4.3): quick-create task. The
	// owning project is the optional projectID the user picked (may be zero,
	// in which case the hook no-ops).
	s.tryOpenTrainingSession(ctx, task, projectID, "")
	// Match every other Enqueue* path: kick the daemon WS so the task
	// gets claimed promptly instead of waiting for the next 30 s poll
	// cycle. Without this the user perceives "quick create never
	// triggered" because the modal closes immediately and the task
	// sits in 'queued' until the next sleepWithContextOrWakeup tick.
	s.NotifyTaskEnqueued(ctx, task)
	return task, nil
}

// ErrChatTaskAgentArchived signals that EnqueueChatTask refused to
// queue work because the destination agent has been archived. This
// is a productizable state — surface it to the user as "this agent
// has been archived" rather than retrying.
var ErrChatTaskAgentArchived = errors.New("chat task: agent archived")

// ErrChatTaskAgentNoRuntime signals that EnqueueChatTask refused to
// queue work because the destination agent has no currently online runtime.
// This covers both "no daemon configured" and "bound daemon is offline" —
// productizable as "agent offline".
var ErrChatTaskAgentNoRuntime = errors.New("chat task: agent has no runtime")

// EnqueueChatTask creates a queued task for a chat session.
// Unlike issue tasks, chat tasks have no issue_id.
//
// Errors split into two layers:
//
//   - Productizable rejections (agent archived, no online runtime) return
//     the sentinel errors above. Callers (e.g. the Lark dispatcher)
//     can errors.Is them to decide a user-visible outcome.
//
//   - Infrastructure failures (DB load / insert errors) are wrapped
//     as ordinary errors. The caller should treat them as retryable
//     or page-worthy, NOT as user-facing state.
//
// initiatorUserID is the user who actually sent the triggering message — the
// real requester behind this run. Callers pass it explicitly because
// chat_session.creator_id is not a reliable source: Lark group sessions set the
// creator to the installer, not the sender (see the lark dispatcher). Web chat
// passes the request user; the lark dispatcher passes the inbound sender of the
// latest message in the silence window. Stored on the task so the daemon brief
// can attribute the run to the right person. See MUL-2645.
func (s *TaskService) EnqueueChatTask(ctx context.Context, chatSession db.ChatSession, initiatorUserID pgtype.UUID) (db.AgentInboxEvent, error) {
	// #311: the system never cancels a directed chat request. A new message that
	// arrives while an earlier one is still running must NOT cancel the in-flight
	// run (interruptFollowup=false). It queues behind the agent's current wake;
	// Canonical inbox admission orders every source by created_at/id. So a
	// different requester's request and a same-requester follow-up both get
	// answered in order rather than one silently cancelling the other (the
	// Frank+海鹏 case).
	return s.enqueueChatTask(ctx, chatSession, initiatorUserID, false, 2, false)
}

// EnqueueFreshChatTask creates a chat task that must not resume the prior
// session. Channel re-mentions use this after interrupting stale in-flight work
// so the agent starts from the latest channel context instead of continuing the
// previous mistaken execution path.
func (s *TaskService) EnqueueFreshChatTask(ctx context.Context, chatSession db.ChatSession, initiatorUserID pgtype.UUID) (db.AgentInboxEvent, error) {
	// #311: interruptFollowup=false — a fresh-session chat run still must not
	// cancel an in-flight directed run; it queues and serializes like EnqueueChatTask.
	return s.enqueueChatTask(ctx, chatSession, initiatorUserID, true, 2, false)
}

// EnqueueAmbientChatTask is for low-priority channel observation runs where the
// agent may stay silent or add a reaction instead of posting a full reply.
func (s *TaskService) EnqueueAmbientChatTask(ctx context.Context, chatSession db.ChatSession, initiatorUserID pgtype.UUID) (db.AgentInboxEvent, error) {
	return s.enqueueChatTask(ctx, chatSession, initiatorUserID, true, 1, false)
}

// CreateAmbientChatTaskRow inserts the low-priority ambient chat task row using
// the supplied query handle without publishing realtime or daemon wake side
// effects. Callers that pass transaction-bound queries must call
// PublishChatTaskQueued only after the transaction commits successfully.
func (s *TaskService) CreateAmbientChatTaskRow(ctx context.Context, q *db.Queries, chatSession db.ChatSession, initiatorUserID pgtype.UUID) (db.AgentInboxEvent, error) {
	return s.createChatTaskRow(ctx, q, chatSession, initiatorUserID, true, 1)
}

// CreateFreshChatTaskRow inserts a normal-priority fresh-session chat task
// using the supplied query handle without publishing realtime or daemon wake
// side effects. Transactional callers must call PublishChatTaskQueued only
// after the transaction commits successfully.
func (s *TaskService) CreateFreshChatTaskRow(ctx context.Context, q *db.Queries, chatSession db.ChatSession, initiatorUserID pgtype.UUID) (db.AgentInboxEvent, error) {
	return s.createChatTaskRow(ctx, q, chatSession, initiatorUserID, true, 2)
}

func (s *TaskService) enqueueChatTask(ctx context.Context, chatSession db.ChatSession, initiatorUserID pgtype.UUID, forceFreshSession bool, priority int32, interruptFollowup bool) (db.AgentInboxEvent, error) {
	task, err := s.createChatTaskRow(ctx, s.Queries, chatSession, initiatorUserID, forceFreshSession, priority)
	if err != nil {
		return db.AgentInboxEvent{}, err
	}
	// Training session-open chokepoint (spec §4.3): chat-bound task. Project
	// resolves via chatSession.ProjectID (seam 1e).
	s.tryOpenTrainingSession(ctx, task, chatSession.ProjectID, "")
	s.PublishChatTaskQueued(ctx, task, interruptFollowup)
	return task, nil
}

func (s *TaskService) createChatTaskRow(ctx context.Context, q *db.Queries, chatSession db.ChatSession, initiatorUserID pgtype.UUID, forceFreshSession bool, priority int32) (db.AgentInboxEvent, error) {
	agent, err := q.GetAgent(ctx, chatSession.AgentID)
	if err != nil {
		slog.Error("chat task enqueue failed", "chat_session_id", util.UUIDToString(chatSession.ID), "error", err)
		return db.AgentInboxEvent{}, fmt.Errorf("load agent: %w", err)
	}
	if agent.ArchivedAt.Valid {
		return db.AgentInboxEvent{}, ErrChatTaskAgentArchived
	}
	if !agent.RuntimeID.Valid {
		slog.Info("chat task enqueue refused: agent has no runtime", "chat_session_id", util.UUIDToString(chatSession.ID), "agent_id", util.UUIDToString(chatSession.AgentID))
		return db.AgentInboxEvent{}, ErrChatTaskAgentNoRuntime
	}
	taskContext, err := WithTaskExecutionConfig(nil, agent.Model.String, agent.ThinkingLevel.String)
	if err != nil {
		return db.AgentInboxEvent{}, fmt.Errorf("snapshot chat task execution config: %w", err)
	}

	task, err := q.CreateChatTask(ctx, db.CreateChatTaskParams{
		AgentID:           chatSession.AgentID,
		RuntimeID:         agent.RuntimeID,
		Priority:          priority,
		ChatSessionID:     chatSession.ID,
		InitiatorUserID:   initiatorUserID,
		ForceFreshSession: pgtype.Bool{Bool: forceFreshSession, Valid: forceFreshSession},
		Context:           taskContext,
	})
	if err != nil {
		slog.Error("chat task enqueue failed", "chat_session_id", util.UUIDToString(chatSession.ID), "error", err)
		return db.AgentInboxEvent{}, fmt.Errorf("create chat task: %w", err)
	}
	return task, nil
}

// PublishChatTaskQueued emits the side effects for a committed chat task row.
// Transactional callers must invoke this only after a successful commit so
// realtime clients and daemons never observe a rolled-back task.
func (s *TaskService) PublishChatTaskQueued(ctx context.Context, task db.AgentInboxEvent, interruptFollowup bool) {
	slog.Info("chat task enqueued", "task_id", util.UUIDToString(task.ID), "chat_session_id", util.UUIDToString(task.ChatSessionID), "agent_id", util.UUIDToString(task.AgentID), "force_fresh_session", task.ForceFreshSession)
	// See EnqueueTaskForIssue for ordering rationale.
	s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
	if interruptFollowup {
		s.interruptInFlightChatTasksForFollowup(ctx, task)
	}
	s.NotifyTaskEnqueued(ctx, task)
}

func (s *TaskService) finalizeCancelledTask(ctx context.Context, task db.AgentInboxEvent) {
	s.captureTaskCancelled(ctx, task)
	s.RouteTerminalTrainingTask(ctx, task)
	s.maybeCleanupEphemeralSandbox(ctx, task)
}

// CancelTasksForIssue cancels every active task on the issue, reconciles each
// affected agent's status, and broadcasts task:cancelled events so frontends
// clear their live cards.
//
// Before #1587 this path was "cancel rows and return" — issue-status flips
// (e.g. user marks the issue `done` or `cancelled` while a task is still
// running) left the agent stuck at status="working" indefinitely, requiring a
// manual `multica agent update <id> --status idle` to unwedge. Matches the
// pattern already used by CancelTask and RerunIssue.
func (s *TaskService) CancelTasksForIssue(ctx context.Context, issueID pgtype.UUID) error {
	cancelled, err := s.Queries.CancelAgentTasksByIssue(ctx, issueID)
	if err != nil {
		return err
	}
	for _, t := range cancelled {
		s.finalizeCancelledTask(ctx, t)
		s.ReconcileAgentStatus(ctx, t.AgentID)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, t)
	}
	return nil
}

// CancelTasksForAgent cancels every active task belonging to an agent
// (queued + dispatched + running), reconciles the agent's status, and
// broadcasts task:cancelled events. Used by the agent-level "Cancel all
// tasks" action — same shape as CancelTasksForIssue but scoped on agent_id.
//
// Returns the cancelled rows so callers can report counts / log them.
func (s *TaskService) CancelTasksForAgent(ctx context.Context, agentID pgtype.UUID) ([]db.AgentInboxEvent, error) {
	cancelled, err := s.Queries.CancelAgentTasksByAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	for _, t := range cancelled {
		s.finalizeCancelledTask(ctx, t)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, t)
	}
	// Reconcile once after the loop — agent transitions from
	// working→available based on remaining task counts, no need to call
	// per row (the rows we just cancelled all belong to the same agent).
	s.ReconcileAgentStatus(ctx, agentID)
	return cancelled, nil
}

// CancelTasksByTriggerComment cancels active tasks whose trigger is the given
// comment. Called from DeleteComment so an agent does not run with the
// now-deleted content already embedded in its prompt. Must be invoked BEFORE
// the comment row is deleted because the FK ON DELETE SET NULL would
// otherwise nullify trigger_comment_id and we'd lose the ability to find
// the affected tasks.
func (s *TaskService) CancelTasksByTriggerComment(ctx context.Context, commentID pgtype.UUID) error {
	cancelled, err := s.Queries.CancelAgentTasksByTriggerComment(ctx, commentID)
	if err != nil {
		return err
	}
	for _, t := range cancelled {
		s.finalizeCancelledTask(ctx, t)
		s.ReconcileAgentStatus(ctx, t.AgentID)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, t)
	}
	return nil
}

// BroadcastCancelledTasks reconciles each affected agent's status and emits
// task:cancelled for every row. Callers must invoke this AFTER committing the
// cancellation so subscribers don't observe a "cancelled" event for a row
// that the tx might still roll back.
func (s *TaskService) BroadcastCancelledTasks(ctx context.Context, cancelled []db.AgentInboxEvent) {
	for _, t := range cancelled {
		s.finalizeCancelledTask(ctx, t)
		s.ReconcileAgentStatus(ctx, t.AgentID)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, t)
	}
}

func (s *TaskService) CaptureCancelledTasks(ctx context.Context, cancelled []db.AgentInboxEvent) {
	for _, t := range cancelled {
		s.finalizeCancelledTask(ctx, t)
	}
}

// FinalizeCancelledResearchWakes finishes research-session Stop for rows that
// CancelInFlightChatTasksByResearchTitle already flipped to cancelled: persist
// partial assistant transcript, mirror into research_message (LRM-820),
// reconcile agent status, and broadcast task:cancelled.
func (s *TaskService) FinalizeCancelledResearchWakes(ctx context.Context, cancelled []db.AgentInboxEvent) {
	for _, t := range cancelled {
		s.finalizeCancelledTask(ctx, t)
		s.finalizeCancelledChatMessage(ctx, t)
		s.ReconcileAgentStatus(ctx, t.AgentID)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, t)
	}
}

type CancelledChatMessageResult struct {
	ChatSessionID  string
	MessageID      string
	Content        string
	RestoreToInput bool
}

type CancelTaskResult struct {
	Task                 db.AgentInboxEvent
	CancelledChatMessage *CancelledChatMessageResult
}

// CancelTask cancels a single task by ID. It broadcasts a task:cancelled event
// so frontends can update immediately.
func (s *TaskService) CancelTask(ctx context.Context, taskID pgtype.UUID) (*db.AgentInboxEvent, error) {
	result, err := s.CancelTaskWithResult(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return &result.Task, nil
}

// CancelTaskWithResult cancels a single task and returns any chat-specific
// cleanup result needed by user-facing callers.
func (s *TaskService) CancelTaskWithResult(ctx context.Context, taskID pgtype.UUID) (*CancelTaskResult, error) {
	task, err := s.Queries.CancelAgentTask(ctx, taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		existing, err := s.Queries.GetAgentTask(ctx, taskID)
		if err != nil {
			return nil, fmt.Errorf("cancel task: %w", err)
		}
		return &CancelTaskResult{Task: existing}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cancel task: %w", err)
	}

	slog.Info("task cancelled", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(task.IssueID))
	s.finalizeCancelledTask(ctx, task)
	cancelledChatMessage := s.finalizeCancelledChatMessage(ctx, task)

	// Reconcile agent status
	s.ReconcileAgentStatus(ctx, task.AgentID)

	// Broadcast cancellation as a task:failed event so frontends clear the live card
	s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, task)

	return &CancelTaskResult{
		Task:                 task,
		CancelledChatMessage: cancelledChatMessage,
	}, nil
}

func (s *TaskService) finalizeCancelledChatMessage(ctx context.Context, task db.AgentInboxEvent) *CancelledChatMessageResult {
	if !task.ChatSessionID.Valid {
		return nil
	}
	var cancelled *CancelledChatMessageResult
	var assistantSnapshot *db.ChatMessage
	if err := s.runInTx(ctx, func(qtx *db.Queries) error {
		messages, err := qtx.ListTaskMessages(ctx, task.ID)
		if err != nil {
			return fmt.Errorf("list cancelled chat task messages: %w", err)
		}
		if len(messages) == 0 {
			deleted, err := qtx.DeleteUserChatMessageByTask(ctx, task.ID)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("delete empty cancelled chat user message: %w", err)
			}
			cancelled = &CancelledChatMessageResult{
				ChatSessionID:  util.UUIDToString(deleted.ChatSessionID),
				MessageID:      util.UUIDToString(deleted.ID),
				Content:        deleted.Content,
				RestoreToInput: true,
			}
			return nil
		}
		// LRM-820: keep already-streamed text; fall back to a short stop marker.
		content := coalesceTaskMessageText(messages)
		if content == "" {
			content = "Stopped."
		}
		row, err := qtx.CreateChatMessage(ctx, db.CreateChatMessageParams{
			ChatSessionID: task.ChatSessionID,
			Role:          "assistant",
			Content:       content,
			TaskID:        task.ID,
			ElapsedMs:     computeChatElapsedMs(task),
		})
		if err != nil {
			return fmt.Errorf("create cancelled chat message: %w", err)
		}
		assistantSnapshot = &row
		return nil
	}); err != nil {
		slog.Error("failed to finalize cancelled chat message",
			"task_id", util.UUIDToString(task.ID),
			"chat_session_id", util.UUIDToString(task.ChatSessionID),
			"error", err,
		)
		return nil
	}
	if assistantSnapshot != nil {
		// Research fleet wakes (title research:<sessionUUID>) must keep the
		// partial reply in the session drawer after Stop.
		s.MirrorResearchChatStoppedReply(ctx, task, *assistantSnapshot)
	}
	return cancelled
}

// StartTask transitions a dispatched task to running.
// Issue status is NOT changed here — the agent manages it via the CLI.
func (s *TaskService) StartTask(ctx context.Context, taskID pgtype.UUID) (*db.AgentInboxEvent, error) {
	var task db.AgentInboxEvent
	err := s.runInTx(ctx, func(qtx *db.Queries) error {
		started, err := qtx.StartAgentTask(ctx, taskID)
		if err != nil {
			return err
		}
		task = started
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("start task: %w", err)
	}

	s.afterTaskStarted(ctx, task)
	return &task, nil
}

type AgentInboxDeliveryFence struct {
	DeliveryID   pgtype.UUID
	InboxEventID pgtype.UUID
	LeaseToken   pgtype.UUID
}

// StartAgentInboxTask persists the immutable provider execution and starts the
// canonical inbox event under the same active-delivery row lock.
func (s *TaskService) StartAgentInboxTask(ctx context.Context, executionID pgtype.UUID, fence AgentInboxDeliveryFence) (*db.AgentInboxEvent, error) {
	var task db.AgentInboxEvent
	err := s.runInTx(ctx, func(qtx *db.Queries) error {
		if _, err := qtx.LockActiveAgentInboxDelivery(ctx, db.LockActiveAgentInboxDeliveryParams{
			ID:           fence.DeliveryID,
			InboxEventID: fence.InboxEventID,
			LeaseToken:   fence.LeaseToken,
		}); err != nil {
			return err
		}
		if err := qtx.CreateAgentInboxExecution(ctx, db.CreateAgentInboxExecutionParams{
			ExecutionID:  executionID,
			InboxEventID: fence.InboxEventID,
		}); err != nil {
			return err
		}
		if _, err := qtx.GetAgentInboxExecution(ctx, db.GetAgentInboxExecutionParams{
			ExecutionID:  executionID,
			InboxEventID: fence.InboxEventID,
		}); err != nil {
			return err
		}
		started, err := qtx.StartAgentTask(ctx, fence.InboxEventID)
		if err != nil {
			return err
		}
		task = started
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("start inbox task: %w", err)
	}

	s.afterTaskStarted(ctx, task)
	return &task, nil
}

func (s *TaskService) afterTaskStarted(ctx context.Context, task db.AgentInboxEvent) {
	slog.Info("task started", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(task.IssueID))
	s.captureTaskStarted(ctx, task)
	// Tell every connected workspace WS client that this task transitioned
	// (dispatched | waiting_local_directory) → running. Without this, the
	// workspace-wide `agentTaskSnapshot` query only refreshes on the 30s
	// staleTime, so any UI that distinguishes "queued" from "running" (e.g.
	// the issue-card agent activity indicator) lags by up to half a minute
	// on the transition users care about most.
	s.broadcastTaskEvent(ctx, protocol.EventTaskRunning, task)
}

// MarkTaskWaitingLocalDirectory parks a dispatched task in the
// waiting_local_directory state while the daemon waits for another in-flight
// task to release the project_resource path lock. reason carries a short
// human-readable hint (typically the contested path) that the UI surfaces
// next to the status. Returns the updated row so the daemon can confirm the
// transition and so the broadcast carries the up-to-date snapshot.
func (s *TaskService) MarkTaskWaitingLocalDirectory(ctx context.Context, taskID pgtype.UUID, reason string) (*db.AgentInboxEvent, error) {
	reason = strings.TrimSpace(reason)
	task, err := s.Queries.MarkAgentTaskWaitingLocalDirectory(ctx, db.MarkAgentTaskWaitingLocalDirectoryParams{
		ID:         taskID,
		WaitReason: pgtype.Text{String: reason, Valid: reason != ""},
	})
	if err != nil {
		return nil, fmt.Errorf("mark task waiting_local_directory: %w", err)
	}

	slog.Info("task waiting_local_directory",
		"task_id", util.UUIDToString(task.ID),
		"issue_id", util.UUIDToString(task.IssueID),
		"reason", reason,
	)
	s.broadcastTaskEvent(ctx, protocol.EventTaskWaitingLocalDirectory, task)
	return &task, nil
}

type CompleteTaskOutcome struct {
	Task         db.AgentInboxEvent
	CompletedNow bool
	AckedSeq     int64
}

// AgentInboxCompleteTxHooks lets the inbox handler extend the canonical task
// completion transaction without splitting delivery/task finalization across
// commits. Before runs before the delivery row is locked so callers such as
// channel onboarding can preserve their required lock order. After runs after
// the task and delivery mutations but before the transaction commits.
type AgentInboxCompleteTxHooks struct {
	Before func(qtx *db.Queries, tx pgx.Tx) error
	After  func(qtx *db.Queries, tx pgx.Tx, outcome *CompleteTaskOutcome) error
}

// CompleteTask marks a task as completed.
func (s *TaskService) CompleteTask(ctx context.Context, taskID pgtype.UUID, result []byte, sessionID, workDir string) (*db.AgentInboxEvent, error) {
	outcome, err := s.completeTask(ctx, taskID, result, sessionID, workDir, nil, AgentInboxCompleteTxHooks{})
	if err != nil {
		return nil, err
	}
	return &outcome.Task, nil
}

// CompleteDaemonTask atomically completes a daemon task. A duplicate or late
// completion returns CompletedNow=false.
func (s *TaskService) CompleteDaemonTask(ctx context.Context, taskID pgtype.UUID, result []byte, sessionID, workDir string) (*CompleteTaskOutcome, error) {
	return s.completeTask(ctx, taskID, result, sessionID, workDir, nil, AgentInboxCompleteTxHooks{})
}

// CompleteDaemonInboxTask fences a non-chat terminal mutation with the active
// transport delivery and acknowledges both in one transaction.
func (s *TaskService) CompleteDaemonInboxTask(ctx context.Context, fence AgentInboxDeliveryFence, result []byte, sessionID, workDir string) (*CompleteTaskOutcome, error) {
	return s.completeTask(ctx, fence.InboxEventID, result, sessionID, workDir, &fence, AgentInboxCompleteTxHooks{})
}

// CompleteDaemonInboxTaskWithFinalization keeps handler-owned terminal
// metadata, execution-ledger updates, and collaboration effects in the same
// transaction as the task transition and delivery acknowledgement.
func (s *TaskService) CompleteDaemonInboxTaskWithFinalization(
	ctx context.Context,
	fence AgentInboxDeliveryFence,
	result []byte,
	sessionID, workDir string,
	hooks AgentInboxCompleteTxHooks,
) (*CompleteTaskOutcome, error) {
	return s.completeTask(ctx, fence.InboxEventID, result, sessionID, workDir, &fence, hooks)
}

// completeTask marks a task as completed.
// Issue status is NOT changed here — the agent manages it via the CLI.
//
// For chat tasks, CompleteAgentTask and the chat_session resume-pointer
// update run in a single transaction. This closes a race where the next
// queued chat message could be claimed in the window between the task
// flipping to 'completed' and chat_session.session_id being refreshed,
// causing the new task to resume against a stale (or NULL) session.
func (s *TaskService) completeTask(
	ctx context.Context,
	taskID pgtype.UUID,
	result []byte,
	sessionID, workDir string,
	fence *AgentInboxDeliveryFence,
	hooks AgentInboxCompleteTxHooks,
) (*CompleteTaskOutcome, error) {
	var task db.AgentInboxEvent
	var ackedSeq int64
	completeAgentTaskUpdated := false
	if err := s.runInTxWithTx(ctx, func(qtx *db.Queries, tx pgx.Tx) error {
		if hooks.Before != nil {
			if tx == nil {
				return errors.New("transaction unavailable for inbox completion finalization")
			}
			if err := hooks.Before(qtx, tx); err != nil {
				return err
			}
		}
		if fence != nil {
			if _, err := qtx.LockCurrentAgentInboxDelivery(ctx, db.LockCurrentAgentInboxDeliveryParams{
				ID:           fence.DeliveryID,
				InboxEventID: fence.InboxEventID,
				LeaseToken:   fence.LeaseToken,
			}); err != nil {
				return err
			}
		}
		t, err := qtx.CompleteAgentTask(ctx, db.CompleteAgentTaskParams{
			ID:        taskID,
			Result:    result,
			SessionID: pgtype.Text{String: sessionID, Valid: sessionID != ""},
			WorkDir:   pgtype.Text{String: workDir, Valid: workDir != ""},
		})
		if err != nil {
			return err
		}
		completeAgentTaskUpdated = true
		task = t

		if fence != nil {
			acked, err := qtx.AckAgentInboxDelivery(ctx, db.AckAgentInboxDeliveryParams{
				ID:           fence.DeliveryID,
				InboxEventID: fence.InboxEventID,
				LeaseToken:   fence.LeaseToken,
			})
			if err != nil {
				return err
			}
			ackedSeq = acked.SeqTo
		}

		if t.ChatSessionID.Valid {
			// Pin the chat_session's runtime_id alongside the session_id so the
			// next claim can apply the runtime-guard. Both fields move together:
			// when there's no session_id to record, leave runtime_id untouched
			// (NULL → COALESCE keeps the existing value).
			var sessionRuntimeID pgtype.UUID
			if sessionID != "" {
				sessionRuntimeID = t.RuntimeID
			}
			// COALESCE in SQL guarantees empty inputs don't wipe the
			// existing resume pointer; we still surface DB errors.
			if err := qtx.UpdateChatSessionSession(ctx, db.UpdateChatSessionSessionParams{
				ID:        t.ChatSessionID,
				SessionID: pgtype.Text{String: sessionID, Valid: sessionID != ""},
				WorkDir:   pgtype.Text{String: workDir, Valid: workDir != ""},
				RuntimeID: sessionRuntimeID,
			}); err != nil {
				return fmt.Errorf("update chat session resume pointer: %w", err)
			}
		}
		if hooks.After != nil {
			if tx == nil {
				return errors.New("transaction unavailable for inbox completion finalization")
			}
			if err := hooks.After(qtx, tx, &CompleteTaskOutcome{
				Task:         task,
				CompletedNow: true,
				AckedSeq:     ackedSeq,
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		// When parallel agents race, a task may already be completed,
		// cancelled, or failed by the time this call runs. The UPDATE
		// … WHERE status = 'running' returns no rows in that case.
		// Treat it as an idempotent success — same pattern as CancelTask.
		if existing, lookupErr := s.Queries.GetAgentTask(ctx, taskID); lookupErr == nil {
			// CompleteAgentTask returning no rows is idempotent only when that
			// first update lost a race to another terminal transition. If the
			// task update succeeded and a later Radar claim returned no rows, the
			// transaction rolled back and the daemon must receive an error so it
			// retries instead of acknowledging an uncompleted running task.
			if fence == nil && errors.Is(err, pgx.ErrNoRows) && !completeAgentTaskUpdated && isTerminalAgentTaskStatus(existing.Status) {
				slog.Info("complete task: already finalized",
					"task_id", util.UUIDToString(taskID),
					"current_status", existing.Status,
					"agent_id", util.UUIDToString(existing.AgentID),
				)
				return &CompleteTaskOutcome{Task: existing}, nil
			}
			slog.Warn("complete task failed",
				"task_id", util.UUIDToString(taskID),
				"current_status", existing.Status,
				"issue_id", util.UUIDToString(existing.IssueID),
				"chat_session_id", util.UUIDToString(existing.ChatSessionID),
				"agent_id", util.UUIDToString(existing.AgentID),
				"error", err,
			)
		} else {
			slog.Warn("complete task failed: task not found",
				"task_id", util.UUIDToString(taskID),
				"lookup_error", lookupErr,
			)
		}
		return nil, fmt.Errorf("complete task: %w", err)
	}

	slog.Info("task completed", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(task.IssueID))
	s.recordEvolutionSkillOutcome(ctx, task.ID, "success", "success")
	s.captureTaskCompleted(ctx, task)
	if s.OnTaskCompleted != nil {
		s.OnTaskCompleted(ctx, task)
	}
	s.FinalizeTerminalTaskSideEffects(ctx, task)

	// Invariant: every completed issue task must have at least one agent
	// comment on the issue, so the user always sees something when a run
	// ends. If the agent posted a comment during execution (result, progress
	// ping, or CLI reply), HasAgentCommentedSince returns true and we skip.
	// Otherwise, synthesize one from the final output. For comment-triggered
	// tasks, TriggerCommentID threads the fallback under the original comment;
	// for assignment-triggered tasks it is NULL and the fallback is top-level.
	// Chat tasks have no IssueID and are handled separately below.
	if task.IssueID.Valid {
		agentCommented, _ := s.Queries.HasAgentCommentedSince(ctx, db.HasAgentCommentedSinceParams{
			IssueID:  task.IssueID,
			AuthorID: task.AgentID,
			Since:    task.StartedAt,
		})
		if !agentCommented {
			var payload protocol.TaskCompletedPayload
			if err := json.Unmarshal(result, &payload); err == nil {
				outputType, outputTypeErr := protocol.NormalizeChatOutputType(payload.Type, strings.TrimSpace(payload.Output) != "" || len(payload.Parts) > 0, payload.Reaction != nil)
				if outputTypeErr != nil {
					slog.Warn("skipping issue fallback comment with invalid chat output type", "task_id", util.UUIDToString(task.ID), "error", outputTypeErr)
				} else if outputType == protocol.ChatOutputKindMessage && payload.Output != "" {
					// Match the CLI's --content / --description behavior: agents that
					// emit literal `\n` 4-char sequences (Python/JSON-style) get them
					// decoded into real newlines before the comment hits the DB. See
					// util.UnescapeBackslashEscapes for the exact contract.
					body := util.UnescapeBackslashEscapes(payload.Output)
					if task.TriggerCommentID.Valid && isTrivialDoneOutput(body) {
						slog.Warn("suppressing trivial comment-trigger fallback output",
							"task_id", util.UUIDToString(task.ID),
							"issue_id", util.UUIDToString(task.IssueID),
							"agent_id", util.UUIDToString(task.AgentID),
						)
					} else {
						s.createAgentComment(ctx, task.IssueID, task.AgentID, redact.Text(body), "comment", task.TriggerCommentID)
					}
				}
			}
		}
	}

	// Quick-create tasks: locate the issue the agent just created and push
	// an inbox confirmation to the requester. The agent has no issue / chat
	// link, so the regular completion paths above don't apply. We find the
	// new issue by querying for the most recent issue this agent created in
	// the requester's workspace since the task started — more robust than
	// parsing the agent's stdout for an identifier.
	if qc, ok := s.parseQuickCreateContext(task); ok {
		s.notifyQuickCreateCompleted(ctx, task, qc)
	}

	// For chat tasks, save assistant reply and broadcast chat:done. The
	// resume pointer was already persisted inside the transaction above.
	if task.ChatSessionID.Valid {
		var assistantMsg *db.ChatMessage
		outputType := protocol.ChatOutputKindNoReply
		var reaction *protocol.ChatReactionPayload
		var payload protocol.TaskCompletedPayload
		var visibleContent string
		var visibleParts []protocol.MessagePart
		outputSuppressedReason := ""

		if err := json.Unmarshal(result, &payload); err == nil {
			outputSuppressedReason = payload.OutputSuppressedReason
			target := strings.TrimSpace(payload.Target)
			// Same unescape as the issue-comment path above: literal `\n` from
			// agent stdout becomes a real newline so the chat panel renders
			// paragraph breaks instead of one wall of prose.
			body := util.UnescapeBackslashEscapes(payload.Output)
			parts := payload.Parts
			if unwrappedBody, unwrappedParts, unwrapped, unwrapErr := messageparts.UnwrapStructuredMessageSend(body, parts); unwrapErr != nil {
				if unwrapped {
					slog.Warn("dropping invalid structured assistant chat output", "task_id", util.UUIDToString(task.ID), "error", unwrapErr)
					body = ""
					parts = nil
				}
			} else if unwrapped {
				body = unwrappedBody
				parts = unwrappedParts
			}
			var partsErr error
			body, parts, partsErr = messageparts.Normalize(body, parts)
			if partsErr != nil {
				slog.Warn("dropping invalid chat message parts", "task_id", util.UUIDToString(task.ID), "error", partsErr)
				parts = nil
			}
			normalizedOutputType, outputTypeErr := protocol.NormalizeChatOutputType(payload.Type, strings.TrimSpace(body) != "" || len(parts) > 0, payload.Reaction != nil)
			if outputTypeErr != nil {
				slog.Warn("skipping assistant chat message with invalid output type", "task_id", util.UUIDToString(task.ID), "error", outputTypeErr)
			} else if normalizedOutputType == protocol.ChatOutputKindNoReply {
				outputType = protocol.ChatOutputKindNoReply
			} else if normalizedOutputType == protocol.ChatOutputKindReaction {
				outputType = protocol.ChatOutputKindReaction
				reaction = payload.Reaction
			} else if target != "" {
				outputType = protocol.ChatOutputKindMessage
				visibleContent = redact.Text(body)
				visibleParts = parts
			} else if strings.TrimSpace(body) == "" {
				slog.Warn("skipping empty assistant chat message", "task_id", util.UUIDToString(task.ID))
			} else {
				outputType = protocol.ChatOutputKindMessage
				row, err := s.Queries.CreateChatMessage(ctx, db.CreateChatMessageParams{
					ChatSessionID: task.ChatSessionID,
					Role:          "assistant",
					Content:       redact.Text(body),
					Parts:         messageparts.MustJSON(parts),
					TaskID:        task.ID,
					ElapsedMs:     computeChatElapsedMs(task),
				})
				if err != nil {
					slog.Error("failed to save assistant chat message", "task_id", util.UUIDToString(task.ID), "error", err)
				} else {
					assistantMsg = &row
					// Event-driven unread: stamp unread_since on the first unread
					// assistant message. No-op if the session already has unread.
					// If the user is actively viewing the session, the frontend's
					// auto-mark-read effect will clear this within a tick.
					if err := s.Queries.SetUnreadSinceIfNull(ctx, task.ChatSessionID); err != nil {
						slog.Warn("failed to set unread_since", "chat_session_id", util.UUIDToString(task.ChatSessionID), "error", err)
					}
					s.MirrorResearchChatReply(ctx, task, row)
				}
			}
		}

		s.broadcastChatDone(ctx, task, assistantMsg, outputType, payload.Target, visibleContent, visibleParts, reaction, outputSuppressedReason)
	}

	// Reconcile agent status
	s.ReconcileAgentStatus(ctx, task.AgentID)

	// Broadcast
	s.broadcastTaskEvent(ctx, protocol.EventTaskCompleted, task)

	return &CompleteTaskOutcome{
		Task:         task,
		CompletedNow: true,
		AckedSeq:     ackedSeq,
	}, nil
}

func (s *TaskService) terminalTaskProjectID(ctx context.Context, task db.AgentInboxEvent) (pgtype.UUID, error) {
	if task.IssueID.Valid {
		issue, err := s.Queries.GetIssue(ctx, task.IssueID)
		if err != nil {
			return pgtype.UUID{}, err
		}
		return issue.ProjectID, nil
	}
	if task.ChatSessionID.Valid {
		session, err := s.Queries.GetChatSession(ctx, task.ChatSessionID)
		if err != nil {
			return pgtype.UUID{}, err
		}
		return session.ProjectID, nil
	}
	return pgtype.UUID{}, nil
}

func isTerminalAgentTaskStatus(status string) bool {
	switch status {
	case "acked", "suppressed":
		return true
	default:
		return false
	}
}

// FailTask marks a task as failed.
// Issue status is NOT changed here — the agent manages it via the CLI.
//
// sessionID/workDir are optional: when the agent established a real session
// before failing (e.g. crashed mid-conversation, was cancelled, or hit a
// tool error), the daemon should pass them so we can preserve the resume
// pointer on both the task row and the chat_session — otherwise the next
// chat turn would silently start a brand-new session and lose memory.
//
// failureReason is a coarse classifier consumed by the auto-retry path.
// Pass "" when unknown — the server runs the raw error text through
// taskfailure.Classify so the persisted failure_reason still lands in
// the canonical refined taxonomy rather than the legacy "agent_error"
// coarse bucket. Daemon callers that already produced a refined reason
// (via classifyPoisonedError, the timeout / runtime classifier, etc.)
// will have their value preserved untouched.
func (s *TaskService) FailTask(ctx context.Context, taskID pgtype.UUID, errMsg, sessionID, workDir, failureReason string) (*db.AgentInboxEvent, error) {
	outcome, err := s.failTask(ctx, taskID, errMsg, sessionID, workDir, failureReason, false, nil, nil)
	if err != nil {
		return nil, err
	}
	return &outcome.Task, nil
}

// FailTaskWithoutPublicOutput records the terminal failure while suppressing
// issue comments, chat messages, and requester notifications. Restricted
// cognition profiles use this fail-closed path so provider/config/schema
// failures remain internal and can never become a public agent response.
func (s *TaskService) FailTaskWithoutPublicOutput(ctx context.Context, taskID pgtype.UUID, errMsg, sessionID, workDir, failureReason string) (*db.AgentInboxEvent, error) {
	outcome, err := s.failTask(ctx, taskID, errMsg, sessionID, workDir, failureReason, true, nil, nil)
	if err != nil {
		return nil, err
	}
	return &outcome.Task, nil
}

type FailTaskOutcome struct {
	Task     db.AgentInboxEvent
	AckedSeq int64
}

// AgentInboxFailTxFinalizer extends the fenced failure transaction with
// handler-owned terminal metadata and execution-ledger state. It runs after
// the task and delivery mutations but before commit.
type AgentInboxFailTxFinalizer func(qtx *db.Queries, tx pgx.Tx, outcome *FailTaskOutcome) error

// FailAgentInboxTask fences a non-chat terminal failure and delivery
// acknowledgement in one transaction before any retry child is created.
func (s *TaskService) FailAgentInboxTask(ctx context.Context, fence AgentInboxDeliveryFence, errMsg, sessionID, workDir, failureReason string) (*FailTaskOutcome, error) {
	return s.failTask(ctx, fence.InboxEventID, errMsg, sessionID, workDir, failureReason, false, &fence, nil)
}

// FailAgentInboxTaskWithFinalization keeps terminal metadata, execution-ledger
// failure, and collaboration effects in the same transaction as the task
// transition and delivery acknowledgement.
func (s *TaskService) FailAgentInboxTaskWithFinalization(
	ctx context.Context,
	fence AgentInboxDeliveryFence,
	errMsg, sessionID, workDir, failureReason string,
	finalize AgentInboxFailTxFinalizer,
) (*FailTaskOutcome, error) {
	return s.failTask(ctx, fence.InboxEventID, errMsg, sessionID, workDir, failureReason, false, &fence, finalize)
}

func (s *TaskService) failTask(
	ctx context.Context,
	taskID pgtype.UUID,
	errMsg, sessionID, workDir, failureReason string,
	suppressPublicOutput bool,
	fence *AgentInboxDeliveryFence,
	finalize AgentInboxFailTxFinalizer,
) (*FailTaskOutcome, error) {
	// MUL-2946: synthesise a refined reason from the error text whenever the
	// caller didn't supply one. This is the last write-path guard against
	// "agent_error" coarse rows ending up in agent_inbox_event.failure_reason
	// — every other path either provides a classified reason directly
	// (sweepers writing 'queued_expired' / 'runtime_offline' / 'timeout'
	// / 'runtime_recovery' via SQL) or runs the daemon's classifyPoisonedError
	// + taskfailure.Classify chain.
	if failureReason == "" {
		failureReason = taskfailure.Classify(errMsg).String()
	}
	var task db.AgentInboxEvent
	var ackedSeq int64
	if err := s.runInTxWithTx(ctx, func(qtx *db.Queries, tx pgx.Tx) error {
		if fence != nil {
			if _, err := qtx.LockCurrentAgentInboxDelivery(ctx, db.LockCurrentAgentInboxDeliveryParams{
				ID:           fence.DeliveryID,
				InboxEventID: fence.InboxEventID,
				LeaseToken:   fence.LeaseToken,
			}); err != nil {
				return err
			}
		}
		t, err := qtx.FailAgentTask(ctx, db.FailAgentTaskParams{
			ID:            taskID,
			Error:         pgtype.Text{String: errMsg, Valid: true},
			FailureReason: pgtype.Text{String: failureReason, Valid: failureReason != ""},
			SessionID:     pgtype.Text{String: sessionID, Valid: sessionID != ""},
			WorkDir:       pgtype.Text{String: workDir, Valid: workDir != ""},
		})
		if err != nil {
			return err
		}
		task = t

		if fence != nil {
			acked, err := qtx.AckAgentInboxDelivery(ctx, db.AckAgentInboxDeliveryParams{
				ID:           fence.DeliveryID,
				InboxEventID: fence.InboxEventID,
				LeaseToken:   fence.LeaseToken,
			})
			if err != nil {
				return err
			}
			ackedSeq = acked.SeqTo
		}

		// Keep resume-unsafe sessions on the task row for observability, but
		// do not promote them to the chat-level resume pointer. If the existing
		// pointer is the same poisoned session, clear it instead: merely not
		// promoting the failed session leaves an older pointer intact, causing
		// every subsequent chat turn to resume the same stuck conversation.
		if t.ChatSessionID.Valid && resumeUnsafeFailureReason(failureReason) && sessionID != "" {
			if err := qtx.ClearChatSessionResumeIfMatch(ctx, db.ClearChatSessionResumeIfMatchParams{
				ID:        t.ChatSessionID,
				SessionID: pgtype.Text{String: sessionID, Valid: true},
			}); err != nil {
				return fmt.Errorf("clear poisoned chat session resume pointer: %w", err)
			}
		} else if t.ChatSessionID.Valid {
			// Pin the chat_session's runtime_id alongside the session_id so the
			// next claim can apply the runtime-guard. Both fields move together:
			// when there's no session_id to record, leave runtime_id untouched
			// (NULL → COALESCE keeps the existing value).
			var sessionRuntimeID pgtype.UUID
			if sessionID != "" {
				sessionRuntimeID = t.RuntimeID
			}
			if err := qtx.UpdateChatSessionSession(ctx, db.UpdateChatSessionSessionParams{
				ID:        t.ChatSessionID,
				SessionID: pgtype.Text{String: sessionID, Valid: sessionID != ""},
				WorkDir:   pgtype.Text{String: workDir, Valid: workDir != ""},
				RuntimeID: sessionRuntimeID,
			}); err != nil {
				return fmt.Errorf("update chat session resume pointer: %w", err)
			}
		}
		if finalize != nil {
			if tx == nil {
				return errors.New("transaction unavailable for inbox failure finalization")
			}
			if err := finalize(qtx, tx, &FailTaskOutcome{Task: task, AckedSeq: ackedSeq}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		if existing, lookupErr := s.Queries.GetAgentTask(ctx, taskID); lookupErr == nil {
			if fence == nil && errors.Is(err, pgx.ErrNoRows) {
				slog.Info("fail task: already finalized",
					"task_id", util.UUIDToString(taskID),
					"current_status", existing.Status,
					"agent_id", util.UUIDToString(existing.AgentID),
				)
				return &FailTaskOutcome{Task: existing}, nil
			}
			slog.Warn("fail task failed",
				"task_id", util.UUIDToString(taskID),
				"current_status", existing.Status,
				"issue_id", util.UUIDToString(existing.IssueID),
				"chat_session_id", util.UUIDToString(existing.ChatSessionID),
				"agent_id", util.UUIDToString(existing.AgentID),
				"error", err,
			)
		} else {
			slog.Warn("fail task failed: task not found",
				"task_id", util.UUIDToString(taskID),
				"lookup_error", lookupErr,
			)
		}
		return nil, fmt.Errorf("fail task: %w", err)
	}

	slog.Warn("task failed", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(task.IssueID), "error", errMsg, "failure_reason", failureReason)
	s.recordEvolutionSkillOutcome(ctx, task.ID, "failure", "failure")
	s.captureTaskFailed(ctx, task)
	s.RouteTerminalTrainingTask(ctx, task)

	// Auto-retry eligible failures (orphan, timeout, runtime_offline,
	// runtime_recovery). The helper itself enforces attempt < max_attempts
	// and only triggers for issue/chat tasks.
	retried, _ := s.MaybeRetryFailedTask(ctx, task)
	s.maybeCleanupEphemeralSandbox(ctx, task)

	// Skip the per-failure system comment when we'll immediately retry —
	// the new task will surface its own status to the user, and we don't
	// want to spam the issue with "task timed out" messages on every
	// daemon hiccup.
	if !suppressPublicOutput && errMsg != "" && task.IssueID.Valid && retried == nil {
		s.createAgentComment(ctx, task.IssueID, task.AgentID, redact.Text(errMsg), "system", task.TriggerCommentID)
	}

	// Mirror the issue fallback for chat tasks: write an assistant
	// chat_message tagged with the daemon-reported failure_reason so the
	// conversation history shows what happened. Skip when auto-retry is
	// pending (the new attempt will write its own outcome) — same guard as
	// the issue path above.
	if !suppressPublicOutput && task.ChatSessionID.Valid && retried == nil {
		if _, err := s.Queries.CreateChatMessage(ctx, db.CreateChatMessageParams{
			ChatSessionID: task.ChatSessionID,
			Role:          "assistant",
			Content:       redact.Text(errMsg),
			TaskID:        pgtype.UUID{Bytes: task.ID.Bytes, Valid: true},
			FailureReason: pgtype.Text{String: failureReason, Valid: failureReason != ""},
			ElapsedMs:     computeChatElapsedMs(task),
		}); err != nil {
			slog.Error("failed to save failure chat message",
				"task_id", util.UUIDToString(task.ID),
				"chat_session_id", util.UUIDToString(task.ChatSessionID),
				"error", err)
		} else if err := s.Queries.SetUnreadSinceIfNull(ctx, task.ChatSessionID); err != nil {
			slog.Warn("failed to set unread_since on failure",
				"chat_session_id", util.UUIDToString(task.ChatSessionID),
				"error", err)
		}
	}

	// Quick-create tasks: push a failure inbox notification to the
	// requester so they can either retry or fall back to the advanced form
	// without losing their original prompt. Skipped when an auto-retry is
	// pending — the new attempt will write its own outcome.
	if !suppressPublicOutput && retried == nil {
		if qc, ok := s.parseQuickCreateContext(task); ok {
			s.notifyQuickCreateFailed(ctx, task, qc, errMsg)
		}
	}
	// Reconcile agent status
	s.ReconcileAgentStatus(ctx, task.AgentID)

	// Broadcast
	s.broadcastTaskEvent(ctx, protocol.EventTaskFailed, task)

	return &FailTaskOutcome{Task: task, AckedSeq: ackedSeq}, nil
}

// retryableReasons enumerates failure reasons that the auto-retry path is
// allowed to act on. Agent-side errors (compile failures, model rejections,
// etc.) are intentionally excluded — those are real problems that the user
// should see, not infrastructure flakiness.
var retryableReasons = map[string]bool{
	"runtime_offline":           true,
	"runtime_recovery":          true,
	"timeout":                   true,
	"codex_semantic_inactivity": true,
}

func resumeUnsafeFailureReason(reason string) bool {
	switch reason {
	// Keep in sync with GetLastTaskSession / GetLastChatTaskSession and
	// CreateRetryTask's fresh-session CASE WHEN.
	case "iteration_limit", "agent_fallback_message", "api_invalid_request", "codex_semantic_inactivity", "grok_first_turn_no_progress", taskfailure.ReasonAgentContextOverflow.String():
		return true
	default:
		return false
	}
}

// MaybeRetryFailedTask spawns a fresh queued attempt for a recently-failed
// task when the failure was infrastructure-shaped (daemon crash, runtime
// went offline, dispatch/run timeout) and the task hasn't exhausted its
// max_attempts budget. The child task inherits agent/runtime/issue/chat
// links and, for resume-safe failures, the parent's session_id/work_dir so
// the agent can resume the conversation when the backend supports it. Returns
// the new task, or nil when no retry was created.
//
// Autopilot tasks are NOT auto-retried here; the autopilot scheduler owns
// its own re-run cadence and we don't want to double-fire it.
func (s *TaskService) MaybeRetryFailedTask(ctx context.Context, parent db.AgentInboxEvent) (*db.AgentInboxEvent, error) {
	if parent.Status != "acked" || !parent.TerminalOutcome.Valid || parent.TerminalOutcome.String != "failed" {
		return nil, nil
	}
	reason := ""
	if parent.FailureReason.Valid {
		reason = parent.FailureReason.String
	}
	if !retryableReasons[reason] {
		return nil, nil
	}
	if parent.Attempt >= parent.MaxAttempts {
		slog.Info("task auto-retry skipped: budget exhausted",
			"task_id", util.UUIDToString(parent.ID),
			"attempt", parent.Attempt,
			"max_attempts", parent.MaxAttempts,
		)
		return nil, nil
	}
	if parent.AutopilotRunID.Valid {
		// Autopilot has its own retry semantics; do not double-trigger.
		return nil, nil
	}
	if !parent.IssueID.Valid && !parent.ChatSessionID.Valid {
		return nil, nil
	}

	var retryResources *EphemeralRetryResources
	var err error
	if reason == "runtime_offline" {
		if _, ephemeral := extractEphemeralSandbox(parent.Context); ephemeral && s.EphemeralSandboxManager != nil {
			retryResources, err = s.EphemeralSandboxManager.PrepareRetry(ctx, parent)
			if err != nil {
				return nil, fmt.Errorf("prepare ephemeral sandbox retry: %w", err)
			}
			if retryResources == nil {
				return nil, fmt.Errorf("prepare ephemeral sandbox retry: empty resources")
			}
		}
	}

	child, err := s.createRetryTaskWithPendingWakeTransfer(ctx, parent.ID, retryResources)
	if err != nil {
		if retryResources != nil {
			if reclaimErr := s.EphemeralSandboxManager.Reclaim(context.WithoutCancel(ctx), retryResources); reclaimErr != nil {
				err = errors.Join(err, fmt.Errorf("reclaim ephemeral retry resources: %w", reclaimErr))
			}
		}
		slog.Warn("task auto-retry failed",
			"parent_task_id", util.UUIDToString(parent.ID),
			"reason", reason,
			"error", err,
		)
		return nil, err
	}
	slog.Info("task auto-retry enqueued",
		"parent_task_id", util.UUIDToString(parent.ID),
		"child_task_id", util.UUIDToString(child.ID),
		"reason", reason,
		"attempt", child.Attempt,
		"max_attempts", child.MaxAttempts,
	)
	// Retry creates a fresh queued row, same status transition (∅ → queued)
	// as EnqueueTaskFor*. Broadcast queued first, then notify the daemon —
	// see EnqueueTaskForIssue for ordering rationale.
	// D9: open a FRESH areal RL session for the retry child before announcing it
	// (mirrors enqueueMentionTask's open->broadcast->notify order). The child's
	// context was stripped of areal_proxy (Task 6), so maybeOpenTrainingSession's
	// idempotency guard passes and a new session is opened + mapped (D10).
	s.openFreshSessionForRetryChild(ctx, child)
	s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, child)
	s.NotifyTaskEnqueued(ctx, child)

	// Fire the optional subagent-lifecycle callback so activity events
	// can be recorded for the retry child task.
	if s.OnChildTaskCreated != nil {
		s.OnChildTaskCreated(ctx, parent, child)
	}

	return &child, nil
}

// openFreshSessionForRetryChild opens a FRESH areal RL session for a retry child
// before the child is broadcast/announced to the daemon, mirroring
// enqueueMentionTask's open->broadcast->notify order (D9). Task 6 stripped
// areal_proxy from the child's context (createRetryTaskWithPendingWakeTransfer),
// so maybeOpenTrainingSession's idempotency guard passes and a new session is
// opened + mapped (D10 RecordSessionAgentRun fires for the child's task.ID).
// The parent's session was already closed by RouteTerminalTrainingTask.
//
// Resolution mirrors the other Enqueue* chokepoints (enqueueMentionTask):
// child.IssueID -> GetIssue -> issue.ProjectID -> GetProject -> proj.EnvID, then
// tryOpenTrainingSession. tryOpenTrainingSession already gates on s.Training ==
// nil and logs errors loudly without failing the enqueue, so this helper is
// best-effort: a resolution miss or session-open error is logged and skipped,
// never failing the retry.
func (s *TaskService) openFreshSessionForRetryChild(ctx context.Context, child db.AgentInboxEvent) {
	if !child.IssueID.Valid {
		return
	}
	issue, err := s.Queries.GetIssue(ctx, child.IssueID)
	if err != nil {
		slog.Warn("interaction_dag: retry child session open skipped: issue lookup failed",
			"child_task_id", util.UUIDToString(child.ID),
			"issue_id", util.UUIDToString(child.IssueID),
			"error", err,
		)
		return
	}
	if !issue.ProjectID.Valid {
		return
	}
	envID := ""
	if proj, err := s.Queries.GetProject(ctx, issue.ProjectID); err == nil {
		envID = util.UUIDToString(proj.EnvID)
	} else {
		slog.Warn("interaction_dag: retry child session open: project lookup failed",
			"child_task_id", util.UUIDToString(child.ID),
			"project_id", util.UUIDToString(issue.ProjectID),
			"error", err,
		)
	}
	s.tryOpenTrainingSession(ctx, child, issue.ProjectID, envID)
}

func (s *TaskService) createRetryTaskWithPendingWakeTransfer(ctx context.Context, parentID pgtype.UUID, resources *EphemeralRetryResources) (db.AgentInboxEvent, error) {
	params := db.CreateRetryTaskParams{ID: parentID}
	if resources != nil {
		params.RuntimeID = resources.RuntimeID
		params.Context = resources.Context
	}
	if s.TxStarter == nil {
		child, err := s.Queries.CreateRetryTask(ctx, params)
		if err != nil {
			return db.AgentInboxEvent{}, fmt.Errorf("create retry task: %w", err)
		}
		// D9: CreateRetryTask copies the parent's context verbatim, so the child
		// would inherit the parent's (now-closed) RL session. Strip areal_proxy so
		// the child opens a fresh session at its own session-open chokepoint. maybe
		// OpenTrainingSession re-loads the task from DB, so no in-memory fixup needed.
		if err := s.Queries.StripArealProxyFromTaskContext(ctx, child.ID); err != nil {
			return db.AgentInboxEvent{}, fmt.Errorf("strip areal_proxy from retry child: %w", err)
		}
		return child, nil
	}
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return db.AgentInboxEvent{}, fmt.Errorf("begin retry task transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := s.Queries.WithTx(tx)
	child, err := qtx.CreateRetryTask(ctx, params)
	if err != nil {
		return db.AgentInboxEvent{}, fmt.Errorf("create retry task: %w", err)
	}
	if err := qtx.StripArealProxyFromTaskContext(ctx, child.ID); err != nil {
		return db.AgentInboxEvent{}, fmt.Errorf("strip areal_proxy from retry child: %w", err)
	}
	if err := transferQueuedPendingWakeTask(ctx, tx, parentID, child.ID); err != nil {
		return db.AgentInboxEvent{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return db.AgentInboxEvent{}, err
	}
	return child, nil
}

func transferQueuedPendingWakeTask(ctx context.Context, tx pgx.Tx, parentID, childID pgtype.UUID) error {
	rows, err := tx.Query(ctx, `
		SELECT status
		FROM channel_ambient_pending_wake
		WHERE task_id = $1
		FOR UPDATE`, parentID)
	if err != nil {
		return fmt.Errorf("check pending wake state: %w", err)
	}
	defer rows.Close()

	pendingRows := int64(0)
	for rows.Next() {
		pendingRows++
		var status string
		if err := rows.Scan(&status); err != nil {
			return fmt.Errorf("scan pending wake state: %w", err)
		}
		if status != "queued" {
			return fmt.Errorf("pending wake for parent task is %s, want queued", status)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("scan pending wake state: %w", err)
	}

	tag, err := tx.Exec(ctx, `
		UPDATE channel_ambient_pending_wake
		SET task_id = $2, updated_at = now()
		WHERE task_id = $1 AND status = 'queued'`, parentID, childID)
	if err != nil {
		return fmt.Errorf("update pending wake task: %w", err)
	}
	if tag.RowsAffected() != pendingRows {
		return fmt.Errorf("pending wake transfer updated %d rows, want %d", tag.RowsAffected(), pendingRows)
	}
	return nil
}

// RerunIssue creates a fresh queued task for an agent on the issue. Used by
// the manual rerun endpoint.
//
// Target agent resolution:
//   - sourceTaskID Valid: rerun the agent that ran that task (and reuse its
//     leader/worker role). This is what the execution log retry button uses
//     so a per-row retry survives a subsequent assignee change and correctly
//     re-fires the squad worker or mention agent whose row was clicked. The
//     source task's trigger_comment_id is also inherited (when the caller
//     didn't pass one) so a per-row rerun of a comment- or mention-triggered
//     task stays comment-triggered — the daemon's buildCommentPrompt path
//     keys on TriggerCommentID, and losing it would degrade the rerun into
//     a generic issue run that no longer carries the original comment.
//   - sourceTaskID empty: fall back to the issue's current assignee (agent
//     or squad leader). This preserves the CLI / API contract for callers
//     that have an issue ID but no specific task to target.
//
// The new task is flagged force_fresh_session=true so the daemon starts a
// clean agent session instead of resuming the prior (agent_id, issue_id)
// session. A user clicking rerun has just judged the prior output bad —
// resuming the same conversation would replay the same poisoned state.
// Auto-retry of an orphaned mid-flight failure (HandleFailedTasks →
// MaybeRetryFailedTask → CreateRetryTask) does NOT take this path, so
// MUL-1128's mid-flight resume contract is preserved.
//
// Only tasks belonging to the target agent on this issue are cancelled.
// Tasks owned by other agents on the same issue (e.g. a parallel
// @-mention agent) are left alone — rerun must not collateral-cancel
// them.
func (s *TaskService) RerunIssue(ctx context.Context, issueID pgtype.UUID, sourceTaskID pgtype.UUID, triggerCommentID pgtype.UUID) (*db.AgentInboxEvent, error) {
	issue, err := s.Queries.GetIssue(ctx, issueID)
	if err != nil {
		return nil, fmt.Errorf("load issue: %w", err)
	}

	// Determine the target agent for the rerun.
	var (
		agentID  pgtype.UUID
		isLeader bool
	)
	if sourceTaskID.Valid {
		sourceTask, err := s.Queries.GetAgentTask(ctx, sourceTaskID)
		if err != nil {
			return nil, fmt.Errorf("load source task: %w", err)
		}
		if !sourceTask.IssueID.Valid || util.UUIDToString(sourceTask.IssueID) != util.UUIDToString(issueID) {
			return nil, fmt.Errorf("source task does not belong to this issue")
		}
		agentID = sourceTask.AgentID
		isLeader = sourceTask.IsLeaderTask
		// Inherit trigger provenance so a per-row rerun of a comment- or
		// mention-triggered task stays a comment-triggered task. Without
		// this the daemon's buildCommentPrompt path is skipped (it keys on
		// TriggerCommentID) and the rerun degrades into a generic issue
		// run that has lost the original comment context. Only override
		// when the caller didn't pass one explicitly.
		if !triggerCommentID.Valid && sourceTask.TriggerCommentID.Valid {
			triggerCommentID = sourceTask.TriggerCommentID
		}
	} else {
		if issue.AssigneeType.String != "agent" || !issue.AssigneeID.Valid {
			return nil, fmt.Errorf("issue is not assigned to an agent")
		}
		agentID = issue.AssigneeID
	}

	// Cancel only the target agent's active/queued tasks on this issue.
	cancelled, err := s.Queries.CancelAgentTasksByIssueAndAgent(ctx, db.CancelAgentTasksByIssueAndAgentParams{
		IssueID: issueID,
		AgentID: agentID,
	})
	if err != nil {
		slog.Warn("rerun: cancel prior tasks failed",
			"issue_id", util.UUIDToString(issueID),
			"agent_id", util.UUIDToString(agentID),
			"error", err,
		)
	}
	for _, t := range cancelled {
		s.finalizeCancelledTask(ctx, t)
		s.ReconcileAgentStatus(ctx, t.AgentID)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, t)
	}

	task, err := s.enqueueRerunTask(ctx, issue, agentID, triggerCommentID, isLeader)
	if err != nil {
		return nil, err
	}
	slog.Info("issue rerun enqueued",
		"task_id", util.UUIDToString(task.ID),
		"issue_id", util.UUIDToString(issueID),
		"agent_id", util.UUIDToString(agentID),
		"source_task_id", util.UUIDToString(sourceTaskID),
		"is_leader", isLeader,
		"cancelled_prior", len(cancelled),
	)
	return &task, nil
}

// enqueueRerunTask enqueues a fresh task for the given agent on the issue.
// When the target agent is the issue's single-agent assignee we use the
// assignee-driven path (enqueueIssueTask) so the issue-assignee bookkeeping
// stays in sync; otherwise (a prior assignee that has since been reassigned,
// or a mention agent) we use the mention path with the same
// force_fresh_session=true contract.
func (s *TaskService) enqueueRerunTask(ctx context.Context, issue db.Issue, agentID pgtype.UUID, triggerCommentID pgtype.UUID, isLeader bool) (db.AgentInboxEvent, error) {
	if issue.AssigneeType.String == "agent" && issue.AssigneeID.Valid &&
		util.UUIDToString(issue.AssigneeID) == util.UUIDToString(agentID) {
		return s.enqueueIssueTask(ctx, issue, triggerCommentID, true)
	}
	return s.enqueueMentionTask(ctx, issue, agentID, triggerCommentID, isLeader, true)
}

// ephemeralSandboxContextKey is the top-level key under which the Phase 5
// ephemeral_sandbox marker is stored in a task's context JSONB (written at
// dispatch by mergeEphemeralSandboxContext). It carries the sandbox_instance_id
// the terminal cleanup hook reads to reclaim the ephemeral Cube sandbox.
const ephemeralSandboxContextKey = "ephemeral_sandbox"

// EphemeralSandboxMarker holds the Phase 5 sandbox-instance marker stored at
// context.ephemeral_sandbox on an env-dispatch ephemeral rollout task.
type EphemeralSandboxMarker struct {
	SandboxInstanceID string `json:"sandbox_instance_id"`
	ActorUserID       string `json:"actor_user_id"`
	// CleanupOnTerminal defaults to true when omitted for compatibility with
	// existing ephemeral rollout tasks. Env-dispatch channels set it false so
	// their sandbox remains available until the channel cleanup endpoint runs.
	CleanupOnTerminal *bool `json:"cleanup_on_terminal,omitempty"`
}

// ExtractEphemeralSandbox reads the Phase 5 ephemeral_sandbox marker from a
// task's context. Returns the marker and true when the task is an ephemeral
// rollout, or (nil, false) when it's not.
func ExtractEphemeralSandbox(raw []byte) (*EphemeralSandboxMarker, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var envelope struct {
		EphemeralSandbox *EphemeralSandboxMarker `json:"ephemeral_sandbox"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, false
	}
	p := envelope.EphemeralSandbox
	return p, p != nil && p.SandboxInstanceID != ""
}

// extractEphemeralSandbox preserves the package-local helper used by existing
// cleanup code and tests while the handler consumes the exported parser.
func extractEphemeralSandbox(raw []byte) (*EphemeralSandboxMarker, bool) {
	return ExtractEphemeralSandbox(raw)
}

// maybeCleanupEphemeralSandbox is the Phase 5 terminal hook: if this task was
// dispatched against an ephemeral env-dispatch sandbox, and no other active task
// is still bound to the same pre-created runtime R', reclaim the sandbox
// instance (stop+delete via sandboxd) and mark R' offline. Best-effort — errors
// are logged but never fail the terminal flow.
// FinalizeTerminalTaskSideEffects runs the post-commit side effects that every
// terminal task owes regardless of how it reached terminal: the interaction-DAG
// segment close (D11), RL training routing, and ephemeral sandbox teardown.
//
// The segment close must precede RouteTerminalTrainingTask, which ends the RL
// session — CloseSegmentForEvent exports the just-closed trajectory over the
// still-live session. One segment per task; the delegation edge to the parent is
// recorded at the child's close.
//
// Every terminal path must call this. completeTask and failTask cover issue and
// work tasks, but a chat-session task is acked directly by the daemon inbox
// handler and never reaches either — and env-dispatch rollout agents always run
// in a chat session, so they closed no segment (every assembled DAG came back
// empty) and leaked their sandbox.
//
// Each step self-gates: tasks outside a project, without areal_proxy context,
// or without an ephemeral sandbox marker are no-ops here.
func (s *TaskService) FinalizeTerminalTaskSideEffects(ctx context.Context, task db.AgentInboxEvent) {
	// Message dispatch roots carry their project through chat_session rather
	// than issue_id, so resolve both task shapes before recording.
	if projectID, err := s.terminalTaskProjectID(ctx, task); err != nil {
		slog.Warn("interaction_dag: terminal task project lookup failed",
			"task_id", util.UUIDToString(task.ID), "error", err)
	} else if projectID.Valid {
		s.closeSegmentForTerminal(ctx, task, util.UUIDToString(projectID), s.leanEnvSnapshot(ctx, projectID))
	}
	s.RouteTerminalTrainingTask(ctx, task)
	s.maybeCleanupEphemeralSandbox(ctx, task)
}

func (s *TaskService) maybeCleanupEphemeralSandbox(ctx context.Context, task db.AgentInboxEvent) {
	marker, ok := extractEphemeralSandbox(task.Context)
	if !ok {
		return // not an ephemeral rollout
	}
	if marker.CleanupOnTerminal != nil && !*marker.CleanupOnTerminal {
		return // lifecycle is owned by an explicit cleanup endpoint
	}
	if s.EphemeralSandboxManager != nil {
		if err := s.EphemeralSandboxManager.Cleanup(ctx, task); err != nil {
			slog.Warn("ephemeral sandbox cleanup failed",
				"task_id", util.UUIDToString(task.ID),
				"error", err,
			)
		}
		return
	}
	if s.EphemeralSandboxCleaner == nil {
		return
	}

	// Guard: skip if another active task is still on R' (e.g. a retry child
	// that inherited runtime_id via CreateRetryTask). Tearing down while a
	// live task references R' would orphan that task.
	if task.RuntimeID.Valid {
		hasOther, err := s.Queries.HasOtherActiveTaskForRuntime(ctx, db.HasOtherActiveTaskForRuntimeParams{
			RuntimeID:   task.RuntimeID,
			ExcludeTask: task.ID,
		})
		if err != nil {
			slog.Warn("ephemeral sandbox cleanup: check other active tasks failed",
				"task_id", util.UUIDToString(task.ID),
				"runtime_id", util.UUIDToString(task.RuntimeID),
				"error", err,
			)
			return
		}
		if hasOther {
			return // sibling/child still active on R'; skip
		}
	}

	workspaceID := s.ResolveTaskWorkspaceID(ctx, task)
	if workspaceID == "" {
		slog.Warn("ephemeral sandbox cleanup: cannot resolve workspace",
			"task_id", util.UUIDToString(task.ID),
		)
		return
	}

	// 1. Set R' offline immediately so the sweeper doesn't keep the runtime alive
	//    (the sandbox deletion is async, so the daemon may briefly re-register).
	// offline_reason intentionally left unset/NULL here: this path hasn't
	// been researched as a "confirmed" reason family for task ① (agent
	// intentional-stop signal) — not guessing a reason_code for it.
	if task.RuntimeID.Valid {
		if err := s.Queries.SetAgentRuntimeOffline(ctx, db.SetAgentRuntimeOfflineParams{ID: task.RuntimeID}); err != nil {
			slog.Warn("ephemeral sandbox cleanup: set R' offline failed",
				"task_id", util.UUIDToString(task.ID),
				"runtime_id", util.UUIDToString(task.RuntimeID),
				"error", err,
			)
		}
	}

	// 2. Reclaim the Cube sandbox instance (best-effort; the sandboxd delete job
	//    is async — the stale-sweeper + 7d GC handle anything that slips through).
	if err := s.EphemeralSandboxCleaner.DeleteSandboxInstance(ctx, workspaceID, marker.SandboxInstanceID); err != nil {
		slog.Warn("ephemeral sandbox cleanup: delete sandbox instance failed",
			"task_id", util.UUIDToString(task.ID),
			"workspace_id", workspaceID,
			"sandbox_instance_id", marker.SandboxInstanceID,
			"error", err,
		)
	} else {
		slog.Info("ephemeral sandbox cleanup: sandbox reclaimed",
			"task_id", util.UUIDToString(task.ID),
			"sandbox_instance_id", marker.SandboxInstanceID,
		)
	}
}

// HandleFailedTasks runs the post-failure side effects for a batch of
// freshly-failed tasks: optional auto-retry, task:failed event broadcast,
// agent status reconciliation, and (when an issue has no remaining active
// task and isn't being retried) resetting the issue back to todo so the
// daemon can pick it up again.
//
// All callers that surface a task as failed — sweepers, FailTask,
// recover-orphans — funnel through here so the same UI-consistency
// guarantees apply on every code path.
func (s *TaskService) HandleFailedTasks(ctx context.Context, tasks []db.AgentInboxEvent) int {
	if len(tasks) == 0 {
		return 0
	}

	affectedAgents := make(map[string]pgtype.UUID)
	processedIssues := make(map[string]bool)
	retriedIssues := make(map[string]bool)
	retried := 0

	for _, t := range tasks {
		// Auto-retry first so the issue stays in_progress rather than
		// flapping todo → in_progress within a tick.
		if child, _ := s.MaybeRetryFailedTask(ctx, t); child != nil {
			retried++
			if t.IssueID.Valid {
				retriedIssues[util.UUIDToString(t.IssueID)] = true
			}
		}

		failureReason := "agent_error"
		if t.FailureReason.Valid && t.FailureReason.String != "" {
			failureReason = t.FailureReason.String
		}
		s.captureTaskFailed(ctx, t)

		workspaceID := ""
		if t.IssueID.Valid {
			if issue, err := s.Queries.GetIssue(ctx, t.IssueID); err == nil {
				workspaceID = util.UUIDToString(issue.WorkspaceID)
				// Reset stuck in_progress issues only when no other active
				// task exists for the issue and no retry was just enqueued.
				issueKey := util.UUIDToString(t.IssueID)
				if issue.Status == "in_progress" && !processedIssues[issueKey] && !retriedIssues[issueKey] {
					processedIssues[issueKey] = true
					hasActive, checkErr := s.Queries.HasActiveTaskForIssue(ctx, t.IssueID)
					if checkErr != nil {
						slog.Warn("handle failed tasks: active check failed",
							"issue_id", issueKey,
							"error", checkErr,
						)
					} else if !hasActive {
						if _, updateErr := s.Queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
							ID:          t.IssueID,
							Status:      "todo",
							WorkspaceID: issue.WorkspaceID,
						}); updateErr != nil {
							slog.Warn("handle failed tasks: reset stuck issue failed",
								"issue_id", issueKey,
								"error", updateErr,
							)
						}
					}
				}
			}
		}
		if workspaceID == "" {
			workspaceID = s.ResolveTaskWorkspaceID(ctx, t)
		}

		if workspaceID != "" {
			s.Bus.Publish(events.Event{
				Type:        protocol.EventTaskFailed,
				WorkspaceID: workspaceID,
				ActorType:   "system",
				Payload: map[string]any{
					"task_id":        util.UUIDToString(t.ID),
					"agent_id":       util.UUIDToString(t.AgentID),
					"issue_id":       util.UUIDToString(t.IssueID),
					"status":         "failed",
					"failure_reason": failureReason,
				},
			})
		}

		s.maybeCleanupEphemeralSandbox(ctx, t)

		affectedAgents[util.UUIDToString(t.AgentID)] = t.AgentID
	}

	for _, agentID := range affectedAgents {
		s.ReconcileAgentStatus(ctx, agentID)
	}
	return retried
}

// runInTx executes fn inside a single DB transaction. If TxStarter is nil
// (e.g. some tests construct TaskService directly), fn runs against the
// regular Queries handle without transactional guarantees.
func (s *TaskService) runInTx(ctx context.Context, fn func(*db.Queries) error) error {
	return s.runInTxWithTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		return fn(q)
	})
}

// runInTxWithTx is the raw transaction seam for terminal paths that must
// compose service-owned mutations with handler-owned writes before one commit.
func (s *TaskService) runInTxWithTx(ctx context.Context, fn func(*db.Queries, pgx.Tx) error) error {
	if s.TxStarter == nil {
		return fn(s.Queries, nil)
	}
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := fn(s.Queries.WithTx(tx), tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ReportProgress persists the latest task progress before broadcasting it, so
// supervisors and reconnecting clients do not depend on catching an ephemeral
// websocket event.
func (s *TaskService) ReportProgress(ctx context.Context, taskID string, workspaceID string, summary string, step, total int) {
	taskUUID, err := util.ParseUUID(taskID)
	if err != nil {
		slog.Warn("persist task progress skipped: invalid task id", "task_id", taskID, "error", err)
	} else {
		if err := s.Queries.UpsertAgentTaskProgressSnapshot(ctx, db.UpsertAgentTaskProgressSnapshotParams{
			TaskID:  taskUUID,
			Summary: summary,
			Step:    int32(step),
			Total:   int32(total),
		}); err != nil {
			slog.Warn("persist task progress failed", "task_id", taskID, "error", err)
		}
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventTaskProgress,
		WorkspaceID: workspaceID,
		ActorType:   "system",
		ActorID:     "",
		TaskID:      taskID,
		Payload: protocol.TaskProgressPayload{
			TaskID:  taskID,
			Summary: summary,
			Step:    step,
			Total:   total,
		},
	})
}

// ReconcileAgentStatus refreshes agent status from the current active task set.
func (s *TaskService) ReconcileAgentStatus(ctx context.Context, agentID pgtype.UUID) {
	agent, err := s.Queries.RefreshAgentStatusFromTasks(ctx, agentID)
	if err != nil {
		return
	}
	slog.Debug("agent status reconciled", "agent_id", util.UUIDToString(agentID), "status", agent.Status)
	s.publishAgentStatus(agent)
}

func (s *TaskService) updateAgentStatus(ctx context.Context, agentID pgtype.UUID, status string) {
	agent, err := s.Queries.UpdateAgentStatus(ctx, db.UpdateAgentStatusParams{
		ID:     agentID,
		Status: status,
	})
	if err != nil {
		return
	}
	s.publishAgentStatus(agent)
}

func (s *TaskService) publishAgentStatus(agent db.Agent) {
	s.Bus.Publish(events.Event{
		Type:        protocol.EventAgentStatus,
		WorkspaceID: util.UUIDToString(agent.WorkspaceID),
		ActorType:   "system",
		ActorID:     "",
		Payload:     map[string]any{"agent": agentToMap(agent)},
	})
}

// LoadAgentSkills loads an agent's skills with their files for non-execution views.
func (s *TaskService) LoadAgentSkills(ctx context.Context, agentID pgtype.UUID) []AgentSkillData {
	return s.loadAgentSkills(ctx, agentID, pgtype.UUID{}, pgtype.UUID{})
}

// LoadAgentSkillsForTask records the exact evolution version injected into a
// production task. Recording is best-effort so feedback telemetry cannot block a claim.
func (s *TaskService) LoadAgentSkillsForTask(ctx context.Context, agentID, taskID pgtype.UUID) []AgentSkillData {
	return s.loadAgentSkills(ctx, agentID, taskID, taskID)
}

// LoadAgentSkillsForInbox records inbox feedback under the event as a stable
// execution identifier without violating feedback.task_id's task-queue foreign key.
func (s *TaskService) LoadAgentSkillsForInbox(ctx context.Context, agentID, inboxEventID pgtype.UUID) []AgentSkillData {
	return s.loadAgentSkills(ctx, agentID, pgtype.UUID{}, inboxEventID)
}

// AgentMemoryData is the bounded memory snapshot delivered with one execution.
// User-scoped memories are filtered against the attested task initiator before
// they leave the server; the daemon never receives another member's private
// preferences for the current run.
type AgentMemoryData struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Content     string `json:"content"`
	Scope       string `json:"scope"`
	SubjectType string `json:"subject_type,omitempty"`
	SubjectID   string `json:"subject_id,omitempty"`
	SyncKey     string `json:"sync_key"`
	ContentHash string `json:"content_hash,omitempty"`
}

type agentMemoryDeliveryConfig struct {
	Scope   string `json:"scope"`
	Subject struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	} `json:"subject"`
	Applies memoryApplicability `json:"applies"`
}

type memoryApplicability struct {
	ProjectIDs []string `json:"project_ids"`
	ProjectID  string   `json:"project_id"`
	ChannelIDs []string `json:"channel_ids"`
	ChannelID  string   `json:"channel_id"`
	TaskTypes  []string `json:"task_types"`
	ExpiresAt  string   `json:"expires_at"`
}

type MemoryExecutionScope struct {
	InitiatorType     string
	InitiatorID       string
	ProjectID         string
	ChannelID         string
	ChannelKind       string
	ChatSessionID     string
	IssueID           string
	IncludeUserMemory *bool // nil = compute from ChannelKind/ChatSessionID + message texts
	MessageTexts      []string
	TaskType          string
	Now               time.Time
}

// LoadAgentMemoriesForExecution returns only memories that apply to the
// current execution. Legacy rows without delivery metadata remain agent-local.
//
// LRM-1000: workspace wiki / team_knowledge pages are injected as task-related
// seeds + ≤2 hop neighborhood only — never a full active dump (KV-cache friendly).
func (s *TaskService) LoadAgentMemoriesForExecution(ctx context.Context, agentID, workspaceID pgtype.UUID, execution MemoryExecutionScope) []AgentMemoryData {
	memories, err := s.Queries.ListAgentMemoriesByAgent(ctx, agentID)
	if err != nil {
		memories = nil
	}
	result := make([]AgentMemoryData, 0, len(memories))
	for _, memory := range memories {
		scope, subjectType, subjectID, applies := agentMemoryDeliveryForExecution(memory.Config, execution)
		if !applies {
			continue
		}
		result = append(result, AgentMemoryData{
			ID:          util.UUIDToString(memory.ID),
			Name:        memory.Name,
			Content:     memory.Content,
			Scope:       scope,
			SubjectType: subjectType,
			SubjectID:   subjectID,
			SyncKey:     memory.SyncKey,
			ContentHash: memory.ContentHash,
		})
	}
	// Prefer gated wiki neighborhood. Legacy ListActiveTeamKnowledgeForExecution
	// dump is intentionally not used on the wake path.
	wikiPages := s.LoadTaskRelatedKnowledgeNeighborhood(ctx, workspaceID, execution, knowledgeWikiMaxHops)
	if len(wikiPages) > 0 {
		result = append(result, wikiPages...)
	}
	return result
}

func teamKnowledgeMemoryData(item db.ListActiveTeamKnowledgeForExecutionRow) AgentMemoryData {
	id := util.UUIDToString(item.ID)
	return AgentMemoryData{
		ID:      id,
		Name:    "Team knowledge · " + strings.TrimSpace(item.Title),
		Content: item.Content,
		Scope:   "workspace",
		SyncKey: "team_knowledge:" + id,
	}
}

func agentMemoryDeliveryForExecution(config []byte, execution MemoryExecutionScope) (string, string, string, bool) {
	cfg := agentMemoryDeliveryConfig{}
	_ = json.Unmarshal(config, &cfg)
	scope := strings.ToLower(strings.TrimSpace(cfg.Scope))
	if scope == "" {
		scope = "agent"
	}
	subjectType := strings.ToLower(strings.TrimSpace(cfg.Subject.Type))
	subjectID := strings.TrimSpace(cfg.Subject.ID)
	if scope == "project" && strings.TrimSpace(execution.ProjectID) == "" {
		return scope, subjectType, subjectID, false
	}
	if scope == "user" || scope == "member" {
		if !executionIncludesUserMemory(execution) {
			return scope, subjectType, subjectID, false
		}
		matchesMember := strings.EqualFold(strings.TrimSpace(execution.InitiatorType), "member") &&
			strings.TrimSpace(execution.InitiatorID) != "" && subjectType == "member" && subjectID == strings.TrimSpace(execution.InitiatorID)
		return scope, subjectType, subjectID, matchesMember && memoryApplicabilityMatches(cfg.Applies, execution)
	}
	return scope, subjectType, subjectID, memoryApplicabilityMatches(cfg.Applies, execution)
}

func executionIncludesUserMemory(execution MemoryExecutionScope) bool {
	if execution.IncludeUserMemory != nil {
		return *execution.IncludeUserMemory
	}
	return memoryscope.IncludeUserMemory(execution.ChannelKind, execution.ChatSessionID, execution.MessageTexts...)
}

func teamKnowledgeAppliesForExecution(metadata []byte, execution MemoryExecutionScope) bool {
	container := struct {
		Applies memoryApplicability `json:"applies"`
	}{}
	if json.Unmarshal(metadata, &container) != nil {
		return true
	}
	return memoryApplicabilityMatches(container.Applies, execution)
}

func memoryApplicabilityMatches(applies memoryApplicability, execution MemoryExecutionScope) bool {
	projectIDs := appendCleanApplicabilityValue(applies.ProjectIDs, applies.ProjectID)
	channelIDs := appendCleanApplicabilityValue(applies.ChannelIDs, applies.ChannelID)
	if !applicabilityListMatches(projectIDs, execution.ProjectID) || !applicabilityListMatches(channelIDs, execution.ChannelID) || !applicabilityListMatches(applies.TaskTypes, execution.TaskType) {
		return false
	}
	if expiresAt := strings.TrimSpace(applies.ExpiresAt); expiresAt != "" {
		expires, err := time.Parse(time.RFC3339, expiresAt)
		if err != nil {
			return false
		}
		now := execution.Now
		if now.IsZero() {
			now = time.Now()
		}
		if !now.Before(expires) {
			return false
		}
	}
	return true
}

func appendCleanApplicabilityValue(values []string, value string) []string {
	if strings.TrimSpace(value) == "" {
		return values
	}
	return append(append([]string(nil), values...), strings.TrimSpace(value))
}

func applicabilityListMatches(values []string, current string) bool {
	if len(values) == 0 {
		return true
	}
	current = strings.TrimSpace(current)
	if current == "" {
		return false
	}
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), current) {
			return true
		}
	}
	return false
}

func (s *TaskService) loadAgentSkills(ctx context.Context, agentID, taskID, executionID pgtype.UUID) []AgentSkillData {
	skills, err := s.Queries.ListAgentSkills(ctx, agentID)
	if err != nil || len(skills) == 0 {
		return nil
	}

	result := make([]AgentSkillData, 0, len(skills))
	for _, sk := range skills {
		data := AgentSkillData{
			ID:          util.UUIDToString(sk.ID),
			Name:        sk.Name,
			Description: sk.Description,
			Content:     sk.Content,
		}
		files, _ := s.Queries.ListSkillFiles(ctx, sk.ID)
		for _, f := range files {
			data.Files = append(data.Files, AgentSkillFileData{Path: f.Path, Content: f.Content})
		}
		result = append(result, data)
		if sk.SourceEvolutionUnitID.Valid && sk.SourceEvolutionUnitVersionID.Valid && executionID.Valid {
			if err := s.Queries.RecordEvolutionSkillInjection(ctx, db.RecordEvolutionSkillInjectionParams{
				WorkspaceID: sk.WorkspaceID,
				AgentID:     agentID,
				TaskID:      taskID,
				UnitID:      sk.SourceEvolutionUnitID,
				VersionID:   sk.SourceEvolutionUnitVersionID,
				ExecutionID: executionID,
			}); err != nil {
				slog.Warn("record evolution skill injection failed", "task_id", util.UUIDToString(taskID), "unit_id", util.UUIDToString(sk.SourceEvolutionUnitID), "error", err)
			}
		}
	}
	return result
}

func (s *TaskService) recordEvolutionSkillOutcome(ctx context.Context, taskID pgtype.UUID, event, outcome string) {
	s.RecordEvolutionSkillOutcome(ctx, taskID, event, outcome)
}

// RecordEvolutionSkillOutcome attributes an execution result to every
// evolution-backed skill version captured when that execution was dispatched.
func (s *TaskService) RecordEvolutionSkillOutcome(ctx context.Context, executionID pgtype.UUID, event, outcome string) {
	if !executionID.Valid {
		return
	}
	if err := s.Queries.RecordEvolutionSkillOutcome(ctx, db.RecordEvolutionSkillOutcomeParams{
		Event: event, Outcome: outcome, ExecutionID: executionID,
	}); err != nil {
		slog.Warn("record evolution skill outcome failed", "execution_id", util.UUIDToString(executionID), "event", event, "error", err)
	}
}

// RecordMemoryInjections writes claim-time feedback that each delivered memory
// was retrieved into this execution (LRM-984). Fail-soft.
func (s *TaskService) RecordMemoryInjections(ctx context.Context, workspaceID, agentID, executionID pgtype.UUID, memories []AgentMemoryData) {
	if s == nil || s.Queries == nil || !executionID.Valid || len(memories) == 0 {
		return
	}
	for _, memory := range memories {
		unitID, err := util.ParseUUID(memory.ID)
		if err != nil || !unitID.Valid {
			continue
		}
		if err := s.Queries.RecordEvolutionMemoryInjection(ctx, db.RecordEvolutionMemoryInjectionParams{
			WorkspaceID: workspaceID,
			AgentID:     agentID,
			TaskID:      pgtype.UUID{},
			UnitID:      unitID,
			LocalUnitID: strings.TrimSpace(memory.SyncKey),
			ExecutionID: executionID,
			SyncKey:     strings.TrimSpace(memory.SyncKey),
			Scope:       strings.TrimSpace(memory.Scope),
		}); err != nil {
			slog.Warn("record evolution memory injection failed",
				"execution_id", util.UUIDToString(executionID),
				"memory_id", memory.ID,
				"error", err,
			)
		}
	}
}

// RecordEvolutionUnitUsed marks every memory/skill injected for this execution
// as used when the run completes successfully (LRM-984). Fail-soft.
func (s *TaskService) RecordEvolutionUnitUsed(ctx context.Context, executionID pgtype.UUID) {
	if s == nil || s.Queries == nil || !executionID.Valid {
		return
	}
	if err := s.Queries.RecordEvolutionUnitUsed(ctx, executionID); err != nil {
		slog.Warn("record evolution unit used failed",
			"execution_id", util.UUIDToString(executionID),
			"error", err,
		)
	}
}

// AgentSkillData represents a skill for task execution responses.
type AgentSkillData struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Description string               `json:"description,omitempty"`
	Content     string               `json:"content"`
	Files       []AgentSkillFileData `json:"files,omitempty"`
}

// AgentSkillFileData represents a supporting file within a skill.
type AgentSkillFileData struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// computeChatElapsedMs returns the wall-clock duration from task creation
// (user hit send) to terminal state (completed/failed). Stored on the
// assistant chat_message so the UI can render "Replied in 38s" /
// "Failed after 12s". Uses created_at — not started_at — because users
// experience total wait time, including queue + dispatch, not just the
// daemon's actual run time.
func computeChatElapsedMs(task db.AgentInboxEvent) pgtype.Int8 {
	if !task.CompletedAt.Valid || !task.CreatedAt.Valid {
		return pgtype.Int8{}
	}
	ms := task.CompletedAt.Time.Sub(task.CreatedAt.Time).Milliseconds()
	if ms < 0 {
		ms = 0
	}
	return pgtype.Int8{Int64: ms, Valid: true}
}

func priorityToInt(p string) int32 {
	switch p {
	case "urgent":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

// NotifyTaskEnqueued is the cross-package shim for callers outside
// TaskService (e.g. AutopilotService.dispatchRunOnly) that insert a
// row into agent_inbox_event directly and need to wake the daemon.
func (s *TaskService) NotifyTaskEnqueued(ctx context.Context, task db.AgentInboxEvent) {
	s.captureTaskQueued(ctx, task)
	s.notifyTaskAvailable(task)
}

// notifyTaskAvailable wakes the daemon after a canonical inbox event is
// inserted so it does not have to wait for its next poll.
func (s *TaskService) notifyTaskAvailable(task db.AgentInboxEvent) {
	if !task.RuntimeID.Valid {
		return
	}
	runtimeKey := util.UUIDToString(task.RuntimeID)
	if s.Wakeup == nil {
		return
	}
	s.Wakeup.NotifyTaskAvailable(runtimeKey, util.UUIDToString(task.ID))
}

func (s *TaskService) broadcastTaskDispatch(ctx context.Context, task db.AgentInboxEvent) {
	var payload map[string]any
	if task.Context != nil {
		json.Unmarshal(task.Context, &payload)
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["task_id"] = util.UUIDToString(task.ID)
	payload["runtime_id"] = util.UUIDToString(task.RuntimeID)
	payload["issue_id"] = util.UUIDToString(task.IssueID)
	payload["agent_id"] = util.UUIDToString(task.AgentID)
	// chat_session_id is the routing key the chat window uses to writethrough
	// `chatKeys.pendingTask` to status="running" the moment the daemon claims
	// the task. Without it the pill stays stuck at "Queued" until completion.
	if task.ChatSessionID.Valid {
		payload["chat_session_id"] = util.UUIDToString(task.ChatSessionID)
	}

	workspaceID := s.ResolveTaskWorkspaceID(ctx, task)
	if workspaceID == "" {
		return
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventTaskDispatch,
		WorkspaceID: workspaceID,
		ActorType:   "system",
		ActorID:     "",
		Payload:     payload,
	})
}

func (s *TaskService) broadcastTaskEvent(ctx context.Context, eventType string, task db.AgentInboxEvent) {
	workspaceID := s.ResolveTaskWorkspaceID(ctx, task)
	if workspaceID == "" {
		return
	}
	payload := map[string]any{
		"task_id":  util.UUIDToString(task.ID),
		"agent_id": util.UUIDToString(task.AgentID),
		"issue_id": util.UUIDToString(task.IssueID),
		"status":   task.Status,
	}
	if task.ChatSessionID.Valid {
		payload["chat_session_id"] = util.UUIDToString(task.ChatSessionID)
	}
	event := events.Event{
		Type:        eventType,
		WorkspaceID: workspaceID,
		ActorType:   "system",
		ActorID:     "",
		Payload:     payload,
	}
	if eventType == protocol.EventTaskCancelled {
		event.TaskID = util.UUIDToString(task.ID)
	}
	s.Bus.Publish(event)
}

// ResolveTaskWorkspaceID determines the workspace ID for a task.
// For issue tasks, it comes from the issue. For chat tasks, from the chat session.
// Legacy autopilot_run_id tasks use agent_inbox_event.workspace_id (tables dropped LRM-1051).
// Returns "" when none of the links resolve — callers treat that as "not found".
func (s *TaskService) ResolveTaskWorkspaceID(ctx context.Context, task db.AgentInboxEvent) string {
	canonicalWorkspaceID := util.UUIDToString(task.WorkspaceID)
	if task.IssueID.Valid {
		if issue, err := s.Queries.GetIssue(ctx, task.IssueID); err == nil {
			sourceWorkspaceID := util.UUIDToString(issue.WorkspaceID)
			if canonicalWorkspaceID != "" && sourceWorkspaceID != canonicalWorkspaceID {
				return ""
			}
			return sourceWorkspaceID
		}
	}
	if task.ChatSessionID.Valid {
		if cs, err := s.Queries.GetChatSession(ctx, task.ChatSessionID); err == nil {
			sourceWorkspaceID := util.UUIDToString(cs.WorkspaceID)
			if canonicalWorkspaceID != "" && sourceWorkspaceID != canonicalWorkspaceID {
				return ""
			}
			return sourceWorkspaceID
		}
	}
	// Autopilot tables dropped (LRM-1051); do not resolve workspace via run.
	// Quick-create tasks have no issue / chat / autopilot link — workspace
	// lives in the context JSONB. Returning "" here is what blocked
	// requireDaemonTaskAccess (404 on /start, /progress, /complete, /fail
	// for the daemon) and silently dropped task:dispatch / task:completed
	// broadcasts, which is why quick-create tasks appeared stuck queued.
	if qc, ok := s.parseQuickCreateContext(task); ok {
		if canonicalWorkspaceID != "" && qc.WorkspaceID != canonicalWorkspaceID {
			return ""
		}
		return qc.WorkspaceID
	}
	return canonicalWorkspaceID
}

func (s *TaskService) broadcastChatDone(ctx context.Context, task db.AgentInboxEvent, msg *db.ChatMessage, outputType, target string, content string, parts []protocol.MessagePart, reaction *protocol.ChatReactionPayload, outputSuppressedReason string) {
	workspaceID := s.ResolveTaskWorkspaceID(ctx, task)
	if workspaceID == "" {
		return
	}
	if outputType == "" {
		outputType = protocol.ChatOutputKindNoReply
	}
	payload := protocol.ChatDonePayload{
		ChatSessionID:          util.UUIDToString(task.ChatSessionID),
		TaskID:                 util.UUIDToString(task.ID),
		Type:                   outputType,
		Target:                 strings.TrimSpace(target),
		Reaction:               reaction,
		OutputSuppressedReason: outputSuppressedReason,
	}
	if msg != nil {
		payload.Type = protocol.ChatOutputKindMessage
		payload.Reaction = nil
		payload.MessageID = util.UUIDToString(msg.ID)
		payload.Content = msg.Content
		payload.Parts = messageparts.Decode(msg.Parts)
		if msg.CreatedAt.Valid {
			payload.CreatedAt = msg.CreatedAt.Time.UTC().Format(time.RFC3339Nano)
		}
		if msg.ElapsedMs.Valid {
			payload.ElapsedMs = msg.ElapsedMs.Int64
		}
	} else if outputType == protocol.ChatOutputKindMessage {
		payload.Content = content
		payload.Parts = parts
	}
	recipientUserIDs := []string{}
	if s.Queries != nil && task.ChatSessionID.Valid {
		if session, err := s.Queries.GetChatSession(ctx, task.ChatSessionID); err == nil {
			recipientUserIDs = []string{util.UUIDToString(session.CreatorID)}
		} else {
			slog.Warn("chat done: resolve chat session creator failed", "chat_session_id", util.UUIDToString(task.ChatSessionID), "error", err)
		}
	}
	s.Bus.Publish(events.Event{
		Type:             protocol.EventChatDone,
		WorkspaceID:      workspaceID,
		ActorType:        "system",
		ActorID:          "",
		ChatSessionID:    util.UUIDToString(task.ChatSessionID),
		RecipientUserIDs: recipientUserIDs,
		Payload:          payload,
	})
}

func (s *TaskService) broadcastIssueUpdated(issue db.Issue) {
	prefix := s.getIssuePrefix(issue.WorkspaceID)
	s.Bus.Publish(events.Event{
		Type:        protocol.EventIssueUpdated,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "system",
		ActorID:     "",
		Payload:     map[string]any{"issue": issueToMap(issue, prefix)},
	})
}

func (s *TaskService) getIssuePrefix(workspaceID pgtype.UUID) string {
	ws, err := s.Queries.GetWorkspace(context.Background(), workspaceID)
	if err != nil {
		return ""
	}
	return ws.IssuePrefix
}

func (s *TaskService) createAgentComment(ctx context.Context, issueID, agentID pgtype.UUID, content, commentType string, parentID pgtype.UUID) {
	if content == "" {
		return
	}
	// Look up issue to get workspace ID for mention expansion and broadcasting.
	issue, err := s.Queries.GetIssue(ctx, issueID)
	if err != nil {
		return
	}
	// Resolve the thread root for thread-level side effects without overwriting
	// parentID. The stored parent_id must remain the exact comment being replied
	// to; recursive thread reads recover the root when needed.
	var rootComment *db.Comment
	if parentID.Valid {
		if root, err := s.Queries.GetThreadRoot(ctx, db.GetThreadRootParams{
			CommentID:   parentID,
			WorkspaceID: issue.WorkspaceID,
		}); err == nil {
			rootComment = &root
		}
	}
	// Expand bare issue identifiers (e.g. MUL-117) into mention links.
	content = mention.ExpandIssueIdentifiers(ctx, s.Queries, issue.WorkspaceID, content)
	comment, err := s.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issueID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "agent",
		AuthorID:    agentID,
		Content:     content,
		Type:        commentType,
		ParentID:    parentID,
	})
	if err != nil {
		return
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventCommentCreated,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "agent",
		ActorID:     util.UUIDToString(agentID),
		Payload: map[string]any{
			"comment": map[string]any{
				"id":          util.UUIDToString(comment.ID),
				"issue_id":    util.UUIDToString(comment.IssueID),
				"author_type": comment.AuthorType,
				"author_id":   util.UUIDToString(comment.AuthorID),
				"content":     comment.Content,
				"type":        comment.Type,
				"parent_id":   util.UUIDToPtr(comment.ParentID),
				"created_at":  comment.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
			},
			"issue_title":  issue.Title,
			"issue_status": issue.Status,
		},
	})
	s.AutoUnresolveThreadOnReply(ctx, rootComment, util.UUIDToString(issue.WorkspaceID), "agent", util.UUIDToString(agentID))
}

// AutoUnresolveThreadOnReply clears resolved_at on the thread root when a
// reply lands in a resolved thread, and broadcasts comment:unresolved. Shared
// between the user-facing Handler.CreateComment path and the agent-facing
// TaskService.createAgentComment path so the resolved-then-replied state can
// never desync (one of the bugs Emacs flagged on PR #2300). Errors are logged
// — the reply itself already committed, the desync is recoverable on next read.
func (s *TaskService) AutoUnresolveThreadOnReply(ctx context.Context, parent *db.Comment, workspaceID, actorType, actorID string) {
	if parent == nil || !parent.ResolvedAt.Valid {
		return
	}
	updated, err := s.Queries.UnresolveComment(ctx, parent.ID)
	if err != nil {
		slog.Warn("auto-unresolve on reply failed", "error", err, "comment_id", util.UUIDToString(parent.ID))
		return
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventCommentUnresolved,
		WorkspaceID: workspaceID,
		ActorType:   actorType,
		ActorID:     actorID,
		Payload: map[string]any{
			"comment": map[string]any{
				"id":               util.UUIDToString(updated.ID),
				"issue_id":         util.UUIDToString(updated.IssueID),
				"author_type":      updated.AuthorType,
				"author_id":        util.UUIDToString(updated.AuthorID),
				"content":          updated.Content,
				"type":             updated.Type,
				"parent_id":        util.UUIDToPtr(updated.ParentID),
				"created_at":       util.TimestampToString(updated.CreatedAt),
				"updated_at":       util.TimestampToString(updated.UpdatedAt),
				"resolved_at":      util.TimestampToPtr(updated.ResolvedAt),
				"resolved_by_type": util.TextToPtr(updated.ResolvedByType),
				"resolved_by_id":   util.UUIDToPtr(updated.ResolvedByID),
			},
		},
	})
}

func issueToMap(issue db.Issue, issuePrefix string) map[string]any {
	return map[string]any{
		"id":              util.UUIDToString(issue.ID),
		"workspace_id":    util.UUIDToString(issue.WorkspaceID),
		"number":          issue.Number,
		"identifier":      issuePrefix + "-" + strconv.Itoa(int(issue.Number)),
		"title":           issue.Title,
		"description":     util.TextToPtr(issue.Description),
		"status":          issue.Status,
		"priority":        issue.Priority,
		"assignee_type":   util.TextToPtr(issue.AssigneeType),
		"assignee_id":     util.UUIDToPtr(issue.AssigneeID),
		"creator_type":    issue.CreatorType,
		"creator_id":      util.UUIDToString(issue.CreatorID),
		"parent_issue_id": util.UUIDToPtr(issue.ParentIssueID),
		"position":        issue.Position,
		"start_date":      util.DateToPtr(issue.StartDate),
		"due_date":        util.DateToPtr(issue.DueDate),
		"created_at":      util.TimestampToString(issue.CreatedAt),
		"updated_at":      util.TimestampToString(issue.UpdatedAt),
	}
}

// parseQuickCreateContext returns the quick-create payload if the task's
// context JSONB contains type == "quick_create"; otherwise the bool is
// false so callers can short-circuit. Tasks linked to an issue / chat /
// autopilot are never quick-create even if they happen to carry a
// context blob, so those are filtered up front.
func (s *TaskService) parseQuickCreateContext(task db.AgentInboxEvent) (QuickCreateContext, bool) {
	if task.IssueID.Valid || task.ChatSessionID.Valid || task.AutopilotRunID.Valid {
		return QuickCreateContext{}, false
	}
	if len(task.Context) == 0 {
		return QuickCreateContext{}, false
	}
	var qc QuickCreateContext
	if err := json.Unmarshal(task.Context, &qc); err != nil {
		return QuickCreateContext{}, false
	}
	if qc.Type != QuickCreateContextType {
		return QuickCreateContext{}, false
	}
	return qc, true
}

// notifyQuickCreateCompleted writes a success inbox notification to the
// requester pointing at the issue the agent just created. The issue is
// stamped with origin_type=quick_create + origin_id=<task_id> by the
// daemon-injected MULTICA_QUICK_CREATE_TASK_ID env var, so this lookup is
// deterministic — robust against the same agent creating other issues in
// parallel (e.g. assignment task running while max_concurrent_tasks > 1
// permits another quick-create alongside it).
func (s *TaskService) notifyQuickCreateCompleted(ctx context.Context, task db.AgentInboxEvent, qc QuickCreateContext) {
	requesterID, err := util.ParseUUID(qc.RequesterID)
	if err != nil {
		slog.Warn("quick-create completion: invalid requester id", "task_id", util.UUIDToString(task.ID), "error", err)
		return
	}
	workspaceID, err := util.ParseUUID(qc.WorkspaceID)
	if err != nil {
		slog.Warn("quick-create completion: invalid workspace id", "task_id", util.UUIDToString(task.ID), "error", err)
		return
	}
	issue, err := s.Queries.GetIssueByOrigin(ctx, db.GetIssueByOriginParams{
		WorkspaceID: workspaceID,
		OriginType:  pgtype.Text{String: "quick_create", Valid: true},
		OriginID:    task.ID,
	})
	if err != nil {
		// No issue created — agent ran to completion but the CLI call must
		// have failed. Surface as a failure inbox so the user sees something.
		slog.Warn("quick-create completion: no issue found, writing failure inbox",
			"task_id", util.UUIDToString(task.ID),
			"agent_id", util.UUIDToString(task.AgentID),
			"workspace_id", qc.WorkspaceID,
		)
		s.notifyQuickCreateFailed(ctx, task, qc, "agent finished without creating an issue")
		return
	}

	// Link the new issue back to this task so subsequent reads of the task
	// (Activity tab, Recent work, etc.) render it as a normal issue task
	// (kind = "direct") instead of staying on the "Creating issue" active-
	// wording label. Best-effort: a write failure here doesn't block the
	// inbox notification, which is the more important signal to the user.
	if err := s.Queries.LinkTaskToIssue(ctx, db.LinkTaskToIssueParams{
		ID:      task.ID,
		IssueID: issue.ID,
	}); err != nil {
		slog.Warn("quick-create completion: link task→issue failed",
			"task_id", util.UUIDToString(task.ID),
			"issue_id", util.UUIDToString(issue.ID),
			"error", err,
		)
	}

	// Subscribe the requester so they receive notifications for follow-up
	// comments and updates. The DB row's creator_type/creator_id is the
	// agent (it ran the CLI), but the human who triggered the quick-create
	// is the semantic creator from a UX perspective — without this they
	// only see the one-shot completion inbox and miss everything after.
	// Best-effort: log on failure but don't block the inbox notification.
	if err := s.Queries.AddIssueSubscriber(ctx, db.AddIssueSubscriberParams{
		IssueID:  issue.ID,
		UserType: "member",
		UserID:   requesterID,
		Reason:   "creator",
	}); err != nil {
		slog.Warn("quick-create completion: subscribe requester failed",
			"task_id", util.UUIDToString(task.ID),
			"issue_id", util.UUIDToString(issue.ID),
			"requester_id", qc.RequesterID,
			"error", err,
		)
	} else {
		s.Bus.Publish(events.Event{
			Type:        protocol.EventSubscriberAdded,
			WorkspaceID: qc.WorkspaceID,
			ActorType:   "agent",
			ActorID:     util.UUIDToString(task.AgentID),
			Payload: map[string]any{
				"issue_id":  util.UUIDToString(issue.ID),
				"user_type": "member",
				"user_id":   qc.RequesterID,
				"reason":    "creator",
			},
		})
	}
	prefix := s.getIssuePrefix(workspaceID)
	identifier := fmt.Sprintf("%s-%d", prefix, issue.Number)
	s.handleQuickCreateSourceReturn(ctx, task, qc, issue, identifier)
	details, _ := json.Marshal(map[string]any{
		"task_id":         util.UUIDToString(task.ID),
		"agent_id":        util.UUIDToString(task.AgentID),
		"issue_id":        util.UUIDToString(issue.ID),
		"identifier":      identifier,
		"original_prompt": qc.Prompt,
	})
	item, err := s.Queries.CreateInboxItem(ctx, db.CreateInboxItemParams{
		WorkspaceID:   workspaceID,
		RecipientType: "member",
		RecipientID:   requesterID,
		Type:          "quick_create_done",
		Severity:      "info",
		IssueID:       issue.ID,
		Title:         issue.Title,
		Body:          pgtype.Text{},
		ActorType:     pgtype.Text{String: "agent", Valid: true},
		ActorID:       task.AgentID,
		Details:       details,
	})
	if err != nil {
		slog.Error("quick-create completion: inbox write failed", "task_id", util.UUIDToString(task.ID), "error", err)
		return
	}
	s.publishQuickCreateInbox(item, qc.WorkspaceID, util.UUIDToString(task.AgentID), issue.Status)
}

// notifyQuickCreateFailed writes a failure inbox notification carrying the
// original prompt + agent ID so the frontend can render an "Edit as
// advanced form" entry that pre-fills the legacy create-issue modal
// without asking the user to retype.
func (s *TaskService) notifyQuickCreateFailed(ctx context.Context, task db.AgentInboxEvent, qc QuickCreateContext, errMsg string) {
	requesterID, err := util.ParseUUID(qc.RequesterID)
	if err != nil {
		return
	}
	workspaceID, err := util.ParseUUID(qc.WorkspaceID)
	if err != nil {
		return
	}
	if errMsg == "" {
		errMsg = "Quick create did not finish successfully"
	}
	details, _ := json.Marshal(map[string]any{
		"task_id":         util.UUIDToString(task.ID),
		"agent_id":        util.UUIDToString(task.AgentID),
		"original_prompt": qc.Prompt,
		"error":           redact.Text(errMsg),
	})
	item, err := s.Queries.CreateInboxItem(ctx, db.CreateInboxItemParams{
		WorkspaceID:   workspaceID,
		RecipientType: "member",
		RecipientID:   requesterID,
		Type:          "quick_create_failed",
		Severity:      "action_required",
		IssueID:       pgtype.UUID{},
		Title:         "Quick create failed",
		Body:          pgtype.Text{String: redact.Text(errMsg), Valid: true},
		ActorType:     pgtype.Text{String: "agent", Valid: true},
		ActorID:       task.AgentID,
		Details:       details,
	})
	if err != nil {
		slog.Error("quick-create failure: inbox write failed", "task_id", util.UUIDToString(task.ID), "error", err)
		return
	}
	s.publishQuickCreateInbox(item, qc.WorkspaceID, util.UUIDToString(task.AgentID), "")
}

// publishQuickCreateInbox emits the WS event so the requester's inbox list
// updates immediately. Mirrors the payload shape used by the other inbox
// listeners (notification_listeners.go).
func (s *TaskService) publishQuickCreateInbox(item db.InboxItem, workspaceID, agentID, issueStatus string) {
	resp := map[string]any{
		"id":             util.UUIDToString(item.ID),
		"workspace_id":   util.UUIDToString(item.WorkspaceID),
		"recipient_type": item.RecipientType,
		"recipient_id":   util.UUIDToString(item.RecipientID),
		"type":           item.Type,
		"severity":       item.Severity,
		"issue_id":       util.UUIDToPtr(item.IssueID),
		"title":          item.Title,
		"body":           util.TextToPtr(item.Body),
		"read":           item.Read,
		"archived":       item.Archived,
		"created_at":     util.TimestampToString(item.CreatedAt),
		"actor_type":     util.TextToPtr(item.ActorType),
		"actor_id":       util.UUIDToPtr(item.ActorID),
		"details":        json.RawMessage(item.Details),
		"issue_status":   issueStatus,
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventInboxNew,
		WorkspaceID: workspaceID,
		ActorType:   "agent",
		ActorID:     agentID,
		Payload:     map[string]any{"item": resp},
	})
}

// agentToMap builds a simple map for broadcasting agent status updates.
func agentToMap(a db.Agent) map[string]any {
	var rc any
	if a.RuntimeConfig != nil {
		json.Unmarshal(a.RuntimeConfig, &rc)
	}
	return map[string]any{
		"id":                   util.UUIDToString(a.ID),
		"workspace_id":         util.UUIDToString(a.WorkspaceID),
		"runtime_id":           util.UUIDToString(a.RuntimeID),
		"name":                 a.Name,
		"description":          a.Description,
		"avatar_url":           util.TextToPtr(a.AvatarUrl),
		"runtime_mode":         a.RuntimeMode,
		"runtime_config":       rc,
		"status":               a.Status,
		"max_concurrent_tasks": a.MaxConcurrentTasks,
		"owner_id":             util.UUIDToPtr(a.OwnerID),
		"skills":               []any{},
		"created_at":           util.TimestampToString(a.CreatedAt),
		"updated_at":           util.TimestampToString(a.UpdatedAt),
		"archived_at":          util.TimestampToPtr(a.ArchivedAt),
		"archived_by":          util.UUIDToPtr(a.ArchivedBy),
	}
}
