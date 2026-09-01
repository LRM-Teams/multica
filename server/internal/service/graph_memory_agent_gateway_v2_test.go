// SPDX-License-Identifier: Apache-2.0

package service

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// seedGatewayNegotiationTables creates the minimal agent_runtime and
// graph_memory_channel_agent shapes in the private schema — the boundary
// legacy schema does not carry the 451 tables the negotiation query reads.
func (h *exploreV2Harness) seedGatewayNegotiationTables(t *testing.T) {
	t.Helper()
	if _, err := h.conn.Exec(h.ctx, `
		CREATE TABLE IF NOT EXISTS agent_runtime (
		  id uuid PRIMARY KEY,
		  workspace_id uuid NOT NULL,
		  metadata jsonb NOT NULL DEFAULT '{}'::jsonb
		)`); err != nil {
		t.Fatalf("create minimal agent_runtime table: %v", err)
	}
	if _, err := h.conn.Exec(h.ctx, `
		CREATE TABLE IF NOT EXISTS graph_memory_channel_agent (
		  channel_id uuid PRIMARY KEY,
		  workspace_id uuid NOT NULL,
		  agent_id uuid,
		  runtime_id uuid,
		  status text NOT NULL DEFAULT 'active'
		)`); err != nil {
		t.Fatalf("create minimal managed agent table: %v", err)
	}
	_, err := h.conn.Exec(h.ctx, `
		INSERT INTO graph_memory_channel_agent(channel_id,workspace_id,status)
		VALUES($1,$2,'active') ON CONFLICT (channel_id) DO NOTHING`, h.channel, h.workspace)
	require.NoError(t, err)
}

func (h *exploreV2Harness) insertGatewayRuntime(t *testing.T, metadata string) pgtype.UUID {
	t.Helper()
	id := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	_, err := h.conn.Exec(h.ctx, `
		INSERT INTO agent_runtime(id,workspace_id,metadata)
		VALUES($1,$2,$3::jsonb)`, id, h.workspace, metadata)
	require.NoError(t, err)
	return id
}

func (h *exploreV2Harness) bindGatewayManagedAgent(t *testing.T, runtimeID pgtype.UUID) {
	t.Helper()
	_, err := h.conn.Exec(h.ctx, `
		UPDATE graph_memory_channel_agent SET runtime_id=$2 WHERE channel_id=$1`, h.channel, runtimeID)
	require.NoError(t, err)
}

// The negotiation matrix: generation 2 requires BOTH the daemon-side
// capability (persisted on the managed agent's runtime row at registration)
// AND a green workspace explore gate. Either side missing means generation 1
// — the legacy v1 surface keeps serving an old daemon unchanged.
func TestGraphMemoryAgentGatewayNegotiation_Matrix(t *testing.T) {
	h := newExploreV2Harness(t, true)
	defer h.Close()
	h.seedGatewayNegotiationTables(t)

	capable := h.insertGatewayRuntime(t, `{"capabilities":["memory_explore_v2"]}`)
	legacy := h.insertGatewayRuntime(t, `{"capabilities":["agent_cli_transport"]}`)
	h.bindGatewayManagedAgent(t, capable)

	gw := NewGraphMemoryAgentGateway(h.pubPool, nil)
	assert.Equal(t, 2, gw.NegotiatedProtocolGeneration(h.ctx, h.workspace.String(), h.channel.String()),
		"capability + green gate negotiate generation 2")

	h.bindGatewayManagedAgent(t, legacy)
	assert.Equal(t, 1, gw.NegotiatedProtocolGeneration(h.ctx, h.workspace.String(), h.channel.String()),
		"daemon without the capability stays on generation 1")

	h.bindGatewayManagedAgent(t, capable)
	_, err := db.New(h.pubPool).SetMemoryReadPhaseGate(h.ctx, db.SetMemoryReadPhaseGateParams{
		WorkspaceID: h.workspace, ExploreEnabled: false, RetractionCanaryOk: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, gw.NegotiatedProtocolGeneration(h.ctx, h.workspace.String(), h.channel.String()),
		"capability alone never authorizes a disabled server path")

	// An unbound managed agent (no runtime row) is the pre-rollout state.
	require.NoError(t, h.bindGatewayManagedAgentUnbound(t))
	assert.Equal(t, 1, gw.NegotiatedProtocolGeneration(h.ctx, h.workspace.String(), h.channel.String()))
}

func (h *exploreV2Harness) bindGatewayManagedAgentUnbound(t *testing.T) error {
	_, err := h.conn.Exec(h.ctx, `
		UPDATE graph_memory_channel_agent SET runtime_id=NULL WHERE channel_id=$1`, h.channel)
	return err
}

// The run-level pin: an active run's protocol generation is whatever its
// start negotiated, recorded by the presence of the trajectory's Explore
// plan row. A later gate flip keeps the pin at 2 — the next v2 operation
// fails closed rather than silently serving v1 payloads — and a v1 run never
// switches to v2 mid-run.
func TestGraphMemoryAgentGatewayProtocol_PinnedByPlanRow(t *testing.T) {
	h := newExploreV2Harness(t, true)
	defer h.Close()

	gw := NewGraphMemoryAgentGateway(h.pubPool, nil)
	require.Equal(t, 1, gw.protocolForRun(h.ctx, h.workspace, "traj-unpinned"),
		"a run without a plan row stays on generation 1 even while the gate is green")

	plan, err := NewMemoryExplorePlanService(h.pubPool).CreatePlan(h.ctx, h.workspace, "traj-pinned", h.planGraphs())
	require.NoError(t, err)
	require.NotEmpty(t, plan.TrajectoryID)
	require.Equal(t, 2, gw.protocolForRun(h.ctx, h.workspace, "traj-pinned"),
		"the plan row written by a generation-2 start pins the run at 2")

	// Gate flips red mid-run: the pin survives and the v2 surface refuses
	// the operation — v2 unavailable, never fallback exposure.
	_, err = db.New(h.pubPool).SetMemoryReadPhaseGate(h.ctx, db.SetMemoryReadPhaseGateParams{
		WorkspaceID: h.workspace, ExploreEnabled: false, RetractionCanaryOk: true,
	})
	require.NoError(t, err)
	require.Equal(t, 2, gw.protocolForRun(h.ctx, h.workspace, "traj-pinned"))
	_, err = gw.v2.Explore(h.ctx, h.workspace, "traj-pinned", memorygraph.MemoryRef{
		Kind: memorygraph.MemoryRefStagingAtom, AtomID: h.atomID, SegmentID: h.segment,
	})
	assert.ErrorIs(t, err, ErrMemoryRouteDisabled,
		"a pinned v2 run must not fall back to serving v1 payloads")
}
