package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	agentpkg "github.com/multica-ai/multica/server/pkg/agent"
)

const (
	evolutionReviewPromptVersion         = "evolution-review-v1"
	defaultEvolutionReviewBaseURL        = "https://api.openai.com/v1"
	defaultEvolutionReviewTimeoutSeconds = 60
	maxEvolutionReviewFileBytes          = 8 * 1024
	maxEvolutionReviewPayloadBytes       = 32 * 1024
	maxEvolutionReviewContentBudgetBytes = 24 * 1024
	maxEvolutionReviewTitleBytes         = 200
	maxEvolutionReviewSummaryBytes       = 2000
	maxEvolutionReviewListItems          = 20
	maxEvolutionReviewListItemBytes      = 80
)

const evolutionReviewSystemPrompt = `You are a reviewer for reusable agent memories and skills.
You must not approve secrets, credentials, unsafe paths, or unclear instructions.
Return strict JSON only.
Do not include markdown.
Your decision is advisory. Prefer needs_review when uncertain.
Return exactly one JSON object with this shape:
{
  "decision": "promote | needs_review | reject",
  "confidence": 0.0,
  "risk_level": "low | medium | high",
  "unit_type": "memory | skill | workflow | tool_pattern | preference",
  "title": "string",
  "summary": "string",
  "suggested_tags": [],
  "suggested_task_types": [],
  "suggested_scope": "workspace",
  "risks": [],
  "rationale": "string"
}`

type EvolutionAgentReviewConfig struct {
	Provider       string
	ExecutablePath string
	Model          string
	Timeout        time.Duration
	Backend        agentpkg.Backend
}

type AgentEvolutionReviewer struct {
	provider       string
	executablePath string
	model          string
	timeout        time.Duration
	backend        agentpkg.Backend
}

type EvolutionHTTPReviewConfig struct {
	Provider string
	Model    string
	BaseURL  string
	APIKey   string
	Timeout  time.Duration
	Client   *http.Client
}

type OpenAICompatibleEvolutionReviewer struct {
	provider string
	model    string
	baseURL  string
	apiKey   string
	timeout  time.Duration
	client   *http.Client
}

type staticNeedsReviewEvolutionReviewer struct {
	reason   string
	metadata map[string]any
}

func NewEvolutionReviewerFromEnv() (EvolutionReviewer, bool) {
	if !envBool("EVOLUTION_REVIEW_ENABLED") {
		return NoopEvolutionReviewer{}, false
	}
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("EVOLUTION_REVIEW_PROVIDER")))
	if provider == "" {
		provider = "pi"
	}
	if provider == "openai" || provider == "openai-compatible" {
		reviewer, err := NewOpenAICompatibleEvolutionReviewer(EvolutionHTTPReviewConfig{
			Provider: provider,
			Model:    strings.TrimSpace(os.Getenv("EVOLUTION_REVIEW_MODEL")),
			BaseURL:  strings.TrimSpace(os.Getenv("EVOLUTION_REVIEW_BASE_URL")),
			APIKey:   strings.TrimSpace(os.Getenv("EVOLUTION_REVIEW_API_KEY")),
			Timeout:  envSeconds("EVOLUTION_REVIEW_TIMEOUT_SECONDS", defaultEvolutionReviewTimeoutSeconds),
		})
		if err != nil {
			return misconfiguredEvolutionReviewer("llm_reviewer", err), true
		}
		return reviewer, true
	}

	reviewer, err := NewAgentEvolutionReviewer(EvolutionAgentReviewConfig{
		Provider:       provider,
		ExecutablePath: strings.TrimSpace(os.Getenv("EVOLUTION_REVIEW_AGENT_PATH")),
		Model:          strings.TrimSpace(os.Getenv("EVOLUTION_REVIEW_AGENT_MODEL")),
		Timeout:        envSeconds("EVOLUTION_REVIEW_TIMEOUT_SECONDS", defaultEvolutionReviewTimeoutSeconds),
	})
	if err != nil {
		return misconfiguredEvolutionReviewer("agent_reviewer", err), true
	}
	return reviewer, true
}

