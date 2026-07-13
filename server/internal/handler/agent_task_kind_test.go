package handler

import (
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestComputeTaskKindRecognizesAgentRadarContext(t *testing.T) {
	task := db.AgentTaskQueue{
		Context: []byte(`{"type":"agent_radar","radar_run_id":"radar-run-1"}`),
	}

	if got := computeTaskKind(task); got != "agent_radar" {
		t.Fatalf("computeTaskKind() = %q, want agent_radar", got)
	}
}
