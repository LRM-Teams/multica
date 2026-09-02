// SPDX-License-Identifier: Apache-2.0

package service

// Slice 1.3 batch 2 (skill-graph spec §6/§12.3): typed-ref resolution is
// server-side, pattern projections fail closed in the task-recall plane, the
// internal SkillEvolutionRef resolver is capability-scoped by purpose, and
// v1/v2 payloads stay generation-separated.

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/skillevolution"
)

// typedRefGraphStore installs the workspace root plus one channel-route
// graph holding a recall node and a pattern projection, then returns the
// pinned-graph plan inputs the harness uses.
func typedRefGraphStore(t *testing.T, workspaceID, channelID string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("MULTICA_WORKSPACES_ROOT", root)
	dir, err := memorygraph.EnsureScopedDir(root, workspaceID, memorygraph.GraphDirKindChannel, channelID)
	require.NoError(t, err)
	store := memorygraph.NewStore(dir)
	require.NoError(t, store.Init())
	now := time.Now().UTC()
	for _, node := range []*memorygraph.Node{
		{NodeID: "typed-recall-node", Visibility: "channel", ChannelID: channelID, Body: "typed ref fixture body", CreatedBy: memorygraph.CreatorIngester, CreatedVersion: 1, UpdatedVersion: 1, ObservedAt: now},
		{NodeID: "typed-pattern-node", Role: memorygraph.NodeRolePattern, Visibility: "channel", ChannelID: channelID, Body: "pattern projection", CreatedBy: memorygraph.CreatorConsolidator, CreatedVersion: 1, UpdatedVersion: 1, ObservedAt: now},
	} {
		require.NoError(t, store.SaveNode(1, node))
	}
}

// Authorization is re-derived from server-side state (spec §6 rule 3): a
// forged caller channel on a graph_node ref never changes which graph
// authorizes it, an id in no pinned graph fails closed, and a pinned graph
// with no store on disk authorizes nothing.
func TestTypedRefGraphNodeResolutionIsServerSide(t *testing.T) {
	h := newExploreV2Harness(t, true)
	defer h.Close()
	typedRefGraphStore(t, h.workspace.String(), h.channel.String())

	svc := NewMemoryExplorePlanService(h.pubPool)
	plan, err := svc.CreatePlan(h.ctx, h.workspace, "traj-typed-ref", h.planGraphs())
	require.NoError(t, err)

	forged := memorygraph.MemoryRef{Kind: memorygraph.MemoryRefGraphNode, NodeID: "typed-recall-node", ChannelID: uuid.NewString()}
	resolved, err := svc.ResolveRef(h.ctx, h.workspace, plan, forged)
	require.NoError(t, err, "the pinned channel graph authorizes regardless of caller fields")
	assert.Equal(t, h.channel.String(), resolved.Ref.ChannelID,
		"the resolved ref carries the server-derived channel, not the forged caller field")

	_, err = svc.ResolveRef(h.ctx, h.workspace, plan, memorygraph.MemoryRef{Kind: memorygraph.MemoryRefGraphNode, NodeID: "no-such-node"})
	require.ErrorIs(t, err, ErrMemoryRefUnauthorized)

	missing := plan
	missing.Graphs = []PinnedGraph{{Kind: "channel", OwnerID: uuid.NewString(), Generation: 1}}
	_, err = svc.ResolveRef(h.ctx, h.workspace, missing, memorygraph.MemoryRef{Kind: memorygraph.MemoryRefGraphNode, NodeID: "typed-recall-node"})
	require.ErrorIs(t, err, ErrMemoryRefUnauthorized, "a pinned graph without an initialized store authorizes nothing")
}

