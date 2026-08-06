package handler

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/researchrun"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Presence phase enum (LRM-1377). Stable wire values for FE/Core.
const (
	ResearchPresencePhaseIdle    = "idle"
	ResearchPresencePhaseQueued  = "queued"
	ResearchPresencePhaseRunning = "running"
	ResearchPresencePhaseDone    = "done"
	ResearchPresencePhaseFailed  = "failed"
	ResearchPresencePhaseStale   = "stale"
)

// researchPresenceStaleAfter marks queued/running presence as stale when the
// latest signal is older than this. Documented in presence-contract-v2.md.
const researchPresenceStaleAfter = 15 * time.Minute

const (
	researchPresenceGenericDispatchTitle = "调研任务已分派"
	researchPresenceGenericStartedTitle  = "Agent 开始执行调研任务"
)

// ResearchPresenceEntry is one agent's live presence for a session (LRM-1377).
// Legacy clients read activity + updated_at; new fields are additive.
// Presence is a derived view — run.tasks/attempts remain the execution SoT.
type ResearchPresenceEntry struct {
	Activity      string  `json:"activity"`
	UpdatedAt     int64   `json:"updated_at"` // unix ms; 0 when never observed
	Phase         string  `json:"phase"`
	Role          string  `json:"role,omitempty"`
	FleetMemberID string  `json:"fleet_member_id,omitempty"`
	TaskID        *string `json:"task_id"`
	NodeID        *string `json:"node_id"`
	BranchID      *string `json:"branch_id"`
	Stage         *string `json:"stage"`
	ExpiresAt     *int64  `json:"expires_at"` // unix ms; null when unknown
	StaleReason   *string `json:"stale_reason"`
}

type researchPresenceMember struct {
	AgentID       string
	Role          string
	FleetMemberID string
}

type presenceSignalKind int

const (
	presenceSignalGeneric presenceSignalKind = iota + 1
	presenceSignalActivity
	presenceSignalExplicit // payload.phase == "presence"
	presenceSignalAttempt  // run-v2 attempt/task ledger (preferred lifecycle SoT)
	presenceSignalDone
	presenceSignalFailed
)

type presenceSignal struct {
	Kind        presenceSignalKind
	Activity    string
	UpdatedAt   int64
	TaskID      string
	NodeID      string
	BranchID    string
	Stage       string
	ExpiresAtMs int64
	PhaseHint   string // queued|running|done|failed|idle when known
}

// buildResearchPresenceMap rebuilds ephemeral presence from the latest
// agent_activity graph node per actor (legacy helper; prefer roster builder).
func buildResearchPresenceMap(nodes []db.ResearchGraphNode) map[string]ResearchPresenceEntry {
	out := map[string]ResearchPresenceEntry{}
	for _, n := range nodes {
		if n.NodeType != "agent_activity" || !n.ActorAgentID.Valid {
			continue
		}
		agentID := uuidToString(n.ActorAgentID)
		activity := strings.TrimSpace(n.Title)
		if activity == "" {
			activity = strings.TrimSpace(n.Summary)
		}
		if activity == "" {
			continue
		}
		updatedAt := nodeUpdatedAtMs(n)
		prev, ok := out[agentID]
		if !ok || updatedAt >= prev.UpdatedAt {
			out[agentID] = ResearchPresenceEntry{
				Activity:  activity,
				UpdatedAt: updatedAt,
				Phase:     ResearchPresencePhaseRunning,
			}
		}
	}
	return out
}

// buildResearchPresenceRoster returns one presence entry per active fleet
// member, merging graph activity with run-v2 attempt/task projections (LRM-1377).
// When tasks/attempts are provided they are the preferred lifecycle source;
// graph captions may still enrich activity text.
func buildResearchPresenceRoster(
	members []researchPresenceMember,
	nodes []db.ResearchGraphNode,
	now time.Time,
) map[string]ResearchPresenceEntry {
	return buildResearchPresenceRosterWithRun(members, nodes, nil, nil, "", "", now)
}

