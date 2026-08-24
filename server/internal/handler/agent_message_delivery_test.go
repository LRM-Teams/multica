package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type capturedAgentDeliveryNotifier struct {
	workspaceID string
	daemonID    string
	payload     protocol.AgentDeliverPayload
	deliveries  []capturedAgentDelivery
}

type capturedAgentDelivery struct {
	workspaceID string
	daemonID    string
	payload     protocol.AgentDeliverPayload
}

func (n *capturedAgentDeliveryNotifier) NotifyWorkspaceAgentDelivery(workspaceID, daemonID string, payload protocol.AgentDeliverPayload) bool {
	n.workspaceID = workspaceID
	n.daemonID = daemonID
	n.payload = payload
	n.deliveries = append(n.deliveries, capturedAgentDelivery{workspaceID: workspaceID, daemonID: daemonID, payload: payload})
	return true
}

func TestWorkspaceDaemonReadyRedeliversUnacknowledgedMessagesInSequenceOrder(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "message-redelivery-" + uuid.NewString()[:8]
	runtimeID := seedMachineLockedRuntime(t, daemonID, "message redelivery")
	agentID := createHandlerTestAgentOnRuntime(t, "message-redelivery-"+uuid.NewString()[:8], runtimeID)
	channelName := "message-redelivery-" + uuid.NewString()
	channelID := seedChannelForTest(t, channelName, testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3) ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("add Agent member: %v", err)
	}

	previousNotifier := testHandler.AgentDeliveryNotifier
	testHandler.AgentDeliveryNotifier = nil
	t.Cleanup(func() { testHandler.AgentDeliveryNotifier = previousNotifier })
	var sent []ChannelMessageResponse
	for _, content := range []string{"first unacknowledged", "second unacknowledged"} {
		recorder := sendChannelMessageForTest(t, channelID, testUserID, map[string]any{
			"content": content, "client_message_id": uuid.NewString(),
		})
		if recorder.Code != http.StatusCreated {
			t.Fatalf("create canonical Message: status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		var message ChannelMessageResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &message); err != nil {
			t.Fatalf("decode canonical Message: %v", err)
		}
		sent = append(sent, message)
	}

	notifier := &capturedAgentDeliveryNotifier{}
	testHandler.AgentDeliveryNotifier = notifier
	h := *testHandler
	h.AgentDeliveryNotifier = notifier
	h.DaemonHub = daemonws.NewHub()
	h.RunnerPresenceSource = fakeRunnerPresenceSource{current: map[string]bool{
		daemonID + "/" + testWorkspaceID + "/instance-1": true,
	}}
	ready := protocol.WorkspaceReadyPayload{WorkspaceID: testWorkspaceID, DaemonInstanceID: "instance-1"}
	raw, err := json.Marshal(ready)
	if err != nil {
		t.Fatal(err)
	}
	identity := daemonws.ClientIdentity{DaemonID: daemonID, WorkspaceID: testWorkspaceID}
	if err := h.HandleWorkspaceDaemonFrame(ctx, identity, "instance-1", protocol.EventWorkspaceDaemonReady, raw); err != nil {
		t.Fatalf("accept WorkspaceDaemon ready: %v", err)
	}
	if len(notifier.deliveries) != 2 {
		t.Fatalf("redelivered %d Messages, want 2", len(notifier.deliveries))
	}
	for index, delivery := range notifier.deliveries {
		if delivery.payload.AgentID != agentID || delivery.payload.Message.ID != sent[index].ID || delivery.payload.Seq != sent[index].Seq {
			t.Fatalf("redelivery[%d] = %+v, want message=%s seq=%d", index, delivery.payload, sent[index].ID, sent[index].Seq)
		}
	}
}

