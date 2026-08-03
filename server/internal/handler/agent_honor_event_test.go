package handler

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
)

func TestAgentFleetClassChangedPayloadIncludesAgentName(t *testing.T) {
	payload := agentFleetClassChangedPayload(service.AgentFleetClassEvent{
		Previous:   "corvette",
		Current:    "frigate",
		FleetScore: 42,
	}, "Frontend Engineer")

	if got := payload["agent_name"]; got != "Frontend Engineer" {
		t.Fatalf("agent_name = %q, want %q", got, "Frontend Engineer")
	}
	if got := payload["class_id"]; got != "frigate" {
		t.Fatalf("class_id = %q, want %q", got, "frigate")
	}
}
