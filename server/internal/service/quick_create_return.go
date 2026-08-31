package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/messageparts"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	quickCreateReturnMetadataKey = "chat_issue_return"
)

type quickCreateReturnTarget struct {
	channelID   pgtype.UUID
	channelKind string
	threadRoot  pgtype.UUID
	threadID    *string
	depth       int
}

type quickCreateReturnMessage struct {
	id                  pgtype.UUID
	channelID           pgtype.UUID
	workspaceID         pgtype.UUID
	authorType          string
	authorID            pgtype.UUID
	authorName          string
	content             string
	parts               []byte
	source              string
	externalMessageID   pgtype.Text
	clientMessageID     pgtype.Text
	replyToMessageID    pgtype.UUID
	threadRootMessageID pgtype.UUID
	threadID            pgtype.Text
	triggerDepth        int
	seq                 int64
	createdAt           pgtype.Timestamptz
	editedAt            pgtype.Timestamptz
	deletedAt           pgtype.Timestamptz
}

func (s *TaskService) handleQuickCreateSourceReturn(ctx context.Context, task db.AgentInboxEvent, qc QuickCreateContext, issue db.Issue, identifier string) {
	if qc.Source == nil {
		return
	}
	workspaceID, err := util.ParseUUID(qc.WorkspaceID)
	if err != nil {
		return
	}
	target, reason, ok := s.resolveQuickCreateReturnTarget(ctx, workspaceID, qc)
	if !ok {
		s.recordQuickCreateReturn(ctx, task, workspaceID, issue.ID, task.AgentID, map[string]any{
			"status": "skipped",
			"reason": reason,
		})
		s.recordQuickCreateTaskActivity(ctx, task.ID, "Quick-create source return skipped: "+reason)
		return
	}

	agentName := s.quickCreateAgentDisplayName(ctx, task.AgentID)
	requesterMention := s.quickCreateRequesterMention(ctx, qc.RequesterID)
	content := buildQuickCreateReturnContent(requesterMention, identifier, util.UUIDToString(issue.ID), issue.Status, issue.Title)
	clientMessageID := "quick-create-return:" + util.UUIDToString(task.ID)
	msg, created, err := s.insertQuickCreateReturnMessage(ctx, task, workspaceID, task.AgentID, agentName, target, content, clientMessageID)
	if err != nil {
		slog.Warn("quick-create completion: source return insert failed",
			"task_id", util.UUIDToString(task.ID),
			"issue_id", util.UUIDToString(issue.ID),
			"error", err,
		)
		s.recordQuickCreateReturn(ctx, task, workspaceID, issue.ID, task.AgentID, map[string]any{
			"status": "skipped",
			"reason": "source_return_insert_failed",
		})
		s.recordQuickCreateTaskActivity(ctx, task.ID, "Quick-create source return skipped: source_return_insert_failed")
		return
	}

	s.recordQuickCreateReturn(ctx, task, workspaceID, issue.ID, task.AgentID, map[string]any{
		"status":                   "sent",
		"channel_id":               util.UUIDToString(target.channelID),
		"thread_root_message_id":   util.UUIDToString(target.threadRoot),
		"return_message_id":        util.UUIDToString(msg.id),
		"return_client_message_id": clientMessageID,
	})
	if !created {
		return
	}
	_, _ = s.exec(ctx, `UPDATE channel SET updated_at = now() WHERE id = $1`, target.channelID)
	if target.channelKind == "dm" {
		s.clearQuickCreateDMHidden(ctx, workspaceID, target.channelID)
	}
	s.publishQuickCreateChannelMessage(ctx, workspaceID, task.AgentID, target.channelID, quickCreateReturnMessagePayload(msg))
}

func (s *TaskService) recordQuickCreateReturn(ctx context.Context, task db.AgentInboxEvent, workspaceID, issueID, agentID pgtype.UUID, value map[string]any) {
	raw, _ := json.Marshal(value)
	s.setIssueMetadataAndPublish(ctx, workspaceID, issueID, agentID, quickCreateReturnMetadataKey, raw)
}

