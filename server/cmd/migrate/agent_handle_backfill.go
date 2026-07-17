package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/identityhandle"
)

// agentHandleBackfillRow is intentionally string-only so the deterministic
// planning contract can be tested without a live database.
type agentHandleBackfillRow struct {
	ID          string
	WorkspaceID string
	Name        string
	DisplayName string
}

func runAgentASCIIHandleBackfillHook(ctx context.Context, pool *pgxpool.Pool) error {
	return runAgentHandleRepairHook(ctx, pool, planAgentASCIIHandleBackfill)
}

func runAgentDefaultHandleRepairHook(ctx context.Context, pool *pgxpool.Pool) error {
	return runAgentHandleRepairHook(ctx, pool, planAgentDefaultHandleRepair)
}

func runAgentHandleRepairHook(
	ctx context.Context,
	pool *pgxpool.Pool,
	plan func([]agentHandleBackfillRow) (map[string]string, error),
) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin agent handle backfill: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT id::text, workspace_id::text, name,
		       COALESCE(NULLIF(display_name, ''), name)
		FROM agent
		ORDER BY workspace_id, created_at, id`)
	if err != nil {
		return fmt.Errorf("list agents for handle backfill: %w", err)
	}
	defer rows.Close()

	agents := make([]agentHandleBackfillRow, 0)
	for rows.Next() {
		var agent agentHandleBackfillRow
		if err := rows.Scan(&agent.ID, &agent.WorkspaceID, &agent.Name, &agent.DisplayName); err != nil {
			return fmt.Errorf("scan agent for handle backfill: %w", err)
		}
		agents = append(agents, agent)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate agents for handle backfill: %w", err)
	}

	updates, err := plan(agents)
	if err != nil {
		return err
	}
	for _, agent := range agents {
		name, ok := updates[agent.ID]
		if !ok {
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE agent SET name = $1 WHERE id = $2::uuid`, name, agent.ID); err != nil {
			return fmt.Errorf("backfill agent %s handle: %w", agent.ID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit agent handle backfill: %w", err)
	}
	return nil
}

// planAgentASCIIHandleBackfill changes only invalid historical agent handles.
// Valid current handles reserve their names. This deliberately has no alias
// output: raw historic @actor_N text remains ordinary historical text while
// structured references continue to point to immutable agent UUIDs.
func planAgentASCIIHandleBackfill(agents []agentHandleBackfillRow) (map[string]string, error) {
	used := make(map[string]map[string]struct{})
	for _, agent := range agents {
		if identityhandle.Validate(agent.Name) != nil {
			continue
		}
		if used[agent.WorkspaceID] == nil {
			used[agent.WorkspaceID] = make(map[string]struct{})
		}
		used[agent.WorkspaceID][agent.Name] = struct{}{}
	}

	updates := make(map[string]string)
	for _, agent := range agents {
		if identityhandle.Validate(agent.Name) == nil {
			continue
		}
		if used[agent.WorkspaceID] == nil {
			used[agent.WorkspaceID] = make(map[string]struct{})
		}
		base := identityhandle.Base(strings.TrimSpace(agent.DisplayName), agent.Name)
		for attempt := 1; attempt <= 100; attempt++ {
			candidate := identityhandle.Candidate(base, attempt)
			if _, exists := used[agent.WorkspaceID][candidate]; exists {
				continue
			}
			updates[agent.ID] = candidate
			used[agent.WorkspaceID][candidate] = struct{}{}
			break
		}
		if _, ok := updates[agent.ID]; !ok {
			return nil, fmt.Errorf("agent handle backfill exhausted candidates for agent %s", agent.ID)
		}
	}
	return updates, nil
}

// planAgentDefaultHandleRepair retires the two historic system fallback
// usernames. They are syntactically valid but are not meaningful, user-chosen
// identities; all other valid usernames remain reserved and untouched.
func planAgentDefaultHandleRepair(agents []agentHandleBackfillRow) (map[string]string, error) {
	used := make(map[string]map[string]struct{})
	for _, agent := range agents {
		if identityhandle.Validate(agent.Name) != nil {
			continue
		}
		if used[agent.WorkspaceID] == nil {
			used[agent.WorkspaceID] = make(map[string]struct{})
		}
		used[agent.WorkspaceID][agent.Name] = struct{}{}
	}

	updates := make(map[string]string)
	for _, agent := range agents {
		if !isHistoricDefaultHandle(agent.Name) {
			continue
		}
		if used[agent.WorkspaceID] == nil {
			used[agent.WorkspaceID] = make(map[string]struct{})
		}
		base := identityhandle.Base(strings.TrimSpace(agent.DisplayName), agent.Name)
		for attempt := 1; attempt <= 100; attempt++ {
			candidate := identityhandle.Candidate(base, attempt)
			if _, exists := used[agent.WorkspaceID][candidate]; exists {
				continue
			}
			updates[agent.ID] = candidate
			used[agent.WorkspaceID][candidate] = struct{}{}
			break
		}
		if _, ok := updates[agent.ID]; !ok {
			return nil, fmt.Errorf("agent default handle repair exhausted candidates for agent %s", agent.ID)
		}
	}
	return updates, nil
}

func isHistoricDefaultHandle(handle string) bool {
	return handle == "actor" || handle == "agent"
}