func buildResearchPresenceRosterWithRun(
	members []researchPresenceMember,
	nodes []db.ResearchGraphNode,
	tasks []researchrun.Task,
	attempts []researchrun.Attempt,
	sessionID string,
	runStage string,
	now time.Time,
) map[string]ResearchPresenceEntry {
	byAgent := map[string][]presenceSignal{}
	for _, n := range nodes {
		sig, agentID, ok := presenceSignalFromNode(n)
		if !ok {
			continue
		}
		byAgent[agentID] = append(byAgent[agentID], sig)
	}
	for agentID, sigs := range presenceSignalsFromRun(sessionID, runStage, tasks, attempts) {
		byAgent[agentID] = append(byAgent[agentID], sigs...)
	}

	out := make(map[string]ResearchPresenceEntry, len(members))
	for _, m := range members {
		if m.AgentID == "" {
			continue
		}
		entry := mergePresenceSignals(byAgent[m.AgentID], now)
		entry.Role = m.Role
		entry.FleetMemberID = m.FleetMemberID
		out[m.AgentID] = entry
	}
	return out
}

// presenceSignalsFromRun projects execution presence from the durable run
// ledger. Attempts are the source of truth; tasks cover assigned-but-not-yet-
// attempted dispatch states. node_id is the deterministic canvas task node.
func presenceSignalsFromRun(
	sessionID, runStage string,
	tasks []researchrun.Task,
	attempts []researchrun.Attempt,
) map[string][]presenceSignal {
	if len(tasks) == 0 && len(attempts) == 0 {
		return nil
	}
	taskByID := make(map[string]researchrun.Task, len(tasks))
	for _, t := range tasks {
		id := strings.TrimSpace(t.ID)
		if id == "" {
			continue
		}
		taskByID[id] = t
	}

	byAgentAttempts := map[string][]researchrun.Attempt{}
	for _, a := range attempts {
		aid := strings.TrimSpace(a.AssignedAgentID)
		if aid == "" {
			continue
		}
		byAgentAttempts[aid] = append(byAgentAttempts[aid], a)
	}

	out := map[string][]presenceSignal{}
	for agentID, list := range byAgentAttempts {
		best := selectCurrentAttempt(list)
		if best == nil {
			continue
		}
		task := taskByID[strings.TrimSpace(best.TaskID)]
		out[agentID] = append(out[agentID], presenceSignalFromAttempt(sessionID, runStage, task, *best))
	}

	// Assigned tasks with no attempt yet (dispatching / running row only).
	for _, t := range tasks {
		aid := strings.TrimSpace(t.AssignedAgentID)
		if aid == "" {
			continue
		}
		if _, hasAttempt := byAgentAttempts[aid]; hasAttempt {
			continue
		}
		switch t.Status {
		case researchrun.TaskStatusDispatching, researchrun.TaskStatusRunning:
			out[aid] = append(out[aid], presenceSignalFromTask(sessionID, runStage, t))
		}
	}
	return out
}

func selectCurrentAttempt(list []researchrun.Attempt) *researchrun.Attempt {
	if len(list) == 0 {
		return nil
	}
	var open *researchrun.Attempt
	var terminal *researchrun.Attempt
	for i := range list {
		a := &list[i]
		ts := attemptUpdatedAtMs(*a)
		switch a.Status {
		case researchrun.AttemptStatusDispatching, researchrun.AttemptStatusRunning, researchrun.AttemptStatusCancelling:
			if open == nil ||
				a.AttemptNumber > open.AttemptNumber ||
				(a.AttemptNumber == open.AttemptNumber && ts >= attemptUpdatedAtMs(*open)) {
				open = a
			}
		default:
			if terminal == nil ||
				a.AttemptNumber > terminal.AttemptNumber ||
				(a.AttemptNumber == terminal.AttemptNumber && ts >= attemptUpdatedAtMs(*terminal)) {
				terminal = a
			}
		}
	}
	if open != nil {
		return open
	}
	return terminal
}

