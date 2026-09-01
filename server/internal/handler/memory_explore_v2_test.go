// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func exploreV2Route(t *testing.T, method, path, body string) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	req.Header.Set("Content-Type", "application/json")
	// Handlers under /api/workspaces/{id}/… read the id from the chi route
	// context; direct calls must provide it explicitly.
	if strings.Contains(path, "/memory/explore-v2/") {
		parts := strings.Split(path, "/")
		for i, segment := range parts {
			if segment == "workspaces" && i+1 < len(parts) {
				route := chi.NewRouteContext()
				route.URLParams.Add("id", parts[i+1])
				req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, route))
				break
			}
		}
	}
	w := httptest.NewRecorder()
	return w, req
}

// Route registration alone never enables access: with the workspace's
// memory_explore_v2 gate red every v2 route refuses (503) — never a v1-shaped
// fallback — and leaves nothing behind.
func TestMemoryExploreV2API_DisabledGateKeepsRoutesUnavailable(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	// The fixture workspace (not a per-test one): published DAG rows are
	// immutable, so cleanup relies on the harness's global DAG truncate,
	// which only covers the fixture workspace.
	workspaceUUID := mustParseUUIDString(t, testWorkspaceID)
	workspaceID := workspaceUUID.String()
	channelUUID := createGraphMemoryTestChannel(t, workspaceUUID)
	channelID := channelUUID.String()
	setMemoryExplorePhaseGate(t, workspaceID, false)

	w, req := exploreV2Route(t, "POST",
		"/api/workspaces/"+workspaceID+"/memory/explore-v2/search",
		`{"query":"dispatch routing","channel_id":"`+channelID+`"}`)
	req.Header.Set("X-User-ID", testUserID)
	testHandler.MemoryExploreV2Search(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code, "disabled gate must refuse search: %s", w.Body.String())

	w, req = exploreV2Route(t, "POST",
		"/api/workspaces/"+workspaceID+"/memory/explore-v2/evidence",
		`{"trajectory_id":"traj-x","ref":{"kind":"staging_atom","atom_id":"a1","segment_id":"s1"}}`)
	req.Header.Set("X-User-ID", testUserID)
	testHandler.MemoryExploreV2Evidence(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code, "disabled gate must refuse evidence")

	w, req = exploreV2Route(t, "GET",
		"/api/workspaces/"+workspaceID+"/memory/explore-v2/history?trajectory_id=traj-x", "")
	req.Header.Set("X-User-ID", testUserID)
	testHandler.MemoryExploreV2History(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code, "disabled gate must refuse history")
}

