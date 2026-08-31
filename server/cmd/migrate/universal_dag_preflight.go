package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const universalDAGPreflightQuery = `
WITH legacy_rows AS (
    SELECT
        segment.segment_id,
        segment.project_id,
        segment.agent_run_id,
        segment.start_seq,
        segment.end_seq,
        segment.trajectory_source,
        segment.trajectory <> '[]'::jsonb AS has_readable_trajectory,
        dispatch.run_id::text AS run_id,
        message.message_count,
        message.distinct_message_seq_count,
        message.min_message_seq,
        message.max_message_seq,
        current_setting('transaction_read_only')::boolean AS transaction_read_only
    FROM interaction_dag_segment AS segment
    LEFT JOIN env_dispatch_run AS dispatch
      ON dispatch.project_id::text = segment.project_id
    LEFT JOIN LATERAL (
        SELECT
            count(*)::bigint AS message_count,
            count(DISTINCT task_message.seq)::bigint AS distinct_message_seq_count,
            min(task_message.seq)::integer AS min_message_seq,
            max(task_message.seq)::integer AS max_message_seq
        FROM task_message
        WHERE task_message.task_id::text = segment.agent_run_id
          AND task_message.seq BETWEEN segment.start_seq AND segment.end_seq
    ) AS message ON true
)
SELECT
    segment_id,
    project_id,
    COALESCE(run_id, ''),
    agent_run_id,
    start_seq,
    end_seq,
    trajectory_source,
    has_readable_trajectory,
    message_count,
    distinct_message_seq_count,
    min_message_seq,
    max_message_seq,
    count(*) OVER (
        PARTITION BY run_id, agent_run_id, start_seq, end_seq
    )::bigint AS duplicate_range_count,
    transaction_read_only
FROM legacy_rows
ORDER BY segment_id
`

const (
	universalDAGMappable           = "mappable"
	universalDAGResanitizeRequired = "resanitize_required"
	universalDAGDuplicateConflict  = "duplicate_conflict"
	universalDAGUnmappable         = "unmappable"
)

type universalDAGLegacyRow struct {
	RowIdentity             string
	ProjectID               string
	RunID                   string
	AgentRunID              string
	StartSeq                int32
	EndSeq                  int32
	TrajectorySource        string
	HasReadableTrajectory   bool
	MessageCount            int64
	DistinctMessageSeqCount int64
	MinMessageSeq           *int32
	MaxMessageSeq           *int32
	DuplicateRangeCount     int64
	TransactionReadOnly     bool
}

type universalDAGPreflightSource interface {
	LoadUniversalDAGLegacyRows(context.Context) ([]universalDAGLegacyRow, error)
}

type postgresUniversalDAGPreflightSource struct {
	pool *pgxpool.Pool
}

type universalDAGPreflightCounts struct {
	Mappable           int `json:"mappable"`
	ResanitizeRequired int `json:"resanitize_required"`
	DuplicateConflict  int `json:"duplicate_conflict"`
	Unmappable         int `json:"unmappable"`
}

type universalDAGPreflightHashes struct {
	Mappable           []string `json:"mappable"`
	ResanitizeRequired []string `json:"resanitize_required"`
	DuplicateConflict  []string `json:"duplicate_conflict"`
	Unmappable         []string `json:"unmappable"`
}

type universalDAGPreflightReport struct {
	Counts    universalDAGPreflightCounts `json:"counts"`
	RowHashes universalDAGPreflightHashes `json:"row_hashes"`
}

func validateUniversalDAGPreflightArgs(args []string) error {
	if len(args) != 1 || args[0] != "--check-only" {
		return fmt.Errorf("universal-dag-preflight accepts exactly --check-only")
	}
	return nil
}

func executeUniversalDAGPreflightCommand(
	ctx context.Context,
	args []string,
	source universalDAGPreflightSource,
	output io.Writer,
) (int, error) {
	if err := validateUniversalDAGPreflightArgs(args); err != nil {
		return 1, err
	}
	clean, err := runUniversalDAGPreflight(ctx, source, output)
	if err != nil {
		return 1, err
	}
	if !clean {
		return 2, nil
	}
	return 0, nil
}

