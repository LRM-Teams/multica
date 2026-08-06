package handler

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestBuildResearchWakePrompt(t *testing.T) {
	var sid pgtype.UUID
	_ = sid.Scan("11111111-1111-1111-1111-111111111111")
	prompt := buildResearchWakePrompt(db.ResearchSession{
		ID:           sid,
		Title:        "T",
		Goal:         "G",
		Status:       "running",
		CurrentStage: "s1_plan",
	}, "please dig deeper", "user")
	if !strings.Contains(prompt, "Research Fleet assignment") {
		t.Fatal("missing header")
	}
	if !strings.Contains(prompt, "please dig deeper") {
		t.Fatal("missing body")
	}
	if !strings.Contains(prompt, "multica research") {
		t.Fatal("missing tool hint")
	}
	if !strings.Contains(prompt, researchChatSessionTitle(sid)[len("research:"):]) {
		t.Fatal("missing session id")
	}
}

func TestResearchDomainPlaybooks(t *testing.T) {
	books := researchDomainPlaybooks()
	for _, domain := range []string{"tech", "market", "academic", "game", "ai_engineering", "academic_papers", "finance", "design_visual"} {
		if books[domain] == "" {
			t.Fatalf("missing playbook %s", domain)
		}
	}
}

func TestNextResearchStage(t *testing.T) {
	if got := nextResearchStage("s1_plan"); got != "s2_sources" {
		t.Fatalf("got %q", got)
	}
	if got := nextResearchStage("s4_delivery"); got != "done" {
		t.Fatalf("got %q", got)
	}
	if got := nextResearchStage("unknown"); got != "s1_plan" {
		t.Fatalf("got %q", got)
	}
}

func TestResearchSeedRolesIncludeLead(t *testing.T) {
	roles := researchSeedRoles()
	if len(roles) < 5 {
		t.Fatalf("expected initial fleet size >= 5, got %d", len(roles))
	}
	foundLead := false
	for _, r := range roles {
		if r.IsLead {
			foundLead = true
			if r.Name != ronaldoAgentName {
				t.Fatalf("lead name = %q, want %q", r.Name, ronaldoAgentName)
			}
			if r.Instructions == "" || len(r.Instructions) < 200 {
				t.Fatal("ronaldo instructions must be detailed")
			}
		}
	}
	if !foundLead {
		t.Fatal("missing lead seed role")
	}
}
