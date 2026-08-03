package handler

import (
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestUndeliveredMentionStructJSON(t *testing.T) {
	t.Parallel()
	u := UndeliveredMention{
		Type:    "member",
		ID:      "user-1",
		Handle:  "alice",
		Reason:  "not_channel_member",
		Actions: []string{"invite"},
	}
	if u.Reason != "not_channel_member" || len(u.Actions) != 1 || u.Actions[0] != "invite" {
		t.Fatalf("unexpected undelivered shape: %+v", u)
	}
}

func TestValidateDMMentionMembershipSkipsGroup(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	err := h.validateDMMentionMembership(t.Context(), ChannelResponse{Kind: "group"}, "hi @someone", nil)
	if err != nil {
		t.Fatalf("group must not use DM membership gate: %v", err)
	}
}

func TestValidateDMMentionMembershipEmptyOK(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	err := h.validateDMMentionMembership(t.Context(), ChannelResponse{Kind: "dm"}, "hello", []protocol.MessagePart{})
	if err != nil {
		t.Fatalf("empty mentions: %v", err)
	}
}
