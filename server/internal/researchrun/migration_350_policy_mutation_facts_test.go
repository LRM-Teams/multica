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
