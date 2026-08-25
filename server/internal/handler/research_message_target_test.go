package handler

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

func TestResolveUserResearchMessageTargetPrefersDirectorOnV6(t *testing.T) {
	requested := pgtype.UUID{}
	director := parseUUID("11111111-1111-1111-1111-111111111111")
	fleet := parseUUID("22222222-2222-2222-2222-222222222222")
	got := resolveUserResearchMessageTarget(researchrun.OrchestratorVersionV6, requested, director, fleet)
	if uuidToString(got) != uuidToString(director) {
		t.Fatalf("v6 default=%s want director", uuidToString(got))
	}
}

func TestResolveUserResearchMessageTargetKeepsFleetLeadOnV5(t *testing.T) {
	director := parseUUID("11111111-1111-1111-1111-111111111111")
	fleet := parseUUID("22222222-2222-2222-2222-222222222222")
	got := resolveUserResearchMessageTarget(researchrun.OrchestratorVersionV5, pgtype.UUID{}, director, fleet)
	if uuidToString(got) != uuidToString(fleet) {
		t.Fatalf("v5 default=%s want fleet lead", uuidToString(got))
	}
}

func TestResolveUserResearchMessageTargetHonorsExplicitTarget(t *testing.T) {
	requested := parseUUID("33333333-3333-3333-3333-333333333333")
	director := parseUUID("11111111-1111-1111-1111-111111111111")
	fleet := parseUUID("22222222-2222-2222-2222-222222222222")
	got := resolveUserResearchMessageTarget(researchrun.OrchestratorVersionV6, requested, director, fleet)
	if uuidToString(got) != uuidToString(requested) {
		t.Fatalf("explicit=%s", uuidToString(got))
	}
}