// Pattern projections never resolve through the task-recall plane (spec
// §12.3): the public graph_node ref validates, but the resolver refuses it
// while an ordinary memory node of the same shape resolves.
func TestTypedRefPatternPurposeFailsClosedInTaskRecallExplore(t *testing.T) {
	h := newExploreV2Harness(t, true)
	defer h.Close()
	typedRefGraphStore(t, h.workspace.String(), h.channel.String())

	svc := h.svc()
	_, err := svc.Start(h.ctx, h.workspace, "traj-pattern-purpose", h.planGraphs())
	require.NoError(t, err)

	patternRef := memorygraph.MemoryRef{Kind: memorygraph.MemoryRefGraphNode, NodeID: "typed-pattern-node", ChannelID: h.channel.String()}
	require.NoError(t, memorygraph.ValidateMemoryRef(patternRef))
	_, err = svc.Explore(h.ctx, h.workspace, "traj-pattern-purpose", patternRef)
	require.ErrorIs(t, err, ErrMemoryRefUnauthorized, "pattern projections require an evolution-plane capability")

	_, err = svc.Explore(h.ctx, h.workspace, "traj-pattern-purpose",
		memorygraph.MemoryRef{Kind: memorygraph.MemoryRefGraphNode, NodeID: "typed-recall-node", ChannelID: h.channel.String()})
	require.NoError(t, err, "an ordinary node of the same ref shape resolves")
}

// The internal SkillEvolutionRef resolver is the only path to pattern
// projections, gated by server-derived purpose: evolution-plane purposes
// resolve, task_recall and evaluation_audit fail closed, ledger-backed kinds
// stay closed until Phase 2, and forged ids never masquerade as patterns.
func TestPatternPurposeSkillEvolutionRefResolver(t *testing.T) {
	workspaceID := uuid.NewString()
	store := memorygraph.NewStore(t.TempDir())
	require.NoError(t, store.Init())
	now := time.Now().UTC()
	for _, node := range []*memorygraph.Node{
		{NodeID: "pattern-42", Role: memorygraph.NodeRolePattern, Visibility: "channel", ChannelID: uuid.NewString(), Body: "pattern projection", CreatedBy: memorygraph.CreatorConsolidator, CreatedVersion: 1, UpdatedVersion: 1, ObservedAt: now},
		{NodeID: "memory-42", Visibility: "channel", ChannelID: uuid.NewString(), Body: "plain memory", CreatedBy: memorygraph.CreatorIngester, CreatedVersion: 1, UpdatedVersion: 1, ObservedAt: now},
	} {
		require.NoError(t, store.SaveNode(1, node))
	}
	resolver := NewSkillEvolutionRefResolver(store, workspaceID)
	patternRef := skillevolution.SkillEvolutionRef{Kind: skillevolution.RefPattern, ID: "pattern-42", WorkspaceID: workspaceID}

	for _, purpose := range []MemoryPurpose{MemoryPurposeSkillEvolution, MemoryPurposeCuratorReview} {
		resolved, err := resolver.Resolve(patternRef, purpose)
		require.NoError(t, err, "purpose %s resolves the pattern projection", purpose)
		assert.Equal(t, "pattern-42", resolved.NodeID)
	}

	for _, purpose := range []MemoryPurpose{MemoryPurposeTaskRecall, MemoryPurposeEvaluationAudit} {
		_, err := resolver.Resolve(patternRef, purpose)
		require.ErrorIs(t, err, ErrSkillEvolutionRefUnauthorized, "purpose %s must not read the pattern plane", purpose)
	}

	_, err := resolver.Resolve(skillevolution.SkillEvolutionRef{Kind: skillevolution.RefPattern, ID: "memory-42", WorkspaceID: workspaceID}, MemoryPurposeSkillEvolution)
	require.ErrorIs(t, err, ErrSkillEvolutionRefUnauthorized, "a non-pattern node never resolves as a pattern")

	_, err = resolver.Resolve(skillevolution.SkillEvolutionRef{Kind: skillevolution.RefPattern, ID: "pattern-42", WorkspaceID: uuid.NewString()}, MemoryPurposeSkillEvolution)
	require.ErrorIs(t, err, ErrSkillEvolutionRefUnauthorized, "cross-workspace refs never resolve")

	_, err = resolver.Resolve(skillevolution.SkillEvolutionRef{Kind: skillevolution.RefSkillCandidate, ID: "cand-1", WorkspaceID: workspaceID}, MemoryPurposeSkillEvolution)
	require.ErrorIs(t, err, ErrSkillEvolutionRefUnauthorized, "ledger-backed kinds stay closed until the Phase 2 ledger exists")

	_, err = resolver.Resolve(skillevolution.SkillEvolutionRef{Kind: skillevolution.RefPattern, ID: "", WorkspaceID: workspaceID}, MemoryPurposeSkillEvolution)
	require.ErrorIs(t, err, ErrSkillEvolutionRefUnauthorized, "malformed refs fail validation inside the resolver")
}

