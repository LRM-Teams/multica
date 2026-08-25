package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	workOwnerLeaseRoleExecutor    = "executor"
	workOwnerLeaseRoleReviewer    = "reviewer"
	workOwnerLeaseRoleCoordinator = "coordinator"
	workOwnerLeaseStatusActive    = "active"
	workOwnerLeaseStatusReleased  = "released"
	workOwnerLeaseDefaultTTL      = 72 * time.Hour
	workOwnerLeaseMaxTTL          = 14 * 24 * time.Hour
)

type WorkOwnerLeaseResponse struct {
	ID               string   `json:"id"`
	WorkspaceID      string   `json:"workspace_id"`
	IssueID          string   `json:"issue_id"`
	OwnerAgentID     string   `json:"owner_agent_id"`
	Role             string   `json:"role"`
	CanonicalBranch  *string  `json:"canonical_branch,omitempty"`
	ConversationID   *string  `json:"conversation_id,omitempty"`
	RuntimeLane      *string  `json:"runtime_lane,omitempty"`
	AllowedPaths     []string `json:"allowed_paths"`
	MigrationNumbers []int    `json:"migration_numbers"`
	HandoffTo        *string  `json:"handoff_to,omitempty"`
	Status           string   `json:"status"`
	ExpiresAt        string   `json:"expires_at"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
}

type workOwnerLeaseAcquireRequest struct {
	IssueID          string   `json:"issue_id"`
	Role             string   `json:"role"`
	CanonicalBranch  string   `json:"canonical_branch"`
	ConversationID   string   `json:"conversation_id"`
	RuntimeLane      string   `json:"runtime_lane"`
	AllowedPaths     []string `json:"allowed_paths"`
	MigrationNumbers []int    `json:"migration_numbers"`
	TTLHours         int      `json:"ttl_hours"`
}

type workOwnerLeaseReleaseRequest struct {
	LeaseID string `json:"lease_id"`
	IssueID string `json:"issue_id"`
	Role    string `json:"role"`
}

func (h *Handler) AgentWorkOwnerLeaseAcquire(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	var req workOwnerLeaseAcquireRequest
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
	lease, err := h.acquireWorkOwnerLease(r.Context(), source.origin.workspaceID, source.origin.agentID, req)
	if err != nil {
		writeWorkOwnerLeaseError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, lease)
}

func (h *Handler) AgentWorkOwnerLeaseRelease(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	var req workOwnerLeaseReleaseRequest
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
	if err := h.releaseWorkOwnerLease(r.Context(), source.origin.workspaceID, source.origin.agentID, req); err != nil {
		writeWorkOwnerLeaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"released": true})
}

func (h *Handler) AgentWorkOwnerLeaseList(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	var body struct {
		IssueID string `json:"issue_id"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	_ = decoder.Decode(&body) // empty body lists workspace actives
	leases, err := h.listActiveWorkOwnerLeases(r.Context(), source.origin.workspaceID, strings.TrimSpace(body.IssueID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list work owner leases")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"leases": leases})
}

func (h *Handler) acquireWorkOwnerLease(ctx context.Context, workspaceID, agentID pgtype.UUID, req workOwnerLeaseAcquireRequest) (WorkOwnerLeaseResponse, error) {
	if err := h.expireStaleWorkOwnerLeases(ctx); err != nil {
		return WorkOwnerLeaseResponse{}, err
	}
	issueIDRaw := strings.TrimSpace(req.IssueID)
	if issueIDRaw == "" {
		return WorkOwnerLeaseResponse{}, errWorkOwnerLeaseBadRequest("issue_id is required")
	}
	issueID, err := util.ParseUUID(issueIDRaw)
	if err != nil {
		return WorkOwnerLeaseResponse{}, errWorkOwnerLeaseBadRequest("invalid issue_id")
	}
	if _, err := h.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
		ID: issueID, WorkspaceID: workspaceID,
	}); err != nil {
		return WorkOwnerLeaseResponse{}, errWorkOwnerLeaseBadRequest("issue not found in this workspace")
	}
	role := strings.TrimSpace(req.Role)
	if role == "" {
		role = workOwnerLeaseRoleExecutor
	}
	switch role {
	case workOwnerLeaseRoleExecutor, workOwnerLeaseRoleReviewer, workOwnerLeaseRoleCoordinator:
	default:
		return WorkOwnerLeaseResponse{}, errWorkOwnerLeaseBadRequest("invalid role")
	}
	ttl := workOwnerLeaseDefaultTTL
	if req.TTLHours > 0 {
		ttl = time.Duration(req.TTLHours) * time.Hour
		if ttl > workOwnerLeaseMaxTTL {
			ttl = workOwnerLeaseMaxTTL
		}
	}
	allowed, _ := json.Marshal(req.AllowedPaths)
	if req.AllowedPaths == nil {
		allowed = []byte("[]")
	}
	migrations, _ := json.Marshal(req.MigrationNumbers)
	if req.MigrationNumbers == nil {
		migrations = []byte("[]")
	}
	var branch, conversation, lane *string
	if v := strings.TrimSpace(req.CanonicalBranch); v != "" {
		branch = &v
	}
	if v := strings.TrimSpace(req.ConversationID); v != "" {
		conversation = &v
	}
	if v := strings.TrimSpace(req.RuntimeLane); v != "" {
		lane = &v
	}
	row := h.DB.QueryRow(ctx, `
		INSERT INTO work_owner_lease (
			workspace_id, issue_id, owner_agent_id, role, canonical_branch, conversation_id, runtime_lane,
			allowed_paths, migration_numbers, status, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9::jsonb, $10, $11)
		RETURNING id, workspace_id, issue_id, owner_agent_id, role, canonical_branch, conversation_id, runtime_lane,
		          allowed_paths, migration_numbers, handoff_to, status, expires_at, created_at, updated_at`,
		workspaceID, issueID, agentID, role, branch, conversation, lane, string(allowed), string(migrations),
		workOwnerLeaseStatusActive, time.Now().UTC().Add(ttl),
	)
	lease, err := scanWorkOwnerLease(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return WorkOwnerLeaseResponse{}, errWorkOwnerLeaseConflict("issue already has an active executor lease")
		}
		return WorkOwnerLeaseResponse{}, err
	}
	return lease, nil
}

