package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestAgentPrincipalRoundTrip(t *testing.T) {
	p := AgentPrincipal{
		AgentID:     "11111111-1111-1111-1111-111111111111",
		WorkspaceID: "22222222-2222-2222-2222-222222222222",
		OwnerUserID: "33333333-3333-3333-3333-333333333333",
		ActorSource: "agent_credential",
	}
	ctx := WithAgentPrincipal(context.Background(), p)
	got, ok := AgentPrincipalFromContext(ctx)
	if !ok {
		t.Fatal("expected agent principal")
	}
	if got.AgentID != p.AgentID || got.WorkspaceID != p.WorkspaceID || got.ActorSource != p.ActorSource {
		t.Fatalf("got %+v want %+v", got, p)
	}
	if _, ok := HumanPrincipalFromContext(ctx); ok {
		t.Fatal("did not expect human principal")
	}
}

func TestRequireAgentPrincipalRejectsHuman(t *testing.T) {
	h := RequireAgentPrincipal(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/agent/channels", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/agent/channels", nil)
	ctx := WithAgentPrincipal(req.Context(), AgentPrincipal{
		AgentID: "11111111-1111-1111-1111-111111111111", WorkspaceID: "22222222-2222-2222-2222-222222222222", ActorSource: "agent_credential",
	})
	h.ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204", rec.Code)
	}
}

// #801 completion criterion ④ (Parker): admin / platform-structure surfaces
// must 403 when AgentPrincipal hits human routes. RejectAgentOnHumanAPI is
// the fail-closed gate. These contracts must distinguish:
//   - issue labels on /api/agent/issues/{id}/labels  → ALLOW (necessary work)
//   - global /api/labels CRUD                        → 403 (platform structure)
// Do not edit counterfactual ①② while Barry independent-verifies them.

func agentPrincipalCtx(r *http.Request) *http.Request {
	ctx := WithAgentPrincipal(r.Context(), AgentPrincipal{
		AgentID:     "11111111-1111-1111-1111-111111111111",
		WorkspaceID: "22222222-2222-2222-2222-222222222222",
		OwnerUserID: "33333333-3333-3333-3333-333333333333",
		ActorSource: "agent_credential",
	})
	return r.WithContext(ctx)
}

func TestRejectAgentOnHumanAPI_AdminSurfaces403(t *testing.T) {
	// Downstream would succeed if middleware did not reject — proves the gate.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	h := RejectAgentOnHumanAPI(inner)

	// Parker product boundary: agent = worker, not platform admin.
	// Each row is a human/admin surface that must not be usable with AgentPrincipal.
	cases := []struct {
		name   string
		method string
		path   string
	}{
		// project write + structure
		{"project create", http.MethodPost, "/api/projects"},
		{"project update", http.MethodPut, "/api/projects/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		{"project delete", http.MethodDelete, "/api/projects/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		{"project resource create", http.MethodPost, "/api/projects/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/resources"},
		{"project resource delete", http.MethodDelete, "/api/projects/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/resources/r1"},
		// squad fully retired for agents (middleware 403 before 410 handler)
		{"squad list", http.MethodGet, "/api/squads"},
		{"squad create", http.MethodPost, "/api/squads"},
		{"squad set-role", http.MethodPatch, "/api/squads/s1/members/role"},
		// global labels CRUD — NOT issue-on-issue labels
		{"labels list global", http.MethodGet, "/api/labels"},
		{"labels create global", http.MethodPost, "/api/labels"},
		{"labels update global", http.MethodPut, "/api/labels/l1"},
		{"labels delete global", http.MethodDelete, "/api/labels/l1"},
		// agent admin / lifecycle
		{"agents list admin", http.MethodGet, "/api/agents"},
		{"agents create", http.MethodPost, "/api/agents"},
		// agents update/archive allowed through middleware only for UUID paths;
		// canUpdateAgent/canManageAgent enforce self-only (task #125).
		{"agents update non-uuid", http.MethodPut, "/api/agents/a1"},
		{"agent skills set", http.MethodPut, "/api/agents/a1/skills"},
		// autopilot
		// PAT / me
		{"tokens list", http.MethodGet, "/api/tokens"},
		{"me", http.MethodGet, "/api/me"},
		// runtime ops
		{"runtimes list", http.MethodGet, "/api/runtimes"},
		// human issue write still on human path
		{"human issue create", http.MethodPost, "/api/issues"},
		{"human channels list", http.MethodGet, "/api/channels"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := agentPrincipalCtx(httptest.NewRequest(tc.method, tc.path, nil))
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s want 403 for agent on %s %s",
					rec.Code, rec.Body.String(), tc.method, tc.path)
			}
		})
	}
}

