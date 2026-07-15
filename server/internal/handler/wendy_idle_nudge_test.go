package handler

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// #3 idle nudge: a managed group with a team but nobody working must trigger a
// Beckham idle-nudge review, and be debounced afterward.
func TestDispatchIdleNudgesTriggersIdleGroup(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	prev := IdleNudgeDebounce
	IdleNudgeDebounce = time.Minute
	t.Cleanup(func() { IdleNudgeDebounce = prev })

	manager := createRadarSupervisorForExecutorTest(t) // runtime-ready agent
	worker := createHandlerTestAgent(t, "Idle Worker "+uuid.NewString(), nil)
	channelID := seedChannelForTest(t, "idle-nudge-"+uuid.NewString(), testUserID)
	addRadarAgentMembersForExecutorTest(t, channelID, manager.ID.String(), worker)
	bindChannelGroupManagerForTest(t, channelID, manager.ID.String())

	if _, err := testHandler.DispatchIdleNudges(ctx, 10); err != nil {
		t.Fatalf("dispatch idle nudges: %v", err)
	}
	if !idleNudgeRunExists(t, channelID, manager.ID.String()) {
		t.Fatal("expected an idle-nudge radar run for the idle group, found none")
	}

	// Debounce: a second sweep must not enqueue another idle nudge for it.
	before := idleNudgeRunCount(t, channelID)
	if _, err := testHandler.DispatchIdleNudges(ctx, 10); err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
	if after := idleNudgeRunCount(t, channelID); after != before {
		t.Fatalf("idle-nudge runs = %d after debounce window, want %d (no new nudge)", after, before)
	}
}

// #3 idle nudge: when an agent member is actively working, the group is NOT
// idle-nudged.
func TestDispatchIdleNudgesSkipsWhenAgentWorking(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	prev := IdleNudgeDebounce
	IdleNudgeDebounce = time.Minute
	t.Cleanup(func() { IdleNudgeDebounce = prev })

	manager := createRadarSupervisorForExecutorTest(t)
	worker := createHandlerTestAgent(t, "Busy Worker "+uuid.NewString(), nil)
	channelID := seedChannelForTest(t, "idle-busy-"+uuid.NewString(), testUserID)
	addRadarAgentMembersForExecutorTest(t, channelID, manager.ID.String(), worker)
	bindChannelGroupManagerForTest(t, channelID, manager.ID.String())

	// Worker is actively working.
	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, context)
		SELECT id, runtime_id, 'running', '{"type":"chat"}'::jsonb FROM agent WHERE id = $1
		RETURNING id
	`, worker).Scan(&taskID); err != nil {
		t.Fatalf("seed active task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	if _, err := testHandler.DispatchIdleNudges(ctx, 10); err != nil {
		t.Fatalf("dispatch idle nudges: %v", err)
	}
	if idleNudgeRunExists(t, channelID, manager.ID.String()) {
		t.Fatal("idle-nudge was dispatched even though an agent is working")
	}
}

// #2: a mention prompt whose trigger was authored by the channel's group manager
// (Beckham) is directed, not a weak agent-to-agent notification.
func TestChannelMentionPromptDirectedForGroupManager(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	manager := createRadarSupervisorForExecutorTest(t)
	channelID := seedChannelForTest(t, "gm-directed-"+uuid.NewString(), testUserID)
	bindChannelGroupManagerForTest(t, channelID, manager.ID.String())

	managerID := uuidToString(manager.ID)
	ch := ChannelResponse{ID: channelID, WorkspaceID: testWorkspaceID, Name: "gm-directed", Kind: "group"}
	trigger := ChannelMessageResponse{Type: "agent", AuthorID: &managerID, Content: "@worker 报进度"}

	prompt := testHandler.buildChannelMentionPrompt(ctx, ch, trigger, channelFacilitatorState{})
	if !containsAll(prompt, "贝克汉姆", "DIRECTED", "group manager coordinating this channel") {
		t.Fatalf("group-manager mention prompt not directed:\n%s", prompt)
	}

	// A non-manager agent author stays weak (no directed clause).
	other := createHandlerTestAgent(t, "Other Agent "+uuid.NewString(), nil)
	trigger2 := ChannelMessageResponse{Type: "agent", AuthorID: &other, Content: "@worker 看看"}
	prompt2 := testHandler.buildChannelMentionPrompt(ctx, ch, trigger2, channelFacilitatorState{})
	if containsAll(prompt2, "the group manager coordinating this channel") {
		t.Fatalf("non-manager mention should not get the directed group-manager clause:\n%s", prompt2)
	}
}

func idleNudgeRunExists(t *testing.T, channelID, managerID string) bool {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM agent_radar_run
		WHERE agent_id = $1 AND trigger_ref LIKE 'idle_nudge:' || $2 || ':%'
	`, managerID, channelID).Scan(&n); err != nil {
		t.Fatalf("count idle-nudge runs: %v", err)
	}
	return n > 0
}

func idleNudgeRunCount(t *testing.T, channelID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM agent_radar_run WHERE trigger_ref LIKE 'idle_nudge:' || $1 || ':%'
	`, channelID).Scan(&n); err != nil {
		t.Fatalf("count idle-nudge runs: %v", err)
	}
	return n
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
