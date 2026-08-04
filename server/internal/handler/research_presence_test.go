package handler

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
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
