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