func (s *TaskService) setIssueMetadataAndPublish(ctx context.Context, workspaceID, issueID, agentID pgtype.UUID, key string, raw []byte) {
	updated, err := s.Queries.SetIssueMetadataKey(ctx, db.SetIssueMetadataKeyParams{
		ID:          issueID,
		WorkspaceID: workspaceID,
		Key:         key,
		Value:       raw,
	})
	if err != nil {
		slog.Warn("quick-create completion: set issue metadata failed",
			"issue_id", util.UUIDToString(issueID),
			"key", key,
			"error", err,
		)
		return
	}
	if s.Bus == nil {
		return
	}
	var metadata map[string]any
	if err := json.Unmarshal(updated.Metadata, &metadata); err != nil {
		metadata = map[string]any{}
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventIssueMetadataChanged,
		WorkspaceID: util.UUIDToString(workspaceID),
		ActorType:   "agent",
		ActorID:     util.UUIDToString(agentID),
		Payload: map[string]any{
			"issue_id": util.UUIDToString(issueID),
			"metadata": metadata,
		},
	})
}

func (s *TaskService) resolveQuickCreateReturnTarget(ctx context.Context, workspaceID pgtype.UUID, qc QuickCreateContext) (quickCreateReturnTarget, string, bool) {
	src := qc.Source
	channelID, err := util.ParseUUID(src.ChannelID)
	if err != nil {
		return quickCreateReturnTarget{}, "invalid_source_channel", false
	}
	rootID, err := util.ParseUUID(src.ThreadRootMessageID)
	if err != nil {
		return quickCreateReturnTarget{}, "invalid_source_thread", false
	}
	requesterID, err := util.ParseUUID(qc.RequesterID)
	if err != nil {
		return quickCreateReturnTarget{}, "invalid_requester", false
	}
	var kind string
	var archivedAt pgtype.Timestamptz
	if err := s.queryRow(ctx, `
		SELECT kind, archived_at
		FROM channel
		WHERE id = $1 AND workspace_id = $2`, channelID, workspaceID).Scan(&kind, &archivedAt); err != nil {
		return quickCreateReturnTarget{}, "source_channel_unavailable", false
	}
	if archivedAt.Valid {
		return quickCreateReturnTarget{}, "source_channel_archived", false
	}
	var requesterIsMember bool
	if err := s.queryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM channel_member
			WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'user' AND member_id = $3
		)`, channelID, workspaceID, requesterID).Scan(&requesterIsMember); err != nil || !requesterIsMember {
		return quickCreateReturnTarget{}, "source_requester_not_member", false
	}
	var threadID pgtype.Text
	var triggerDepth int
	if err := s.queryRow(ctx, `
		SELECT thread_id, trigger_depth
		FROM channel_message
		WHERE id = $1
		  AND channel_id = $2
		  AND workspace_id = $3
		  AND deleted_at IS NULL`, rootID, channelID, workspaceID).Scan(&threadID, &triggerDepth); err != nil {
		return quickCreateReturnTarget{}, "source_thread_unavailable", false
	}
	if src.SourceMessageID != "" && src.SourceMessageID != src.ThreadRootMessageID {
		sourceMessageID, err := util.ParseUUID(src.SourceMessageID)
		if err != nil {
			return quickCreateReturnTarget{}, "invalid_source_message", false
		}
		var exists bool
		if err := s.queryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM channel_message
				WHERE id = $1
				  AND channel_id = $2
				  AND workspace_id = $3
				  AND deleted_at IS NULL
				  AND thread_root_message_id = $4
			)`, sourceMessageID, channelID, workspaceID, rootID).Scan(&exists); err != nil || !exists {
			return quickCreateReturnTarget{}, "source_message_unavailable", false
		}
	}
	if !threadID.Valid || strings.TrimSpace(threadID.String) == "" {
		threadID = pgtype.Text{String: uuid.NewString(), Valid: true}
		if _, err := s.exec(ctx, `
			UPDATE channel_message
			SET thread_id = $4
			WHERE id = $1
			  AND channel_id = $2
			  AND workspace_id = $3
			  AND thread_id IS NULL`, rootID, channelID, workspaceID, threadID.String); err != nil {
			return quickCreateReturnTarget{}, "source_thread_update_failed", false
		}
	}
	return quickCreateReturnTarget{
		channelID:   channelID,
		channelKind: kind,
		threadRoot:  rootID,
		threadID:    &threadID.String,
		depth:       triggerDepth + 1,
	}, "", true
}

