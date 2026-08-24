package memorygraph

import (
	"context"
	"encoding/json"
	"fmt"
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
