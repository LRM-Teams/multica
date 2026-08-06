package handler

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestSequentialCollaborationSessionCreatesSingleTurnGrant(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}, {}, {}})
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
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}, {}})
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

func TestSequentialCollaborationRejectsDuplicateTurnConsumption(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	trigger := fixture.insertMessage(t, "user", testUserID, "Count once", nil)
	result, err := fixture.handler.createCollaborationSession(context.Background(), collaborationSessionCreateParams{
		WorkspaceID:         parseUUID(testWorkspaceID),
		ChannelID:           parseUUID(fixture.channel.ID),
		SourceMessageID:     parseUUID(trigger.ID),
		ParticipantAgentIDs: []pgtype.UUID{parseUUID(fixture.agentIDs[0])},
		Mode:                "sequential",
		CompletionCondition: map[string]any{"max_turns": 2},
	})
	if err != nil {
		t.Fatalf("create collaboration session: %v", err)
	}
	var eventID pgtype.UUID
	if err := testPool.QueryRow(context.Background(), `SELECT inbox_event_id FROM collaboration_turn WHERE session_id = $1 AND turn_index = 0`, result.SessionID).Scan(&eventID); err != nil {
		t.Fatalf("load turn event: %v", err)
	}
	event, err := fixture.handler.Queries.GetAgentInboxEvent(context.Background(), eventID)
	if err != nil {
		t.Fatalf("load turn inbox event: %v", err)
	}
	tx, err := fixture.handler.TxStarter.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin first completion: %v", err)
	}
	if _, err := fixture.handler.completeCollaborationTurnTx(context.Background(), tx, event); err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatalf("first completion: %v", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("commit first completion: %v", err)
	}
	tx, err = fixture.handler.TxStarter.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin duplicate completion: %v", err)
	}
	_, err = fixture.handler.completeCollaborationTurnTx(context.Background(), tx, event)
	_ = tx.Rollback(context.Background())
	if err == nil {
		t.Fatal("duplicate completion unexpectedly succeeded")
	}
}

func TestSequentialCollaborationRejectsStaleSessionVersion(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	trigger := fixture.insertMessage(t, "user", testUserID, "Count once", nil)
	result, err := fixture.handler.createCollaborationSession(context.Background(), collaborationSessionCreateParams{
		WorkspaceID:         parseUUID(testWorkspaceID),
		ChannelID:           parseUUID(fixture.channel.ID),
		SourceMessageID:     parseUUID(trigger.ID),
		ParticipantAgentIDs: []pgtype.UUID{parseUUID(fixture.agentIDs[0])},
		Mode:                "sequential",
	})
	if err != nil {
		t.Fatalf("create collaboration session: %v", err)
	}
	var eventID pgtype.UUID
	if err := testPool.QueryRow(context.Background(), `SELECT inbox_event_id FROM collaboration_turn WHERE session_id = $1`, result.SessionID).Scan(&eventID); err != nil {
		t.Fatalf("load turn event: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE collaboration_session SET version = version + 1 WHERE id = $1`, result.SessionID); err != nil {
		t.Fatalf("make turn stale: %v", err)
	}
	event, err := fixture.handler.Queries.GetAgentInboxEvent(context.Background(), eventID)
	if err != nil {
		t.Fatalf("load turn inbox event: %v", err)
	}
	tx, err := fixture.handler.TxStarter.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin stale completion: %v", err)
	}
	_, err = fixture.handler.completeCollaborationTurnTx(context.Background(), tx, event)
	_ = tx.Rollback(context.Background())
	if err == nil {
		t.Fatal("stale completion unexpectedly succeeded")
	}
}

func TestCollaborationTurnTimeoutSuspendsSession(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	trigger := fixture.insertMessage(t, "user", testUserID, "Count slowly", nil)
	result, err := fixture.handler.createCollaborationSession(context.Background(), collaborationSessionCreateParams{
		WorkspaceID:         parseUUID(testWorkspaceID),
		ChannelID:           parseUUID(fixture.channel.ID),
		SourceMessageID:     parseUUID(trigger.ID),
		ParticipantAgentIDs: []pgtype.UUID{parseUUID(fixture.agentIDs[0])},
		Mode:                "sequential",
		TurnTimeout:         time.Second,
	})
	if err != nil {
		t.Fatalf("create collaboration session: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE collaboration_turn SET deadline_at = now() - interval '1 millisecond' WHERE session_id = $1`, result.SessionID); err != nil {
		t.Fatalf("expire turn: %v", err)
	}
	if got := fixture.handler.SweepCollaborationTurnTimeouts(context.Background(), 10); got != 1 {
		t.Fatalf("SweepCollaborationTurnTimeouts() = %d, want 1", got)
	}
	var sessionStatus, grantStatus string
	if err := testPool.QueryRow(context.Background(), `
		SELECT session.status, turn.grant_status
		FROM collaboration_session session
		JOIN collaboration_turn turn ON turn.session_id = session.id
		WHERE session.id = $1`, result.SessionID).Scan(&sessionStatus, &grantStatus); err != nil {
		t.Fatalf("inspect expired turn: %v", err)
	}
	if sessionStatus != "suspended" || grantStatus != "expired" {
		t.Fatalf("session=%s turn=%s, want suspended/expired", sessionStatus, grantStatus)
	}
}

func TestCollaborationTurnRejectsWrongAgentEvent(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}, {}})
	trigger := fixture.insertMessage(t, "user", testUserID, "Only the granted agent may act", nil)
	result, err := fixture.handler.createCollaborationSession(context.Background(), collaborationSessionCreateParams{
		WorkspaceID:         parseUUID(testWorkspaceID),
		ChannelID:           parseUUID(fixture.channel.ID),
		SourceMessageID:     parseUUID(trigger.ID),
		ParticipantAgentIDs: []pgtype.UUID{parseUUID(fixture.agentIDs[0]), parseUUID(fixture.agentIDs[1])},
		Mode:                "sequential",
	})
	if err != nil {
		t.Fatalf("create collaboration session: %v", err)
	}
	var eventID pgtype.UUID
	if err := testPool.QueryRow(context.Background(), `SELECT inbox_event_id FROM collaboration_turn WHERE session_id = $1`, result.SessionID).Scan(&eventID); err != nil {
		t.Fatalf("load turn event: %v", err)
	}
	event, err := fixture.handler.Queries.GetAgentInboxEvent(context.Background(), eventID)
	if err != nil {
		t.Fatalf("load turn inbox event: %v", err)
	}
	event.AgentID = parseUUID(fixture.agentIDs[1])
	tx, err := fixture.handler.TxStarter.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin wrong-agent completion: %v", err)
	}
	_, err = fixture.handler.completeCollaborationTurnTx(context.Background(), tx, event)
	_ = tx.Rollback(context.Background())
	if err == nil {
		t.Fatal("wrong-agent completion unexpectedly succeeded")
	}
}

