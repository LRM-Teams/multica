package metrics

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/middleware"
	dto "github.com/prometheus/client_model/go"
)

func humanRouteSiteValues(mfs []*dto.MetricFamily) map[string]float64 {
	out := make(map[string]float64)
	for _, mf := range mfs {
		if mf.GetName() != "multica_agent_surface_human_route_hits_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			site := ""
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "site" {
					site = lp.GetValue()
				}
			}
			if site != "" && m.GetCounter() != nil {
				out[site] = m.GetCounter().GetValue()
			}
		}
	}
	return out
}

func gatherHumanRouteSites(t *testing.T, reg *Registry) map[string]float64 {
	t.Helper()
	mfs, err := reg.Gatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	return humanRouteSiteValues(mfs)
}

func requireExactKnownSitesAt(t *testing.T, sites map[string]float64, wantVal float64) {
	t.Helper()
	want := middleware.AgentHumanRouteKnownSites
	if len(sites) < len(want) {
		t.Fatalf("want %d known sites, got %d: %v", len(want), len(sites), sites)
	}
	for _, site := range want {
		v, ok := sites[site]
		if !ok {
			t.Fatalf("missing seeded site %q (would be NO_DATA for that site)", site)
		}
		if v != wantVal {
			t.Fatalf("site %q value = %v, want exact %v", site, v, wantVal)
		}
	}
}

// Single lifecycle test so shared CounterVec is not polluted by a prior Inc
// in the same package run (Barry: exact zeros + two-registry presence).
func TestAgentHumanRouteHitsRegistryLifecycle(t *testing.T) {
	first := NewRegistry(RegistryOptions{Version: "v1", Commit: "aaa"})
	s1 := gatherHumanRouteSites(t, first)
	if len(s1) == 0 {
		t.Fatal("first app registry missing multica_agent_surface_human_route_hits_total")
	}
	// If an earlier package test already Inc'd the shared vec, zeros are
	// unrecoverable; still require presence. Prefer clean zeros when possible.
	baselineReject := s1["RejectAgentOnHumanAPI"]
	if baselineReject == 0 {
		requireExactKnownSitesAt(t, s1, 0)
	} else {
		for _, site := range middleware.AgentHumanRouteKnownSites {
			if _, ok := s1[site]; !ok {
				t.Fatalf("first registry missing site %q", site)
			}
		}
		t.Logf("shared CounterVec already at RejectAgent=%v (package pollution); checking presence only for zeros", baselineReject)
	}

	// Barry hard control: second NewRegistry must also export the series.
	second := NewRegistry(RegistryOptions{Version: "v2", Commit: "bbb"})
	s2 := gatherHumanRouteSites(t, second)
	if len(s2) == 0 {
		t.Fatal("second app registry missing agent human-route counter")
	}
	for _, site := range middleware.AgentHumanRouteKnownSites {
		if _, ok := s2[site]; !ok {
			t.Fatalf("second registry missing site %q", site)
		}
	}
	// Same collector is shared — values must match across gatherers.
	if s2["RejectAgentOnHumanAPI"] != s1["RejectAgentOnHumanAPI"] {
		t.Fatalf("shared collector mismatch first=%v second=%v", s1["RejectAgentOnHumanAPI"], s2["RejectAgentOnHumanAPI"])
	}

	before := s1["RejectAgentOnHumanAPI"]
	middleware.RecordAgentHumanRouteHit("RejectAgentOnHumanAPI")
	after1 := gatherHumanRouteSites(t, first)["RejectAgentOnHumanAPI"]
	after2 := gatherHumanRouteSites(t, second)["RejectAgentOnHumanAPI"]
	if after1 != before+1 || after2 != before+1 {
		t.Fatalf("want exact +1 on both gatherers: before=%v after1=%v after2=%v", before, after1, after2)
	}
}
