package handler

// LRM-1507 — Real-time Agent work projection + event timeline.
//
// This is a *derived* read projection. It never writes its own state table;
// the research-run ledger (researchrun.RunSnapshot) stays the single source of
// truth for tasks, attempts, sources, observations and claims. The timeline is
// rebuilt deterministically from that ledger, so it is stable across refreshes
// and is naturally scoped to one session+workspace (no cross-workspace leakage).

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/researchrun"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// WorkTimelineEvent is one canonical lifecycle event for a research task.
// Kind uses the LRM-1507 vocabulary so clients can pivot the timeline UI.
type WorkTimelineEvent struct {
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	AgentID   string `json:"agent_id,omitempty"`
	Attempt   *int   `json:"attempt,omitempty"`
	Timestamp string `json:"timestamp"`
	UnixMs    int64  `json:"unix_ms"`
	Detail    string `json:"detail,omitempty"`
}

// WorkProjectionEntry is the per-task projection returned to the UI.
type WorkProjectionEntry struct {
	TaskID          string              `json:"task_id"`
	Kind            string              `json:"kind"`
	Objective       string              `json:"objective"`
	Scope           string              `json:"scope,omitempty"`
	ExpectedOutput  string              `json:"expected_output,omitempty"`
	Round           int                 `json:"round"`
	AssignedAgentID string              `json:"assigned_agent_id,omitempty"`
	Stage           string              `json:"stage"`
	Action          string              `json:"action,omitempty"`
	Status          string              `json:"status"`
	StepsDone       int                 `json:"steps_done"`
	StepsTotal      int                 `json:"steps_total"`
	ProgressPercent *int                `json:"progress_percent,omitempty"`
	LatestFinding   string              `json:"latest_finding,omitempty"`
	EvidenceCount   int                 `json:"evidence_count"`
	BlockedReason   string              `json:"blocked_reason,omitempty"`
	FailureReason   string              `json:"failure_reason,omitempty"`
	NextStep        string              `json:"next_step,omitempty"`
	Timeline        []WorkTimelineEvent `json:"timeline"`
}

type workProjectionResponse struct {
	SessionID string                `json:"session_id"`
	Stage     string                `json:"stage"`
	RunStatus string                `json:"run_status"`
	Entries   []WorkProjectionEntry `json:"entries"`
}

var workProjectionStageRole = map[string]string{
	"pending":     "queued",
	"ready":       "ready",
	"dispatching": "dispatch",
	"running":     "executing",
	"succeeded":   "complete",
	"failed":      "failed",
	"blocked":     "blocked",
	"obsolete":    "obsolete",
	"cancelled":   "cancelled",
}

func workProjectionItoa(v int) string { return strconv.Itoa(v) }

func workProjectionTruncate(text string) string {
	const max = 160
	if len(text) <= max {
		return text
	}
	return text[:max] + "\u2026"
}

func workTimelineEventMilli(t time.Time, agentID string, attempt *int, kind, label, detail string) WorkTimelineEvent {
	unixMs := t.UnixMilli()
	return WorkTimelineEvent{
		Kind: kind, Label: label, AgentID: agentID, Attempt: attempt,
		Timestamp: t.UTC().Format(time.RFC3339Nano), UnixMs: unixMs, Detail: detail,
	}
}

func workTimelineAppend(ev *[]WorkTimelineEvent, timeline WorkTimelineEvent) {
	if timeline.UnixMs <= 0 {
		return
	}
	*ev = append(*ev, timeline)
}

func workProjectionStageFor(status string) string {
	if s, ok := workProjectionStageRole[string(status)]; ok {
		return s
	}
	return string(status)
}

