package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
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

// DiagnosisReport is the result of an on-demand diagnosis run. Rewards are
// already persisted in the DAG store; the report carries status only.
type DiagnosisReport struct {
	RunID             string             `json:"run_id"`
	CompletedSegments int                `json:"completed_segments"`
	TotalSegments     int                `json:"total_segments"`
	Status            DiagnosisRunStatus `json:"status"`
}

// DiagnosisOnDemandConfig holds the dependencies for the on-demand diagnosis
// flow (Tasks 1-6). When set, Diagnose routes through the persistent-session
// path; when nil, it falls back to the legacy one-shot prompt path.
type DiagnosisOnDemandConfig struct {
	StateStore    *DiagnosisStateStore
	DAGWriter     DiagnosisDAGWriter
	MessagePager  DiagnosisMessagePager
	PiRPC         agentpkg.PiRPCBackend
	ExtensionRoot string
}

func completeDiagnosisRun(ctx context.Context, state *DiagnosisStateStore, runID, topologyHash string) error {
	return state.CompleteRun(ctx, runID, topologyHash)
}

func loadExistingCompletedDiagnosis(
	ctx context.Context,
	state *DiagnosisStateStore,
	projectID, taskID, topologyHash string,
) (DiagnosisReport, bool, error) {
	run, segments, err := state.LoadCompletedRun(ctx, projectID, taskID)
	if errors.Is(err, ErrDiagnosisRunNotFound) {
		return DiagnosisReport{}, false, nil
	}
	if err != nil {
		return DiagnosisReport{}, false, err
	}
	if run.TopologyHash != topologyHash {
		return DiagnosisReport{}, false, nil
	}
	completed := 0
	for _, segment := range segments {
		if segment.Status == SegmentDiagnosisCompleted {
			completed++
		}
	}
	if completed != len(segments) {
		return DiagnosisReport{}, false, fmt.Errorf("diagnosis on-demand: completed run %s has incomplete segments", run.RunID)
	}
	return DiagnosisReport{
		RunID:             run.RunID,
		CompletedSegments: completed,
		TotalSegments:     len(segments),
		Status:            DiagnosisRunCompleted,
	}, true, nil
}

// DiagnosisSegmentTarget is the immutable message-range snapshot used by one
// on-demand run. AssistantSeqs is the authoritative set the tool server must
// score; it is deliberately not reconstructed from a count or numeric range.
type DiagnosisSegmentTarget struct {
	SegmentID     string
	AgentRunID    string
	StartSeq      int32
	EndSeq        int32
	AssistantSeqs []int32
}

// freezeDiagnosisSegmentTargets snapshots the actual messages behind each
// segment before Pi starts. A resumed run must see the same target set; a
// changed message range is rejected rather than silently scoring another turn.
func (r *DiagnosisAgentRunner) freezeDiagnosisSegmentTargets(
	ctx context.Context,
	state *DiagnosisStateStore,
	run DiagnosisRunCheckpoint,
	segmentIDs []string,
) ([]DiagnosisSegmentTarget, error) {
	if r.dagStore == nil || r.messageStore == nil {
		return nil, fmt.Errorf("diagnosis on-demand: stores not configured")
	}
	targets := make([]DiagnosisSegmentTarget, 0, len(segmentIDs))
	for _, segmentID := range segmentIDs {
		segment, err := r.dagStore.GetInteractionDAGSegmentByID(ctx, segmentID)
		if err != nil {
			return nil, fmt.Errorf("diagnosis on-demand: get segment %s: %w", segmentID, err)
		}
		if segment.ProjectID != run.ProjectID {
			return nil, fmt.Errorf("diagnosis on-demand: segment %s is outside project", segmentID)
		}
		messages, err := r.messageStore.MessagesForTaskInRange(
			ctx, segment.AgentRunID, segment.StartSeq, segment.EndSeq,
		)
		if err != nil {
			return nil, fmt.Errorf("diagnosis on-demand: messages for segment %s: %w", segmentID, err)
		}
		assistantSeqs := make([]int32, 0, len(messages))
		for _, message := range messages {
			if message.Type == "assistant" {
				assistantSeqs = append(assistantSeqs, message.Seq)
			}
		}

		checkpoint, err := state.GetSegment(ctx, run.RunID, segmentID)
		if err != nil {
			return nil, fmt.Errorf("diagnosis on-demand: load segment checkpoint %s: %w", segmentID, err)
		}
		if checkpoint.Status == SegmentDiagnosisPending {
			if _, err := state.StartSegmentWithTargets(
				ctx, run.RunID, segmentID, len(messages), assistantSeqs,
			); err != nil {
				return nil, fmt.Errorf("diagnosis on-demand: freeze targets for segment %s: %w", segmentID, err)
			}
		} else if checkpoint.ExpectedMessageCount != len(messages) || !sameInt32s(checkpoint.ExpectedRewardSeqs, assistantSeqs) {
			return nil, fmt.Errorf("diagnosis on-demand: target set changed for segment %s", segmentID)
		}

		targets = append(targets, DiagnosisSegmentTarget{
			SegmentID:     segmentID,
			AgentRunID:    segment.AgentRunID,
			StartSeq:      segment.StartSeq,
			EndSeq:        segment.EndSeq,
			AssistantSeqs: assistantSeqs,
		})
	}
	return targets, nil
}

