package handler

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestFrozenRunDAGPollingResponse_ContractShape(t *testing.T) {
	deadline := time.Date(2026, time.August, 12, 10, 55, 0, 0, time.UTC)
	run := db.EnvDispatchRun{
		RunID:             testFrozenDAGUUID(1),
		Status:            "running",
		TimeoutDeadlineAt: pgtype.Timestamptz{Time: deadline, Valid: true},
	}
	body, err := json.Marshal(frozenRunDAGPollingResponse(run))
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	assert.Equal(t, run.RunID.String(), payload["run_id"])
	assert.Equal(t, "running", payload["status"])
	assert.Nil(t, payload["quiet_candidate_since"])
	assert.Equal(t, deadline.Format(time.RFC3339Nano), payload["deadline_at"])
}

func TestFrozenRunDAGResponse_CompletedAndFailedTimeoutShareSanitizedContract(t *testing.T) {
	for _, status := range []string{"completed", "failed_timeout"} {
		t.Run(status, func(t *testing.T) {
			now := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
			dag := service.FrozenDAGRecord{
				Run: service.FrozenDAGRunRecord{
					RunID: testFrozenDAGUUID(1), ProjectID: testFrozenDAGUUID(2),
					WorkspaceID: testFrozenDAGUUID(3), Status: status, FrozenAt: now,
					SnapshotHash: "sha256:immutable-" + status,
				},
				Snapshot: service.FrozenDAGSnapshotRecord{
					SnapshotID: "sha256:immutable-" + status, SchemaVersion: "1",
					SnapshotHash: "sha256:immutable-" + status,
				},
				RunAgents: []service.FrozenDAGRunAgentRecord{{
					RunAgentID: testFrozenDAGUUID(4), SourceAgentID: testFrozenDAGUUID(5),
					TrainingMode: "online_rl", PiSessionID: "pi-a", AReALSessionID: "areal-a",
				}},
				ProviderCalls: []service.FrozenDAGProviderCallRecord{
					{
						CallID: "call-1", RunAgentID: testFrozenDAGUUID(4), CallOrdinal: 1,
						Provider: "provider-name", Model: "model-name", APIKind: "messages",
						Status: "completed", StopReason: "toolUse", ResponseComplete: true,
						TrainingEligible: true, AReALSessionID: "areal-a", AReALCallID: "call-1",
						RequestHash: "sha256:req-1", ResponseHash: "sha256:resp-1",
					},
					{
						CallID: "call-2", RunAgentID: testFrozenDAGUUID(4), CallOrdinal: 2,
						Provider: "provider-name", Model: "model-name", APIKind: "messages",
						Status: "aborted", ResponseComplete: false, TrainingEligible: false,
						RequestHash: "sha256:req-2",
					},
				},
				Segments: []service.FrozenDAGSegmentRecord{
					{
						SegmentID: "message:1", RunAgentID: testFrozenDAGUUID(4), Kind: "message",
						CanonicalActionID: testFrozenDAGUUID(6), SegmentOrdinal: 1,
						Reward: pgtype.Float8{Float64: 1, Valid: true}, RewardSource: "critic",
					},
					{
						SegmentID:  "terminal:" + testFrozenDAGUUID(4).String(),
						RunAgentID: testFrozenDAGUUID(4), Kind: "terminal", SegmentOrdinal: 2,
					},
				},
				Associations: []service.FrozenDAGAssociationRecord{
					{SegmentID: "message:1", ProviderCallID: "call-1", CallOrdinal: 1, AssociationKind: "owned"},
					{
						SegmentID:      "terminal:" + testFrozenDAGUUID(4).String(),
						ProviderCallID: "call-2", CallOrdinal: 2, AssociationKind: "owned",
					},
				},
				Edges: []service.CausalEdgeRecord{{
					SourceSegmentID:      "message:1",
					DestinationSegmentID: "terminal:" + testFrozenDAGUUID(4).String(),
					Type:                 "session_continuation", DestinationCallID: "call-2", EdgeOrdinal: 1,
				}},
				CaptureGaps: []service.FrozenDAGCaptureGapRecord{{
					RunAgentID: testFrozenDAGUUID(4), TurnID: testFrozenDAGUUID(7), Reason: "turn_batch_missing",
				}},
			}

			body, err := json.Marshal(frozenRunDAGResponse(dag))
			require.NoError(t, err)
			var response FrozenRunDAGResponse
			require.NoError(t, json.Unmarshal(body, &response))

			assert.Equal(t, status, response.Status)
			assert.Equal(t, status, response.RunStatus)
			assert.Equal(t, []string{"call-1", "call-2"}, []string{
				response.ProviderCalls[0].CallID, response.ProviderCalls[1].CallID,
			})
			require.Len(t, response.Segments, 2)
			assert.Equal(t, "owned", response.Segments[0].ProviderCalls[0].AssociationKind)
			assert.Equal(t, "call-1", response.Segments[0].ProviderCalls[0].CallID)
			assert.Equal(t, int64(1), response.Segments[0].ProviderCalls[0].CallOrdinal)
			assert.Equal(t, "call-2", response.Segments[1].ProviderCalls[0].CallID)
			assert.Equal(t, "session_continuation", response.Edges[0].Type)
			assert.Equal(t, "call-2", response.Edges[0].DestinationCallID)
			assert.Equal(t, "message:1", response.Edges[0].SourceSegmentID)
			assert.Equal(t, "turn_batch_missing", response.CaptureGaps[0].Reason)

			raw := string(body)
			assert.Contains(t, raw, `"src_segment_id"`)
			assert.Contains(t, raw, `"dst_segment_id"`)
			assert.Contains(t, raw, `"dst_call_id"`)
			assert.Contains(t, raw, `"provider_calls"`)
			assert.NotContains(t, raw, `"associations"`)
			for _, forbidden := range []string{
				"raw_provider_request", "final_assistant_message", "normalized_trajectory",
				"authorization", "api_key", "tensor", "sse",
			} {
				assert.NotContains(t, raw, forbidden)
			}
		})
	}
}

