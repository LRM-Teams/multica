// SPDX-License-Identifier: Apache-2.0

package memorygraph

import (
	"math"
	"sync"
	"time"
)

// SearchClass is the candidate channel one search hit came from (Task 9):
// consolidated graph nodes of the pinned version, or active staging atoms of
// the Task 7 ledger.
type SearchClass string

const (
	SearchGraphNode SearchClass = "graph_node"
	SearchAtom      SearchClass = "staging_atom"
)

// Class-fusion priors: deterministic weights applied per channel before
// merging, so a graph hit and an atom hit with identical channel scores are
// ordered by class priority, never by map iteration.
const (
	searchGraphClassPrior = 0.6
	searchAtomClassPrior  = 0.4
	// atomFreshnessWeight is the share of an atom hit's fused score
	// contributed by the 14-day shadow half-life component.
	atomFreshnessWeight = 0.2
	// AtomShadowHalfLife is the decay constant of the staging channel: an
	// atom's freshness halves every 14 days until it is either consolidated
	// into a graph node or expires.
	AtomShadowHalfLife = 14 * 24 * time.Hour
)

// SearchScoreComponents exposes every deterministic part of a hit's fused
// score so shadow comparisons and tests can decompose ranking decisions.
type SearchScoreComponents struct {
	Lexical         float64 `json:"lexical"`
	Vector          float64 `json:"vector"`
	ShadowFreshness float64 `json:"shadow_freshness,omitempty"`
	ClassPrior      float64 `json:"class_prior"`
}

// SearchHit is one class-aware result of HybridRetriever.SearchAt.
type SearchHit struct {
	Ref        MemoryRef             `json:"ref"`
	Class      SearchClass           `json:"class"`
	Score      float64               `json:"score"`
	Components SearchScoreComponents `json:"components"`
}

// AtomDoc is one active atom of the DB ledger as installed into the
// retriever's staging channel. The DB loader (service layer) resolves the
// snapshot at a publish_seq watermark and applies the Task 8A retraction
// fence before handing atoms over; the retriever re-asserts the exclusions
// it can prove locally (retraction set, consumed flag, channel partition,
// publish_seq ceiling) so no loader bug can leak a forbidden atom.
type AtomDoc struct {
	AtomID     string
	SegmentID  string
	Body       string
	PublishSeq int64
	CreatedAt  time.Time
	ChannelID  string // exact-channel partition; "" = project scope
	Consumed   bool   // consolidated into a graph node: only the node surfaces
}

// atomIndex is the BM25 index over the currently installed atom snapshot.
type atomIndex struct {
	mu     sync.RWMutex
	bm25   *BM25Index
	atoms  map[string]AtomDoc
	seqMax int64
}

func newAtomIndex() *atomIndex {
	return &atomIndex{bm25: NewBM25Index(), atoms: make(map[string]AtomDoc)}
}

// install atomically replaces the snapshot and its lexical index. retracted
// and consumed atoms stay in the map (so callers can inspect the ledger) but
// are never indexed for retrieval.
func (ix *atomIndex) install(atoms []AtomDoc, publishSeqMax int64, retracted map[string]bool) {
	index := NewBM25Index()
	next := make(map[string]AtomDoc, len(atoms))
	for _, a := range atoms {
		next[a.AtomID] = a
		if a.Consumed || retracted[a.AtomID] {
			continue
		}
		index.Add(a.AtomID, a.Body)
	}
	ix.mu.Lock()
	ix.bm25 = index
	ix.atoms = next
	ix.seqMax = publishSeqMax
	ix.mu.Unlock()
}

// visible reports whether one installed atom may surface for the view at the
// caller's watermark.
func (ix *atomIndex) visible(a AtomDoc, view GraphView, publishSeqMax int64) bool {
	if a.Consumed || a.PublishSeq > publishSeqMax || a.AtomID == "" {
		return false
	}
	if a.ChannelID == "" {
		return view.AllowProject
	}
	return view.ChannelID == a.ChannelID
}

// search runs the lexical channel over the snapshot, normalized to [0,1].
func (ix *atomIndex) search(query string, topK int) (map[string]float64, []string) {
	ix.mu.RLock()
	index := ix.bm25
	ix.mu.RUnlock()
	if topK <= 0 {
		topK = DefaultRetrievalConfig().TopK
	}
	hits := index.Search(query, max(topK*4, topK))
	norm := make(map[string]float64, len(hits))
	order := make([]string, 0, len(hits))
	maxScore := 0.0
	for _, h := range hits {
		if h.Score > maxScore {
			maxScore = h.Score
		}
	}
	if maxScore == 0 {
		return norm, order
	}
	for _, h := range hits {
		norm[h.ID] = h.Score / maxScore
		order = append(order, h.ID)
	}
	return norm, order
}

// shadowFreshness is the 14-day half-life component of the staging channel.
func atomShadowFreshness(at time.Time, now time.Time) float64 {
	if at.IsZero() {
		return 1
	}
	age := now.Sub(at)
	if age <= 0 {
		return 1
	}
	// 0.5^(age/halfLife) without math.Pow dependencies on fractional powers
	// of two: exact for multiples of the half-life, monotone otherwise.
	return expNegLn2(float64(age) / float64(AtomShadowHalfLife))
}

// expNegLn2 returns 2^-x. math.Pow is exact for the half-life multiples the
// component is asserted on (0.5^k are exact binary fractions) and correctly
// rounded elsewhere, so ranking stays deterministic across platforms.
func expNegLn2(x float64) float64 {
	return math.Pow(0.5, x)
}
