// SPDX-License-Identifier: Apache-2.0

package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/util"
)

func TestInteractionDAGEdge_AcceptanceAndCheckConsumptionCreateChannelMessageEdges(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	producer := bindMixedRLAgent(t, h, 2, "offline_rl")
	consumer := bindMixedRLAgentForRun(t, h, run.RunID, 3, "none")
	producerTurn := createMixedRLTurnWithID(t, h, run.RunID, producer, "70000000-0000-4000-8000-000000000401")
	consumerTurn := createMixedRLTurnWithID(t, h, run.RunID, consumer, "70000000-0000-4000-8000-000000000402")
	advanceMixedRLRunToRunning(t, h, run.RunID)

	triggerMessage := util.MustParseUUID("70000000-0000-4000-8000-000000000403")
	seedMixedRLChannelMessage(t, h, triggerMessage)

	acceptMixedRLTrustedCapture(t, h, run, producer, producerTurn, []ProviderCallInput{
		mixedRLProviderCallInput(run.RunID, producer, producerTurn, "edge-src-call", 1),
	}, []VisibleActionInput{
		mixedRLSucceededMessageAction(run.RunID, producer, producerTurn, triggerMessage, "edge-src-call", 1),
	}, nil)

	checkMessage := util.MustParseUUID("70000000-0000-4000-8000-000000000404")
	seedMixedRLChannelMessage(t, h, checkMessage)
	acceptCall := mixedRLProviderCallInput(run.RunID, consumer, consumerTurn, "edge-dst-accept-call", 1)
	checkCall := mixedRLProviderCallInput(run.RunID, consumer, consumerTurn, "edge-dst-check-call", 2)
	acceptMixedRLTrustedCapture(t, h, run, consumer, consumerTurn, []ProviderCallInput{acceptCall, checkCall},
		[]VisibleActionInput{
			mixedRLSucceededMessageAction(run.RunID, consumer, consumerTurn, checkMessage, "edge-dst-check-call", 1),
		},
		[]MessageConsumptionInput{
			{
				ConsumptionID: util.MustParseUUID("70000000-0000-4000-8000-000000000405"),
				RunID:         run.RunID, RunAgentID: consumer.RunAgentID, TurnID: consumerTurn.TurnID,
				ChannelMessageID: triggerMessage, Source: "accept_message_batch",
				EffectiveFromCallID: "edge-dst-accept-call",
				ConsumedAt:          acceptCall.StartedAt.Add(-time.Second),
			},
			{
				ConsumptionID: util.MustParseUUID("70000000-0000-4000-8000-000000000406"),
				RunID:         run.RunID, RunAgentID: consumer.RunAgentID, TurnID: consumerTurn.TurnID,
				ChannelMessageID: triggerMessage, Source: "message_check",
				EffectiveFromCallID: "edge-dst-check-call",
				ConsumedAt:          checkCall.StartedAt.Add(-time.Second),
			},
		},
	)

	advanceMixedRLRunToQuietCandidate(t, h, run.RunID)
	result, err := NewMixedRLFreezeService(h.runs.queries, h.tx).Freeze(h.ctx, run.RunID, false)
	require.NoError(t, err)
	dag, err := h.ledger.GetFrozenDAG(h.ctx, run.RunID, result.Snapshot.SnapshotID)
	require.NoError(t, err)

	channelEdges := edgesByType(dag.Edges, "channel_message")
	require.Len(t, channelEdges, 2, "acceptance and message_check must each materialize a channel_message edge")
	assert.ElementsMatch(t,
		[]string{"edge-dst-accept-call", "edge-dst-check-call"},
		[]string{channelEdges[0].DestinationCallID, channelEdges[1].DestinationCallID},
	)
	for _, edge := range channelEdges {
		assert.Equal(t, "message:"+triggerMessage.String(), edge.SourceSegmentID)
		assert.True(t, edge.TriggerMessageID.Valid)
		assert.Equal(t, triggerMessage, edge.TriggerMessageID)
		assert.NotEmpty(t, edge.DestinationCallID)
	}
}

