package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/workgraph"
)

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
