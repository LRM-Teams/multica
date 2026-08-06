package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Residual hard controls beyond Ronan agent_surface_source_contract_test.go:
//  - label attach audit actor must be agent (Parker audit product point)
//  - private peer hidden from directory
//  - directory must not leak secret instruction *values* even if keys evolve

// TestBoundary_LabelAttach_AuditActorIsAgent asserts EventIssueLabelsChanged
// publishes actor_type=agent and actor_id=agent (not owner human).
func TestBoundary_LabelAttach_AuditActorIsAgent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "LabelAuditAgent", []byte("[]"))

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number, position)
		VALUES ($1, $2, 'todo', 'none', 'agent', $3, $4, 0)
		RETURNING id`,
		testWorkspaceID, "cf-label-audit-"+uuid.NewString(), agentID, 800000+int(uuid.New().ID()%100000),
	).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })

	var labelID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue_label (workspace_id, name, color)
		VALUES ($1, $2, '#112233')
		RETURNING id`,
		testWorkspaceID, "cf-audit-"+uuid.NewString()[:8],
	).Scan(&labelID); err != nil {
		t.Fatalf("create label: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM issue_label WHERE id = $1`, labelID) })

	var (
		mu   sync.Mutex
		got  *events.Event
		done = make(chan struct{}, 1)
	)
	testHandler.Bus.Subscribe(protocol.EventIssueLabelsChanged, func(e events.Event) {
		mu.Lock()
		defer mu.Unlock()
		ev := e
		got = &ev
		select {
		case done <- struct{}{}:
		default:
		}
	})

	req := newRequest(http.MethodPost, "/api/agent/issues/"+issueID+"/labels", map[string]any{
		"label_id": labelID,
	})
	req = withAgentPrincipal(req, agentID, testWorkspaceID, testUserID)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "id", issueID)
	rec := httptest.NewRecorder()
	testHandler.AttachAgentIssueLabel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("AttachAgentIssueLabel status=%d body=%s", rec.Code, rec.Body.String())
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for EventIssueLabelsChanged")
	}
	mu.Lock()
	defer mu.Unlock()
	if got == nil {
		t.Fatal("no event captured")
	}
	if got.ActorType != "agent" {
		t.Fatalf("audit ActorType=%q want agent (must not attribute to owner)", got.ActorType)
	}
	if got.ActorID != agentID {
		t.Fatalf("audit ActorID=%q want agent %s (not owner %s)", got.ActorID, agentID, testUserID)
	}
}

