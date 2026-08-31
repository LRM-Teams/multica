// SPDX-License-Identifier: Apache-2.0

package memorygraph

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// Dive budgets (spec §6): independently configurable per workspace profile;
// these are the storage-level defaults.
const (
	DefaultDiveMaxRounds      = 6
	DefaultDiveMaxViewedNodes = 24
	DefaultDiveMaxSourceFiles = 4
	DefaultDiveTimeout        = 10 * time.Minute
	// diveNodeBodyMaxRunes caps each viewed node's body in the grading
	// prompt; source media beyond descriptions is loaded through the
	// server-owned source tools (Phase 4), never inline.
	diveNodeBodyMaxRunes = 400
)

// DiveConfig carries the Dive judge's independent budget and model (spec §6,
// brief D24): the model default inherits Explore's at wiring time; a
// workspace override is honored only within server policy.
type DiveConfig struct {
	MaxRounds      int           // dive-side graph-round budget
	MaxViewedNodes int           // dive-side distinct-view budget
	MaxSourceFiles int           // dive-side source-file budget
	Timeout        time.Duration // wall clock per dive attempt
	Model          string        // judge model; empty inherits Explore at wiring time
}

func (c DiveConfig) normalized() DiveConfig {
	if c.MaxRounds <= 0 {
		c.MaxRounds = DefaultDiveMaxRounds
	}
	if c.MaxViewedNodes <= 0 {
		c.MaxViewedNodes = DefaultDiveMaxViewedNodes
	}
	if c.MaxSourceFiles <= 0 {
		c.MaxSourceFiles = DefaultDiveMaxSourceFiles
	}
	if c.Timeout <= 0 {
		c.Timeout = DefaultDiveTimeout
	}
	return c
}

// DiveRunInput is one terminal explore trajectory as presented to the Dive
// judge (A5): summary, viewed nodes, submitted nodes, server-counted rounds,
// and terminal status.
type DiveRunInput struct {
	TrajectoryID     string
	SeedIndex        int
	Status           string // found|miss|error|budget|timeout
	Summary          string
	ViewedNodeIDs    []string
	SubmittedNodeIDs []string
	Rounds           int
}

// diveRunNormal reports whether the run completed normally (found or
// legitimate miss) and is therefore graded; execution errors, timeouts, and
// budget violations bypass model grading with reward 0 (spec §6/§7).
func diveRunNormal(status string) bool {
	return status == "found" || status == "miss"
}

// PartitionDiveRuns splits terminal explore runs into normally completed
// (graded) and bypassed (error/timeout/budget — reward 0, never graded).
func PartitionDiveRuns(runs []DiveRunInput) (normal, bypassed []DiveRunInput) {
	for _, r := range runs {
		if diveRunNormal(r.Status) {
			normal = append(normal, r)
		} else {
			bypassed = append(bypassed, r)
		}
	}
	return normal, bypassed
}

// DiveTrajectoryScore carries the continuous per-dimension scores for one
// normally completed trajectory (spec §7); each dimension is in [0,1].
type DiveTrajectoryScore struct {
	TrajectoryID string
	Relevance    float64
	Groundedness float64
	Completeness float64
}

// DiveInformationItem is one necessary-information statement produced by a
// Dive (spec §8); the persistent graph-scoped catalog and stable identities
// are built in Phase 5.
type DiveInformationItem struct {
	Statement  string   `json:"statement"`
	SourceRefs []string `json:"source_refs"`
	NodeIDs    []string `json:"node_ids"`
	Rationale  string   `json:"rationale"`
}

// DiveResult is the parsed outcome of one Dive attempt.
type DiveResult struct {
	Scores               []DiveTrajectoryScore
	NecessaryInformation []DiveInformationItem
	Incomplete           bool // budget exhaustion: grades stand, no authoritative ground truth
	Rounds               int  // server-counted dive rounds
	Bypassed             []DiveRunInput
	RawResponse          string // audit-only
}

// Diver is the Dive judge executor (spec §6): it grades every normally
// completed explore trajectory of one recall against the recall's pinned
// graph version.
type Diver struct {
	store   *Store
	backend AgentBackend
	cfg     DiveConfig
	scope   ProviderScope
}

// NewDiver requires the server-resolved Dive identity alongside the backend.
func NewDiver(store *Store, backend AgentBackend, cfg DiveConfig, scope ProviderScope) *Diver {
	cfg = cfg.normalized()
	cfg.Model = scope.Model
	return &Diver{store: store, backend: backend, cfg: cfg, scope: scope}
}

