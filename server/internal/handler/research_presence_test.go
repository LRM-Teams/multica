package handler

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/researchrun"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestBuildResearchPresenceMap_LatestPerAgent(t *testing.T) {
	agentA := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	agentB := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	t1 := time.Date(2026, 7, 30, 7, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)

	nodes := []db.ResearchGraphNode{
		{
			NodeType:     "goal",
			Title:        "ignore",
			ActorAgentID: pgtype.UUID{Bytes: agentA, Valid: true},
			UpdatedAt:    pgtype.Timestamptz{Time: t2, Valid: true},
		},
		{
			NodeType:     "agent_activity",
			Title:        "old A",
			ActorAgentID: pgtype.UUID{Bytes: agentA, Valid: true},
			UpdatedAt:    pgtype.Timestamptz{Time: t1, Valid: true},
		},
		{
			NodeType:     "agent_activity",
			Title:        "new A",
			ActorAgentID: pgtype.UUID{Bytes: agentA, Valid: true},
			UpdatedAt:    pgtype.Timestamptz{Time: t2, Valid: true},
		},
		{
			NodeType:     "agent_activity",
			Title:        "B busy",
			ActorAgentID: pgtype.UUID{Bytes: agentB, Valid: true},
			UpdatedAt:    pgtype.Timestamptz{Time: t1, Valid: true},
		},
		{
			NodeType:     "agent_activity",
			Title:        "   ",
			ActorAgentID: pgtype.UUID{Bytes: agentB, Valid: true},
			UpdatedAt:    pgtype.Timestamptz{Time: t2, Valid: true},
		},
	}

	got := buildResearchPresenceMap(nodes)
	if got[agentA.String()].Activity != "new A" {
		t.Fatalf("agent A activity = %q, want new A", got[agentA.String()].Activity)
	}
	if got[agentA.String()].UpdatedAt != t2.UnixMilli() {
		t.Fatalf("agent A updated_at = %d, want %d", got[agentA.String()].UpdatedAt, t2.UnixMilli())
	}
	if got[agentB.String()].Activity != "B busy" {
		t.Fatalf("agent B should keep last non-empty activity, got %q", got[agentB.String()].Activity)
	}
}

func TestConfidenceFromPayload(t *testing.T) {
	raw := json.RawMessage(`{"confidence":0.42,"note":"x"}`)
	c := confidenceFromPayload(raw)
	if c == nil || *c != 0.42 {
		t.Fatalf("confidence = %v, want 0.42", c)
	}
	if confidenceFromPayload(json.RawMessage(`{}`)) != nil {
		t.Fatal("empty payload should yield nil confidence")
	}
}

