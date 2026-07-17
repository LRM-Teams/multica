package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/stretchr/testify/require"
)

// setupEnvDispatchChannelRolloutFixture seeds a minimal EnvDispatch message
// rollout (env + project + group channel + agent member + pending binding) and
// returns the IDs. Rows are cleaned up defensively; the cleanup handler under
// test also removes them.
func setupEnvDispatchChannelRolloutFixture(t *testing.T) (envID, projectID, channelID, agentID string) {
	t.Helper()
	ctx := context.Background()
	agentID = createHandlerTestAgent(t, "Env Dispatch Channel Routes Agent", nil)
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO environment (workspace_id, sandbox_ids, mode)
		VALUES ($1, '{}', 'scratch') RETURNING id`, testWorkspaceID).Scan(&envID))
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM environment WHERE id = $1`, envID) })
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title, env_id)
		VALUES ($1, $2, $3) RETURNING id`, testWorkspaceID, "Env Dispatch Routes Project "+uuid.NewString(), envID).Scan(&projectID))
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID) })
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, kind, project_id, created_by)
		VALUES ($1, $2, 'group', $3, $4) RETURNING id`,
		testWorkspaceID, "env-dispatch-routes-"+uuid.NewString(), projectID, testUserID).Scan(&channelID))
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID) })
	_, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID)
	require.NoError(t, err)
	_, err = testPool.Exec(ctx, `
		INSERT INTO environment_agent_sandbox (env_id, channel_id, agent_id, status, sandbox_config)
		VALUES ($1, $2, $3, 'pending', '{}'::jsonb)`, envID, channelID, agentID)
	require.NoError(t, err)
	return envID, projectID, channelID, agentID
}

// authedChannelRequest builds a request that carries the test user + workspace
// (as the auth middleware would) and the named chi URL param.
func authedChannelRequest(method, path, paramKey, paramValue string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("X-User-ID", testUserID)
	r = r.WithContext(middleware.SetMemberContext(r.Context(), testWorkspaceID, db.Member{}))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(paramKey, paramValue)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestChannelDagFacadeResolvesBoundProject(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	_, _, channelID, _ := setupEnvDispatchChannelRolloutFixture(t)

	w := httptest.NewRecorder()
	testHandler.GetEnvDispatchChannelDag(w, authedChannelRequest(http.MethodGet, "/api/v1/env-dispatch/channels/"+channelID+"/dag", "channelID", channelID))
	// The facade resolved the bound project and ran the project-scoped DAG
	// status decision. A fresh rollout has no terminal root task -> 202.
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (in_progress); body=%s", w.Code, w.Body.String())
	}
}

func TestChannelDagFacadeMissingChannelReturns404(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	w := httptest.NewRecorder()
	testHandler.GetEnvDispatchChannelDag(w, authedChannelRequest(http.MethodGet, "/api/v1/env-dispatch/channels/"+uuid.NewString()+"/dag", "channelID", uuid.NewString()))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}

func TestChannelCleanupDeletesChannelProjectEnvAndBindings(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	envID, projectID, channelID, agentID := setupEnvDispatchChannelRolloutFixture(t)

	w := httptest.NewRecorder()
	testHandler.DeleteEnvDispatchChannel(w, authedChannelRequest(http.MethodDelete, "/api/v1/env-dispatch/channels/"+channelID, "channelID", channelID))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	// Channel, project, env, and bindings are all gone.
	for _, q := range []struct{ name, sql, id string }{
		{"channel", `SELECT id FROM channel WHERE id = $1`, channelID},
		{"project", `SELECT id FROM project WHERE id = $1`, projectID},
		{"env", `SELECT id FROM environment WHERE id = $1`, envID},
		{"binding", `SELECT env_id FROM environment_agent_sandbox WHERE env_id = $1`, envID},
	} {
		if err := testPool.QueryRow(ctx, q.sql, q.id).Scan(new(string)); err != pgx.ErrNoRows {
			t.Fatalf("%s should be deleted, got err=%v", q.name, err)
		}
	}
	// Agent remains (it is not part of the rollout).
	var exists bool
	require.NoError(t, testPool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent WHERE id = $1)`, agentID).Scan(&exists))
	if !exists {
		t.Fatal("agent should remain after channel cleanup")
	}
}

func TestChannelCleanupIsIdempotent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	_, _, channelID, _ := setupEnvDispatchChannelRolloutFixture(t)

	w1 := httptest.NewRecorder()
	testHandler.DeleteEnvDispatchChannel(w1, authedChannelRequest(http.MethodDelete, "/api/v1/env-dispatch/channels/"+channelID, "channelID", channelID))
	if w1.Code != http.StatusNoContent {
		t.Fatalf("first delete: status = %d, want 204; body=%s", w1.Code, w1.Body.String())
	}
	w2 := httptest.NewRecorder()
	testHandler.DeleteEnvDispatchChannel(w2, authedChannelRequest(http.MethodDelete, "/api/v1/env-dispatch/channels/"+channelID, "channelID", channelID))
	if w2.Code != http.StatusNoContent {
		t.Fatalf("second delete: status = %d, want 204 (idempotent); body=%s", w2.Code, w2.Body.String())
	}
}
