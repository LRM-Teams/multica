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

// requireExactKnownSitesZero is unconditional — no package-pollution soft-pass
// (Barry/Vera hard-gate: 7 sites exact 0, never baseline≠0 branch).
func requireExactKnownSitesZero(t *testing.T, sites map[string]float64) {
	t.Helper()
	want := middleware.AgentHumanRouteKnownSites
	if len(sites) != len(want) {
		// Allow only the exact known set (no extras required; unknown site ok if present)
		for _, site := range want {
			if _, ok := sites[site]; !ok {
				t.Fatalf("missing seeded site %q among %v", site, sites)
			}
		}
	}
	for _, site := range want {
		v, ok := sites[site]
		if !ok {
			t.Fatalf("missing seeded site %q (would be NO_DATA for that site)", site)
		}
		if v != 0 {
			t.Fatalf("site %q initial value = %v, want exact 0 (no pollution soft-pass)", site, v)
		}
	}
}

// Per-registry CounterVecs: each NewRegistry starts at exact 0; Inc fans out.
func TestAgentHumanRouteHitsRegistryLifecycle(t *testing.T) {
	first := NewRegistry(RegistryOptions{Version: "v1", Commit: "aaa"})
	s1 := gatherHumanRouteSites(t, first)
	if len(s1) == 0 {
		t.Fatal("first app registry missing multica_agent_surface_human_route_hits_total")
	}
	requireExactKnownSitesZero(t, s1)

	second := NewRegistry(RegistryOptions{Version: "v2", Commit: "bbb"})
	s2 := gatherHumanRouteSites(t, second)
	if len(s2) == 0 {
		t.Fatal("second app registry missing agent human-route counter")
	}
	// Unconditional zeros on second registry too (own CounterVec).
	requireExactKnownSitesZero(t, s2)

	middleware.RecordAgentHumanRouteHit("RejectAgentOnHumanAPI")
	after1 := gatherHumanRouteSites(t, first)["RejectAgentOnHumanAPI"]
	after2 := gatherHumanRouteSites(t, second)["RejectAgentOnHumanAPI"]
	if after1 != 1 || after2 != 1 {
		t.Fatalf("want exact 0→1 on both gatherers: after1=%v after2=%v", after1, after2)
	}
	// Other sites remain 0 on both.
	for _, site := range middleware.AgentHumanRouteKnownSites {
		if site == "RejectAgentOnHumanAPI" {
			continue
		}
		if gatherHumanRouteSites(t, first)[site] != 0 || gatherHumanRouteSites(t, second)[site] != 0 {
			t.Fatalf("site %q must stay 0 after unrelated Inc", site)
		}
	}
}
