package computer

import (
	"errors"
	"fmt"
)

// Binding authorization / idempotency / negative-control rules (#2489, #2493).
// These are pure decision functions shared by service-side and local setup so
// the "what may be bound" contract is single and unit-testable without a
// database. Cross-user ownership is enforced server-side (the local Store has
// no owner column and identity is never derived from a slug).

var (
	// ErrBindingUnauthorized is returned when the actor is not allowed to
	// create or manage the requested binding.
	ErrBindingUnauthorized = errors.New("binding unauthorized")
)

// BindingRequest is whom+what a binding creation/validation targets.
type BindingRequest struct {
	ActorUserID      string
	TargetComputerID string
	TargetWorkspaceID string
}

// ValidationKind distinguishes an idempotent repair from a fresh create.
type ValidationKind int

const (
	// ValidationKindUnknown is the zero/unset value.
	ValidationKindUnknown ValidationKind = iota
	// ValidationKindCreate is a new Binding.
	ValidationKindCreate
	// ValidationKindRepair is re-establishing an existing Binding (idempotent).
	ValidationKindRepair
)

// ValidateCreate enforces the authorization and idempotency rules for
// establishing a Binding: actor/Computer/Workspace ids must all be present,
// and an existing active Binding for the same (Computer, Workspace) is an
// idempotent repair (#2490), never a duplicate. It fails closed on a reason
// that is not a valid create or repair.
func ValidateCreate(req BindingRequest, current []WorkspaceBinding) (ValidationKind, error) {
	if req.ActorUserID == "" {
		return ValidationKindUnknown, errors.New("binding request requires an actor identity")
	}
	if req.TargetComputerID == "" {
		return ValidationKindUnknown, errors.New("binding request requires a Computer identity")
	}
	if req.TargetWorkspaceID == "" {
		return ValidationKindUnknown, errors.New("binding request requires an immutable workspace id (never a slug)")
	}
	for _, b := range current {
		if b.WorkspaceID == req.TargetWorkspaceID {
			if !b.Active {
				// An explicitly revoked binding must be re-created, not treated
				// as a quiet repair.
				return ValidationKindCreate, nil
			}
			return ValidationKindRepair, nil
		}
	}
	return ValidationKindCreate, nil
}

// ValidateRemove enforces #2493's removal fail-closed contract: an actor may
// remove only a Binding that currently exists for a Workspace, and only with
// an explicit actor identity (cross-user removal is denied server-side).
func ValidateRemove(req BindingRequest, current []WorkspaceBinding) error {
	if req.ActorUserID == "" {
		return fmt.Errorf("%w: removal requires the actor identity", ErrBindingUnauthorized)
	}
	if req.TargetComputerID == "" {
		return fmt.Errorf("%w: removal requires the Computer identity", ErrBindingUnauthorized)
	}
	if req.TargetWorkspaceID == "" {
		return fmt.Errorf("%w: removal requires the immutable workspace id", ErrBindingUnauthorized)
	}
	for _, b := range current {
		if b.WorkspaceID == req.TargetWorkspaceID {
			return nil // target + actor + workspace present → allowed to remove
		}
	}
	return fmt.Errorf("%w: no binding for workspace %s", ErrBindingUnauthorized, req.TargetWorkspaceID)
}