func TestAgentDeliveryAcknowledgementRequiresExactSequenceAndStopsRedelivery(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "message-ack-" + uuid.NewString()[:8]
	runtimeID := seedMachineLockedRuntime(t, daemonID, "message acknowledgement")
	agentID := createHandlerTestAgentOnRuntime(t, "message-ack-"+uuid.NewString()[:8], runtimeID)
	channelID := seedChannelForTest(t, "message-ack-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3) ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatal(err)
	}
	previousNotifier := testHandler.AgentDeliveryNotifier
	testHandler.AgentDeliveryNotifier = nil
	t.Cleanup(func() { testHandler.AgentDeliveryNotifier = previousNotifier })
	recorder := sendChannelMessageForTest(t, channelID, testUserID, map[string]any{"content": "ack me", "client_message_id": uuid.NewString()})
	var message ChannelMessageResponse
	if recorder.Code != http.StatusCreated || json.Unmarshal(recorder.Body.Bytes(), &message) != nil {
		t.Fatalf("create canonical Message: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	identity := daemonws.ClientIdentity{DaemonID: daemonID, WorkspaceID: testWorkspaceID}
	deliveryID := "message:" + message.ID + ":agent:" + agentID
	if err := testHandler.HandleAgentDeliveryAck(ctx, identity, protocol.AgentDeliverAckPayload{AgentID: agentID, Seq: message.Seq + 1, DeliveryID: deliveryID}); err == nil {
		t.Fatal("acknowledgement with the wrong sequence was accepted")
	}
	if err := testHandler.HandleAgentDeliveryAck(ctx, identity, protocol.AgentDeliverAckPayload{AgentID: agentID, Seq: message.Seq, DeliveryID: deliveryID}); err != nil {
		t.Fatalf("acknowledge exact delivery: %v", err)
	}
	var acked bool
	if err := testPool.QueryRow(ctx, `SELECT acked_at IS NOT NULL FROM agent_message_delivery WHERE agent_id = $1 AND message_id = $2`, agentID, message.ID).Scan(&acked); err != nil {
		t.Fatalf("load durable acknowledgement: %v", err)
	}
	if !acked {
		t.Fatal("exact acknowledgement was not durable")
	}
	notifier := &capturedAgentDeliveryNotifier{}
	h := *testHandler
	h.AgentDeliveryNotifier = notifier
	h.DaemonHub = daemonws.NewHub()
	h.RunnerPresenceSource = fakeRunnerPresenceSource{current: map[string]bool{
		daemonID + "/" + testWorkspaceID + "/instance-1": true,
	}}
	ready, err := json.Marshal(protocol.WorkspaceReadyPayload{WorkspaceID: testWorkspaceID, DaemonInstanceID: "instance-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.HandleWorkspaceDaemonFrame(ctx, identity, "instance-1", protocol.EventWorkspaceDaemonReady, ready); err != nil {
		t.Fatalf("accept WorkspaceDaemon ready after acknowledgement: %v", err)
	}
	if len(notifier.deliveries) != 0 {
		t.Fatalf("redelivered %d acknowledged Messages, want 0", len(notifier.deliveries))
	}
}

func TestCanonicalMessageProjectsAgentDeliveryEnvelope(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "daemon-message-delivery-envelope-" + uuid.NewString()[:8]
	runtimeID := seedMachineLockedRuntime(t, daemonID, "message delivery envelope")
	agentID := createHandlerTestAgentOnRuntime(t, "message-delivery-envelope-"+uuid.NewString()[:8], runtimeID)
	channelName := "message-delivery-envelope-" + uuid.NewString()
	channelID := seedChannelForTest(t, channelName, testUserID)
	if _, err := testPool.Exec(ctx, `INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id) VALUES ($1, $2, 'agent', $3) ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("add Agent member: %v", err)
	}
	notifier := &capturedAgentDeliveryNotifier{}
	previous := testHandler.AgentDeliveryNotifier
	testHandler.AgentDeliveryNotifier = notifier
	t.Cleanup(func() { testHandler.AgentDeliveryNotifier = previous })
	recorder := sendChannelMessageForTest(t, channelID, testUserID, map[string]any{
		"content":           "canonical body",
		"client_message_id": uuid.NewString(),
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create canonical Message: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var message ChannelMessageResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &message); err != nil {
		t.Fatalf("decode canonical Message: %v", err)
	}
	want := protocol.AgentMessageProjection{ID: message.ID, Target: "channel:" + channelID, ReplyTarget: "#" + channelName, Seq: message.Seq, Content: "canonical body", Parts: message.Parts, ChannelID: channelID, ChannelKind: "group", InitiatorType: "member", InitiatorID: testUserID}
	got := notifier.payload.Message
	if notifier.workspaceID != testWorkspaceID || notifier.daemonID != daemonID || notifier.payload.AgentID != agentID || notifier.payload.DeliveryID == "" || got.ID != want.ID || got.Target != want.Target || got.ReplyTarget != want.ReplyTarget || got.Seq != want.Seq || got.Content != want.Content || len(got.Parts) != len(want.Parts) || got.ChannelID != want.ChannelID || got.ChannelKind != want.ChannelKind || got.InitiatorType != want.InitiatorType || got.InitiatorID != want.InitiatorID {
		t.Fatalf("delivery workspace=%q daemon=%q payload=%+v, want workspace=%q daemon=%q Message=%+v", notifier.workspaceID, notifier.daemonID, notifier.payload, testWorkspaceID, daemonID, want)
	}
	channel, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatalf("load channel %s", channelID)
	}
	before := len(notifier.deliveries)
	testHandler.deliverCanonicalMessageToChannelAgents(ctx, channel, message)
	if got := len(notifier.deliveries); got != before {
		t.Fatalf("duplicate canonical delivery notified %d times, want %d", got, before)
	}
}

func TestCanonicalDMMessageProjectsRecipientRelativeReplyTarget(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	cleanupDMArtifacts(t)
	daemonID := "daemon-message-delivery-dm-" + uuid.NewString()[:8]
	runtimeID := seedMachineLockedRuntime(t, daemonID, "DM message delivery target")
	agentID := createHandlerTestAgentOnRuntime(t, "message-delivery-dm-"+uuid.NewString()[:8], runtimeID)
	channelID := seedAgentDMChannel(t, agentID)
	var userHandle string
	if err := testPool.QueryRow(ctx, `SELECT name FROM "user" WHERE id = $1`, testUserID).Scan(&userHandle); err != nil {
		t.Fatalf("load DM user handle: %v", err)
	}
	notifier := &capturedAgentDeliveryNotifier{}
	previous := testHandler.AgentDeliveryNotifier
	testHandler.AgentDeliveryNotifier = notifier
	t.Cleanup(func() { testHandler.AgentDeliveryNotifier = previous })
	recorder := sendChannelMessageForTest(t, channelID, testUserID, map[string]any{
		"content":           "hello in DM",
		"client_message_id": uuid.NewString(),
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create canonical DM Message: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if got, want := notifier.payload.Message.ReplyTarget, "dm:@"+userHandle; got != want {
		t.Fatalf("DM reply target = %q, want %q; payload=%+v", got, want, notifier.payload)
	}
}

func TestCanonicalAgentMessageLiveDeliveryMatchesRecoveryEligibility(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := seedMachineLockedRuntime(t, "daemon-message-live-delivery-"+uuid.NewString()[:8], "message live delivery")
	authorAgentID := createHandlerTestAgentOnRuntime(t, "message-live-author-"+uuid.NewString()[:8], runtimeID)
	recipientAgentID := createHandlerTestAgentOnRuntime(t, "message-live-recipient-"+uuid.NewString()[:8], runtimeID)
	channelID := seedChannelForTest(t, "message-live-agent-"+uuid.NewString(), testUserID)
	for _, agentID := range []string{authorAgentID, recipientAgentID} {
		if _, err := testPool.Exec(ctx, `INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id) VALUES ($1, $2, 'agent', $3) ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
			t.Fatalf("add Agent member: %v", err)
		}
	}
	message, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(authorAgentID), "Author Agent", "agent-authored canonical body", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr(uuid.NewString()), 0)
	if err != nil {
		t.Fatalf("insert Agent Message: %v", err)
	}
	notifier := &capturedAgentDeliveryNotifier{}
	previous := testHandler.AgentDeliveryNotifier
	testHandler.AgentDeliveryNotifier = notifier
	t.Cleanup(func() { testHandler.AgentDeliveryNotifier = previous })
	testHandler.publishChannelToMembers(ctx, protocol.EventChannelMessage, testWorkspaceID, "agent", authorAgentID, parseUUID(channelID), message)
	if notifier.payload.AgentID != recipientAgentID || notifier.payload.Message.ID != message.ID {
		t.Fatalf("live Agent Message delivery = %+v, want recipient %s Message %s", notifier.payload, recipientAgentID, message.ID)
	}
}

