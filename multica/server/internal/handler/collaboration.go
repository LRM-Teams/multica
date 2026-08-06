package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	channelCollaborationTurnReason     = "collaboration_turn"
	collaborationManagerFallbackReason = "collaboration_manager_fallback"
)

type CreateCollaborationSessionRequest struct {
	Mode                string         `json:"mode"`
	Goal                string         `json:"goal"`
	ExpectedStep        string         `json:"expected_step"`
	ParticipantAgentIDs []string       `json:"participant_agent_ids"`
	IssueID             string         `json:"issue_id,omitempty"`
	SourceMessageID     string         `json:"source_message_id,omitempty"`
	CompletionCondition map[string]any `json:"completion_condition,omitempty"`
	WorkGraph           map[string]any `json:"work_graph,omitempty"`
	TurnTimeoutSeconds  int            `json:"turn_timeout_seconds,omitempty"`
}

type CollaborationSessionResponse struct {
	ID        string `json:"id"`
	FirstTurn string `json:"first_turn"`
	Status    string `json:"status"`
}

func (h *Handler) CreateCollaborationSession(w http.ResponseWriter, r *http.Request) {
	workspaceID := ctxWorkspaceID(r.Context())
	userID := requestUserID(r)
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, r.Context(), workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if !h.requireGroupChannel(w, r.Context(), workspaceID, channelID) {
		return
	}
	var req CreateCollaborationSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.ParticipantAgentIDs) == 0 {
		writeError(w, http.StatusBadRequest, "participant_agent_ids is required")
		return
	}
	participants := make([]pgtype.UUID, 0, len(req.ParticipantAgentIDs))
	for _, raw := range req.ParticipantAgentIDs {
		agentID, ok := parseUUIDOrBadRequest(w, raw, "participant_agent_id")
		if !ok {
			return
		}
		if !h.requireChannelAgentMember(w, r.Context(), workspaceID, channelID, agentID) {
			return
		}
		participants = append(participants, agentID)
	}
	var issueID pgtype.UUID
	if strings.TrimSpace(req.IssueID) != "" {
		parsed, ok := parseUUIDOrBadRequest(w, req.IssueID, "issue_id")
		if !ok {
			return
		}
		if _, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{ID: parsed, WorkspaceID: parseUUID(workspaceID)}); err != nil {
			writeError(w, http.StatusBadRequest, "issue_id is not in this workspace")
			return
		}
		issueID = parsed
	}
	var sourceMessageID pgtype.UUID
	if strings.TrimSpace(req.SourceMessageID) != "" {
		parsed, ok := parseUUIDOrBadRequest(w, req.SourceMessageID, "source_message_id")
		if !ok {
			return
		}
		sourceMessageID = parsed
	}
	var timeout time.Duration
	if req.TurnTimeoutSeconds > 0 {
		timeout = time.Duration(req.TurnTimeoutSeconds) * time.Second
	}
	result, err := h.createCollaborationSession(r.Context(), collaborationSessionCreateParams{
		WorkspaceID:         parseUUID(workspaceID),
		ChannelID:           channelID,
		IssueID:             issueID,
		SourceMessageID:     sourceMessageID,
		ParticipantAgentIDs: participants,
		Mode:                req.Mode,
		Goal:                req.Goal,
		ExpectedStep:        req.ExpectedStep,
		CompletionCondition: req.CompletionCondition,
		WorkGraph:           req.WorkGraph,
		TurnTimeout:         timeout,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.recordChannelAgentPromptWake(r.Context(), result.Wake.channel, result.Wake.agent, result.Wake.trigger, result.Wake.reason, result.Wake.result)
	writeJSON(w, http.StatusCreated, CollaborationSessionResponse{ID: uuidToString(result.SessionID), FirstTurn: uuidToString(result.FirstTurn), Status: "active"})
}

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
	Wake      channelAgentWake
}