func presenceSignalFromAttempt(sessionID, runStage string, task researchrun.Task, a researchrun.Attempt) presenceSignal {
	activity := strings.TrimSpace(task.Objective)
	if activity == "" {
		activity = strings.TrimSpace(string(task.Kind))
	}
	taskID := firstNonEmpty(strings.TrimSpace(a.TaskID), strings.TrimSpace(task.ID))
	nodeID := ""
	if sessionID != "" && taskID != "" {
		nodeID = runGraphNodeID(sessionID, runGraphKindTask, taskID)
	}
	sig := presenceSignal{
		Activity:    activity,
		UpdatedAt:   attemptUpdatedAtMs(a),
		TaskID:      taskID,
		NodeID:      nodeID,
		Stage:       strings.TrimSpace(runStage),
		ExpiresAtMs: attemptExpiresAtMs(task, a),
	}
	switch a.Status {
	case researchrun.AttemptStatusDispatching:
		sig.Kind = presenceSignalAttempt
		sig.PhaseHint = ResearchPresencePhaseQueued
		if sig.Activity == "" {
			sig.Activity = researchPresenceGenericDispatchTitle
		}
	case researchrun.AttemptStatusRunning:
		sig.Kind = presenceSignalAttempt
		sig.PhaseHint = ResearchPresencePhaseRunning
		if sig.Activity == "" {
			sig.Activity = researchPresenceGenericStartedTitle
		}
	case researchrun.AttemptStatusCancelling:
		sig.Kind = presenceSignalAttempt
		sig.PhaseHint = ResearchPresencePhaseRunning
		sig.Activity = "正在停止超时任务"
	case researchrun.AttemptStatusSucceeded:
		sig.Kind = presenceSignalDone
		sig.PhaseHint = ResearchPresencePhaseDone
		if sig.Activity == "" {
			sig.Activity = "调研结果已入账"
		}
	case researchrun.AttemptStatusFailed, researchrun.AttemptStatusLost:
		sig.Kind = presenceSignalFailed
		sig.PhaseHint = ResearchPresencePhaseFailed
		if fail := strings.TrimSpace(a.FailureClass); fail != "" {
			sig.Activity = fail
		} else if diag := strings.TrimSpace(a.Diagnostics); diag != "" {
			sig.Activity = diag
		} else if sig.Activity == "" {
			sig.Activity = "调研任务尝试失败"
		}
	case researchrun.AttemptStatusCancelled:
		sig.Kind = presenceSignalAttempt
		sig.PhaseHint = ResearchPresencePhaseIdle
		sig.Activity = ""
		sig.TaskID = ""
		sig.NodeID = ""
		sig.ExpiresAtMs = 0
	default:
		sig.Kind = presenceSignalAttempt
		sig.PhaseHint = ResearchPresencePhaseRunning
	}
	return sig
}

func presenceSignalFromTask(sessionID, runStage string, task researchrun.Task) presenceSignal {
	activity := strings.TrimSpace(task.Objective)
	if activity == "" {
		activity = strings.TrimSpace(string(task.Kind))
	}
	taskID := strings.TrimSpace(task.ID)
	nodeID := ""
	if sessionID != "" && taskID != "" {
		nodeID = runGraphNodeID(sessionID, runGraphKindTask, taskID)
	}
	sig := presenceSignal{
		Kind:      presenceSignalAttempt,
		Activity:  activity,
		UpdatedAt: taskUpdatedAtMs(task),
		TaskID:    taskID,
		NodeID:    nodeID,
		Stage:     strings.TrimSpace(runStage),
	}
	switch task.Status {
	case researchrun.TaskStatusDispatching:
		sig.PhaseHint = ResearchPresencePhaseQueued
		if sig.Activity == "" {
			sig.Activity = researchPresenceGenericDispatchTitle
		}
	default:
		sig.PhaseHint = ResearchPresencePhaseRunning
		if sig.Activity == "" {
			sig.Activity = researchPresenceGenericStartedTitle
		}
	}
	if task.TimeoutSeconds > 0 {
		start := task.StartedAt
		if start == nil {
			start = task.ReadyAt
		}
		if start != nil && !start.IsZero() {
			sig.ExpiresAtMs = start.Add(time.Duration(task.TimeoutSeconds) * time.Second).UnixMilli()
		}
	}
	return sig
}

