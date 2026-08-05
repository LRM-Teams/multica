package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/events"
	serverhandler "github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type idleMessageFakeRuntime struct {
	mu      sync.Mutex
	batches [][]agent.ResidentMessage
	notices []agent.ResidentPendingNotice
}

func (r *idleMessageFakeRuntime) AcceptPendingNotice(_ context.Context, notice agent.ResidentPendingNotice) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notices = append(r.notices, notice)
	return nil
}

func (r *idleMessageFakeRuntime) Execute(context.Context, string, agent.ExecOptions) (*agent.Session, error) {
	return nil, nil
}

func (r *idleMessageFakeRuntime) AcceptMessageBatch(_ context.Context, messages []agent.ResidentMessage) (agent.ResidentMessageAcceptance, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batches = append(r.batches, append([]agent.ResidentMessage(nil), messages...))
	done := make(chan error)
	close(done)
	return agent.ResidentMessageAcceptance{Done: done}, nil
}

func (r *idleMessageFakeRuntime) snapshot() [][]agent.ResidentMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]agent.ResidentMessage(nil), r.batches...)
}

func (r *idleMessageFakeRuntime) noticeSnapshot() []agent.ResidentPendingNotice {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]agent.ResidentPendingNotice(nil), r.notices...)
}

func TestMessageRealServerMachineProxyRuntimeAcceptance(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil || pool.Ping(context.Background()) != nil {
		t.Skip("acceptance database is unavailable")
	}
	t.Cleanup(pool.Close)
	workspaceID, userID, runtimeID, agentID, channelID, member := seedIdleMessageAcceptanceFixture(t, pool)

	root := t.TempDir()
	if err := seedIdleMessageAcceptanceBoundaries(context.Background(), pool, root, workspaceID, agentID); err != nil {
		t.Fatalf("seed initial Context Boundaries: %v", err)
	}
	fakeRuntime := &idleMessageFakeRuntime{}
	d := &Daemon{
		logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		messageCoordinators: make(map[string]*MessageCoordinator),
		canonicalRuntimes:   newCanonicalAgentRuntimePool(),
		agentRuntimeTurns:   newAgentRuntimeTurnCoordinator(Config{}, slog.Default()),
	}
	d.canonicalRuntimes.slots[agentID+"\x00"+runtimeID] = &canonicalAgentRuntimeSlot{
		mode: canonicalRuntimeResident, backend: fakeRuntime,
	}
	if _, err := d.ensureIdleMessageCoordinator(agentID, runtimeID, root); err != nil {
		t.Fatalf("ensureIdleMessageCoordinator: %v", err)
	}
	d.messageCoordinators[agentID].ConfigurePendingNotices(func(ctx context.Context, snapshot PendingNoticeSnapshot) error {
		return d.canonicalRuntimes.handoffBusyNotice(ctx, agentID, runtimeID, snapshot)
	}, 20*time.Millisecond, 30*time.Millisecond)

	hub := daemonws.NewHub()
	eventBus := events.New()
	eventBus.SubscribeAll(func(event events.Event) {
		if event.RealtimeDeliveryAck != nil {
			event.RealtimeDeliveryAck(nil)
		}
	})
	serverHandler := serverhandler.New(
		db.New(pool), pool, realtime.NewHub(), eventBus, service.NewEmailService(), nil, nil,
		analytics.NoopClient{}, serverhandler.Config{}, hub,
	)
	serverHandler.AgentDeliveryNotifier = hub
	acks := make(chan protocol.AgentDeliverAckPayload, 2)
	activities := make(chan protocol.AgentMessageHandoffPayload, 2)
	recoveryRequests := make(chan protocol.AgentRecoveryRequest, 2)
	hub.SetAgentDeliveryAckHandler(func(ctx context.Context, identity daemonws.ClientIdentity, ack protocol.AgentDeliverAckPayload) error {
		acks <- ack
		return serverHandler.HandleAgentDeliveryAck(ctx, identity, ack)
	})
	hub.SetAgentRecoveryHandler(func(ctx context.Context, identity daemonws.ClientIdentity, request protocol.AgentRecoveryRequest) (protocol.AgentRecoveryPage, error) {
		recoveryRequests <- request
		return serverHandler.HandleAgentMessageRecovery(ctx, identity, request)
	})
	hub.SetAgentMessageHandoffHandler(func(ctx context.Context, identity daemonws.ClientIdentity, payload protocol.AgentMessageHandoffPayload) error {
		activities <- payload
		return serverHandler.HandleAgentMessageHandoff(ctx, identity, payload)
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, daemonws.ClientIdentity{WorkspaceID: workspaceID, RuntimeIDs: []string{runtimeID}})
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial daemon websocket: %v", err)
	}
	writes := make(chan []byte, 16)
	writerDone := make(chan struct{})
	go d.runWSWriter(conn, writes, writerDone)
	d.setReminderWS(writes, writerDone, conn.Close)
	readDone := make(chan error, 1)
	go func() { readDone <- d.readTaskWakeupMessages(conn, make(chan taskWakeup, 1), writes, nil) }()
	t.Cleanup(func() {
		d.clearReminderWS(writes)
		_ = conn.Close()
		<-readDone
		close(writes)
		<-writerDone
	})

	d.beginMessageRecovery(writes)
	select {
	case request := <-recoveryRequests:
		if request.AgentID != agentID || request.RecoveryID == "" {
			t.Fatalf("startup recovery request = %+v", request)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("startup recovery request was not observed")
	}
	deadline := time.Now().Add(2 * time.Second)
	for !d.messageCoordinators[agentID].FreshnessKnown() && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if !d.messageCoordinators[agentID].FreshnessKnown() {
		t.Fatal("startup recovery did not complete")
	}

	body, _ := json.Marshal(map[string]any{"content": "hello", "client_message_id": uuid.NewString()})
	req := httptest.NewRequest(http.MethodPost, "/api/channels/"+channelID+"/messages", bytes.NewReader(body))
	req.Header.Set("X-User-ID", userID)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("channelId", channelID)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx)
	ctx = middleware.SetMemberContext(ctx, workspaceID, member)
	ctx = middleware.WithHumanPrincipal(ctx, middleware.HumanPrincipal{UserID: userID})
	req = req.WithContext(ctx)
	recorder := httptest.NewRecorder()
	serverHandler.SendChannelMessage(recorder, req)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create canonical Message: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var created struct {
		ID  string `json:"id"`
		Seq int64  `json:"seq"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil || created.ID == "" || created.Seq <= 0 {
		t.Fatalf("decode canonical Message: %v body=%s", err, recorder.Body.String())
	}
	select {
	case ack := <-acks:
		if ack.AgentID != agentID || ack.Seq != created.Seq || ack.DeliveryID == "" {
			t.Fatalf("ack = %+v", ack)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canonical delivery was not acknowledged")
	}

	select {
	case activity := <-activities:
		if activity.AgentID != agentID || activity.RuntimeID != runtimeID || activity.Count != 1 {
			t.Fatalf("Activity = %+v", activity)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Message received Activity was not emitted")
	}
	for i := 0; i < 100; i++ {
		runtime.Gosched()
	}
	select {
	case duplicate := <-activities:
		t.Fatalf("duplicate Message received Activity = %+v", duplicate)
	default:
	}
	if batches := fakeRuntime.snapshot(); len(batches) != 1 || len(batches[0]) != 1 || batches[0][0].ID != created.ID {
		t.Fatalf("runtime batches = %+v, want exactly one concrete Message handoff", batches)
	}
	target := "channel:" + channelID
	if seq, err := d.CredentialProxy().SeenUpToSeq(agentID, target); err != nil || seq != created.Seq {
		t.Fatalf("Credential Proxy boundary = %d, %v", seq, err)
	}
	raw, err := os.ReadFile(filepath.Join(root, consumedSeqsFileName))
	if err != nil {
		t.Fatalf("read boundary file: %v", err)
	}
	var boundaries map[string]int64
	if err := json.Unmarshal(raw, &boundaries); err != nil || boundaries[target] != created.Seq {
		t.Fatalf("boundary file = %s, err=%v", raw, err)
	}
	if strings.Contains(string(raw), "hello") || strings.Contains(string(raw), created.ID) {
		t.Fatalf("boundary file persisted a Message body or identity: %s", raw)
	}
	var activityCount int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM agent_activity_event WHERE workspace_id=$1 AND agent_id=$2 AND event_type='message_received'`, workspaceID, agentID).Scan(&activityCount); err != nil || activityCount != 1 {
		t.Fatalf("Message received Activity count = %d, err=%v", activityCount, err)
	}

	// A second canonical Message arrives while the same runtime session is
	// busy. The Machine acknowledges transport acceptance, coalesces a
	// content-free Notice, and does not advance the boundary or Activity.
	slot := d.canonicalRuntimes.slots[agentID+"\x00"+runtimeID]
	slot.mu.Lock()
	slot.running = true
	slot.mu.Unlock()
	busyBody, _ := json.Marshal(map[string]any{"content": "busy secret", "client_message_id": uuid.NewString()})
	busyReq := httptest.NewRequest(http.MethodPost, "/api/channels/"+channelID+"/messages", bytes.NewReader(busyBody))
	busyReq.Header.Set("X-User-ID", userID)
	busyRouteCtx := chi.NewRouteContext()
	busyRouteCtx.URLParams.Add("channelId", channelID)
	busyCtx := context.WithValue(busyReq.Context(), chi.RouteCtxKey, busyRouteCtx)
	busyCtx = middleware.SetMemberContext(busyCtx, workspaceID, member)
	busyCtx = middleware.WithHumanPrincipal(busyCtx, middleware.HumanPrincipal{UserID: userID})
	busyRecorder := httptest.NewRecorder()
	serverHandler.SendChannelMessage(busyRecorder, busyReq.WithContext(busyCtx))
	if busyRecorder.Code != http.StatusCreated {
		t.Fatalf("create busy canonical Message: status=%d body=%s", busyRecorder.Code, busyRecorder.Body.String())
	}
	var busyCreated struct {
		ID  string `json:"id"`
		Seq int64  `json:"seq"`
	}
	if err := json.Unmarshal(busyRecorder.Body.Bytes(), &busyCreated); err != nil || busyCreated.ID == "" {
		t.Fatalf("decode busy Message: %v body=%s", err, busyRecorder.Body.String())
	}
	select {
	case ack := <-acks:
		if ack.AgentID != agentID || ack.Seq != busyCreated.Seq {
			t.Fatalf("busy ack = %+v", ack)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("busy canonical delivery was not acknowledged")
	}
	deadline = time.Now().Add(2 * time.Second)
	for len(fakeRuntime.noticeSnapshot()) == 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	notices := fakeRuntime.noticeSnapshot()
	if len(notices) != 1 || notices[0].TotalPending != 1 || len(notices[0].ChangedTargets) != 1 || notices[0].ChangedTargets[0].Target != target {
		t.Fatalf("busy runtime Notices = %+v", notices)
	}
	if got := d.messageCoordinators[agentID].Boundaries()[target]; got != created.Seq {
		t.Fatalf("boundary after busy Notice = %d, want %d", got, created.Seq)
	}
	select {
	case duplicate := <-activities:
		t.Fatalf("busy Notice emitted Message received Activity = %+v", duplicate)
	default:
	}

	turnKey := agentRuntimeTurnSlotKey{AgentID: agentID, RuntimeID: runtimeID}
	if !d.agentRuntimeTurns.reserve(turnKey, "task-busy") {
		t.Fatal("reserve busy turn")
	}
	checkRecorder := httptest.NewRecorder()
	checkRequest := httptest.NewRequest(http.MethodPost, "/credential-proxy/messages/check", bytes.NewBufferString(
		fmt.Sprintf(`{"agent_id":%q,"task_id":"task-busy"}`, agentID),
	))
	d.credentialProxyMessageCheckHandler().ServeHTTP(checkRecorder, checkRequest)
	d.agentRuntimeTurns.release(turnKey, "task-busy")
	if checkRecorder.Code != http.StatusOK {
		t.Fatalf("Credential Proxy check: status=%d body=%s", checkRecorder.Code, checkRecorder.Body.String())
	}
	var checked MessageCheckResult
	if err := json.Unmarshal(checkRecorder.Body.Bytes(), &checked); err != nil {
		t.Fatalf("decode checked Messages: %v", err)
	}
	if len(checked.Messages) != 1 || checked.Messages[0].ID != busyCreated.ID || checked.Messages[0].Content != "busy secret" || checked.HasMore {
		t.Fatalf("checked Messages = %+v", checked)
	}
	if got := d.messageCoordinators[agentID].Boundaries()[target]; got != busyCreated.Seq {
		t.Fatalf("boundary after check = %d, want %d", got, busyCreated.Seq)
	}
	if seq, err := d.CredentialProxy().SeenUpToSeq(agentID, target); err != nil || seq != busyCreated.Seq {
		t.Fatalf("Credential Proxy boundary after check = %d, %v", seq, err)
	}
	if batches := fakeRuntime.snapshot(); len(batches) != 1 {
		t.Fatalf("message check duplicated runtime body handoff: %+v", batches)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM agent_activity_event WHERE workspace_id=$1 AND agent_id=$2 AND event_type='message_received'`, workspaceID, agentID).Scan(&activityCount); err != nil || activityCount != 1 {
		t.Fatalf("Message received Activity after check = %d, err=%v", activityCount, err)
	}

	d.beginMessageRecovery(writes)
	select {
	case request := <-recoveryRequests:
		if request.AgentID != agentID || request.RecoveryID == "" || request.Boundaries[target] != busyCreated.Seq {
			t.Fatalf("reconnect recovery request = %+v", request)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reconnect recovery request was not observed")
	}
}

func seedIdleMessageAcceptanceFixture(t *testing.T, pool *pgxpool.Pool) (workspaceID, userID, runtimeID, agentID, channelID string, member db.Member) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name,email) VALUES ($1,$2) RETURNING id`, "delivery-"+suffix[:8], suffix+"@delivery.test").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name,slug,description,issue_prefix) VALUES ($1,$2,'','DLY') RETURNING id`, "delivery-"+suffix[:8], "delivery-"+suffix).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id=$1`, workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id=$1`, userID)
	})
	if err := pool.QueryRow(ctx, `INSERT INTO member (workspace_id,user_id,role) VALUES ($1,$2,'owner') RETURNING id,workspace_id,user_id,role,created_at,last_active_at`, workspaceID, userID).Scan(&member.ID, &member.WorkspaceID, &member.UserID, &member.Role, &member.CreatedAt, &member.LastActiveAt); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO agent_runtime (workspace_id,name,runtime_mode,provider,status,device_info,owner_id,last_seen_at) VALUES ($1,$2,'cloud','pi','online','acceptance',$3,now()) RETURNING id`, workspaceID, "delivery-runtime-"+suffix[:8], userID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO agent (workspace_id,name,description,runtime_mode,runtime_config,runtime_id,max_concurrent_tasks,owner_id,instructions,custom_env,custom_args,model) VALUES ($1,$2,'','cloud','{}',$3,1,$4,'','{}','[]','composer-1.5') RETURNING id`, workspaceID, "delivery_agent_"+suffix[:8], runtimeID, userID).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO channel (workspace_id,name,created_by,kind) VALUES ($1,$2,$3,'group') RETURNING id`, workspaceID, "delivery-channel-"+suffix[:8], userID).Scan(&channelID); err != nil {
		t.Fatal(err)
	}
	for memberType, memberID := range map[string]string{"user": userID, "agent": agentID} {
		if _, err := pool.Exec(ctx, `INSERT INTO channel_member (channel_id,workspace_id,member_type,member_id) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, channelID, workspaceID, memberType, memberID); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func seedIdleMessageAcceptanceBoundaries(ctx context.Context, pool *pgxpool.Pool, root, workspaceID, agentID string) error {
	rows, err := pool.Query(ctx, `
		SELECT CASE WHEN message.thread_root_message_id IS NULL
		            THEN 'channel:' || message.channel_id::text
		            ELSE 'thread:' || message.thread_root_message_id::text END,
		       max(message.seq)
		FROM channel_message message
		JOIN channel_member member
		  ON member.channel_id=message.channel_id
		 AND member.workspace_id=message.workspace_id
		 AND member.member_type='agent'
		 AND member.member_id=$2
		WHERE message.workspace_id=$1 AND message.deleted_at IS NULL
		  AND NOT (message.author_type='agent' AND message.author_id=$2)
		GROUP BY 1`, workspaceID, agentID)
	if err != nil {
		return err
	}
	defer rows.Close()
	boundaries := make(map[string]int64)
	for rows.Next() {
		var target string
		var seq int64
		if err := rows.Scan(&target, &seq); err != nil {
			return err
		}
		boundaries[target] = seq
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return writeConsumedSeqs(filepath.Join(root, consumedSeqsFileName), boundaries)
}
