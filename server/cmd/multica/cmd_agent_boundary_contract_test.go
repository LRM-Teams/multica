package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// #802 / #801 boundary contract tests (Vera).
//
// Product lock (Frank + Barry + Parker + Ronan revised table):
//   - necessary capabilities PASS only when CLI hits dedicated /api/agent/*
//   - agent-aware human alias is transitional observation, does NOT count as done
//   - nonessential temporary owner fallback: named allowlist only; unlisted = 403
//   - no production implementation in this task — tests define done
//
// Order: ① channel list/members/mute → ② attachment → ③ remove/claim (handler/daemon) → ④ rest

const boundaryContractChannelID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
const boundaryContractAttachmentID = "11111111-2222-3333-4444-555555555555"

func boundaryCLIEnv(t *testing.T, srvURL string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MULTICA_TOKEN", "test-agent-token")
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-boundary")
	t.Setenv("MULTICA_SERVER_URL", srvURL)
}

func newChannelListTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "list"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("output", "json", "")
	return cmd
}

func newChannelMembersTestCmd(target string) *cobra.Command {
	cmd := &cobra.Command{Use: "members"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("target", "", "")
	cmd.Flags().String("output", "json", "")
	_ = cmd.Flags().Set("target", target)
	return cmd
}

func newAttachmentViewTestCmd(output string) *cobra.Command {
	cmd := &cobra.Command{Use: "view"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("id", "", "")
	cmd.Flags().String("output", "", "")
	_ = cmd.Flags().Set("output", output)
	return cmd
}

// --- ① channel list / members / mute: dedicated path contracts ---

// TestBoundary_ChannelList_HitsDedicatedAgentAPI asserts multica channel list
// hits GET /api/agent/channels only — never human /api/channels.
// PASS criterion: dedicated path only (alias does not count).
func TestBoundary_ChannelList_HitsDedicatedAgentAPI(t *testing.T) {
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/agent/channels":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": boundaryContractChannelID, "name": "member-only", "member_count": 1},
			})
		default:
			// Human path must not be used for agent runtime channel list.
			http.Error(w, "human path forbidden for agent boundary contract", http.StatusForbidden)
		}
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnv(t, srv.URL)

	if err := runChannelList(newChannelListTestCmd(), nil); err != nil {
		t.Fatalf("runChannelList: %v (paths=%v)", err, gotPaths)
	}
	if len(gotPaths) != 1 || gotPaths[0] != "GET /api/agent/channels" {
		t.Fatalf("channel list paths = %v, want exactly [GET /api/agent/channels]", gotPaths)
	}
}

// TestBoundary_ChannelMembers_HitsDedicatedAgentAPI asserts members list uses
// GET /api/agent/channels/{id}/members (Ronan table / agent_channels.go).
func TestBoundary_ChannelMembers_HitsDedicatedAgentAPI(t *testing.T) {
	wantPath := "/api/agent/channels/" + boundaryContractChannelID + "/members"
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		if r.URL.Path == wantPath {
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "agent-1", "member_type": "agent", "name": "vera", "role": "member"},
			})
			return
		}
		http.Error(w, "human path forbidden for agent boundary contract", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnv(t, srv.URL)

	// UUID target avoids an extra list-channels resolve hop.
	if err := runChannelMembers(newChannelMembersTestCmd(boundaryContractChannelID), nil); err != nil {
		t.Fatalf("runChannelMembers: %v (paths=%v)", err, gotPaths)
	}
	if len(gotPaths) != 1 || gotPaths[0] != "GET "+wantPath {
		t.Fatalf("channel members paths = %v, want exactly [GET %s]", gotPaths, wantPath)
	}
}

