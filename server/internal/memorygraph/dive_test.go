// SPDX-License-Identifier: Apache-2.0

package memorygraph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// Spec §6/§7, acceptance A5/A6: the Dive judge pins the exact Explore graph
// version (never falls back to current), receives every normally completed
// run — found=false included — with its summary/viewed/submitted/rounds/
// status payload, and bypasses Explore error/timeout/budget runs with
// reward 0 and no model grading.

// fakeDiveBackend captures the grading prompt and replays a scripted final
// response.
type fakeDiveBackend struct {
	mu      sync.Mutex
	prompts []string
	output  string
	err     error
	status  string
}

func (f *fakeDiveBackend) Execute(_ context.Context, prompt string, _ agent.ExecOptions) (*agent.Session, error) {
	f.mu.Lock()
	f.prompts = append(f.prompts, prompt)
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if f.status != "" && f.status != "completed" {
		msgs := make(chan agent.Message)
		close(msgs)
		results := make(chan agent.Result, 1)
		results <- agent.Result{Status: f.status, Output: f.output}
		close(results)
		return &agent.Session{Messages: msgs, Result: results}, nil
	}
	return completedSessionWithMessages(f.output), nil
}

func (f *fakeDiveBackend) lastPrompt(t *testing.T) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.prompts) == 0 {
		t.Fatal("dive backend never invoked")
	}
	return f.prompts[len(f.prompts)-1]
}

func mustDiveStore(t *testing.T) *Store {
	t.Helper()
	store := NewStore(filepath.Join(t.TempDir(), "memory_graph"))
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	seedGraphNode(t, store, 1, "n1", "the dispatch router retries failed batch jobs with exponential backoff")
	seedGraphNode(t, store, 1, "n2", "the batch scheduler holds a per-queue mutex")
	return store
}

func diveTestRuns() []DiveRunInput {
	return []DiveRunInput{
		{
			TrajectoryID:     "t1",
			SeedIndex:        0,
			Status:           "found",
			Summary:          "router retry backoff is the documented fix",
			ViewedNodeIDs:    []string{"n1"},
			SubmittedNodeIDs: []string{"n1"},
			Rounds:           3,
		},
		{
			TrajectoryID:  "t2",
			SeedIndex:     1,
			Status:        "miss",
			Summary:       "",
			ViewedNodeIDs: []string{"n2"},
			Rounds:        1,
		},
		{
			TrajectoryID: "t3",
			SeedIndex:    2,
			Status:       "error",
			Summary:      "backend exploded",
			Rounds:       0,
		},
	}
}

const diveScoresJSON = `{"scores": [
  {"trajectory_id": "t1", "relevance": 0.9, "groundedness": 0.4, "completeness": 0.7},
  {"trajectory_id": "t2", "relevance": 0.2, "groundedness": 0.2, "completeness": 0.2}
], "necessary_information": [{"statement": "router retries use backoff", "source_refs": ["seg:s1"], "node_ids": ["n1"], "rationale": "audit trail"}], "incomplete": false}`

func TestPartitionDiveRuns(t *testing.T) {
	normal, bypassed := PartitionDiveRuns([]DiveRunInput{
		{TrajectoryID: "a", Status: "found"},
		{TrajectoryID: "b", Status: "miss"},
		{TrajectoryID: "c", Status: "error"},
		{TrajectoryID: "d", Status: "budget"},
		{TrajectoryID: "e", Status: "timeout"},
	})
	if len(normal) != 2 || normal[0].TrajectoryID != "a" || normal[1].TrajectoryID != "b" {
		t.Fatalf("normal = %+v, want [a b]", normal)
	}
	if len(bypassed) != 3 || bypassed[0].TrajectoryID != "c" || bypassed[1].TrajectoryID != "d" || bypassed[2].TrajectoryID != "e" {
		t.Fatalf("bypassed = %+v, want [c d e]", bypassed)
	}
}

