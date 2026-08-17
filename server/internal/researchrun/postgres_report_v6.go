package researchrun

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type ResearchReportV6 interface {
	CreateV6ReportUpload(context.Context, V6AttemptAccess, ReportUploadDeclaration) (ReportUploadCapability, error)
	CompleteV6ReportUpload(context.Context, V6AttemptAccess, string, string) (VerifiedReportObject, error)
}

func (s *PostgresStore) CreateV6ReportUpload(ctx context.Context, access V6AttemptAccess, in ReportUploadDeclaration) (ReportUploadCapability, error) {
	if s.reportStorage == nil {
		return ReportUploadCapability{}, ErrV6DirectorUnavailable
	}
	if _, err := uuid.Parse(in.ClientRequestID); err != nil {
		return ReportUploadCapability{}, ErrInvalidContract
	}
	if _, ok := map[string]bool{"document": true, "script": true, "style": true, "image": true, "font": true, "data": true}[in.Role]; !ok {
		return ReportUploadCapability{}, ErrInvalidContract
	}
	clean := path.Clean(in.Path)
	if clean != in.Path || clean == "." || strings.HasPrefix(clean, "/") || strings.Contains(clean, "..") || in.ByteSize < 0 || in.ByteSize > v6ReportMaxBytes {
		return ReportUploadCapability{}, ErrInvalidContract
	}
	if !strings.HasPrefix(in.ContentHash, "sha256:") || len(in.ContentHash) != 71 {
		return ReportUploadCapability{}, ErrInvalidContract
	}
	if !validV6ReportMediaType(in.Role, in.MediaType) {
		return ReportUploadCapability{}, ErrInvalidContract
	}
	tx, err := s.beginResearchTx(ctx, txOpV6ReportUploadCreate, pgx.TxOptions{})
	if err != nil {
		return ReportUploadCapability{}, err
	}
	defer tx.Rollback(ctx)
	var reportID string
	var revision int
	err = tx.QueryRow(ctx, `SELECT w.target_id::text,r.revision FROM research_work_item_attempt a JOIN research_work_item w ON w.id=a.work_item_id
		JOIN research_report r ON r.id=w.target_id WHERE a.workspace_id=$1::uuid AND a.session_id=$2::uuid AND a.work_item_id=$3::uuid AND a.id=$4::uuid
		AND a.assigned_agent_id=$5::uuid AND ($6='' OR a.inbox_task_id=$6::uuid) AND a.status IN('dispatching','running') AND w.target_kind='report' AND w.expected_result_schema_id='report_package_submission'`, access.WorkspaceID, access.RunID, access.WorkItemID, access.AttemptID, access.AgentID, access.InboxTaskID).Scan(&reportID, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReportUploadCapability{}, ErrAttemptNotAssigned
	}
	if err != nil {
		return ReportUploadCapability{}, err
	}
	var existingID, key, status, existingPath, existingRole, existingMedia, existingHash string
	var existingWorkItemID, existingAttemptID, existingAgentID, existingReportID string
	var existingSize int64
	var expires time.Time
	err = tx.QueryRow(ctx, `SELECT id::text,storage_key,status,expires_at,path,role,media_type,content_hash,byte_size,work_item_id::text,work_item_attempt_id::text,agent_id::text,report_id::text FROM research_report_upload_session WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND client_request_id=$3::uuid`, access.WorkspaceID, access.RunID, in.ClientRequestID).Scan(&existingID, &key, &status, &expires, &existingPath, &existingRole, &existingMedia, &existingHash, &existingSize, &existingWorkItemID, &existingAttemptID, &existingAgentID, &existingReportID)
	if err == nil {
		if existingPath != clean || existingRole != in.Role || existingMedia != in.MediaType || existingHash != in.ContentHash || existingSize != in.ByteSize || existingWorkItemID != access.WorkItemID || existingAttemptID != access.AttemptID || existingAgentID != access.AgentID || existingReportID != reportID {
			return ReportUploadCapability{}, ErrResultConflict
		}
		if status == "rejected" || status == "expired" || (!expires.After(time.Now()) && status != "verified") {
			return ReportUploadCapability{}, ErrResultConflict
		}
		if status == "verified" {
			return ReportUploadCapability{ResourceID: existingID, Status: "verified", ExpiresAt: expires}, nil
		}
		_ = tx.Rollback(ctx)
		cap, callErr := s.reportStorage.CreateImmutableUpload(ctx, key, in, time.Until(expires))
		cap.ResourceID = existingID
		cap.Status = "pending"
		if cap.URL == "" {
			cap.URL = fmt.Sprintf("/api/agent/research/sessions/%s/work-items/%s/attempts/%s/report-uploads/%s/content", access.RunID, access.WorkItemID, access.AttemptID, existingID)
			cap.Method = "PUT"
		}
		return cap, callErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ReportUploadCapability{}, err
	}
	id := uuid.NewString()
	key = fmt.Sprintf("research-v6/%s/%s/%s/%s", access.WorkspaceID, access.RunID, access.AttemptID, id)
	expires = time.Now().UTC().Add(15 * time.Minute)
	token := make([]byte, 32)
	_, _ = rand.Read(token)
	sum := sha256.Sum256(token)
	_, err = tx.Exec(ctx, `INSERT INTO research_report_upload_session(id,workspace_id,session_id,work_item_id,work_item_attempt_id,agent_id,report_id,report_revision,client_request_id,path,role,media_type,byte_size,content_hash,storage_key,capability_hash,expires_at)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7::uuid,$8,$9::uuid,$10,$11,$12,$13,$14,$15,$16,$17)`, id, access.WorkspaceID, access.RunID, access.WorkItemID, access.AttemptID, access.AgentID, reportID, revision, in.ClientRequestID, clean, in.Role, in.MediaType, in.ByteSize, in.ContentHash, key, hex.EncodeToString(sum[:]), expires)
	if err != nil {
		return ReportUploadCapability{}, err
	}
	if err = s.commitResearchTx(ctx, txOpV6ReportUploadCreate, tx); err != nil {
		return ReportUploadCapability{}, err
	}
	cap, err := s.reportStorage.CreateImmutableUpload(ctx, key, in, 15*time.Minute)
	cap.ResourceID = id
	cap.Status = "pending"
	if cap.URL == "" {
		cap.URL = fmt.Sprintf("/api/agent/research/sessions/%s/work-items/%s/attempts/%s/report-uploads/%s/content", access.RunID, access.WorkItemID, access.AttemptID, id)
		cap.Method = "PUT"
	}
	return cap, err
}

