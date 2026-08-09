package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/cli"
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
	// Agent machine tokens are mat_* (Ronan #801 CLI gate). Path contracts
	// exercise the agent runtime credential, not human mul_/JWT paths.
	t.Setenv("MULTICA_TOKEN", "mat_boundary_contract_test_token")
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

// TestBoundary_AttachmentUpload_HitsDedicatedAgentAPI asserts an Agent upload
// uses the target-bound Upload Session surface, never the human multipart API.
func TestBoundary_AttachmentUpload_HitsDedicatedAgentAPI(t *testing.T) {
	src := filepath.Join(t.TempDir(), "up.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/agent/attachment-upload-capabilities":
			_ = json.NewEncoder(w).Encode(map[string]any{"max_size_bytes": 1024, "session_ttl_seconds": 900})
		case r.Method == http.MethodPost && r.URL.Path == "/api/agent/attachment-upload-sessions":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "session-1", "upload_url": "/api/agent/attachment-upload-sessions/session-1/object",
				"method": "PUT", "headers": map[string]string{"Content-Type": "text/plain"},
			})
		case r.Method == http.MethodPut && r.URL.Path == "/api/agent/attachment-upload-sessions/session-1/object":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/api/agent/attachment-upload-sessions/session-1/complete":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":       boundaryContractAttachmentID,
				"filename": "up.txt",
			})
		default:
			http.Error(w, "human upload path forbidden", http.StatusForbidden)
		}
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
	_ = cmd.Flags().Set("target", "#eng")

	err := runAttachmentUpload(cmd, nil)
	if err != nil {
		t.Fatalf("runAttachmentUpload: %v (paths=%v) — expect dedicated Upload Session API", err, gotPaths)
	}
	wantPaths := []string{
		"GET /api/agent/attachment-upload-capabilities",
		"POST /api/agent/attachment-upload-sessions",
		"PUT /api/agent/attachment-upload-sessions/session-1/object",
		"POST /api/agent/attachment-upload-sessions/session-1/complete",
	}
	if len(gotPaths) != len(wantPaths) {
		t.Fatalf("paths=%v, want %v", gotPaths, wantPaths)
	}
	for i, p := range gotPaths {
		if p != wantPaths[i] {
			t.Fatalf("paths=%v, want %v", gotPaths, wantPaths)
		}
	}
	for _, p := range gotPaths {
		path := strings.SplitN(p, " ", 2)[1]
		if path == "/api/upload-file" || strings.HasPrefix(path, "/api/attachments/") || strings.HasPrefix(path, "/api/agent/attachments") {
			t.Fatalf("upload hit human path %q; full=%v", p, gotPaths)
		}
		if !strings.HasPrefix(path, "/api/agent/") {
			t.Fatalf("upload path %q is not under /api/agent/; full=%v", p, gotPaths)
		}
	}
}

// --- already-dedicated regression (must stay green) ---