func TestBuildResearchPresenceRoster_FiveMembersIncludingIdleReporter(t *testing.T) {
	now := time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC)
	ids := []uuid.UUID{
		uuid.MustParse("11111111-1111-1111-1111-111111111111"), // lead / reporter — no events
		uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		uuid.MustParse("44444444-4444-4444-4444-444444444444"),
		uuid.MustParse("55555555-5555-5555-5555-555555555555"),
	}
	members := []researchPresenceMember{
		{AgentID: ids[0].String(), Role: "lead", FleetMemberID: "m-lead"},
		{AgentID: ids[1].String(), Role: "domain_a", FleetMemberID: "m-a"},
		{AgentID: ids[2].String(), Role: "domain_b", FleetMemberID: "m-b"},
		{AgentID: ids[3].String(), Role: "domain_c", FleetMemberID: "m-c"},
		{AgentID: ids[4].String(), Role: "reporter", FleetMemberID: "m-r"},
	}
	tRecent := now.Add(-2 * time.Minute)
	nodes := []db.ResearchGraphNode{
		activityNode(ids[1], "正在调研竞品定价", tRecent, `{"phase":"presence","task_id":"task-a","node_id":"node-a"}`),
		activityNode(ids[2], researchPresenceGenericStartedTitle, tRecent, `{"event_type":"task_started","details":{"task_id":"task-b"}}`),
		activityNode(ids[3], researchPresenceGenericDispatchTitle, tRecent, `{"event_type":"task_dispatching","details":{"task_id":"task-c"}}`),
		// ids[0] and ids[4] have no events — must still appear as idle.
	}

	got := buildResearchPresenceRoster(members, nodes, now)
	if len(got) != 5 {
		t.Fatalf("presence size = %d, want 5", len(got))
	}
	for _, id := range ids {
		if _, ok := got[id.String()]; !ok {
			t.Fatalf("missing agent %s in presence roster", id)
		}
	}
	if got[ids[0].String()].Phase != ResearchPresencePhaseIdle {
		t.Fatalf("lead without events: phase=%q want idle", got[ids[0].String()].Phase)
	}
	if got[ids[0].String()].Activity != "" || got[ids[0].String()].TaskID != nil {
		t.Fatalf("idle lead must not invent activity/task: %+v", got[ids[0].String()])
	}
	if got[ids[4].String()].Phase != ResearchPresencePhaseIdle {
		t.Fatalf("reporter without events: phase=%q want idle", got[ids[4].String()].Phase)
	}
	if got[ids[1].String()].Phase != ResearchPresencePhaseRunning {
		t.Fatalf("explicit presence phase=%q", got[ids[1].String()].Phase)
	}
	if got[ids[1].String()].Role != "domain_a" || got[ids[1].String()].FleetMemberID != "m-a" {
		t.Fatalf("role keys = %+v", got[ids[1].String()])
	}
	if deref(got[ids[1].String()].TaskID) != "task-a" || deref(got[ids[1].String()].NodeID) != "node-a" {
		t.Fatalf("task/node = %+v", got[ids[1].String()])
	}
	if got[ids[2].String()].Phase != ResearchPresencePhaseRunning {
		t.Fatalf("task_started phase=%q", got[ids[2].String()].Phase)
	}
	if got[ids[3].String()].Phase != ResearchPresencePhaseQueued {
		t.Fatalf("task_dispatching phase=%q", got[ids[3].String()].Phase)
	}
}

func TestBuildResearchPresenceRoster_GenericDoesNotOverrideSpecific(t *testing.T) {
	now := time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC)
	agent := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	t0 := now.Add(-5 * time.Minute)
	t1 := now.Add(-1 * time.Minute)
	members := []researchPresenceMember{{AgentID: agent.String(), Role: "domain_a", FleetMemberID: "m1"}}
	nodes := []db.ResearchGraphNode{
		activityNode(agent, "深挖供应链风险证据", t0, `{"phase":"presence","task_id":"task-1","node_id":"node-1"}`),
		activityNode(agent, researchPresenceGenericStartedTitle, t1, `{"event_type":"task_started","details":{"task_id":"task-1"}}`),
	}
	got := buildResearchPresenceRoster(members, nodes, now)[agent.String()]
	if got.Activity != "深挖供应链风险证据" {
		t.Fatalf("activity = %q, want specific caption", got.Activity)
	}
	if got.Phase != ResearchPresencePhaseRunning {
		t.Fatalf("phase = %q, want running (enriched from task_started)", got.Phase)
	}
	if deref(got.TaskID) != "task-1" || deref(got.NodeID) != "node-1" {
		t.Fatalf("associations = task=%v node=%v", got.TaskID, got.NodeID)
	}
}

func TestBuildResearchPresenceRoster_TerminalClearsStarted(t *testing.T) {
	now := time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC)
	agent := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	t0 := now.Add(-10 * time.Minute)
	t1 := now.Add(-2 * time.Minute)
	members := []researchPresenceMember{{AgentID: agent.String(), Role: "domain_b", FleetMemberID: "m2"}}
	nodes := []db.ResearchGraphNode{
		activityNode(agent, researchPresenceGenericStartedTitle, t0, `{"event_type":"task_started","details":{"task_id":"task-9"}}`),
		{
			ID:           pgtype.UUID{Bytes: uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc"), Valid: true},
			NodeType:     "finding",
			Title:        "调研结果已入账",
			Summary:      "ok",
			ActorAgentID: pgtype.UUID{Bytes: agent, Valid: true},
			Payload:      []byte(`{"event_type":"task_result_accepted","details":{"task_id":"task-9"}}`),
			UpdatedAt:    pgtype.Timestamptz{Time: t1, Valid: true},
		},
	}
	got := buildResearchPresenceRoster(members, nodes, now)[agent.String()]
	if got.Phase != ResearchPresencePhaseDone {
		t.Fatalf("phase = %q, want done", got.Phase)
	}
	if got.Activity != "调研结果已入账" {
		t.Fatalf("activity = %q after terminal", got.Activity)
	}
	if got.Activity == researchPresenceGenericStartedTitle {
		t.Fatal("terminal must clear generic started caption")
	}
}