func TestDivePinsExactGraphVersion(t *testing.T) {
	store := mustDiveStore(t)
	backend := &fakeDiveBackend{output: diveScoresJSON}
	diver := NewDiver(store, backend, DiveConfig{})

	// The store's current version is 1; asking for v2 must fail even though a
	// current version exists — no fallback (A6).
	if _, err := diver.Dive(context.Background(), "q", 2, diveTestRuns()); err == nil ||
		!strings.Contains(err.Error(), "pinned graph version 2") {
		t.Fatalf("missing pinned version error = %v", err)
	}
	if len(backend.prompts) != 0 {
		t.Fatal("judge invoked despite the missing pinned version")
	}

	// A corrupt pinned version fails the dive as well.
	if err := os.MkdirAll(filepath.Join(store.VersionDir(2), "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.VersionDir(2), "nodes", "bad.md"), []byte("garbage without frontmatter"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := diver.Dive(context.Background(), "q", 2, diveTestRuns()); err == nil ||
		!strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("corrupt pinned version error = %v", err)
	}

	// The intact pinned version grades normally.
	res, err := diver.Dive(context.Background(), "q", 1, diveTestRuns())
	if err != nil {
		t.Fatalf("Dive on pinned v1: %v", err)
	}
	if len(res.Scores) != 2 {
		t.Fatalf("scores = %+v, want 2", res.Scores)
	}
}

func TestDiveInputPayloadCompleteness(t *testing.T) {
	store := mustDiveStore(t)
	backend := &fakeDiveBackend{output: diveScoresJSON}
	diver := NewDiver(store, backend, DiveConfig{})

	res, err := diver.Dive(context.Background(), "router retry policy", 1, diveTestRuns())
	if err != nil {
		t.Fatalf("Dive: %v", err)
	}
	prompt := backend.lastPrompt(t)
	for _, want := range []string{
		"Query: router retry policy",
		"Pinned graph version: 1",
		"t1", "status found", "rounds 3", "router retry backoff is the documented fix",
		"t2", "status miss", "rounds 1",
		"Submitted nodes: n1",
		"exponential backoff", // viewed node body as grounding evidence
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("grading prompt missing %q:\n%s", want, prompt)
		}
	}
	// The error run bypasses grading entirely (A5): never in the prompt.
	if strings.Contains(prompt, "t3") || strings.Contains(prompt, "backend exploded") {
		t.Fatalf("bypassed run leaked into the grading prompt:\n%s", prompt)
	}
	if len(res.Bypassed) != 1 || res.Bypassed[0].TrajectoryID != "t3" {
		t.Fatalf("Bypassed = %+v, want [t3]", res.Bypassed)
	}
	if res.Rounds != 1 {
		t.Fatalf("dive rounds = %d, want 1 (server-counted)", res.Rounds)
	}
	if res.Incomplete {
		t.Fatal("Incomplete = true, want false")
	}
	if len(res.NecessaryInformation) != 1 || res.NecessaryInformation[0].Statement != "router retries use backoff" {
		t.Fatalf("NecessaryInformation = %+v", res.NecessaryInformation)
	}
	// Scores parsed per run.
	if len(res.Scores) != 2 || res.Scores[0].TrajectoryID != "t1" || res.Scores[0].Relevance != 0.9 ||
		res.Scores[0].Groundedness != 0.4 || res.Scores[0].Completeness != 0.7 {
		t.Fatalf("scores = %+v", res.Scores)
	}
}

func TestDiveScoreValidation(t *testing.T) {
	cases := map[string]string{
		"out of range": `{"scores": [
		  {"trajectory_id": "t1", "relevance": 1.5, "groundedness": 0.4, "completeness": 0.7},
		  {"trajectory_id": "t2", "relevance": 0.2, "groundedness": 0.2, "completeness": 0.2}
		], "incomplete": false}`,
		"unknown trajectory": `{"scores": [
		  {"trajectory_id": "t1", "relevance": 0.9, "groundedness": 0.4, "completeness": 0.7},
		  {"trajectory_id": "tX", "relevance": 0.2, "groundedness": 0.2, "completeness": 0.2}
		], "incomplete": false}`,
		"missing score": `{"scores": [
		  {"trajectory_id": "t1", "relevance": 0.9, "groundedness": 0.4, "completeness": 0.7}
		], "incomplete": false}`,
		"duplicate score": `{"scores": [
		  {"trajectory_id": "t1", "relevance": 0.9, "groundedness": 0.4, "completeness": 0.7},
		  {"trajectory_id": "t1", "relevance": 0.8, "groundedness": 0.4, "completeness": 0.7},
		  {"trajectory_id": "t2", "relevance": 0.2, "groundedness": 0.2, "completeness": 0.2}
		], "incomplete": false}`,
		"not json": `I could not grade these trajectories.`,
	}
	for name, output := range cases {
		t.Run(name, func(t *testing.T) {
			store := mustDiveStore(t)
			backend := &fakeDiveBackend{output: output}
			diver := NewDiver(store, backend, DiveConfig{})
			if _, err := diver.Dive(context.Background(), "q", 1, diveTestRuns()); err == nil {
				t.Fatalf("Dive with %s response must fail", name)
			}
		})
	}
}

func TestDiveIncompleteFlag(t *testing.T) {
	store := mustDiveStore(t)
	backend := &fakeDiveBackend{output: `{"scores": [
	  {"trajectory_id": "t1", "relevance": 0.9, "groundedness": 0.4, "completeness": 0.7},
	  {"trajectory_id": "t2", "relevance": 0.2, "groundedness": 0.2, "completeness": 0.2}
	], "necessary_information": [], "incomplete": true}`}
	diver := NewDiver(store, backend, DiveConfig{})
	res, err := diver.Dive(context.Background(), "q", 1, diveTestRuns())
	if err != nil {
		t.Fatalf("Dive: %v", err)
	}
	if !res.Incomplete {
		t.Fatal("Incomplete = false, want true (budget exhaustion preserves grading, blocks ground truth)")
	}
}

func TestDiveBackendFailure(t *testing.T) {
	store := mustDiveStore(t)
	backend := &fakeDiveBackend{err: fmt.Errorf("model endpoint 503")}
	diver := NewDiver(store, backend, DiveConfig{Timeout: time.Minute})
	if _, err := diver.Dive(context.Background(), "q", 1, diveTestRuns()); err == nil {
		t.Fatal("backend failure must fail the dive (the job layer retries)")
	}

	backend = &fakeDiveBackend{status: "error", output: ""}
	diver = NewDiver(store, backend, DiveConfig{})
	if _, err := diver.Dive(context.Background(), "q", 1, diveTestRuns()); err == nil {
		t.Fatal("non-completed judge session must fail the dive")
	}
}
