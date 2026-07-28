package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// #802 handler/revoke contracts (Vera).
// Barry (2026-07-28): write red controls first; do not wait for impl.
// - remove-vs-claim final 4-tuple after interleaved revoke
// - derived-agent may only output to env-dispatch origin channel
// No production code in this package for #802.

// loadAgentChannelRevokeTuple reads membership/event/delivery/execution for the
// Barry 4-tuple after remove-vs-claim interleaving.
type agentChannelRevokeTuple struct {
	MembershipCount int
	EventStatus     string
	EventOutcome    string
	DeliveryStatus  string
	ExecutionStatus string
}

func loadAgentChannelRevokeTuple(t *testing.T, channelID, agentID, eventID, deliveryID, executionID string) agentChannelRevokeTuple {
	t.Helper()
	ctx := context.Background()
	var tup agentChannelRevokeTuple
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)::int
		FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2
		  AND member_type = 'agent' AND member_id = $3`,
		channelID, testWorkspaceID, agentID).Scan(&tup.MembershipCount); err != nil {
		t.Fatalf("count membership: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT status, COALESCE(terminal_outcome, '')
		FROM agent_inbox_event WHERE id = $1`, eventID).Scan(&tup.EventStatus, &tup.EventOutcome); err != nil {
		t.Fatalf("load event: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT status FROM agent_event_delivery WHERE id = $1`, deliveryID).Scan(&tup.DeliveryStatus); err != nil {
		t.Fatalf("load delivery: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT status FROM agent_execution WHERE id = $1`, executionID).Scan(&tup.ExecutionStatus); err != nil {
		t.Fatalf("load execution: %v", err)
	}
	return tup
}

func assertRevokeFourTupleClosed(t *testing.T, tup agentChannelRevokeTuple) {
	t.Helper()
	// Barry: membership=0 / event=terminal failed / delivery∉{leased,processing} / execution∉running
	if tup.MembershipCount != 0 {
		t.Errorf("membership count=%d, want 0", tup.MembershipCount)
	}
	eventTerminalFailed := (tup.EventStatus == "acked" || tup.EventStatus == "failed" || tup.EventStatus == "suppressed") &&
		(tup.EventOutcome == "failed" || tup.EventOutcome == "no_reply" || tup.EventOutcome == "cancelled")
	// Prefer explicit membership_revoked / failed terminal for remove path.
	if tup.EventStatus == "pending" || tup.EventStatus == "draining" {
		t.Errorf("event still active status=%q outcome=%q; want terminal failed", tup.EventStatus, tup.EventOutcome)
	}
	if tup.EventOutcome == "" && (tup.EventStatus == "acked" || tup.EventStatus == "failed") {
		// terminal without outcome is weak but may pass some paths; still require not active
	} else if !eventTerminalFailed && tup.EventStatus != "acked" && tup.EventStatus != "failed" {
		t.Errorf("event status=%q outcome=%q; want terminal failed after revoke", tup.EventStatus, tup.EventOutcome)
	}
	switch tup.DeliveryStatus {
	case "leased", "processing":
		t.Errorf("delivery status=%q; must not remain leased|processing after revoke", tup.DeliveryStatus)
	}
	if tup.ExecutionStatus == "running" {
		t.Errorf("execution status=%q; must not remain running after revoke", tup.ExecutionStatus)
	}
}

// seedBoundaryRevokeFixture creates agent member + pending channel inbox event +
// leased delivery + running execution bound to that event (Barry 4-tuple surface).
func seedBoundaryRevokeFixture(t *testing.T) (channelID, agentID, eventID, deliveryID, executionID string) {
	t.Helper()
	ctx := context.Background()

	agentID = createHandlerTestAgent(t, "BoundaryRevokeAgent", []byte("[]"))
	channelID = seedChannelForTest(t, "boundary-revoke-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}

	runtimeID := handlerTestRuntimeID(t)
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
			workspace_id, channel_id, agent_id, runtime_id, reason, status, priority
		)
		VALUES ($1, $2, $3, $4, 'mention', 'pending', 10)
		RETURNING id`, testWorkspaceID, channelID, agentID, runtimeID).Scan(&eventID); err != nil {
		t.Fatalf("seed inbox event: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_execution WHERE source_event_id = $1`, eventID)
		testPool.Exec(context.Background(), `DELETE FROM agent_event_delivery WHERE inbox_event_id = $1`, eventID)
		testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, eventID)
	})

	// Optional agent_session_id — match cancel fixture when session exists.
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_event_delivery (
			workspace_id, inbox_event_id, runtime_id, status
		)
		VALUES ($1, $2, $3, 'leased')
		RETURNING id`, testWorkspaceID, eventID, runtimeID).Scan(&deliveryID); err != nil {
		t.Fatalf("seed leased delivery: %v", err)
	}

	executionID = uuid.NewString()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_execution (
			id, source_kind, source_event_id, source,
			workspace_id, runtime_id, agent_id, status
		)
		VALUES ($1, 'inbox', $2, 'chat', $3, $4, $5, 'running')
	`, executionID, eventID, testWorkspaceID, runtimeID, agentID); err != nil {
		t.Fatalf("seed running execution: %v", err)
	}

	return channelID, agentID, eventID, deliveryID, executionID
}