func (s *TaskService) insertQuickCreateReturnMessage(ctx context.Context, task db.AgentInboxEvent, workspaceID, agentID pgtype.UUID, agentName string, target quickCreateReturnTarget, content, clientMessageID string) (quickCreateReturnMessage, bool, error) {
	if s.TxStarter == nil {
		return quickCreateReturnMessage{}, false, errors.New("missing transaction starter")
	}
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return quickCreateReturnMessage{}, false, err
	}
	defer tx.Rollback(ctx)
	const insertSQL = `
		INSERT INTO channel_message (
			channel_id, workspace_id, author_type, author_id, author_name,
			content, parts, source, client_message_id, thread_root_message_id,
			thread_id, trigger_depth
		)
		VALUES ($1, $2, 'agent', $3, $4, $5, $6::jsonb, 'multica', $7, $8, $9, $10)
		ON CONFLICT DO NOTHING
		RETURNING id, channel_id, workspace_id, author_type, author_id, author_name, content, parts,
		          source, external_message_id, client_message_id, reply_to_message_id,
		          thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at`
	row := tx.QueryRow(ctx, insertSQL,
		target.channelID,
		workspaceID,
		agentID,
		agentName,
		content,
		messageparts.MustJSON(nil),
		clientMessageID,
		target.threadRoot,
		target.threadID,
		target.depth,
	)
	msg, scanErr := scanQuickCreateReturnMessage(row)
	if scanErr != nil && !errors.Is(scanErr, pgx.ErrNoRows) {
		return quickCreateReturnMessage{}, false, scanErr
	}
	if errors.Is(scanErr, pgx.ErrNoRows) {
		const selectSQL = `
			SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts,
			       source, external_message_id, client_message_id, reply_to_message_id,
			       thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
			FROM channel_message
			WHERE workspace_id = $1
			  AND channel_id = $2
			  AND author_type = 'agent'
			  AND author_id = $3
			  AND client_message_id = $4
			LIMIT 1`
		msg, err = scanQuickCreateReturnMessage(tx.QueryRow(ctx, selectSQL, workspaceID, target.channelID, agentID, clientMessageID))
		return msg, false, err
	}

	if _, err := s.RecordVisibleTaskActionTx(
		ctx, s.Queries.WithTx(tx), tx, task, DAGCloseMessage, msg.id, content,
		pgtype.UUID{}, target.channelID, pgtype.UUID{}, pgtype.UUID{}, "",
	); err != nil {
		return quickCreateReturnMessage{}, false, fmt.Errorf("record quick-create return boundary: %w", err)
	}

	var afterCommit func(context.Context)
	if s.PrepareCanonicalChannelMessageCommit != nil {
		afterCommit, err = s.PrepareCanonicalChannelMessageCommit(ctx, tx, CanonicalChannelMessage{
			ID:                  msg.id,
			WorkspaceID:         msg.workspaceID,
			ChannelID:           msg.channelID,
			ThreadRootMessageID: msg.threadRootMessageID,
			ThreadID:            msg.threadID,
			AuthorType:          msg.authorType,
			Seq:                 msg.seq,
		})
		if err != nil {
			return quickCreateReturnMessage{}, false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return quickCreateReturnMessage{}, false, err
	}
	if afterCommit != nil {
		afterCommit(context.WithoutCancel(ctx))
	}
	return msg, true, nil
}

func scanQuickCreateReturnMessage(row pgx.Row) (quickCreateReturnMessage, error) {
	var msg quickCreateReturnMessage
	err := row.Scan(
		&msg.id,
		&msg.channelID,
		&msg.workspaceID,
		&msg.authorType,
		&msg.authorID,
		&msg.authorName,
		&msg.content,
		&msg.parts,
		&msg.source,
		&msg.externalMessageID,
		&msg.clientMessageID,
		&msg.replyToMessageID,
		&msg.threadRootMessageID,
		&msg.threadID,
		&msg.triggerDepth,
		&msg.seq,
		&msg.createdAt,
		&msg.editedAt,
		&msg.deletedAt,
	)
	return msg, err
}