// TestBoundary_Directory_PrivateAgentListedForPeer supersedes the old
// "hidden from peer" contract (task #908: agent existence/listing is no
// longer visibility-gated — every agent in the workspace is a listable
// colleague, Frank 2026-07-30 10:56 "所有的代码全部删掉，默认public的").
// The sensitive-tab data (Activity/Files/Reminders/Usage) is what stays
// gated, separately, and this directory endpoint never exposed that data —
// see TestBoundary_Directory_NoSecretInstructionValue below for the boundary
// that does still apply.
func TestBoundary_Directory_PrivateAgentListedForPeer(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	privateID := createHandlerTestAgent(t, "DirPrivatePeer", []byte("[]"))
	callerID := createHandlerTestAgent(t, "DirPrivateCaller", []byte("[]"))

	req := newRequest(http.MethodGet, "/api/agent/agents", nil)
	req = withAgentPrincipal(req, callerID, testWorkspaceID, testUserID)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	rec := httptest.NewRecorder()
	testHandler.ListAgentDirectoryAgents(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var items []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, it := range items {
		if id, _ := it["id"].(string); id == privateID {
			found = true
		}
	}
	if !found {
		t.Fatalf("private peer agent %s missing from directory (existence should be unconditional post-#908)", privateID)
	}
}

// TestBoundary_Directory_NoSecretInstructionValue seeds a public agent with a
// secret instructions payload and asserts the directory JSON body never contains it.
func TestBoundary_Directory_NoSecretInstructionValue(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	const secret = "SECRET_SYSTEM_PROMPT_DO_NOT_LEAK_BOUNDARY"
	victimID := createHandlerTestAgent(t, "DirSecretVictim", []byte("[]"))
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent SET instructions = $2
		WHERE id = $1`, victimID, secret); err != nil {
		t.Fatalf("seed secret instructions: %v", err)
	}
	callerID := createHandlerTestAgent(t, "DirSecretCaller", []byte("[]"))

	req := newRequest(http.MethodGet, "/api/agent/agents", nil)
	req = withAgentPrincipal(req, callerID, testWorkspaceID, testUserID)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	rec := httptest.NewRecorder()
	testHandler.ListAgentDirectoryAgents(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("directory response body contains secret instructions for public agent %s", victimID)
	}
}

// --- Parker product contract (2026-07-28): uploader-owned secure staging ---
//  (a) uploader agent may view/bind own unbound upload
//  (b) foreign agent with the id → exact 404 (anti-IDOR; not 403 existence leak)
//  (c) after bind, visibility follows reference rules only
//      (uploader fallback MUST NOT survive bind + membership remove)
// Barry 2026-07-28: missing and invisible both 404; 403 only principal/human-API shape.

func agentMultipartUpload(t *testing.T, agentID string, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fw, err := w.CreateFormFile("file", "stage.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte("staging-bytes")); err != nil {
		t.Fatal(err)
	}
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/agent/attachments", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req = withAgentPrincipal(req, agentID, testWorkspaceID, testUserID)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	rec := httptest.NewRecorder()
	testHandler.UploadAgentAttachment(rec, req)
	return rec
}

// TestBoundary_StagingUnbound_UploaderCanViewOwn asserts mat agent may upload
// unbound and then GET metadata for that attachment (secure staging).
func TestBoundary_StagingUnbound_UploaderCanViewOwn(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prev := testHandler.Storage
	testHandler.Storage = &mockStorage{}
	t.Cleanup(func() { testHandler.Storage = prev })

	uploader := createHandlerTestAgent(t, "StageUploader", []byte("[]"))
	upRec := agentMultipartUpload(t, uploader, nil)
	if upRec.Code != http.StatusOK && upRec.Code != http.StatusCreated {
		t.Fatalf("uploader unbound upload status=%d body=%s; want 200 (Parker secure staging)", upRec.Code, upRec.Body.String())
	}
	var att map[string]any
	if err := json.Unmarshal(upRec.Body.Bytes(), &att); err != nil {
		t.Fatalf("decode upload: %v", err)
	}
	attID, _ := att["id"].(string)
	if attID == "" {
		t.Fatalf("upload response missing id: %s", upRec.Body.String())
	}

	getReq := newRequest(http.MethodGet, "/api/agent/attachments/"+attID, nil)
	getReq = withAgentPrincipal(getReq, uploader, testWorkspaceID, testUserID)
	getReq = withChannelTestWorkspaceCtx(t, getReq, testUserID)
	getReq = withURLParam(getReq, "id", attID)
	getRec := httptest.NewRecorder()
	testHandler.GetAgentAttachment(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("uploader GET own unbound attachment status=%d body=%s; want 200", getRec.Code, getRec.Body.String())
	}
}

// TestBoundary_StagingUnbound_ForeignAgentDenied asserts another agent cannot
// view an unbound attachment even when it knows the id.
func TestBoundary_StagingUnbound_ForeignAgentDenied(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prev := testHandler.Storage
	testHandler.Storage = &mockStorage{}
	t.Cleanup(func() { testHandler.Storage = prev })

	uploader := createHandlerTestAgent(t, "StageOwner", []byte("[]"))
	foreign := createHandlerTestAgent(t, "StageForeign", []byte("[]"))
	upRec := agentMultipartUpload(t, uploader, nil)
	if upRec.Code != http.StatusOK && upRec.Code != http.StatusCreated {
		t.Fatalf("uploader unbound upload status=%d body=%s; want 200 first", upRec.Code, upRec.Body.String())
	}
	var att map[string]any
	_ = json.Unmarshal(upRec.Body.Bytes(), &att)
	attID, _ := att["id"].(string)
	if attID == "" {
		t.Fatalf("missing id: %s", upRec.Body.String())
	}

	getReq := newRequest(http.MethodGet, "/api/agent/attachments/"+attID, nil)
	getReq = withAgentPrincipal(getReq, foreign, testWorkspaceID, testUserID)
	getReq = withChannelTestWorkspaceCtx(t, getReq, testUserID)
	getReq = withURLParam(getReq, "id", attID)
	getRec := httptest.NewRecorder()
	testHandler.GetAgentAttachment(getRec, getReq)
	// Barry anti-IDOR: known-but-invisible must look like missing → exact 404.
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("foreign agent status=%d body=%s; want exact 404 (no existence leak via 403)", getRec.Code, getRec.Body.String())
	}
}

// TestBoundary_StagingBindThenRemove_DeniesUploaderFallback:
// self unbound 200 → bind to ordinary group → member 200 → remove membership →
// metadata/content/download all 403. Uploader fallback must not bypass revoke.
func TestBoundary_StagingBindThenRemove_DeniesUploaderFallback(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	prev := testHandler.Storage
	testHandler.Storage = &mockStorage{}
	t.Cleanup(func() { testHandler.Storage = prev })
	ctx := context.Background()

	uploader := createHandlerTestAgent(t, "StageBindAgent", []byte("[]"))
	upRec := agentMultipartUpload(t, uploader, nil)
	if upRec.Code != http.StatusOK && upRec.Code != http.StatusCreated {
		t.Fatalf("unbound upload status=%d body=%s", upRec.Code, upRec.Body.String())
	}
	var att map[string]any
	if err := json.Unmarshal(upRec.Body.Bytes(), &att); err != nil {
		t.Fatal(err)
	}
	attID, _ := att["id"].(string)
	if attID == "" {
		t.Fatalf("missing id: %s", upRec.Body.String())
	}

	// Ordinary group with human owner (auto-seed) + agent member.
	channelID := seedChannelForTest(t, "stage-bind-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1, $2, 'agent', $3, 'member')
		ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, uploader); err != nil {
		t.Fatalf("add agent member: %v", err)
	}
	// Bind attachment to channel (message-send bind simulation).
	if _, err := testPool.Exec(ctx, `
		UPDATE attachment SET channel_id = $1
		WHERE id = $2 AND workspace_id = $3`,
		channelID, attID, testWorkspaceID); err != nil {
		t.Fatalf("bind attachment to channel: %v", err)
	}

	// While member: metadata OK.
	getWhileMember := func() int {
		req := newRequest(http.MethodGet, "/api/agent/attachments/"+attID, nil)
		req = withAgentPrincipal(req, uploader, testWorkspaceID, testUserID)
		req = withChannelTestWorkspaceCtx(t, req, testUserID)
		req = withURLParam(req, "id", attID)
		rec := httptest.NewRecorder()
		testHandler.GetAgentAttachment(rec, req)
		return rec.Code
	}
	if code := getWhileMember(); code != http.StatusOK {
		t.Fatalf("member GET bound attachment status=%d; want 200", code)
	}

	// Remove agent membership (revoke surface).
	if _, err := testPool.Exec(ctx, `
		DELETE FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2
		  AND member_type = 'agent' AND member_id = $3`,
		channelID, testWorkspaceID, uploader); err != nil {
		t.Fatalf("remove agent member: %v", err)
	}

	// After remove: metadata / content / download must all 404 (anti-IDOR deny) —
	// not uploader fallback 200, and not 403 existence leak.
	assert404 := func(name string, fn func(http.ResponseWriter, *http.Request)) {
		t.Helper()
		req := newRequest(http.MethodGet, "/api/agent/attachments/"+attID, nil)
		req = withAgentPrincipal(req, uploader, testWorkspaceID, testUserID)
		req = withChannelTestWorkspaceCtx(t, req, testUserID)
		req = withURLParam(req, "id", attID)
		rec := httptest.NewRecorder()
		fn(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("after remove %s status=%d body=%s; want 404 (uploader fallback must not survive bind)", name, rec.Code, rec.Body.String())
		}
	}
	assert404("GetAgentAttachment", testHandler.GetAgentAttachment)
	assert404("GetAgentAttachmentContent", testHandler.GetAgentAttachmentContent)
	assert404("DownloadAgentAttachment", testHandler.DownloadAgentAttachment)
}
