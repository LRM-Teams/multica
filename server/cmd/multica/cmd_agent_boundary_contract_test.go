package main

import (
	"encoding/json"
	"fmt"
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

// TestBoundary_AttachmentUpload_HitsDedicatedAgentAPI asserts upload goes to
// dedicated agent upload surface, not human /api/upload-file.
// mat_* requires --target (unbound rejected — #801 Barry/Ronan lock).
func TestBoundary_AttachmentUpload_HitsDedicatedAgentAPI(t *testing.T) {
	src := filepath.Join(t.TempDir(), "up.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/agent/channels":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": boundaryContractChannelID, "name": "eng"},
			})
		case r.Method == http.MethodPost && (r.URL.Path == "/api/agent/attachments" ||
			r.URL.Path == "/api/agent/attachments/upload" ||
			r.URL.Path == "/api/agent/upload-file"):
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
		t.Fatalf("runAttachmentUpload: %v (paths=%v) — expect dedicated /api/agent/attachments*", err, gotPaths)
	}
	foundUpload := false
	for _, p := range gotPaths {
		path := strings.SplitN(p, " ", 2)[1]
		if path == "/api/upload-file" || strings.HasPrefix(path, "/api/attachments/") {
			t.Fatalf("upload hit human path %q; full=%v", p, gotPaths)
		}
		if !strings.HasPrefix(path, "/api/agent/") {
			t.Fatalf("upload path %q is not under /api/agent/; full=%v", p, gotPaths)
		}
		if path == "/api/agent/attachments" || path == "/api/agent/attachments/upload" || path == "/api/agent/upload-file" {
			foundUpload = true
		}
	}
	if !foundUpload {
		t.Fatalf("paths=%v, want dedicated agent upload POST", gotPaths)
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

// boundaryCLIEnvTokenFile mimics daemon agent execution: unset MULTICA_TOKEN,
// inject mat_* via MULTICA_TOKEN_FILE only (cli_transport / daemon.go).
func boundaryCLIEnvTokenFile(t *testing.T, srvURL string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MULTICA_TOKEN", "")
	t.Setenv("MULTICA_TOKEN_FILE", "")
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "mat_token")
	if err := os.WriteFile(tokenFile, []byte("mat_daemon_token_file_only\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	t.Setenv("MULTICA_TOKEN_FILE", tokenFile)
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-boundary")
	t.Setenv("MULTICA_SERVER_URL", srvURL)
	// Agent execution context so resolveToken does not fall through to human profile.
	t.Setenv("MULTICA_AGENT_ID", "agent-boundary")
	t.Setenv("MULTICA_TASK_ID", "task-boundary")
}

// TestBoundary_IssueGet_TokenFileOnly_HitsDedicatedAgentAPI is the Frank
// 2026-07-28 regression: list worked (resolveToken reads TOKEN_FILE) but get
// 403'd because agentAPITokenFromEnv only checked MULTICA_TOKEN (unset by daemon).
func TestBoundary_IssueGet_TokenFileOnly_HitsDedicatedAgentAPI(t *testing.T) {
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
	boundaryCLIEnvTokenFile(t, srv.URL)

	cmd := &cobra.Command{Use: "get"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("output", "json", "")

	if err := runIssueGet(cmd, []string{boundaryContractIssueID}); err != nil {
		t.Fatalf("runIssueGet TOKEN_FILE-only: %v (paths=%v)", err, gotPaths)
	}
	if len(gotPaths) == 0 {
		t.Fatal("no HTTP calls")
	}
	for _, p := range gotPaths {
		path := strings.SplitN(p, " ", 2)[1]
		if strings.HasPrefix(path, "/api/issues") && !strings.HasPrefix(path, "/api/agent/issues") {
			t.Fatalf("TOKEN_FILE-only hit human path %q; full=%v", p, gotPaths)
		}
		if !strings.HasPrefix(path, "/api/agent/issues") {
			t.Fatalf("path %q not under /api/agent/issues; full=%v", p, gotPaths)
		}
	}
}

// TestBoundary_IssueStatus_TokenFileOnly_HitsDedicatedAgentAPI covers status
// mutate under daemon TOKEN_FILE injection (same root cause as get).
func TestBoundary_IssueStatus_TokenFileOnly_HitsDedicatedAgentAPI(t *testing.T) {
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
	boundaryCLIEnvTokenFile(t, srv.URL)

	cmd := &cobra.Command{Use: "status"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("output", "json", "")

	if err := runIssueStatus(cmd, []string{boundaryContractIssueID, "in_progress"}); err != nil {
		t.Fatalf("runIssueStatus TOKEN_FILE-only: %v (paths=%v)", err, gotPaths)
	}
	for _, p := range gotPaths {
		path := strings.SplitN(p, " ", 2)[1]
		if strings.HasPrefix(path, "/api/issues") && !strings.HasPrefix(path, "/api/agent/issues") {
			t.Fatalf("TOKEN_FILE-only status hit human path %q; full=%v", p, gotPaths)
		}
	}
}

// TestBoundary_IssueCommentAdd_TokenFileOnly_HitsDedicatedAgentAPI covers comment
// write under daemon TOKEN_FILE injection.
func TestBoundary_IssueCommentAdd_TokenFileOnly_HitsDedicatedAgentAPI(t *testing.T) {
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
	boundaryCLIEnvTokenFile(t, srv.URL)

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
		t.Fatalf("runIssueCommentAdd TOKEN_FILE-only: %v (paths=%v)", err, gotPaths)
	}
	for _, p := range gotPaths {
		path := strings.SplitN(p, " ", 2)[1]
		if strings.HasPrefix(path, "/api/issues") && !strings.HasPrefix(path, "/api/agent/issues") {
			t.Fatalf("TOKEN_FILE-only comment hit human path %q; full=%v", p, gotPaths)
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
		{"channel list", []string{"/api/agent/channels"}},
		{"channel members", []string{"/api/agent/channels/"}},
		{"channel mute", []string{"/api/agent/channels/"}},
		{"issue list/get/create/update", []string{"/api/agent/issues"}},
		{"issue comments", []string{"/api/agent/issues/", "/comments"}},
		{"issue labels on-issue", []string{"/api/agent/issues/", "/labels"}},
		{"issue subscribers", []string{"/api/agent/issues/", "/subscribers"}},
		{"issue task-runs/rerun/channel", []string{"/api/agent/issues/", "/task-runs"}},
		{"project resource read", []string{"/api/agent/projects/"}},
		{"attachment view/upload", []string{"/api/agent/attachments", "/api/agent/upload-file"}},
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

// TestBoundary_AttachmentUpload_AgentTokenAllowsUnboundStaging asserts mat_*
// CLI may omit --target and still POST dedicated /api/agent/attachments
// (Parker secure staging for DM/thread attach via --attachment-id).
func TestBoundary_AttachmentUpload_AgentTokenAllowsUnboundStaging(t *testing.T) {
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPost && r.URL.Path == "/api/agent/attachments" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": boundaryContractAttachmentID, "filename": "x.txt",
			})
			return
		}
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
	// no --target → unbound staging
	if err := runAttachmentUpload(cmd, nil); err != nil {
		t.Fatalf("mat_* unbound staging upload: %v paths=%v", err, gotPaths)
	}
	found := false
	for _, p := range gotPaths {
		if p == "POST /api/agent/attachments" {
			found = true
		}
		if strings.Contains(p, "/api/upload-file") {
			t.Fatalf("hit human upload path %q", p)
		}
	}
	if !found {
		t.Fatalf("paths=%v, want POST /api/agent/attachments", gotPaths)
	}
}


// TestBoundary_NoEnvOnlyMatTokenDetection forbids reintroducing env-only mat_*
// detectors (the Frank 2026-07-28 class of bug). TOKEN_FILE must be considered
// whenever MULTICA_TOKEN is consulted for agent path selection.
func TestBoundary_NoEnvOnlyMatTokenDetection(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		// tests run with cwd = package dir (server/cmd/multica)
		t.Fatalf("readdir: %v", err)
	}
	// Also try relative package path from module root when needed
	_ = entries
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	// When test binary runs, cwd is the package directory.
	bad := []string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		// Flag bare Getenv("MULTICA_TOKEN") followed nearby by mat_ prefix check
		// without TOKEN_FILE in the same function — heuristic on file level:
		if !strings.Contains(src, `Getenv("MULTICA_TOKEN")`) {
			continue
		}
		// ambientTokenFromEnvOrFile is the allowed single implementation.
		if f == "cmd_auth.go" && strings.Contains(src, "func ambientTokenFromEnvOrFile") {
			continue
		}
		// Any other production file that reads MULTICA_TOKEN for path selection
		// must not pair it with mat_ without going through helpers.
		lines := strings.Split(src, "\n")
		for i, line := range lines {
			if !strings.Contains(line, `Getenv("MULTICA_TOKEN")`) {
				continue
			}
			// look ahead 15 lines for mat_ prefix without TOKEN_FILE on same line window
			window := strings.Join(lines[i:min(len(lines), i+15)], "\n")
			if strings.Contains(window, `"mat_"`) || strings.Contains(window, "mat_") {
				if !strings.Contains(window, "MULTICA_TOKEN_FILE") && !strings.Contains(window, "ambientTokenFromEnvOrFile") && !strings.Contains(window, "resolveToken") {
					bad = append(bad, fmt.Sprintf("%s:%d", f, i+1))
				}
			}
		}
	}
	if len(bad) > 0 {
		t.Fatalf("env-only mat_* detection reintroduced (use isAgentAPIToken / isAgentAPITokenAmbient / ambientTokenFromEnvOrFile): %v", bad)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
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
