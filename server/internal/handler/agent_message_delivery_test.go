package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestAgentMessageRecoveryPagesStableSequenceFenceAcrossTargets(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "message-recovery-runner-" + uuid.NewString()[:8]
	runtimeID := seedMachineLockedRuntime(t, daemonID, "message recovery")
	agentID := createHandlerTestAgentOnRuntime(t, "message-recovery-"+uuid.NewString()[:8], runtimeID)
	channelOne := seedChannelForTest(t, "message-recovery-one-"+uuid.NewString(), testUserID)
	channelTwo := seedChannelForTest(t, "message-recovery-two-"+uuid.NewString(), testUserID)
	for _, channelID := range []string{channelOne, channelTwo} {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
			VALUES ($1, $2, 'agent', $3)
			ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
			t.Fatalf("add Agent member: %v", err)
		}
	}
	boundaries := make(map[string]int64)
	rows, err := testPool.Query(ctx, `
		SELECT target, COALESCE(max(seq), 0)
		FROM agent_message_delivery
		WHERE workspace_id = $1 AND agent_id = $2
		GROUP BY target`, testWorkspaceID, agentID)
	if err != nil {
		t.Fatalf("load initial Agent boundaries: %v", err)
	}
	for rows.Next() {
		var target string
		var sequence int64
		if err := rows.Scan(&target, &sequence); err != nil {
			rows.Close()
			t.Fatalf("scan initial Agent boundary: %v", err)
		}
		boundaries[target] = sequence
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("iterate initial Agent boundaries: %v", err)
	}
	rows.Close()
	insert := func(channelID, content string) ChannelMessageResponse {
		message, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", content, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr(uuid.NewString()), 0)
		if err != nil {
			t.Fatalf("insert canonical Message: %v", err)
		}
		channel, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
		if !found {
			t.Fatalf("load channel %s", channelID)
		}
		testHandler.deliverCanonicalMessageToChannelAgents(ctx, channel, message)
		return message
	}
	before := []ChannelMessageResponse{
		insert(channelOne, "one"),
		insert(channelOne, "two"),
		insert(channelTwo, "three"),
	}
	identity := daemonws.ClientIdentity{DaemonID: daemonID, WorkspaceID: testWorkspaceID}
	request := protocol.AgentRecoveryRequest{AgentID: agentID, RecoveryID: uuid.NewString(), Boundaries: boundaries, Limit: 2}
	page, err := testHandler.HandleAgentMessageRecovery(ctx, identity, request)
	if err != nil {
		t.Fatalf("first recovery page: %v", err)
	}
	if !page.HasMore || page.NextCursor == "" || len(page.Messages) != 2 || page.SnapshotID == "" || page.HighWatermark != page.SnapshotID {
		t.Fatalf("first page = %+v", page)
	}
	notifier := &capturedAgentDeliveryNotifier{}
	previousNotifier := testHandler.AgentDeliveryNotifier
	testHandler.AgentDeliveryNotifier = notifier
	t.Cleanup(func() { testHandler.AgentDeliveryNotifier = previousNotifier })
	liveRecorder := sendChannelMessageForTest(t, channelOne, testUserID, map[string]any{
		"content":           "created after recovery fence",
		"client_message_id": uuid.NewString(),
	})
	if liveRecorder.Code != http.StatusCreated {
		t.Fatalf("create live Message during recovery: status=%d body=%s", liveRecorder.Code, liveRecorder.Body.String())
	}
	var live ChannelMessageResponse
	if err := json.Unmarshal(liveRecorder.Body.Bytes(), &live); err != nil {
		t.Fatalf("decode live Message: %v", err)
	}
	if notifier.payload.Message.ID != live.ID || notifier.payload.AgentID != agentID {
		t.Fatalf("post-fence Message was not covered by live delivery: %+v", notifier.payload)
	}
	all := append([]protocol.AgentMessageProjection(nil), page.Messages...)
	for page.HasMore {
		request.SnapshotID = page.SnapshotID
		request.Cursor = page.NextCursor
		page, err = testHandler.HandleAgentMessageRecovery(ctx, identity, request)
		if err != nil {
			t.Fatalf("next recovery page: %v", err)
		}
		all = append(all, page.Messages...)
	}
	if len(all) != len(before) {
		t.Fatalf("snapshot returned %d Messages, want %d: %+v", len(all), len(before), all)
	}
	gotIDs := make(map[string]bool, len(all))
	for _, message := range all {
		gotIDs[message.ID] = true
	}
	for _, message := range before {
		if !gotIDs[message.ID] {
			t.Fatalf("snapshot omitted Message %s", message.ID)
		}
	}
	if gotIDs[live.ID] {
		t.Fatalf("post-fence live Message %s leaked into recovery snapshot", live.ID)
	}

	boundary := map[string]int64{"channel:" + channelOne: before[1].Seq}
	page, err = testHandler.HandleAgentMessageRecovery(ctx, identity, protocol.AgentRecoveryRequest{AgentID: agentID, RecoveryID: uuid.NewString(), Boundaries: boundary, Limit: agentMessageRecoveryMaxPage + 50})
	if err != nil {
		t.Fatalf("boundary recovery: %v", err)
	}
	for _, message := range page.Messages {
		if message.Target == "channel:"+channelOne && message.Seq <= before[1].Seq {
			t.Fatalf("replayed covered Message: %+v", message)
		}
	}
	foundAbsentTarget := false
	for _, message := range page.Messages {
		if message.Target == "channel:"+channelTwo {
			foundAbsentTarget = true
		}
	}
	if !foundAbsentTarget {
		t.Fatal("target absent from boundary map was omitted")
	}
}

