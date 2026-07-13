package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	agentpkg "github.com/multica-ai/multica/server/pkg/agent"
)

// Prompt-building budgets (the per-message / per-segment-byte / turn caps live
// in diagnosis_tools.go). maxDiagnosisSegments bounds how many segments are
// surfaced so a large DAG cannot overflow the prompt; maxDiagnosisPayloadBytes
// is a final byte ceiling that drops trailing segments if exceeded (mirrors
// evolution_review_provider.go's payload-shrinking); maxDiagnosisContextBytes
// truncates the task goal/gold.
const (
	maxDiagnosisSegments     = 10
	maxDiagnosisPayloadBytes = 64 * 1024
	maxDiagnosisContextBytes = 8 * 1024
)

type StepReward struct {
	SegmentID string `json:"segment_id"`
	Seq       int    `json:"seq"`
	Score     int    `json:"score"`
	Rationale string `json:"rationale"`
}

type DiagnosisAgentConfig struct {
	Provider       string
	ExecutablePath string
	Model          string
	Timeout        time.Duration
	ScoreMax       int
	Backend        agentpkg.Backend
	// DAGStore + MessageStore let the runner fetch the interaction DAG, per-
	// segment LLM messages, and the root-task context to build the rich prompt.
	// They may be nil at construction (constructor-only uses, e.g. systemPrompt);
	// Diagnose errors if they are unset.
	DAGStore     InteractionDAGStore
	MessageStore MessageStore
}

type DiagnosisAgentRunner struct {
	provider       string
	executablePath string
	model          string
	timeout        time.Duration
	scoreMax       int
	backend        agentpkg.Backend
	dagStore       InteractionDAGStore
	messageStore   MessageStore
}

// NewDiagnosisAgentRunner constructs a diagnosis runner. It returns an error
// if no Backend is supplied and the agent backend cannot be created, rather
// than returning a runner that silently fails at Diagnose time.
func NewDiagnosisAgentRunner(cfg DiagnosisAgentConfig) (*DiagnosisAgentRunner, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if provider == "" {
		provider = "pi"
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	scoreMax := cfg.ScoreMax
	if scoreMax <= 0 {
		scoreMax = 10
	}
	backend := cfg.Backend
	if backend == nil {
		created, err := agentpkg.New(provider, agentpkg.Config{ExecutablePath: strings.TrimSpace(cfg.ExecutablePath)})
		if err != nil {
			return nil, fmt.Errorf("diagnosis agent: create %q backend: %w", provider, err)
		}
		backend = created
	}
	return &DiagnosisAgentRunner{provider: provider, executablePath: strings.TrimSpace(cfg.ExecutablePath), model: strings.TrimSpace(cfg.Model), timeout: timeout, scoreMax: scoreMax, backend: backend, dagStore: cfg.DAGStore, messageStore: cfg.MessageStore}, nil
}

// systemPrompt builds the diagnosis system prompt, embedding the concrete
// [0, scoreMax] scoring range so the model scores each turn within valid
// bounds rather than an unspecified "specified range".
func (r *DiagnosisAgentRunner) systemPrompt() string {
	return fmt.Sprintf(`You are a diagnosis agent that evaluates the contribution of each LLM output (assistant turn) to completing a collaborative task.
You receive a JSON payload containing: the task context (goal and gold/acceptance criteria), the interaction DAG of segments and edges, and the per-segment message turns.
For each assistant/LLM output turn present in the segments, assign an integer score between 0 and %d inclusive, where higher scores indicate more valuable contributions toward the task goal.
Return only a JSON array of step reward objects. Do not include any other text or markdown.
Each object must have these fields:
- "segment_id": string identifier of the segment containing the turn
- "seq": positive integer sequence number of the turn within the segment
- "score": integer score between 0 and %d inclusive
- "rationale": string explaining the score`, r.scoreMax, r.scoreMax)
}

func parseStepRewards(output string, scoreMax int) ([]StepReward, error) {
	trimmed := strings.TrimSpace(output)
	// Strip ```json and ``` fences
	if strings.HasPrefix(trimmed, "```json") {
		trimmed = strings.TrimPrefix(trimmed, "```json")
	} else if strings.HasPrefix(trimmed, "```") {
		trimmed = strings.TrimPrefix(trimmed, "```")
	}
	if strings.HasSuffix(trimmed, "```") {
		trimmed = strings.TrimSuffix(trimmed, "```")
	}
	trimmed = strings.TrimSpace(trimmed)

	var raw []StepReward
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil, err
	}

	// Process each step reward, clamp score, skip seq < 1, and deduplicate (last occurrence wins)
	seen := make(map[string]int) // key: segment_id+":"+seq, value: index in result
	result := make([]StepReward, 0, len(raw))
	for _, r := range raw {
		if r.Seq < 1 {
			continue
		}
		// Clamp score to [0, scoreMax]
		if r.Score < 0 {
			r.Score = 0
		} else if r.Score > scoreMax {
			r.Score = scoreMax
		}
		key := fmt.Sprintf("%s:%d", r.SegmentID, r.Seq)
		if idx, ok := seen[key]; ok {
			// Replace existing entry
			result[idx] = r
		} else {
			// Add new entry
			seen[key] = len(result)
			result = append(result, r)
		}
	}

	return result, nil
}

