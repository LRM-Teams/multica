package memorygraph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/multica-ai/multica/server/pkg/agent"
)

const (
	// ingestTrajectoryBudgetBytes caps the trajectory text embedded in the
	// summarizer prompt; oldest messages are dropped first.
	ingestTrajectoryBudgetBytes = 16 * 1024
	// extractiveSummaryMaxRunes caps the last-assistant-message excerpt used
	// by the deterministic fallback summary.
	extractiveSummaryMaxRunes = 500
	// defaultIngestTimeout is the per-ingest agent wall-clock timeout.
	defaultIngestTimeout = 2 * time.Minute
)

// SegmentExport is the segment-close payload handed to the ingester by the
// interaction-dag seams (design §5.1). Trajectory is the allowlisted
// task_message snapshot (local path) or an AReaL export; it may be empty —
// the trained seam paths keep the trajectory in the bridge (tensor_ref) and
// the ingester tolerates empty trajectories.
type SegmentExport struct {
	SegmentID    string
	AgentRunID   string
	Trajectory   json.RawMessage
	ClosingEvent string
}

// IngestMetrics is the narrow metrics surface of the Ingester (design §7,
// review R14): result is "ok" (LLM summary), "fallback" (extractive
// fallback), or "error" (staging write failure). *metrics.BusinessMetrics
// satisfies it server-side.
type IngestMetrics interface {
	RecordGraphMemoryIngest(result string)
}

// Ingester writes immutable source segment summaries into
// staging/segments/<segment_id>.md (design §5.1, Q24). Summarization is
// best-effort: any LLM failure falls back to a deterministic extractive
// summary and never fails the ingest.
type Ingester struct {
	store    *Store
	backend  AgentBackend // may be nil: ingest then always uses the extractive fallback
	provider string       // agent CLI provider name (e.g. "pi"); integration-time wiring
	model    string
	timeout  time.Duration
	metrics  IngestMetrics // nil → no metric emission
}

// NewIngester returns an Ingester over the given store. A zero/negative
// timeout falls back to defaultIngestTimeout. backend may be nil.
func NewIngester(store *Store, backend AgentBackend, provider, model string, timeout time.Duration) *Ingester {
	if timeout <= 0 {
		timeout = defaultIngestTimeout
	}
	return &Ingester{
		store:    store,
		backend:  backend,
		provider: provider,
		model:    model,
		timeout:  timeout,
	}
}

// SetMetrics installs the ingest-outcome metrics sink (nil-safe).
func (i *Ingester) SetMetrics(m IngestMetrics) { i.metrics = m }

// recordResult emits the ingest outcome metric (design §7).
func (i *Ingester) recordResult(result string) {
	if i.metrics != nil {
		i.metrics.RecordGraphMemoryIngest(result)
	}
}

// Ingest summarizes one closed segment into the staging area. It is
// idempotent: staging segments are immutable (Q24), so an existing staging
// file for seg.SegmentID short-circuits to nil. Summarization failures never
// propagate; only staging-write failures return an error.
func (i *Ingester) Ingest(ctx context.Context, seg SegmentExport) error {
	if i.store == nil {
		return errors.New("ingest: store not configured")
	}
	if seg.SegmentID == "" {
		return errors.New("ingest: segment_id required")
	}
	// Immutable staging (Q24): already ingested -> no-op (not counted: the
	// metric tracks fresh ingest outcomes only).
	if _, err := i.store.ReadStagingSegment(seg.SegmentID); err == nil {
		return nil
	}

	msgs := extractTrajectoryMessages(seg.Trajectory, ingestTrajectoryBudgetBytes)
	summary, entities, tags, fallback := i.summarize(ctx, seg, msgs)

	content, err := stagingSegmentContent(seg, summary, entities, tags, fallback)
	if err != nil {
		i.recordResult("error")
		return err
	}
	if err := i.store.WriteStagingSegment(seg.SegmentID, content); err != nil {
		// A concurrent ingest won the race: staging is immutable, so this is
		// still a successful no-op for us.
		if _, readErr := i.store.ReadStagingSegment(seg.SegmentID); readErr == nil {
			return nil
		}
		i.recordResult("error")
		return fmt.Errorf("ingest segment %s: %w", seg.SegmentID, err)
	}
	if fallback {
		i.recordResult("fallback")
	} else {
		i.recordResult("ok")
	}
	return nil
}