func TestAgentMessageRecoveryRejectsCorruptStateAndWrongRuntime(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	daemonID := "message-recovery-invalid-" + uuid.NewString()[:8]
	runtimeID := seedMachineLockedRuntime(t, daemonID, "message recovery invalid")
	agentID := createHandlerTestAgentOnRuntime(t, "message-recovery-invalid-"+uuid.NewString()[:8], runtimeID)
	identity := daemonws.ClientIdentity{DaemonID: daemonID, WorkspaceID: testWorkspaceID}
	forgedCursorRaw, err := json.Marshal(agentMessageRecoveryCursor{
		Target: "channel:one", Seq: 1, ID: uuid.NewString(), SnapshotHash: "forged", Checksum: "forged",
	})
	if err != nil {
		t.Fatalf("marshal forged cursor: %v", err)
	}
	for name, request := range map[string]protocol.AgentRecoveryRequest{
		"missing recovery id":       {AgentID: agentID, Boundaries: map[string]int64{}},
		"regressed boundary":        {AgentID: agentID, RecoveryID: uuid.NewString(), Boundaries: map[string]int64{"channel:one": -1}},
		"damaged snapshot":          {AgentID: agentID, RecoveryID: uuid.NewString(), Boundaries: map[string]int64{}, SnapshotID: "not-base64"},
		"damaged cursor":            {AgentID: agentID, RecoveryID: uuid.NewString(), Boundaries: map[string]int64{}, Cursor: "not-base64"},
		"well formed forged cursor": {AgentID: agentID, RecoveryID: uuid.NewString(), Boundaries: map[string]int64{}, Cursor: base64.RawURLEncoding.EncodeToString(forgedCursorRaw)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := testHandler.HandleAgentMessageRecovery(context.Background(), identity, request); err == nil {
				t.Fatal("invalid recovery state was accepted")
			}
		})
	}
	wrong := daemonws.ClientIdentity{DaemonID: "wrong-" + daemonID, WorkspaceID: testWorkspaceID}
	if _, err := testHandler.HandleAgentMessageRecovery(context.Background(), wrong, protocol.AgentRecoveryRequest{AgentID: agentID, RecoveryID: uuid.NewString(), Boundaries: map[string]int64{}}); err == nil {
		t.Fatal("wrong runtime recovered Agent Messages")
	}
	if _, err := testHandler.HandleAgentMessageRecovery(context.Background(), identity, protocol.AgentRecoveryRequest{AgentID: "not-a-uuid", RecoveryID: uuid.NewString(), Boundaries: map[string]int64{}}); err == nil {
		t.Fatal("invalid Agent UUID reached the trusted UUID parser")
	}
}

func TestAgentMessageHandoffReceiptIsIdempotent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "message-handoff-runner-" + uuid.NewString()[:8]
	runtimeID := seedMachineLockedRuntime(t, daemonID, "message handoff")
	agentID := createHandlerTestAgentOnRuntime(t, "message-handoff-activity-"+uuid.NewString()[:8], runtimeID)
	identity := daemonws.ClientIdentity{DaemonID: daemonID, WorkspaceID: testWorkspaceID}
	payload := protocol.AgentMessageHandoffPayload{AgentID: agentID, RuntimeID: runtimeID, HandoffID: uuid.NewString(), Count: 2, Targets: []string{"channel:one"}}
	if err := testHandler.HandleAgentMessageHandoff(ctx, identity, payload); err != nil {
		t.Fatalf("first handoff receipt: %v", err)
	}
	if err := testHandler.HandleAgentMessageHandoff(ctx, identity, payload); err != nil {
		t.Fatalf("duplicate handoff receipt: %v", err)
	}
	var count int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_message_handoff_receipt
		WHERE workspace_id = $1 AND agent_id = $2 AND handoff_id = $3`, testWorkspaceID, agentID, payload.HandoffID).Scan(&count); err != nil {
		t.Fatalf("count handoff receipt: %v", err)
	}
	if count != 1 {
		t.Fatalf("Message handoff receipt count = %d, want 1", count)
	}
}

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

func TestCanonicalMessageProjectsAgentDeliveryEnvelope(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "daemon-message-delivery-envelope-" + uuid.NewString()[:8]
	runtimeID := seedMachineLockedRuntime(t, daemonID, "message delivery envelope")
	agentID := createHandlerTestAgentOnRuntime(t, "message-delivery-envelope-"+uuid.NewString()[:8], runtimeID)
	channelID := seedChannelForTest(t, "message-delivery-envelope-"+uuid.NewString(), testUserID)
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
	want := protocol.AgentMessageProjection{ID: message.ID, Target: "channel:" + channelID, Seq: message.Seq, Content: "canonical body", Parts: message.Parts}
	got := notifier.payload.Message
	if notifier.workspaceID != testWorkspaceID || notifier.daemonID != daemonID || notifier.payload.AgentID != agentID || notifier.payload.DeliveryID == "" || got.ID != want.ID || got.Target != want.Target || got.Seq != want.Seq || got.Content != want.Content || len(got.Parts) != len(want.Parts) {
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
	_, got = deliver("@second only", []protocol.MessagePart{{Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "agent", RefID: secondAgentID, Label: "@second"}}, nil)
	assertRecipients("mention", got, secondAgentID)

	testHandler.followChannelThreadAgentUnlessExplicitlyUnfollowed(ctx, parseUUID(channelID), parseUUID(root.ID), parseUUID(firstAgentID))
	_, got = deliver("thread participant follow-up", nil, &root.ID)
	assertRecipients("thread participant", got, firstAgentID)
	_, got = deliver("@second thread", []protocol.MessagePart{{Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "agent", RefID: secondAgentID, Label: "@second"}}, &root.ID)
	assertRecipients("thread mention", got, secondAgentID)
}
