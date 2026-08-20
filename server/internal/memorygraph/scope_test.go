package memorygraph

import (
	"context"
	"testing"
)

func TestGraphViewAllows(t *testing.T) {
	projectNode := &Node{NodeID: "n1", Visibility: "project"}
	channelNode := &Node{NodeID: "n2", Visibility: "channel", ChannelID: "chan-a"}
	legacyNode := &Node{NodeID: "n3"} // "" reads as project

	projectView := GraphView{AllowProject: true}
	if !projectView.Allows(projectNode) || !projectView.Allows(legacyNode) {
		t.Fatal("project view must allow project-visible and legacy nodes")
	}
	if projectView.Allows(channelNode) {
		t.Fatal("project view must not allow channel-visible nodes")
	}
	exactView := GraphView{AllowProject: true, ChannelID: "chan-a"}
	if !exactView.Allows(channelNode) {
		t.Fatal("exact-channel view must allow its own channel nodes")
	}
	otherChannel := GraphView{AllowProject: true, ChannelID: "chan-b"}
	if otherChannel.Allows(channelNode) {
		t.Fatal("channel A material must never be visible to channel B (spec §14 test 3)")
	}
	channelOnly := GraphView{ChannelID: "chan-a"}
	if channelOnly.Allows(projectNode) {
		t.Fatal("channel-only view must not allow project-visible nodes")
	}
	if (&Node{NodeID: "n4", Visibility: "bogus"}).Visibility != "bogus" || (GraphView{AllowProject: true}).Allows(&Node{Visibility: "bogus"}) {
		t.Fatal("unknown visibility must fail closed")
	}
}

func TestStagingSegmentMetaRoundTrip(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	meta := &SegmentMeta{
		WorkspaceID: "ws", Visibility: "channel", ChannelID: "chan-a",
		ProjectID: "p", AgentID: "a", TaskID: "t", LineageGeneration: 3,
	}
	if err := store.WriteStagingSegmentMeta("seg-1", meta); err != nil {
		t.Fatal(err)
	}
	got, err := store.ReadStagingSegmentMeta("seg-1")
	if err != nil {
		t.Fatal(err)
	}
	if *got != *meta {
		t.Fatalf("meta = %+v, want %+v", got, meta)
	}
	if _, err := store.ReadStagingSegmentMeta("missing"); err == nil {
		t.Fatal("missing meta must error")
	}
}

func TestHybridRetrieverAppliesGraphView(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	v, err := store.CreateVersionFrom(1, "test")
	if err != nil {
		t.Fatal(err)
	}
	nodes := []*Node{
		{NodeID: "proj-node", ContentHash: ComputeContentHash("alpha beta"), Visibility: "project", Body: "alpha beta"},
		{NodeID: "chan-node", ContentHash: ComputeContentHash("alpha beta"), Visibility: "channel", ChannelID: "chan-a", Body: "alpha beta"},
	}
	for _, n := range nodes {
		if err := store.SaveNode(v, n); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SwitchCurrent(v); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultRetrievalConfig()
	cfg.View = GraphView{AllowProject: true} // no channel access
	retr := NewHybridRetriever(store, nil, cfg)
	if err := retr.Rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	docs, err := retr.Search(context.Background(), "alpha beta")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		if d.ID == "chan-node" {
			t.Fatal("retriever must reapply the graph view: channel node leaked into project-only view")
		}
	}
	if len(docs) == 0 {
		t.Fatal("project node must remain retrievable")
	}
}
