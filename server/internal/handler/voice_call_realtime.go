package handler

import (
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/service/voicecall"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (h *Handler) publishVoiceCallUpdated(session voicecall.Session) {
	if h == nil ||
		h.Bus == nil ||
		session.WorkspaceID == "" ||
		session.UserID == "" ||
		session.ID == "" {
		return
	}
	h.Bus.Publish(events.Event{
		Type:             protocol.EventVoiceCallUpdated,
		WorkspaceID:      session.WorkspaceID,
		ActorType:        "system",
		RecipientUserIDs: []string{session.UserID},
		Payload: protocol.VoiceCallUpdatedPayload{
			WorkspaceID: session.WorkspaceID,
			CallID:      session.ID,
		},
	})
}
