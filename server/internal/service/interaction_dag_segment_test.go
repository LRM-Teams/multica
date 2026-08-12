// SPDX-License-Identifier: Apache-2.0

package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestInteractionDAGSegment_MultiCallAssignmentInCanonicalOrder(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	turn := createMixedRLTurn(t, h, agent)
	advanceMixedRLRunToRunning(t, h, run.RunID)

	messageID := util.MustParseUUID("70000000-0000-4000-8000-000000000301")
	seedMixedRLChannelMessage(t, h, messageID)

	calls := []ProviderCallInput{
		mixedRLProviderCallInput(run.RunID, agent, turn, "seg-multi-call-1", 1),
		mixedRLProviderCallInput(run.RunID, agent, turn, "seg-multi-call-2", 2),
		mixedRLProviderCallInput(run.RunID, agent, turn, "seg-multi-call-3", 3),
	}
	action := mixedRLSucceededMessageAction(run.RunID, agent, turn, messageID, "seg-multi-call-3", 1)
	acceptMixedRLTrustedCapture(t, h, run, agent, turn, calls, []VisibleActionInput{action}, nil)

	segments, associations := listMixedRLProvisionalGraph(t, h, run.RunID)
	require.Len(t, segments, 1, "one successful message action must close exactly one message segment")
	assert.Equal(t, "message:"+messageID.String(), segments[0].SegmentID)
	require.Len(t, associations, 3, "all unassigned prior calls must be assigned to the owning segment")
	assert.Equal(t, []string{"seg-multi-call-1", "seg-multi-call-2", "seg-multi-call-3"}, associationCallIDs(associations))
	for _, association := range associations {
		assert.Equal(t, "owned", association.AssociationKind)
		assert.Equal(t, segments[0].SegmentID, association.SegmentID)
	}
}

func TestInteractionDAGSegment_SettlementCarryOverDoesNotCreateSegment(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	firstTurn := createMixedRLTurnWithID(t, h, run.RunID, agent, "70000000-0000-4000-8000-000000000302")
	advanceMixedRLRunToRunning(t, h, run.RunID)

	acceptMixedRLTrustedCapture(t, h, run, agent, firstTurn, []ProviderCallInput{
		mixedRLProviderCallInput(run.RunID, agent, firstTurn, "seg-settle-call-1", 1),
		mixedRLProviderCallInput(run.RunID, agent, firstTurn, "seg-settle-call-2", 2),
	}, nil, nil)

	segments, associations := listMixedRLProvisionalGraph(t, h, run.RunID)
	assert.Empty(t, segments, "settlement alone must never create a segment")
	assert.Empty(t, associations, "settlement alone must leave calls unassigned")

	secondTurn := createMixedRLTurnWithID(t, h, run.RunID, agent, "70000000-0000-4000-8000-000000000303")
	messageID := util.MustParseUUID("70000000-0000-4000-8000-000000000304")
	seedMixedRLChannelMessage(t, h, messageID)
	acceptMixedRLTrustedCapture(t, h, run, agent, secondTurn, []ProviderCallInput{
		mixedRLProviderCallInput(run.RunID, agent, secondTurn, "seg-settle-call-3", 3),
	}, []VisibleActionInput{
		mixedRLSucceededMessageAction(run.RunID, agent, secondTurn, messageID, "seg-settle-call-3", 1),
	}, nil)

	segments, associations = listMixedRLProvisionalGraph(t, h, run.RunID)
	require.Len(t, segments, 1)
	require.Len(t, associations, 3, "later visible action must own earlier settlement-carry-over calls")
	assert.Equal(t, []string{"seg-settle-call-1", "seg-settle-call-2", "seg-settle-call-3"}, associationCallIDs(associations))
	for _, association := range associations {
		assert.Equal(t, "owned", association.AssociationKind)
		assert.Equal(t, "message:"+messageID.String(), association.SegmentID)
	}
}

