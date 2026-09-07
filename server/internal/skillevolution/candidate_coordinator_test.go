// SPDX-License-Identifier: Apache-2.0

package skillevolution

// CandidateCoordinator over an in-memory CandidateStore: admission is
// needs_review-only, replay is a no-op, transitions validate the state
// machine before the CAS, and terminal candidates never revive.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCandidateStore struct {
	records map[string]SkillCandidateRecord
}

func newFakeCandidateStore() *fakeCandidateStore {
	return &fakeCandidateStore{records: map[string]SkillCandidateRecord{}}
}

func (f *fakeCandidateStore) InsertCandidate(_ context.Context, record SkillCandidateRecord) error {
	key := record.WorkspaceID + ":" + record.CandidateID
	if existing, ok := f.records[key]; ok {
		if existing.Status != record.Status {
			return ErrLedgerConflict
		}
		return nil // identical replay
	}
	f.records[key] = record
	return nil
}

func (f *fakeCandidateStore) GetCandidate(_ context.Context, workspaceID, candidateID string) (SkillCandidateRecord, error) {
	record, ok := f.records[workspaceID+":"+candidateID]
	if !ok {
		return SkillCandidateRecord{}, ErrLedgerNotFound
	}
	return record, nil
}

func (f *fakeCandidateStore) TransitionCandidateStatus(_ context.Context, workspaceID, candidateID string, from, to CandidateStatus) error {
	key := workspaceID + ":" + candidateID
	record, ok := f.records[key]
	if !ok {
		return ErrLedgerNotFound
	}
	if record.Status != from {
		return ErrLedgerConflict
	}
	record.Status = to
	f.records[key] = record
	return nil
}

func TestCandidateCoordinatorAdmitsNeedsReviewOnly(t *testing.T) {
	coordinator := NewCandidateCoordinator(newFakeCandidateStore())
	candidate := validCandidateRecord()

	admitted, err := coordinator.AdmitCandidate(context.Background(), candidate)
	require.NoError(t, err)
	assert.Equal(t, CandidateStatusNeedsReview, admitted.Status)

	// Identical replay resolves to the stored record.
	replayed, err := coordinator.AdmitCandidate(context.Background(), candidate)
	require.NoError(t, err)
	assert.Equal(t, admitted.CandidateID, replayed.CandidateID)

	// Any later lifecycle state is the evaluation/approval planes' call —
	// never admission's.
	premature := validCandidateRecord()
	premature.Status = CandidateStatusAccepted
	_, err = coordinator.AdmitCandidate(context.Background(), premature)
	assert.ErrorIs(t, err, ErrInvalidContract)

	// Shape failures surface the contract error.
	broken := validCandidateRecord()
	broken.RequestedScope = "org"
	_, err = coordinator.AdmitCandidate(context.Background(), broken)
	assert.ErrorIs(t, err, ErrInvalidContract)
}

func TestCandidateCoordinatorTransitionsValidateTheMachine(t *testing.T) {
	coordinator := NewCandidateCoordinator(newFakeCandidateStore())
	candidate := validCandidateRecord()
	admitted, err := coordinator.AdmitCandidate(context.Background(), candidate)
	require.NoError(t, err)

	// Same-status call is an idempotent no-op.
	same, err := coordinator.TransitionCandidate(context.Background(), admitted.WorkspaceID, admitted.CandidateID, CandidateStatusNeedsReview)
	require.NoError(t, err)
	assert.Equal(t, CandidateStatusNeedsReview, same.Status)

	// Legal edge.
	shadowed, err := coordinator.TransitionCandidate(context.Background(), admitted.WorkspaceID, admitted.CandidateID, CandidateStatusShadow)
	require.NoError(t, err)
	assert.Equal(t, CandidateStatusShadow, shadowed.Status)

	// Illegal jump (shadow -> accepted skips evaluating).
	_, err = coordinator.TransitionCandidate(context.Background(), admitted.WorkspaceID, admitted.CandidateID, CandidateStatusAccepted)
	assert.ErrorIs(t, err, ErrLedgerConflict)

	// Terminal stability: rejected is terminal; nothing revives it.
	_, err = coordinator.TransitionCandidate(context.Background(), admitted.WorkspaceID, admitted.CandidateID, CandidateStatusRejected)
	require.NoError(t, err)
	for _, next := range []CandidateStatus{CandidateStatusNeedsReview, CandidateStatusEvaluating, CandidateStatusAccepted, CandidateStatusSuperseded} {
		_, err = coordinator.TransitionCandidate(context.Background(), admitted.WorkspaceID, admitted.CandidateID, next)
		assert.ErrorIs(t, err, ErrLedgerConflict, "rejected refuses %s", next)
	}

	// Unknown ids stay NotFound.
	_, err = coordinator.TransitionCandidate(context.Background(), admitted.WorkspaceID, "candidate-missing", CandidateStatusShadow)
	assert.ErrorIs(t, err, ErrLedgerNotFound)
}
