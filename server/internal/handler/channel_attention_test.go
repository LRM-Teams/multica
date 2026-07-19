package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestChannelAttentionMessageClassification(t *testing.T) {
	enabled := &Handler{cfg: Config{
		ChannelAttentionEnabled: true,
		ChannelUnmentionedMode:  channelUnmentionedModeAttentionRound,
	}}
	group := ChannelResponse{Kind: "group"}
	dm := ChannelResponse{Kind: "dm"}
	agentMention := []protocol.MessagePart{{
		Type:       protocol.MessagePartTypeReference,
		RefType:    "mention",
		RefSubType: "agent",
		RefID:      uuid.NewString(),
	}}
	allMention := []protocol.MessagePart{{
		Type:       protocol.MessagePartTypeReference,
		RefType:    "mention",
		RefSubType: "all",
	}}

	tests := []struct {
		name    string
		handler *Handler
		channel ChannelResponse
		content string
		parts   []protocol.MessagePart
		want    bool
	}{
		{name: "human group message", handler: enabled, channel: group, content: "Please compare the two proposals", want: true},
		{name: "structured agent mention", handler: enabled, channel: group, content: "Please review", parts: agentMention, want: false},
		{name: "structured all mention", handler: enabled, channel: group, content: "Please review", parts: allMention, want: false},
		{name: "plain all command", handler: enabled, channel: group, content: "@all please review", want: false},
		{name: "localized group command", handler: enabled, channel: group, content: "大家请同步进展", want: false},
		{name: "direct message", handler: enabled, channel: dm, content: "Please compare the proposals", want: false},
		{name: "disabled", handler: &Handler{cfg: Config{ChannelAttentionEnabled: false, ChannelUnmentionedMode: channelUnmentionedModeAttentionRound}}, channel: group, content: "Please compare", want: false},
		{name: "legacy rollback", handler: &Handler{cfg: Config{ChannelAttentionEnabled: true, ChannelUnmentionedMode: channelUnmentionedModeLegacyFull}}, channel: group, content: "Please compare", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.handler.shouldQueueChannelAttention(tt.channel, tt.content, tt.parts); got != tt.want {
				t.Fatalf("shouldQueueChannelAttention() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseChannelAttentionDecisionStrict(t *testing.T) {
	valid := `{"decision":"ANSWER","confidence":0.75,"value_type":"direct_answer","summary":"use the indexed result","evidence_refs":["message:7"],"model_version":"probe-model","seen_up_to_seq":7}`
	got, err := parseChannelAttentionDecision(json.RawMessage(valid))
	if err != nil {
		t.Fatalf("valid decision rejected: %v", err)
	}
	if got.Decision != "ANSWER" || got.Confidence != 0.75 || got.ValueType != "direct_answer" || got.SeenUpToSeq != 7 {
		t.Fatalf("parsed decision = %+v", got)
	}

	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ``},
		{name: "not object", raw: `[]`},
		{name: "unknown field", raw: `{"decision":"SILENT","confidence":1,"value_type":"none","summary":"","evidence_refs":[],"model_version":"m","seen_up_to_seq":1,"extra":true}`},
		{name: "missing field", raw: `{"decision":"SILENT","confidence":1,"value_type":"none","summary":"","evidence_refs":[],"model_version":"m"}`},
		{name: "null array", raw: `{"decision":"SILENT","confidence":1,"value_type":"none","summary":"","evidence_refs":null,"model_version":"m","seen_up_to_seq":1}`},
		{name: "trailing value", raw: valid + ` {}`},
		{name: "bad decision", raw: `{"decision":"PUBLISH","confidence":1,"value_type":"none","summary":"","evidence_refs":[],"model_version":"m","seen_up_to_seq":1}`},
		{name: "bad confidence", raw: `{"decision":"SILENT","confidence":1.01,"value_type":"none","summary":"","evidence_refs":[],"model_version":"m","seen_up_to_seq":1}`},
		{name: "bad value type", raw: `{"decision":"SILENT","confidence":1,"value_type":"opinion","summary":"","evidence_refs":[],"model_version":"m","seen_up_to_seq":1}`},
		{name: "empty model", raw: `{"decision":"SILENT","confidence":1,"value_type":"none","summary":"","evidence_refs":[],"model_version":" ","seen_up_to_seq":1}`},
		{name: "negative sequence", raw: `{"decision":"SILENT","confidence":1,"value_type":"none","summary":"","evidence_refs":[],"model_version":"m","seen_up_to_seq":-1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseChannelAttentionDecision(json.RawMessage(tt.raw)); err == nil {
				t.Fatalf("parseChannelAttentionDecision(%s) unexpectedly succeeded", tt.raw)
			}
		})
	}
}

func TestParseChannelAttentionConvergenceVoteStrict(t *testing.T) {
	valid := `{"vote":"MERGE","target_agent_id":"` + uuid.NewString() + `","summary":"please include my benchmark result"}`
	got, err := parseChannelAttentionConvergenceVote(json.RawMessage(valid))
	if err != nil {
		t.Fatalf("valid convergence vote rejected: %v", err)
	}
	if got.Vote != "MERGE" || got.Summary != "please include my benchmark result" || got.TargetAgentID == "" {
		t.Fatalf("parsed convergence vote = %+v", got)
	}

	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ``},
		{name: "not object", raw: `[]`},
		{name: "unknown field", raw: `{"vote":"YIELD","target_agent_id":"","summary":"","extra":true}`},
		{name: "missing field", raw: `{"vote":"YIELD","summary":""}`},
		{name: "bad vote", raw: `{"vote":"ANSWER","target_agent_id":"","summary":""}`},
		{name: "bad target", raw: `{"vote":"MERGE","target_agent_id":"agent-a","summary":""}`},
		{name: "trailing value", raw: `{"vote":"KEEP","target_agent_id":"","summary":""} {}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseChannelAttentionConvergenceVote(json.RawMessage(tt.raw)); err == nil {
				t.Fatalf("parseChannelAttentionConvergenceVote(%s) unexpectedly succeeded", tt.raw)
			}
		})
	}
}

func TestChannelAttentionHumanUnmentionedCreatesProbeRound(t *testing.T) {
	fixture := newChannelAttentionFixture(t, []attentionRuntimeSpec{{}, {}})
	trigger := fixture.insertMessage(t, "user", testUserID, "Please compare the release risks", nil)
	fixture.handler.dispatchChannelMessageToAgents(context.Background(), fixture.channel, trigger, parseUUID(testUserID))

	var rounds, participants, attentionEvents, fullEvents int
	if err := testPool.QueryRow(context.Background(), `
		SELECT
		  count(DISTINCT round.id)::int,
		  count(DISTINCT participant.id)::int,
		  count(DISTINCT event.id) FILTER (
		    WHERE event.delivery_mode = 'attention'
		      AND event.response_mode = 'no_public_output'
		      AND event.requires_wake
		  )::int,
		  count(DISTINCT event.id) FILTER (
		    WHERE event.delivery_mode = 'execute'
		      AND event.requires_wake
		  )::int
		FROM channel_attention_round round
		LEFT JOIN channel_attention_participant participant ON participant.round_id = round.id
		LEFT JOIN agent_inbox_event event ON event.id = participant.inbox_event_id
		WHERE round.channel_id = $1`, fixture.channel.ID).Scan(&rounds, &participants, &attentionEvents, &fullEvents); err != nil {
		t.Fatalf("inspect attention round: %v", err)
	}
	if rounds != 1 || participants != 2 || attentionEvents != 2 || fullEvents != 0 {
		t.Fatalf("rounds=%d participants=%d attention=%d full=%d, want 1/2/2/0", rounds, participants, attentionEvents, fullEvents)
	}
}

func TestChannelAttentionSixteenAgentsCreateOneRoundWithoutFullExecution(t *testing.T) {
	specs := make([]attentionRuntimeSpec, 16)
	fixture := newChannelAttentionFixture(t, specs)
	trigger := fixture.insertMessage(t, "user", testUserID, "Compare the release risks", nil)
	fixture.handler.dispatchChannelMessageToAgents(context.Background(), fixture.channel, trigger, parseUUID(testUserID))

	var rounds, participants, probes, full int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(DISTINCT round.id)::int,
		       count(DISTINCT participant.id)::int,
		       count(DISTINCT event.id) FILTER (WHERE event.delivery_mode = 'attention')::int,
		       count(DISTINCT event.id) FILTER (WHERE event.delivery_mode = 'execute' AND event.requires_wake)::int
		FROM channel_attention_round round
		LEFT JOIN channel_attention_participant participant ON participant.round_id = round.id
		LEFT JOIN agent_inbox_event event ON event.id = participant.inbox_event_id
		WHERE round.channel_id = $1`, fixture.channel.ID).Scan(&rounds, &participants, &probes, &full); err != nil {
		t.Fatalf("inspect 16-agent round: %v", err)
	}
	if rounds != 1 || participants != 16 || probes != 16 || full != 0 {
		t.Fatalf("rounds=%d participants=%d probes=%d full=%d, want 1/16/16/0", rounds, participants, probes, full)
	}
}

func TestChannelAttentionDebounceMergesSequenceRange(t *testing.T) {
	fixture := newChannelAttentionFixture(t, []attentionRuntimeSpec{{}, {}})
	first := fixture.insertMessage(t, "user", testUserID, "First related update", nil)
	fixture.handler.dispatchChannelMessageToAgents(context.Background(), fixture.channel, first, parseUUID(testUserID))
	second := fixture.insertMessage(t, "user", testUserID, "Second related update", nil)
	fixture.handler.dispatchChannelMessageToAgents(context.Background(), fixture.channel, second, parseUUID(testUserID))

	var rounds int
	var seqFrom, seqTo int64
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)::int, min(seq_from), max(seq_to)
		FROM channel_attention_round WHERE channel_id = $1`, fixture.channel.ID).Scan(&rounds, &seqFrom, &seqTo); err != nil {
		t.Fatalf("inspect merged round: %v", err)
	}
	if rounds != 1 || seqFrom != first.Seq || seqTo != second.Seq {
		t.Fatalf("rounds=%d seq=%d..%d, want 1/%d..%d", rounds, seqFrom, seqTo, first.Seq, second.Seq)
	}
}

