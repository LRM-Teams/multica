package handler

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/promptcontext"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestReferencedEntityMentionsUsesStructuredThenLegacyCanonicalRefs(t *testing.T) {
	const (
		issueID = "11111111-1111-1111-1111-111111111111"
		agentID = "22222222-2222-2222-2222-222222222222"
	)
	agentStart := 2
	issueStart := 20
	source := referencedEntitySource{
		Content: fmt.Sprintf(
			"duplicate [MUL-1](mention://issue/%s) and [@Agent](mention://agent/%s)",
			issueID,
			agentID,
		),
		Parts: []protocol.MessagePart{
			{Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "member", RefID: uuid.NewString()},
			{Type: protocol.MessagePartTypeReference, RefType: "issue-ref", RefSubType: "issue", RefID: issueID, ContentStartUTF16: &issueStart},
			{Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "agent", RefID: agentID, ContentStartUTF16: &agentStart},
		},
	}
	originalParts := append([]protocol.MessagePart(nil), source.Parts...)
	got := referencedEntityMentions(source)
	want := []struct {
		kind string
		id   string
	}{
		{kind: "agent", id: agentID},
		{kind: "issue", id: issueID},
	}
	if !reflect.DeepEqual(source.Parts, originalParts) {
		t.Fatalf("source parts mutated:\n got %#v\nwant %#v", source.Parts, originalParts)
	}
	if len(got) != len(want) {
		t.Fatalf("mentions = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i].Type != want[i].kind || got[i].ID != want[i].id {
			t.Fatalf("mention[%d] = %#v, want %s/%s", i, got[i], want[i].kind, want[i].id)
		}
	}
}

