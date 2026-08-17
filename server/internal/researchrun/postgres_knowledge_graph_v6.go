package researchrun

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type V6SuccessorRef struct {
	InputArtifactVersionID, InsightVersionID, ArtifactVersionID string
	Tier                                                        V6Tier
	Relation                                                    string
}

func (s *PostgresStore) ResolveV6Successor(ctx context.Context, workspaceID, runID, inputArtifactVersionID string) (V6SuccessorRef, error) {
	var ref V6SuccessorRef
	err := s.pool.QueryRow(ctx, `SELECT a.input_artifact_version_id::text,a.successor_insight_version_id::text,
		v.artifact_version_id::text,v.tier,a.relation FROM research_node_absorption a
		JOIN research_insight_version v ON (v.workspace_id,v.session_id,v.id)=(a.workspace_id,a.session_id,a.successor_insight_version_id)
		WHERE a.workspace_id=$1::uuid AND a.session_id=$2::uuid AND a.input_artifact_version_id=$3::uuid`,
		workspaceID, runID, inputArtifactVersionID).Scan(&ref.InputArtifactVersionID, &ref.InsightVersionID, &ref.ArtifactVersionID, &ref.Tier, &ref.Relation)
	if errors.Is(err, pgx.ErrNoRows) {
		return V6SuccessorRef{}, ErrRunNotFound
	}
	return ref, err
}

func (s *PostgresStore) IsV6NodeFresh(ctx context.Context, workspaceID, runID, artifactVersionID string) (bool, error) {
	var fresh bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM research_branch_frontier
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND node_artifact_version_id=$3::uuid
		AND removed_by_event_sequence IS NULL)`, workspaceID, runID, artifactVersionID).Scan(&fresh)
	return fresh, err
}
