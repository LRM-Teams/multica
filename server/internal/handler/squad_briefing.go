package handler

import (
	"context"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Squad product retired (Frank 2026-07-28). Briefing is a no-op so historical
// enqueue paths that still reference this helper never teach agents dead CLI.
func buildSquadLeaderBriefing(ctx context.Context, q *db.Queries, squad db.Squad) string {
	_ = ctx
	_ = q
	_ = squad
	return ""
}
