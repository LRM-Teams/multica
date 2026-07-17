package service

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestAgentMemoryDeliveryForExecutionScopesUsers(t *testing.T) {
	memberConfig := []byte(`{"scope":"user","subject":{"type":"member","id":"user-frank"}}`)
	if _, _, _, ok := agentMemoryDeliveryForExecution(memberConfig, "member", "user-frank"); !ok {
		t.Fatal("Frank's memory was not delivered to Frank")
	}
	if _, _, _, ok := agentMemoryDeliveryForExecution(memberConfig, "member", "user-jiang"); ok {
		t.Fatal("Frank's memory leaked into Jiang's execution")
	}
	if _, _, _, ok := agentMemoryDeliveryForExecution(memberConfig, "agent", "agent-1"); ok {
		t.Fatal("member memory leaked into an agent-initiated execution")
	}
	if scope, _, _, ok := agentMemoryDeliveryForExecution(nil, "member", "user-jiang"); !ok || scope != "agent" {
		t.Fatalf("legacy memory delivery = scope %q, applies %v", scope, ok)
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
