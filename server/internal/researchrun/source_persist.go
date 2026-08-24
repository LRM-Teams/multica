package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type PersistSourceIngestionInput struct {
	Intent SourceIngestionIntent
	Title  string
	Text   string
}

type PersistSourceIngestionResult struct {
	SourceSnapshotID string
	Fingerprint      string
	Replayed         bool
}

func (s *PostgresStore) PersistSourceIngestion(ctx context.Context, in PersistSourceIngestionInput) (PersistSourceIngestionResult, error) {
	if in.Intent.SourceSnapshotID == "" {
		in.Intent.SourceSnapshotID = uuid.NewString()
	}
	validated, err := ValidateSourceIngestionIntent(in.Intent)
	if err != nil {
		return PersistSourceIngestionResult{}, err
	}
	if validated.Intent.Kind == SourceIngestionScreenedRetrieval {
		return PersistSourceIngestionResult{}, fmt.Errorf("%w: screened retrieval must use FetchAndIngestScreenedSource", ErrInvalidContract)
	}
	tx, err := s.beginResearchTx(ctx, txOpSearchLineageRecord, pgx.TxOptions{})
	if err != nil {
		return PersistSourceIngestionResult{}, err
	}
	defer tx.Rollback(ctx)
	result, err := persistSourceIngestionTx(ctx, tx, validated, in.Title, in.Text)
	if err != nil {
		return PersistSourceIngestionResult{}, err
	}
	if err = s.commitResearchTx(ctx, txOpSearchLineageRecord, tx); err != nil {
		return PersistSourceIngestionResult{}, err
	}
	return result, nil
}

func persistSourceIngestionTx(ctx context.Context, tx pgx.Tx, validated ValidatedSourceIngestion, title, text string) (PersistSourceIngestionResult, error) {
	intent := validated.Intent
	contentHash := strings.TrimPrefix(intent.ContentHash, "sha256:")
	locator := intent.Locator
	canonicalURL := intent.CanonicalURL
	if canonicalURL == "" {
		canonicalURL = locator
	}
	var existingID, existingKind string
	err := tx.QueryRow(ctx, `SELECT id::text, ingestion_kind FROM research_source_snapshot
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND canonical_url=$3 AND content_hash=$4`,
		intent.WorkspaceID, intent.SessionID, canonicalURL, contentHash).Scan(&existingID, &existingKind)
	if err == nil {
		if existingKind != string(intent.Kind) {
			return PersistSourceIngestionResult{}, fmt.Errorf("%w: source already exists with a different ingestion kind", ErrResultConflict)
		}
		return PersistSourceIngestionResult{SourceSnapshotID: existingID, Fingerprint: validated.Fingerprint, Replayed: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return PersistSourceIngestionResult{}, err
	}
	metadata, err := json.Marshal(map[string]any{
		"ingestion_policy":      SourceIngestionPolicyVersionV1,
		"ingestion_fingerprint": validated.Fingerprint,
		"locator":               locator,
		"reason":                intent.Reason,
	})
	if err != nil {
		return PersistSourceIngestionResult{}, err
	}
	independence := truncateBytes(string(intent.Kind), 160)
	if _, err = tx.Exec(ctx, `INSERT INTO research_source_snapshot
		(id,workspace_id,session_id,produced_by_task_id,canonical_url,title,publisher,source_class,evidence_traits,independence_key,retrieved_at,
		 snapshot_text,content_hash,metadata,verification_status,ingestion_kind,screening_decision_id,
		 origin_user_id,origin_attachment_id,origin_workspace_artifact_id,origin_adapter,origin_dataset_id)
		VALUES($1::uuid,$2::uuid,$3::uuid,NULLIF($4,'')::uuid,$5,$6,'','other','{}'::text[],$7,$8,$9,$10,$11::jsonb,'pending',$12,NULL,
		       NULLIF($13,'')::uuid,NULLIF($14,'')::uuid,NULLIF($15,'')::uuid,$16,$17)`,
		intent.SourceSnapshotID, intent.WorkspaceID, intent.SessionID, intent.TaskID, canonicalURL,
		truncateBytes(title, 4096), independence, intent.CapturedAt, text, contentHash, metadata, string(intent.Kind),
		intent.UserID, intent.AttachmentID, intent.WorkspaceArtifactID, intent.Adapter, intent.DatasetID); err != nil {
		return PersistSourceIngestionResult{}, err
	}
	if _, err = appendEvent(ctx, tx, intent.WorkspaceID, intent.SessionID, "source_ingested", "source-ingested:"+intent.SourceSnapshotID, "system", "", rebuildablePayload(map[string]any{
		"source_snapshot_id": intent.SourceSnapshotID,
		"ingestion_kind":     string(intent.Kind),
		"content_hash":       intent.ContentHash,
		"fingerprint":        validated.Fingerprint,
		"canonical_url":      canonicalURL,
	})); err != nil {
		return PersistSourceIngestionResult{}, err
	}
	return PersistSourceIngestionResult{SourceSnapshotID: intent.SourceSnapshotID, Fingerprint: validated.Fingerprint}, nil
}

func (k SourceIngestionKind) String() string { return string(k) }
