package handler

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
)

func TestAgentFleetRankResponseIncludesConfiguredMinimumSample(t *testing.T) {
	response := toAgentFleetRankResponse(service.AgentFleetRankView{
		SampleTasks:    4,
		MinSampleTasks: 12,
	})

	if response.MinSampleTasks != 12 {
		t.Fatalf("minimum sample tasks = %d, want 12", response.MinSampleTasks)
	}
}
