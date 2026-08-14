package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestAttemptScopedSnapshotReturnsFrozenLegacyContext(t *testing.T) {
	workspaceID := "00000000-0000-0000-0000-000000000001"
	sessionID := "00000000-0000-0000-0000-000000000002"
	now := time.Date(2026, time.August, 14, 2, 0, 0, 0, time.UTC)
	engine := &recordingResearchRunEngine{snapshot: researchrun.RunSnapshot{
		LegacyContext: &researchrun.FrozenLegacyContext{
			Sources:           []researchrun.FrozenLegacySource{{ID: "frozen-source", SessionID: sessionID, Title: "Frozen source", CreatedAt: now, UpdatedAt: now}},
			Messages:          []researchrun.FrozenResearchMessage{{ID: "frozen-message", SessionID: sessionID, SenderType: "system", Body: "Frozen message", CreatedAt: now}},
			ProductRounds:     []researchrun.FrozenProductRound{{ID: "frozen-round", SessionID: sessionID, RoundNumber: 1, Decision: "continue", CreatedAt: now}},
			ThoughtStrategies: []researchrun.FrozenThoughtStrategyNode{{ID: "frozen-thought", SessionID: sessionID, Payload: json.RawMessage(`{"thought_strategy":{"rationale":"Frozen rationale","expected_outcome":"Frozen outcome"}}`), UpdatedAt: now}},
			Report:            &researchrun.FrozenResearchReport{ID: "frozen-report", SessionID: sessionID, Revision: 1, ContentMD: "# Frozen", CreatedAt: now, UpdatedAt: now},
		},
	}}
	h := &Handler{ResearchRun: engine}
	req := httptest.NewRequest(http.MethodGet, "/api/agent/research/sessions/"+sessionID, nil)
	req.Header.Set("X-Workspace-ID", workspaceID)
	req = withURLParam(req, "id", sessionID)
	recorder := httptest.NewRecorder()

	h.getResearchSessionSnapshot(recorder, req, true)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var snapshot ResearchSessionSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if len(snapshot.Sources) != 1 || snapshot.Sources[0].Title != "Frozen source" ||
		len(snapshot.Messages) != 1 || snapshot.Messages[0].Body != "Frozen message" ||
		len(snapshot.ProductRounds) != 1 || snapshot.ProductRounds[0].Decision != "continue" ||
		len(snapshot.ThoughtStrategies) != 1 || snapshot.ThoughtStrategies[0].Rationale != "Frozen rationale" ||
		snapshot.Report == nil || snapshot.Report.ContentMD != "# Frozen" {
		t.Fatalf("snapshot omitted frozen legacy context: %+v", snapshot)
	}
}
