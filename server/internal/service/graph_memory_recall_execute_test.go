// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/pkg/agent"
)

func TestGraphMemoryRecallInjectionContentBoundsCitations(t *testing.T) {
	citations := make([]memorygraph.Citation, graphMemoryRecallMaxCitationCount+1)
	for i := range citations {
		citations[i] = memorygraph.Citation{NodeID: fmt.Sprintf("n%d", i), Level: i, Epistemic: "observed"}
	}
	content := graphMemoryRecallInjectionContent(strings.Repeat("x", graphMemoryRecallMaxSummaryChars+1), citations)
	if !strings.Contains(content, graphMemoryRecallTruncationMarker) {
		t.Fatalf("content missing summary truncation marker: %q", content)
	}
	if strings.Count(content, "\n- n") != graphMemoryRecallMaxCitationCount || !strings.Contains(content, "\n- …and 1 more") {
		t.Fatalf("content did not cap citations: %q", content)
	}
}

// replayAgentBackend hands every Execute the same completed session.
type replayAgentBackend struct{ output string }

func (b *replayAgentBackend) Execute(_ context.Context, _ string, _ agent.ExecOptions) (*agent.Session, error) {
	msgs := make(chan agent.Message)
	close(msgs)
	results := make(chan agent.Result, 1)
	results <- agent.Result{Status: "completed", Output: b.output}
	close(results)
	return &agent.Session{Messages: msgs, Result: results}, nil
}

func testPriorPlan(query string) *GraphMemoryRecallPlan {
	return &GraphMemoryRecallPlan{
		Query: query, GraphVersion: 1,
		WorkspaceID: "ws-1", GraphKind: "graph", GraphOwnerID: "owner-1",
		GraphView: memorygraph.GraphView{ChannelID: "chan-1"},
	}
}

// A pre-populated brief for the normalized query is reused as-is; a nil
// backend proves no provider work happens on the cache-hit path.
func TestPriorBriefCacheHitSkipsCompression(t *testing.T) {
	e := &GraphMemoryRecallExecutor{model: "m"}
	store := memorygraph.NewPriorRecordStore(t.TempDir())
	rec := &memorygraph.PriorRecord{GraphVersion: 1, Query: "old", Briefs: map[string]memorygraph.PriorBrief{
		memorygraph.NormalizeRecallKey("Query B"): {Summary: "cached"},
	}}
	plan := testPriorPlan("Query B")

	brief := e.priorBrief(context.Background(), plan, rec, store, graphPriorOwnerKey(plan), nil)
	if brief == nil || brief.Summary != "cached" {
		t.Fatalf("brief = %+v, want cached", brief)
	}
}

// Cache miss: compression runs against the backend and the parsed brief is
// written back under the normalized query key for the next recall.
func TestPriorBriefCompressMissWritesBack(t *testing.T) {
	e := &GraphMemoryRecallExecutor{model: "m"}
	dir := t.TempDir()
	store := memorygraph.NewPriorRecordStore(dir)
	rec := &memorygraph.PriorRecord{GraphVersion: 1, Query: "old", Transcript: []memorygraph.TraceMessage{
		{Kind: "message", Sequence: 0, Type: "text", Content: "explored n-a"},
	}}
	backend := &replayAgentBackend{output: `{"summary":"fresh","node_ids":["n-a"],"observations":["o"],"rejected":[],"open_questions":[]}`}
	plan := testPriorPlan("Query B")

	brief := e.priorBrief(context.Background(), plan, rec, store, graphPriorOwnerKey(plan), backend)
	if brief == nil || brief.Summary != "fresh" || len(brief.NodeIDs) != 1 {
		t.Fatalf("brief = %+v, want compressed", brief)
	}
	reloaded, err := store.Load(graphPriorOwnerKey(plan))
	if err != nil || reloaded == nil {
		t.Fatalf("Load after write-back: %v %v", reloaded, err)
	}
	if got := reloaded.Briefs[memorygraph.NormalizeRecallKey("Query B")].Summary; got != "fresh" {
		t.Fatalf("written-back brief = %q, want fresh", got)
	}
}
