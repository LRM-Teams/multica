package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/researchrun"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestCreateResearchMessageWithPassportCommitsMatchingArtifact(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}

	sessionID := seedInitializedResearchSessionForSnapshotTest(t)
	workspaceID := parseUUID(testWorkspaceID)
	msg, err := testHandler.createResearchMessageWithPassport(context.Background(), db.CreateResearchMessageParams{
		WorkspaceID:   workspaceID,
		SessionID:     sessionID,
		SenderType:    "user",
		SenderID:      parseUUID(testUserID),
		TargetAgentID: pgtype.UUID{},
		Body:          "continue",
		CardKind:      "chat",
		Meta:          []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("create research message: %v", err)
	}

	var kind, contentHash, hashOrigin, provenance string
	if err = testPool.QueryRow(context.Background(), `
		SELECT passport.entity_kind, version.content_hash, version.hash_origin,
		       passport.provenance_completeness
		FROM research_artifact_passport passport
		JOIN research_artifact_version version
		  ON (version.workspace_id,version.session_id,version.artifact_id,version.version)=
		     (passport.workspace_id,passport.session_id,passport.id,passport.current_version)
		WHERE passport.workspace_id = $1 AND passport.session_id = $2 AND passport.id = $3
	`, workspaceID, sessionID, msg.ID).Scan(&kind, &contentHash, &hashOrigin, &provenance); err != nil {
		t.Fatalf("load research message passport: %v", err)
	}
	if kind != "research_message" {
		t.Fatalf("passport kind=%q want research_message", kind)
	}
	wantHash, err := researchrun.ArtifactContentHash(researchrun.ArtifactKindResearchMessage, map[string]any{
		"sender_type": msg.SenderType, "sender_id": uuidToString(msg.SenderID),
		"target_agent_id": uuidToString(msg.TargetAgentID), "body": msg.Body,
		"card_kind": msg.CardKind, "meta": json.RawMessage(msg.Meta),
	})
	if err != nil {
		t.Fatal(err)
	}
	if contentHash != wantHash || hashOrigin != string(researchrun.ArtifactHashOriginProduction) ||
		provenance != string(researchrun.ArtifactProvenanceComplete) {
		t.Fatalf("hash=%q want=%q origin=%q provenance=%q", contentHash, wantHash, hashOrigin, provenance)
	}
}

func TestPostResearchMessageClientRequestIDValidation(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}

	sessionID := seedInitializedResearchSessionForSnapshotTest(t)

	// Case 1: Non-V6 session accepts message without client_request_id and auto-assigns one.
	req := withURLParam(newRequest("POST", "/research/sessions/"+uuidToString(sessionID)+"/messages", map[string]any{
		"body": "hello from user",
	}), "id", uuidToString(sessionID))

	rec := httptest.NewRecorder()
	testHandler.PostResearchMessage(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for non-v6 message without client_request_id, got %d: %s", rec.Code, rec.Body.String())
	}

	// Case 2: V6 session rejects message without client_request_id with 400 Bad Request.
	if _, err := testPool.Exec(context.Background(), `UPDATE research_session SET orchestrator_version = 'research-run-v6' WHERE id = $1`, sessionID); err != nil {
		t.Fatalf("failed to update orchestrator_version: %v", err)
	}

	reqV6 := withURLParam(newRequest("POST", "/research/sessions/"+uuidToString(sessionID)+"/messages", map[string]any{
		"body": "hello from user",
	}), "id", uuidToString(sessionID))

	recV6 := httptest.NewRecorder()
	testHandler.PostResearchMessage(recV6, reqV6)
	if recV6.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for v6 message without client_request_id, got %d: %s", recV6.Code, recV6.Body.String())
	}
}
