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

func TestAgentMemoryDeliveryExcludesUserOnGroupChatUnlessBringIn(t *testing.T) {
	memberConfig := []byte(`{"scope":"user","subject":{"type":"member","id":"user-frank"}}`)
	group := MemoryExecutionScope{
		InitiatorType: "member",
		InitiatorID:   "user-frank",
		ChannelID:     "channel-a",
		ChannelKind:   "group",
		ChatSessionID: "chat-1",
		MessageTexts:  []string{"设定个总目标"},
		TaskType:      "chat",
	}
	if _, _, _, ok := agentMemoryDeliveryForExecution(memberConfig, group); ok {
		t.Fatal("group chat must exclude user-scoped DB memory by default")
	}
	bringIn := group
	bringIn.MessageTexts = []string{"请带上我的个人偏好"}
	if _, _, _, ok := agentMemoryDeliveryForExecution(memberConfig, bringIn); !ok {
		t.Fatal("explicit bring-in must allow user-scoped DB memory")
	}
}

func TestAgentMemoryDeliveryRequiresBoundProject(t *testing.T) {
	projectConfig := []byte(`{"scope":"project","subject":{"type":"project","id":"project-a"}}`)
	if _, _, _, ok := agentMemoryDeliveryForExecution(projectConfig, MemoryExecutionScope{InitiatorType: "member", InitiatorID: "user-frank"}); ok {
		t.Fatal("project-scoped memory must not load when ProjectID is empty")
	}
	if _, _, _, ok := agentMemoryDeliveryForExecution(projectConfig, MemoryExecutionScope{ProjectID: "project-a"}); !ok {
		t.Fatal("project-scoped memory should load when ProjectID is bound")
	}
	if _, _, _, ok := agentMemoryDeliveryForExecution(projectConfig, MemoryExecutionScope{ProjectID: "project-b"}); ok {
		t.Fatal("project-a memory leaked into project-b")
	}
}

func TestAgentMemoryDeliveryFailsClosedForMalformedOrUnknownConfig(t *testing.T) {
	tests := [][]byte{
		[]byte(`{"scope":"user","subject":`),
		[]byte(`{"scope":{"unexpected":true}}`),
		[]byte(`{"scope":"unknown"}`),
		[]byte(`{"scope":"user","subject":{"type":"member"}}`),
		[]byte(`null`),
	}
	for _, config := range tests {
		if _, _, _, ok := agentMemoryDeliveryForExecution(config, MemoryExecutionScope{InitiatorType: "member", InitiatorID: "user-frank"}); ok {
			t.Fatalf("invalid config was delivered: %s", config)
		}
	}
}

func TestAgentMemoryDeliveryScopesChannelsBySubject(t *testing.T) {
	config := []byte(`{"scope":"channel","subject":{"type":"channel","id":"channel-a"}}`)
	if _, _, _, ok := agentMemoryDeliveryForExecution(config, MemoryExecutionScope{ChannelID: "channel-a"}); !ok {
		t.Fatal("channel-a memory was not delivered to channel-a")
	}
	if _, _, _, ok := agentMemoryDeliveryForExecution(config, MemoryExecutionScope{ChannelID: "channel-b"}); ok {
		t.Fatal("channel-a memory leaked into channel-b")
	}
}

func TestTeamKnowledgeMemoryDataIsWorkspaceScoped(t *testing.T) {
	item := db.ListActiveTeamKnowledgeForExecutionRow{
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
