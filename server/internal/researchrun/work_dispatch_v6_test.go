package researchrun

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestV6AttemptAccessMarshalUsesSnakeCase(t *testing.T) {
	raw, err := json.Marshal(V6AttemptAccess{
		WorkspaceID: "ws",
		RunID:       "run",
		WorkItemID:  "work",
		AttemptID:   "attempt",
		AgentID:     "agent",
		InboxTaskID: "inbox",
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{`"workspace_id"`, `"run_id"`, `"work_item_id"`, `"attempt_id"`, `"agent_id"`, `"inbox_task_id"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("marshal=%s missing %s", text, want)
		}
	}
	for _, stale := range []string{`"WorkspaceID"`, `"RunID"`, `"WorkItemID"`, `"AttemptID"`, `"AgentID"`, `"InboxTaskID"`} {
		if strings.Contains(text, stale) {
			t.Fatalf("marshal=%s still uses exported field name %s", text, stale)
		}
	}
}

func TestParseV6DispatchAccessIDsAcceptsSnakeCaseAndLegacyPascalCase(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		payload string
	}{
		{name: "snake", payload: `{"access":{"workspace_id":"ws","run_id":"run","work_item_id":"work","attempt_id":"attempt","agent_id":"agent","inbox_task_id":""}}`},
		{name: "legacy_pascal", payload: `{"access":{"WorkspaceID":"ws","RunID":"run","WorkItemID":"work","AttemptID":"attempt","AgentID":"agent","InboxTaskID":""}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			attemptID, workItemID, err := parseV6DispatchAccessIDs([]byte(tc.payload))
			if err != nil {
				t.Fatal(err)
			}
			if attemptID != "attempt" || workItemID != "work" {
				t.Fatalf("attempt=%q work=%q", attemptID, workItemID)
			}
			var intent V6DispatchIntentPayload
			if err := json.Unmarshal([]byte(tc.payload), &intent); err != nil {
				t.Fatal(err)
			}
			if intent.Access.InboxTaskID != "" {
				t.Fatalf("empty inbox_task_id became %q", intent.Access.InboxTaskID)
			}
		})
	}
}
