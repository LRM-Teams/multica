package main

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type artifactConstraintMode struct {
	name      string
	immediate bool
}

var artifactConstraintModes = []artifactConstraintMode{
	{name: "immediate", immediate: true},
	{name: "ordinary_commit"},
}

func commitOrForceArtifactConstraints(ctx context.Context, tx pgx.Tx, immediate bool) error {
	if immediate {
		if _, err := tx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func assertArtifactConstraint(t *testing.T, err error, want string) {
	t.Helper()
	pgErr, ok := err.(*pgconn.PgError)
	if !ok || pgErr.ConstraintName != want {
		t.Fatalf("constraint error=%v want=%s", err, want)
	}
}

func assertArtifactConstraintOneOf(t *testing.T, err error, wants ...string) {
	t.Helper()
	pgErr, ok := err.(*pgconn.PgError)
	if ok {
		for _, want := range wants {
			if pgErr.ConstraintName == want {
				return
			}
		}
	}
	t.Fatalf("constraint error=%v want one of %v", err, wants)
}