func buildWorkTimeline(t *researchrun.Task, attempts []researchrun.Attempt, snapshot *researchrun.RunSnapshot) []WorkTimelineEvent {
	evs := make([]WorkTimelineEvent, 0, 4+len(attempts))
	appendEv := func(ev WorkTimelineEvent) { workTimelineAppend(&evs, ev) }

	if t.StartedAt != nil {
		appendEv(workTimelineEventMilli(*t.StartedAt, t.AssignedAgentID, nil, "dispatch", "\u4efb\u52a1\u6d3e\u53d1", "dispatch task to agent"))
	} else if t.ReadyAt != nil {
		appendEv(workTimelineEventMilli(*t.ReadyAt, t.AssignedAgentID, nil, "dispatch", "\u4efb\u52a1\u5c31\u7eea", "task ready for dispatch"))
	}

	switch t.Kind {
	case researchrun.TaskKindDiscover, researchrun.TaskKindCounterSearch, researchrun.TaskKindDeepRead:
		if t.StartedAt != nil {
			appendEv(workTimelineEventMilli(*t.StartedAt, t.AssignedAgentID, nil, "query", "\u68c0\u7d22\u4e0e\u7ebf\u7d22", "query + source discovery"))
		}
	case researchrun.TaskKindVerify, researchrun.TaskKindCitationAudit:
		if t.StartedAt != nil {
			appendEv(workTimelineEventMilli(*t.StartedAt, t.AssignedAgentID, nil, "validation", "\u8bc1\u636e\u6838\u9a8c", "validation of claims/sources"))
		}
	case researchrun.TaskKindSynthesize, researchrun.TaskKindReplan, researchrun.TaskKindQualityGate:
		if t.StartedAt != nil {
			appendEv(workTimelineEventMilli(*t.StartedAt, t.AssignedAgentID, nil, "report", "\u4ea7\u51fa\u5177\u7ed3", "report / synthesis"))
		}
	}

	for i := range attempts {
		a := &attempts[i]
		if a.TaskID != t.ID {
			continue
		}
		attemptNo := a.AttemptNumber
		if attemptNo > 1 && (a.Status == researchrun.AttemptStatusFailed || a.Status == researchrun.AttemptStatusLost || a.Status == researchrun.AttemptStatusCancelled) {
			appendEv(workTimelineEventMilli(a.DispatchedAt, a.AssignedAgentID, &attemptNo, "retry", "\u91cd\u8bd5", "attempt "+workProjectionItoa(attemptNo)))
		} else if a.Status == researchrun.AttemptStatusCancelled || a.CancelRequestedAt != nil {
			at := a.CancelRequestedAt
			if at == nil {
				at = &a.DispatchedAt
			}
			appendEv(workTimelineEventMilli(*at, a.AssignedAgentID, &attemptNo, "cancel", "\u53d6\u6d88", "attempt "+workProjectionItoa(attemptNo)+" cancelled"))
		} else if a.Status == researchrun.AttemptStatusFailed || a.Status == researchrun.AttemptStatusLost {
			appendEv(workTimelineEventMilli(a.DispatchedAt, a.AssignedAgentID, &attemptNo, "blocked", "\u6267\u884c\u53d7\u963b", workProjectionTruncate(a.FailureClass+" "+a.SourceFailureReason)))
		}
	}

	if t.Status == researchrun.TaskStatusDispatching || t.Status == researchrun.TaskStatusRunning {
		for i := range attempts {
			a := &attempts[i]
			if a.TaskID != t.ID {
				continue
			}
			if a.RuntimeObservedAt != nil && (a.Status == researchrun.AttemptStatusRunning || a.Status == researchrun.AttemptStatusDispatching) {
				appendEv(workTimelineEventMilli(*a.RuntimeObservedAt, a.AssignedAgentID, &a.AttemptNumber, "source_read", "\u8bfb\u53d6\u8fdb\u5ea6", "runtime observed active"))
			}
		}
	}

	for i := range snapshot.Sources {
		s := &snapshot.Sources[i]
		if s.ProducedByTaskID == t.ID {
			appendEv(workTimelineEventMilli(s.RetrievedAt, t.AssignedAgentID, nil, "source_read", "\u6765\u6e90\u8bfb\u53d6", workProjectionTruncate(s.Title)))
		}
	}

	for i := range snapshot.Claims {
		c := &snapshot.Claims[i]
		if c.ProducedByTaskID != t.ID {
			continue
		}
		appendEv(workTimelineEventMilli(c.CreatedAt, t.AssignedAgentID, nil, "finding", "\u53d1\u73b0", workProjectionTruncate(c.Text)))
		if c.Status == researchrun.ClaimStatusSupported || c.Status == researchrun.ClaimStatusDisputed || c.Status == researchrun.ClaimStatusRefuted {
			appendEv(workTimelineEventMilli(c.UpdatedAt, t.AssignedAgentID, nil, "validation", "\u6838\u9a8c", "claim "+string(c.Status)))
		}
	}

	switch t.Status {
	case researchrun.TaskStatusSucceeded:
		if t.CompletedAt != nil {
			// A report-producing task surfaces a report event before its terminal
			// complete event, so the canvas timeline shows the report being written.
			switch t.Kind {
			case researchrun.TaskKindSynthesize, researchrun.TaskKindReplan, researchrun.TaskKindQualityGate:
				appendEv(workTimelineEventMilli(*t.CompletedAt, t.AssignedAgentID, nil, "report", "\u62a5\u544a\u4ea7\u51fa", "report written by synthesis task"))
			}
			appendEv(workTimelineEventMilli(*t.CompletedAt, t.AssignedAgentID, nil, "complete", "\u5b8c\u6210", "task succeeded"))
		}
	case researchrun.TaskStatusFailed:
		if t.CompletedAt != nil {
			appendEv(workTimelineEventMilli(*t.CompletedAt, t.AssignedAgentID, nil, "blocked", "\u5931\u8d25", workProjectionTruncate(t.TerminalReason)))
		}
	case researchrun.TaskStatusBlocked:
		if t.CompletedAt != nil {
			appendEv(workTimelineEventMilli(*t.CompletedAt, t.AssignedAgentID, nil, "blocked", "\u963b\u585e", workProjectionTruncate(t.TerminalReason)))
		}
	}

	sort.SliceStable(evs, func(i, j int) bool { return evs[i].UnixMs < evs[j].UnixMs })
	return evs
}

