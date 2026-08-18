package computer

import (
	"os"
	"testing"
)

func TestValidateSuccessorPIDVersionMatchesPIDAndVersion(t *testing.T) {
	pid := os.Getpid()
	want := SuccessorPIDVersion{ServicePID: pid, ComputerVersion: "v10.0.0"}
	got := MachineAttestation{ComputerVersion: "10.0.0", ServiceGeneration: "gen-1", ServicePID: pid, SourceServicePID: 11}
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
}
