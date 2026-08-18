package researchrun

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

const v6ProjectionDefaultPageSize = 1000
const v6ProjectionMaximumPageSize = 10000

type v6ProjectionCursor struct {
	SnapshotID string `json:"snapshot_id"`
	SliceKey   string `json:"slice_key"`
	Page       int    `json:"page"`
	Limit      int    `json:"limit"`
}

func v6ProjectionStableID(kind, entityID string, revision int) string {
	id := "pv6:" + kind + ":" + entityID
	if revision > 0 {
		id += fmt.Sprintf(":%d", revision)
	}
	return id
}

func v6ProjectionEdgeID(kind, from, to string) string {
	return "pe6:" + kind + ":" + from + ":" + to
}

func encodeV6ProjectionCursor(cursor v6ProjectionCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeV6ProjectionCursor(raw string) (v6ProjectionCursor, error) {
	var cursor v6ProjectionCursor
	bytes, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || json.Unmarshal(bytes, &cursor) != nil || uuid.Validate(cursor.SnapshotID) != nil || cursor.SliceKey == "" || cursor.Page < 1 || cursor.Limit < 1 || cursor.Limit > v6ProjectionMaximumPageSize {
		return cursor, ErrInvalidContract
	}
	return cursor, nil
}

func hashV6Projection(nodes []V6ProjectionNode, edges []V6ProjectionEdge, density []V6ProjectionDensityBin) (string, error) {
	canonical, err := marshalV6CanonicalJSON(map[string]any{"nodes": nodes, "edges": edges, "density_bins": density})
	if err != nil {
		return "", err
	}
	return ArtifactContentHashFromCanonicalJSON(canonical), nil
}

func normalizeV6Projection(nodes []V6ProjectionNode, edges []V6ProjectionEdge, density []V6ProjectionDensityBin) {
	for index := range nodes {
		sort.Strings(nodes[index].BranchIDs)
		nodes[index].CatalogSummary = strings.TrimSpace(nodes[index].CatalogSummary)
		nodes[index].CatalogSummary = truncateProjectionText(nodes[index].CatalogSummary, 512)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	sort.Slice(density, func(i, j int) bool { return density[i].ID < density[j].ID })
}
