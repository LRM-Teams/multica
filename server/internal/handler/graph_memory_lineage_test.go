package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/service"
)

func TestGetGraphMemoryChannelLineage(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("test database unavailable")
	}
	ctx := context.Background()
	workspaceID := createGraphMemoryTestWorkspace(t)
	// An owner must exist before the plain member joins (migration 301), and
	// channel creation auto-seeds the creator as group owner only when the
	// creator is a workspace member (migration 237).
	mustGraphMemoryWorkspaceOwner(t, workspaceID)
	mustGraphMemoryMember(t, workspaceID, "member")
	channelID := createGraphMemoryTestChannel(t, workspaceID)
	// No route yet: stable empty answer, not an error.
	req := withRouteParams(newRequest(http.MethodGet,
		"/api/workspaces/"+workspaceID.String()+"/graph-memory/channels/"+channelID.String()+"/lineage", nil),
		"id", workspaceID.String(), "channelId", channelID.String())
	rec := httptest.NewRecorder()
	testHandler.GetGraphMemoryChannelLineage(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"lineage":[]`) {
		t.Fatalf("empty lineage: status=%d body=%s", rec.Code, rec.Body.String())
	}
	// After resolution the current route and generation appear.
	if _, err := service.ResolveChannelRoute(ctx, testPool, workspaceID.String(), channelID.String()); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	testHandler.GetGraphMemoryChannelLineage(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"routing_mode":"standalone"`) {
		t.Fatalf("resolved lineage: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetGraphMemoryChannelLineagePrivateChannelAuthorization(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("test database unavailable")
	}
	ctx := context.Background()
	workspaceID := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryWorkspaceOwner(t, workspaceID)
	mustGraphMemoryMember(t, workspaceID, "member")
	channelID := createGraphMemoryTestChannel(t, workspaceID)
	if _, err := service.ResolveChannelRoute(ctx, testPool, workspaceID.String(), channelID.String()); err != nil {
		t.Fatal(err)
	}

	nonMemberID := createGraphMemoryLineagePrincipal(t, workspaceID.String(), "member")
	channelMemberID := createGraphMemoryLineagePrincipal(t, workspaceID.String(), "member")
	adminID := createGraphMemoryLineagePrincipal(t, workspaceID.String(), "admin")
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'user', $3)`, channelID, workspaceID, channelMemberID); err != nil {
		t.Fatalf("add channel member: %v", err)
	}

	unknownID := uuid.NewString()
	unknownRec := graphMemoryLineageResponseFor(t, nonMemberID, workspaceID.String(), unknownID)
	if unknownRec.Code != http.StatusNotFound {
		t.Fatalf("unknown channel status=%d body=%s", unknownRec.Code, unknownRec.Body.String())
	}
	deniedRec := graphMemoryLineageResponseFor(t, nonMemberID, workspaceID.String(), channelID.String())
	if deniedRec.Code != unknownRec.Code || deniedRec.Body.String() != unknownRec.Body.String() {
		t.Fatalf("private non-member response=%d %s, want unknown-channel response=%d %s", deniedRec.Code, deniedRec.Body.String(), unknownRec.Code, unknownRec.Body.String())
	}

	memberRec := graphMemoryLineageResponseFor(t, channelMemberID, workspaceID.String(), channelID.String())
	if memberRec.Code != http.StatusOK || !strings.Contains(memberRec.Body.String(), `"lineage"`) {
		t.Fatalf("private channel member status=%d body=%s", memberRec.Code, memberRec.Body.String())
	}
	adminRec := graphMemoryLineageResponseFor(t, adminID, workspaceID.String(), channelID.String())
	if adminRec.Code != http.StatusOK || !strings.Contains(adminRec.Body.String(), `"lineage"`) {
		t.Fatalf("private workspace admin status=%d body=%s", adminRec.Code, adminRec.Body.String())
	}
}

func TestGetGraphMemoryChannelLineagePublicChannelAllowsWorkspaceNonMember(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("test database unavailable")
	}
	ctx := context.Background()
	workspaceID := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryWorkspaceOwner(t, workspaceID)
	mustGraphMemoryMember(t, workspaceID, "member")

	var channelID string
	if err := testPool.QueryRow(ctx, `
		SELECT id::text FROM channel
		WHERE workspace_id = $1 AND system_key = 'general'`, workspaceID).Scan(&channelID); err != nil {
		t.Fatalf("load public general channel: %v", err)
	}
	if _, err := service.ResolveChannelRoute(ctx, testPool, workspaceID.String(), channelID); err != nil {
		t.Fatal(err)
	}
	workspaceMemberID := createGraphMemoryLineagePrincipal(t, workspaceID.String(), "member")
	rec := graphMemoryLineageResponseFor(t, workspaceMemberID, workspaceID.String(), channelID)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"lineage"`) {
		t.Fatalf("public workspace member status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetGraphMemoryChannelLineageRejectsCrossTenantAndKindMismatches(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("test database unavailable")
	}
	ctx := context.Background()
	workspaceA := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryWorkspaceOwner(t, workspaceA)
	mustGraphMemoryMember(t, workspaceA, "member")
	channelID := createGraphMemoryTestChannel(t, workspaceA)
	if _, err := service.ResolveChannelRoute(ctx, testPool, workspaceA.String(), channelID.String()); err != nil {
		t.Fatal(err)
	}
	workspaceB := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryWorkspaceOwner(t, workspaceB)
	mustGraphMemoryMember(t, workspaceB, "member")

	crossTenantRec := graphMemoryLineageResponseFor(t, testUserID, workspaceB.String(), channelID.String())
	if crossTenantRec.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant channel lineage status=%d body=%s", crossTenantRec.Code, crossTenantRec.Body.String())
	}

	var projectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title) VALUES ($1, $2) RETURNING id::text`,
		workspaceA, "graph-memory-lineage-kind-mismatch-"+uuid.NewString()).Scan(&projectID); err != nil {
		t.Fatalf("create project graph owner: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE graph_memory_channel_route
		SET current_graph_kind = 'channel', current_graph_owner_id = $3
		WHERE workspace_id = $1 AND channel_id = $2`, workspaceA, channelID, projectID); err != nil {
		t.Fatalf("seed mismatched route: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE graph_memory_channel_lineage
		SET graph_kind = 'channel', graph_owner_id = $3
		WHERE workspace_id = $1 AND channel_id = $2`, workspaceA, channelID, projectID); err != nil {
		t.Fatalf("seed mismatched lineage: %v", err)
	}
	kindMismatchRec := graphMemoryLineageResponseFor(t, testUserID, workspaceA.String(), channelID.String())
	if kindMismatchRec.Code != http.StatusNotFound {
		t.Fatalf("kind-mismatched lineage status=%d body=%s", kindMismatchRec.Code, kindMismatchRec.Body.String())
	}
}

func createGraphMemoryLineagePrincipal(t *testing.T, workspaceID, role string) string {
	t.Helper()
	ctx := context.Background()
	var userID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id::text`,
		"graph-memory-lineage-"+uuid.NewString()[:8], "graph-memory-lineage-"+uuid.NewString()+"@multica.test").Scan(&userID); err != nil {
		t.Fatalf("create graph memory lineage user: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, $3)`, workspaceID, userID, role); err != nil {
		t.Fatalf("add graph memory lineage workspace member: %v", err)
	}
	return userID
}

func graphMemoryLineageResponseFor(t *testing.T, userID, workspaceID, channelID string) *httptest.ResponseRecorder {
	t.Helper()
	req := newRequestAs(userID, http.MethodGet,
		"/api/workspaces/"+workspaceID+"/graph-memory/channels/"+channelID+"/lineage", nil)
	req = withRouteParams(req, "id", workspaceID, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.GetGraphMemoryChannelLineage(rec, req)
	return rec
}

// Task 16: the lineage surfaces the latest migration binding generation
// and the copy worker's phase (spec §12 observability).
func TestGetGraphMemoryChannelLineageShowsMigration(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("test database unavailable")
	}
	ctx := context.Background()
	workspaceID := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryMember(t, workspaceID, "owner")
	channelID := createGraphMemoryTestChannel(t, workspaceID)
	if _, err := service.ResolveChannelRoute(ctx, testPool, workspaceID.String(), channelID.String()); err != nil {
		t.Fatal(err)
	}
	// A channel with no published atoms has watermark 0, so the binding
	// records the generation without queuing a copy.
	var projectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title) VALUES ($1, $2) RETURNING id`,
		workspaceID, "Lineage migration project").Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	bindChannelProjectStrings(t, ctx, channelID.String(), projectID)

	req := withRouteParams(newRequest(http.MethodGet,
		"/api/workspaces/"+workspaceID.String()+"/graph-memory/channels/"+channelID.String()+"/lineage", nil),
		"id", workspaceID.String(), "channelId", channelID.String())
	rec := httptest.NewRecorder()
	testHandler.GetGraphMemoryChannelLineage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("lineage after bind: status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"migration":{`) || !strings.Contains(body, `"binding_generation":1`) {
		t.Fatalf("migration section missing: %s", body)
	}
	if strings.Contains(body, `"phase"`) {
		t.Fatalf("no copy queued at watermark 0, phase must stay absent: %s", body)
	}
}
