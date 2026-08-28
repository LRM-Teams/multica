package problemevolution

import (
	"encoding/json"
	"strings"
	"testing"
)

func scoredEvent(t *testing.T, score Score, profile BehaviorProfile) EvolverEvent {
	t.Helper()
	payload, err := json.Marshal(CandidateScoredPayload{Score: score, BehaviorProfile: profile})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	return EvolverEvent{
		SchemaVersion: SchemaVersion,
		ClientEventID: "0f3a4d1e-0000-4000-8000-000000000001",
		EventType:     EventCandidateScored,
		CandidateRef:  "c1",
		Payload:       payload,
	}
}

func validScore() Score {
	return Score{
		SchemaVersion:  SchemaVersion,
		Total:          0.75,
		Scale:          ScaleUnitInterval,
		HardGatePassed: true,
		Dimensions: []ScoreDimension{
			{DimensionID: "correctness", Score: 0.8, Weight: 0.5, Hard: true},
			{DimensionID: "coverage", Score: 0.7, Weight: 0.5},
		},
	}
}

func validProfile() BehaviorProfile {
	return BehaviorProfile{
		SchemaVersion: SchemaVersion,
		Kind:          BehaviorKindDimensionVector,
		Entries:       []BehaviorEntry{{Key: "correctness", Value: 0.8}},
	}
}

func TestEventValidateRejectsUnknownEventType(t *testing.T) {
	event := EvolverEvent{
		SchemaVersion: SchemaVersion,
		ClientEventID: "abc",
		EventType:     "candidate_promoted_to_winner",
	}
	if err := event.Validate(); err == nil {
		t.Fatal("expected an event type outside the allowlist to be rejected")
	}
}

func TestEventValidateRequiresClientEventID(t *testing.T) {
	event := EvolverEvent{SchemaVersion: SchemaVersion, EventType: EventBatchStarted}
	if err := event.Validate(); err == nil {
		t.Fatal("expected a missing client_event_id to be rejected")
	}
}

func TestEventValidateRequiresCandidateRefForCandidateEvents(t *testing.T) {
	event := EvolverEvent{
		SchemaVersion: SchemaVersion,
		ClientEventID: "abc",
		EventType:     EventCandidateScored,
	}
	if err := event.Validate(); err == nil {
		t.Fatal("expected a candidate event without candidate_id to be rejected")
	}
}

func TestEventValidateRejectsUnsupportedSchemaVersion(t *testing.T) {
	event := EvolverEvent{
		SchemaVersion: SchemaVersion + 1,
		ClientEventID: "abc",
		EventType:     EventBatchStarted,
	}
	if err := event.Validate(); err == nil {
		t.Fatal("expected a future schema_version to be rejected")
	}
}

func TestValidatePayloadAcceptsScoredCandidate(t *testing.T) {
	event := scoredEvent(t, validScore(), validProfile())
	if err := event.Validate(); err != nil {
		t.Fatalf("envelope rejected: %v", err)
	}
	if err := event.ValidatePayload(); err != nil {
		t.Fatalf("payload rejected: %v", err)
	}
}

func TestValidatePayloadRejectsOutOfRangeScore(t *testing.T) {
	score := validScore()
	score.Total = 1.4
	event := scoredEvent(t, score, validProfile())
	if err := event.ValidatePayload(); err == nil {
		t.Fatal("expected a total outside 0..1 to be rejected")
	}
}

