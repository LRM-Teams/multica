package service

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestDefaultCanonicalBranch(t *testing.T) {
	t.Parallel()
	issue := db.Issue{Number: 1523}
	issue.ID = pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	agentID := pgtype.UUID{Bytes: [16]byte{2, 3, 4, 5, 6, 7, 8, 9}, Valid: true}
	got := defaultCanonicalBranch(issue, agentID)
	if !strings.HasPrefix(got, "agent/") || !strings.Contains(got, "issue-1523") {
		t.Fatalf("branch=%q", got)
	}
}
