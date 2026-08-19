package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/agentavatar"
)

var durableAgentAvatarPattern = regexp.MustCompile(`^https://cdn\.leagent\.me/agent-avatars/v3/agent-(0[1-9]|1[0-5])\.png$`)

func TestAgentAvatar_DurableCreateAndVerifiedUpdateProvenance(t *testing.T) {
	requireAvatarTestDatabase(t)

	marker := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	created := createAvatarTestAgent(t, "avatar-durable-"+marker, nil)
	wantAssignedURL := agentavatar.DefaultURL(created.ID)
	if created.AvatarURL == nil || *created.AvatarURL != wantAssignedURL {
		t.Fatalf("create avatar_url = %v, want %q", created.AvatarURL, wantAssignedURL)
	}
	if created.AvatarSource != agentAvatarSourceAssigned {
		t.Fatalf("create avatar_source = %q, want assigned", created.AvatarSource)
	}
	requirePersistedAgentAvatar(t, created.ID, *created.AvatarURL, agentAvatarSourceAssigned, "")

	legacyPickedURL := "/agent-avatars/human-24.jpg"
	pickedURL := agentavatar.LegacyURL(24)
	picked := updateAvatarTestAgent(t, created.ID, map[string]any{
		"kind":       agentAvatarSourcePicked,
		"preset_url": legacyPickedURL,
	}, http.StatusOK)
	if picked.AvatarURL == nil || *picked.AvatarURL != pickedURL || picked.AvatarSource != agentAvatarSourcePicked {
		t.Fatalf("picked response = avatar_url %v source %q", picked.AvatarURL, picked.AvatarSource)
	}
	requirePersistedAgentAvatar(t, created.ID, pickedURL, agentAvatarSourcePicked, "")

	uploadedURL := "/uploads/agents/" + marker + ".png"
	attachmentID := createAvatarTestAttachment(t, uploadedURL, "image/png", testUserID, false)
	uploaded := updateAvatarTestAgent(t, created.ID, map[string]any{
		"kind":          agentAvatarSourceUploaded,
		"attachment_id": attachmentID,
	}, http.StatusOK)
	if uploaded.AvatarURL == nil || *uploaded.AvatarURL != uploadedURL || uploaded.AvatarSource != agentAvatarSourceUploaded {
		t.Fatalf("uploaded response = avatar_url %v source %q", uploaded.AvatarURL, uploaded.AvatarSource)
	}
	requirePersistedAgentAvatar(t, created.ID, uploadedURL, agentAvatarSourceUploaded, attachmentID)
}

func TestAgentAvatar_CreateUploadUsesOwnedImageAttachment(t *testing.T) {
	requireAvatarTestDatabase(t)

	marker := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	uploadedURL := "/uploads/agents/create-" + marker + ".png"
	attachmentID := createAvatarTestAttachment(t, uploadedURL, "image/png", testUserID, false)
	created := createAvatarTestAgent(t, "avatar-uploaded-"+marker, map[string]any{
		"kind":          agentAvatarSourceUploaded,
		"attachment_id": attachmentID,
	})
	if created.AvatarURL == nil || *created.AvatarURL != uploadedURL || created.AvatarSource != agentAvatarSourceUploaded {
		t.Fatalf("uploaded create response = avatar_url %v source %q", created.AvatarURL, created.AvatarSource)
	}
	requirePersistedAgentAvatar(t, created.ID, uploadedURL, agentAvatarSourceUploaded, attachmentID)
}

