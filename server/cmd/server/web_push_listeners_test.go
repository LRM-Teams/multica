package main

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestShouldDeliverChannelMessageWebPushMuteGate(t *testing.T) {
	authorID := "user-author"
	recipientID := "user-recipient"
	base := handler.ChannelMessageResponse{
		ID:          "msg-1",
		WorkspaceID: "ws-1",
		ChannelID:   "ch-1",
		Type:        "user",
		AuthorID:    &authorID,
		AuthorName:  "Alice",
		Content:     "hello",
	}

	tests := []struct {
		name      string
		message   handler.ChannelMessageResponse
		actorType string
		actorID   string
		muted     bool
		want      bool
	}{
		{name: "unmuted regular user message", message: base, actorType: "member", actorID: authorID, want: true},
		{name: "muted regular user message suppressed", message: base, actorType: "member", actorID: authorID, muted: true, want: false},
		{name: "muted direct member mention still delivers", message: withMemberMention(base, recipientID), actorType: "member", actorID: authorID, muted: true, want: true},
		{name: "muted all mention still delivers", message: withContent(base, "[@all](mention://all/all) heads up"), actorType: "member", actorID: authorID, muted: true, want: true},
		{name: "self sent user message suppressed", message: withAuthor(base, recipientID), actorType: "member", actorID: recipientID, want: false},
		{name: "system message suppressed", message: withType(base, "system"), actorType: "system", actorID: "system", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldDeliverChannelMessageWebPush(tt.message, tt.actorType, tt.actorID, recipientID, tt.muted)
			if got != tt.want {
				t.Fatalf("shouldDeliverChannelMessageWebPush() = %v, want %v", got, tt.want)
			}
		})
	}
}

func withMemberMention(msg handler.ChannelMessageResponse, recipientID string) handler.ChannelMessageResponse {
	msg.Parts = []protocol.MessagePart{{
		Type:       protocol.MessagePartTypeReference,
		RefType:    "mention",
		RefSubType: "member",
		RefID:      recipientID,
		Label:      "Recipient",
	}}
	return msg
}

func withContent(msg handler.ChannelMessageResponse, content string) handler.ChannelMessageResponse {
	msg.Content = content
	return msg
}

func withAuthor(msg handler.ChannelMessageResponse, authorID string) handler.ChannelMessageResponse {
	msg.AuthorID = &authorID
	return msg
}

func withType(msg handler.ChannelMessageResponse, typ string) handler.ChannelMessageResponse {
	msg.Type = typ
	return msg
}
