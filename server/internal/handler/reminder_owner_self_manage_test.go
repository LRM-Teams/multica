package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// LRM-1057: owning-agent cancel/update/snooze must not require the turn
// initiator to match reminder.initiator_user_id.
func TestAuthorizeNaturalLanguageReminderMutationAllowsOwningAgent(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/agent/reminders/cancel", nil)

	ok := h.authorizeNaturalLanguageReminderMutation(rec, req, nil, agentTransportSource{}, agentReminder{})
	if !ok {
		t.Fatalf("expected owning-agent mutation to be allowed, got deny status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("expected no error body on allow, got %s", rec.Body.String())
	}
}
