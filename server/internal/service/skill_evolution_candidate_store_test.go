// SPDX-License-Identifier: Apache-2.0

package service

// Candidate plane against the faithful schema: admission with the
// immutable contract document, replay/conflict semantics, rebuild-on-read
// where the columns stay authoritative, and CAS transitions under the DB
// terminal guard.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/skillevolution"
)

func candidateRecordFor(f *skillEvolutionLedgerFixture, runID string, patternIDs []string) skillevolution.SkillCandidateRecord {
	return skillevolution.SkillCandidateRecord{
		ContractKind:           "candidate",
		SchemaVersion:          1,
		WorkspaceID:            f.workspaceID,
		RunID:                  runID,
		CandidateID:            "cand-" + uuid.NewString()[:12],
		NewSkillName:           "spreadsheet-hidden-row-export",
		RequestedScope:         "agent",
		BaseArtifactHash:       "sha256:" + repeatRunes('1', 64),
		CandidateArtifactHash:  "sha256:" + repeatRunes('2', 64),
		ProposedDiffHash:       "sha256:" + repeatRunes('3', 64),
		Status:                 skillevolution.CandidateStatusNeedsReview,
		CurrentArtifactVersion: 1,
		MotivatingPatterns:     patternIDs,
		ProposerActor:          "service:skill-proposer",
		ProposerModelID:        "model-x",
		ProposerPolicyVersion:  "proposer-policy-1",
		CreatedAt:              time.Now().UTC(),
	}
}

func repeatRunes(ch byte, n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = ch
	}
	return string(out)
}

func newCandidateFixture(t *testing.T) (*skillEvolutionLedgerFixture, *skillevolution.CandidateCoordinator, string) {
	t.Helper()
	f := newSkillEvolutionLedgerFixture(t)
	runID := uuid.NewString()
	_, err := f.coordinator.StartRun(context.Background(), f.runRecord(runID))
	require.NoError(t, err)
	return f, skillevolution.NewCandidateCoordinator(f.ledger), runID
}

// seedCandidatePatterns lands two real pattern revisions so the
// candidate's motivating-pattern links satisfy the FK.
func seedCandidatePatterns(t *testing.T, f *skillEvolutionLedgerFixture) []string {
	t.Helper()
	ctx := context.Background()
	ids := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		record, err := skillevolution.DraftTentativePattern(skillevolution.PatternDraftInput{
			PatternID:         "pattern-cand-" + uuid.NewString()[:8],
			WorkspaceID:       f.workspaceID,
			EvolutionKey:      "agent-1:spreadsheet:env-3",
			Kind:              skillevolution.PatternKindFailure,
			Problem:           "Sheet export omits hidden rows",
			Applicability:     "spreadsheet export tasks with filtered rows",
			RootCauseSummary:  "export reads the visible range instead of the full row set",
			RecommendedAction: "iterate the full row set before formatting",
			TaskType:          "spreadsheet",
			EnvironmentKey:    "env-3",
			ToolCapabilityID:  "xlsx-writer",
			GeneratorVersion:  "maintainer-1",
			PolicyVersion:     skillevolution.DefaultPatternConsolidationPolicy().PolicyVersion,
			PositiveEvidence: []skillevolution.SkillEvolutionRef{
				{Kind: skillevolution.RefEvaluationRun, ID: uuid.NewString(), WorkspaceID: f.workspaceID},
			},
			CreatedByActor: "maintainer:run-1",
			CreatedAt:      time.Now().UTC(),
		})
		require.NoError(t, err)
		require.NoError(t, f.ledger.InsertPatternRevision(ctx, record))
		ids = append(ids, record.PatternID)
	}
	return ids
}

// Admission persists the contract document verbatim; reads rebuild the
// record with the columns authoritative for everything mutable.
func TestSkillEvolutionCandidateStoreAdmitAndRebuild(t *testing.T) {
	f, coordinator, runID := newCandidateFixture(t)
	patternIDs := seedCandidatePatterns(t, f)
	ctx := context.Background()
	candidate := candidateRecordFor(f, runID, patternIDs)

	admitted, err := coordinator.AdmitCandidate(ctx, candidate)
	require.NoError(t, err)
	assert.Equal(t, skillevolution.CandidateStatusNeedsReview, admitted.Status)
	assert.Equal(t, candidate.CandidateID, admitted.CandidateID)

	// The motivating pattern links landed.
	var links int
	require.NoError(t, f.pool.QueryRow(ctx,
		`SELECT count(*) FROM skill_candidate_pattern WHERE workspace_id=$1::uuid AND candidate_id=$2`,
		f.workspaceID, candidate.CandidateID).Scan(&links))
	assert.Equal(t, 2, links)

	// Round-trip: proposer metadata from the contract, mutable fields
	// from the columns.
	read, err := f.ledger.GetCandidate(ctx, f.workspaceID, candidate.CandidateID)
	require.NoError(t, err)
	assert.Equal(t, candidate.ProposerActor, read.ProposerActor)
	assert.Equal(t, candidate.ProposerModelID, read.ProposerModelID)
	assert.Equal(t, candidate.ProposerPolicyVersion, read.ProposerPolicyVersion)
	assert.Equal(t, candidate.MotivatingPatterns, read.MotivatingPatterns)
	assert.Equal(t, candidate.NewSkillName, read.NewSkillName)
	assert.Equal(t, skillevolution.CandidateStatusNeedsReview, read.Status)
}

