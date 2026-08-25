// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/util"
)

// GraphMemoryLeaseService owns graph-version retention leases (spec §15,
// A26): GC must not collect a version while any open lease references it.
type GraphMemoryLeaseService struct {
	pool *pgxpool.Pool
}

func NewGraphMemoryLeaseService(pool *pgxpool.Pool) *GraphMemoryLeaseService {
	return &GraphMemoryLeaseService{pool: pool}
}

// AcquireVersionLease opens (or reuses) a retention lease for one consumer
// on one pinned graph version. consumerKind must be recall|dive|export|
// backtest; unknown owners fail closed. Idempotent per
// (graph_kind, graph_owner_id, graph_version, consumer_kind, consumer_id):
// an already-open row is returned without inserting a second.
func (s *GraphMemoryLeaseService) AcquireVersionLease(ctx context.Context, graphKind, graphOwnerID string, graphVersion int, consumerKind, consumerID string) (string, error) {
	return acquireVersionLease(ctx, s.pool, graphKind, graphOwnerID, graphVersion, consumerKind, consumerID)
}

// ReleaseVersionLease marks the lease released. An already-released id is a
// no-op success; an unknown id fails closed.
func (s *GraphMemoryLeaseService) ReleaseVersionLease(ctx context.Context, leaseID string) error {
	return releaseVersionLease(ctx, s.pool, leaseID)
}

// OpenLeasedVersions returns the graph versions that currently have at
// least one open lease for the given owner.
func (s *GraphMemoryLeaseService) OpenLeasedVersions(ctx context.Context, graphKind, graphOwnerID string) (map[int]bool, error) {
	return openLeasedVersions(ctx, s.pool, graphKind, graphOwnerID)
}

func acquireVersionLease(ctx context.Context, q graphMemoryQuerier, graphKind, graphOwnerID string, graphVersion int, consumerKind, consumerID string) (string, error) {
	if err := validateGraphMemoryConsumerKind(consumerKind); err != nil {
		return "", err
	}
	if graphVersion < 1 {
		return "", fmt.Errorf("graph memory lease: graph version must be >= 1")
	}
	consumer, err := util.ParseUUID(consumerID)
	if err != nil {
		return "", fmt.Errorf("graph memory lease: consumer id: %w", err)
	}
	ws, owner, err := lookupGraphOwnerWorkspace(ctx, q, graphKind, graphOwnerID)
	if err != nil {
		return "", err
	}

	var existing pgtype.UUID
	err = q.QueryRow(ctx, `
		SELECT id FROM graph_memory_version_lease
		WHERE graph_kind = $1 AND graph_owner_id = $2 AND graph_version = $3
		  AND consumer_kind = $4 AND consumer_id = $5 AND released_at IS NULL
	`, graphKind, owner, graphVersion, consumerKind, consumer).Scan(&existing)
	switch {
	case err == nil:
		return util.UUIDToString(existing), nil
	case !errors.Is(err, pgx.ErrNoRows):
		return "", fmt.Errorf("graph memory lease: lookup open lease: %w", err)
	}

	var id pgtype.UUID
	if err := q.QueryRow(ctx, `
		INSERT INTO graph_memory_version_lease
		  (workspace_id, graph_kind, graph_owner_id, graph_version, consumer_kind, consumer_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, ws, graphKind, owner, graphVersion, consumerKind, consumer).Scan(&id); err != nil {
		return "", fmt.Errorf("graph memory lease: insert: %w", err)
	}
	return util.UUIDToString(id), nil
}

func releaseVersionLease(ctx context.Context, q graphMemoryQuerier, leaseID string) error {
	id, err := util.ParseUUID(leaseID)
	if err != nil {
		return fmt.Errorf("graph memory lease: lease id: %w", err)
	}
	var found pgtype.UUID
	err = q.QueryRow(ctx, `
		UPDATE graph_memory_version_lease
		SET released_at = COALESCE(released_at, now())
		WHERE id = $1
		RETURNING id
	`, id).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("graph memory lease: unknown lease %s", leaseID)
	}
	if err != nil {
		return fmt.Errorf("graph memory lease: release: %w", err)
	}
	return nil
}

func openLeasedVersions(ctx context.Context, q graphMemoryQuerier, graphKind, graphOwnerID string) (map[int]bool, error) {
	owner, err := util.ParseUUID(graphOwnerID)
	if err != nil {
		return nil, fmt.Errorf("graph memory lease: graph owner id: %w", err)
	}
	rows, err := q.Query(ctx, `
		SELECT DISTINCT graph_version FROM graph_memory_version_lease
		WHERE graph_kind = $1 AND graph_owner_id = $2 AND released_at IS NULL
	`, graphKind, owner)
	if err != nil {
		return nil, fmt.Errorf("graph memory lease: list open versions: %w", err)
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = true
	}
	return out, rows.Err()
}

func validateGraphMemoryConsumerKind(kind string) error {
	switch kind {
	case "recall", "dive", "export", "backtest":
		return nil
	}
	return fmt.Errorf("graph memory lease: invalid consumer kind %q", kind)
}

func lookupGraphOwnerWorkspace(ctx context.Context, q graphMemoryQuerier, graphKind, graphOwnerID string) (pgtype.UUID, pgtype.UUID, error) {
	owner, err := util.ParseUUID(graphOwnerID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, fmt.Errorf("graph memory lease: graph owner id: %w", err)
	}
	var query string
	switch graphKind {
	case "project":
		query = `SELECT workspace_id FROM project WHERE id = $1`
	case "channel":
		query = `SELECT workspace_id FROM channel WHERE id = $1`
	default:
		return pgtype.UUID{}, pgtype.UUID{}, fmt.Errorf("graph memory lease: unknown graph kind %q", graphKind)
	}
	var ws pgtype.UUID
	err = q.QueryRow(ctx, query, owner).Scan(&ws)
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, pgtype.UUID{}, fmt.Errorf("graph memory lease: unknown %s owner %s", graphKind, graphOwnerID)
	}
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, fmt.Errorf("graph memory lease: lookup owner: %w", err)
	}
	return ws, owner, nil
}

// graphMemoryQuerier is the pool-or-tx surface used by lease helpers so Dive
// can acquire/release inside an existing transaction.
type graphMemoryQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}
