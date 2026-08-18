package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestPatchComputerWorkJournalRejectsNonOwner(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	daemonID := setupComputerWorkDigestOwner(t, testUserID)
	memberID := createRuntimeLocalSkillTestMember(t, "member")
	w := httptest.NewRecorder()
	testHandler.PatchComputerWorkJournal(w, computerWorkJournalRequest(memberID, daemonID, true))
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-owner journal patch = %d: %s", w.Code, w.Body.String())
	}
}

func TestComputerWorkJournalToggleChangesOwnerDigest(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	daemonID, hub, conn := setupComputerWorkDigestLiveBinding(t, testUserID)
	go serveComputerWorkJournalFixture(t, conn, daemonID)
	local := *testHandler
	local.DaemonHub = hub
	start := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	end := start.Add(7 * 24 * time.Hour)

	off := httptest.NewRecorder()
	local.GetComputerWorkDigest(off, computerWorkDigestRequest(testUserID, daemonID, start, end))
	if off.Code != http.StatusOK {
		t.Fatalf("disabled digest = %d: %s", off.Code, off.Body.String())
	}
	disabled, err := protocol.ParseWorkDigest(off.Body.Bytes())
	if err != nil || !disabled.Disabled || len(disabled.Repos) != 0 {
		t.Fatalf("disabled digest %+v err=%v", disabled, err)
	}

	enable := httptest.NewRecorder()
	local.PatchComputerWorkJournal(enable, computerWorkJournalRequest(testUserID, daemonID, true))
	if enable.Code != http.StatusOK {
		t.Fatalf("enable journal = %d: %s", enable.Code, enable.Body.String())
	}

	on := httptest.NewRecorder()
	local.GetComputerWorkDigest(on, computerWorkDigestRequest(testUserID, daemonID, start, end))
	if on.Code != http.StatusOK {
		t.Fatalf("enabled digest = %d: %s", on.Code, on.Body.String())
	}
	harvested, err := protocol.ParseWorkDigest(on.Body.Bytes())
	if err != nil || harvested.Disabled || len(harvested.Repos) != 1 {
		t.Fatalf("enabled digest %+v err=%v", harvested, err)
	}

	disable := httptest.NewRecorder()
	local.PatchComputerWorkJournal(disable, computerWorkJournalRequest(testUserID, daemonID, false))
	if disable.Code != http.StatusOK {
		t.Fatalf("disable journal = %d: %s", disable.Code, disable.Body.String())
	}
	again := httptest.NewRecorder()
	local.GetComputerWorkDigest(again, computerWorkDigestRequest(testUserID, daemonID, start, end))
	closed, err := protocol.ParseWorkDigest(again.Body.Bytes())
	if err != nil || !closed.Disabled || len(closed.Repos) != 0 {
		t.Fatalf("re-disabled digest %+v err=%v body=%s", closed, err, again.Body.String())
	}

	list := httptest.NewRecorder()
	local.ListComputers(list, newRequestAsUser(testUserID, http.MethodGet, "/api/computers", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list computers = %d: %s", list.Code, list.Body.String())
	}
	enabledBit, ok := workJournalEnabledFor(list.Body.Bytes(), daemonID)
	if !ok || enabledBit {
		t.Fatalf("list after disable = %s", list.Body.String())
	}
}

func TestPatchComputerWorkJournalOfflineReturnsExplicitError(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	daemonID := setupComputerWorkDigestOwner(t, testUserID)
	local := *testHandler
	local.DaemonHub = daemonws.NewHub()
	w := httptest.NewRecorder()
	local.PatchComputerWorkJournal(w, computerWorkJournalRequest(testUserID, daemonID, true))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("offline journal patch = %d: %s", w.Code, w.Body.String())
	}
}

func computerWorkJournalRequest(userID, daemonID string, enabled bool) *http.Request {
	req := newRequestAsUser(userID, http.MethodPatch, "/api/computers/"+daemonID+"/work-journal", map[string]bool{"enabled": enabled})
	return withURLParam(req, "daemonId", daemonID)
}

func serveComputerWorkJournalFixture(t *testing.T, conn *websocket.Conn, daemonID string) {
	t.Helper()
	enabled := false
	start := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	fixture := protocol.WorkDigest{
		ComputerID: daemonID,
		Window:     protocol.WorkDigestWindow{Start: start, End: start.Add(7 * 24 * time.Hour)},
		Repos: []protocol.WorkDigestRepo{{
			Root: "/home/owner/code/app",
			Commits: []protocol.WorkDigestCommit{{
				Hash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", At: start.Add(2 * time.Hour),
				Author: "owner", Subject: "wire SSO login", FileCount: 1, Insertions: 1,
			}},
			Dirty: []protocol.WorkDigestDirtyPath{{Path: "internal/auth/sso.go", Status: protocol.WorkDigestDirtyUntracked}},
		}},
	}
	for {
		if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			return
		}
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var message protocol.Message
		if err := json.Unmarshal(raw, &message); err != nil {
			t.Error(err)
			return
		}
		switch message.Type {
		case protocol.EventComputerWorkJournal:
			var command protocol.ComputerWorkJournalPayload
			if err := json.Unmarshal(message.Payload, &command); err != nil {
				t.Error(err)
				return
			}
			enabled = command.Enabled
			frame, err := json.Marshal(protocol.Message{
				Type: protocol.EventComputerWorkJournalDone,
				Payload: mustMarshalJSON(protocol.ComputerWorkJournalDonePayload{
					RequestID: command.RequestID, OK: true, Enabled: enabled,
				}),
			})
			if err != nil || conn.WriteMessage(websocket.TextMessage, frame) != nil {
				return
			}
		case protocol.EventComputerWorkDigest:
			var command protocol.ComputerWorkDigestPayload
			if err := json.Unmarshal(message.Payload, &command); err != nil {
				t.Error(err)
				return
			}
			digest := protocol.WorkDigest{
				ComputerID: daemonID,
				Window:     command.Window(),
				Disabled:   !enabled,
				Repos:      []protocol.WorkDigestRepo{},
			}
			if enabled {
				digest = fixture
				digest.Window = command.Window()
				digest.Disabled = false
			}
			frame, err := json.Marshal(protocol.Message{
				Type: protocol.EventComputerWorkDigestDone,
				Payload: mustMarshalJSON(protocol.ComputerWorkDigestDonePayload{
					RequestID: command.RequestID, OK: true, Digest: &digest,
				}),
			})
			if err != nil || conn.WriteMessage(websocket.TextMessage, frame) != nil {
				return
			}
		}
	}
}

func workJournalEnabledFor(raw []byte, daemonID string) (bool, bool) {
	var rows []computerConnectionResponse
	if err := json.Unmarshal(raw, &rows); err != nil {
		return false, false
	}
	for _, row := range rows {
		if row.DaemonID == daemonID {
			return row.WorkJournalEnabled, true
		}
	}
	return false, false
}
