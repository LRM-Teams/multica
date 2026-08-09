package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	workOwnerLeaseRoleExecutor = "executor"
	workOwnerLeaseStatusActive = "active"
	workOwnerLeaseDefaultTTL   = 72 * time.Hour
)

var ErrWorkOwnerLeaseConflict = errors.New("issue already has an active executor lease owned by another agent")

// ensureExecutorWorkOwnerLease acquires or renews the active executor lease for
// the issue assignee before a task is enqueued. If another agent already holds
// the active executor lease, enqueue must fail closed.
func (s *TaskService) ensureExecutorWorkOwnerLease(ctx context.Context, issue db.Issue, agentID pgtype.UUID) error {
	exec := s.dbExec()
	if exec == nil {
		// Without a DBTX handle we cannot enforce the lease; fail closed so a
		// miswired TaskService cannot bypass ownership.
		return fmt.Errorf("work owner lease: database executor unavailable")
	}
	if _, err := exec.Exec(ctx, `
		UPDATE work_owner_lease
		SET status = 'expired', updated_at = now()
		WHERE status = 'active' AND expires_at <= now()`); err != nil {
		return fmt.Errorf("expire work owner leases: %w", err)
	}

	var (
		leaseID       pgtype.UUID
		ownerAgentID  pgtype.UUID
		canonical     pgtype.Text
	)
	err := exec.QueryRow(ctx, `
		SELECT id, owner_agent_id, canonical_branch
		FROM work_owner_lease
		WHERE issue_id = $1 AND role = $2 AND status = $3
		LIMIT 1`,
		issue.ID, workOwnerLeaseRoleExecutor, workOwnerLeaseStatusActive,
	).Scan(&leaseID, &ownerAgentID, &canonical)
	switch {
	case err == nil:
		if ownerAgentID != agentID {
			return fmt.Errorf("%w: lease=%s owner=%s requester=%s",
				ErrWorkOwnerLeaseConflict,
				util.UUIDToString(leaseID),
				util.UUIDToString(ownerAgentID),
				util.UUIDToString(agentID),
			)
		}
		_, err = exec.Exec(ctx, `
			UPDATE work_owner_lease
			SET expires_at = $2, updated_at = now()
			WHERE id = $1`, leaseID, time.Now().UTC().Add(workOwnerLeaseDefaultTTL))
		return err
	case errors.Is(err, pgx.ErrNoRows):
		// create below
	default:
		return fmt.Errorf("load work owner lease: %w", err)
	}

	branch := defaultCanonicalBranch(issue, agentID)
	_, err = exec.Exec(ctx, `
		INSERT INTO work_owner_lease (
			workspace_id, issue_id, owner_agent_id, role, canonical_branch, status, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		issue.WorkspaceID, issue.ID, agentID, workOwnerLeaseRoleExecutor, branch,
		workOwnerLeaseStatusActive, time.Now().UTC().Add(workOwnerLeaseDefaultTTL),
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrWorkOwnerLeaseConflict
		}
		return fmt.Errorf("create work owner lease: %w", err)
	}
	return nil
}

func defaultCanonicalBranch(issue db.Issue, agentID pgtype.UUID) string {
	issueKey := strings.ToLower(util.UUIDToString(issue.ID))
	if issue.Number > 0 {
		issueKey = fmt.Sprintf("issue-%d", issue.Number)
	}
	agentShort := strings.ToLower(util.UUIDToString(agentID))
	if len(agentShort) > 8 {
		agentShort = agentShort[:8]
	}
	return fmt.Sprintf("agent/%s/%s", agentShort, issueKey)
}
