package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ensureMergableFleetMember idempotently creates the workspace fleet + one
// active lead member and returns the agent id (for X-Agent-ID). Safe to call
// more than once per testWorkspaceID — the fleet/member are upserted.
func ensureMergableFleetMember(t *testing.T) string {
	t.Helper()
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	// Reuse any existing active lead member in this workspace so repeated calls
	// (multiple tests / multiple sessions) stay idempotent and never churn agents
	// or conflict on the active-lead unique index.
	var existing string
	if err := testPool.QueryRow(ctx, `
		SELECT agent_id::text FROM research_fleet_member
		WHERE workspace_id = $1 AND is_lead = true AND status = 'active'
		LIMIT 1
	`, testWorkspaceID).Scan(&existing); err == nil && existing != "" {
		return existing
	}

	suffix := uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, "merge-fleet-"+suffix, nil)

	var fleetID pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		INSERT INTO research_fleet (workspace_id, lead_agent_id)
		VALUES ($1, $2)
		ON CONFLICT (workspace_id) DO UPDATE
		  SET lead_agent_id = EXCLUDED.lead_agent_id, updated_at = now()
		RETURNING id
	`, testWorkspaceID, agentID).Scan(&fleetID); err != nil {
		t.Fatalf("upsert research fleet: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO research_fleet_member (workspace_id, fleet_id, agent_id, role, status, is_lead)
		SELECT $1, $2, $3, 'lead', 'active', true
		WHERE NOT EXISTS (
			SELECT 1 FROM research_fleet_member
			WHERE workspace_id = $1 AND is_lead = true AND status = 'active'
		)
	`, testWorkspaceID, fleetID, agentID); err != nil {
		t.Fatalf("upsert fleet member: %v", err)
	}

	// Return whatever active lead agent the workspace now has (the freshly
	// created one, or a pre-existing one from a prior test run).
	var leadAgent string
	if err := testPool.QueryRow(ctx, `
		SELECT agent_id::text FROM research_fleet_member
		WHERE workspace_id = $1 AND is_lead = true AND status = 'active'
		LIMIT 1
	`, testWorkspaceID).Scan(&leadAgent); err != nil {
		t.Fatalf("reload active lead agent: %v", err)
	}
	return leadAgent
}

// newMergableSession creates a research session in testWorkspaceID reusing the
// already-ensured fleet (+lead member).
func newMergableSession(t *testing.T, agentID, title string) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	var fleetID pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM research_fleet WHERE workspace_id = $1
	`, testWorkspaceID).Scan(&fleetID); err != nil {
		t.Fatalf("load research fleet: %v", err)
	}
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin session tx: %v", err)
	}
	var session pgtype.UUID
	if err = tx.QueryRow(ctx, `
		INSERT INTO research_session (
			workspace_id, fleet_id, created_by, title, goal, status, current_stage,
			depth_tier, product_round, product_round_budget
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id
	`, parseUUID(testWorkspaceID), fleetID, parseUUID(testUserID), title+"-"+suffix,
		"prove LRM-1505 typed graph merge", "running", "s2_sources", "standard", int32(1), int32(5)).Scan(&session); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("create research session: %v", err)
	}
	if _, err = tx.Exec(ctx, `SELECT research_ensure_run_session_passport($1, $2)`, parseUUID(testWorkspaceID), session); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("ensure run session passport: %v", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit research session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM research_session WHERE id = $1`, session)
	})
	return session
}

// helper to bootstrap a research session owned by an active fleet member in
// testWorkspaceID. Returns the session id and the fleet member (for X-Agent-ID).
func setupMergableResearchSession(t *testing.T, title string) (pgtype.UUID, string) {
	t.Helper()
	agentID := ensureMergableFleetMember(t)
	return newMergableSession(t, agentID, title), agentID
}

// makeTypedInputNode inserts a typed "finding" node (round 1 / round 2) so we
// have real persisted results to fuse.
func makeTypedInputNode(t *testing.T, sessionID pgtype.UUID, round int32, title string) pgtype.UUID {
	t.Helper()
	n := createTestGraphNodeTyped(t, context.Background(), db.CreateResearchGraphNodeTypedParams{
		WorkspaceID:     parseUUID(testWorkspaceID),
		SessionID:       sessionID,
		NodeType:        "finding",
		Title:           title,
		Summary:         title + " summary",
		Status:          "active",
		ActorAgentID:    pgtype.UUID{},
		Level:           "M",
		Round:           round,
		ClusterID:       pgtype.UUID{},
		Confidence:      pgtype.Float8{Float64: 0.7, Valid: true},
		DocumentCount:   1,
		ConclusionCount: 0,
		GoalVersionID:   pgtype.UUID{},
		DerivedFrom:     pgtype.UUID{},
		MergedFrom:      []pgtype.UUID{},
		SupersededBy:    pgtype.UUID{},
		RestartOf:       pgtype.UUID{},
		InvalidatedBy:   pgtype.UUID{},
		Payload:         []byte(`{}`),
	})
	return n.ID
}

