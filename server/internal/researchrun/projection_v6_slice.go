package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) ProjectionV6Slice(ctx context.Context, request V6ProjectionSliceRequest) (V6ProjectionSnapshot, error) {
	if request.Depth != 1 || request.RootNodeID == "" || uuid.Validate(request.SnapshotID) != nil {
		return V6ProjectionSnapshot{}, ErrInvalidContract
	}
	if request.Limit == 0 {
		request.Limit = v6ProjectionDefaultPageSize
	}
	if request.Limit < 1 || request.Limit > v6ProjectionMaximumPageSize {
		return V6ProjectionSnapshot{}, ErrInvalidContract
	}
	sliceKey := "root:" + request.RootNodeID + ":depth:1"
	if len(sliceKey) > 160 {
		return V6ProjectionSnapshot{}, ErrInvalidContract
	}
	if request.Cursor != "" {
		cursor, err := decodeV6ProjectionCursor(request.Cursor)
		if err != nil || cursor.SnapshotID != request.SnapshotID || cursor.SliceKey != sliceKey || cursor.Limit != request.Limit {
			return V6ProjectionSnapshot{}, ErrInvalidContract
		}
		return s.loadV6ProjectionPage(ctx, request.WorkspaceID, request.RunID, cursor)
	}
	nodes, edges, density, sequence, projectionHash, err := s.loadPinnedCanonicalV6Projection(ctx, request.WorkspaceID, request.RunID, request.SnapshotID)
	if err != nil {
		return V6ProjectionSnapshot{}, err
	}
	nodeByID := make(map[string]V6ProjectionNode, len(nodes))
	for _, node := range nodes {
		nodeByID[node.ID] = node
	}
	if _, ok := nodeByID[request.RootNodeID]; !ok {
		return V6ProjectionSnapshot{}, ErrInvalidContract
	}
	selected := map[string]struct{}{request.RootNodeID: {}}
	for _, edge := range edges {
		if edge.FromNodeID == request.RootNodeID {
			selected[edge.ToNodeID] = struct{}{}
		}
		if edge.ToNodeID == request.RootNodeID {
			selected[edge.FromNodeID] = struct{}{}
		}
	}
	sliceNodes := make([]V6ProjectionNode, 0, len(selected))
	for id := range selected {
		if node, ok := nodeByID[id]; ok {
			sliceNodes = append(sliceNodes, node)
		}
	}
	sliceEdges := make([]V6ProjectionEdge, 0)
	for _, edge := range edges {
		_, fromOK := selected[edge.FromNodeID]
		_, toOK := selected[edge.ToNodeID]
		if fromOK && toOK {
			sliceEdges = append(sliceEdges, edge)
		}
	}
	sort.Slice(sliceNodes, func(i, j int) bool { return sliceNodes[i].ID < sliceNodes[j].ID })
	sort.Slice(sliceEdges, func(i, j int) bool { return sliceEdges[i].ID < sliceEdges[j].ID })
	pages := paginateV6Projection(request.SnapshotID, request.WorkspaceID, request.RunID, sequence, projectionHash, sliceKey, request.Limit, sliceNodes, sliceEdges, density)
	tx, err := s.beginResearchTx(ctx, txOpV6ProjectionSlice, pgx.TxOptions{})
	if err != nil {
		return V6ProjectionSnapshot{}, err
	}
	defer tx.Rollback(ctx)
	for index := range pages {
		payload, marshalErr := marshalV6CanonicalJSON(pages[index])
		if marshalErr != nil {
			return V6ProjectionSnapshot{}, marshalErr
		}
		cursorKey := fmt.Sprintf("page:%08d:limit:%d", index+1, request.Limit)
		if _, err = tx.Exec(ctx, `INSERT INTO research_projection_slice(workspace_id,session_id,snapshot_id,slice_key,cursor_key,node_count,edge_count,density_count,payload_hash,payload_bytes) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(snapshot_id,slice_key,cursor_key) DO UPDATE SET payload_hash=EXCLUDED.payload_hash,payload_bytes=EXCLUDED.payload_bytes,node_count=EXCLUDED.node_count,edge_count=EXCLUDED.edge_count,density_count=EXCLUDED.density_count`, request.WorkspaceID, request.RunID, request.SnapshotID, sliceKey, cursorKey, len(pages[index].Nodes), len(pages[index].Edges), len(pages[index].DensityBins), ArtifactContentHashFromCanonicalJSON(payload), payload); err != nil {
			return V6ProjectionSnapshot{}, err
		}
	}
	if err = s.commitResearchTx(ctx, txOpV6ProjectionSlice, tx); err != nil {
		return V6ProjectionSnapshot{}, err
	}
	return pages[0], nil
}

func (s *PostgresStore) loadPinnedCanonicalV6Projection(ctx context.Context, workspaceID, runID, snapshotID string) ([]V6ProjectionNode, []V6ProjectionEdge, []V6ProjectionDensityBin, int64, string, error) {
	return s.loadPinnedV6ProjectionPayload(ctx, workspaceID, runID, snapshotID, "canonical")
}

func (s *PostgresStore) loadPinnedV6ProjectionPayload(ctx context.Context, workspaceID, runID, snapshotID, sliceKey string) ([]V6ProjectionNode, []V6ProjectionEdge, []V6ProjectionDensityBin, int64, string, error) {
	var sequence int64
	var projectionHash string
	if err := s.pool.QueryRow(ctx, `SELECT through_event_sequence,projection_hash FROM research_projection_snapshot WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid AND expires_at>now()`, workspaceID, runID, snapshotID).Scan(&sequence, &projectionHash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, nil, 0, "", ErrProjectionResyncRequired
		}
		return nil, nil, nil, 0, "", err
	}
	rows, err := s.pool.Query(ctx, `SELECT payload_bytes,payload_hash FROM research_projection_slice WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND snapshot_id=$3::uuid AND slice_key=$4 ORDER BY cursor_key`, workspaceID, runID, snapshotID, sliceKey)
	if err != nil {
		return nil, nil, nil, 0, "", err
	}
	defer rows.Close()
	nodes, edges, density := []V6ProjectionNode{}, []V6ProjectionEdge{}, []V6ProjectionDensityBin{}
	for rows.Next() {
		var payload []byte
		var payloadHash string
		var page V6ProjectionSnapshot
		if err = rows.Scan(&payload, &payloadHash); err != nil || ArtifactContentHashFromCanonicalJSON(payload) != payloadHash || json.Unmarshal(payload, &page) != nil || page.ProjectionHash != projectionHash {
			return nil, nil, nil, 0, "", ErrProjectionResyncRequired
		}
		nodes = append(nodes, page.Nodes...)
		edges = append(edges, page.Edges...)
		density = append(density, page.DensityBins...)
	}
	if err = rows.Err(); err != nil {
		return nil, nil, nil, 0, "", err
	}
	return nodes, edges, density, sequence, projectionHash, nil
}
