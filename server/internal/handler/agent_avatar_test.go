package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

var durableAgentAvatarPattern = regexp.MustCompile(`^/agent-avatars/human-(0[1-9]|1[0-9]|2[0-4])\.jpg$`)

func TestAgentAvatar_DurableCreateAndUpdateProvenance(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	marker := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	created := createAvatarTestAgent(t, "avatar-durable-"+marker, nil)
	if created.AvatarURL == nil || !durableAgentAvatarPattern.MatchString(*created.AvatarURL) {
		t.Fatalf("create avatar_url = %v, want concrete preset path", created.AvatarURL)
	}
	if created.AvatarSource != "assigned" {
		t.Fatalf("create avatar_source = %q, want assigned", created.AvatarSource)
	}
	requirePersistedAgentAvatar(t, created.ID, *created.AvatarURL, "assigned")

	pickedURL := "/agent-avatars/human-24.jpg"
	picked := updateAvatarTestAgent(t, created.ID, pickedURL, http.StatusOK)
	if picked.AvatarURL == nil || *picked.AvatarURL != pickedURL || picked.AvatarSource != "picked" {
		t.Fatalf("picked response = avatar_url %v source %q", picked.AvatarURL, picked.AvatarSource)
	}
	requirePersistedAgentAvatar(t, created.ID, pickedURL, "picked")

	uploadedURL := "/uploads/agents/" + marker + ".png"
	uploaded := updateAvatarTestAgent(t, created.ID, uploadedURL, http.StatusOK)
	if uploaded.AvatarURL == nil || *uploaded.AvatarURL != uploadedURL || uploaded.AvatarSource != "uploaded" {
		t.Fatalf("uploaded response = avatar_url %v source %q", uploaded.AvatarURL, uploaded.AvatarSource)
	}
	requirePersistedAgentAvatar(t, created.ID, uploadedURL, "uploaded")

	updateAvatarTestAgent(t, created.ID, "   ", http.StatusBadRequest)
	requirePersistedAgentAvatar(t, created.ID, uploadedURL, "uploaded")
}

func TestAgentAvatar_CreateWithProvidedValueIsAssignedOnce(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	provided := "  /uploads/agents/preselected.png  "
	created := createAvatarTestAgent(t, "avatar-provided-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:10], &provided)
	wantURL := strings.TrimSpace(provided)
	if created.AvatarURL == nil || *created.AvatarURL != wantURL || created.AvatarSource != "assigned" {
		t.Fatalf("provided create response = avatar_url %v source %q", created.AvatarURL, created.AvatarSource)
	}
	requirePersistedAgentAvatar(t, created.ID, wantURL, "assigned")
}

func TestAgentAvatar_DirectInsertUsesSameDurableBoundary(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	name := "avatar-trigger-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	var agentID, avatarURL, avatarSource string
	err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, display_name, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id
		)
		VALUES ($1, $2, $2, 'cloud', '{}'::jsonb, $3, 'private', 1, $4)
		RETURNING id, avatar_url, avatar_source
	`, testWorkspaceID, name, testRuntimeID, testUserID).Scan(&agentID, &avatarURL, &avatarSource)
	if err != nil {
		t.Fatalf("direct insert agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, parseUUID(agentID))
	})

	if !durableAgentAvatarPattern.MatchString(avatarURL) || avatarSource != "assigned" {
		t.Fatalf("direct insert avatar_url=%q source=%q", avatarURL, avatarSource)
	}
}

func TestAgentAvatar_ConcurrentUpdatesKeepURLAndSourceAtomic(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	marker := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	created := createAvatarTestAgent(t, "avatar-concurrent-"+marker, nil)
	updates := []struct {
		url    string
		source string
	}{
		{url: "/agent-avatars/human-01.jpg", source: "picked"},
		{url: "/agent-avatars/human-24.jpg", source: "picked"},
		{url: "/uploads/agents/" + marker + "-a.png", source: "uploaded"},
		{url: "/uploads/agents/" + marker + "-b.png", source: "uploaded"},
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
					"avatar_url": update.url,
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
	if err := testPool.QueryRow(context.Background(), `
		SELECT avatar_url, avatar_source
		FROM agent
		WHERE id = $1
	`, parseUUID(created.ID)).Scan(&gotURL, &gotSource); err != nil {
		t.Fatalf("read final concurrent avatar: %v", err)
	}
	for _, update := range updates {
		if gotURL == update.url {
			if gotSource != update.source {
				t.Fatalf("final avatar_url=%q source=%q, want source=%q", gotURL, gotSource, update.source)
			}
			return
		}
	}
	t.Fatalf("final avatar_url=%q source=%q was not one of the committed updates", gotURL, gotSource)
}

func createAvatarTestAgent(t *testing.T, displayName string, avatarURL *string) AgentResponse {
	t.Helper()
	body := map[string]any{
		"display_name":         displayName,
		"runtime_id":           testRuntimeID,
		"visibility":           "private",
		"max_concurrent_tasks": 1,
	}
	if avatarURL != nil {
		body["avatar_url"] = *avatarURL
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

func updateAvatarTestAgent(t *testing.T, agentID, avatarURL string, wantStatus int) AgentResponse {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(http.MethodPut, "/api/agents/"+agentID, map[string]any{
		"avatar_url": avatarURL,
	}), "id", agentID)
	testHandler.UpdateAgent(recorder, request)
	if recorder.Code != wantStatus {
		t.Fatalf("update avatar %q: expected %d, got %d: %s", avatarURL, wantStatus, recorder.Code, recorder.Body.String())
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

func requirePersistedAgentAvatar(t *testing.T, agentID, wantURL, wantSource string) {
	t.Helper()
	var gotURL, gotSource string
	if err := testPool.QueryRow(context.Background(), `
		SELECT avatar_url, avatar_source
		FROM agent
		WHERE id = $1
	`, parseUUID(agentID)).Scan(&gotURL, &gotSource); err != nil {
		t.Fatalf("read persisted avatar: %v", err)
	}
	if gotURL != wantURL || gotSource != wantSource {
		t.Fatalf("persisted avatar_url=%q source=%q, want url=%q source=%q", gotURL, gotSource, wantURL, wantSource)
	}
}