func doMerge(t *testing.T, sessionID pgtype.UUID, agentID string, inputIDs []string, idem string) (int, map[string]any) {
	t.Helper()
	var ids []string
	for _, in := range inputIDs {
		ids = append(ids, in)
	}
	req := newRequest(http.MethodPost, "/api/research/sessions/"+uuidToString(sessionID)+"/graph/merge", map[string]any{
		"input_node_ids":  ids,
		"title":           "三源融合结论",
		"summary":         "三处成果融合成单一定论",
		"idempotency_key": idem,
		"reason":          "三处证据重合度高，合并为一",
		"level":           "L",
		"confidence":      0.9,
	})
	req.Header.Set("X-Agent-ID", agentID)
	rec := httptest.NewRecorder()
	testHandler.PostResearchGraphMerge(rec, withURLParam(req, "id", uuidToString(sessionID)))
	var body map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&body)
	return rec.Code, body
}

func doGetTyped(t *testing.T, sessionID pgtype.UUID) (int, ResearchGraphTypedResp) {
	t.Helper()
	req := newRequest(http.MethodGet, "/api/research/sessions/"+uuidToString(sessionID)+"/graph/typed", nil)
	rec := httptest.NewRecorder()
	testHandler.GetResearchGraphTyped(rec, withURLParam(req, "id", uuidToString(sessionID)))
	var resp ResearchGraphTypedResp
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	return rec.Code, resp
}

// AC1: 两轮真实成果 + 一次三合一融合，刷新后等级/谱系/集群/旧节点状态完全一致。
func TestResearchGraphMergeThreeWayFusion(t *testing.T) {
	sessionID, agentID := setupMergableResearchSession(t, "fusion")
	n1 := makeTypedInputNode(t, sessionID, 1, "发现A(r1)")
	n2 := makeTypedInputNode(t, sessionID, 1, "发现B(r1)")
	n3 := makeTypedInputNode(t, sessionID, 2, "发现C(r2)") // round 2 result

	code, body := doMerge(t, sessionID, agentID,
		[]string{uuidToString(n1), uuidToString(n2), uuidToString(n3)}, "merge-"+uuid.NewString())
	if code != http.StatusCreated {
		t.Fatalf("merge status=%d body=%v", code, body)
	}
	conclusionID, _ := body["node"].(map[string]any)["id"].(string)
	if conclusionID == "" {
		t.Fatalf("merge did not return a conclusion node: %v", body)
	}

	// Refresh (GET typed) — everything must be identical to the persisted state.
	code, graph := doGetTyped(t, sessionID)
	if code != http.StatusOK {
		t.Fatalf("get typed status=%d", code)
	}
	if graph.GraphVersion < 1 {
		t.Fatalf("graph_version not bumped: %d", graph.GraphVersion)
	}

	var conclusion *ResearchGraphTypedNodeResp
	inputStatus := map[string]string{}
	var conclusionCount int32
	foundConclusion := 0
	for i := range graph.Nodes {
		n := graph.Nodes[i]
		if n.ID == conclusionID {
			conclusion = &graph.Nodes[i]
			foundConclusion++
		}
		if n.ID == uuidToString(n1) || n.ID == uuidToString(n2) || n.ID == uuidToString(n3) {
			inputStatus[n.ID] = n.Status
		}
	}
	if foundConclusion != 1 {
		t.Fatalf("expected exactly 1 conclusion node, found %d", foundConclusion)
	}
	if conclusion.NodeType != "conclusion" {
		t.Fatalf("conclusion node_type=%q", conclusion.NodeType)
	}
	if conclusion.Level != "L" {
		t.Fatalf("conclusion level=%q", conclusion.Level)
	}
	if len(conclusion.MergedFrom) != 3 {
		t.Fatalf("conclusion merged_from=%v (want 3)", conclusion.MergedFrom)
	}
	if conclusion.ConclusionCount != 3 {
		t.Fatalf("conclusion conclusion_count=%d (want 3)", conclusion.ConclusionCount)
	}
	for _, want := range []string{uuidToString(n1), uuidToString(n2), uuidToString(n3)} {
		if inputStatus[want] != "superseded" {
			t.Fatalf("input node %s status=%q (want superseded)", want, inputStatus[want])
		}
	}
	// lineage merged index must include the 3 inputs for the conclusion.
	if got := graph.Lineage.Merged[conclusionID]; len(got) != 3 {
		t.Fatalf("lineage.merged[conclusion]=%v (want 3 inputs)", got)
	}
	// superseded lineage: each input must point at the conclusion.
	for _, want := range []string{uuidToString(n1), uuidToString(n2), uuidToString(n3)} {
		if graph.Lineage.Superseded[want] != conclusionID {
			t.Fatalf("lineage.superseded[%s]=%q (want %s)", want, graph.Lineage.Superseded[want], conclusionID)
		}
	}
	// merged_from edges must be present in the graph.
	mergedEdges := 0
	for _, e := range graph.Edges {
		if e.EdgeType == "merged_from" && e.ToNodeID == conclusionID {
			mergedEdges++
		}
	}
	if mergedEdges != 3 {
		t.Fatalf("merged_from edges=%d (want 3)", mergedEdges)
	}
	_ = conclusionCount
}

