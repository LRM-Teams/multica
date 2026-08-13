package handler

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestCreateResearchMessageWithPassportCommitsMatchingArtifact(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}

	sessionID := seedInitializedResearchSessionForSnapshotTest(t)
	workspaceID := parseUUID(testWorkspaceID)
	msg, err := testHandler.createResearchMessageWithPassport(context.Background(), db.CreateResearchMessageParams{
		WorkspaceID:   workspaceID,
		SessionID:     sessionID,
		SenderType:    "user",
		SenderID:      parseUUID(testUserID),
		TargetAgentID: pgtype.UUID{},
		Body:          "continue",
		CardKind:      "chat",
		Meta:          []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("create research message: %v", err)
	}

	var kind string
	if err = testPool.QueryRow(context.Background(), `
		SELECT entity_kind
		FROM research_artifact_passport
		WHERE workspace_id = $1 AND session_id = $2 AND id = $3
	`, workspaceID, sessionID, msg.ID).Scan(&kind); err != nil {
		t.Fatalf("load research message passport: %v", err)
	}
	if kind != "research_message" {
		t.Fatalf("passport kind=%q want research_message", kind)
	}
}