func misconfiguredEvolutionReviewer(source string, err error) EvolutionReviewer {
	return staticNeedsReviewEvolutionReviewer{
		reason: "evolution review misconfigured",
		metadata: map[string]any{
			"source": source,
			"kind":   "configuration_error",
			"error":  err.Error(),
		},
	}
}

func NewAgentEvolutionReviewer(cfg EvolutionAgentReviewConfig) (*AgentEvolutionReviewer, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if provider == "" {
		provider = "pi"
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = time.Duration(defaultEvolutionReviewTimeoutSeconds) * time.Second
	}
	backend := cfg.Backend
	if backend == nil {
		created, err := agentpkg.New(provider, agentpkg.Config{ExecutablePath: strings.TrimSpace(cfg.ExecutablePath)})
		if err != nil {
			return nil, err
		}
		backend = created
	}
	return &AgentEvolutionReviewer{provider: provider, executablePath: strings.TrimSpace(cfg.ExecutablePath), model: strings.TrimSpace(cfg.Model), timeout: timeout, backend: backend}, nil
}

func NewOpenAICompatibleEvolutionReviewer(cfg EvolutionHTTPReviewConfig) (*OpenAICompatibleEvolutionReviewer, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	switch provider {
	case "", "openai", "openai-compatible":
		if provider == "" {
			provider = "openai-compatible"
		}
	default:
		return nil, fmt.Errorf("unsupported OpenAI-compatible evolution review provider %q", cfg.Provider)
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		return nil, errors.New("EVOLUTION_REVIEW_MODEL is required when OpenAI-compatible review is enabled")
	}
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		return nil, errors.New("EVOLUTION_REVIEW_API_KEY is required when OpenAI-compatible review is enabled")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultEvolutionReviewBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid EVOLUTION_REVIEW_BASE_URL %q", cfg.BaseURL)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = time.Duration(defaultEvolutionReviewTimeoutSeconds) * time.Second
	}
	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}
	return &OpenAICompatibleEvolutionReviewer{provider: provider, model: model, baseURL: baseURL, apiKey: apiKey, timeout: timeout, client: client}, nil
}

func (r staticNeedsReviewEvolutionReviewer) Review(context.Context, EvolutionReviewInput) (EvolutionReviewResult, error) {
	return EvolutionReviewResult{
		Decision:   EvolutionReviewNeedsReview,
		Confidence: 0,
		RiskLevel:  EvolutionReviewRiskMedium,
		Rationale:  r.reason,
		Metadata:   r.metadata,
	}, nil
}

func (r *AgentEvolutionReviewer) Review(ctx context.Context, input EvolutionReviewInput) (EvolutionReviewResult, error) {
	payloadBytes, payloadMeta := evolutionReviewPayload(input)
	metadata := map[string]any{
		"source":         "agent_reviewer",
		"provider":       r.provider,
		"prompt_version": evolutionReviewPromptVersion,
	}
	if r.model != "" {
		metadata["model"] = r.model
	}
	for key, value := range payloadMeta {
		metadata[key] = value
	}

	customArgs := []string(nil)
	if r.provider == "pi" {
		customArgs = []string{"--no-tools"}
	}
	session, err := r.backend.Execute(ctx, string(payloadBytes), agentpkg.ExecOptions{
		Model:        r.model,
		SystemPrompt: evolutionReviewSystemPrompt,
		ThreadName:   "evolution-review",
		Timeout:      r.timeout,
		CustomArgs:   customArgs,
	})
	if err != nil {
		return evolutionReviewFallback("agent_start_error", err.Error(), metadata), nil
	}
	for range session.Messages {
	}
	result, ok := <-session.Result
	if !ok {
		return evolutionReviewFallback("agent_error", "agent returned no result", metadata), nil
	}
	if result.SessionID != "" {
		metadata["session_id"] = result.SessionID
	}
	if result.Status != "completed" {
		reason := result.Error
		if strings.TrimSpace(reason) == "" {
			reason = "agent review did not complete: " + result.Status
		}
		return evolutionReviewFallback("agent_error", reason, metadata), nil
	}
	if strings.TrimSpace(result.Output) == "" {
		return evolutionReviewFallback("empty_response", "agent returned no review content", metadata), nil
	}
	return parseEvolutionReviewResult(result.Output, metadata), nil
}

