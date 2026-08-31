package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) LoadV6WorkArtifact(ctx context.Context, access V6AttemptAccess, artifactVersionID string) (V6WorkArtifact, error) {
	artifact := V6WorkArtifact{ArtifactVersionID: artifactVersionID}
	err := s.pool.QueryRow(ctx, `
		SELECT frozen->>'kind',frozen->>'representation',frozen->>'representation_hash'
		FROM research_work_item_attempt a
		JOIN research_work_item w ON (w.workspace_id,w.session_id,w.id)=(a.workspace_id,a.session_id,a.work_item_id)
		JOIN research_team_membership m ON (m.workspace_id,m.session_id,m.id)=(a.workspace_id,a.session_id,a.membership_id)
		CROSS JOIN LATERAL jsonb_array_elements(COALESCE(a.manifest->'artifacts','[]'::jsonb)) frozen
		WHERE a.workspace_id=$1::uuid AND a.session_id=$2::uuid AND a.work_item_id=$3::uuid AND a.id=$4::uuid
		  AND a.assigned_agent_id=$5::uuid AND m.agent_id=$5::uuid AND m.state NOT IN ('archived','failed')
		  AND a.status IN ('dispatching','running') AND w.status IN ('dispatching','running','awaiting_input')
		  AND ($6='' OR a.inbox_task_id=$6::uuid) AND frozen->>'artifact_version_id'=$7
	`, access.WorkspaceID, access.RunID, access.WorkItemID, access.AttemptID, access.AgentID, access.InboxTaskID, artifactVersionID).
		Scan(&artifact.Kind, &artifact.Representation, &artifact.RepresentationHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return V6WorkArtifact{}, ErrAttemptNotAssigned
	}
	if err != nil {
		return V6WorkArtifact{}, err
	}
	if artifact.Representation != "full" {
		return V6WorkArtifact{}, fmt.Errorf("%w: unsupported work artifact representation", ErrInvalidContract)
	}

	switch artifact.Kind {
	case string(ArtifactKindResultArtifact):
		var storedHash, taskSchemaID string
		var taskSchema json.RawMessage
		err = s.pool.QueryRow(ctx, `
			SELECT result.result,version.content_hash,producer_work.payload_schema_id,
			       COALESCE(producer.manifest->'task_specific_schema','null'::jsonb)
			FROM research_artifact_version version
			JOIN research_artifact_passport passport
			  ON (passport.workspace_id,passport.session_id,passport.id)=(version.workspace_id,version.session_id,version.artifact_id)
			JOIN research_result_artifact result
			  ON (result.workspace_id,result.session_id,result.id)=(version.workspace_id,version.session_id,version.artifact_id)
			JOIN research_work_item_attempt producer
			  ON (producer.workspace_id,producer.session_id,producer.id)=(result.workspace_id,result.session_id,result.work_item_attempt_id)
			JOIN research_work_item producer_work
			  ON (producer_work.workspace_id,producer_work.session_id,producer_work.id)=(producer.workspace_id,producer.session_id,producer.work_item_id)
			WHERE version.workspace_id=$1::uuid AND version.session_id=$2::uuid AND version.id=$3::uuid
			  AND passport.entity_kind='result_artifact' AND passport.lifecycle_status='accepted'
		`, access.WorkspaceID, access.RunID, artifactVersionID).Scan(&artifact.Content, &storedHash, &taskSchemaID, &taskSchema)
		if err == nil {
			var decoded DecodedV6Contract
			decoded, err = DecodeV6Contract(artifact.Content, V6ContractAtomicResultSubmission, boundV6SecondStage{
				schemaID: taskSchemaID,
				schema:   taskSchema,
			})
			if err == nil && (decoded.ContentHash != storedHash || storedHash != artifact.RepresentationHash) {
				err = fmt.Errorf("%w: frozen result representation hash mismatch", ErrInvalidContract)
			}
		}
	case string(ArtifactKindInsight):
		var layers V6ContentLayers
		var storedHash string
		err = s.pool.QueryRow(ctx, `
			SELECT insight.catalog_summary,insight.brief_summary,insight.objective,insight.conclusion,insight.content,
			       insight.scope,insight.uncertainties,insight.conflicts,insight.open_questions,version.content_hash
			FROM research_artifact_version version
			JOIN research_artifact_passport passport
			  ON (passport.workspace_id,passport.session_id,passport.id)=(version.workspace_id,version.session_id,version.artifact_id)
			JOIN research_insight_version insight
			  ON (insight.workspace_id,insight.session_id,insight.artifact_version_id)=(version.workspace_id,version.session_id,version.id)
			WHERE version.workspace_id=$1::uuid AND version.session_id=$2::uuid AND version.id=$3::uuid
			  AND passport.entity_kind='insight' AND passport.lifecycle_status='accepted'
		`, access.WorkspaceID, access.RunID, artifactVersionID).Scan(
			&layers.CatalogSummary, &layers.BriefSummary, &layers.Objective, &layers.Conclusion, &layers.Content,
			&layers.Scope, &layers.Uncertainties, &layers.Conflicts, &layers.OpenQuestions, &storedHash)
		if err == nil {
			var canonical []byte
			canonical, err = marshalV6CanonicalJSON(layers)
			if err == nil && (ArtifactContentHashFromCanonicalJSON(canonical) != storedHash || storedHash != artifact.RepresentationHash) {
				err = fmt.Errorf("%w: frozen insight representation hash mismatch", ErrInvalidContract)
			}
			if err == nil {
				artifact.Content, err = json.Marshal(layers)
			}
		}
	default:
		return V6WorkArtifact{}, fmt.Errorf("%w: unsupported work artifact kind", ErrInvalidContract)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return V6WorkArtifact{}, ErrRunNotFound
	}
	if err != nil {
		return V6WorkArtifact{}, err
	}
	return artifact, nil
}
