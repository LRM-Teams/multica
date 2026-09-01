// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/memorygraph"
)

// Recall-target federation (unification spec §4.4): a channel/project recall
// also searches the workspace research graph with its own view, cites its
// nodes graph-qualified, and never leaks research-visible content through
// project/channel views — and research is never a fallback for scope-less
// tasks.

const (
	federationWS   = "11111111-1111-1111-1111-111111111111"
	federationPrj  = "22222222-2222-2222-2222-222222222222"
	federationBare = "33333333-3333-3333-3333-333333333333"
)

// researchFixture builds a service whose workspaces root holds a research
// graph (with research- and project-visible nodes) and a primary project
// graph (with project- and research-visible nodes) under one workspace.
func researchFixture(t *testing.T) (*GraphMemoryRecallService, string, string, string) {
	t.Helper()
	root := t.TempDir()

	researchDir, err := memorygraph.EnsureScopedDir(root, federationWS, memorygraph.GraphDirKindResearch, federationWS)
	if err != nil {
		t.Fatalf("EnsureScopedDir research: %v", err)
	}
	store := memorygraph.NewStore(researchDir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init research: %v", err)
	}
	v := currentVersionOf(t, store)
	saveFederationNode(t, store, v, "research_node:hit", "cache regression traced to connection pool exhaustion", "research")
	saveFederationNode(t, store, v, "intruder:project-node", "project visible node inside the research graph", "project")

	projectDir, err := memorygraph.EnsureScopedDir(root, federationWS, memorygraph.GraphDirKindProject, federationPrj)
	if err != nil {
		t.Fatalf("EnsureScopedDir project: %v", err)
	}
	pstore := memorygraph.NewStore(projectDir)
	if err := pstore.Init(); err != nil {
		t.Fatalf("Init project: %v", err)
	}
	pv := currentVersionOf(t, pstore)
	saveFederationNode(t, pstore, pv, "project:hit", "cache regression traced to connection pool exhaustion", "")
	saveFederationNode(t, pstore, pv, "intruder:research-node", "research visible node inside the project graph", "research")

	svc := NewGraphMemoryRecallService(nil, GraphMemoryLimits{}, root, "graph", nil)
	return svc, researchDir, projectDir, root
}

func currentVersionOf(t *testing.T, store *memorygraph.Store) int {
	t.Helper()
	v, err := store.CurrentVersion()
	if err != nil {
		t.Fatalf("CurrentVersion: %v", err)
	}
	return v
}

func saveFederationNode(t *testing.T, store *memorygraph.Store, v int, id, body, visibility string) {
	t.Helper()
	n := &memorygraph.Node{
		NodeID: id, Body: body, Visibility: visibility,
		Epistemic: memorygraph.StatusProposed, TemporalStatus: memorygraph.TemporalCurrent,
		CreatedBy: memorygraph.CreatorIngester, CreatedVersion: v, UpdatedVersion: v, ObservedAt: time.Now().UTC(),
	}
	if err := store.SaveNode(v, n); err != nil {
		t.Fatalf("SaveNode %s: %v", id, err)
	}
}

// The workspace research graph joins the recall target list when it exists
// with a valid identity, and is silently absent otherwise (federation is
// additive and non-fatal — a missing research graph never blocks recall, and
// a research dir without the identity marker fails closed).
func TestGraphMemoryRecallResearchTargetResolution(t *testing.T) {
	svc, researchDir, _, _ := researchFixture(t)

	target := svc.researchTarget(federationWS)
	if target == nil {
		t.Fatalf("research target missing for a valid research graph")
	}
	if target.Dir != researchDir || target.Version != 1 {
		t.Fatalf("target = %+v, want dir %s v1", target, researchDir)
	}
	if !target.View.AllowResearch || target.View.AllowProject || target.View.ChannelID != "" {
		t.Fatalf("research view = %+v, want research-only visibility", target.View)
	}

	// A workspace without a research graph resolves no research target.
	if target := svc.researchTarget(federationBare); target != nil {
		t.Fatalf("research target = %+v, want nil for a missing graph", target)
	}

	// A research dir whose identity marker does not match fails closed.
	root := t.TempDir()
	dir, err := memorygraph.DirForScope(root, federationWS, memorygraph.GraphDirKindResearch, federationWS)
	if err != nil {
		t.Fatalf("DirForScope: %v", err)
	}
	bare := NewGraphMemoryRecallService(nil, GraphMemoryLimits{}, root, "graph", nil)
	if err := memorygraph.NewStore(dir).Init(); err != nil {
		t.Fatalf("Init bare store: %v", err)
	}
	if target := bare.researchTarget(federationWS); target != nil {
		t.Fatalf("research target = %+v, want nil for an invalid identity", target)
	}
}