func TestCanonicalMessageDeliveryPreservesChannelMentionAndThreadRecipients(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := seedMachineLockedRuntime(t, "daemon-message-routing-"+uuid.NewString()[:8], "message routing")
	firstAgentID := createHandlerTestAgentOnRuntime(t, "message-routing-first-"+uuid.NewString()[:8], runtimeID)
	secondAgentID := createHandlerTestAgentOnRuntime(t, "message-routing-second-"+uuid.NewString()[:8], runtimeID)
	channelID := seedChannelForTest(t, "message-routing-"+uuid.NewString(), testUserID)
	for _, agentID := range []string{firstAgentID, secondAgentID} {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
			VALUES ($1, $2, 'agent', $3)
			ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
			t.Fatalf("add Agent member: %v", err)
		}
	}
	channel, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("load channel")
	}
	notifier := &capturedAgentDeliveryNotifier{}
	previous := testHandler.AgentDeliveryNotifier
	testHandler.AgentDeliveryNotifier = notifier
	t.Cleanup(func() { testHandler.AgentDeliveryNotifier = previous })

	deliver := func(content string, parts []protocol.MessagePart, rootID *string) (ChannelMessageResponse, map[string]bool) {
		t.Helper()
		replyToMessageID := pgtype.UUID{}
		threadRootMessageID := pgtype.UUID{}
		if rootID != nil {
			replyToMessageID = parseUUID(*rootID)
			threadRootMessageID = parseUUID(*rootID)
		}
		message, err := testHandler.insertChannelMessageWithParts(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", content, parts, "multica", nil, replyToMessageID, threadRootMessageID, nil, 0)
		if err != nil {
			t.Fatalf("insert canonical Message: %v", err)
		}
		before := len(notifier.deliveries)
		testHandler.deliverCanonicalMessageToChannelAgents(ctx, channel, message)
		got := make(map[string]bool)
		for _, delivery := range notifier.deliveries[before:] {
			got[delivery.payload.AgentID] = true
		}
		return message, got
	}
	assertRecipients := func(name string, got map[string]bool, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s recipients = %v, want %v", name, got, want)
		}
		for _, agentID := range want {
			if !got[agentID] {
				t.Fatalf("%s recipients = %v, missing %s", name, got, agentID)
			}
		}
	}

	root, got := deliver("normal channel message", nil, nil)
	assertRecipients("normal channel", got, firstAgentID, secondAgentID)
	// Human @mention: target + unmuted bystanders share channel context
	// (replaces dual-write ambient wake after #2295).
	_, got = deliver("@second only", []protocol.MessagePart{{Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "agent", RefID: secondAgentID, Label: "@second"}}, nil)
	assertRecipients("mention", got, firstAgentID, secondAgentID)

	testHandler.followChannelThreadAgentUnlessExplicitlyUnfollowed(ctx, parseUUID(channelID), parseUUID(root.ID), parseUUID(firstAgentID))
	_, got = deliver("thread participant follow-up", nil, &root.ID)
	assertRecipients("thread participant", got, firstAgentID)
	// Human thread @mention: explicit target + unmuted thread followers.
	_, got = deliver("@second thread", []protocol.MessagePart{{Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "agent", RefID: secondAgentID, Label: "@second"}}, &root.ID)
	assertRecipients("thread mention", got, firstAgentID, secondAgentID)
}

