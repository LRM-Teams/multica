package handler

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Graph memory turn capture (issue #3943). The #2295 hard-cut removed the
// task-shaped mention wakes, and with them the only interaction DAG record
// points for ordinary channel conversation turns: a directed user message and
// its managed-agent reply minted no task, no segment, and therefore no atoms.
// Canonical Message delivery became the sole chat receive path, so capture now
// hangs off that path and its reply counterpart.
//
// Each captured message gets one self-contained anchor task with
// reason='graph_capture' that is BORN TERMINAL: status='acked',
// terminal_outcome='completed', requires_wake=false. A born-terminal row is
// invisible to the inbox drain, the daemon ack path, and the 2h queue sweeper,
// so the anchor never schedules or consumes work — it exists purely so the
// message text can be replayed through RecordTaskMessageBoundaryTx into a
// graph/eligible segment. Replays are collapsed by the partial unique index
// uq_agent_inbox_event_graph_capture_message (one anchor per message+agent).

// captureDirectedGraphTurnTx records a human's directed channel message (an
// @mention of the active managed memory agent) as a turn anchor. Delivery
// persistence calls this inside the message's own transaction, next to the
// steering-event hook: the directed flag already encodes the
// mention-of-active-managed-agent gate computed per recipient.
func (h *Handler) captureDirectedGraphTurnTx(ctx context.Context, tx pgx.Tx, ch ChannelResponse, message ChannelMessageResponse, recipientAgentID pgtype.UUID, directed bool) error {
	if h == nil || tx == nil || !directed {
		return nil
	}
	if !strings.EqualFold(ch.Kind, "group") {
		return nil
	}
	// Human turns only: managed-agent self-messages are captured on the reply
	// hook, and other agent traffic already owns its own DAG paths.
	if !channelMessageIsHumanAuthored(message.Type) {
		return nil
	}
	return h.mintGraphCaptureAnchorTx(ctx, tx,
		parseUUID(ch.WorkspaceID), parseUUID(ch.ID), recipientAgentID,
		parseUUID(message.ID), message.Seq, message.Content,
	)
}

// captureGraphAgentReplyTx records a managed memory agent's channel reply as a
// turn anchor. The transport insert calls this from the visible-message
// afterInsert hook only when no draining task matched — a task-shaped wake
// still records its own messages, and double capture would fork the DAG.
func (h *Handler) captureGraphAgentReplyTx(ctx context.Context, tx pgx.Tx, workspaceID, agentID pgtype.UUID, ch ChannelResponse, message ChannelMessageResponse) error {
	if h == nil || tx == nil || !workspaceID.Valid || !agentID.Valid {
		return nil
	}
	if !strings.EqualFold(ch.Kind, "group") {
		return nil
	}
	if !managedGraphMemoryAgent(ctx, tx, parseUUID(ch.ID), agentID) {
		return nil
	}
	return h.mintGraphCaptureAnchorTx(ctx, tx,
		workspaceID, parseUUID(ch.ID), agentID,
		parseUUID(message.ID), message.Seq, message.Content,
	)
}

// mintGraphCaptureAnchorTx inserts the born-terminal anchor and replays the
// message through the universal DAG inside the caller's transaction. The
// partial unique index makes concurrent or replayed mints a no-op (no row
// returned); the profile gate keeps legacy workspaces at zero cost.
func (h *Handler) mintGraphCaptureAnchorTx(ctx context.Context, tx pgx.Tx, workspaceID, channelID, agentID, messageID pgtype.UUID, seq int64, content string) error {
	if h == nil || h.TaskService == nil || h.TaskService.UniversalDAG == nil || h.Queries == nil || tx == nil {
		return nil
	}
	if !workspaceID.Valid || !channelID.Valid || !agentID.Valid || !messageID.Valid || seq <= 0 || strings.TrimSpace(content) == "" {
		return nil
	}
	// Pool-side profile read (never the open transaction's connection): the
	// capture only exists for workspaces whose memory pipeline is graph.
	if h.graphMemoryProfileForWorkspace(ctx, workspaceID).memoryType != "graph" {
		return nil
	}
	var anchorID, anchorWorkspaceID, anchorChannelID pgtype.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
		  workspace_id, channel_id, agent_id, source_message_id, reason,
		  delivery_mode, response_mode, requires_wake, status, terminal_outcome,
		  terminal_at, acked_at, priority, seq_from, seq_to
		)
		VALUES ($1, $2, $3, $4, 'graph_capture', 'observe', 'no_public_output', false,
		        'acked', 'completed', now(), now(), 0, $5, $5)
		ON CONFLICT (source_message_id, agent_id)
		  WHERE reason = 'graph_capture' AND source_message_id IS NOT NULL
		DO NOTHING
		RETURNING id, workspace_id, channel_id`,
		workspaceID, channelID, agentID, messageID, seq,
	).Scan(&anchorID, &anchorWorkspaceID, &anchorChannelID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // replay: the anchor and its DAG boundaries already exist
	}
	if err != nil {
		return fmt.Errorf("mint graph capture anchor: %w", err)
	}
	task := db.AgentInboxEvent{ID: anchorID, WorkspaceID: anchorWorkspaceID, ChannelID: anchorChannelID}
	if _, _, err := h.TaskService.RecordTaskMessageBoundaryTx(ctx, h.Queries.WithTx(tx), tx, service.TaskMessageBoundaryInput{
		Task: task,
		Message: db.CreateTaskMessageParams{
			Type:       "text",
			Content:    pgtype.Text{String: content, Valid: true},
			Visibility: "user_facing",
		},
		BoundaryKind:    service.DAGBoundaryVisible,
		CloseActionKind: service.DAGCloseMessage,
		ChannelID:       anchorChannelID,
	}); err != nil {
		return fmt.Errorf("graph capture turn boundary: %w", err)
	}
	// Closes the anchor's lifecycle: the terminal boundary resolves the
	// generation-1 segment the visible boundary just closed, and resolves the
	// memory type through the pool-backed queries (SQLSTATE 25P02 rule).
	if err := h.TaskService.RecordTerminalTaskBoundaryTx(ctx, h.Queries.WithTx(tx), tx, task); err != nil {
		return fmt.Errorf("graph capture terminal boundary: %w", err)
	}
	return nil
}
