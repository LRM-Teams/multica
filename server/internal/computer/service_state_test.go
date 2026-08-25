package computer

import (
	"os"
	"testing"
	"time"
)

func TestServiceStateIsSeparateFromServicePID(t *testing.T) {
	root := t.TempDir()
	state := persistedServiceState{ComputerID: "computer-a", ServiceGeneration: "service-4", PID: 1234, StartedAt: time.Now().UTC()}
	if err := writeServiceState(root, state); err != nil {
		t.Fatal(err)
	}
	if serviceStatePath(root) == servicePIDPath(root) {
		t.Fatal("service state and service PID must be separate files")
	}
	if _, err := os.Stat(serviceStatePath(root)); err != nil {
		t.Fatal(err)
	}
}
