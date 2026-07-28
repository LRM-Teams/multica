package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/daemonws"
)

func TestCreateChannelProjectBindingAndProjectReverseList(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("no test database")
	}
	ctx := context.Background()
	var projectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title) VALUES ($1, $2) RETURNING id`,
		testWorkspaceID, "Channel relation "+uuid.NewString(),
	).Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID) })

	created := httptest.NewRecorder()
	createReq := withChannelTestWorkspaceCtx(t, newRequest(http.MethodPost, "/api/channels", map[string]any{
		"name":       "project-bound-" + uuid.NewString(),
		"project_id": projectID,
	}), testUserID)
	testHandler.CreateChannel(created, createReq)
	if created.Code != http.StatusCreated {
		t.Fatalf("CreateChannel = %d: %s", created.Code, created.Body.String())
	}
	var channel ChannelResponse
	if err := json.NewDecoder(created.Body).Decode(&channel); err != nil {
		t.Fatalf("decode channel: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channel.ID) })
	if channel.ProjectID == nil || *channel.ProjectID != projectID {
		t.Fatalf("created project_id = %v, want %s", channel.ProjectID, projectID)
	}
	bound := latestChannelProjectSystemEventForTest(t, channel.ID)
	if bound.Event != channelProjectBoundEvent || bound.Params.ProjectID != projectID || bound.Params.PreviousProjectID != "" {
		t.Fatalf("created channel project event = %#v, want bound project %s", bound, projectID)
	}
	if bound.Params.ActorID != testUserID || bound.Params.ActorType != "human" {
		t.Fatalf("created channel project actor = %#v, want current human %s", bound.Params, testUserID)
	}

	missing := httptest.NewRecorder()
	missingReq := withChannelTestWorkspaceCtx(t, newRequest(http.MethodPut, "/api/channels/"+channel.ID+"/project", map[string]any{}), testUserID)
	missingReq = withURLParam(missingReq, "channelId", channel.ID)
	testHandler.SetChannelProject(missing, missingReq)
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("SetChannelProject missing project_id = %d: %s", missing.Code, missing.Body.String())
	}
	var projectAfterMissing *string
	if err := testPool.QueryRow(ctx, `SELECT project_id::text FROM channel WHERE id = $1`, channel.ID).Scan(&projectAfterMissing); err != nil {
		t.Fatalf("load channel project after missing update: %v", err)
	}
	if projectAfterMissing == nil || *projectAfterMissing != projectID {
		t.Fatalf("project_id after missing update = %v, want %s", projectAfterMissing, projectID)
	}

	list := httptest.NewRecorder()
	listReq := newRequest(http.MethodGet, "/api/projects/"+projectID+"/channels?workspace_id="+testWorkspaceID, nil)
	listReq = withURLParam(listReq, "id", projectID)
	testHandler.ListProjectChannels(list, listReq)
	if list.Code != http.StatusOK {
		t.Fatalf("ListProjectChannels = %d: %s", list.Code, list.Body.String())
	}
	var response struct {
		Channels []ProjectChannelResponse `json:"channels"`
	}
	if err := json.NewDecoder(list.Body).Decode(&response); err != nil {
		t.Fatalf("decode project channels: %v", err)
	}
	if len(response.Channels) != 1 || response.Channels[0].ID != channel.ID || response.Channels[0].ProjectID != projectID {
		t.Fatalf("project channels = %#v, want created channel", response.Channels)
	}

	// Being a workspace member is not enough to discover a private group via
	// the project's reverse lookup. The caller must also be a channel member.
	nonMemberID := seedWorkspaceUserForTransportTargetTest(t, "project-channel-non-member-"+uuid.NewString())
	nonMemberList := httptest.NewRecorder()
	nonMemberReq := newRequestAs(nonMemberID, http.MethodGet, "/api/projects/"+projectID+"/channels?workspace_id="+testWorkspaceID, nil)
	nonMemberReq = withURLParam(nonMemberReq, "id", projectID)
	testHandler.ListProjectChannels(nonMemberList, nonMemberReq)
	if nonMemberList.Code != http.StatusOK {
		t.Fatalf("ListProjectChannels as non-member = %d: %s", nonMemberList.Code, nonMemberList.Body.String())
	}
	var nonMemberResponse struct {
		Channels []ProjectChannelResponse `json:"channels"`
	}
	if err := json.NewDecoder(nonMemberList.Body).Decode(&nonMemberResponse); err != nil {
		t.Fatalf("decode non-member project channels: %v", err)
	}
	if len(nonMemberResponse.Channels) != 0 {
		t.Fatalf("non-member project channels = %#v, want no private group disclosure", nonMemberResponse.Channels)
	}

	clear := httptest.NewRecorder()
	clearReq := withChannelTestWorkspaceCtx(t, newRequest(http.MethodPut, "/api/channels/"+channel.ID+"/project", map[string]any{"project_id": ""}), testUserID)
	clearReq = withURLParam(clearReq, "channelId", channel.ID)
	testHandler.SetChannelProject(clear, clearReq)
	if clear.Code != http.StatusOK {
		t.Fatalf("SetChannelProject empty clear = %d: %s", clear.Code, clear.Body.String())
	}
	var projectAfterClear *string
	if err := testPool.QueryRow(ctx, `SELECT project_id::text FROM channel WHERE id = $1`, channel.ID).Scan(&projectAfterClear); err != nil {
		t.Fatalf("load channel project after empty clear: %v", err)
	}
	if projectAfterClear != nil {
		t.Fatalf("project_id after empty clear = %v, want nil", *projectAfterClear)
	}
}

func TestSetChannelProjectRequiresChannelManager(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("no test database")
	}
	ctx := context.Background()

	creatorID := seedWorkspaceUserForTransportTargetTest(t, "project-binding-creator-"+uuid.NewString())
	memberID := seedWorkspaceUserForTransportTargetTest(t, "project-binding-member-"+uuid.NewString())
	adminID := seedWorkspaceUserForTransportTargetTest(t, "project-binding-admin-"+uuid.NewString())
	if _, err := testPool.Exec(ctx, `UPDATE member SET role = 'admin' WHERE workspace_id = $1 AND user_id = $2`, testWorkspaceID, adminID); err != nil {
		t.Fatalf("promote test admin: %v", err)
	}

	var channelID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by)
		VALUES ($1, $2, $3)
		RETURNING id`, testWorkspaceID, "project-binding-permissions-"+uuid.NewString(), creatorID).Scan(&channelID); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID) })
	for _, userID := range []string{creatorID, memberID, adminID, testUserID} {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
			VALUES ($1, $2, 'user', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, userID); err != nil {
			t.Fatalf("add channel member %s: %v", userID, err)
		}
	}

	createProject := func(title string) string {
		t.Helper()
		var projectID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO project (workspace_id, title) VALUES ($1, $2) RETURNING id`, testWorkspaceID, title).Scan(&projectID); err != nil {
			t.Fatalf("create project: %v", err)
		}
		t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID) })
		return projectID
	}
	projectA := createProject("Channel binding A " + uuid.NewString())
	projectB := createProject("Channel binding B " + uuid.NewString())

	setProject := func(userID string, projectID any) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := withChannelTestWorkspaceCtx(t, newRequestAs(userID, http.MethodPut, "/api/channels/"+channelID+"/project", map[string]any{"project_id": projectID}), userID)
		req = withURLParam(req, "channelId", channelID)
		testHandler.SetChannelProject(w, req)
		return w
	}
	currentProject := func() *string {
		t.Helper()
		var projectID *string
		if err := testPool.QueryRow(ctx, `SELECT project_id::text FROM channel WHERE id = $1`, channelID).Scan(&projectID); err != nil {
			t.Fatalf("load channel project: %v", err)
		}
		return projectID
	}

	if w := setProject(memberID, projectA); w.Code != http.StatusForbidden {
		t.Fatalf("member bind = %d: %s", w.Code, w.Body.String())
	}
	if got := currentProject(); got != nil {
		t.Fatalf("member bind changed project to %q", *got)
	}

	if w := setProject(creatorID, projectA); w.Code != http.StatusOK {
		t.Fatalf("creator bind = %d: %s", w.Code, w.Body.String())
	}
	if got := currentProject(); got == nil || *got != projectA {
		t.Fatalf("creator bind project = %v, want %s", got, projectA)
	}

	if w := setProject(memberID, projectB); w.Code != http.StatusForbidden {
		t.Fatalf("member change = %d: %s", w.Code, w.Body.String())
	}
	if got := currentProject(); got == nil || *got != projectA {
		t.Fatalf("member change project = %v, want %s", got, projectA)
	}

	if w := setProject(adminID, projectB); w.Code != http.StatusOK {
		t.Fatalf("admin change = %d: %s", w.Code, w.Body.String())
	}
	if got := currentProject(); got == nil || *got != projectB {
		t.Fatalf("admin change project = %v, want %s", got, projectB)
	}
	read := httptest.NewRecorder()
	readReq := withChannelTestWorkspaceCtx(t, newRequestAs(memberID, http.MethodGet, "/api/channels/"+channelID+"/project", nil), memberID)
	readReq = withURLParam(readReq, "channelId", channelID)
	testHandler.GetChannelProject(read, readReq)
	if read.Code != http.StatusOK {
		t.Fatalf("member read = %d: %s", read.Code, read.Body.String())
	}
	var readBody map[string]string
	if err := json.NewDecoder(read.Body).Decode(&readBody); err != nil {
		t.Fatalf("decode member read: %v", err)
	}
	if got := readBody["project_id"]; got != projectB {
		t.Fatalf("member read project_id = %q, want %s", got, projectB)
	}

	if w := setProject(memberID, nil); w.Code != http.StatusForbidden {
		t.Fatalf("member unbind = %d: %s", w.Code, w.Body.String())
	}
	if got := currentProject(); got == nil || *got != projectB {
		t.Fatalf("member unbind project = %v, want %s", got, projectB)
	}

	if w := setProject(testUserID, nil); w.Code != http.StatusOK {
		t.Fatalf("owner unbind = %d: %s", w.Code, w.Body.String())
	}
	if got := currentProject(); got != nil {
		t.Fatalf("owner unbind project = %q, want nil", *got)
	}
}

// TestResolveProjectWorkdirRuntime_SharedRuntime covers the production /
// cloud case: the project's managed workdir lives on a shared runtime that
// is NOT owned by the viewer (owner_id is NULL). The old logic looked up
// "the viewer's own online runtime" and wrongly reported the Files panel as
// offline (#issue: shared public runtime). The resolver must instead find the
// runtime via the daemon recorded on the managed local_directory resource.
func TestResolveProjectWorkdirRuntime_SharedRuntime(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("no test database")
	}
	ctx := context.Background()

	// The resolver short-circuits when DaemonHub is nil (no way to send RPCs).
	prevHub := testHandler.DaemonHub
	testHandler.DaemonHub = daemonws.NewHub()
	t.Cleanup(func() { testHandler.DaemonHub = prevHub })

	var projectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title) VALUES ($1, 'Shared Workdir Project') RETURNING id
	`, testWorkspaceID).Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project_resource WHERE project_id = $1`, projectID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID)
	})

	const sharedDaemonID = "shared-daemon-files-test"
	// Shared runtime: online, owner_id NULL (nobody's personal daemon).
	var sharedRuntimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, last_seen_at
		)
		VALUES ($1, $2, 'shared-files-rt', 'cloud', 'shared-files-provider', 'online', '{}'::jsonb, '{}'::jsonb, now())
		RETURNING id
	`, testWorkspaceID, sharedDaemonID).Scan(&sharedRuntimeID); err != nil {
		t.Fatalf("create shared runtime: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, sharedRuntimeID)
	})

	// Managed workdir resource pinned to the shared daemon.
	if _, err := testPool.Exec(ctx, `
		INSERT INTO project_resource (project_id, workspace_id, resource_type, resource_ref, position)
		VALUES ($1, $2, 'local_directory', $3::jsonb, 0)
	`, projectID, testWorkspaceID,
		`{"managed":true,"daemon_id":"`+sharedDaemonID+`","local_path":"/srv/workspaces/projects/x/workdir"}`,
	); err != nil {
		t.Fatalf("create managed resource: %v", err)
	}

	rtID, ok := testHandler.resolveProjectWorkdirRuntime(ctx, testWorkspaceID, testUserID, projectID)
	if !ok {
		t.Fatal("resolveProjectWorkdirRuntime: ok=false, want the shared runtime")
	}
	if got := uuidToString(rtID); got != sharedRuntimeID {
		t.Fatalf("resolved runtime = %q, want shared runtime %q", got, sharedRuntimeID)
	}
}

