package handler

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// explicitModelForRuntime picks a concrete provider model for bootstrap/system
// agents (Wendy, onboarding assistant, fleet seeds). Never returns "auto".
func explicitModelForRuntime(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "cursor":
		return "composer-1.5"
	case "anthropic", "claude", "pi":
		return "claude-sonnet-4-6"
	case "openai", "codex", "copilot":
		return "gpt-5.4"
	default:
		return "composer-1.5"
	}
}

func pgTextModelForRuntime(provider string) pgtype.Text {
	model := explicitModelForRuntime(provider)
	return pgtype.Text{String: model, Valid: model != ""}
}

func ensureAgentHasExplicitModel(ctx context.Context, q *db.Queries, agent db.Agent, provider string) (db.Agent, error) {
	if strings.TrimSpace(agent.Model.String) != "" {
		return agent, nil
	}
	model := explicitModelForRuntime(provider)
	return q.UpdateAgent(ctx, db.UpdateAgentParams{
		ID:    agent.ID,
		Model: pgtype.Text{String: model, Valid: true},
	})
}
