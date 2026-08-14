package handler

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

const researchV6SnapshotMaximumPageItems = 1000

type researchV6SnapshotCursor struct {
	SnapshotID string `json:"snapshot_id"`
	Offset     int    `json:"offset"`
}

func paginateResearchV6Snapshot(snapshot researchV6Snapshot, limit int, encodedCursor string) (researchV6Snapshot, error) {
	if limit <= 0 || limit > researchV6SnapshotMaximumPageItems {
		return researchV6Snapshot{}, fmt.Errorf("snapshot limit must be in [1,%d]", researchV6SnapshotMaximumPageItems)
	}
	offset := 0
	if encodedCursor != "" {
		cursor, err := decodeResearchV6SnapshotCursor(encodedCursor)
		if err != nil || cursor.SnapshotID != snapshot.SnapshotID {
			return researchV6Snapshot{}, fmt.Errorf("snapshot cursor baseline changed; resync required")
		}
		offset = cursor.Offset
	}
	total := len(snapshot.Nodes) + len(snapshot.Edges)
	if offset < 0 || offset > total {
		return researchV6Snapshot{}, fmt.Errorf("snapshot cursor offset is invalid")
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := researchV6Snapshot{SnapshotID: snapshot.SnapshotID, RunID: snapshot.RunID, ThroughEventSequence: snapshot.ThroughEventSequence, GraphContentHash: snapshot.GraphContentHash, Nodes: []researchV6ProjectionNode{}, Edges: []researchV6ProjectionEdge{}, Clusters: snapshot.Clusters}
	for index := offset; index < end; index++ {
		if index < len(snapshot.Nodes) {
			page.Nodes = append(page.Nodes, snapshot.Nodes[index])
		} else {
			page.Edges = append(page.Edges, snapshot.Edges[index-len(snapshot.Nodes)])
		}
	}
	if end < total {
		next := encodeResearchV6SnapshotCursor(researchV6SnapshotCursor{SnapshotID: snapshot.SnapshotID, Offset: end})
		page.NextCursor = &next
	}
	return page, nil
}

func encodeResearchV6SnapshotCursor(cursor researchV6SnapshotCursor) string {
	payload, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(payload)
}
func decodeResearchV6SnapshotCursor(value string) (researchV6SnapshotCursor, error) {
	var cursor researchV6SnapshotCursor
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cursor, err
	}
	err = json.Unmarshal(payload, &cursor)
	return cursor, err
}