func TestLarkBareAgentMentionDoesNotQueueAttentionRound(t *testing.T) {
	fixture := newChannelAttentionFixture(t, []attentionRuntimeSpec{{}, {}})
	larkChatID := "oc_" + uuid.NewString()
	if _, err := testPool.Exec(context.Background(), `UPDATE channel SET lark_chat_id = $1 WHERE id = $2`, larkChatID, fixture.channel.ID); err != nil {
		t.Fatalf("set lark chat id: %v", err)
	}
	content := "@" + fixture.agentNames[0] + " please review"
	req := newRequestAs(testUserID, http.MethodPost, "/api/lark/channel-messages/import", ImportLarkChannelMessageRequest{
		LarkChatID: larkChatID,
		AuthorName: "Feishu user",
		Content:    content,
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	rec := httptest.NewRecorder()
	fixture.handler.ImportLarkChannelMessage(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("lark import: status=%d body=%s", rec.Code, rec.Body.String())
	}

	var rounds, targetFull, probes int
	if err := testPool.QueryRow(context.Background(), `
		SELECT
		  (SELECT count(*) FROM channel_attention_round WHERE channel_id = $1)::int,
		  count(*) FILTER (WHERE agent_id = $2 AND delivery_mode = 'execute' AND requires_wake)::int,
		  count(*) FILTER (WHERE delivery_mode = 'attention')::int
		FROM agent_inbox_event WHERE channel_id = $1`, fixture.channel.ID, fixture.agentIDs[0]).Scan(&rounds, &targetFull, &probes); err != nil {
		t.Fatalf("inspect lark dispatch: %v", err)
	}
	if rounds != 0 || targetFull != 1 || probes != 0 {
		t.Fatalf("rounds=%d target_full=%d probes=%d, want 0/1/0", rounds, targetFull, probes)
	}
}

func TestChannelAttentionExplicitMentionExecutesOnlyTarget(t *testing.T) {
	fixture := newChannelAttentionFixture(t, []attentionRuntimeSpec{{}, {}})
	targetID := fixture.agentIDs[0]
	parts := []protocol.MessagePart{{
		Type:       protocol.MessagePartTypeReference,
		RefType:    "mention",
		RefSubType: "agent",
		RefID:      targetID,
		Label:      "@" + fixture.agentNames[0],
	}}
	trigger := fixture.insertMessage(t, "user", testUserID, "Please review this change", parts)
	fixture.handler.dispatchChannelMessageToAgents(context.Background(), fixture.channel, trigger, parseUUID(testUserID))

	var roundCount, probeCount, targetFull, otherFull int
	if err := testPool.QueryRow(context.Background(), `
		SELECT
		  (SELECT count(*) FROM channel_attention_round WHERE channel_id = $1)::int,
		  (SELECT count(*) FROM channel_attention_participant participant
		     JOIN channel_attention_round round ON round.id = participant.round_id
		    WHERE round.channel_id = $1)::int,
		  count(*) FILTER (WHERE agent_id = $2 AND delivery_mode = 'execute' AND response_mode = 'public_response' AND requires_wake)::int,
		  count(*) FILTER (WHERE agent_id = $3 AND delivery_mode = 'execute' AND requires_wake)::int
		FROM agent_inbox_event
		WHERE channel_id = $1`, fixture.channel.ID, targetID, fixture.agentIDs[1]).Scan(&roundCount, &probeCount, &targetFull, &otherFull); err != nil {
		t.Fatalf("inspect explicit mention dispatch: %v", err)
	}
	if roundCount != 0 || probeCount != 0 || targetFull != 1 || otherFull != 0 {
		t.Fatalf("rounds=%d probes=%d target_full=%d other_full=%d, want 0/0/1/0", roundCount, probeCount, targetFull, otherFull)
	}
}

func TestChannelAttentionAgentAndSystemMessagesObserveOnly(t *testing.T) {
	fixture := newChannelAttentionFixture(t, []attentionRuntimeSpec{{}, {}})
	for _, author := range []struct {
		typeName string
		id       string
	}{
		{typeName: "agent", id: fixture.agentIDs[0]},
		{typeName: "system"},
	} {
		trigger := fixture.insertMessage(t, author.typeName, author.id, "Status changed; retain this context", nil)
		fixture.handler.dispatchChannelMessageToAgents(context.Background(), fixture.channel, trigger, pgtype.UUID{})
	}

	var rounds, full, observe int
	if err := testPool.QueryRow(context.Background(), `
		SELECT
		  (SELECT count(*) FROM channel_attention_round WHERE channel_id = $1)::int,
		  count(*) FILTER (WHERE requires_wake OR delivery_mode = 'execute')::int,
		  count(*) FILTER (WHERE delivery_mode = 'observe' AND response_mode = 'no_public_output' AND NOT requires_wake)::int
		FROM agent_inbox_event
		WHERE channel_id = $1`, fixture.channel.ID).Scan(&rounds, &full, &observe); err != nil {
		t.Fatalf("inspect observe-only dispatch: %v", err)
	}
	if rounds != 0 || full != 0 || observe == 0 {
		t.Fatalf("rounds=%d full=%d observe=%d, want 0/0/>0", rounds, full, observe)
	}
}

func TestChannelAttentionDebounceBlocksThenAllowsLease(t *testing.T) {
	fixture := newChannelAttentionFixture(t, []attentionRuntimeSpec{{}})
	trigger := fixture.insertMessage(t, "user", testUserID, "Check whether this needs a response", nil)
	fixture.handler.dispatchChannelMessageToAgents(context.Background(), fixture.channel, trigger, parseUUID(testUserID))
	runtime := fixture.runtime(t, 0)

	if _, err := fixture.handler.leaseAgentInboxEventForRuntime(context.Background(), runtime); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("lease before debounce = %v, want pgx.ErrNoRows", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE channel_attention_round SET dispatch_at = created_at
		WHERE channel_id = $1`, fixture.channel.ID); err != nil {
		t.Fatalf("expire debounce: %v", err)
	}
	delivery, err := fixture.handler.leaseAgentInboxEventForRuntime(context.Background(), runtime)
	if err != nil {
		t.Fatalf("lease after debounce: %v", err)
	}
	var participantStatus, roundStatus string
	if err := testPool.QueryRow(context.Background(), `
		SELECT participant.status, round.status
		FROM channel_attention_participant participant
		JOIN channel_attention_round round ON round.id = participant.round_id
		WHERE participant.inbox_event_id = $1`, delivery.InboxEventID).Scan(&participantStatus, &roundStatus); err != nil {
		t.Fatalf("inspect leased participant: %v", err)
	}
	if participantStatus != "running" || roundStatus != "resolving" {
		t.Fatalf("participant=%q round=%q, want running/resolving", participantStatus, roundStatus)
	}
}

func TestChannelAttentionUnavailableRuntimesNeverFallbackToFull(t *testing.T) {
	fixture := newChannelAttentionFixture(t, []attentionRuntimeSpec{
		{status: "offline"},
		{provider: "other"},
		{omitCapability: true},
	})
	trigger := fixture.insertMessage(t, "user", testUserID, "Can anyone validate the dependency?", nil)
	fixture.handler.dispatchChannelMessageToAgents(context.Background(), fixture.channel, trigger, parseUUID(testUserID))

	var unavailable, inboxEvents, fullEvents int
	if err := testPool.QueryRow(context.Background(), `
		SELECT
		  count(*) FILTER (WHERE participant.status = 'unavailable')::int,
		  count(participant.inbox_event_id)::int,
		  (SELECT count(*) FROM agent_inbox_event event
		    WHERE event.channel_id = $1 AND event.delivery_mode = 'execute' AND event.requires_wake)::int
		FROM channel_attention_participant participant
		JOIN channel_attention_round round ON round.id = participant.round_id
		WHERE round.channel_id = $1`, fixture.channel.ID).Scan(&unavailable, &inboxEvents, &fullEvents); err != nil {
		t.Fatalf("inspect unavailable participants: %v", err)
	}
	if unavailable != 3 || inboxEvents != 0 || fullEvents != 0 {
		t.Fatalf("unavailable=%d inbox=%d full=%d, want 3/0/0", unavailable, inboxEvents, fullEvents)
	}
}

func TestChannelAttentionDuplicateTriggerIsIdempotent(t *testing.T) {
	fixture := newChannelAttentionFixture(t, []attentionRuntimeSpec{{}, {}})
	trigger := fixture.insertMessage(t, "user", testUserID, "Inspect this once", nil)
	for range 2 {
		fixture.handler.dispatchChannelMessageToAgents(context.Background(), fixture.channel, trigger, parseUUID(testUserID))
	}

	var rounds, participants, events int
	if err := testPool.QueryRow(context.Background(), `
		SELECT
		  count(DISTINCT round.id)::int,
		  count(DISTINCT participant.id)::int,
		  count(DISTINCT participant.inbox_event_id)::int
		FROM channel_attention_round round
		LEFT JOIN channel_attention_participant participant ON participant.round_id = round.id
		WHERE round.channel_id = $1`, fixture.channel.ID).Scan(&rounds, &participants, &events); err != nil {
		t.Fatalf("inspect duplicate dispatch: %v", err)
	}
	if rounds != 1 || participants != 2 || events != 2 {
		t.Fatalf("rounds=%d participants=%d events=%d, want 1/2/2", rounds, participants, events)
	}
}

func TestChannelAttentionCompletionPersistsDecisionWithoutChannelOutput(t *testing.T) {
	fixture := newChannelAttentionFixture(t, []attentionRuntimeSpec{{}})
	trigger := fixture.insertMessage(t, "user", testUserID, "Find the exact version", nil)
	fixture.handler.dispatchChannelMessageToAgents(context.Background(), fixture.channel, trigger, parseUUID(testUserID))
	if _, err := testPool.Exec(context.Background(), `UPDATE channel_attention_round SET dispatch_at = created_at WHERE channel_id = $1`, fixture.channel.ID); err != nil {
		t.Fatalf("expire debounce: %v", err)
	}
	delivery, err := fixture.handler.leaseAgentInboxEventForRuntime(context.Background(), fixture.runtime(t, 0))
	if err != nil {
		t.Fatalf("lease attention event: %v", err)
	}
	event, err := fixture.handler.Queries.GetAgentInboxEvent(context.Background(), delivery.InboxEventID)
	if err != nil {
		t.Fatalf("load inbox event: %v", err)
	}
	executionID := parseUUID(uuid.NewString())
	if err := fixture.handler.Queries.CreateAgentInboxExecution(context.Background(), db.CreateAgentInboxExecutionParams{ExecutionID: executionID, InboxEventID: event.ID}); err != nil {
		t.Fatalf("create attention execution: %v", err)
	}
	if err := fixture.handler.Queries.UpsertAgentUsage(context.Background(), db.UpsertAgentUsageParams{
		ExecutionID: executionID, Source: "chat", Provider: "pi", Model: "probe-model", InputTokens: 12, OutputTokens: 7,
	}); err != nil {
		t.Fatalf("record attention usage: %v", err)
	}
	before := fixture.channelMessageCount(t)
	tx, err := fixture.handler.TxStarter.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin completion: %v", err)
	}
	completion, err := fixture.handler.completeChannelAttentionParticipantTx(context.Background(), tx, event, executionID, channelAttentionDecision{
		Decision: "ANSWER", Confidence: 0.91, ValueType: "direct_answer",
		Summary: "version is pinned", EvidenceRefs: []string{"message:" + trigger.ID},
		ModelVersion: "untrusted-client-model", SeenUpToSeq: trigger.Seq,
	})
	if err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatalf("complete participant: %v", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("commit completion: %v", err)
	}
	if !completion.roundResolved {
		t.Fatal("single-participant round was not resolved")
	}

	var status, decision, summary, roundStatus string
	var seen int64
	if err := testPool.QueryRow(context.Background(), `
		SELECT participant.status, participant.decision, participant.summary,
		       participant.seen_up_to_seq, round.status
		FROM channel_attention_participant participant
		JOIN channel_attention_round round ON round.id = participant.round_id
		WHERE participant.inbox_event_id = $1`, delivery.InboxEventID).Scan(&status, &decision, &summary, &seen, &roundStatus); err != nil {
		t.Fatalf("inspect completed decision: %v", err)
	}
	if status != "decided" || decision != "ANSWER" || summary != "version is pinned" || seen != trigger.Seq || roundStatus != "resolved" {
		t.Fatalf("participant=%q/%q/%q seen=%d round=%q", status, decision, summary, seen, roundStatus)
	}
	if after := fixture.channelMessageCount(t); after != before {
		t.Fatalf("channel message count changed from %d to %d after internal completion", before, after)
	}
}

func TestChannelAttentionTimeoutSuppressesPendingWork(t *testing.T) {
	fixture := newChannelAttentionFixture(t, []attentionRuntimeSpec{{}, {}})
	trigger := fixture.insertMessage(t, "user", testUserID, "Investigate before the deadline", nil)
	fixture.handler.dispatchChannelMessageToAgents(context.Background(), fixture.channel, trigger, parseUUID(testUserID))
	if _, err := testPool.Exec(context.Background(), `
		UPDATE channel_attention_round
		SET created_at = now() - interval '2 seconds',
		    dispatch_at = now() - interval '1 second',
		    deadline_at = now() - interval '1 millisecond'
		WHERE channel_id = $1`, fixture.channel.ID); err != nil {
		t.Fatalf("expire round deadline: %v", err)
	}
	if got := fixture.handler.SweepChannelAttentionTimeouts(context.Background(), 10); got != 1 {
		t.Fatalf("SweepChannelAttentionTimeouts() = %d, want 1", got)
	}

	var roundStatus string
	var timedOut, suppressed int
	if err := testPool.QueryRow(context.Background(), `
		SELECT round.status,
		       count(*) FILTER (WHERE participant.status = 'timed_out')::int,
		       count(*) FILTER (WHERE event.status = 'suppressed')::int
		FROM channel_attention_round round
		JOIN channel_attention_participant participant ON participant.round_id = round.id
		LEFT JOIN agent_inbox_event event ON event.id = participant.inbox_event_id
		WHERE round.channel_id = $1
		GROUP BY round.status`, fixture.channel.ID).Scan(&roundStatus, &timedOut, &suppressed); err != nil {
		t.Fatalf("inspect timed-out round: %v", err)
	}
	if roundStatus != "timed_out" || timedOut != 2 || suppressed != 2 {
		t.Fatalf("round=%q timed_out=%d suppressed=%d, want timed_out/2/2", roundStatus, timedOut, suppressed)
	}
}

func TestChannelAttentionTimeoutInfersRuntimeCapacityWithoutDeniedDrain(t *testing.T) {
	fixture := newChannelAttentionFixture(t, []attentionRuntimeSpec{{}, {}})
	fixture.handler.cfg.ChannelAttentionMaxConcurrentPerRuntime = 1
	trigger := fixture.insertMessage(t, "user", testUserID, "Investigate under runtime pressure", nil)
	fixture.handler.dispatchChannelMessageToAgents(context.Background(), fixture.channel, trigger, parseUUID(testUserID))
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_inbox_event
		SET runtime_id = $2
		WHERE channel_id = $1 AND delivery_mode = 'attention'`, fixture.channel.ID, fixture.runtimeIDs[0]); err != nil {
		t.Fatalf("co-locate attention events: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE channel_attention_participant participant
		SET status = 'running', started_at = now()
		FROM agent_inbox_event event
		WHERE participant.inbox_event_id = event.id
		  AND event.channel_id = $1 AND event.agent_id = $2`, fixture.channel.ID, fixture.agentIDs[0]); err != nil {
		t.Fatalf("mark capacity holder running: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE channel_attention_round
		SET status = 'resolving', created_at = now() - interval '2 seconds',
		    dispatch_at = now() - interval '1 second', deadline_at = now() - interval '1 millisecond'
		WHERE channel_id = $1`, fixture.channel.ID); err != nil {
		t.Fatalf("expire capacity round: %v", err)
	}
	if got := fixture.handler.SweepChannelAttentionTimeouts(context.Background(), 10); got != 1 {
		t.Fatalf("SweepChannelAttentionTimeouts() = %d, want 1", got)
	}
	var reason string
	if err := testPool.QueryRow(context.Background(), `
		SELECT participant.last_error
		FROM channel_attention_participant participant
		WHERE participant.agent_id = $1`, fixture.agentIDs[1]).Scan(&reason); err != nil {
		t.Fatalf("load pending participant timeout reason: %v", err)
	}
	if reason != "runtime_capacity" {
		t.Fatalf("timeout reason=%q, want runtime_capacity", reason)
	}
}

type attentionRuntimeSpec struct {
	status         string
	provider       string
	omitCapability bool
}

type channelAttentionFixture struct {
	handler    *Handler
	channel    ChannelResponse
	agentIDs   []string
	agentNames []string
	runtimeIDs []string
}

func newChannelAttentionFixture(t *testing.T, specs []attentionRuntimeSpec) channelAttentionFixture {
	t.Helper()
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	local := *testHandler
	local.cfg.ChannelAttentionEnabled = true
	local.cfg.ChannelUnmentionedMode = channelUnmentionedModeAttentionRound
	local.cfg.ChannelAttentionDebounce = 2 * time.Second
	local.cfg.ChannelAttentionMaxWait = 8 * time.Second
	local.cfg.ChannelAttentionContextMessages = 8
	local.cfg.ChannelAttentionMemoryBudgetBytes = 4 * 1024
	local.cfg.ChannelAttentionMaxOutputTokens = 96
	local.cfg.ChannelAttentionToolsEnabled = false
	local.cfg.ChannelAttentionMaxConcurrentPerRuntime = 16

	suffix := uuid.NewString()
	var channelID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by, kind)
		VALUES ($1, $2, $3, 'group')
		RETURNING id`, testWorkspaceID, "attention-"+suffix, testUserID).Scan(&channelID); err != nil {
		t.Fatalf("create attention channel: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'user', $3)`, channelID, testWorkspaceID, testUserID); err != nil {
		t.Fatalf("add attention channel user: %v", err)
	}

	fixture := channelAttentionFixture{handler: &local}
	for i, spec := range specs {
		status := spec.status
		if status == "" {
			status = "online"
		}
		provider := spec.provider
		if provider == "" {
			provider = "pi"
		}
		capabilities := []string{protocol.DaemonCapabilityRestrictedExecution}
		if spec.omitCapability {
			capabilities = []string{}
		}
		metadata, err := json.Marshal(map[string]any{"capabilities": capabilities})
		if err != nil {
			t.Fatalf("marshal attention runtime metadata: %v", err)
		}
		var runtimeID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_runtime (
			  workspace_id, name, runtime_mode, provider, status,
			  device_info, metadata, last_seen_at
			)
			VALUES ($1, $2, 'cloud', $3, $4, 'attention test runtime', $5, now())
			RETURNING id`, testWorkspaceID, fmt.Sprintf("attention-runtime-%s-%d", suffix, i), provider, status, metadata).Scan(&runtimeID); err != nil {
			t.Fatalf("create attention runtime: %v", err)
		}
		name := fmt.Sprintf("attention_agent_%s_%d", suffix[:8], i)
		var agentID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent (
			  workspace_id, name, display_name, description, runtime_mode,
			  runtime_config, runtime_id, visibility, max_concurrent_tasks,
			  owner_id, instructions, custom_env, custom_args, mcp_config
			)
			VALUES ($1, $2, $2, '', 'cloud', '{}'::jsonb, $3, 'private', 1,
			        $4, '', '{}'::jsonb, '[]'::jsonb, '[]'::jsonb)
			RETURNING id`, testWorkspaceID, name, runtimeID, testUserID).Scan(&agentID); err != nil {
			t.Fatalf("create attention agent: %v", err)
		}
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
			VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
			t.Fatalf("add attention agent to channel: %v", err)
		}
		fixture.runtimeIDs = append(fixture.runtimeIDs, runtimeID)
		fixture.agentIDs = append(fixture.agentIDs, agentID)
		fixture.agentNames = append(fixture.agentNames, name)
	}
	channel, ok := local.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !ok {
		t.Fatal("load attention channel")
	}
	fixture.channel = channel
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = testPool.Exec(cleanupCtx, `DELETE FROM channel WHERE id = $1`, channelID)
		for _, agentID := range fixture.agentIDs {
			_, _ = testPool.Exec(cleanupCtx, `DELETE FROM agent WHERE id = $1`, agentID)
		}
		for _, runtimeID := range fixture.runtimeIDs {
			_, _ = testPool.Exec(cleanupCtx, `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
		}
	})
	return fixture
}

func (f channelAttentionFixture) insertMessage(t *testing.T, authorType, authorID, content string, parts []protocol.MessagePart) ChannelMessageResponse {
	t.Helper()
	id := pgtype.UUID{}
	if authorID != "" {
		id = parseUUID(authorID)
	}
	message, err := f.handler.insertChannelMessageWithParts(
		context.Background(), parseUUID(f.channel.ID), parseUUID(testWorkspaceID),
		authorType, id, authorType+" test", content, parts, "multica", nil,
		pgtype.UUID{}, pgtype.UUID{}, nil, 0,
	)
	if err != nil {
		t.Fatalf("insert attention trigger: %v", err)
	}
	return message
}

func (f channelAttentionFixture) runtime(t *testing.T, index int) db.AgentRuntime {
	t.Helper()
	loaded, err := f.handler.Queries.GetAgentRuntime(context.Background(), parseUUID(f.runtimeIDs[index]))
	if err != nil {
		t.Fatalf("load attention runtime: %v", err)
	}
	return loaded
}

func (f channelAttentionFixture) channelMessageCount(t *testing.T) int {
	t.Helper()
	var count int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM channel_message WHERE channel_id = $1`, f.channel.ID).Scan(&count); err != nil {
		t.Fatalf("count attention channel messages: %v", err)
	}
	return count
}