func TestInteractionDAGSegment_FailedActionsDoNotClaimOwnership(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	firstTurn := createMixedRLTurnWithID(t, h, run.RunID, agent, "70000000-0000-4000-8000-000000000305")
	advanceMixedRLRunToRunning(t, h, run.RunID)

	failedCanonical := util.MustParseUUID("70000000-0000-4000-8000-000000000306")
	seedMixedRLChannelMessage(t, h, failedCanonical)
	acceptMixedRLTrustedCapture(t, h, run, agent, firstTurn, []ProviderCallInput{
		mixedRLProviderCallInput(run.RunID, agent, firstTurn, "seg-failed-call", 1),
	}, []VisibleActionInput{{
		ActionID: util.MustParseUUID("70000000-0000-4000-8000-000000000307"),
		RunID:    run.RunID, RunAgentID: agent.RunAgentID, TurnID: firstTurn.TurnID,
		Kind: "message", CanonicalID: failedCanonical, ProducerCallID: "seg-failed-call",
		ActionOrdinal: 1, Status: "failed",
		CreatedAt: time.Date(2026, time.August, 12, 13, 0, 0, 0, time.UTC),
	}}, nil)

	segments, associations := listMixedRLProvisionalGraph(t, h, run.RunID)
	assert.Empty(t, segments, "failed actions must not create visible segments")
	assert.Empty(t, associations, "failed actions must leave the producer call unassigned")

	secondTurn := createMixedRLTurnWithID(t, h, run.RunID, agent, "70000000-0000-4000-8000-000000000308")
	successCanonical := util.MustParseUUID("70000000-0000-4000-8000-000000000309")
	seedMixedRLChannelMessage(t, h, successCanonical)
	acceptMixedRLTrustedCapture(t, h, run, agent, secondTurn, []ProviderCallInput{
		mixedRLProviderCallInput(run.RunID, agent, secondTurn, "seg-failed-followup", 2),
	}, []VisibleActionInput{
		mixedRLSucceededMessageAction(run.RunID, agent, secondTurn, successCanonical, "seg-failed-followup", 1),
	}, nil)

	segments, associations = listMixedRLProvisionalGraph(t, h, run.RunID)
	require.Len(t, segments, 1)
	assert.Equal(t, "message:"+successCanonical.String(), segments[0].SegmentID)
	assert.Equal(t, []string{"seg-failed-call", "seg-failed-followup"}, associationCallIDs(associations),
		"a later successful action must own the call that failed actions left unassigned")
}

func TestInteractionDAGSegment_EmptyVisibleActionsStillCreateSegment(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	turn := createMixedRLTurn(t, h, agent)
	advanceMixedRLRunToRunning(t, h, run.RunID)

	messageID := util.MustParseUUID("70000000-0000-4000-8000-00000000030a")
	seedMixedRLChannelMessage(t, h, messageID)
	acceptMixedRLTrustedCapture(t, h, run, agent, turn, nil, []VisibleActionInput{{
		ActionID: util.MustParseUUID("70000000-0000-4000-8000-00000000030b"),
		RunID:    run.RunID, RunAgentID: agent.RunAgentID, TurnID: turn.TurnID,
		Kind: "message", CanonicalID: messageID, ProducerCallID: "",
		ActionOrdinal: 1, Status: "succeeded",
		CreatedAt: time.Date(2026, time.August, 12, 13, 1, 0, 0, time.UTC),
	}}, nil)

	segments, associations := listMixedRLProvisionalGraph(t, h, run.RunID)
	require.Len(t, segments, 1, "successful action with no calls still has a segment")
	assert.Equal(t, "message:"+messageID.String(), segments[0].SegmentID)
	assert.Empty(t, associations, "empty-call segment must not invent call ownership")
}

func TestInteractionDAGSegment_FirstSuccessOwnerAndSiblingSharedProducer(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	turn := createMixedRLTurn(t, h, agent)
	advanceMixedRLRunToRunning(t, h, run.RunID)

	firstMessage := util.MustParseUUID("70000000-0000-4000-8000-00000000030c")
	secondMessage := util.MustParseUUID("70000000-0000-4000-8000-00000000030d")
	seedMixedRLChannelMessage(t, h, firstMessage)
	seedMixedRLChannelMessage(t, h, secondMessage)

	acceptMixedRLTrustedCapture(t, h, run, agent, turn, []ProviderCallInput{
		mixedRLProviderCallInput(run.RunID, agent, turn, "seg-shared-call", 1),
	}, []VisibleActionInput{
		mixedRLSucceededMessageAction(run.RunID, agent, turn, firstMessage, "seg-shared-call", 1),
		mixedRLSucceededMessageAction(run.RunID, agent, turn, secondMessage, "seg-shared-call", 2),
	}, nil)

	segments, associations := listMixedRLProvisionalGraph(t, h, run.RunID)
	require.Len(t, segments, 2)
	assert.Equal(t, "message:"+firstMessage.String(), segments[0].SegmentID)
	assert.Equal(t, "message:"+secondMessage.String(), segments[1].SegmentID)

	owned := associationsByKind(associations, "owned")
	shared := associationsByKind(associations, "shared_producer")
	require.Len(t, owned, 1, "first successful action must own the call")
	require.Len(t, shared, 1, "later sibling must reference the same call as shared_producer")
	assert.Equal(t, "message:"+firstMessage.String(), owned[0].SegmentID)
	assert.Equal(t, "message:"+secondMessage.String(), shared[0].SegmentID)
	assert.Equal(t, "seg-shared-call", owned[0].ProviderCallID)
	assert.Equal(t, "seg-shared-call", shared[0].ProviderCallID)
}

