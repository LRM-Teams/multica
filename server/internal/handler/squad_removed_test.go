package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestSquadFeatureRemovedReturnsGone(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	w := httptest.NewRecorder()
	req := newRequest(http.MethodGet, "/api/squads", nil)
	testHandler.SquadFeatureRemoved(w, req)

	if w.Code != http.StatusGone {
		t.Fatalf("expected 410, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "squad feature has been removed") {
		t.Fatalf("expected removed message, got %s", w.Body.String())
	}
}

func TestValidateAssigneePairRejectsSquad(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	status, msg := testHandler.validateAssigneePair(
		contextlessRequest().Context(),
		contextlessRequest(),
		testWorkspaceID,
		pgtype.Text{String: "squad", Valid: true},
		pgtype.UUID{Valid: true},
	)
	if status != http.StatusGone {
		t.Fatalf("expected 410, got %d (%s)", status, msg)
	}
}

func contextlessRequest() *http.Request {
	return newRequest(http.MethodPost, "/api/issues", nil)
}
