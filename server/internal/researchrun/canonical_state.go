package researchrun

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

const canonicalStateSchemaVersion = "research-canonical-state-v1"

var (
	ErrInvalidCanonicalState    = errors.New("invalid research canonical state")
	ErrRunEventSequenceGap      = errors.New("research run event sequence gap")
	ErrRunEventSequenceConflict = errors.New("research run event sequence conflict")
)

// CanonicalStateSection contains one normalized set of rows from the durable
// Research Run model. Projection and scheduler bookkeeping are intentionally
// absent; they have separate replay and health checks.
type CanonicalStateSection struct {
	Name string          `json:"name"`
	Rows json.RawMessage `json:"rows"`
}

// CanonicalStateSnapshot is scoped to one durable Run identity. It is designed
// to compare a Run before and after retries, crash recovery, and event replay;
// it does not claim that independently-created Runs share generated identities.
type CanonicalStateSnapshot struct {
	SchemaVersion string                  `json:"schema_version"`
	Sections      []CanonicalStateSection `json:"sections"`
}

// NewCanonicalStateSnapshot validates and normalizes section and row ordering
// so query plans and JSON object key order cannot change the digest.
func NewCanonicalStateSnapshot(sections []CanonicalStateSection) (CanonicalStateSnapshot, error) {
	normalized := make([]CanonicalStateSection, 0, len(sections))
	seen := make(map[string]struct{}, len(sections))
	for _, section := range sections {
		name := strings.TrimSpace(section.Name)
		if name == "" {
			return CanonicalStateSnapshot{}, fmt.Errorf("%w: section name is empty", ErrInvalidCanonicalState)
		}
		if _, exists := seen[name]; exists {
			return CanonicalStateSnapshot{}, fmt.Errorf("%w: duplicate section %q", ErrInvalidCanonicalState, name)
		}
		rows, err := normalizeCanonicalRows(section.Rows)
		if err != nil {
			return CanonicalStateSnapshot{}, fmt.Errorf("%w: section %q: %v", ErrInvalidCanonicalState, name, err)
		}
		seen[name] = struct{}{}
		normalized = append(normalized, CanonicalStateSection{Name: name, Rows: rows})
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Name < normalized[j].Name })
	return CanonicalStateSnapshot{SchemaVersion: canonicalStateSchemaVersion, Sections: normalized}, nil
}

func normalizeCanonicalRows(raw json.RawMessage) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var rows []any
	if err := decoder.Decode(&rows); err != nil {
		return nil, fmt.Errorf("rows must be a JSON array: %w", err)
	}
	if rows == nil {
		return nil, errors.New("rows must be a JSON array")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected trailing JSON value")
		}
		return nil, fmt.Errorf("rows contain trailing data: %w", err)
	}
	canonicalRows := make([][]byte, 0, len(rows))
	for _, row := range rows {
		canonical, err := json.Marshal(row)
		if err != nil {
			return nil, fmt.Errorf("canonicalize row: %w", err)
		}
		canonicalRows = append(canonicalRows, canonical)
	}
	sort.Slice(canonicalRows, func(i, j int) bool { return bytes.Compare(canonicalRows[i], canonicalRows[j]) < 0 })
	var out bytes.Buffer
	out.WriteByte('[')
	for index, row := range canonicalRows {
		if index > 0 {
			out.WriteByte(',')
		}
		out.Write(row)
	}
	out.WriteByte(']')
	return json.RawMessage(out.Bytes()), nil
}