// DiagnoseOnDemand runs the persistent per-segment diagnosis flow. It creates or
// resumes a diagnosis run, starts the loopback tool server, launches a single
// persistent Pi RPC session with the trusted extension, and processes one
// segment per turn with compaction between segments.
func (r *DiagnosisAgentRunner) DiagnoseOnDemand(ctx context.Context, projectID, taskID string, orderedSegmentIDs []string, cfg DiagnosisOnDemandConfig) (DiagnosisReport, error) {
	topoHash := topologyHashFromIDs(orderedSegmentIDs)

	// Create or resume a diagnosis run.
	runCkpt, segments, err := cfg.StateStore.LoadResumableRun(ctx, projectID, taskID)
	if err != nil && !errors.Is(err, ErrDiagnosisRunNotFound) {
		return DiagnosisReport{}, fmt.Errorf("diagnosis on-demand: load run: %w", err)
	}
	if errors.Is(err, ErrDiagnosisRunNotFound) {
		report, found, completedErr := loadExistingCompletedDiagnosis(ctx, cfg.StateStore, projectID, taskID, topoHash)
		if completedErr != nil {
			return DiagnosisReport{}, completedErr
		}
		if found {
			return report, nil
		}
		// New run.
		runID := fmt.Sprintf("diag-%s-%d", taskID[:min(8, len(taskID))], time.Now().UnixMilli())
		runCkpt, err = cfg.StateStore.CreateRun(ctx, DiagnosisRunCheckpoint{
			RunID:             runID,
			ProjectID:         projectID,
			TaskID:            taskID,
			TopologyHash:      topoHash,
			OrderedSegmentIDs: orderedSegmentIDs,
		})
		if err != nil {
			return DiagnosisReport{}, fmt.Errorf("diagnosis on-demand: create run: %w", err)
		}
		segments, _ = cfg.StateStore.ListSegments(ctx, runCkpt.RunID)
	}
	targets, err := r.freezeDiagnosisSegmentTargets(ctx, cfg.StateStore, runCkpt, orderedSegmentIDs)
	if err != nil {
		return DiagnosisReport{}, err
	}
	segments, err = cfg.StateStore.ListSegments(ctx, runCkpt.RunID)
	if err != nil {
		return DiagnosisReport{}, fmt.Errorf("diagnosis on-demand: list frozen segments: %w", err)
	}

	// Start the loopback tool server.
	server, err := NewDiagnosisToolServer(runCkpt, cfg.StateStore, cfg.MessagePager, cfg.DAGWriter)
	if err != nil {
		return DiagnosisReport{}, fmt.Errorf("diagnosis on-demand: tool server: %w", err)
	}
	if err := server.SetSegmentTargets(targets); err != nil {
		return DiagnosisReport{}, fmt.Errorf("diagnosis on-demand: configure segment targets: %w", err)
	}
	baseURL, err := server.ListenAndServe()
	if err != nil {
		return DiagnosisReport{}, fmt.Errorf("diagnosis on-demand: listen: %w", err)
	}
	defer server.Shutdown(ctx)

	// Generate the trusted extension.
	extPath, err := GenerateDiagnosisPiExtension(cfg.ExtensionRoot, 0o600)
	if err != nil {
		return DiagnosisReport{}, fmt.Errorf("diagnosis on-demand: extension: %w", err)
	}
	defer os.Remove(extPath)

	// Set environment for the Pi process.
	os.Setenv("MULTICA_DIAGNOSIS_API_URL", baseURL)
	os.Setenv("MULTICA_DIAGNOSIS_CAPABILITY_TOKEN", server.BearerToken())
	defer func() {
		os.Unsetenv("MULTICA_DIAGNOSIS_API_URL")
		os.Unsetenv("MULTICA_DIAGNOSIS_CAPABILITY_TOKEN")
	}()

	// Compute missing seqs from the DAG store.
	segInfos := make([]segmentDiagnosisInfo, 0, len(segments))
	completedCount := 0
	for _, seg := range segments {
		if seg.Status == SegmentDiagnosisCompleted {
			completedCount++
			continue
		}
		// Query expected reward count from DAG.
		totalRewards, _ := cfg.DAGWriter.CountDiagnosisStepRewards(ctx, projectID, seg.SegmentID)
		segInfos = append(segInfos, segmentDiagnosisInfo{
			SegmentID:        seg.SegmentID,
			ExpectedMessages: seg.ExpectedMessageCount,
			ExpectedRewards:  seg.ExpectedRewardCount,
			RecordedRewards:  totalRewards,
		})
	}

	if len(segInfos) == 0 {
		if err := completeDiagnosisRun(ctx, cfg.StateStore, runCkpt.RunID, topoHash); err != nil {
			return DiagnosisReport{}, fmt.Errorf("diagnosis on-demand: complete run: %w", err)
		}
		return DiagnosisReport{
			RunID:             runCkpt.RunID,
			CompletedSegments: completedCount,
			TotalSegments:     len(segments),
			Status:            DiagnosisRunCompleted,
		}, nil
	}

	// Start the persistent Pi RPC session.
	bootstrapPrompt := r.buildTopologyBootstrapPrompt(projectID, orderedSegmentIDs, segInfos, segments)
	piSession, err := cfg.PiRPC.Execute(ctx, bootstrapPrompt, agentpkg.ExecOptions{
		Model:                 r.model,
		SystemPrompt:          r.onDemandSystemPrompt(),
		DisableTools:          true,
		TrustedExtensionPaths: []string{extPath},
		TrustedExtensionRoot:  cfg.ExtensionRoot,
		Timeout:               r.timeout,
	})
	if err != nil {
		return DiagnosisReport{}, fmt.Errorf("diagnosis on-demand: pi session: %w", err)
	}

	// Disable Pi's auto-compaction — Multica controls it at segment boundaries.
	if err := cfg.PiRPC.SetAutoCompaction(ctx, false); err != nil {
		slog.Warn("diagnosis on-demand: set auto compaction failed", "error", err)
	}

	result, ok := <-piSession.Result
	if !ok {
		return DiagnosisReport{}, fmt.Errorf("diagnosis on-demand: pi session closed without result")
	}
	if result.Status != "completed" {
		return DiagnosisReport{Status: DiagnosisRunFailed}, fmt.Errorf("diagnosis on-demand: pi session %s: %s", result.Status, result.Error)
	}

	// After the session, verify all segments are completed.
	segments, _ = cfg.StateStore.ListSegments(ctx, runCkpt.RunID)
	completedCount = 0
	for _, seg := range segments {
		if seg.Status == SegmentDiagnosisCompleted {
			completedCount++
		}
	}

	if err := completeDiagnosisRun(ctx, cfg.StateStore, runCkpt.RunID, topoHash); err != nil {
		return DiagnosisReport{}, fmt.Errorf("diagnosis on-demand: complete run: %w", err)
	}

	return DiagnosisReport{
		RunID:             runCkpt.RunID,
		CompletedSegments: completedCount,
		TotalSegments:     len(segments),
		Status:            DiagnosisRunCompleted,
	}, nil
}

