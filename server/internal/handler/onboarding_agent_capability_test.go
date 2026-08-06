package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestAgentExecutionProfile_HiringSkillOnlyForStructuredBinding(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	boundID := createHandlerTestAgent(t, "bound_onboarding_"+strings.ReplaceAll(uuid.NewString(), "-", "_"), nil)
	ordinaryID := createHandlerTestAgent(t, "ordinary_wendy_"+strings.ReplaceAll(uuid.NewString(), "-", "_"), nil)
	if _, err := testPool.Exec(ctx, `UPDATE agent SET display_name = 'People Partner' WHERE id = $1`, boundID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent SET display_name = 'Wendy' WHERE id = $1`, ordinaryID); err != nil {
		t.Fatal(err)
	}
	bindOnboardingAgentForTest(t, boundID)

	bound, err := testHandler.Queries.GetAgent(ctx, parseUUID(boundID))
	if err != nil {
		t.Fatal(err)
	}
	ordinary, err := testHandler.Queries.GetAgent(ctx, parseUUID(ordinaryID))
	if err != nil {
		t.Fatal(err)
	}

	assertHiringContract := func(t *testing.T, skillsText string, want bool) {
		t.Helper()
		markers := []string{"multica-creating-agents", "agent:create", "/api/agent/actions/prepare"}
		for _, marker := range markers {
			if strings.Contains(skillsText, marker) != want {
				t.Fatalf("marker %q present=%v want=%v", marker, strings.Contains(skillsText, marker), want)
			}
		}
	}
	var boundText, ordinaryText strings.Builder
	for _, skill := range testHandler.builtinSkillsForAgent(ctx, bound) {
		boundText.WriteString(skill.Name)
		boundText.WriteString(skill.Content)
		for _, file := range skill.Files {
			boundText.WriteString(file.Content)
		}
	}
	for _, skill := range testHandler.builtinSkillsForAgent(ctx, ordinary) {
		ordinaryText.WriteString(skill.Name)
		ordinaryText.WriteString(skill.Content)
		for _, file := range skill.Files {
			ordinaryText.WriteString(file.Content)
		}
	}
	assertHiringContract(t, boundText.String(), true)
	assertHiringContract(t, ordinaryText.String(), false)
}