func TestBuildResearchPresenceRoster_FailedTerminal(t *testing.T) {
	now := time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC)
	agent := uuid.MustParse("dddddddd-dddd-dddd-dddd-dddddddddddd")
	t0 := now.Add(-4 * time.Minute)
	t1 := now.Add(-1 * time.Minute)
	members := []researchPresenceMember{{AgentID: agent.String(), Role: "domain_c", FleetMemberID: "m3"}}
	nodes := []db.ResearchGraphNode{
		activityNode(agent, researchPresenceGenericStartedTitle, t0, `{"event_type":"task_started","details":{"task_id":"task-f"}}`),
		{
			NodeType:     "dead_end",
			Title:        "调研任务尝试失败",
			Summary:      "timeout",
			ActorAgentID: pgtype.UUID{Bytes: agent, Valid: true},
			Payload:      []byte(`{"event_type":"task_attempt_failed","details":{"task_id":"task-f"}}`),
			UpdatedAt:    pgtype.Timestamptz{Time: t1, Valid: true},
		},
	}
	got := buildResearchPresenceRoster(members, nodes, now)[agent.String()]
	if got.Phase != ResearchPresencePhaseFailed {
		t.Fatalf("phase = %q, want failed", got.Phase)
	}
}

func TestBuildResearchPresenceRoster_StaleRunning(t *testing.T) {
	now := time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC)
	agent := uuid.MustParse("eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee")
	staleAt := now.Add(-researchPresenceStaleAfter - time.Minute)
	members := []researchPresenceMember{{AgentID: agent.String(), Role: "domain_a", FleetMemberID: "m4"}}
	nodes := []db.ResearchGraphNode{
		activityNode(agent, "长时间无更新的具体动作", staleAt, `{"phase":"presence"}`),
	}
	got := buildResearchPresenceRoster(members, nodes, now)[agent.String()]
	if got.Phase != ResearchPresencePhaseStale {
		t.Fatalf("phase = %q, want stale", got.Phase)
	}
	if got.StaleReason == nil || *got.StaleReason != "presence_expired" {
		t.Fatalf("stale_reason = %v", got.StaleReason)
	}
	if got.Activity != "长时间无更新的具体动作" {
		t.Fatalf("stale should keep activity text, got %q", got.Activity)
	}
}

