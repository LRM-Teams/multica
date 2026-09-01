package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
)

// Slice 3.2 (unification spec §4.4): the Agent gateway serves BOTH the
// channel-route graph and the federated workspace research graph, citations
// are graph-qualified in graph_memory_agent_citation, research visibility is
// fail-closed, and idempotency/quota namespaces are per graph.

func gatewayResearchTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("integration test requires Postgres at DATABASE_URL")
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type gatewayResearchFixture struct {
	pool        *pgxpool.Pool
	gateway     *GraphMemoryAgentGateway
	workspaceID string
	channelID   string
	agentID     string
	root        string
}

// seedGatewayResearchWorkspace installs the managed-agent authorization
// chain (workspace → profile → channel → managed agent → channel_member →
// agent state lease) and returns the fixture.
func seedGatewayResearchWorkspace(t *testing.T) *gatewayResearchFixture {
	t.Helper()
	pool := gatewayResearchTestPool(t)
	ctx := context.Background()

	var workspaceID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ('Gateway Research Test', 'gw-research-'||$1, '', 'GWR')
		RETURNING id::text`, uuid.NewString()[:8]).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM workspace WHERE id=$1::uuid`, workspaceID) })

	var ownerID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ('gw-owner-'||$1, 'gw-owner-'||$1||'@multica.ai')
		RETURNING id::text`, uuid.NewString()[:8]).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'owner')`, workspaceID, ownerID); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO graph_memory_profile(workspace_id,memory_type,graph_memory_mode,explore_max_rounds,explore_nodes_per_expansion)
		VALUES($1::uuid,'graph','agent',6,4)`, workspaceID); err != nil {
		t.Fatal(err)
	}

	var channelID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by)
		VALUES ($1::uuid, 'gw-research-channel-'||$2, $3::uuid)
		RETURNING id::text`, workspaceID, uuid.NewString()[:8], ownerID).Scan(&channelID); err != nil {
		t.Fatal(err)
	}

	var runtimeID, agentID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime(workspace_id,daemon_id,name,runtime_mode,provider,status,device_info,metadata,visibility,last_seen_at)
		VALUES($1::uuid,$2,'gw-research-runtime','local','pi','online','','{}','private',now()) RETURNING id::text`,
		workspaceID, "gw-research-daemon-"+uuid.NewString()[:8]).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent(workspace_id,name,display_name,runtime_mode,runtime_config,runtime_id,owner_id,managed_role,instructions)
		VALUES($1::uuid,$2,'Memory gateway','local','{}',$3::uuid,$4::uuid,'graph_memory_channel','managed memory') RETURNING id::text`,
		workspaceID, "gw-research-agent-"+uuid.NewString()[:8], runtimeID, ownerID).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO graph_memory_channel_agent(channel_id,workspace_id,agent_id,runtime_id,sponsor_user_id,handle,display_name,status)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,'Memory gateway','active')`,
		channelID, workspaceID, agentID, runtimeID, ownerID, "gw-agent-"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO channel_member(channel_id,workspace_id,member_type,member_id,role) VALUES($1::uuid,$2::uuid,'agent',$3::uuid,'member')`, channelID, workspaceID, agentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO graph_memory_agent_state(channel_id,lease_expires_at) VALUES($1::uuid,now()+interval '5 minutes')`, channelID); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	t.Setenv("MULTICA_WORKSPACES_ROOT", root)

	// Channel-route graph with one channel-visible node and one intruder the
	// research view must never see.
	channelDir, err := memorygraph.EnsureScopedDir(root, workspaceID, memorygraph.GraphDirKindChannel, channelID)
	if err != nil {
		t.Fatal(err)
	}
	channelStore := memorygraph.NewStore(channelDir)
	if err := channelStore.Init(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, node := range []*memorygraph.Node{
		{NodeID: "channel-node", Body: "dispatch retries use exponential backoff", Visibility: "channel", ChannelID: channelID, CreatedBy: memorygraph.CreatorIngester, CreatedVersion: 1, UpdatedVersion: 1, ObservedAt: now},
		{NodeID: "channel-secret", Body: "private channel conclusion", Visibility: "channel", ChannelID: channelID, CreatedBy: memorygraph.CreatorIngester, CreatedVersion: 1, UpdatedVersion: 1, ObservedAt: now},
	} {
		if err := channelStore.SaveNode(1, node); err != nil {
			t.Fatal(err)
		}
	}

	return &gatewayResearchFixture{
		pool: pool, gateway: NewGraphMemoryAgentGateway(pool),
		workspaceID: workspaceID, channelID: channelID, agentID: agentID, root: root,
	}
}

// seedResearchGraph creates the workspace research graph with a
// research-visible node and a project-visible intruder, returning the store.
func (f *gatewayResearchFixture) seedResearchGraph(t *testing.T) *memorygraph.Store {
	t.Helper()
	dir, err := memorygraph.EnsureScopedDir(f.root, f.workspaceID, memorygraph.GraphDirKindResearch, f.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	store := memorygraph.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, node := range []*memorygraph.Node{
		{NodeID: "research-node", Body: "study found cache pools exhaust under load", Visibility: "research", CreatedBy: memorygraph.CreatorIngester, CreatedVersion: 1, UpdatedVersion: 1, ObservedAt: now},
		{NodeID: "research-intruder", Body: "project scoped conclusion", Visibility: "project", CreatedBy: memorygraph.CreatorIngester, CreatedVersion: 1, UpdatedVersion: 1, ObservedAt: now},
	} {
		if err := store.SaveNode(1, node); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

// callGateway invokes one gateway operation with a raw JSON body.
func (f *gatewayResearchFixture) callGateway(t *testing.T, operation, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/graph-memory/"+operation, bytes.NewBufferString(body))
	recorder := httptest.NewRecorder()
	if err := f.gateway.ServeHTTP(recorder, req, f.workspaceID, f.agentID, f.channelID, operation); err != nil {
		t.Fatalf("gateway %s: %v", operation, err)
	}
	return recorder
}

func decodeGatewayStart(t *testing.T, recorder *httptest.ResponseRecorder) (trajectoryID string, graphVersion int) {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("start status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		TrajectoryID string `json:"trajectory_id"`
		GraphVersion int    `json:"graph_version"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil || payload.TrajectoryID == "" {
		t.Fatalf("start payload=%s err=%v", recorder.Body.String(), err)
	}
	return payload.TrajectoryID, payload.GraphVersion
}

