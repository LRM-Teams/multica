package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestResolveComputerName(t *testing.T) {
	tests := []struct {
		name                                        string
		deviceName, runtimeDisplayName, runtimeName string
		daemonID                                    string
		want                                        string
	}{
		{
			name:       "the Computer's own device name wins",
			deviceName: "s144", runtimeDisplayName: "renamed-runtime",
			runtimeName: "Cursor (other)", daemonID: "daemon-abcdef123",
			want: "s144",
		},
		{
			name:               "falls back to the runtime's user-set label",
			runtimeDisplayName: "s144", runtimeName: "Cursor (host)",
			daemonID: "daemon-abcdef123",
			want:     "s144",
		},
		{
			// A machine label, never the "Provider (host)" code-agent string.
			name:        "pulls the hostname out of a legacy runtime name",
			runtimeName: "Cursor (s144)", daemonID: "daemon-abcdef123",
			want: "s144",
		},
		{
			name:        "keeps a runtime name that carries no provider glue",
			runtimeName: "plain-box", daemonID: "daemon-abcdef123",
			want: "plain-box",
		},
		{
			name: "last resort is a short daemon id", daemonID: "daemon-abcdef123",
			want: "daemon-a",
		},
		{name: "nothing to show at all", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveComputerName(tt.deviceName, tt.runtimeDisplayName, tt.runtimeName, tt.daemonID)
			if got != tt.want {
				t.Fatalf("resolveComputerName = %q, want %q", got, tt.want)
			}
		})
	}
}