// TestBoundary_ChannelMute_HitsDedicatedAgentAPI asserts mute/unmute hit
// dedicated agent channel mute routes, not human /api/channels/.../agent-mute
// as the final cutover target. Resolution may use agent channel list.
func TestBoundary_ChannelMute_HitsDedicatedAgentAPI(t *testing.T) {
	var requests []string
	mutePath := "/api/agent/channels/" + boundaryContractChannelID + "/mute"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/agent/channels":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": boundaryContractChannelID, "name": "斗地主开发"},
			})
		case r.Method == http.MethodPut && r.URL.Path == mutePath:
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "muted": true})
		case r.Method == http.MethodDelete && r.URL.Path == mutePath:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "human path forbidden for agent boundary contract", http.StatusForbidden)
		}
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnv(t, srv.URL)

	if err := setChannelMute(newChannelMuteTestCmd(t, "#斗地主开发"), true); err != nil {
		t.Fatalf("mute: %v (requests=%v)", err, requests)
	}
	if err := setChannelMute(newChannelMuteTestCmd(t, "#斗地主开发"), false); err != nil {
		t.Fatalf("unmute: %v (requests=%v)", err, requests)
	}

	var humanHits []string
	for _, req := range requests {
		if strings.Contains(req, "/api/channels/") && !strings.HasPrefix(strings.SplitN(req, " ", 2)[1], "/api/agent/") {
			humanHits = append(humanHits, req)
		}
		if strings.Contains(req, "/agent-mute") {
			humanHits = append(humanHits, req)
		}
	}
	if len(humanHits) > 0 {
		t.Fatalf("channel mute still hit human/legacy paths %v; full=%v", humanHits, requests)
	}
	wantMute := "PUT " + mutePath
	wantUnmute := "DELETE " + mutePath
	foundMute, foundUnmute := false, false
	for _, req := range requests {
		if req == wantMute {
			foundMute = true
		}
		if req == wantUnmute {
			foundUnmute = true
		}
	}
	if !foundMute || !foundUnmute {
		t.Fatalf("requests=%v, want include %q and %q", requests, wantMute, wantUnmute)
	}
}

// TestBoundary_ChannelList_DoesNotHitHumanChannels is an explicit negative:
// if the mock only serves human /api/channels, the CLI must fail (not succeed
// via owner-borrow human path). Documents fail-closed for wrong surface.
func TestBoundary_ChannelList_DoesNotHitHumanChannels(t *testing.T) {
	var humanHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/channels" {
			humanHits++
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": boundaryContractChannelID, "name": "owner-only-leak", "member_count": 99},
			})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnv(t, srv.URL)

	err := runChannelList(newChannelListTestCmd(), nil)
	// Either error (dedicated 404) or — if still on human path — we fail the
	// assertion below because humanHits>0 is forbidden for PASS.
	if err == nil && humanHits > 0 {
		t.Fatalf("channel list succeeded via human /api/channels (%d hits); dedicated-only required", humanHits)
	}
	if humanHits > 0 && err != nil {
		// CLI still called human path then failed somehow — still a contract fail.
		t.Fatalf("channel list contacted human /api/channels (%d hits) before err=%v", humanHits, err)
	}
}

// --- ② attachment view/upload ---

// TestBoundary_AttachmentView_HitsDedicatedAgentAPI asserts metadata fetch is
// GET /api/agent/attachments/{id} (not /api/attachments/{id}).
func TestBoundary_AttachmentView_HitsDedicatedAgentAPI(t *testing.T) {
	outFile := filepath.Join(t.TempDir(), "out.bin")
	wantMeta := "/api/agent/attachments/" + boundaryContractAttachmentID
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == wantMeta:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":           boundaryContractAttachmentID,
				"filename":     "secret.png",
				"size_bytes":   "3",
				"download_url": "/api/agent/attachments/" + boundaryContractAttachmentID + "/download",
			})
		case r.URL.Path == "/api/agent/attachments/"+boundaryContractAttachmentID+"/download":
			_, _ = w.Write([]byte("abc"))
		default:
			http.Error(w, "human attachment path forbidden", http.StatusForbidden)
		}
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnv(t, srv.URL)

	if err := runAttachmentView(newAttachmentViewTestCmd(outFile), []string{boundaryContractAttachmentID}); err != nil {
		t.Fatalf("runAttachmentView: %v (paths=%v)", err, gotPaths)
	}
	if len(gotPaths) < 1 || gotPaths[0] != "GET "+wantMeta {
		t.Fatalf("first path = %v, want GET %s first", gotPaths, wantMeta)
	}
	for _, p := range gotPaths {
		if strings.HasPrefix(strings.SplitN(p, " ", 2)[1], "/api/attachments/") {
			t.Fatalf("hit human attachment path %q; full=%v", p, gotPaths)
		}
	}
	data, err := os.ReadFile(outFile)
	if err != nil || string(data) != "abc" {
		t.Fatalf("downloaded file = %q err=%v", data, err)
	}
}

