package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestGetComputerWorkDigestOwnerReceivesMetadataDigest(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	daemonID, hub, conn := setupComputerWorkDigestLiveBinding(t, testUserID)
	start := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	end := start.Add(7 * 24 * time.Hour)
	want := protocol.WorkDigest{
		ComputerID: daemonID,
		Window:     protocol.WorkDigestWindow{Start: start, End: end},
		Repos: []protocol.WorkDigestRepo{{
			Root:    "/home/owner/code/app",
			Remotes: []string{"git@github.com:org/app.git"},
			Commits: []protocol.WorkDigestCommit{{
				Hash:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				At:         start.Add(2 * time.Hour),
				Author:     "owner",
				Subject:    "wire SSO login",
				FileCount:  3,
				Insertions: 40,
				Deletions:  8,
			}},
			Dirty: []protocol.WorkDigestDirtyPath{{
				Path:   "internal/auth/sso.go",
				Status: protocol.WorkDigestDirtyModified,
			}},
		}},
	}
	go replyComputerWorkDigest(t, conn, want, false)

	local := *testHandler
	local.DaemonHub = hub
	w := httptest.NewRecorder()
	local.GetComputerWorkDigest(w, computerWorkDigestRequest(testUserID, daemonID, start, end))
	if w.Code != http.StatusOK {
		t.Fatalf("owner digest = %d: %s", w.Code, w.Body.String())
	}
	got, err := protocol.ParseWorkDigest(w.Body.Bytes())
	if err != nil {
		t.Fatalf("owner digest shape: %v body=%s", err, w.Body.String())
	}
	if got.ComputerID != daemonID || got.Disabled || len(got.Repos) != 1 || got.Repos[0].Commits[0].Subject != "wire SSO login" {
		t.Fatalf("owner digest %+v", got)
	}
	if strings.Contains(w.Body.String(), `"content"`) || strings.Contains(w.Body.String(), `"diff"`) {
		t.Fatalf("digest carried file body fields: %s", w.Body.String())
	}
}

func TestGetComputerWorkDigestRejectsWorkspaceMemberWhoIsNotOwner(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	daemonID := setupComputerWorkDigestOwner(t, testUserID)
	memberID := createRuntimeLocalSkillTestMember(t, "member")
	start := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	w := httptest.NewRecorder()
	testHandler.GetComputerWorkDigest(w, computerWorkDigestRequest(memberID, daemonID, start, start.Add(24*time.Hour)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-owner digest = %d: %s", w.Code, w.Body.String())
	}
}

func TestGetComputerWorkDigestDisabledReturnsEmptyRepos(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	daemonID, hub, conn := setupComputerWorkDigestLiveBinding(t, testUserID)
	start := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	end := start.Add(7 * 24 * time.Hour)
	go replyComputerWorkDigest(t, conn, protocol.WorkDigest{
		ComputerID: daemonID,
		Window:     protocol.WorkDigestWindow{Start: start, End: end},
		Disabled:   true,
		Repos:      []protocol.WorkDigestRepo{},
	}, false)

	local := *testHandler
	local.DaemonHub = hub
	w := httptest.NewRecorder()
	local.GetComputerWorkDigest(w, computerWorkDigestRequest(testUserID, daemonID, start, end))
	if w.Code != http.StatusOK {
		t.Fatalf("disabled digest = %d: %s", w.Code, w.Body.String())
	}
	got, err := protocol.ParseWorkDigest(w.Body.Bytes())
	if err != nil {
		t.Fatalf("disabled digest shape: %v body=%s", err, w.Body.String())
	}
	if !got.Disabled || len(got.Repos) != 0 {
		t.Fatalf("disabled digest %+v", got)
	}
}

func TestGetComputerWorkDigestOfflineComputerReturnsExplicitError(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	daemonID := setupComputerWorkDigestOwner(t, testUserID)
	start := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	local := *testHandler
	local.DaemonHub = daemonws.NewHub()
	w := httptest.NewRecorder()
	local.GetComputerWorkDigest(w, computerWorkDigestRequest(testUserID, daemonID, start, start.Add(24*time.Hour)))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("offline digest = %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "computer_offline") {
		t.Fatalf("offline error = %s", w.Body.String())
	}
}

func setupComputerWorkDigestOwner(t *testing.T, ownerID string) string {
	t.Helper()
	daemonID := "work-digest-" + uuid.NewString()
	if _, err := testPool.Exec(context.Background(), `INSERT INTO computer_identity_owner (daemon_id, user_id) VALUES ($1, $2)`, daemonID, ownerID); err != nil {
		t.Fatal(err)
	}
	bindMachineUpgradeWorkspace(t, daemonID, testWorkspaceID, ownerID)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_identity_owner WHERE daemon_id=$1`, daemonID)
	})
	return daemonID
}

func setupComputerWorkDigestLiveBinding(t *testing.T, ownerID string) (string, *daemonws.Hub, *websocket.Conn) {
	t.Helper()
	daemonID := setupComputerWorkDigestOwner(t, ownerID)
	hub := daemonws.NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, daemonws.ClientIdentity{DaemonID: daemonID, WorkspaceID: testWorkspaceID})
	}))
	t.Cleanup(server.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ready, err := json.Marshal(protocol.Message{
		Type: protocol.EventWorkspaceRunnerReady,
		Payload: mustMarshalJSON(protocol.WorkspaceRunnerReadyPayload{
			WorkspaceID: testWorkspaceID, DaemonInstanceID: "instance-work-digest",
			ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceRunnerAgentProcess},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, ready); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for hub.WorkspaceRunnerConnectionCount(daemonID, testWorkspaceID) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("Binding did not become ready")
		}
		time.Sleep(time.Millisecond)
	}
	return daemonID, hub, conn
}

func computerWorkDigestRequest(userID, daemonID string, start, end time.Time) *http.Request {
	path := "/api/computers/" + daemonID + "/work-digest?start=" + start.Format(time.RFC3339) + "&end=" + end.Format(time.RFC3339)
	return withURLParam(newRequestAsUser(userID, http.MethodGet, path, nil), "daemonId", daemonID)
}

func replyComputerWorkDigest(t *testing.T, conn *websocket.Conn, digest protocol.WorkDigest, fail bool) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Error(err)
		return
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Error(err)
		return
	}
	var message protocol.Message
	if err := json.Unmarshal(raw, &message); err != nil {
		t.Error(err)
		return
	}
	if message.Type != protocol.EventComputerWorkDigest {
		t.Errorf("unexpected Binding frame %q", message.Type)
		return
	}
	var command protocol.ComputerWorkDigestPayload
	if err := json.Unmarshal(message.Payload, &command); err != nil {
		t.Error(err)
		return
	}
	if err := command.Validate(); err != nil {
		t.Error(err)
		return
	}
	done := protocol.ComputerWorkDigestDonePayload{RequestID: command.RequestID, OK: !fail}
	if fail {
		done.Error = "harvest failed"
	} else {
		copyDigest := digest
		done.Digest = &copyDigest
	}
	frame, err := json.Marshal(protocol.Message{Type: protocol.EventComputerWorkDigestDone, Payload: mustMarshalJSON(done)})
	if err != nil {
		t.Error(err)
		return
	}
	if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
		t.Error(err)
	}
}