func TestHydrateReferencedEntitiesBoundsAndFailsClosed(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	privateAgentID, _, plainMemberID := privateAgentTestFixture(t)
	if _, err := testPool.Exec(ctx, `
		UPDATE agent
		SET description = 'private role', instructions = 'PRIVATE INSTRUCTIONS'
		WHERE id = $1
	`, privateAgentID); err != nil {
		t.Fatalf("update private agent: %v", err)
	}

	var publicAgentID string
	publicAgentName := "reference-public-" + uuid.NewString()[:8]
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, display_name, description, runtime_mode, runtime_config, runtime_id, max_concurrent_tasks, owner_id, instructions, custom_env, custom_args
		, model) VALUES ($1, $2, 'Reference Public', 'triage role' || E'\n## untrusted', 'cloud', '{}'::jsonb, $3, 1, $4, 'NEVER EXPOSE THIS', '{}'::jsonb, '[]'::jsonb, 'composer-1.5')
		RETURNING id
	`, testWorkspaceID, publicAgentName, handlerTestRuntimeID(t), testUserID).Scan(&publicAgentID); err != nil {
		t.Fatalf("create public agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, publicAgentID)
	})

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, status, priority, creator_type, creator_id,
			assignee_type, assignee_id, number
		)
		VALUES ($1, 'Hydrate' || E'\n## title [unsafe]', 'in_progress', 'high',
		        'member', $2, 'agent', $3,
		        COALESCE((SELECT MAX(number) FROM issue WHERE workspace_id = $1), 0) + 1)
		RETURNING id
	`, testWorkspaceID, testUserID, publicAgentID).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)
	})

	var pullRequestID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO github_pull_request (
			workspace_id, installation_id, repo_owner, repo_name, pr_number,
			title, state, html_url, pr_created_at, pr_updated_at
		)
		VALUES ($1, 1, 'multica-ai', 'multica', 708,
		        'reference hydration', 'open', 'https://example.test/pr/708', now(), now())
		RETURNING id
	`, testWorkspaceID).Scan(&pullRequestID); err != nil {
		t.Fatalf("create linked pull request: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM github_pull_request WHERE id = $1`, pullRequestID)
	})
	if _, err := testPool.Exec(ctx, `
		INSERT INTO issue_pull_request (issue_id, pull_request_id)
		VALUES ($1, $2)
	`, issueID, pullRequestID); err != nil {
		t.Fatalf("link pull request: %v", err)
	}

	foreignID := uuid.NewString()
	source := referencedEntitySource{
		Content: fmt.Sprintf(
			"duplicate [issue](mention://issue/%s), private [@agent](mention://agent/%s), foreign [issue](mention://issue/%s)",
			issueID,
			privateAgentID,
			foreignID,
		),
		Parts: []protocol.MessagePart{
			{Type: protocol.MessagePartTypeReference, RefType: "issue-ref", RefSubType: "issue", RefID: issueID},
			{Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "agent", RefID: publicAgentID},
			{Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "agent", RefID: privateAgentID},
		},
	}
	original := referencedEntitySource{Content: source.Content, Parts: append([]protocol.MessagePart(nil), source.Parts...)}

	got := testHandler.hydrateReferencedEntities(ctx, testWorkspaceID, "member", plainMemberID, source)
	if !reflect.DeepEqual(source, original) {
		t.Fatalf("source mutated:\n got %#v\nwant %#v", source, original)
	}
	// Task #908: referenced-entity hydration is a "usage" surface (like
	// @mention), unconditional now — the private agent is hydrated too, not
	// omitted. What must still hold is the field-level boundary: only
	// Description ever reaches the snapshot content, never Instructions,
	// regardless of which agent it is.
	if len(got.Snapshots) != 3 {
		t.Fatalf("snapshots = %#v, want issue + public agent + private agent (existence unconditional post-#908)", got.Snapshots)
	}
	if got.OmittedCount != 0 {
		t.Fatalf("omitted count = %d, want 0 (failed permission/lookups are silent)", got.OmittedCount)
	}

	issueSnapshot := got.Snapshots[0]
	for _, want := range []string{"issue ", "Hydrate ## title", "status: in_progress", "assignee: Reference Public", "priority: high", "PRs: #708 open"} {
		if !strings.Contains(issueSnapshot.Content, want) {
			t.Errorf("issue snapshot missing %q: %q", want, issueSnapshot.Content)
		}
	}
	if strings.Contains(issueSnapshot.Content, "\n") {
		t.Fatalf("issue snapshot is not single-line: %q", issueSnapshot.Content)
	}

	publicAgentSnapshot := got.Snapshots[1]
	for _, want := range []string{"agent Reference Public", "role: triage role ## untrusted"} {
		if !strings.Contains(publicAgentSnapshot.Content, want) {
			t.Errorf("public agent snapshot missing %q: %q", want, publicAgentSnapshot.Content)
		}
	}
	for _, forbidden := range []string{"NEVER EXPOSE THIS", "PRIVATE INSTRUCTIONS", "private role"} {
		if strings.Contains(publicAgentSnapshot.Content, forbidden) {
			t.Errorf("public agent snapshot leaked %q: %q", forbidden, publicAgentSnapshot.Content)
		}
	}

	privateAgentSnapshot := got.Snapshots[2]
	if !strings.Contains(privateAgentSnapshot.Content, "role: private role") {
		t.Errorf("private agent snapshot missing its own role: %q", privateAgentSnapshot.Content)
	}
	for _, forbidden := range []string{"NEVER EXPOSE THIS", "PRIVATE INSTRUCTIONS"} {
		if strings.Contains(privateAgentSnapshot.Content, forbidden) {
			t.Errorf("private agent snapshot leaked instructions (only Description should ever reach a snapshot) %q: %q", forbidden, privateAgentSnapshot.Content)
		}
	}

	channelID := seedChannelForTest(t, "reference-hydration-"+uuid.NewString()[:8], testUserID)
	trigger := ChannelMessageResponse{
		ID:         uuid.NewString(),
		AuthorID:   &plainMemberID,
		AuthorName: "Plain Member",
		Type:       "user",
		Content:    "please inspect the referenced issue",
		Parts: []protocol.MessagePart{
			{Type: protocol.MessagePartTypeReference, RefType: "issue-ref", RefSubType: "issue", RefID: issueID},
		},
	}
	originalTrigger := trigger
	originalTrigger.Parts = append([]protocol.MessagePart(nil), trigger.Parts...)
	prompt := testHandler.buildChannelMentionPromptForActor(
		ctx,
		ChannelResponse{ID: channelID, WorkspaceID: testWorkspaceID, Name: "reference hydration"},
		trigger,
		channelFacilitatorState{},
		"member",
		plainMemberID,
	)
	if !reflect.DeepEqual(trigger, originalTrigger) {
		t.Fatalf("channel trigger mutated:\n got %#v\nwant %#v", trigger, originalTrigger)
	}
	if got := strings.Count(prompt, trigger.Content); got != 1 {
		t.Fatalf("current message occurrence count = %d, want 1:\n%s", got, prompt)
	}
	for _, want := range []string{"Current message to respond to:", "Referenced entity snapshots", "PRs: #708 open"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("channel prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestHydrateReferencedEntitiesCapsSuccessfulLookups(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	parts := make([]protocol.MessagePart, 0, promptcontext.MaxReferencedEntities+1)
	issueIDs := make([]string, 0, promptcontext.MaxReferencedEntities+1)
	for i := 0; i < promptcontext.MaxReferencedEntities+1; i++ {
		var issueID string
		title := fmt.Sprintf("reference cap issue %02d", i)
		if err := testPool.QueryRow(ctx, `
			INSERT INTO issue (
				workspace_id, title, status, priority, creator_type, creator_id, number
			)
			VALUES ($1, $2, 'todo', 'medium', 'member', $3,
			        COALESCE((SELECT MAX(number) FROM issue WHERE workspace_id = $1), 0) + 1)
			RETURNING id
		`, testWorkspaceID, title, testUserID).Scan(&issueID); err != nil {
			t.Fatalf("create issue %d: %v", i, err)
		}
		issueIDs = append(issueIDs, issueID)
		parts = append(parts, protocol.MessagePart{
			Type:       protocol.MessagePartTypeReference,
			RefType:    "issue-ref",
			RefSubType: "issue",
			RefID:      issueID,
		})
	}
	t.Cleanup(func() {
		for _, issueID := range issueIDs {
			testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)
		}
	})

	got := testHandler.hydrateReferencedEntities(
		ctx,
		testWorkspaceID,
		"member",
		testUserID,
		referencedEntitySource{Parts: parts},
	)
	if len(got.Snapshots) != promptcontext.MaxReferencedEntities {
		t.Fatalf("snapshots = %d, want %d", len(got.Snapshots), promptcontext.MaxReferencedEntities)
	}
	if got.OmittedCount != 1 {
		t.Fatalf("omitted count = %d, want 1", got.OmittedCount)
	}
	for _, snapshot := range got.Snapshots {
		if strings.Contains(snapshot.Content, "reference cap issue 08") {
			t.Fatalf("ninth issue was expanded despite cap: %#v", got.Snapshots)
		}
	}
}