// TestBoundary_AttachmentUpload_HitsDedicatedAgentAPI asserts upload goes to
// dedicated agent upload surface, not human /api/upload-file.
func TestBoundary_AttachmentUpload_HitsDedicatedAgentAPI(t *testing.T) {
	src := filepath.Join(t.TempDir(), "up.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		// Accept either nested or flat dedicated upload names once Ronan lands them.
		if r.Method == http.MethodPost && (r.URL.Path == "/api/agent/attachments" ||
			r.URL.Path == "/api/agent/attachments/upload" ||
			r.URL.Path == "/api/agent/upload-file") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":       boundaryContractAttachmentID,
				"filename": "up.txt",
			})
			return
		}
		http.Error(w, "human upload path forbidden", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnv(t, srv.URL)

	cmd := &cobra.Command{Use: "upload"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("path", "", "")
	cmd.Flags().String("target", "", "")
	_ = cmd.Flags().Set("path", src)

	err := runAttachmentUpload(cmd, nil)
	if err != nil {
		t.Fatalf("runAttachmentUpload: %v (paths=%v) — expect dedicated /api/agent/attachments*", err, gotPaths)
	}
	for _, p := range gotPaths {
		path := strings.SplitN(p, " ", 2)[1]
		if path == "/api/upload-file" || strings.HasPrefix(path, "/api/attachments/") {
			t.Fatalf("upload hit human path %q; full=%v", p, gotPaths)
		}
		if !strings.HasPrefix(path, "/api/agent/") {
			t.Fatalf("upload path %q is not under /api/agent/; full=%v", p, gotPaths)
		}
	}
}

// --- already-dedicated regression (must stay green) ---

// TestBoundary_MessageSend_AlreadyDedicated is a control: message send must
// remain on /api/agent/messages/send (regression against re-mixing human path).
func TestBoundary_MessageSend_AlreadyDedicated(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.URL.Path != "/api/agent/messages/send" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"action":  "message_send",
			"created": true,
			"message": map[string]any{"id": "msg-1"},
		})
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnv(t, srv.URL)

	cmd := newMessageSendCmd()
	_ = cmd.Flags().Set("target", "#multica")
	_ = cmd.Flags().Set("message", "boundary regression")
	if err := runAgentMessageSend(cmd, nil); err != nil {
		t.Fatalf("runAgentMessageSend: %v", err)
	}
	if gotPath != "/api/agent/messages/send" {
		t.Fatalf("path = %q, want /api/agent/messages/send", gotPath)
	}
}

