package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
)

const (
	migrationLeaseStatusReserved  = "reserved"
	migrationLeaseStatusCommitted = "committed"
	migrationLeaseStatusReleased  = "released"
	migrationLeaseStatusExpired   = "expired"

	migrationLeaseDefaultTTL = 72 * time.Hour
	migrationLeaseMaxTTL     = 14 * 24 * time.Hour
)

var migrationFilenameNumberRe = regexp.MustCompile(`^(\d+)_`)

type MigrationLeaseResponse struct {
	ID              string  `json:"id"`
	WorkspaceID     string  `json:"workspace_id"`
	MigrationNumber int     `json:"migration_number"`
	OwnerAgentID    string  `json:"owner_agent_id"`
	IssueID         *string `json:"issue_id,omitempty"`
	PRNumber        *int    `json:"pr_number,omitempty"`
	Filename        string  `json:"filename"`
	Status          string  `json:"status"`
	ExpiresAt       string  `json:"expires_at"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
}

type migrationLeaseReserveRequest struct {
	MigrationNumber int     `json:"migration_number"`
	Filename        string  `json:"filename"`
	IssueID         *string `json:"issue_id"`
	PRNumber        *int    `json:"pr_number"`
	TTLHours        int     `json:"ttl_hours"`
}

type migrationLeaseReleaseRequest struct {
	MigrationNumber int    `json:"migration_number"`
	LeaseID         string `json:"lease_id"`
}

// AgentMigrationLeaseReserve reserves a migration number for the calling agent.
func (h *Handler) AgentMigrationLeaseReserve(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	var req migrationLeaseReserveRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	lease, err := h.reserveMigrationLease(r.Context(), source.origin.workspaceID, source.origin.agentID, req)
	if err != nil {
		writeMigrationLeaseError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, lease)
}

// AgentMigrationLeaseRelease releases an active reserved lease owned by the caller.
func (h *Handler) AgentMigrationLeaseRelease(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	var req migrationLeaseReleaseRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.releaseMigrationLease(r.Context(), source.origin.workspaceID, source.origin.agentID, req); err != nil {
		writeMigrationLeaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"released": true})
}

// AgentMigrationLeaseList lists active reserved leases visible in the workspace.
func (h *Handler) AgentMigrationLeaseList(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	leases, err := h.listActiveMigrationLeases(r.Context(), source.origin.workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list migration leases")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"leases": leases})
}

func (h *Handler) reserveMigrationLease(ctx context.Context, workspaceID, agentID pgtype.UUID, req migrationLeaseReserveRequest) (MigrationLeaseResponse, error) {
	if err := h.expireStaleMigrationLeases(ctx); err != nil {
		return MigrationLeaseResponse{}, err
	}
	number := req.MigrationNumber
	filename := strings.TrimSpace(req.Filename)
	if number <= 0 && filename != "" {
		parsed, err := migrationNumberFromFilename(filename)
		if err != nil {
			return MigrationLeaseResponse{}, err
		}
		number = parsed
	}
	if number <= 0 {
		return MigrationLeaseResponse{}, errMigrationLeaseBadRequest("migration_number or filename with leading number is required")
	}
	if filename == "" {
		filename = fmt.Sprintf("%d_reserved.up.sql", number)
	} else {
		filename = filepath.Base(filename)
		parsed, err := migrationNumberFromFilename(filename)
		if err != nil {
			return MigrationLeaseResponse{}, err
		}
		if parsed != number {
			return MigrationLeaseResponse{}, errMigrationLeaseBadRequest("filename number does not match migration_number")
		}
	}
	ttl := migrationLeaseDefaultTTL
	if req.TTLHours > 0 {
		ttl = time.Duration(req.TTLHours) * time.Hour
		if ttl > migrationLeaseMaxTTL {
			ttl = migrationLeaseMaxTTL
		}
	}
	var issueID pgtype.UUID
	if req.IssueID != nil && strings.TrimSpace(*req.IssueID) != "" {
		parsed, err := util.ParseUUID(strings.TrimSpace(*req.IssueID))
		if err != nil {
			return MigrationLeaseResponse{}, errMigrationLeaseBadRequest("invalid issue_id")
		}
		issueID = parsed
	}
	expiresAt := time.Now().UTC().Add(ttl)
	row := h.DB.QueryRow(ctx, `
		INSERT INTO migration_lease (
			workspace_id, migration_number, owner_agent_id, issue_id, pr_number, filename, status, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, workspace_id, migration_number, owner_agent_id, issue_id, pr_number, filename, status, expires_at, created_at, updated_at`,
		workspaceID, number, agentID, nullableUUID(issueID), req.PRNumber, filename, migrationLeaseStatusReserved, expiresAt,
	)
	lease, err := scanMigrationLease(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return MigrationLeaseResponse{}, errMigrationLeaseConflict(fmt.Sprintf("migration number %d is already reserved", number))
		}
		return MigrationLeaseResponse{}, err
	}
	return lease, nil
}

func (h *Handler) releaseMigrationLease(ctx context.Context, workspaceID, agentID pgtype.UUID, req migrationLeaseReleaseRequest) error {
	if err := h.expireStaleMigrationLeases(ctx); err != nil {
		return err
	}
	leaseID := strings.TrimSpace(req.LeaseID)
	number := req.MigrationNumber
	if leaseID == "" && number <= 0 {
		return errMigrationLeaseBadRequest("lease_id or migration_number is required")
	}
	tag, err := h.DB.Exec(ctx, `
		UPDATE migration_lease
		SET status = $1, updated_at = now()
		WHERE workspace_id = $2
		  AND owner_agent_id = $3
		  AND status = $4
		  AND ($5 = '' OR id::text = $5)
		  AND ($6 <= 0 OR migration_number = $6)`,
		migrationLeaseStatusReleased, workspaceID, agentID, migrationLeaseStatusReserved, leaseID, number,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errMigrationLeaseNotFound("active migration lease not found for this agent")
	}
	return nil
}

func (h *Handler) listActiveMigrationLeases(ctx context.Context, workspaceID pgtype.UUID) ([]MigrationLeaseResponse, error) {
	if err := h.expireStaleMigrationLeases(ctx); err != nil {
		return nil, err
	}
	rows, err := h.DB.Query(ctx, `
		SELECT id, workspace_id, migration_number, owner_agent_id, issue_id, pr_number, filename, status, expires_at, created_at, updated_at
		FROM migration_lease
		WHERE workspace_id = $1 AND status = $2
		ORDER BY migration_number ASC`, workspaceID, migrationLeaseStatusReserved)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]MigrationLeaseResponse, 0)
	for rows.Next() {
		lease, err := scanMigrationLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, lease)
	}
	return out, rows.Err()
}

func (h *Handler) expireStaleMigrationLeases(ctx context.Context) error {
	_, err := h.DB.Exec(ctx, `
		UPDATE migration_lease
		SET status = $1, updated_at = now()
		WHERE status = $2 AND expires_at <= now()`,
		migrationLeaseStatusExpired, migrationLeaseStatusReserved,
	)
	return err
}

func scanMigrationLease(row rowScanner) (MigrationLeaseResponse, error) {
	var (
		id, workspaceID, ownerAgentID, issueID pgtype.UUID
		number                                 int
		prNumber                               pgtype.Int4
		filename, status                       string
		expiresAt, createdAt, updatedAt        pgtype.Timestamptz
	)
	if err := row.Scan(&id, &workspaceID, &number, &ownerAgentID, &issueID, &prNumber, &filename, &status, &expiresAt, &createdAt, &updatedAt); err != nil {
		return MigrationLeaseResponse{}, err
	}
	resp := MigrationLeaseResponse{
		ID:              uuidToString(id),
		WorkspaceID:     uuidToString(workspaceID),
		MigrationNumber: number,
		OwnerAgentID:    uuidToString(ownerAgentID),
		IssueID:         uuidToPtr(issueID),
		Filename:        filename,
		Status:          status,
		ExpiresAt:       timestampToString(expiresAt),
		CreatedAt:       timestampToString(createdAt),
		UpdatedAt:       timestampToString(updatedAt),
	}
	if prNumber.Valid {
		n := int(prNumber.Int32)
		resp.PRNumber = &n
	}
	return resp, nil
}

func migrationNumberFromFilename(filename string) (int, error) {
	base := filepath.Base(strings.TrimSpace(filename))
	m := migrationFilenameNumberRe.FindStringSubmatch(base)
	if len(m) != 2 {
		return 0, errMigrationLeaseBadRequest("filename must start with a migration number, e.g. 310_foo.up.sql")
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0, errMigrationLeaseBadRequest("invalid migration number in filename")
	}
	return n, nil
}

type migrationLeaseError struct {
	status  int
	message string
}

func (e *migrationLeaseError) Error() string { return e.message }

func errMigrationLeaseBadRequest(msg string) error {
	return &migrationLeaseError{status: http.StatusBadRequest, message: msg}
}
func errMigrationLeaseConflict(msg string) error {
	return &migrationLeaseError{status: http.StatusConflict, message: msg}
}
func errMigrationLeaseNotFound(msg string) error {
	return &migrationLeaseError{status: http.StatusNotFound, message: msg}
}

func writeMigrationLeaseError(w http.ResponseWriter, err error) {
	var mle *migrationLeaseError
	if errors.As(err, &mle) {
		writeError(w, mle.status, mle.message)
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "migration lease not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "migration lease operation failed")
}
