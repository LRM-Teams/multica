package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const wendyUnlockNudgeKind = "unlock"

func (h *Handler) DispatchDueWendyHandoffs(ctx context.Context, limit int32) (int, error) {
	if limit <= 0 {
		return 0, nil
	}

	claimToken := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	handoffs, err := h.Queries.ClaimDuePendingHandoffs(ctx, db.ClaimDuePendingHandoffsParams{
		ClaimToken: claimToken,
		Urgency:    "fast",
		ReasonCode: "unlock",
		Limit:      limit,
	})
	if err != nil {
		return 0, fmt.Errorf("claim due Wendy unlock handoffs: %w", err)
	}

	dispatched := 0
	var firstErr error
	for _, handoff := range handoffs {
		ok, err := h.dispatchClaimedWendyUnlockHandoff(ctx, handoff, claimToken)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			slog.Warn("Wendy unlock handoff dispatch failed", "handoff_id", uuidToString(handoff.ID), "error", err)
			continue
		}
		if ok {
			dispatched++
		}
	}
	return dispatched, firstErr
}

func (h *Handler) dispatchClaimedWendyUnlockHandoff(ctx context.Context, handoff db.PendingHandoff, claimToken pgtype.UUID) (bool, error) {
	if handoff.TargetActorType != "agent" || !handoff.TargetActorID.Valid || !handoff.ChannelID.Valid || len(handoff.RelatedNodeIds) == 0 {
		return false, h.cancelWendyUnlockHandoff(ctx, handoff, claimToken, "handoff is missing an agent target, channel, or work node")
	}

	supervisorID, err := h.Queries.GetWorkspaceSupervisorAgentID(ctx, handoff.WorkspaceID)
	if err != nil {
		return false, h.cancelWendyUnlockHandoff(ctx, handoff, claimToken, "workspace has no Wendy supervisor binding")
	}
	supervisor, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
		ID:          supervisorID,
		WorkspaceID: handoff.WorkspaceID,
	})
	if err != nil {
		return false, h.cancelWendyUnlockHandoff(ctx, handoff, claimToken, "workspace Wendy supervisor is unavailable")
	}
	run := db.AgentRadarRun{
		WorkspaceID: handoff.WorkspaceID,
		AgentID:     supervisor.ID,
		TriggerKind: "scheduled",
	}
	if err := h.validateScheduledRadarSupervisor(ctx, run, supervisor); err != nil {
		return false, h.cancelWendyUnlockHandoff(ctx, handoff, claimToken, "workspace Wendy supervisor binding is invalid")
	}

	target, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
		ID:          handoff.TargetActorID,
		WorkspaceID: handoff.WorkspaceID,
	})
	if err != nil || target.ArchivedAt.Valid || !target.RuntimeID.Valid {
		return false, h.cancelWendyUnlockHandoff(ctx, handoff, claimToken, "unlock target agent is unavailable")
	}
	if !h.channelHasAgentMember(ctx, handoff.WorkspaceID, handoff.ChannelID, supervisor.ID) ||
		!h.channelHasAgentMember(ctx, handoff.WorkspaceID, handoff.ChannelID, target.ID) {
		return false, h.cancelWendyUnlockHandoff(ctx, handoff, claimToken, "Wendy or unlock target is not a channel member")
	}

	node, err := h.Queries.GetWorkNodeByID(ctx, handoff.RelatedNodeIds[0])
	if err != nil || !radarUUIDsMatch(node.WorkspaceID, handoff.WorkspaceID) {
		return false, h.cancelWendyUnlockHandoff(ctx, handoff, claimToken, "unlock work node is unavailable")
	}
	if node.Status == "done" || node.Status == "cancelled" || node.OwnerType != "agent" || !radarUUIDsMatch(node.OwnerID, target.ID) {
		return false, h.cancelWendyUnlockHandoff(ctx, handoff, claimToken, "unlock work node is no longer eligible")
	}

	channel, found := h.getChannel(ctx, uuidToString(handoff.WorkspaceID), handoff.ChannelID)
	if !found || channel.Kind != "group" || channel.ArchivedAt != nil {
		return false, h.cancelWendyUnlockHandoff(ctx, handoff, claimToken, "unlock channel is unavailable")
	}
	composer := h.WendyComposer
	if composer == nil {
		composer = templateWendyComposer{}
	}
	content, err := composer.ComposeUnlock(ctx, UnlockComposeInput{
		TargetAgentID:   uuidToString(target.ID),
		TargetAgentName: agentDisplayName(target),
		IssueTitle:      node.Title,
	})
	if err != nil {
		return false, h.retryWendyUnlockHandoff(ctx, handoff, claimToken, fmt.Errorf("compose Wendy unlock: %w", err))
	}
	if err := validateWendyUnlockContent(content, uuidToString(target.ID)); err != nil {
		return false, h.retryWendyUnlockHandoff(ctx, handoff, claimToken, fmt.Errorf("validate Wendy unlock content: %w", err))
	}

	if h.TxStarter == nil {
		return false, h.retryWendyUnlockHandoff(ctx, handoff, claimToken, errors.New("Wendy handoff transaction starter unavailable"))
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return false, h.retryWendyUnlockHandoff(ctx, handoff, claimToken, fmt.Errorf("begin Wendy unlock transaction: %w", err))
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()
	qtx := h.Queries.WithTx(tx)
	retryAfterRollback := func(cause error) (bool, error) {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			cause = fmt.Errorf("%w; rollback Wendy unlock transaction: %v", cause, rollbackErr)
		}
		return false, h.retryWendyUnlockHandoff(ctx, handoff, claimToken, cause)
	}

	if err := h.lockWendyUnlockChannelMember(ctx, tx, handoff.WorkspaceID, handoff.ChannelID, supervisor.ID); err != nil {
		return retryAfterRollback(err)
	}
	directive := radarAgentMentionDirective{
		TargetAgent: target,
		Target: radarChannelExecutionTarget{
			ChannelID: handoff.ChannelID,
			Activity:  radarActivityTarget{Kind: "channel", ID: handoff.ChannelID, Trusted: true},
		},
		Channel: channel,
		Content: content,
	}
	execution, err := h.executePreparedRadarAgentMentionInTx(ctx, qtx, tx, run, supervisor, directive)
	if err != nil {
		return retryAfterRollback(err)
	}
	if _, err := qtx.MarkPendingHandoffDone(ctx, db.MarkPendingHandoffDoneParams{
		ID:         handoff.ID,
		ClaimToken: claimToken,
	}); err != nil {
		return retryAfterRollback(fmt.Errorf("mark Wendy unlock handoff done: %w", err))
	}
	if _, err := qtx.TouchWorkNodeWendyNudge(ctx, db.TouchWorkNodeWendyNudgeParams{
		ID:          node.ID,
		WorkspaceID: handoff.WorkspaceID,
		NudgeKind:   wendyUnlockNudgeKind,
	}); err != nil {
		return retryAfterRollback(fmt.Errorf("touch Wendy unlock work node: %w", err))
	}
	if err := tx.Commit(ctx); err != nil {
		return retryAfterRollback(fmt.Errorf("commit Wendy unlock: %w", err))
	}
	committed = true
	if execution.AfterCommit != nil {
		execution.AfterCommit()
	}
	return true, nil
}

