// SPDX-License-Identifier: Apache-2.0

// Package apicontract holds byte-level pins for responses external clients
// parse. It exists as its own package because the packages that define these
// types gate their tests on a reachable Postgres (`internal/handler`'s TestMain
// exits 0 when the database is absent), which would leave a contract pin
// silently unexecuted in exactly the environments where it is most needed.
package apicontract

import (
	"encoding/json"
	"testing"

	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/service"
)

// TestBranchDispatchResponseShapeIsPinned freezes the AReaL-facing dispatch
// contract. Serving branch dispatch by checkpoint resume must not change these
// bytes; a failure here means the client contract moved, not that the pin is
// stale. Every field is populated on purpose, so renaming an `omitempty` field
// is caught too. The pinned bytes carry the mixed-dispatch response fields:
// MarshalJSON injects the quiet-window/timeout defaults when unset, and
// chat_session_id stays off the wire (the AReaL client reads only the four
// fields named below).
func TestBranchDispatchResponseShapeIsPinned(t *testing.T) {
	resp := handler.EnvDispatchResponse{
		ChannelID: "ch-1",
		ProjectID: "proj-1",
		Message:   "dispatched",
		Rollouts: []handler.EnvRolloutResponse{{
			ChannelID:   "ch-1",
			LeaderRunID: "run-1",
			AgentSandboxes: map[string]service.AgentSandboxStatus{
				"a-1": {Status: "ready", SandboxInstanceID: "inst-1", RuntimeID: "rt-1"},
			},
			EnvID:         "env-child-1",
			ProjectID:     "proj-1",
			IssueID:       "issue-1",
			ChatSessionID: "cs-1",
			AgentRunID:    "run-1",
			Error:         "boom",
			Traceback:     "trace",
			SandboxRefs:   []service.SandboxInstanceRef{{InstanceID: "inst-1", WorkspaceID: "ws-1", NodeID: "node-1"}},
			AgentSandboxRefs: map[string]service.SandboxInstanceRef{
				"a-1": {InstanceID: "inst-1", WorkspaceID: "ws-1", NodeID: "node-1"},
			},
		}},
	}
	got, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"channel_id":"ch-1","project_id":"proj-1","rollouts":[{"channel_id":"ch-1","leader_run_id":"run-1","agent_sandboxes":{"a-1":{"status":"ready","sandbox_instance_id":"inst-1","runtime_id":"rt-1"}},"env_id":"env-child-1","project_id":"proj-1","issue_id":"issue-1","agent_run_id":"run-1","error":"boom","traceback":"trace","sandbox_refs":[{"instance_id":"inst-1","workspace_id":"ws-1","node_id":"node-1"}],"agent_sandbox_refs":{"a-1":{"instance_id":"inst-1","workspace_id":"ws-1","node_id":"node-1"}}}],"quiet_window_ms":2000,"total_timeout_seconds":3300,"initial_message_submitted_at":"0001-01-01T00:00:00Z","run_agents":[],"message":"dispatched"}`
	if string(got) != want {
		t.Fatalf("branch dispatch contract drifted.\n got: %s\nwant: %s", got, want)
	}
}

// TestBranchDispatchResponseKeepsTheFieldsAReaLActuallyReads names the four
// fields the AReaL client hard-depends on, so a future change can see what it
// would break rather than only that "some bytes moved":
//
//   - project_id absent  -> the client raises "response missing project_id"
//   - channel_id absent  -> a message dispatch raises "missing channel_id"
//   - rollouts[0].env_id -> the branch frontier the tree search forks from
//   - rollouts[].error   -> its mere presence makes the client reclaim the
//     dispatch and fail, so it must stay absent on success
func TestBranchDispatchResponseKeepsTheFieldsAReaLActuallyReads(t *testing.T) {
	resp := handler.EnvDispatchResponse{
		ChannelID: "ch-1",
		ProjectID: "proj-1",
		Rollouts:  []handler.EnvRolloutResponse{{EnvID: "env-child-1", ProjectID: "proj-1"}},
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded["project_id"] != "proj-1" {
		t.Fatalf("project_id = %v, want proj-1", decoded["project_id"])
	}
	if decoded["channel_id"] != "ch-1" {
		t.Fatalf("channel_id = %v, want ch-1", decoded["channel_id"])
	}
	rollouts, ok := decoded["rollouts"].([]any)
	if !ok || len(rollouts) != 1 {
		t.Fatalf("rollouts = %v, want one entry", decoded["rollouts"])
	}
	first, ok := rollouts[0].(map[string]any)
	if !ok {
		t.Fatalf("rollout[0] = %v, want an object", rollouts[0])
	}
	if first["env_id"] != "env-child-1" {
		t.Fatalf("rollouts[0].env_id = %v, want env-child-1", first["env_id"])
	}
	if _, present := first["error"]; present {
		t.Fatal("a successful rollout must omit error; its presence makes the client reclaim the dispatch")
	}
}
