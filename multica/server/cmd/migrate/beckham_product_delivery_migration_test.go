package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestBeckhamProductDeliveryMigration205PreservesLegacyRadarActions(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("beckham_product_delivery_migration_test_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE"); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
	})
	if _, err := conn.Exec(ctx, "SET search_path TO "+quotedSchema); err != nil {
		t.Fatalf("set search path: %v", err)
	}

	if _, err := conn.Exec(ctx, `
		CREATE TABLE agent_radar_action (
			id BIGSERIAL PRIMARY KEY,
			action_type TEXT NOT NULL CONSTRAINT agent_radar_action_action_type_check
				CHECK (action_type IN (
					'no_action',
					'post_channel_message',
					'reply_thread',
					'mention_agent',
					'create_issue',
					'comment_issue',
					'assign_issue',
					'schedule_reminder',
					'update_agent_plan'
				))
		);
		INSERT INTO agent_radar_action (action_type)
		VALUES ('schedule_reminder'), ('assign_issue'), ('comment_issue');
	`); err != nil {
		t.Fatalf("seed pre-205 radar action history: %v", err)
	}

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(testFile), "..", "..", "migrations")
	upSQL, err := os.ReadFile(filepath.Join(migrationsDir, "205_beckham_product_delivery_actions.up.sql"))
	if err != nil {
		t.Fatalf("read migration 205 up: %v", err)
	}
	if _, err := conn.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("apply migration 205 up with legacy radar action history: %v", err)
	}

	var historicalRows int
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FROM agent_radar_action
		WHERE action_type IN ('schedule_reminder', 'assign_issue', 'comment_issue')
	`).Scan(&historicalRows); err != nil || historicalRows != 3 {
		t.Fatalf("historical rows after migration = %d, err=%v, want 3", historicalRows, err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO agent_radar_action (action_type) VALUES ('request_rework')`); err != nil {
		t.Fatalf("insert request_rework after migration 205: %v", err)
	}
	_, err = conn.Exec(ctx, `INSERT INTO agent_radar_action (action_type) VALUES ('unknown_action')`)
	assertPgCode(t, err, "23514", "unknown post-205 radar action")

	downSQL, err := os.ReadFile(filepath.Join(migrationsDir, "205_beckham_product_delivery_actions.down.sql"))
	if err != nil {
		t.Fatalf("read migration 205 down: %v", err)
	}
	if _, err := conn.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("apply migration 205 down: %v", err)
	}
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FROM agent_radar_action
		WHERE action_type IN ('schedule_reminder', 'assign_issue', 'comment_issue')
	`).Scan(&historicalRows); err != nil || historicalRows != 3 {
		t.Fatalf("historical rows after down = %d, err=%v, want 3", historicalRows, err)
	}
	var requestReworkRows int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM agent_radar_action WHERE action_type = 'request_rework'`).Scan(&requestReworkRows); err != nil || requestReworkRows != 0 {
		t.Fatalf("request_rework rows after down = %d, err=%v, want 0", requestReworkRows, err)
	}
	_, err = conn.Exec(ctx, `INSERT INTO agent_radar_action (action_type) VALUES ('request_rework')`)
	assertPgCode(t, err, "23514", "request_rework after migration 205 down")
}