func attemptUpdatedAtMs(a researchrun.Attempt) int64 {
	if a.CompletedAt != nil && !a.CompletedAt.IsZero() {
		return a.CompletedAt.UnixMilli()
	}
	if a.ResultSubmittedAt != nil && !a.ResultSubmittedAt.IsZero() {
		return a.ResultSubmittedAt.UnixMilli()
	}
	if a.StartedAt != nil && !a.StartedAt.IsZero() {
		return a.StartedAt.UnixMilli()
	}
	if !a.DispatchedAt.IsZero() {
		return a.DispatchedAt.UnixMilli()
	}
	return 0
}

func taskUpdatedAtMs(t researchrun.Task) int64 {
	if t.CompletedAt != nil && !t.CompletedAt.IsZero() {
		return t.CompletedAt.UnixMilli()
	}
	if t.StartedAt != nil && !t.StartedAt.IsZero() {
		return t.StartedAt.UnixMilli()
	}
	if t.ReadyAt != nil && !t.ReadyAt.IsZero() {
		return t.ReadyAt.UnixMilli()
	}
	return 0
}

func attemptExpiresAtMs(task researchrun.Task, a researchrun.Attempt) int64 {
	if task.TimeoutSeconds <= 0 {
		return 0
	}
	start := a.DispatchedAt
	if a.StartedAt != nil && !a.StartedAt.IsZero() {
		start = *a.StartedAt
	}
	if start.IsZero() {
		return 0
	}
	return start.Add(time.Duration(task.TimeoutSeconds) * time.Second).UnixMilli()
}

func mergePresenceSignals(signals []presenceSignal, now time.Time) ResearchPresenceEntry {
	if len(signals) == 0 {
		return ResearchPresenceEntry{
			Activity:    "",
			UpdatedAt:   0,
			Phase:       ResearchPresencePhaseIdle,
			TaskID:      nil,
			NodeID:      nil,
			BranchID:    nil,
			Stage:       nil,
			ExpiresAt:   nil,
			StaleReason: nil,
		}
	}

	var latestTerminal *presenceSignal
	var bestSpecific *presenceSignal
	var latestGeneric *presenceSignal
	var latestAny *presenceSignal

	for i := range signals {
		s := &signals[i]
		if latestAny == nil || s.UpdatedAt >= latestAny.UpdatedAt {
			latestAny = s
		}
		switch s.Kind {
		case presenceSignalDone, presenceSignalFailed:
			if latestTerminal == nil || s.UpdatedAt >= latestTerminal.UpdatedAt {
				latestTerminal = s
			}
		case presenceSignalExplicit, presenceSignalActivity, presenceSignalAttempt:
			if bestSpecific == nil ||
				s.Kind > bestSpecific.Kind ||
				(s.Kind == bestSpecific.Kind && s.UpdatedAt >= bestSpecific.UpdatedAt) {
				bestSpecific = s
			}
		case presenceSignalGeneric:
			if latestGeneric == nil || s.UpdatedAt >= latestGeneric.UpdatedAt {
				latestGeneric = s
			}
		}
	}

	// Terminal wins when it is the newest lifecycle signal (clears "开始执行").
	if latestTerminal != nil {
		nonTerminalLatest := int64(0)
		if bestSpecific != nil && bestSpecific.UpdatedAt > nonTerminalLatest {
			nonTerminalLatest = bestSpecific.UpdatedAt
		}
		if latestGeneric != nil && latestGeneric.UpdatedAt > nonTerminalLatest {
			nonTerminalLatest = latestGeneric.UpdatedAt
		}
		if latestTerminal.UpdatedAt >= nonTerminalLatest {
			return finalizePresenceEntry(*latestTerminal, now)
		}
	}

	// Attempt ledger / specific presence preferred over newer generic titles.
	if bestSpecific != nil {
		entry := *bestSpecific
		// Prefer concrete graph caption onto attempt lifecycle when both exist.
		if entry.Kind == presenceSignalAttempt {
			var caption *presenceSignal
			for i := range signals {
				s := &signals[i]
				if s.Kind == presenceSignalExplicit || s.Kind == presenceSignalActivity {
					if caption == nil || s.UpdatedAt >= caption.UpdatedAt {
						caption = s
					}
				}
			}
			if caption != nil && strings.TrimSpace(caption.Activity) != "" &&
				!isGenericResearchPresenceActivity(caption.Activity, "") {
				entry.Activity = caption.Activity
				if caption.NodeID != "" && entry.NodeID == "" {
					entry.NodeID = caption.NodeID
				}
				if caption.BranchID != "" && entry.BranchID == "" {
					entry.BranchID = caption.BranchID
				}
			}
		}
		// Enrich phase/task/node from a newer generic lifecycle without
		// replacing the concrete activity text.
		if latestGeneric != nil && latestGeneric.UpdatedAt >= bestSpecific.UpdatedAt {
			if entry.PhaseHint == "" && latestGeneric.PhaseHint != "" {
				entry.PhaseHint = latestGeneric.PhaseHint
			}
			if entry.TaskID == "" {
				entry.TaskID = latestGeneric.TaskID
			}
			if entry.BranchID == "" {
				entry.BranchID = latestGeneric.BranchID
			}
			// Keep specific node_id for locate; only fill if missing.
			if entry.NodeID == "" {
				entry.NodeID = latestGeneric.NodeID
			}
			if latestGeneric.UpdatedAt > entry.UpdatedAt {
				entry.UpdatedAt = latestGeneric.UpdatedAt
			}
		}
		return finalizePresenceEntry(entry, now)
	}

	if latestGeneric != nil {
		return finalizePresenceEntry(*latestGeneric, now)
	}
	if latestAny != nil {
		return finalizePresenceEntry(*latestAny, now)
	}
	return ResearchPresenceEntry{Phase: ResearchPresencePhaseIdle}
}

