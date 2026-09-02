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

func TestPatchComputerCollectRootsRejectsNonOwner(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	daemonID := setupComputerWorkDigestOwner(t, testUserID)
	memberID := createRuntimeLocalSkillTestMember(t, "member")
	w := httptest.NewRecorder()
	testHandler.PatchComputerCollectRoots(w, computerCollectRootsRequest(memberID, daemonID, http.MethodPatch, []string{"~/code"}))
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-owner collect-roots patch = %d: %s", w.Code, w.Body.String())
	}
}

func TestComputerCollectRootsGetAndPatchRoundTrip(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	daemonID, hub, conn := setupComputerWorkDigestLiveBinding(t, testUserID)
	go serveComputerCollectRootsFixture(t, conn)
	local := *testHandler
	local.DaemonHub = hub

	empty := httptest.NewRecorder()
	local.GetComputerCollectRoots(empty, computerCollectRootsRequest(testUserID, daemonID, http.MethodGet, nil))
	if empty.Code != http.StatusOK {
		t.Fatalf("empty get = %d: %s", empty.Code, empty.Body.String())
	}
	if got := decodeCollectRoots(t, empty.Body.Bytes()); len(got) != 0 {
		t.Fatalf("unset roots = %#v", got)
	}

	patch := httptest.NewRecorder()
	local.PatchComputerCollectRoots(patch, computerCollectRootsRequest(testUserID, daemonID, http.MethodPatch, []string{"~/code", "/opt/app"}))
	if patch.Code != http.StatusOK {
		t.Fatalf("patch = %d: %s", patch.Code, patch.Body.String())
	}
	if got := decodeCollectRoots(t, patch.Body.Bytes()); len(got) != 2 || got[0] != "~/code" || got[1] != "/opt/app" {
		t.Fatalf("patched %#v", got)
	}

	again := httptest.NewRecorder()
	local.GetComputerCollectRoots(again, computerCollectRootsRequest(testUserID, daemonID, http.MethodGet, nil))
	if got := decodeCollectRoots(t, again.Body.Bytes()); len(got) != 2 || got[0] != "~/code" {
		t.Fatalf("get after patch %#v", got)
	}

	clear := httptest.NewRecorder()
	local.PatchComputerCollectRoots(clear, computerCollectRootsRequest(testUserID, daemonID, http.MethodPatch, []string{}))
	if got := decodeCollectRoots(t, clear.Body.Bytes()); len(got) != 0 {
		t.Fatalf("cleared %#v", got)
	}
}

func TestPatchComputerCollectRootsOfflineReturnsExplicitError(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	daemonID := setupComputerWorkDigestOwner(t, testUserID)
	local := *testHandler
	local.DaemonHub = daemonws.NewHub()
	w := httptest.NewRecorder()
	local.PatchComputerCollectRoots(w, computerCollectRootsRequest(testUserID, daemonID, http.MethodPatch, []string{"~/code"}))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("offline collect-roots patch = %d: %s", w.Code, w.Body.String())
	}
}

func computerCollectRootsRequest(userID, daemonID, method string, roots []string) *http.Request {
	var body any
	if method == http.MethodPatch {
		body = map[string]any{"roots": roots}
	}
	req := newRequestAsUser(userID, method, "/api/computers/"+daemonID+"/collect-roots", body)
	return withURLParam(req, "daemonId", daemonID)
}

func decodeCollectRoots(t *testing.T, raw []byte) []string {
	t.Helper()
	var payload computerCollectRootsResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode collect roots: %v body=%s", err, raw)
	}
	if payload.Roots == nil {
		return []string{}
	}
	return payload.Roots
}

func serveComputerCollectRootsFixture(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	roots := []string{}
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
		if message.Type != protocol.EventComputerCollectRoots {
			continue
		}
		var command protocol.ComputerCollectRootsPayload
		if err := json.Unmarshal(message.Payload, &command); err != nil {
			t.Error(err)
			return
		}
		if command.Set {
			roots = append([]string{}, command.Roots...)
			if roots == nil {
				roots = []string{}
			}
		}
		frame, err := json.Marshal(protocol.Message{
			Type: protocol.EventComputerCollectRootsDone,
			Payload: mustMarshalJSON(protocol.ComputerCollectRootsDonePayload{
				RequestID: command.RequestID, OK: true, Roots: roots,
			}),
		})
		if err != nil || conn.WriteMessage(websocket.TextMessage, frame) != nil {
			return
		}
	}
}