func TestRunDeliveryObligationAccountingIsIdempotentAndNonNegative(t *testing.T) {
	tracker := service.NewEnvDispatchActivityTracker()
	if !tracker.CreateDeliveryObligation("delivery-1") {
		t.Fatal("first obligation creation was not recorded")
	}
	if tracker.CreateDeliveryObligation("delivery-1") {
		t.Fatal("duplicate obligation creation changed accounting")
	}
	if got := tracker.PendingDeliveries(); got != 1 {
		t.Fatalf("pending deliveries after create = %d, want 1", got)
	}
	if !tracker.SettleDeliveryObligation("delivery-1") {
		t.Fatal("first settlement was not recorded")
	}
	if tracker.SettleDeliveryObligation("delivery-1") {
		t.Fatal("duplicate settlement changed accounting")
	}
	if got := tracker.PendingDeliveries(); got != 0 {
		t.Fatalf("pending deliveries after duplicate settlement = %d, want 0", got)
	}
}

type mixedRunDeliveryFixture struct {
	ctx        context.Context
	runs       *service.EnvDispatchRunStore
	runID      pgtype.UUID
	runAgentID pgtype.UUID
	sourceID   pgtype.UUID
	agentID    string
	runtimeID  string
	daemonID   string
	channelID  string
	message    ChannelMessageResponse
}

