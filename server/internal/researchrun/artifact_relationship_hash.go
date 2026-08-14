package researchrun

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

type artifactRelationshipHashRecord struct {
	ownerVersionID string
	direction      string
	referenceID    string
	otherVersionID string
	relation       string
	manifestID     string
	explicitlyUsed bool
	purpose        string
	ordinal        int
}

func loadArtifactRelationshipHashesTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID string,
) (map[string]string, error) {
	rows, err := tx.Query(ctx, `
		WITH current_versions AS (
		  SELECT version.id
		  FROM research_artifact_passport passport
		  JOIN research_artifact_version version
		    ON version.workspace_id=passport.workspace_id
		   AND version.session_id=passport.session_id
		   AND version.artifact_id=passport.id
		   AND version.version=passport.current_version
		  WHERE passport.workspace_id=$1::uuid AND passport.session_id=$2::uuid
		), relationships AS (
		  SELECT current.id AS owner_version_id, 'input'::text AS direction,
		         reference.id, reference.input_version_id AS other_version_id,
		         reference.relation, COALESCE(reference.manifest_id::text, ''),
		         reference.explicitly_used, reference.purpose, reference.ordinal
		  FROM current_versions current
		  JOIN research_artifact_input_reference reference
		    ON reference.workspace_id=$1::uuid AND reference.session_id=$2::uuid
		   AND reference.consumer_version_id=current.id
		  UNION ALL
		  SELECT current.id, 'output'::text, reference.id,
		         reference.consumer_version_id, reference.relation,
		         COALESCE(reference.manifest_id::text, ''), reference.explicitly_used,
		         reference.purpose, reference.ordinal
		  FROM current_versions current
		  JOIN research_artifact_input_reference reference
		    ON reference.workspace_id=$1::uuid AND reference.session_id=$2::uuid
		   AND reference.input_version_id=current.id
		)
		SELECT owner_version_id::text,direction,id::text,other_version_id::text,
		       relation,manifest_id,explicitly_used,purpose,ordinal
		FROM relationships
		ORDER BY owner_version_id,direction,ordinal,id
	`, workspaceID, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := make(map[string][]artifactRelationshipHashRecord)
	for rows.Next() {
		var record artifactRelationshipHashRecord
		if err = rows.Scan(
			&record.ownerVersionID, &record.direction, &record.referenceID,
			&record.otherVersionID, &record.relation, &record.manifestID,
			&record.explicitlyUsed, &record.purpose, &record.ordinal,
		); err != nil {
			return nil, err
		}
		records[record.ownerVersionID] = append(records[record.ownerVersionID], record)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	hashes := make(map[string]string, len(records))
	for versionID, versionRecords := range records {
		hashes[versionID] = hashArtifactRelationshipRecords(versionRecords)
	}
	return hashes, nil
}

func hashArtifactRelationshipRecords(records []artifactRelationshipHashRecord) string {
	parts := make([]string, 0, len(records))
	for _, record := range records {
		parts = append(parts, fmt.Sprintf(
			"%s:%s:%s:%s:%s:%t:%s:%d",
			record.direction, record.referenceID, record.otherVersionID,
			record.relation, record.manifestID, record.explicitlyUsed,
			record.purpose, record.ordinal,
		))
	}
	return contentHashFromPayload([]byte(strings.Join(parts, "\n")))
}

func casArtifactRelationshipHashesTx(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, sessionID string,
	entries []artifactVersionCandidate,
) error {
	hashes, err := loadArtifactRelationshipHashesTx(ctx, tx, workspaceID, sessionID)
	if err != nil {
		return err
	}
	emptyHash := contentHashFromPayload(nil)
	for _, entry := range entries {
		actual := hashes[entry.VersionRowID]
		if actual == "" {
			actual = emptyHash
		}
		if entry.RelationshipHash == "" || actual != entry.RelationshipHash {
			return fmt.Errorf("%w: artifact relationship identity CAS failed", ErrInvalidTransition)
		}
	}
	return nil
}
