// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/util"
)

// GraphMemoryInfoItem is one catalog row plus its OR-equivalence node group
// (spec §8). Status gates management-backtest eligibility.
type GraphMemoryInfoItem struct {
	ID            string
	WorkspaceID   string
	GraphKind     string
	GraphOwnerID  string
	Statement     string
	StatementHash string
	Rationale     string
	SourceRefs    []string
	Status        string
	NodeIDs       []string
}

// GraphMemoryInfoCatalogService owns the persistent necessary-information
// catalog: stable per-graph identities, node-group unions, and per-recall
// required links.
type GraphMemoryInfoCatalogService struct {
	pool *pgxpool.Pool
}

func NewGraphMemoryInfoCatalogService(pool *pgxpool.Pool) *GraphMemoryInfoCatalogService {
	return &GraphMemoryInfoCatalogService{pool: pool}
}

// NormalizeInfoStatement lowercases, collapses all whitespace runs to a
// single space, and trims. The catalog hashes this form.
func NormalizeInfoStatement(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func infoStatementHash(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

// UpsertDiveInformationItems writes Dive necessary-information items into
// the graph-scoped catalog and links them to the recall. Existing items
// keep their first statement/rationale/status (no downgrade); node ids and
// source refs are unioned. Empty statements are skipped. An empty items
// slice is a no-op.
func (s *GraphMemoryInfoCatalogService) UpsertDiveInformationItems(ctx context.Context, q graphMemoryQuerier, recallID string, items []memorygraph.DiveInformationItem, authoritative bool) ([]string, error) {
	if len(items) == 0 {
		return nil, nil
	}
	rUUID, err := util.ParseUUID(recallID)
	if err != nil {
		return nil, fmt.Errorf("graph memory info catalog: recall id: %w", err)
	}
	var (
		ws, owner pgtype.UUID
		kind      string
	)
	err = q.QueryRow(ctx, `
		SELECT workspace_id, graph_kind, graph_owner_id
		FROM graph_memory_recall WHERE id = $1
	`, rUUID).Scan(&ws, &kind, &owner)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, fmt.Errorf("graph memory info catalog: unknown recall %s", recallID)
	case err != nil:
		return nil, fmt.Errorf("graph memory info catalog: load recall: %w", err)
	}

	var ids []string
	for _, item := range items {
		id, err := upsertDiveInformationItem(ctx, q, ws, owner, kind, item, authoritative)
		if err != nil {
			return nil, err
		}
		if !id.Valid {
			continue
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO graph_memory_recall_info_item (recall_id, item_id)
			VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, rUUID, id); err != nil {
			return nil, fmt.Errorf("graph memory info catalog: link recall: %w", err)
		}
		ids = append(ids, util.UUIDToString(id))
	}
	return ids, nil
}

func upsertDiveInformationItem(ctx context.Context, q graphMemoryQuerier, ws, owner pgtype.UUID, kind string, item memorygraph.DiveInformationItem, authoritative bool) (pgtype.UUID, error) {
	normalized := NormalizeInfoStatement(item.Statement)
	if normalized == "" {
		return pgtype.UUID{}, nil
	}
	hash := infoStatementHash(normalized)
	refsJSON, err := json.Marshal(nonEmptyStrings(item.SourceRefs))
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("graph memory info catalog: marshal source_refs: %w", err)
	}

	var id pgtype.UUID
	err = q.QueryRow(ctx, `
		SELECT id FROM graph_memory_info_item
		WHERE graph_kind = $1 AND graph_owner_id = $2 AND statement_hash = $3
	`, kind, owner, hash).Scan(&id)
	switch {
	case err == nil:
		// Reuse: never rewrite statement, rationale, or status.
	case errors.Is(err, pgx.ErrNoRows):
		status := "incomplete"
		if authoritative {
			status = "authoritative"
		}
		err = q.QueryRow(ctx, `
			INSERT INTO graph_memory_info_item (
			  workspace_id, graph_kind, graph_owner_id, statement, statement_hash,
			  rationale, source_refs, status
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (graph_kind, graph_owner_id, statement_hash) DO NOTHING
			RETURNING id
		`, ws, kind, owner, item.Statement, hash, item.Rationale, refsJSON, status).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			err = q.QueryRow(ctx, `
				SELECT id FROM graph_memory_info_item
				WHERE graph_kind = $1 AND graph_owner_id = $2 AND statement_hash = $3
			`, kind, owner, hash).Scan(&id)
		}
		if err != nil {
			return pgtype.UUID{}, fmt.Errorf("graph memory info catalog: insert item: %w", err)
		}
	default:
		return pgtype.UUID{}, fmt.Errorf("graph memory info catalog: lookup item: %w", err)
	}

	if _, err := q.Exec(ctx, `
		UPDATE graph_memory_info_item
		SET source_refs = (
		      SELECT COALESCE(jsonb_agg(to_jsonb(v)), '[]'::jsonb)
		      FROM (
		        SELECT DISTINCT v FROM (
		          SELECT jsonb_array_elements_text(source_refs) AS v
		          UNION
		          SELECT jsonb_array_elements_text($2::jsonb)
		        ) s
		        WHERE v <> ''
		      ) u
		    ),
		    updated_at = now()
		WHERE id = $1
	`, id, refsJSON); err != nil {
		return pgtype.UUID{}, fmt.Errorf("graph memory info catalog: merge source_refs: %w", err)
	}

	nodes := nonEmptyStrings(item.NodeIDs)
	if len(nodes) > 0 {
		if _, err := q.Exec(ctx, `
			INSERT INTO graph_memory_info_item_node (item_id, node_id, added_by)
			SELECT $1, unnest($2::text[]), 'dive'
			ON CONFLICT DO NOTHING
		`, id, nodes); err != nil {
			return pgtype.UUID{}, fmt.Errorf("graph memory info catalog: merge nodes: %w", err)
		}
	}
	return id, nil
}