// The public MemoryRef vocabulary stays closed to the evolution kinds: an
// evolution ref smuggled into a task-recall payload never validates.
func TestV1V2PublicMemoryRefKindsStayClosedToEvolutionRefs(t *testing.T) {
	for _, kind := range []string{"pattern", "skill", "skill_candidate", "assertion_manifest", "evaluation_run", "approval"} {
		ref := memorygraph.MemoryRef{Kind: memorygraph.MemoryRefKind(kind), NodeID: "x"}
		assert.Error(t, memorygraph.ValidateMemoryRef(ref), "public MemoryRef must reject evolution kind %q", kind)
	}
}

// A v1 gateway run serves node-only payloads: no protocol_generation, no
// v2 layered-navigation metadata, no v2 plan/seeds envelope (spec §6 rule 4,
// acceptance 17).
func TestV1V2GatewayV1ResponsesStayNodeOnly(t *testing.T) {
	f := seedGatewayResearchWorkspace(t)

	start := f.callGateway(t, "start", `{"query":"generation isolation","idempotency_key":"v1v2-start"}`)
	require.Equal(t, http.StatusOK, start.Code, start.Body.String())
	for _, key := range []string{"protocol_generation", "\"plan\"", "\"seeds\""} {
		assert.NotContains(t, start.Body.String(), key, "v1 start must not carry v2 envelope field %s", key)
	}
	var startPayload struct {
		TrajectoryID string `json:"trajectory_id"`
	}
	require.NoError(t, json.Unmarshal(start.Body.Bytes(), &startPayload))
	require.NotEmpty(t, startPayload.TrajectoryID)

	explore := f.callGateway(t, "explore", `{"trajectory_id":"`+startPayload.TrajectoryID+`","node_ids":["channel-node"],"idempotency_key":"v1v2-explore"}`)
	require.Equal(t, http.StatusOK, explore.Code, explore.Body.String())
	for _, key := range []string{"protocol_generation", "node_role", "derived_atom_kinds"} {
		assert.NotContains(t, explore.Body.String(), key, "v1 explore must not carry v2 metadata %s", key)
	}
}

// The v2 explore payload carries typed refs and edges only — never v1
// node-only body/level fields or layered-navigation metadata — and the v1
// tool-server default stays node-only.
func TestV1V2ExploreV2PayloadCarriesNoNodeOnlyFields(t *testing.T) {
	h := newExploreV2Harness(t, true)
	defer h.Close()
	svc := h.svc()
	_, err := svc.Start(h.ctx, h.workspace, "traj-v1v2-payload", h.planGraphs())
	require.NoError(t, err)

	neighbors, err := svc.Explore(h.ctx, h.workspace, "traj-v1v2-payload",
		memorygraph.MemoryRef{Kind: memorygraph.MemoryRefStagingAtom, AtomID: h.atomID, SegmentID: h.segment})
	require.NoError(t, err)
	raw, err := json.Marshal(neighbors)
	require.NoError(t, err)
	for _, key := range []string{"node_role", "derived_atom_kinds", "protocol_generation", "\"body\"", "\"level\""} {
		assert.NotContains(t, string(raw), key, "v2 neighbors payload must not carry v1 node-only field %s", key)
	}

	assert.False(t, memorygraph.DefaultExploreConfig().LayeredNavigation,
		"the v1 tool-server default must stay node-only")
}
