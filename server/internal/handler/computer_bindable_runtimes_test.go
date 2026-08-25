package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// Picking where an agent runs is a two-level choice — machine, then provider —
// because that is the shape of the thing: a Computer's daemon core hosts one
// runtime per provider. So the Computer list carries the runtimes the caller
// may bind to, already filtered. Visibility exists only at the runtime level,
// and the server is now the only place that filter lives; no client re-derives
// it from a flat runtime list.
func TestListComputers_CarriesOnlyBindableRuntimes(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	suffix := uuid.NewString()
	daemonID := "bindable-daemon-" + suffix

	var otherUserID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id
	`, "Bindable Owner "+suffix[:8], "bindable-"+suffix+"@multica.test").Scan(&otherUserID); err != nil {
		t.Fatalf("create other user: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO computers (id, user_id) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING
	`, daemonID, otherUserID); err != nil {
		t.Fatalf("create computer: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO computer_workspace_bindings
		  (daemon_id, workspace_id, user_id, execution_token_hash, active)
		VALUES ($1, $2, $3, $4, TRUE)
	`, daemonID, testWorkspaceID, otherUserID, "bindable-"+suffix); err != nil {
		t.Fatalf("bind computer: %v", err)
	}

	// Same machine, two runtimes: one shared with the workspace, one private
	// to its owner. The caller is not that owner.
	newRuntime := func(provider, visibility string) string {
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_runtime (
			  workspace_id, daemon_id, name, runtime_mode, provider, status,
			  device_info, metadata, visibility, last_seen_at
			) VALUES ($1, $2, $3, 'local', $4, 'online', '', '{}'::jsonb, $5, now())
			RETURNING id
		`, testWorkspaceID, daemonID, provider+"-"+suffix, provider, visibility).Scan(&id); err != nil {
			t.Fatalf("create %s runtime: %v", visibility, err)
		}
		return id
	}
	publicRuntimeID := newRuntime("claude", "public")
	privateRuntimeID := newRuntime("cursor", "private")

	t.Cleanup(func() {
		bg := context.Background()
		_, _ = testPool.Exec(bg, `DELETE FROM agent_runtime WHERE daemon_id = $1`, daemonID)
		_, _ = testPool.Exec(bg, `DELETE FROM computer_workspace_bindings WHERE daemon_id = $1`, daemonID)
		_, _ = testPool.Exec(bg, `DELETE FROM computers WHERE id = $1`, daemonID)
		_, _ = testPool.Exec(bg, `DELETE FROM "user" WHERE id = $1`, otherUserID)
	})

	w := httptest.NewRecorder()
	testHandler.ListComputers(w, newRequest(http.MethodGet, "/api/computers", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("ListComputers: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var computers []computerConnectionResponse
	if err := json.NewDecoder(w.Body).Decode(&computers); err != nil {
		t.Fatalf("decode computers: %v", err)
	}

	var found *computerConnectionResponse
	for i := range computers {
		if computers[i].DaemonID == daemonID {
			found = &computers[i]
		}
	}
	// The machine itself stays visible — Computers have no visibility of their
	// own, only their runtimes do.
	if found == nil {
		t.Fatalf("another member's Computer missing from the list (%d rows)", len(computers))
	}

	ids := make([]string, 0, len(found.Runtimes))
	for _, rt := range found.Runtimes {
		ids = append(ids, rt.ID)
	}
	if len(ids) != 1 || ids[0] != publicRuntimeID {
		t.Fatalf("bindable runtimes = %v, want only the public one (%s); private %s must not be offered",
			ids, publicRuntimeID, privateRuntimeID)
	}
	if found.Runtimes[0].Provider != "claude" {
		t.Fatalf("bindable provider = %q, want claude", found.Runtimes[0].Provider)
	}
}