func finalizePresenceEntry(sig presenceSignal, now time.Time) ResearchPresenceEntry {
	phase := sig.PhaseHint
	if phase == "" {
		switch sig.Kind {
		case presenceSignalDone:
			phase = ResearchPresencePhaseDone
		case presenceSignalFailed:
			phase = ResearchPresencePhaseFailed
		case presenceSignalGeneric, presenceSignalAttempt:
			phase = ResearchPresencePhaseRunning
		default:
			phase = ResearchPresencePhaseRunning
		}
	}

	var staleReason *string
	if phase == ResearchPresencePhaseQueued || phase == ResearchPresencePhaseRunning {
		if sig.ExpiresAtMs > 0 && now.UnixMilli() >= sig.ExpiresAtMs {
			phase = ResearchPresencePhaseStale
			reason := "attempt_expired"
			staleReason = &reason
		} else if sig.UpdatedAt > 0 {
			age := now.Sub(time.UnixMilli(sig.UpdatedAt))
			if age >= researchPresenceStaleAfter {
				phase = ResearchPresencePhaseStale
				reason := "presence_expired"
				staleReason = &reason
			}
		}
	}

	return ResearchPresenceEntry{
		Activity:    sig.Activity,
		UpdatedAt:   sig.UpdatedAt,
		Phase:       phase,
		TaskID:      stringPtrOrNil(sig.TaskID),
		NodeID:      stringPtrOrNil(sig.NodeID),
		BranchID:    stringPtrOrNil(sig.BranchID),
		Stage:       stringPtrOrNil(sig.Stage),
		ExpiresAt:   int64PtrOrNil(sig.ExpiresAtMs),
		StaleReason: staleReason,
	}
}

