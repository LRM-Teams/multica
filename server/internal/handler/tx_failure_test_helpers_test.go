package handler

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

// commitFailingTxStarter wraps a txStarter so every transaction it begins
// fails on Commit — used to test rollback/error handling on the commit path
// without needing a real DB failure.
type commitFailingTxStarter struct {
	base txStarter
}

func (s commitFailingTxStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.base.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &commitFailingTx{Tx: tx}, nil
}

type commitFailingTx struct {
	pgx.Tx
}

func (tx *commitFailingTx) Commit(context.Context) error {
	return errors.New("injected commit failure")
}

// queryRowFailingTxStarter keeps all real PostgreSQL transaction behavior but
// injects one QueryRow failure at a selected statement. It is used to prove
// that writes performed earlier in the same transaction are actually rolled
// back rather than merely observing application stage ordering.
type queryRowFailingTxStarter struct {
	base        txStarter
	sqlContains string
	err         error
}

func (s queryRowFailingTxStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.base.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &queryRowFailingTx{Tx: tx, sqlContains: s.sqlContains, err: s.err}, nil
}

type queryRowFailingTx struct {
	pgx.Tx
	sqlContains string
	err         error
}

func (tx *queryRowFailingTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if tx.err != nil && strings.Contains(sql, tx.sqlContains) {
		return failRow{err: tx.err}
	}
	return tx.Tx.QueryRow(ctx, sql, args...)
}

type queryFailingTxStarter struct {
	base        txStarter
	sqlContains string
	err         error
}

func (s queryFailingTxStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.base.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &queryFailingTx{Tx: tx, sqlContains: s.sqlContains, err: s.err}, nil
}

type queryFailingTx struct {
	pgx.Tx
	sqlContains string
	err         error
}

func (tx *queryFailingTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if tx.err != nil && strings.Contains(sql, tx.sqlContains) {
		return nil, tx.err
	}
	return tx.Tx.Query(ctx, sql, args...)
}