// AC2: 重复提交融合命令（同一 idempotency key）不产生第二个融合节点。
func TestResearchGraphMergeIdempotent(t *testing.T) {
	sessionID, agentID := setupMergableResearchSession(t, "idem")
	n1 := makeTypedInputNode(t, sessionID, 1, "A")
	n2 := makeTypedInputNode(t, sessionID, 1, "B")
	n3 := makeTypedInputNode(t, sessionID, 2, "C")

	key := "merge-idem-" + uuid.NewString()
	ids := []string{uuidToString(n1), uuidToString(n2), uuidToString(n3)}

	code1, body1 := doMerge(t, sessionID, agentID, ids, key)
	if code1 != http.StatusCreated {
		t.Fatalf("first merge status=%d body=%v", code1, body1)
	}
	firstID, _ := body1["node"].(map[string]any)["id"].(string)

	code2, body2 := doMerge(t, sessionID, agentID, ids, key)
	if code2 != http.StatusOK {
		t.Fatalf("second merge status=%d body=%v (want 200 replay)", code2, body2)
	}
	if body2["duplicate"] != true {
		t.Fatalf("second merge duplicate=%v (want true)", body2["duplicate"])
	}
	secondID, _ := body2["node"].(map[string]any)["id"].(string)
	if secondID != firstID {
		t.Fatalf("second merge returned a different conclusion: %s vs %s", secondID, firstID)
	}

	// Only one conclusion node in the graph.
	_, graph := doGetTyped(t, sessionID)
	conclusions := 0
	for _, n := range graph.Nodes {
		if n.NodeType == "conclusion" {
			conclusions++
		}
	}
	if conclusions != 1 {
		t.Fatalf("expected exactly 1 conclusion node after idempotent replay, found %d", conclusions)
	}
}

// AC3: 不同 session 无法互相引用节点（cross-session rejection）。
func TestResearchGraphMergeCrossSessionRejected(t *testing.T) {
	sessionA, agentID := setupMergableResearchSession(t, "cross-sess-a")
	sessionB, _ := setupMergableResearchSession(t, "cross-sess-b")
	n1 := makeTypedInputNode(t, sessionA, 1, "A")
	n2 := makeTypedInputNode(t, sessionA, 1, "B")
	foreign := makeTypedInputNode(t, sessionB, 1, "foreign") // belongs to sessionB

	code, body := doMerge(t, sessionA, agentID,
		[]string{uuidToString(n1), uuidToString(n2), uuidToString(foreign)},
		"merge-cross-sess-"+uuid.NewString())
	if code == http.StatusCreated {
		t.Fatalf("merge with foreign-session node must be rejected, got 201")
	}
	if code != http.StatusConflict && code != http.StatusBadRequest {
		t.Fatalf("cross-session rejection status=%d body=%v", code, body)
	}
	// No conclusion may be created.
	_, graph := doGetTyped(t, sessionA)
	for _, n := range graph.Nodes {
		if n.NodeType == "conclusion" {
			t.Fatalf("cross-session merge must not create a conclusion node")
		}
	}
}

// AC3b: 不同 workspace 的节点不可被引用（GetResearchGraphNode 按 workspace 作用域）。
func TestResearchGraphMergeCrossWorkspaceRejected(t *testing.T) {
	sessionA, agentID := setupMergableResearchSession(t, "cross-ws-a")
	if testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	// A truly different workspace whose node cannot be referenced from sessionA.
	otherWSID, err := uuid.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO workspace (id, name, slug) VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING
	`, otherWSID, "cross-ws-foreign", "cross-ws-"+uuid.NewString()[:8]); err != nil {
		t.Fatalf("create foreign workspace: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, otherWSID) })

	nA := makeTypedInputNode(t, sessionA, 1, "A")
	nB := makeTypedInputNode(t, sessionA, 1, "B")

	// Foreign node: belongs to sessionA id but is stored under a different
	// workspace, so workspace-scoped lookup must not find it.
	foreignID, err := uuid.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	insertTestGraphNodeRaw(t, ctx, parseUUID(foreignID.String()), parseUUID(otherWSID.String()), sessionA, "finding", "foreign-ws", "foreign")

	code, body := doMerge(t, sessionA, agentID,
		[]string{uuidToString(nA), uuidToString(nB), foreignID.String()},
		"merge-cross-ws-"+uuid.NewString())
	if code == http.StatusCreated {
		t.Fatalf("merge with foreign-workspace node must be rejected, got 201")
	}
	if code != http.StatusBadRequest && code != http.StatusConflict {
		t.Fatalf("cross-workspace rejection status=%d body=%v", code, body)
	}
	msg, _ := body["error"].(string)
	if !strings.Contains(strings.ToLower(msg), "input node not found in this workspace") {
		t.Fatalf("unexpected error message: %q", msg)
	}
	// No conclusion may be created.
	_, graph := doGetTyped(t, sessionA)
	for _, n := range graph.Nodes {
		if n.NodeType == "conclusion" {
			t.Fatalf("cross-workspace merge must not create any conclusion node")
		}
	}
}