func int64PtrOrNil(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

func presenceSignalFromNode(n db.ResearchGraphNode) (presenceSignal, string, bool) {
	if !n.ActorAgentID.Valid {
		return presenceSignal{}, "", false
	}
	agentID := uuidToString(n.ActorAgentID)
	updatedAt := nodeUpdatedAtMs(n)
	payload := parsePresencePayload(n.Payload)
	eventType := strings.TrimSpace(payload.EventType)
	taskID := firstNonEmpty(payload.TaskID, payload.DetailsTaskID)
	nodeID := firstNonEmpty(payload.NodeID, payload.TargetNodeID, payload.TargetNode)
	branchID := strings.TrimSpace(payload.BranchID)
	title := strings.TrimSpace(n.Title)
	summary := strings.TrimSpace(n.Summary)
	activity := title
	if activity == "" {
		activity = summary
	}

	switch n.NodeType {
	case "agent_activity":
		if activity == "" {
			return presenceSignal{}, "", false
		}
		sig := presenceSignal{
			Activity:  activity,
			UpdatedAt: updatedAt,
			TaskID:    taskID,
			// Only real event associations — never invent from the activity row id.
			NodeID:   nodeID,
			BranchID: branchID,
		}
		if strings.EqualFold(strings.TrimSpace(payload.Phase), "presence") {
			sig.Kind = presenceSignalExplicit
			sig.PhaseHint = ResearchPresencePhaseRunning
			return sig, agentID, true
		}
		if isGenericResearchPresenceActivity(activity, eventType) {
			sig.Kind = presenceSignalGeneric
			switch eventType {
			case "task_dispatching":
				sig.PhaseHint = ResearchPresencePhaseQueued
			case "task_started":
				sig.PhaseHint = ResearchPresencePhaseRunning
			default:
				if activity == researchPresenceGenericDispatchTitle {
					sig.PhaseHint = ResearchPresencePhaseQueued
				} else {
					sig.PhaseHint = ResearchPresencePhaseRunning
				}
			}
			return sig, agentID, true
		}
		sig.Kind = presenceSignalActivity
		sig.PhaseHint = ResearchPresencePhaseRunning
		return sig, agentID, true

	case "finding":
		if eventType == "task_result_accepted" || activity == "调研结果已入账" {
			if activity == "" {
				activity = "调研结果已入账"
			}
			return presenceSignal{
				Kind:      presenceSignalDone,
				Activity:  activity,
				UpdatedAt: updatedAt,
				TaskID:    taskID,
				NodeID:    nodeID,
				BranchID:  branchID,
				PhaseHint: ResearchPresencePhaseDone,
			}, agentID, true
		}
	case "dead_end":
		switch eventType {
		case "task_attempt_failed", "task_blocked", "run_failed":
			if activity == "" {
				activity = title
			}
			if activity == "" {
				activity = "调研任务失败"
			}
			return presenceSignal{
				Kind:      presenceSignalFailed,
				Activity:  activity,
				UpdatedAt: updatedAt,
				TaskID:    taskID,
				NodeID:    nodeID,
				BranchID:  branchID,
				PhaseHint: ResearchPresencePhaseFailed,
			}, agentID, true
		}
		if activity == "调研任务尝试失败" || activity == "调研任务因前置失败而阻塞" {
			return presenceSignal{
				Kind:      presenceSignalFailed,
				Activity:  activity,
				UpdatedAt: updatedAt,
				TaskID:    taskID,
				NodeID:    nodeID,
				BranchID:  branchID,
				PhaseHint: ResearchPresencePhaseFailed,
			}, agentID, true
		}
	}
	return presenceSignal{}, "", false
}

func isGenericResearchPresenceActivity(activity, eventType string) bool {
	switch eventType {
	case "task_dispatching", "task_started":
		return true
	}
	switch activity {
	case researchPresenceGenericDispatchTitle, researchPresenceGenericStartedTitle:
		return true
	}
	return false
}

type presencePayloadView struct {
	Phase         string
	EventType     string
	TaskID        string
	DetailsTaskID string
	NodeID        string
	TargetNodeID  string
	TargetNode    string
	BranchID      string
}

func parsePresencePayload(raw []byte) presencePayloadView {
	var out presencePayloadView
	if len(raw) == 0 {
		return out
	}
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		return out
	}
	out.Phase = anyString(top["phase"])
	out.EventType = anyString(top["event_type"])
	if out.EventType == "" {
		out.EventType = anyString(top["eventType"])
	}
	out.TaskID = anyString(top["task_id"])
	if out.TaskID == "" {
		out.TaskID = anyString(top["taskId"])
	}
	out.NodeID = anyString(top["node_id"])
	if out.NodeID == "" {
		out.NodeID = anyString(top["nodeId"])
	}
	out.TargetNodeID = anyString(top["target_node_id"])
	if out.TargetNodeID == "" {
		out.TargetNodeID = anyString(top["targetNodeId"])
	}
	out.TargetNode = anyString(top["target_node"])
	if out.TargetNode == "" {
		out.TargetNode = anyString(top["targetNode"])
	}
	out.BranchID = anyString(top["branch_id"])
	if out.BranchID == "" {
		out.BranchID = anyString(top["branchId"])
	}
	if details, ok := top["details"].(map[string]any); ok {
		if out.DetailsTaskID == "" {
			out.DetailsTaskID = anyString(details["task_id"])
			if out.DetailsTaskID == "" {
				out.DetailsTaskID = anyString(details["taskId"])
			}
		}
		if out.NodeID == "" {
			out.NodeID = anyString(details["node_id"])
			if out.NodeID == "" {
				out.NodeID = anyString(details["nodeId"])
			}
		}
		if out.TargetNodeID == "" {
			out.TargetNodeID = anyString(details["target_node_id"])
			if out.TargetNodeID == "" {
				out.TargetNodeID = anyString(details["targetNodeId"])
			}
		}
		if out.TargetNode == "" {
			out.TargetNode = anyString(details["target_node"])
			if out.TargetNode == "" {
				out.TargetNode = anyString(details["targetNode"])
			}
		}
		if out.BranchID == "" {
			out.BranchID = anyString(details["branch_id"])
			if out.BranchID == "" {
				out.BranchID = anyString(details["branchId"])
			}
		}
	}
	return out
}