func buildWorkProjectionEntry(t *researchrun.Task, attempts []researchrun.Attempt, snapshot *researchrun.RunSnapshot) WorkProjectionEntry {
	evidence := 0
	for i := range snapshot.Sources {
		if snapshot.Sources[i].ProducedByTaskID == t.ID {
			evidence++
		}
	}
	for i := range snapshot.Observations {
		if snapshot.Observations[i].ProducedByTaskID == t.ID {
			evidence++
		}
	}
	for i := range snapshot.Claims {
		if snapshot.Claims[i].ProducedByTaskID == t.ID {
			evidence++
		}
	}

	latestFinding := ""
	for i := range snapshot.Claims {
		c := &snapshot.Claims[i]
		if c.ProducedByTaskID == t.ID {
			if txt := workProjectionTruncate(c.Text); txt != "" {
				latestFinding = txt
			}
		}
	}

	stepsTotal := t.MaxAttempts
	stepsDone := t.AttemptCount
	var pct *int
	if stepsTotal > 0 && (stepsDone > 0 || t.Status == researchrun.TaskStatusSucceeded) {
		v := stepsDone * 100 / stepsTotal
		if t.Status == researchrun.TaskStatusSucceeded {
			v = 100
		}
		if v < 0 {
			v = 0
		}
		if v > 100 {
			v = 100
		}
		pct = &v
	}

	return WorkProjectionEntry{
		TaskID:          t.ID,
		Kind:            string(t.Kind),
		Objective:       t.Objective,
		Scope:           t.RequiredCapability,
		ExpectedOutput:  t.ExpectedResult,
		Round:           t.GoalVersion,
		AssignedAgentID: t.AssignedAgentID,
		Stage:           workProjectionStageFor(string(t.Status)),
		Action:          workProjectionAction(t),
		Status:          string(t.Status),
		StepsDone:       stepsDone,
		StepsTotal:      stepsTotal,
		ProgressPercent: pct,
		LatestFinding:   latestFinding,
		EvidenceCount:   evidence,
		BlockedReason:   blockedReasonFor(t),
		FailureReason:   failureReasonFor(t),
		NextStep:        nextStepFor(t),
		Timeline:        buildWorkTimeline(t, attempts, snapshot),
	}
}

// workProjectionAction returns a short human summary of what a task is doing
// right now, derived from its status (never from runtime liveness).
func workProjectionAction(t *researchrun.Task) string {
	switch t.Status {
	case researchrun.TaskStatusPending:
		return "等待派发"
	case researchrun.TaskStatusReady:
		return "已就绪待派发"
	case researchrun.TaskStatusDispatching:
		return "正在派发执行"
	case researchrun.TaskStatusRunning:
		return "执行中"
	case researchrun.TaskStatusBlocked:
		return "受阻待处理"
	default:
		return ""
	}
}

func blockedReasonFor(t *researchrun.Task) string {
	if t.Status == researchrun.TaskStatusBlocked {
		return workProjectionTruncate(t.TerminalReason)
	}
	return ""
}

func failureReasonFor(t *researchrun.Task) string {
	if t.Status == researchrun.TaskStatusFailed {
		return workProjectionTruncate(t.TerminalReason)
	}
	return ""
}

