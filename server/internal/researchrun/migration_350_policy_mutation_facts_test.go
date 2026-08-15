package researchrun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigration350RequiresExactPolicyMutationFacts(t *testing.T) {
	up, err := os.ReadFile(filepath.Join("..", "..", "migrations", "350_research_artifact_policy_mutation_facts.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	sql := string(up)
	for _, required := range []string{
		"research_artifact_policy_mutation_fact_shape_check",
		"WHEN 'current_version'",
		"new_current_version = old_current_version + 1",
		"new_current_version IS NOT NULL",
		"WHEN 'access'",
		"old_access_level IS DISTINCT FROM new_access_level",
		"old_access_level IS NOT NULL AND new_access_level IS NOT NULL",
		"WHEN 'lifecycle'",
		"old_lifecycle_status IS DISTINCT FROM new_lifecycle_status",
		"old_lifecycle_status IS NOT NULL AND new_lifecycle_status IS NOT NULL",
		"WHEN 'verification'",
		"WHEN 'supersession'",
		"WHEN 'eligibility'",
		"WHEN 'grant_create'",
		"WHEN 'grant_revoke'",
		"VALIDATE CONSTRAINT research_artifact_policy_mutation_fact_shape_check",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration 350 missing %q", required)
		}
	}

	down, err := os.ReadFile(filepath.Join("..", "..", "migrations", "350_research_artifact_policy_mutation_facts.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(down), "DROP CONSTRAINT IF EXISTS research_artifact_policy_mutation_fact_shape_check") {
		t.Fatal("migration 350 down does not remove its named constraint")
	}
}

func TestCurrentVersionWritersDoNotFabricateAccessFacts(t *testing.T) {
	for _, name := range []string{
		"postgres_artifact.go",
		"postgres_inquiry_transition.go",
	} {
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(source)
		currentVersion := strings.Index(text, "'current_version'")
		if currentVersion < 0 {
			t.Fatalf("%s does not write a current_version mutation", name)
		}
		windowEnd := currentVersion + 500
		if windowEnd > len(text) {
			windowEnd = len(text)
		}
		window := text[currentVersion:windowEnd]
		if !strings.Contains(window, "NULL,NULL") {
			t.Fatalf("%s current_version mutation must leave access facts null", name)
		}
	}
}

func TestMigration362GuardsCurrentVersionAndAccessReciprocity(t *testing.T) {
	up, err := os.ReadFile(filepath.Join("..", "..", "migrations", "362_research_artifact_access_policy_guard.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"research_artifact_current_version_to_policy_guard_fn",
		"research_artifact_policy_mutation_to_current_version_guard_fn",
		"m.mutation_kind = 'current_version'",
		"m.mutation_kind = 'access'",
		"m.old_access_level IS NULL",
		"old_version.access_level",
		"new_version.access_level",
		"research_artifact_current_version_to_policy_guard",
		"research_artifact_policy_mutation_to_current_version_guard",
	} {
		if !strings.Contains(string(up), required) {
			t.Fatalf("migration 362 missing %q", required)
		}
	}

	down, err := os.ReadFile(filepath.Join("..", "..", "migrations", "362_research_artifact_access_policy_guard.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(down), "NEW.mutation_kind NOT IN ('current_version', 'access')") {
		t.Fatal("migration 362 down does not restore the current-version-only guard")
	}
}
