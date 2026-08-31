package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestGraphMemoryAgentObserveActivityRenewsTimestampLease(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	workspaceID := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryMember(t, workspaceID, "owner")
	channelID := createGraphMemoryTestChannel(t, workspaceID)
	const idleGraceSeconds = 90
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_profile
		 (workspace_id,memory_type,graph_memory_mode,memory_agent_idle_grace_seconds)
		VALUES($1,'graph','agent',$2)`, workspaceID, idleGraceSeconds); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_channel_agent
		 (channel_id,workspace_id,handle,display_name,status)
		VALUES($1,$2,$3,$4,'active')`, channelID, workspaceID, "memory-activity-test", "Memory activity test"); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO graph_memory_agent_state(channel_id) VALUES($1)`, channelID); err != nil {
		t.Fatal(err)
	}

	observedAt := time.Now().UTC().Truncate(time.Microsecond)
	control := service.NewPostgresGraphMemoryAgentControlPlane(testPool)
	if err := control.ObserveActivity(ctx, workspaceID.String(), channelID.String(), observedAt); err != nil {
		t.Fatalf("ObserveActivity: %v", err)
	}

	var leaseExpiresAt time.Time
	if err := testPool.QueryRow(ctx, `SELECT lease_expires_at FROM graph_memory_agent_state WHERE channel_id=$1`, channelID).Scan(&leaseExpiresAt); err != nil {
		t.Fatal(err)
	}
	want := observedAt.Add(idleGraceSeconds * time.Second)
	if !leaseExpiresAt.Equal(want) {
		t.Fatalf("lease_expires_at=%s, want %s", leaseExpiresAt.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}

	claim, err := service.NewGraphMemoryAgentRunStore(testPool).Claim(
		ctx, workspaceID.String(), channelID.String(), "channel", channelID.String(), "wake after observed activity", 0,
	)
	if err != nil {
		t.Fatalf("Claim after ObserveActivity: %v", err)
	}
	if claim.RunID == "" || claim.TrajectoryID == "" || claim.Resumed {
		t.Fatalf("claim after ObserveActivity = %+v", claim)
	}
}

func TestGraphMemoryAgentRunStoreFencingIdempotencyQuotaAndCitation(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	workspaceID := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryMember(t, workspaceID, "owner")
	channelID := createGraphMemoryTestChannel(t, workspaceID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_profile
		 (workspace_id,memory_type,graph_memory_mode,memory_agent_max_tokens_per_hour,memory_agent_max_nodes_per_minute)
		VALUES($1,'graph','agent',1000,5)`, workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_channel_agent
		 (channel_id,workspace_id,handle,display_name,status)
		VALUES($1,$2,$3,$4,'active')`, channelID, workspaceID, "memory-run-test", "Memory run test"); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_agent_state(channel_id,lease_expires_at)
		VALUES($1,$2)`, channelID, time.Now().UTC().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	store := service.NewGraphMemoryAgentRunStore(testPool)
	claim, err := store.Claim(ctx, workspaceID.String(), channelID.String(), "channel", channelID.String(), "initial immutable query", 7)
	if err != nil {
		t.Fatal(err)
	}
	if claim.RunID == "" || claim.TrajectoryID == "" || claim.FencingToken <= 1 || claim.Resumed || claim.TokenBudgetLeft != 1000 {
		t.Fatalf("claim = %+v", claim)
	}
	resumed, err := store.Claim(ctx, workspaceID.String(), channelID.String(), "channel", channelID.String(), "replacement must be ignored", 9)
	if err != nil {
		t.Fatal(err)
	}
	if !resumed.Resumed || resumed.RunID != claim.RunID || resumed.InitialQuery != claim.InitialQuery || resumed.GraphVersion != 7 {
		t.Fatalf("resumed claim = %+v, initial = %+v", resumed, claim)
	}
	owner := service.NewGraphMemoryAgentExecutionOwner(store)
	execution, err := owner.ClaimAndStart(ctx, service.GraphMemoryAgentExecutionRequest{
		WorkspaceID: workspaceID.String(), ChannelID: channelID.String(), TargetKind: "channel", TargetID: channelID.String(),
		InitialQuery: "ignored on resumed claim", GraphVersion: 7, ConsumedSeq: 17,
		Store: memorygraph.NewStore(t.TempDir()), ExploreConfig: memorygraph.DefaultExploreConfig(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if execution.Claim.RunID != claim.RunID || execution.BaseURL == "" || execution.Token == "" {
		t.Fatalf("execution = %+v", execution)
	}
	defer execution.Shutdown(context.Background())

	if err := store.ValidateToolOperationQuota(ctx, claim.RunID, claim.FencingToken, "explore", "too-many", json.RawMessage(`{"node_ids":["1","2","3","4","5"]}`)); !errors.Is(err, service.ErrGraphMemoryAgentQuotaExceeded) {
		t.Fatalf("nodes-per-call quota err=%v", err)
	}
	quotaRequest := json.RawMessage(`{"node_ids":["1","2","3","4"]}`)
	quotaReservation, err := store.ReserveToolOperation(ctx, claim.RunID, claim.FencingToken, "quota-1", "explore", quotaRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteToolOperation(ctx, claim.RunID, claim.FencingToken, quotaReservation.OperationID, json.RawMessage(`{"nodes":[]}`), ""); err != nil {
		t.Fatal(err)
	}
	if err := store.ValidateToolOperationQuota(ctx, claim.RunID, claim.FencingToken, "explore", "quota-2", json.RawMessage(`{"node_ids":["5","6"]}`)); !errors.Is(err, service.ErrGraphMemoryAgentQuotaExceeded) {
		t.Fatalf("nodes-per-minute quota err=%v", err)
	}

	request := json.RawMessage(`{"query":"dispatch","limit":4}`)
	reservation, err := store.ReserveToolOperation(ctx, claim.RunID, claim.FencingToken, "start-1", "start", request)
	if err != nil || reservation.OperationID == "" || reservation.Pending || reservation.Replay {
		t.Fatalf("reservation = %+v err=%v", reservation, err)
	}
	pending, err := store.ReserveToolOperation(ctx, claim.RunID, claim.FencingToken, "start-1", "start", json.RawMessage(`{"limit":4,"query":"dispatch"}`))
	if err != nil || !pending.Pending || pending.OperationID != reservation.OperationID {
		t.Fatalf("pending replay = %+v err=%v", pending, err)
	}
	if _, err := store.ReserveToolOperation(ctx, claim.RunID, claim.FencingToken, "start-1", "redirect", request); !errors.Is(err, service.ErrGraphMemoryToolReplayConflict) {
		t.Fatalf("conflicting replay err=%v", err)
	}
	response := json.RawMessage(`{"trajectory_id":"` + claim.TrajectoryID + `","nodes":["n1"]}`)
	if err := store.CompleteToolOperation(ctx, claim.RunID, claim.FencingToken, reservation.OperationID, response, ""); err != nil {
		t.Fatal(err)
	}
	replay, err := store.ReserveToolOperation(ctx, claim.RunID, claim.FencingToken, "start-1", "start", request)
	if err != nil || !replay.Replay || replay.Pending || string(replay.Response) == "" {
		t.Fatalf("terminal replay = %+v err=%v", replay, err)
	}

	steeringMessage, err := testHandler.insertChannelMessage(ctx, channelID, workspaceID, "user", parseUUID(testUserID), "Tester", "focus on routing", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	steeringTx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	steeringProjection := protocol.AgentMessageProjection{
		ID: steeringMessage.ID, ChannelID: channelID.String(), Target: "channel:" + channelID.String(),
		Seq: steeringMessage.Seq, Content: steeringMessage.Content, Directed: true,
		InitiatorType: "member", InitiatorID: testUserID, InitiatorName: "Tester",
	}
	if err := persistGraphMemoryAgentSteeringEvent(ctx, steeringTx, steeringProjection); err != nil {
		_ = steeringTx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := persistGraphMemoryAgentSteeringEvent(ctx, steeringTx, steeringProjection); err != nil {
		_ = steeringTx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := steeringTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var steeringCount int
	var steeringOrdinal int64
	if err := testPool.QueryRow(ctx, `SELECT count(*)::int,min(ordinal) FROM graph_memory_agent_steering_event WHERE run_id=$1::uuid`, claim.RunID).Scan(&steeringCount, &steeringOrdinal); err != nil {
		t.Fatal(err)
	}
	if steeringCount != 1 || steeringOrdinal != 1 {
		t.Fatalf("steering events count=%d ordinal=%d", steeringCount, steeringOrdinal)
	}
	if err := store.RecordViewedNodes(ctx, claim.RunID, claim.FencingToken, []string{"n1", "n1"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddUsage(ctx, claim.RunID, claim.FencingToken, 400, 500); err != nil {
		t.Fatal(err)
	}
	if err := store.AddUsage(ctx, claim.RunID, claim.FencingToken, 60, 50); !errors.Is(err, service.ErrGraphMemoryAgentQuotaExceeded) {
		t.Fatalf("quota err=%v", err)
	}
	var persistedInputTokens, persistedOutputTokens int64
	if err := testPool.QueryRow(ctx, `
		SELECT input_tokens,output_tokens
		FROM graph_memory_agent_run
		WHERE id=$1::uuid`, claim.RunID).Scan(&persistedInputTokens, &persistedOutputTokens); err != nil {
		t.Fatal(err)
	}
	if persistedInputTokens != 460 || persistedOutputTokens != 550 {
		t.Fatalf("persisted usage input=%d output=%d, want 460/550", persistedInputTokens, persistedOutputTokens)
	}
	patch := json.RawMessage(`{"objective":"checkpointed objective","viewed_node_ids":["n1"],"open_questions":["next"]}`)
	if err := store.Finish(ctx, claim.RunID, claim.FencingToken, "submitted", 17, patch, []service.GraphMemoryAgentCitationInput{{
		NodeID: "n1", GraphVersion: 7, Level: "1", EpistemicStatus: "supported", Tags: []string{"routing"},
		Title: "Dispatch", FirstParagraph: "Dispatch detail", Excerpt: "Dispatch detail", ContentHash: "sha256:n1",
	}}); err != nil {
		t.Fatal(err)
	}
	var runStatus string
	var consumedSeq int64
	var activeRun bool
	var citationCount int
	if err := testPool.QueryRow(ctx, `
		SELECT r.status,s.consumed_seq,s.active_run_id IS NOT NULL,
		       (SELECT count(*)::int FROM graph_memory_agent_citation WHERE trajectory_id=$2::uuid)
		FROM graph_memory_agent_run r JOIN graph_memory_agent_state s ON s.channel_id=r.channel_id
		WHERE r.id=$1::uuid`, claim.RunID, claim.TrajectoryID).Scan(&runStatus, &consumedSeq, &activeRun, &citationCount); err != nil {
		t.Fatal(err)
	}
	if runStatus != "submitted" || consumedSeq != 17 || activeRun || citationCount != 1 {
		t.Fatalf("terminal state status=%s seq=%d active=%v citations=%d", runStatus, consumedSeq, activeRun, citationCount)
	}
	if _, err := store.Claim(ctx, workspaceID.String(), channelID.String(), "ambient", "", "next query", 7); !errors.Is(err, service.ErrGraphMemoryAgentQuotaExceeded) {
		t.Fatalf("fresh claim after over-quota usage err=%v", err)
	}
	message, err := testHandler.insertChannelMessage(ctx, channelID, workspaceID, "user", parseUUID(testUserID), "Tester", "cited answer", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE graph_memory_agent_citation SET message_id=$2 WHERE trajectory_id=$1::uuid`, claim.TrajectoryID, parseUUID(message.ID)); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	httpReq := newRequest(http.MethodGet, "/api/workspaces/"+workspaceID.String()+"/graph-memory/messages/"+message.ID+"/citations", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("id", workspaceID.String())
	routeContext.URLParams.Add("messageId", message.ID)
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, routeContext))
	testHandler.GetGraphMemoryMessageCitations(recorder, httpReq)
	if recorder.Code != http.StatusOK {
		t.Fatalf("citation API status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var citationPayload graphMemoryMessageCitationsResponse
	if json.Unmarshal(recorder.Body.Bytes(), &citationPayload) != nil || len(citationPayload.Items) != 1 || citationPayload.Items[0].ContentHash != "sha256:n1" {
		t.Fatalf("citation API payload=%s", recorder.Body.String())
	}
	if err := store.CompleteToolOperation(ctx, claim.RunID, claim.FencingToken, reservation.OperationID, response, ""); !errors.Is(err, service.ErrGraphMemoryAgentRunFenced) {
		t.Fatalf("stale completion err=%v", err)
	}
}

func TestGraphMemoryAgentRunStoreRejectsUnviewedCitationAtomically(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	workspaceID := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryMember(t, workspaceID, "owner")
	channelID := createGraphMemoryTestChannel(t, workspaceID)
	_, _ = testPool.Exec(ctx, `INSERT INTO graph_memory_profile(workspace_id,memory_type,graph_memory_mode) VALUES($1,'graph','agent')`, workspaceID)
	_, _ = testPool.Exec(ctx, `INSERT INTO graph_memory_channel_agent(channel_id,workspace_id,handle,display_name,status) VALUES($1,$2,$3,$4,'active')`, channelID, workspaceID, "memory-citation-test", "Memory citation test")
	_, _ = testPool.Exec(ctx, `INSERT INTO graph_memory_agent_state(channel_id,lease_expires_at) VALUES($1,now()+interval '1 minute')`, channelID)
	store := service.NewGraphMemoryAgentRunStore(testPool)
	claim, err := store.Claim(ctx, workspaceID.String(), channelID.String(), "ambient", "", "query", 1)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Finish(ctx, claim.RunID, claim.FencingToken, "submitted", 1, json.RawMessage(`{}`), []service.GraphMemoryAgentCitationInput{{NodeID: "never-viewed", GraphVersion: 1, ContentHash: "hash"}})
	if err == nil {
		t.Fatal("unviewed citation unexpectedly accepted")
	}
	var status string
	if err := testPool.QueryRow(ctx, `SELECT status FROM graph_memory_agent_run WHERE id=$1::uuid`, claim.RunID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "running" {
		t.Fatalf("run status=%s, want running after rolled-back submission", status)
	}
}

func TestGraphMemoryAgentGatewayBindsManagedPrincipalAndSurvivesStatelessRequests(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	workspaceID := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryMember(t, workspaceID, "owner")
	channelID := createGraphMemoryTestChannel(t, workspaceID)
	if _, err := testPool.Exec(ctx, `INSERT INTO graph_memory_profile(workspace_id,memory_type,graph_memory_mode,explore_max_rounds) VALUES($1,'graph','agent',6)`, workspaceID); err != nil {
		t.Fatal(err)
	}
	var runtimeID, agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime(workspace_id,daemon_id,name,runtime_mode,provider,status,device_info,metadata,visibility,last_seen_at)
		VALUES($1,$2,$3,'local','pi','online','','{}','private',now()) RETURNING id::text`,
		workspaceID, "graph-gateway-daemon-"+workspaceID.String()[:8], "graph-gateway-runtime").Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent(workspace_id,name,display_name,runtime_mode,runtime_config,runtime_id,owner_id,managed_role,instructions)
		VALUES($1,$2,'Memory gateway','local','{}',$3,$4,'graph_memory_channel','managed memory') RETURNING id::text`,
		workspaceID, "memory-gateway-"+channelID.String()[:8], runtimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_channel_agent(channel_id,workspace_id,agent_id,runtime_id,sponsor_user_id,handle,display_name,status)
		VALUES($1,$2,$3,$4,$5,$6,'Memory gateway','active')`,
		channelID, workspaceID, agentID, runtimeID, testUserID, "memory-gateway-"+channelID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO channel_member(channel_id,workspace_id,member_type,member_id,role) VALUES($1,$2,'agent',$3,'member')`, channelID, workspaceID, agentID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO graph_memory_agent_state(channel_id,lease_expires_at) VALUES($1,now()+interval '5 minutes')`, channelID); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	t.Setenv("MULTICA_WORKSPACES_ROOT", root)
	graphDir, err := memorygraph.EnsureScopedDir(root, workspaceID.String(), memorygraph.GraphDirKindChannel, channelID.String())
	if err != nil {
		t.Fatal(err)
	}
	graphStore := memorygraph.NewStore(graphDir)
	if err := graphStore.Init(); err != nil {
		t.Fatal(err)
	}
	node := &memorygraph.Node{
		NodeID: "routing-node", Body: "dispatch routing retries use exponential backoff", Visibility: "channel", ChannelID: channelID.String(),
		CreatedBy: memorygraph.CreatorIngester, CreatedVersion: 1, UpdatedVersion: 1, ObservedAt: time.Now().UTC(),
	}
	if err := graphStore.SaveNode(1, node); err != nil {
		t.Fatal(err)
	}
	channel, found := testHandler.getChannel(ctx, workspaceID.String(), channelID)
	if !found {
		t.Fatal("gateway channel not found")
	}
	trigger, err := testHandler.insertChannelMessage(ctx, channelID, workspaceID, "user", parseUUID(testUserID), "Tester", "inspect routing", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	managedAgent, err := testHandler.Queries.GetAgent(ctx, parseUUID(agentID))
	if err != nil {
		t.Fatal(err)
	}
	delivery, _, err := persistCanonicalMessageDelivery(ctx, testPool, channel, trigger, managedAgent)
	if err != nil {
		t.Fatal(err)
	}
	if !delivery.Message.GraphMemoryTools {
		t.Fatal("managed Agent delivery did not receive Graph tool capability")
	}

	testHandler.GraphMemoryAgentGateway = service.NewGraphMemoryAgentGateway(testPool, graphMemoryTestProviderResolver("test-provider", "test-model"))
	callAs := func(principalAgentID, operation, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/agent/channels/"+channelID.String()+"/graph-memory/"+operation, bytes.NewBufferString(body))
		route := chi.NewRouteContext()
		route.URLParams.Add("channelId", channelID.String())
		route.URLParams.Add("operation", operation)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, route))
		req = req.WithContext(middleware.WithAgentPrincipal(req.Context(), middleware.AgentPrincipal{AgentID: principalAgentID, WorkspaceID: workspaceID.String(), ActorSource: "agent_credential"}))
		recorder := httptest.NewRecorder()
		testHandler.GraphMemoryAgentTool(recorder, req)
		return recorder
	}
	denied := callAs("00000000-0000-4000-8000-000000000001", "start", `{"query":"steal scope","idempotency_key":"denied"}`)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("foreign agent status=%d body=%s", denied.Code, denied.Body.String())
	}
	call := func(operation, body string) *httptest.ResponseRecorder {
		return callAs(agentID, operation, body)
	}

	start := call("start", `{"query":"dispatch routing","idempotency_key":"start-message-1","workspace_id":"00000000-0000-4000-8000-000000000999","graph_version":999}`)
	if start.Code != http.StatusOK {
		t.Fatalf("start status=%d body=%s", start.Code, start.Body.String())
	}
	var startPayload struct {
		TrajectoryID string `json:"trajectory_id"`
		GraphVersion int    `json:"graph_version"`
	}
	if err := json.Unmarshal(start.Body.Bytes(), &startPayload); err != nil || startPayload.TrajectoryID == "" || startPayload.GraphVersion != 1 {
		t.Fatalf("start payload=%s err=%v", start.Body.String(), err)
	}
	futureMessage, err := testHandler.insertChannelMessage(ctx, channelID, workspaceID, "user", parseUUID(testUserID), "Tester", "arrived during exploration", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	explore := call("explore", `{"trajectory_id":"`+startPayload.TrajectoryID+`","node_ids":["routing-node"],"idempotency_key":"explore-message-1"}`)
	if explore.Code != http.StatusOK {
		t.Fatalf("explore status=%d body=%s", explore.Code, explore.Body.String())
	}
	var explorePayload struct {
		Round int `json:"round"`
	}
	if err := json.Unmarshal(explore.Body.Bytes(), &explorePayload); err != nil || explorePayload.Round != 1 {
		t.Fatalf("first explore payload=%s err=%v", explore.Body.String(), err)
	}
	secondExplore := call("explore", `{"trajectory_id":"`+startPayload.TrajectoryID+`","node_ids":["routing-node"],"idempotency_key":"explore-message-2"}`)
	if secondExplore.Code != http.StatusOK {
		t.Fatalf("second explore status=%d body=%s", secondExplore.Code, secondExplore.Body.String())
	}
	if err := json.Unmarshal(secondExplore.Body.Bytes(), &explorePayload); err != nil || explorePayload.Round != 2 {
		t.Fatalf("second explore payload=%s err=%v", secondExplore.Body.String(), err)
	}
	submit := call("submit", `{"trajectory_id":"`+startPayload.TrajectoryID+`","found":true,"summary":"Routing uses bounded retry.","node_ids":["routing-node"],"idempotency_key":"submit-message-1"}`)
	if submit.Code != http.StatusOK {
		t.Fatalf("submit status=%d body=%s", submit.Code, submit.Body.String())
	}
	var citationCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*)::int FROM graph_memory_agent_citation citation JOIN graph_memory_agent_trajectory trajectory ON trajectory.id=citation.trajectory_id WHERE trajectory.id=$1::uuid`, startPayload.TrajectoryID).Scan(&citationCount); err != nil {
		t.Fatal(err)
	}
	if citationCount != 1 {
		t.Fatalf("citation count=%d, want 1", citationCount)
	}
	usageRequest := httptest.NewRequest(http.MethodPost, "/api/agent/channels/"+channelID.String()+"/graph-memory-usage", bytes.NewBufferString(`{"input_tokens":12,"output_tokens":3}`))
	usageRoute := chi.NewRouteContext()
	usageRoute.URLParams.Add("channelId", channelID.String())
	usageRequest = usageRequest.WithContext(context.WithValue(usageRequest.Context(), chi.RouteCtxKey, usageRoute))
	usageRequest = usageRequest.WithContext(middleware.WithAgentPrincipal(usageRequest.Context(), middleware.AgentPrincipal{AgentID: agentID, WorkspaceID: workspaceID.String(), ActorSource: "agent_credential"}))
	usageRecorder := httptest.NewRecorder()
	testHandler.GraphMemoryAgentUsage(usageRecorder, usageRequest)
	if usageRecorder.Code != http.StatusOK {
		t.Fatalf("usage status=%d body=%s", usageRecorder.Code, usageRecorder.Body.String())
	}
	var recordedInput, recordedOutput int64
	if err := testPool.QueryRow(ctx, `SELECT input_tokens,output_tokens FROM graph_memory_agent_run WHERE id=(SELECT run_id FROM graph_memory_agent_trajectory WHERE id=$1::uuid)`, startPayload.TrajectoryID).Scan(&recordedInput, &recordedOutput); err != nil {
		t.Fatal(err)
	}
	if recordedInput != 12 || recordedOutput != 3 {
		t.Fatalf("recorded usage input=%d output=%d", recordedInput, recordedOutput)
	}
	var consumedSeq int64
	if err := testPool.QueryRow(ctx, `SELECT consumed_seq FROM graph_memory_agent_state WHERE channel_id=$1`, channelID).Scan(&consumedSeq); err != nil {
		t.Fatal(err)
	}
	if consumedSeq != trigger.Seq || consumedSeq >= futureMessage.Seq {
		t.Fatalf("consumed_seq=%d, trigger=%d future=%d", consumedSeq, trigger.Seq, futureMessage.Seq)
	}

	secondStart := call("start", `{"query":"follow-up routing","idempotency_key":"start-message-2"}`)
	if secondStart.Code != http.StatusOK {
		t.Fatalf("second start status=%d body=%s", secondStart.Code, secondStart.Body.String())
	}
	if err := json.Unmarshal(secondStart.Body.Bytes(), &startPayload); err != nil || startPayload.TrajectoryID == "" {
		t.Fatalf("second start payload=%s err=%v", secondStart.Body.String(), err)
	}
	redirect := call("redirect", `{"trajectory_id":"`+startPayload.TrajectoryID+`","query":"focus on retry limits","steering_message_id":"`+futureMessage.ID+`","idempotency_key":"redirect-message-2"}`)
	if redirect.Code != http.StatusOK {
		t.Fatalf("redirect status=%d body=%s", redirect.Code, redirect.Body.String())
	}
	checkpoint := call("checkpoint", `{"trajectory_id":"`+startPayload.TrajectoryID+`","state":{"objective":"retry limits","open_questions":["cap"]},"idempotency_key":"checkpoint-message-2"}`)
	if checkpoint.Code != http.StatusOK {
		t.Fatalf("checkpoint status=%d body=%s", checkpoint.Code, checkpoint.Body.String())
	}
}
