package handler

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"

	"github.com/multica-ai/multica/server/internal/service"
)

func TestFrozenRunDAGResponse_IsSanitizedAndPreservesFrozenOrdering(t *testing.T) {
	now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	dag := service.FrozenDAGRecord{
		Run: service.FrozenDAGRunRecord{
			RunID:        testFrozenDAGUUID(1),
			ProjectID:    testFrozenDAGUUID(2),
			WorkspaceID:  testFrozenDAGUUID(3),
			Status:       "completed",
			FrozenAt:     now,
			SnapshotHash: "sha256:immutable",
		},
		Snapshot: service.FrozenDAGSnapshotRecord{
			SnapshotID: "sha256:immutable", SchemaVersion: "1", SnapshotHash: "sha256:immutable",
		},
		ProviderCalls: []service.FrozenDAGProviderCallRecord{{CallID: "call-1"}, {CallID: "call-2"}},
		Segments:      []service.FrozenDAGSegmentRecord{{SegmentID: "message:1"}, {SegmentID: "terminal:2"}},
		Edges: []service.CausalEdgeRecord{{
			SourceSegmentID: "message:1", DestinationSegmentID: "terminal:2",
			Type: "session_continuation", DestinationCallID: "call-2", EdgeOrdinal: 1,
		}},
	}

	body, err := json.Marshal(frozenRunDAGResponse(dag))
	assert.NoError(t, err)
	var response FrozenRunDAGResponse
	assert.NoError(t, json.Unmarshal(body, &response))
	assert.Equal(t, []string{"call-1", "call-2"}, []string{
		response.ProviderCalls[0].CallID,
		response.ProviderCalls[1].CallID,
	})
	for _, forbidden := range []string{"raw_provider_request", "final_assistant_message", "normalized_trajectory", "authorization", "tensor"} {
		assert.NotContains(t, string(body), forbidden)
	}
}

func testFrozenDAGUUID(last byte) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte{0x70, 0, 0, 0, 0, 0, 0x40, 0, 0x80, 0, 0, 0, 0, 0, 0, last}, Valid: true}
}