func seedMixedRunDeliveryFixture(t *testing.T) mixedRunDeliveryFixture {
	t.Helper()
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "mixed-delivery-" + uuid.NewString()[:8]
	runtimeID := seedMachineLockedRuntime(t, daemonID, "mixed delivery")
	agentID := createHandlerTestAgentOnRuntime(t, "mixed-delivery-agent-"+uuid.NewString()[:8], runtimeID)
	channelID := seedChannelForTest(t, "mixed-delivery-channel-"+uuid.NewString(), testUserID)
	message, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "mixed delivery", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr(uuid.NewString()), 0)
	if err != nil {
		t.Fatalf("insert canonical message: %v", err)
	}
	var projectID string
	if err := testPool.QueryRow(ctx, `INSERT INTO project (workspace_id, title) VALUES ($1, $2) RETURNING id`, testWorkspaceID, "Mixed delivery "+uuid.NewString()).Scan(&projectID); err != nil {
		t.Fatalf("create mixed delivery project: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID) })
	runID := parseUUID(uuid.NewString())
	if _, err := testPool.Exec(ctx, `
		INSERT INTO env_dispatch_run (
		  project_id, workspace_id, training_mode, run_id, local_channel_id, status,
		  quiet_window_ms, total_timeout_seconds, initial_message_submitted_at, timeout_deadline_at
		) VALUES ($1, $2, false, $3, $4, 'running', 2000, 3300, now(), now() + interval '3300 seconds')`,
		projectID, testWorkspaceID, runID, channelID); err != nil {
		t.Fatalf("create mixed delivery run: %v", err)
	}
	var runAgentID pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		INSERT INTO env_dispatch_run_agent (
		  run_id, source_agent_id, execution_agent_id, runtime_id, pi_session_id, training_mode, capture_boundary
		) VALUES ($1, $2, $2, $3, $4, 'none', $5)
		RETURNING run_agent_id`, runID, agentID, runtimeID, "pi-session-"+uuid.NewString(), "capture-"+uuid.NewString()).Scan(&runAgentID); err != nil {
		t.Fatalf("bind mixed delivery run agent: %v", err)
	}
	return mixedRunDeliveryFixture{
		ctx: ctx, runs: service.NewEnvDispatchRunStore(testHandler.Queries), runID: runID, runAgentID: runAgentID,
		sourceID: parseUUID(agentID), agentID: agentID, runtimeID: runtimeID, daemonID: daemonID, channelID: channelID, message: message,
	}
}

func (fixture mixedRunDeliveryFixture) insertMessage(t *testing.T, content string) ChannelMessageResponse {
	t.Helper()
	message, err := testHandler.insertChannelMessage(fixture.ctx, parseUUID(fixture.channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", content, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr(uuid.NewString()), 0)
	if err != nil {
		t.Fatalf("insert canonical message: %v", err)
	}
	return message
}

func TestCanonicalSendRollsBackMessageWithObligationAndRetryRepairsAtomically(t *testing.T) {
	fixture := seedMixedRunDeliveryFixture(t)
	if _, err := testPool.Exec(fixture.ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
		ON CONFLICT DO NOTHING`, fixture.channelID, testWorkspaceID, fixture.agentID); err != nil {
		t.Fatalf("add mixed-run channel member: %v", err)
	}
	channel, found := testHandler.getChannel(fixture.ctx, testWorkspaceID, parseUUID(fixture.channelID))
	if !found {
		t.Fatal("load mixed-run channel")
	}
	clientMessageID := "atomic-obligation-" + uuid.NewString()
	input := canonicalChannelMessageInput{
		Channel: channel, WorkspaceID: testWorkspaceID, UserID: testUserID,
		Content: "atomic mixed-run delivery", Parts: []protocol.MessagePart{}, ClientMessageID: &clientMessageID,
	}

	originalStarter := testHandler.TxStarter
	testHandler.TxStarter = queryRowFailingTxStarter{
		base: originalStarter, sqlContains: "INSERT INTO env_dispatch_delivery_obligation",
		err: errors.New("injected delivery obligation persistence failure"),
	}
	_, sendErr := testHandler.sendPreparedCanonicalChannelMessage(fixture.ctx, input)
	testHandler.TxStarter = originalStarter
	if sendErr == nil || !strings.Contains(sendErr.Error(), "injected delivery obligation persistence failure") {
		t.Fatalf("canonical send error = %v, want injected obligation failure", sendErr)
	}

	var messages, deliveries, obligations int
	if err := testPool.QueryRow(fixture.ctx, `
		SELECT count(*) FROM channel_message
		WHERE channel_id = $1 AND author_id = $2 AND client_message_id = $3`,
		fixture.channelID, testUserID, clientMessageID).Scan(&messages); err != nil {
		t.Fatalf("count rolled-back messages: %v", err)
	}
	if err := testPool.QueryRow(fixture.ctx, `SELECT count(*) FROM agent_message_delivery d
		JOIN channel_message m ON m.id = d.message_id
		WHERE m.channel_id = $1 AND m.client_message_id = $2`, fixture.channelID, clientMessageID).Scan(&deliveries); err != nil {
		t.Fatalf("count rolled-back deliveries: %v", err)
	}
	if err := testPool.QueryRow(fixture.ctx, `SELECT count(*) FROM env_dispatch_delivery_obligation o
		JOIN channel_message m ON m.id = o.channel_message_id
		WHERE m.channel_id = $1 AND m.client_message_id = $2`, fixture.channelID, clientMessageID).Scan(&obligations); err != nil {
		t.Fatalf("count rolled-back obligations: %v", err)
	}
	if messages != 0 || deliveries != 0 || obligations != 0 {
		t.Fatalf("failed atomic send persisted messages=%d deliveries=%d obligations=%d, want all zero", messages, deliveries, obligations)
	}

	result, err := testHandler.sendPreparedCanonicalChannelMessage(fixture.ctx, input)
	if err != nil {
		t.Fatalf("retry canonical send: %v", err)
	}
	result.Acknowledge(fixture.ctx)
	if _, err := testHandler.sendPreparedCanonicalChannelMessage(fixture.ctx, input); err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	if err := testPool.QueryRow(fixture.ctx, `
		SELECT count(*),
		       (SELECT count(*) FROM agent_message_delivery d JOIN channel_message dm ON dm.id = d.message_id
		        WHERE dm.channel_id = $1 AND dm.client_message_id = $2),
		       (SELECT count(*) FROM env_dispatch_delivery_obligation o JOIN channel_message om ON om.id = o.channel_message_id
		        WHERE om.channel_id = $1 AND om.client_message_id = $2)
		FROM channel_message m WHERE m.channel_id = $1 AND m.author_id = $3 AND m.client_message_id = $2`,
		fixture.channelID, clientMessageID, testUserID).Scan(&messages, &deliveries, &obligations); err != nil {
		t.Fatalf("count retried canonical send rows: %v", err)
	}
	if messages != 1 || deliveries != 1 || obligations != 1 {
		t.Fatalf("retried/replayed send rows messages=%d deliveries=%d obligations=%d, want exactly one each", messages, deliveries, obligations)
	}
	var pending int64
	if err := testPool.QueryRow(fixture.ctx, `SELECT pending_delivery_count FROM env_dispatch_run WHERE run_id = $1`, fixture.runID).Scan(&pending); err != nil {
		t.Fatalf("load canonical send pending delivery count: %v", err)
	}
	if pending != 1 {
		t.Fatalf("canonical send pending delivery count = %d, want 1", pending)
	}
	if err := testHandler.HandleAgentDeliveryAck(fixture.ctx, daemonws.ClientIdentity{DaemonID: fixture.daemonID, WorkspaceID: testWorkspaceID}, protocol.AgentDeliverAckPayload{
		AgentID: fixture.agentID, Seq: result.Message.Message.Seq,
		DeliveryID: "message:" + result.Message.Message.ID + ":agent:" + fixture.agentID,
	}); err != nil {
		t.Fatalf("settle canonical send delivery through daemon ack: %v", err)
	}
	if err := testPool.QueryRow(fixture.ctx, `SELECT pending_delivery_count FROM env_dispatch_run WHERE run_id = $1`, fixture.runID).Scan(&pending); err != nil {
		t.Fatalf("load settled pending delivery count: %v", err)
	}
	if pending != 0 {
		t.Fatalf("settled canonical send pending delivery count = %d, want 0", pending)
	}

	// Simulate a legacy partial send: the canonical message survived but both
	// recipient rows are absent. Replay must repair them rather than returning
	// early on Created=false.
	if _, err := testPool.Exec(fixture.ctx, `DELETE FROM env_dispatch_delivery_obligation
		WHERE channel_message_id IN (SELECT id FROM channel_message WHERE channel_id = $1 AND client_message_id = $2)`, fixture.channelID, clientMessageID); err != nil {
		t.Fatalf("remove obligation for legacy partial send: %v", err)
	}
	if _, err := testPool.Exec(fixture.ctx, `DELETE FROM agent_message_delivery
		WHERE message_id IN (SELECT id FROM channel_message WHERE channel_id = $1 AND client_message_id = $2)`, fixture.channelID, clientMessageID); err != nil {
		t.Fatalf("remove delivery for legacy partial send: %v", err)
	}
	if _, err := testPool.Exec(fixture.ctx, `UPDATE env_dispatch_run SET pending_delivery_count = 0 WHERE run_id = $1`, fixture.runID); err != nil {
		t.Fatalf("reset legacy partial pending count: %v", err)
	}
	repaired, err := testHandler.sendPreparedCanonicalChannelMessage(fixture.ctx, input)
	if err != nil {
		t.Fatalf("repair legacy partial canonical send: %v", err)
	}
	if repaired.Created {
		t.Fatal("legacy partial repair incorrectly created a second canonical message")
	}
	repaired.Acknowledge(fixture.ctx)
	if err := testPool.QueryRow(fixture.ctx, `
		SELECT count(*),
		       (SELECT count(*) FROM agent_message_delivery d JOIN channel_message dm ON dm.id = d.message_id
		        WHERE dm.channel_id = $1 AND dm.client_message_id = $2),
		       (SELECT count(*) FROM env_dispatch_delivery_obligation o JOIN channel_message om ON om.id = o.channel_message_id
		        WHERE om.channel_id = $1 AND om.client_message_id = $2)
		FROM channel_message m WHERE m.channel_id = $1 AND m.author_id = $3 AND m.client_message_id = $2`,
		fixture.channelID, clientMessageID, testUserID).Scan(&messages, &deliveries, &obligations); err != nil {
		t.Fatalf("count repaired legacy partial rows: %v", err)
	}
	if messages != 1 || deliveries != 1 || obligations != 1 {
		t.Fatalf("legacy partial repair rows messages=%d deliveries=%d obligations=%d, want exactly one each", messages, deliveries, obligations)
	}
}

