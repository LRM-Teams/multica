package handler

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

// Stable UUID namespace for run-v2 → canvas graph projection (LRM-1401).
// Same session + entity always yields the same node/edge IDs across replays.
var researchRunGraphNamespace = uuid.MustParse("6b1f0c2e-9a47-4d8f-b3e1-2f5a8c7d9041")

const (
	runGraphKindRoot     = "root"
	runGraphKindQuestion = "question"
	runGraphKindTask     = "task"
	runGraphKindAttempt  = "attempt"
	runGraphKindClaim    = "claim"
	runGraphKindGate     = "gate"
	runGraphKindEdge     = "edge"
)

// projectRunV2Graph maps canonical run-v2 ledgers onto research canvas nodes/edges.
// Event-log rows are not used — this is the research-semantic truth surface.
// Deterministic for a given RunSnapshot (same state_version → same IDs, edges, order).
func projectRunV2Graph(snap researchrun.RunSnapshot) (nodes []ResearchGraphNodeResp, edges []ResearchGraphEdgeResp) {
	sessionID := strings.TrimSpace(snap.Run.SessionID)
	if sessionID == "" {
		return nil, nil
	}
	ts := runGraphTimestamp(snap)
	phase := strings.TrimSpace(snap.Run.CurrentStage)

	rootID := runGraphNodeID(sessionID, runGraphKindRoot, sessionID)
	displayGoal := displayResearchGoal(firstNonEmpty(snap.Contract.Goal, snap.Run.Goal))
	rootTitle := strings.TrimSpace(snap.Run.Title)
	if rootTitle == "" || looksLikeResearchTemplate(rootTitle) {
		rootTitle = truncateRunes(displayGoal, 80)
	}
	if rootTitle == "" {
		rootTitle = "调研目标"
	}
	rootStatus := projectRunStatus(snap.Run.Status)
	rootPayload := runGraphPayload(map[string]any{
		"projection":        "run_v2",
		"kind":              runGraphKindRoot,
		"created_by":        nullIfEmpty(snap.Run.CreatedBy),
		"state_version":     snap.Run.StateVersion,
		"goal_version":      snap.Run.GoalVersion,
		"plan_version":      snap.Run.PlanVersion,
		"phase":             phase,
		"run_status":        string(snap.Run.Status),
		"run_stats":         snap.Run.Stats,
		"run_config":        snap.Run.Config,
		"next_reconcile_at": snap.Run.NextReconcileAt,
		"stop_reason":       nullIfEmpty(snap.Run.StopReason),
		"last_error":        nullIfEmpty(snap.Run.LastError),
		"contract":          snap.Contract,
		"method":            snap.Method,
		"content": map[string]any{
			"goal": displayGoal,
		},
	})
	nodes = append(nodes, ResearchGraphNodeResp{
		ID:           rootID,
		SessionID:    sessionID,
		NodeType:     "goal",
		Title:        rootTitle,
		Summary:      truncateRunes(displayGoal, 240),
		Status:       rootStatus,
		ActorAgentID: nil,
		Payload:      rootPayload,
		ThemeKey:     "type:goal",
		Phase:        phase,
		Assessment:   researchAssessmentPendingReview,
		Content:      ResearchNodeContentFaces{Goal: displayGoal},
		ChildIDs:     []string{},
		CreatedAt:    ts,
		UpdatedAt:    ts,
	})

	taskByID := make(map[string]researchrun.Task, len(snap.Tasks))
	for _, task := range snap.Tasks {
		taskByID[task.ID] = task
	}
	questionIDs := map[string]string{}
	questions := append([]researchrun.Question(nil), snap.Questions...)
	sort.SliceStable(questions, func(i, j int) bool {
		if questions[i].Priority != questions[j].Priority {
			return questions[i].Priority > questions[j].Priority
		}
		return questions[i].ID < questions[j].ID
	})
	for _, q := range questions {
		id := strings.TrimSpace(q.ID)
		if id == "" {
			continue
		}
		nodeID := runGraphNodeID(sessionID, runGraphKindQuestion, id)
		questionIDs[id] = nodeID
		title := strings.TrimSpace(q.Question)
		if title == "" {
			title = "调研问题"
		}
		status := projectQuestionStatus(q.Status)
		var actor *string
		if task, ok := taskByID[q.CreatedByTaskID]; ok {
			if aid := strings.TrimSpace(task.AssignedAgentID); aid != "" {
				actor = &aid
			}
		}
		payload := runGraphPayload(map[string]any{
			"projection":           "run_v2",
			"kind":                 runGraphKindQuestion,
			"question_id":          id,
			"parent_question_id":   nullIfEmpty(q.ParentQuestionID),
			"created_by_task_id":   nullIfEmpty(q.CreatedByTaskID),
			"question_kind":        string(q.Kind),
			"question_status":      string(q.Status),
			"required":             q.Required,
			"priority":             q.Priority,
			"answer_claim_id":      nullIfEmpty(q.AnswerClaimID),
			"terminal_explanation": nullIfEmpty(q.TerminalExplanation),
			"phase":                phase,
			"theme_key":            "question:" + string(q.Kind),
			"content": map[string]any{
				"goal": title,
			},
		})
		nodes = append(nodes, ResearchGraphNodeResp{
			ID:           nodeID,
			SessionID:    sessionID,
			NodeType:     "subquestion",
			Title:        truncateRunes(title, 120),
			Summary:      truncateRunes(firstNonEmpty(q.TerminalExplanation, title), 240),
			Status:       status,
			ActorAgentID: actor,
			Payload:      payload,
			ThemeKey:     "question:" + string(q.Kind),
			Phase:        phase,
			Assessment:   researchAssessmentPendingReview,
			Content:      ResearchNodeContentFaces{Goal: title},
			ChildIDs:     []string{},
			CreatedAt:    ts,
			UpdatedAt:    ts,
		})
	}

	taskIDs := map[string]string{}
	tasks := append([]researchrun.Task(nil), snap.Tasks...)
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].Priority != tasks[j].Priority {
			return tasks[i].Priority > tasks[j].Priority
		}
		return tasks[i].ID < tasks[j].ID
	})
	for _, task := range tasks {
		id := strings.TrimSpace(task.ID)
		if id == "" {
			continue
		}
		nodeID := runGraphNodeID(sessionID, runGraphKindTask, id)
		taskIDs[id] = nodeID
		title := strings.TrimSpace(task.Objective)
		if title == "" {
			title = string(task.Kind)
		}
		var actor *string
		if aid := strings.TrimSpace(task.AssignedAgentID); aid != "" {
			actor = &aid
		}
		status := projectTaskStatus(task.Status)
		nodeType := "probe"
		if task.Status == researchrun.TaskStatusFailed || task.Status == researchrun.TaskStatusBlocked {
			nodeType = "dead_end"
		} else if task.Status == researchrun.TaskStatusSucceeded {
			nodeType = "finding"
		}
		payload := runGraphPayload(map[string]any{
			"projection":          "run_v2",
			"kind":                runGraphKindTask,
			"task_id":             id,
			"question_id":         nullIfEmpty(task.QuestionID),
			"parent_task_id":      nullIfEmpty(task.ParentTaskID),
			"task_kind":           string(task.Kind),
			"task_status":         string(task.Status),
			"assigned_agent_id":   nullIfEmpty(task.AssignedAgentID),
			"attempt_count":       task.AttemptCount,
			"max_attempts":        task.MaxAttempts,
			"expected_result":     task.ExpectedResult,
			"acceptance_criteria": json.RawMessage(task.AcceptanceCriteria),
			"required_capability": task.RequiredCapability,
			"terminal_reason":     nullIfEmpty(task.TerminalReason),
			"ready_at":            task.ReadyAt,
			"started_at":          task.StartedAt,
			"completed_at":        task.CompletedAt,
			"phase":               phase,
			"details": map[string]any{
				"task_id":           id,
				"question_id":       nullIfEmpty(task.QuestionID),
				"parent_task_id":    nullIfEmpty(task.ParentTaskID),
				"agent_id":          nullIfEmpty(task.AssignedAgentID),
				"task_kind":         string(task.Kind),
				"assigned_agent_id": nullIfEmpty(task.AssignedAgentID),
			},
			"content": map[string]any{
				"goal":               truncateRunes(title, 240),
				"operation_approach": string(task.Kind),
				"research_approach":  task.RequiredCapability,
			},
		})
		nodes = append(nodes, ResearchGraphNodeResp{
			ID:           nodeID,
			SessionID:    sessionID,
			NodeType:     nodeType,
			Title:        truncateRunes(title, 120),
			Summary:      truncateRunes(firstNonEmpty(task.TerminalReason, task.ExpectedResult, title), 240),
			Status:       status,
			ActorAgentID: actor,
			Payload:      payload,
			ThemeKey:     "task:" + string(task.Kind),
			Phase:        phase,
			Assessment:   researchAssessmentPendingReview,
			Content: ResearchNodeContentFaces{
				Goal:              truncateRunes(title, 240),
				OperationApproach: string(task.Kind),
				ResearchApproach:  task.RequiredCapability,
			},
			ChildIDs:  []string{},
			CreatedAt: ts,
			UpdatedAt: ts,
		})
	}

	attempts := append([]researchrun.Attempt(nil), snap.Attempts...)
	sort.SliceStable(attempts, func(i, j int) bool {
		if attempts[i].TaskID != attempts[j].TaskID {
			return attempts[i].TaskID < attempts[j].TaskID
		}
		if attempts[i].AttemptNumber != attempts[j].AttemptNumber {
			return attempts[i].AttemptNumber < attempts[j].AttemptNumber
		}
		return attempts[i].ID < attempts[j].ID
	})
	attemptNodeIDs := map[string]string{}
	for _, attempt := range attempts {
		id := strings.TrimSpace(attempt.ID)
		if id == "" {
			continue
		}
		nodeID := runGraphNodeID(sessionID, runGraphKindAttempt, id)
		attemptNodeIDs[id] = nodeID
		var actor *string
		if aid := strings.TrimSpace(attempt.AssignedAgentID); aid != "" {
			actor = &aid
		}
		status := projectAttemptStatus(attempt.Status)
		nodeType := "probe"
		title := "尝试 #" + strconv.Itoa(attempt.AttemptNumber)
		summary := strings.TrimSpace(attempt.Diagnostics)
		if attempt.Status == researchrun.AttemptStatusFailed || attempt.Status == researchrun.AttemptStatusLost {
			nodeType = "dead_end"
			title = "尝试失败 #" + strconv.Itoa(attempt.AttemptNumber)
			if summary == "" {
				summary = strings.TrimSpace(attempt.FailureClass)
			}
		} else if attempt.Status == researchrun.AttemptStatusSucceeded {
			nodeType = "finding"
			title = "尝试成功 #" + strconv.Itoa(attempt.AttemptNumber)
		}
		payload := runGraphPayload(map[string]any{
			"projection":                  "run_v2",
			"kind":                        runGraphKindAttempt,
			"attempt_id":                  id,
			"task_id":                     attempt.TaskID,
			"attempt_number":              attempt.AttemptNumber,
			"attempt_status":              string(attempt.Status),
			"assigned_agent_id":           nullIfEmpty(attempt.AssignedAgentID),
			"inbox_task_id":               nullIfEmpty(attempt.InboxTaskID),
			"dispatch_key":                nullIfEmpty(attempt.DispatchKey),
			"execution_target":            attempt.ExecutionTarget,
			"task_objective":              taskByID[attempt.TaskID].Objective,
			"task_expected_result":        taskByID[attempt.TaskID].ExpectedResult,
			"task_acceptance_criteria":    json.RawMessage(taskByID[attempt.TaskID].AcceptanceCriteria),
			"result_hash":                 nullIfEmpty(attempt.ResultHash),
			"failure_class":               nullIfEmpty(attempt.FailureClass),
			"source_failure_reason":       nullIfEmpty(attempt.SourceFailureReason),
			"diagnostics":                 nullIfEmpty(attempt.Diagnostics),
			"pending_failure_class":       nullIfEmpty(attempt.PendingFailure),
			"pending_failure_diagnostics": nullIfEmpty(attempt.PendingDiagnostics),
			"pending_failure_retryable":   attempt.PendingRetryable,
			"dispatched_at":               attempt.DispatchedAt,
			"runtime_started_at":          attempt.RuntimeStartedAt,
			"runtime_last_observed_at":    attempt.RuntimeObservedAt,
			"runtime_lease_expires_at":    attempt.RuntimeLeaseUntil,
			"cancellation_requested_at":   attempt.CancelRequestedAt,
			"cancellation_completed_at":   attempt.CancelCompletedAt,
			"result_submitted_at":         attempt.ResultSubmittedAt,
			"completed_at":                attempt.CompletedAt,
			"phase":                       phase,
			"details": map[string]any{
				"task_id":                     attempt.TaskID,
				"attempt_id":                  id,
				"agent_id":                    nullIfEmpty(attempt.AssignedAgentID),
				"assigned_agent_id":           nullIfEmpty(attempt.AssignedAgentID),
				"inbox_task_id":               nullIfEmpty(attempt.InboxTaskID),
				"dispatch_key":                nullIfEmpty(attempt.DispatchKey),
				"execution_target":            attempt.ExecutionTarget,
				"failure_class":               nullIfEmpty(attempt.FailureClass),
				"source_failure_reason":       nullIfEmpty(attempt.SourceFailureReason),
				"diagnostics":                 nullIfEmpty(attempt.Diagnostics),
				"pending_failure_class":       nullIfEmpty(attempt.PendingFailure),
				"pending_failure_diagnostics": nullIfEmpty(attempt.PendingDiagnostics),
				"pending_failure_retryable":   attempt.PendingRetryable,
				"dispatched_at":               attempt.DispatchedAt,
				"runtime_started_at":          attempt.RuntimeStartedAt,
				"runtime_last_observed_at":    attempt.RuntimeObservedAt,
				"runtime_lease_expires_at":    attempt.RuntimeLeaseUntil,
				"cancellation_requested_at":   attempt.CancelRequestedAt,
				"cancellation_completed_at":   attempt.CancelCompletedAt,
				"result_submitted_at":         attempt.ResultSubmittedAt,
				"completed_at":                attempt.CompletedAt,
			},
		})
		nodes = append(nodes, ResearchGraphNodeResp{
			ID:           nodeID,
			SessionID:    sessionID,
			NodeType:     nodeType,
			Title:        title,
			Summary:      truncateRunes(summary, 240),
			Status:       status,
			ActorAgentID: actor,
			Payload:      payload,
			ThemeKey:     "type:" + nodeType,
			Phase:        phase,
			Assessment:   researchAssessmentPendingReview,
			ChildIDs:     []string{},
			CreatedAt:    ts,
			UpdatedAt:    ts,
		})
	}

	claims := append([]researchrun.Claim(nil), snap.Claims...)
	sort.SliceStable(claims, func(i, j int) bool {
		if !claims[i].CreatedAt.Equal(claims[j].CreatedAt) {
			return claims[i].CreatedAt.Before(claims[j].CreatedAt)
		}
		return claims[i].ID < claims[j].ID
	})
	claimNodeIDs := map[string]string{}
	for _, claim := range claims {
		id := strings.TrimSpace(claim.ID)
		if id == "" {
			continue
		}
		nodeID := runGraphNodeID(sessionID, runGraphKindClaim, id)
		claimNodeIDs[id] = nodeID
		nodeType := "finding"
		status := "done"
		switch claim.Status {
		case researchrun.ClaimStatusDisputed, researchrun.ClaimStatusUnresolved:
			nodeType = "conflict"
			status = "active"
		case researchrun.ClaimStatusRefuted:
			nodeType = "refuted"
			status = "abandoned"
		case researchrun.ClaimStatusSuperseded:
			nodeType = "pivot"
			status = "done"
		case researchrun.ClaimStatusProposed:
			status = "pending"
		}
		title := strings.TrimSpace(claim.Text)
		if title == "" {
			title = "结论"
		}
		conf := claim.Confidence
		var actor *string
		if task, ok := taskByID[claim.ProducedByTaskID]; ok {
			if aid := strings.TrimSpace(task.AssignedAgentID); aid != "" {
				actor = &aid
			}
		}
		payload := runGraphPayload(map[string]any{
			"projection":            "run_v2",
			"kind":                  runGraphKindClaim,
			"claim_id":              id,
			"claim_status":          string(claim.Status),
			"produced_by_task_id":   nullIfEmpty(claim.ProducedByTaskID),
			"confidence":            conf,
			"significance":          nullIfEmpty(claim.Significance),
			"resolution":            nullIfEmpty(claim.Resolution),
			"evidence_standard_key": nullIfEmpty(claim.EvidenceStandardKey),
			"evidence":              claim.Evidence,
			"phase":                 phase,
			"assessment":            claimAssessment(claim.Status),
			"content": map[string]any{
				"result": truncateRunes(title, 240),
			},
			"details": map[string]any{
				"task_id": nullIfEmpty(claim.ProducedByTaskID),
			},
		})
		nodes = append(nodes, ResearchGraphNodeResp{
			ID:           nodeID,
			SessionID:    sessionID,
			NodeType:     nodeType,
			Title:        truncateRunes(title, 120),
			Summary:      truncateRunes(firstNonEmpty(claim.Resolution, claim.Significance, title), 240),
			Status:       status,
			ActorAgentID: actor,
			Payload:      payload,
			Confidence:   &conf,
			ThemeKey:     "type:" + nodeType,
			Phase:        phase,
			Assessment:   claimAssessment(claim.Status),
			Content:      ResearchNodeContentFaces{Result: truncateRunes(title, 240)},
			ChildIDs:     []string{},
			CreatedAt:    ts,
			UpdatedAt:    ts,
		})
	}

	gateID := ""
	if shouldProjectGate(snap) {
		gateID = runGraphNodeID(sessionID, runGraphKindGate, "quality")
		gateStatus := "pending"
		gateTitle := "质量门槛"
		gateSummary := "等待门槛评估"
		if snap.Gate.Passed {
			gateStatus = "done"
			gateTitle = "质量门槛已通过"
			gateSummary = "交付门槛已满足"
		} else if len(snap.Gate.Findings) > 0 {
			gateStatus = "failed"
			gateTitle = "质量门槛未通过"
			gateSummary = snap.Gate.Findings[0].Message
		}
		findings := make([]map[string]any, 0, len(snap.Gate.Findings))
		for _, f := range snap.Gate.Findings {
			findings = append(findings, map[string]any{
				"code":     f.Code,
				"severity": f.Severity,
				"message":  f.Message,
			})
		}
		payload := runGraphPayload(map[string]any{
			"projection": "run_v2",
			"kind":       runGraphKindGate,
			"gate":       map[string]any{"passed": snap.Gate.Passed, "findings": findings},
			"claim_ids":  sortedRunGraphClaimIDs(claims),
			"phase":      phase,
		})
		nodes = append(nodes, ResearchGraphNodeResp{
			ID:         gateID,
			SessionID:  sessionID,
			NodeType:   "stage_gate",
			Title:      gateTitle,
			Summary:    truncateRunes(gateSummary, 240),
			Status:     gateStatus,
			Payload:    payload,
			ThemeKey:   "type:stage_gate",
			Phase:      phase,
			Assessment: researchAssessmentPendingReview,
			ChildIDs:   []string{},
			CreatedAt:  ts,
			UpdatedAt:  ts,
		})
	}

	nodeIndex := map[string]int{}
	for i := range nodes {
		nodeIndex[nodes[i].ID] = i
	}

	addEdge := func(fromID, toID, edgeType string) {
		if fromID == "" || toID == "" || fromID == toID {
			return
		}
		if _, ok := nodeIndex[fromID]; !ok {
			return
		}
		if _, ok := nodeIndex[toID]; !ok {
			return
		}
		edgeID := runGraphEdgeID(sessionID, edgeType, fromID, toID)
		edges = append(edges, ResearchGraphEdgeResp{
			ID:         edgeID,
			SessionID:  sessionID,
			FromNodeID: fromID,
			ToNodeID:   toID,
			EdgeType:   edgeType,
			CreatedAt:  ts,
		})
	}

	// Question tree: parent_question_id → leads_to; else root → question.
	for _, q := range questions {
		toID := questionIDs[q.ID]
		if toID == "" {
			continue
		}
		fromID := rootID
		if parent := strings.TrimSpace(q.ParentQuestionID); parent != "" {
			if pid, ok := questionIDs[parent]; ok {
				fromID = pid
			}
		}
		addEdge(fromID, toID, researchTreeEdgeType)
	}

	// Tasks: parent_task_id → leads_to; else question → task; else root → task.
	for _, task := range tasks {
		toID := taskIDs[task.ID]
		if toID == "" {
			continue
		}
		fromID := rootID
		if parent := strings.TrimSpace(task.ParentTaskID); parent != "" {
			if pid, ok := taskIDs[parent]; ok {
				fromID = pid
			}
		} else if qid := strings.TrimSpace(task.QuestionID); qid != "" {
			if pid, ok := questionIDs[qid]; ok {
				fromID = pid
			}
		}
		addEdge(fromID, toID, researchTreeEdgeType)
	}

	// Attempts hang under their task.
	for _, attempt := range attempts {
		toID := attemptNodeIDs[attempt.ID]
		fromID := taskIDs[attempt.TaskID]
		addEdge(fromID, toID, researchTreeEdgeType)
	}

	// Claims hang under producing task, else root.
	for _, claim := range claims {
		toID := claimNodeIDs[claim.ID]
		fromID := rootID
		if tid := strings.TrimSpace(claim.ProducedByTaskID); tid != "" {
			if pid, ok := taskIDs[tid]; ok {
				fromID = pid
			}
		}
		addEdge(fromID, toID, researchTreeEdgeType)
	}

	if gateID != "" {
		addEdge(rootID, gateID, researchTreeEdgeType)
	}

	sort.SliceStable(edges, func(i, j int) bool {
		if edges[i].FromNodeID != edges[j].FromNodeID {
			return edges[i].FromNodeID < edges[j].FromNodeID
		}
		if edges[i].ToNodeID != edges[j].ToNodeID {
			return edges[i].ToNodeID < edges[j].ToNodeID
		}
		if edges[i].EdgeType != edges[j].EdgeType {
			return edges[i].EdgeType < edges[j].EdgeType
		}
		return edges[i].ID < edges[j].ID
	})

	applyRunGraphTreeFields(nodes, edges)
	return nodes, edges
}