// Green gate: search answers structured refs (never raw maps), evidence
// resolves one strictly-parsed ref against the trajectory's plan, history is
// bounded. A malformed ref is rejected before any resolver runs.
func TestMemoryExploreV2API_SearchEvidenceHistoryUnderGreenGate(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	workspaceUUID := mustParseUUIDString(t, testWorkspaceID)
	workspaceID := workspaceUUID.String()
	channelUUID := createGraphMemoryTestChannel(t, workspaceUUID)
	channelID := channelUUID.String()
	setMemoryExplorePhaseGate(t, workspaceID, true)
	t.Cleanup(func() { setMemoryExplorePhaseGate(t, workspaceID, false) })

	// A published, unfenced atom for the search channel: reuse the recall
	// publisher pipeline through the service layer.
	plan, atomID, segmentID := publishExploreV2FixtureAtom(t, workspaceUUID, channelUUID)

	// Search: structured hits with refs; no raw ref maps in or out.
	w, req := exploreV2Route(t, "POST",
		"/api/workspaces/"+workspaceID+"/memory/explore-v2/search",
		`{"query":"NIMBUS codename","channel_id":"`+channelID+`"}`)
	req.Header.Set("X-User-ID", testUserID)
	testHandler.MemoryExploreV2Search(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var search struct {
		WorkspaceID string `json:"workspace_id"`
		Hits        []struct {
			Ref struct {
				Kind      string `json:"kind"`
				AtomID    string `json:"atom_id"`
				SegmentID string `json:"segment_id"`
			} `json:"ref"`
			Class string  `json:"class"`
			Score float64 `json:"score"`
		} `json:"hits"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &search))
	require.NotEmpty(t, search.Hits, "the published atom must be searchable")
	assert.Equal(t, "staging_atom", search.Hits[0].Ref.Kind)
	assert.Equal(t, atomID, search.Hits[0].Ref.AtomID)

	// Evidence: strict typed ref decode; malformed kind refused with 400.
	w, req = exploreV2Route(t, "POST",
		"/api/workspaces/"+workspaceID+"/memory/explore-v2/evidence",
		`{"trajectory_id":"`+plan.TrajectoryID+`","ref":{"kind":"staging_atom","atom_id":"`+atomID+`","segment_id":"`+segmentID+`"}}`)
	req.Header.Set("X-User-ID", testUserID)
	testHandler.MemoryExploreV2Evidence(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var evidence struct {
		SegmentID string `json:"segment_id"`
		Summary   string `json:"summary"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &evidence))
	assert.Equal(t, segmentID, evidence.SegmentID)

	w, req = exploreV2Route(t, "POST",
		"/api/workspaces/"+workspaceID+"/memory/explore-v2/evidence",
		`{"trajectory_id":"`+plan.TrajectoryID+`","ref":{"kind":"graph_node","atom_id":"`+atomID+`"}}`)
	req.Header.Set("X-User-ID", testUserID)
	testHandler.MemoryExploreV2Evidence(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code, "a forged ref shape must be rejected before resolution")

	// History: bounded list of the trajectory's walked refs.
	w, req = exploreV2Route(t, "GET",
		"/api/workspaces/"+workspaceID+"/memory/explore-v2/history?trajectory_id="+plan.TrajectoryID, "")
	req.Header.Set("X-User-ID", testUserID)
	testHandler.MemoryExploreV2History(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var history struct {
		TrajectoryID string `json:"trajectory_id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &history))
	assert.Equal(t, plan.TrajectoryID, history.TrajectoryID)
}

// taskUUIDForExploreV2Fixture is the deterministic task identity of the v2
// API fixture pipeline. Deterministic ids keep re-runs of the suite on the
// same disposable schema convergent instead of accumulating one publish per
// run.
func taskUUIDForExploreV2Fixture() pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}, Valid: true}
}

func mustParseUUIDString(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	parsed, err := util.ParseUUID(value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return parsed
}

// runtimeIDForExploreV2Fixture provisions one local runtime row for the
// fixture agent (agent.runtime_id is NOT NULL).
func runtimeIDForExploreV2Fixture(t *testing.T, workspaceUUID pgtype.UUID) string {
	t.Helper()
	var runtimeID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_runtime(workspace_id,daemon_id,name,runtime_mode,provider,status,device_info,metadata,visibility,last_seen_at)
		VALUES($1,$2,'explore-v2-runtime','local','pi','online','','{}','private',now()) RETURNING id::text`,
		workspaceUUID, "explore-v2-daemon-"+workspaceUUID.String()[:8]).Scan(&runtimeID); err != nil {
		t.Fatalf("seed fixture runtime: %v", err)
	}
	return runtimeID
}

// publishExploreV2FixtureAtom runs one task through the canonical DAG
// publish pipeline so a real, unfenced atom exists for the channel scope,
// then pins an Explore plan for a fresh trajectory over that channel graph.
func publishExploreV2FixtureAtom(t *testing.T, workspaceUUID, channelUUID pgtype.UUID) (plan service.MemoryExplorePlan, atomID, segmentID string) {
	workspaceID := workspaceUUID.String()
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Drive the REAL canonical pipeline: one task + message, one visible
	// boundary, one publish transaction — so the atom is a true published,
	// unfenced, channel-scoped atom (the same path production uses).
	runtimeID := runtimeIDForExploreV2Fixture(t, workspaceUUID)
	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent(workspace_id,name,display_name,runtime_mode,runtime_config,runtime_id,instructions)
		VALUES($1,$2,'Explore v2 fixture','local','{}',$3,'fixture') RETURNING id::text`,
		workspaceUUID, "explore-v2-agent-"+workspaceID[:8], runtimeID).Scan(&agentID); err != nil {
		t.Fatalf("seed fixture agent: %v", err)
	}
	// The fixture workspace is shared. Other handler tests resolve "the"
	// workspace agent with an unordered LIMIT 1, so this fixture must not
	// leave a runtime-bound agent behind once the test finishes. Deletion
	// runs leaves-first: the inbox fixture trigger opens a wake session for
	// every seeded event, and task rows reference the task before the agent.
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		taskID := taskUUIDForExploreV2Fixture()
		_, _ = testPool.Exec(cctx, `DELETE FROM agent_session WHERE agent_id=$1::uuid`, agentID)
		_, _ = testPool.Exec(cctx, `DELETE FROM task_message WHERE task_id=$1`, taskID)
		_, _ = testPool.Exec(cctx, `DELETE FROM agent_inbox_event WHERE agent_id=$1::uuid`, agentID)
		_, _ = testPool.Exec(cctx, `DELETE FROM agent WHERE id=$1::uuid`, agentID)
		_, _ = testPool.Exec(cctx, `DELETE FROM agent_runtime WHERE id=$1::uuid`, runtimeID)
	})
	taskUUID := taskUUIDForExploreV2Fixture()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_inbox_event(id,workspace_id,channel_id,agent_id,reason)
		VALUES($1,$2,$3,$4::uuid,'explore_v2_api_fixture') ON CONFLICT (id) DO NOTHING`, taskUUID, workspaceUUID, channelUUID, agentID); err != nil {
		t.Fatalf("seed fixture task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO task_message(task_id,seq,content,type)
		VALUES($1,1,'My project codename is NIMBUS and the launch date is March 3rd.','user')`, taskUUID); err != nil {
		t.Fatalf("seed fixture message: %v", err)
	}
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin boundary tx: %v", err)
	}
	boundary, err := service.NewUniversalInteractionDAG().RecordBoundaryTx(ctx, db.New(tx), tx, service.DAGBoundaryInput{
		WorkspaceID:  workspaceUUID,
		Task:         db.AgentInboxEvent{ID: taskUUID, WorkspaceID: workspaceUUID, ChannelID: channelUUID},
		BoundaryKind: service.DAGBoundaryVisible, CloseActionKind: service.DAGCloseMessage,
		EndSeq: 1, ActionKey: taskUUID.String() + ":explore-v2-api", ChannelID: channelUUID,
		ActionID:        pgtype.UUID{Bytes: [16]byte{9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9}, Valid: true},
		RouteGeneration: 1, MemoryTypeAtEvent: "graph",
	})
	if err != nil {
		tx.Rollback(ctx)
		t.Fatalf("record boundary: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit boundary: %v", err)
	}
	// The outbox claim is ordered oldest-updated-first with a bounded batch,
	// and the shared fixture workspace accumulates pending rows from earlier
	// handler tests. One claim can starve this just-recorded segment, so
	// drain until THIS segment's atom exists (or the queue empties without
	// it, which is the actual failure).
	publisher := service.NewInteractionDAGPublisher(testPool)
	segmentID = boundary.SegmentID
	for attempt := 0; ; attempt++ {
		published, err := publisher.PublishClaim(ctx, 10)
		if err != nil {
			t.Fatalf("publish claim: %v", err)
		}
		err = testPool.QueryRow(ctx, `
			SELECT atom_id FROM graph_memory_atom
			WHERE workspace_id=$1 AND segment_id=$2`, workspaceUUID, boundary.SegmentID).Scan(&atomID)
		if err == nil {
			break
		}
		if published == 0 || attempt >= 200 {
			var status, lastError string
			var attempts int32
			_ = testPool.QueryRow(ctx, `
				SELECT o.status, o.attempts, COALESCE(o.last_error,'') FROM interaction_dag_publish_outbox o
				WHERE o.workspace_id=$1 AND o.segment_id=$2`, workspaceUUID, boundary.SegmentID).Scan(&status, &attempts, &lastError)
			t.Fatalf("load published atom for segment %s: %v (outbox status=%s attempts=%d last_error=%q)",
				boundary.SegmentID, err, status, attempts, lastError)
		}
	}
	plans := service.NewMemoryExplorePlanService(testPool)
	plan, err = plans.CreatePlan(ctx, workspaceUUID, "traj-explore-v2-api", []service.PinnedGraph{
		{Kind: "channel", OwnerID: channelUUID.String(), Generation: 1},
	})
	if err != nil {
		t.Fatalf("create explore plan: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM memory_explore_plan WHERE workspace_id=$1 AND trajectory_id='traj-explore-v2-api'`, workspaceUUID)
	})
	return plan, atomID, segmentID
}
