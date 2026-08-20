package researchrun

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestRebuildCanonicalRunFromEventsRequiresRebuildSchema(t *testing.T) {
	_, err := RebuildCanonicalRunFromEvents([]RunEvent{{
		WorkspaceID: "ws", SessionID: "run", Sequence: 1, Type: "v6_run_bootstrapped",
		Payload: json.RawMessage(`{"director_agent_id":"d1"}`),
	}})
	if !errors.Is(err, ErrIncompleteEventLog) {
		t.Fatalf("legacy event: %v", err)
	}
}

func TestRebuildCanonicalRunFromEventsAppliesRebuildableFacts(t *testing.T) {
	boot, _ := json.Marshal(rebuildablePayload(map[string]any{
		"orchestrator_version": OrchestratorVersionV6, "director_agent_id": "director-1", "status": "running",
	}))
	done, _ := json.Marshal(rebuildablePayload(map[string]any{"status": "completed"}))
	rebuilt, err := RebuildCanonicalRunFromEvents([]RunEvent{
		{WorkspaceID: "ws", SessionID: "run", Sequence: 1, Type: "v6_run_bootstrapped", Payload: boot, CreatedAt: time.Now()},
		{WorkspaceID: "ws", SessionID: "run", Sequence: 2, Type: "run_completed", Payload: done, CreatedAt: time.Now()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.DirectorAgentID != "director-1" || rebuilt.Status != "completed" || rebuilt.EventsApplied != 2 || rebuilt.ThroughSequence != 2 {
		t.Fatalf("%+v", rebuilt)
	}
}

func TestPersistSourceIngestionRejectsScreenedKind(t *testing.T) {
	hash := "sha256:" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab" + "ab"
	store := &PostgresStore{}
	_, err := store.PersistSourceIngestion(t.Context(), PersistSourceIngestionInput{Intent: SourceIngestionIntent{
		PolicyVersion: SourceIngestionPolicyVersionV1, Kind: SourceIngestionScreenedRetrieval,
		WorkspaceID: "11111111-1111-1111-1111-111111111111", SessionID: "22222222-2222-2222-2222-222222222222",
		SourceSnapshotID: "33333333-3333-3333-3333-333333333333", ContentHash: hash, CapturedAt: time.Now().UTC(),
		Locator: "source-candidate:33333333-3333-3333-3333-333333333333", Reason: "Accepted screened candidate fetched as evidence.",
		CanonicalURL: "https://example.com/doc", TaskID: "44444444-4444-4444-4444-444444444444",
		AttemptID: "55555555-5555-5555-5555-555555555555", AgentID: "66666666-6666-6666-6666-666666666666",
		SearchPlanID: "77777777-7777-7777-7777-777777777777", QueryExecutionID: "88888888-8888-8888-8888-888888888888",
		SourceCandidateID: "33333333-3333-3333-3333-333333333333", ScreeningDecisionID: "99999999-9999-9999-9999-999999999999",
		ScreeningDecisionFingerprint: hash, ScreeningDisposition: "accepted",
	}})
	if err == nil || !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("screened persist: %v", err)
	}
}
