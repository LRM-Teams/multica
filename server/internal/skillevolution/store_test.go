// SPDX-License-Identifier: Apache-2.0

package skillevolution

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validBackfillCheckpoint() BackfillCheckpoint {
	return BackfillCheckpoint{
		WorkspaceID:     uuid.NewString(),
		JobID:           "backfill-2026-09-02-001",
		Kind:            BackfillTrajectoryEligibility,
		Mode:            BackfillModeDryRun,
		Actor:           "admin:ops",
		PolicyVersion:   "policy-7",
		SourceWatermark: "agent_inbox_event:2026-08-31T00:00:00Z",
		SelectedCount:   12,
		RejectedCount:   3,
		Reason:          "first report-only pass",
		CreatedAt:       time.Now().UTC(),
	}
}

// The backfill report contract stays closed: known kinds, known modes, a
// responsible actor, and non-negative counts (spec §12.12).
func TestBackfillCheckpointContract(t *testing.T) {
	checkpoint := validBackfillCheckpoint()
	require.NoError(t, checkpoint.Validate())

	badKind := validBackfillCheckpoint()
	badKind.Kind = BackfillCheckpointKind("grant_eligibility")
	assert.Error(t, badKind.Validate())

	badMode := validBackfillCheckpoint()
	badMode.Mode = BackfillMode("silent")
	assert.Error(t, badMode.Validate())

	noActor := validBackfillCheckpoint()
	noActor.Actor = ""
	assert.Error(t, noActor.Validate())

	negative := validBackfillCheckpoint()
	negative.RejectedCount = -1
	assert.Error(t, negative.Validate())

	longWatermark := validBackfillCheckpoint()
	longWatermark.SourceWatermark = strings.Repeat("w", 257)
	assert.Error(t, longWatermark.Validate())

	badWorkspace := validBackfillCheckpoint()
	badWorkspace.WorkspaceID = "not\na uuid"
	assert.ErrorIs(t, badWorkspace.Validate(), ErrInvalidContract)

	executed := validBackfillCheckpoint()
	executed.Mode = BackfillModeExecuted
	executed.Kind = BackfillLegacySkillProjection
	require.NoError(t, executed.Validate())
}
