package handler

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestBuildParentAssigneeDisplayMentionOmitsURL(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	// Without Queries the label lookup fails closed to empty — that is the
	// safe display-only default when assignee cannot be resolved.
	parent := db.Issue{}
	parent.AssigneeType = pgtype.Text{String: "agent", Valid: true}
	parent.AssigneeID = pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	if got := h.buildParentAssigneeDisplayMention(t.Context(), parent); got != "" {
		t.Fatalf("expected empty without agent row, got %q", got)
	}
}

func TestBuildParentAssigneeDisplayMentionIsDisplayOnly(t *testing.T) {
	t.Parallel()
	// Contract: stage-barrier system comments must never embed mention://,
	// even when a label is available. Format the display prefix directly.
	label := sanitizeMentionLabel("Tess]")
	got := formatParentAssigneeDisplayMention(label)
	if got != "@Tess " {
		t.Fatalf("unexpected display mention %q", got)
	}
	if strings.Contains(got, "mention://") {
		t.Fatalf("display mention must not contain mention://: %q", got)
	}
}

func TestSanitizeChildTitleStillStripsMentionMarkdown(t *testing.T) {
	t.Parallel()
	in := "Please see [@All](mention://member/11111111-1111-1111-1111-111111111111)"
	out := sanitizeChildTitleForSystemComment(in)
	if out == in {
		t.Fatal("expected mention markdown to be broken")
	}
}

func TestIssueStageBarrierDecisionDefaultsToNoNotify(t *testing.T) {
	t.Parallel()
	var d issueStageBarrierDecision
	if d.ShouldNotify {
		t.Fatal("zero decision must not notify")
	}
}
