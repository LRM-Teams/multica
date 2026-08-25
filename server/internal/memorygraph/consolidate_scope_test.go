package memorygraph

import (
	"strings"
	"testing"
)

func scopeTestStore(t *testing.T) (*Store, int) {
	t.Helper()
	store := NewStore(t.TempDir())
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	v, err := store.CurrentVersion()
	if err != nil {
		t.Fatal(err)
	}
	return store, v
}

// Spec §5: promotion never mutates the source node from channel to project
// visibility; any op attempting to flip an existing node's scope is
// rejected and the source node stays untouched.
func TestConsolidationRejectsVisibilityMutation(t *testing.T) {
	store, v := scopeTestStore(t)
	src := &Node{
		NodeID: "chan-node", ContentHash: ComputeContentHash("channel fact"),
		Visibility: "channel", ChannelID: "chan-a",
		SourceAgentIDs: []string{"agent-1"}, Body: "channel fact",
	}
	if err := store.SaveNode(v, src); err != nil {
		t.Fatal(err)
	}
	c := NewConsolidator(store, nil, DefaultConsolidateConfig(), "pi", NewOpLogger(store), NewTraceRecorder(store.Root))
	g, err := LoadGraph(store, v)
	if err != nil {
		t.Fatal(err)
	}
	_, rejected, err := c.applyOperations(g, v, "test", []ConsolidateOp{
		{Op: OpUpdateNode, NodeID: "chan-node", Node: &Node{Visibility: "project"}}, // promotion via in-place flip: forbidden
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected) != 1 || !strings.Contains(rejected[0].Reason, "visibility_mutation") {
		t.Fatalf("visibility mutation must be rejected as visibility_mutation, rejected=%v", rejected)
	}
	if got := g.Node("chan-node"); got.Visibility != "channel" || got.ChannelID != "chan-a" {
		t.Fatalf("source node mutated: %+v", got)
	}
}

// Spec §5: consolidation-created nodes carry the scope of their source
// segments (default "project" when no sidecar exists) and merge provenance
// monotonically across the op's segment refs.
func TestConsolidationStampsNewNodeScopeFromSegments(t *testing.T) {
	store, v := scopeTestStore(t)
	if err := store.WriteStagingSegmentMeta("seg-chan", &SegmentMeta{
		WorkspaceID: "ws", Visibility: "channel", ChannelID: "chan-a",
		AgentID: "agent-1", TaskID: "task-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteStagingSegmentMeta("seg-proj", &SegmentMeta{
		WorkspaceID: "ws", Visibility: "project", ProjectID: "proj-a",
		AgentID: "agent-2", TaskID: "task-2",
	}); err != nil {
		t.Fatal(err)
	}
	c := NewConsolidator(store, nil, DefaultConsolidateConfig(), "pi", NewOpLogger(store), NewTraceRecorder(store.Root))
	g, err := LoadGraph(store, v)
	if err != nil {
		t.Fatal(err)
	}
	applied, rejected, err := c.applyOperations(g, v, "test", []ConsolidateOp{
		{Op: OpAddNode, Node: &Node{NodeID: "n-chan", Body: "channel fact", SegmentRefs: []string{"seg-chan"}}},
		{Op: OpAddNode, Node: &Node{NodeID: "n-mixed", Body: "mixed fact", SegmentRefs: []string{"seg-chan", "seg-proj"}}},
		{Op: OpAddNode, Node: &Node{NodeID: "n-legacy", Body: "legacy fact", SegmentRefs: []string{"seg-missing"}}},
		// Explicit promotion: a new project-visible node derived from
		// channel sources; allowed because no existing node is mutated.
		{Op: OpAddNode, Node: &Node{NodeID: "n-promoted", Body: "promoted fact", Visibility: "project", SegmentRefs: []string{"seg-chan"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied != 4 || len(rejected) != 0 {
		t.Fatalf("applied=%d rejected=%v", applied, rejected)
	}
	if got := g.Node("n-chan"); got.Visibility != "channel" || got.ChannelID != "chan-a" {
		t.Fatalf("channel-source node scope = %+v", got)
	}
	if got := g.Node("n-mixed"); got.Visibility == "channel" {
		t.Fatalf("mixed-source node must fail safe to project visibility: %+v", got)
	} else {
		for _, want := range []string{"agent-1", "agent-2"} {
			if !containsString(got.SourceAgentIDs, want) {
				t.Fatalf("mixed node provenance missing %s: %+v", want, got.SourceAgentIDs)
			}
		}
		if !containsString(got.SourceChannelIDs, "chan-a") || !containsString(got.SourceTaskIDs, "task-1") || !containsString(got.SourceTaskIDs, "task-2") {
			t.Fatalf("mixed node provenance = %+v", got)
		}
	}
	if got := g.Node("n-legacy"); got.Visibility != "project" || got.ChannelID != "" {
		t.Fatalf("sidecar-less node must default to project visibility: %+v", got)
	}
	if got := g.Node("n-promoted"); got.Visibility != "project" || !containsString(got.SourceChannelIDs, "chan-a") {
		t.Fatalf("promoted node = %+v", got)
	}
}

// Spec §5: updating an existing node with unchanged (or empty) scope is
// allowed, preserves the stored scope, and union-merges provenance.
func TestConsolidationUpdatePreservesScopeAndMergesProvenance(t *testing.T) {
	store, v := scopeTestStore(t)
	src := &Node{
		NodeID: "chan-node", Visibility: "channel", ChannelID: "chan-a",
		SourceAgentIDs: []string{"agent-1"}, SourceTaskIDs: []string{"task-1"},
		Body: "channel fact",
	}
	if err := store.SaveNode(v, src); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteStagingSegmentMeta("seg-2", &SegmentMeta{
		WorkspaceID: "ws", Visibility: "channel", ChannelID: "chan-a",
		AgentID: "agent-2", TaskID: "task-2",
	}); err != nil {
		t.Fatal(err)
	}
	c := NewConsolidator(store, nil, DefaultConsolidateConfig(), "pi", NewOpLogger(store), NewTraceRecorder(store.Root))
	g, err := LoadGraph(store, v)
	if err != nil {
		t.Fatal(err)
	}
	applied, rejected, err := c.applyOperations(g, v, "test", []ConsolidateOp{
		{Op: OpUpdateNode, NodeID: "chan-node", Node: &Node{
			Body: "channel fact, refined", SegmentRefs: []string{"seg-2"},
			SourceAgentIDs: []string{"agent-1"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied != 1 || len(rejected) != 0 {
		t.Fatalf("applied=%d rejected=%v", applied, rejected)
	}
	got := g.Node("chan-node")
	if got.Visibility != "channel" || got.ChannelID != "chan-a" {
		t.Fatalf("scope not preserved: %+v", got)
	}
	if got.Body != "channel fact, refined" {
		t.Fatalf("body not updated: %+v", got)
	}
	if !containsString(got.SourceAgentIDs, "agent-1") || !containsString(got.SourceAgentIDs, "agent-2") ||
		!containsString(got.SourceTaskIDs, "task-1") || !containsString(got.SourceTaskIDs, "task-2") {
		t.Fatalf("provenance not merged monotonically: %+v", got)
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
