package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestTopLevelAgentDirs(t *testing.T) {
	nodes := []protocol.WorkdirFileNode{
		{Path: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", IsDir: true, Size: 10},
		{Path: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/notes", IsDir: true},
		{Path: "readme.txt", IsDir: false, Size: 3},
		{Path: "orphan-slug", IsDir: true},
		{Path: "", IsDir: true},
	}
	got := topLevelAgentDirs(nodes)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(got), got)
	}
	if got[0].Path != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" || got[1].Path != "orphan-slug" {
		t.Fatalf("unexpected dirs: %+v", got)
	}
}

func TestAgentWorkspaceOrphan(t *testing.T) {
	if !agentWorkspaceOrphan(db.Agent{}, false) {
		t.Fatal("missing agent should be orphan")
	}
	live := db.Agent{}
	if agentWorkspaceOrphan(live, true) {
		t.Fatal("live agent should not be orphan")
	}
	archived := db.Agent{ArchivedAt: pgtype.Timestamptz{Valid: true, Time: time.Now()}}
	if !agentWorkspaceOrphan(archived, true) {
		t.Fatal("archived agent should be orphan")
	}
}

func TestAgentsRootRelPath(t *testing.T) {
	got := agentsRootRelPath("ws-1")
	want := "ws-1/.multica/agents"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	got = agentWorkspaceRelPath("ws-1", "agent-1")
	want = "ws-1/.multica/agents/agent-1"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestListRuntimeAgentWorkspaces_MemberOKOffline(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := handlerTestRuntimeID(t)
	req := withURLParam(newRequest(http.MethodGet, "/api/runtimes/"+runtimeID+"/agent-workspaces", nil), "runtimeId", runtimeID)
	w := httptest.NewRecorder()
	testHandler.ListRuntimeAgentWorkspaces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp RuntimeAgentWorkspacesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// No daemon hub connection in unit tests → offline (or ok empty if hub nil treated offline).
	if resp.Status != "offline" && resp.Status != "ok" && resp.Status != "missing" {
		t.Fatalf("unexpected status %q", resp.Status)
	}
	if resp.Items == nil {
		t.Fatal("items should be non-nil slice")
	}
}

func TestDeleteRuntimeAgentWorkspace_ForbiddenForOtherMember(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := handlerTestRuntimeID(t)
	other := createAgentFilesTestMember(t, "member")
	req := withURLParams(
		newRequestAs(other, http.MethodDelete, "/api/runtimes/"+runtimeID+"/agent-workspaces/deadbeef-dead-beef-dead-beefdeadbeef", nil),
		"runtimeId", runtimeID,
		"dirName", "deadbeef-dead-beef-dead-beefdeadbeef",
	)
	w := httptest.NewRecorder()
	testHandler.DeleteRuntimeAgentWorkspace(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeleteRuntimeAgentWorkspace_InvalidDirName(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := handlerTestRuntimeID(t)
	req := withURLParams(
		newRequest(http.MethodDelete, "/api/runtimes/"+runtimeID+"/agent-workspaces/bad", nil),
		"runtimeId", runtimeID,
		"dirName", "../escape",
	)
	w := httptest.NewRecorder()
	testHandler.DeleteRuntimeAgentWorkspace(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}