func TestAgentAvatar_AttachmentBindsAtMostOneAgentUnderConcurrentCreate(t *testing.T) {
	requireAvatarTestDatabase(t)

	marker := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	attachmentID := createAvatarTestAttachment(t, "/uploads/agents/unique-"+marker+".png", "image/png", testUserID, false)
	start := make(chan struct{})
	type result struct {
		status  int
		agentID string
		body    string
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			recorder := httptest.NewRecorder()
			testHandler.CreateAgent(recorder, newRequest(http.MethodPost, "/api/agents", map[string]any{
				"name":                 fmt.Sprintf("avatar-unique-%d-%s", i, marker),
				"display_name":         fmt.Sprintf("avatar-unique-%d-%s", i, marker),
				"runtime_id":           testRuntimeID,
				"model":                "composer-1.5",
				"visibility":           "private",
				"max_concurrent_tasks": 1,
				"avatar_selection": map[string]any{
					"kind":          agentAvatarSourceUploaded,
					"attachment_id": attachmentID,
				},
			}))
			got := result{status: recorder.Code, body: recorder.Body.String()}
			if recorder.Code == http.StatusCreated {
				var response AgentResponse
				if err := json.NewDecoder(recorder.Body).Decode(&response); err == nil {
					got.agentID = response.ID
				} else {
					got.body = err.Error()
				}
			}
			results <- got
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	statusCounts := map[int]int{}
	for got := range results {
		statusCounts[got.status]++
		if got.agentID != "" {
			agentID := got.agentID
			t.Cleanup(func() {
				testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, parseUUID(agentID))
			})
		}
		if got.status != http.StatusCreated && got.status != http.StatusConflict {
			t.Errorf("unexpected create status %d: %s", got.status, got.body)
		}
		if got.status == http.StatusConflict && !strings.Contains(got.body, "already bound") {
			t.Errorf("conflict body %q does not explain attachment binding", got.body)
		}
	}
	if statusCounts[http.StatusCreated] != 1 || statusCounts[http.StatusConflict] != 1 {
		t.Fatalf("concurrent attachment create statuses = %#v, want one 201 and one 409", statusCounts)
	}
	var bindings int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM agent WHERE avatar_attachment_id = $1
	`, attachmentID).Scan(&bindings); err != nil {
		t.Fatalf("count avatar attachment bindings: %v", err)
	}
	if bindings != 1 {
		t.Fatalf("avatar attachment bindings = %d, want exactly 1", bindings)
	}
}

func TestAgentAvatar_TrustedDraftMapsAvatarToAssigned(t *testing.T) {
	requireAvatarTestDatabase(t)

	marker := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	draftURL := "https://draft.example.test/avatar-" + marker + ".png"
	var draftID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_creation_draft (
			workspace_id, target_user_id, name, avatar_url, initial_notes, initial_memory
		) VALUES ($1, $2, $3, $4, '{"notes/context.md":"draft-note"}'::jsonb, '{}'::jsonb)
		RETURNING id
	`, testWorkspaceID, testUserID, "avatar-draft-"+marker, draftURL).Scan(&draftID); err != nil {
		t.Fatalf("create trusted agent draft: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_creation_draft WHERE id = $1`, draftID)
	})

	recorder := httptest.NewRecorder()
	testHandler.CreateAgent(recorder, newRequest(http.MethodPost, "/api/agents", map[string]any{
		"name":                 "avatar-from-draft-" + marker,
		"display_name":         "avatar-from-draft-" + marker,
		"runtime_id":           testRuntimeID,
		"model":                "composer-1.5",
		"visibility":           "private",
		"max_concurrent_tasks": 1,
		"draft_id":             draftID,
	}))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create from trusted draft: expected 201, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var created AgentResponse
	if err := json.NewDecoder(recorder.Body).Decode(&created); err != nil {
		t.Fatalf("decode draft create: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, parseUUID(created.ID))
	})
	if created.AvatarURL == nil || *created.AvatarURL != draftURL || created.AvatarSource != agentAvatarSourceAssigned {
		t.Fatalf("draft avatar response url=%v source=%q, want %q/assigned", created.AvatarURL, created.AvatarSource, draftURL)
	}
	requirePersistedAgentAvatar(t, created.ID, draftURL, agentAvatarSourceAssigned, "")
}