func (s postgresUniversalDAGPreflightSource) LoadUniversalDAGLegacyRows(ctx context.Context) ([]universalDAGLegacyRow, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin read-only preflight transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, universalDAGPreflightQuery)
	if err != nil {
		return nil, fmt.Errorf("query legacy interaction DAG rows: %w", err)
	}
	defer rows.Close()

	result := make([]universalDAGLegacyRow, 0)
	for rows.Next() {
		var row universalDAGLegacyRow
		var minMessageSeq pgtype.Int4
		var maxMessageSeq pgtype.Int4
		if err := rows.Scan(
			&row.RowIdentity,
			&row.ProjectID,
			&row.RunID,
			&row.AgentRunID,
			&row.StartSeq,
			&row.EndSeq,
			&row.TrajectorySource,
			&row.HasReadableTrajectory,
			&row.MessageCount,
			&row.DistinctMessageSeqCount,
			&minMessageSeq,
			&maxMessageSeq,
			&row.DuplicateRangeCount,
			&row.TransactionReadOnly,
		); err != nil {
			return nil, fmt.Errorf("scan legacy interaction DAG row: %w", err)
		}
		if minMessageSeq.Valid {
			value := minMessageSeq.Int32
			row.MinMessageSeq = &value
		}
		if maxMessageSeq.Valid {
			value := maxMessageSeq.Int32
			row.MaxMessageSeq = &value
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read legacy interaction DAG rows: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit read-only preflight transaction: %w", err)
	}
	return result, nil
}

func runUniversalDAGPreflight(
	ctx context.Context,
	source universalDAGPreflightSource,
	output io.Writer,
) (bool, error) {
	rows, err := source.LoadUniversalDAGLegacyRows(ctx)
	if err != nil {
		return false, err
	}

	report := universalDAGPreflightReport{
		RowHashes: universalDAGPreflightHashes{
			Mappable:           []string{},
			ResanitizeRequired: []string{},
			DuplicateConflict:  []string{},
			Unmappable:         []string{},
		},
	}
	for _, row := range rows {
		classification := classifyUniversalDAGLegacyRow(row)
		hash := hashUniversalDAGRowIdentity(row.RowIdentity)
		switch classification {
		case universalDAGMappable:
			report.Counts.Mappable++
			report.RowHashes.Mappable = append(report.RowHashes.Mappable, hash)
		case universalDAGResanitizeRequired:
			report.Counts.ResanitizeRequired++
			report.RowHashes.ResanitizeRequired = append(report.RowHashes.ResanitizeRequired, hash)
		case universalDAGDuplicateConflict:
			report.Counts.DuplicateConflict++
			report.RowHashes.DuplicateConflict = append(report.RowHashes.DuplicateConflict, hash)
		default:
			report.Counts.Unmappable++
			report.RowHashes.Unmappable = append(report.RowHashes.Unmappable, hash)
		}
	}

	sort.Strings(report.RowHashes.Mappable)
	sort.Strings(report.RowHashes.ResanitizeRequired)
	sort.Strings(report.RowHashes.DuplicateConflict)
	sort.Strings(report.RowHashes.Unmappable)
	if err := json.NewEncoder(output).Encode(report); err != nil {
		return false, fmt.Errorf("encode universal DAG preflight report: %w", err)
	}

	clean := report.Counts.ResanitizeRequired == 0 &&
		report.Counts.DuplicateConflict == 0 &&
		report.Counts.Unmappable == 0
	return clean, nil
}

func classifyUniversalDAGLegacyRow(row universalDAGLegacyRow) string {
	if !row.TransactionReadOnly ||
		row.RowIdentity == "" ||
		!isUniversalDAGUUID(row.ProjectID) ||
		!isUniversalDAGUUID(row.RunID) ||
		!isUniversalDAGUUID(row.AgentRunID) {
		return universalDAGUnmappable
	}
	if row.TrajectorySource != "areal_tensor" && row.TrajectorySource != "task_messages" {
		return universalDAGUnmappable
	}
	if row.StartSeq <= 0 || row.EndSeq < row.StartSeq {
		return universalDAGUnmappable
	}

	expectedMessages := int64(row.EndSeq) - int64(row.StartSeq) + 1
	if row.MessageCount != expectedMessages || row.DistinctMessageSeqCount != expectedMessages {
		return universalDAGUnmappable
	}
	if row.MinMessageSeq == nil || row.MaxMessageSeq == nil ||
		*row.MinMessageSeq != row.StartSeq || *row.MaxMessageSeq != row.EndSeq {
		return universalDAGUnmappable
	}
	if row.DuplicateRangeCount > 1 {
		return universalDAGDuplicateConflict
	}
	if row.HasReadableTrajectory {
		return universalDAGResanitizeRequired
	}
	return universalDAGMappable
}

func isUniversalDAGUUID(raw string) bool {
	var value pgtype.UUID
	return value.Scan(raw) == nil && value.Valid
}

func hashUniversalDAGRowIdentity(identity string) string {
	digest := sha256.Sum256([]byte("interaction_dag_segment\x00" + identity))
	return hex.EncodeToString(digest[:])
}
