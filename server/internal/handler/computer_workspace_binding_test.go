package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func createBindingTestWorkspace(t *testing.T, userID, role string) string {
	t.Helper()
	ctx := context.Background()
	marker := uuid.NewString()
	var workspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, '', $3)
		RETURNING id
	`, "Binding test "+marker, "binding-test-"+marker, "B"+marker[:5]).Scan(&workspaceID); err != nil {
		t.Fatalf("create Binding test workspace: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, $3)
	`, workspaceID, userID, role); err != nil {
		t.Fatalf("add Binding test member: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id=$1`, workspaceID)
	})
	return workspaceID
}

func createComputerWorkspaceBindingForTest(t *testing.T, userID, computerID, workspaceID string) *httptest.ResponseRecorder {
	t.Helper()
	req := newRequestAs(userID, http.MethodPost, "/api/computers/"+computerID+"/workspace-connections", map[string]any{
		"workspace_id": workspaceID,
	})
	req.SetPathValue("daemonId", computerID)
	w := httptest.NewRecorder()
	testHandler.CreateComputerWorkspaceBinding(w, req)
	return w
}

func TestComputerWorkspaceBinding_OneOwnerCanConnectMultipleWorkspaces(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	computerID := "binding-multi-workspace-" + uuid.NewString()
	siblingWorkspaceID := createBindingTestWorkspace(t, testUserID, "owner")
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_workspace_bindings WHERE daemon_id=$1`, computerID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_identity_owner WHERE daemon_id=$1`, computerID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM daemon_token WHERE daemon_id=$1`, computerID)
	})

	for _, workspaceID := range []string{testWorkspaceID, siblingWorkspaceID} {
		w := createComputerWorkspaceBindingForTest(t, testUserID, computerID, workspaceID)
		if w.Code != http.StatusOK {
			t.Fatalf("connect workspace %s: got %d: %s", workspaceID, w.Code, w.Body.String())
		}
	}

	var owners, activeConnections int
	if err := testPool.QueryRow(context.Background(), `
		SELECT
		  (SELECT count(*) FROM computer_identity_owner WHERE daemon_id=$1),
		  (SELECT count(*) FROM computer_workspace_bindings WHERE daemon_id=$1 AND active)
	`, computerID).Scan(&owners, &activeConnections); err != nil {
		t.Fatal(err)
	}
	if owners != 1 || activeConnections != 2 {
		t.Fatalf("owners/connections = %d/%d, want 1/2", owners, activeConnections)
	}

}

func TestComputerWorkspaceBinding_ExplicitReconnectClearsDeletionFence(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	computerID := "binding-reconnect-" + uuid.NewString()
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM daemon_registration_tombstone WHERE workspace_id=$1 AND daemon_id=lower($2)`, testWorkspaceID, computerID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_workspace_bindings WHERE daemon_id=$1`, computerID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_identity_owner WHERE daemon_id=$1`, computerID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM daemon_token WHERE daemon_id=$1`, computerID)
	})

	if _, err := testPool.Exec(ctx, `
		INSERT INTO daemon_registration_tombstone (workspace_id, daemon_id, removed_by)
		VALUES ($1, lower($2), $3)
	`, testWorkspaceID, computerID, testUserID); err != nil {
		t.Fatalf("insert deletion fence: %v", err)
	}

	w := createComputerWorkspaceBindingForTest(t, testUserID, computerID, testWorkspaceID)
	if w.Code != http.StatusOK {
		t.Fatalf("reconnect Computer: got %d: %s", w.Code, w.Body.String())
	}

	var tombstones int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM daemon_registration_tombstone
		WHERE workspace_id=$1 AND daemon_id=lower($2)
	`, testWorkspaceID, computerID).Scan(&tombstones); err != nil {
		t.Fatal(err)
	}
	if tombstones != 0 {
		t.Fatalf("deletion fences after explicit reconnect = %d, want 0", tombstones)
	}
}

