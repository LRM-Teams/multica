package handler

import (
	"errors"
	"testing"
)

func TestMigrationNumberFromFilename(t *testing.T) {
	t.Parallel()
	n, err := migrationNumberFromFilename("310_migration_lease.up.sql")
	if err != nil || n != 310 {
		t.Fatalf("got %d err=%v", n, err)
	}
	n, err = migrationNumberFromFilename("server/migrations/309_channel_message_kind_source.down.sql")
	if err != nil || n != 309 {
		t.Fatalf("path got %d err=%v", n, err)
	}
	if _, err := migrationNumberFromFilename("no_number.sql"); err == nil {
		t.Fatal("expected error for missing number")
	}
}

func TestMigrationNumberFromFilenameRejectsZero(t *testing.T) {
	t.Parallel()
	_, err := migrationNumberFromFilename("0_bad.up.sql")
	if err == nil {
		t.Fatal("expected error for zero")
	}
	var mle *migrationLeaseError
	if !errors.As(err, &mle) || mle.status != 400 {
		t.Fatalf("want bad request, got %v", err)
	}
}
