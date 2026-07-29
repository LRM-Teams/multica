package handler

import (
	"context"
	"testing"

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