func (r *OpenAICompatibleEvolutionReviewer) Review(ctx context.Context, input EvolutionReviewInput) (EvolutionReviewResult, error) {
	payloadBytes, payloadMeta := evolutionReviewPayload(input)
	requestBody, err := json.Marshal(openAIChatCompletionRequest{
		Model: r.model,
		Messages: []openAIChatCompletionMessage{
			{Role: "system", Content: evolutionReviewSystemPrompt},
			{Role: "user", Content: string(payloadBytes)},
		},
		Temperature:    0,
		ResponseFormat: map[string]string{"type": "json_object"},
	})
	if err != nil {
		return evolutionReviewFallback("request_encode_error", err.Error(), r.metadata(payloadMeta)), nil
	}

	callCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, r.chatCompletionsURL(), bytes.NewReader(requestBody))
	if err != nil {
		return evolutionReviewFallback("request_build_error", err.Error(), r.metadata(payloadMeta)), nil
	}
	req.Header.Set("Authorization", "Bearer "+r.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		kind := "provider_error"
		if errors.Is(callCtx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			kind = "timeout"
		}
		return evolutionReviewFallback(kind, err.Error(), r.metadata(payloadMeta)), nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return evolutionReviewFallback("response_read_error", err.Error(), r.metadata(payloadMeta)), nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return evolutionReviewFallback("provider_error", fmt.Sprintf("provider returned HTTP %d", resp.StatusCode), r.metadata(payloadMeta, map[string]any{"http_status": resp.StatusCode, "response": truncateUTF8Bytes(string(body), 1024)})), nil
	}

	var completion openAIChatCompletionResponse
	if err := json.Unmarshal(body, &completion); err != nil {
		return evolutionReviewFallback("response_decode_error", err.Error(), r.metadata(payloadMeta)), nil
	}
	if completion.Error != nil && strings.TrimSpace(completion.Error.Message) != "" {
		return evolutionReviewFallback("provider_error", completion.Error.Message, r.metadata(payloadMeta)), nil
	}
	if len(completion.Choices) == 0 || strings.TrimSpace(completion.Choices[0].Message.Content) == "" {
		return evolutionReviewFallback("empty_response", "provider returned no review content", r.metadata(payloadMeta)), nil
	}
	return parseEvolutionReviewResult(completion.Choices[0].Message.Content, r.metadata(payloadMeta)), nil
}

func (r *OpenAICompatibleEvolutionReviewer) chatCompletionsURL() string {
	return r.baseURL + "/chat/completions"
}

func (r *OpenAICompatibleEvolutionReviewer) metadata(extra ...map[string]any) map[string]any {
	metadata := map[string]any{
		"source":         "llm_reviewer",
		"provider":       r.provider,
		"model":          r.model,
		"prompt_version": evolutionReviewPromptVersion,
	}
	for _, item := range extra {
		for key, value := range item {
			metadata[key] = value
		}
	}
	return metadata
}

type openAIChatCompletionRequest struct {
	Model          string                        `json:"model"`
	Messages       []openAIChatCompletionMessage `json:"messages"`
	Temperature    float64                       `json:"temperature"`
	ResponseFormat map[string]string             `json:"response_format,omitempty"`
}

type openAIChatCompletionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatCompletionResponse struct {
	Choices []struct {
		Message openAIChatCompletionMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

type evolutionReviewResponse struct {
	Decision           string   `json:"decision"`
	Confidence         float64  `json:"confidence"`
	RiskLevel          string   `json:"risk_level"`
	UnitType           string   `json:"unit_type"`
	Title              string   `json:"title"`
	Summary            string   `json:"summary"`
	SuggestedTags      []string `json:"suggested_tags"`
	SuggestedTaskTypes []string `json:"suggested_task_types"`
	SuggestedScope     string   `json:"suggested_scope"`
	Risks              []string `json:"risks"`
	Rationale          string   `json:"rationale"`
}

func evolutionReviewPayload(input EvolutionReviewInput) ([]byte, map[string]any) {
	remaining := maxEvolutionReviewContentBudgetBytes
	content, contentTruncated := truncateForReviewBudget(input.Content, maxEvolutionReviewFileBytes, &remaining)
	files := make([]map[string]any, 0, len(input.Files))
	for _, file := range input.Files {
		fileContent, truncated := truncateForReviewBudget(file.Content, maxEvolutionReviewFileBytes, &remaining)
		files = append(files, map[string]any{
			"path":       truncateUTF8Bytes(file.Path, 512),
			"mime_type":  truncateUTF8Bytes(file.MimeType, 128),
			"size_bytes": file.SizeBytes,
			"content":    fileContent,
			"truncated":  truncated,
		})
	}
	payload := map[string]any{
		"unit_type":         input.UnitType,
		"title":             truncateUTF8Bytes(input.Title, maxEvolutionReviewTitleBytes),
		"summary":           truncateUTF8Bytes(input.Summary, maxEvolutionReviewSummaryBytes),
		"content":           content,
		"content_truncated": contentTruncated,
		"sensitivity":       input.Sensitivity,
		"confidence":        input.Confidence,
		"suggested_scope":   input.SuggestedScope,
		"tags":              sanitizeReviewStringList(input.Tags, maxEvolutionReviewListItems, maxEvolutionReviewListItemBytes),
		"tools":             sanitizeReviewStringList(input.Tools, maxEvolutionReviewListItems, maxEvolutionReviewListItemBytes),
		"task_types":        sanitizeReviewStringList(input.TaskTypes, maxEvolutionReviewListItems, maxEvolutionReviewListItemBytes),
		"project_types":     sanitizeReviewStringList(input.ProjectTypes, maxEvolutionReviewListItems, maxEvolutionReviewListItemBytes),
		"languages":         sanitizeReviewStringList(input.Languages, maxEvolutionReviewListItems, maxEvolutionReviewListItemBytes),
		"frameworks":        sanitizeReviewStringList(input.Frameworks, maxEvolutionReviewListItems, maxEvolutionReviewListItemBytes),
		"files":             files,
		"deterministic_validation": map[string]any{
			"hard_reject_passed":                  true,
			"dedupe_hash_present":                 true,
			"reviewer_receives_after_secret_scan": true,
		},
	}
	encoded, _ := json.Marshal(payload)
	for len(encoded) > maxEvolutionReviewPayloadBytes && len(files) > 0 {
		last := files[len(files)-1]
		last["content"] = ""
		last["truncated"] = true
		files = files[:len(files)-1]
		payload["files"] = files
		encoded, _ = json.Marshal(payload)
	}
	if len(encoded) > maxEvolutionReviewPayloadBytes {
		payload["content"] = ""
		payload["content_truncated"] = true
		encoded, _ = json.Marshal(payload)
	}
	return encoded, map[string]any{
		"payload_bytes":     len(encoded),
		"content_truncated": contentTruncated,
		"file_count":        len(input.Files),
		"files_sent":        len(files),
	}
}

func parseEvolutionReviewResult(content string, metadata map[string]any) EvolutionReviewResult {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "{") {
		return evolutionReviewFallback("invalid_json", "review content is not a JSON object", metadata)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return evolutionReviewFallback("invalid_json", err.Error(), metadata)
	}
	var response evolutionReviewResponse
	if err := json.Unmarshal([]byte(trimmed), &response); err != nil {
		return evolutionReviewFallback("invalid_json", err.Error(), metadata)
	}

	decision, decisionOK := normalizeEvolutionReviewDecision(response.Decision)
	riskLevel, riskOK := normalizeEvolutionReviewRiskLevel(response.RiskLevel)
	if !decisionOK || !riskOK {
		decision = EvolutionReviewNeedsReview
		if !riskOK {
			riskLevel = EvolutionReviewRiskMedium
		}
	}
	confidence := clampReviewConfidence(response.Confidence)
	rationale := truncateUTF8Bytes(strings.TrimSpace(response.Rationale), maxEvolutionReviewSummaryBytes)
	if rationale == "" {
		rationale = "reviewer returned no rationale"
	}
	resultMetadata := copyReviewMetadata(metadata)
	resultMetadata["raw_decision"] = response.Decision
	resultMetadata["raw_risk_level"] = response.RiskLevel
	resultMetadata["unit_type"] = response.UnitType
	if !decisionOK {
		resultMetadata["normalized_decision_reason"] = "unknown decision"
	}
	if !riskOK {
		resultMetadata["normalized_risk_reason"] = "unknown risk level"
	}

	return EvolutionReviewResult{
		Decision:           decision,
		Confidence:         confidence,
		RiskLevel:          riskLevel,
		Title:              truncateUTF8Bytes(strings.TrimSpace(response.Title), maxEvolutionReviewTitleBytes),
		Summary:            truncateUTF8Bytes(strings.TrimSpace(response.Summary), maxEvolutionReviewSummaryBytes),
		SuggestedTags:      sanitizeReviewStringList(response.SuggestedTags, 12, 64),
		SuggestedTaskTypes: sanitizeReviewStringList(response.SuggestedTaskTypes, 12, 64),
		SuggestedScope:     truncateUTF8Bytes(strings.TrimSpace(response.SuggestedScope), 64),
		Risks:              sanitizeReviewStringList(response.Risks, 12, 200),
		Rationale:          rationale,
		Metadata:           resultMetadata,
	}
}

func evolutionReviewFallback(kind, reason string, metadata map[string]any) EvolutionReviewResult {
	fallbackMetadata := copyReviewMetadata(metadata)
	fallbackMetadata["kind"] = kind
	fallbackMetadata["reason"] = reason
	return EvolutionReviewResult{
		Decision:   EvolutionReviewNeedsReview,
		Confidence: 0,
		RiskLevel:  EvolutionReviewRiskMedium,
		Rationale:  "evolution review requires manual review: " + reason,
		Metadata:   fallbackMetadata,
	}
}

func normalizeEvolutionReviewDecision(value string) (EvolutionReviewDecision, bool) {
	switch strings.TrimSpace(value) {
	case string(EvolutionReviewPromote):
		return EvolutionReviewPromote, true
	case string(EvolutionReviewNeedsReview):
		return EvolutionReviewNeedsReview, true
	case string(EvolutionReviewReject):
		return EvolutionReviewReject, true
	default:
		return EvolutionReviewNeedsReview, false
	}
}

func normalizeEvolutionReviewRiskLevel(value string) (EvolutionReviewRiskLevel, bool) {
	switch strings.TrimSpace(value) {
	case string(EvolutionReviewRiskLow):
		return EvolutionReviewRiskLow, true
	case string(EvolutionReviewRiskMedium):
		return EvolutionReviewRiskMedium, true
	case string(EvolutionReviewRiskHigh):
		return EvolutionReviewRiskHigh, true
	default:
		return EvolutionReviewRiskMedium, false
	}
}

func clampReviewConfidence(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func truncateForReviewBudget(value string, maxPerItem int, remaining *int) (string, bool) {
	limit := maxPerItem
	if remaining != nil && *remaining < limit {
		limit = *remaining
	}
	if limit < 0 {
		limit = 0
	}
	truncated := len(value) > limit
	out := truncateUTF8Bytes(value, limit)
	if remaining != nil {
		*remaining -= len(out)
	}
	return out, truncated
}

func sanitizeReviewStringList(values []string, maxItems, maxBytes int) []string {
	out := make([]string, 0, min(len(values), maxItems))
	seen := map[string]struct{}{}
	for _, value := range values {
		trimmed := truncateUTF8Bytes(strings.TrimSpace(value), maxBytes)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
		if len(out) >= maxItems {
			break
		}
	}
	return out
}

func truncateUTF8Bytes(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	truncated := value[:maxBytes]
	for !utf8.ValidString(truncated) && len(truncated) > 0 {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated
}

func copyReviewMetadata(in map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range in {
		out[key] = value
	}
	return out
}

func envBool(name string) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func envSeconds(name string, def int) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return time.Duration(def) * time.Second
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return time.Duration(def) * time.Second
	}
	return time.Duration(seconds) * time.Second
}
