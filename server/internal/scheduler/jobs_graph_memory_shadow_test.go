package scheduler

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The Task 21 canary sweep runs once per workspace, not once per graph dir:
// the targets are deduplicated and derived only from canonical layouts whose
// path actually encodes a workspace id.
func TestShadowGateSweepTargetsDedupeWorkspaces(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "workspaces")
	dirs := []string{
		filepath.Join(root, "11111111-1111-1111-1111-111111111111", "memory_graph", "channels", "22222222-2222-2222-2222-222222222222"),
		filepath.Join(root, "11111111-1111-1111-1111-111111111111", "memory_graph", "projects", "33333333-3333-3333-3333-333333333333"),
		filepath.Join(root, "44444444-4444-4444-4444-444444444444", "memory_graph", "projects", "33333333-3333-3333-3333-333333333333"),
		// Non-canonical layouts contribute nothing.
		filepath.Join(root, "legacy-root-level"),
		filepath.Join(root, "not-a-uuid", "memory_graph", "projects", "x"),
	}

	targets := shadowGateSweepTargets(dirs)
	require.Len(t, targets, 2)
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", targets[0])
	assert.Equal(t, "44444444-4444-4444-4444-444444444444", targets[1])

	assert.Empty(t, shadowGateSweepTargets(nil))
}