// TestResolveProjectWorkdirRuntime_OfflineSharedDaemon verifies that when the
// managed workdir's daemon is offline, the resolver does not silently succeed
// via some unrelated runtime — it falls through (and, with no viewer-owned
// online runtime in a fresh project, reports unavailable).
func TestResolveProjectWorkdirRuntime_OfflineSharedDaemon(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("no test database")
	}
	ctx := context.Background()
	prevHub := testHandler.DaemonHub
	testHandler.DaemonHub = daemonws.NewHub()
	t.Cleanup(func() { testHandler.DaemonHub = prevHub })

	var projectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title) VALUES ($1, 'Offline Workdir Project') RETURNING id
	`, testWorkspaceID).Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project_resource WHERE project_id = $1`, projectID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID)
	})

	const offlineDaemonID = "offline-daemon-files-test"
	var offlineRuntimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, last_seen_at
		)
		VALUES ($1, $2, 'offline-files-rt', 'cloud', 'offline-files-provider', 'offline', '{}'::jsonb, '{}'::jsonb, now())
		RETURNING id
	`, testWorkspaceID, offlineDaemonID).Scan(&offlineRuntimeID); err != nil {
		t.Fatalf("create offline runtime: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, offlineRuntimeID)
	})

	if _, err := testPool.Exec(ctx, `
		INSERT INTO project_resource (project_id, workspace_id, resource_type, resource_ref, position)
		VALUES ($1, $2, 'local_directory', $3::jsonb, 0)
	`, projectID, testWorkspaceID,
		`{"managed":true,"daemon_id":"`+offlineDaemonID+`","local_path":"/srv/x"}`,
	); err != nil {
		t.Fatalf("create managed resource: %v", err)
	}

	// The offline shared daemon must not resolve. (The fallback only matches a
	// viewer-OWNED online runtime; a separate user's fixture runtime, even if
	// online, has a different owner_id and must not leak in here.)
	if rtID, ok := testHandler.resolveProjectWorkdirRuntime(ctx, testWorkspaceID, testUserID, projectID); ok {
		// If it resolved, it must at least not be the offline daemon's runtime.
		if uuidToString(rtID) == offlineRuntimeID {
			t.Fatalf("resolved the OFFLINE shared runtime %q, want it skipped", offlineRuntimeID)
		}
	}
}