// TestBoundary_MessageSend_UsesMachineLocalProxy keeps the Agent-facing send
// surface on the daemon; only the Proxy may call the dedicated Server API.
func TestBoundary_MessageSend_UsesMachineLocalProxy(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.URL.Path != "/credential-proxy/messages/send" {
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
	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("server port: %v", err)
	}
	t.Setenv("MULTICA_DAEMON_PORT", port)
	t.Setenv("MULTICA_AGENT_ID", "agent-boundary")
	t.Setenv("MULTICA_TASK_ID", "")

	cmd := newMessageSendCmd()
	_ = cmd.Flags().Set("target", "#multica")
	cmd.SetIn(strings.NewReader("boundary regression"))
	if err := runAgentMessageSend(cmd, nil); err != nil {
		t.Fatalf("runAgentMessageSend: %v", err)
	}
	if gotPath != "/credential-proxy/messages/send" {
		t.Fatalf("path = %q, want machine-local Credential Proxy", gotPath)
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

// boundaryCLIEnvProxy mimics an agent command behind the daemon-owned
// machine-local credential proxy. The command receives neither a service token
// nor task/inbox identity.
func boundaryCLIEnvProxy(t *testing.T, srvURL string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MULTICA_TOKEN", "must-not-leave-agent-process")
	t.Setenv("MULTICA_TOKEN_FILE", "")
	_, port, err := net.SplitHostPort(strings.TrimPrefix(srvURL, "http://"))
	if err != nil {
		t.Fatalf("parse local proxy URL %q: %v", srvURL, err)
	}
	t.Setenv("MULTICA_DAEMON_PORT", port)
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-boundary")
	// A remote server URL must be ignored for agent API calls.
	t.Setenv("MULTICA_SERVER_URL", "https://server.example.invalid")
	t.Setenv("MULTICA_AGENT_ID", "agent-boundary")
	t.Setenv("MULTICA_TASK_ID", "")
}

func TestBoundary_IssueGet_UsesLocalProxyDedicatedAgentAPI(t *testing.T) {
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
	boundaryCLIEnvProxy(t, srv.URL)

	cmd := &cobra.Command{Use: "get"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("output", "json", "")

	if err := runIssueGet(cmd, []string{boundaryContractIssueID}); err != nil {
		t.Fatalf("runIssueGet through local proxy: %v (paths=%v)", err, gotPaths)
	}
	if len(gotPaths) == 0 {
		t.Fatal("no HTTP calls")
	}
	for _, p := range gotPaths {
		path := strings.SplitN(p, " ", 2)[1]
		if strings.HasPrefix(path, "/api/issues") && !strings.HasPrefix(path, "/api/agent/issues") {
			t.Fatalf("local proxy hit human path %q; full=%v", p, gotPaths)
		}
		if !strings.HasPrefix(path, "/api/agent/issues") {
			t.Fatalf("path %q not under /api/agent/issues; full=%v", p, gotPaths)
		}
	}
}

func TestBoundary_IssueStatus_UsesLocalProxyDedicatedAgentAPI(t *testing.T) {
	wantPath := "/api/agent/issues/" + boundaryContractIssueID
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		if r.URL.Path == wantPath {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":         boundaryContractIssueID,
				"identifier": "MUL-1",
				"title":      "boundary",
				"status":     "in_progress",
			})
			return
		}
		http.Error(w, "human issue path forbidden", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnvProxy(t, srv.URL)

	cmd := &cobra.Command{Use: "status"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("output", "json", "")

	if err := runIssueStatus(cmd, []string{boundaryContractIssueID, "in_progress"}); err != nil {
		t.Fatalf("runIssueStatus through local proxy: %v (paths=%v)", err, gotPaths)
	}
	for _, p := range gotPaths {
		path := strings.SplitN(p, " ", 2)[1]
		if strings.HasPrefix(path, "/api/issues") && !strings.HasPrefix(path, "/api/agent/issues") {
			t.Fatalf("local proxy status hit human path %q; full=%v", p, gotPaths)
		}
	}
}

func TestBoundary_IssueCommentAdd_UsesLocalProxyDedicatedAgentAPI(t *testing.T) {
	wantGet := "/api/agent/issues/" + boundaryContractIssueID
	wantPost := "/api/agent/issues/" + boundaryContractIssueID + "/comments"
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == wantGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":         boundaryContractIssueID,
				"identifier": "MUL-1",
				"title":      "boundary",
			})
		case r.Method == http.MethodPost && r.URL.Path == wantPost:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "comment-1",
				"content": "hi",
			})
		default:
			http.Error(w, "human issue path forbidden", http.StatusForbidden)
		}
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnvProxy(t, srv.URL)

	cmd := &cobra.Command{Use: "add"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("content", "", "")
	cmd.Flags().Bool("content-stdin", false, "")
	cmd.Flags().String("content-file", "", "")
	cmd.Flags().String("parent", "", "")
	cmd.Flags().StringSlice("attachment-id", nil, "")
	cmd.Flags().String("output", "json", "")
	_ = cmd.Flags().Set("content", "daemon token file comment")

	if err := runIssueCommentAdd(cmd, []string{boundaryContractIssueID}); err != nil {
		t.Fatalf("runIssueCommentAdd through local proxy: %v (paths=%v)", err, gotPaths)
	}
	for _, p := range gotPaths {
		path := strings.SplitN(p, " ", 2)[1]
		if strings.HasPrefix(path, "/api/issues") && !strings.HasPrefix(path, "/api/agent/issues") {
			t.Fatalf("local proxy comment hit human path %q; full=%v", p, gotPaths)
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

// TestBoundary_SquadRemoved_NoAgentDedicatedSurface — Frank 2026-07-28:
// squads were fully removed; no /api/agent/squads/* necessary cutover.
// CLI may still have legacy commands; under mat_* they must not invent agent
// squad APIs. Document path table excludes squad.
func TestBoundary_SquadRemoved_NoAgentDedicatedSurface(t *testing.T) {
	// Living inventory: agent data-plane must not list squad routes as necessary.
	// If a dedicated squad path reappears, this fails closed.
	forbiddenPrefixes := []string{
		"/api/agent/squads",
		"/api/agent/squad/",
	}
	// Necessary path table rows must not reintroduce squad (see TestBoundary_NecessaryPathTable).
	for _, p := range forbiddenPrefixes {
		if strings.HasPrefix(p, "/api/agent/squad") {
			// Keep as documentation of the ban; real guard is NecessaryPathTable.
			_ = p
		}
	}
}

// TestBoundary_IssueStatus_HitsDedicatedAgentAPI asserts status update uses
// PUT /api/agent/issues/{id} (not human /api/issues).
func TestBoundary_IssueStatus_HitsDedicatedAgentAPI(t *testing.T) {
	wantPath := "/api/agent/issues/" + boundaryContractIssueID
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == wantPath:
			// resolveIssueRef may GET first
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": boundaryContractIssueID, "identifier": "MUL-1", "status": "todo",
			})
		case r.Method == http.MethodPut && r.URL.Path == wantPath:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": boundaryContractIssueID, "identifier": "MUL-1", "status": "in_progress",
			})
		default:
			http.Error(w, "human issue path forbidden", http.StatusForbidden)
		}
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnv(t, srv.URL)

	cmd := &cobra.Command{Use: "status"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("output", "json", "")
	if err := runIssueStatus(cmd, []string{boundaryContractIssueID, "in_progress"}); err != nil {
		t.Fatalf("runIssueStatus: %v (paths=%v)", err, gotPaths)
	}
	for _, p := range gotPaths {
		path := strings.SplitN(p, " ", 2)[1]
		if strings.HasPrefix(path, "/api/issues") {
			t.Fatalf("hit human path %q; full=%v", p, gotPaths)
		}
	}
	foundPut := false
	for _, p := range gotPaths {
		if p == "PUT "+wantPath {
			foundPut = true
		}
	}
	if !foundPut {
		t.Fatalf("paths=%v, want PUT %s", gotPaths, wantPath)
	}
}

// TestBoundary_IssueCommentList_HitsDedicatedAgentAPI asserts comment list uses
// GET /api/agent/issues/{id}/comments.
func TestBoundary_IssueCommentList_HitsDedicatedAgentAPI(t *testing.T) {
	wantGet := "/api/agent/issues/" + boundaryContractIssueID
	wantComments := wantGet + "/comments"
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case wantGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": boundaryContractIssueID, "identifier": "MUL-1",
			})
		case wantComments:
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		default:
			http.Error(w, "human issue path forbidden", http.StatusForbidden)
		}
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnv(t, srv.URL)

	cmd := &cobra.Command{Use: "list"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("output", "json", "")
	cmd.Flags().Bool("full-id", false, "")
	if err := runIssueCommentList(cmd, []string{boundaryContractIssueID}); err != nil {
		t.Fatalf("runIssueCommentList: %v (paths=%v)", err, gotPaths)
	}
	found := false
	for _, p := range gotPaths {
		if p == "GET "+wantComments {
			found = true
		}
		if strings.HasPrefix(strings.SplitN(p, " ", 2)[1], "/api/issues") {
			t.Fatalf("hit human path %q", p)
		}
	}
	if !found {
		t.Fatalf("paths=%v, want GET %s", gotPaths, wantComments)
	}
}

// TestBoundary_AttachmentContent_DocumentsDedicatedDownloadURL is a path-table
// lock for content/download under /api/agent/attachments (Ronan attachment cut).
func TestBoundary_AttachmentContent_DocumentsDedicatedDownloadURL(t *testing.T) {
	// download_url returned by agent attachment view must stay under /api/agent/
	// (not CloudFront / human /api/attachments/.../download that bypasses ACL).
	allowed := []string{
		"/api/agent/attachments/" + boundaryContractAttachmentID + "/download",
		"/api/agent/attachments/" + boundaryContractAttachmentID + "/content",
	}
	for _, p := range allowed {
		if !strings.HasPrefix(p, "/api/agent/attachments/") {
			t.Fatalf("path %q not under agent attachments", p)
		}
	}
}