// summarize returns the segment summary, entities and tags. fallback is true
// when the deterministic extractive summary was used (nil backend, execute
// error, or unparseable/empty output).
func (i *Ingester) summarize(ctx context.Context, seg SegmentExport, msgs []trajectoryMessage) (summary string, entities, tags []string, fallback bool) {
	if i.backend != nil {
		if out, err := i.llmSummary(ctx, seg, msgs); err == nil {
			return out.Summary, out.Entities, out.Tags, false
		}
	}
	return extractiveSummary(msgs), nil, nil, true
}

// ingestSummaryOutput is the strict-JSON final-response contract of the
// summarizer prompt.
type ingestSummaryOutput struct {
	Summary  string   `json:"summary"`
	Entities []string `json:"entities"`
	Tags     []string `json:"tags"`
}

// llmSummary runs one backend.Execute against the summarizer prompt and
// parses the strict-JSON final response.
func (i *Ingester) llmSummary(ctx context.Context, seg SegmentExport, msgs []trajectoryMessage) (*ingestSummaryOutput, error) {
	execCtx := ctx
	cancel := context.CancelFunc(func() {})
	if i.timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, i.timeout)
	}
	defer cancel()

	session, err := i.backend.Execute(execCtx, i.buildPrompt(seg, msgs), agent.ExecOptions{
		Model:            i.model,
		ThreadName:       "memorygraph-ingest",
		Timeout:          i.timeout,
		EphemeralSession: true,
	})
	if err != nil {
		return nil, fmt.Errorf("execute: %w", err)
	}
	for range session.Messages {
	}
	result, ok := <-session.Result
	if !ok {
		return nil, errors.New("agent session ended without a result")
	}
	if result.Status != "completed" {
		reason := strings.TrimSpace(result.Error)
		if reason == "" {
			reason = "agent did not complete: " + result.Status
		}
		return nil, errors.New(reason)
	}
	var out ingestSummaryOutput
	if !extractIngestOutput(result.Output, &out) {
		return nil, errors.New("final response is not a valid summary JSON object")
	}
	out.Summary = strings.TrimSpace(out.Summary)
	if out.Summary == "" {
		return nil, errors.New("summary is empty")
	}
	return &out, nil
}

// buildPrompt assembles the summarizer prompt with the (budget-capped)
// trajectory messages embedded.
func (i *Ingester) buildPrompt(seg SegmentExport, msgs []trajectoryMessage) string {
	var b strings.Builder
	b.WriteString("Summarize one closed agent segment for long-term graph memory.\n\n")
	fmt.Fprintf(&b, "Segment ID: %s\nAgent run ID: %s\nClosing event: %s\n\n", seg.SegmentID, seg.AgentRunID, seg.ClosingEvent)
	if len(msgs) == 0 {
		b.WriteString("Trajectory messages: (none available)\n")
	} else {
		fmt.Fprintf(&b, "Trajectory messages (%d, oldest first):\n", len(msgs))
		for _, m := range msgs {
			fmt.Fprintf(&b, "--- [%s] ---\n%s\n", m.Role, m.Content)
		}
	}
	b.WriteString("\nWrite a compact factual summary of what was done and learned. ")
	b.WriteString("Your FINAL response must be exactly one JSON object and nothing else (no prose, no markdown fences):\n")
	b.WriteString("{\"summary\":string,\"entities\":[string],\"tags\":[string]}\n")
	b.WriteString("summary: 2-6 factual sentences, no narrative. entities: concrete projects, files, tools, or people mentioned. tags: short topical keywords.\n")
	return b.String()
}

// extractIngestOutput parses the strict-JSON final response, tolerating
// surrounding prose by slicing from the first "{" to the last "}" (same
// approach as extractExploreOutput).
func extractIngestOutput(output string, dst *ingestSummaryOutput) bool {
	start := strings.Index(output, "{")
	end := strings.LastIndex(output, "}")
	if start < 0 || end < start {
		return false
	}
	return json.Unmarshal([]byte(output[start:end+1]), dst) == nil
}