func TestCollaborationSessionBindsIssueOptionally(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	trigger := fixture.insertMessage(t, "user", testUserID, "Coordinate this issue", nil)
	var issueID string
	if err := testPool.QueryRow(context.Background(), `
		WITH next_number AS (
		  SELECT COALESCE(MAX(number), 0) + 1 AS n FROM issue WHERE workspace_id = $1
		)
		INSERT INTO issue (workspace_id, title, description, status, priority, creator_type, creator_id, number)
		SELECT $1, 'collaboration issue binding', '', 'todo', 'none', 'member', $2, n
		FROM next_number
		RETURNING id`, testWorkspaceID, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	result, err := fixture.handler.createCollaborationSession(context.Background(), collaborationSessionCreateParams{
		WorkspaceID:         parseUUID(testWorkspaceID),
		ChannelID:           parseUUID(fixture.channel.ID),
		IssueID:             parseUUID(issueID),
		SourceMessageID:     parseUUID(trigger.ID),
		ParticipantAgentIDs: []pgtype.UUID{parseUUID(fixture.agentIDs[0])},
		Mode:                "sequential",
	})
	if err != nil {
		t.Fatalf("create issue-bound collaboration session: %v", err)
	}
	var boundIssue string
	if err := testPool.QueryRow(context.Background(), `SELECT issue_id::text FROM collaboration_session WHERE id = $1`, result.SessionID).Scan(&boundIssue); err != nil {
		t.Fatalf("load bound issue: %v", err)
	}
	if boundIssue != issueID {
		t.Fatalf("issue_id = %s, want %s", boundIssue, issueID)
	}
}

func TestCollaborationTurnTransportConsumeAuditsAndCompletionAdvances(t *testing.T) {
	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}, {}})
	trigger := fixture.insertMessage(t, "user", testUserID, "Count to two with transport", nil)
	result, err := fixture.handler.createCollaborationSession(context.Background(), collaborationSessionCreateParams{
		WorkspaceID:         parseUUID(testWorkspaceID),
		ChannelID:           parseUUID(fixture.channel.ID),
		SourceMessageID:     parseUUID(trigger.ID),
		ParticipantAgentIDs: []pgtype.UUID{parseUUID(fixture.agentIDs[0]), parseUUID(fixture.agentIDs[1])},
		Mode:                "sequential",
		CompletionCondition: map[string]any{"max_turns": 2},
	})
	if err != nil {
		t.Fatalf("create collaboration session: %v", err)
	}
	var firstEventID pgtype.UUID
	if err := testPool.QueryRow(context.Background(), `SELECT inbox_event_id FROM collaboration_turn WHERE session_id = $1 AND turn_index = 0`, result.SessionID).Scan(&firstEventID); err != nil {
		t.Fatalf("load first event: %v", err)
	}
	event, err := fixture.handler.Queries.GetAgentInboxEvent(context.Background(), firstEventID)
	if err != nil {
		t.Fatalf("load event: %v", err)
	}
	tx, err := fixture.handler.TxStarter.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin completion: %v", err)
	}
	wakes, err := fixture.handler.completeCollaborationTurnTx(context.Background(), tx, event)
	if err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatalf("complete pre-consumed turn: %v", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("commit completion: %v", err)
	}
	if len(wakes) != 1 || uuidToString(wakes[0].agent.ID) != fixture.agentIDs[1] {
		t.Fatalf("next wakes = %+v, want agent %s", wakes, fixture.agentIDs[1])
	}
	var grantStatus string
	var auditCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT turn.grant_status,
		       (SELECT count(*) FROM channel_decision_audit audit
		         WHERE audit.inbox_event_id = $2 AND audit.event_type = 'turn_grant_consumed')::int
		FROM collaboration_turn turn
		WHERE turn.session_id = $1 AND turn.turn_index = 0`, result.SessionID, firstEventID).Scan(&grantStatus, &auditCount); err != nil {
		t.Fatalf("inspect consumed turn: %v", err)
	}
	if grantStatus != "consumed" || auditCount != 1 {
		t.Fatalf("turn status=%s audit=%d, want consumed/1", grantStatus, auditCount)
	}
}