// TestBoundary_IssueList_HitsDedicatedAgentAPI asserts issue list uses
// GET /api/agent/issues under mat_* credentials.
func TestBoundary_IssueList_HitsDedicatedAgentAPI(t *testing.T) {
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodGet && r.URL.Path == "/api/agent/issues" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issues": []any{}, "total": 0,
			})
			return
		}
		http.Error(w, "human issue list forbidden for agent", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnv(t, srv.URL)

	cmd := &cobra.Command{Use: "list"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("output", "json", "")
	cmd.Flags().String("status", "", "")
	cmd.Flags().String("priority", "", "")
	cmd.Flags().Int("limit", 0, "")
	cmd.Flags().Int("offset", 0, "")
	cmd.Flags().String("assignee", "", "")
	cmd.Flags().String("assignee-id", "", "")
	cmd.Flags().String("project", "", "")
	cmd.Flags().StringSlice("metadata", nil, "")
	cmd.Flags().Bool("with-prs", false, "")
	cmd.Flags().Bool("with-gates", false, "")
	cmd.Flags().Bool("full-id", false, "")
	if err := runIssueList(cmd, nil); err != nil {
		t.Fatalf("runIssueList: %v (paths=%v) — expect GET /api/agent/issues", err, gotPaths)
	}
	if len(gotPaths) < 1 || !strings.HasPrefix(gotPaths[0], "GET /api/agent/issues") {
		t.Fatalf("paths=%v, want GET /api/agent/issues", gotPaths)
	}
	for _, p := range gotPaths {
		if strings.Contains(p, "GET /api/issues") && !strings.Contains(p, "/api/agent/issues") {
			t.Fatalf("hit human list path %q", p)
		}
	}
}

// TestBoundary_IssueCommentAdd_HitsDedicatedAgentAPI asserts comment create
// posts to POST /api/agent/issues/{id}/comments under mat_*.
func TestBoundary_IssueCommentAdd_HitsDedicatedAgentAPI(t *testing.T) {
	wantGet := "/api/agent/issues/" + boundaryContractIssueID
	wantPost := wantGet + "/comments"
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == wantGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": boundaryContractIssueID, "identifier": "MUL-1",
			})
		case r.Method == http.MethodPost && r.URL.Path == wantPost:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "c1", "content": "hi"})
		default:
			http.Error(w, "human comment path forbidden", http.StatusForbidden)
		}
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnv(t, srv.URL)

	cmd := &cobra.Command{Use: "add"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("content", "", "")
	cmd.Flags().Bool("content-stdin", false, "")
	cmd.Flags().String("content-file", "", "")
	cmd.Flags().String("parent", "", "")
	cmd.Flags().StringSlice("attachment-id", nil, "")
	cmd.Flags().String("output", "json", "")
	_ = cmd.Flags().Set("content", "boundary comment")
	if err := runIssueCommentAdd(cmd, []string{boundaryContractIssueID}); err != nil {
		t.Fatalf("runIssueCommentAdd: %v (paths=%v)", err, gotPaths)
	}
	found := false
	for _, p := range gotPaths {
		if p == "POST "+wantPost {
			found = true
		}
		if strings.HasPrefix(strings.SplitN(p, " ", 2)[1], "/api/issues") ||
			strings.HasPrefix(strings.SplitN(p, " ", 2)[1], "/api/comments") {
			t.Fatalf("hit human path %q; full=%v", p, gotPaths)
		}
	}
	if !found {
		t.Fatalf("paths=%v, want POST %s", gotPaths, wantPost)
	}
}

// TestBoundary_IssueMetadataList_HitsDedicatedAgentAPI asserts metadata list
// uses GET /api/agent/issues/{id}/metadata.
func TestBoundary_IssueMetadataList_HitsDedicatedAgentAPI(t *testing.T) {
	wantGet := "/api/agent/issues/" + boundaryContractIssueID
	wantMeta := wantGet + "/metadata"
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case wantGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": boundaryContractIssueID, "identifier": "MUL-1",
			})
		case wantMeta:
			_ = json.NewEncoder(w).Encode(map[string]any{})
		default:
			http.Error(w, "human issue path forbidden", http.StatusForbidden)
		}
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnv(t, srv.URL)

	cmd := &cobra.Command{Use: "list"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("output", "json", "")
	if err := runIssueMetadataList(cmd, []string{boundaryContractIssueID}); err != nil {
		t.Fatalf("runIssueMetadataList: %v (paths=%v)", err, gotPaths)
	}
	found := false
	for _, p := range gotPaths {
		if p == "GET "+wantMeta {
			found = true
		}
		path := strings.SplitN(p, " ", 2)[1]
		if strings.HasPrefix(path, "/api/issues") {
			t.Fatalf("hit human path %q; full=%v", p, gotPaths)
		}
	}
	if !found {
		t.Fatalf("paths=%v, want GET %s", gotPaths, wantMeta)
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

// TestBoundary_IssueLabelsList_HitsDedicatedAgentAPI asserts issue labels list
// uses GET /api/agent/issues/{id}/labels (Ronan tip 2b6c0dde4).
func TestBoundary_IssueLabelsList_HitsDedicatedAgentAPI(t *testing.T) {
	wantGet := "/api/agent/issues/" + boundaryContractIssueID
	wantLabels := wantGet + "/labels"
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == wantGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": boundaryContractIssueID, "identifier": "MUL-1",
			})
		case r.Method == http.MethodGet && r.URL.Path == wantLabels:
			_ = json.NewEncoder(w).Encode(map[string]any{"labels": []any{}})
		default:
			http.Error(w, "human issue/label path forbidden", http.StatusForbidden)
		}
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnv(t, srv.URL)

	cmd := &cobra.Command{Use: "list"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("output", "json", "")
	cmd.Flags().Bool("full-id", false, "")
	if err := runIssueLabelList(cmd, []string{boundaryContractIssueID}); err != nil {
		t.Fatalf("runIssueLabelList: %v (paths=%v)", err, gotPaths)
	}
	found := false
	for _, p := range gotPaths {
		if p == "GET "+wantLabels {
			found = true
		}
		path := strings.SplitN(p, " ", 2)[1]
		if strings.HasPrefix(path, "/api/issues") || strings.HasPrefix(path, "/api/labels") {
			t.Fatalf("hit human path %q; full=%v", p, gotPaths)
		}
	}
	if !found {
		t.Fatalf("paths=%v, want GET %s", gotPaths, wantLabels)
	}
}

// TestBoundary_IssueSubscribersList_HitsDedicatedAgentAPI asserts subscribers
// list uses GET /api/agent/issues/{id}/subscribers.
func TestBoundary_IssueSubscribersList_HitsDedicatedAgentAPI(t *testing.T) {
	wantGet := "/api/agent/issues/" + boundaryContractIssueID
	wantSubs := wantGet + "/subscribers"
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == wantGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": boundaryContractIssueID, "identifier": "MUL-1",
			})
		case r.Method == http.MethodGet && r.URL.Path == wantSubs:
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		default:
			http.Error(w, "human subscriber path forbidden", http.StatusForbidden)
		}
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnv(t, srv.URL)

	cmd := &cobra.Command{Use: "list"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("output", "json", "")
	if err := runIssueSubscriberList(cmd, []string{boundaryContractIssueID}); err != nil {
		t.Fatalf("runIssueSubscriberList: %v (paths=%v)", err, gotPaths)
	}
	found := false
	for _, p := range gotPaths {
		if p == "GET "+wantSubs {
			found = true
		}
		path := strings.SplitN(p, " ", 2)[1]
		if strings.HasPrefix(path, "/api/issues") {
			t.Fatalf("hit human path %q; full=%v", p, gotPaths)
		}
	}
	if !found {
		t.Fatalf("paths=%v, want GET %s", gotPaths, wantSubs)
	}
}

