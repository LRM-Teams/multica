package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestUpdateAgentModelChangeClearsProviderBlock(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	runtimeID := createClaudeProviderRuntime(t)
	markBlocked := func(t *testing.T, agentID string) {
		t.Helper()
		if _, err := testPool.Exec(ctx, `
			UPDATE agent
			SET provider_blocked_until = NULL,
			    provider_block_detail = 'previous model failed'
			WHERE id = $1
		`, agentID); err != nil {
			t.Fatalf("mark provider blocked: %v", err)
		}
	}
	readBlock := func(t *testing.T, agentID string) (string, pgtype.Timestamptz) {
		t.Helper()
		var detail string
		var until pgtype.Timestamptz
		if err := testPool.QueryRow(ctx, `
			SELECT provider_block_detail, provider_blocked_until
			FROM agent
			WHERE id = $1
		`, agentID).Scan(&detail, &until); err != nil {
			t.Fatalf("read provider block: %v", err)
		}
		return detail, until
	}

	t.Run("changed model clears stale block", func(t *testing.T) {
		agentID := createAgentOnRuntime(t, "model-change-clears-block", runtimeID, "")
		markBlocked(t, agentID)

		w := httptest.NewRecorder()
		req := withURLParam(newRequest(http.MethodPut, "/api/agents/"+agentID, map[string]any{
			"model": "composer-2",
		}), "id", agentID)
		testHandler.UpdateAgent(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("update model: expected 200, got %d: %s", w.Code, w.Body.String())
		}

		detail, until := readBlock(t, agentID)
		if detail != "" || until.Valid {
			t.Fatalf("provider block after model change = detail %q until %+v, want cleared", detail, until)
		}
	})

	t.Run("same model preserves active block", func(t *testing.T) {
		agentID := createAgentOnRuntime(t, "same-model-keeps-block", runtimeID, "")
		markBlocked(t, agentID)

		w := httptest.NewRecorder()
		req := withURLParam(newRequest(http.MethodPut, "/api/agents/"+agentID, map[string]any{
			"model": "composer-1.5",
		}), "id", agentID)
		testHandler.UpdateAgent(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("no-op model update: expected 200, got %d: %s", w.Code, w.Body.String())
		}

		detail, _ := readBlock(t, agentID)
		if detail != "previous model failed" {
			t.Fatalf("provider block detail after no-op model update = %q, want preserved", detail)
		}
	})
}
