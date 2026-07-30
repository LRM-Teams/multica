package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestDiagnoseEnvDispatchProject_WithoutEnablementFlags(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Setenv("DIAGNOSIS_AGENT_PATH", "/nonexistent/multica-pi")

	ctx := context.Background()
	projectID, rootTaskID := seedHandlerDagNonTrainingCompletedRoot(t, ctx, testWorkspaceID)
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM env_dispatch_run WHERE project_id = $1`, projectID)
		testPool.Exec(ctx, `DELETE FROM project WHERE id = $1`, projectID)
	})
	if _, err := testPool.Exec(ctx, `UPDATE agent_inbox_event SET status = 'acked' WHERE id = $1`, rootTaskID); err != nil {
		t.Fatalf("set root task terminal: %v", err)
	}
	seedDAGSegment(t, projectID, projectID+"-diagnosis", rootTaskID, 1)

	w := httptest.NewRecorder()
	r := withURLParam(newRequest(http.MethodPost, "/api/v1/env-dispatch/"+projectID+"/diagnosis", nil), "projectID", projectID)
	// Direct handler call bypasses workspace middleware; mirror other env_dispatch tests.
	r = r.WithContext(middleware.SetMemberContext(r.Context(), testWorkspaceID, db.Member{}))
	testHandler.DiagnoseEnvDispatchProject(w, r)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code, "valid terminal DAG must reach the runner seam: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), `"error":"diagnosis_failed"`)
}

func TestDiagnosisTopologicalSegmentIDs_RespectsEdges(t *testing.T) {
	ordered, err := diagnosisTopologicalSegmentIDs(service.AssembledDag{
		Segments: []service.AssembledSegment{
			{SegmentID: "root"},
			{SegmentID: "child"},
			{SegmentID: "leaf"},
		},
		Edges: []service.AssembledEdge{
			{SrcSegmentID: "root", DstSegmentID: "child"},
			{SrcSegmentID: "child", DstSegmentID: "leaf"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"root", "child", "leaf"}, ordered)
}
