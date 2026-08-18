package computer

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
)

// MachineAttestation is the live Computer's answer to the local control
// question Raft names machine-attestation: who is running, which version,
// which Bindings, and which process it replaced.
type MachineAttestation struct {
	ComputerVersion     string   `json:"computerVersion"`
	ServiceGeneration   string   `json:"serviceGeneration"`
	ServicePID          int      `json:"servicePid"`
	SourceServicePID    int      `json:"sourceServicePid,omitempty"`
	ManagedWorkspaceIDs []string `json:"managedWorkspaceIds"`
	ManagedSetRevision  string   `json:"managedSetRevision"`
}

// SuccessorPIDVersion is the PID+version the launcher requires of the child
// it spawned after the predecessor processes have already exited.
type SuccessorPIDVersion struct {
	ServicePID                  int
	SourceServicePID            int
	ComputerVersion             string
	AcceptedManagedWorkspaceIDs []string
	AcceptedManagedSetRevision  string
}

// ValidateSuccessorPIDVersion accepts only when the control answer's PID is
// the spawned child, its version matches the target release, and the managed
// Binding set matches the journalled predecessor snapshot.
func ValidateSuccessorPIDVersion(want SuccessorPIDVersion, got MachineAttestation) error {
	if got.ServicePID <= 0 || got.ServicePID != want.ServicePID {
		return fmt.Errorf("service pid %d does not match child %d", got.ServicePID, want.ServicePID)
	}
	if !sameComputerVersion(want.ComputerVersion, got.ComputerVersion) {
		return fmt.Errorf("computer version %q does not match target %q", got.ComputerVersion, want.ComputerVersion)
	}
	if strings.TrimSpace(got.ServiceGeneration) == "" {
		return fmt.Errorf("service generation is empty")
	}
	if got.SourceServicePID != want.SourceServicePID {
		return fmt.Errorf("source service pid %d does not match predecessor %d", got.SourceServicePID, want.SourceServicePID)
	}
	if !sameHostStringSet(got.ManagedWorkspaceIDs, want.AcceptedManagedWorkspaceIDs) || got.ManagedSetRevision != want.AcceptedManagedSetRevision {
		return fmt.Errorf("managed runner set has not converged")
	}
	return nil
}

func ProbeMachineAttestation(ctx context.Context, endpoint string) (MachineAttestation, error) {
	var result MachineAttestation
	err := callLocalJSON(ctx, endpoint, LocalControlMachineAttestationOperation, 2*time.Second, nil, nil, &result)
	return result, err
}

func managedSetRevision(workspaceIDs []string) string {
	sum := sha256.Sum256([]byte(strings.Join(normalizedWorkspaceIDs(workspaceIDs), "\n")))
	return fmt.Sprintf("%x", sum[:])
}

func sameComputerVersion(want, got string) bool {
	normalize := func(v string) string {
		return strings.TrimPrefix(strings.TrimSpace(v), "v")
	}
	want, got = normalize(want), normalize(got)
	return want != "" && want == got
}
