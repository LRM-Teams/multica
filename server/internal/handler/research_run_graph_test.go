package handler

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/researchrun"
)

func TestProjectRunV2GraphZeroTasks(t *testing.T) {
	snap := researchrun.RunSnapshot{
		Run: researchrun.Run{
			SessionID:      "11111111-1111-1111-1111-111111111111",
			Title:          "决策：是否进入东南亚",
			Goal:           "评估东南亚市场进入时机",
			Status:         researchrun.RunStatusRunning,
			CurrentStage:   "s1_plan",
			StateVersion:   3,
			LastProgressAt: time.Unix(1_700_000_000, 0).UTC(),
		},
		Contract: researchrun.ResearchContract{Goal: "评估东南亚市场进入时机"},
		Questions: []researchrun.Question{
			{ID: "q1", Question: "本地法规风险？", Status: researchrun.QuestionStatusOpen, Priority: 1, Kind: researchrun.QuestionKindDimension},
		},
	}
	nodes, edges := projectRunV2Graph(snap)
	if len(nodes) < 2 {
		t.Fatalf("nodes=%d, want root+question", len(nodes))
	}
	assertEdgesValid(t, nodes, edges)
	if len(edges) == 0 {
		t.Fatal("expected root→question edge")
	}
	byType := countNodeTypes(nodes)
	if byType["goal"] != 1 || byType["subquestion"] != 1 {
		t.Fatalf("node types=%v", byType)
	}
	for _, n := range nodes {
		if payloadKind(n) == "task" || payloadKind(n) == "attempt" {
			t.Fatalf("unexpected task/attempt node with zero tasks: %+v", n)
		}
	}
}

func TestProjectRunV2GraphParallelTasks(t *testing.T) {
	sessionID := "22222222-2222-2222-2222-222222222222"
	agentA := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	agentB := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	snap := researchrun.RunSnapshot{
		Run: researchrun.Run{
			SessionID:      sessionID,
			Title:          "并行取证",
			Goal:           "并行验证两条线索",
			Status:         researchrun.RunStatusRunning,
			CurrentStage:   "s2_sources",
			StateVersion:   12,
			LastProgressAt: time.Unix(1_700_000_100, 0).UTC(),
		},
		Contract: researchrun.ResearchContract{Goal: "并行验证两条线索"},
		Questions: []researchrun.Question{
			{ID: "q1", Question: "价格证据充分吗？", Status: researchrun.QuestionStatusInProgress, Priority: 2, Kind: researchrun.QuestionKindGap},
		},
		Tasks: []researchrun.Task{
			{ID: "t1", QuestionID: "q1", Objective: "抓取公开报价", Kind: researchrun.TaskKindDiscover, Status: researchrun.TaskStatusRunning, AssignedAgentID: agentA, Priority: 2},
			{ID: "t2", QuestionID: "q1", Objective: "交叉核验渠道", Kind: researchrun.TaskKindVerify, Status: researchrun.TaskStatusReady, AssignedAgentID: agentB, Priority: 1},
		},
	}
	nodes, edges := projectRunV2Graph(snap)
	assertEdgesValid(t, nodes, edges)
	running := findNodeByPayload(t, nodes, "task_id", "t1")
	if running.Status != "running" {
		t.Fatalf("running task status=%q", running.Status)
	}
	if running.ActorAgentID == nil || *running.ActorAgentID != agentA {
		t.Fatalf("running actor=%v, want %s", running.ActorAgentID, agentA)
	}
	ready := findNodeByPayload(t, nodes, "task_id", "t2")
	if ready.Status != "ready" {
		t.Fatalf("ready task status=%q", ready.Status)
	}
	q := findNodeByPayload(t, nodes, "question_id", "q1")
	if q.Status != "running" {
		t.Fatalf("question status=%q", q.Status)
	}
	// Both tasks under the same question.
	kids := map[string]bool{}
	for _, id := range q.ChildIDs {
		kids[id] = true
	}
	if !kids[running.ID] || !kids[ready.ID] {
		t.Fatalf("question children=%v, want both tasks", q.ChildIDs)
	}
}