func TestDurableDeliveryObligationCreationIsAtomicAndIdempotent(t *testing.T) {
	fixture := seedMixedRunDeliveryFixture(t)
	deliveryID := parseUUID(uuid.NewString())
	input := service.CreateDeliveryObligationInput{
		DeliveryID: deliveryID, RunID: fixture.runID, ChannelMessageID: parseUUID(fixture.message.ID),
		SourceRecipientAgentID: fixture.sourceID, RunAgentID: fixture.runAgentID, State: "queued", QueuedAt: time.Now().UTC(),
	}
	created, err := fixture.runs.CreateDeliveryObligation(fixture.ctx, input)
	if err != nil {
		t.Fatalf("create durable delivery obligation: %v", err)
	}
	if created.State != "queued" || uuidToString(created.DeliveryID) != uuidToString(deliveryID) {
		t.Fatalf("created obligation = %+v, want queued %s", created, uuidToString(deliveryID))
	}
	var pending int64
	if err := testPool.QueryRow(fixture.ctx, `SELECT pending_delivery_count FROM env_dispatch_run WHERE run_id = $1`, fixture.runID).Scan(&pending); err != nil {
		t.Fatalf("load pending delivery activity: %v", err)
	}
	if pending != 1 {
		t.Errorf("pending delivery count after durable create = %d, want 1", pending)
	}
	replayed, err := fixture.runs.CreateDeliveryObligation(fixture.ctx, input)
	if err != nil {
		t.Errorf("idempotent delivery obligation replay returned error: %v", err)
	} else if uuidToString(replayed.DeliveryID) != uuidToString(deliveryID) || replayed.State != "queued" {
		t.Errorf("idempotent delivery replay = %+v, want original queued obligation", replayed)
	}
	var rows int
	if err := testPool.QueryRow(fixture.ctx, `SELECT count(*) FROM env_dispatch_delivery_obligation WHERE run_id = $1`, fixture.runID).Scan(&rows); err != nil {
		t.Fatalf("count durable obligations: %v", err)
	}
	if rows != 1 {
		t.Fatalf("durable obligation rows = %d, want 1", rows)
	}
}

