package computer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// MachineAttestationPath is the local control method on the existing
// loopback listener. Raft Computer asks the same control socket
// ("machine-attestation"); it does not change service-status. This path is
// that method, not a public HTTP API and not /health.
const MachineAttestationPath = "/machine-attestation"

// MachineAttestation is the live Computer's answer to the local control
// question Raft names machine-attestation: who is running, which version,
// which Bindings, and which process it replaced.
type MachineAttestation struct {
	ComputerVersion     string   `json:"computer_version"`
	ServiceGeneration   string   `json:"service_generation"`
	ComputerGeneration  int64    `json:"computer_generation,omitempty"`
	ServicePID          int      `json:"service_pid"`
	SourceServicePID    int      `json:"source_service_pid,omitempty"`
	ManagedWorkspaceIDs []string `json:"managed_workspace_ids"`
	ManagedSetRevision  string   `json:"managed_set_revision"`
}

// ProbeMachineAttestation asks the resident on the existing control port.
func ProbeMachineAttestation(ctx context.Context, port int) (MachineAttestation, error) {
	addr := fmt.Sprintf("http://127.0.0.1:%d%s", port, MachineAttestationPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
	if err != nil {
		return MachineAttestation{}, err
	}
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return MachineAttestation{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return MachineAttestation{}, fmt.Errorf("machine-attestation control returned %s", resp.Status)
	}
	var attestation MachineAttestation
	if err := json.NewDecoder(resp.Body).Decode(&attestation); err != nil {
		return MachineAttestation{}, err
	}
	return attestation, nil
}

// SuccessorPIDVersion is the PID+version the launcher requires of the child
// it spawned. Source-dead is not a gate: the waiter is still the incumbent.
type SuccessorPIDVersion struct {
	ServicePID      int
	ComputerVersion string
}

// ValidateSuccessorPIDVersion accepts only when the control answer's PID is
// the spawned child and its version matches the target release.
func ValidateSuccessorPIDVersion(want SuccessorPIDVersion, got MachineAttestation) error {
	if got.ServicePID <= 0 || got.ServicePID != want.ServicePID {
		return fmt.Errorf("service pid %d does not match child %d", got.ServicePID, want.ServicePID)
	}
	if !sameComputerVersion(want.ComputerVersion, got.ComputerVersion) {
		return fmt.Errorf("computer version %q does not match target %q", got.ComputerVersion, want.ComputerVersion)
	}
	return nil
}

func sameComputerVersion(want, got string) bool {
	normalize := func(v string) string {
		return strings.TrimPrefix(strings.TrimSpace(v), "v")
	}
	want, got = normalize(want), normalize(got)
	return want != "" && want == got
}