func nextStepFor(t *researchrun.Task) string {
	switch t.Status {
	case researchrun.TaskStatusBlocked:
		return "等待重派或因阻塞解除后重试"
	case researchrun.TaskStatusFailed:
		if t.AttemptCount < t.MaxAttempts {
			return "将自动重试或重新派发"
		}
		return "已用尽重试次数，等待人工介入"
	default:
		return ""
	}
}

// GetResearchWorkProjection returns the realtime per-task work projection +
// event timeline for a research session. It is scope-bound to the request
// workspace + session and derives everything from the run ledger (no invented
// state table), so no cross-workspace or unpermitted task info can leak.
func (h *Handler) GetResearchWorkProjection(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if _, err := h.Queries.GetResearchSession(r.Context(), db.GetResearchSessionParams{ID: sessionID, WorkspaceID: wsUUID}); err != nil {
		writeError(w, http.StatusNotFound, "research session not found")
		return
	}

	resp := workProjectionResponse{
		SessionID: uuidToString(sessionID),
		Entries:   []WorkProjectionEntry{},
	}

	sessionKey := uuidToString(sessionID)
	if h.ResearchRun != nil {
		snap, serr := h.ResearchRun.Snapshot(r.Context(), sessionKey, workspaceID)
		if serr == nil {
			resp.Stage = strings.TrimSpace(snap.Run.CurrentStage)
			resp.RunStatus = string(snap.Run.Status)
			for i := range snap.Tasks {
				t := &snap.Tasks[i]
				resp.Entries = append(resp.Entries, buildWorkProjectionEntry(t, snap.Attempts, &snap))
			}
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// workProjectionThrottleInterval is the minimum gap between two WS nudges for
// the same research session. Lifecycle events within this window are coalesced
// into a single nudge, which bounds WS fan-out under bursts (many tasks
// dispatching/running at once) while the HTTP snapshot stays authoritative.
const workProjectionThrottleInterval = 150 * time.Millisecond

// workProjectionNotifier coalesces per-session work-projection nudges so a
// burst of task lifecycle events produces at most one WS event per session per
// interval. Every emitted nudge carries a per-session monotonic "sequence" so a
// reconnecting client can detect it missed mid-stream nudges while offline.
// The projection is a pure, deterministic function of the run ledger, so an
// HTTP snapshot refetch is always lossless — that is the reconnect contract:
// a client that reconnects (or observes a sequence gap) must refetch the
// authoritative HTTP snapshot rather than reconstruct history from nudges.
type workProjectionNotifier struct {
	mu       sync.Mutex
	interval time.Duration
	publish  func(scopeKey string, seq int64)
	dirty    map[string]struct{}
	seq      map[string]int64
	timer    *time.Timer
	closed   bool
}

func newWorkProjectionNotifier(interval time.Duration, publish func(scopeKey string, seq int64)) *workProjectionNotifier {
	return &workProjectionNotifier{
		interval: interval,
		publish:  publish,
		dirty:    map[string]struct{}{},
		seq:      map[string]int64{},
	}
}

// scopeKeyForWorkProjection uniquely identifies a research session across
// workspaces so nudges are scoped and can never leak between workspaces.
func scopeKeyForWorkProjection(workspaceID, sessionKey string) string {
	return workspaceID + "|" + sessionKey
}

// Notify marks a research session dirty so a single coalesced nudge is emitted
// for it within the throttle interval. A burst of lifecycle events within the
// window collapses to one push. Safe for concurrent callers.
func (n *workProjectionNotifier) Notify(scopeKey string) {
	if scopeKey == "" {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return
	}
	if _, ok := n.dirty[scopeKey]; ok {
		return // already scheduled; coalesce the burst
	}
	n.dirty[scopeKey] = struct{}{}
	if n.timer == nil {
		n.timer = time.AfterFunc(n.interval, n.tick)
	}
}

func (n *workProjectionNotifier) tick() {
	n.mu.Lock()
	n.drainLocked()
	n.mu.Unlock()
}

// drainLocked emits one nudge per dirty session and resets the timer for any
// work that arrived while draining. Called with n.mu held.
func (n *workProjectionNotifier) drainLocked() {
	if len(n.dirty) == 0 {
		if n.timer != nil {
			n.timer = nil
		}
		return
	}
	pending := n.dirty
	n.dirty = map[string]struct{}{}
	if n.timer != nil {
		n.timer = nil
	}
	type job struct {
		key string
		seq int64
	}
	jobs := make([]job, 0, len(pending))
	for key := range pending {
		n.seq[key]++
		jobs = append(jobs, job{key, n.seq[key]})
	}
	for _, j := range jobs {
		if n.publish != nil {
			n.publish(j.key, j.seq)
		}
	}
}

// Flush drains all pending nudges immediately. Used by tests to avoid racing a
// timer and by shutdown paths to ensure nothing is dropped.
func (n *workProjectionNotifier) Flush() {
	n.mu.Lock()
	n.drainLocked()
	n.mu.Unlock()
}

// Close stops the notifier and flushes pending nudges exactly once.
func (n *workProjectionNotifier) Close() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return
	}
	n.closed = true
	n.drainLocked()
}

// publishWorkProjectionNudge pushes one coalesced, scoped WS event for a
// research session when its task work projection changes, so the canvas
// timeline can refresh without polling. It is best-effort and always carries a
// monotonic sequence; the HTTP snapshot is the authoritative projection.
func (h *Handler) publishWorkProjectionNudge(workspaceID, sessionKey string, seq int64, at time.Time) {
	if h == nil || h.ResearchRun == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snap, err := h.ResearchRun.Snapshot(ctx, sessionKey, workspaceID)
	if err != nil {
		// Best-effort: skip the nudge; the client refetches the HTTP snapshot.
		return
	}
	tasks := make([]workProjectionNudgeTask, 0, len(snap.Tasks))
	for i := range snap.Tasks {
		t := &snap.Tasks[i]
		tasks = append(tasks, workProjectionNudgeTask{
			TaskID:  t.ID,
			AgentID: t.AssignedAgentID,
			Status:  string(t.Status),
			Stage:   workProjectionStageFor(string(t.Status)),
		})
	}
	h.publish(protocol.EventResearchSessionWorkProjection, workspaceID, "session", sessionKey, workProjectionNudge{
		SessionID: sessionKey,
		Sequence:  seq,
		Tasks:     tasks,
		UnixMs:    at.UnixMilli(),
	})
}

// workProjectionNudge is the WS payload for a coalesced work-projection event.
type workProjectionNudge struct {
	SessionID string                   `json:"session_id"`
	Sequence  int64                    `json:"sequence"`
	Tasks     []workProjectionNudgeTask `json:"tasks"`
	UnixMs    int64                    `json:"unix_ms"`
}

// workProjectionNudgeTask is one task's latest status within a nudge.
type workProjectionNudgeTask struct {
	TaskID  string `json:"task_id"`
	AgentID string `json:"agent_id,omitempty"`
	Status  string `json:"status"`
	Stage   string `json:"stage"`
}

// workProjectionLifecycleEvents lists the run-ledger event types that change a
// task's status, so the canvas timeline can be nudged without polling. Scope is
// deliberately task-level: session-global (run_) events are handled via the
// HTTP snapshot, not per-task WS pushes.
var workProjectionLifecycleEvents = map[string]bool{
	"control_task_created":              true,
	"task_dispatching":                  true,
	"task_dispatched":                   true,
	"task_started":                      true,
	"task_waiting_for_execution_target": true,
	"task_attempt_failed":               true,
	"task_attempt_cancelling":           true,
	"task_blocked":                      true,
	"task_result_accepted":              true,
	"node_command_retry":                true,
	"node_command_reassign":             true,
}

// isTaskLifecycleEvent reports whether a run-ledger event type should trigger a
// per-task work-projection WS push.
func isTaskLifecycleEvent(eventType string) bool {
	if workProjectionLifecycleEvents[eventType] {
		return true
	}
	return strings.HasPrefix(eventType, "node_command_")
}

// publishWorkProjectionForEvent marks a research session dirty so a single
// coalesced, throttled WS nudge is emitted for the task(s) touched by a
// lifecycle event. It is best-effort and non-fatal: the HTTP projection
// snapshot stays authoritative, so a dropped nudge (or one coalesced away by
// the throttle) only means the client refreshes on the next poll.
func publishWorkProjectionForEvent(ctx context.Context, h *Handler, event researchrun.RunEvent) error {
	if h == nil || h.ResearchRun == nil || h.WorkProjectionNotifier == nil {
		return nil
	}
	h.WorkProjectionNotifier.Notify(scopeKeyForWorkProjection(event.WorkspaceID, event.SessionID))
	return nil
}

// workProjectionPayloadString reads a top-level string field from a run-event
// payload without failing when the key is absent.
func workProjectionPayloadString(payload json.RawMessage, key string) string {
	if len(payload) == 0 {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal(payload, &obj); err != nil {
		return ""
	}
	if v, ok := obj[key].(string); ok {
		return v
	}
	return ""
}
