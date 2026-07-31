package handler

import (
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// researchFleetMaxActiveMembers caps non-archived roster size (lead + seeds +
// specialty hires). Aligns with depth budget (LRM-676 / LRM-888): seed five
// (lead+4) plus up to seven specialty hires — enough for gap coverage without
// unbounded expansion.
const researchFleetMaxActiveMembers = 12

func countNonArchivedFleetMembers(members []db.ResearchFleetMember) int {
	n := 0
	for _, m := range members {
		if m.Status != "archived" {
			n++
		}
	}
	return n
}

func researchRosterAtCap(activeCount int) bool {
	return activeCount >= researchFleetMaxActiveMembers
}

func resolveResearchHireModel(requested string, runtimeProvider string) pgtype.Text {
	model := strings.TrimSpace(requested)
	if model == "" {
		return pgTextModelForRuntime(runtimeProvider)
	}
	return pgtype.Text{String: model, Valid: true}
}

// researchAgentMayMutateSessionGoal is always false for fleet agents.
// Mid-flight goal edits are user-only (LRM-898); agents must not rewrite
// research_session.goal during the investigation.
func researchAgentMayMutateSessionGoal() bool {
	return false
}
