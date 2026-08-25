package computer

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestValidateSuccessorPIDVersionMatchesPIDAndVersion(t *testing.T) {
	pid := os.Getpid()
	want := SuccessorPIDVersion{
		ServicePID: pid, SourceServicePID: 11, ComputerVersion: "v10.0.0",
		AcceptedManagedWorkspaceIDs: []string{"workspace-a"}, AcceptedManagedSetRevision: managedSetRevision([]string{"workspace-a"}),
	}
	got := MachineAttestation{
		ComputerVersion: "10.0.0", ServiceGeneration: "gen-1", ServicePID: pid, SourceServicePID: 11,
		ManagedWorkspaceIDs: []string{"workspace-a"}, ManagedSetRevision: managedSetRevision([]string{"workspace-a"}),
	}
	if err := ValidateSuccessorPIDVersion(want, got); err != nil {
		t.Fatal(err)
	}
	wrongPID := got
	wrongPID.ServicePID = pid + 1
	if err := ValidateSuccessorPIDVersion(want, wrongPID); err == nil {
		t.Fatal("mismatched pid must fail")
	}
	wrongVersion := got
	wrongVersion.ComputerVersion = "v9.9.9"
	if err := ValidateSuccessorPIDVersion(want, wrongVersion); err == nil {
		t.Fatal("mismatched version must fail")
	}
	emptyGeneration := got
	emptyGeneration.ServiceGeneration = ""
	if err := ValidateSuccessorPIDVersion(want, emptyGeneration); err == nil {
		t.Fatal("empty service generation must fail")
	}
	wrongSource := got
	wrongSource.SourceServicePID++
	if err := ValidateSuccessorPIDVersion(want, wrongSource); err == nil {
		t.Fatal("mismatched source service pid must fail")
	}
	wrongSet := got
	wrongSet.ManagedWorkspaceIDs = []string{"workspace-b"}
	wrongSet.ManagedSetRevision = managedSetRevision([]string{"workspace-b"})
	if err := ValidateSuccessorPIDVersion(want, wrongSet); err == nil {
		t.Fatal("mismatched managed set must fail")
	}
}

func TestMachineAttestationJSONUsesCamelCaseIdentity(t *testing.T) {
	raw, err := json.Marshal(MachineAttestation{
		ComputerVersion: "v1.0.0", ServiceGeneration: "service-1", ServicePID: 101,
		SourceServicePID: 100, ManagedWorkspaceIDs: []string{"workspace-a"}, ManagedSetRevision: "revision-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, field := range []string{"computerVersion", "serviceGeneration", "servicePid", "sourceServicePid", "managedWorkspaceIds", "managedSetRevision"} {
		if !strings.Contains(text, `"`+field+`"`) {
			t.Fatalf("machine attestation missing %q: %s", field, text)
		}
	}
	if strings.Contains(text, "_") {
		t.Fatalf("machine attestation retained snake_case fields: %s", text)
	}
}