func anyString(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func stringPtrOrNil(v string) *string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	return &v
}

func nodeUpdatedAtMs(n db.ResearchGraphNode) int64 {
	if n.UpdatedAt.Valid {
		return n.UpdatedAt.Time.UnixMilli()
	}
	if n.CreatedAt.Valid {
		return n.CreatedAt.Time.UnixMilli()
	}
	return 0
}

func researchPresenceMembersFromFleet(members []db.ResearchFleetMember) []researchPresenceMember {
	// Mirror researchFleetToResponse: one active member per role.
	seenRole := map[string]db.ResearchFleetMember{}
	for _, m := range members {
		if m.Status == "archived" {
			continue
		}
		prev, ok := seenRole[m.Role]
		if !ok || (m.IsLead && !prev.IsLead) || (!m.IsLead && !prev.IsLead && m.CreatedAt.Time.Before(prev.CreatedAt.Time)) {
			seenRole[m.Role] = m
		}
	}
	ordered := make([]researchPresenceMember, 0, len(seenRole))
	for _, m := range members {
		kept, ok := seenRole[m.Role]
		if !ok || kept.ID != m.ID {
			continue
		}
		delete(seenRole, m.Role)
		ordered = append(ordered, researchPresenceMember{
			AgentID:       uuidToString(m.AgentID),
			Role:          m.Role,
			FleetMemberID: uuidToString(m.ID),
		})
	}
	return ordered
}

// researchPresenceMembersFromRunFleet uses the session-bound fleet roster
// (same table, scoped via session.fleet_id). Dedupes by agent_id only so
// scout/validator style roles are not collapsed away.
func researchPresenceMembersFromRunFleet(members []researchrun.FleetMember) []researchPresenceMember {
	out := make([]researchPresenceMember, 0, len(members))
	seen := map[string]struct{}{}
	for _, m := range members {
		if strings.EqualFold(strings.TrimSpace(m.Status), "archived") {
			continue
		}
		aid := strings.TrimSpace(m.AgentID)
		if aid == "" {
			continue
		}
		if _, ok := seen[aid]; ok {
			continue
		}
		seen[aid] = struct{}{}
		out = append(out, researchPresenceMember{
			AgentID: aid,
			Role:    m.Role,
		})
	}
	return out
}