func TestProjectRunV2GraphRetryAndFailedAttempt(t *testing.T) {
	sessionID := "33333333-3333-3333-3333-333333333333"
	agentID := "cccccccc-cccc-cccc-cccc-cccccccccccc"
	runtimeStartedAt := time.Unix(1_700_000_150, 0).UTC()
	runtimeObservedAt := time.Unix(1_700_000_180, 0).UTC()
	runtimeLeaseUntil := time.Unix(1_700_000_240, 0).UTC()
	cancelRequestedAt := time.Unix(1_700_000_190, 0).UTC()
	snap := researchrun.RunSnapshot{
		Run: researchrun.Run{
			SessionID:      sessionID,
			Title:          "重试路径",
			Goal:           "失败后重试",
			Status:         researchrun.RunStatusRunning,
			CurrentStage:   "s2_sources",
			StateVersion:   20,
			LastProgressAt: time.Unix(1_700_000_200, 0).UTC(),
		},
		Contract: researchrun.ResearchContract{Goal: "失败后重试"},
		Questions: []researchrun.Question{
			{ID: "q1", Question: "来源是否独立？", Status: researchrun.QuestionStatusInProgress, Priority: 1, Kind: researchrun.QuestionKindContradiction},
		},
		Tasks: []researchrun.Task{
			{ID: "t1", QuestionID: "q1", Objective: "深读报告", Kind: researchrun.TaskKindDeepRead, Status: researchrun.TaskStatusRunning, AssignedAgentID: agentID, AttemptCount: 2, MaxAttempts: 3, Priority: 1},
		},
		Attempts: []researchrun.Attempt{
			{ID: "a1", TaskID: "t1", AttemptNumber: 1, AssignedAgentID: agentID, Status: researchrun.AttemptStatusFailed, FailureClass: "result_not_submitted", Diagnostics: "missing structured result"},
			{
				ID: "a2", TaskID: "t1", AttemptNumber: 2, AssignedAgentID: agentID,
				InboxTaskID: "inbox-2", DispatchKey: "research:dispatch:a2", Status: researchrun.AttemptStatusCancelling,
				DispatchedAt: time.Unix(1_700_000_100, 0).UTC(), RuntimeStartedAt: &runtimeStartedAt,
				RuntimeObservedAt: &runtimeObservedAt, RuntimeLeaseUntil: &runtimeLeaseUntil,
				CancelRequestedAt: &cancelRequestedAt, PendingFailure: "task_timeout",
				PendingDiagnostics: "runtime exceeded 30 seconds", PendingRetryable: true,
			},
		},
	}
	nodes, edges := projectRunV2Graph(snap)
	assertEdgesValid(t, nodes, edges)
	failed := findNodeByPayload(t, nodes, "attempt_id", "a1")
	if failed.Status != "failed" || failed.NodeType != "dead_end" {
		t.Fatalf("failed attempt node type/status=%s/%s", failed.NodeType, failed.Status)
	}
	obj := payloadObject(failed.Payload)
	if obj["failure_class"] != "result_not_submitted" {
		t.Fatalf("failure_class=%v", obj["failure_class"])
	}
	details, _ := obj["details"].(map[string]any)
	if details["failure_class"] != "result_not_submitted" {
		t.Fatalf("details.failure_class=%v", details["failure_class"])
	}
	retry := findNodeByPayload(t, nodes, "attempt_id", "a2")
	if retry.Status != "running" {
		t.Fatalf("retry attempt status=%q", retry.Status)
	}
	retryPayload := payloadObject(retry.Payload)
	if retryPayload["attempt_status"] != "cancelling" || retryPayload["inbox_task_id"] != "inbox-2" || retryPayload["pending_failure_class"] != "task_timeout" {
		t.Fatalf("retry runtime payload=%v", retryPayload)
	}
	retryDetails, _ := retryPayload["details"].(map[string]any)
	if retryDetails["runtime_started_at"] != runtimeStartedAt.Format(time.RFC3339) || retryDetails["runtime_lease_expires_at"] != runtimeLeaseUntil.Format(time.RFC3339) {
		t.Fatalf("retry runtime details=%v", retryDetails)
	}
	if retryDetails["pending_failure_diagnostics"] != "runtime exceeded 30 seconds" || retryDetails["pending_failure_retryable"] != true {
		t.Fatalf("retry pending failure details=%v", retryDetails)
	}
	task := findNodeByPayload(t, nodes, "task_id", "t1")
	if len(task.ChildIDs) != 2 {
		t.Fatalf("task children=%v, want both attempts", task.ChildIDs)
	}
}