func (h *Handler) createCollaborationSession(ctx context.Context, params collaborationSessionCreateParams) (collaborationSessionCreateResult, error) {
	if h == nil || h.TxStarter == nil {
		return collaborationSessionCreateResult{}, errors.New("collaboration transaction starter unavailable")
	}
	if len(params.ParticipantAgentIDs) == 0 {
		return collaborationSessionCreateResult{}, errors.New("collaboration session requires at least one participant")
	}
	seenParticipants := map[string]struct{}{}
	for _, agentID := range params.ParticipantAgentIDs {
		if !agentID.Valid {
			return collaborationSessionCreateResult{}, errors.New("collaboration participant_agent_ids must be valid UUIDs")
		}
		key := uuidToString(agentID)
		if _, exists := seenParticipants[key]; exists {
			return collaborationSessionCreateResult{}, errors.New("collaboration participant_agent_ids must be unique")
		}
		seenParticipants[key] = struct{}{}
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
	if err := validateCollaborationSessionParticipantsTx(ctx, tx, params.WorkspaceID, params.ChannelID, params.ParticipantAgentIDs); err != nil {
		return collaborationSessionCreateResult{}, err
	}
	if params.IssueID.Valid {
		var issueExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM issue WHERE id = $1 AND workspace_id = $2)`, params.IssueID, params.WorkspaceID).Scan(&issueExists); err != nil {
			return collaborationSessionCreateResult{}, err
		}
		if !issueExists {
			return collaborationSessionCreateResult{}, errors.New("collaboration issue_id is not in this workspace")
		}
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

func validateCollaborationSessionParticipantsTx(ctx context.Context, tx pgx.Tx, workspaceID, channelID pgtype.UUID, agentIDs []pgtype.UUID) error {
	for _, agentID := range agentIDs {
		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1
			  FROM channel_member member
			  JOIN agent ON agent.id = member.member_id
			  WHERE member.workspace_id = $1
			    AND member.channel_id = $2
			    AND member.member_type = 'agent'
			    AND member.member_id = $3
			    AND agent.workspace_id = $1
			    AND agent.archived_at IS NULL
			)`, workspaceID, channelID, agentID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("collaboration participant %s is not an active agent member of this channel", uuidToString(agentID))
		}
	}
	return nil
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

func (h *Handler) createCollaborationTurnGrantTx(ctx context.Context, tx pgx.Tx, in collaborationTurnGrantInput) (pgtype.UUID, channelAgentWake, error) {
	agent, err := h.Queries.WithTx(tx).GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: in.agentID, WorkspaceID: in.workspaceID})
	if err != nil {
		return pgtype.UUID{}, channelAgentWake{}, err
	}
	prompt := buildCollaborationTurnPrompt(in)
	result, err := h.enqueueChannelAgentPromptRangeWithTx(ctx, h.Queries.WithTx(tx), tx, in.channel, agent, in.trigger, pgtype.UUID{}, prompt, channelCollaborationTurnReason, 10, in.trigger.Seq, in.trigger.Seq)
	if err != nil {
		return pgtype.UUID{}, channelAgentWake{}, err
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
		return pgtype.UUID{}, channelAgentWake{}, err
	}
	if err := recordChannelDecisionAuditExec(ctx, tx, channelDecisionAuditEvent{
		WorkspaceID: in.workspaceID, ChannelID: parseUUID(in.channel.ID), SourceKind: "collaboration_turn", SourceID: turnID,
		EventType: "turn_grant_created", AgentID: in.agentID, InboxEventID: result.Event.ID,
		Payload: map[string]any{
			"session_id": uuidToString(in.sessionID), "turn_index": in.turnIndex, "participant_index": in.participantIndex,
			"session_version": in.sessionVersion, "grant_seq": in.trigger.Seq,
		},
	}); err != nil {
		return pgtype.UUID{}, channelAgentWake{}, err
	}
	wake := channelAgentWake{channel: in.channel, agent: agent, trigger: in.trigger, reason: channelCollaborationTurnReason, result: result}
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

