// SPDX-License-Identifier: Apache-2.0

package arealrl

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testAdminKey = "admin-secret-key"
	testProxyKey = "session-proxy-key-xyz"
)

func TestStartSession_RequestAndResponse(t *testing.T) {
	var (
		gotPath   string
		gotMethod string
		gotAuth   string
		gotBody   map[string]any
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"session_id":"task-123-0","api_key":"` + testProxyKey + `"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, testAdminKey)
	creds, err := c.StartSession(context.Background(), "task-123", "")
	if err != nil {
		t.Fatalf("StartSession returned error: %v", err)
	}

	if gotPath != "/rl/start_session" {
		t.Errorf("path = %q, want /rl/start_session", gotPath)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotAuth != "Bearer "+testAdminKey {
		t.Errorf("auth = %q, want Bearer admin key", gotAuth)
	}
	if gotBody["session_ref"] != "task-123" {
		t.Errorf("body session_ref = %v, want task-123", gotBody["session_ref"])
	}
	if _, ok := gotBody["task_id"]; ok {
		t.Errorf("body must not contain task_id (session_ref is canonical), got %v", gotBody["task_id"])
	}
	// group_size:1 per spec §4.6
	if gs, ok := gotBody["group_size"]; !ok || gs.(float64) != 1 {
		t.Errorf("body group_size = %v, want 1", gotBody["group_size"])
	}

	if creds.SessionID != "task-123-0" {
		t.Errorf("SessionID = %q, want task-123-0", creds.SessionID)
	}
	if creds.ProxyKey != testProxyKey {
		t.Errorf("ProxyKey = %q, want %q (mapped from api_key)", creds.ProxyKey, testProxyKey)
	}
}

func TestStartSession_Non2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"Invalid admin API key."}`))
	}))
	defer srv.Close()

	c := New(srv.URL, testAdminKey)
	if _, err := c.StartSession(context.Background(), "task-123", ""); err == nil {
		t.Fatal("expected error on non-2xx, got nil")
	}
}

func TestStartSession_EmptySessionIDIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"session_id":"","api_key":"` + testProxyKey + `"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, testAdminKey)
	if _, err := c.StartSession(context.Background(), "task-123", ""); err == nil {
		t.Fatal("expected error on empty session_id, got nil")
	}
}

func TestStartSession_EmptyAPIKeyIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"session_id":"task-123-0","api_key":""}`))
	}))
	defer srv.Close()

	c := New(srv.URL, testAdminKey)
	if _, err := c.StartSession(context.Background(), "task-123", ""); err == nil {
		t.Fatal("expected error on empty api_key, got nil")
	}
}

func TestSetReward_RequestAndAuth(t *testing.T) {
	var (
		gotPath string
		gotAuth string
		gotBody map[string]any
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"message":"success"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, testAdminKey)
	if err := c.SetReward(context.Background(), testProxyKey, 1.0); err != nil {
		t.Fatalf("SetReward returned error: %v", err)
	}

	if gotPath != "/rl/set_reward" {
		t.Errorf("path = %q, want /rl/set_reward", gotPath)
	}
	if gotAuth != "Bearer "+testProxyKey {
		t.Errorf("auth = %q, want Bearer proxy key (session-key auth)", gotAuth)
	}
	if r, ok := gotBody["reward"]; !ok || r.(float64) != 1.0 {
		t.Errorf("body reward = %v, want 1.0", gotBody["reward"])
	}
	// Must NOT carry a session_id per experimental contract §4.6.
	if _, ok := gotBody["session_id"]; ok {
		t.Errorf("body unexpectedly contains session_id: %v", gotBody)
	}
}

func TestSetReward_Non2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer srv.Close()

	c := New(srv.URL, testAdminKey)
	if err := c.SetReward(context.Background(), testProxyKey, 1.0); err == nil {
		t.Fatal("expected error on non-2xx, got nil")
	}
}

func TestStartSession_IncludesEnvIDWhenNonEmpty(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &gotBody)
		json.NewEncoder(w).Encode(map[string]any{"session_id": "s1", "api_key": "k1"})
	}))
	defer srv.Close()
	c := New(srv.URL, "admin-key")
	_, err := c.StartSession(context.Background(), "task-1", "env_abc")
	require.NoError(t, err)
	assert.Equal(t, "env_abc", gotBody["env_id"])
}

func TestStartSession_OmitsEnvIDWhenEmpty(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &gotBody)
		json.NewEncoder(w).Encode(map[string]any{"session_id": "s1", "api_key": "k1"})
	}))
	defer srv.Close()
	c := New(srv.URL, "admin-key")
	_, err := c.StartSession(context.Background(), "task-1", "")
	require.NoError(t, err)
	_, hasEnvID := gotBody["env_id"]
	assert.False(t, hasEnvID, "env_id should be omitted when empty")
}

func TestCloseSegment_RequestAndAuth(t *testing.T) {
	var (
		gotPath string
		gotAuth string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"message":"success","interaction_count":3,"session_id":"s1","trajectory_id":7,"ready_transition":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL, testAdminKey)
	trajectoryID, err := c.CloseSegment(context.Background(), testProxyKey)
	require.NoError(t, err)
	assert.Equal(t, "/rl/close_segment", gotPath)
	assert.Equal(t, "Bearer "+testProxyKey, gotAuth, "close_segment uses session-key auth")
	assert.Equal(t, 7, trajectoryID)
}

func TestCloseSegment_Non2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail":"no active segment"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, testAdminKey)
	if _, err := c.CloseSegment(context.Background(), testProxyKey); err == nil {
		t.Fatal("expected error on non-2xx, got nil")
	}
}

func TestCloseSegment_MissingTrajectoryIDIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// trajectory_id omitted -> null: no trajectory closed.
		_, _ = w.Write([]byte(`{"message":"success","interaction_count":0,"session_id":"s1","ready_transition":false}`))
	}))
	defer srv.Close()

	c := New(srv.URL, testAdminKey)
	if _, err := c.CloseSegment(context.Background(), testProxyKey); err == nil {
		t.Fatal("expected error when trajectory_id is missing, got nil")
	}
}

func TestExportTrajectory_RequestAndAuth(t *testing.T) {
	var (
		gotPath string
		gotAuth string
		gotBody map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"traj":{"shard_id":"abc-123","input_ids":[1,2]}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, testAdminKey)
	traj, err := c.ExportTrajectory(context.Background(), "s1", 7)
	require.NoError(t, err)
	assert.Equal(t, "/export_trajectories", gotPath)
	assert.Equal(t, "Bearer "+testAdminKey, gotAuth, "export uses admin-key auth")
	// Body shape: per-segment export, session kept alive.
	assert.Equal(t, []any{"s1"}, gotBody["session_ids"])
	assert.EqualValues(t, 7, gotBody["trajectory_id"])
	assert.Equal(t, false, gotBody["remove_session"])
	if _, ok := gotBody["group_id"]; ok {
		t.Errorf("body unexpectedly contains group_id: %v", gotBody)
	}
	// Returned raw traj carries the tensor ref.
	var parsed map[string]any
	require.NoError(t, json.Unmarshal(traj, &parsed))
	assert.Equal(t, "abc-123", parsed["shard_id"])
}

func TestExportTrajectory_Non2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"trajectory_id 99 not found in session s1"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, testAdminKey)
	if _, err := c.ExportTrajectory(context.Background(), "s1", 99); err == nil {
		t.Fatal("expected error on non-2xx, got nil")
	}
}
