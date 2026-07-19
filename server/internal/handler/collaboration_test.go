package handler

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestSequentialCollaborationSessionCreatesSingleTurnGrant(t *testing.T) {
	fixture := newChannelAttentionFixture(t, []attentionRuntimeSpec{{}, {}, {}})
	trigger := fixture.insertMessage(t, "user", testUserID, "Agents, count to two in order", nil)
	result, err := fixture.handler.createCollaborationSession(context.Background(), collaborationSessionCreateParams{
		WorkspaceID:     parseUUID(testWorkspaceID),
		ChannelID:       parseUUID(fixture.channel.ID),
		SourceMessageID: parseUUID(trigger.ID),
		ParticipantAgentIDs: []pgtype.UUID{
			parseUUID(fixture.agentIDs[0]),
			parseUUID(fixture.agentIDs[1]),
			parseUUID(fixture.agentIDs[2]),
		},
		Mode:                "sequential",
		Goal:                "Count to two",
		ExpectedStep:        "Reply with exactly one number for your turn.",
		CompletionCondition: map[string]any{"max_turns": 2},
	})
	if err != nil {
		t.Fatalf("create collaboration session: %v", err)
	}
	if !result.SessionID.Valid || !result.FirstTurn.Valid || !result.Wake.result.Event.ID.Valid {
		t.Fatalf("invalid create result: %+v", result)
	}

	var granted, consumed int
	var firstAgent string
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FILTER (WHERE turn.grant_status = 'granted')::int,
		       count(*) FILTER (WHERE turn.grant_status = 'consumed')::int,
		       min(turn.agent_id::text)
		FROM collaboration_turn turn
		WHERE turn.session_id = $1`, result.SessionID).Scan(&granted, &consumed, &firstAgent); err != nil {
		t.Fatalf("inspect collaboration turns: %v", err)
	}
	if granted != 1 || consumed != 0 || firstAgent != fixture.agentIDs[0] {
		t.Fatalf("granted=%d consumed=%d first=%s, want 1/0/%s", granted, consumed, firstAgent, fixture.agentIDs[0])
	}
}

func TestSequentialCollaborationTurnAdvancesToNextAgent(t *testing.T) {
	fixture := newChannelAttentionFixture(t, []attentionRuntimeSpec{{}, {}})
	trigger := fixture.insertMessage(t, "user", testUserID, "Count to two", nil)
	result, err := fixture.handler.createCollaborationSession(context.Background(), collaborationSessionCreateParams{
		WorkspaceID:     parseUUID(testWorkspaceID),
		ChannelID:       parseUUID(fixture.channel.ID),
		SourceMessageID: parseUUID(trigger.ID),
		ParticipantAgentIDs: []pgtype.UUID{
			parseUUID(fixture.agentIDs[0]),
			parseUUID(fixture.agentIDs[1]),
		},
		Mode:                "sequential",
		Goal:                "Count to two",
		CompletionCondition: map[string]any{"max_turns": 2},
	})
	if err != nil {
		t.Fatalf("create collaboration session: %v", err)
	}

	var firstEventID pgtype.UUID
	if err := testPool.QueryRow(context.Background(), `
		SELECT inbox_event_id
		FROM collaboration_turn
		WHERE session_id = $1 AND turn_index = 0`, result.SessionID).Scan(&firstEventID); err != nil {
		t.Fatalf("load first turn event: %v", err)
	}
	event, err := fixture.handler.Queries.GetAgentInboxEvent(context.Background(), firstEventID)
	if err != nil {
		t.Fatalf("load first turn inbox event: %v", err)
	}
	tx, err := fixture.handler.TxStarter.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin turn completion: %v", err)
	}
	wakes, err := fixture.handler.completeCollaborationTurnTx(context.Background(), tx, event)
	if err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatalf("complete collaboration turn: %v", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("commit turn completion: %v", err)
	}
	if len(wakes) != 1 || uuidToString(wakes[0].agent.ID) != fixture.agentIDs[1] {
		t.Fatalf("next wakes = %+v, want agent %s", wakes, fixture.agentIDs[1])
	}

	var sessionStatus string
	var currentTurn, granted, consumed int
	var nextAgent string
	if err := testPool.QueryRow(context.Background(), `
		SELECT session.status, session.current_turn_index,
		       count(*) FILTER (WHERE turn.grant_status = 'granted')::int,
		       count(*) FILTER (WHERE turn.grant_status = 'consumed')::int,
		       max(turn.agent_id::text) FILTER (WHERE turn.turn_index = 1)
		FROM collaboration_session session
		JOIN collaboration_turn turn ON turn.session_id = session.id
		WHERE session.id = $1
		GROUP BY session.status, session.current_turn_index`, result.SessionID).Scan(&sessionStatus, &currentTurn, &granted, &consumed, &nextAgent); err != nil {
		t.Fatalf("inspect advanced session: %v", err)
	}
	if sessionStatus != "active" || currentTurn != 1 || granted != 1 || consumed != 1 || nextAgent != fixture.agentIDs[1] {
		t.Fatalf("status=%s current=%d granted=%d consumed=%d next=%s", sessionStatus, currentTurn, granted, consumed, nextAgent)
	}

	var secondEventID pgtype.UUID
	if err := testPool.QueryRow(context.Background(), `
		SELECT inbox_event_id
		FROM collaboration_turn
		WHERE session_id = $1 AND turn_index = 1`, result.SessionID).Scan(&secondEventID); err != nil {
		t.Fatalf("load second turn event: %v", err)
	}
	secondEvent, err := fixture.handler.Queries.GetAgentInboxEvent(context.Background(), secondEventID)
	if err != nil {
		t.Fatalf("load second turn inbox event: %v", err)
	}
	tx, err = fixture.handler.TxStarter.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin second completion: %v", err)
	}
	wakes, err = fixture.handler.completeCollaborationTurnTx(context.Background(), tx, secondEvent)
	if err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatalf("complete second turn: %v", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("commit second completion: %v", err)
	}
	if len(wakes) != 0 {
		t.Fatalf("second completion wakes = %d, want 0", len(wakes))
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT session.status,
		       count(*) FILTER (WHERE turn.grant_status = 'granted')::int,
		       count(*) FILTER (WHERE turn.grant_status = 'consumed')::int
		FROM collaboration_session session
		JOIN collaboration_turn turn ON turn.session_id = session.id
		WHERE session.id = $1
		GROUP BY session.status`, result.SessionID).Scan(&sessionStatus, &granted, &consumed); err != nil {
		t.Fatalf("inspect completed session: %v", err)
	}
	if sessionStatus != "completed" || granted != 0 || consumed != 2 {
		t.Fatalf("completed status=%s granted=%d consumed=%d, want completed/0/2", sessionStatus, granted, consumed)
	}
}