// Dive runs one grading pass. The pinned version must exist and load
// cleanly — a missing, deleted, or corrupt version fails the dive; there is
// no fallback to the current version (A6).
func (d *Diver) Dive(ctx context.Context, query string, version int, runs []DiveRunInput) (*DiveResult, error) {
	if d == nil || d.backend == nil {
		return nil, fmt.Errorf("dive: backend not configured")
	}
	if err := validateProviderScope(d.scope, ProviderPurposeDive); err != nil {
		return nil, fmt.Errorf("dive: %w", err)
	}
	versions, err := d.store.ListVersions()
	if err != nil {
		return nil, fmt.Errorf("dive: list versions: %w", err)
	}
	pinned := false
	for _, v := range versions {
		if v == version {
			pinned = true
			break
		}
	}
	if !pinned {
		return nil, fmt.Errorf("dive: pinned graph version %d is missing (no fallback to current)", version)
	}
	g, err := LoadGraph(d.store, version)
	if err != nil {
		return nil, fmt.Errorf("dive: pinned graph version %d is corrupt: %w", version, err)
	}
	normal, bypassed := PartitionDiveRuns(runs)

	ctx, cancel := context.WithTimeout(ctx, d.cfg.Timeout)
	defer cancel()
	session, err := d.backend.Execute(ctx, d.buildPrompt(query, version, g, normal), agent.ExecOptions{
		Timeout:          d.cfg.Timeout,
		Model:            d.cfg.Model,
		EphemeralSession: true,
	})
	if err != nil {
		return nil, fmt.Errorf("dive: execute: %w", err)
	}
	for range session.Messages {
	}
	result, ok := <-session.Result
	if !ok {
		return nil, fmt.Errorf("dive: agent session ended without a result")
	}
	if result.Status != "completed" {
		return nil, fmt.Errorf("dive: judge agent did not complete: %s", result.Status)
	}

	parsed, err := parseDiveResponse(result.Output, normal)
	if err != nil {
		return nil, err
	}
	parsed.Rounds = 1
	parsed.Bypassed = bypassed
	parsed.RawResponse = result.Output
	return parsed, nil
}

// diveResponseJSON is the strict-JSON final response contract of the judge
// agent.
type diveResponseJSON struct {
	Scores []struct {
		TrajectoryID string  `json:"trajectory_id"`
		Relevance    float64 `json:"relevance"`
		Groundedness float64 `json:"groundedness"`
		Completeness float64 `json:"completeness"`
	} `json:"scores"`
	NecessaryInformation []DiveInformationItem `json:"necessary_information"`
	Incomplete           bool                  `json:"incomplete"`
}

// parseDiveResponse parses and validates the judge's final response: every
// normal run must carry exactly one score, no unknown trajectory may be
// scored, and every dimension must lie in [0,1] (spec §7). Violations are
// dive failures (the job layer retries with backoff).
func parseDiveResponse(output string, normal []DiveRunInput) (*DiveResult, error) {
	var resp diveResponseJSON
	if !extractJSONObject(output, &resp) {
		return nil, fmt.Errorf("dive: final response is not a valid JSON object")
	}
	want := make(map[string]bool, len(normal))
	for _, r := range normal {
		want[r.TrajectoryID] = false
	}
	res := &DiveResult{
		NecessaryInformation: resp.NecessaryInformation,
		Incomplete:           resp.Incomplete,
	}
	for _, s := range resp.Scores {
		seen, known := want[s.TrajectoryID]
		if !known {
			return nil, fmt.Errorf("dive: score for unknown trajectory %q", s.TrajectoryID)
		}
		if seen {
			return nil, fmt.Errorf("dive: duplicate score for trajectory %q", s.TrajectoryID)
		}
		for name, v := range map[string]float64{
			"relevance": s.Relevance, "groundedness": s.Groundedness, "completeness": s.Completeness,
		} {
			if v < 0 || v > 1 {
				return nil, fmt.Errorf("dive: %s score %v for trajectory %q outside [0,1]", name, v, s.TrajectoryID)
			}
		}
		want[s.TrajectoryID] = true
		res.Scores = append(res.Scores, DiveTrajectoryScore{
			TrajectoryID: s.TrajectoryID,
			Relevance:    s.Relevance,
			Groundedness: s.Groundedness,
			Completeness: s.Completeness,
		})
	}
	for id, seen := range want {
		if !seen {
			return nil, fmt.Errorf("dive: no score for normal trajectory %q", id)
		}
	}
	return res, nil
}

// buildPrompt renders the grading prompt: the query, the pinned version, the
// budget declaration, and every normal run's full payload with the viewed
// nodes' bodies from the pinned version as grounding evidence. Bypassed runs
// never reach the judge.
func (d *Diver) buildPrompt(query string, version int, g *Graph, normal []DiveRunInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are the graph-memory Dive judge. Grade each explore trajectory below.\n")
	fmt.Fprintf(&b, "Query: %s\n", query)
	fmt.Fprintf(&b, "Pinned graph version: %d\n", version)
	fmt.Fprintf(&b, "Budgets: max rounds %d, max viewed nodes %d, max source files %d.\n\n",
		d.cfg.MaxRounds, d.cfg.MaxViewedNodes, d.cfg.MaxSourceFiles)
	for _, r := range normal {
		fmt.Fprintf(&b, "Trajectory %s (seed %d, status %s, rounds %d):\n", r.TrajectoryID, r.SeedIndex, r.Status, r.Rounds)
		fmt.Fprintf(&b, "Summary: %s\n", r.Summary)
		fmt.Fprintf(&b, "Submitted nodes: %s\n", strings.Join(r.SubmittedNodeIDs, ", "))
		b.WriteString("Viewed nodes:\n")
		for _, id := range r.ViewedNodeIDs {
			body := ""
			if n := g.Node(id); n != nil {
				body = truncateRunes(n.Body, diveNodeBodyMaxRunes)
			}
			fmt.Fprintf(&b, "- %s: %s\n", id, body)
		}
		b.WriteString("\n")
	}
	b.WriteString("Respond with a single JSON object: {\"scores\": [{\"trajectory_id\": ..., \"relevance\": 0..1, \"groundedness\": 0..1, \"completeness\": 0..1}], \"necessary_information\": [{\"statement\": ..., \"source_refs\": [...], \"node_ids\": [...], \"rationale\": ...}], \"incomplete\": bool}. Score every trajectory listed above and no others.\n")
	return b.String()
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
