package handler

import (
	"context"
	"errors"

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
