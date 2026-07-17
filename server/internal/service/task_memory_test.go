package service

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestAgentMemoryDeliveryForExecutionScopesUsers(t *testing.T) {
	memberConfig := []byte(`{"scope":"user","subject":{"type":"member","id":"user-frank"}}`)
	if _, _, _, ok := agentMemoryDeliveryForExecution(memberConfig, MemoryExecutionScope{InitiatorType: "member", InitiatorID: "user-frank"}); !ok {
		t.Fatal("Frank's memory was not delivered to Frank")
	}
	if _, _, _, ok := agentMemoryDeliveryForExecution(memberConfig, MemoryExecutionScope{InitiatorType: "member", InitiatorID: "user-jiang"}); ok {
		t.Fatal("Frank's memory leaked into Jiang's execution")
	}
	if _, _, _, ok := agentMemoryDeliveryForExecution(memberConfig, MemoryExecutionScope{InitiatorType: "agent", InitiatorID: "agent-1"}); ok {
		t.Fatal("member memory leaked into an agent-initiated execution")
	}
	if scope, _, _, ok := agentMemoryDeliveryForExecution(nil, MemoryExecutionScope{InitiatorType: "member", InitiatorID: "user-jiang"}); !ok || scope != "agent" {
		t.Fatalf("legacy memory delivery = scope %q, applies %v", scope, ok)
	}
}

func TestAgentMemoryDeliveryFiltersProjectChannelTaskAndExpiry(t *testing.T) {
	config := []byte(`{
          "scope":"user",
          "subject":{"type":"member","id":"user-frank"},
          "applies":{
            "project_ids":["project-a"],
            "channel_ids":["channel-a"],
            "task_types":["chat"],
            "expires_at":"2026-08-01T00:00:00Z"
          }
        }`)
	base := MemoryExecutionScope{InitiatorType: "member", InitiatorID: "user-frank", ProjectID: "project-a", ChannelID: "channel-a", TaskType: "chat", Now: time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)}
	if _, _, _, ok := agentMemoryDeliveryForExecution(config, base); !ok {
		t.Fatal("exact applicability match was not delivered")
	}
	wrongProject := base
	wrongProject.ProjectID = "project-b"
	if _, _, _, ok := agentMemoryDeliveryForExecution(config, wrongProject); ok {
		t.Fatal("project-a memory leaked into project-b")
	}
	wrongChannel := base
	wrongChannel.ChannelID = "channel-b"
	if _, _, _, ok := agentMemoryDeliveryForExecution(config, wrongChannel); ok {
		t.Fatal("channel-a memory leaked into channel-b")
	}
	expired := base
	expired.Now = time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if _, _, _, ok := agentMemoryDeliveryForExecution(config, expired); ok {
		t.Fatal("expired memory was delivered")
	}
}

func TestTeamKnowledgeMemoryDataIsWorkspaceScoped(t *testing.T) {
	item := db.ActiveTeamKnowledgeForExecution{
		ID:      pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
		Kind:    "policy",
		Title:   "Acknowledge before work",
		Content: "Reply before starting substantive work.",
	}
	got := teamKnowledgeMemoryData(item)
	if got.Scope != "workspace" || got.Content != item.Content || got.SyncKey == "" {
		t.Fatalf("team knowledge memory = %#v", got)
	}
}
