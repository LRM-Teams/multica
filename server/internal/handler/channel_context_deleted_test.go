package handler

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestAgentChannelContextExcludesDeletedMessages(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	channelID := seedChannelForTest(t, "deleted-agent-context-"+uuid.NewString(), testUserID)
	live, err := testHandler.insertChannelMessage(
		ctx,
		parseUUID(channelID),
		parseUUID(testWorkspaceID),
		"user",
		parseUUID(testUserID),
		"Tester",
		"live main message",
		"multica",
		nil,
		pgtype.UUID{},
		pgtype.UUID{},
		nil,
		0,
	)
	if err != nil {
		t.Fatalf("insert live main message: %v", err)
	}
	deleted, err := testHandler.insertChannelMessage(
		ctx,
		parseUUID(channelID),
		parseUUID(testWorkspaceID),
		"user",
		parseUUID(testUserID),
		"Tester",
		"deleted main secret",
		"multica",
		nil,
		pgtype.UUID{},
		pgtype.UUID{},
		nil,
		0,
	)
	if err != nil {
		t.Fatalf("insert deleted main message: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE channel_message SET deleted_at = now() WHERE id = $1`, deleted.ID); err != nil {
		t.Fatalf("delete main message: %v", err)
	}

	mainMessages := testHandler.recentChannelMessages(ctx, testWorkspaceID, channelID, 20)
	if len(mainMessages) != 1 || mainMessages[0].ID != live.ID {
		t.Fatalf("main context = %+v, want only live message %s", mainMessages, live.ID)
	}

	root, err := testHandler.insertChannelMessage(
		ctx,
		parseUUID(channelID),
		parseUUID(testWorkspaceID),
		"user",
		parseUUID(testUserID),
		"Tester",
		"live root",
		"multica",
		nil,
		pgtype.UUID{},
		pgtype.UUID{},
		strPtr("deleted-agent-thread"),
		0,
	)
	if err != nil {
		t.Fatalf("insert live root: %v", err)
	}
	liveReply, err := testHandler.insertChannelMessage(
		ctx,
		parseUUID(channelID),
		parseUUID(testWorkspaceID),
		"agent",
		parseUUID(testUserID),
		"Agent",
		"live reply",
		"multica",
		nil,
		pgtype.UUID{},
		parseUUID(root.ID),
		strPtr("deleted-agent-thread"),
		0,
	)
	if err != nil {
		t.Fatalf("insert live reply: %v", err)
	}
	deletedReply, err := testHandler.insertChannelMessage(
		ctx,
		parseUUID(channelID),
		parseUUID(testWorkspaceID),
		"user",
		parseUUID(testUserID),
		"Tester",
		"deleted thread secret",
		"multica",
		nil,
		pgtype.UUID{},
		parseUUID(root.ID),
		strPtr("deleted-agent-thread"),
		0,
	)
	if err != nil {
		t.Fatalf("insert deleted reply: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE channel_message SET deleted_at = now() WHERE id = $1`, deletedReply.ID); err != nil {
		t.Fatalf("delete thread reply: %v", err)
	}

	threadMessages := testHandler.channelThreadContextMessages(
		ctx,
		testWorkspaceID,
		channelID,
		root.ID,
		20,
	)
	if len(threadMessages) != 2 ||
		threadMessages[0].ID != root.ID ||
		threadMessages[1].ID != liveReply.ID {
		t.Fatalf(
			"thread context = %+v, want live root %s and reply %s",
			threadMessages,
			root.ID,
			liveReply.ID,
		)
	}
}
