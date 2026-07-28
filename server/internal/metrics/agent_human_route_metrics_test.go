package metrics

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/middleware"
)

func TestAgentHumanRouteHitsRegisteredOnAppRegistry(t *testing.T) {
	reg := NewRegistry(RegistryOptions{Version: "test", Commit: "deadbeef"})
	mfs, err := reg.Gatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	sampleCount := 0
	for _, mf := range mfs {
		if mf.GetName() != "multica_agent_surface_human_route_hits_total" {
			continue
		}
		found = true
		sampleCount = len(mf.GetMetric())
	}
	if !found {
		t.Fatal("multica_agent_surface_human_route_hits_total missing from app registry (would be NO_DATA on scrape)")
	}
	// Seeded known sites from middleware.RegisterAgentHumanRouteMetrics
	if sampleCount < 7 {
		t.Fatalf("want >=7 seeded site series, got %d", sampleCount)
	}
	middleware.RecordAgentHumanRouteHit("RejectAgentOnHumanAPI")
	mfs, err = reg.Gatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var rejectVal float64
	for _, mf := range mfs {
		if mf.GetName() != "multica_agent_surface_human_route_hits_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "site" && lp.GetValue() == "RejectAgentOnHumanAPI" {
					rejectVal = m.GetCounter().GetValue()
				}
			}
		}
	}
	if rejectVal < 1 {
		t.Fatalf("RejectAgentOnHumanAPI counter = %v, want >= 1 after Inc", rejectVal)
	}
}
