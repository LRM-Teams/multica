package handler

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestAgentActionFinalPayloadHashIncludesAllFinalCreateInputsWithoutPersistingSecrets(t *testing.T) {
	params := db.CreateAgentParams{
		Name:               "proposal-agent",
		DisplayName:        "Proposal Agent",
		Description:        "summary",
		Instructions:       "follow the project rules",
		RuntimeMode:        "cloud",
		RuntimeConfig:      []byte(`{"endpoint":"https://example.test"}`),
		RuntimeID:          parseUUID("11111111-1111-4111-8111-111111111111"),
		MaxConcurrentTasks: 3,
		CustomEnv:          []byte(`{"API_KEY":"secret-a"}`),
		CustomArgs:         []byte(`["--safe"]`),
		McpConfig:          []byte(`{"token":"secret-b"}`),
		AvatarUrl:          pgtype.Text{String: "https://example.test/avatar.png", Valid: true},
		AvatarSource:       "picked",
		Model:              pgtype.Text{String: "composer-1.5", Valid: true},
		ThinkingLevel:      pgtype.Text{String: "high", Valid: true},
	}
	proposed := map[string]any{"preferred_computer": "Mac Studio"}
	base := agentActionFinalPayloadHash(params, proposed)

	mutations := []struct {
		name string
		edit func(*db.CreateAgentParams)
	}{
		{"instructions", func(p *db.CreateAgentParams) { p.Instructions = "different instructions" }},
		{"runtime config", func(p *db.CreateAgentParams) { p.RuntimeConfig = []byte(`{"endpoint":"https://other.test"}`) }},
		{"custom env", func(p *db.CreateAgentParams) { p.CustomEnv = []byte(`{"API_KEY":"secret-c"}`) }},
		{"custom args", func(p *db.CreateAgentParams) { p.CustomArgs = []byte(`["--different"]`) }},
		{"mcp config", func(p *db.CreateAgentParams) { p.McpConfig = []byte(`{"token":"secret-d"}`) }},
		{"avatar", func(p *db.CreateAgentParams) {
			p.AvatarUrl = pgtype.Text{String: "https://example.test/other.png", Valid: true}
		}},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			changed := params
			tc.edit(&changed)
			if got := agentActionFinalPayloadHash(changed, proposed); got == base {
				t.Fatalf("%s did not change the idempotency hash", tc.name)
			}
		})
	}

	stored := agentActionFinalPayload(params, proposed)
	for _, forbidden := range []string{"instructions", "runtime_config", "custom_env", "custom_args", "mcp_config"} {
		if _, leaked := stored[forbidden]; leaked {
			t.Fatalf("stored final payload leaked %s: %#v", forbidden, stored)
		}
	}
}
