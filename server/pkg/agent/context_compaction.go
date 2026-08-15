package agent

import (
	"errors"
	"sync"
	"time"
)

// ErrResidentTurnNoSemanticWork means a Message turn after compaction
// finished with no thinking, tools, or text. That turn must not count as
// delivery coverage (Raft: compact does not consume inbox).
var ErrResidentTurnNoSemanticWork = errors.New("resident turn produced no semantic work after context compaction")

const proactiveContextCompactionPercent = 60.0

// After a closeout compact that does not get occupancy under this line, do
// not compact again until occupancy actually falls (new/reset session).
const proactiveContextCompactionResumePercent = 45.0

// postTurnCompactionTimeout bounds a closeout compact so a stuck provider
// cannot hold the resident admission lock after the user-visible turn ended.
const postTurnCompactionTimeout = 3 * time.Minute

const proactiveContextCompactionInstructions = `Preserve a structured checkpoint of the current conversation. Retain user intent, decisions, constraints, unresolved questions, active work, external side effects, changed files, test results, and source references. Distinguish verified facts from assumptions. Keep the checkpoint concise and sufficient for the next turn.`

// compactionAttemptState stops repeating a closeout compact that did not get
// occupancy under the resume line.
type compactionAttemptState struct {
	mu           sync.Mutex
	lastFailed   bool
	lastAfterPct *float64
}

func occupancyPercent(stats *RuntimeTokenStats) (float64, bool) {
	if stats == nil {
		return 0, false
	}
	if stats.ContextPercent != nil {
		return *stats.ContextPercent, true
	}
	if stats.ContextTokens == nil || stats.ContextWindow == nil || *stats.ContextWindow <= 0 {
		return 0, false
	}
	return float64(*stats.ContextTokens) * 100 / float64(*stats.ContextWindow), true
}

func shouldProactivelyCompact(stats *RuntimeTokenStats) bool {
	return shouldProactivelyCompactAt(stats, nil)
}

func shouldProactivelyCompactAt(stats *RuntimeTokenStats, st *compactionAttemptState) bool {
	pct, ok := occupancyPercent(stats)
	if !ok || pct < proactiveContextCompactionPercent {
		return false
	}
	if st == nil {
		return true
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.lastFailed && st.lastAfterPct != nil && *st.lastAfterPct >= proactiveContextCompactionResumePercent {
		return false
	}
	return true
}

func (st *compactionAttemptState) recordAttempt(failed bool, after *RuntimeTokenStats) {
	if st == nil {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.lastFailed = failed
	if pct, ok := occupancyPercent(after); ok {
		copied := pct
		st.lastAfterPct = &copied
		return
	}
	if !failed {
		st.lastAfterPct = nil
	}
}