// Hash returns a SHA-256 digest of the normalized snapshot.
func (snapshot CanonicalStateSnapshot) Hash() (string, error) {
	normalized, err := NewCanonicalStateSnapshot(snapshot.Sections)
	if err != nil {
		return "", err
	}
	canonical, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("%w: marshal snapshot: %v", ErrInvalidCanonicalState, err)
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

type canonicalStateQuery struct {
	name  string
	query string
}

var canonicalStateQueries = []canonicalStateQuery{
	{name: "run", query: `
		SELECT COALESCE(jsonb_agg(row_data ORDER BY row_data::text), '[]'::jsonb)
		FROM (
			SELECT to_jsonb(run) - ARRAY[
				'created_at', 'updated_at', 'last_progress_at', 'next_reconcile_at',
				'reconcile_lease_token', 'reconcile_lease_expires_at', 'reconcile_lease_generation', 'last_user_activity_at'
			] AS row_data
			FROM research_session run
			WHERE run.id = $1::uuid AND run.workspace_id = $2::uuid
		) rows`},
	{name: "contracts", query: canonicalRowsQuery("research_contract_revision", "id, workspace_id, session_id, created_at")},
	{name: "questions", query: canonicalRowsQuery("research_question", "workspace_id, session_id, created_at, updated_at")},
	{name: "tasks", query: canonicalRowsQuery("research_task", "workspace_id, session_id, ready_at, started_at, completed_at, created_at, updated_at")},
	{name: "task_dependencies", query: `
		SELECT COALESCE(jsonb_agg(row_data ORDER BY row_data::text), '[]'::jsonb)
		FROM (
			SELECT to_jsonb(dependency) - ARRAY['created_at'] AS row_data
			FROM research_task_dependency dependency
			JOIN research_task task ON task.id = dependency.task_id
			WHERE task.session_id = $1::uuid AND task.workspace_id = $2::uuid
		) rows`},
	{name: "attempts", query: canonicalRowsQuery("research_task_attempt", "workspace_id, session_id, dispatched_at, started_at, runtime_started_at, runtime_last_observed_at, runtime_lease_expires_at, cancellation_requested_at, result_submitted_at, completed_at, cancellation_completed_at, created_at, updated_at")},
	{name: "search_plans", query: canonicalRowsQuery("research_search_plan", "workspace_id, session_id, created_at")},
	{name: "query_executions", query: canonicalRowsQuery("research_query_execution", "workspace_id, session_id, created_at")},
	{name: "source_candidates", query: canonicalRowsQuery("research_source_candidate", "workspace_id, session_id, created_at")},
	{name: "screening_decisions", query: canonicalRowsQuery("research_screening_decision", "workspace_id, session_id, created_at")},
	{name: "source_snapshots", query: canonicalRowsQuery("research_source_snapshot", "workspace_id, session_id, created_at")},
	{name: "observations", query: canonicalRowsQuery("research_observation", "workspace_id, session_id, created_at")},
	{name: "claims", query: canonicalRowsQuery("research_claim", "workspace_id, session_id, created_at, updated_at")},
	{name: "claim_evidence", query: canonicalRowsQuery("research_claim_evidence", "workspace_id, session_id, created_at, updated_at")},
	{name: "hypotheses", query: canonicalRowsQuery("research_hypothesis", "workspace_id, session_id, created_at, updated_at")},
	{name: "branches", query: canonicalRowsQuery("research_branch", "workspace_id, session_id, created_at, updated_at")},
	{name: "insights", query: canonicalRowsQuery("research_insight", "workspace_id, session_id, created_at, updated_at")},
	{name: "inquiry_edges", query: canonicalRowsQuery("research_inquiry_edge", "workspace_id, session_id, created_at")},
	{name: "decisions", query: canonicalRowsQuery("research_decision", "id, workspace_id, session_id, created_at")},
	{name: "reports", query: canonicalRowsQuery("research_report", "workspace_id, session_id, created_at, updated_at")},
	{name: "report_claims", query: `
		SELECT COALESCE(jsonb_agg(row_data ORDER BY row_data::text), '[]'::jsonb)
		FROM (
			SELECT to_jsonb(report_claim) - ARRAY['created_at'] AS row_data
			FROM research_report_claim report_claim
			JOIN research_report report ON report.id = report_claim.report_id
			WHERE report.session_id = $1::uuid AND report.workspace_id = $2::uuid
		) rows`},
}

func canonicalRowsQuery(table, excludedColumns string) string {
	columns := strings.Split(excludedColumns, ",")
	quoted := make([]string, 0, len(columns))
	for _, column := range columns {
		quoted = append(quoted, "'"+strings.TrimSpace(column)+"'")
	}
	return fmt.Sprintf(`
		SELECT COALESCE(jsonb_agg(row_data ORDER BY row_data::text), '[]'::jsonb)
		FROM (
			SELECT to_jsonb(entity) - ARRAY[%s] AS row_data
			FROM %s entity
			WHERE entity.session_id = $1::uuid AND entity.workspace_id = $2::uuid
		) rows`, strings.Join(quoted, ", "), table)
}

// CanonicalState loads the durable V1-V5 research model without scheduler,
// projection, lease, and row-maintenance bookkeeping fields.
func (store *PostgresStore) CanonicalState(ctx context.Context, sessionID, workspaceID string) (CanonicalStateSnapshot, error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return CanonicalStateSnapshot{}, err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err = tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM research_session
			WHERE id = $1::uuid AND workspace_id = $2::uuid
		)
	`, sessionID, workspaceID).Scan(&exists); err != nil {
		return CanonicalStateSnapshot{}, err
	}
	if !exists {
		return CanonicalStateSnapshot{}, ErrRunNotFound
	}
	sections := make([]CanonicalStateSection, 0, len(canonicalStateQueries))
	for _, specification := range canonicalStateQueries {
		var rows json.RawMessage
		if err = tx.QueryRow(ctx, specification.query, sessionID, workspaceID).Scan(&rows); err != nil {
			return CanonicalStateSnapshot{}, fmt.Errorf("load canonical state section %q: %w", specification.name, err)
		}
		sections = append(sections, CanonicalStateSection{Name: specification.name, Rows: rows})
	}
	snapshot, err := NewCanonicalStateSnapshot(sections)
	if err != nil {
		return CanonicalStateSnapshot{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CanonicalStateSnapshot{}, err
	}
	return snapshot, nil
}

// ListRunEvents returns the immutable event ledger in sequence order. It is
// separate from ListUnprojectedEvents because replay also reads projected rows.
func (store *PostgresStore) ListRunEvents(ctx context.Context, sessionID, workspaceID string, afterSequence int64, limit int) ([]RunEvent, error) {
	if afterSequence < 0 {
		return nil, fmt.Errorf("%w: negative event sequence", ErrRunEventSequenceGap)
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err = tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM research_session
			WHERE id = $1::uuid AND workspace_id = $2::uuid
		)
	`, sessionID, workspaceID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrRunNotFound
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text, workspace_id::text, session_id::text, sequence,
		       event_type, idempotency_key, actor_type, COALESCE(actor_id::text, ''),
		       payload, projection_attempts, created_at
		FROM research_run_event
		WHERE session_id = $1::uuid AND workspace_id = $2::uuid AND sequence > $3
		ORDER BY sequence
		LIMIT $4
	`, sessionID, workspaceID, afterSequence, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]RunEvent, 0)
	for rows.Next() {
		var event RunEvent
		if err = rows.Scan(&event.ID, &event.WorkspaceID, &event.SessionID, &event.Sequence,
			&event.Type, &event.IdempotencyKey, &event.ActorType, &event.ActorID,
			&event.Payload, &event.ProjectionAttempts, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return events, nil
}

// ReplayRunEvents applies a contiguous event sequence exactly once. Already
// applied events at or before afterSequence are ignored; conflicting duplicates
// and gaps fail before later events are applied.
func ReplayRunEvents(ctx context.Context, afterSequence int64, events []RunEvent, apply func(context.Context, RunEvent) error) (int64, error) {
	if afterSequence < 0 {
		return afterSequence, fmt.Errorf("%w: negative starting sequence", ErrRunEventSequenceGap)
	}
	if apply == nil {
		return afterSequence, errors.New("research run event replay apply function is nil")
	}
	lastSequence := afterSequence
	var lastApplied *RunEvent
	for _, event := range events {
		if err := ctx.Err(); err != nil {
			return lastSequence, err
		}
		if event.Sequence <= afterSequence {
			continue
		}
		if event.Sequence == lastSequence {
			if lastApplied != nil && replayEventsEqual(*lastApplied, event) {
				continue
			}
			return lastSequence, fmt.Errorf("%w at sequence %d", ErrRunEventSequenceConflict, event.Sequence)
		}
		if event.Sequence != lastSequence+1 {
			return lastSequence, fmt.Errorf("%w: got %d after %d", ErrRunEventSequenceGap, event.Sequence, lastSequence)
		}
		if err := apply(ctx, event); err != nil {
			return lastSequence, err
		}
		lastSequence = event.Sequence
		copy := event
		lastApplied = &copy
	}
	return lastSequence, nil
}

func replayEventsEqual(left, right RunEvent) bool {
	if left.ID != right.ID || left.Sequence != right.Sequence || left.WorkspaceID != right.WorkspaceID ||
		left.SessionID != right.SessionID || left.Type != right.Type || left.IdempotencyKey != right.IdempotencyKey ||
		left.ActorType != right.ActorType || left.ActorID != right.ActorID {
		return false
	}
	leftPayload, err := normalizeCanonicalRows(json.RawMessage("[" + string(left.Payload) + "]"))
	if err != nil {
		return false
	}
	rightPayload, err := normalizeCanonicalRows(json.RawMessage("[" + string(right.Payload) + "]"))
	return err == nil && bytes.Equal(leftPayload, rightPayload)
}
