package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func createWorkspaceOwnerInvariantMember(t *testing.T, role string) (userID, memberID string) {
	t.Helper()
	ctx := context.Background()
	email := "owner-invariant-" + uuid.NewString() + "@multica.test"
	if err := testPool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ('Owner invariant', $1) RETURNING id`, email).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := testPool.QueryRow(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, $3) RETURNING id`, testWorkspaceID, userID, role).Scan(&memberID); err != nil {
		t.Fatalf("create member: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID) })
	return userID, memberID
}

func TestWorkspaceOwnerInvariant_DatabaseRejectsSecondOwner(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	userID, memberID := createWorkspaceOwnerInvariantMember(t, "member")
	_, err := testPool.Exec(context.Background(), `UPDATE member SET role = 'owner' WHERE id = $1`, memberID)
	if err == nil {
		_, _ = testPool.Exec(context.Background(), `UPDATE member SET role = 'member' WHERE id = $1`, memberID)
		t.Fatalf("database accepted a second owner user %s", userID)
	}
}

func TestUpdateMember_RejectsOwnerPromotionWithStableConflict(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	_, memberID := createWorkspaceOwnerInvariantMember(t, "member")
	req := withRouteParams(
		newRequest(http.MethodPatch, "/api/workspaces/"+testWorkspaceID+"/members/"+memberID, map[string]string{"role": "owner"}),
		"id", testWorkspaceID,
		"memberId", memberID,
	)
	rec := httptest.NewRecorder()
	testHandler.UpdateMember(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("owner promotion status=%d body=%s, want 409", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["code"] != "workspace_owner_immutable" {
		t.Fatalf("owner promotion body=%v err=%v", body, err)
	}
}

func TestWorkspaceOwnerLifecycle_IsImmutable(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	var ownerMemberID string
	if err := testPool.QueryRow(context.Background(), `SELECT id FROM member WHERE workspace_id = $1 AND user_id = $2`, testWorkspaceID, testUserID).Scan(&ownerMemberID); err != nil {
		t.Fatalf("load owner member: %v", err)
	}

	t.Run("demote", func(t *testing.T) {
		req := withRouteParams(newRequest(http.MethodPatch, "/api/workspaces/x/members/x", map[string]string{"role": "admin"}), "id", testWorkspaceID, "memberId", ownerMemberID)
		rec := httptest.NewRecorder()
		testHandler.UpdateMember(rec, req)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("remove", func(t *testing.T) {
		req := withRouteParams(newRequest(http.MethodDelete, "/api/workspaces/x/members/x", nil), "id", testWorkspaceID, "memberId", ownerMemberID)
		rec := httptest.NewRecorder()
		testHandler.DeleteMember(rec, req)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("leave", func(t *testing.T) {
		req := withURLParam(newRequest(http.MethodDelete, "/api/workspaces/x/leave", nil), "id", testWorkspaceID)
		rec := httptest.NewRecorder()
		testHandler.LeaveWorkspace(rec, req)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})
}