func (h *Handler) lockWendyUnlockChannelMember(ctx context.Context, exec db.DBTX, workspaceID, channelID, supervisorID pgtype.UUID) error {
	var memberID pgtype.UUID
	err := exec.QueryRow(ctx, `
		SELECT cm.member_id
		FROM channel ch
		JOIN channel_member cm
		  ON cm.channel_id = ch.id
		 AND cm.workspace_id = ch.workspace_id
		 AND cm.member_type = 'agent'
		WHERE ch.id = $1
		  AND ch.workspace_id = $2
		  AND ch.kind = 'group'
		  AND ch.archived_at IS NULL
		  AND cm.member_id = $3
		FOR SHARE OF ch, cm
	`, channelID, workspaceID, supervisorID).Scan(&memberID)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("Wendy is no longer a channel member")
	}
	if err != nil {
		return fmt.Errorf("lock Wendy channel membership: %w", err)
	}
	return nil
}

func (h *Handler) cancelWendyUnlockHandoff(ctx context.Context, handoff db.PendingHandoff, claimToken pgtype.UUID, reason string) error {
	if _, err := h.Queries.MarkPendingHandoffCancelled(ctx, db.MarkPendingHandoffCancelledParams{
		ID:         handoff.ID,
		ClaimToken: claimToken,
	}); err != nil {
		return fmt.Errorf("cancel Wendy unlock handoff: %w", err)
	}
	slog.Info("Wendy unlock handoff cancelled", "handoff_id", uuidToString(handoff.ID), "reason", reason)
	return nil
}

func (h *Handler) retryWendyUnlockHandoff(ctx context.Context, handoff db.PendingHandoff, claimToken pgtype.UUID, cause error) error {
	if _, err := h.Queries.ReturnClaimedPendingHandoffForRetry(ctx, db.ReturnClaimedPendingHandoffForRetryParams{
		ID:         handoff.ID,
		ClaimToken: claimToken,
	}); err != nil {
		return fmt.Errorf("%v; return Wendy unlock handoff to pending: %w", cause, err)
	}
	return cause
}

func validateWendyUnlockContent(content, targetAgentID string) error {
	mentions := util.ParseMentions(content)
	if len(mentions) != 1 || mentions[0].Type != "agent" || mentions[0].ID != targetAgentID {
		return errors.New("unlock content must contain only the target agent mention")
	}
	return nil
}
