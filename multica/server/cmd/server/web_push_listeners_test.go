package main

import (
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestRegisterWebPushListenersDoesNotSubscribeInboxNew(t *testing.T) {
	bus := events.New()
	registerWebPushListeners(bus, nil, handler.Config{
		WebPushVAPIDPublicKey:  "public",
		WebPushVAPIDPrivateKey: "private",
		WebPushVAPIDSubject:    "mailto:test@example.com",
	})

	if got := busListenerCount(bus, protocol.EventInboxNew); got != 0 {
		t.Fatalf("inbox:new must not fan out to desktop web push, got %d listeners", got)
	}
	if got := busListenerCount(bus, protocol.EventChannelMessage); got != 1 {
		t.Fatalf("channel:message should keep desktop web push fanout, got %d listeners", got)
	}
}

func TestShouldDeliverChannelMessageWebPushLRM769(t *testing.T) {
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
		name        string
		message     handler.ChannelMessageResponse
		actorType   string
		actorID     string
		kind        string
		notifyLevel string
		want        bool
	}{
		{name: "default group ordinary message delivers", message: base, actorType: "member", actorID: authorID, kind: "channel", notifyLevel: "default", want: true},
		{name: "all group ordinary message delivers", message: base, actorType: "member", actorID: authorID, kind: "channel", notifyLevel: "all", want: true},
		{name: "mentions group ordinary message suppressed", message: base, actorType: "member", actorID: authorID, kind: "channel", notifyLevel: "mentions", want: false},
		{name: "mentions group mention still delivers", message: withMemberMention(base, recipientID), actorType: "member", actorID: authorID, kind: "channel", notifyLevel: "mentions", want: true},
		{name: "mentions group all mention delivers", message: withContent(base, "[@all](mention://all/all) heads up"), actorType: "member", actorID: authorID, kind: "channel", notifyLevel: "mentions", want: true},
		{name: "muted group ordinary message suppressed", message: base, actorType: "member", actorID: authorID, kind: "channel", notifyLevel: "muted", want: false},
		{name: "muted group mention suppressed", message: withMemberMention(base, recipientID), actorType: "member", actorID: authorID, kind: "channel", notifyLevel: "muted", want: false},
		{name: "muted group all mention suppressed", message: withContent(base, "[@all](mention://all/all) heads up"), actorType: "member", actorID: authorID, kind: "channel", notifyLevel: "muted", want: false},
		{name: "dm ordinary message delivers even if muted", message: base, actorType: "member", actorID: authorID, kind: "dm", notifyLevel: "muted", want: true},
		{name: "self sent user message suppressed", message: withAuthor(base, recipientID), actorType: "member", actorID: recipientID, kind: "dm", notifyLevel: "all", want: false},
		{name: "system message suppressed", message: withType(base, "system"), actorType: "system", actorID: "system", kind: "channel", notifyLevel: "all", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldDeliverChannelMessageWebPush(tt.message, tt.actorType, tt.actorID, recipientID, tt.kind, tt.notifyLevel)
			if got != tt.want {
				t.Fatalf("shouldDeliverChannelMessageWebPush() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWebPushEndpointHashDoesNotExposeEndpoint(t *testing.T) {
	endpoint := "https://push.example.test/send/token-secret"
	hash := webPushEndpointHash(endpoint)
	if len(hash) != 12 {
		t.Fatalf("endpoint hash length = %d, want 12", len(hash))
	}
	if hash == endpoint || hash == "token-secret" {
		t.Fatalf("endpoint hash exposed raw endpoint: %q", hash)
	}
	if got := webPushEndpointHash("  " + endpoint + "  "); got != hash {
		t.Fatalf("endpoint hash should trim whitespace, got %q want %q", got, hash)
	}
}

func TestWebPushPayloadLogFields(t *testing.T) {
	userID := pgtype.UUID{Bytes: [16]byte{0x12, 0x34}, Valid: true}
	fields := webPushPayloadLogFields(userID, webPushInboxPayload{
		WorkspaceID: "ws-1",
		ItemID:      "msg-1",
		ChannelID:   "ch-1",
	})

	want := []any{"recipient_id", "12340000-0000-0000-0000-000000000000", "workspace_id", "ws-1", "message_id", "msg-1", "channel_id", "ch-1"}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("log fields = %#v, want %#v", fields, want)
	}
}

func busListenerCount(bus *events.Bus, eventType string) int {
	listeners := reflect.ValueOf(bus).Elem().FieldByName("listeners")
	registered := listeners.MapIndex(reflect.ValueOf(eventType))
	if !registered.IsValid() {
		return 0
	}
	return registered.Len()
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
