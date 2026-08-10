package computer

import (
	"fmt"
	"time"
)

// BindingStore is the minimal persistence seam the service layer needs. The
// real implementation is a sqlc-backed adapter against the
// computer_workspace_bindings table (migration 307); tests substitute an
// in-memory fake so the authorization/idempotency/negative-control contract is
// verified without a database.
type BindingStore interface {
	ListScoped(computerID, userID string) ([]WorkspaceBinding, error)
	UpsertScoped(BindingRequest, WorkspaceBinding) error
	RevokeScoped(BindingRequest) ([]string, error)
}

// BindingService implements the server-side creation/repair/revocation
// contract for Workspace Bindings, scoped to the signed-in user + Computer +
// Workspace, with a revocable execution credential and fail-closed negative
// controls (#2489, #2493).
type BindingService struct {
	Store BindingStore
}

// CreateResult describes the outcome of a binding create/repair.
type CreateResult struct {
	Binding WorkspaceBinding
	Kind    ValidationKind
}

// Create validates (authorization + idempotency) then persists a Binding. A
// repeat for the same (Computer, Workspace) by the same actor is a repair, not
// a duplicate. A revoked binding is re-created fresh (no silent quiet-repair).
func (s *BindingService) Create(req BindingRequest, b WorkspaceBinding) (CreateResult, error) {
	cur, err := s.Store.ListScoped(req.TargetComputerID, "")
	if err != nil {
		return CreateResult{}, err
	}
	kind, err := ValidateCreate(req, cur)
	if err != nil {
		return CreateResult{}, err
	}
	if b.WorkspaceID == "" || b.WorkspaceID != req.TargetWorkspaceID {
		return CreateResult{}, fmt.Errorf("binding workspace_id must equal the immutable request target")
	}
	b.Active = true
	b.ComputerID = req.TargetComputerID
	b.UserID = req.ActorUserID
	b.AcceptedAt = time.Now().UTC()
	if err := s.Store.UpsertScoped(req, b); err != nil {
		return CreateResult{}, err
	}
	return CreateResult{Binding: b, Kind: kind}, nil
}

// Revoke revokes exactly one Binding by immutable workspace_id, preserving every
// sibling and all local Agent data (#2493). Returns ErrBindingUnauthorized on
// fail-closed paths.
func (s *BindingService) Revoke(req BindingRequest, workspaceID string) ([]string, error) {
	cur, err := s.Store.ListScoped(req.TargetComputerID, "")
	if err != nil {
		return nil, err
	}
	if err := ValidateRemove(req, cur); err != nil {
		return nil, err
	}
	for _, binding := range cur {
		if binding.WorkspaceID == req.TargetWorkspaceID {
			req.BindingOwnerUserID = binding.UserID
			break
		}
	}
	revokedTokenHashes, err := s.Store.RevokeScoped(req)
	if err != nil {
		return nil, err
	}
	return revokedTokenHashes, nil
}

// All returns the current active bindings.
func (s *BindingService) All(computerID, userID string) ([]WorkspaceBinding, error) {
	return s.Store.ListScoped(computerID, userID)
}