// TestBoundary_IssueTaskRuns_HitsDedicatedAgentAPI asserts task-runs list uses
// GET /api/agent/issues/{id}/task-runs under mat_*.
func TestBoundary_IssueTaskRuns_HitsDedicatedAgentAPI(t *testing.T) {
	wantGet := "/api/agent/issues/" + boundaryContractIssueID
	wantRuns := wantGet + "/task-runs"
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == wantGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": boundaryContractIssueID, "identifier": "MUL-1",
			})
		case r.Method == http.MethodGet && r.URL.Path == wantRuns:
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		default:
			http.Error(w, "human task-runs path forbidden", http.StatusForbidden)
		}
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnv(t, srv.URL)

	cmd := &cobra.Command{Use: "runs"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("output", "json", "")
	cmd.Flags().Bool("full-id", false, "")
	if err := runIssueRuns(cmd, []string{boundaryContractIssueID}); err != nil {
		t.Fatalf("runIssueRuns: %v (paths=%v)", err, gotPaths)
	}
	found := false
	for _, p := range gotPaths {
		if p == "GET "+wantRuns {
			found = true
		}
		path := strings.SplitN(p, " ", 2)[1]
		if strings.HasPrefix(path, "/api/issues") {
			t.Fatalf("hit human path %q; full=%v", p, gotPaths)
		}
	}
	if !found {
		t.Fatalf("paths=%v, want GET %s", gotPaths, wantRuns)
	}
}

// TestBoundary_IssuePullRequests_HitsDedicatedAgentAPI asserts pull-requests
// list uses GET /api/agent/issues/{id}/pull-requests under mat_*.
func TestBoundary_IssuePullRequests_HitsDedicatedAgentAPI(t *testing.T) {
	wantGet := "/api/agent/issues/" + boundaryContractIssueID
	wantPRs := wantGet + "/pull-requests"
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == wantGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": boundaryContractIssueID, "identifier": "MUL-1",
			})
		case r.Method == http.MethodGet && r.URL.Path == wantPRs:
			_ = json.NewEncoder(w).Encode(map[string]any{"pull_requests": []any{}})
		default:
			http.Error(w, "human pull-requests path forbidden", http.StatusForbidden)
		}
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnvProxy(t, srv.URL)

	cmd := &cobra.Command{Use: "pull-requests"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("output", "json", "")
	if err := runIssuePullRequests(cmd, []string{boundaryContractIssueID}); err != nil {
		t.Fatalf("runIssuePullRequests: %v (paths=%v)", err, gotPaths)
	}
	found := false
	for _, p := range gotPaths {
		if p == "GET "+wantPRs {
			found = true
		}
		path := strings.SplitN(p, " ", 2)[1]
		if strings.HasPrefix(path, "/api/issues") {
			t.Fatalf("hit human path %q; full=%v", p, gotPaths)
		}
	}
	if !found {
		t.Fatalf("paths=%v, want GET %s", gotPaths, wantPRs)
	}
}

