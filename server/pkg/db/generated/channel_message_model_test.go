package db

import (
	"reflect"
	"testing"
)

// TestChannelMessageHasP1Columns is S2/S4 static half of task #85 P1:
// models.ChannelMessage must expose the columns that real channel_message
// rows carry. If someone shrinks the struct again, this fails before CI
// spends minutes on integration.
func TestChannelMessageHasP1Columns(t *testing.T) {
	want := []string{
		"ID", "ChannelID", "WorkspaceID", "AuthorType", "AuthorID", "AuthorName",
		"Content", "Source", "ExternalMessageID", "CreatedAt", "ThreadID", "TriggerDepth",
		"ReplyToMessageID", "ThreadRootMessageID", "Parts", "ConversationID", "Seq",
		"ClientMessageID", "EditedAt", "DeletedAt", "QuoteMessageID", "QuoteSnapshot",
		"MembershipGenerationID",
	}
	rt := reflect.TypeOf(ChannelMessage{})
	have := map[string]bool{}
	for i := 0; i < rt.NumField(); i++ {
		have[rt.Field(i).Name] = true
	}
	for _, name := range want {
		if !have[name] {
			t.Errorf("ChannelMessage missing field %s (task #85 P1)", name)
		}
	}
}
