package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"

	"github.com/jackc/pgx/v5"
)

type v6ReportSubmission struct {
	WorkspaceID        string             `json:"workspace_id"`
	RunID              string             `json:"run_id"`
	WorkItemID         string             `json:"work_item_id"`
	AttemptID          string             `json:"attempt_id"`
	AgentID            string             `json:"agent_id"`
	ManifestID         string             `json:"manifest_id"`
	ManifestHash       string             `json:"manifest_hash"`
	GoalVersion        int                `json:"goal_version"`
	InputSnapshotHash  string             `json:"input_snapshot_hash"`
	Title              string             `json:"title"`
	Summary            string             `json:"summary"`
	DocumentResourceID string             `json:"document_resource_id"`
	Resources          []V6ReportResource `json:"resources"`
	PlainText          string             `json:"plain_text"`
	ScriptHashes       []string           `json:"script_hashes"`
	StyleHashes        []string           `json:"style_hashes"`
	PackageHash        string             `json:"package_hash"`
	InputNodes         []V6NodeRef        `json:"input_nodes"`
	Outline            json.RawMessage    `json:"outline"`
	Citations          json.RawMessage    `json:"citations"`
}

func (s *PostgresStore) applyReportPackageV6(ctx context.Context, submissionID string, decoded DecodedV6Contract) (string, error) {
	if s.reportStorage == nil {
		return "", ErrV6DirectorUnavailable
	}
	var in v6ReportSubmission
	if json.Unmarshal(decoded.Envelope, &in) != nil {
		return "", ErrInvalidContract
	}
	var reportID string
	var revision, goalVersion int
	var snapshotHash, reportMaturity string
	err := s.pool.QueryRow(ctx, `SELECT w.target_id::text,r.revision,w.goal_version,COALESCE(r.input_snapshot_hash,''),r.maturity FROM research_work_item_attempt a JOIN research_work_item w ON w.id=a.work_item_id JOIN research_report r ON r.id=w.target_id
		WHERE a.workspace_id=$1::uuid AND a.session_id=$2::uuid AND a.id=$3::uuid AND a.work_item_id=$4::uuid AND a.assigned_agent_id=$5::uuid AND a.manifest_id=$6::uuid AND a.manifest_hash=$7 AND a.status='running' AND w.status='running'`, in.WorkspaceID, in.RunID, in.AttemptID, in.WorkItemID, in.AgentID, in.ManifestID, in.ManifestHash).Scan(&reportID, &revision, &goalVersion, &snapshotHash, &reportMaturity)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrAttemptNotAssigned
	}
	if err != nil {
		return "", err
	}
	if goalVersion != in.GoalVersion || snapshotHash != in.InputSnapshotHash {
		return "", ErrWorkItemChanged
	}
	rows, err := s.pool.Query(ctx, `SELECT CASE WHEN iv.id IS NULL THEN 'result_s' ELSE 'insight' END,p.id::text,i.node_artifact_version_id::text,COALESCE(iv.tier,'S'),i.content_hash FROM research_report_input i JOIN research_artifact_version v ON v.id=i.node_artifact_version_id JOIN research_artifact_passport p ON p.id=v.artifact_id LEFT JOIN research_insight_version iv ON iv.artifact_version_id=v.id WHERE i.report_id=$1::uuid AND i.report_revision=$2 ORDER BY i.node_artifact_version_id`, reportID, revision)
	if err != nil {
		return "", err
	}
	expected := []string{}
	for rows.Next() {
		var kind, id, versionID, tier, hash string
		if err = rows.Scan(&kind, &id, &versionID, &tier, &hash); err != nil {
			rows.Close()
			return "", err
		}
		expected = append(expected, kind+":"+id+":"+versionID+":"+tier+":"+hash)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return "", err
	}
	rows.Close()
	actual := make([]string, len(in.InputNodes))
	for i, node := range in.InputNodes {
		actual[i] = node.Kind + ":" + node.ID + ":" + node.VersionID + ":" + string(node.Tier) + ":" + node.ContentHash
	}
	sort.Strings(actual)
	if !slices.Equal(expected, actual) {
		return "", ErrWorkItemChanged
	}
	resources := make([]V6ReportResource, 0, len(in.Resources))
	var verifiedResourceCount int
	if err = s.pool.QueryRow(ctx, `SELECT count(*)::int FROM research_report_resource WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND report_id=$3::uuid AND report_revision=$4 AND upload_status='verified'`, in.WorkspaceID, in.RunID, reportID, revision).Scan(&verifiedResourceCount); err != nil {
		return "", err
	}
	if verifiedResourceCount != len(in.Resources) {
		return "", fmt.Errorf("%w: report manifest must cover every verified resource", ErrInvalidContract)
	}
	seen := map[string]struct{}{}
	for _, decl := range in.Resources {
		if _, ok := seen[decl.ResourceID]; ok {
			return "", ErrInvalidContract
		}
		seen[decl.ResourceID] = struct{}{}
		var r V6ReportResource
		var key, generation string
		err = s.pool.QueryRow(ctx, `SELECT resource_id::text,path,role,media_type,byte_size,content_hash,storage_key,storage_generation FROM research_report_resource WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND report_id=$3::uuid AND report_revision=$4 AND resource_id=$5::uuid AND upload_status='verified'`, in.WorkspaceID, in.RunID, reportID, revision, decl.ResourceID).Scan(&r.ResourceID, &r.Path, &r.Role, &r.MediaType, &r.ByteSize, &r.ContentHash, &key, &generation)
		if err != nil {
			return "", ErrInvalidContract
		}
		if r.Path != decl.Path || r.Role != decl.Role || r.MediaType != decl.MediaType || r.ByteSize != decl.ByteSize || r.ContentHash != decl.ContentHash {
			return "", ErrResultConflict
		}
		reader, readErr := s.reportStorage.ReadVerified(ctx, key, generation)
		if readErr != nil {
			return "", readErr
		}
		r.Bytes, readErr = io.ReadAll(io.LimitReader(reader, v6ReportMaxBytes+1))
		_ = reader.Close()
		if readErr != nil {
			return "", readErr
		}
		if int64(len(r.Bytes)) != r.ByteSize || len(r.Bytes) > v6ReportMaxBytes {
			return "", ErrResultConflict
		}
		resources = append(resources, r)
	}
	compiled, err := CompileV6ReportPackageWithMetadata(resources, in.DocumentResourceID, in.PlainText, V6ReportPackageMetadata{GoalVersion: in.GoalVersion, InputSnapshotHash: in.InputSnapshotHash, InputNodes: in.InputNodes, Title: in.Title, Summary: in.Summary, Outline: in.Outline, Citations: in.Citations})
	if err != nil {
		return "", err
	}
	sort.Strings(in.ScriptHashes)
	sort.Strings(in.StyleHashes)
	if compiled.PackageHash != in.PackageHash || !slices.Equal(compiled.ScriptHashes, in.ScriptHashes) || !slices.Equal(compiled.StyleHashes, in.StyleHashes) {
		return "", ErrResultConflict
	}
	designDossier, err := ExtractV6ReportDesignDossier(compiled.HTML)
	if err != nil {
		return "", err
	}
	designFields, err := parseV6ReportDesignDossier(designDossier)
	if err != nil {
		return "", err
	}
	if designFields["maturity"] != reportMaturity {
		return "", fmt.Errorf("%w: report design dossier maturity does not match frozen report state", ErrInvalidContract)
	}
	key := "research-v6-reports/" + in.WorkspaceID + "/" + in.RunID + "/" + reportID + "/" + compiled.PackageHash + ".html"
	stored, err := s.reportStorage.PutImmutable(ctx, key, compiled.HTML, "text/html;charset=utf-8")
	if err != nil {
		return "", err
	}
	tx, err := s.beginResearchTx(ctx, txOpV6ReportPackageAccept, pgx.TxOptions{})
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, in.RunID, in.WorkspaceID); err != nil {
		return "", err
	}
	command, err := tx.Exec(ctx, `UPDATE research_report SET title=$4,summary=$5,plain_text=$6,package_hash=$7,document_content_hash=$8,document_storage_key=$9,document_storage_generation=$10,document_byte_size=$11,csp_script_hashes=$12::jsonb,csp_style_hashes=$13::jsonb,outline=$14::jsonb,citations=$15::jsonb,design_dossier=$16,status='draft',updated_at=now()
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid AND revision=$17 AND status<>'published'`, in.WorkspaceID, in.RunID, reportID, in.Title, in.Summary, in.PlainText, compiled.PackageHash, compiled.DocumentHash, stored.Key, stored.Generation, len(compiled.HTML), mustJSONRaw(compiled.CSPScriptHashes), mustJSONRaw(compiled.CSPStyleHashes), in.Outline, in.Citations, designDossier, revision)
	if err != nil {
		return "", err
	}
	if command.RowsAffected() != 1 {
		return "", ErrWorkItemChanged
	}
	goalVersion32 := int32(goalVersion)
	if err = registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{WorkspaceID: in.WorkspaceID, SessionID: in.RunID, EntityID: reportID, Kind: ArtifactKindReportRevision, ProvenanceCompleteness: ArtifactProvenanceComplete, GoalVersion: &goalVersion32, SchemaName: string(ArtifactKindReportRevision), SchemaVersion: OrchestratorVersionV6, AccessLevel: ArtifactAccessRaw, HashOrigin: ArtifactHashOriginProduction, ContentHash: compiled.PackageHash, ProducedByWorkItemID: in.WorkItemID, ProducedByWorkAttemptID: in.AttemptID, ProducedByAgentID: in.AgentID}); err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `UPDATE research_work_item_attempt SET status='succeeded',result_kind='report',result_entity_id=$2::uuid,result_hash=$3,result_submitted_at=now(),completed_at=now(),updated_at=now() WHERE id=$1::uuid AND status='running'`, in.AttemptID, reportID, compiled.PackageHash)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `UPDATE research_work_item SET status='succeeded',completed_at=now(),lease_token=NULL,lease_expires_at=NULL,updated_at=now() WHERE id=$1::uuid`, in.WorkItemID)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `UPDATE research_v6_work_submission SET status='accepted',outcome=jsonb_build_object('report_id',$2::text,'revision',$3::int,'package_hash',$4::text),updated_at=now() WHERE id=$1::uuid`, submissionID, reportID, revision, compiled.PackageHash)
	if err != nil {
		return "", err
	}
	if _, err = appendEvent(ctx, tx, in.WorkspaceID, in.RunID, "v6_report_draft_accepted", "v6-report-draft:"+reportID+":"+compiled.PackageHash, "agent", in.AgentID, map[string]any{"report_id": reportID, "revision": revision, "package_hash": compiled.PackageHash}); err != nil {
		return "", err
	}
	if err = s.commitResearchTx(ctx, txOpV6ReportPackageAccept, tx); err != nil {
		return "", err
	}
	return reportID, nil
}