// Production regression: repeated failure events once expanded into many
// visually duplicated canvas nodes. Canonical projection must be unique and
// byte-stable when the same snapshot is replayed.
func TestProjectRunV2GraphDeterministicReplay(t *testing.T) {
	snap := fixtureSevenQuestionSession()
	aNodes, aEdges := projectRunV2Graph(snap)
	bNodes, bEdges := projectRunV2Graph(snap)
	assertUniqueGraphIdentities(t, aNodes, aEdges)
	assertUniqueGraphIdentities(t, bNodes, bEdges)
	if len(aNodes) != len(bNodes) || len(aEdges) != len(bEdges) {
		t.Fatalf("replay size mismatch nodes %d/%d edges %d/%d", len(aNodes), len(bNodes), len(aEdges), len(bEdges))
	}
	for i := range aNodes {
		if aNodes[i].ID != bNodes[i].ID || aNodes[i].Status != bNodes[i].Status || aNodes[i].Title != bNodes[i].Title {
			t.Fatalf("node[%d] mismatch: %+v vs %+v", i, aNodes[i], bNodes[i])
		}
		if string(aNodes[i].Payload) != string(bNodes[i].Payload) {
			t.Fatalf("node[%d] payload mismatch", i)
		}
	}
	for i := range aEdges {
		if aEdges[i] != bEdges[i] {
			t.Fatalf("edge[%d] mismatch: %+v vs %+v", i, aEdges[i], bEdges[i])
		}
	}
}

func assertUniqueGraphIdentities(t *testing.T, nodes []ResearchGraphNodeResp, edges []ResearchGraphEdgeResp) {
	t.Helper()
	nodeIDs := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		if node.ID == "" {
			t.Fatal("projected graph contains an empty node ID")
		}
		if _, duplicate := nodeIDs[node.ID]; duplicate {
			t.Fatalf("projected graph contains duplicate node ID %q", node.ID)
		}
		nodeIDs[node.ID] = struct{}{}
	}
	edgeIDs := make(map[string]struct{}, len(edges))
	for _, edge := range edges {
		if edge.ID == "" {
			t.Fatal("projected graph contains an empty edge ID")
		}
		if _, duplicate := edgeIDs[edge.ID]; duplicate {
			t.Fatalf("projected graph contains duplicate edge ID %q", edge.ID)
		}
		edgeIDs[edge.ID] = struct{}{}
	}
}