func quickCreateReturnMessagePayload(msg quickCreateReturnMessage) map[string]any {
	var parts []protocol.MessagePart
	if len(msg.parts) > 0 {
		_ = json.Unmarshal(msg.parts, &parts)
	}
	if parts == nil {
		parts = []protocol.MessagePart{}
	}
	return map[string]any{
		"id":                     util.UUIDToString(msg.id),
		"channel_id":             util.UUIDToString(msg.channelID),
		"workspace_id":           util.UUIDToString(msg.workspaceID),
		"seq":                    msg.seq,
		"type":                   msg.authorType,
		"author_id":              util.UUIDToPtr(msg.authorID),
		"author_name":            msg.authorName,
		"content":                msg.content,
		"parts":                  parts,
		"source":                 msg.source,
		"external_message_id":    util.TextToPtr(msg.externalMessageID),
		"client_message_id":      util.TextToPtr(msg.clientMessageID),
		"reply_to_message_id":    util.UUIDToPtr(msg.replyToMessageID),
		"thread_root_message_id": util.UUIDToPtr(msg.threadRootMessageID),
		"thread_id":              util.TextToPtr(msg.threadID),
		"trigger_depth":          msg.triggerDepth,
		"created_at":             util.TimestampToString(msg.createdAt),
		"edited_at":              util.TimestampToPtr(msg.editedAt),
		"deleted_at":             util.TimestampToPtr(msg.deletedAt),
	}
}

func buildQuickCreateReturnContent(requesterMention, identifier, issueID, status, title string) string {
	title = strings.TrimSpace(title)
	status = strings.TrimSpace(status)
	if status == "" {
		status = "todo"
	}
	issueLabel := identifier
	if issueLabel == "" {
		issueLabel = issueID
	}
	var b strings.Builder
	if requesterMention != "" {
		b.WriteString(requesterMention)
		b.WriteString(" ")
	}
	fmt.Fprintf(&b, "Created issue [%s](mention://issue/%s) from this thread.", issueLabel, issueID)
	if title != "" {
		fmt.Fprintf(&b, " %s", title)
	}
	fmt.Fprintf(&b, " Status: %s.", status)
	return b.String()
}

func (s *TaskService) quickCreateAgentDisplayName(ctx context.Context, agentID pgtype.UUID) string {
	if agentID.Valid {
		if agent, err := s.Queries.GetAgent(ctx, agentID); err == nil {
			if strings.TrimSpace(agent.DisplayName) != "" {
				return agent.DisplayName
			}
			if strings.TrimSpace(agent.Name) != "" {
				return agent.Name
			}
		}
	}
	return "Agent"
}

func (s *TaskService) quickCreateRequesterMention(ctx context.Context, requesterID string) string {
	requesterUUID, err := util.ParseUUID(requesterID)
	if err != nil {
		return ""
	}
	var name, displayName string
	if err := s.queryRow(ctx, `SELECT name, display_name FROM "user" WHERE id = $1`, requesterUUID).Scan(&name, &displayName); err != nil {
		return ""
	}
	label := strings.TrimSpace(displayName)
	if label == "" {
		label = strings.TrimSpace(name)
	}
	if label == "" {
		return ""
	}
	label = strings.ReplaceAll(label, "]", "\\]")
	return fmt.Sprintf("[@%s](mention://member/%s)", label, requesterID)
}

func (s *TaskService) recordQuickCreateTaskActivity(ctx context.Context, taskID pgtype.UUID, content string) {
	if !taskID.Valid || strings.TrimSpace(content) == "" {
		return
	}
	if err := s.runInTxWithTx(ctx, func(qtx *db.Queries, tx pgx.Tx) error {
		task := db.AgentInboxEvent{ID: taskID}
		if err := tx.QueryRow(ctx, `
			SELECT workspace_id, channel_id
			FROM agent_inbox_event
			WHERE id = $1`, taskID).Scan(&task.WorkspaceID, &task.ChannelID); err != nil {
			return fmt.Errorf("load quick-create task identity: %w", err)
		}
		_, _, err := s.RecordTaskMessageBoundaryTx(ctx, qtx, tx, TaskMessageBoundaryInput{
			Task: task,
			Message: db.CreateTaskMessageParams{
				Type:       "text",
				Content:    pgtype.Text{String: content, Valid: true},
				Visibility: "user_facing",
			},
			BoundaryKind:    DAGBoundaryVisible,
			CloseActionKind: DAGCloseMessage,
			ChannelID:       task.ChannelID,
		})
		return err
	}); err != nil {
		slog.Warn("quick-create completion: record canonical task activity failed", "task_id", util.UUIDToString(taskID), "error", err)
	}
}

