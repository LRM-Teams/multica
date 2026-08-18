package researchrun

import (
	"context"
	"errors"
	"reflect"
	"sort"

	"github.com/jackc/pgx/v5"
)

var ErrProjectionResyncRequired = errors.New("research V6 projection resync required")

const v6ProjectionMaximumDeltaEvents = 1000

func (s *PostgresStore) ProjectionV6Deltas(ctx context.Context, request V6ProjectionDeltaRequest) (V6ProjectionDeltaPage, error) {
	page := V6ProjectionDeltaPage{RunID: request.RunID, Deltas: []V6ProjectionDelta{}, NextCursor: nil}
	if request.After < 0 {
		return page, ErrInvalidContract
	}
	var baselineID, clientSnapshotID, baselineHash string
	var baselineSequence int64
	if request.SnapshotID != "" {
		var originalExists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM research_projection_snapshot WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid AND expires_at>now())`, request.WorkspaceID, request.RunID, request.SnapshotID).Scan(&originalExists); err != nil {
			return page, err
		}
		if !originalExists {
			page.ResyncRequired = true
			return page, nil
		}
		clientSnapshotID = request.SnapshotID
	}
	query := `SELECT id::text,through_event_sequence,projection_hash FROM research_projection_snapshot WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND through_event_sequence=$3 AND expires_at>now()`
	args := []any{request.WorkspaceID, request.RunID, request.After}
	if request.ProjectionHash != "" {
		query += ` AND projection_hash=$4`
		args = append(args, request.ProjectionHash)
	}
	query += ` ORDER BY generation DESC LIMIT 1`
	err := s.pool.QueryRow(ctx, query, args...).Scan(&baselineID, &baselineSequence, &baselineHash)
	if errors.Is(err, pgx.ErrNoRows) {
		page.ResyncRequired = true
		return page, nil
	}
	if err != nil {
		return page, err
	}
	if clientSnapshotID == "" {
		clientSnapshotID = baselineID
	}
	var currentSequence, firstRetained, retainedAfter int64
	if err = s.pool.QueryRow(ctx, `SELECT COALESCE(max(sequence),0),COALESCE(min(sequence),0),count(*) FILTER(WHERE sequence>$3) FROM research_run_event WHERE workspace_id=$1::uuid AND session_id=$2::uuid`, request.WorkspaceID, request.RunID, request.After).Scan(&currentSequence, &firstRetained, &retainedAfter); err != nil {
		return page, err
	}
	if currentSequence == request.After {
		return page, nil
	}
	if currentSequence < request.After || retainedAfter != currentSequence-request.After || (firstRetained > 0 && firstRetained > request.After+1) || currentSequence-request.After > v6ProjectionMaximumDeltaEvents {
		page.ResyncRequired = true
		return page, nil
	}
	baselineNodes, baselineEdges, _, _, loadedHash, err := s.loadPinnedV6ProjectionPayload(ctx, request.WorkspaceID, request.RunID, baselineID, "default")
	if err != nil || loadedHash != baselineHash || baselineSequence != request.After {
		page.ResyncRequired = true
		return page, nil
	}
	currentFirst, err := s.createV6ProjectionSnapshot(ctx, V6ProjectionPageRequest{WorkspaceID: request.WorkspaceID, RunID: request.RunID, Limit: v6ProjectionMaximumPageSize})
	if err != nil {
		return page, err
	}
	currentNodes, currentEdges, _, observedSequence, currentHash, err := s.loadPinnedV6ProjectionPayload(ctx, request.WorkspaceID, request.RunID, currentFirst.SnapshotID, "default")
	if err != nil {
		return page, err
	}
	if observedSequence != currentSequence {
		currentSequence = observedSequence
		if currentSequence-request.After > v6ProjectionMaximumDeltaEvents {
			page.ResyncRequired = true
			return page, nil
		}
	}
	upsertNodes, removeNodeIDs := diffV6ProjectionNodes(baselineNodes, currentNodes)
	upsertEdges, removeEdgeIDs := diffV6ProjectionEdges(baselineEdges, currentEdges)
	if len(upsertNodes) > 10000 || len(removeNodeIDs) > 10000 || len(upsertEdges) > 20000 || len(removeEdgeIDs) > 20000 {
		page.ResyncRequired = true
		return page, nil
	}
	previousHash := baselineHash
	for sequence := request.After + 1; sequence <= currentSequence; sequence++ {
		delta := V6ProjectionDelta{ContractKind: "projection_delta", SchemaVersion: 6, WorkspaceID: request.WorkspaceID, RunID: request.RunID, SnapshotID: clientSnapshotID, EventSequence: sequence, PreviousProjectionHash: previousHash, ProjectionHash: previousHash, UpsertNodes: []V6ProjectionNode{}, RemoveNodeIDs: []string{}, UpsertEdges: []V6ProjectionEdge{}, RemoveEdgeIDs: []string{}, InvalidateSliceKeys: []string{}}
		if sequence == currentSequence {
			delta.ProjectionHash = currentHash
			delta.UpsertNodes, delta.RemoveNodeIDs = upsertNodes, removeNodeIDs
			delta.UpsertEdges, delta.RemoveEdgeIDs = upsertEdges, removeEdgeIDs
			delta.InvalidateSliceKeys = []string{}
		}
		page.Deltas = append(page.Deltas, delta)
		previousHash = delta.ProjectionHash
	}
	return page, nil
}

func diffV6ProjectionNodes(before, after []V6ProjectionNode) ([]V6ProjectionNode, []string) {
	left := make(map[string]V6ProjectionNode, len(before))
	for _, item := range before {
		left[item.ID] = item
	}
	upserts := []V6ProjectionNode{}
	for _, item := range after {
		prior, exists := left[item.ID]
		if !exists || !reflect.DeepEqual(prior, item) {
			upserts = append(upserts, item)
		}
		delete(left, item.ID)
	}
	removed := make([]string, 0, len(left))
	for id := range left {
		removed = append(removed, id)
	}
	sort.Slice(upserts, func(i, j int) bool { return upserts[i].ID < upserts[j].ID })
	sort.Strings(removed)
	return upserts, removed
}

func diffV6ProjectionEdges(before, after []V6ProjectionEdge) ([]V6ProjectionEdge, []string) {
	left := make(map[string]V6ProjectionEdge, len(before))
	for _, item := range before {
		left[item.ID] = item
	}
	upserts := []V6ProjectionEdge{}
	for _, item := range after {
		prior, exists := left[item.ID]
		if !exists || !reflect.DeepEqual(prior, item) {
			upserts = append(upserts, item)
		}
		delete(left, item.ID)
	}
	removed := make([]string, 0, len(left))
	for id := range left {
		removed = append(removed, id)
	}
	sort.Slice(upserts, func(i, j int) bool { return upserts[i].ID < upserts[j].ID })
	sort.Strings(removed)
	return upserts, removed
}