// Diagnose runs the diagnosis agent for a project and returns one StepReward
// per LLM output. The runner fetches the interaction DAG, per-segment messages,
// and the root-task context via the read-only Go helpers, embeds them in a JSON
// prompt (mirrors evolution_review_provider.go), and runs the Pi agent with
// --no-tools. workspaceID is the dispatch workspace; the caller already has it.
func (r *DiagnosisAgentRunner) Diagnose(ctx context.Context, projectID, workspaceID string) ([]StepReward, error) {
	if r.backend == nil {
		return nil, fmt.Errorf("diagnosis agent backend not initialized")
	}
	if r.dagStore == nil || r.messageStore == nil {
		return nil, fmt.Errorf("diagnosis agent stores not configured")
	}
	projectUUID, err := util.ParseUUID(projectID)
	if err != nil {
		return nil, fmt.Errorf("diagnosis agent: invalid project_id %q: %w", projectID, err)
	}
	workspaceUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("diagnosis agent: invalid workspace_id %q: %w", workspaceID, err)
	}

	prompt, err := r.buildDiagnosisPrompt(ctx, projectUUID, workspaceUUID)
	if err != nil {
		return nil, err
	}

	customArgs := []string(nil)
	if r.provider == "pi" {
		customArgs = []string{"--no-tools"}
	}
	session, err := r.backend.Execute(ctx, prompt, agentpkg.ExecOptions{
		Model:        r.model,
		SystemPrompt: r.systemPrompt(),
		ThreadName:   "diagnosis",
		Timeout:      r.timeout,
		CustomArgs:   customArgs,
	})
	if err != nil {
		return nil, err
	}
	result, ok := <-session.Result
	if !ok {
		return nil, fmt.Errorf("diagnosis agent returned no result")
	}
	if result.Status != "completed" {
		reason := result.Error
		if strings.TrimSpace(reason) == "" {
			reason = "diagnosis did not complete: " + result.Status
		}
		return nil, fmt.Errorf("%s", reason)
	}
	if strings.TrimSpace(result.Output) == "" {
		return nil, fmt.Errorf("diagnosis agent returned no content")
	}
	return parseStepRewards(result.Output, r.scoreMax)
}

