package handler

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
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

func TestAgentHonorAudienceRequiresNamedOwnedAgent(t *testing.T) {
	ownerID := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}

	tests := []struct {
		name      string
		agent     db.Agent
		queryErr  error
		wantOK    bool
		wantName  string
		wantUsers int
	}{
		{
			name: "display name",
			agent: db.Agent{
				Name:        "frontend-engineer",
				DisplayName: "前端工程师",
				OwnerID:     ownerID,
			},
			wantOK:    true,
			wantName:  "前端工程师",
			wantUsers: 1,
		},
		{
			name:     "query failure",
			queryErr: errors.New("database unavailable"),
			wantOK:   false,
		},
		{
			name: "missing owner",
			agent: db.Agent{
				Name: "frontend-engineer",
			},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recipients, agentName, ok := agentHonorAudience(tt.agent, tt.queryErr)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if agentName != tt.wantName {
				t.Fatalf("agent name = %q, want %q", agentName, tt.wantName)
			}
			if len(recipients) != tt.wantUsers {
				t.Fatalf("recipient count = %d, want %d", len(recipients), tt.wantUsers)
			}
		})
	}
}
