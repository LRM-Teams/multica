package researchrun

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestV6NodeDetailExposesImmutableContentLayers(t *testing.T) {
	if _, ok := reflect.TypeOf(V6ProjectionNodeDetail{}).FieldByName("ContentLayers"); !ok {
		t.Fatal("V6 node detail does not expose immutable content layers")
	}
	raw, err := os.ReadFile("projection_v6_detail.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"research_result_node", "research_insight_version", "objective", "conclusion"} {
		if !strings.Contains(string(raw), required) {
			t.Fatalf("V6 node detail content query missing %q", required)
		}
	}
}

func TestV6ProjectionUsesCanonicalPostgresAndPinnedPages(t *testing.T) {
	raw, err := os.ReadFile("postgres_projection_v6.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, required := range []string{"research_projection_snapshot", "research_projection_slice", "research_result_node", "research_insight_version", "research_node_absorption", "RepeatableRead", "research_v6_outbox", "pending_agent"} {
		if !strings.Contains(source, required) {
			t.Fatalf("projection implementation missing %q", required)
		}
	}
}

func TestV6WorkProjectionUsesAssignedBranchScope(t *testing.T) {
	raw, err := os.ReadFile("postgres_projection_v6.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, required := range []string{
		"research_v6_work_item_branch",
		"agent_inbox_event inbox",
		"inbox.updated_at",
		"inbox.started_at",
		"inbox.completed_at",
		"agent_task_progress_snapshot",
		"progress.updated_at",
		`w.kind<>'director'`,
		"build.defaultVisible[workNodeID] = false",
		`v6ProjectionEdgeID("collapsed_path"`,
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("V6 Work projection missing %q", required)
		}
	}
}

func TestV6WorkProjectionPreservesAttemptFailureDiagnostics(t *testing.T) {
	termination := projectionTerminationForWork("failed", true, "attempt_budget_exhausted", "", "contract_rejected", "content_layers.conclusion is required")
	if termination == nil {
		t.Fatal("failed Work projection omitted termination")
	}
	if termination.ReasonCode != "resource_failure" || termination.ReasonDetail != "attempt_budget_exhausted：content_layers.conclusion is required" {
		t.Fatalf("termination=%+v", termination)
	}
	if got := projectionTerminationForWork("succeeded", true, "", "", "", ""); got != nil {
		t.Fatalf("successful Work has termination=%+v", got)
	}
}

func BenchmarkV6ProjectionPagination(b *testing.B) {
	for _, size := range []int{1000, 10000, 50000} {
		b.Run(fmt.Sprintf("nodes_%d", size), func(b *testing.B) {
			nodes := make([]V6ProjectionNode, size)
			for index := range nodes {
				nodes[index] = V6ProjectionNode{ID: fmt.Sprintf("pv6:result_s:%036d", index), Kind: "result_s", Tier: "S", CanonicalRef: V6ProjectionEntityRef{Kind: "result", ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", index)}, BranchIDs: []string{}, State: V6ProjectionState{Execution: "succeeded", Conclusion: "accepted", Integration: "unmatched"}, CatalogSummary: "bounded", UpdatedAt: "2026-08-14T00:00:00Z"}
			}
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				pages := paginateV6Projection("00000000-0000-4000-8000-000000000601", "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000003", 1, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "default", 1000, nodes, nil, nil)
				if len(pages) == 0 {
					b.Fatal("missing pages")
				}
			}
		})
	}
}

func TestV6ProjectionStableIdentityIncludesCanonicalRevision(t *testing.T) {
	one := v6ProjectionStableID("insight", "00000000-0000-4000-8000-000000000001", 1)
	two := v6ProjectionStableID("insight", "00000000-0000-4000-8000-000000000001", 2)
	if one == two || !strings.Contains(one, ":1") || !strings.Contains(two, ":2") {
		t.Fatalf("unstable revision identity: %q %q", one, two)
	}
}

func TestV6ProjectionSnapshotTransactionBoundary(t *testing.T) {
	raw, err := os.ReadFile("postgres_projection_v6.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"txOpV6ProjectionSnapshot", "commitResearchTx", "RepeatableRead"} {
		if !strings.Contains(string(raw), required) {
			t.Fatalf("projection transaction missing %q", required)
		}
	}
}

func TestV6ProjectionSliceTransactionBoundary(t *testing.T) {
	raw, err := os.ReadFile("projection_v6_slice.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "txOpV6ProjectionSlice") || !strings.Contains(string(raw), "commitResearchTx") {
		t.Fatal("projection slice is not persisted transactionally")
	}
}
