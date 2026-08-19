package researchrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) ReviewV6Report(ctx context.Context, in ReviewV6ReportInput) (V6ReportReview, error) {
	if s.reportStorage == nil {
		return V6ReportReview{}, ErrV6DirectorUnavailable
	}
	switch in.Decision {
	case "published", "needs_research", "needs_revision", "technical_failure":
	default:
		return V6ReportReview{}, ErrInvalidContract
	}
	if strings.TrimSpace(in.Reason) == "" {
		return V6ReportReview{}, ErrInvalidContract
	}
	var replay V6ReportReview
	var replayReason string
	var replayState int64
	err := s.pool.QueryRow(ctx, `SELECT id::text,decision,report_id::text,report_revision,COALESCE(render_artifact_version_id::text,''),reason,input_state_version FROM research_report_review WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND report_id=$3::uuid AND director_cycle_id=$4::uuid`, in.WorkspaceID, in.RunID, in.ReportID, in.DirectorCycleID).Scan(&replay.ID, &replay.Decision, &replay.ReportID, &replay.Revision, &replay.RenderArtifactVersionID, &replayReason, &replayState)
	if err == nil {
		if replay.Decision != in.Decision || replayReason != in.Reason || replay.Revision != in.ExpectedRevision || replayState != in.ExpectedStateVersion {
			return V6ReportReview{}, ErrResultConflict
		}
		return replay, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return V6ReportReview{}, err
	}
	var revision, generation int
	var goalVersion, planVersion int32
	var state int64
	var key, storageGeneration, documentHash string
	var scriptsRaw, stylesRaw json.RawMessage
	err = s.pool.QueryRow(ctx, `SELECT r.revision,s.state_version,r.document_storage_key,r.document_storage_generation,r.document_content_hash,r.csp_script_hashes,r.csp_style_hashes,a.generation,s.goal_version,s.plan_version FROM research_report r JOIN research_session s ON s.id=r.session_id JOIN research_director_assignment a ON a.id=$4::uuid JOIN research_director_cycle c ON c.id=$5::uuid AND c.director_assignment_id=a.id WHERE r.workspace_id=$1::uuid AND r.session_id=$2::uuid AND r.id=$3::uuid AND r.status='draft' AND a.session_id=r.session_id AND a.status='active'`, in.WorkspaceID, in.RunID, in.ReportID, in.DirectorAssignmentID, in.DirectorCycleID).Scan(&revision, &state, &key, &storageGeneration, &documentHash, &scriptsRaw, &stylesRaw, &generation, &goalVersion, &planVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return V6ReportReview{}, ErrV6DirectorUnavailable
	}
	if err != nil {
		return V6ReportReview{}, err
	}
	if revision != in.ExpectedRevision || state != in.ExpectedStateVersion {
		return V6ReportReview{}, ErrWorkItemChanged
	}
	reader, err := s.reportStorage.ReadVerified(ctx, key, storageGeneration)
	if err != nil {
		return V6ReportReview{}, err
	}
	html, err := io.ReadAll(io.LimitReader(reader, V6ReportMaxCompiledBytes+1))
	_ = reader.Close()
	if err != nil {
		return V6ReportReview{}, err
	}
	if len(html) > V6ReportMaxCompiledBytes {
		return V6ReportReview{}, ErrResultConflict
	}
	if ArtifactContentHashFromCanonicalJSON(html) != documentHash {
		return V6ReportReview{}, ErrResultConflict
	}
	var scripts, styles []string
	if json.Unmarshal(scriptsRaw, &scripts) != nil || json.Unmarshal(stylesRaw, &styles) != nil {
		return V6ReportReview{}, ErrInvalidContract
	}
	if err = ValidateV6ReportCSPHashes(scripts, styles); err != nil {
		return V6ReportReview{}, err
	}
	mode := ResolveV6ReportReviewMode(s.reportRenderer != nil, s.reportFrameAncestors)
	var renderHash string
	var diagnostics map[string]any
	if mode == V6ReportReviewModeIsolated {
		ancestors, ancestorErr := NormalizeV6ReportFrameAncestors(s.reportFrameAncestors)
		if ancestorErr != nil || len(ancestors) == 0 || s.reportRenderer == nil {
			return V6ReportReview{}, ErrV6DirectorUnavailable
		}
		csp := V6ReportDocumentCSP(scripts, styles, ancestors)
		render, renderErr := s.reportRenderer.RenderReport(ctx, ReportRenderInput{HTML: html, CSP: csp})
		if renderErr != nil {
			return V6ReportReview{}, renderErr
		}
		if render.EffectiveCSP != csp || len(render.Screenshot) == 0 || len(render.Screenshot) > 10<<20 || !bytes.HasPrefix(render.Screenshot, []byte("\x89PNG\r\n\x1a\n")) || len(render.Diagnostics) > 256<<10 || !json.Valid(render.Diagnostics) || !bytes.HasPrefix(bytes.TrimSpace(render.Diagnostics), []byte("{")) {
			return V6ReportReview{}, fmt.Errorf("%w: renderer did not enforce report CSP", ErrInvalidContract)
		}
		renderHash = ArtifactContentHashFromCanonicalJSON(render.Screenshot)
		stored, storeErr := s.reportStorage.PutImmutable(ctx, "research-v6-report-renders/"+in.ReportID+"/"+renderHash+".png", render.Screenshot, "image/png")
		if storeErr != nil {
			return V6ReportReview{}, storeErr
		}
		diagnostics = map[string]any{"renderer": json.RawMessage(render.Diagnostics), "screenshot_storage_key": stored.Key, "screenshot_storage_generation": stored.Generation, "effective_csp": render.EffectiveCSP}
	} else {
		renderHash = documentHash
		diagnostics = map[string]any{"renderer": "skipped", "reason": "report_renderer_unavailable"}
	}
	tx, err := s.beginResearchTx(ctx, txOpV6ReportReview, pgx.TxOptions{})
	if err != nil {
		return V6ReportReview{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, in.RunID, in.WorkspaceID); err != nil {
		return V6ReportReview{}, err
	}
	command, err := tx.Exec(ctx, `UPDATE research_session SET state_version=state_version+1,updated_at=now() WHERE workspace_id=$1::uuid AND id=$2::uuid AND state_version=$3 AND status='running'`, in.WorkspaceID, in.RunID, in.ExpectedStateVersion)
	if err != nil {
		return V6ReportReview{}, err
	}
	if command.RowsAffected() != 1 {
		return V6ReportReview{}, ErrWorkItemChanged
	}
	review := V6ReportReview{ID: uuid.NewString(), Decision: in.Decision, ReportID: in.ReportID, Revision: revision}
	createdAt := time.Now().UTC()
	_, err = tx.Exec(ctx, `INSERT INTO research_report_review(id,workspace_id,session_id,report_id,report_revision,director_assignment_id,director_generation,director_cycle_id,input_state_version,decision,reason,render_diagnostics,created_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::uuid,$7,$8::uuid,$9,$10,$11,$12::jsonb,$13)`, review.ID, in.WorkspaceID, in.RunID, in.ReportID, revision, in.DirectorAssignmentID, generation, in.DirectorCycleID, state, in.Decision, in.Reason, mustJSONRaw(diagnostics), createdAt)
	if err != nil {
		return V6ReportReview{}, err
	}
	if err = registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{WorkspaceID: in.WorkspaceID, SessionID: in.RunID, EntityID: review.ID, Kind: ArtifactEntityKind("v6_report_review"), SourceCreatedAt: &createdAt, ProvenanceCompleteness: ArtifactProvenanceComplete, GoalVersion: &goalVersion, PlanVersion: &planVersion, SchemaName: "v6_report_review", SchemaVersion: OrchestratorVersionV6, AccessLevel: ArtifactAccessRaw, HashOrigin: ArtifactHashOriginProduction, ContentHash: renderHash}); err != nil {
		return V6ReportReview{}, err
	}
	if err = tx.QueryRow(ctx, `SELECT v.id::text FROM research_artifact_passport p JOIN research_artifact_version v ON v.artifact_id=p.id AND v.version=p.current_version WHERE p.workspace_id=$1::uuid AND p.session_id=$2::uuid AND p.id=$3::uuid`, in.WorkspaceID, in.RunID, review.ID).Scan(&review.RenderArtifactVersionID); err != nil {
		return V6ReportReview{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_report_review SET render_artifact_version_id=$2::uuid WHERE id=$1::uuid`, review.ID, review.RenderArtifactVersionID); err != nil {
		return V6ReportReview{}, err
	}
	command, err = tx.Exec(ctx, `UPDATE research_report SET status=$4,reviewed_by_director_assignment_id=$5::uuid,published_at=CASE WHEN $4='published' THEN now() ELSE published_at END,updated_at=now() WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid AND revision=$6 AND status='draft'`, in.WorkspaceID, in.RunID, in.ReportID, in.Decision, in.DirectorAssignmentID, revision)
	if err != nil {
		return V6ReportReview{}, err
	}
	if command.RowsAffected() != 1 {
		return V6ReportReview{}, ErrWorkItemChanged
	}
	if _, err = appendEvent(ctx, tx, in.WorkspaceID, in.RunID, "v6_report_reviewed", "v6-report-review:"+review.ID, "system", "", map[string]any{"report_id": in.ReportID, "revision": revision, "decision": in.Decision, "director_assignment_id": in.DirectorAssignmentID}); err != nil {
		return V6ReportReview{}, err
	}
	if err = s.commitResearchTx(ctx, txOpV6ReportReview, tx); err != nil {
		return V6ReportReview{}, err
	}
	return review, nil
}
