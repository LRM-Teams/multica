package researchrun

import (
	"context"
	"fmt"
	"strings"
)

type artifactLifecycleChangeKind string

const (
	artifactLifecycleWithdraw  artifactLifecycleChangeKind = "withdraw"
	artifactLifecycleSupersede artifactLifecycleChangeKind = "supersede"
)

type artifactLifecycleChange struct {
	OperationID         string
	WorkspaceID         string
	SessionID           string
	ArtifactID          string
	Kind                artifactLifecycleChangeKind
	SuccessorArtifactID string
	DecisionID          string
	ActorType           string
	ActorID             string
	Reason              string
}

type artifactLifecycleOutcome struct {
	ArtifactID          string
	Lifecycle           ArtifactLifecycleStatus
	EligibilityRevision int64
	PolicyWatermark     int64
	Replayed            bool
}

type artifactLifecycleStore interface {
	ApplyArtifactLifecycleChange(context.Context, artifactLifecycleChange) (artifactLifecycleOutcome, error)
}

// artifactLifecycleModule owns the only production entry point for withdrawing
// or superseding a passport. Callers name intent; lock order, CAS, reciprocal
// ledger writes, watermark reservation, and replay stay behind this seam.
type artifactLifecycleModule struct {
	store artifactLifecycleStore
}

func (module artifactLifecycleModule) Change(ctx context.Context, change artifactLifecycleChange) (artifactLifecycleOutcome, error) {
	change.OperationID = strings.TrimSpace(change.OperationID)
	change.WorkspaceID = strings.TrimSpace(change.WorkspaceID)
	change.SessionID = strings.TrimSpace(change.SessionID)
	change.ArtifactID = strings.TrimSpace(change.ArtifactID)
	change.SuccessorArtifactID = strings.TrimSpace(change.SuccessorArtifactID)
	change.DecisionID = strings.TrimSpace(change.DecisionID)
	change.ActorType = strings.TrimSpace(change.ActorType)
	change.ActorID = strings.TrimSpace(change.ActorID)
	change.Reason = strings.TrimSpace(change.Reason)
	if change.OperationID == "" || change.WorkspaceID == "" || change.SessionID == "" || change.ArtifactID == "" || change.Reason == "" {
		return artifactLifecycleOutcome{}, fmt.Errorf("%w: incomplete artifact lifecycle change", ErrInvalidContract)
	}
	if change.ActorType == "" {
		change.ActorType = "system"
	}
	switch change.Kind {
	case artifactLifecycleWithdraw:
		if change.SuccessorArtifactID != "" || change.DecisionID != "" {
			return artifactLifecycleOutcome{}, fmt.Errorf("%w: withdrawal cannot name a successor or decision", ErrInvalidContract)
		}
	case artifactLifecycleSupersede:
		if change.SuccessorArtifactID == "" || change.DecisionID == "" || change.SuccessorArtifactID == change.ArtifactID {
			return artifactLifecycleOutcome{}, fmt.Errorf("%w: supersession requires distinct successor and decision", ErrInvalidContract)
		}
	default:
		return artifactLifecycleOutcome{}, fmt.Errorf("%w: unsupported artifact lifecycle change", ErrInvalidContract)
	}
	return module.store.ApplyArtifactLifecycleChange(ctx, change)
}
