package memorygraph

import (
	"fmt"
)

// QueryRecorder glues Explorer output and the on-disk query log of one
// window (design §5.2): recalls are appended at recall time, and entries
// carrying the legacy judge fields feed version backtests (design Q26).
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
		if e.LegacyNonAuthoritative {
			continue
		}
		if e.Version > aVersion && e.Version <= bVersion && e.JudgeDone {
			out = append(out, e)
		}
	}
	return out, nil
}
