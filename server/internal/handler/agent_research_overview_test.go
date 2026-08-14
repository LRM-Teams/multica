package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestAgentResearchSessionWithoutAttemptReturnsBoundedFleetOverview(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	sessionID := seedInitializedResearchSessionForSnapshotTest(t)
	workspaceID := parseUUID(testWorkspaceID)
	fleet, err := testHandler.Queries.GetResearchFleetByWorkspace(context.Background(), workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	members, err := testHandler.Queries.ListResearchFleetMembers(context.Background(), db.ListResearchFleetMembersParams{
		FleetID: fleet.ID, WorkspaceID: workspaceID,
	})
	if err != nil || len(members) == 0 {
		t.Fatalf("fleet members err=%v len=%d", err, len(members))
	}
	agentID := uuidToString(members[0].AgentID)
	path := "/api/agent/research/sessions/" + uuidToString(sessionID)
	req := withURLParam(newRequest(http.MethodGet, path, nil), "id", uuidToString(sessionID))
	req.Header.Set("X-Agent-ID", agentID)
	req = req.WithContext(middleware.WithAgentPrincipal(req.Context(), middleware.AgentPrincipal{
		AgentID: agentID, WorkspaceID: testWorkspaceID, ActorSource: "agent_credential",
	}))

	recorder := httptest.NewRecorder()
	testHandler.GetAgentResearchSessionSnapshot(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body map[string]json.RawMessage
	if err = json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 2 || body["session"] == nil || body["fleet"] == nil {
		t.Fatalf("overview keys=%v want exactly session/fleet", mapKeys(body))
	}
	encoded := recorder.Body.String()
	for _, forbidden := range []string{
		`"goal"`, `"title"`, `"run"`, `"nodes"`, `"edges"`, `"sources"`,
		`"messages"`, `"report"`, `"evals"`, `"product_rounds"`,
		`"hash"`, `"grant"`, `"workspace_id"`, `"fleet_id"`,
	} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("bounded overview leaked %s in %s", forbidden, encoded)
		}
	}
}

func TestAgentResearchSessionWithoutAttemptRejectsNonFleetMember(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	sessionID := seedInitializedResearchSessionForSnapshotTest(t)
	agentID := uuid.NewString()
	path := "/api/agent/research/sessions/" + uuidToString(sessionID)
	req := withURLParam(newRequest(http.MethodGet, path, nil), "id", uuidToString(sessionID))
	req.Header.Set("X-Agent-ID", agentID)
	req = req.WithContext(middleware.WithAgentPrincipal(req.Context(), middleware.AgentPrincipal{
		AgentID: agentID, WorkspaceID: testWorkspaceID, ActorSource: "agent_credential",
	}))

	recorder := httptest.NewRecorder()
	testHandler.GetAgentResearchSessionSnapshot(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s want 403", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), uuidToString(sessionID)) {
		t.Fatalf("non-member response leaked session id: %s", recorder.Body.String())
	}
}

func mapKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