func (s *PostgresStore) CompleteV6ReportUpload(ctx context.Context, access V6AttemptAccess, resourceID, requestID string) (VerifiedReportObject, error) {
	if _, err := uuid.Parse(requestID); err != nil {
		return VerifiedReportObject{}, ErrInvalidContract
	}
	if s.reportStorage == nil {
		return VerifiedReportObject{}, ErrV6DirectorUnavailable
	}
	var key string
	var size int64
	var media, hash, status string
	err := s.pool.QueryRow(ctx, `SELECT u.storage_key,u.byte_size,u.media_type,u.content_hash,u.status FROM research_report_upload_session u JOIN research_work_item_attempt a ON a.id=u.work_item_attempt_id WHERE u.workspace_id=$1::uuid AND u.session_id=$2::uuid AND u.id=$3::uuid AND u.work_item_id=$4::uuid AND u.work_item_attempt_id=$5::uuid AND u.agent_id=$6::uuid AND ($7='' OR a.inbox_task_id=$7::uuid) AND (u.expires_at>now() OR u.status='verified')`, access.WorkspaceID, access.RunID, resourceID, access.WorkItemID, access.AttemptID, access.AgentID, access.InboxTaskID).Scan(&key, &size, &media, &hash, &status)
	if err != nil {
		return VerifiedReportObject{}, ErrAttemptNotAssigned
	}
	if status != "pending" && status != "verified" {
		return VerifiedReportObject{}, ErrResultConflict
	}
	object, err := s.reportStorage.VerifyImmutableUpload(ctx, key)
	if err != nil {
		return VerifiedReportObject{}, err
	}
	if object.ByteSize != size || object.MediaType != media || object.ContentHash != hash {
		return VerifiedReportObject{}, ErrResultConflict
	}
	tx, err := s.beginResearchTx(ctx, txOpV6ReportUploadComplete, pgx.TxOptions{})
	if err != nil {
		return VerifiedReportObject{}, err
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `UPDATE research_report_upload_session SET status='verified',storage_generation=$2,completion_request_id=COALESCE(completion_request_id,$3::uuid),completed_at=COALESCE(completed_at,now()) WHERE id=$1::uuid AND status IN('pending','verified') AND (completion_request_id IS NULL OR completion_request_id=$3::uuid)`, resourceID, object.Generation, requestID)
	if err != nil {
		return VerifiedReportObject{}, err
	}
	if command.RowsAffected() != 1 {
		return VerifiedReportObject{}, ErrResultConflict
	}
	_, err = tx.Exec(ctx, `INSERT INTO research_report_resource(workspace_id,session_id,report_id,report_revision,resource_id,path,role,media_type,byte_size,content_hash,storage_key,storage_generation,upload_status)
		SELECT workspace_id,session_id,report_id,report_revision,id,path,role,media_type,byte_size,content_hash,storage_key,storage_generation,'verified' FROM research_report_upload_session WHERE id=$1::uuid ON CONFLICT(report_id,report_revision,resource_id) DO NOTHING`, resourceID)
	if err != nil {
		return VerifiedReportObject{}, err
	}
	if err = s.commitResearchTx(ctx, txOpV6ReportUploadComplete, tx); err != nil {
		return VerifiedReportObject{}, err
	}
	return object, nil
}
