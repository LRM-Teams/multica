package handler

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestMapThoughtStrategiesProjectsCompleteItem(t *testing.T) {
	sessionID := mustTestUUID("11111111-1111-1111-1111-111111111111")
	nodeID := mustTestUUID("22222222-2222-2222-2222-222222222222")
	updated := pgtype.Timestamptz{Time: time.Date(2026, 8, 4, 4, 0, 0, 0, time.UTC), Valid: true}

	rows := []db.ResearchGraphNode{
		{
			ID: nodeID, SessionID: sessionID, NodeType: "subquestion",
			Title: "竞品定价", Summary: "MUST NOT leak into rationale",
			Status: "active", UpdatedAt: updated,
			Payload: []byte(`{
				"rationale": "先横向挂牌价再纵向促销",
				"expected_outcome": "欧洲挂牌价对照表",
				"strategy_label": "价带扫描",
				"strategy_revision": "v3",
				"state": "active"
			}`),
		},
		{
			ID: mustTestUUID("33333333-3333-3333-3333-333333333333"),
			SessionID: sessionID, NodeType: "probe", Title: "空节点", Status: "active",
			Payload: []byte(`{}`),
		},
	}

	out := mapThoughtStrategies(rows)
	if len(out) != 1 {
		t.Fatalf("len=%d want 1 (empty omitted)", len(out))
	}
	item := out[0]
	if item.NodeID != uuidToString(nodeID) {
		t.Fatalf("node_id=%q", item.NodeID)
	}
	if item.Rationale != "先横向挂牌价再纵向促销" || item.ExpectedOutcome != "欧洲挂牌价对照表" {
		t.Fatalf("faces=%+v", item)
	}
	if item.StrategyLabel == nil || *item.StrategyLabel != "价带扫描" {
		t.Fatalf("strategy_label=%v", item.StrategyLabel)
	}
	if item.StrategyRevision == nil || *item.StrategyRevision != "v3" {
		t.Fatalf("strategy_revision=%v", item.StrategyRevision)
	}
	if item.State != researchThoughtStateActive {
		t.Fatalf("state=%q", item.State)
	}
	if item.Rationale == "MUST NOT leak into rationale" || item.ExpectedOutcome == "MUST NOT leak into rationale" {
		t.Fatal("must not invent from title/summary")
	}
}

func TestMapThoughtStrategiesDraftingPartial(t *testing.T) {
	sessionID := mustTestUUID("11111111-1111-1111-1111-111111111111")
	rows := []db.ResearchGraphNode{
		{
			ID: mustTestUUID("44444444-4444-4444-4444-444444444444"),
			SessionID: sessionID, NodeType: "subquestion", Title: "监管", Status: "active",
			Payload: []byte(`{
				"rationale": "改监管合规主线",
				"state": "drafting"
			}`),
		},
		{
			ID: mustTestUUID("55555555-5555-5555-5555-555555555555"),
			SessionID: sessionID, NodeType: "finding", Title: "半缺", Status: "active",
			Payload: []byte(`{"expected_outcome": "只有结果没有思路"}`),
		},
	}

	out := mapThoughtStrategies(rows)
	if len(out) != 1 {
		t.Fatalf("len=%d want 1 (partial without drafting omitted)", len(out))
	}
	if out[0].State != researchThoughtStateDrafting || out[0].Rationale == "" || out[0].ExpectedOutcome != "" {
		t.Fatalf("drafting item=%+v", out[0])
	}
}

func TestMapThoughtStrategiesNestedAndRevisionFallback(t *testing.T) {
	sessionID := mustTestUUID("11111111-1111-1111-1111-111111111111")
	updated := pgtype.Timestamptz{Time: time.Date(2026, 8, 4, 5, 30, 0, 0, time.UTC), Valid: true}
	rows := []db.ResearchGraphNode{
		{
			ID: mustTestUUID("66666666-6666-6666-6666-666666666666"),
			SessionID: sessionID, NodeType: "goal", Title: "出海", Status: "active", UpdatedAt: updated,
			Payload: []byte(`{
				"thought_strategy": {
					"rationale": "以电池供应链韧性为主线",
					"expectedOutcome": "韧性短名单",
					"strategyLabel": "供应链",
					"state": "settled"
				}
			}`),
		},
	}

	out := mapThoughtStrategies(rows)
	if len(out) != 1 {
		t.Fatalf("len=%d", len(out))
	}
	item := out[0]
	if item.State != researchThoughtStateSettled {
		t.Fatalf("state=%q", item.State)
	}
	if item.StrategyLabel == nil || *item.StrategyLabel != "供应链" {
		t.Fatalf("label=%v", item.StrategyLabel)
	}
	if item.StrategyRevision == nil || *item.StrategyRevision != timestampToString(updated) {
		t.Fatalf("revision fallback want updated_at, got %v", item.StrategyRevision)
	}
}

func TestMapThoughtStrategiesNeverUsesTitleSummary(t *testing.T) {
	rows := []db.ResearchGraphNode{
		{
			ID: mustTestUUID("77777777-7777-7777-7777-777777777777"),
			SessionID: mustTestUUID("11111111-1111-1111-1111-111111111111"),
			NodeType: "subquestion", Title: "看起来像思路", Summary: "看起来像目标",
			Status: "active", Payload: []byte(`{}`),
		},
	}
	if out := mapThoughtStrategies(rows); len(out) != 0 {
		t.Fatalf("empty payload must omit, got %+v", out)
	}
}