func TestInteractionDAGEdge_DeliveryAloneDoesNotCreateEdge(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	producer := bindMixedRLAgent(t, h, 2, "offline_rl")
	consumer := bindMixedRLAgentForRun(t, h, run.RunID, 3, "none")
	producerTurn := createMixedRLTurnWithID(t, h, run.RunID, producer, "70000000-0000-4000-8000-000000000411")
	consumerTurn := createMixedRLTurnWithID(t, h, run.RunID, consumer, "70000000-0000-4000-8000-000000000412")
	advanceMixedRLRunToRunning(t, h, run.RunID)

	delivered := util.MustParseUUID("70000000-0000-4000-8000-000000000413")
	consumed := util.MustParseUUID("70000000-0000-4000-8000-000000000416")
	seedMixedRLChannelMessage(t, h, delivered)
	seedMixedRLChannelMessage(t, h, consumed)
	acceptMixedRLTrustedCapture(t, h, run, producer, producerTurn, []ProviderCallInput{
		mixedRLProviderCallInput(run.RunID, producer, producerTurn, "edge-delivery-src", 1),
		mixedRLProviderCallInput(run.RunID, producer, producerTurn, "edge-consumed-src", 2),
	}, []VisibleActionInput{
		mixedRLSucceededMessageAction(run.RunID, producer, producerTurn, delivered, "edge-delivery-src", 1),
		mixedRLSucceededMessageAction(run.RunID, producer, producerTurn, consumed, "edge-consumed-src", 2),
	}, nil)

	consumerReply := util.MustParseUUID("70000000-0000-4000-8000-000000000414")
	seedMixedRLChannelMessage(t, h, consumerReply)
	dstCall := mixedRLProviderCallInput(run.RunID, consumer, consumerTurn, "edge-delivery-dst", 1)
	acceptMixedRLTrustedCapture(t, h, run, consumer, consumerTurn, []ProviderCallInput{dstCall},
		[]VisibleActionInput{
			mixedRLSucceededMessageAction(run.RunID, consumer, consumerTurn, consumerReply, "edge-delivery-dst", 1),
		},
		[]MessageConsumptionInput{{
			ConsumptionID: util.MustParseUUID("70000000-0000-4000-8000-000000000417"),
			RunID:         run.RunID, RunAgentID: consumer.RunAgentID, TurnID: consumerTurn.TurnID,
			ChannelMessageID: consumed, Source: "message_check",
			EffectiveFromCallID: "edge-delivery-dst",
			ConsumedAt:          dstCall.StartedAt.Add(-time.Second),
		}},
	)

	deliveryID := util.MustParseUUID("70000000-0000-4000-8000-000000000415")
	_, err := h.runs.CreateDeliveryObligation(h.ctx, CreateDeliveryObligationInput{
		DeliveryID: deliveryID,
		RunID:      run.RunID, ChannelMessageID: delivered,
		SourceRecipientAgentID: consumer.SourceAgentID,
		RunAgentID:             consumer.RunAgentID,
		State:                  "pending",
	})
	require.NoError(t, err)
	// Settle so quiet_candidate activity invariants hold; the obligation row
	// remains as evidence that delivery alone does not create a causal edge.
	_, err = h.runs.SettleDeliveryObligation(h.ctx, deliveryID, "completed", time.Now().UTC())
	require.NoError(t, err)

	advanceMixedRLRunToQuietCandidate(t, h, run.RunID)
	result, err := NewMixedRLFreezeService(h.runs.queries, h.tx).Freeze(h.ctx, run.RunID, false)
	require.NoError(t, err)
	dag, err := h.ledger.GetFrozenDAG(h.ctx, run.RunID, result.Snapshot.SnapshotID)
	require.NoError(t, err)

	channelEdges := edgesByType(dag.Edges, "channel_message")
	require.Len(t, channelEdges, 1, "only concrete consumption evidence creates a channel_message edge")
	assert.Equal(t, consumed, channelEdges[0].TriggerMessageID)
	assert.NotEqual(t, delivered, channelEdges[0].TriggerMessageID,
		"delivery obligation alone must not create a causal edge")
}

