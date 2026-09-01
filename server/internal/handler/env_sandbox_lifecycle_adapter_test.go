package handler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestEphemeralSandboxCleanerEnqueuesDeleteWithSandboxCreatorAsInitiator pins
// the actor used by the generic cleanup_on_terminal hook. The cleaner has no
// request context, so it must resolve one from the sandbox itself: a
// "delete" job's initiator_user_id is NOT NULL, and EnvSandboxLifecycleService
// .Delete only falls back to a force-delete when the node is unavailable. With
// an empty actor the enqueue fails to parse and a reachable node's sandbox is
// left running in Cube - the leak this pins shut.
func TestNormalizeLifecycleDockerSelection(t *testing.T) {
	tests := []struct {
		name, template, image, wantTemplate, wantImage string
		wantErr                                        bool
	}{
		{name: "Cube default unchanged", template: "default", wantTemplate: "default"},
		{name: "configured image", template: "default", image: "runtime:test", wantTemplate: "docker:runtime:test", wantImage: "runtime:test"},
		{name: "image encoded in template", template: "docker:runtime:test", wantTemplate: "docker:runtime:test", wantImage: "runtime:test"},
		{name: "matching image and template", template: "docker:runtime:test", image: "runtime:test", wantTemplate: "docker:runtime:test", wantImage: "runtime:test"},
		{name: "conflicting image and template", template: "docker:runtime:a", image: "runtime:b", wantErr: true},
		{name: "explicit Cube conflict", template: "tpl-explicit", image: "runtime:test", wantErr: true},
		{name: "empty Docker template", template: "docker:", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template, image, err := normalizeLifecycleDockerSelection(tt.template, tt.image)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantTemplate, template)
			require.Equal(t, tt.wantImage, image)
		})
	}
}

func TestPickSandboxNodeUsesFreshExactDockerInventory(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	now := time.Now().UTC()
	const targetImage = "runtime:test"
	metadata := func(image, syncedAt, inventoryErr string) []byte {
		raw, err := json.Marshal(map[string]any{
			"docker_images":           []any{map[string]any{"image_ref": image}},
			"docker_images_synced_at": syncedAt,
			"docker_images_error":     inventoryErr,
		})
		require.NoError(t, err)
		return raw
	}
	inventories := [][]byte{
		metadata("runtime:other", now.Format(time.RFC3339), ""),
		metadata(targetImage, now.Format(time.RFC3339), "inventory unavailable"),
		metadata(targetImage, now.Add(-sandboxDockerInventoryMaxAge-time.Minute).Format(time.RFC3339), ""),
		metadata(targetImage, now.Format(time.RFC3339), ""),
	}
	nodeIDs := make([]string, 0, len(inventories))
	for i, inventory := range inventories {
		var nodeID string
		require.NoError(t, testPool.QueryRow(ctx, `
			INSERT INTO sandbox_node (node_key, name, owner_user_id, status, capabilities, max_concurrency, metadata, last_seen_at)
			VALUES ($1, $2, $3, 'online', '{}'::jsonb, 1, $4, now())
			RETURNING id`, "inventory-node-"+uuid.NewString(), "inventory node", testUserID, inventory).Scan(&nodeID))
		nodeIDs = append(nodeIDs, nodeID)
		_, err := testPool.Exec(ctx, `
			INSERT INTO sandbox_workspace_binding (workspace_id, node_id, enabled, policy, created_by, created_at)
			VALUES ($1, $2, true, '{}'::jsonb, $3, $4)`, testWorkspaceID, nodeID, testUserID, now.Add(time.Duration(i)*time.Second))
		require.NoError(t, err)
	}
	t.Cleanup(func() {
		for _, nodeID := range nodeIDs {
			testPool.Exec(context.Background(), `DELETE FROM sandbox_workspace_binding WHERE workspace_id = $1 AND node_id = $2`, testWorkspaceID, nodeID)
			testPool.Exec(context.Background(), `DELETE FROM sandbox_node WHERE id = $1`, nodeID)
		}
	})

	adapter := &envSandboxLifecycleDepsAdapter{h: testHandler}
	selected, err := adapter.pickSandboxNode(ctx, parseUUID(testWorkspaceID), "", targetImage)
	require.NoError(t, err)
	require.Equal(t, nodeIDs[3], uuidToString(selected.ID), "must skip mismatch, errored, and stale inventories")

	_, err = adapter.pickSandboxNode(ctx, parseUUID(testWorkspaceID), nodeIDs[0], targetImage)
	require.ErrorContains(t, err, "not advertised by the selected node")
	_, err = adapter.pickSandboxNode(ctx, parseUUID(testWorkspaceID), nodeIDs[2], targetImage)
	require.ErrorContains(t, err, "not advertised by the selected node")
}

func TestEphemeralSandboxCleanerEnqueuesDeleteWithSandboxCreatorAsInitiator(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	var nodeID, instanceID string
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO sandbox_node (node_key, name, owner_user_id, capabilities, max_concurrency, metadata)
		VALUES ($1, 'ephemeral cleaner node', $2, '{}'::jsonb, 1, '{}'::jsonb)
		RETURNING id`, "ephemeral-cleaner-"+uuid.NewString(), testUserID).Scan(&nodeID))
	require.NoError(t, testPool.QueryRow(ctx, `
		INSERT INTO sandbox_instance (workspace_id, creator_user_id, node_id, status, template, limits, metadata)
		VALUES ($1, $2, $3, 'running', 'default', '{}'::jsonb, '{}'::jsonb)
		RETURNING id`, testWorkspaceID, testUserID, nodeID).Scan(&instanceID))
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM sandbox_instance WHERE id = $1`, instanceID)
		testPool.Exec(context.Background(), `DELETE FROM sandbox_node WHERE id = $1`, nodeID)
	})

	cleaner := newEphemeralSandboxCleaner(newEnvSandboxLifecycleService(testHandler))
	// Errors are not asserted: the notify step after the insert may fail with no
	// sandboxd listening. What must hold is that the job was recorded.
	_ = cleaner.DeleteSandboxInstance(ctx, testWorkspaceID, instanceID)

	var initiator string
	err := testPool.QueryRow(ctx, `
		SELECT initiator_user_id FROM sandbox_job
		 WHERE instance_id = $1 AND type = 'delete'`, instanceID).Scan(&initiator)
	require.NoError(t, err, "a delete job must be queued for the sandbox")
	require.Equal(t, testUserID, initiator, "the sandbox creator must own the delete job")
}
