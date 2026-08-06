package handler

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

// applyAgentProviderQuotaBlock pins agent *display* as provider-quota blocked
// (tasks #64/#77). No-op for transient capacity 429s. Owner-facing activity is
// emitted once per lock window. Claim/drain skip while locked is task #92.
func (h *Handler) applyAgentProviderQuotaBlock(
	ctx context.Context,
	workspaceID, agentID, runtimeID, taskID pgtype.UUID,
	errText, failureReason string,
) {
	if h == nil || h.Queries == nil {
		return
	}
	if !taskfailure.IsStickyProviderQuotaLock(errText, failureReason) {
		return
	}
	now := time.Now()
	until, untilOK := taskfailure.ParseProviderBlockedUntil(errText, now, time.Local)
	detail := truncateForActivity(errText, 500)

	alreadyLocked := false
	if rows, err := h.Queries.ListAgentProviderBlockByIDs(ctx, []pgtype.UUID{agentID}); err != nil {
		slog.Warn("provider block: failed to read existing lock", "agent_id", uuidToString(agentID), "error", err)
	} else if len(rows) > 0 {
		alreadyLocked = true
	}

	untilArg := pgtype.Timestamptz{}
	if untilOK {
		untilArg = pgtype.Timestamptz{Time: until, Valid: true}
	}
	if err := h.Queries.MarkAgentProviderBlocked(ctx, db.MarkAgentProviderBlockedParams{
		ID:                   agentID,
		ProviderBlockedUntil: untilArg,
		ProviderBlockDetail:  detail,
	}); err != nil {
		slog.Warn("provider block: failed to mark agent blocked", "agent_id", uuidToString(agentID), "error", err)
		return
	}

	if alreadyLocked {
		return
	}

}

// inboxFailureRetryable: sticky provider-quota failures are not auto-retryable
// (task #92 / Parker: quota is an external hard constraint). Already-replied
// turns stay non-retryable as before.
func inboxFailureRetryable(errText, failureReason string, alreadyReplied bool) bool {
	if alreadyReplied {
		return false
	}
	if taskfailure.IsStickyProviderQuotaLock(errText, failureReason) {
		return false
	}
	return true
}