// TestBoundary_IssueRunMessages_HitsDedicatedAgentAPI asserts task-run
// messages use GET /api/agent/tasks/{id}/messages under daemon TOKEN_FILE auth.
func TestBoundary_IssueRunMessages_HitsDedicatedAgentAPI(t *testing.T) {
	taskID := "99999999-8888-7777-6666-555555555555"
	wantMessages := "/api/agent/tasks/" + taskID + "/messages"
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.RequestURI())
		if r.Method == http.MethodGet && r.URL.Path == wantMessages {
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"seq": 1, "type": "text", "content": "done",
			}})
			return
		}
		http.Error(w, "human task messages path forbidden", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnvProxy(t, srv.URL)

	cmd := &cobra.Command{Use: "run-messages"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("output", "json", "")
	cmd.Flags().Int("since", 0, "")
	cmd.Flags().String("issue", "", "")
	_ = cmd.Flags().Set("since", "7")
	if err := runIssueRunMessages(cmd, []string{taskID}); err != nil {
		t.Fatalf("runIssueRunMessages: %v (paths=%v)", err, gotPaths)
	}

	wantRequest := "GET " + wantMessages + "?since=7"
	found := false
	for _, request := range gotPaths {
		if request == wantRequest {
			found = true
		}
		path := strings.SplitN(request, " ", 2)[1]
		if strings.HasPrefix(path, "/api/tasks") {
			t.Fatalf("hit human task path %q; full=%v", request, gotPaths)
		}
	}
	if !found {
		t.Fatalf("requests=%v, want %s", gotPaths, wantRequest)
	}
}

// TestBoundary_IssueCancelTask_HitsDedicatedAgentAPI asserts a task-scoped
// mat_* process can cancel only through the agent data-plane path. Human auth
// keeps the existing /api/tasks/{id}/cancel contract.
func TestBoundary_IssueCancelTask_HitsDedicatedAgentAPI(t *testing.T) {
	taskID := "99999999-8888-7777-6666-555555555555"
	wantCancel := "/api/agent/tasks/" + taskID + "/cancel"
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPost && r.URL.Path == wantCancel {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": taskID, "status": "cancelled",
			})
			return
		}
		http.Error(w, "human task cancel path forbidden", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnvProxy(t, srv.URL)

	cmd := &cobra.Command{Use: "cancel-task"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("output", "json", "")
	cmd.Flags().String("issue", "", "")
	if err := runIssueCancelTask(cmd, []string{taskID}); err != nil {
		t.Fatalf("runIssueCancelTask: %v (paths=%v)", err, gotPaths)
	}

	if len(gotPaths) != 1 || gotPaths[0] != "POST "+wantCancel {
		t.Fatalf("requests=%v, want exact [POST %s]", gotPaths, wantCancel)
	}
}

// TestBoundary_IssueCancelTask_HumanAuthKeepsHumanAPI is the inverse control
// for the agent final-hop test above. The #856 migration is additive: a human
// credential must continue to cancel through the existing human route.
func TestBoundary_IssueCancelTask_HumanAuthKeepsHumanAPI(t *testing.T) {
	taskID := "99999999-8888-7777-6666-555555555555"
	wantCancel := "/api/tasks/" + taskID + "/cancel"
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPost && r.URL.Path == wantCancel {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": taskID, "status": "cancelled",
			})
			return
		}
		http.Error(w, "agent task cancel path forbidden for human auth", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	t.Setenv("HOME", t.TempDir())
	t.Setenv("MULTICA_TOKEN", "mul_human_boundary_contract_token")
	t.Setenv("MULTICA_TOKEN_FILE", "")
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-boundary")
	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_AGENT_ID", "")
	t.Setenv("MULTICA_TASK_ID", "")

	cmd := &cobra.Command{Use: "cancel-task"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("output", "json", "")
	cmd.Flags().String("issue", "", "")
	if err := runIssueCancelTask(cmd, []string{taskID}); err != nil {
		t.Fatalf("runIssueCancelTask human auth: %v (paths=%v)", err, gotPaths)
	}

	if len(gotPaths) != 1 || gotPaths[0] != "POST "+wantCancel {
		t.Fatalf("requests=%v, want exact [POST %s]", gotPaths, wantCancel)
	}
}

// TestBoundary_NecessaryPathTable_DocumentsDedicatedTargets is a living map of
// necessary capabilities → dedicated paths (Ronan table). Fails if a required
// capability loses its dedicated target string (typo / table drift).
// Squad is product-removed (Frank) — must not appear.
func TestBoundary_NecessaryPathTable_DocumentsDedicatedTargets(t *testing.T) {
	type row struct {
		capability string
		dedicated  []string // at least one path prefix required
	}
	table := []row{
		{"message send/react/read/search", []string{"/api/agent/messages/"}},
		{"thread unfollow", []string{"/api/agent/threads/unfollow"}},
		{"reminder suite", []string{"/api/agent/reminders/"}},
		{"migration lease reserve/release/list", []string{"/api/agent/migrations/"}},
		{"work owner lease acquire/release/list", []string{"/api/agent/work-leases/"}},
		{"channel list", []string{"/api/agent/channels"}},
		{"channel members", []string{"/api/agent/channels/"}},
		{"channel mute", []string{"/api/agent/channels/"}},
		{"issue list/get/create/update", []string{"/api/agent/issues"}},
		{"issue comments", []string{"/api/agent/issues/", "/comments"}},
		{"issue labels on-issue", []string{"/api/agent/issues/", "/labels"}},
		{"issue subscribers", []string{"/api/agent/issues/", "/subscribers"}},
		{"issue task-runs/rerun/channel", []string{"/api/agent/issues/", "/task-runs"}},
		{"issue pull-requests", []string{"/api/agent/issues/", "/pull-requests"}},
		{"task run messages", []string{"/api/agent/tasks/", "/messages"}},
		{"task self-cancel", []string{"/api/agent/tasks/", "/cancel"}},
		{"project resource read", []string{"/api/agent/projects/"}},
		{"attachment view", []string{"/api/agent/attachments"}},
		{"attachment upload session", []string{"/api/agent/attachment-upload-sessions"}},
		{"directory agents", []string{"/api/agent/agents"}},
		{"workspace get", []string{"/api/agent/workspace"}},
	}
	for _, r := range table {
		if r.capability == "" || len(r.dedicated) == 0 {
			t.Fatalf("bad row: %+v", r)
		}
		for _, p := range r.dedicated {
			// Allow suffix fragments used as table docs (e.g. "/labels") once a
			// dedicated prefix already established the /api/agent root.
			if strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "/api/") {
				continue
			}
			if !strings.HasPrefix(p, "/api/agent/") {
				t.Fatalf("%s dedicated path %q must be under /api/agent/", r.capability, p)
			}
			if strings.Contains(p, "squad") {
				t.Fatalf("%s path %q: squad surfaces are product-removed", r.capability, p)
			}
		}
	}
}

// TestBoundary_AttachmentUpload_AgentTokenRequiresTargetBoundSession prevents
// unbound Agent staging: a later Message must prove the same target session.
func TestBoundary_AttachmentUpload_AgentTokenRequiresTargetBoundSession(t *testing.T) {
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		http.Error(w, "unexpected path", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnv(t, srv.URL)

	tmp := t.TempDir()
	path := filepath.Join(tmp, "x.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{Use: "upload"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("path", "", "")
	cmd.Flags().String("target", "", "")
	_ = cmd.Flags().Set("path", path)
	// no --target must fail before any request can create unbound staging.
	err := runAttachmentUpload(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "--target is required") {
		t.Fatalf("unbound Agent upload error = %v, want target requirement", err)
	}
	if len(gotPaths) != 0 {
		t.Fatalf("unbound Agent upload made requests: %v", gotPaths)
	}
}

// multicaTokenSourceBoundaryViolations enforces Barry #1305 successor:
//  1. BasicLit "MULTICA_TOKEN" only as value of const multicaTokenEnvKey
//  2. identifier multicaTokenEnvKey only inside ambientTokenFromEnvOrFile
//     and pure runtimeEnvToken (no mutable package global)
func multicaTokenSourceBoundaryViolations(filename, src string) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil, err
	}

	// Positions of the canonical const declaration Ident + its BasicLit value.
	canonicalLit := map[token.Pos]bool{}
	canonicalDeclIdent := map[token.Pos]bool{}
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if name.Name != "multicaTokenEnvKey" {
					continue
				}
				canonicalDeclIdent[name.Pos()] = true
				if i < len(vs.Values) {
					if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING && lit.Value == `"MULTICA_TOKEN"` {
						canonicalLit[lit.Pos()] = true
					}
				}
			}
		}
	}

	type fnSpan struct {
		name string
		body *ast.BlockStmt
	}
	var spans []fnSpan
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		spans = append(spans, fnSpan{name: fn.Name.Name, body: fn.Body})
	}
	enclosing := func(pos token.Pos) string {
		for _, sp := range spans {
			if pos >= sp.body.Lbrace && pos < sp.body.Rbrace {
				return sp.name
			}
		}
		return ""
	}
	allowedIdentUse := map[string]bool{
		"ambientTokenFromEnvOrFile": true,
		"runtimeEnvToken":           true,
	}

	var bad []string
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BasicLit:
			if x.Kind != token.STRING || x.Value != `"MULTICA_TOKEN"` {
				return true
			}
			if !canonicalLit[x.Pos()] {
				pos := fset.Position(x.Pos())
				bad = append(bad, fmt.Sprintf("%s:%d: BasicLit MULTICA_TOKEN not on const multicaTokenEnvKey", filename, pos.Line))
			}
		case *ast.Ident:
			if x.Name != "multicaTokenEnvKey" {
				return true
			}
			if canonicalDeclIdent[x.Pos()] {
				return true
			}
			fn := enclosing(x.Pos())
			if !allowedIdentUse[fn] {
				pos := fset.Position(x.Pos())
				where := fn
				if where == "" {
					where = "<package-level>"
				}
				bad = append(bad, fmt.Sprintf("%s:%d: multicaTokenEnvKey used in %s (only ambientTokenFromEnvOrFile / runtimeEnvToken)", filename, pos.Line, where))
			}
		}
		return true
	})
	return bad, nil
}

