package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

const RebuildSchemaV1 = "research-run-rebuild-v1"

var ErrIncompleteEventLog = errors.New("research event log is incomplete for canonical rebuild")

type RebuiltCanonicalRun struct {
	WorkspaceID         string `json:"workspace_id"`
	SessionID           string `json:"session_id"`
	OrchestratorVersion string `json:"orchestrator_version,omitempty"`
	DirectorAgentID     string `json:"director_agent_id,omitempty"`
	Status              string `json:"status,omitempty"`
	EventsApplied       int    `json:"events_applied"`
	ThroughSequence     int64  `json:"through_event_sequence"`
}

func rebuildablePayload(fields map[string]any) map[string]any {
	out := map[string]any{rebuildSchemaField: RebuildSchemaV1}
	for key, value := range fields {
		out[key] = value
	}
	return out
}

const rebuildSchemaField = "rebuild_schema"

func RebuildCanonicalRunFromEvents(events []RunEvent) (RebuiltCanonicalRun, error) {
	if len(events) == 0 {
		return RebuiltCanonicalRun{}, fmt.Errorf("%w: event log is empty", ErrIncompleteEventLog)
	}
	rebuilt := RebuiltCanonicalRun{WorkspaceID: events[0].WorkspaceID, SessionID: events[0].SessionID}
	for _, event := range events {
		if event.WorkspaceID != rebuilt.WorkspaceID || event.SessionID != rebuilt.SessionID {
			return RebuiltCanonicalRun{}, fmt.Errorf("%w: event %d crosses run identity", ErrIncompleteEventLog, event.Sequence)
		}
		payload, ok := decodeRebuildablePayload(event.Payload)
		if !ok {
			return RebuiltCanonicalRun{}, fmt.Errorf("%w: event %d (%s) lacks rebuild_schema", ErrIncompleteEventLog, event.Sequence, event.Type)
		}
		switch event.Type {
		case "v6_run_bootstrapped":
			rebuilt.OrchestratorVersion = stringField(payload, "orchestrator_version")
			rebuilt.DirectorAgentID = stringField(payload, "director_agent_id")
			rebuilt.Status = "running"
		case "run_completed":
			rebuilt.Status = "completed"
		case "source_ingested":
			// Source rows are reconstructed by PersistSourceIngestion callers; the
			// rebuild only proves the event carries identity facts.
		}
		rebuilt.EventsApplied++
		rebuilt.ThroughSequence = event.Sequence
	}
	return rebuilt, nil
}

func (s *PostgresStore) RebuildCanonicalRun(ctx context.Context, sessionID, workspaceID string) (RebuiltCanonicalRun, error) {
	events, err := s.ListRunEvents(ctx, sessionID, workspaceID, 0, 1000)
	if err != nil {
		return RebuiltCanonicalRun{}, err
	}
	return RebuildCanonicalRunFromEvents(events)
}

func decodeRebuildablePayload(raw json.RawMessage) (map[string]any, bool) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, false
	}
	schema, _ := payload[rebuildSchemaField].(string)
	if schema != RebuildSchemaV1 {
		return nil, false
	}
	return payload, true
}

func stringField(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return value
}
