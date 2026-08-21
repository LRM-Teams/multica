package handler

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ensureSystemGeneralForTest creates the workspace #general system channel if it
// is missing, so the commit path's membership step has a target.
func ensureSystemGeneralForTest(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	var id string
	err := testPool.QueryRow(ctx, `
		SELECT id::text FROM channel WHERE workspace_id = $1 AND system_key = 'general' LIMIT 1`,
		parseUUID(testWorkspaceID)).Scan(&id)
	if err == nil {
		return id
	}
	_ = testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, description, created_by, kind, system_key)
		VALUES ($1, 'general', 'Workspace-wide conversation', $2, 'group', 'general')
		RETURNING id::text`, parseUUID(testWorkspaceID), parseUUID(testUserID)).Scan(&id)
	return id
}

func TestCommitAgentFromActionMessage_ConcurrentConfirmationCreatesOneAgent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	ws := parseUUID(testWorkspaceID)
	owner := parseUUID(testUserID)
	_ = ensureSystemGeneralForTest(t)
	channelID := seedTestChannelForOwner(t)
	messageID := seedPreparedActionForTest(t, channelID)
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM agent_action WHERE channel_message_id = $1`, parseUUID(messageID))
		_, _ = testPool.Exec(ctx, `DELETE FROM channel_message WHERE id = $1`, parseUUID(messageID))
		_, _ = testPool.Exec(ctx, `DELETE FROM channel WHERE id = $1`, parseUUID(channelID))
	})

	params := db.CreateAgentParams{
		WorkspaceID: ws, Name: "concurrent-proposal-agent", DisplayName: "concurrent-proposal-agent", Description: "concurrent proposal", RuntimeMode: "cloud",
		RuntimeConfig: []byte("{}"), RuntimeID: parseUUID(testRuntimeID),
		OwnerID: owner, CustomEnv: []byte("{}"),
		CustomArgs: []byte("[]"), Model: pgtype.Text{String: "composer-1.5", Valid: true},
	}
	type result struct {
		agent db.Agent
		err   error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for range 2 {
		go func() {
			ready.Done()
			<-start
			agent, err := testHandler.createAgentFromActionMessage(ctx, ws, owner, parseUUID(messageID), params, "Concurrent Proposal Agent")
			results <- result{agent: agent, err: err}
		}()
	}
	ready.Wait()
	close(start)
	first, second := <-results, <-results

	successes := 0
	conflicts := 0
	var createdID pgtype.UUID
	for _, got := range []result{first, second} {
		if got.err == nil {
			successes++
			createdID = got.agent.ID
			continue
		}
		if ce, ok := got.err.(*codedActionCommitError); ok && ce.status == 409 && ce.code == agentActionMessageNotPrepared {
			conflicts++
			continue
		}
		t.Fatalf("unexpected concurrent result: %v", got.err)
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent results: successes=%d conflicts=%d, want 1/1", successes, conflicts)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(ctx, `DELETE FROM agent WHERE id = $1`, createdID) })

	var agentCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent a
		JOIN agent_action aa ON aa.result_agent_id = a.id
		WHERE aa.channel_message_id = $1 AND aa.status = 'executed'`, parseUUID(messageID)).Scan(&agentCount); err != nil || agentCount != 1 {
		t.Fatalf("committed Agent count=%d err=%v, want 1", agentCount, err)
	}
}

// seedTestChannelForOwner creates a disposable group channel the workspace owner
// (testUserID) is a member of, so the commit path's visibility check passes.
func seedTestChannelForOwner(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by, kind)
		VALUES ($1, $2, $3, 'group')
		RETURNING id::text`, testWorkspaceID, "aac-"+randomID(), testUserID).Scan(&id); err != nil {
		t.Fatalf("create action channel: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'user', $3)
		ON CONFLICT DO NOTHING`, parseUUID(id), parseUUID(testWorkspaceID), parseUUID(testUserID)); err != nil {
		t.Fatalf("add owner channel member: %v", err)
	}
	return id
}

// seedPreparedActionForTest inserts a canonical channel_message carrying the
// agent:create action part plus its prepared agent_action row, and returns the
// message id.
func seedPreparedActionForTest(t *testing.T, channelID string) string {
	t.Helper()
	ctx := context.Background()
	ws := parseUUID(testWorkspaceID)

	actionPart := agentActionMessagePart("proposal-agent", "summary", nil)
	partsRaw := "[{\"type\":\"reference\",\"ref_type\":\"agent:create\",\"ref_id\":\"proposal-agent\",\"label\":\"proposal-agent\",\"params\":" + string(actionPart.Params) + "}]"

	var msgID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, source, parts)
		VALUES ($1, $2, 'agent', NULL, 'proposer', '[agent:create proposal] proposal-agent', 'multica', $3::jsonb)
		RETURNING id::text`, parseUUID(channelID), ws, partsRaw).Scan(&msgID); err != nil {
		t.Fatalf("insert action message: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_action (channel_message_id, workspace_id, action_type, status, proposed_payload, prepared_by_agent_id, prepared_at)
		VALUES ($1, $2, 'agent:create', 'prepared', '{"name":"proposal-agent","description":"summary"}'::jsonb, NULL, now())`,
		parseUUID(msgID), ws); err != nil {
		t.Fatalf("seed agent_action: %v", err)
	}
	return msgID
}

// TestCommitAgentFromActionMessage verifies LRM-2343 S2 atomic, idempotent
// commit: a prepared agent:create proposal Message commits to an Agent, the
// action becomes executed, the Agent lands in system #general, and every replay
// returns a terminal conflict without creating another Agent.
func TestCommitAgentFromActionMessage(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	ws := parseUUID(testWorkspaceID)
	owner := parseUUID(testUserID)
	generalID := ensureSystemGeneralForTest(t)

	// A group channel the owner can see (visibility seam).
	channelID := seedTestChannelForOwner(t)
	cleanupChannel := func() { _, _ = testPool.Exec(ctx, `DELETE FROM channel WHERE id = $1`, parseUUID(channelID)) }

	messageID := seedPreparedActionForTest(t, channelID)
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM agent_action WHERE channel_message_id = $1`, parseUUID(messageID))
		_, _ = testPool.Exec(ctx, `DELETE FROM channel_message WHERE id = $1`, parseUUID(messageID))
		cleanupChannel()
	})

	createParams := db.CreateAgentParams{
		WorkspaceID:   ws,
		Name:          "proposal-agent",
		DisplayName:   "proposal-agent",
		Description:   "summary",
		RuntimeMode:   "cloud",
		RuntimeConfig: []byte("{}"),
		RuntimeID:     parseUUID(testRuntimeID),
		OwnerID:       owner,
		CustomEnv:     []byte("{}"),
		CustomArgs:    []byte("[]"),
		McpConfig:     nil,
		Model:         pgtype.Text{String: "composer-1.5", Valid: true},
		ThinkingLevel: pgtype.Text{},
	}

	created, err := testHandler.createAgentFromActionMessage(ctx, ws, owner, parseUUID(messageID), createParams, "Proposal Agent")
	if err != nil {
		t.Fatalf("commit failed: %v", err)
	}
	if !created.ID.Valid {
		t.Fatalf("commit returned no agent")
	}
	if created.Name != "proposal-agent" || created.DisplayName != "proposal-agent" {
		t.Fatalf("created identity = name %q display %q", created.Name, created.DisplayName)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM agent WHERE id = $1`, created.ID)
	})

	// Action is executed with result + committer recorded.
	var status, resultAgent string
	var finalPayloadRaw []byte
	if err := testPool.QueryRow(ctx, `
		SELECT status, COALESCE(result_agent_id::text, ''), final_payload
		FROM agent_action WHERE channel_message_id = $1`, parseUUID(messageID)).Scan(&status, &resultAgent, &finalPayloadRaw); err != nil {
		t.Fatalf("load action: %v", err)
	}
	if status != "executed" {
		t.Fatalf("action status=%q want executed", status)
	}
	if resultAgent != uuidToString(created.ID) {
		t.Fatalf("result_agent mismatched: %q vs %q", resultAgent, uuidToString(created.ID))
	}
	var finalPayload map[string]any
	if err := json.Unmarshal(finalPayloadRaw, &finalPayload); err != nil {
		t.Fatalf("decode final payload: %v", err)
	}
	if finalPayload["runtime_id"] != testRuntimeID || finalPayload["model"] != "composer-1.5" {
		t.Fatalf("final payload missing committed configuration: %#v", finalPayload)
	}
	for _, forbidden := range []string{"instructions", "runtime_config", "custom_env", "custom_args", "mcp_config"} {
		if _, leaked := finalPayload[forbidden]; leaked {
			t.Fatalf("final payload must not persist %s: %#v", forbidden, finalPayload)
		}
	}

	var partsRaw []byte
	if err := testPool.QueryRow(ctx, `SELECT parts FROM channel_message WHERE id = $1`, parseUUID(messageID)).Scan(&partsRaw); err != nil {
		t.Fatalf("load committed proposal parts: %v", err)
	}
	var parts []protocol.MessagePart
	if err := json.Unmarshal(partsRaw, &parts); err != nil || len(parts) != 1 {
		t.Fatalf("decode committed proposal parts: parts=%#v err=%v", parts, err)
	}
	var partParams map[string]any
	if err := json.Unmarshal(parts[0].Params, &partParams); err != nil {
		t.Fatalf("decode committed proposal params: %v", err)
	}
	if partParams["status"] != agentActionStatusExecuted || partParams["result_agent_id"] != uuidToString(created.ID) {
		t.Fatalf("message proposal part was not marked executed: %#v", partParams)
	}
	for _, forbidden := range []string{"runtime_id", "model", "thinking_level", "custom_env", "credentials"} {
		if _, leaked := partParams[forbidden]; leaked {
			t.Fatalf("public proposal part leaked %s: %#v", forbidden, partParams)
		}
	}

	// Agent is a member of system #general.
	var n int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_member
		WHERE channel_id = $1 AND member_type = 'agent' AND member_id = $2`,
		parseUUID(generalID), created.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("expected agent in #general, got n=%d err=%v", n, err)
	}

	var launchID, launchRuntimeID string
	if err := testPool.QueryRow(ctx, `
		SELECT launch_id::text, runtime_id::text
		FROM agent_runner_launch_projection WHERE agent_id = $1`, created.ID).Scan(&launchID, &launchRuntimeID); err != nil {
		t.Fatalf("load desired launch: %v", err)
	}
	if launchID == "" || launchRuntimeID != uuidToString(createParams.RuntimeID) {
		t.Fatalf("desired launch = id=%q runtime=%q", launchID, launchRuntimeID)
	}

	// A completed confirmation is terminal. Even an identical replay conflicts;
	// clients recover by reloading the completed Message, never by treating the
	// second click as another successful commit.
	if _, err := testHandler.createAgentFromActionMessage(ctx, ws, owner, parseUUID(messageID), createParams, "Proposal Agent"); err == nil {
		t.Fatal("expected 409 conflict on identical replay, got nil")
	} else if ce, ok := err.(*codedActionCommitError); !ok || ce.status != 409 || ce.code != agentActionMessageNotPrepared {
		t.Fatalf("expected terminal coded 409, got %v", err)
	}
	var createdCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent WHERE id = $1`, created.ID).Scan(&createdCount); err != nil || createdCount != 1 {
		t.Fatalf("replay changed created Agent count: count=%d err=%v", createdCount, err)
	}

	// Different final payload -> 409.
	diffParams := createParams
	diffParams.Model = pgtype.Text{String: "different-model", Valid: true}
	if _, err := testHandler.createAgentFromActionMessage(ctx, ws, owner, parseUUID(messageID), diffParams, "Proposal Agent"); err == nil {
		t.Fatalf("expected 409 conflict on different final payload, got nil")
	} else if ce, ok := err.(*codedActionCommitError); !ok || ce.status != 409 {
		t.Fatalf("expected coded 409, got %v", err)
	}
}

func TestCreateAgentManagedCommitAtomicallyCreatesMembershipAndDesiredLaunch(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	ws := parseUUID(testWorkspaceID)
	generalID := ensureSystemGeneralForTest(t)
	params := db.CreateAgentParams{
		WorkspaceID:   ws,
		Description:   "manual create",
		RuntimeMode:   "cloud",
		RuntimeConfig: []byte("{}"),
		RuntimeID:     parseUUID(testRuntimeID),
		OwnerID:       parseUUID(testUserID),
		CustomEnv:     []byte("{}"),
		CustomArgs:    []byte("[]"),
		Model:         pgtype.Text{String: "composer-1.5", Valid: true},
	}
	created, err := testHandler.createAgentManagedCommit(ctx, ws, params, "Manual Intent Agent")
	if err != nil {
		t.Fatalf("manual managed commit: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(ctx, `DELETE FROM agent WHERE id = $1`, created.ID) })

	var memberships int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_member
		WHERE channel_id = $1 AND member_type = 'agent' AND member_id = $2`,
		parseUUID(generalID), created.ID).Scan(&memberships); err != nil || memberships != 1 {
		t.Fatalf("manual general membership = %d, err=%v", memberships, err)
	}
	var launchCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_runner_launch_projection WHERE agent_id = $1`, created.ID).Scan(&launchCount); err != nil || launchCount != 1 {
		t.Fatalf("manual desired launch count = %d, err=%v", launchCount, err)
	}
}

// TestAgentActionFinalPayloadHashStability verifies the idempotency hash is
// deterministic, differs across content, and never encodes secrets by dumping
// raw create params (it only hashes the final non-sensitive fields).
func TestAgentActionFinalPayloadHashStability(t *testing.T) {
	base := db.CreateAgentParams{
		DisplayName:   "Agent A",
		Name:          "",
		Description:   "desc",
		RuntimeID:     parseUUID("00000000-0000-0000-0000-000000000001"),
		Model:         pgtype.Text{String: "m1", Valid: true},
		ThinkingLevel: pgtype.Text{},
	}
	h1 := agentActionFinalPayloadHash(base, map[string]any{"preferred_computer": "box-1"})
	h2 := agentActionFinalPayloadHash(base, map[string]any{"preferred_computer": "box-1"})
	if h1 != h2 {
		t.Fatalf("hash not stable: %s vs %s", h1, h2)
	}
	diff := base
	diff.Model = pgtype.Text{String: "m2", Valid: true}
	if agentActionFinalPayloadHash(diff, map[string]any{"preferred_computer": "box-1"}) == h1 {
		t.Fatalf("hash collides across differing final payload")
	}
	// Different preferred computer must change the hash (part of final payload).
	if agentActionFinalPayloadHash(base, map[string]any{"preferred_computer": "box-2"}) == h1 {
		t.Fatalf("hash ignores preferred computer")
	}
	// The hash must be a fixed-length hex digest, never a raw JSON dump.
	if len(h1) != 64 {
		t.Fatalf("expected sha256 hex (64), got %d", len(h1))
	}
}
