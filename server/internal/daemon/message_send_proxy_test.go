package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// TestMessageSendHoldPresentationContract pins the warning-row strings that the
// frontend Activity tab renders for a soft-held send. These are the values the
// FE test (dax/fe-soft-hold-activity) asserts against, so any change here must
// be coordinated with the FE test copy.
func TestMessageSendHoldPresentationContract(t *testing.T) {
	if got := messageSendHoldTitle(); got != "Message held — review newer messages before sending" {
		t.Fatalf("messageSendHoldTitle=%q", got)
	}
	if got := messageSendHoldSubtext(3); got != "3 newer messages available — review then resend" {
		t.Fatalf("messageSendHoldSubtext(3)=%q", got)
	}
	if got := messageSendHoldSubtext(0); got != "Send held — review the channel before resending" {
		t.Fatalf("messageSendHoldSubtext(0)=%q", got)
	}
}

// TestObserveMessageSendHoldPublishesSystemActivityEntry verifies that observing
// a held send projects a fail-soft "system" activity entry (which the runner
// activity projection renders as a warning row with title/subtext) for the held
// agent, without erroring.
func TestObserveMessageSendHoldPublishesSystemActivityEntry(t *testing.T) {
	now := time.Date(2026, time.August, 8, 0, 0, 0, 0, time.UTC)
	var sent []protocol.AgentActivityPayload
	producer := newAgentActivityProducer(func() time.Time { return now }, func(payload protocol.AgentActivityPayload) { sent = append(sent, payload) })
	installActivityProducerAgent(t, producer)

	d := New(Config{}, nil)
	d.runnerInstanceID = "daemon-instance-1"
	d.agentActivityProducers["workspace-1"] = producer

	d.observeMessageSendHold("agent-a", "workspace-1", "#general", 3, "server_race")
	if len(sent) != 1 {
		t.Fatalf("sent payloads=%d, want 1", len(sent))
	}
	payload := sent[0]
	if payload.Snapshot.AgentID != "agent-a" {
		t.Fatalf("entry agent_id=%q, want agent-a", payload.Snapshot.AgentID)
	}
	if len(payload.Entries) != 1 {
		t.Fatalf("entries=%d, want 1 system entry", len(payload.Entries))
	}
	entry := payload.Entries[0]
	if entry.Kind != "system" {
		t.Fatalf("entry kind=%q, want system", entry.Kind)
	}
	var body protocol.AgentActivitySystemBody
	if err := json.Unmarshal(entry.Body, &body); err != nil {
		t.Fatalf("decode system entry body: %v", err)
	}
	if body.Title != messageSendHoldTitle() {
		t.Fatalf("entry title=%q, want %q", body.Title, messageSendHoldTitle())
	}
	if body.Text != messageSendHoldSubtext(3) {
		t.Fatalf("entry text=%q, want %q", body.Text, messageSendHoldSubtext(3))
	}
}

// TestObserveMessageSendHoldIsFailSoftWhenAgentNotManaged ensures the hold
// observation never errors or panics when the agent is not currently managed on
// this Runner; the projection is best-effort and the send outcome is untouched.
func TestObserveMessageSendHoldIsFailSoftWhenAgentNotManaged(t *testing.T) {
	d := New(Config{}, nil)
	d.runnerInstanceID = "daemon-instance-1"
	// No producer is registered for the workspace; observe must not panic.
	d.observeMessageSendHold("agent-unknown", "workspace-unknown", "#general", 0, "freshness_unknown")
}
