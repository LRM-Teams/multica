package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
)

// Recall federation (unification spec §4.4): a scoped recall federates the
// workspace research graph as an additional target, a workspace without a
// research graph recalls exactly as before, and research is never a fallback
// — a scope-less task resolves no graph at all even when the research graph
// exists and is populated.
func TestGraphMemoryRecallFederatesResearchGraph(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	fx := mustGraphMemoryRecallFixture(t)
	root := t.TempDir()
	mustGraphMemoryGraphProfile(t, fx.workspaceID, false, 1)

	// Channel task routed into the project lineage: the primary target.
	if _, err := testPool.Exec(ctx, `UPDATE channel SET project_id = $2 WHERE id = $1`, fx.channelID, fx.projectID); err != nil {
		t.Fatal(err)
	}
	mustGraphMemoryTaskScope(t, fx.taskID, "channel_id", fx.channelID)
	mustGraphMemoryGraphDir(t, root, util.UUIDToString(fx.workspaceID), memorygraph.GraphDirKindProject, fx.projectID)

	svc := newGraphMemoryRecallServiceForTest(root)

	// Before the research graph exists: no research target, recall unchanged.
	req := graphMemoryRecallRequestForFixture(fx, "trace-fed-"+uuid.NewString()[:8])
	plan, err := svc.Begin(ctx, req)
	if err != nil {
		t.Fatalf("Begin without research graph: %v", err)
	}
	if plan.Research != nil {
		t.Fatalf("research target = %+v, want nil before the research graph exists", plan.Research)
	}

	// With the research graph present it joins the plan with its own pinned
	// version and research-only view.
	researchDir := mustGraphMemoryGraphDir(t, root, util.UUIDToString(fx.workspaceID), memorygraph.GraphDirKindResearch, fx.workspaceID)
	req2 := graphMemoryRecallRequestForFixture(fx, "trace-fed2-"+uuid.NewString()[:8])
	plan2, err := svc.Begin(ctx, req2)
	if err != nil {
		t.Fatalf("Begin with research graph: %v", err)
	}
	if plan2.Research == nil {
		t.Fatalf("research target missing from the federated plan")
	}
	if plan2.Research.Dir != researchDir || plan2.Research.Version != 1 {
		t.Fatalf("research target = %+v, want %s v1", plan2.Research, researchDir)
	}
	if !plan2.Research.View.AllowResearch || plan2.Research.View.AllowProject || plan2.Research.View.ChannelID != "" {
		t.Fatalf("research view = %+v, want research-only visibility", plan2.Research.View)
	}

	// Scope-less task: no channel, no issue — the research graph must not
	// become a fallback target.
	scopeless := mustGraphMemoryRecallFixture(t)
	scopelessRoot := t.TempDir()
	mustGraphMemoryGraphProfile(t, scopeless.workspaceID, false, 1)
	mustGraphMemoryGraphDir(t, scopelessRoot, util.UUIDToString(scopeless.workspaceID), memorygraph.GraphDirKindResearch, scopeless.workspaceID)
	scopelessSvc := newGraphMemoryRecallServiceForTest(scopelessRoot)
	_, err = scopelessSvc.Begin(ctx, graphMemoryRecallRequestForFixture(scopeless, "trace-fed3-"+uuid.NewString()[:8]))
	if !errors.Is(err, service.ErrGraphMemoryRecallNoScope) {
		t.Fatalf("scope-less Begin error = %v, want ErrGraphMemoryRecallNoScope (research is not a fallback)", err)
	}
}
