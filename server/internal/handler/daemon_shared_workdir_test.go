package handler

import (
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestTaskToResponseMapsSharedWorkdirFromContext pins the claim-time wiring for
// the shared_sandbox workdir anchor (research D5): a task whose context
// carries a `shared_workdir` marker (stamped at enqueue for shared-mode
// env-dispatch runs) surfaces the sample env id on the claim response so the
// daemon anchors the agent's turn to the sample's single shared workdir.
func TestTaskToResponseMapsSharedWorkdirFromContext(t *testing.T) {
	envID := "11111111-1111-1111-1111-111111111111"
	ctx := []byte(`{"shared_workdir":{"env_id":"` + envID + `"},"ephemeral_sandbox":{"sandbox_instance_id":"s"}}`)

	resp := taskToResponse(db.AgentInboxEvent{Context: ctx}, "ws-1")

	if resp.SharedWorkdirEnvID != envID {
		t.Fatalf("SharedWorkdirEnvID = %q, want %q", resp.SharedWorkdirEnvID, envID)
	}
}

// TestTaskToResponseNoSharedWorkdirForNormalTask ensures a non-shared task
// never carries the workdir anchor: absent / empty / unrelated / malformed
// context all yield an empty SharedWorkdirEnvID, so the daemon keeps today's
// per-agent workdir roots.
func TestTaskToResponseNoSharedWorkdirForNormalTask(t *testing.T) {
	cases := [][]byte{
		nil,
		[]byte(`{}`),
		[]byte(`{"other":"value"}`),
		[]byte(`not json`),
		[]byte(`{"shared_workdir":{}}`), // missing env_id
	}
	for _, ctx := range cases {
		resp := taskToResponse(db.AgentInboxEvent{Context: ctx}, "ws-1")
		if resp.SharedWorkdirEnvID != "" {
			t.Errorf("context %q: expected empty SharedWorkdirEnvID, got %q", ctx, resp.SharedWorkdirEnvID)
		}
	}
}