func TestBuildResearchPresenceRosterWithRun_AttemptProjectionFiveFleet(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	sessionID := "d3cb52ae-bb85-4731-91d7-30c779063770"
	ids := []string{
		"11111111-1111-1111-1111-111111111111", // lead idle
		"22222222-2222-2222-2222-222222222222", // scout running
		"33333333-3333-3333-3333-333333333333", // domain queued
		"44444444-4444-4444-4444-444444444444", // idle
		"55555555-5555-5555-5555-555555555555", // idle
	}
	members := []researchPresenceMember{
		{AgentID: ids[0], Role: "lead"},
		{AgentID: ids[1], Role: "scout"},
		{AgentID: ids[2], Role: "domain_a"},
		{AgentID: ids[3], Role: "domain_b"},
		{AgentID: ids[4], Role: "reporter"},
	}
	started := now.Add(-2 * time.Minute)
	dispatched := now.Add(-1 * time.Minute)
	tasks := []researchrun.Task{
		{
			ID: "task-scout", SessionID: sessionID, Objective: "深挖竞品定价证据",
			Kind: researchrun.TaskKindDiscover, Status: researchrun.TaskStatusRunning,
			AssignedAgentID: ids[1], TimeoutSeconds: 1800, StartedAt: &started,
		},
		{
			ID: "task-domain", SessionID: sessionID, Objective: "校验供应链来源",
			Kind: researchrun.TaskKindVerify, Status: researchrun.TaskStatusDispatching,
			AssignedAgentID: ids[2], TimeoutSeconds: 900,
		},
	}
	attempts := []researchrun.Attempt{
		{
			ID: "att-scout", SessionID: sessionID, TaskID: "task-scout", AttemptNumber: 1,
			AssignedAgentID: ids[1], Status: researchrun.AttemptStatusRunning,
			DispatchedAt: started, StartedAt: &started,
		},
		{
			ID: "att-domain", SessionID: sessionID, TaskID: "task-domain", AttemptNumber: 1,
			AssignedAgentID: ids[2], Status: researchrun.AttemptStatusDispatching,
			DispatchedAt: dispatched,
		},
	}

	// No agent_activity graph nodes — mirrors run-v2 sessions that only have ledger.
	got := buildResearchPresenceRosterWithRun(
		members, nil, tasks, attempts, sessionID, "s2_sources", now,
	)
	if len(got) != 5 {
		t.Fatalf("presence size = %d, want 5", len(got))
	}
	if got[ids[0]].Phase != ResearchPresencePhaseIdle || got[ids[0]].TaskID != nil {
		t.Fatalf("lead without attempt must stay idle: %+v", got[ids[0]])
	}
	scout := got[ids[1]]
	if scout.Phase != ResearchPresencePhaseRunning {
		t.Fatalf("scout phase=%q want running", scout.Phase)
	}
	if scout.Activity != "深挖竞品定价证据" {
		t.Fatalf("scout activity=%q", scout.Activity)
	}
	if deref(scout.TaskID) != "task-scout" {
		t.Fatalf("scout task_id=%v", scout.TaskID)
	}
	wantNode := runGraphNodeID(sessionID, runGraphKindTask, "task-scout")
	if deref(scout.NodeID) != wantNode {
		t.Fatalf("scout node_id=%v want %s", scout.NodeID, wantNode)
	}
	if deref(scout.Stage) != "s2_sources" {
		t.Fatalf("scout stage=%v", scout.Stage)
	}
	if scout.ExpiresAt == nil || *scout.ExpiresAt != started.Add(1800*time.Second).UnixMilli() {
		t.Fatalf("scout expires_at=%v", scout.ExpiresAt)
	}

	domain := got[ids[2]]
	if domain.Phase != ResearchPresencePhaseQueued {
		t.Fatalf("domain phase=%q want queued", domain.Phase)
	}
	if deref(domain.TaskID) != "task-domain" {
		t.Fatalf("domain task_id=%v", domain.TaskID)
	}
}

func TestBuildResearchPresenceRosterWithRun_AttemptPreferredOverGraphOnlyLifecycle(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	sessionID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	agent := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	members := []researchPresenceMember{{AgentID: agent.String(), Role: "scout"}}
	started := now.Add(-3 * time.Minute)
	tasks := []researchrun.Task{{
		ID: "task-1", Objective: "从 attempt 投影的目标", Kind: researchrun.TaskKindDeepRead,
		Status: researchrun.TaskStatusRunning, AssignedAgentID: agent.String(),
		TimeoutSeconds: 600, StartedAt: &started,
	}}
	attempts := []researchrun.Attempt{{
		ID: "att-1", TaskID: "task-1", AttemptNumber: 2, AssignedAgentID: agent.String(),
		Status: researchrun.AttemptStatusRunning, DispatchedAt: started, StartedAt: &started,
	}}
	// Stale generic graph event must not become the only SoT.
	nodes := []db.ResearchGraphNode{
		activityNode(agent, researchPresenceGenericStartedTitle, now.Add(-30*time.Second),
			`{"event_type":"task_started","details":{"task_id":"stale-graph-task"}}`),
	}
	got := buildResearchPresenceRosterWithRun(
		members, nodes, tasks, attempts, sessionID, "s3_validation", now,
	)[agent.String()]
	if got.Activity != "从 attempt 投影的目标" {
		t.Fatalf("activity=%q want attempt objective", got.Activity)
	}
	if deref(got.TaskID) != "task-1" {
		t.Fatalf("task_id=%v want task-1 from attempt SoT", got.TaskID)
	}
	if deref(got.Stage) != "s3_validation" {
		t.Fatalf("stage=%v", got.Stage)
	}
}