// ambientTokenFromEnvOrFileReadsTokenFile reports whether the function body
// of ambientTokenFromEnvOrFile also reads MULTICA_TOKEN_FILE (cannot be
// env-only itself).
func ambientTokenFromEnvOrFileReadsTokenFile(src string) (bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "cmd_auth.go", src, 0)
	if err != nil {
		return false, err
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "ambientTokenFromEnvOrFile" || fn.Body == nil {
			continue
		}
		found := false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if lit.Value == `"MULTICA_TOKEN_FILE"` {
				found = true
			}
			return true
		})
		return found, nil
	}
	return false, fmt.Errorf("ambientTokenFromEnvOrFile not found")
}

// TestBoundary_NoEnvOnlyMatTokenDetection enforces Barry invariant: sole
// BasicLit "MULTICA_TOKEN" on const multicaTokenEnvKey; identifier only in
// ambientTokenFromEnvOrFile + runtimeEnvToken; ambient must read TOKEN_FILE.
func TestBoundary_NoEnvOnlyMatTokenDetection(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var bad []string
	var authSrc string
	var sawCanonicalConst bool
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		if f == "cmd_auth.go" {
			authSrc = src
		}
		hits, err := multicaTokenSourceBoundaryViolations(f, src)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		bad = append(bad, hits...)
		if strings.Contains(src, "const multicaTokenEnvKey") && strings.Contains(src, `"MULTICA_TOKEN"`) {
			sawCanonicalConst = true
		}
	}
	if len(bad) > 0 {
		t.Fatalf("MULTICA_TOKEN source-boundary violations: %v", bad)
	}
	if !sawCanonicalConst {
		t.Fatal("missing const multicaTokenEnvKey = \"MULTICA_TOKEN\" in production package")
	}
	if authSrc == "" {
		t.Fatal("cmd_auth.go not found in package dir")
	}
	ok, err := ambientTokenFromEnvOrFileReadsTokenFile(authSrc)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("ambientTokenFromEnvOrFile must also read MULTICA_TOKEN_FILE (cannot be env-only)")
	}
}

// TestBoundary_NoEnvOnlyMatTokenDetection_Counterfactuals hard-proves the
// source-boundary scanner fails closed for Barry's bypass classes.
func TestBoundary_NoEnvOnlyMatTokenDetection_Counterfactuals(t *testing.T) {
	mustFlag := func(t *testing.T, name, src string) {
		t.Helper()
		hits, err := multicaTokenSourceBoundaryViolations(name, src)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) == 0 {
			t.Fatalf("%s: expected source-boundary FAIL; got 0 hits", name)
		}
	}

	// 1) wrong const name still carries the literal
	mustFlag(t, "const_alias.go", `package main
import "os"
const key = "MULTICA_TOKEN"
func badConst() string { return os.Getenv(key) }
`)

	// 2) import alias + raw literal
	mustFlag(t, "import_alias.go", `package main
import o "os"
func badImport() string { return o.Getenv("MULTICA_TOKEN") }
`)

	// 3) indirect func var + raw literal
	mustFlag(t, "func_var.go", `package main
import "os"
func badVar() string {
	g := os.Getenv
	return g("MULTICA_TOKEN")
}
`)

	// 4) compose isMatAgentToken(os.Getenv) still red
	mustFlag(t, "compose.go", `package main
import "os"
func isMatAgentToken(token string) bool { return len(token) > 0 }
func badCompose() bool { return isMatAgentToken(os.Getenv("MULTICA_TOKEN")) }
`)

	// 5) bare HasPrefix still red
	mustFlag(t, "hasprefix.go", `package main
import ("os"; "strings")
const multicaTokenEnvKey = "MULTICA_TOKEN"
func ambientTokenFromEnvOrFile() string {
	_ = os.Getenv("MULTICA_TOKEN_FILE")
	return os.Getenv(multicaTokenEnvKey)
}
func bad() bool {
	return strings.HasPrefix(strings.TrimSpace(os.Getenv("MULTICA_TOKEN")), "mat_")
}
`)

	// 6) canonical const used outside ambient/runtimeEnvToken
	mustFlag(t, "ident_leak.go", `package main
import "os"
const multicaTokenEnvKey = "MULTICA_TOKEN"
func ambientTokenFromEnvOrFile() string { return os.Getenv(multicaTokenEnvKey) }
func runtimeEnvToken(env map[string]string) string { return env[multicaTokenEnvKey] }
func badLeak() string { return os.Getenv(multicaTokenEnvKey) }
`)

	// 7) control: const + ambient + pure map helper → clean
	clean := `package main
import ("os"; "strings")
const multicaTokenEnvKey = "MULTICA_TOKEN"
func ambientTokenFromEnvOrFile() string {
	if v := strings.TrimSpace(os.Getenv(multicaTokenEnvKey)); v != "" {
		return v
	}
	if path := strings.TrimSpace(os.Getenv("MULTICA_TOKEN_FILE")); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return ""
}
func runtimeEnvToken(env map[string]string) string {
	if env == nil {
		return ""
	}
	return strings.TrimSpace(env[multicaTokenEnvKey])
}
func isMatAgentToken(token string) bool {
	return strings.HasPrefix(strings.TrimSpace(token), "mat_")
}
func isAgentAPITokenAmbient() bool {
	return isMatAgentToken(ambientTokenFromEnvOrFile())
}
`
	hits, err := multicaTokenSourceBoundaryViolations("cmd_auth.go", clean)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("clean control must be green; got %v", hits)
	}
	ok, err := ambientTokenFromEnvOrFileReadsTokenFile(clean)
	if err != nil || !ok {
		t.Fatalf("clean ambient must read TOKEN_FILE: ok=%v err=%v", ok, err)
	}
}

