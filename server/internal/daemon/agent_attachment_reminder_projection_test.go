package daemon

import (
	"os"
	"strings"
	"testing"
	"time"

	"log/slog"
)

func TestReminderCacheCleanupCannotDetachAgent(t *testing.T) {
	registry := newLocalAgentAttachmentRegistry(t.TempDir(), nil)
	if _, err := registry.Apply("workspace-a", AgentAttachmentEvent{
		Kind: AgentAttachmentEventAttach, AgentID: "agent-a", RuntimeID: "runtime-a",
		AttachmentGeneration: 1, LifecycleSeq: 1,
	}); err != nil {
		t.Fatal(err)
	}
	clock := &fakeReminderClock{now: time.Now()}
	cache := newReminderCache(clock, slog.Default(), nil)
	cache.upsert(reminderJob("reminder-a", "agent-a", 1, clock.now.Add(time.Hour)))
	if err := cache.removeOwner("agent-a"); err != nil {
		t.Fatal(err)
	}
	if _, found := registry.Resolve("workspace-a", "agent-a"); !found {
		t.Fatal("Reminder cache cleanup detached the Agent")
	}
}

func TestReminderProductionReadsAttachmentRegistryInsteadOfManagerQueries(t *testing.T) {
	raw, err := os.ReadFile("wakeup.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"reminderAgents.get(",
		"reminderAgents.residentAgentIDs(",
		"reminderAgents.runtimeResidencies(",
		"reminderAgents.lifecycleCursors(",
		"reminderAgents.retiredAgentIDs(",
		"reminderAgents.reconcileRuntimeSet(",
	} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("Reminder projection still reads manager query %q", forbidden)
		}
	}
}
