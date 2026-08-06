package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestChannelMemberBatchAddIsAllOrNone(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	validTargetID := createChannelPlainMember(t)
	missingTargetID := uuid.NewString()
	channelID := seedChannelForTest(t, "member-batch-atomic-"+uuid.NewString(), testUserID)
	auditBefore := countAllChannelMemberSuccessActivityForTest(t, channelID)
	systemBefore := countChannelSystemMessagesForTest(t, channelID)

	req := newRequestAs(testUserID, http.MethodPost,
		"/api/channels/"+channelID+"/members/batch",
		AddChannelMembersRequest{Members: []AddChannelMemberRequest{
			{MemberType: "user", MemberID: validTargetID},
			{MemberType: "user", MemberID: missingTargetID},
		}})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.AddChannelMembers(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("mixed valid/invalid batch want 404 got %d: %s", rec.Code, rec.Body.String())
	}
	assertChannelUserMembershipCount(t, channelID, validTargetID, 0)
	assertChannelUserMembershipCount(t, channelID, missingTargetID, 0)
	assertChannelMemberArtifactCountsUnchanged(t, channelID, auditBefore, systemBefore)
}

func TestChannelMemberRemoveSystemEventFailureRollsBackMutationAndRevoke(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	seed := seedBoundarySequentialRevoke(t)
	channelID := seed.channelID
	agentID := seed.agentID
	before := loadAgentChannelRevokeTuple(t, channelID, agentID, seed.eventID)
	auditBefore := countAllChannelMemberSuccessActivityForTest(t, channelID)
	systemBefore := countChannelSystemMessagesForTest(t, channelID)
	restore := installRoleMutationInsertFail(testHandler, channelID)
	t.Cleanup(restore)

	req := newRequestAs(testUserID, http.MethodDelete,
		"/api/channels/"+channelID+"/members/agent/"+agentID+"?expected_remove_effect=none",
		nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withRouteParams(req,
		"channelId", channelID,
		"memberType", "agent",
		"memberId", agentID,
	)
	rec := httptest.NewRecorder()
	testHandler.RemoveChannelMember(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("forced durable-event failure want 500 got %d: %s", rec.Code, rec.Body.String())
	}
	if after := loadAgentChannelRevokeTuple(t, channelID, agentID, seed.eventID); after != before {
		t.Fatalf("failed transaction changed revoke tuple: before=%+v after=%+v", before, after)
	}
	assertChannelMemberArtifactCountsUnchanged(t, channelID, auditBefore, systemBefore)

	restore()
	rec = httptest.NewRecorder()
	testHandler.RemoveChannelMember(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("retry after removing injector want 200 got %d: %s", rec.Code, rec.Body.String())
	}
	assertChannelAgentMembershipCount(t, context.Background(), channelID, agentID, 0)
}

func TestChannelMemberRemoveAuditFailureRollsBackMutationAndRevoke(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	seed := seedBoundarySequentialRevoke(t)
	channelID := seed.channelID
	agentID := seed.agentID
	before := loadAgentChannelRevokeTuple(t, channelID, agentID, seed.eventID)
	auditBefore := countAllChannelMemberSuccessActivityForTest(t, channelID)
	systemBefore := countChannelSystemMessagesForTest(t, channelID)
	previous := testHandler.TxStarter
	restore := func() { testHandler.TxStarter = previous }
	testHandler.TxStarter = memberManagementActivityFailingTxStarter{base: previous}
	t.Cleanup(restore)

	req := newRequestAs(testUserID, http.MethodDelete,
		"/api/channels/"+channelID+"/members/agent/"+agentID+"?expected_remove_effect=none",
		nil)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withRouteParams(req,
		"channelId", channelID,
		"memberType", "agent",
		"memberId", agentID,
	)
	rec := httptest.NewRecorder()
	testHandler.RemoveChannelMember(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("forced audit failure want 500 got %d: %s", rec.Code, rec.Body.String())
	}
	if after := loadAgentChannelRevokeTuple(t, channelID, agentID, seed.eventID); after != before {
		t.Fatalf("failed transaction changed revoke tuple: before=%+v after=%+v", before, after)
	}
	assertChannelMemberArtifactCountsUnchanged(t, channelID, auditBefore, systemBefore)

	restore()
	rec = httptest.NewRecorder()
	testHandler.RemoveChannelMember(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("retry after removing injector want 200 got %d: %s", rec.Code, rec.Body.String())
	}
	assertChannelAgentMembershipCount(t, context.Background(), channelID, agentID, 0)
}

func TestChannelMemberPromotionFirstSerializesAgainstRemove(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	adminID := createChannelWorkspaceAdmin(t)
	targetID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "promotion-before-remove-"+uuid.NewString(), testUserID, targetID)
	auditBefore := countAllChannelMemberSuccessActivityForTest(t, channelID)
	systemBefore := countChannelSystemMessagesForTest(t, channelID)

	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin role mutation: %v", err)
	}
	defer tx.Rollback(ctx)
	var lockedChannelID pgtype.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM channel WHERE id = $1 FOR UPDATE`, channelID).Scan(&lockedChannelID); err != nil {
		t.Fatalf("lock channel for promotion: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE channel_member
		SET role = 'manager'
		WHERE channel_id = $1 AND workspace_id = $2
		  AND member_type = 'user' AND member_id = $3`,
		channelID, testWorkspaceID, targetID); err != nil {
		t.Fatalf("promote target in held transaction: %v", err)
	}

	atomic.StoreInt32(&testMemberManagementLockAttemptEntered, 0)
	t.Cleanup(func() { atomic.StoreInt32(&testMemberManagementLockAttemptEntered, 0) })
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := newRequestAs(adminID, http.MethodDelete,
			"/api/channels/"+channelID+"/members/user/"+targetID+"?expected_remove_effect=none",
			nil)
		req = withChannelTestWorkspaceCtx(t, req, adminID)
		req = withRouteParams(req,
			"channelId", channelID,
			"memberType", "user",
			"memberId", targetID,
		)
		rec := httptest.NewRecorder()
		testHandler.RemoveChannelMember(rec, req)
		result <- rec
	}()
	waitForMemberManagementLockAttempt(t)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit target promotion: %v", err)
	}

	rec := <-result
	if rec.Code != http.StatusForbidden {
		t.Fatalf("workspace admin removing concurrently promoted manager want 403 got %d: %s", rec.Code, rec.Body.String())
	}
	assertChannelUserMembershipCount(t, channelID, targetID, 1)
	assertChannelMemberArtifactCountsUnchanged(t, channelID, auditBefore, systemBefore)
}

