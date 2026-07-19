package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const channelCollaborationTurnReason = "collaboration_turn"

type collaborationSessionCreateParams struct {
	WorkspaceID         pgtype.UUID
	ChannelID           pgtype.UUID
	IssueID             pgtype.UUID
	SourceMessageID     pgtype.UUID
	ParticipantAgentIDs []pgtype.UUID
	Mode                string
	Goal                string
	ExpectedStep        string
	CompletionCondition map[string]any
	WorkGraph           map[string]any
	CreatedByRunID      pgtype.UUID
	TurnTimeout         time.Duration
}

type collaborationSessionCreateResult struct {
	SessionID pgtype.UUID
	FirstTurn pgtype.UUID
	Wake      channelAttentionWake
}

func (h *Handler) createCollaborationSession(ctx context.Context, params collaborationSessionCreateParams) (collaborationSessionCreateResult, error) {
	if h == nil || h.TxStarter == nil {
		return collaborationSessionCreateResult{}, errors.New("collaboration transaction starter unavailable")
	}
	if len(params.ParticipantAgentIDs) == 0 {
		return collaborationSessionCreateResult{}, errors.New("collaboration session requires at least one participant")
	}
	mode := strings.TrimSpace(params.Mode)
	if mode == "" {
		mode = "sequential"
	}
	if mode != "sequential" && mode != "dependency" && mode != "parallel" && mode != "proposal" && mode != "race" {
		return collaborationSessionCreateResult{}, fmt.Errorf("unsupported collaboration mode %q", mode)
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return collaborationSessionCreateResult{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('collaboration_session'), hashtext($1::text))`, params.ChannelID); err != nil {
		return collaborationSessionCreateResult{}, err
	}
	ch, ok := h.getChannel(ctx, uuidToString(params.WorkspaceID), params.ChannelID)
	if !ok {
		return collaborationSessionCreateResult{}, errors.New("collaboration channel not found")
	}
	trigger, err := h.collaborationTriggerMessageTx(ctx, tx, params)
	if err != nil {
		return collaborationSessionCreateResult{}, err
	}
	condition, err := json.Marshal(defaultCollaborationObject(params.CompletionCondition))
	if err != nil {
		return collaborationSessionCreateResult{}, err
	}
	graph, err := json.Marshal(defaultCollaborationObject(params.WorkGraph))
	if err != nil {
		return collaborationSessionCreateResult{}, err
	}
	var sessionID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO collaboration_session (
		  workspace_id, channel_id, issue_id, source_message_id, mode, status,
		  goal, participant_agent_ids, current_turn_index, expected_step,
		  completion_condition, work_graph, created_by_run_id
		)
		VALUES ($1, $2, $3, $4, $5, 'active', $6, $7, 0, $8, $9::jsonb, $10::jsonb, $11)
		RETURNING id`, params.WorkspaceID, params.ChannelID, nullableUUID(params.IssueID), nullableUUID(params.SourceMessageID), mode,
		params.Goal, params.ParticipantAgentIDs, params.ExpectedStep, condition, graph, nullableUUID(params.CreatedByRunID)).Scan(&sessionID); err != nil {
		return collaborationSessionCreateResult{}, err
	}
	turnID, wake, err := h.createCollaborationTurnGrantTx(ctx, tx, collaborationTurnGrantInput{
		sessionID:        sessionID,
		workspaceID:      params.WorkspaceID,
		channel:          ch,
		trigger:          trigger,
		agentID:          params.ParticipantAgentIDs[0],
		turnIndex:        0,
		participantIndex: 0,
		sessionVersion:   1,
		goal:             params.Goal,
		expectedStep:     params.ExpectedStep,
		turnTimeout:      params.TurnTimeout,
	})
	if err != nil {
		return collaborationSessionCreateResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return collaborationSessionCreateResult{}, err
	}
	return collaborationSessionCreateResult{SessionID: sessionID, FirstTurn: turnID, Wake: wake}, nil
}

func defaultCollaborationObject(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}