// The gateway serves the research graph end to end: research start pins the
// research graph version, explore reads research-visible nodes only, and the
// submitted citation is graph-qualified in graph_memory_agent_citation.
func TestGraphMemoryAgentGatewayServesResearchGraph(t *testing.T) {
	f := seedGatewayResearchWorkspace(t)
	f.seedResearchGraph(t)

	trajectoryID, graphVersion := decodeGatewayStart(t, f.callGateway(t, "start",
		`{"query":"cache pool exhaustion","idempotency_key":"research-start-1","graph":"research"}`))
	if graphVersion != 1 {
		t.Fatalf("research start graph_version=%d, want the research graph version 1", graphVersion)
	}

	explore := f.callGateway(t, "explore", `{"trajectory_id":"`+trajectoryID+`","node_ids":["research-node"],"idempotency_key":"research-explore-1","graph":"research"}`)
	if explore.Code != http.StatusOK {
		t.Fatalf("research explore status=%d body=%s", explore.Code, explore.Body.String())
	}
	if !strings.Contains(explore.Body.String(), "cache pools exhaust under load") {
		t.Fatalf("research explore body lacks the research node:\n%s", explore.Body.String())
	}

	submit := f.callGateway(t, "submit", `{"trajectory_id":"`+trajectoryID+`","found":true,"summary":"Pools exhaust.","node_ids":["research-node"],"idempotency_key":"research-submit-1","graph":"research"}`)
	if submit.Code != http.StatusOK {
		t.Fatalf("research submit status=%d body=%s", submit.Code, submit.Body.String())
	}

	var identity string
	var citationVersion int
	if err := f.pool.QueryRow(context.Background(), `
		SELECT graph_identity, graph_version FROM graph_memory_agent_citation WHERE trajectory_id=$1::uuid AND node_id='research-node'`,
		trajectoryID).Scan(&identity, &citationVersion); err != nil {
		t.Fatalf("research citation row: %v", err)
	}
	if identity != "research:"+f.workspaceID {
		t.Fatalf("citation graph_identity=%q, want research:%s", identity, f.workspaceID)
	}
	if citationVersion != 1 {
		t.Fatalf("citation graph_version=%d, want the research graph version", citationVersion)
	}
}