func TestChannelMemberOwnershipTransferFirstSerializesAgainstSelfLeave(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	nextOwnerID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "owner-transfer-before-leave-"+uuid.NewString(), testUserID, nextOwnerID)

	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin ownership transfer: %v", err)
	}
	defer tx.Rollback(ctx)
	var lockedChannelID pgtype.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM channel WHERE id = $1 FOR UPDATE`, channelID).Scan(&lockedChannelID); err != nil {
		t.Fatalf("lock channel for ownership transfer: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE channel_member
		SET role = 'manager'
		WHERE channel_id = $1 AND workspace_id = $2
		  AND member_type = 'user' AND role = 'owner'`,
		channelID, testWorkspaceID,
	); err != nil {
		t.Fatalf("demote current owner in held transaction: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE channel_member
		SET role = 'owner'
		WHERE channel_id = $1 AND workspace_id = $2
		  AND member_type = 'user' AND member_id = $3`,
		channelID, testWorkspaceID, nextOwnerID,
	); err != nil {
		t.Fatalf("transfer ownership in held transaction: %v", err)
	}

	atomic.StoreInt32(&testMemberManagementLockAttemptEntered, 0)
	t.Cleanup(func() { atomic.StoreInt32(&testMemberManagementLockAttemptEntered, 0) })
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := newRequestAs(testUserID, http.MethodDelete,
			"/api/channels/"+channelID+"/members/user/"+testUserID+"?expected_remove_effect=none",
			nil)
		req = withChannelTestWorkspaceCtx(t, req, testUserID)
		req = withRouteParams(req,
			"channelId", channelID,
			"memberType", "user",
			"memberId", testUserID,
		)
		rec := httptest.NewRecorder()
		testHandler.RemoveChannelMember(rec, req)
		result <- rec
	}()
	waitForMemberManagementLockAttempt(t)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit ownership transfer: %v", err)
	}

	rec := <-result
	if rec.Code != http.StatusOK {
		t.Fatalf("old owner leave after serialized transfer want 200 got %d: %s", rec.Code, rec.Body.String())
	}
	assertChannelUserMembershipCount(t, channelID, testUserID, 0)
	assertChannelUserMembershipCount(t, channelID, nextOwnerID, 1)
	var ownerID string
	if err := testPool.QueryRow(ctx, `
		SELECT member_id::text
		FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2
		  AND member_type = 'user' AND role = 'owner'`,
		channelID,
		testWorkspaceID,
	).Scan(&ownerID); err != nil {
		t.Fatalf("load surviving owner: %v", err)
	}
	if ownerID != nextOwnerID {
		t.Fatalf("surviving owner=%s want %s", ownerID, nextOwnerID)
	}
}

func waitForMemberManagementLockAttempt(t *testing.T) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for atomic.LoadInt32(&testMemberManagementLockAttemptEntered) < 1 {
		select {
		case <-deadline:
			t.Fatal("member-management request never entered channel lock attempt")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

type memberManagementActivityFailingTxStarter struct {
	base txStarter
}

func (s memberManagementActivityFailingTxStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.base.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &memberManagementActivityFailingTx{Tx: tx}, nil
}

type memberManagementActivityFailingTx struct {
	pgx.Tx
}

func (tx *memberManagementActivityFailingTx) Exec(
	ctx context.Context,
	sql string,
	args ...any,
) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "INSERT INTO activity_log") {
		return pgconn.CommandTag{}, errors.New("injected member-management activity failure")
	}
	return tx.Tx.Exec(ctx, sql, args...)
}