func TestInteractionDAGEdge_PreciseDestinationCallInMultiCallSegment(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	producer := bindMixedRLAgent(t, h, 2, "offline_rl")
	consumer := bindMixedRLAgentForRun(t, h, run.RunID, 3, "offline_rl")
	producerTurn := createMixedRLTurnWithID(t, h, run.RunID, producer, "70000000-0000-4000-8000-000000000421")
	consumerTurn := createMixedRLTurnWithID(t, h, run.RunID, consumer, "70000000-0000-4000-8000-000000000422")
	advanceMixedRLRunToRunning(t, h, run.RunID)

	trigger := util.MustParseUUID("70000000-0000-4000-8000-000000000423")
	seedMixedRLChannelMessage(t, h, trigger)
	acceptMixedRLTrustedCapture(t, h, run, producer, producerTurn, []ProviderCallInput{
		mixedRLProviderCallInput(run.RunID, producer, producerTurn, "edge-precise-src", 1),
	}, []VisibleActionInput{
		mixedRLSucceededMessageAction(run.RunID, producer, producerTurn, trigger, "edge-precise-src", 1),
	}, nil)

	earlyCall := mixedRLProviderCallInput(run.RunID, consumer, consumerTurn, "edge-precise-early", 1)
	lateCall := mixedRLProviderCallInput(run.RunID, consumer, consumerTurn, "edge-precise-late", 2)
	lateCall.StartedAt = earlyCall.StartedAt.Add(2 * time.Second)
	lateCall.CompletedAt = lateCall.StartedAt.Add(time.Second)
	reply := util.MustParseUUID("70000000-0000-4000-8000-000000000424")
	seedMixedRLChannelMessage(t, h, reply)
	acceptMixedRLTrustedCapture(t, h, run, consumer, consumerTurn, []ProviderCallInput{earlyCall, lateCall},
		[]VisibleActionInput{
			mixedRLSucceededMessageAction(run.RunID, consumer, consumerTurn, reply, "edge-precise-late", 1),
		},
		[]MessageConsumptionInput{{
			ConsumptionID: util.MustParseUUID("70000000-0000-4000-8000-000000000425"),
			RunID:         run.RunID, RunAgentID: consumer.RunAgentID, TurnID: consumerTurn.TurnID,
			ChannelMessageID: trigger, Source: "message_check",
			EffectiveFromCallID: "edge-precise-late",
			ConsumedAt:          lateCall.StartedAt.Add(-time.Second),
		}},
	)

	advanceMixedRLRunToQuietCandidate(t, h, run.RunID)
	result, err := NewMixedRLFreezeService(h.runs.queries, h.tx).Freeze(h.ctx, run.RunID, false)
	require.NoError(t, err)
	dag, err := h.ledger.GetFrozenDAG(h.ctx, run.RunID, result.Snapshot.SnapshotID)
	require.NoError(t, err)

	channelEdges := edgesByType(dag.Edges, "channel_message")
	require.Len(t, channelEdges, 1)
	assert.Equal(t, "edge-precise-late", channelEdges[0].DestinationCallID,
		"dst_call_id must target the first affected call, not an earlier call in the destination segment")
	assert.NotEqual(t, "edge-precise-early", channelEdges[0].DestinationCallID)
}

