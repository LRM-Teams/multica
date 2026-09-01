// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
)

// bindChannelProject routes a test channel bind through the Task 16
// binding service. Migration 470's guard trigger rejects every other
// UPDATE of channel.project_id, including the ones tests used to issue
// directly, so tests must go through the same single production writer.
func bindChannelProject(t *testing.T, ctx context.Context, channelID, projectID pgtype.UUID) {
	t.Helper()
	bindings := service.NewChannelProjectBindingService(testPool)
	_, err := bindings.SetChannelProject(ctx, service.ChannelProjectBindingParams{
		WorkspaceID:  graphMemoryChannelWorkspace(t, ctx, channelID),
		ChannelID:    channelID,
		NewProjectID: projectID,
		Actor:        "test:binding",
	})
	require.NoError(t, err)
}

// bindChannelProjectStrings is the string-id form of bindChannelProject.
func bindChannelProjectStrings(t *testing.T, ctx context.Context, channelID, projectID string) {
	t.Helper()
	bindChannelProject(t, ctx, util.MustParseUUID(channelID), util.MustParseUUID(projectID))
}

func graphMemoryChannelWorkspace(t *testing.T, ctx context.Context, channelID pgtype.UUID) pgtype.UUID {
	t.Helper()
	var workspaceID string
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT workspace_id FROM channel WHERE id = $1`, channelID).Scan(&workspaceID))
	return util.MustParseUUID(workspaceID)
}