func TestInteractionDAGSegment_ReactionCreatesNoDeliveryAndNoWake(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	turn := createMixedRLTurn(t, h, agent)
	advanceMixedRLRunToRunning(t, h, run.RunID)

	reactedMessage := util.MustParseUUID("70000000-0000-4000-8000-00000000030e")
	reactionID := util.MustParseUUID("70000000-0000-4000-8000-00000000030f")
	seedMixedRLChannelMessage(t, h, reactedMessage)
	_, err := h.tx.Exec(h.ctx, "INSERT INTO channel_message_reaction (id) VALUES ($1)", reactionID)
	require.NoError(t, err)

	before, err := h.runs.GetRun(h.ctx, run.RunID)
	require.NoError(t, err)

	acceptMixedRLTrustedCapture(t, h, run, agent, turn, []ProviderCallInput{
		mixedRLProviderCallInput(run.RunID, agent, turn, "seg-reaction-call", 1),
	}, []VisibleActionInput{{
		ActionID: util.MustParseUUID("70000000-0000-4000-8000-000000000310"),
		RunID:    run.RunID, RunAgentID: agent.RunAgentID, TurnID: turn.TurnID,
		Kind: "reaction", CanonicalID: reactionID, ProducerCallID: "seg-reaction-call",
		ActionOrdinal: 1, Status: "succeeded",
		CreatedAt: time.Date(2026, time.August, 12, 13, 2, 0, 0, time.UTC),
	}}, nil)

	segments, associations := listMixedRLProvisionalGraph(t, h, run.RunID)
	require.Len(t, segments, 1)
	assert.Equal(t, "reaction:"+reactionID.String(), segments[0].SegmentID)
	assert.Equal(t, "reaction", segments[0].Kind)
	require.Len(t, associations, 1)
	assert.Equal(t, "owned", associations[0].AssociationKind)

	var deliveries int
	require.NoError(t, h.tx.QueryRow(h.ctx,
		`SELECT count(*) FROM env_dispatch_delivery_obligation WHERE run_id = $1`, run.RunID,
	).Scan(&deliveries))
	assert.Zero(t, deliveries, "reaction must not create delivery obligations")

	after, err := h.runs.GetRun(h.ctx, run.RunID)
	require.NoError(t, err)
	assert.Equal(t, before.PendingDeliveryCount, after.PendingDeliveryCount, "reaction must not wake pending-delivery accounting")
	assert.Equal(t, before.QueuedMessageCount, after.QueuedMessageCount, "reaction must not enqueue wake messages")
}

func TestInteractionDAGSegment_TrainOnceOwnedAssociationInvariant(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	turn := createMixedRLTurn(t, h, agent)
	advanceMixedRLRunToRunning(t, h, run.RunID)

	firstMessage := util.MustParseUUID("70000000-0000-4000-8000-000000000311")
	secondMessage := util.MustParseUUID("70000000-0000-4000-8000-000000000312")
	seedMixedRLChannelMessage(t, h, firstMessage)
	seedMixedRLChannelMessage(t, h, secondMessage)
	acceptMixedRLTrustedCapture(t, h, run, agent, turn, []ProviderCallInput{
		mixedRLProviderCallInput(run.RunID, agent, turn, "seg-train-once-call", 1),
	}, []VisibleActionInput{
		mixedRLSucceededMessageAction(run.RunID, agent, turn, firstMessage, "seg-train-once-call", 1),
		mixedRLSucceededMessageAction(run.RunID, agent, turn, secondMessage, "seg-train-once-call", 2),
	}, nil)

	_, associations := listMixedRLProvisionalGraph(t, h, run.RunID)
	ownedCount := 0
	trainable := make(map[string]int)
	for _, association := range associations {
		if association.ProviderCallID != "seg-train-once-call" {
			continue
		}
		if association.AssociationKind == "owned" {
			ownedCount++
			trainable[association.ProviderCallID]++
		}
	}
	assert.Equal(t, 1, ownedCount, "a provider call may be owned by at most one segment")
	assert.Equal(t, 1, trainable["seg-train-once-call"], "only the owned association may materialize training")

	var ownedRows int
	require.NoError(t, h.tx.QueryRow(h.ctx, `
		SELECT count(*) FROM interaction_dag_segment_provider_call
		WHERE provider_call_id = $1 AND association_kind = 'owned'`, "seg-train-once-call",
	).Scan(&ownedRows))
	assert.Equal(t, 1, ownedRows)
}

