package memorygraph

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// DefaultPriorCompressionTimeout bounds the one-shot continuation
// compression call; a slower compressor degrades to a prior-less recall.
const DefaultPriorCompressionTimeout = 60 * time.Second

// PriorBrief is the query-aware, graph-evidence-only digest of a previous
// recall's adopted exploration (spec §5.3): node ids and findings, never
// raw transcripts, credentials, or session internals.
type PriorBrief struct {
	Summary       string   `json:"summary"`
	NodeIDs       []string `json:"node_ids"`
	Observations  []string `json:"observations,omitempty"`
	Rejected      []string `json:"rejected,omitempty"`
	OpenQuestions []string `json:"open_questions,omitempty"`
}

// PriorCompressor distills one adopted transcript into a PriorBrief for
// the next recall's query (spec §5.2: lazy per-B, query-aware).
type PriorCompressor struct {
	backend AgentBackend
	model   string
	timeout time.Duration
}

func NewPriorCompressor(backend AgentBackend, model string, timeout time.Duration) *PriorCompressor {
	if timeout <= 0 {
		timeout = DefaultPriorCompressionTimeout
	}
	return &PriorCompressor{backend: backend, model: model, timeout: timeout}
}

// Compress runs the single LLM call. Any failure is returned as an error;
// the caller degrades to a prior-less recall.
func (c *PriorCompressor) Compress(ctx context.Context, query string, transcript []TraceMessage) (*PriorBrief, error) {
	if c.backend == nil {
		return nil, fmt.Errorf("prior compress: backend not configured")
	}
	if len(transcript) == 0 {
		return nil, fmt.Errorf("prior compress: empty transcript")
	}
	raw, err := json.Marshal(transcript)
	if err != nil {
		return nil, fmt.Errorf("prior compress: transcript: %w", err)
	}
	var b strings.Builder
	b.WriteString("You compress the sanitized exploration transcript of a PREVIOUS memory-graph recall into prior evidence for the CURRENT query.\n\n")
	fmt.Fprintf(&b, "Current query: %s\n\n", query)
	b.WriteString("Rules:\n")
	b.WriteString("- Output graph evidence only: node ids seen in the transcript, findings, rejected branches, open questions.\n")
	b.WriteString("- Never invent node ids; every node id must appear in the transcript.\n")
	b.WriteString("- If nothing in the transcript is relevant to the current query, return empty evidence.\n\n")
	b.WriteString("Transcript (allowlisted message records):\n")
	b.Write(raw)
	b.WriteString("\n\nYour FINAL response must be exactly one JSON object and nothing else:\n")
	b.WriteString(`{"summary":string,"node_ids":[string],"observations":[string],"rejected":[string],"open_questions":[string]}`)

	execCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	session, err := c.backend.Execute(execCtx, b.String(), agent.ExecOptions{
		Model: c.model, ThreadName: "memorygraph-prior-compress", Timeout: c.timeout, EphemeralSession: true,
	})
	if err != nil {
		return nil, fmt.Errorf("prior compress: execute: %w", err)
	}
	// Session.Messages is a 256-cap buffered channel; keep it flowing until
	// it closes (it closes before Result resolves) so long outputs stall
	// nobody. The compressor itself only needs the final result.
	go func() {
		for range session.Messages {
		}
	}()
	result, ok := <-session.Result
	if !ok {
		return nil, fmt.Errorf("prior compress: session ended without a result")
	}
	if result.Status != "completed" {
		return nil, fmt.Errorf("prior compress: session status %q", result.Status)
	}
	var brief PriorBrief
	start, end := strings.Index(result.Output, "{"), strings.LastIndex(result.Output, "}")
	if start < 0 || end < start || json.Unmarshal([]byte(result.Output[start:end+1]), &brief) != nil {
		return nil, fmt.Errorf("prior compress: unparseable output")
	}
	return &brief, nil
}

// PriorRecord is the per-channel continuation state (spec §5.1): the
// latest found recall's sanitized adopted transcript plus the brief cache.
// It lives under <graph_dir>/continuation/<sha1(key)>.json and is replaced
// wholesale by each new found recall; a graph version change invalidates
// it (caller compares GraphVersion, spec D8).
type PriorRecord struct {
	GraphVersion int                   `json:"graph_version"`
	Query        string                `json:"query"`
	CreatedAt    time.Time             `json:"created_at"`
	Transcript   []TraceMessage        `json:"transcript"`
	Briefs       map[string]PriorBrief `json:"briefs,omitempty"`
}

type PriorRecordStore struct{ dir string }

func NewPriorRecordStore(dir string) *PriorRecordStore { return &PriorRecordStore{dir: dir} }

func priorRecordPath(dir, key string) string {
	sum := sha1.Sum([]byte(key))
	return filepath.Join(dir, fmt.Sprintf("%x.json", sum))
}

// Save writes the record atomically (temp file + rename). Errors are the
// caller's to log; continuation is best-effort by design.
func (s *PriorRecordStore) Save(key string, rec PriorRecord) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	path := priorRecordPath(s.dir, key)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load returns nil, nil when no record exists for the key.
func (s *PriorRecordStore) Load(key string) (*PriorRecord, error) {
	data, err := os.ReadFile(priorRecordPath(s.dir, key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rec PriorRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

// NormalizeRecallKey canonicalizes a query for exact-match brief caching
// (spec D11): case-folding with whitespace runs collapsed. Mirrors the
// daemon-side batch coalescing key.
func NormalizeRecallKey(query string) string {
	return strings.Join(strings.Fields(strings.ToLower(query)), " ")
}
