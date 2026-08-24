package service

import (
	"testing"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/memorygraph"
)

// Spec §5/§8: channel-origin segments default to channel visibility and
// carry exact lineage provenance; project-only tasks default to project
// visibility; unscoped tasks never touch the graph.
func TestIngestScopeForTask(t *testing.T) {
	ws, proj, ch := uuid.NewString(), uuid.NewString(), uuid.NewString()

	meta, kind, owner, ok := ingestScopeForTask(ws, proj, "", GraphRouteResolution{}, "agent-1", "task-1")
	if !ok || kind != memorygraph.GraphDirKindProject || owner != proj {
		t.Fatalf("project-only scope = (%v, %v, %v)", kind, owner, ok)
	}
	if meta.Visibility != "project" || meta.ChannelID != "" {
		t.Fatalf("project-only meta = %+v", meta)
	}

	route := GraphRouteResolution{RoutingMode: "project_lineage", GraphKind: "project", GraphOwnerID: proj, Generation: 4}
	meta, kind, owner, ok = ingestScopeForTask(ws, proj, ch, route, "agent-1", "task-1")
	if !ok || kind != memorygraph.GraphDirKindProject || owner != proj {
		t.Fatalf("project-lineage scope = (%v, %v, %v)", kind, owner, ok)
	}
	if meta.Visibility != "channel" || meta.ChannelID != ch || meta.LineageGeneration != 4 {
		t.Fatalf("channel-origin meta = %+v", meta)
	}

	route = GraphRouteResolution{RoutingMode: "standalone", GraphKind: "channel", GraphOwnerID: ch, Generation: 1}
	_, kind, owner, ok = ingestScopeForTask(ws, "", ch, route, "agent-1", "task-1")
	if !ok || kind != memorygraph.GraphDirKindChannel || owner != ch {
		t.Fatalf("standalone scope = (%v, %v, %v)", kind, owner, ok)
	}

	if _, _, _, ok = ingestScopeForTask(ws, "", "", GraphRouteResolution{}, "agent-1", "task-1"); ok {
		t.Fatal("unscoped task must not resolve any graph (spec §14 test 11)")
	}
}
