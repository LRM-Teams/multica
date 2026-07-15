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

func (h *Handler) DispatchDueWendyHandoffs(ctx context.Context, limit int32) (int, error) {
	if limit <= 0 {
		return 0, nil
	}

	claimToken := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	handoffs, err := h.Queries.ClaimDueWendyHandoffs(ctx, db.ClaimDueWendyHandoffsParams{
		ClaimToken: claimToken,
		Limit:      limit,
	})
	if err != nil {
		return 0, fmt.Errorf("claim due Wendy handoffs: %w", err)
	}

	dispatched := 0
	var firstErr error
	for _, handoff := range handoffs {
		ok, err := h.dispatchClaimedWendyUnlockHandoff(ctx, handoff, claimToken)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			slog.Warn("Wendy handoff dispatch failed", "handoff_id", uuidToString(handoff.ID), "reason_code", handoff.ReasonCode, "error", err)
			continue
		}
		if ok {
			dispatched++
		}
	}
	return dispatched, firstErr
}

func (h *Handler) dispatchClaimedWendyUnlockHandoff(ctx context.Context, handoff db.PendingHandoff, claimToken pgtype.UUID) (bool, error) {
	if !isWendyHandoffReason(handoff.ReasonCode) || !handoff.TargetActorID.Valid || !handoff.ChannelID.Valid || len(handoff.RelatedNodeIds) == 0 {
		return false, h.cancelWendyUnlockHandoff(ctx, handoff, claimToken, "handoff is missing a valid target, channel, or work node")
	}

	managerID, ok := h.resolveGroupManagerForChannel(ctx, handoff.WorkspaceID, handoff.ChannelID)
	if !ok {
		return false, h.cancelWendyUnlockHandoff(ctx, handoff, claimToken, "channel has no group manager (Beckham) to speak the handoff")
	}
	// supervisor holds the channel's group manager (Beckham) — the speaker for
	// this handoff. Kept named supervisor for continuity with the downstream
	// posting/lock helpers.
	supervisor, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
		ID:          managerID,
		WorkspaceID: handoff.WorkspaceID,
	})
	if err != nil || supervisor.ArchivedAt.Valid || !supervisor.RuntimeID.Valid {
		return false, h.cancelWendyUnlockHandoff(ctx, handoff, claimToken, "channel group manager is unavailable")
	}
	// Synthetic run for mention attribution/activity only (not persisted, not a
	// scheduled supervisor radar run).
	run := db.AgentRadarRun{
		WorkspaceID: handoff.WorkspaceID,
		AgentID:     supervisor.ID,
		TriggerKind: "event",
	}
	if handoff.TargetActorType == "member" {
		return h.dispatchClaimedWendyMemberHandoff(ctx, handoff, claimToken, supervisor)
	}
	if handoff.TargetActorType != "agent" {
		return false, h.cancelWendyUnlockHandoff(ctx, handoff, claimToken, "handoff target type is unsupported")
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

	targetNodeID := handoff.RelatedNodeIds[0]
	if handoff.ReasonCode == "block_route" && len(handoff.RelatedNodeIds) > 1 {
		targetNodeID = handoff.RelatedNodeIds[1]
	}
	node, err := h.Queries.GetWorkNodeByID(ctx, targetNodeID)
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
	content, err := composeWendyAgentHandoff(ctx, composer, handoff.ReasonCode, target, node)
	if err != nil {
		return false, h.retryWendyUnlockHandoff(ctx, handoff, claimToken, fmt.Errorf("compose Wendy unlock: %w", err))
	}
	if err := validateWendyHandoffContent(content, "agent", uuidToString(target.ID)); err != nil {
		return false, h.retryWendyUnlockHandoff(ctx, handoff, claimToken, fmt.Errorf("validate Wendy handoff content: %w", err))
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
		NudgeKind:   handoff.ReasonCode,
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

func isWendyHandoffReason(reason string) bool {
	switch reason {
	case "unlock", "block_route", "interrupt_stop", "stalled_ask_why", "progress_nudge", "start_work":
		return true
	default:
		return false
	}
}

func composeWendyAgentHandoff(ctx context.Context, composer WendyComposer, reason string, target db.Agent, node db.WorkNode) (string, error) {
	if reason == "unlock" {
		return composer.ComposeUnlock(ctx, UnlockComposeInput{TargetAgentID: uuidToString(target.ID), TargetAgentName: agentDisplayName(target), IssueTitle: node.Title})
	}
	return templateWendyHandoff(reason, "agent", uuidToString(target.ID), agentDisplayName(target), node.Title), nil
}

func templateWendyHandoff(reason, actorType, actorID, actorName, title string) string {
	mention := mentionMarkdown(actorType, actorID, actorName)
	switch reason {
	case "start_work":
		return fmt.Sprintf("%s 新事项已就绪，请开始处理：%s", mention, title)
	case "block_route":
		return fmt.Sprintf("%s 请优先排查并解除阻塞：%s", mention, title)
	case "interrupt_stop":
		return fmt.Sprintf("%s 请先停止当前工作并等待前置返工完成：%s", mention, title)
	case "stalled_ask_why":
		return fmt.Sprintf("%s %s 似乎卡住了，能说明当前阻碍吗？", mention, title)
	default:
		return fmt.Sprintf("%s 请跟进并推进：%s", mention, title)
	}
}

func (h *Handler) dispatchClaimedWendyMemberHandoff(ctx context.Context, handoff db.PendingHandoff, claimToken pgtype.UUID, supervisor db.Agent) (bool, error) {
	if !h.channelHasAgentMember(ctx, handoff.WorkspaceID, handoff.ChannelID, supervisor.ID) || !h.channelHasMember(ctx, handoff.WorkspaceID, handoff.ChannelID, handoff.TargetActorID) {
		return false, h.cancelWendyUnlockHandoff(ctx, handoff, claimToken, "Wendy or member target is not a channel member")
	}
	var name string
	if err := h.DB.QueryRow(ctx, `SELECT COALESCE(NULLIF(u.display_name, ''), NULLIF(u.name, ''), '成员') FROM member m JOIN "user" u ON u.id = m.user_id WHERE m.id = $1 AND m.workspace_id = $2`, handoff.TargetActorID, handoff.WorkspaceID).Scan(&name); err != nil {
		return false, h.cancelWendyUnlockHandoff(ctx, handoff, claimToken, "member target is unavailable")
	}
	node, err := h.Queries.GetWorkNodeByID(ctx, handoff.RelatedNodeIds[0])
	if err != nil || !radarUUIDsMatch(node.WorkspaceID, handoff.WorkspaceID) {
		return false, h.cancelWendyUnlockHandoff(ctx, handoff, claimToken, "member work node is unavailable")
	}
	content := templateWendyHandoff(handoff.ReasonCode, "member", uuidToString(handoff.TargetActorID), name, node.Title)
	if err := validateWendyHandoffContent(content, "member", uuidToString(handoff.TargetActorID)); err != nil {
		return false, h.retryWendyUnlockHandoff(ctx, handoff, claimToken, err)
	}
	if _, err := h.insertChannelMessage(ctx, handoff.ChannelID, handoff.WorkspaceID, "agent", supervisor.ID, agentDisplayName(supervisor), content, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0); err != nil {
		return false, h.retryWendyUnlockHandoff(ctx, handoff, claimToken, err)
	}
	if _, err := h.Queries.MarkPendingHandoffDone(ctx, db.MarkPendingHandoffDoneParams{ID: handoff.ID, ClaimToken: claimToken}); err != nil {
		return false, err
	}
	return true, nil
}

func (h *Handler) channelHasMember(ctx context.Context, workspaceID, channelID, memberID pgtype.UUID) bool {
	var exists bool
	return h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM channel_member cm
			JOIN member m ON m.user_id = cm.member_id AND m.workspace_id = cm.workspace_id
			WHERE cm.workspace_id = $1 AND cm.channel_id = $2
			  AND cm.member_type = 'user' AND m.id = $3
		)`, workspaceID, channelID, memberID).Scan(&exists) == nil && exists
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
	return validateWendyHandoffContent(content, "agent", targetAgentID)
}

func validateWendyHandoffContent(content, targetType, targetID string) error {
	mentions := util.ParseMentions(content)
	if len(mentions) != 1 || mentions[0].Type != targetType || mentions[0].ID != targetID {
		return errors.New("handoff content must contain only the target mention")
	}
	return nil
}
