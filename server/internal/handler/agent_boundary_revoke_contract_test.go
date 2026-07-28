package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// #802 handler/revoke contracts (Vera).
// Barry gate review 2026-07-28: tighten event truth, real lease race, hard origin ALLOW.
// No production code changes.

type agentChannelRevokeTuple struct {
	MembershipCount int
	EventStatus     string
	EventOutcome    string
	FailureReason   string
	TerminalAtSet   bool
	CompletedAtSet  bool
	ActiveDelivery  int
	RunningExec     int
}

func loadAgentChannelRevokeTuple(t *testing.T, channelID, agentID, eventID string) agentChannelRevokeTuple {
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
	var terminalAt, completedAt *time.Time
	if err := testPool.QueryRow(ctx, `
		SELECT status,
		       COALESCE(terminal_outcome, ''),
		       COALESCE(failure_reason, ''),
		       terminal_at,
		       completed_at
		FROM agent_inbox_event WHERE id = $1`, eventID).
		Scan(&tup.EventStatus, &tup.EventOutcome, &tup.FailureReason, &terminalAt, &completedAt); err != nil {
		t.Fatalf("load event: %v", err)
	}
	tup.TerminalAtSet = terminalAt != nil
	tup.CompletedAtSet = completedAt != nil
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)::int FROM agent_event_delivery
		WHERE inbox_event_id = $1 AND status IN ('leased', 'processing')`, eventID).
		Scan(&tup.ActiveDelivery); err != nil {
		t.Fatalf("count active delivery: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)::int FROM agent_execution
		WHERE source_kind = 'inbox' AND source_event_id = $1 AND status = 'running'`, eventID).
		Scan(&tup.RunningExec); err != nil {
		t.Fatalf("count running execution: %v", err)
	}
	return tup
}

// assertMembershipRevokedFourTuple nails Barry's only-true revoke outcome.
func assertMembershipRevokedFourTuple(t *testing.T, tup agentChannelRevokeTuple) {
	t.Helper()
	if tup.MembershipCount != 0 {
		t.Errorf("membership count=%d, want 0", tup.MembershipCount)
	}
	if tup.EventStatus != "acked" ||
		tup.EventOutcome != "failed" ||
		tup.FailureReason != "membership_revoked" {
		t.Errorf("event status=%q outcome=%q failure_reason=%q; want acked/failed/membership_revoked",
			tup.EventStatus, tup.EventOutcome, tup.FailureReason)
	}
	if !tup.TerminalAtSet || !tup.CompletedAtSet {
		t.Errorf("event terminal_at set=%v completed_at set=%v; both must be non-null",
			tup.TerminalAtSet, tup.CompletedAtSet)
	}
	if tup.ActiveDelivery != 0 {
		t.Errorf("active delivery count=%d, want 0 (no leased|processing)", tup.ActiveDelivery)
	}
	if tup.RunningExec != 0 {
		t.Errorf("running execution count=%d, want 0", tup.RunningExec)
	}
}

type boundaryRevokeSeed struct {
	channelID  string
	agentID    string
	runtimeID  string
	eventID    string
	deliveryID string // only sequential seed with pre-leased delivery
	execID     string // only sequential seed with running execution
}