func TestBuildResearchPresenceRosterWithRun_AttemptExpired(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	sessionID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	agent := "cccccccc-cccc-cccc-cccc-cccccccccccc"
	members := []researchPresenceMember{{AgentID: agent, Role: "scout"}}
	started := now.Add(-20 * time.Minute)
	tasks := []researchrun.Task{{
		ID: "task-exp", Objective: "超时任务", Status: researchrun.TaskStatusRunning,
		AssignedAgentID: agent, TimeoutSeconds: 600, StartedAt: &started,
	}}
	attempts := []researchrun.Attempt{{
		ID: "att-exp", TaskID: "task-exp", AttemptNumber: 1, AssignedAgentID: agent,
		Status: researchrun.AttemptStatusRunning, DispatchedAt: started, StartedAt: &started,
	}}
	got := buildResearchPresenceRosterWithRun(
		members, nil, tasks, attempts, sessionID, "s2_sources", now,
	)[agent]
	if got.Phase != ResearchPresencePhaseStale {
		t.Fatalf("phase=%q want stale", got.Phase)
	}
	if got.StaleReason == nil || *got.StaleReason != "attempt_expired" {
		t.Fatalf("stale_reason=%v want attempt_expired", got.StaleReason)
	}
}

func TestResearchPresenceMembersFromRunFleet_KeepsScoutRoles(t *testing.T) {
	got := researchPresenceMembersFromRunFleet([]researchrun.FleetMember{
		{AgentID: "a1", Role: "lead", Status: "active", IsLead: true},
		{AgentID: "a2", Role: "scout", Status: "active"},
		{AgentID: "a3", Role: "scout", Status: "archived"},
		{AgentID: "a2", Role: "scout", Status: "active"}, // dup agent
		{AgentID: "a4", Role: "reporter", Status: "active"},
	})
	if len(got) != 3 {
		t.Fatalf("got %+v, want 3 active unique agents", got)
	}
}

func TestBuildResearchPresenceRoster_NoFabricatedAssociations(t *testing.T) {
	now := time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC)
	agent := uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")
	members := []researchPresenceMember{{AgentID: agent.String(), Role: "domain_a", FleetMemberID: "m5"}}
	nodes := []db.ResearchGraphNode{
		activityNode(agent, "只有标题没有关联字段", now.Add(-time.Minute), `{"phase":"presence"}`),
	}
	got := buildResearchPresenceRoster(members, nodes, now)[agent.String()]
	if got.TaskID != nil || got.NodeID != nil || got.BranchID != nil {
		t.Fatalf("expected null associations, got task=%v node=%v branch=%v", got.TaskID, got.NodeID, got.BranchID)
	}
	if got.StaleReason != nil {
		t.Fatalf("fresh presence must not be stale, got %v", got.StaleReason)
	}
}

func TestResearchPresenceMembersFromFleet_SkipsArchived(t *testing.T) {
	a := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	b := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	rows := []db.ResearchFleetMember{
		{ID: pgtype.UUID{Bytes: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Valid: true}, AgentID: pgtype.UUID{Bytes: a, Valid: true}, Role: "lead", Status: "active", IsLead: true},
		{ID: pgtype.UUID{Bytes: uuid.MustParse("22222222-2222-2222-2222-222222222222"), Valid: true}, AgentID: pgtype.UUID{Bytes: b, Valid: true}, Role: "domain_a", Status: "archived"},
	}
	got := researchPresenceMembersFromFleet(rows)
	if len(got) != 1 || got[0].AgentID != a.String() {
		t.Fatalf("got %+v", got)
	}
}

func activityNode(agent uuid.UUID, title string, at time.Time, payload string) db.ResearchGraphNode {
	return db.ResearchGraphNode{
		ID:           pgtype.UUID{Bytes: uuid.New(), Valid: true},
		NodeType:     "agent_activity",
		Title:        title,
		Summary:      title,
		ActorAgentID: pgtype.UUID{Bytes: agent, Valid: true},
		Payload:      []byte(payload),
		UpdatedAt:    pgtype.Timestamptz{Time: at, Valid: true},
		CreatedAt:    pgtype.Timestamptz{Time: at, Valid: true},
	}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