func TestAgentAvatar_CreateAndUpdateRejectRawURL(t *testing.T) {
	requireAvatarTestDatabase(t)

	body := map[string]any{
		"name":                 "avatar-raw-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10],
		"display_name":         "avatar-raw-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10],
		"runtime_id":           testRuntimeID,
		"model":                "composer-1.5",
		"visibility":           "private",
		"max_concurrent_tasks": 1,
		"avatar_url":           "/uploads/unverified.png",
	}
	recorder := httptest.NewRecorder()
	testHandler.CreateAgent(recorder, newRequest(http.MethodPost, "/api/agents", body))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("raw create avatar_url: expected 400, got %d: %s", recorder.Code, recorder.Body.String())
	}

	created := createAvatarTestAgent(t, "avatar-raw-update-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:10], nil)
	recorder = httptest.NewRecorder()
	request := withURLParam(newRequest(http.MethodPut, "/api/agents/"+created.ID, map[string]any{
		"avatar_url": "/uploads/unverified.png",
	}), "id", created.ID)
	testHandler.UpdateAgent(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("raw update avatar_url: expected 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestAgentAvatar_UploadSelectionFailsClosed(t *testing.T) {
	requireAvatarTestDatabase(t)

	otherUploaderID := uuid.NewString()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO "user" (id, name, email)
		VALUES ($1, $2, $2 || '@example.test')
	`, otherUploaderID, "avatar-other-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:8]); err != nil {
		t.Fatalf("create other uploader: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, otherUploaderID)
	})

	tests := []struct {
		name       string
		content    string
		uploaderID string
		bound      bool
		want       int
	}{
		{name: "non-image", content: "text/plain", uploaderID: testUserID, want: http.StatusBadRequest},
		{name: "foreign uploader", content: "image/png", uploaderID: otherUploaderID, want: http.StatusForbidden},
		{name: "business-bound", content: "image/png", uploaderID: testUserID, bound: true, want: http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attachmentID := createAvatarTestAttachment(t, "/uploads/agents/"+uuid.NewString()+".png", tt.content, tt.uploaderID, tt.bound)
			body := map[string]any{
				"name":                 "avatar-invalid-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10],
				"display_name":         "avatar-invalid-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10],
				"runtime_id":           testRuntimeID,
				"model":                "composer-1.5",
				"visibility":           "private",
				"max_concurrent_tasks": 1,
				"avatar_selection": map[string]any{
					"kind":          agentAvatarSourceUploaded,
					"attachment_id": attachmentID,
				},
			}
			recorder := httptest.NewRecorder()
			testHandler.CreateAgent(recorder, newRequest(http.MethodPost, "/api/agents", body))
			if recorder.Code != tt.want {
				t.Fatalf("expected %d, got %d: %s", tt.want, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestAgentAvatar_DirectInsertUsesSameDurableBoundary(t *testing.T) {
	requireAvatarTestDatabase(t)

	ctx := context.Background()
	name := "avatar-trigger-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	var agentID, avatarURL, avatarSource string
	err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, display_name, runtime_mode, runtime_config, runtime_id, max_concurrent_tasks, owner_id
		, model) VALUES ($1, $2, $2, 'cloud', '{}'::jsonb, $3, 1, $4, 'composer-1.5')
		RETURNING id, avatar_url, avatar_source
	`, testWorkspaceID, name, testRuntimeID, testUserID).Scan(&agentID, &avatarURL, &avatarSource)
	if err != nil {
		t.Fatalf("direct insert agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, parseUUID(agentID))
	})

	if !durableAgentAvatarPattern.MatchString(avatarURL) || avatarSource != agentAvatarSourceAssigned {
		t.Fatalf("direct insert avatar_url=%q source=%q", avatarURL, avatarSource)
	}
}

func TestAgentAvatar_ConcurrentCreatesAndDirectInsertsAreComplete(t *testing.T) {
	requireAvatarTestDatabase(t)

	const count = 12
	start := make(chan struct{})
	errCh := make(chan error, count*2)
	createdIDs := make(chan string, count*2)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		i := i
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			name := fmt.Sprintf("avatar-api-%d-%s", i, strings.ReplaceAll(uuid.NewString(), "-", "")[:8])
			recorder := httptest.NewRecorder()
			testHandler.CreateAgent(recorder, newRequest(http.MethodPost, "/api/agents", map[string]any{
				"name": name, "display_name": name, "runtime_id": testRuntimeID, "visibility": "private", "max_concurrent_tasks": 1,
				"model": "composer-1.5",
			}))
			if recorder.Code != http.StatusCreated {
				errCh <- fmt.Errorf("api create %d: %s", recorder.Code, recorder.Body.String())
				return
			}
			var response AgentResponse
			if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
				errCh <- err
				return
			}
			if response.AvatarURL == nil || !durableAgentAvatarPattern.MatchString(*response.AvatarURL) || response.AvatarSource != agentAvatarSourceAssigned {
				errCh <- fmt.Errorf("api create returned partial avatar: %+v", response)
				return
			}
			createdIDs <- response.ID
		}()

		go func() {
			defer wg.Done()
			<-start
			name := fmt.Sprintf("avatar-sql-%d-%s", i, strings.ReplaceAll(uuid.NewString(), "-", "")[:8])
			var id, avatarURL, source string
			err := testPool.QueryRow(context.Background(), `
				INSERT INTO agent (
			workspace_id, name, display_name, runtime_mode, runtime_config, runtime_id, max_concurrent_tasks, owner_id
		, model) VALUES ($1, $2, $2, 'cloud', '{}'::jsonb, $3, 1, $4, 'composer-1.5')
				RETURNING id, avatar_url, avatar_source
			`, testWorkspaceID, name, testRuntimeID, testUserID).Scan(&id, &avatarURL, &source)
			if err != nil {
				errCh <- err
				return
			}
			if !durableAgentAvatarPattern.MatchString(avatarURL) || source != agentAvatarSourceAssigned {
				errCh <- fmt.Errorf("direct insert returned partial avatar url=%q source=%q", avatarURL, source)
				return
			}
			createdIDs <- id
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	close(createdIDs)
	for err := range errCh {
		t.Error(err)
	}
	for id := range createdIDs {
		id := id
		t.Cleanup(func() {
			testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, parseUUID(id))
		})
	}
}