// Research citations are graph-qualified (research:<workspace>/node:<id>)
// while primary-graph citations stay bare, so badges can tell the graphs
// apart, and the section carries the research knowledge itself (bounded node
// bodies), not just ids.
func TestGraphMemoryRecallResearchCitationsQualified(t *testing.T) {
	section, citations := researchRecallSection(federationWS,
		[]researchRecallHit{{
			Citation: memorygraph.Citation{NodeID: "research_node:hit", Level: 0, Epistemic: memorygraph.StatusProposed},
			Body:     "cache regression traced to connection pool exhaustion",
		}},
		[]memorygraph.Citation{{NodeID: "project:hit", Level: 0, Epistemic: memorygraph.StatusProposed}},
	)
	qualified := "research:" + federationWS + "/node:research_node:hit"
	if !strings.Contains(section, qualified) {
		t.Fatalf("section lacks the qualified citation %q:\n%s", qualified, section)
	}
	if !strings.Contains(section, "cache regression traced to connection pool exhaustion") {
		t.Fatalf("section lacks the research body:\n%s", section)
	}
	qualifiedCount, barePrimary := 0, false
	for _, c := range citations {
		if c.NodeID == qualified {
			qualifiedCount++
		}
		if c.NodeID == "project:hit" {
			barePrimary = true
		}
	}
	if qualifiedCount != 1 || !barePrimary {
		t.Fatalf("citations = %+v, want one qualified research entry plus the bare primary", citations)
	}
}

// Visibility isolation both ways: the primary project view never returns
// research-visible nodes (even ones physically stored in the project graph),
// and the federated research view never returns project- or channel-visible
// nodes.
func TestGraphMemoryRecallResearchVisibilityIsolated(t *testing.T) {
	_, researchDir, projectDir, _ := researchFixture(t)
	ctx := context.Background()

	docs := searchWithView(t, ctx, projectDir, memorygraph.GraphView{AllowProject: true}, "cache regression")
	if !containsDoc(docs, "project:hit") {
		t.Fatalf("project view missed the project node: %v", docs)
	}
	if containsDoc(docs, "intruder:research-node") {
		t.Fatalf("project view leaked a research-visible node: %v", docs)
	}

	docs = searchWithView(t, ctx, researchDir, memorygraph.GraphView{AllowResearch: true}, "cache regression")
	if !containsDoc(docs, "research_node:hit") {
		t.Fatalf("research view missed the research node: %v", docs)
	}
	if containsDoc(docs, "intruder:project-node") {
		t.Fatalf("research view leaked a project-visible node: %v", docs)
	}

	// An active view without the research flag never sees research content —
	// fail closed in both directions.
	if docs := searchWithView(t, ctx, researchDir, memorygraph.GraphView{AllowProject: true}, "cache regression"); containsDoc(docs, "research_node:hit") {
		t.Fatalf("project view leaked research content from the research graph: %v", docs)
	}
	if docs := searchWithView(t, ctx, researchDir, memorygraph.GraphView{ChannelID: "chan-1"}, "cache regression"); len(docs) != 0 {
		t.Fatalf("channel view returned nodes from the research graph: %v", docs)
	}
}

func searchWithView(t *testing.T, ctx context.Context, dir string, view memorygraph.GraphView, query string) []string {
	t.Helper()
	store := memorygraph.NewStore(dir)
	cfg := memorygraph.DefaultRetrievalConfig()
	cfg.View = view
	retr := memorygraph.NewHybridRetriever(store, nil, cfg)
	if err := retr.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	docs, err := retr.Search(ctx, query)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	ids := make([]string, 0, len(docs))
	for _, d := range docs {
		ids = append(ids, d.ID)
	}
	return ids
}

func containsDoc(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
