package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestMigration445StoresActivityFactsAndBoundedText(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/445_daemon_owned_agent_activity_presentation.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, required := range []string{
		"DROP TABLE IF EXISTS agent_activity_probe",
		"summary_label",
		"title TEXT",
		"subtext TEXT",
		"activity_kind TEXT",
		"detail_kind TEXT",
		"body_kind TEXT",
		"DROP COLUMN IF EXISTS client_sequence",
		"DROP COLUMN IF EXISTS producer_fact_id",
		"DROP COLUMN IF EXISTS entry_body",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration 445 missing %q", required)
		}
	}
	for _, forbidden := range []string{"summary_tone", "summary_visibility", "tone TEXT"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("migration 445 contains server-owned visual semantic %q", forbidden)
		}
	}
}