func TestAgentAvatar_ConcurrentUpdatesKeepURLSourceAndAttachmentAtomic(t *testing.T) {
	requireAvatarTestDatabase(t)

	marker := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	created := createAvatarTestAgent(t, "avatar-concurrent-"+marker, nil)
	type updateCase struct {
		selection    map[string]any
		url          string
		source       string
		attachmentID string
	}
	updates := []updateCase{
		{selection: map[string]any{"kind": agentAvatarSourcePicked, "preset_url": "/agent-avatars/human-01.jpg"}, url: agentavatar.LegacyURL(1), source: agentAvatarSourcePicked},
		{selection: map[string]any{"kind": agentAvatarSourcePicked, "preset_url": agentavatar.URL(15)}, url: agentavatar.URL(15), source: agentAvatarSourcePicked},
	}
	for _, suffix := range []string{"a", "b"} {
		url := "/uploads/agents/" + marker + "-" + suffix + ".png"
		attachmentID := createAvatarTestAttachment(t, url, "image/png", testUserID, false)
		updates = append(updates, updateCase{
			selection: map[string]any{"kind": agentAvatarSourceUploaded, "attachment_id": attachmentID},
			url:       url, source: agentAvatarSourceUploaded, attachmentID: attachmentID,
		})
	}

	start := make(chan struct{})
	errCh := make(chan string, len(updates)*4)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		for _, update := range updates {
			update := update
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				recorder := httptest.NewRecorder()
				request := withURLParam(newRequest(http.MethodPut, "/api/agents/"+created.ID, map[string]any{
					"avatar_selection": update.selection,
				}), "id", created.ID)
				testHandler.UpdateAgent(recorder, request)
				if recorder.Code != http.StatusOK {
					errCh <- recorder.Body.String()
					return
				}
				var response AgentResponse
				if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
					errCh <- err.Error()
					return
				}
				if response.AvatarURL == nil || *response.AvatarURL != update.url || response.AvatarSource != update.source {
					errCh <- "response avatar URL/source pair was not atomic"
				}
			}()
		}
	}
	close(start)
	wg.Wait()
	close(errCh)
	for errText := range errCh {
		t.Errorf("concurrent update: %s", errText)
	}

	var gotURL, gotSource string
	var gotAttachmentID *string
	if err := testPool.QueryRow(context.Background(), `
		SELECT avatar_url, avatar_source, avatar_attachment_id::text
		FROM agent
		WHERE id = $1
	`, parseUUID(created.ID)).Scan(&gotURL, &gotSource, &gotAttachmentID); err != nil {
		t.Fatalf("read final concurrent avatar: %v", err)
	}
	for _, update := range updates {
		if gotURL != update.url {
			continue
		}
		if gotSource != update.source {
			t.Fatalf("final avatar_url=%q source=%q, want source=%q", gotURL, gotSource, update.source)
		}
		if update.attachmentID == "" && gotAttachmentID != nil {
			t.Fatalf("picked avatar retained attachment %v", gotAttachmentID)
		}
		if update.attachmentID != "" && (gotAttachmentID == nil || *gotAttachmentID != update.attachmentID) {
			t.Fatalf("uploaded avatar attachment=%v, want %q", gotAttachmentID, update.attachmentID)
		}
		return
	}
	t.Fatalf("final avatar_url=%q source=%q was not one of the committed updates", gotURL, gotSource)
}

