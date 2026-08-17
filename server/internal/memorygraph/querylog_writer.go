package memorygraph

import (
	"fmt"
)

// QueryRecorder glues Explorer output, the async Judge, and the on-disk
// query log of one window (design §5.2/5.3): recalls are appended at recall
// time, judge results are written back asynchronously, and judged entries
// feed version backtests (design Q26).
type QueryRecorder struct {
	store    *Store
	windowID string
}

// NewQueryRecorder returns a QueryRecorder writing query_log/<windowID>.jsonl.
func NewQueryRecorder(store *Store, windowID string) *QueryRecorder {
	return &QueryRecorder{store: store, windowID: windowID}
}

// RecordRecall appends one recall entry to the window log.
func (r *QueryRecorder) RecordRecall(e QueryLogEntry) error {
	if err := r.store.AppendQueryLog(r.windowID, &e); err != nil {
		return fmt.Errorf("query recorder: record recall %s: %w", e.TraceID, err)
	}
	return nil
}

// BaselineSignal is the judge-time baseline coverage record written back
// onto a query-log entry (design Q13/A2, review R10): the hybrid top-k hit
// ids on the current version and whether the ground truth set lay within
// their n-hop neighborhood (n = the adopted path's explore rounds).
type BaselineSignal struct {
	Covered bool
	TopK    []string
}

// ApplyJudge writes the judge result back onto the entry with the given trace
// id: it marks the entry judged, records the score, the relevant-node ground
// truth set, and the judge-time baseline coverage signal (design §5.3). It
// reports whether a matching entry was found.
func (r *QueryRecorder) ApplyJudge(traceID string, res *JudgeResult, baseline BaselineSignal) (bool, error) {
	if res == nil {
		return false, fmt.Errorf("query recorder: nil judge result")
	}
	found, err := r.store.UpdateQueryLogEntry(r.windowID, traceID, func(e *QueryLogEntry) {
		e.JudgeDone = true
		e.JudgeScore = res.Score
		e.RelevantNodes = res.RelevantNodes
		e.BaselineCovered = baseline.Covered
		e.BaselineTopK = baseline.TopK
	})
	if err != nil {
		return false, fmt.Errorf("query recorder: apply judge for trace %s: %w", traceID, err)
	}
	return found, nil
}

// QueriesBetween returns the judged entries of the current window whose graph
// version falls in (aVersion, bVersion] — the backtest input for a version
// transition (design Q26).
func (r *QueryRecorder) QueriesBetween(aVersion, bVersion int) ([]*QueryLogEntry, error) {
	entries, err := r.store.ReadQueryLog(r.windowID)
	if err != nil {
		return nil, fmt.Errorf("query recorder: read window %s: %w", r.windowID, err)
	}
	var out []*QueryLogEntry
	for _, e := range entries {
		if e.Version > aVersion && e.Version <= bVersion && e.JudgeDone {
			out = append(out, e)
		}
	}
	return out, nil
}