func (s *TaskService) publishQuickCreateChannelMessage(ctx context.Context, workspaceID, agentID, channelID pgtype.UUID, payload any) {
	if s.Bus == nil {
		return
	}
	recipients := s.quickCreateChannelHumanMemberIDs(ctx, workspaceID, channelID)
	s.Bus.Publish(events.Event{
		Type:             protocol.EventChannelMessage,
		WorkspaceID:      util.UUIDToString(workspaceID),
		ActorType:        "agent",
		ActorID:          util.UUIDToString(agentID),
		RecipientUserIDs: recipients,
		Payload:          payload,
	})
}

func (s *TaskService) quickCreateChannelHumanMemberIDs(ctx context.Context, workspaceID, channelID pgtype.UUID) []string {
	rows, err := s.query(ctx, `
		SELECT member_id
		FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'user'`, channelID, workspaceID)
	if err != nil {
		return []string{}
	}
	defer rows.Close()
	seen := map[string]struct{}{}
	var out []string
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err == nil && id.Valid {
			sid := util.UUIDToString(id)
			if _, ok := seen[sid]; !ok {
				seen[sid] = struct{}{}
				out = append(out, sid)
			}
		}
	}
	return out
}

func (s *TaskService) clearQuickCreateDMHidden(ctx context.Context, workspaceID, channelID pgtype.UUID) {
	_, err := s.exec(ctx, `
		WITH user_peers AS (
			SELECT cm.member_id AS user_id, peer.member_type AS peer_type, peer.member_id AS peer_id
			FROM channel_member cm
			JOIN LATERAL (
				SELECT member_type, member_id
				FROM channel_member m2
				WHERE m2.channel_id = cm.channel_id
				  AND NOT (m2.member_type = 'user' AND m2.member_id = cm.member_id)
				ORDER BY m2.created_at ASC
				LIMIT 1
			) peer ON true
			WHERE cm.channel_id = $1 AND cm.workspace_id = $2 AND cm.member_type = 'user'
		),
		cleared_peer AS (
		  UPDATE dm_peer_state s
		  SET hidden_at = NULL, updated_at = now()
		  FROM user_peers p
		  WHERE s.workspace_id = $2
		    AND s.user_id = p.user_id
		    AND s.peer_type = p.peer_type
		    AND s.peer_id = p.peer_id
		    AND s.hidden_at IS NOT NULL
		  RETURNING s.user_id
		),
		conv AS (
		  SELECT id
		  FROM conversation
		  WHERE channel_id = $1
		)
		UPDATE conversation_member cm
		SET closed_at = NULL,
		    updated_at = now()
		FROM conv
		WHERE cm.conversation_id = conv.id
		  AND cm.workspace_id = $2
		  AND cm.member_type = 'user'
		  AND cm.closed_at IS NOT NULL`, channelID, workspaceID)
	if err != nil {
		slog.Warn("quick-create completion: clear DM hidden state failed", "workspace_id", util.UUIDToString(workspaceID), "channel_id", util.UUIDToString(channelID), "error", err)
	}
}

func (s *TaskService) dbExec() db.DBTX {
	if s.TxStarter != nil {
		// pgxpool.Pool is passed as TxStarter in production/tests and also
		// implements DBTX. Prefer it so this helper can use raw SQL without
		// opening a transaction just for one idempotent insert.
		if exec, ok := s.TxStarter.(db.DBTX); ok {
			return exec
		}
	}
	if s.Queries != nil {
		// No public accessor exists on generated Queries; nil means callers
		// that need raw SQL should skip instead of reaching into internals.
		return nil
	}
	return nil
}

func (s *TaskService) queryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if exec := s.dbExec(); exec != nil {
		return exec.QueryRow(ctx, sql, args...)
	}
	return missingRow{}
}

func (s *TaskService) query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if exec := s.dbExec(); exec != nil {
		return exec.Query(ctx, sql, args...)
	}
	return nil, errors.New("missing db executor")
}

func (s *TaskService) exec(ctx context.Context, sql string, args ...any) (any, error) {
	if exec := s.dbExec(); exec != nil {
		return exec.Exec(ctx, sql, args...)
	}
	return nil, errors.New("missing db executor")
}

type missingRow struct{}

func (missingRow) Scan(...any) error {
	return errors.New("missing db executor")
}
