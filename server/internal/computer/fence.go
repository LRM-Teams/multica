package computer

import (
	"errors"
	"fmt"
)

// Upgrade generation fencing (#2494). Before an incumbent is released, the
// successor must attest the expected version, a stable Computer Identity, the
// current lifecycle/connection generation, and the complete managed Binding
// set. A stale resident (old generation) must never be allowed to refresh
// connectivity or accept duplicate work.

// SuccessorAttestation is what a candidate successor reports before a release.
type SuccessorAttestation struct {
	Version      string
	ComputerID   string
	Generation   int64
	BindingSet   []WorkspaceBinding
	ExpectedGen  int64
	ExpectedVer  string
	ExpectedID   string
	ExpectedSet  map[string]bool // immutable workspace_ids the incumbent manages
}

// ErrStaleGeneration / ErrFenceDenied classify a rejected handoff.
var (
	ErrStaleGeneration = errors.New("stale generation")
	ErrFenceDenied     = errors.New("fence denied")
)

// ReleaseIncumbent returns nil only when the successor proves ownership of the
// complete Computer: correct expected version, stable Identity, current
// generation, and a Binding set equal to the expected managed set. Any
// mismatch or missing Binding preserves the incumbent and records an
// actionable failure (returned error).
func ReleaseIncumbent(a SuccessorAttestation) error {
	if a.Version != a.ExpectedVer {
		return fmt.Errorf("%w: successor version %q != expected %q (incumbent preserved)", ErrFenceDenied, a.Version, a.ExpectedVer)
	}
	if a.ComputerID == "" || a.ComputerID != a.ExpectedID {
		return fmt.Errorf("%w: successor identity %q != expected stable identity %q (incumbent preserved)", ErrFenceDenied, a.ComputerID, a.ExpectedID)
	}
	if a.Generation < a.ExpectedGen {
		return fmt.Errorf("%w: %v — successor is a stale resident on generation %d (expected >= %d); it must not refresh connectivity or accept work", ErrStaleGeneration, ErrStaleGeneration, a.Generation, a.ExpectedGen)
	}
	if !bindingSetComplete(a.BindingSet, a.ExpectedSet) {
		return fmt.Errorf("%w: successor is missing a managed Binding and must not be released", ErrFenceDenied)
	}
	return nil
}

// bindingSetComplete is true when the successor's bindings exactly cover the
// expected managed workspace set (no missing and no extra scope drift).
func bindingSetComplete(got []WorkspaceBinding, expected map[string]bool) bool {
	if len(got) != len(expected) {
		return false
	}
	for _, b := range got {
		if !b.Active || !expected[b.WorkspaceID] {
			return false
		}
	}
	return true
}