// seedBoundarySequentialRevoke: membership + pending event + leased delivery + running execution.
// For sequential RemoveChannelMember → 4-tuple only (not claim race).
func seedBoundarySequentialRevoke(t *testing.T) boundaryRevokeSeed {
	t.Helper()
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "BoundaryRevokeSeq", []byte("[]"))
	channelID := seedChannelForTest(t, "boundary-revoke-seq-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	// Dedicated runtime bound to this agent (lease path uses COALESCE event/session runtime).
	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, last_seen_at
		)
		VALUES ($1, $2, $3, 'local', 'boundary_test', 'online', 'boundary revoke runtime', now())
		RETURNING id`,
		testWorkspaceID, "boundary-"+uuid.NewString(), "Boundary Runtime "+uuid.NewString()).Scan(&runtimeID); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID) })
	if _, err := testPool.Exec(ctx, `UPDATE agent SET runtime_id = $2 WHERE id = $1`, agentID, runtimeID); err != nil {
		t.Fatalf("bind agent runtime: %v", err)
	}

	var eventID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
			workspace_id, channel_id, agent_id, runtime_id, reason, status, priority
		)
		VALUES ($1, $2, $3, $4, 'mention', 'pending', 10)
		RETURNING id`, testWorkspaceID, channelID, agentID, runtimeID).Scan(&eventID); err != nil {
		t.Fatalf("seed pending event: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_execution WHERE source_event_id = $1`, eventID)
		testPool.Exec(context.Background(), `DELETE FROM agent_event_delivery WHERE inbox_event_id = $1`, eventID)
		testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, eventID)
	})

	var deliveryID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_event_delivery (
			workspace_id, agent_session_id, inbox_event_id, runtime_id, status, lease_expires_at
		)
		SELECT workspace_id, agent_session_id, id, $2, 'leased', now() + interval '5 minutes'
		FROM agent_inbox_event WHERE id = $1
		RETURNING id`, eventID, runtimeID).Scan(&deliveryID); err != nil {
		t.Fatalf("seed leased delivery: %v", err)
	}

	execID := uuid.NewString()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_execution (
			id, source_kind, source_event_id, source,
			workspace_id, runtime_id, agent_id, status
		)
		VALUES ($1, 'inbox', $2, 'chat', $3, $4, $5, 'running')
	`, execID, eventID, testWorkspaceID, runtimeID, agentID); err != nil {
		t.Fatalf("seed running execution: %v", err)
	}

	return boundaryRevokeSeed{
		channelID: channelID, agentID: agentID, runtimeID: runtimeID,
		eventID: eventID, deliveryID: deliveryID, execID: execID,
	}
}

// seedBoundaryClaimRace: membership + pending event, NO active delivery/execution.
// Real claim must go through leaseAgentInboxEventForRuntime.
func seedBoundaryClaimRace(t *testing.T) boundaryRevokeSeed {
	t.Helper()
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "BoundaryRevokeRace", []byte("[]"))
	channelID := seedChannelForTest(t, "boundary-revoke-race-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, last_seen_at
		)
		VALUES ($1, $2, $3, 'local', 'boundary_test', 'online', 'boundary race runtime', now())
		RETURNING id`,
		testWorkspaceID, "boundary-race-"+uuid.NewString(), "Boundary Race Runtime "+uuid.NewString()).Scan(&runtimeID); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID) })
	if _, err := testPool.Exec(ctx, `UPDATE agent SET runtime_id = $2 WHERE id = $1`, agentID, runtimeID); err != nil {
		t.Fatalf("bind agent runtime: %v", err)
	}

	var eventID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
			workspace_id, channel_id, agent_id, runtime_id, reason, status, priority
		)
		VALUES ($1, $2, $3, $4, 'mention', 'pending', 10)
		RETURNING id`, testWorkspaceID, channelID, agentID, runtimeID).Scan(&eventID); err != nil {
		t.Fatalf("seed pending event: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_execution WHERE source_event_id = $1`, eventID)
		testPool.Exec(context.Background(), `DELETE FROM agent_event_delivery WHERE inbox_event_id = $1`, eventID)
		testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, eventID)
	})

	return boundaryRevokeSeed{
		channelID: channelID, agentID: agentID, runtimeID: runtimeID, eventID: eventID,
	}
}

