package handler

import (
	"context"
	"testing"
)

// TestWorkspaceDefaultSelfPlayEnvColumn verifies migration 141 added the
// nullable FK column env-dispatch reads for self_play default-env resolution.
func TestWorkspaceDefaultSelfPlayEnvColumn(t *testing.T) {
	ctx := context.Background()
	var isNullable, dataType string
	err := testPool.QueryRow(ctx, `
		SELECT is_nullable, data_type
		  FROM information_schema.columns
		 WHERE table_name = 'workspace'
		   AND column_name = 'default_self_play_env_id'
	`).Scan(&isNullable, &dataType)
	if err != nil {
		t.Fatalf("column default_self_play_env_id not found: %v", err)
	}
	if isNullable != "YES" {
		t.Errorf("default_self_play_env_id must be nullable, got is_nullable=%q", isNullable)
	}
	if dataType != "uuid" {
		t.Errorf("default_self_play_env_id must be uuid, got %q", dataType)
	}
}