// TestBoundary_ThreadUnfollow_AlreadyDedicated keeps unfollow on dedicated path.
func TestBoundary_ThreadUnfollow_AlreadyDedicated(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.URL.Path != "/api/agent/threads/unfollow" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnv(t, srv.URL)

	cmd := &cobra.Command{Use: "unfollow"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("target", "", "")
	_ = cmd.Flags().Set("target", "#multica:aaaaaaaa")
	if err := runThreadUnfollow(cmd, nil); err != nil {
		t.Fatalf("runThreadUnfollow: %v", err)
	}
	if gotPath != "/api/agent/threads/unfollow" {
		t.Fatalf("path = %q, want /api/agent/threads/unfollow", gotPath)
	}
}

// --- nonessential allowlist documentation ---

// TestBoundary_NonessentialAllowlist_InventoryDocumentsNamedSurfaces locks the
// temporary owner-fallback set as an explicit inventory. When Ronan publishes
// callers inventory, keep this table in sync. Unlisted human admin surfaces
// must fail closed for agent principals (server tests land with #801).
//
// This test does not hit the network; it fails if the inventory is emptied
// without a matching "all nonessential deleted" note — keeps temporary visible.
func TestBoundary_NonessentialAllowlist_InventoryDocumentsNamedSurfaces(t *testing.T) {
	// Named temporary owner-fallback surfaces (Barry/Ronan). Empty deleteOwner
	// means "not yet assigned" — still must stay listed until deleted.
	type entry struct {
		surface     string
		cliCommand  string // empty if no real caller → must be 403, not fallback
		humanPath   string
		deleteOwner string // PR/owner that will remove fallback
	}
	inventory := []entry{
		// From Ronan revised table — only keep rows with real callers once inventory lands.
		// Placeholders until callers inventory; prefer 403 if cliCommand stays empty.
		{surface: "channel create/archive/pin", cliCommand: "", humanPath: "/api/channels", deleteOwner: "slice2"},
		{surface: "channel members add/batch admin", cliCommand: "channel member add", humanPath: "/api/channels/{id}/members/batch", deleteOwner: "slice2"},
		{surface: "project create/update/resource write", cliCommand: "", humanPath: "/api/projects", deleteOwner: "slice2"},
	}
	if len(inventory) == 0 {
		t.Fatal("nonessential allowlist inventory empty — either all deleted (document in test comment) or list was dropped by mistake")
	}
	for _, e := range inventory {
		if strings.TrimSpace(e.surface) == "" || strings.TrimSpace(e.humanPath) == "" {
			t.Fatalf("inventory row incomplete: %+v", e)
		}
		if e.cliCommand == "" {
			// Documented intent: no real caller → server must 403 agent principal
			// (not invent owner fallback). Server-side assertion tracked under #801.
			t.Logf("nonessential %q has no CLI caller → expect agent 403 on %s (delete=%s)", e.surface, e.humanPath, e.deleteOwner)
		}
	}
}

const boundaryContractIssueID = "1881a167-4bb6-4602-944b-f40ce4192fe6"
const boundaryContractProjectID = "p1111111-2222-3333-4444-555555555555"

// --- ④ issue + project resource (necessary; still human paths today) ---

// TestBoundary_IssueGet_HitsDedicatedAgentAPI asserts issue get uses
// GET /api/agent/issues/{id} (resolve + read both under dedicated).
func TestBoundary_IssueGet_HitsDedicatedAgentAPI(t *testing.T) {
	wantPath := "/api/agent/issues/" + boundaryContractIssueID
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		if r.URL.Path == wantPath {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":         boundaryContractIssueID,
				"identifier": "MUL-1",
				"title":      "boundary",
				"status":     "todo",
			})
			return
		}
		http.Error(w, "human issue path forbidden", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnv(t, srv.URL)

	cmd := &cobra.Command{Use: "get"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("output", "json", "")

	if err := runIssueGet(cmd, []string{boundaryContractIssueID}); err != nil {
		t.Fatalf("runIssueGet: %v (paths=%v)", err, gotPaths)
	}
	for _, p := range gotPaths {
		path := strings.SplitN(p, " ", 2)[1]
		if strings.HasPrefix(path, "/api/issues") {
			t.Fatalf("hit human issue path %q; full=%v", p, gotPaths)
		}
		if !strings.HasPrefix(path, "/api/agent/issues") {
			t.Fatalf("path %q not under /api/agent/issues; full=%v", p, gotPaths)
		}
	}
}

// TestBoundary_IssueCreate_HitsDedicatedAgentAPI asserts create posts to
// POST /api/agent/issues.
func TestBoundary_IssueCreate_HitsDedicatedAgentAPI(t *testing.T) {
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPost && r.URL.Path == "/api/agent/issues" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":         boundaryContractIssueID,
				"identifier": "MUL-1",
				"title":      "created",
			})
			return
		}
		http.Error(w, "human issue path forbidden", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnv(t, srv.URL)

	cmd := &cobra.Command{Use: "create"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("title", "", "")
	cmd.Flags().String("description", "", "")
	cmd.Flags().Bool("description-stdin", false, "")
	cmd.Flags().String("description-file", "", "")
	cmd.Flags().String("status", "", "")
	cmd.Flags().String("priority", "", "")
	cmd.Flags().String("assignee", "", "")
	cmd.Flags().String("assignee-id", "", "")
	cmd.Flags().String("parent", "", "")
	cmd.Flags().String("project", "", "")
	cmd.Flags().String("channel", "", "")
	cmd.Flags().String("start-date", "", "")
	cmd.Flags().String("due-date", "", "")
	cmd.Flags().StringArray("acceptance-criteria", nil, "")
	cmd.Flags().String("source-channel", "", "")
	cmd.Flags().String("source-message", "", "")
	cmd.Flags().String("output", "json", "")
	cmd.Flags().StringSlice("attachment-id", nil, "")
	_ = cmd.Flags().Set("title", "boundary create")

	if err := runIssueCreate(cmd, nil); err != nil {
		t.Fatalf("runIssueCreate: %v (paths=%v)", err, gotPaths)
	}
	if len(gotPaths) != 1 || gotPaths[0] != "POST /api/agent/issues" {
		t.Fatalf("paths = %v, want [POST /api/agent/issues]", gotPaths)
	}
}

// TestBoundary_WorkspaceGet_HitsDedicatedAgentAPI asserts workspace get uses
// GET /api/agent/workspace (or /api/agent/workspaces/{id}) — not human /api/workspaces.
func TestBoundary_WorkspaceGet_HitsDedicatedAgentAPI(t *testing.T) {
	wsID := "w1111111-2222-3333-4444-555555555555"
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodGet && (r.URL.Path == "/api/agent/workspace" ||
			r.URL.Path == "/api/agent/workspaces/"+wsID ||
			r.URL.Path == "/api/agent/workspace/"+wsID) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":   wsID,
				"name": "ws",
				"slug": "ws",
			})
			return
		}
		http.Error(w, "human workspace path forbidden", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnv(t, srv.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", wsID)

	cmd := &cobra.Command{Use: "get"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("output", "json", "")
	cmd.Flags().Bool("full-id", false, "")

	if err := runWorkspaceGet(cmd, []string{wsID}); err != nil {
		t.Fatalf("runWorkspaceGet: %v (paths=%v)", err, gotPaths)
	}
	for _, p := range gotPaths {
		path := strings.SplitN(p, " ", 2)[1]
		if strings.HasPrefix(path, "/api/workspaces") {
			t.Fatalf("hit human workspace path %q; full=%v", p, gotPaths)
		}
		if !strings.HasPrefix(path, "/api/agent/") {
			t.Fatalf("path %q not under /api/agent/; full=%v", p, gotPaths)
		}
	}
}

// TestBoundary_ReminderList_AlreadyDedicated keeps reminder list on dedicated path.
func TestBoundary_ReminderList_AlreadyDedicated(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.URL.Path != "/api/agent/reminders/list" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"reminders": []any{}})
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnv(t, srv.URL)

	cmd := &cobra.Command{Use: "list"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("status", "", "")
	if err := runReminderList(cmd, nil); err != nil {
		t.Fatalf("runReminderList: %v", err)
	}
	if gotPath != "/api/agent/reminders/list" {
		t.Fatalf("path = %q, want /api/agent/reminders/list", gotPath)
	}
}

// TestBoundary_SquadMemberSetRole_HitsDedicatedAgentAPI asserts set-role uses
// PATCH /api/agent/squads/{id}/members/role (leader authority still server-side).
func TestBoundary_SquadMemberSetRole_HitsDedicatedAgentAPI(t *testing.T) {
	squadID := "s1111111-2222-3333-4444-555555555555"
	wantPath := "/api/agent/squads/" + squadID + "/members/role"
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPatch && r.URL.Path == wantPath {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			return
		}
		http.Error(w, "human squad path forbidden", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnv(t, srv.URL)

	cmd := &cobra.Command{Use: "set-role"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("member-id", "", "")
	cmd.Flags().String("member-type", "", "")
	cmd.Flags().String("role", "", "")
	cmd.Flags().String("output", "json", "")
	_ = cmd.Flags().Set("member-id", "agent-1")
	_ = cmd.Flags().Set("member-type", "agent")
	_ = cmd.Flags().Set("role", "member")

	if err := runSquadMemberSetRole(cmd, []string{squadID}); err != nil {
		t.Fatalf("runSquadMemberSetRole: %v (paths=%v)", err, gotPaths)
	}
	if len(gotPaths) != 1 || gotPaths[0] != "PATCH "+wantPath {
		t.Fatalf("paths = %v, want [PATCH %s]", gotPaths, wantPath)
	}
}

// TestBoundary_ProjectResourceList_HitsDedicatedAgentAPI asserts resource list
// uses GET /api/agent/projects/{id}/resources.
func TestBoundary_ProjectResourceList_HitsDedicatedAgentAPI(t *testing.T) {
	wantPath := "/api/agent/projects/" + boundaryContractProjectID + "/resources"
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		if r.URL.Path == wantPath {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resources": []map[string]any{},
			})
			return
		}
		http.Error(w, "human project path forbidden", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnv(t, srv.URL)

	cmd := &cobra.Command{Use: "list"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("output", "json", "")
	cmd.Flags().Bool("full-id", false, "")

	if err := runProjectResourceList(cmd, []string{boundaryContractProjectID}); err != nil {
		t.Fatalf("runProjectResourceList: %v (paths=%v)", err, gotPaths)
	}
	if len(gotPaths) != 1 || gotPaths[0] != "GET "+wantPath {
		t.Fatalf("paths = %v, want [GET %s]", gotPaths, wantPath)
	}
}

// TestBoundary_NecessaryPathTable_DocumentsDedicatedTargets is a living map of
// necessary capabilities → dedicated paths (Ronan table). Fails if a required
// capability loses its dedicated target string (typo / table drift).
func TestBoundary_NecessaryPathTable_DocumentsDedicatedTargets(t *testing.T) {
	type row struct {
		capability string
		dedicated  []string // at least one path prefix required
	}
	table := []row{
		{"message send/react/read/search", []string{"/api/agent/messages/"}},
		{"thread unfollow", []string{"/api/agent/threads/unfollow"}},
		{"reminder suite", []string{"/api/agent/reminders/"}},
		{"channel list", []string{"/api/agent/channels"}},
		{"channel members", []string{"/api/agent/channels/"}},
		{"channel mute", []string{"/api/agent/channels/"}},
		{"issue suite", []string{"/api/agent/issues"}},
		{"project resource read", []string{"/api/agent/projects/"}},
		{"attachment view/upload", []string{"/api/agent/attachments", "/api/agent/upload-file"}},
		{"directory/workspace", []string{"/api/agent/directory", "/api/agent/workspace"}},
		{"squad set-role/activity", []string{"/api/agent/squads"}},
	}
	for _, r := range table {
		if r.capability == "" || len(r.dedicated) == 0 {
			t.Fatalf("bad row: %+v", r)
		}
		for _, p := range r.dedicated {
			if !strings.HasPrefix(p, "/api/agent/") {
				t.Fatalf("%s dedicated path %q must be under /api/agent/", r.capability, p)
			}
		}
	}
}
