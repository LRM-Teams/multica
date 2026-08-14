package handler

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/researchrun"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestGraphCreationCommitsProductionContentHashes(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}

	ctx := context.Background()
	sessionID, _ := setupMergableResearchSession(t, "production graph passport")
	workspaceID := parseUUID(testWorkspaceID)
	first := createTestGraphNode(t, ctx, db.CreateResearchGraphNodeParams{
		WorkspaceID:  workspaceID,
		SessionID:    sessionID,
		NodeType:     "finding",
		Title:        "first",
		Summary:      "first persisted finding",
		Status:       "active",
		ActorAgentID: pgtype.UUID{},
		Payload:      json.RawMessage(`{"evidence":{"b":2,"a":1}}`),
	})
	second := createTestGraphNode(t, ctx, db.CreateResearchGraphNodeParams{
		WorkspaceID:  workspaceID,
		SessionID:    sessionID,
		NodeType:     "finding",
		Title:        "second",
		Summary:      "second persisted finding",
		Status:       "active",
		ActorAgentID: pgtype.UUID{},
		Payload:      json.RawMessage(`{}`),
	})
	edge := createTestGraphEdge(t, ctx, db.CreateResearchGraphEdgeParams{
		WorkspaceID: workspaceID,
		SessionID:   sessionID,
		FromNodeID:  first.ID,
		ToNodeID:    second.ID,
		EdgeType:    "supports",
	})

	assertGraphProductionPassport(t, ctx, sessionID, first.ID, researchrun.ArtifactKindGraphNode, "research_graph_node")
	assertGraphProductionPassport(t, ctx, sessionID, edge.ID, researchrun.ArtifactKindGraphEdge, "research_graph_edge")
}

func assertGraphProductionPassport(
	t *testing.T,
	ctx context.Context,
	sessionID, entityID pgtype.UUID,
	kind researchrun.ArtifactEntityKind,
	table string,
) {
	t.Helper()

	var persistedJSON string
	query := `SELECT (to_jsonb(row_value) - ARRAY['id', 'workspace_id', 'session_id'])::text
		FROM ` + table + ` row_value
		WHERE workspace_id = $1::uuid AND session_id = $2 AND id = $3`
	if err := testPool.QueryRow(ctx, query, testWorkspaceID, sessionID, entityID).Scan(&persistedJSON); err != nil {
		t.Fatalf("load persisted %s: %v", kind, err)
	}
	expectedHash, err := researchrun.ArtifactContentHash(kind, map[string]any{
		"persisted": json.RawMessage(persistedJSON),
	})
	if err != nil {
		t.Fatalf("hash persisted %s: %v", kind, err)
	}

	var provenance, hashOrigin, contentHash string
	if err := testPool.QueryRow(ctx, `
		SELECT p.provenance_completeness, v.hash_origin, v.content_hash
		FROM research_artifact_passport p
		JOIN research_artifact_version v
		  ON (v.workspace_id, v.session_id, v.artifact_id, v.version) =
		     (p.workspace_id, p.session_id, p.id, p.current_version)
		WHERE p.workspace_id = $1::uuid AND p.session_id = $2 AND p.id = $3
	`, testWorkspaceID, sessionID, entityID).Scan(&provenance, &hashOrigin, &contentHash); err != nil {
		t.Fatalf("load %s passport: %v", kind, err)
	}
	if provenance != string(researchrun.ArtifactProvenanceComplete) {
		t.Fatalf("provenance = %q, want complete", provenance)
	}
	if hashOrigin != string(researchrun.ArtifactHashOriginProduction) {
		t.Fatalf("hash origin = %q, want production", hashOrigin)
	}
	if contentHash != expectedHash {
		t.Fatalf("content hash = %q, want %q", contentHash, expectedHash)
	}
}