func TestComputerWorkspaceBinding_RejectsCrossUserComputerTakeover(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	computerID := "binding-owner-fence-" + uuid.NewString()
	foreignEmail := "binding-owner-fence-" + uuid.NewString() + "@multica.ai"
	var foreignUserID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ('Binding foreign user', $1) RETURNING id
	`, foreignEmail).Scan(&foreignUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'member')
	`, testWorkspaceID, foreignUserID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_workspace_bindings WHERE daemon_id=$1`, computerID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_identity_owner WHERE daemon_id=$1`, computerID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM daemon_token WHERE daemon_id=$1`, computerID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id=$1`, foreignUserID)
	})
	foreignWorkspaceID := createBindingTestWorkspace(t, foreignUserID, "owner")

	if w := createComputerWorkspaceBindingForTest(t, testUserID, computerID, testWorkspaceID); w.Code != http.StatusOK {
		t.Fatalf("establish original owner: got %d: %s", w.Code, w.Body.String())
	}
	if w := createComputerWorkspaceBindingForTest(t, foreignUserID, computerID, foreignWorkspaceID); w.Code != http.StatusForbidden {
		t.Fatalf("cross-user takeover: got %d, want 403: %s", w.Code, w.Body.String())
	}

	deleteReq := newRequestAs(foreignUserID, http.MethodDelete, "/api/computers/"+computerID, nil)
	deleteReq = withURLParam(deleteReq, "daemonId", computerID)
	deleteResponse := httptest.NewRecorder()
	testHandler.DeleteComputer(deleteResponse, deleteReq)
	if deleteResponse.Code != http.StatusForbidden {
		t.Fatalf("cross-user delete: got %d, want 403: %s", deleteResponse.Code, deleteResponse.Body.String())
	}

	var ownerUserID string
	if err := testPool.QueryRow(ctx, `SELECT user_id FROM computer_identity_owner WHERE daemon_id=$1`, computerID).Scan(&ownerUserID); err != nil {
		t.Fatal(err)
	}
	if ownerUserID != testUserID {
		t.Fatalf("Computer owner = %s, want %s", ownerUserID, testUserID)
	}
	var originalActive, foreignConnections, foreignTokens int
	if err := testPool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM computer_workspace_bindings WHERE daemon_id=$1 AND user_id=$4 AND active),
		  (SELECT count(*) FROM computer_workspace_bindings WHERE daemon_id=$1 AND user_id=$2),
		  (SELECT count(*) FROM daemon_token WHERE daemon_id=$1 AND workspace_id=$3)
	`, computerID, foreignUserID, foreignWorkspaceID, testUserID).Scan(&originalActive, &foreignConnections, &foreignTokens); err != nil {
		t.Fatal(err)
	}
	if originalActive != 1 || foreignConnections != 0 || foreignTokens != 0 {
		t.Fatalf("rejected takeover left original/connections/tokens = %d/%d/%d, want 1/0/0", originalActive, foreignConnections, foreignTokens)
	}
}

func TestMembershipLossRevokesZeroRuntimeConnectionAndPreservesSibling(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	computerID := "binding-membership-loss-" + uuid.NewString()
	email := "binding-membership-loss-" + uuid.NewString() + "@multica.ai"
	var userID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ('Binding membership-loss user', $1) RETURNING id
	`, email).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_workspace_bindings WHERE daemon_id=$1`, computerID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_identity_owner WHERE daemon_id=$1`, computerID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM daemon_token WHERE daemon_id=$1`, computerID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id=$1`, userID)
	})
	revokedWorkspaceID := createBindingTestWorkspace(t, testUserID, "owner")
	siblingWorkspaceID := createBindingTestWorkspace(t, testUserID, "owner")
	for _, workspaceID := range []string{revokedWorkspaceID, siblingWorkspaceID} {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'admin')
		`, workspaceID, userID); err != nil {
			t.Fatalf("add membership to workspace %s: %v", workspaceID, err)
		}
	}

	for _, workspaceID := range []string{revokedWorkspaceID, siblingWorkspaceID} {
		if w := createComputerWorkspaceBindingForTest(t, userID, computerID, workspaceID); w.Code != http.StatusOK {
			t.Fatalf("connect workspace %s: got %d: %s", workspaceID, w.Code, w.Body.String())
		}
	}

	var memberID string
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM member WHERE workspace_id=$1 AND user_id=$2
	`, revokedWorkspaceID, userID).Scan(&memberID); err != nil {
		t.Fatal(err)
	}
	result, err := testHandler.revokeAndRemoveMember(
		ctx,
		parseUUID(revokedWorkspaceID),
		parseUUID(userID),
		parseUUID(memberID),
		parseUUID(userID),
	)
	if err != nil {
		t.Fatalf("revoke membership: %v", err)
	}
	if result.RevokedBindings != 1 || len(result.Runtimes) != 0 {
		t.Fatalf("revoked bindings/runtimes = %d/%d, want 1/0", result.RevokedBindings, len(result.Runtimes))
	}

	var revokedActive, siblingActive, revokedTokens, memberCount int
	if err := testPool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM computer_workspace_bindings WHERE daemon_id=$1 AND workspace_id=$2 AND active),
		  (SELECT count(*) FROM computer_workspace_bindings WHERE daemon_id=$1 AND workspace_id=$3 AND active),
		  (SELECT count(*) FROM daemon_token WHERE daemon_id=$1 AND workspace_id=$2),
		  (SELECT count(*) FROM member WHERE workspace_id=$2 AND user_id=$4)
	`, computerID, revokedWorkspaceID, siblingWorkspaceID, userID).Scan(
		&revokedActive, &siblingActive, &revokedTokens, &memberCount,
	); err != nil {
		t.Fatal(err)
	}
	if revokedActive != 0 || siblingActive != 1 || revokedTokens != 0 || memberCount != 0 {
		t.Fatalf(
			"revoked/sibling/tokens/member = %d/%d/%d/%d, want 0/1/0/0",
			revokedActive, siblingActive, revokedTokens, memberCount,
		)
	}
}

func TestComputerHeartbeat_FencesGenerationAfterConnectionAuthorization(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	computerID := "computer-generation-fence-" + uuid.NewString()
	if w := createComputerWorkspaceBindingForTest(t, testUserID, computerID, testWorkspaceID); w.Code != http.StatusOK {
		t.Fatalf("establish connection: got %d: %s", w.Code, w.Body.String())
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_generation WHERE daemon_id=$1`, computerID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM daemon_heartbeat WHERE daemon_id=$1`, computerID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_workspace_bindings WHERE daemon_id=$1`, computerID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_identity_owner WHERE daemon_id=$1`, computerID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM daemon_token WHERE daemon_id=$1`, computerID)
	})

	heartbeat := func(userID string, generation int64) *httptest.ResponseRecorder {
		req := newRequestAs(userID, http.MethodPost, "/api/daemon/computer/heartbeat", map[string]any{
			"daemon_id": computerID, "workspace_id": testWorkspaceID, "generation": generation,
		})
		w := httptest.NewRecorder()
		testHandler.ComputerHeartbeat(w, req)
		return w
	}

	if w := heartbeat(testUserID, 2); w.Code != http.StatusOK {
		t.Fatalf("claim generation 2: got %d: %s", w.Code, w.Body.String())
	}
	if w := heartbeat(testUserID, 1); w.Code != http.StatusConflict {
		t.Fatalf("stale generation: got %d, want 409: %s", w.Code, w.Body.String())
	}

	foreignEmail := "computer-generation-" + uuid.NewString() + "@multica.ai"
	var foreignUserID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ('Generation foreign user', $1) RETURNING id
	`, foreignEmail).Scan(&foreignUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')
	`, testWorkspaceID, foreignUserID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id=$1`, foreignUserID)
	})
	if w := heartbeat(foreignUserID, 3); w.Code != http.StatusForbidden {
		t.Fatalf("unconnected member generation claim: got %d, want 403: %s", w.Code, w.Body.String())
	}

	var generation int64
	if err := testPool.QueryRow(ctx, `SELECT generation FROM computer_generation WHERE daemon_id=$1`, computerID).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	if generation != 2 {
		t.Fatalf("generation after rejected claim = %d, want 2", generation)
	}
}
