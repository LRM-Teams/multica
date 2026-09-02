// SPDX-License-Identifier: Apache-2.0

package memorygraph

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startLayeredToolServer starts a real tool server with a BM25-only
// retriever (start requires one) over the given config.
func startLayeredToolServer(t *testing.T, store *Store, cfg ExploreConfig) (string, string) {
	t.Helper()
	retr := NewHybridRetriever(store, nil, DefaultRetrievalConfig())
	require.NoError(t, retr.Rebuild(context.Background()))
	srv, err := NewExploreToolServer(store, retr, cfg, storeCurrentVersion(t, store))
	require.NoError(t, err)
	baseURL, token, err := srv.Start(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return baseURL, token
}

// layeredNavigationStore builds a graph with one topic view, two member
// nodes, one pattern node, and the membership + relation edges between
// them (spec §3.3 views, §12.3 pattern isolation).
func layeredNavigationStore(t *testing.T) *Store {
	t.Helper()
	store := NewStore(t.TempDir())
	require.NoError(t, store.Init())
	now := time.Now().UTC()
	nodes := []*Node{
		{NodeID: "topic-dispatch", Role: NodeRoleTopic, Level: 3, Body: "topic view over dispatch behavior"},
		{NodeID: "member-leaf", Level: 0, Body: "leaf statement about dispatch retries", DerivedAtomKinds: []AtomKind{AtomFact, AtomEvent}},
		{NodeID: "member-mid", Level: 1, Body: "mid-level summary of dispatch behavior", DerivedAtomKinds: []AtomKind{AtomDecision}},
		{NodeID: "pattern-hidden", Role: NodeRolePattern, Level: 0, Body: "recurrence summary for evolution curation"},
	}
	for _, n := range nodes {
		n.CreatedBy = CreatorConsolidator
		n.CreatedVersion = 1
		n.UpdatedVersion = 1
		n.ObservedAt = now
		require.NoError(t, store.SaveNode(1, n))
	}
	edges := []*Edge{
		// Membership: member --member_of--> view (spec §3.3).
		{EdgeID: "m1", Type: EdgeTypeMemberOf, From: "member-leaf", To: "topic-dispatch", SourceLevel: 0, TargetLevel: 3, LevelDelta: 3, CreatedBy: CreatorConsolidator, CreatedVersion: 1},
		{EdgeID: "m2", Type: EdgeTypeMemberOf, From: "member-mid", To: "topic-dispatch", SourceLevel: 1, TargetLevel: 3, LevelDelta: 2, CreatedBy: CreatorConsolidator, CreatedVersion: 1},
		// A relation edge pointing into the pattern node: without a pattern
		// grant the pattern must never surface as an expand candidate.
		{EdgeID: "m3", Type: EdgeTypeSupports, From: "member-leaf", To: "pattern-hidden", SourceLevel: 0, TargetLevel: 0, LevelDelta: 0, CreatedBy: CreatorConsolidator, CreatedVersion: 1},
		// One skill relation edge so the vocabulary is exercised end to end.
		{EdgeID: "m4", Type: EdgeTypeApplicableTo, From: "member-mid", To: "topic-dispatch", SourceLevel: 1, TargetLevel: 3, LevelDelta: 2, CreatedBy: CreatorConsolidator, CreatedVersion: 1},
	}
	// SaveEdges rewrites the whole edge files, so all relations go in one
	// call (a per-edge loop would leave only the last edge on disk).
	require.NoError(t, store.SaveEdges(1, nil, edges))
	return store
}

func TestGraphValidateAdmitsMembershipAndSkillRelationEdges(t *testing.T) {
	for _, edgeType := range []string{
		EdgeTypeMemberOf, EdgeTypeValidatedBy, EdgeTypeApplicableTo,
		EdgeTypeRequires, EdgeTypeUses, EdgeTypeComposesWith,
		EdgeTypeComposedOf, EdgeTypeRecommendedFor, EdgeTypeConflictsWith,
	} {
		assert.True(t, RelationEdgeTypes[edgeType], "relation edge %q must be admitted", edgeType)
	}
	assert.False(t, RelationEdgeTypes["made_up_relation"], "unknown relation types stay rejected")
}

// TestExpandTraversesMembershipEdgesBothDirections: layered navigation
// reaches view members from the Topic node and the Topic node from a
// member, via the explicit membership edge (spec §7.3). Expand candidates
// are served inline by /explore's Neighbors (no standalone /expand route).
func TestExpandTraversesMembershipEdgesBothDirections(t *testing.T) {
	store := layeredNavigationStore(t)
	cfg := testExploreConfig()
	cfg.MaxExpandPerRound = 10
	baseURL, token := startLayeredToolServer(t, store, cfg)
	traj := exploreStart(t, baseURL, token, "dispatch retries")

	viewResp := exploreNodes(t, baseURL, token, traj, "topic-dispatch")
	require.Len(t, viewResp.Nodes, 1)
	memberVias := map[string]string{}
	for _, c := range viewResp.Nodes[0].Neighbors {
		memberVias[c.NodeID] = c.Via
	}
	assert.Equal(t, "member_of", memberVias["member-leaf"], "view expand must reach members via member_of")
	assert.Equal(t, "member_of", memberVias["member-mid"])

	leafResp := exploreNodes(t, baseURL, token, traj, "member-leaf")
	require.Len(t, leafResp.Nodes, 1)
	vias := map[string]string{}
	for _, c := range leafResp.Nodes[0].Neighbors {
		vias[c.NodeID] = c.Via
	}
	assert.Equal(t, "member_of", vias["topic-dispatch"], "member expand must reach the view via member_of")
}

// TestExpandExcludesPatternWithoutGrant: the supports edge into the
// pattern node exists, but no expand candidate may surface the pattern
// without the evolution-plane grant (spec §12.3, Slice 1.3 test-first).
func TestExpandExcludesPatternWithoutGrant(t *testing.T) {
	store := layeredNavigationStore(t)
	cfg := testExploreConfig()
	cfg.MaxExpandPerRound = 10
	baseURL, token := startLayeredToolServer(t, store, cfg)
	traj := exploreStart(t, baseURL, token, "dispatch retries")

	leafResp := exploreNodes(t, baseURL, token, traj, "member-leaf")
	require.Len(t, leafResp.Nodes, 1)
	for _, c := range leafResp.Nodes[0].Neighbors {
		assert.NotEqual(t, "pattern-hidden", c.NodeID, "pattern node surfaced as an expand candidate without a grant")
	}

	// Exploring the pattern node by id is rejected like any invisible node.
	status, body := explorePost(baseURL, token, "/explore", map[string]any{
		"trajectory_id": traj,
		"node_ids":      []string{"pattern-hidden"},
	})
	assert.Equal(t, http.StatusNotFound, status, "exploring a pattern node without a grant must 404, body %s", body)
}

// TestV1ExploreResponsesStayNodeOnly: with layered navigation off (the
// default), the wire responses carry no v2 role/kind metadata at all —
// v1 clients cannot obtain Atom/Skill/View capability fields (spec §6
// rule 4, acceptance 17).
func TestV1ExploreResponsesStayNodeOnly(t *testing.T) {
	store := layeredNavigationStore(t)
	cfg := testExploreConfig()
	baseURL, token := startLayeredToolServer(t, store, cfg)
	traj := exploreStart(t, baseURL, token, "dispatch retries")

	status, body := explorePost(baseURL, token, "/explore", map[string]any{
		"trajectory_id": traj,
		"node_ids":      []string{"member-mid"},
	})
	require.Equal(t, http.StatusOK, status, string(body))
	assert.NotContains(t, string(body), "node_role", "v1 responses must not carry v2 role metadata")
	assert.NotContains(t, string(body), "derived_atom_kinds", "v1 responses must not carry v2 kind metadata")

	var resp exploreResponse
	require.NoError(t, json.Unmarshal(body, &resp))
	require.Len(t, resp.Nodes, 1)
	assert.Empty(t, resp.Nodes[0].NodeRole)
	assert.Empty(t, resp.Nodes[0].DerivedAtomKinds)
}

// TestLayeredNavigationServesRoleMetadata: with the v2 flag on, explored
// nodes carry their effective role and derived atom kinds, and candidates
// carry their role (spec §7.3 v2 additive fields).
func TestLayeredNavigationServesRoleMetadata(t *testing.T) {
	store := layeredNavigationStore(t)
	cfg := testExploreConfig()
	cfg.MaxExpandPerRound = 10
	cfg.LayeredNavigation = true
	baseURL, token := startLayeredToolServer(t, store, cfg)
	traj := exploreStart(t, baseURL, token, "dispatch retries")

	status, body := explorePost(baseURL, token, "/explore", map[string]any{
		"trajectory_id": traj,
		"node_ids":      []string{"topic-dispatch", "member-mid"},
	})
	require.Equal(t, http.StatusOK, status, string(body))
	assert.Contains(t, string(body), "node_role")

	var resp exploreResponse
	require.NoError(t, json.Unmarshal(body, &resp))
	require.Len(t, resp.Nodes, 2)
	byID := map[string]exploredNode{}
	for _, n := range resp.Nodes {
		byID[n.NodeID] = n
	}
	assert.Equal(t, "topic", byID["topic-dispatch"].NodeRole)
	assert.Equal(t, "memory", byID["member-mid"].NodeRole, "role-less nodes report the effective memory role")
	assert.Equal(t, []AtomKind{AtomDecision}, byID["member-mid"].DerivedAtomKinds)

	for _, c := range byID["topic-dispatch"].Neighbors {
		if c.NodeID == "member-mid" {
			assert.Equal(t, "memory", c.NodeRole)
		}
	}
}

// exploreStart reserves one trajectory through the real /start endpoint.
func exploreStart(t *testing.T, baseURL, token, query string) string {
	t.Helper()
	status, body := explorePost(baseURL, token, "/start", map[string]any{"query": query, "idempotency_key": "ik-" + strings.ReplaceAll(query, " ", "-")})
	if status != http.StatusOK {
		t.Fatalf("start: status = %d body %s", status, body)
	}
	var resp struct {
		TrajectoryID string `json:"trajectory_id"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	require.NotEmpty(t, resp.TrajectoryID)
	return resp.TrajectoryID
}