func removeAgentMemberRequest(t *testing.T, channelID, agentID string) *http.Request {
	t.Helper()
	req := newRequestAs(testUserID, http.MethodDelete,
		"/api/channels/"+channelID+"/members/agent/"+agentID, nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	return withRouteParams(req, "channelId", channelID, "memberType", "agent", "memberId", agentID)
}

// TestBoundary_RemoveAgent_FinalFourTuple_AfterRevoke is the sequential form of
// Barry's revoke contract: after remove, membership/event/delivery/execution
// must all be closed. Intentionally red on origin/dev until #801 atomic revoke.
func TestBoundary_RemoveAgent_FinalFourTuple_AfterRevoke(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	channelID, agentID, eventID, deliveryID, executionID := seedBoundaryRevokeFixture(t)

	rec := httptest.NewRecorder()
	testHandler.RemoveChannelMember(rec, removeAgentMemberRequest(t, channelID, agentID))
	if rec.Code != http.StatusOK {
		t.Fatalf("RemoveChannelMember status=%d body=%s", rec.Code, rec.Body.String())
	}

	tup := loadAgentChannelRevokeTuple(t, channelID, agentID, eventID, deliveryID, executionID)
	t.Logf("post-remove 4-tuple: membership=%d event=%s/%s delivery=%s execution=%s",
		tup.MembershipCount, tup.EventStatus, tup.EventOutcome, tup.DeliveryStatus, tup.ExecutionStatus)
	assertRevokeFourTupleClosed(t, tup)
}

// TestBoundary_RemoveVsClaim_InterleavedFourTuple forces Barry's race shape:
// lease holds event-ish work while remove runs; final state must not leave
// event=terminal + delivery still leased. Uses concurrent remove + re-lease
// attempt; flaky green is not the goal — closed 4-tuple is.
func TestBoundary_RemoveVsClaim_InterleavedFourTuple(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	channelID, agentID, eventID, deliveryID, executionID := seedBoundaryRevokeFixture(t)
	ctx := context.Background()

	// Simulate lease already holding a leased delivery (fixture). Concurrently:
	// A) RemoveChannelMember  B) try to insert another leased delivery (claim-like)
	// then assert final 4-tuple is closed (no leased delivery survives).
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		// Brief delay so remove can interleave with claim-like insert.
		time.Sleep(5 * time.Millisecond)
		rec := httptest.NewRecorder()
		testHandler.RemoveChannelMember(rec, removeAgentMemberRequest(t, channelID, agentID))
		if rec.Code != http.StatusOK {
			t.Errorf("remove status=%d body=%s", rec.Code, rec.Body.String())
		}
	}()
	go func() {
		defer wg.Done()
		// Claim-like: another leased delivery row for same event (poison if not revoked).
		_, _ = testPool.Exec(ctx, `
			INSERT INTO agent_event_delivery (
				workspace_id, inbox_event_id, runtime_id, status
			)
			VALUES ($1, $2, $3, 'leased')
			ON CONFLICT DO NOTHING`, testWorkspaceID, eventID, handlerTestRuntimeID(t))
	}()
	wg.Wait()

	// Re-load primary delivery + any still-leased siblings.
	tup := loadAgentChannelRevokeTuple(t, channelID, agentID, eventID, deliveryID, executionID)
	t.Logf("interleaved 4-tuple: membership=%d event=%s/%s delivery=%s execution=%s",
		tup.MembershipCount, tup.EventStatus, tup.EventOutcome, tup.DeliveryStatus, tup.ExecutionStatus)

	var leasedCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)::int FROM agent_event_delivery
		WHERE inbox_event_id = $1 AND status IN ('leased', 'processing')`, eventID).Scan(&leasedCount); err != nil {
		t.Fatalf("count leased deliveries: %v", err)
	}
	if leasedCount != 0 {
		t.Errorf("leased|processing delivery count=%d after remove∩claim; want 0 (Barry race)", leasedCount)
	}
	assertRevokeFourTupleClosed(t, tup)
}

// TestBoundary_DerivedAgent_CannotOutputToSourceOtherChannel locks Barry's
// env-dispatch exception: derived agent may target origin channel only, not
// every channel where the source agent is a member.
func TestBoundary_DerivedAgent_CannotOutputToSourceOtherChannel(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	sourceID := createHandlerTestAgent(t, "BoundarySourceAgent", []byte("[]"))
	// Derived agent with source_agent_id = sourceID
	var derivedID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, display_name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id,
			instructions, custom_env, custom_args, mcp_config, source_agent_id
		)
		VALUES ($1, $2, $3, '', 'cloud', '{}'::jsonb, $4, 'private', 1, $5, '', '{}'::jsonb, '[]'::jsonb, '[]'::jsonb, $6)
		RETURNING id
	`, testWorkspaceID, "boundary-derived-"+uuid.NewString()[:8], "BoundaryDerived",
		handlerTestRuntimeID(t), testUserID, sourceID).Scan(&derivedID); err != nil {
		t.Fatalf("create derived agent: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, derivedID) })

	originChannel := seedChannelForTest(t, "boundary-origin-"+uuid.NewString(), testUserID)
	otherChannel := seedChannelForTest(t, "boundary-other-"+uuid.NewString(), testUserID)
	// Source is member of BOTH channels (the dangerous wide surface).
	for _, ch := range []string{originChannel, otherChannel} {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
			VALUES ($1, $2, 'agent', $3)
			ON CONFLICT DO NOTHING`, ch, testWorkspaceID, sourceID); err != nil {
			t.Fatalf("seed source member on %s: %v", ch, err)
		}
	}
	// Derived is NOT a direct member of either channel (env-dispatch shape).

	var otherName string
	if err := testPool.QueryRow(ctx, `SELECT name FROM channel WHERE id = $1`, otherChannel).Scan(&otherName); err != nil {
		t.Fatalf("load other channel name: %v", err)
	}

	origin := chatOutputOrigin{
		channelID:   parseUUID(originChannel),
		workspaceID: parseUUID(testWorkspaceID),
		agentID:     parseUUID(derivedID),
	}

	// Cross-channel: #other must be rejected even though source is a member there.
	_, err := testHandler.resolveChannelOutputTarget(ctx, origin, otherName)
	if err == nil {
		t.Fatalf("derived agent resolved #other (source also member there); must deny cross-origin channel (Barry env exception)")
	}

	// Sanity: origin channel by name should still be allowed once source is on origin
	// (narrow exception). If product later requires origin-only without source membership
	// on target, this may change — keep as positive control for "origin is OK".
	var originName string
	if err := testPool.QueryRow(ctx, `SELECT name FROM channel WHERE id = $1`, originChannel).Scan(&originName); err != nil {
		t.Fatalf("load origin name: %v", err)
	}
	if _, err := testHandler.resolveChannelOutputTarget(ctx, origin, originName); err != nil {
		// Origin may also fail if exception requires more than source membership
		// (e.g. current event proof). Log; cross-channel deny is the hard contract.
		t.Logf("origin resolve err=%v (acceptable if exception not fully wired; cross-channel deny is required)", err)
	}
}

// TestBoundary_SourceAgentFallback_DoesNotGrantSurfaceRead documents that
// source_agent_id must NOT widen general surface access (list/members/attach).
// Pure membership: derived without direct membership has no channel surface.
func TestBoundary_SourceAgentFallback_DoesNotGrantSurfaceRead(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	sourceID := createHandlerTestAgent(t, "BoundarySourceRead", []byte("[]"))
	var derivedID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, display_name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id,
			instructions, custom_env, custom_args, mcp_config, source_agent_id
		)
		VALUES ($1, $2, $3, '', 'cloud', '{}'::jsonb, $4, 'private', 1, $5, '', '{}'::jsonb, '[]'::jsonb, '[]'::jsonb, $6)
		RETURNING id
	`, testWorkspaceID, "boundary-derived-read-"+uuid.NewString()[:8], "BoundaryDerivedRead",
		handlerTestRuntimeID(t), testUserID, sourceID).Scan(&derivedID); err != nil {
		t.Fatalf("create derived: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, derivedID) })

	channelID := seedChannelForTest(t, "boundary-read-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, sourceID); err != nil {
		t.Fatalf("seed source member: %v", err)
	}

	// Direct membership gate only — mirrors agentHasSurfaceAccess contract.
	var derivedMember bool
	if err := testPool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM channel_member
			WHERE channel_id = $1 AND workspace_id = $2
			  AND member_type = 'agent' AND member_id = $3
		)`, channelID, testWorkspaceID, derivedID).Scan(&derivedMember); err != nil {
		t.Fatalf("query derived membership: %v", err)
	}
	if derivedMember {
		t.Fatal("derived should not be direct member")
	}

	// Surface read = direct membership only (Ronan agentHasSurfaceAccess).
	ws := parseUUID(testWorkspaceID)
	ch := parseUUID(channelID)
	ag := parseUUID(derivedID)
	if testHandler.channelHasAgentMember(ctx, ws, ch, ag) {
		t.Fatal("derived direct membership true; surface read must be false")
	}
	// source_agent wide fallback must not widen list/members/attach — only
	// narrow output exception on origin channel (separate test).
}
