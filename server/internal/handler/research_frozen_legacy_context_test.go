package handler

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/researchrun"
)

func TestFrozenLegacyContextMapsCompatibilityFamilies(t *testing.T) {
	now := time.Date(2026, time.August, 13, 14, 30, 0, 123000000, time.UTC)
	sources := mapFrozenLegacySources([]researchrun.FrozenLegacySource{{
		ID: "source-1", SessionID: "session-1", URL: "https://example.test", Title: "Frozen",
		Payload: json.RawMessage(`{"frozen":true}`), CreatedAt: now, UpdatedAt: now,
	}})
	if len(sources) != 1 || sources[0].Title != "Frozen" || sources[0].CreatedAt != now.Format(time.RFC3339Nano) {
		t.Fatalf("sources=%+v", sources)
	}

	messages := mapFrozenResearchMessages([]researchrun.FrozenResearchMessage{{
		ID: "message-1", SessionID: "session-1", SenderType: "system", Body: "Frozen message",
		Meta: json.RawMessage(`{"match_decision":{"matched_node_ids":["node-1"]}}`), CreatedAt: now,
	}})
	if len(messages) != 1 || messages[0].Body != "Frozen message" || messages[0].CardKind != "chat" || messages[0].MatchDecision == nil {
		t.Fatalf("messages=%+v", messages)
	}

	rounds := mapFrozenProductRounds([]researchrun.FrozenProductRound{{
		ID: "round-1", SessionID: "session-1", RoundNumber: 1, Decision: "continue", CreatedAt: now,
	}})
	if len(rounds) != 1 || rounds[0].Decision != "continue" || string(rounds[0].CoverageGaps) != "[]" {
		t.Fatalf("rounds=%+v", rounds)
	}

	thoughts := mapFrozenThoughtStrategies([]researchrun.FrozenThoughtStrategyNode{{
		ID: "node-1", SessionID: "session-1", UpdatedAt: now,
		Payload: json.RawMessage(`{"thought_strategy":{"rationale":"Frozen rationale","expected_outcome":"Frozen outcome"}}`),
	}})
	if len(thoughts) != 1 || thoughts[0].NodeID != "node-1" || thoughts[0].Rationale != "Frozen rationale" {
		t.Fatalf("thoughts=%+v", thoughts)
	}

	report := mapFrozenResearchReport(&researchrun.FrozenResearchReport{
		ID: "report-1", SessionID: "session-1", Revision: 2, ContentMD: "# Frozen", CreatedAt: now, UpdatedAt: now,
	})
	if report == nil || report.ContentMD != "# Frozen" || report.Revision != 2 {
		t.Fatalf("report=%+v", report)
	}
}