func TestDurableDeliveryObligationSettlementIsAtomicAndIdempotent(t *testing.T) {
	fixture := seedMixedRunDeliveryFixture(t)
	deliveryID := parseUUID(uuid.NewString())
	if _, err := testPool.Exec(fixture.ctx, `
		INSERT INTO env_dispatch_delivery_obligation (
		  delivery_id, run_id, channel_message_id, source_recipient_agent_id, run_agent_id, state, queued_at
		) VALUES ($1, $2, $3, $4, $5, 'accepted', now())`,
		deliveryID, fixture.runID, fixture.message.ID, fixture.sourceID, fixture.runAgentID); err != nil {
		t.Fatalf("seed accepted delivery obligation: %v", err)
	}
	if _, err := testPool.Exec(fixture.ctx, `UPDATE env_dispatch_run SET pending_delivery_count = 1 WHERE run_id = $1`, fixture.runID); err != nil {
		t.Fatalf("seed pending delivery activity: %v", err)
	}
	settledAt := time.Now().UTC()
	settled, err := fixture.runs.SettleDeliveryObligation(fixture.ctx, deliveryID, "completed", settledAt)
	if err != nil {
		t.Fatalf("settle durable delivery obligation: %v", err)
	}
	if settled.State != "completed" || settled.SettledAt.IsZero() {
		t.Fatalf("settled obligation = %+v, want completed with settled_at", settled)
	}
	var pending int64
	if err := testPool.QueryRow(fixture.ctx, `SELECT pending_delivery_count FROM env_dispatch_run WHERE run_id = $1`, fixture.runID).Scan(&pending); err != nil {
		t.Fatalf("load pending delivery activity: %v", err)
	}
	if pending != 0 {
		t.Errorf("pending delivery count after settlement = %d, want 0", pending)
	}
	replayed, err := fixture.runs.SettleDeliveryObligation(fixture.ctx, deliveryID, "completed", settledAt.Add(time.Second))
	if err != nil {
		t.Errorf("idempotent delivery settlement replay returned error: %v", err)
	} else if replayed.State != "completed" || replayed.SettledAt.IsZero() {
		t.Errorf("idempotent settlement replay = %+v, want completed obligation", replayed)
	}
}

func TestMixedRunLifecycleTransitionReportsPersistCountersIdempotently(t *testing.T) {
	fixture := seedMixedRunDeliveryFixture(t)
	identity := daemonws.ClientIdentity{DaemonID: fixture.daemonID, WorkspaceID: testWorkspaceID}
	dimensions := []string{
		protocol.MixedRunActivityActiveTurn,
		protocol.MixedRunActivityQueuedMessage,
		protocol.MixedRunActivityInflightTool,
		protocol.MixedRunActivityUnfinishedCaptureBatch,
	}
	reportErr := func(dimension string, delta int, eventID string) error {
		raw, err := json.Marshal(map[string]any{
			"agent_id": fixture.agentID, "runtime_id": fixture.runtimeID,
			"run_id": uuidToString(fixture.runID), "run_agent_id": uuidToString(fixture.runAgentID),
			"transition_id": eventID, "dimension": dimension, "delta": delta,
		})
		if err != nil {
			return err
		}
		return testHandler.HandleWorkspaceDaemonFrame(fixture.ctx, identity, "runner-instance", protocol.EventMixedRunActivityTransition, raw)
	}
	report := func(dimension string, delta int, eventID string) {
		t.Helper()
		if err := reportErr(dimension, delta, eventID); err != nil {
			t.Fatalf("report %s delta %d: %v", dimension, delta, err)
		}
	}
	for _, dimension := range dimensions {
		report(dimension, 1, dimension+":start")
		report(dimension, 1, dimension+":start")
	}
	var active, queued, tools, captures int64
	var runStatus string
	load := func() {
		t.Helper()
		if err := testPool.QueryRow(fixture.ctx, `SELECT active_turn_count, queued_message_count,
			inflight_tool_count, unfinished_capture_batch_count, status FROM env_dispatch_run WHERE run_id = $1`, fixture.runID).
			Scan(&active, &queued, &tools, &captures, &runStatus); err != nil {
			t.Fatalf("load lifecycle counters: %v", err)
		}
	}
	load()
	if active != 1 || queued != 1 || tools != 1 || captures != 1 {
		t.Fatalf("started lifecycle counters active=%d queued=%d tools=%d captures=%d, want all one", active, queued, tools, captures)
	}
	if err := reportErr(protocol.MixedRunActivityActiveTurn, -1, protocol.MixedRunActivityActiveTurn+":start"); err == nil || !strings.Contains(err.Error(), "conflicts with a different payload") {
		t.Fatalf("transition payload collision error = %v", err)
	}
	load()
	if active != 1 || queued != 1 || tools != 1 || captures != 1 {
		t.Fatalf("payload collision changed counters active=%d queued=%d tools=%d captures=%d", active, queued, tools, captures)
	}
	for _, dimension := range dimensions {
		report(dimension, -1, dimension+":end")
		report(dimension, -1, dimension+":end")
	}
	load()
	if active != 0 || queued != 0 || tools != 0 || captures != 0 {
		t.Fatalf("settled lifecycle counters active=%d queued=%d tools=%d captures=%d, want all zero", active, queued, tools, captures)
	}
	if runStatus != "quiet_candidate" {
		t.Fatalf("settled lifecycle status=%q, want quiet_candidate", runStatus)
	}
	const underflowTransitionID = "active_turn:underflow"
	if err := reportErr(protocol.MixedRunActivityActiveTurn, -1, underflowTransitionID); err == nil || !strings.Contains(err.Error(), "would make counter negative") {
		t.Fatalf("counter underflow error = %v", err)
	}
	load()
	if active != 0 || queued != 0 || tools != 0 || captures != 0 {
		t.Fatalf("underflow changed counters active=%d queued=%d tools=%d captures=%d", active, queued, tools, captures)
	}
	var underflowRows int
	if err := testPool.QueryRow(fixture.ctx, `SELECT count(*) FROM env_dispatch_activity_transition
		WHERE run_id = $1 AND transition_id = $2`, fixture.runID, underflowTransitionID).Scan(&underflowRows); err != nil {
		t.Fatalf("count rolled-back underflow transition: %v", err)
	}
	if underflowRows != 0 {
		t.Fatalf("underflow transition rows = %d, want transaction rollback", underflowRows)
	}
}

