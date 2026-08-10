package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestCanDeleteRuntimeRequiresExactOwner(t *testing.T) {
	ownerID := uuid.NewString()
	otherID := uuid.NewString()
	tests := []struct {
		name    string
		member  db.Member
		runtime db.AgentRuntime
		want    bool
	}{
		{
			name:    "runtime owner",
			member:  db.Member{UserID: parseUUID(ownerID), Role: "member"},
			runtime: db.AgentRuntime{OwnerID: parseUUID(ownerID)},
			want:    true,
		},
		{
			name:    "workspace admin is not runtime owner",
			member:  db.Member{UserID: parseUUID(otherID), Role: "admin"},
			runtime: db.AgentRuntime{OwnerID: parseUUID(ownerID)},
		},
		{
			name:    "workspace owner is not runtime owner",
			member:  db.Member{UserID: parseUUID(otherID), Role: "owner"},
			runtime: db.AgentRuntime{OwnerID: parseUUID(ownerID)},
		},
		{
			name:    "orphan runtime has no implicit override",
			member:  db.Member{UserID: parseUUID(ownerID), Role: "owner"},
			runtime: db.AgentRuntime{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := canDeleteRuntime(tt.member, tt.runtime); got != tt.want {
				t.Fatalf("canDeleteRuntime() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRuntimeDeleteEndpointsRequireRuntimeOwner(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	daemonID := "delete-owner-gate-" + uuid.NewString()
	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, owner_id, last_seen_at
		)
		VALUES ($1, $2, $3, 'local', 'claude', 'offline',
		        'Owner Gate Computer', '{}'::jsonb, $4, now())
		RETURNING id
	`, testWorkspaceID, daemonID, "Owner Gate "+uuid.NewString(), testUserID).Scan(&runtimeID); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})

	callers := []struct {
		name   string
		userID string
	}{
		{name: "plain member", userID: createRuntimeLocalSkillTestMember(t, "member")},
		{name: "workspace admin", userID: createRuntimeLocalSkillTestMember(t, "admin")},
	}
	endpoints := []struct {
		name     string
		method   string
		path     string
		paramKey string
		paramVal string
		body     any
		handle   func(http.ResponseWriter, *http.Request)
	}{
		{
			name:     "delete runtime",
			method:   http.MethodDelete,
			path:     "/api/runtimes/" + runtimeID,
			paramKey: "runtimeId",
			paramVal: runtimeID,
			handle:   testHandler.DeleteAgentRuntime,
		},
		{
			name:     "archive agents and delete runtime",
			method:   http.MethodPost,
			path:     "/api/runtimes/" + runtimeID + "/archive-agents-and-delete",
			paramKey: "runtimeId",
			paramVal: runtimeID,
			body:     map[string]any{"expected_active_agent_ids": []string{}},
			handle:   testHandler.ArchiveAgentsAndDeleteRuntime,
		},
		{
			name:     "delete computer",
			method:   http.MethodDelete,
			path:     "/api/computers/" + daemonID,
			paramKey: "daemonId",
			paramVal: daemonID,
			handle:   testHandler.DeleteComputer,
		},
	}

	for _, caller := range callers {
		for _, endpoint := range endpoints {
			t.Run(caller.name+"/"+endpoint.name, func(t *testing.T) {
				req := newRequestAsUser(
					caller.userID,
					endpoint.method,
					endpoint.path,
					endpoint.body,
				)
				req = withURLParam(req, endpoint.paramKey, endpoint.paramVal)
				w := httptest.NewRecorder()

				endpoint.handle(w, req)

				if w.Code != http.StatusForbidden {
					t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
				}

				var count int
				if err := testPool.QueryRow(ctx, `
					SELECT count(*) FROM agent_runtime WHERE id = $1
				`, runtimeID).Scan(&count); err != nil {
					t.Fatalf("read runtime after forbidden request: %v", err)
				}
				if count != 1 {
					t.Fatalf("forbidden request mutated runtime: count=%d", count)
				}
			})
		}
	}
}
