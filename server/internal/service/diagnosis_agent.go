package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentpkg "github.com/multica-ai/multica/server/pkg/agent"
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
}

type DiagnosisAgentRunner struct {
	provider       string
	executablePath string
	model          string
	timeout        time.Duration
	scoreMax       int
	backend        agentpkg.Backend
}

const diagnosisSystemPrompt = `You are a diagnosis agent that evaluates the contribution of each LLM output (turn) to completing a task.
Score each turn with an integer in the specified range, where higher scores indicate more valuable contributions.
Return only a JSON array of step reward objects. Do not include any other text or markdown.
Each object must have these fields:
- "segment_id": string identifier of the segment
- "seq": positive integer sequence number of the turn within the segment
- "score": integer score within the specified range
- "rationale": string explaining the score`

func NewDiagnosisAgentRunner(cfg DiagnosisAgentConfig) *DiagnosisAgentRunner {
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
			// Backend creation failed, but we'll return the runner anyway (Diagnose will handle error)
			return &DiagnosisAgentRunner{provider: provider, executablePath: strings.TrimSpace(cfg.ExecutablePath), model: strings.TrimSpace(cfg.Model), timeout: timeout, scoreMax: scoreMax, backend: nil}
		}
		backend = created
	}
	return &DiagnosisAgentRunner{provider: provider, executablePath: strings.TrimSpace(cfg.ExecutablePath), model: strings.TrimSpace(cfg.Model), timeout: timeout, scoreMax: scoreMax, backend: backend}
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

func (r *DiagnosisAgentRunner) Diagnose(ctx context.Context, projectID string) ([]StepReward, error) {
	// TODO: Build the actual prompt using project ID (not implemented yet; stub)
	prompt := fmt.Sprintf("Diagnose project %s. ScoreMax is %d.", projectID, r.scoreMax)

	if r.backend == nil {
		return nil, fmt.Errorf("diagnosis agent backend not initialized")
	}

	customArgs := []string(nil)
	if r.provider == "pi" {
		customArgs = []string{"--no-tools"}
	}
	session, err := r.backend.Execute(ctx, prompt, agentpkg.ExecOptions{
		Model:        r.model,
		SystemPrompt: diagnosisSystemPrompt,
		ThreadName:   "diagnosis",
		Timeout:      r.timeout,
		CustomArgs:   customArgs,
	})
	if err != nil {
		return nil, err
	}
	for range session.Messages {
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