type segmentDiagnosisInfo struct {
	SegmentID        string
	ExpectedMessages int
	ExpectedRewards  int
	RecordedRewards  int
}

// topologyHashFromIDs computes a deterministic hash of ordered segment IDs.
func topologyHashFromIDs(ids []string) string {
	data, _ := json.Marshal(ids)
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

// onDemandSystemPrompt builds the system prompt for the on-demand diagnosis
// agent: it instructs Pi to use only the five diagnosis tools, process one
// segment at a time, page messages, record rewards incrementally, and finish
// each segment before moving to the next.
func (r *DiagnosisAgentRunner) onDemandSystemPrompt() string {
	return fmt.Sprintf(`You are a diagnosis agent that evaluates each LLM output (assistant turn) within a collaborative task DAG.

CAPABILITIES — use ONLY these tools:
- multica_get_segment_messages — fetch one page of messages for a segment
- multica_record_step_rewards — persist step scores (0–%d) incrementally
- multica_get_diagnosis_progress — read authoritative server state
- multica_finish_segment — mark a segment complete (server validates coverage)
- multica_complete_diagnosis — finalize after all segments are done

PROCESS each segment:
1. Call multica_get_segment_messages for the segment. Page through ALL messages until Complete=true.
2. Score every assistant turn (0–%d). Call multica_record_step_rewards after each page.
3. When all messages are fetched AND all rewards are recorded, call multica_finish_segment.
4. The server will reject finish until message and reward coverage are both complete.
5. Move to the next segment only after the current one is confirmed completed.

RULES:
- Never fabricate or skip turns — every assistant message must be scored.
- Scores reflect contribution toward the task goal, not message length or style.
- Conflicting rewrites of the same (segment_id, seq) are rejected — use multica_get_diagnosis_progress to reconcile.
- The database is authoritative — if progress disagrees with your memory, trust the server.`, r.scoreMax, r.scoreMax)
}

// buildTopologyBootstrapPrompt assembles the initial prompt containing task
// context, scoring rubric, ordered topology, and segment/step IDs — but NO
// segment message bodies. Messages are fetched on demand via the extension.
func (r *DiagnosisAgentRunner) buildTopologyBootstrapPrompt(projectID string, orderedSegmentIDs []string, infos []segmentDiagnosisInfo, segments []SegmentDiagnosisCheckpoint) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Project: %s\n", projectID))
	sb.WriteString(fmt.Sprintf("Score max: %d\n\n", r.scoreMax))

	sb.WriteString("SEGMENT TOPOLOGY (ordered):\n")
	for i, seg := range segments {
		status := string(seg.Status)
		sb.WriteString(fmt.Sprintf("  %d. segment_id=%s status=%s messages=%d rewards=%d/%d\n",
			i+1, seg.SegmentID, status,
			seg.ExpectedMessageCount, seg.RewardCount, seg.ExpectedRewardCount))
	}
	sb.WriteString("\nINCOMPLETE SEGMENTS to process:\n")
	for _, info := range infos {
		sb.WriteString(fmt.Sprintf("  segment_id=%s expected_messages=%d expected_rewards=%d recorded_rewards=%d\n",
			info.SegmentID, info.ExpectedMessages, info.ExpectedRewards, info.RecordedRewards))
	}
	sb.WriteString("\nStart with the first incomplete segment. Use multica_get_segment_messages to read its messages page by page.\n")
	return sb.String()
}