func TestInteractionDAGEdge_ReactionSourceIsReactedMessageProducer(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	producer := bindMixedRLAgent(t, h, 2, "offline_rl")
	reactor := bindMixedRLAgentForRun(t, h, run.RunID, 3, "offline_rl")
	producerTurn := createMixedRLTurnWithID(t, h, run.RunID, producer, "70000000-0000-4000-8000-000000000431")
	reactorTurn := createMixedRLTurnWithID(t, h, run.RunID, reactor, "70000000-0000-4000-8000-000000000432")
	advanceMixedRLRunToRunning(t, h, run.RunID)

	messageID := util.MustParseUUID("70000000-0000-4000-8000-000000000433")
	reactionID := util.MustParseUUID("70000000-0000-4000-8000-000000000434")
	seedMixedRLChannelMessage(t, h, messageID)
	_, err := h.tx.Exec(h.ctx, "INSERT INTO channel_message_reaction (id, channel_message_id) VALUES ($1, $2)", reactionID, messageID)
	require.NoError(t, err)

	acceptMixedRLTrustedCapture(t, h, run, producer, producerTurn, []ProviderCallInput{
		mixedRLProviderCallInput(run.RunID, producer, producerTurn, "edge-reaction-src", 1),
	}, []VisibleActionInput{
		mixedRLSucceededMessageAction(run.RunID, producer, producerTurn, messageID, "edge-reaction-src", 1),
	}, nil)

	actionUUID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("mixed-rl-reaction-action:"+reactionID.String()))
	acceptMixedRLTrustedCapture(t, h, run, reactor, reactorTurn, []ProviderCallInput{
		mixedRLProviderCallInput(run.RunID, reactor, reactorTurn, "edge-reaction-call", 1),
	}, []VisibleActionInput{{
		ActionID: pgtype.UUID{Bytes: actionUUID, Valid: true},
		RunID:    run.RunID, RunAgentID: reactor.RunAgentID, TurnID: reactorTurn.TurnID,
		Kind: "reaction", CanonicalID: reactionID, ProducerCallID: "edge-reaction-call",
		ActionOrdinal: 1, Status: "succeeded",
		CreatedAt: time.Date(2026, time.August, 12, 13, 30, 0, 0, time.UTC),
	}}, nil)

	advanceMixedRLRunToQuietCandidate(t, h, run.RunID)
	result, err := NewMixedRLFreezeService(h.runs.queries, h.tx).Freeze(h.ctx, run.RunID, false)
	require.NoError(t, err)
	dag, err := h.ledger.GetFrozenDAG(h.ctx, run.RunID, result.Snapshot.SnapshotID)
	require.NoError(t, err)

	reactionEdges := edgesByType(dag.Edges, "reaction")
	require.Len(t, reactionEdges, 1)
	assert.Equal(t, "message:"+messageID.String(), reactionEdges[0].SourceSegmentID,
		"reaction edge source must be the reacted message's producer segment")
	assert.Equal(t, "reaction:"+reactionID.String(), reactionEdges[0].DestinationSegmentID)
	assert.Equal(t, messageID, reactionEdges[0].TriggerMessageID)
	assert.Empty(t, edgesByType(dag.Edges, "channel_message"),
		"reaction must not invent a delivery/channel_message edge")
}

