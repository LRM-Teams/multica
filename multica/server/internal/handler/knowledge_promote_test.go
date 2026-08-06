package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/service"
)

func TestPromoteKnowledgePageCreatesDerivedFromAndAbout(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	channel := createGoalTestChannel(t)
	issueID := createHandlerTestIssue(t, "knowledge promote "+uuid.NewString()[:8])

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/knowledge/promote", map[string]any{
		"source_type": "issue",
		"source_id":   issueID,
		"target_kind": "context",
		"title":       "Channel routing lock",
		"content":     "Wiki pages inject task-related + ≤2 hops only.",
		"subject_id":  channel.ID,
	})
	req = withURLParam(req, "id", testWorkspaceID)
	testHandler.PromoteKnowledgePage(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("PromoteKnowledgePage = %d: %s", w.Code, w.Body.String())
	}
	var created promoteKnowledgeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM team_knowledge_edge WHERE workspace_id=$1 AND (from_id=$2 OR to_id=$2)`, testWorkspaceID, created.ID)
		_, _ = testPool.Exec(ctx, `DELETE FROM team_knowledge_item WHERE id=$1`, created.ID)
	})
	if created.Kind != "context" || created.Title == "" {
		t.Fatalf("page = %#v", created)
	}
	var derivedFrom, aboutIssue, aboutChannel int
	for _, edge := range created.Edges {
		if edge.EdgeType == service.KnowledgeEdgeDerivedFrom && edge.ToKind == "issue" && edge.ToID == issueID {
			derivedFrom++
		}
		if edge.EdgeType == service.KnowledgeEdgeAbout && edge.ToKind == "issue" && edge.ToID == issueID {
			aboutIssue++
		}
		if edge.EdgeType == service.KnowledgeEdgeAbout && edge.ToKind == "channel" && edge.ToID == channel.ID {
			aboutChannel++
		}
	}
	if derivedFrom != 1 || aboutIssue != 1 || aboutChannel != 1 {
		t.Fatalf("edges derived_from=%d about_issue=%d about_channel=%d edges=%#v", derivedFrom, aboutIssue, aboutChannel, created.Edges)
	}

	neighbors := httptest.NewRecorder()
	nreq := newRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/memory-curation/team-knowledge/"+created.ID+"/neighbors?hops=1", nil)
	nreq = withRouteParams(nreq, "id", testWorkspaceID, "itemId", created.ID)
	testHandler.ListKnowledgeNeighbors(neighbors, nreq)
	if neighbors.Code != http.StatusOK {
		t.Fatalf("neighbors = %d: %s", neighbors.Code, neighbors.Body.String())
	}
	var nb knowledgeNeighborsResponse
	if err := json.Unmarshal(neighbors.Body.Bytes(), &nb); err != nil {
		t.Fatalf("decode neighbors: %v", err)
	}
	if len(nb.Edges) < 2 {
		t.Fatalf("expected queryable edges, got %#v", nb)
	}
}

func TestKnowledgeNeighborhoodInjectsSeedNotFullDump(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	channel := createGoalTestChannel(t)
	issueID := createHandlerTestIssue(t, "wiki inject "+uuid.NewString()[:8])

	// Unrelated workspace dump page (no applies / no about) must NOT inject.
	var dumpID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO team_knowledge_item (workspace_id, kind, title, content, status, metadata)
		VALUES ($1, 'memory', 'Unrelated dump', 'should not inject', 'active', '{}'::jsonb)
		RETURNING id::text
	`, testWorkspaceID).Scan(&dumpID); err != nil {
		t.Fatalf("seed dump page: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM team_knowledge_item WHERE id=$1`, dumpID)
	})

	promoted, err := testHandler.TaskService.PromoteKnowledgePage(ctx, service.KnowledgePromoteInput{
		WorkspaceID: parseUUID(testWorkspaceID),
		SourceType:  "issue",
		SourceID:    parseUUID(issueID),
		TargetKind:  "context",
		Title:       "Inject seed",
		Content:     "task related context page",
		SubjectID:   parseUUID(channel.ID),
		ActorType:   "member",
		ActorID:     parseUUID(testUserID),
	})
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	pageID := uuidToString(promoted.Page.ID)
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM team_knowledge_edge WHERE workspace_id=$1 AND (from_id=$2 OR to_id=$2)`, testWorkspaceID, pageID)
		_, _ = testPool.Exec(ctx, `DELETE FROM team_knowledge_item WHERE id=$1`, pageID)
	})

	// Linked neighbor via about/shared style edge between wiki pages.
	var neighborID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO team_knowledge_item (workspace_id, kind, title, content, status, metadata)
		VALUES ($1, 'decision', 'Neighbor decision', 'one hop away', 'active', jsonb_build_object('subject_id', $2::text))
		RETURNING id::text
	`, testWorkspaceID, "00000000-0000-0000-0000-000000000001").Scan(&neighborID); err != nil {
		t.Fatalf("seed neighbor: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM team_knowledge_edge WHERE from_id=$1 OR to_id=$1`, neighborID)
		_, _ = testPool.Exec(ctx, `DELETE FROM team_knowledge_item WHERE id=$1`, neighborID)
	})
	if _, err := testPool.Exec(ctx, `
		INSERT INTO team_knowledge_edge (
			workspace_id, edge_type, from_kind, from_id, to_kind, to_id, created_by_type
		) VALUES ($1, 'about', 'team_knowledge', $2::uuid, 'team_knowledge', $3::uuid, 'system')
	`, testWorkspaceID, pageID, neighborID); err != nil {
		t.Fatalf("link neighbor: %v", err)
	}

	got := testHandler.TaskService.LoadTaskRelatedKnowledgeNeighborhood(ctx, parseUUID(testWorkspaceID), service.MemoryExecutionScope{
		ChannelID: channel.ID,
		IssueID:   issueID,
	}, 2)
	ids := map[string]bool{}
	for _, item := range got {
		ids[item.ID] = true
	}
	if !ids[pageID] {
		t.Fatalf("seed page missing from neighborhood: %#v", got)
	}
	if !ids[neighborID] {
		t.Fatalf("1-hop neighbor missing: %#v", got)
	}
	if ids[dumpID] {
		t.Fatalf("unrelated dump page must not inject")
	}
}

func createHandlerTestIssue(t *testing.T, title string) string {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title": title,
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("CreateIssue = %d: %s", w.Code, w.Body.String())
	}
	var created IssueResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("decode issue: %v body=%s", err, w.Body.String())
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id=$1`, created.ID)
	})
	return created.ID
}
