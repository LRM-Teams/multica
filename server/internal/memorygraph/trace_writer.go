package memorygraph

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// TraceRecorder persists explore/consolidate agent trajectories as JSONL
// files under <graph_dir>/trajectories/ for later training export:
//
//	trajectories/explore/<trace_id>__<run_id>.jsonl        one file per explore run (K runs per query)
//	trajectories/consolidate/<version>__<actor>.jsonl      one file per consolidation trajectory
//
// File layout: a header record, one message record per streamed agent
// message (allowlisted shape modeled on serializeLocalTrajectory in
// server/internal/service/interaction_dag.go — provider keys, session ids
// and diagnostic internals are excluded by construction), and a footer
// record with the final run outcome. A composed reward is appended later as
// a reward record (see RewardComposer), joining trajectories to the query
// log by trace id.
//
// All writes are best-effort: a trace persistence failure is logged and
// never fails the explore/consolidate run. A nil *TraceRecorder is a valid
// no-op recorder; integration wiring decides whether recording is on.
type TraceRecorder struct {
	graphDir string
}

// NewTraceRecorder returns a TraceRecorder writing under graphDir (the
// memory_graph store root).
func NewTraceRecorder(graphDir string) *TraceRecorder {
	return &TraceRecorder{graphDir: graphDir}
}

// TraceDrain buffers the streamed messages of one trajectory. The drain
// goroutine starts as soon as the session is created — before Result is
// awaited — because Session.Messages is a 256-cap buffered channel and a
// long trajectory stalls when nobody reads it.
type TraceDrain struct {
	once sync.Once
	done chan []agent.Message
	msgs []agent.Message
}

// Drain starts a goroutine buffering every message of msgs until the channel
// closes (pkg/agent closes Messages before resolving Result). The drain runs
// even when r is nil (recording disabled) so the channel buffer can never
// stall the agent; the buffered messages are simply discarded.
func (r *TraceRecorder) Drain(msgs <-chan agent.Message) *TraceDrain {
	d := &TraceDrain{done: make(chan []agent.Message, 1)}
	if msgs == nil {
		d.done <- nil
		return d
	}
	go func() {
		var buf []agent.Message
		for m := range msgs {
			buf = append(buf, m)
		}
		d.done <- buf
	}()
	return d
}

// Messages returns the drained messages in arrival order, blocking until the
// session's message channel closed. Safe to call repeatedly (the buffered
// slice is handed out once); a nil drain yields nil.
func (d *TraceDrain) Messages() []agent.Message {
	if d == nil {
		return nil
	}
	d.once.Do(func() { d.msgs = <-d.done })
	return d.msgs
}

// traceHeader is the first record of every trajectory file.
type traceHeader struct {
	Kind         string    `json:"kind"` // "header"
	TraceID      string    `json:"trace_id"`
	RunID        string    `json:"run_id"`
	GraphVersion int       `json:"graph_version"`
	Seed         int       `json:"seed"`
	Model        string    `json:"model"`
	StartedAt    time.Time `json:"started_at"`
	PromptChars  int       `json:"prompt_chars"`
}

// TraceMessage is the allowlisted per-message record: sequence, type, tool,
// content, input, output and nothing else (same columns as
// serializeLocalTrajectory). Input is the JSON-marshaled tool input map,
// empty when nil.
type TraceMessage struct {
	Kind     string `json:"kind"` // "message"
	Sequence int    `json:"sequence"`
	Type     string `json:"type"`
	Tool     string `json:"tool"`
	Content  string `json:"content"`
	Input    string `json:"input"`
	Output   string `json:"output"`
}

// exploreTraceFooter is the last record of an explore trajectory file.
type exploreTraceFooter struct {
	Kind       string    `json:"kind"` // "footer"
	Found      bool      `json:"found"`
	Rounds     int       `json:"rounds"`
	NodeIDs    []string  `json:"node_ids"`
	Error      string    `json:"error"`
	FinishedAt time.Time `json:"finished_at"`
}

// consolidateTraceFooter is the last record of a consolidate trajectory file.
type consolidateTraceFooter struct {
	Kind       string    `json:"kind"` // "footer"
	Applied    int       `json:"applied"`
	Rejected   int       `json:"rejected"`
	Error      string    `json:"error"`
	FinishedAt time.Time `json:"finished_at"`
}

// RewardTraceRecord is appended to every explore trajectory file of a trace
// when the delayed reward is composed (judge write-back or timeout sweep).
// Miss reports that the composed reward is the miss penalty; JudgeScore is 0
// for swept traces (no judge result ever arrived).
type RewardTraceRecord struct {
	Kind       string  `json:"kind"` // "reward"
	TraceID    string  `json:"trace_id"`
	JudgeScore float64 `json:"judge_score"`
	Reward     float64 `json:"reward"`
	Rounds     int     `json:"rounds"`
	Miss       bool    `json:"miss"`
}

// ExploreTraceMeta carries the fixed coordinates of one explore trajectory
// into the header record. PromptChars records the prompt size only — prompts
// embed staging summaries and are far too large (and untrusted) to persist.
type ExploreTraceMeta struct {
	TraceID      string // query id shared by all K runs of the recall
	RunID        string
	GraphVersion int
	Seed         int
	Model        string
	StartedAt    time.Time
	PromptChars  int
}