// The same candidate id replaying the identical contract is a no-op; a
// different contract under the same id is a conflict, never an
// overwrite.
func TestSkillEvolutionCandidateStoreReplayAndConflict(t *testing.T) {
	f, coordinator, runID := newCandidateFixture(t)
	patternIDs := seedCandidatePatterns(t, f)
	ctx := context.Background()
	candidate := candidateRecordFor(f, runID, patternIDs)
	_, err := coordinator.AdmitCandidate(ctx, candidate)
	require.NoError(t, err)

	same, err := coordinator.AdmitCandidate(ctx, candidate)
	require.NoError(t, err)
	assert.Equal(t, candidate.CandidateID, same.CandidateID)

	different := candidate
	different.BaseArtifactHash = "sha256:" + repeatRunes('9', 64)
	_, err = coordinator.AdmitCandidate(ctx, different)
	assert.ErrorIs(t, err, skillevolution.ErrLedgerConflict)

	// The row still carries the original contract.
	read, err := f.ledger.GetCandidate(ctx, f.workspaceID, candidate.CandidateID)
	require.NoError(t, err)
	assert.Equal(t, candidate.BaseArtifactHash, read.BaseArtifactHash)

	// Admission gates the lifecycle: only needs_review enters.
	premature := candidateRecordFor(f, runID, patternIDs)
	premature.CandidateID = "cand-" + uuid.NewString()[:12]
	premature.Status = skillevolution.CandidateStatusAccepted
	_, err = coordinator.AdmitCandidate(ctx, premature)
	assert.ErrorIs(t, err, skillevolution.ErrInvalidContract)
}

// Transitions CAS against the stored status; illegal edges fail closed
// and terminal candidates stay put at the database level too.
func TestSkillEvolutionCandidateStoreTransitionsAndTerminal(t *testing.T) {
	f, coordinator, runID := newCandidateFixture(t)
	patternIDs := seedCandidatePatterns(t, f)
	ctx := context.Background()
	candidate := candidateRecordFor(f, runID, patternIDs)
	admitted, err := coordinator.AdmitCandidate(ctx, candidate)
	require.NoError(t, err)

	// A stale from-status is a conflict at the store floor.
	err = f.ledger.TransitionCandidateStatus(ctx, f.workspaceID, admitted.CandidateID,
		skillevolution.CandidateStatusShadow, skillevolution.CandidateStatusEvaluating)
	assert.ErrorIs(t, err, skillevolution.ErrLedgerConflict)

	// Legal path: needs_review -> shadow -> evaluating -> rejected.
	for _, next := range []skillevolution.CandidateStatus{
		skillevolution.CandidateStatusShadow,
		skillevolution.CandidateStatusEvaluating,
		skillevolution.CandidateStatusRejected,
	} {
		moved, err := coordinator.TransitionCandidate(ctx, f.workspaceID, admitted.CandidateID, next)
		require.NoError(t, err)
		assert.Equal(t, next, moved.Status)
	}

	// Terminal at the DB: even a direct SQL revival attempt fails.
	_, err = f.pool.Exec(ctx,
		`UPDATE skill_candidate SET status='needs_review' WHERE workspace_id=$1::uuid AND candidate_id=$2`,
		f.workspaceID, admitted.CandidateID)
	require.Error(t, err, "the terminal guard refuses revival")
	_, err = coordinator.TransitionCandidate(ctx, f.workspaceID, admitted.CandidateID, skillevolution.CandidateStatusAccepted)
	assert.ErrorIs(t, err, skillevolution.ErrLedgerConflict)

	// Unknown ids stay NotFound.
	_, err = f.ledger.GetCandidate(ctx, f.workspaceID, "cand-missing")
	assert.ErrorIs(t, err, skillevolution.ErrLedgerNotFound)
}