func TestInteractionDAGEdge_SessionContinuationPositiveDedupAndNegativeRules(t *testing.T) {
	h := newMixedRLRepositoryHarness(t)
	run := createMixedRLRun(t, h)
	agent := bindMixedRLAgent(t, h, 2, "offline_rl")
	firstTurn := createMixedRLTurnWithID(t, h, run.RunID, agent, "70000000-0000-4000-8000-000000000441")
	secondTurn := createMixedRLTurnWithID(t, h, run.RunID, agent, "70000000-0000-4000-8000-000000000442")
	advanceMixedRLRunToRunning(t, h, run.RunID)

	firstMessage := util.MustParseUUID("70000000-0000-4000-8000-000000000443")
	secondMessage := util.MustParseUUID("70000000-0000-4000-8000-000000000444")
	seedMixedRLChannelMessage(t, h, firstMessage)
	seedMixedRLChannelMessage(t, h, secondMessage)

	acceptMixedRLTrustedCapture(t, h, run, agent, firstTurn, []ProviderCallInput{
		mixedRLProviderCallInput(run.RunID, agent, firstTurn, "edge-cont-call-1", 1),
	}, []VisibleActionInput{
		mixedRLSucceededMessageAction(run.RunID, agent, firstTurn, firstMessage, "edge-cont-call-1", 1),
	}, nil)

	secondFirst := mixedRLProviderCallInput(run.RunID, agent, secondTurn, "edge-cont-call-2", 2)
	secondLater := mixedRLProviderCallInput(run.RunID, agent, secondTurn, "edge-cont-call-3", 3)
	acceptMixedRLTrustedCapture(t, h, run, agent, secondTurn, []ProviderCallInput{secondFirst, secondLater},
		[]VisibleActionInput{
			mixedRLSucceededMessageAction(run.RunID, agent, secondTurn, secondMessage, "edge-cont-call-2", 1),
		}, nil)

	advanceMixedRLRunToQuietCandidate(t, h, run.RunID)
	result, err := NewMixedRLFreezeService(h.runs.queries, h.tx).Freeze(h.ctx, run.RunID, false)
	require.NoError(t, err)
	dag, err := h.ledger.GetFrozenDAG(h.ctx, run.RunID, result.Snapshot.SnapshotID)
	require.NoError(t, err)

	continuations := edgesByType(dag.Edges, "session_continuation")
	require.Len(t, continuations, 1, "same-session adjacent owned calls across segments create one continuation edge")
	assert.Equal(t, "message:"+firstMessage.String(), continuations[0].SourceSegmentID)
	assert.Equal(t, "message:"+secondMessage.String(), continuations[0].DestinationSegmentID)
	assert.Equal(t, "edge-cont-call-2", continuations[0].DestinationCallID,
		"dedup keeps the earliest qualifying destination call")
	assert.False(t, continuations[0].TriggerMessageID.Valid)

	// Negative: empty-call segment and terminal sequencing do not invent continuation edges.
	otherRunID := util.MustParseUUID("70000000-0000-4000-8000-000000000450")
	otherProjectID := util.MustParseUUID("70000000-0000-4000-8000-000000000451")
	otherRun := createMixedRLRunWithIDs(t, h, otherRunID, otherProjectID)
	otherAgent := bindMixedRLAgentForRun(t, h, otherRun.RunID, 4, "offline_rl")
	emptyTurn := createMixedRLTurnWithID(t, h, otherRun.RunID, otherAgent, "70000000-0000-4000-8000-000000000452")
	orphanTurn := createMixedRLTurnWithID(t, h, otherRun.RunID, otherAgent, "70000000-0000-4000-8000-000000000453")
	advanceMixedRLRunToRunning(t, h, otherRun.RunID)
	emptyMessage := util.MustParseUUID("70000000-0000-4000-8000-000000000454")
	seedMixedRLChannelMessage(t, h, emptyMessage)
	acceptMixedRLTrustedCapture(t, h, otherRun, otherAgent, emptyTurn, nil, []VisibleActionInput{{
		ActionID: util.MustParseUUID("70000000-0000-4000-8000-000000000455"),
		RunID:    otherRun.RunID, RunAgentID: otherAgent.RunAgentID, TurnID: emptyTurn.TurnID,
		Kind: "message", CanonicalID: emptyMessage, ProducerCallID: "",
		ActionOrdinal: 1, Status: "succeeded",
		CreatedAt: time.Date(2026, time.August, 12, 13, 40, 0, 0, time.UTC),
	}}, nil)
	acceptMixedRLTrustedCapture(t, h, otherRun, otherAgent, orphanTurn, []ProviderCallInput{
		mixedRLProviderCallInput(otherRun.RunID, otherAgent, orphanTurn, "edge-cont-orphan", 1),
	}, nil, nil)
	advanceMixedRLRunToQuietCandidate(t, h, otherRun.RunID)
	otherResult, err := NewMixedRLFreezeService(h.runs.queries, h.tx).Freeze(h.ctx, otherRun.RunID, false)
	require.NoError(t, err)
	otherDAG, err := h.ledger.GetFrozenDAG(h.ctx, otherRun.RunID, otherResult.Snapshot.SnapshotID)
	require.NoError(t, err)
	assert.Empty(t, edgesByType(otherDAG.Edges, "session_continuation"),
		"no continuation within one segment, for segments with no owned call, or from terminal persistence order")
}

func edgesByType(edges []CausalEdgeRecord, edgeType string) []CausalEdgeRecord {
	matched := make([]CausalEdgeRecord, 0)
	for _, edge := range edges {
		if edge.Type == edgeType {
			matched = append(matched, edge)
		}
	}
	return matched
}