func TestPersistentHarnessEventsRequireFalsifiableFields(t *testing.T) {
	payload, err := json.Marshal(ChangeProposedPayload{
		Iteration: 1, Component: "systemprompt.md", RootCause: "missing constraint",
		FixSummary: "add constraint guidance", PredictedPassTaskNames: []string{"task-a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	event := EvolverEvent{
		SchemaVersion: SchemaVersion, ClientEventID: "persistent-change-1",
		EventType: EventChangeProposed, Payload: payload,
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("envelope rejected: %v", err)
	}
	if err := event.ValidatePayload(); err != nil {
		t.Fatalf("valid change proposal rejected: %v", err)
	}
	payload, _ = json.Marshal(ChangeProposedPayload{Iteration: 1, Component: "tools"})
	event.Payload = payload
	if err := event.ValidatePayload(); err == nil {
		t.Fatal("change without root cause or predicted pass tasks was accepted")
	}
}

func TestPersistentHarnessTaskResultRejectsHoldoutOutOfRange(t *testing.T) {
	payload, _ := json.Marshal(TaskResultPayload{
		Iteration: 1, TaskName: "task-a", RolloutIndex: 0,
		Split: "holdout", Reward: 0.5, Verdict: "pass",
	})
	event := EvolverEvent{
		SchemaVersion: SchemaVersion, ClientEventID: "persistent-result-1",
		EventType: EventTaskResult, Payload: payload,
	}
	if err := event.ValidatePayload(); err != nil {
		t.Fatalf("valid task result rejected: %v", err)
	}
	payload, _ = json.Marshal(TaskResultPayload{
		Iteration: 1, TaskName: "task-a", Split: "private", Reward: 0.5, Verdict: "pass",
	})
	event.Payload = payload
	if err := event.ValidatePayload(); err == nil {
		t.Fatal("unknown result split was accepted")
	}
}

func TestValidatePayloadRejectsUnnormalisedScale(t *testing.T) {
	score := validScore()
	score.Scale = "percent"
	event := scoredEvent(t, score, validProfile())
	if err := event.ValidatePayload(); err == nil {
		t.Fatal("expected a non-unit-interval scale to be rejected")
	}
}

func TestValidatePayloadRejectsEvolverSelfPromotion(t *testing.T) {
	// Selection is the platform's decision: the evolver may only report a
	// candidate as selectable or failed, never as elite or selected.
	payload, err := json.Marshal(CandidateFinishedPayload{Status: "elite"})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	event := EvolverEvent{
		SchemaVersion: SchemaVersion,
		ClientEventID: "abc",
		EventType:     EventCandidateFinished,
		CandidateRef:  "c1",
		Payload:       payload,
	}
	if err := event.ValidatePayload(); err == nil {
		t.Fatal("expected an evolver-set elite status to be rejected")
	}
}

func TestValidatePayloadRequiresArtifactHash(t *testing.T) {
	payload, err := json.Marshal(CandidateArtifactPayload{
		Kind:         "answer",
		RelativePath: "artifacts/c1.md",
	})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	event := EvolverEvent{
		SchemaVersion: SchemaVersion,
		ClientEventID: "abc",
		EventType:     EventCandidateArtifact,
		CandidateRef:  "c1",
		Payload:       payload,
	}
	if err := event.ValidatePayload(); err == nil {
		t.Fatal("expected an artifact without a content hash to be rejected")
	}
}

func TestTruncateFreeTextBoundsProse(t *testing.T) {
	long := strings.Repeat("x", MaxFreeTextBytes+500)
	truncated := TruncateFreeText(long)
	if len(truncated) > MaxFreeTextBytes+4 {
		t.Fatalf("expected bounded text, got %d bytes", len(truncated))
	}
	if TruncateFreeText("  hello  ") != "hello" {
		t.Fatal("expected surrounding whitespace to be trimmed")
	}
}

func TestArtifactRelativePathRejectsEscapes(t *testing.T) {
	for _, declared := range []string{
		"../secrets.txt",
		"/etc/passwd",
		"artifacts/../../escape.md",
		"other/c1.md",
		"C:/windows/system32",
	} {
		if _, err := ArtifactRelativePath(DefaultArtifactDir, declared); err == nil {
			t.Fatalf("expected %q to be rejected", declared)
		}
	}
	resolved, err := ArtifactRelativePath(DefaultArtifactDir, "artifacts/c1.md")
	if err != nil {
		t.Fatalf("expected a contained path to be accepted, got %v", err)
	}
	if resolved != "artifacts/c1.md" {
		t.Fatalf("unexpected resolved path %q", resolved)
	}
}

func TestAllowedEventTypesCoversValidatedTypes(t *testing.T) {
	for _, eventType := range AllowedEventTypes() {
		if !IsAllowedEventType(eventType) {
			t.Fatalf("%q is listed but not allowed", eventType)
		}
	}
	if IsAllowedEventType("hidden_answer_leak") {
		t.Fatal("unexpected event type accepted")
	}
}