func TestConcurrentPostAckAndNewObligationCannotLeaveRunFalselyQuiescent(t *testing.T) {
	fixture := seedMixedRunDeliveryFixture(t)
	if _, _, err := persistCanonicalMessageDelivery(fixture.ctx, testHandler.DB, ChannelResponse{ID: fixture.channelID, WorkspaceID: testWorkspaceID, Kind: "group", Name: "mixed-delivery"}, fixture.message, db.Agent{ID: parseUUID(fixture.agentID), RuntimeID: parseUUID(fixture.runtimeID)}); err != nil {
		t.Fatalf("persist initial Agent delivery: %v", err)
	}
	if err := testHandler.createMixedRunDeliveryObligation(fixture.ctx, uuidToString(fixture.runID), uuidToString(fixture.runAgentID), fixture.message.ID, fixture.agentID); err != nil {
		t.Fatalf("create initial delivery obligation: %v", err)
	}
	newMessage := fixture.insertMessage(t, "new obligation racing old acknowledgement")
	identity := daemonws.ClientIdentity{DaemonID: fixture.daemonID, WorkspaceID: testWorkspaceID}
	ack := protocol.AgentDeliverAckPayload{AgentID: fixture.agentID, Seq: fixture.message.Seq, DeliveryID: "message:" + fixture.message.ID + ":agent:" + fixture.agentID}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		<-start
		errs <- testHandler.HandleAgentDeliveryAck(context.Background(), identity, ack)
	}()
	go func() {
		defer wg.Done()
		<-start
		errs <- testHandler.createMixedRunDeliveryObligation(context.Background(), uuidToString(fixture.runID), uuidToString(fixture.runAgentID), newMessage.ID, fixture.agentID)
	}()
	go func() {
		defer wg.Done()
		<-start
		_, _ = fixture.runs.TransitionStatus(context.Background(), fixture.runID, "running", "quiet_candidate")
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent delivery activity failed: %v", err)
		}
	}

	var status string
	var pending int64
	if err := testPool.QueryRow(fixture.ctx, `SELECT status, pending_delivery_count FROM env_dispatch_run WHERE run_id = $1`, fixture.runID).Scan(&status, &pending); err != nil {
		t.Fatalf("load post-race run activity: %v", err)
	}
	if status == "quiet_candidate" || pending != 1 {
		t.Fatalf("post-ack/new-obligation race left status=%q pending=%d, want running with one pending obligation", status, pending)
	}
	var completed, queued int
	if err := testPool.QueryRow(fixture.ctx, `
		SELECT count(*) FILTER (WHERE state = 'completed'), count(*) FILTER (WHERE state = 'queued')
		FROM env_dispatch_delivery_obligation WHERE run_id = $1`, fixture.runID).Scan(&completed, &queued); err != nil {
		t.Fatalf("load post-race obligation states: %v", err)
	}
	if completed != 1 || queued != 1 {
		t.Fatalf("post-race obligation states completed=%d queued=%d, want one each", completed, queued)
	}
}