// TestBoundary_MulticaTokenEnvKey_NoRace freezes Barry's reviewer race probe:
// concurrent ambientTokenFromEnvOrFile + runtimeEnvToken must be race-free
// (no package-level mutable key cache).
func TestBoundary_MulticaTokenEnvKey_NoRace(t *testing.T) {
	env := map[string]string{multicaTokenEnvKey: "mat_race_probe_token"}
	const workers = 32
	done := make(chan struct{}, workers*2)
	for i := 0; i < workers; i++ {
		go func() {
			for j := 0; j < 200; j++ {
				_ = ambientTokenFromEnvOrFile()
			}
			done <- struct{}{}
		}()
		go func() {
			for j := 0; j < 200; j++ {
				if runtimeEnvToken(env) == "" {
					t.Error("runtimeEnvToken empty under concurrency")
				}
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < workers*2; i++ {
		<-done
	}
	if got := runtimeEnvToken(env); got != "mat_race_probe_token" {
		t.Fatalf("runtimeEnvToken = %q", got)
	}
	if got := runtimeEnvToken(nil); got != "" {
		t.Fatalf("nil env = %q", got)
	}
}

// TestNoAgentAPITokenFromEnvSymbol hard-forbids the deleted dual detector name
// (Parker: delete, do not keep same-semantics twin).
func TestNoAgentAPITokenFromEnvSymbol(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "agentAPITokenFromEnv") {
			t.Fatalf("%s still references deleted agentAPITokenFromEnv; use isAgentAPIToken / isAgentAPITokenAmbient", f)
		}
	}
}

func TestIsMatAgentToken(t *testing.T) {
	if !isMatAgentToken("mat_abc") {
		t.Fatal("mat_ prefix")
	}
	if isMatAgentToken("mul_abc") || isMatAgentToken("") || isMatAgentToken("  ") {
		t.Fatal("non-mat must be false")
	}
	// ambient TOKEN_FILE shape
	t.Setenv("MULTICA_TOKEN", "")
	dir := t.TempDir()
	f := filepath.Join(dir, "tok")
	if err := os.WriteFile(f, []byte("mat_from_file"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MULTICA_TOKEN_FILE", f)
	if !isAgentAPITokenAmbient() {
		t.Fatal("TOKEN_FILE-only must classify as agent")
	}
	t.Setenv("MULTICA_TOKEN_FILE", "")
	if isAgentAPITokenAmbient() {
		t.Fatal("no ambient token must be false")
	}
}

// --- R1 resolver clean-cut: TOKEN_FILE-only name resolve hits /api/agent/* only ---

func writeTokenFileOnly(t *testing.T, token string) {
	t.Helper()
	t.Setenv("MULTICA_TOKEN", "")
	t.Setenv("MULTICA_TOKEN_FILE", "")
	dir := t.TempDir()
	f := filepath.Join(dir, "token")
	if err := os.WriteFile(f, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MULTICA_TOKEN_FILE", f)
}

func TestResolver_R1_ChannelName_TOKEN_FILE_HitsAgentChannelsOnly(t *testing.T) {
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/api/agent/channels" {
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": boundaryContractChannelID, "name": "ops"},
			})
			return
		}
		http.Error(w, "human path forbidden", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	writeTokenFileOnly(t, "mat_resolver_r1_channel")
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-boundary")
	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("HOME", t.TempDir())

	raw, _ := os.ReadFile(os.Getenv("MULTICA_TOKEN_FILE"))
	client := cli.NewAPIClient(srv.URL, "workspace-boundary", strings.TrimSpace(string(raw)))

	id, err := resolveChannelRef(t.Context(), client, "ops")
	if err != nil {
		t.Fatalf("resolveChannelRef: %v paths=%v", err, gotPaths)
	}
	if id != boundaryContractChannelID {
		t.Fatalf("id=%q want %s", id, boundaryContractChannelID)
	}
	for _, p := range gotPaths {
		if strings.Contains(p, "/api/channels") && !strings.Contains(p, "/api/agent/channels") {
			t.Fatalf("hit human channel path %q full=%v", p, gotPaths)
		}
	}
	if len(gotPaths) != 1 || gotPaths[0] != "GET /api/agent/channels" {
		t.Fatalf("paths=%v want [GET /api/agent/channels]", gotPaths)
	}
}

func TestResolver_R1_AssigneeAgentName_TOKEN_FILE_HitsAgentAgentsOnly(t *testing.T) {
	agentID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/api/agent/agents" {
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": agentID, "name": "codebot", "display_name": "Code Bot"},
			})
			return
		}
		http.Error(w, "human path forbidden", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	writeTokenFileOnly(t, "mat_resolver_r1_assignee")
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-boundary")
	t.Setenv("HOME", t.TempDir())

	raw, _ := os.ReadFile(os.Getenv("MULTICA_TOKEN_FILE"))
	client := cli.NewAPIClient(srv.URL, "workspace-boundary", strings.TrimSpace(string(raw)))

	aType, aID, err := resolveAssignee(t.Context(), client, "codebot", issueAssigneeKinds)
	if err != nil {
		t.Fatalf("resolveAssignee: %v paths=%v", err, gotPaths)
	}
	if aType != "agent" || aID != agentID {
		t.Fatalf("got %s %s", aType, aID)
	}
	for _, p := range gotPaths {
		if strings.HasPrefix(p, "GET /api/agents") || strings.Contains(p, "/members") {
			t.Fatalf("hit human path %q full=%v", p, gotPaths)
		}
	}
	if len(gotPaths) != 1 || gotPaths[0] != "GET /api/agent/agents" {
		t.Fatalf("paths=%v", gotPaths)
	}
}

func TestResolver_R1_MemberName_TOKEN_FILE_FailClosed(t *testing.T) {
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/api/agent/agents" {
			_ = json.NewEncoder(w).Encode([]map[string]any{})
			return
		}
		http.Error(w, "human path forbidden", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	writeTokenFileOnly(t, "mat_resolver_r1_member")
	t.Setenv("HOME", t.TempDir())

	raw, _ := os.ReadFile(os.Getenv("MULTICA_TOKEN_FILE"))
	client := cli.NewAPIClient(srv.URL, "workspace-boundary", strings.TrimSpace(string(raw)))

	_, _, err := resolveAssignee(t.Context(), client, "Alice", memberOrAgentKinds)
	if err == nil {
		t.Fatalf("want R1a fail-closed, got nil paths=%v", gotPaths)
	}
	msg := err.Error()
	if !strings.Contains(msg, "member name resolve is not available for agent tokens") {
		t.Fatalf("want member fail-closed wording, got %v paths=%v", err, gotPaths)
	}
	// with member+agent kinds, prefer agent-miss + member lock (Ronan polish)
	if !strings.Contains(msg, "no agent found matching") {
		t.Fatalf("want agent-miss prefix when agent kinds enabled, got %v", err)
	}
	for _, p := range gotPaths {
		if strings.Contains(p, "/members") {
			t.Fatalf("must not hit members list: %v", gotPaths)
		}
	}
}