func requireAvatarTestDatabase(t *testing.T) {
	t.Helper()
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
}

func createAvatarTestAgent(t *testing.T, displayName string, selection map[string]any) AgentResponse {
	t.Helper()
	body := map[string]any{
		"name":                 "avatar-agent-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8],
		"display_name":         displayName,
		"runtime_id":           testRuntimeID,
		"model":                "composer-1.5",
		"visibility":           "private",
		"max_concurrent_tasks": 1,
	}
	if selection != nil {
		body["avatar_selection"] = selection
	}
	recorder := httptest.NewRecorder()
	testHandler.CreateAgent(recorder, newRequest(http.MethodPost, "/api/agents", body))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create agent: expected 201, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var response AgentResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, parseUUID(response.ID))
	})
	return response
}

func updateAvatarTestAgent(t *testing.T, agentID string, selection map[string]any, wantStatus int) AgentResponse {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(http.MethodPut, "/api/agents/"+agentID, map[string]any{
		"avatar_selection": selection,
	}), "id", agentID)
	testHandler.UpdateAgent(recorder, request)
	if recorder.Code != wantStatus {
		t.Fatalf("update avatar: expected %d, got %d: %s", wantStatus, recorder.Code, recorder.Body.String())
	}
	if wantStatus != http.StatusOK {
		return AgentResponse{}
	}
	var response AgentResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	return response
}

func createAvatarTestAttachment(t *testing.T, url, contentType, uploaderID string, businessBound bool) string {
	t.Helper()
	var attachmentID string
	var issueID any
	var boundIssueID string
	if businessBound {
		if err := testPool.QueryRow(context.Background(), `
			INSERT INTO issue (workspace_id, title, creator_type, creator_id)
			VALUES ($1, 'avatar attachment binding', 'member', $2)
			RETURNING id
		`, testWorkspaceID, testUserID).Scan(&boundIssueID); err != nil {
			t.Fatalf("create bound issue: %v", err)
		}
		issueID = boundIssueID
	}
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO attachment (
			workspace_id, issue_id, uploader_type, uploader_id, filename, url, content_type, size_bytes
		) VALUES ($1, $2, 'member', $3, 'avatar.png', $4, $5, 8)
		RETURNING id
	`, testWorkspaceID, issueID, uploaderID, url, contentType).Scan(&attachmentID); err != nil {
		t.Fatalf("create avatar attachment: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `
			UPDATE agent
			SET avatar_source = 'assigned', avatar_attachment_id = NULL
			WHERE avatar_attachment_id = $1
		`, attachmentID)
		testPool.Exec(context.Background(), `DELETE FROM attachment WHERE id = $1`, attachmentID)
		if boundIssueID != "" {
			testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, boundIssueID)
		}
	})
	return attachmentID
}

func requirePersistedAgentAvatar(t *testing.T, agentID, wantURL, wantSource, wantAttachmentID string) {
	t.Helper()
	var gotURL, gotSource string
	var gotAttachmentID *string
	if err := testPool.QueryRow(context.Background(), `
		SELECT avatar_url, avatar_source, avatar_attachment_id::text
		FROM agent
		WHERE id = $1
	`, parseUUID(agentID)).Scan(&gotURL, &gotSource, &gotAttachmentID); err != nil {
		t.Fatalf("read persisted avatar: %v", err)
	}
	if gotURL != wantURL || gotSource != wantSource {
		t.Fatalf("persisted avatar_url=%q source=%q, want url=%q source=%q", gotURL, gotSource, wantURL, wantSource)
	}
	if wantAttachmentID == "" && gotAttachmentID != nil {
		t.Fatalf("persisted avatar attachment=%v, want nil", gotAttachmentID)
	}
	if wantAttachmentID != "" && (gotAttachmentID == nil || *gotAttachmentID != wantAttachmentID) {
		t.Fatalf("persisted avatar attachment=%v, want %q", gotAttachmentID, wantAttachmentID)
	}
}
