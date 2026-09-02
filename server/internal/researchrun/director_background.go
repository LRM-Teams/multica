// SPDX-License-Identifier: Apache-2.0

package researchrun

import (
	"context"
	"time"
)

// V6BackgroundKnowledgeEntry is one recalled memory-graph node inside the
// Director Brief background-knowledge block (unification spec §5). Epistemic
// and ObservedAt ride along so the Director can weigh each entry; the block's
// guidance states background knowledge is planning reference, never evidence.
type V6BackgroundKnowledgeEntry struct {
	NodeID     string    `json:"node_id"`
	Graph      string    `json:"graph"` // "research" | "project"
	Epistemic  string    `json:"epistemic"`
	ObservedAt time.Time `json:"observed_at"`
	Summary    string    `json:"summary"`
}

// V6BackgroundKnowledgeProvider recalls background knowledge for one Director
// cycle. Implementations run one bounded retrieval per graph over the
// workspace research graph plus — for a project-bound session — the bound
// project's graph, with rejected/superseded nodes filtered by default. An
// empty goal or an unavailable graph yields no entries; only infrastructure
// errors surface, and callers treat every provider error as "no background"
// so recall never blocks the cycle.
type V6BackgroundKnowledgeProvider interface {
	BackgroundKnowledge(ctx context.Context, workspaceID, runID, goal, projectID string) ([]V6BackgroundKnowledgeEntry, error)
}
