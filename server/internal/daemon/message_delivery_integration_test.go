package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/agentworkspace"
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

func (r *idleMessageFakeRuntime) AcceptIdleInboxNotice(_ context.Context, notice agent.ResidentPendingNotice) (agent.ResidentMessageAcceptance, error) {
	r.mu.Lock()
	r.notices = append(r.notices, notice)
	r.mu.Unlock()
	done := make(chan error)
	close(done)
	return agent.ResidentMessageAcceptance{Done: done}, nil
}

func (r *idleMessageFakeRuntime) Execute(context.Context, string, agent.ExecOptions) (*agent.Session, error) {
	return nil, nil
}

func (r *idleMessageFakeRuntime) EnsureResidentProcess(context.Context) error {
	return nil
}

func (r *idleMessageFakeRuntime) RuntimeAlive() (bool, bool) {
	return true, true
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

func openMessageDeliveryAcceptanceDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dbURL == "" {
		t.Skip("DATABASE_URL is unset; run `make test` or load the checkout environment before DB-backed acceptance tests")
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Fatalf("configure acceptance database: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Skipf("acceptance database is unavailable: %v", err)
	}
	var deliveryTablePresent bool
	if err := pool.QueryRow(context.Background(), `SELECT to_regclass('public.agent_message_delivery') IS NOT NULL`).Scan(&deliveryTablePresent); err != nil {
		pool.Close()
		t.Fatalf("inspect acceptance database schema: %v", err)
	}
	if !deliveryTablePresent {
		pool.Close()
		t.Fatal("acceptance database is not migrated through agent_message_delivery; run `make migrate-up` with the same ENV_FILE")
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestMessageRealServerMachineProxyRuntimeAcceptance(t *testing.T) {
	pool := openMessageDeliveryAcceptanceDatabase(t)
	workspaceID, userID, runtimeID, agentID, channelID, daemonID, member := seedIdleMessageAcceptanceFixture(t, pool)

	workspacesRoot := t.TempDir()
	root := agentworkspace.Root(workspacesRoot, workspaceID, agentID)
	if err := ensureMulticaAgentRoot(root); err != nil {
		t.Fatal(err)
	}
	fakeRuntime := &idleMessageFakeRuntime{}
	d := New(Config{DaemonID: daemonID, WorkspacesRoot: workspacesRoot}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.mu.Lock()
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
	d.workspaces[workspaceID] = newWorkspaceState(workspaceID, []string{runtimeID})
	d.mu.Unlock()
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
	hub.SetAgentDeliveryAckHandler(func(ctx context.Context, identity daemonws.ClientIdentity, ack protocol.AgentDeliverAckPayload) error {
		acks <- ack
		return serverHandler.HandleAgentDeliveryAck(ctx, identity, ack)
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, daemonws.ClientIdentity{DaemonID: daemonID, WorkspaceID: workspaceID})
	}))
	defer server.Close()
	d.cfg.ServerBaseURL = server.URL
	d.client.SetWorkspaceDaemonToken(workspaceID, "workspace-token", time.Now().Add(time.Hour))
	d.mu.Lock()
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
	d.mu.Unlock()
	runner, err := d.newWorkspaceRunner(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	d.attachWorkspaceRunner(runner)
	t.Cleanup(func() {
		d.detachWorkspaceRunner(runner)
		runner.inboxes.Close()
	})
	d.canonicalRuntimes.slots[agentID+"\x00"+runtimeID] = &canonicalAgentRuntimeSlot{
		backend: fakeRuntime,
	}
	if _, err := runner.processes.Start(agentProcessStartRequest{AgentID: agentID, RuntimeID: runtimeID, LaunchID: "acceptance-launch", StartDispatchID: "acceptance-launch" + "-dispatch"}); err != nil {
		t.Fatalf("accept test APM launch: %v", err)
	}
	if _, err := d.ensureIdleMessageCoordinator(workspaceID, agentID, runtimeID); err != nil {
		t.Fatalf("ensureIdleMessageCoordinator: %v", err)
	}
	// Start() only admits the launch into Starting. Provider handoff and the
	// no-ACK rejection path both require Running; otherwise Accept buffers
	// and ACKs before the fake runtime is ever asked.
	markTestLaunchRunning(t, runner, agentID)
	coordinator, _ := resolveTestInbox(t, d, InboxKey{WorkspaceID: workspaceID, AgentID: agentID})
	coordinator.ConfigurePendingNotices(func(ctx context.Context, snapshot InboxNoticeSnapshot, commitIfCurrent InboxNoticeCommitIfCurrent) error {
		return d.canonicalRuntimes.deliverBusyInboxNotice(ctx, agentID, runtimeID, snapshot, commitIfCurrent)
	}, 20*time.Millisecond, 30*time.Millisecond)
	teardownRunner := startIdleMessageAcceptanceRunner(t, d, hub, workspaceID, daemonID)
	defer teardownRunner()

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

	if batches := fakeRuntime.snapshot(); len(batches) != 1 || len(batches[0]) != 1 || batches[0][0].ID != created.ID {
		t.Fatalf("runtime batches = %+v, want exactly one concrete Message acceptance", batches)
	}
	target := "channel:" + channelID
	if seq, err := d.CredentialProxy().SeenUpToSeq(agentID, target); err != nil || seq != created.Seq {
		t.Fatalf("Credential Proxy boundary = %d, %v", seq, err)
	}
	// A second canonical Message arrives while the same runtime session is
	// busy. The Machine acknowledges transport acceptance, coalesces a
	// content-free Notice, and does not advance the message boundary.
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
	deadline := time.Now().Add(2 * time.Second)
	for len(fakeRuntime.noticeSnapshot()) == 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	notices := fakeRuntime.noticeSnapshot()
	if len(notices) != 1 || notices[0].TotalPending != 1 || len(notices[0].ChangedTargets) != 1 || notices[0].ChangedTargets[0].Target != target {
		t.Fatalf("busy runtime Notices = %+v", notices)
	}
	if got := coordinator.Boundaries()[target]; got != created.Seq {
		t.Fatalf("boundary after busy Notice = %d, want %d", got, created.Seq)
	}
	checkRecorder := httptest.NewRecorder()
	checkRequest := httptest.NewRequest(http.MethodPost, "/credential-proxy/messages/check", bytes.NewBufferString(
		fmt.Sprintf(`{"agent_id":%q}`, agentID),
	))
	d.credentialProxyMessageCheckHandler().ServeHTTP(checkRecorder, checkRequest)
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
	if checked.CoverageReceipt == "" {
		t.Fatal("message check did not return a local coverage receipt")
	}
	if got := coordinator.Boundaries()[target]; got != created.Seq {
		t.Fatalf("boundary before check output commit = %d, want prior %d", got, created.Seq)
	}
	if err := coordinator.CommitCoverage(checked.CoverageReceipt); err != nil {
		t.Fatalf("commit checked coverage: %v", err)
	}
	if got := coordinator.Boundaries()[target]; got != busyCreated.Seq {
		t.Fatalf("boundary after check output commit = %d, want %d", got, busyCreated.Seq)
	}
	if seq, err := d.CredentialProxy().SeenUpToSeq(agentID, target); err != nil || seq != busyCreated.Seq {
		t.Fatalf("Credential Proxy boundary after check = %d, %v", seq, err)
	}
	if batches := fakeRuntime.snapshot(); len(batches) != 1 {
		t.Fatalf("message check duplicated runtime body handoff: %+v", batches)
	}
}

func seedIdleMessageAcceptanceFixture(t *testing.T, pool *pgxpool.Pool) (workspaceID, userID, runtimeID, agentID, channelID, daemonID string, member db.Member) {
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
	daemonID = "delivery-daemon-" + suffix[:8]
	if err := pool.QueryRow(ctx, `INSERT INTO agent_runtime (workspace_id,name,runtime_mode,provider,status,device_info,daemon_id,owner_id,last_seen_at) VALUES ($1,$2,'cloud','pi','online','acceptance',$3,$4,now()) RETURNING id`, workspaceID, "delivery-runtime-"+suffix[:8], daemonID, userID).Scan(&runtimeID); err != nil {
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

func startIdleMessageAcceptanceRunner(t *testing.T, d *Daemon, hub *daemonws.Hub, workspaceID, daemonID string) func() {
	t.Helper()
	d.cfg.DaemonID = daemonID
	runner := d.currentWorkspaceRunner(workspaceID)
	if runner == nil {
		var err error
		runner, err = d.newWorkspaceRunner(workspaceID)
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runner.Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for hub.WorkspaceRunnerConnectionCount(daemonID, workspaceID) != 1 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if hub.WorkspaceRunnerConnectionCount(daemonID, workspaceID) != 1 {
		cancel()
		select {
		case <-done:
			t.Fatal("Workspace Runner did not connect")
		case <-time.After(time.Second):
			t.Fatal("Workspace Runner did not connect")
		}
	}
	return func() {
		hub.CloseWorkspaceRunner(daemonID, workspaceID, d.runnerInstanceID)
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("Workspace Runner did not stop")
		}
	}
}

// recordingResidentMessage delegates native idle Message input to an injected
// backend and remembers every batch the Agent runtime actually accepted, so a
// test can distinguish completed delivery from provider rejection.
type recordingResidentMessage struct {
	agent.Backend
	mu       sync.Mutex
	batches  [][]agent.ResidentMessage
	observed chan struct{}
}

func (r *recordingResidentMessage) AcceptMessageBatch(ctx context.Context, messages []agent.ResidentMessage) (agent.ResidentMessageAcceptance, error) {
	r.mu.Lock()
	r.batches = append(r.batches, append([]agent.ResidentMessage(nil), messages...))
	r.mu.Unlock()
	r.observed <- struct{}{}
	return r.Backend.(agent.ResidentMessageInput).AcceptMessageBatch(ctx, messages)
}

func (r *recordingResidentMessage) EnsureResidentProcess(ctx context.Context) error {
	starter, ok := r.Backend.(agent.ResidentRuntimeStarter)
	if !ok {
		return errors.New("recorded runtime does not support resident process start")
	}
	return starter.EnsureResidentProcess(ctx)
}

func (r *recordingResidentMessage) RuntimeAlive() (bool, bool) {
	liveness, ok := r.Backend.(agent.ResidentRuntimeLivenessChecker)
	if !ok {
		return false, false
	}
	return liveness.RuntimeAlive()
}

// ForceKill preserves the runtime-pool teardown contract when this recorder
// wraps a real resident backend. Without forwarding the capability, the pool
// cannot stop an in-flight provider process before TempDir cleanup.
func (r *recordingResidentMessage) ForceKill() error {
	killable, ok := r.Backend.(agent.ResidentRuntimeForceKillable)
	if !ok {
		return errors.New("recorded runtime does not support force kill")
	}
	return killable.ForceKill()
}

func (r *recordingResidentMessage) snapshot() [][]agent.ResidentMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]agent.ResidentMessage(nil), r.batches...)
}

// failingResidentMessageRuntime simulates a provider rejecting input before
// APM acceptance. Raft leaves the delivery unacknowledged in this phase.
type failingResidentMessageRuntime struct{}

func (failingResidentMessageRuntime) Execute(context.Context, string, agent.ExecOptions) (*agent.Session, error) {
	return nil, nil
}

func (failingResidentMessageRuntime) EnsureResidentProcess(context.Context) error {
	return nil
}

func (failingResidentMessageRuntime) RuntimeAlive() (bool, bool) {
	return true, true
}

func (failingResidentMessageRuntime) AcceptMessageBatch(context.Context, []agent.ResidentMessage) (agent.ResidentMessageAcceptance, error) {
	return agent.ResidentMessageAcceptance{}, errors.New("runtime Message handoff unavailable (simulated crash window)")
}

// TestIdleMessageRealWebSocketCrashRestartRehandsDeliveredMessage models the
// T3 crash acceptance (#7): a canonical Message is server-acked, the
// coordinator is then killed before the runtime handoff / boundary persist, and
// a fresh coordinator on the same Agent root completes recovery and hands the
// Message over instead of losing it.
func TestIdleMessageRealWebSocketCrashRestartRehandsDeliveredMessage(t *testing.T) {
	pool := openMessageDeliveryAcceptanceDatabase(t)
	workspaceID, userID, runtimeID, agentID, channelID, daemonID, member := seedIdleMessageAcceptanceFixture(t, pool)

	workspacesRoot := isolatedWorkspacesRoot(t)
	root := agentworkspace.Root(workspacesRoot, workspaceID, agentID)
	if err := ensureMulticaAgentRoot(root); err != nil {
		t.Fatal(err)
	}

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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, daemonws.ClientIdentity{DaemonID: daemonID, WorkspaceID: workspaceID})
	}))
	defer server.Close()

	target := "channel:" + channelID

	// connect wires one Daemon to a fresh websocket on the same Agent root and
	// returns the daemon, its observation channels, the runtime batch recorder,
	// and a teardown that drops the websocket exactly like a process crash.
	connect := func(backend agent.Backend) (
		d *Daemon,
		acks chan protocol.AgentDeliverAckPayload,
		batches func() [][]agent.ResidentMessage,
		batchObserved <-chan struct{},
		teardown func(),
	) {
		normal := &recordingResidentMessage{Backend: backend, observed: make(chan struct{}, 1)}
		d = New(Config{DaemonID: daemonID, ServerBaseURL: server.URL, WorkspacesRoot: workspacesRoot}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		d.client.SetWorkspaceDaemonToken(workspaceID, "workspace-token", time.Now().Add(time.Hour))
		d.mu.Lock()
		d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
		d.workspaces[workspaceID] = newWorkspaceState(workspaceID, []string{runtimeID})
		d.mu.Unlock()
		runner, err := d.newWorkspaceRunner(workspaceID)
		if err != nil {
			t.Fatal(err)
		}
		d.attachWorkspaceRunner(runner)
		d.canonicalRuntimes.slots[agentID+"\x00"+runtimeID] = &canonicalAgentRuntimeSlot{
			backend: normal,
		}
		if _, err := runner.processes.Start(agentProcessStartRequest{AgentID: agentID, RuntimeID: runtimeID, LaunchID: "acceptance-launch", StartDispatchID: "acceptance-launch" + "-dispatch"}); err != nil {
			t.Fatalf("accept test APM launch: %v", err)
		}
		if _, err := d.ensureIdleMessageCoordinator(workspaceID, agentID, runtimeID); err != nil {
			t.Fatalf("ensureIdleMessageCoordinator: %v", err)
		}
		markTestLaunchRunning(t, runner, agentID)
		acks = make(chan protocol.AgentDeliverAckPayload, 2)
		hub.SetAgentDeliveryAckHandler(func(ctx context.Context, identity daemonws.ClientIdentity, ack protocol.AgentDeliverAckPayload) error {
			acks <- ack
			return serverHandler.HandleAgentDeliveryAck(ctx, identity, ack)
		})
		stopRunner := startIdleMessageAcceptanceRunner(t, d, hub, workspaceID, daemonID)
		teardown = func() {
			stopRunner()
			d.detachWorkspaceRunner(runner)
			// Crash teardown must terminate any resident provider process before
			// the workspace root is released. closeAll only closes idle backends;
			// a late provider write can otherwise race testing.T's TempDir cleanup.
			// A provider may be stuck in an external process. Keep teardown
			// bounded so a wedged provider cannot hang the whole test package;
			// the runner/process-manager closes below remains the final cleanup.
			terminated := make(chan error, 1)
			go func() { terminated <- d.canonicalRuntimes.forceTerminateAll() }()
			select {
			case err := <-terminated:
				if err != nil {
					t.Errorf("force terminate canonical runtime: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Errorf("force terminate canonical runtime timed out")
			}
			// The runner's Run defer normally closes these resources, but the
			// crash/restart path can detach immediately after the websocket has
			// stopped while a process-manager cleanup is still in flight. Close
			// them explicitly so t.TempDir never races a late .multica write.
			runner.processes.Close()
			runner.activity.Close()
			runner.inboxes.Close()
			if err := d.canonicalRuntimes.closeAll(); err != nil {
				t.Errorf("close canonical runtime: %v", err)
			}
		}
		return d, acks, normal.snapshot, normal.observed, teardown
	}

	postMessage := func() (string, int64) {
		body, _ := json.Marshal(map[string]any{"content": "crash-me", "client_message_id": uuid.NewString()})
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
		return created.ID, created.Seq
	}
	waitAck := func(acks chan protocol.AgentDeliverAckPayload, wantSeq int64) {
		select {
		case ack := <-acks:
			if ack.AgentID != agentID || ack.Seq != wantSeq || ack.DeliveryID == "" {
				t.Fatalf("ack = %+v, want seq %d", ack, wantSeq)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("canonical delivery was not acknowledged")
		}
	}
	waitBatch := func(observed <-chan struct{}, snapshot func() [][]agent.ResidentMessage, wantID string) [][]agent.ResidentMessage {
		select {
		case <-observed:
			return snapshot()
		case <-time.After(2 * time.Second):
			t.Fatalf("runtime handoff was not observed for canonical Message %s", wantID)
			return nil
		}
	}
	waitBoundary := func(d *Daemon, target string, want int64) {
		deadline := time.Now().Add(2 * time.Second)
		for {
			boundary, err := d.CredentialProxy().SeenUpToSeq(agentID, target)
			if err == nil && boundary == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("Context Boundary did not reach %d for %s: got %d, err %v", want, target, boundary, err)
				return
			}
			runtime.Gosched()
		}
	}

	// Phase A — provider rejection. The body remains Pending, the delivery is
	// not acknowledged, and the in-memory boundary does not advance.
	dA, acksA, batchesA, observedA, teardownA := connect(failingResidentMessageRuntime{})
	idA, seqA := postMessage()
	select {
	case ack := <-acksA:
		t.Fatalf("provider rejection was acknowledged: %+v", ack)
	case <-time.After(200 * time.Millisecond):
	}
	if got := waitBatch(observedA, batchesA, idA); len(got) != 1 || len(got[0]) != 1 || got[0][0].ID != idA {
		t.Fatalf("rejected runtime handoff attempt = %+v, want %s", got, idA)
	}
	runnerA, err := dA.resolveWorkspaceRunnerByAgent(agentID)
	if err != nil {
		t.Fatalf("resolve runner before delivery: %v", err)
	}
	if got, known, boundaryErr := runnerA.messageContextBoundary(agentID, target); boundaryErr != nil || (known && got >= seqA) {
		t.Fatalf("boundary before delivery = %d (known=%t), %v; want unknown or below %d", got, known, boundaryErr, seqA)
	}
	// Disconnect: the Server still owns the unacknowledged delivery.
	teardownA()
	_ = dA

	// Phase B — fresh daemon, same Agent root. Server redelivery and local
	// Pending dedup converge on one provider handoff: nothing is lost or doubled.
	dB, acksB, batchesB, observedB, teardownB := connect(&idleMessageFakeRuntime{})
	if got := waitBatch(observedB, batchesB, idA); len(got) != 1 || len(got[0]) != 1 || got[0][0].ID != idA {
		t.Fatalf("restarted runtime batches = %+v, want canonical Message %s handed off", got, idA)
	}
	waitAck(acksB, seqA)
	waitBoundary(dB, target, seqA)
	teardownB()
}