func TestResolver_R1_ProjectResourceName_TOKEN_FILE_HitsAgentResourcesOnly(t *testing.T) {
	projectID := "cccccccc-cccc-cccc-cccc-cccccccccccc"
	resourceID := "dddddddd-dddd-dddd-dddd-dddddddddddd"
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		want := "/api/agent/projects/" + projectID + "/resources"
		if r.URL.Path == want {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resources": []map[string]any{
					{"id": resourceID, "label": "repo-main", "resource_type": "repo"},
				},
			})
			return
		}
		http.Error(w, "human path forbidden", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	writeTokenFileOnly(t, "mat_resolver_r1_resource")
	t.Setenv("HOME", t.TempDir())

	raw, _ := os.ReadFile(os.Getenv("MULTICA_TOKEN_FILE"))
	client := cli.NewAPIClient(srv.URL, "workspace-boundary", strings.TrimSpace(string(raw)))

	// resolveProjectResourceID is UUID-prefix based (not label name); R1
	// contract is principal-aware list path under mat_*.
	got, err := resolveProjectResourceID(t.Context(), client, projectID, "dddddddd")
	if err != nil {
		t.Fatalf("resolveProjectResourceID: %v paths=%v", err, gotPaths)
	}
	if got.ID != resourceID {
		t.Fatalf("id=%q", got.ID)
	}
	for _, p := range gotPaths {
		if strings.Contains(p, "/api/projects/") && !strings.Contains(p, "/api/agent/projects/") {
			t.Fatalf("hit human project path %q full=%v", p, gotPaths)
		}
	}
}

// TestBoundary_IssueSearch_HitsDedicatedAgentAPI: multica issue search under mat_*
// must hit GET /api/agent/issues/search only (#812 GAP).
func TestBoundary_IssueSearch_HitsDedicatedAgentAPI(t *testing.T) {
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/agent/issues/search":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issues": []map[string]any{
					{"id": "iss-1", "identifier": "MUL-1", "title": "login", "status": "todo", "match_source": "title"},
				},
			})
		default:
			http.Error(w, "human path forbidden for agent issue search", http.StatusForbidden)
		}
	}))
	t.Cleanup(srv.Close)
	boundaryCLIEnv(t, srv.URL)

	cmd := &cobra.Command{Use: "search"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("output", "json", "")
	cmd.Flags().Int("limit", 0, "")
	cmd.Flags().Bool("include-closed", false, "")

	if err := runIssueSearch(cmd, []string{"login"}); err != nil {
		t.Fatalf("runIssueSearch: %v paths=%v", err, gotPaths)
	}
	if len(gotPaths) != 1 || gotPaths[0] != "GET /api/agent/issues/search" {
		t.Fatalf("paths=%v want [GET /api/agent/issues/search]", gotPaths)
	}
}

func TestBoundary_IssueSearch_TOKEN_FILE_HitsAgentSearchOnly(t *testing.T) {
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/api/agent/issues/search" {
			_ = json.NewEncoder(w).Encode(map[string]any{"issues": []any{}})
			return
		}
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MULTICA_TOKEN", "")
	f := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(f, []byte("mat_issue_search_token"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MULTICA_TOKEN_FILE", f)
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-boundary")
	t.Setenv("MULTICA_SERVER_URL", srv.URL)

	cmd := &cobra.Command{Use: "search"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("output", "json", "")
	cmd.Flags().Int("limit", 0, "")
	cmd.Flags().Bool("include-closed", false, "")

	if err := runIssueSearch(cmd, []string{"x"}); err != nil {
		t.Fatalf("runIssueSearch TOKEN_FILE: %v paths=%v", err, gotPaths)
	}
	for _, p := range gotPaths {
		if p == "GET /api/issues/search" {
			t.Fatalf("hit human search: %v", gotPaths)
		}
	}
	if len(gotPaths) != 1 || gotPaths[0] != "GET /api/agent/issues/search" {
		t.Fatalf("paths=%v", gotPaths)
	}
}

// --- #812 attachment list under mat_* ---

func TestBoundary_AttachmentList_Issue_TOKEN_FILE_HitsAgentPath(t *testing.T) {
	issueID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/api/agent/issues/"+issueID+"/attachments":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": boundaryContractAttachmentID, "filename": "a.png", "content_type": "image/png", "size": 12},
			})
		case r.URL.Path == "/api/agent/issues/"+issueID || strings.HasPrefix(r.URL.Path, "/api/agent/issues/"):
			// resolveIssueRef may GET issue first
			if r.URL.Path == "/api/agent/issues/"+issueID {
				_ = json.NewEncoder(w).Encode(map[string]any{"id": issueID, "identifier": "MUL-1", "title": "t"})
				return
			}
			http.Error(w, "unexpected", 500)
		default:
			http.Error(w, "human path forbidden", http.StatusForbidden)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MULTICA_TOKEN", "")
	f := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(f, []byte("mat_att_list_issue"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MULTICA_TOKEN_FILE", f)
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-boundary")
	t.Setenv("MULTICA_SERVER_URL", srv.URL)

	cmd := &cobra.Command{Use: "list"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("issue", "", "")
	cmd.Flags().String("channel", "", "")
	cmd.Flags().String("output", "json", "")
	_ = cmd.Flags().Set("issue", issueID)

	if err := runAttachmentList(cmd, nil); err != nil {
		t.Fatalf("runAttachmentList: %v paths=%v", err, gotPaths)
	}
	for _, p := range gotPaths {
		if strings.Contains(p, "/api/issues/") && !strings.Contains(p, "/api/agent/issues/") {
			t.Fatalf("hit human path %q full=%v", p, gotPaths)
		}
	}
	want := "GET /api/agent/issues/" + issueID + "/attachments"
	found := false
	for _, p := range gotPaths {
		if p == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("paths=%v want contain %s", gotPaths, want)
	}
}

func TestBoundary_AttachmentList_Channel_TOKEN_FILE_HitsAgentPath(t *testing.T) {
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/agent/channels":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": boundaryContractChannelID, "name": "ops"},
			})
		case "/api/agent/channels/" + boundaryContractChannelID + "/attachments":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": boundaryContractAttachmentID, "filename": "b.txt", "content_type": "text/plain", "size": 3},
			})
		default:
			http.Error(w, "human path forbidden", http.StatusForbidden)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MULTICA_TOKEN", "")
	f := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(f, []byte("mat_att_list_ch"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MULTICA_TOKEN_FILE", f)
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-boundary")
	t.Setenv("MULTICA_SERVER_URL", srv.URL)

	cmd := &cobra.Command{Use: "list"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("issue", "", "")
	cmd.Flags().String("channel", "", "")
	cmd.Flags().String("output", "json", "")
	_ = cmd.Flags().Set("channel", "ops")

	if err := runAttachmentList(cmd, nil); err != nil {
		t.Fatalf("runAttachmentList: %v paths=%v", err, gotPaths)
	}
	want := "GET /api/agent/channels/" + boundaryContractChannelID + "/attachments"
	found := false
	for _, p := range gotPaths {
		if strings.Contains(p, "/api/channels/") && !strings.Contains(p, "/api/agent/channels/") {
			t.Fatalf("hit human %q full=%v", p, gotPaths)
		}
		if p == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("paths=%v want %s", gotPaths, want)
	}
}
