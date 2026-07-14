package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/workgraph"
)

// #3: ambient watcher resolution must prefer the bound workspace supervisor when
// she is a channel member, rather than guessing by name/insert order.
func TestResolveWendyAmbientAgentPrefersSupervisorMember(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	supervisor := createRadarSupervisorForExecutorTest(t)
	bindWendySupervisorForHandoffTest(t, supervisor.ID.String())
	clone := createHandlerTestAgent(t, "Wendy", nil) // a personal Wendy clone
	channelID := seedChannelForTest(t, "ambient-resolve-sup-"+uuid.NewString(), testUserID)
	addRadarAgentMembersForExecutorTest(t, channelID, supervisor.ID.String(), clone)

	got, ok := testHandler.resolveWendyAmbientAgentForChannel(ctx, parseUUID(testWorkspaceID), parseUUID(channelID))
	if !ok {
		t.Fatal("resolve returned not found, want supervisor")
	}
	if uuidToString(got) != supervisor.ID.String() {
		t.Fatalf("resolved %s, want supervisor %s", uuidToString(got), supervisor.ID.String())
	}
}

// #3: when the supervisor is not in the channel, resolution falls back to a
// named Wendy member deterministically (stable across calls).
func TestResolveWendyAmbientAgentFallsBackDeterministically(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	supervisor := createRadarSupervisorForExecutorTest(t)
	bindWendySupervisorForHandoffTest(t, supervisor.ID.String()) // bound but NOT a channel member
	clone := createHandlerTestAgent(t, "Wendy", nil)
	channelID := seedChannelForTest(t, "ambient-resolve-fallback-"+uuid.NewString(), testUserID)
	addRadarAgentMembersForExecutorTest(t, channelID, clone)

	first, ok := testHandler.resolveWendyAmbientAgentForChannel(ctx, parseUUID(testWorkspaceID), parseUUID(channelID))
	if !ok {
		t.Fatal("resolve returned not found, want the personal Wendy clone")
	}
	if uuidToString(first) != clone {
		t.Fatalf("resolved %s, want clone %s", uuidToString(first), clone)
	}
	second, _ := testHandler.resolveWendyAmbientAgentForChannel(ctx, parseUUID(testWorkspaceID), parseUUID(channelID))
	if uuidToString(second) != uuidToString(first) {
		t.Fatalf("resolution not deterministic: %s then %s", uuidToString(first), uuidToString(second))
	}
}

// #4: the ambient review markdown must surface the workspace's open issues, not
// only channel-linked work nodes (issue nodes carry no primary_channel_id).
func TestBuildWendyAmbientMarkdownIncludesOpenIssues(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	title := "Ambient Visible Issue " + uuid.NewString()
	createIssueForTimeline(t, title)
	channelID := seedChannelForTest(t, "ambient-md-"+uuid.NewString(), testUserID)

	watch := workgraph.ChannelAmbientWatch{
		WorkspaceID: parseUUID(testWorkspaceID),
		ChannelID:   parseUUID(channelID),
	}
	ch := ChannelResponse{ID: channelID, WorkspaceID: testWorkspaceID, Name: "ambient-md", Kind: "group"}

	md, err := testHandler.buildWendyAmbientChannelMarkdown(ctx, watch, ch)
	if err != nil {
		t.Fatalf("build ambient markdown: %v", err)
	}
	if !strings.Contains(md, "## Open Workspace Issues") {
		t.Fatalf("markdown missing Open Workspace Issues section:\n%s", md)
	}
	if !strings.Contains(md, title) {
		t.Fatalf("markdown missing open issue %q:\n%s", title, md)
	}
}
