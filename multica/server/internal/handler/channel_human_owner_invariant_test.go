package handler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Barry #1286 successor gate: final-state ordinary-group human-owner invariant.

func ownerInvariantErr(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if strings.Contains(pgErr.Message, "ordinary group must have at least one human owner") {
			return true
		}
	}
	return strings.Contains(err.Error(), "ordinary group must have at least one human owner")
}

func insertUserAndMember(t *testing.T, workspaceID string) string {
	t.Helper()
	ctx := context.Background()
	var userID string
	if err := testPool.QueryRow(ctx, `
INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`,
		"inv-user-"+uuid.NewString()[:8], "inv-user-"+uuid.NewString()[:8]+"@test.local").Scan(&userID); err != nil {
		t.Fatalf("user: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')
ON CONFLICT DO NOTHING`, workspaceID, userID); err != nil {
		t.Fatalf("member: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM member WHERE workspace_id = $1 AND user_id = $2`, workspaceID, userID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	return userID
}

// Auto-seed: bare ordinary INSERT with workspace-member created_by ends with 1 owner.
func TestHumanOwnerInvariant_BareOrdinaryGroupInsertAutoSeedsOwner(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()
	var chID string
	if err := testPool.QueryRow(ctx, `
INSERT INTO channel (workspace_id, name, created_by, kind)
VALUES ($1, $2, $3, 'group') RETURNING id`,
		testWorkspaceID, "inv-bare-ok-"+uuid.NewString()[:8], testUserID).Scan(&chID); err != nil {
		t.Fatalf("bare ordinary INSERT: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, chID) })
	var owners int
	if err := testPool.QueryRow(ctx, `
SELECT count(*) FROM channel_member
WHERE channel_id = $1 AND role = 'owner' AND member_type = 'user'`, chID).Scan(&owners); err != nil {
		t.Fatal(err)
	}
	if owners != 1 {
		t.Fatalf("auto-seed owners=%d want 1", owners)
	}
}

// Non-member created_by cannot auto-seed → deferred channel trigger fails.
func TestHumanOwnerInvariant_BareOrdinaryGroupNonMemberCreatorFails(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()
	var ghost string
	if err := testPool.QueryRow(ctx, `
INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`,
		"inv-ghost", "inv-ghost-"+uuid.NewString()[:8]+"@test.local").Scan(&ghost); err != nil {
		t.Fatalf("ghost user: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, ghost) })
	_, err := testPool.Exec(ctx, `
INSERT INTO channel (workspace_id, name, created_by, kind)
VALUES ($1, $2, $3, 'group')`, testWorkspaceID, "inv-bare-ghost-"+uuid.NewString()[:8], ghost)
	if !ownerInvariantErr(err) {
		t.Fatalf("non-member created_by want fail, got: %v", err)
	}
}

func TestHumanOwnerInvariant_DMToOrdinaryZeroOwnerFails(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()
	var chID string
	if err := testPool.QueryRow(ctx, `
INSERT INTO channel (workspace_id, name, created_by, kind)
VALUES ($1, $2, $3, 'dm') RETURNING id`,
		testWorkspaceID, "inv-dm2g-"+uuid.NewString()[:8], testUserID).Scan(&chID); err != nil {
		t.Fatalf("seed dm: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, chID) })
	_, err := testPool.Exec(ctx, `UPDATE channel SET kind = 'group', system_key = NULL WHERE id = $1`, chID)
	if !ownerInvariantErr(err) {
		t.Fatalf("dm→ordinary 0 owners want fail, got: %v", err)
	}
}

func TestHumanOwnerInvariant_MoveSoleOwnerChannelIDFails(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()
	// A ordinary group owned by testUser. B is a DM (no owner invariant) so
	// moving the owner row does not trip max-1 unique on the destination —
	// only A's final 0-owner state should fail the deferred check.
	var a, b string
	if err := testPool.QueryRow(ctx, `
INSERT INTO channel (workspace_id, name, created_by, kind)
VALUES ($1, $2, $3, 'group') RETURNING id`,
		testWorkspaceID, "inv-move-a-"+uuid.NewString()[:8], testUserID).Scan(&a); err != nil {
		t.Fatalf("a: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
INSERT INTO channel (workspace_id, name, created_by, kind)
VALUES ($1, $2, $3, 'dm') RETURNING id`,
		testWorkspaceID, "inv-move-b-"+uuid.NewString()[:8], testUserID).Scan(&b); err != nil {
		t.Fatalf("b: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = ANY($1::uuid[])`, []string{a, b})
	})

	// Move sole owner of A onto B.
	_, err := testPool.Exec(ctx, `
UPDATE channel_member SET channel_id = $1
WHERE channel_id = $2 AND member_type = 'user' AND member_id = $3`, b, a, testUserID)
	if !ownerInvariantErr(err) {
		t.Fatalf("move sole owner want fail, got: %v", err)
	}
	var ownersA int
	if err := testPool.QueryRow(ctx, `
SELECT count(*) FROM channel_member
WHERE channel_id = $1 AND role = 'owner' AND member_type = 'user'`, a).Scan(&ownersA); err != nil {
		t.Fatal(err)
	}
	if ownersA != 1 {
		t.Fatalf("source owners after failed move = %d want 1", ownersA)
	}
}

func TestHumanOwnerInvariant_AtomicTransferOK(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()
	other := insertUserAndMember(t, testWorkspaceID)
	var chID string
	if err := testPool.QueryRow(ctx, `
INSERT INTO channel (workspace_id, name, created_by, kind)
VALUES ($1, $2, $3, 'group') RETURNING id`,
		testWorkspaceID, "inv-xfer-"+uuid.NewString()[:8], testUserID).Scan(&chID); err != nil {
		t.Fatalf("channel: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, chID) })
	// Add other as member (not owner).
	if _, err := testPool.Exec(ctx, `
INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
VALUES ($1, $2, 'user', $3, 'member')
ON CONFLICT DO NOTHING`, chID, testWorkspaceID, other); err != nil {
		t.Fatalf("add member: %v", err)
	}

	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
UPDATE channel_member SET role = 'member'
WHERE channel_id = $1 AND member_type = 'user' AND member_id = $2`, chID, testUserID); err != nil {
		t.Fatalf("demote: %v", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE channel_member SET role = 'owner'
WHERE channel_id = $1 AND member_type = 'user' AND member_id = $2`, chID, other); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("transfer commit: %v", err)
	}
	var owners int
	var ownerID string
	if err := testPool.QueryRow(ctx, `
SELECT count(*), min(member_id::text)
FROM channel_member
WHERE channel_id = $1 AND role = 'owner' AND member_type = 'user'`, chID).Scan(&owners, &ownerID); err != nil {
		t.Fatal(err)
	}
	if owners != 1 || ownerID != other {
		t.Fatalf("after transfer owners=%d owner=%s want 1/%s", owners, ownerID, other)
	}
}

func TestHumanOwnerInvariant_DeleteChannelCascadeOK(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()
	var chID string
	if err := testPool.QueryRow(ctx, `
INSERT INTO channel (workspace_id, name, created_by, kind)
VALUES ($1, $2, $3, 'group') RETURNING id`,
		testWorkspaceID, "inv-delch-"+uuid.NewString()[:8], testUserID).Scan(&chID); err != nil {
		t.Fatalf("channel: %v", err)
	}
	if _, err := testPool.Exec(ctx, `DELETE FROM channel WHERE id = $1`, chID); err != nil {
		t.Fatalf("DELETE channel cascade want OK, got: %v", err)
	}
}

func TestHumanOwnerInvariant_SoleDemoteFails(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()
	var chID string
	if err := testPool.QueryRow(ctx, `
INSERT INTO channel (workspace_id, name, created_by, kind)
VALUES ($1, $2, $3, 'group') RETURNING id`,
		testWorkspaceID, "inv-demote-"+uuid.NewString()[:8], testUserID).Scan(&chID); err != nil {
		t.Fatalf("channel: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, chID) })
	_, err := testPool.Exec(ctx, `
UPDATE channel_member SET role = 'member'
WHERE channel_id = $1 AND member_type = 'user' AND member_id = $2`, chID, testUserID)
	if !ownerInvariantErr(err) {
		t.Fatalf("sole demote want fail, got: %v", err)
	}
}

func TestHumanOwnerInvariant_DeleteWorkspaceCascadeOK(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()
	var wsID, userID string
	suffix := uuid.NewString()[:8]
	if err := testPool.QueryRow(ctx, `
INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`,
		"inv-ws-user-"+suffix, fmt.Sprintf("inv-ws-%s@test.local", suffix)).Scan(&userID); err != nil {
		t.Fatalf("user: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
INSERT INTO workspace (name, slug, description, issue_prefix)
VALUES ($1, $2, 'inv delete workspace', 'INV') RETURNING id`,
		"inv-ws-"+uuid.NewString()[:8], "inv-ws-"+uuid.NewString()[:8]).Scan(&wsID); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, wsID, userID); err != nil {
		t.Fatalf("ws member: %v", err)
	}
	var chID string
	if err := testPool.QueryRow(ctx, `
INSERT INTO channel (workspace_id, name, created_by, kind)
VALUES ($1, $2, $3, 'group') RETURNING id`,
		wsID, "inv-delws-group", userID).Scan(&chID); err != nil {
		t.Fatalf("channel: %v", err)
	}
	if _, err := testPool.Exec(ctx, `DELETE FROM workspace WHERE id = $1`, wsID); err != nil {
		t.Fatalf("DELETE workspace cascade want OK, got: %v", err)
	}
	_, _ = testPool.Exec(ctx, `DELETE FROM "user" WHERE id = $1`, userID)
	_ = chID
}

func TestHumanOwnerInvariant_ChannelWorkspaceUnilateralChangeFails(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()
	// Ordinary group in fixture workspace with auto-seeded owner.
	var chID string
	if err := testPool.QueryRow(ctx, `
INSERT INTO channel (workspace_id, name, created_by, kind)
VALUES ($1, $2, $3, 'group') RETURNING id`,
		testWorkspaceID, "inv-ws-ch-"+uuid.NewString()[:8], testUserID).Scan(&chID); err != nil {
		t.Fatalf("channel: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, chID) })

	// Isolated target workspace; owner is NOT a member there.
	var otherWS string
	suffix := uuid.NewString()[:8]
	if err := testPool.QueryRow(ctx, `
INSERT INTO workspace (name, slug, description, issue_prefix)
VALUES ($1, $2, 'inv ws move', 'IWM') RETURNING id`,
		"inv-ws-"+suffix, "inv-ws-"+suffix).Scan(&otherWS); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, otherWS) })

	_, err := testPool.Exec(ctx, `UPDATE channel SET workspace_id = $1 WHERE id = $2`, otherWS, chID)
	if !ownerInvariantErr(err) {
		t.Fatalf("channel workspace unilateral change want fail, got: %v", err)
	}
}

func TestHumanOwnerInvariant_OwnerRowWorkspaceUnilateralChangeFails(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()
	var chID string
	if err := testPool.QueryRow(ctx, `
INSERT INTO channel (workspace_id, name, created_by, kind)
VALUES ($1, $2, $3, 'group') RETURNING id`,
		testWorkspaceID, "inv-ws-row-"+uuid.NewString()[:8], testUserID).Scan(&chID); err != nil {
		t.Fatalf("channel: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, chID) })

	var otherWS string
	suffix := uuid.NewString()[:8]
	if err := testPool.QueryRow(ctx, `
INSERT INTO workspace (name, slug, description, issue_prefix)
VALUES ($1, $2, 'inv row move', 'IRM') RETURNING id`,
		"inv-row-"+suffix, "inv-row-"+suffix).Scan(&otherWS); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, otherWS) })

	_, err := testPool.Exec(ctx, `
UPDATE channel_member SET workspace_id = $1
WHERE channel_id = $2 AND member_type = 'user' AND member_id = $3`,
		otherWS, chID, testUserID)
	// Migration 245 may reject this malformed intermediate row before the
	// deferred owner invariant: moving workspace_id alone also makes the row's
	// actor provenance cross-workspace. Either constraint must block the same
	// forbidden unilateral change.
	if !ownerInvariantErr(err) &&
		(err == nil || !strings.Contains(err.Error(), "channel actor provenance must reference an existing same-workspace actor")) {
		t.Fatalf("owner-row workspace unilateral change want fail, got: %v", err)
	}
}

func TestHumanOwnerInvariant_BilateralWorkspaceMoveOK(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()
	// Owner must be member of both workspaces for a legal bilateral move.
	var otherWS string
	suffix := uuid.NewString()[:8]
	if err := testPool.QueryRow(ctx, `
INSERT INTO workspace (name, slug, description, issue_prefix)
VALUES ($1, $2, 'inv bilateral', 'IBL') RETURNING id`,
		"inv-bi-"+suffix, "inv-bi-"+suffix).Scan(&otherWS); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')
ON CONFLICT DO NOTHING`, otherWS, testUserID); err != nil {
		t.Fatalf("member other ws: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, otherWS)
	})

	var chID string
	if err := testPool.QueryRow(ctx, `
INSERT INTO channel (workspace_id, name, created_by, kind)
VALUES ($1, $2, $3, 'group') RETURNING id`,
		testWorkspaceID, "inv-bi-ch-"+uuid.NewString()[:8], testUserID).Scan(&chID); err != nil {
		t.Fatalf("channel: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, chID) })

	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE channel SET workspace_id = $1 WHERE id = $2`, otherWS, chID); err != nil {
		t.Fatalf("channel ws: %v", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE channel_member SET workspace_id = $1 WHERE channel_id = $2`, otherWS, chID); err != nil {
		t.Fatalf("member ws: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("bilateral move commit want OK, got: %v", err)
	}

	var owners int
	if err := testPool.QueryRow(ctx, `
SELECT count(*) FROM channel c
JOIN channel_member cm ON cm.channel_id = c.id AND cm.workspace_id = c.workspace_id
  AND cm.role = 'owner' AND cm.member_type = 'user'
JOIN member m ON m.workspace_id = c.workspace_id AND m.user_id = cm.member_id
WHERE c.id = $1`, chID).Scan(&owners); err != nil {
		t.Fatal(err)
	}
	if owners != 1 {
		t.Fatalf("after bilateral move eligible owners=%d want 1", owners)
	}
}

func TestHumanOwnerInvariant_WorkspaceMemberRemovalGhostSoleOwnerFails(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()
	// Dedicated user as sole owner so we can remove their workspace membership.
	owner := insertUserAndMember(t, testWorkspaceID)
	var chID string
	if err := testPool.QueryRow(ctx, `
INSERT INTO channel (workspace_id, name, created_by, kind)
VALUES ($1, $2, $3, 'group') RETURNING id`,
		testWorkspaceID, "inv-ghost-"+uuid.NewString()[:8], owner).Scan(&chID); err != nil {
		t.Fatalf("channel: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, chID) })

	_, err := testPool.Exec(ctx, `
DELETE FROM member WHERE workspace_id = $1 AND user_id = $2`, testWorkspaceID, owner)
	if !ownerInvariantErr(err) {
		t.Fatalf("remove sole owner workspace membership want fail, got: %v", err)
	}
}

// TestEligibleOwnerAuditRepairs239WindowMismatch: migration 240/241 full-table
// repair must fix channel/owner workspace mismatch left from the 239 window.
func TestEligibleOwnerAuditRepairs239WindowMismatch(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()

	// Isolated workspace B; owner is only a member of A (fixture).
	var wsB string
	suffix := uuid.NewString()[:8]
	if err := testPool.QueryRow(ctx, `
INSERT INTO workspace (name, slug, description, issue_prefix)
VALUES ($1, $2, 'audit repair', 'AR') RETURNING id`,
		"audit-b-"+suffix, "audit-b-"+suffix).Scan(&wsB); err != nil {
		t.Fatalf("wsB: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, wsB) })

	// Create ordinary group in A with auto-seed owner (eligible).
	var chID string
	if err := testPool.QueryRow(ctx, `
INSERT INTO channel (workspace_id, name, created_by, kind)
VALUES ($1, $2, $3, 'group') RETURNING id`,
		testWorkspaceID, "audit-mismatch-"+suffix, testUserID).Scan(&chID); err != nil {
		t.Fatalf("channel: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, chID) })

	// Bypass triggers to recreate 239-window damage: channel moved to B, owner row still A.
	if _, err := testPool.Exec(ctx, `SET session_replication_role = replica`); err != nil {
		t.Fatalf("replica role: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE channel SET workspace_id = $1 WHERE id = $2`, wsB, chID); err != nil {
		_, _ = testPool.Exec(ctx, `SET session_replication_role = DEFAULT`)
		t.Fatalf("force channel ws: %v", err)
	}
	if _, err := testPool.Exec(ctx, `SET session_replication_role = DEFAULT`); err != nil {
		t.Fatalf("restore role: %v", err)
	}

	var eligibleBefore int
	_ = testPool.QueryRow(ctx, `
SELECT count(*) FROM channel c
JOIN channel_member cm ON cm.channel_id = c.id AND cm.workspace_id = c.workspace_id
  AND cm.role = 'owner' AND cm.member_type = 'user'
JOIN member m ON m.workspace_id = c.workspace_id AND m.user_id = cm.member_id
WHERE c.id = $1`, chID).Scan(&eligibleBefore)
	if eligibleBefore != 0 {
		t.Fatalf("setup: expected 0 eligible owners before repair, got %d", eligibleBefore)
	}

	// Owner is not a member of B → repair cannot align workspace; must insert/promote fails
	// unless we also seed owner into B. Seed owner into B so align-workspace repair works.
	if _, err := testPool.Exec(ctx, `
INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')
ON CONFLICT DO NOTHING`, wsB, testUserID); err != nil {
		t.Fatalf("seed owner into B: %v", err)
	}

	// Run the same repair steps as migration 240/241 (workspace align path).
	if _, err := testPool.Exec(ctx, `
UPDATE channel_member cm
SET workspace_id = c.workspace_id
FROM channel c
WHERE cm.channel_id = c.id
  AND c.id = $1
  AND c.kind = 'group' AND c.system_key IS NULL
  AND cm.role = 'owner' AND cm.member_type = 'user'
  AND cm.workspace_id IS DISTINCT FROM c.workspace_id
  AND EXISTS (
    SELECT 1 FROM member m
    WHERE m.workspace_id = c.workspace_id AND m.user_id = cm.member_id
  )`, chID); err != nil {
		t.Fatalf("repair align: %v", err)
	}

	var eligibleAfter int
	if err := testPool.QueryRow(ctx, `
SELECT count(*) FROM channel c
JOIN channel_member cm ON cm.channel_id = c.id AND cm.workspace_id = c.workspace_id
  AND cm.role = 'owner' AND cm.member_type = 'user'
JOIN member m ON m.workspace_id = c.workspace_id AND m.user_id = cm.member_id
WHERE c.id = $1`, chID).Scan(&eligibleAfter); err != nil {
		t.Fatal(err)
	}
	if eligibleAfter != 1 {
		t.Fatalf("after repair eligible owners=%d want 1", eligibleAfter)
	}
}

// TestEligibleOwnerAuditFailsWhenUnrepairable documents fail-closed when no
// workspace member can become owner (migration 240 would RAISE).
func TestEligibleOwnerAuditFailsWhenUnrepairable(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test DB not configured")
	}
	ctx := context.Background()
	var wsB string
	suffix := uuid.NewString()[:8]
	if err := testPool.QueryRow(ctx, `
INSERT INTO workspace (name, slug, description, issue_prefix)
VALUES ($1, $2, 'audit fail', 'AF') RETURNING id`,
		"audit-fail-"+suffix, "audit-fail-"+suffix).Scan(&wsB); err != nil {
		t.Fatalf("wsB: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, wsB) })

	var chID string
	if err := testPool.QueryRow(ctx, `
INSERT INTO channel (workspace_id, name, created_by, kind)
VALUES ($1, $2, $3, 'group') RETURNING id`,
		testWorkspaceID, "audit-unfix-"+suffix, testUserID).Scan(&chID); err != nil {
		t.Fatalf("channel: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, chID) })

	if _, err := testPool.Exec(ctx, `SET session_replication_role = replica`); err != nil {
		t.Fatal(err)
	}
	// Move channel to B; owner remains A and is NOT member of B; no created_by membership on B.
	if _, err := testPool.Exec(ctx, `UPDATE channel SET workspace_id = $1, created_by = $2 WHERE id = $3`,
		wsB, testUserID, chID); err != nil {
		_, _ = testPool.Exec(ctx, `SET session_replication_role = DEFAULT`)
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `SET session_replication_role = DEFAULT`); err != nil {
		t.Fatal(err)
	}

	// Simulate migration final check: eligible count must be 0 and unrepairable.
	var eligible int
	_ = testPool.QueryRow(ctx, `
SELECT count(*) FROM channel c
JOIN channel_member cm ON cm.channel_id = c.id AND cm.workspace_id = c.workspace_id
  AND cm.role = 'owner' AND cm.member_type = 'user'
JOIN member m ON m.workspace_id = c.workspace_id AND m.user_id = cm.member_id
WHERE c.id = $1`, chID).Scan(&eligible)
	if eligible != 0 {
		t.Fatalf("expected unrepairable 0 eligible, got %d", eligible)
	}
	// Align path no-ops without B membership
	tag, err := testPool.Exec(ctx, `
UPDATE channel_member cm
SET workspace_id = c.workspace_id
FROM channel c
WHERE cm.channel_id = c.id AND c.id = $1
  AND cm.role = 'owner' AND cm.member_type = 'user'
  AND EXISTS (SELECT 1 FROM member m WHERE m.workspace_id = c.workspace_id AND m.user_id = cm.member_id)`, chID)
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() != 0 {
		t.Fatalf("align should not fix without membership, rows=%d", tag.RowsAffected())
	}
}
