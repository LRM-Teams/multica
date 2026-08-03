package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/memorysync"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestMemorySyncStrategyADecisions(t *testing.T) {
	// Mirror the handler branch mapping so regressions are obvious.
	cases := []struct {
		existing, incoming, want string
	}{
		{"先报进度", "先报进度", memorysync.DecisionSame},
		{"先报进度", "长任务开始前先报进度并持续汇报", memorysync.DecisionMoreSpecific},
		{"紧急也要先报进度", "紧急时别报进度，直接干", memorysync.DecisionOpposed},
	}
	for _, tc := range cases {
		got := memorysync.Compare(tc.existing, tc.incoming)
		if got.Decision != tc.want {
			t.Fatalf("%q vs %q: got %s want %s (%s)", tc.existing, tc.incoming, got.Decision, tc.want, got.Reason)
		}
	}
}

func TestAgentMemoryCenterIncrementalDeleteAndNoResurrection(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	var agentID string
	if err := testPool.QueryRow(ctx, `SELECT id::text FROM agent WHERE workspace_id = $1 ORDER BY created_at ASC LIMIT 1`, testWorkspaceID).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `DELETE FROM agent_memory_sync_entry WHERE agent_id = $1`, agentID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_memory_sync_entry WHERE agent_id = $1`, agentID)
	})

	atom := protocol.AgentMemoryCenterSyncAtom{
		RelPath:   "users/member-1/USER.md",
		Scope:     "user",
		SubjectID: "member-1",
		Kind:      memorysync.KindPreference,
		Topic:     "progress_feedback",
		Content:   "长任务开始前先反馈进度",
	}
	identity := memorysync.IdentityKey(atom.Scope, atom.SubjectID, atom.Kind, atom.Topic, atom.Content)

	syncMemoryCenterForTest(t, protocol.AgentMemoryCenterSyncReport{
		AgentID:    agentID,
		RuntimeID:  testRuntimeID,
		MutationID: uuid.NewString(),
		Entries:    []protocol.AgentMemoryCenterSyncAtom{atom},
	}, func(resp protocol.AgentMemoryCenterSyncResponse) {
		if resp.ProtocolVersion != 2 || resp.Accepted != 1 {
			t.Fatalf("initial sync response=%+v", resp)
		}
	})

	first := hydrateMemoryCenterForTest(t, protocol.AgentMemoryHydrateRequest{AgentID: agentID, RuntimeID: testRuntimeID})
	if len(first.Active) != 1 || first.Active[0].IdentityKey != identity || first.Cursor <= 0 {
		t.Fatalf("initial hydrate=%+v", first)
	}

	syncMemoryCenterForTest(t, protocol.AgentMemoryCenterSyncReport{
		AgentID:             agentID,
		RuntimeID:           testRuntimeID,
		MutationID:          uuid.NewString(),
		DeletedIdentityKeys: []string{identity},
	}, func(resp protocol.AgentMemoryCenterSyncResponse) {
		if resp.Deleted != 1 {
			t.Fatalf("delete response=%+v", resp)
		}
	})

	delta := hydrateMemoryCenterForTest(t, protocol.AgentMemoryHydrateRequest{AgentID: agentID, RuntimeID: testRuntimeID, Cursor: first.Cursor})
	if len(delta.Active) != 0 || len(delta.Deleted) == 0 || delta.Deleted[0].IdentityKey != identity || delta.Cursor <= first.Cursor {
		t.Fatalf("delete delta=%+v", delta)
	}

	syncMemoryCenterForTest(t, protocol.AgentMemoryCenterSyncReport{
		AgentID:    agentID,
		RuntimeID:  testRuntimeID,
		MutationID: uuid.NewString(),
		Entries:    []protocol.AgentMemoryCenterSyncAtom{atom},
	}, func(resp protocol.AgentMemoryCenterSyncResponse) {
		if len(resp.TombstonedIdentityKeys) != 1 || resp.TombstonedIdentityKeys[0] != identity {
			t.Fatalf("stale replay response=%+v", resp)
		}
	})

	var activeCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_memory_sync_entry WHERE agent_id = $1 AND identity_key = $2 AND status = 'active'`, agentID, identity).Scan(&activeCount); err != nil {
		t.Fatal(err)
	}
	if activeCount != 0 {
		t.Fatalf("active rows after stale replay=%d", activeCount)
	}
}

func TestAgentMemoryCenterRejectsDeviceLocalContent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	var agentID string
	if err := testPool.QueryRow(context.Background(), `SELECT id::text FROM agent WHERE workspace_id = $1 ORDER BY created_at ASC LIMIT 1`, testWorkspaceID).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	syncMemoryCenterForTest(t, protocol.AgentMemoryCenterSyncReport{
		AgentID:   agentID,
		RuntimeID: testRuntimeID,
		Entries: []protocol.AgentMemoryCenterSyncAtom{{
			RelPath: "memory/MEMORY.md",
			Scope:   "agent",
			Kind:    memorysync.KindFact,
			Content: "Checkout is /home/alice/private/multica",
		}},
	}, func(resp protocol.AgentMemoryCenterSyncResponse) {
		if resp.Accepted != 0 || resp.Skipped != 1 {
			t.Fatalf("device-local response=%+v", resp)
		}
	})
}

func syncMemoryCenterForTest(t *testing.T, body protocol.AgentMemoryCenterSyncReport, assert func(protocol.AgentMemoryCenterSyncResponse)) {
	t.Helper()
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-memory-center/sync", body, testWorkspaceID, "daemon-memory-sync-test")
	rec := httptest.NewRecorder()
	testHandler.SyncAgentMemoryCenter(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sync status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp protocol.AgentMemoryCenterSyncResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	assert(resp)
}

func hydrateMemoryCenterForTest(t *testing.T, body protocol.AgentMemoryHydrateRequest) protocol.AgentMemoryHydrateResponse {
	t.Helper()
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/agent-memory-center/hydrate", body, testWorkspaceID, "daemon-memory-sync-test")
	rec := httptest.NewRecorder()
	testHandler.HydrateAgentMemoryCenter(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("hydrate status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp protocol.AgentMemoryHydrateResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	return resp
}