// TestRejectAgentOnHumanAPI_DedicatedAgentPathsPass proves necessary work
// surfaces under /api/agent/* are NOT blocked by the admin 403 gate —
// especially issue labels (attach/list) vs global /api/labels.
func TestRejectAgentOnHumanAPI_DedicatedAgentPathsPass(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	h := RejectAgentOnHumanAPI(inner)

	allow := []struct {
		name   string
		method string
		path   string
	}{
		{"agent channels", http.MethodGet, "/api/agent/channels"},
		{"agent issue get", http.MethodGet, "/api/agent/issues/i1"},
		// necessary: labels ON an issue (not global label system)
		{"agent issue labels list", http.MethodGet, "/api/agent/issues/i1/labels"},
		{"agent issue labels attach", http.MethodPost, "/api/agent/issues/i1/labels"},
		{"agent issue labels detach", http.MethodDelete, "/api/agent/issues/i1/labels/l1"},
		{"agent directory", http.MethodGet, "/api/agent/agents"},
		{"agent workspace", http.MethodGet, "/api/agent/workspace"},
		{"agent project resources RO", http.MethodGet, "/api/agent/projects/p1/resources"},
		// task #125: self-manage human agent paths pass middleware (handler enforces id)
		{"agents update uuid", http.MethodPut, "/api/agents/11111111-1111-1111-1111-111111111111"},
		{"agents archive uuid", http.MethodPost, "/api/agents/11111111-1111-1111-1111-111111111111/archive"},
	}
	for _, tc := range allow {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := agentPrincipalCtx(httptest.NewRequest(tc.method, tc.path, nil))
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("status=%d body=%s want 204 (dedicated path must pass gate) for %s %s",
					rec.Code, rec.Body.String(), tc.method, tc.path)
			}
		})
	}
}

// TestRejectAgentOnHumanAPI_HumanWithoutPrincipalUnaffected ensures the
// middleware only fires for AgentPrincipal (humans still reach handlers).
func TestRejectAgentOnHumanAPI_HumanWithoutPrincipalUnaffected(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	h := RejectAgentOnHumanAPI(inner)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/projects", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204 for non-agent principal on human path", rec.Code)
	}
}

// TestRejectAgentOnHumanAPI_IssueLabelsVsGlobalLabels documents the product
// split: same domain, two entry points, opposite agent policy.
func TestRejectAgentOnHumanAPI_IssueLabelsVsGlobalLabels(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	h := RejectAgentOnHumanAPI(inner)

	// Global label system → 403
	rec := httptest.NewRecorder()
	req := agentPrincipalCtx(httptest.NewRequest(http.MethodPost, "/api/labels", nil))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("global labels create status=%d want 403", rec.Code)
	}

	// On-issue label attach via dedicated path → pass gate
	rec = httptest.NewRecorder()
	req = agentPrincipalCtx(httptest.NewRequest(http.MethodPost, "/api/agent/issues/i1/labels", nil))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("issue labels attach status=%d want 204 (necessary; not global CRUD)", rec.Code)
	}
}

// TestRejectAgentOnHumanAPI_IncrementsAliasZeroMetric hard-asserts the
// human_route_hits_total counter increments on reject (Barry: metric not just source-looking-right).
func TestRejectAgentOnHumanAPI_IncrementsAliasZeroMetric(t *testing.T) {
	// Per-registry CounterVec (production path via metrics.NewRegistry).
	reg := prometheus.NewRegistry()
	RegisterAgentHumanRouteMetrics(reg)
	// Same-registry re-entry must not panic (Parker successor note).
	RegisterAgentHumanRouteMetrics(reg)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	before := humanRouteSiteFromGather(mfs, "RejectAgentOnHumanAPI")
	if before != 0 {
		t.Fatalf("seeded RejectAgentOnHumanAPI = %v, want exact 0", before)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	h := RejectAgentOnHumanAPI(inner)
	rec := httptest.NewRecorder()
	req := agentPrincipalCtx(httptest.NewRequest(http.MethodGet, "/api/projects", nil))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403", rec.Code)
	}

	mfs, err = reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	after := humanRouteSiteFromGather(mfs, "RejectAgentOnHumanAPI")
	if after != before+1 {
		t.Fatalf("human_route_hits_total{site=RejectAgentOnHumanAPI} before=%v after=%v; want exact +1", before, after)
	}
}

func humanRouteSiteFromGather(mfs []*dto.MetricFamily, site string) float64 {
	for _, mf := range mfs {
		if mf.GetName() != "multica_agent_surface_human_route_hits_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "site" && lp.GetValue() == site && m.GetCounter() != nil {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	return -1 // missing
}

func TestAgentPrincipalMayUseHumanAgentPath(t *testing.T) {
	self := "/api/agents/11111111-1111-1111-1111-111111111111"
	selfMembers := "/api/members/agents/11111111-1111-1111-1111-111111111111"
	cases := []struct {
		method, path string
		want         bool
	}{
		{http.MethodPut, self, true},
		{http.MethodPost, self + "/archive", true},
		{http.MethodPost, self + "/restore", true},
		{http.MethodGet, self, false},
		{http.MethodPost, "/api/agents", false},
		{http.MethodPut, "/api/agents/a1", false},
		{http.MethodPut, self + "/skills", false},
		{http.MethodPost, self + "/credentials", false},
		// Members Directory primary prefix (ADR 0013) — same self-manage rules.
		{http.MethodPut, selfMembers, true},
		{http.MethodPost, selfMembers + "/archive", true},
		{http.MethodPost, selfMembers + "/restore", true},
		{http.MethodGet, selfMembers, false},
		{http.MethodPost, "/api/members/agents", false},
		{http.MethodPut, selfMembers + "/skills", false},
	}
	for _, tc := range cases {
		if got := agentPrincipalMayUseHumanAgentPath(tc.method, tc.path); got != tc.want {
			t.Fatalf("%s %s: got %v want %v", tc.method, tc.path, got, tc.want)
		}
	}
}