// buildDiagnosisPrompt assembles the JSON payload sent to the diagnosis agent:
// task context (goal/gold), the segment DAG, and per-segment LLM messages. It
// caps the segment count and drops trailing segments if the encoded payload
// still exceeds maxDiagnosisPayloadBytes.
func (r *DiagnosisAgentRunner) buildDiagnosisPrompt(ctx context.Context, projectID, workspaceID pgtype.UUID) (string, error) {
	segments, edges, err := GetInteractionDAG(ctx, r.dagStore, r.messageStore, workspaceID, projectID)
	if err != nil {
		return "", fmt.Errorf("diagnosis agent: fetch interaction dag: %w", err)
	}
	if len(segments) == 0 {
		return "", fmt.Errorf("diagnosis agent: no segments for project %s", projectID)
	}
	// Bound the segment count before fetching messages so a large DAG cannot
	// overflow the prompt.
	if len(segments) > maxDiagnosisSegments {
		segments = segments[:maxDiagnosisSegments]
	}

	taskCtx := r.resolveTaskContext(ctx, segments, edges, workspaceID)

	segEntries := make([]map[string]any, 0, len(segments))
	for _, seg := range segments {
		msgs, err := GetSegmentMessages(ctx, r.dagStore, r.messageStore, workspaceID, seg.SegmentID)
		if err != nil {
			return "", fmt.Errorf("diagnosis agent: fetch messages for segment %s: %w", seg.SegmentID, err)
		}
		msgEntries := make([]map[string]any, 0, len(msgs))
		for _, m := range msgs {
			msgEntries = append(msgEntries, map[string]any{
				"seq":       m.Seq,
				"type":      m.Type,
				"content":   m.Content,
				"truncated": m.Truncated,
			})
		}
		segEntries = append(segEntries, map[string]any{
			"segment_id":   seg.SegmentID,
			"agent_run_id": seg.AgentRunID,
			"start_seq":    seg.StartSeq,
			"end_seq":      seg.EndSeq,
			"messages":     msgEntries,
		})
	}

	edgeEntries := make([]map[string]any, 0, len(edges))
	for _, e := range edges {
		edgeEntries = append(edgeEntries, map[string]any{
			"src_segment_id": e.SrcSegmentID,
			"dst_segment_id": e.DstSegmentID,
			"type":           e.Type,
		})
	}

	payload := map[string]any{
		"project_id": projectID.String(),
		"score_max":  r.scoreMax,
		"task_context": map[string]any{
			"goal":         truncateUTF8Bytes(taskCtx.Goal, maxDiagnosisContextBytes),
			"gold_context": truncateUTF8Bytes(taskCtx.GoldContext, maxDiagnosisContextBytes),
		},
		"segments": segEntries,
		"edges":    edgeEntries,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("diagnosis agent: encode prompt: %w", err)
	}
	// Drop trailing segments if the encoded payload still exceeds the byte
	// ceiling (mirrors evolution_review_provider.go's payload shrinking).
	for len(encoded) > maxDiagnosisPayloadBytes && len(segEntries) > 0 {
		segEntries = segEntries[:len(segEntries)-1]
		payload["segments"] = segEntries
		if encoded, err = json.Marshal(payload); err != nil {
			return "", fmt.Errorf("diagnosis agent: encode prompt: %w", err)
		}
	}
	return string(encoded), nil
}

// resolveTaskContext returns the goal/gold for the root collaborative task. The
// root segment is the one with no incoming edge; per D8 its AgentRunID is the
// root task ID, so GetTaskContext resolves the root task's issue. Errors / no
// root are soft-failed to an empty TaskContext (context is optional grounding,
// not required to score).
func (r *DiagnosisAgentRunner) resolveTaskContext(ctx context.Context, segments []SegmentRow, edges []EdgeRow, workspaceID pgtype.UUID) TaskContext {
	dstSet := make(map[string]struct{}, len(edges))
	for _, e := range edges {
		dstSet[e.DstSegmentID] = struct{}{}
	}
	for _, seg := range segments {
		if _, isDst := dstSet[seg.SegmentID]; isDst {
			continue
		}
		tc, err := GetTaskContext(ctx, r.messageStore, workspaceID, seg.AgentRunID)
		if err != nil {
			return TaskContext{}
		}
		return tc
	}
	return TaskContext{}
}