func TestProjectRunV2GraphSevenQuestionFixture(t *testing.T) {
	snap := fixtureSevenQuestionSession()
	nodes, edges := projectRunV2Graph(snap)
	assertEdgesValid(t, nodes, edges)
	if len(edges) == 0 {
		t.Fatal("fixture must produce edges>0")
	}
	questions := 0
	statuses := map[string]bool{}
	var root *ResearchGraphNodeResp
	for i := range nodes {
		n := &nodes[i]
		if n.NodeType == "goal" {
			root = n
			if strings.Contains(n.Title, "【决策问题】") || strings.Contains(n.Summary, "You are conducting") {
				t.Fatalf("root still carries full template: title=%q summary=%q", n.Title, n.Summary)
			}
			if !strings.Contains(n.Summary, "判断是否在 Q4 扩产") && !strings.Contains(n.Content.Goal, "判断是否在 Q4 扩产") {
				t.Fatalf("root should surface user goal, got summary=%q content=%+v", n.Summary, n.Content)
			}
		}
		if payloadKind(*n) == "question" {
			questions++
			statuses[n.Status] = true
			if n.ParentID == nil || *n.ParentID == "" {
				t.Fatalf("question %s missing parent", n.ID)
			}
		}
	}
	if root == nil {
		t.Fatal("missing decision root")
	}
	if questions != 7 {
		t.Fatalf("questions=%d, want 7", questions)
	}
	taskStatuses := map[string]bool{}
	for _, n := range nodes {
		if payloadKind(n) == "task" {
			taskStatuses[n.Status] = true
		}
	}
	for _, want := range []string{"running", "ready", "pending", "failed", "succeeded"} {
		if !taskStatuses[want] {
			t.Fatalf("missing distinguishable task status %q in %v", want, taskStatuses)
		}
	}
	running := findNodeByPayload(t, nodes, "task_id", "task-running")
	if running.ActorAgentID == nil || *running.ActorAgentID != "agent-runner" {
		t.Fatalf("running task actor=%v", running.ActorAgentID)
	}
	failedAttempt := findNodeByPayload(t, nodes, "attempt_id", "attempt-failed")
	if payloadObject(failedAttempt.Payload)["failure_class"] != "timeout" {
		t.Fatalf("failed attempt payload=%s", failedAttempt.Payload)
	}
}

func TestDisplayResearchGoalStripsTemplate(t *testing.T) {
	raw := strings.Repeat("方法说明。", 80) + "\n\n用户具体目标：\n判断是否在 Q4 扩产"
	got := displayResearchGoal(raw)
	if got != "判断是否在 Q4 扩产" {
		t.Fatalf("got %q", got)
	}
	en := strings.Repeat("Method notes. ", 80) + "\n\nUser-specific goal:\nShip or wait?"
	if displayResearchGoal(en) != "Ship or wait?" {
		t.Fatalf("en got %q", displayResearchGoal(en))
	}
}