// The inspector used to resolve an agent's computer by looking its runtime id
// up in GET /api/runtimes — a list that deliberately drops another member's
// private runtime, so the lookup missed and the UI claimed the agent had no
// computer. This endpoint answers the other question directly, and must keep
// doing so for exactly that runtime.
func TestGetAgentRuntimeConfig_NamesComputerBehindPrivateRuntime(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	suffix := uuid.NewString()
	daemonID := "runtime-config-daemon-" + suffix

	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
		  workspace_id, daemon_id, name, runtime_mode, provider, status,
		  device_info, metadata, visibility, last_seen_at
		) VALUES ($1, $2, $3, 'local', 'claude', 'online',
		  '', '{}'::jsonb, 'private', now())
		RETURNING id
	`, testWorkspaceID, daemonID, "Claude Code (s144)").Scan(&runtimeID); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	// Owned by somebody else, so ListVisibleAgentRuntimes will not return it
	// to the caller — the exact shape that used to blank the Computer row.
	var otherUserID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id
	`, "Runtime Config Owner "+suffix[:8], "runtime-config-"+suffix+"@multica.test").Scan(&otherUserID); err != nil {
		t.Fatalf("create other user: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO computers (id, user_id) VALUES ($1, $2)
		ON CONFLICT (id) DO NOTHING
	`, daemonID, otherUserID); err != nil {
		t.Fatalf("create computer: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO computer_workspace_bindings
		  (daemon_id, workspace_id, user_id, execution_token_hash, active)
		VALUES ($1, $2, $3, $4, TRUE)
	`, daemonID, testWorkspaceID, otherUserID, "runtime-config-"+suffix); err != nil {
		t.Fatalf("bind computer: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE computers SET device_name = $2, cli_version = $3, os = $4 WHERE id = $1
	`, daemonID, "s144", "0.3.92", "linux"); err != nil {
		t.Fatalf("set computer metadata: %v", err)
	}

	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config, runtime_id,
			max_concurrent_tasks, owner_id, instructions, custom_env, custom_args, mcp_config, model
		) VALUES ($1, $2, '', 'local', '{}'::jsonb, $3, 1, $4, '', '{}'::jsonb, '[]'::jsonb, '{}'::jsonb, 'composer-1.5')
		RETURNING id
	`, testWorkspaceID, "runtime-config-agent-"+suffix, runtimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = testPool.Exec(bg, `DELETE FROM agent WHERE id = $1`, agentID)
		_, _ = testPool.Exec(bg, `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
		_, _ = testPool.Exec(bg, `DELETE FROM computer_workspace_bindings WHERE daemon_id = $1`, daemonID)
		_, _ = testPool.Exec(bg, `DELETE FROM computers WHERE id = $1`, daemonID)
		_, _ = testPool.Exec(bg, `DELETE FROM "user" WHERE id = $1`, otherUserID)
	})

	// Precondition: the manageable-computers list really does hide it.
	listW := httptest.NewRecorder()
	testHandler.ListAgentRuntimes(listW, newRequest(http.MethodGet, "/api/runtimes", nil))
	var manageable []AgentRuntimeResponse
	if err := json.NewDecoder(listW.Body).Decode(&manageable); err != nil {
		t.Fatalf("decode runtimes: %v", err)
	}
	for _, rt := range manageable {
		if rt.ID == runtimeID {
			t.Fatal("GET /api/runtimes returned another member's private runtime; the premise of this test is gone")
		}
	}

	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodGet, "/api/agents/"+agentID+"/runtime-config", nil), "id", agentID)
	testHandler.GetAgentRuntimeConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetAgentRuntimeConfig: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp AgentRuntimeConfigResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode runtime config: %v", err)
	}

	if resp.Computer == nil {
		t.Fatal("runtime config has no computer for an agent that is bound to one")
	}
	if resp.Computer.Name != "s144" {
		t.Fatalf("computer name = %q, want s144", resp.Computer.Name)
	}
	if resp.Computer.DaemonID != daemonID {
		t.Fatalf("computer daemon_id = %q, want %q", resp.Computer.DaemonID, daemonID)
	}
	if resp.Computer.CLIVersion != "0.3.92" {
		t.Fatalf("computer cli_version = %q, want 0.3.92", resp.Computer.CLIVersion)
	}
	if resp.Computer.OwnerID != otherUserID {
		t.Fatalf("computer owner_id = %q, want %q", resp.Computer.OwnerID, otherUserID)
	}
	if resp.Runtime == nil || resp.Runtime.Provider != "claude" {
		t.Fatalf("runtime = %+v, want provider claude", resp.Runtime)
	}
	if resp.Model != "composer-1.5" {
		t.Fatalf("model = %q, want composer-1.5", resp.Model)
	}
}

// agent.runtime_id is NOT NULL and FK-constrained, so a live agent always has
// a runtime row. "No computer" therefore means something narrower: the machine
// hosting that runtime is no longer bound to this workspace (a revoked
// binding leaves the runtime row behind, because that same FK blocks deleting
// it). Report the runtime and no computer — never invent one from the runtime
// row, which is exactly the layer confusion this endpoint removes.
func TestGetAgentRuntimeConfig_UnboundMachineHasNoComputer(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	suffix := uuid.NewString()

	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
		  workspace_id, daemon_id, name, runtime_mode, provider, status,
		  device_info, metadata, visibility, last_seen_at
		) VALUES ($1, $2, $3, 'local', 'claude', 'online',
		  '', '{}'::jsonb, 'private', now())
		RETURNING id
	`, testWorkspaceID, "dangling-daemon-"+suffix, "dangling-"+suffix).Scan(&runtimeID); err != nil {
		t.Fatalf("create runtime: %v", err)
	}

	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config, runtime_id,
			max_concurrent_tasks, owner_id, instructions, custom_env, custom_args, mcp_config
		) VALUES ($1, $2, '', 'local', '{}'::jsonb, $3, 1, $4, '', '{}'::jsonb, '[]'::jsonb, '{}'::jsonb)
		RETURNING id
	`, testWorkspaceID, "dangling-agent-"+suffix, runtimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = testPool.Exec(bg, `DELETE FROM agent WHERE id = $1`, agentID)
		_, _ = testPool.Exec(bg, `DELETE FROM agent_runner_launch_projection WHERE runtime_id = $1`, runtimeID)
		_, _ = testPool.Exec(bg, `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodGet, "/api/agents/"+agentID+"/runtime-config", nil), "id", agentID)
	testHandler.GetAgentRuntimeConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp AgentRuntimeConfigResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Computer != nil {
		t.Fatalf("reported a computer for a machine with no active binding: %+v", resp.Computer)
	}
	if resp.Runtime == nil || resp.Runtime.Provider != "claude" {
		t.Fatalf("runtime = %+v, want provider claude", resp.Runtime)
	}
}