func (h *Handler) collaborationTriggerMessageTx(ctx context.Context, tx pgx.Tx, params collaborationSessionCreateParams) (ChannelMessageResponse, error) {
	if !params.SourceMessageID.Valid {
		return ChannelMessageResponse{ChannelID: uuidToString(params.ChannelID), WorkspaceID: uuidToString(params.WorkspaceID), TriggerDepth: 0}, nil
	}
	row := tx.QueryRow(ctx, `
		SELECT id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source, external_message_id, client_message_id, reply_to_message_id, quote_message_id, quote_snapshot, thread_root_message_id, thread_id, trigger_depth, seq, created_at, edited_at, deleted_at
		FROM channel_message
		WHERE workspace_id = $1 AND channel_id = $2 AND id = $3`, params.WorkspaceID, params.ChannelID, params.SourceMessageID)
	return scanChannelMessage(row)
}

type collaborationTurnGrantInput struct {
	sessionID        pgtype.UUID
	workspaceID      pgtype.UUID
	channel          ChannelResponse
	trigger          ChannelMessageResponse
	agentID          pgtype.UUID
	turnIndex        int32
	participantIndex int32
	sessionVersion   int32
	goal             string
	expectedStep     string
	turnTimeout      time.Duration
}

func (h *Handler) createCollaborationTurnGrantTx(ctx context.Context, tx pgx.Tx, in collaborationTurnGrantInput) (pgtype.UUID, channelAttentionWake, error) {
	agent, err := h.Queries.WithTx(tx).GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: in.agentID, WorkspaceID: in.workspaceID})
	if err != nil {
		return pgtype.UUID{}, channelAttentionWake{}, err
	}
	prompt := buildCollaborationTurnPrompt(in)
	result, err := h.enqueueChannelAgentPromptRangeWithTx(ctx, h.Queries.WithTx(tx), tx, in.channel, agent, in.trigger, pgtype.UUID{}, prompt, channelCollaborationTurnReason, 10, in.trigger.Seq, in.trigger.Seq)
	if err != nil {
		return pgtype.UUID{}, channelAttentionWake{}, err
	}
	var deadline any
	if in.turnTimeout > 0 {
		deadline = time.Now().Add(in.turnTimeout)
	}
	var turnID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO collaboration_turn (
		  workspace_id, session_id, agent_id, inbox_event_id, turn_index,
		  participant_index, grant_status, grant_seq, session_version, deadline_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'granted', $7, $8, $9)
		RETURNING id`, in.workspaceID, in.sessionID, in.agentID, result.Event.ID, in.turnIndex,
		in.participantIndex, nullableInt64(in.trigger.Seq), in.sessionVersion, deadline).Scan(&turnID); err != nil {
		return pgtype.UUID{}, channelAttentionWake{}, err
	}
	wake := channelAttentionWake{channel: in.channel, agent: agent, trigger: in.trigger, reason: channelCollaborationTurnReason, result: result}
	return turnID, wake, nil
}

func buildCollaborationTurnPrompt(in collaborationTurnGrantInput) string {
	var b strings.Builder
	b.WriteString("You hold the turn_grant for a Multica collaboration session. You are the only participant authorized to produce the visible output for this turn.\n")
	fmt.Fprintf(&b, "collaboration_session_id: %s\n", uuidToString(in.sessionID))
	fmt.Fprintf(&b, "turn_index: %d\n", in.turnIndex)
	fmt.Fprintf(&b, "participant_index: %d\n", in.participantIndex)
	if strings.TrimSpace(in.goal) != "" {
		b.WriteString("Goal: " + strings.TrimSpace(in.goal) + "\n")
	}
	if strings.TrimSpace(in.expectedStep) != "" {
		b.WriteString("Expected step: " + strings.TrimSpace(in.expectedStep) + "\n")
	}
	b.WriteString("Complete only your current turn. Do not ask other agents to act; the server will advance the next turn after your completion.\n")
	return b.String()
}

func (h *Handler) completeCollaborationTurnTx(ctx context.Context, tx pgx.Tx, event db.AgentInboxEvent) ([]channelAttentionWake, error) {
	var sessionID, turnID, workspaceID, channelID pgtype.UUID
	var agentID, sourceMessageID pgtype.UUID
	var mode, sessionStatus string
	var participantIDs []pgtype.UUID
	var turnIndex, participantIndex, currentTurnIndex, version int32
	var goal, expectedStep string
	var completionRaw []byte
	err := tx.QueryRow(ctx, `
		SELECT turn.session_id, turn.id, session.workspace_id, session.channel_id,
		       turn.agent_id, session.source_message_id, session.mode, session.status,
		       session.participant_agent_ids, turn.turn_index, turn.participant_index,
		       session.current_turn_index, session.version, session.goal,
		       session.expected_step, session.completion_condition
		FROM collaboration_turn turn
		JOIN collaboration_session session ON session.id = turn.session_id
		WHERE turn.inbox_event_id = $1
		FOR UPDATE OF turn, session`, event.ID).Scan(&sessionID, &turnID, &workspaceID, &channelID, &agentID, &sourceMessageID, &mode, &sessionStatus, &participantIDs, &turnIndex, &participantIndex, &currentTurnIndex, &version, &goal, &expectedStep, &completionRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if mode != "sequential" || sessionStatus != "active" {
		return nil, nil
	}
	var resultMessageID pgtype.UUID
	_ = tx.QueryRow(ctx, `
		SELECT channel_message_id
		FROM agent_task_transport_audit
		WHERE inbox_event_id = $1
		  AND action = 'message_send'
		  AND channel_message_id IS NOT NULL
		ORDER BY created_at ASC
		LIMIT 1`, event.ID).Scan(&resultMessageID)
	if _, err := tx.Exec(ctx, `
		UPDATE collaboration_turn
		SET grant_status = 'consumed', result_message_id = $2, updated_at = now()
		WHERE id = $1 AND grant_status = 'granted'`, turnID, nullableUUID(resultMessageID)); err != nil {
		return nil, err
	}
	if collaborationSessionComplete(turnIndex, completionRaw) {
		_, err := tx.Exec(ctx, `
			UPDATE collaboration_session
			SET status = 'completed', version = version + 1, updated_at = now()
			WHERE id = $1 AND status = 'active'`, sessionID)
		return nil, err
	}
	if len(participantIDs) == 0 {
		return nil, errors.New("collaboration session has no participants")
	}
	nextParticipantIndex := (participantIndex + 1) % int32(len(participantIDs))
	nextTurnIndex := turnIndex + 1
	if _, err := tx.Exec(ctx, `
		UPDATE collaboration_session
		SET current_turn_index = $2, version = version + 1, updated_at = now()
		WHERE id = $1 AND status = 'active'`, sessionID, nextTurnIndex); err != nil {
		return nil, err
	}
	ch, ok := h.getChannel(ctx, uuidToString(workspaceID), channelID)
	if !ok {
		return nil, errors.New("collaboration channel not found")
	}
	trigger, err := h.collaborationTriggerMessageTx(ctx, tx, collaborationSessionCreateParams{WorkspaceID: workspaceID, ChannelID: channelID, SourceMessageID: sourceMessageID})
	if err != nil {
		return nil, err
	}
	_, wake, err := h.createCollaborationTurnGrantTx(ctx, tx, collaborationTurnGrantInput{
		sessionID:        sessionID,
		workspaceID:      workspaceID,
		channel:          ch,
		trigger:          trigger,
		agentID:          participantIDs[nextParticipantIndex],
		turnIndex:        nextTurnIndex,
		participantIndex: nextParticipantIndex,
		sessionVersion:   version + 1,
		goal:             goal,
		expectedStep:     expectedStep,
	})
	if err != nil {
		return nil, err
	}
	return []channelAttentionWake{wake}, nil
}

func collaborationSessionComplete(turnIndex int32, raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	var condition struct {
		MaxTurns int32 `json:"max_turns"`
	}
	if err := json.Unmarshal(raw, &condition); err != nil || condition.MaxTurns <= 0 {
		return false
	}
	return turnIndex+1 >= condition.MaxTurns
}

func nullableInt64(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}