func fixtureSevenQuestionSession() researchrun.RunSnapshot {
	sessionID := "d3cb52ae-bb85-4731-91d7-30c779063770"
	template := strings.Repeat("【决策问题】模板方法。", 40) + "\n\n用户具体目标：\n判断是否在 Q4 扩产"
	return researchrun.RunSnapshot{
		Run: researchrun.Run{
			SessionID:      sessionID,
			Title:          "扩产决策调研",
			Goal:           template,
			Status:         researchrun.RunStatusRunning,
			CurrentStage:   "s2_sources",
			StateVersion:   42,
			LastProgressAt: time.Unix(1_722_764_000, 0).UTC(),
		},
		Contract: researchrun.ResearchContract{Goal: template},
		Questions: []researchrun.Question{
			{ID: "q1", Question: "需求是否真实增长？", Status: researchrun.QuestionStatusInProgress, Priority: 7, Kind: researchrun.QuestionKindDimension},
			{ID: "q2", Question: "产能瓶颈在哪？", Status: researchrun.QuestionStatusOpen, Priority: 6, Kind: researchrun.QuestionKindGap},
			{ID: "q3", Question: "竞品是否同步扩产？", Status: researchrun.QuestionStatusAnswered, Priority: 5, Kind: researchrun.QuestionKindHypothesis},
			{ID: "q4", Question: "供应链是否跟得上？", Status: researchrun.QuestionStatusOpen, Priority: 4, Kind: researchrun.QuestionKindFollowUp},
			{ID: "q5", Question: "现金流能否支撑？", Status: researchrun.QuestionStatusUnresolved, Priority: 3, Kind: researchrun.QuestionKindContradiction},
			{ID: "q6", Question: "监管审批周期？", Status: researchrun.QuestionStatusOpen, Priority: 2, Kind: researchrun.QuestionKindDimension},
			{ID: "q7", ParentQuestionID: "q1", Question: "增长是否可持续到明年？", Status: researchrun.QuestionStatusInProgress, Priority: 1, Kind: researchrun.QuestionKindFollowUp},
		},
		Tasks: []researchrun.Task{
			{ID: "task-running", QuestionID: "q1", Objective: "收集出货数据", Kind: researchrun.TaskKindDiscover, Status: researchrun.TaskStatusRunning, AssignedAgentID: "agent-runner", Priority: 5},
			{ID: "task-ready", QuestionID: "q2", Objective: "访谈工厂负责人", Kind: researchrun.TaskKindDeepRead, Status: researchrun.TaskStatusReady, Priority: 4},
			{ID: "task-pending", QuestionID: "q4", Objective: "等待前置证据", Kind: researchrun.TaskKindVerify, Status: researchrun.TaskStatusPending, Priority: 3},
			{ID: "task-failed", QuestionID: "q5", Objective: "拉现金流模型", Kind: researchrun.TaskKindCounterSearch, Status: researchrun.TaskStatusFailed, Priority: 2},
			{ID: "task-succeeded", QuestionID: "q3", Objective: "汇总竞品产能", Kind: researchrun.TaskKindDiscover, Status: researchrun.TaskStatusSucceeded, Priority: 1},
			{ID: "task-child", ParentTaskID: "task-running", QuestionID: "q1", Objective: "核对二手来源", Kind: researchrun.TaskKindVerify, Status: researchrun.TaskStatusReady, Priority: 0.5},
		},
		Attempts: []researchrun.Attempt{
			{ID: "attempt-failed", TaskID: "task-running", AttemptNumber: 1, AssignedAgentID: "agent-runner", Status: researchrun.AttemptStatusFailed, FailureClass: "timeout", Diagnostics: "agent lease expired"},
			{ID: "attempt-running", TaskID: "task-running", AttemptNumber: 2, AssignedAgentID: "agent-runner", Status: researchrun.AttemptStatusRunning},
		},
		Claims: []researchrun.Claim{
			{ID: "c1", ProducedByTaskID: "task-succeeded", Text: "竞品 A 已宣布扩产", Status: researchrun.ClaimStatusSupported, Confidence: 0.8, CreatedAt: time.Unix(1_722_764_000, 0).UTC()},
		},
		Gate: researchrun.GateResult{Passed: false, Findings: []researchrun.GateFinding{{Code: "coverage_gap", Severity: "warning", Message: "仍缺独立来源"}}},
	}
}

func assertEdgesValid(t *testing.T, nodes []ResearchGraphNodeResp, edges []ResearchGraphEdgeResp) {
	t.Helper()
	ids := map[string]bool{}
	for _, n := range nodes {
		ids[n.ID] = true
	}
	for _, e := range edges {
		if !ids[e.FromNodeID] || !ids[e.ToNodeID] {
			t.Fatalf("edge %s endpoints missing: %s -> %s", e.ID, e.FromNodeID, e.ToNodeID)
		}
	}
}

func countNodeTypes(nodes []ResearchGraphNodeResp) map[string]int {
	out := map[string]int{}
	for _, n := range nodes {
		out[n.NodeType]++
	}
	return out
}

func payloadKind(n ResearchGraphNodeResp) string {
	obj := payloadObject(n.Payload)
	kind, _ := obj["kind"].(string)
	return kind
}

func findNodeByPayload(t *testing.T, nodes []ResearchGraphNodeResp, key, want string) ResearchGraphNodeResp {
	t.Helper()
	for _, n := range nodes {
		obj := payloadObject(n.Payload)
		if v, _ := obj[key].(string); v == want {
			return n
		}
		if details, ok := obj["details"].(map[string]any); ok {
			if v, _ := details[key].(string); v == want {
				return n
			}
		}
	}
	raw, _ := json.Marshal(nodes)
	t.Fatalf("node with %s=%s not found in %s", key, want, raw)
	return ResearchGraphNodeResp{}
}
