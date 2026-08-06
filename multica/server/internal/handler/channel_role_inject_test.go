package handler

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// failRow makes Scan return a fixed error — used to inject non-NoRows DB errors
// through the real QueryRow → Scan production path.
type failRow struct{ err error }

func (f failRow) Scan(dest ...any) error { return f.err }

// roleMutationDB wraps dbExecutor for tests. It only intercepts the non-locking
// owner role SELECT used by actorIsChannelOwnerRead so the production
// QueryRow(...).Scan path receives a real non-NoRows error.
type roleMutationDB struct {
	inner             dbExecutor
	forceOwnerReadErr error
}

func (d *roleMutationDB) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	return d.inner.Exec(ctx, sql, arguments...)
}

func (d *roleMutationDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return d.inner.Query(ctx, sql, args...)
}

func (d *roleMutationDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if d.forceOwnerReadErr != nil && isOwnerRolePreReadSQL(sql) {
		return failRow{d.forceOwnerReadErr}
	}
	return d.inner.QueryRow(ctx, sql, args...)
}

func isOwnerRolePreReadSQL(sql string) bool {
	// Production actorIsChannelOwnerRead query — must not match FOR UPDATE
	// authorization rechecks.
	return strings.Contains(sql, "SELECT role FROM channel_member") &&
		strings.Contains(sql, "member_type = 'user'") &&
		!strings.Contains(sql, "FOR UPDATE")
}

// roleMutationTxStarter wraps Begin so INSERT INTO channel_message can fail
// inside the real transfer transaction.
type roleMutationTxStarter struct {
	inner               txStarter
	failInsertChannelID string
}

func (s *roleMutationTxStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.inner.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if s.failInsertChannelID == "" {
		return tx, nil
	}
	return &roleMutationTx{Tx: tx, failInsertChannelID: s.failInsertChannelID}, nil
}

// roleMutationTx intercepts the channel_message INSERT QueryRow used by
// insertChannelMessageWithPartsExec.
type roleMutationTx struct {
	pgx.Tx
	failInsertChannelID string
}

func (t *roleMutationTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if t.failInsertChannelID != "" && strings.Contains(sql, "INSERT INTO channel_message") {
		if argMentionsChannel(args, t.failInsertChannelID) {
			return failRow{fmt.Errorf("injected channel_message insert failure for %s", t.failInsertChannelID)}
		}
	}
	return t.Tx.QueryRow(ctx, sql, args...)
}

func (t *roleMutationTx) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	if t.failInsertChannelID != "" && strings.Contains(sql, "INSERT INTO channel_message") {
		if argMentionsChannel(arguments, t.failInsertChannelID) {
			return pgconn.CommandTag{}, fmt.Errorf("injected channel_message insert failure for %s", t.failInsertChannelID)
		}
	}
	return t.Tx.Exec(ctx, sql, arguments...)
}

func argMentionsChannel(args []any, channelID string) bool {
	for _, a := range args {
		switch v := a.(type) {
		case pgtype.UUID:
			if v.Valid && uuidToString(v) == channelID {
				return true
			}
		case string:
			if v == channelID {
				return true
			}
		}
	}
	return false
}

// installRoleMutationOwnerReadFail installs a DB wrapper so the production
// owner pre-read Scan fails with err (must not be pgx.ErrNoRows).
func installRoleMutationOwnerReadFail(h *Handler, err error) func() {
	prev := h.DB
	h.DB = &roleMutationDB{inner: prev, forceOwnerReadErr: err}
	return func() { h.DB = prev }
}

// installRoleMutationInsertFail installs a TxStarter wrapper so INSERT INTO
// channel_message for channelID fails inside the transfer tx.
func installRoleMutationInsertFail(h *Handler, channelID string) func() {
	prev := h.TxStarter
	h.TxStarter = &roleMutationTxStarter{inner: prev, failInsertChannelID: channelID}
	return func() { h.TxStarter = prev }
}