func sortedRunGraphClaimIDs(claims []researchrun.Claim) []string {
	ids := make([]string, 0, len(claims))
	for _, claim := range claims {
		if id := strings.TrimSpace(claim.ID); id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func applyRunGraphTreeFields(nodes []ResearchGraphNodeResp, edges []ResearchGraphEdgeResp) {
	parentOf := map[string]string{}
	childrenOf := map[string][]string{}
	for _, e := range edges {
		if e.EdgeType != researchTreeEdgeType {
			continue
		}
		if _, exists := parentOf[e.ToNodeID]; exists {
			continue
		}
		parentOf[e.ToNodeID] = e.FromNodeID
		childrenOf[e.FromNodeID] = append(childrenOf[e.FromNodeID], e.ToNodeID)
	}
	for i := range nodes {
		id := nodes[i].ID
		if p, ok := parentOf[id]; ok {
			parent := p
			nodes[i].ParentID = &parent
		} else {
			nodes[i].ParentID = nil
		}
		kids := childrenOf[id]
		if kids == nil {
			kids = []string{}
		}
		nodes[i].ChildIDs = kids
		nodes[i].ChildCount = len(kids)
		nodes[i].DescendantCount = countResearchDescendants(id, childrenOf, nil)
	}
}

func shouldProjectGate(snap researchrun.RunSnapshot) bool {
	if snap.Gate.Passed || len(snap.Gate.Findings) > 0 {
		return true
	}
	switch snap.Run.Status {
	case researchrun.RunStatusAwaitingUserConfirm, researchrun.RunStatusCompleted:
		return true
	default:
		return false
	}
}

func runGraphNodeID(sessionID, kind, entityID string) string {
	return uuid.NewSHA1(researchRunGraphNamespace, []byte("node|"+sessionID+"|"+kind+"|"+entityID)).String()
}

func runGraphEdgeID(sessionID, edgeType, fromID, toID string) string {
	return uuid.NewSHA1(researchRunGraphNamespace, []byte("edge|"+sessionID+"|"+edgeType+"|"+fromID+"|"+toID)).String()
}

func runGraphTimestamp(snap researchrun.RunSnapshot) string {
	t := snap.Run.LastProgressAt
	if t.IsZero() && snap.Run.InitializedAt != nil {
		t = *snap.Run.InitializedAt
	}
	if t.IsZero() {
		// Bound to state_version so identical snapshots stay byte-stable.
		t = time.Unix(0, snap.Run.StateVersion).UTC()
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func runGraphPayload(v map[string]any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}")
	}
	return raw
}

func displayResearchGoal(goal string) string {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return ""
	}
	markers := []string{
		"用户具体目标：",
		"用户具体目标:",
		"User-specific goal：",
		"User-specific goal:",
	}
	for _, marker := range markers {
		if i := strings.LastIndex(goal, marker); i >= 0 {
			extracted := strings.TrimSpace(goal[i+len(marker):])
			if extracted != "" {
				return extracted
			}
		}
	}
	if looksLikeResearchTemplate(goal) {
		// Prefer the last non-empty line as the concrete ask.
		lines := strings.Split(goal, "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			line := strings.TrimSpace(lines[i])
			if line == "" || strings.HasPrefix(line, "【") || strings.HasPrefix(line, "[") {
				continue
			}
			return truncateRunes(line, 240)
		}
	}
	return truncateRunes(goal, 240)
}

func looksLikeResearchTemplate(text string) bool {
	if len([]rune(text)) < 400 {
		return false
	}
	return strings.Contains(text, "用户具体目标") ||
		strings.Contains(text, "User-specific goal") ||
		strings.Contains(text, "【决策问题】") ||
		strings.Contains(text, "[Decision question]")
}

func projectRunStatus(status researchrun.RunStatus) string {
	switch status {
	case researchrun.RunStatusRunning:
		return "running"
	case researchrun.RunStatusDrafting:
		return "pending"
	case researchrun.RunStatusAwaitingUserConfirm:
		return "waiting"
	case researchrun.RunStatusCompleted:
		return "done"
	case researchrun.RunStatusFailed:
		return "failed"
	case researchrun.RunStatusCancelled, researchrun.RunStatusArchived:
		return "abandoned"
	case researchrun.RunStatusPaused:
		return "waiting"
	default:
		return "active"
	}
}

func projectQuestionStatus(status researchrun.QuestionStatus) string {
	switch status {
	case researchrun.QuestionStatusOpen:
		return "pending"
	case researchrun.QuestionStatusInProgress:
		return "running"
	case researchrun.QuestionStatusAnswered:
		return "succeeded"
	case researchrun.QuestionStatusUnresolved:
		return "failed"
	case researchrun.QuestionStatusObsolete:
		return "abandoned"
	default:
		return "pending"
	}
}

func projectTaskStatus(status researchrun.TaskStatus) string {
	switch status {
	case researchrun.TaskStatusPending:
		return "pending"
	case researchrun.TaskStatusReady:
		return "ready"
	case researchrun.TaskStatusDispatching, researchrun.TaskStatusRunning:
		return "running"
	case researchrun.TaskStatusSucceeded:
		return "succeeded"
	case researchrun.TaskStatusFailed, researchrun.TaskStatusBlocked:
		return "failed"
	case researchrun.TaskStatusObsolete, researchrun.TaskStatusCancelled:
		return "abandoned"
	default:
		return string(status)
	}
}

func projectAttemptStatus(status researchrun.AttemptStatus) string {
	switch status {
	case researchrun.AttemptStatusDispatching, researchrun.AttemptStatusRunning, researchrun.AttemptStatusCancelling:
		return "running"
	case researchrun.AttemptStatusSucceeded:
		return "succeeded"
	case researchrun.AttemptStatusFailed, researchrun.AttemptStatusLost:
		return "failed"
	case researchrun.AttemptStatusCancelled:
		return "abandoned"
	default:
		return string(status)
	}
}

func claimAssessment(status researchrun.ClaimStatus) string {
	switch status {
	case researchrun.ClaimStatusSupported:
		return researchAssessmentTrusted
	case researchrun.ClaimStatusRefuted, researchrun.ClaimStatusDisputed:
		return researchAssessmentDetour
	default:
		return researchAssessmentPendingReview
	}
}

func nullIfEmpty(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

// projectRunV2TypedGraph maps the canonical run-v2 ledger projection onto the
// LRM-1505 typed graph contract. Snapshot and GET /graph/typed must share this
// surface so D5 never sees divergent node IDs or graph_version values.
func projectRunV2TypedGraph(
	snap researchrun.RunSnapshot,
	limit, offset int,
	paginated bool,
) ResearchGraphTypedResp {
	sessionID := strings.TrimSpace(snap.Run.SessionID)
	canvasNodes, canvasEdges := projectRunV2Graph(snap)
	graphVersion := snap.Run.StateVersion
	if graphVersion < 0 {
		graphVersion = 0
	}

	round := int32(1)
	if snap.Run.GoalVersion > 0 {
		round = int32(snap.Run.GoalVersion)
	}
	var goalVersionID *string
	if snap.Run.GoalVersion > 0 {
		gv := strconv.Itoa(snap.Run.GoalVersion)
		goalVersionID = &gv
	}

	parentOf, _ := buildResearchTreeIndexFromRespEdges(canvasEdges)
	clusterByTheme := buildRunV2TypedClusters(sessionID, canvasNodes, goalVersionID, round)

	typedNodes := make([]ResearchGraphTypedNodeResp, 0, len(canvasNodes))
	for _, n := range canvasNodes {
		kind := runGraphPayloadKind(n.Payload)
		level := runV2TypedLevel(n, kind)
		var clusterID *string
		if theme := strings.TrimSpace(n.ThemeKey); theme != "" && theme != "type:goal" {
			id := runGraphClusterID(sessionID, theme)
			clusterID = &id
		}
		docCount := int32(0)
		conclusionCount := int32(0)
		if kind == runGraphKindClaim && n.NodeType == "finding" {
			conclusionCount = 1
		}
		childIDs := n.ChildIDs
		if childIDs == nil {
			childIDs = []string{}
		}
		var parentID *string
		if p := parentOf[n.ID]; p != "" {
			parentID = &p
		}
		typedNodes = append(typedNodes, ResearchGraphTypedNodeResp{
			ID:              n.ID,
			SessionID:       sessionID,
			NodeType:        n.NodeType,
			Title:           n.Title,
			Summary:         n.Summary,
			Status:          n.Status,
			ActorAgentID:    n.ActorAgentID,
			Payload:         n.Payload,
			Level:           level,
			Round:           round,
			ClusterID:       clusterID,
			Confidence:      n.Confidence,
			DocumentCount:   docCount,
			ConclusionCount: conclusionCount,
			GoalVersionID:   goalVersionID,
			MergedFrom:      []string{},
			ChildIDs:        childIDs,
			ChildrenOf:      []string{},
			ParentID:        parentID,
			CreatedAt:       n.CreatedAt,
			UpdatedAt:       n.UpdatedAt,
		})
	}

	lineage := ResearchGraphLineageResp{
		Derived:     map[string]string{},
		Merged:      map[string][]string{},
		Superseded:  map[string]string{},
		Restarted:   map[string]string{},
		Invalidated: map[string]string{},
		Supersedes:  map[string][]string{},
	}

	clusters := make([]ResearchGraphClusterResp, 0, len(clusterByTheme))
	for _, c := range clusterByTheme {
		clusters = append(clusters, c)
	}
	sort.SliceStable(clusters, func(i, j int) bool { return clusters[i].ID < clusters[j].ID })

	totalCount := int64(len(typedNodes))
	pageNodes := typedNodes
	pageEdges := canvasEdges
	pageClusters := clusters
	if paginated {
		if offset > len(typedNodes) {
			offset = len(typedNodes)
		}
		end := offset + limit
		if end > len(typedNodes) {
			end = len(typedNodes)
		}
		pageNodes = typedNodes[offset:end]
		nodeIDs := runV2TypedNodeIDSet(pageNodes)
		pageEdges = filterRunV2TypedEdges(canvasEdges, nodeIDs)
		pageClusters = filterRunV2TypedClusters(clusters, pageNodes)
	}

	resp := ResearchGraphTypedResp{
		SessionID:    sessionID,
		GraphVersion: graphVersion,
		Nodes:        pageNodes,
		Edges:        pageEdges,
		Clusters:     pageClusters,
		Lineage:      lineage,
	}
	if paginated {
		resp.TotalNodeCount = &totalCount
	}
	return resp
}

func runGraphPayloadKind(payload json.RawMessage) string {
	obj := payloadObjectFromRaw(payload)
	kind, _ := obj["kind"].(string)
	return strings.TrimSpace(kind)
}

func payloadObjectFromRaw(payload json.RawMessage) map[string]any {
	if len(payload) == 0 {
		return map[string]any{}
	}
	var obj map[string]any
	if err := json.Unmarshal(payload, &obj); err != nil || obj == nil {
		return map[string]any{}
	}
	return obj
}

func runV2TypedLevel(node ResearchGraphNodeResp, kind string) string {
	switch kind {
	case runGraphKindRoot:
		return "XXL"
	case runGraphKindQuestion:
		return "M"
	case runGraphKindTask, runGraphKindAttempt:
		if node.ActorAgentID != nil && strings.TrimSpace(*node.ActorAgentID) != "" {
			return "S"
		}
		return "M"
	case runGraphKindClaim:
		switch node.NodeType {
		case "finding":
			return "L"
		default:
			return "M"
		}
	case runGraphKindGate:
		return "L"
	}
	switch node.NodeType {
	case "goal":
		return "XXL"
	case "subquestion":
		return "M"
	case "finding", "stage_gate":
		return "L"
	case "probe", "dead_end":
		if node.ActorAgentID != nil && strings.TrimSpace(*node.ActorAgentID) != "" {
			return "S"
		}
		return "M"
	default:
		return "M"
	}
}

func runGraphClusterID(sessionID, themeKey string) string {
	return uuid.NewSHA1(researchRunGraphNamespace, []byte("cluster|"+sessionID+"|"+themeKey)).String()
}

func buildRunV2TypedClusters(
	sessionID string,
	nodes []ResearchGraphNodeResp,
	goalVersionID *string,
	round int32,
) map[string]ResearchGraphClusterResp {
	out := map[string]ResearchGraphClusterResp{}
	for _, n := range nodes {
		theme := strings.TrimSpace(n.ThemeKey)
		if theme == "" || theme == "type:goal" {
			continue
		}
		if _, ok := out[theme]; ok {
			continue
		}
		id := runGraphClusterID(sessionID, theme)
		label := theme
		if i := strings.Index(label, ":"); i >= 0 && i+1 < len(label) {
			label = label[i+1:]
		}
		clusterType := "topic"
		if i := strings.Index(theme, ":"); i > 0 {
			clusterType = theme[:i]
		}
		out[theme] = ResearchGraphClusterResp{
			ID:            id,
			SessionID:     sessionID,
			Name:          theme,
			Label:         label,
			Level:         "M",
			ClusterType:   clusterType,
			GoalVersionID: goalVersionID,
			Payload:       runGraphPayload(map[string]any{"theme_key": theme, "round": round}),
			CreatedAt:     n.CreatedAt,
			UpdatedAt:     n.UpdatedAt,
		}
	}
	return out
}

func buildResearchTreeIndexFromRespEdges(edges []ResearchGraphEdgeResp) (parentOf map[string]string, childrenOf map[string][]string) {
	parentOf = map[string]string{}
	childrenOf = map[string][]string{}
	for _, e := range edges {
		if e.EdgeType != researchTreeEdgeType {
			continue
		}
		if _, exists := parentOf[e.ToNodeID]; exists {
			continue
		}
		parentOf[e.ToNodeID] = e.FromNodeID
		childrenOf[e.FromNodeID] = append(childrenOf[e.FromNodeID], e.ToNodeID)
	}
	return parentOf, childrenOf
}

func runV2TypedNodeIDSet(nodes []ResearchGraphTypedNodeResp) map[string]struct{} {
	ids := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		if node.ID != "" {
			ids[node.ID] = struct{}{}
		}
	}
	return ids
}

func filterRunV2TypedEdges(edges []ResearchGraphEdgeResp, nodeIDs map[string]struct{}) []ResearchGraphEdgeResp {
	if len(nodeIDs) == 0 {
		return nil
	}
	filtered := make([]ResearchGraphEdgeResp, 0, len(edges))
	for _, edge := range edges {
		if _, ok := nodeIDs[edge.FromNodeID]; !ok {
			continue
		}
		if _, ok := nodeIDs[edge.ToNodeID]; !ok {
			continue
		}
		filtered = append(filtered, edge)
	}
	return filtered
}

func filterRunV2TypedClusters(clusters []ResearchGraphClusterResp, nodes []ResearchGraphTypedNodeResp) []ResearchGraphClusterResp {
	if len(nodes) == 0 {
		return nil
	}
	clusterIDs := make(map[string]struct{})
	for _, node := range nodes {
		if node.ClusterID != nil && *node.ClusterID != "" {
			clusterIDs[*node.ClusterID] = struct{}{}
		}
	}
	if len(clusterIDs) == 0 {
		return nil
	}
	filtered := make([]ResearchGraphClusterResp, 0, len(clusters))
	for _, cluster := range clusters {
		if _, ok := clusterIDs[cluster.ID]; ok {
			filtered = append(filtered, cluster)
		}
	}
	return filtered
}