// ItemsForRecall returns catalog items linked as required for one recall,
// including each item's node-id group.
func (s *GraphMemoryInfoCatalogService) ItemsForRecall(ctx context.Context, recallID string) ([]GraphMemoryInfoItem, error) {
	rUUID, err := util.ParseUUID(recallID)
	if err != nil {
		return nil, fmt.Errorf("graph memory info catalog: recall id: %w", err)
	}
	return loadInfoItems(ctx, s.pool, `
		SELECT i.id, i.workspace_id, i.graph_kind, i.graph_owner_id, i.statement, i.statement_hash,
		       i.rationale, i.source_refs, i.status,
		       COALESCE((SELECT array_agg(n.node_id ORDER BY n.node_id)
		                 FROM graph_memory_info_item_node n WHERE n.item_id = i.id), '{}')
		FROM graph_memory_info_item i
		JOIN graph_memory_recall_info_item l ON l.item_id = i.id
		WHERE l.recall_id = $1
		ORDER BY i.created_at, i.id
	`, rUUID)
}

// BacktestEligibleItems returns only status='authoritative' items for the
// graph scope. incomplete / judge_failed / legacy_non_authoritative never
// enter management backtests (spec §8/§16).
func (s *GraphMemoryInfoCatalogService) BacktestEligibleItems(ctx context.Context, graphKind, graphOwnerID string) ([]GraphMemoryInfoItem, error) {
	owner, err := util.ParseUUID(graphOwnerID)
	if err != nil {
		return nil, fmt.Errorf("graph memory info catalog: graph owner id: %w", err)
	}
	return loadInfoItems(ctx, s.pool, `
		SELECT i.id, i.workspace_id, i.graph_kind, i.graph_owner_id, i.statement, i.statement_hash,
		       i.rationale, i.source_refs, i.status,
		       COALESCE((SELECT array_agg(n.node_id ORDER BY n.node_id)
		                 FROM graph_memory_info_item_node n WHERE n.item_id = i.id), '{}')
		FROM graph_memory_info_item i
		WHERE i.graph_kind = $1 AND i.graph_owner_id = $2 AND i.status = 'authoritative'
		ORDER BY i.created_at, i.id
	`, graphKind, owner)
}

func loadInfoItems(ctx context.Context, q graphMemoryQuerier, sql string, args ...any) ([]GraphMemoryInfoItem, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("graph memory info catalog: list items: %w", err)
	}
	defer rows.Close()
	var out []GraphMemoryInfoItem
	for rows.Next() {
		var (
			id, ws, owner pgtype.UUID
			rawRefs       []byte
			item          GraphMemoryInfoItem
		)
		if err := rows.Scan(
			&id, &ws, &item.GraphKind, &owner, &item.Statement, &item.StatementHash,
			&item.Rationale, &rawRefs, &item.Status, &item.NodeIDs,
		); err != nil {
			return nil, fmt.Errorf("graph memory info catalog: scan item: %w", err)
		}
		item.ID = util.UUIDToString(id)
		item.WorkspaceID = util.UUIDToString(ws)
		item.GraphOwnerID = util.UUIDToString(owner)
		item.SourceRefs = []string{}
		if len(rawRefs) > 0 && string(rawRefs) != "null" {
			if err := json.Unmarshal(rawRefs, &item.SourceRefs); err != nil {
				return nil, fmt.Errorf("graph memory info catalog: decode source_refs: %w", err)
			}
		}
		if item.NodeIDs == nil {
			item.NodeIDs = []string{}
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func nonEmptyStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
