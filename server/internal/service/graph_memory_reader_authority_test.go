// SPDX-License-Identifier: Apache-2.0

package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// activeGraphVersionForStore is the shared reader seam of the three
// production graph readers (recall Begin, agent-gateway start, explore-v2
// SearchExternal). Task 14 pins it: the DB publication index wins even when
// the file-store current pointer has diverged in either direction.
func TestActiveGraphVersionForStore_DBGenerationWinsOverFilePointer(t *testing.T) {
	h := newPublicationHarness(t)
	defer h.Close()

	root := t.TempDir()
	t.Setenv("MULTICA_WORKSPACES_ROOT", root)
	storeDir, err := memorygraph.EnsureScopedDir(root, h.workspace.String(),
		memorygraph.GraphDirKindChannel, h.channel.String())
	require.NoError(t, err)
	store := memorygraph.NewStore(storeDir)
	require.NoError(t, store.Init())
	require.NoError(t, store.SaveNode(1, &memorygraph.Node{
		NodeID: "node-one", Body: "generation one",
		CreatedBy: memorygraph.CreatorIngester, CreatedVersion: 1, UpdatedVersion: 1,
		Visibility: "channel", ChannelID: h.channel.String(),
	}))
	_, err = store.CreateVersionFrom(1, "ttt")
	require.NoError(t, err)
	require.NoError(t, store.SaveNode(2, &memorygraph.Node{
		NodeID: "node-two", Body: "generation two",
		CreatedBy: memorygraph.CreatorIngester, CreatedVersion: 2, UpdatedVersion: 2,
		Visibility: "channel", ChannelID: h.channel.String(),
	}))

	pub, err := PublishGraphMemoryPublication(h.ctx, h.pubPool, GraphMemoryPublicationRequest{
		WorkspaceID: h.workspace, GraphKind: "channel", GraphOwnerID: h.channel,
		Store: store, CandidateVersion: 1, BaseGeneration: 0,
		Sources: h.sources, Coverage: h.coverage, Provenance: h.provenance,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, pub.Generation)

	// File pointer races ahead of the DB (a crashed or manual switch): every
	// reader must still serve the published generation's version 1.
	require.NoError(t, store.SwitchCurrent(2))
	version, err := activeGraphVersionForStore(h.ctx, h.pubPool, h.workspace, "channel", h.channel.String(), store)
	require.NoError(t, err)
	assert.Equal(t, 1, version, "DB generation must win over a raced-ahead file pointer")

	// DB publishes forward while the file pointer lags (the recoverable
	// cache was never healed): readers serve the new generation's version 2.
	require.NoError(t, store.SwitchCurrent(1))
	pub, err = PublishGraphMemoryPublication(h.ctx, h.pubPool, GraphMemoryPublicationRequest{
		WorkspaceID: h.workspace, GraphKind: "channel", GraphOwnerID: h.channel,
		Store: store, CandidateVersion: 2, BaseGeneration: pub.Generation,
		Sources: h.sources, Coverage: h.coverage, Provenance: h.provenance,
	})
	require.NoError(t, err)
	require.EqualValues(t, 2, pub.Generation)
	version, err = activeGraphVersionForStore(h.ctx, h.pubPool, h.workspace, "channel", h.channel.String(), store)
	require.NoError(t, err)
	assert.Equal(t, 2, version, "a lagging file pointer must never shadow the published generation")
}

// The governance status view mirrors reader authority: current_version
// comes from the DB publication index, not the file pointer.
func TestGraphMemoryStatus_ReaderAuthorityMirrorsDB(t *testing.T) {
	h := newPublicationHarness(t)
	defer h.Close()

	root := t.TempDir()
	t.Setenv("MULTICA_WORKSPACES_ROOT", root)
	storeDir, err := memorygraph.EnsureScopedDir(root, h.workspace.String(),
		memorygraph.GraphDirKindChannel, h.channel.String())
	require.NoError(t, err)
	store := memorygraph.NewStore(storeDir)
	require.NoError(t, store.Init())
	require.NoError(t, store.SaveNode(1, &memorygraph.Node{
		NodeID: "node-one", Body: "generation one",
		CreatedBy: memorygraph.CreatorIngester, CreatedVersion: 1, UpdatedVersion: 1,
		Visibility: "channel", ChannelID: h.channel.String(),
	}))
	_, err = store.CreateVersionFrom(1, "ttt")
	require.NoError(t, err)
	require.NoError(t, store.SaveNode(2, &memorygraph.Node{
		NodeID: "node-two", Body: "generation two",
		CreatedBy: memorygraph.CreatorIngester, CreatedVersion: 2, UpdatedVersion: 2,
		Visibility: "channel", ChannelID: h.channel.String(),
	}))
	_, err = PublishGraphMemoryPublication(h.ctx, h.pubPool, GraphMemoryPublicationRequest{
		WorkspaceID: h.workspace, GraphKind: "channel", GraphOwnerID: h.channel,
		Store: store, CandidateVersion: 1, BaseGeneration: 0,
		Sources: h.sources, Coverage: h.coverage, Provenance: h.provenance,
	})
	require.NoError(t, err)
	// The file pointer diverges ahead of the published generation.
	require.NoError(t, store.SwitchCurrent(2))

	status, err := NewGraphMemoryStatusService(db.New(h.pubPool), root).Status(h.ctx, h.workspace.String())
	require.NoError(t, err)
	require.Len(t, status.Graphs, 1)
	assert.Equal(t, 1, status.Graphs[0].CurrentVersion,
		"status must report the DB-authoritative version, not the file pointer")
}