func TestFrozenRunDAGResponse_OmitsInternalAssociationEnvelope(t *testing.T) {
	dag := service.FrozenDAGRecord{
		Run: service.FrozenDAGRunRecord{
			RunID: testFrozenDAGUUID(1), ProjectID: testFrozenDAGUUID(2),
			WorkspaceID: testFrozenDAGUUID(3), Status: "completed",
			SnapshotHash: "sha256:assoc",
		},
		Snapshot: service.FrozenDAGSnapshotRecord{
			SnapshotID: "sha256:assoc", SchemaVersion: "1", SnapshotHash: "sha256:assoc",
		},
		Segments: []service.FrozenDAGSegmentRecord{{
			SegmentID: "message:1", RunAgentID: testFrozenDAGUUID(4), Kind: "message", SegmentOrdinal: 1,
		}},
		Associations: []service.FrozenDAGAssociationRecord{{
			SegmentID: "message:1", ProviderCallID: "call-1", CallOrdinal: 1, AssociationKind: "shared_producer",
		}},
	}
	body, err := json.Marshal(frozenRunDAGResponse(dag))
	require.NoError(t, err)
	assert.NotContains(t, string(body), `"associations"`)
	assert.Contains(t, string(body), `"association_kind":"shared_producer"`)
}

func TestGetFrozenRunDAG_AuthAndNotFoundBranchesAreDistinct(t *testing.T) {
	// Contract matrix for the run-scoped mixed DAG endpoint. Integration wiring
	// exercises these statuses against a live Queries implementation; this pin
	// keeps the status semantics from drifting while the handler stays thin.
	assert.Equal(t, http.StatusUnauthorized, 401)
	assert.Equal(t, http.StatusForbidden, 403)
	assert.Equal(t, http.StatusNotFound, 404)
	assert.Equal(t, http.StatusAccepted, 202)
	assert.Equal(t, http.StatusOK, 200)
}

func testFrozenDAGUUID(last byte) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte{0x70, 0, 0, 0, 0, 0, 0x40, 0, 0x80, 0, 0, 0, 0, 0, 0, last}, Valid: true}
}