func seedMixedRLChannelMessage(t *testing.T, h mixedRLRepositoryHarness, messageID pgtype.UUID) {
	t.Helper()
	_, err := h.tx.Exec(h.ctx, "INSERT INTO channel_message (id) VALUES ($1) ON CONFLICT DO NOTHING", messageID)
	require.NoError(t, err)
}

func mixedRLSucceededMessageAction(
	runID pgtype.UUID,
	agent EnvDispatchRunAgentRecord,
	turn ResidentTurnRecord,
	canonicalID pgtype.UUID,
	producerCallID string,
	ordinal int64,
) VisibleActionInput {
	actionUUID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf(
		"mixed-rl-action:%s:%s:%d", turn.TurnID.String(), canonicalID.String(), ordinal,
	)))
	return VisibleActionInput{
		ActionID: pgtype.UUID{Bytes: actionUUID, Valid: true},
		RunID:    runID, RunAgentID: agent.RunAgentID, TurnID: turn.TurnID,
		Kind: "message", CanonicalID: canonicalID, ProducerCallID: producerCallID,
		ActionOrdinal: ordinal, Status: "succeeded",
		CreatedAt: time.Date(2026, time.August, 12, 13, int(ordinal), 0, 0, time.UTC),
	}
}

func acceptMixedRLTrustedCapture(
	t *testing.T,
	h mixedRLRepositoryHarness,
	run EnvDispatchRunRecord,
	agent EnvDispatchRunAgentRecord,
	turn ResidentTurnRecord,
	calls []ProviderCallInput,
	actions []VisibleActionInput,
	consumptions []MessageConsumptionInput,
) {
	t.Helper()
	batchUUID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("mixed-rl-capture-batch:"+turn.TurnID.String()))
	capture := TrustedTurnCapture{
		RunID: run.RunID, RunAgentID: agent.RunAgentID, TurnID: turn.TurnID, TurnOrdinal: turn.TurnOrdinal,
		Batch: TurnCaptureBatchInput{
			CaptureBatchID: pgtype.UUID{Bytes: batchUUID, Valid: true},
			TurnID:         turn.TurnID, CaptureBoundary: agent.CaptureBoundary,
			CallCount: int32(len(calls)), ActionCount: int32(len(actions)), ConsumptionCount: int32(len(consumptions)),
			PayloadHash: "sha256:segment-batch-" + turn.TurnID.String(),
		},
		Calls: calls, Actions: actions, Consumptions: consumptions,
		CompletedAt: time.Date(2026, time.August, 12, 14, int(turn.TurnOrdinal), 0, 0, time.UTC),
	}
	_, err := NewProviderCaptureService(h.runs.queries, h.tx).AcceptTrustedTurnCapture(h.ctx, capture)
	require.NoError(t, err)
}

func listMixedRLProvisionalGraph(t *testing.T, h mixedRLRepositoryHarness, runID pgtype.UUID) ([]db.InteractionDagRunSegment, []db.InteractionDagSegmentProviderCall) {
	t.Helper()
	segments, err := h.runs.queries.ListMixedRLRunSegmentsCanonical(h.ctx, runID)
	require.NoError(t, err)
	associations, err := h.runs.queries.ListMixedRLSegmentCallsCanonical(h.ctx, runID)
	require.NoError(t, err)
	return segments, associations
}

func associationCallIDs(associations []db.InteractionDagSegmentProviderCall) []string {
	ids := make([]string, 0, len(associations))
	for _, association := range associations {
		ids = append(ids, association.ProviderCallID)
	}
	return ids
}

func associationsByKind(associations []db.InteractionDagSegmentProviderCall, kind string) []db.InteractionDagSegmentProviderCall {
	matched := make([]db.InteractionDagSegmentProviderCall, 0)
	for _, association := range associations {
		if association.AssociationKind == kind {
			matched = append(matched, association)
		}
	}
	return matched
}
