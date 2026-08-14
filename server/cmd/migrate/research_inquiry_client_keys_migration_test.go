package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestResearchInquiryClientKeys352RoundTrips(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("research_inquiry_client_keys_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE")
	})
	if _, err = conn.Exec(ctx, "SET search_path TO "+quotedSchema+", public"); err != nil {
		t.Fatalf("set search path: %v", err)
	}

	if _, err = conn.Exec(ctx, `
		CREATE TABLE research_hypothesis (id uuid PRIMARY KEY, workspace_id uuid NOT NULL, session_id uuid NOT NULL);
		CREATE TABLE research_branch (id uuid PRIMARY KEY, workspace_id uuid NOT NULL, session_id uuid NOT NULL);
		CREATE TABLE research_insight (id uuid PRIMARY KEY, workspace_id uuid NOT NULL, session_id uuid NOT NULL);
		CREATE TABLE research_inquiry_edge (id uuid PRIMARY KEY, workspace_id uuid NOT NULL, session_id uuid NOT NULL);
	`); err != nil {
		t.Fatalf("create inquiry tables: %v", err)
	}
	workspaceID := "10000000-0000-4000-8000-000000000001"
	sessionID := "20000000-0000-4000-8000-000000000001"
	legacyIDs := []string{
		"30000000-0000-4000-8000-000000000001",
		"40000000-0000-4000-8000-000000000001",
		"50000000-0000-4000-8000-000000000001",
		"60000000-0000-4000-8000-000000000001",
	}
	tables := []string{"research_hypothesis", "research_branch", "research_insight", "research_inquiry_edge"}
	for i, table := range tables {
		if _, err = conn.Exec(ctx, "INSERT INTO "+table+" (id,workspace_id,session_id) VALUES ($1::uuid,$2::uuid,$3::uuid)", legacyIDs[i], workspaceID, sessionID); err != nil {
			t.Fatalf("seed %s: %v", table, err)
		}
	}

	up, down := readMigrationPair(t, "352_research_inquiry_client_keys")
	if _, err = conn.Exec(ctx, up); err != nil {
		t.Fatalf("apply up: %v", err)
	}
	for i, table := range tables {
		var clientKey string
		if err = conn.QueryRow(ctx, "SELECT client_key FROM "+table+" WHERE id=$1::uuid", legacyIDs[i]).Scan(&clientKey); err != nil {
			t.Fatalf("read %s client key: %v", table, err)
		}
		if want := "legacy:" + legacyIDs[i]; clientKey != want {
			t.Fatalf("%s client_key=%q want=%q", table, clientKey, want)
		}
	}

	assertConstraint := func(t *testing.T, got error, code, constraint string) {
		t.Helper()
		pgErr, ok := got.(*pgconn.PgError)
		if !ok || pgErr.Code != code || pgErr.ConstraintName != constraint {
			t.Fatalf("error=%v want code=%s constraint=%s", got, code, constraint)
		}
	}
	_, err = conn.Exec(ctx, `INSERT INTO research_hypothesis (id,workspace_id,session_id,client_key) VALUES ('30000000-0000-4000-8000-000000000002',$1::uuid,$2::uuid,$3)`, workspaceID, sessionID, "legacy:"+legacyIDs[0])
	assertConstraint(t, err, "23505", "research_hypothesis_client_key_unique")
	_, err = conn.Exec(ctx, `INSERT INTO research_branch (id,workspace_id,session_id,client_key) VALUES ('40000000-0000-4000-8000-000000000002',$1::uuid,$2::uuid,'bad key')`, workspaceID, sessionID)
	assertConstraint(t, err, "23514", "research_branch_client_key_format")

	if _, err = conn.Exec(ctx, down); err != nil {
		t.Fatalf("apply down: %v", err)
	}
	for _, table := range tables {
		var count int
		if err = conn.QueryRow(ctx, `SELECT count(*)::int FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2 AND column_name='client_key'`, schema, table).Scan(&count); err != nil {
			t.Fatalf("inspect %s after down: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s client_key remained after down", table)
		}
	}
	if _, err = conn.Exec(ctx, up); err != nil {
		t.Fatalf("reapply up: %v", err)
	}
}