func mustJSONRaw(v any) json.RawMessage { raw, _ := json.Marshal(v); return raw }

func (s *PostgresStore) ApplyReceivedV6ReportPackages(ctx context.Context, limit int) (int, error) {
	applied := 0
	for applied < limit {
		tx, err := s.beginResearchTx(ctx, txOpV6ReportPackageClaim, pgx.TxOptions{})
		if err != nil {
			return applied, err
		}
		var id, hash string
		var envelope json.RawMessage
		err = tx.QueryRow(ctx, `SELECT id::text,content_hash,envelope FROM research_v6_work_submission WHERE contract_kind='report_package_submission' AND (status='received' OR(status='processing' AND updated_at<now()-interval '1 minute')) ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id, &hash, &envelope)
		if errors.Is(err, pgx.ErrNoRows) {
			_ = tx.Rollback(ctx)
			return applied, nil
		}
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE research_v6_work_submission SET status='processing',updated_at=now() WHERE id=$1::uuid`, id)
		}
		if err == nil {
			err = s.commitResearchTx(ctx, txOpV6ReportPackageClaim, tx)
		} else {
			_ = tx.Rollback(ctx)
		}
		if err != nil {
			return applied, err
		}
		decoded := DecodedV6Contract{Kind: V6ContractReportPackageSubmission, Envelope: envelope, Canonical: envelope, ContentHash: hash}
		_, err = s.applyReportPackageV6(ctx, id, decoded)
		if err != nil {
			if !isTerminalV6SubmissionError(err) && !isPermanentV6ProcessingError(ctx, err) {
				return applied, err
			}
			reason := err.Error()
			if !isTerminalV6SubmissionError(err) {
				reason = v6SubmissionApplyDiagnostic(err)
			}
			if rejectErr := s.rejectV6ReportPackage(context.WithoutCancel(ctx), id, reason); rejectErr != nil {
				return applied, rejectErr
			}
		}
		applied++
	}
	return applied, nil
}

func (s *PostgresStore) rejectV6ReportPackage(ctx context.Context, submissionID, reason string) error {
	tx, err := s.beginResearchTx(ctx, txOpV6ReportPackageAccept, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE research_v6_work_submission SET status='rejected',outcome=jsonb_build_object('error',$2::text),updated_at=now() WHERE id=$1::uuid`, submissionID, reason); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_work_item_attempt a SET status='failed',failure_class='contract_rejected',diagnostics=$2,completed_at=now(),updated_at=now() FROM research_v6_work_submission sub WHERE sub.id=$1::uuid AND a.id=sub.attempt_id AND a.status IN ('dispatching','running')`, submissionID, reason); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_work_item w SET status='failed',terminal_reason_code='contract_rejected',terminal_reason_detail=$2,lease_token=NULL,lease_expires_at=NULL,updated_at=now() FROM research_v6_work_submission sub WHERE sub.id=$1::uuid AND w.id=sub.work_item_id AND w.status IN ('dispatching','running')`, submissionID, reason); err != nil {
		return err
	}
	return s.commitResearchTx(ctx, txOpV6ReportPackageAccept, tx)
}
