package handler

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestLegacyOnboardingAgentMigrationClassifiesWithoutGuessing(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	type fixture struct {
		kind       string
		workspace  pgtype.UUID
		candidates []pgtype.UUID
		ordinary   pgtype.UUID
	}
	fixtures := make([]fixture, 0, 4)
	for _, kind := range []string{"already_bound", "one_candidate", "no_candidate", "multiple_candidates"} {
		suffix := uuid.NewString()
		var ws, owner pgtype.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO workspace (name, slug, issue_prefix) VALUES ($1,$2,'WEN') RETURNING id`, "migration-"+kind, "migration-"+suffix).Scan(&ws); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(ctx, `INSERT INTO "user" (name,email) VALUES ($1,$2) RETURNING id`, "owner-"+suffix, suffix+"@migration.test").Scan(&owner); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO member (workspace_id,user_id,role) VALUES ($1,$2,'owner')`, ws, owner); err != nil {
			t.Fatal(err)
		}
		f := fixture{kind: kind, workspace: ws}
		candidateCount := map[string]int{"already_bound": 1, "one_candidate": 1, "no_candidate": 0, "multiple_candidates": 2}[kind]
		for i := 0; i < candidateCount; i++ {
			var id pgtype.UUID
			if err := tx.QueryRow(ctx, `INSERT INTO agent (workspace_id,name,display_name,runtime_mode,runtime_config,runtime_id,max_concurrent_tasks,owner_id,model) VALUES ($1,$2,'Wendy','local','{}',$3,1,$4,$5) RETURNING id`, ws, "wendy-"+uuid.NewString(), parseUUID(testRuntimeID), owner, "preserved-model").Scan(&id); err != nil {
				t.Fatal(err)
			}
			f.candidates = append(f.candidates, id)
		}
		if err := tx.QueryRow(ctx, `INSERT INTO agent (workspace_id,name,display_name,runtime_mode,runtime_config,runtime_id,max_concurrent_tasks,owner_id,model) VALUES ($1,$2,'Builder','local','{}',$3,1,$4,'ordinary-model') RETURNING id`, ws, "builder-"+uuid.NewString(), parseUUID(testRuntimeID), owner).Scan(&f.ordinary); err != nil {
			t.Fatal(err)
		}
		if kind == "already_bound" {
			if _, err := tx.Exec(ctx, `UPDATE workspace SET onboarding_agent_id=$2 WHERE id=$1`, ws, f.candidates[0]); err != nil {
				t.Fatal(err)
			}
		}
		fixtures = append(fixtures, f)
	}

	sqlBytes, err := os.ReadFile(filepath.Join("..", "..", "migrations", "302_adopt_legacy_onboarding_agent.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
		t.Fatal(err)
	}
	// Applying the repair twice must be a no-op.
	if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
		t.Fatal(err)
	}

	for _, f := range fixtures {
		var bound pgtype.UUID
		if err := tx.QueryRow(ctx, `SELECT onboarding_agent_id FROM workspace WHERE id=$1`, f.workspace).Scan(&bound); err != nil {
			t.Fatal(err)
		}
		wantBound := f.kind == "already_bound" || f.kind == "one_candidate"
		if bound.Valid != wantBound {
			t.Fatalf("%s binding valid=%v want=%v", f.kind, bound.Valid, wantBound)
		}
		if wantBound && uuidToString(bound) != uuidToString(f.candidates[0]) {
			t.Fatalf("%s bound=%s want=%s", f.kind, uuidToString(bound), uuidToString(f.candidates[0]))
		}
		var ordinaryModel string
		if err := tx.QueryRow(ctx, `SELECT model FROM agent WHERE id=$1`, f.ordinary).Scan(&ordinaryModel); err != nil {
			t.Fatal(err)
		}
		if ordinaryModel != "ordinary-model" {
			t.Fatalf("%s unrelated agent changed: %q", f.kind, ordinaryModel)
		}
		for _, candidate := range f.candidates {
			var model string
			if err := tx.QueryRow(ctx, `SELECT model FROM agent WHERE id=$1`, candidate).Scan(&model); err != nil {
				t.Fatal(err)
			}
			if model != "preserved-model" {
				t.Fatalf("%s candidate model changed: %q", f.kind, model)
			}
		}
	}
}