func (h *Handler) releaseWorkOwnerLease(ctx context.Context, workspaceID, agentID pgtype.UUID, req workOwnerLeaseReleaseRequest) error {
	if err := h.expireStaleWorkOwnerLeases(ctx); err != nil {
		return err
	}
	leaseID := strings.TrimSpace(req.LeaseID)
	issueID := strings.TrimSpace(req.IssueID)
	role := strings.TrimSpace(req.Role)
	if leaseID == "" && issueID == "" {
		return errWorkOwnerLeaseBadRequest("lease_id or issue_id is required")
	}
	if role == "" {
		role = workOwnerLeaseRoleExecutor
	}
	tag, err := h.DB.Exec(ctx, `
		UPDATE work_owner_lease
		SET status = $1, updated_at = now()
		WHERE workspace_id = $2
		  AND owner_agent_id = $3
		  AND status = $4
		  AND ($5 = '' OR id::text = $5)
		  AND ($6 = '' OR issue_id::text = $6)
		  AND role = $7`,
		workOwnerLeaseStatusReleased, workspaceID, agentID, workOwnerLeaseStatusActive, leaseID, issueID, role,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errWorkOwnerLeaseNotFound("active work owner lease not found for this agent")
	}
	return nil
}

func (h *Handler) listActiveWorkOwnerLeases(ctx context.Context, workspaceID pgtype.UUID, issueID string) ([]WorkOwnerLeaseResponse, error) {
	if err := h.expireStaleWorkOwnerLeases(ctx); err != nil {
		return nil, err
	}
	rows, err := h.DB.Query(ctx, `
		SELECT id, workspace_id, issue_id, owner_agent_id, role, canonical_branch, conversation_id, runtime_lane,
		       allowed_paths, migration_numbers, handoff_to, status, expires_at, created_at, updated_at
		FROM work_owner_lease
		WHERE workspace_id = $1
		  AND status = $2
		  AND ($3 = '' OR issue_id::text = $3)
		ORDER BY created_at DESC`, workspaceID, workOwnerLeaseStatusActive, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]WorkOwnerLeaseResponse, 0)
	for rows.Next() {
		lease, err := scanWorkOwnerLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, lease)
	}
	return out, rows.Err()
}

func (h *Handler) expireStaleWorkOwnerLeases(ctx context.Context) error {
	_, err := h.DB.Exec(ctx, `
		UPDATE work_owner_lease
		SET status = 'expired', updated_at = now()
		WHERE status = 'active' AND expires_at <= now()`)
	return err
}

func scanWorkOwnerLease(row rowScanner) (WorkOwnerLeaseResponse, error) {
	var (
		id, workspaceID, issueID, ownerAgentID, handoffTo pgtype.UUID
		role, status                                      string
		canonical, conversation, lane                     pgtype.Text
		allowedRaw, migrationsRaw                         []byte
		expiresAt, createdAt, updatedAt                   pgtype.Timestamptz
	)
	if err := row.Scan(&id, &workspaceID, &issueID, &ownerAgentID, &role, &canonical, &conversation, &lane,
		&allowedRaw, &migrationsRaw, &handoffTo, &status, &expiresAt, &createdAt, &updatedAt); err != nil {
		return WorkOwnerLeaseResponse{}, err
	}
	allowed := []string{}
	_ = json.Unmarshal(allowedRaw, &allowed)
	migrations := []int{}
	_ = json.Unmarshal(migrationsRaw, &migrations)
	resp := WorkOwnerLeaseResponse{
		ID:               uuidToString(id),
		WorkspaceID:      uuidToString(workspaceID),
		IssueID:          uuidToString(issueID),
		OwnerAgentID:     uuidToString(ownerAgentID),
		Role:             role,
		AllowedPaths:     allowed,
		MigrationNumbers: migrations,
		Status:           status,
		ExpiresAt:        timestampToString(expiresAt),
		CreatedAt:        timestampToString(createdAt),
		UpdatedAt:        timestampToString(updatedAt),
	}
	if canonical.Valid {
		v := canonical.String
		resp.CanonicalBranch = &v
	}
	if conversation.Valid {
		v := conversation.String
		resp.ConversationID = &v
	}
	if lane.Valid {
		v := lane.String
		resp.RuntimeLane = &v
	}
	if handoffTo.Valid {
		v := uuidToString(handoffTo)
		resp.HandoffTo = &v
	}
	return resp, nil
}

type workOwnerLeaseError struct {
	status  int
	message string
}

func (e *workOwnerLeaseError) Error() string { return e.message }

func errWorkOwnerLeaseBadRequest(msg string) error {
	return &workOwnerLeaseError{status: http.StatusBadRequest, message: msg}
}
func errWorkOwnerLeaseConflict(msg string) error {
	return &workOwnerLeaseError{status: http.StatusConflict, message: msg}
}
func errWorkOwnerLeaseNotFound(msg string) error {
	return &workOwnerLeaseError{status: http.StatusNotFound, message: msg}
}

func writeWorkOwnerLeaseError(w http.ResponseWriter, err error) {
	var ole *workOwnerLeaseError
	if errors.As(err, &ole) {
		writeError(w, ole.status, ole.message)
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "work owner lease not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "work owner lease operation failed")
}