func (h *Handler) completeCollaborationTurnTx(ctx context.Context, tx pgx.Tx, event db.AgentInboxEvent) ([]channelAgentWake, error) {
	var sessionID, turnID, workspaceID, channelID pgtype.UUID
	var agentID, sourceMessageID pgtype.UUID
	var mode, sessionStatus, turnStatus string
	var participantIDs []pgtype.UUID
	var turnIndex, participantIndex, currentTurnIndex, version, turnSessionVersion int32
	var goal, expectedStep string
	var completionRaw []byte
	var deadline pgtype.Timestamptz
	err := tx.QueryRow(ctx, `
		SELECT turn.session_id, turn.id, session.workspace_id, session.channel_id,
		       turn.agent_id, session.source_message_id, session.mode, session.status,
		       session.participant_agent_ids, turn.turn_index, turn.participant_index,
		       session.current_turn_index, session.version, session.goal,
		       session.expected_step, session.completion_condition, turn.grant_status,
		       turn.session_version, turn.deadline_at
		FROM collaboration_turn turn
		JOIN collaboration_session session ON session.id = turn.session_id
		WHERE turn.inbox_event_id = $1
		FOR UPDATE OF turn, session`, event.ID).Scan(&sessionID, &turnID, &workspaceID, &channelID, &agentID, &sourceMessageID, &mode, &sessionStatus, &participantIDs, &turnIndex, &participantIndex, &currentTurnIndex, &version, &goal, &expectedStep, &completionRaw, &turnStatus, &turnSessionVersion, &deadline)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if mode != "sequential" || sessionStatus != "active" {
		return nil, nil
	}
	if agentID != event.AgentID {
		return nil, errors.New("collaboration turn agent does not match inbox event")
	}
	if turnStatus != "granted" && turnStatus != "consumed" {
		return nil, fmt.Errorf("collaboration turn is %s", turnStatus)
	}
	if currentTurnIndex != turnIndex {
		return nil, errors.New("collaboration turn is stale for session current_turn_index")
	}
	if turnSessionVersion != version {
		return nil, errors.New("collaboration turn session_version is stale")
	}
	if deadline.Valid && !deadline.Time.After(time.Now()) {
		return nil, errors.New("collaboration turn grant expired")
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
	if turnStatus == "granted" {
		result, err := tx.Exec(ctx, `
			UPDATE collaboration_turn
			SET grant_status = 'consumed', result_message_id = $2, updated_at = now()
			WHERE id = $1 AND grant_status = 'granted' AND session_version = $3`, turnID, nullableUUID(resultMessageID), version)
		if err != nil {
			return nil, err
		}
		if result.RowsAffected() != 1 {
			return nil, errors.New("collaboration turn grant was already consumed or changed")
		}
		if err := recordChannelDecisionAuditExec(ctx, tx, channelDecisionAuditEvent{
			WorkspaceID: workspaceID, ChannelID: channelID, SourceKind: "collaboration_turn", SourceID: turnID,
			EventType: "turn_grant_consumed", AgentID: agentID, MessageID: resultMessageID, InboxEventID: event.ID,
			Payload: map[string]any{"session_id": uuidToString(sessionID), "turn_index": turnIndex, "session_version": version, "consumed_by": "inbox_completion"},
		}); err != nil {
			return nil, err
		}
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
	advanceResult, err := tx.Exec(ctx, `
		UPDATE collaboration_session
		SET current_turn_index = $2, version = version + 1, updated_at = now()
		WHERE id = $1 AND status = 'active' AND version = $3`, sessionID, nextTurnIndex, version)
	if err != nil {
		return nil, err
	}
	if advanceResult.RowsAffected() != 1 {
		return nil, errors.New("collaboration session changed while advancing turn")
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
	return []channelAgentWake{wake}, nil
}

func (h *Handler) failCollaborationTurnTx(ctx context.Context, tx pgx.Tx, event db.AgentInboxEvent, reason string) ([]channelAgentWake, error) {
	var sessionID, turnID, workspaceID, channelID, sourceMessageID pgtype.UUID
	var sessionStatus, turnStatus string
	err := tx.QueryRow(ctx, `
		SELECT turn.session_id, turn.id, session.workspace_id, session.channel_id,
		       session.source_message_id, session.status, turn.grant_status
		FROM collaboration_turn turn
		JOIN collaboration_session session ON session.id = turn.session_id
		WHERE turn.inbox_event_id = $1
		FOR UPDATE OF turn, session`, event.ID).Scan(&sessionID, &turnID, &workspaceID, &channelID, &sourceMessageID, &sessionStatus, &turnStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if sessionStatus != "active" || turnStatus != "granted" {
		return nil, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE collaboration_turn SET grant_status = 'expired', updated_at = now() WHERE id = $1 AND grant_status = 'granted'`, turnID); err != nil {
		return nil, err
	}
	if err := recordChannelDecisionAuditExec(ctx, tx, channelDecisionAuditEvent{
		WorkspaceID: workspaceID, ChannelID: channelID, SourceKind: "collaboration_turn", SourceID: turnID,
		EventType: "turn_grant_expired", AgentID: event.AgentID, InboxEventID: event.ID,
		Payload: map[string]any{"session_id": uuidToString(sessionID), "reason": reason},
	}); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE collaboration_session SET status = 'suspended', version = version + 1, updated_at = now() WHERE id = $1 AND status = 'active'`, sessionID); err != nil {
		return nil, err
	}
	return h.createCollaborationManagerFallbackTx(ctx, tx, workspaceID, channelID, sourceMessageID, sessionID, reason)
}

func (h *Handler) createCollaborationManagerFallbackTx(ctx context.Context, tx pgx.Tx, workspaceID, channelID, sourceMessageID, sessionID pgtype.UUID, reason string) ([]channelAgentWake, error) {
	// The retired singleton manager cannot be used as a fallback target. Current
	// managers discover the suspended open loop through their own patrol brief.
	return nil, nil
}

func (h *Handler) SweepCollaborationTurnTimeouts(ctx context.Context, limit int) int {
	if h == nil || h.TxStarter == nil {
		return 0
	}
	if limit <= 0 || limit > 64 {
		limit = 64
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return 0
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('collaboration_turn_timeout'))`); err != nil {
		return 0
	}
	rows, err := tx.Query(ctx, `
		SELECT turn.id, turn.session_id, session.workspace_id, session.channel_id, session.source_message_id
		FROM collaboration_turn turn
		JOIN collaboration_session session ON session.id = turn.session_id
		WHERE turn.grant_status = 'granted'
		  AND session.status = 'active'
		  AND turn.deadline_at IS NOT NULL
		  AND turn.deadline_at <= now()
		ORDER BY turn.deadline_at ASC, turn.id ASC
		LIMIT $1
		FOR UPDATE OF turn, session SKIP LOCKED`, limit)
	if err != nil {
		return 0
	}
	type expiredTurn struct {
		turnID, sessionID, workspaceID, channelID, sourceMessageID pgtype.UUID
	}
	var expired []expiredTurn
	for rows.Next() {
		var item expiredTurn
		if err := rows.Scan(&item.turnID, &item.sessionID, &item.workspaceID, &item.channelID, &item.sourceMessageID); err == nil {
			expired = append(expired, item)
		}
	}
	rows.Close()
	if len(expired) == 0 {
		_ = tx.Commit(ctx)
		return 0
	}
	var wakes []channelAgentWake
	for _, item := range expired {
		if _, err := tx.Exec(ctx, `UPDATE collaboration_turn SET grant_status = 'expired', updated_at = now() WHERE id = $1 AND grant_status = 'granted'`, item.turnID); err != nil {
			return 0
		}
		if err := recordChannelDecisionAuditExec(ctx, tx, channelDecisionAuditEvent{
			WorkspaceID: item.workspaceID, ChannelID: item.channelID, SourceKind: "collaboration_turn", SourceID: item.turnID,
			EventType: "turn_grant_expired", Payload: map[string]any{"session_id": uuidToString(item.sessionID), "reason": "turn grant expired"},
		}); err != nil {
			return 0
		}
		if _, err := tx.Exec(ctx, `UPDATE collaboration_session SET status = 'suspended', version = version + 1, updated_at = now() WHERE id = $1 AND status = 'active'`, item.sessionID); err != nil {
			return 0
		}
		fallbackWakes, err := h.createCollaborationManagerFallbackTx(ctx, tx, item.workspaceID, item.channelID, item.sourceMessageID, item.sessionID, "turn grant expired")
		if err != nil {
			return 0
		}
		wakes = append(wakes, fallbackWakes...)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0
	}
	for _, wake := range wakes {
		h.recordChannelAgentPromptWake(ctx, wake.channel, wake.agent, wake.trigger, wake.reason, wake.result)
	}
	return len(expired)
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