// ConsolidateTraceMeta carries the fixed coordinates of one consolidation
// trajectory. Actor identifies the trajectory ("ttt-<idx>" in TTT mode,
// "consolidator" in-place) and doubles as the run id; the trace id is left
// empty (consolidation trajectories have no query id).
type ConsolidateTraceMeta struct {
	GraphVersion int
	Actor        string
	Model        string
	StartedAt    time.Time
	PromptChars  int
}

// WriteExploreTrace writes the full trajectory file of one explore run:
// header, one record per drained message, footer with the final (post
// budget-override) outcome. Best-effort; nil recorder is a no-op.
func (r *TraceRecorder) WriteExploreTrace(meta ExploreTraceMeta, drain *TraceDrain, run ExploreRun) {
	if r == nil {
		return
	}
	path := filepath.Join(r.graphDir, "trajectories", "explore",
		sanitizeFileComponent(meta.TraceID)+"__"+sanitizeFileComponent(meta.RunID)+".jsonl")
	header := traceHeader{
		Kind:         "header",
		TraceID:      meta.TraceID,
		RunID:        meta.RunID,
		GraphVersion: meta.GraphVersion,
		Seed:         meta.Seed,
		Model:        meta.Model,
		StartedAt:    meta.StartedAt,
		PromptChars:  meta.PromptChars,
	}
	footer := exploreTraceFooter{
		Kind:       "footer",
		Found:      run.Found,
		Rounds:     run.Rounds,
		NodeIDs:    run.NodeIDs,
		Error:      run.Error,
		FinishedAt: time.Now().UTC(),
	}
	r.writeTrace(path, header, drain, footer)
}

// WriteConsolidateTrace writes the full trajectory file of one consolidation
// run. runErr is the trajectory failure, if any. Best-effort; nil recorder
// is a no-op.
func (r *TraceRecorder) WriteConsolidateTrace(meta ConsolidateTraceMeta, drain *TraceDrain, applied, rejected int, runErr error) {
	if r == nil {
		return
	}
	path := filepath.Join(r.graphDir, "trajectories", "consolidate",
		sanitizeFileComponent(itoaVersion(meta.GraphVersion))+"__"+sanitizeFileComponent(meta.Actor)+".jsonl")
	header := traceHeader{
		Kind:         "header",
		RunID:        meta.Actor,
		GraphVersion: meta.GraphVersion,
		Model:        meta.Model,
		StartedAt:    meta.StartedAt,
		PromptChars:  meta.PromptChars,
	}
	errStr := ""
	if runErr != nil {
		errStr = runErr.Error()
	}
	footer := consolidateTraceFooter{
		Kind:       "footer",
		Applied:    applied,
		Rejected:   rejected,
		Error:      errStr,
		FinishedAt: time.Now().UTC(),
	}
	r.writeTrace(path, header, drain, footer)
}

// AppendRewardTrace appends rec to every persisted explore trajectory file
// of rec.TraceID (trajectories/explore/<trace_id>__*.jsonl). No matching
// file is not an error: the trajectory may predate recording or the explore
// may have run with recording disabled. It implements the RewardTraceSink
// hook of RewardComposer.
func (r *TraceRecorder) AppendRewardTrace(rec RewardTraceRecord) error {
	if r == nil {
		return nil
	}
	pattern := filepath.Join(r.graphDir, "trajectories", "explore",
		sanitizeFileComponent(rec.TraceID)+"__*.jsonl")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	rec.Kind = "reward"
	var firstErr error
	for _, path := range matches {
		if err := appendJSONL(path, rec); err != nil {
			slog.Warn("trace: reward append failed", "path", path, "trace_id", rec.TraceID, "error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// writeTrace appends the header, one record per drained message and the
// footer to path. The drain is complete by the time the run outcome is
// known (Messages closes before Result resolves), so Messages() does not
// block meaningfully. A nil drain writes header+footer only (e.g. the
// backend call failed before a session existed). The first write failure
// aborts the file and is only logged.
func (r *TraceRecorder) writeTrace(path string, header traceHeader, drain *TraceDrain, footer any) {
	if err := appendJSONL(path, header); err != nil {
		slog.Warn("trace: header write failed", "path", path, "error", err)
		return
	}
	for i, m := range drain.Messages() {
		if err := appendJSONL(path, serializeTraceMessage(i, m)); err != nil {
			slog.Warn("trace: message write failed", "path", path, "sequence", i, "error", err)
			return
		}
	}
	if err := appendJSONL(path, footer); err != nil {
		slog.Warn("trace: footer write failed", "path", path, "error", err)
	}
}

// serializeTraceMessage maps one agent message onto the allowlisted record
// shape. Sequence is the 0-based arrival order in the session stream.
func serializeTraceMessage(seq int, m agent.Message) TraceMessage {
	input := ""
	if m.Input != nil {
		if b, err := json.Marshal(m.Input); err == nil {
			input = string(b)
		}
	}
	return TraceMessage{
		Kind:     "message",
		Sequence: seq,
		Type:     string(m.Type),
		Tool:     m.Tool,
		Content:  m.Content,
		Input:    input,
		Output:   m.Output,
	}
}

// sanitizeFileComponent maps an id onto a safe single path component:
// [A-Za-z0-9._-] are kept, everything else becomes "_".
func sanitizeFileComponent(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

// itoaVersion renders a graph version for file naming ("v3").
func itoaVersion(v int) string {
	return fmt.Sprintf("v%d", v)
}