// Research visibility fails closed in both directions: a research-graph
// explore never returns project/channel-visible nodes, and the default
// (channel-route) graph never returns research content. An unknown graph
// selector is rejected, and a research request without a research store is
// refused rather than falling back to the channel route.
func TestGraphMemoryAgentGatewayResearchScopeFailsClosed(t *testing.T) {
	f := seedGatewayResearchWorkspace(t)
	f.seedResearchGraph(t)

	trajectoryID, _ := decodeGatewayStart(t, f.callGateway(t, "start",
		`{"query":"anything","idempotency_key":"scope-start-1"}`))

	// Research explore of a project-visible intruder inside the research
	// graph: NODE_NOT_FOUND, not content.
	intruder := f.callGateway(t, "explore", `{"trajectory_id":"`+trajectoryID+`","node_ids":["research-intruder"],"idempotency_key":"scope-explore-1","graph":"research"}`)
	if intruder.Code != http.StatusNotFound {
		t.Fatalf("research intruder explore status=%d body=%s, want 404 fail closed", intruder.Code, intruder.Body.String())
	}

	// Research explore of a channel-graph node id: the id simply does not
	// exist in the research store.
	channelNode := f.callGateway(t, "explore", `{"trajectory_id":"`+trajectoryID+`","node_ids":["channel-node"],"idempotency_key":"scope-explore-2","graph":"research"}`)
	if channelNode.Code != http.StatusNotFound {
		t.Fatalf("research explore of channel node status=%d body=%s, want 404", channelNode.Code, channelNode.Body.String())
	}

	// Default graph (channel route) can read its own node but the same
	// request against research cannot leak it, and research content never
	// resolves on the channel route.
	ok := f.callGateway(t, "explore", `{"trajectory_id":"`+trajectoryID+`","node_ids":["channel-node"],"idempotency_key":"scope-explore-3"}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("channel explore status=%d body=%s", ok.Code, ok.Body.String())
	}
	leak := f.callGateway(t, "explore", `{"trajectory_id":"`+trajectoryID+`","node_ids":["research-node"],"idempotency_key":"scope-explore-4"}`)
	if leak.Code != http.StatusNotFound {
		t.Fatalf("channel explore of research node status=%d body=%s, want 404", leak.Code, leak.Body.String())
	}

	// Unknown selector: rejected, never treated as the default graph.
	if err := f.gateway.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/graph-memory/explore", bytes.NewBufferString(`{"graph":"bogus"}`)),
		f.workspaceID, f.agentID, f.channelID, "explore"); err == nil || !strings.Contains(err.Error(), "graph") {
		t.Fatalf("unknown graph selector err=%v, want graph error", err)
	}

	// No research store: research requests fail closed instead of falling
	// back to the channel route.
	fresh := seedGatewayResearchWorkspace(t)
	if err := fresh.gateway.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/graph-memory/start", bytes.NewBufferString(`{"query":"q","graph":"research"}`)),
		fresh.workspaceID, fresh.agentID, fresh.channelID, "start"); err == nil {
		t.Fatal("research start without a research store must fail closed")
	}
}

// Idempotency namespaces are per graph: within one resumed run, the same
// client key reserves two independent operations — one per graph — and a
// verbatim replay still returns the recorded per-graph result.
func TestGraphMemoryAgentGatewayIdempotencyPerGraph(t *testing.T) {
	f := seedGatewayResearchWorkspace(t)
	f.seedResearchGraph(t)

	trajectoryID, _ := decodeGatewayStart(t, f.callGateway(t, "start", `{"query":"both graphs","idempotency_key":"idem-start"}`))

	channelExplore := f.callGateway(t, "explore", `{"trajectory_id":"`+trajectoryID+`","node_ids":["channel-node"],"idempotency_key":"shared-key"}`)
	if channelExplore.Code != http.StatusOK || !strings.Contains(channelExplore.Body.String(), "exponential backoff") {
		t.Fatalf("channel explore status=%d body=%s", channelExplore.Code, channelExplore.Body.String())
	}

	// Same key on the research graph: a fresh operation serving research
	// content, not a replay of the channel response.
	researchExplore := f.callGateway(t, "explore", `{"trajectory_id":"`+trajectoryID+`","node_ids":["research-node"],"idempotency_key":"shared-key","graph":"research"}`)
	if researchExplore.Code != http.StatusOK {
		t.Fatalf("research explore status=%d body=%s", researchExplore.Code, researchExplore.Body.String())
	}
	if !strings.Contains(researchExplore.Body.String(), "cache pools exhaust") {
		t.Fatalf("research explore replayed the channel response:\n%s", researchExplore.Body.String())
	}

	// Verbatim replay of the channel explore still returns its recorded
	// channel result.
	replay := f.callGateway(t, "explore", `{"trajectory_id":"`+trajectoryID+`","node_ids":["channel-node"],"idempotency_key":"shared-key"}`)
	if replay.Code != http.StatusOK || !strings.Contains(replay.Body.String(), "exponential backoff") {
		t.Fatalf("channel replay status=%d body=%s", replay.Code, replay.Body.String())
	}

	// The recorded durable operations are distinguished by graph identity.
	var identities string
	if err := f.pool.QueryRow(context.Background(), `
		SELECT string_agg(DISTINCT graph_identity, ',') FROM graph_memory_agent_tool_operation
		WHERE trajectory_id=$1::uuid`, trajectoryID).Scan(&identities); err != nil {
		t.Fatalf("tool operation identities: %v", err)
	}
	for _, want := range []string{"channel:" + f.channelID, "research:" + f.workspaceID} {
		if !strings.Contains(identities, want) {
			t.Fatalf("tool operation graph identities=%q, want both %q and the research identity", identities, want)
		}
	}
}