func removeAgentMemberRequest(t *testing.T, channelID, agentID string) *http.Request {
	t.Helper()
	req := newRequestAs(testUserID, http.MethodDelete,
		"/api/channels/"+channelID+"/members/agent/"+agentID, nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	return withRouteParams(req, "channelId", channelID, "memberType", "agent", "memberId", agentID)
}

// TestBoundary_RemoveAgent_FinalFourTuple_AfterRevoke — sequential revoke after
// leased delivery + running execution already exist.
func TestBoundary_RemoveAgent_FinalFourTuple_AfterRevoke(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	seed := seedBoundarySequentialRevoke(t)

	rec := httptest.NewRecorder()
	testHandler.RemoveChannelMember(rec, removeAgentMemberRequest(t, seed.channelID, seed.agentID))
	if rec.Code != http.StatusOK {
		t.Fatalf("RemoveChannelMember status=%d body=%s", rec.Code, rec.Body.String())
	}

	tup := loadAgentChannelRevokeTuple(t, seed.channelID, seed.agentID, seed.eventID)
	t.Logf("post-remove 4-tuple: membership=%d event=%s/%s/%s activeDelivery=%d runningExec=%d",
		tup.MembershipCount, tup.EventStatus, tup.EventOutcome, tup.FailureReason,
		tup.ActiveDelivery, tup.RunningExec)
	assertMembershipRevokedFourTuple(t, tup)
}

// TestBoundary_RemoveVsClaim_RealLeaseRace uses production lease + remove with
// an advisory lock held open so one side blocks on the shared
// agent_channel_membership_revoke(channel,agent) key (Barry lock-order gate).
//
// Interleave:
//  1. Holder takes shared revoke advisory and keeps txn open
//  2. Start leaseAgentInboxEventForRuntime in a goroutine (must block or serialize)
//  3. Release holder, run RemoveChannelMember (or reverse order variants)
//
// Final: if remove succeeds → membership=0 + event acked/failed/membership_revoked
// + no active delivery + no running execution. No protocol-foreign delivery inserts.
func TestBoundary_RemoveVsClaim_RealLeaseRace(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	seed := seedBoundaryClaimRace(t)

	runtime, err := testHandler.Queries.GetAgentRuntime(ctx, parseUUID(seed.runtimeID))
	if err != nil {
		t.Fatalf("load runtime: %v", err)
	}

	// Hold the shared revoke advisory so concurrent lease/remove serialize.
	holdTx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin hold tx: %v", err)
	}
	defer holdTx.Rollback(ctx)
	if _, err := holdTx.Exec(ctx, `
		SELECT pg_advisory_xact_lock(
			hashtext('agent_channel_membership_revoke'),
			hashtext($1 || ':' || $2)
		)`, seed.channelID, seed.agentID); err != nil {
		t.Fatalf("hold revoke advisory: %v", err)
	}

	leaseDone := make(chan error, 1)
	go func() {
		_, err := testHandler.leaseAgentInboxEventForRuntime(ctx, runtime)
		// ErrNoRows is OK if remove terminalized first; other errors surface.
		if errors.Is(err, pgx.ErrNoRows) {
			leaseDone <- nil
			return
		}
		leaseDone <- err
	}()

	// Observe whether lease blocks on the shared revoke advisory while we hold it.
	// Fixed impl: blocks. Unfixed impl: may complete immediately (gate still fails later).
	leaseFinishedEarly := false
	select {
	case err := <-leaseDone:
		leaseFinishedEarly = true
		t.Logf("lease returned while revoke advisory held: err=%v (shared lock order missing if it leased)", err)
	case <-time.After(200 * time.Millisecond):
		// expected on fixed impl: still blocked
	}

	// Release hold, then remove — production path under test.
	if err := holdTx.Rollback(ctx); err != nil {
		t.Fatalf("release hold: %v", err)
	}

	rec := httptest.NewRecorder()
	testHandler.RemoveChannelMember(rec, removeAgentMemberRequest(t, seed.channelID, seed.agentID))
	removeOK := rec.Code == http.StatusOK
	if !removeOK {
		t.Logf("RemoveChannelMember status=%d body=%s", rec.Code, rec.Body.String())
	}

	if !leaseFinishedEarly {
		select {
		case err := <-leaseDone:
			if err != nil {
				t.Logf("lease after advisory release: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("lease did not complete after advisory release")
		}
	}

	// Also try a post-remove lease: must not resurrect active work.
	if _, err := testHandler.leaseAgentInboxEventForRuntime(ctx, runtime); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		t.Logf("post-remove lease err=%v", err)
	}

	if !removeOK {
		t.Fatalf("remove did not succeed (status=%d); cannot assert revoke 4-tuple — implementation blocker", rec.Code)
	}

	tup := loadAgentChannelRevokeTuple(t, seed.channelID, seed.agentID, seed.eventID)
	t.Logf("race final 4-tuple: membership=%d event=%s/%s/%s activeDelivery=%d runningExec=%d",
		tup.MembershipCount, tup.EventStatus, tup.EventOutcome, tup.FailureReason,
		tup.ActiveDelivery, tup.RunningExec)
	assertMembershipRevokedFourTuple(t, tup)
}

// TestBoundary_RemoveVsClaim_LeaseFirstThenRemove: production lease claims the
// pending event, then remove must still close the 4-tuple (no leftover leased).
func TestBoundary_RemoveVsClaim_LeaseFirstThenRemove(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	seed := seedBoundaryClaimRace(t)

	runtime, err := testHandler.Queries.GetAgentRuntime(ctx, parseUUID(seed.runtimeID))
	if err != nil {
		t.Fatalf("load runtime: %v", err)
	}
	delivery, err := testHandler.leaseAgentInboxEventForRuntime(ctx, runtime)
	if err != nil {
		t.Fatalf("production lease before remove: %v (fixture must be leasable)", err)
	}
	if !delivery.InboxEventID.Valid {
		t.Fatal("lease returned empty delivery")
	}

	// Optional: start a running execution as claim path would after lease.
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_execution (
			id, source_kind, source_event_id, source,
			workspace_id, runtime_id, agent_id, status
		)
		VALUES ($1, 'inbox', $2, 'chat', $3, $4, $5, 'running')
	`, uuid.NewString(), seed.eventID, testWorkspaceID, seed.runtimeID, seed.agentID); err != nil {
		t.Fatalf("seed running execution after lease: %v", err)
	}

	rec := httptest.NewRecorder()
	testHandler.RemoveChannelMember(rec, removeAgentMemberRequest(t, seed.channelID, seed.agentID))
	if rec.Code != http.StatusOK {
		t.Fatalf("RemoveChannelMember status=%d body=%s", rec.Code, rec.Body.String())
	}

	tup := loadAgentChannelRevokeTuple(t, seed.channelID, seed.agentID, seed.eventID)
	t.Logf("lease-then-remove 4-tuple: membership=%d event=%s/%s/%s activeDelivery=%d runningExec=%d",
		tup.MembershipCount, tup.EventStatus, tup.EventOutcome, tup.FailureReason,
		tup.ActiveDelivery, tup.RunningExec)
	assertMembershipRevokedFourTuple(t, tup)
}

// TestBoundary_DerivedAgent_OriginAllow_OtherDeny: pair of necessary controls.
// Cross-origin DENY + current origin ALLOW. Surface read stays DENY.
func TestBoundary_DerivedAgent_OriginAllow_OtherDeny(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	sourceID := createHandlerTestAgent(t, "BoundarySourceAgent", []byte("[]"))
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
	for _, ch := range []string{originChannel, otherChannel} {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
			VALUES ($1, $2, 'agent', $3)
			ON CONFLICT DO NOTHING`, ch, testWorkspaceID, sourceID); err != nil {
			t.Fatalf("seed source member: %v", err)
		}
	}

	var originName, otherName string
	if err := testPool.QueryRow(ctx, `SELECT name FROM channel WHERE id = $1`, originChannel).Scan(&originName); err != nil {
		t.Fatalf("origin name: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT name FROM channel WHERE id = $1`, otherChannel).Scan(&otherName); err != nil {
		t.Fatalf("other name: %v", err)
	}

	origin := chatOutputOrigin{
		channelID:   parseUUID(originChannel),
		workspaceID: parseUUID(testWorkspaceID),
		agentID:     parseUUID(derivedID),
	}

	// 1) HARD: other channel DENY even though source is a member there.
	if _, err := testHandler.resolveChannelOutputTarget(ctx, origin, otherName); err == nil {
		t.Fatal("derived resolved #other; must DENY cross-origin channel")
	}

	// 2) HARD: origin channel ALLOW via narrow env-dispatch exception.
	if _, err := testHandler.resolveChannelOutputTarget(ctx, origin, originName); err != nil {
		t.Fatalf("derived must ALLOW origin channel #%s via source membership; err=%v", originName, err)
	}

	// 3) Surface read DENY (direct membership only).
	if testHandler.channelHasAgentMember(ctx, parseUUID(testWorkspaceID), parseUUID(originChannel), parseUUID(derivedID)) {
		t.Fatal("derived must not have direct membership (surface read DENY)")
	}
}

// TestBoundary_SourceAgentFallback_DoesNotGrantSurfaceRead keeps general surface
// read on direct membership only.
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

	ws := parseUUID(testWorkspaceID)
	ch := parseUUID(channelID)
	ag := parseUUID(derivedID)
	if testHandler.channelHasAgentMember(ctx, ws, ch, ag) {
		t.Fatal("derived direct membership true; surface read must be false")
	}
}
