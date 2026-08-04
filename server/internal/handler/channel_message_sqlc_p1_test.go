package handler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestGetChannelMessageByID_SqlcFlowsSeqPartsDeletedAt is S4 for task #85 P1.
//
// Write seq / parts / deleted_at on a real channel_message row, then load via
// the sqlc-generated GetChannelMessageByID (no hand-written Scan of those
// columns in this test). If models.ChannelMessage drops those fields or the
// query column list drifts, this fails.
func TestGetChannelMessageByID_SqlcFlowsSeqPartsDeletedAt(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	channelID := seedChannelForTest(t, "p1-channel-message-"+uuid.NewString(), testUserID)

	msg, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID),
		"user", parseUUID(testUserID), "P1 Tester", "s4 flow body",
		"multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM channel_message WHERE id = $1`, msg.ID)
	})

	parts, _ := json.Marshal([]map[string]any{{"type": "text", "text": "s4-parts-marker"}})
	wantSeq := int64(424242)
	if _, err := testPool.Exec(ctx, `
		UPDATE channel_message
		SET seq = $2,
		    parts = $3::jsonb,
		    deleted_at = $4,
		    client_message_id = $5
		WHERE id = $1`,
		msg.ID, wantSeq, parts, time.Now().UTC().Add(-time.Minute), "client-s4-"+uuid.NewString()); err != nil {
		t.Fatalf("seed columns: %v", err)
	}

	// sqlc path only — do not Scan seq/parts/deleted_at by hand here.
	got, err := db.New(testPool).GetChannelMessageByID(ctx, db.GetChannelMessageByIDParams{
		ID:          parseUUID(msg.ID),
		WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("GetChannelMessageByID: %v", err)
	}
	if got.Seq != wantSeq {
		t.Fatalf("seq=%d want %d — sqlc/model did not flow seq", got.Seq, wantSeq)
	}
	if len(got.Parts) == 0 || string(got.Parts) == "null" {
		t.Fatalf("parts empty — sqlc/model did not flow parts: %q", got.Parts)
	}
	if !got.DeletedAt.Valid {
		t.Fatal("deleted_at not Valid — sqlc/model did not flow deleted_at")
	}
	if !got.ClientMessageID.Valid || got.ClientMessageID.String == "" {
		t.Fatalf("client_message_id not set: %+v", got.ClientMessageID)
	}
}