// extractiveSummary is the deterministic fallback: a message-count header
// plus the first extractiveSummaryMaxRunes runes of the last assistant
// message.
func extractiveSummary(msgs []trajectoryMessage) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[extractive fallback] trajectory contained %d messages.", len(msgs))
	for j := len(msgs) - 1; j >= 0; j-- {
		if strings.EqualFold(msgs[j].Role, "assistant") {
			fmt.Fprintf(&b, "\n\nLast assistant message (truncated to %d runes):\n%s", extractiveSummaryMaxRunes, firstRunes(strings.TrimSpace(msgs[j].Content), extractiveSummaryMaxRunes))
			break
		}
	}
	return b.String()
}

// stagingFrontmatter is the yaml frontmatter of a staging segment file.
type stagingFrontmatter struct {
	SegmentID    string `yaml:"segment_id"`
	AgentRunID   string `yaml:"agent_run_id"`
	ClosingEvent string `yaml:"closing_event"`
	IngestedAt   string `yaml:"ingested_at"`
	Fallback     bool   `yaml:"fallback"`
}

// stagingSegmentContent renders staging/segments/<segment_id>.md: yaml
// frontmatter followed by the summary body plus entity/tag sections.
func stagingSegmentContent(seg SegmentExport, summary string, entities, tags []string, fallback bool) ([]byte, error) {
	fm, err := yaml.Marshal(stagingFrontmatter{
		SegmentID:    seg.SegmentID,
		AgentRunID:   seg.AgentRunID,
		ClosingEvent: seg.ClosingEvent,
		IngestedAt:   time.Now().UTC().Format(time.RFC3339),
		Fallback:     fallback,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal staging frontmatter for %s: %w", seg.SegmentID, err)
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fm)
	b.WriteString("---\n\n")
	b.WriteString(summary)
	if len(entities) > 0 {
		b.WriteString("\n\n## Entities\n")
		for _, e := range entities {
			fmt.Fprintf(&b, "- %s\n", e)
		}
	}
	if len(tags) > 0 {
		b.WriteString("\n## Tags\n")
		for _, t := range tags {
			fmt.Fprintf(&b, "- %s\n", t)
		}
	}
	return []byte(b.String()), nil
}

// trajectoryMessage is one role+content pair extracted from a trajectory
// export.
type trajectoryMessage struct {
	Role    string
	Content string
}

// extractTrajectoryMessages parses role+content text pairs out of a
// trajectory export and caps the total role+content bytes at budgetBytes,
// dropping the oldest messages first. raw may be a JSON array of message
// objects (role/content; content as string or text-part array), an
// allowlisted task_message snapshot (type/content), a {"messages":[...]}
// wrapper, or empty.
func extractTrajectoryMessages(raw json.RawMessage, budgetBytes int) []trajectoryMessage {
	msgs := parseTrajectoryMessages(raw)
	if budgetBytes <= 0 {
		return msgs
	}
	total := 0
	for _, m := range msgs {
		total += len(m.Role) + len(m.Content)
	}
	for total > budgetBytes && len(msgs) > 0 {
		total -= len(msgs[0].Role) + len(msgs[0].Content)
		msgs = msgs[1:]
	}
	return msgs
}

func parseTrajectoryMessages(raw json.RawMessage) []trajectoryMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(trimmed, &items); err != nil {
		var wrapper struct {
			Messages []json.RawMessage `json:"messages"`
		}
		if werr := json.Unmarshal(trimmed, &wrapper); werr != nil || len(wrapper.Messages) == 0 {
			return nil
		}
		items = wrapper.Messages
	}
	var msgs []trajectoryMessage
	for _, item := range items {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(item, &m); err != nil {
			continue
		}
		role := jsonString(m["role"])
		if role == "" {
			role = jsonString(m["type"]) // task_message snapshot entries
		}
		content := jsonContentText(m["content"])
		if role == "" && content == "" {
			continue
		}
		msgs = append(msgs, trajectoryMessage{Role: role, Content: content})
	}
	return msgs
}

// jsonString decodes a JSON string, returning "" for anything else.
func jsonString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// jsonContentText decodes message content given as a plain string or as an
// array of text parts ([{"type":"text","text":"..."}]).
func jsonContentText(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	var sb strings.Builder
	for _, p := range parts {
		if t := jsonString(p["text"]); t != "" {
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(t)
		}
	}
	return sb.String()
}

// firstRunes returns s shortened to at most n runes.
func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
