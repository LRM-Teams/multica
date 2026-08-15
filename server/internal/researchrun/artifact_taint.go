package researchrun

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// deriveManifestOutputAccess propagates the most sensitive normal input. Empty
// context remains raw because model/tool bytes are not a verified transformation.
func deriveManifestOutputAccess(levels []ArtifactAccessLevel) (ArtifactAccessLevel, error) {
	result := ArtifactAccessRaw
	if len(levels) > 0 {
		result = ArtifactAccessVerifiedOnly
	}
	for _, level := range levels {
		switch level {
		case ArtifactAccessRaw:
			return ArtifactAccessRaw, nil
		case ArtifactAccessRedacted:
			result = ArtifactAccessRedacted
		case ArtifactAccessVerifiedOnly:
		default:
			return "", fmt.Errorf("%w: unknown manifest input access level %q", ErrInvalidTransition, level)
		}
	}
	return result, nil
}

func deriveManifestOutputAccessTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID, attemptID string) (ArtifactAccessLevel, error) {
	rows, err := tx.Query(ctx, `SELECT v.access_level FROM research_artifact_context_entry e JOIN research_artifact_context_manifest m ON (m.workspace_id,m.session_id,m.id)=(e.workspace_id,e.session_id,e.manifest_id) JOIN research_artifact_version v ON (v.workspace_id,v.session_id,v.id)=(e.workspace_id,e.session_id,e.artifact_version_id) WHERE m.workspace_id=$1::uuid AND m.session_id=$2::uuid AND m.attempt_id=$3::uuid ORDER BY e.ordinal`, workspaceID, sessionID, attemptID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var levels []ArtifactAccessLevel
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return "", err
		}
		levels = append(levels, ArtifactAccessLevel(raw))
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	return deriveManifestOutputAccess(levels)
}
