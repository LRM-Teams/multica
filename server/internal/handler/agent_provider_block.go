package handler

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

// applyAgentProviderQuotaBlock pins the agent unavailable for a sticky
// provider-quota failure (tasks #64/#77). No-op for transient capacity 429s.
// Owner-facing activity is emitted once per lock window (debounced while an
// active lock already exists).
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
	until := taskfailure.ParseProviderBlockedUntil(errText, now, time.Local)
	reason := string(taskfailure.ReasonAgentProviderQuotaLimit)
	detail := truncateForActivity(errText, 500)

	alreadyLocked := false
	if rows, err := h.Queries.ListAgentProviderBlockByIDs(ctx, []pgtype.UUID{agentID}); err != nil {
		slog.Warn("provider block: failed to read existing lock", "agent_id", uuidToString(agentID), "error", err)
	} else if len(rows) > 0 {
		alreadyLocked = true
	}

	if err := h.Queries.MarkAgentProviderBlocked(ctx, db.MarkAgentProviderBlockedParams{
		ID:                   agentID,
		ProviderBlockedUntil: pgtype.Timestamptz{Time: until, Valid: true},
		ProviderBlockReason:  reason,
		ProviderBlockDetail:  detail,
	}); err != nil {
		slog.Warn("provider block: failed to mark agent blocked", "agent_id", uuidToString(agentID), "error", err)
		return
	}

	if alreadyLocked {
		return
	}

	msg := "Provider quota exhausted"
	if until.After(now) {
		msg = "Provider quota exhausted until " + until.Format("2006-01-02 15:04:05")
	}
	h.recordAgentActivityEvent(ctx, h.DB,
		workspaceID, agentID, runtimeID, taskID,
		activityKindBlocked, "provider_quota_exhausted", "error",
		"agent", agentID, "",
		reason, msg,
		map[string]any{
			"failure_reason":          reason,
			"provider_blocked_until":  until.UTC().Format(time.RFC3339),
			"provider_block_detail":   detail,
			"source_failure_reason":   failureReason,
		},
	)
}

func inboxFailureRetryable(errText, failureReason string, alreadyReplied bool) bool {
	if alreadyReplied {
		return false
	}
	if taskfailure.IsStickyProviderQuotaLock(errText, failureReason) {
		return false
	}
	return true
}
