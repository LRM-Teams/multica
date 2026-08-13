package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// OfflineNormalizationVersion is the authoritative interpretation of typed
// provider-neutral blocks and provider field mappings for offline export.
const OfflineNormalizationVersion = "1"

// MultiCA offline exporter exclusion reasons (request-level auth/snapshot errors
// are never represented here).
const (
	OfflineReasonTrainingIneligible       = "training_ineligible"
	OfflineReasonStopReasonLength         = "stop_reason_length"
	OfflineReasonResponseIncomplete       = "response_incomplete"
	OfflineReasonWrongModeOnlineRL        = "wrong_mode_online_rl"
	OfflineReasonWrongModeNone            = "wrong_mode_none"
	OfflineReasonCallNotInSnapshot        = "call_not_in_snapshot"
	OfflineReasonNormalizationUnsupported = "normalization_unsupported"
	OfflineReasonRawPayloadUnavailable    = "raw_payload_unavailable"
	OfflineReasonHashMismatch             = "hash_mismatch"
)

const (
	offlineStatusTrajectory = "trajectory"
	offlineStatusExcluded   = "excluded"
)

// OfflineTrajectoryService resolves snapshot-bound offline trajectories from
// frozen provider-call material without exposing raw provider payloads.
type OfflineTrajectoryService struct {
	queries *db.Queries
}

func NewOfflineTrajectoryService(queries *db.Queries) *OfflineTrajectoryService {
	return &OfflineTrajectoryService{queries: queries}
}

// OfflineResolveRequest is the authorized, snapshot-bound resolve input.
type OfflineResolveRequest struct {
	RunID       pgtype.UUID
	WorkspaceID pgtype.UUID
	SnapshotID  string
	CallIDs     []string
}

// OfflineResolveLine is one NDJSON result line.
type OfflineResolveLine struct {
	CallID     string                       `json:"call_id"`
	Status     string                       `json:"status"`
	Reason     string                       `json:"reason,omitempty"`
	Details    map[string]any               `json:"details,omitempty"`
	Trajectory *NormalizedOfflineTrajectory `json:"trajectory,omitempty"`
}

// NormalizedOfflineTrajectory is the provider-neutral typed export shape.
type NormalizedOfflineTrajectory struct {
	NormalizationVersion string              `json:"normalization_version"`
	System               *NormalizedContent  `json:"system,omitempty"`
	Messages             []NormalizedMessage `json:"messages"`
	Tools                []NormalizedTool    `json:"tools"`
	GenerationConfig     map[string]any      `json:"generation_config,omitempty"`
	Output               NormalizedMessage   `json:"output"`
	Provider             NormalizedProvider  `json:"provider"`
	RequestHash          string              `json:"request_hash"`
	ResponseHash         string              `json:"response_hash"`
}

type NormalizedContent struct {
	Blocks []NormalizedBlock `json:"blocks"`
}

type NormalizedMessage struct {
	Role   string            `json:"role"`
	Blocks []NormalizedBlock `json:"blocks"`
}

type NormalizedBlock struct {
	Type       string            `json:"type"`
	Text       string            `json:"text,omitempty"`
	Thinking   string            `json:"thinking,omitempty"`
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Arguments  json.RawMessage   `json:"arguments,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	Content    []NormalizedBlock `json:"content,omitempty"`
}

type NormalizedTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type NormalizedProvider struct {
	Name    string `json:"name"`
	Model   string `json:"model"`
	APIKind string `json:"api_kind"`
}

// OfflineCallSource is one frozen snapshot member with training-mode context.
// Raw provider material stays inside the service boundary and never enters the
// exported NDJSON line.
type OfflineCallSource struct {
	CallID                string
	TrainingMode          string
	Provider              string
	Model                 string
	APIKind               string
	RawProviderRequest    []byte
	FinalAssistantMessage []byte
	Status                string
	StopReason            string
	ResponseComplete      bool
	TrainingEligible      bool
	RequestHash           string
	ResponseHash          string
}

// OfflineResolveError is a request-level failure (auth/snapshot), not a per-call
// exclusion.
type OfflineResolveError struct {
	Code    string
	Message string
	Status  int
}

func (e *OfflineResolveError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

// Resolve loads frozen run material, verifies workspace/snapshot binding, and
// returns one explicit result per deduplicated requested call ID.
func (s *OfflineTrajectoryService) Resolve(ctx context.Context, req OfflineResolveRequest) ([]OfflineResolveLine, error) {
	if err := requireMixedRLQueries(s.queries); err != nil {
		return nil, err
	}
	if !req.RunID.Valid || !req.WorkspaceID.Valid {
		return nil, &OfflineResolveError{Code: "validation_failed", Message: "run and workspace identities are required", Status: 400}
	}
	if strings.TrimSpace(req.SnapshotID) == "" {
		return nil, &OfflineResolveError{Code: "validation_failed", Message: "snapshot_id is required", Status: 400}
	}

	run, err := s.queries.GetMixedRLRun(ctx, req.RunID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &OfflineResolveError{Code: "not_found", Message: "mixed run not found", Status: 404}
	}
	if err != nil {
		return nil, err
	}
	if run.WorkspaceID != req.WorkspaceID {
		return nil, &OfflineResolveError{Code: "forbidden", Message: "workspace is not authorized for this run", Status: 403}
	}
	if run.Status != "completed" && run.Status != "failed_timeout" {
		return nil, &OfflineResolveError{Code: "not_ready", Message: "run snapshot is not frozen", Status: 409}
	}
	frozenSnapshotID := mixedRLTextValue(run.FrozenSnapshotID)
	if frozenSnapshotID == "" || frozenSnapshotID != req.SnapshotID {
		return nil, &OfflineResolveError{Code: "snapshot_mismatch", Message: "snapshot_id does not match the frozen run snapshot", Status: 409}
	}

	agents, err := s.queries.ListMixedRLRunAgents(ctx, req.RunID)
	if err != nil {
		return nil, err
	}
	modeByAgent := make(map[string]string, len(agents))
	for _, agent := range agents {
		modeByAgent[agent.RunAgentID.String()] = agent.TrainingMode
	}

	calls, err := s.queries.ListMixedRLProviderCallsCanonical(ctx, req.RunID)
	if err != nil {
		return nil, err
	}
	sources := make([]OfflineCallSource, 0, len(calls))
	for _, call := range calls {
		sources = append(sources, OfflineCallSource{
			CallID:                call.CallID,
			TrainingMode:          modeByAgent[call.RunAgentID.String()],
			Provider:              call.Provider,
			Model:                 call.Model,
			APIKind:               call.ApiKind,
			RawProviderRequest:    cloneBytes(call.RawProviderRequest),
			FinalAssistantMessage: cloneBytes(call.FinalAssistantMessage),
			Status:                call.Status,
			StopReason:            mixedRLTextValue(call.StopReason),
			ResponseComplete:      call.ResponseComplete,
			TrainingEligible:      call.TrainingEligible,
			RequestHash:           call.RequestHash,
			ResponseHash:          mixedRLTextValue(call.ResponseHash),
		})
	}
	return ResolveOfflineTrajectoryLines(sources, req.CallIDs), nil
}

// DeduplicateCallIDs preserves first-seen order while dropping duplicates.
func DeduplicateCallIDs(callIDs []string) []string {
	if len(callIDs) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(callIDs))
	out := make([]string, 0, len(callIDs))
	for _, id := range callIDs {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// OrderOfflineResolveCallIDs returns requested snapshot members in frozen
// canonical order, then non-members in lexicographic order.
func OrderOfflineResolveCallIDs(canonicalMemberIDs []string, requested []string) (members []string, nonMembers []string) {
	requested = DeduplicateCallIDs(requested)
	requestedSet := make(map[string]struct{}, len(requested))
	for _, id := range requested {
		requestedSet[id] = struct{}{}
	}
	memberSet := make(map[string]struct{}, len(canonicalMemberIDs))
	for _, id := range canonicalMemberIDs {
		memberSet[id] = struct{}{}
		if _, ok := requestedSet[id]; ok {
			members = append(members, id)
		}
	}
	for _, id := range requested {
		if _, ok := memberSet[id]; !ok {
			nonMembers = append(nonMembers, id)
		}
	}
	sort.Strings(nonMembers)
	return members, nonMembers
}

// ResolveOfflineTrajectoryLines is the deterministic, side-effect-free resolver
// used by both the DB-backed service and unit tests.
func ResolveOfflineTrajectoryLines(canonical []OfflineCallSource, requested []string) []OfflineResolveLine {
	byID := make(map[string]OfflineCallSource, len(canonical))
	canonicalIDs := make([]string, 0, len(canonical))
	for _, src := range canonical {
		canonicalIDs = append(canonicalIDs, src.CallID)
		byID[src.CallID] = src
	}
	members, nonMembers := OrderOfflineResolveCallIDs(canonicalIDs, requested)
	out := make([]OfflineResolveLine, 0, len(members)+len(nonMembers))
	for _, id := range members {
		out = append(out, NormalizeOfflineCall(byID[id]))
	}
	for _, id := range nonMembers {
		out = append(out, OfflineResolveLine{
			CallID: id,
			Status: offlineStatusExcluded,
			Reason: OfflineReasonCallNotInSnapshot,
		})
	}
	return out
}

// NormalizeOfflineCall applies authoritative MultiCA exclusion reasons and,
// when eligible, versioned lossless provider-neutral normalization.
func NormalizeOfflineCall(src OfflineCallSource) OfflineResolveLine {
	switch src.TrainingMode {
	case "online_rl":
		return excludedOfflineLine(src.CallID, OfflineReasonWrongModeOnlineRL, map[string]any{
			"training_mode":         src.TrainingMode,
			"normalization_version": OfflineNormalizationVersion,
		})
	case "none", "":
		mode := src.TrainingMode
		if mode == "" {
			mode = "none"
		}
		return excludedOfflineLine(src.CallID, OfflineReasonWrongModeNone, map[string]any{
			"training_mode":         mode,
			"normalization_version": OfflineNormalizationVersion,
		})
	case "offline_rl":
		// continue
	default:
		return excludedOfflineLine(src.CallID, OfflineReasonWrongModeNone, map[string]any{
			"training_mode":         src.TrainingMode,
			"normalization_version": OfflineNormalizationVersion,
		})
	}

	if !src.ResponseComplete {
		return excludedOfflineLine(src.CallID, OfflineReasonResponseIncomplete, map[string]any{
			"response_complete": false,
		})
	}
	if src.StopReason == "length" {
		return excludedOfflineLine(src.CallID, OfflineReasonStopReasonLength, map[string]any{
			"stop_reason": src.StopReason,
		})
	}
	if !src.TrainingEligible {
		return excludedOfflineLine(src.CallID, OfflineReasonTrainingIneligible, map[string]any{
			"status":            src.Status,
			"stop_reason":       src.StopReason,
			"response_complete": src.ResponseComplete,
			"training_eligible": false,
		})
	}
	if len(src.RawProviderRequest) == 0 || len(src.FinalAssistantMessage) == 0 {
		return excludedOfflineLine(src.CallID, OfflineReasonRawPayloadUnavailable, nil)
	}
	if offlinePayloadHash(src.RawProviderRequest) != src.RequestHash ||
		offlinePayloadHash(src.FinalAssistantMessage) != src.ResponseHash {
		return excludedOfflineLine(src.CallID, OfflineReasonHashMismatch, map[string]any{
			"request_hash":  src.RequestHash,
			"response_hash": src.ResponseHash,
		})
	}

	trajectory, unsupported, err := normalizeOfflineTrajectoryV1(src)
	if err != nil {
		return excludedOfflineLine(src.CallID, OfflineReasonNormalizationUnsupported, map[string]any{
			"error":                 err.Error(),
			"normalization_version": OfflineNormalizationVersion,
		})
	}
	if unsupported != "" {
		return excludedOfflineLine(src.CallID, OfflineReasonNormalizationUnsupported, map[string]any{
			"semantic_type":         unsupported,
			"normalization_version": OfflineNormalizationVersion,
		})
	}
	return OfflineResolveLine{
		CallID:     src.CallID,
		Status:     offlineStatusTrajectory,
		Trajectory: trajectory,
	}
}

func excludedOfflineLine(callID, reason string, details map[string]any) OfflineResolveLine {
	return OfflineResolveLine{
		CallID:  callID,
		Status:  offlineStatusExcluded,
		Reason:  reason,
		Details: details,
	}
}

func offlinePayloadHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("sha256:%x", sum[:])
}

func normalizeOfflineTrajectoryV1(src OfflineCallSource) (*NormalizedOfflineTrajectory, string, error) {
	var request map[string]any
	if err := json.Unmarshal(src.RawProviderRequest, &request); err != nil {
		return nil, "", errors.New("provider request must be a JSON object")
	}
	if unsupported := unsupportedProviderRequestSemantics(request); unsupported != "" {
		return nil, unsupported, nil
	}

	system, unsupported, err := normalizeSystemContent(request["system"])
	if err != nil {
		return nil, "", err
	}
	if unsupported != "" {
		return nil, unsupported, nil
	}
	messages, unsupported, err := normalizeInputMessages(request["messages"])
	if err != nil {
		return nil, "", err
	}
	if unsupported != "" {
		return nil, unsupported, nil
	}
	tools, unsupported, err := normalizeTools(request["tools"])
	if err != nil {
		return nil, "", err
	}
	if unsupported != "" {
		return nil, unsupported, nil
	}
	generation, unsupported := normalizeGenerationConfig(request)
	if unsupported != "" {
		return nil, unsupported, nil
	}
	output, unsupported, err := normalizeAssistantOutput(src.FinalAssistantMessage)
	if err != nil {
		return nil, "", err
	}
	if unsupported != "" {
		return nil, unsupported, nil
	}

	providerName := src.Provider
	if providerName == "" {
		providerName, _ = request["provider"].(string)
	}
	model := src.Model
	if model == "" {
		model, _ = request["model"].(string)
	}
	apiKind := src.APIKind
	if apiKind == "" {
		apiKind = "messages"
	}

	return &NormalizedOfflineTrajectory{
		NormalizationVersion: OfflineNormalizationVersion,
		System:               system,
		Messages:             messages,
		Tools:                tools,
		GenerationConfig:     generation,
		Output:               output,
		Provider: NormalizedProvider{
			Name:    providerName,
			Model:   model,
			APIKind: apiKind,
		},
		RequestHash:  src.RequestHash,
		ResponseHash: src.ResponseHash,
	}, "", nil
}

func unsupportedProviderRequestSemantics(request map[string]any) string {
	for key := range request {
		normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
		switch normalized {
		case "system", "messages", "tools", "model", "provider", "temperature",
			"top_p", "top_k", "max_tokens", "max_output_tokens", "stop", "stop_sequences",
			"presence_penalty", "frequency_penalty", "tool_choice", "stream",
			"metadata", "user", "n", "api_kind", "capture_boundary", "attempt":
			continue
		case "authorization", "api_key", "apikey", "access_token", "token", "password",
			"secret", "x_api_key":
			// Credential material is redacted later; it never changes model input.
			continue
		case "audio", "input_audio", "images", "image", "attachments", "files",
			"modalities", "response_format", "prediction", "web_search_options":
			return key
		default:
			// Unknown top-level keys that may change model input are rejected
			// rather than silently dropped.
			if strings.HasPrefix(key, "_") {
				continue
			}
			return key
		}
	}
	return ""
}

func normalizeSystemContent(raw any) (*NormalizedContent, string, error) {
	if raw == nil {
		return nil, "", nil
	}
	switch typed := raw.(type) {
	case string:
		if typed == "" {
			return nil, "", nil
		}
		return &NormalizedContent{Blocks: []NormalizedBlock{{Type: "text", Text: typed}}}, "", nil
	case []any:
		blocks, unsupported, err := normalizeBlocks(typed, false)
		if err != nil || unsupported != "" {
			return nil, unsupported, err
		}
		if len(blocks) == 0 {
			return nil, "", nil
		}
		return &NormalizedContent{Blocks: blocks}, "", nil
	case map[string]any:
		if blocksRaw, ok := typed["blocks"]; ok {
			arr, ok := blocksRaw.([]any)
			if !ok {
				return nil, "", errors.New("system.blocks must be an array")
			}
			blocks, unsupported, err := normalizeBlocks(arr, false)
			if err != nil || unsupported != "" {
				return nil, unsupported, err
			}
			return &NormalizedContent{Blocks: blocks}, "", nil
		}
		return nil, "systemObject", nil
	default:
		return nil, "system", nil
	}
}

func normalizeInputMessages(raw any) ([]NormalizedMessage, string, error) {
	if raw == nil {
		return []NormalizedMessage{}, "", nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil, "", errors.New("messages must be an array")
	}
	out := make([]NormalizedMessage, 0, len(arr))
	for _, item := range arr {
		msgMap, ok := item.(map[string]any)
		if !ok {
			return nil, "", errors.New("message must be an object")
		}
		role, _ := msgMap["role"].(string)
		if role == "" {
			return nil, "", errors.New("message role is required")
		}
		blocks, unsupported, err := normalizeMessageBlocks(msgMap)
		if err != nil || unsupported != "" {
			return nil, unsupported, err
		}
		out = append(out, NormalizedMessage{Role: role, Blocks: blocks})
	}
	return out, "", nil
}

func normalizeMessageBlocks(msg map[string]any) ([]NormalizedBlock, string, error) {
	// Message-level fields outside role/blocks/content (for example OpenAI
	// "tool_calls" or "name") may have affected model input and cannot be
	// represented by normalization v1; exclude rather than silently drop.
	// stopReason/stop_reason are response metadata that never re-enter model
	// input, so they are safe to omit from the normalized form.
	extra := make([]string, 0)
	for key := range msg {
		switch key {
		case "role", "blocks", "content", "stopReason", "stop_reason":
		default:
			extra = append(extra, key)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return nil, "message." + extra[0], nil
	}
	if blocksRaw, ok := msg["blocks"]; ok {
		arr, ok := blocksRaw.([]any)
		if !ok {
			return nil, "", errors.New("message.blocks must be an array")
		}
		return normalizeBlocks(arr, false)
	}
	if contentRaw, ok := msg["content"]; ok {
		switch typed := contentRaw.(type) {
		case string:
			if typed == "" {
				return []NormalizedBlock{}, "", nil
			}
			return []NormalizedBlock{{Type: "text", Text: typed}}, "", nil
		case []any:
			return normalizeBlocks(typed, false)
		default:
			return nil, "messageContent", nil
		}
	}
	return []NormalizedBlock{}, "", nil
}

func normalizeTools(raw any) ([]NormalizedTool, string, error) {
	if raw == nil {
		return []NormalizedTool{}, "", nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil, "", errors.New("tools must be an array")
	}
	out := make([]NormalizedTool, 0, len(arr))
	for _, item := range arr {
		toolMap, ok := item.(map[string]any)
		if !ok {
			return nil, "", errors.New("tool must be an object")
		}
		// OpenAI-style nested function wrapper.
		if fn, ok := toolMap["function"].(map[string]any); ok {
			toolMap = fn
		}
		name, _ := toolMap["name"].(string)
		if name == "" {
			return nil, "", errors.New("tool name is required")
		}
		desc, _ := toolMap["description"].(string)
		schemaRaw := toolMap["input_schema"]
		if schemaRaw == nil {
			schemaRaw = toolMap["parameters"]
		}
		var schema json.RawMessage
		if schemaRaw != nil {
			encoded, err := json.Marshal(schemaRaw)
			if err != nil {
				return nil, "", err
			}
			schema = encoded
		}
		for key := range toolMap {
			switch key {
			case "name", "description", "input_schema", "parameters", "type":
				continue
			default:
				return nil, key, nil
			}
		}
		out = append(out, NormalizedTool{Name: name, Description: desc, InputSchema: schema})
	}
	return out, "", nil
}

func normalizeGenerationConfig(request map[string]any) (map[string]any, string) {
	out := map[string]any{}
	copyFloat := func(srcKey, dstKey string) {
		if value, ok := request[srcKey]; ok {
			out[dstKey] = value
		}
	}
	copyFloat("temperature", "temperature")
	copyFloat("top_p", "top_p")
	copyFloat("top_k", "top_k")
	if value, ok := request["max_output_tokens"]; ok {
		out["max_output_tokens"] = value
	} else if value, ok := request["max_tokens"]; ok {
		out["max_output_tokens"] = value
	}
	if value, ok := request["stop_sequences"]; ok {
		out["stop_sequences"] = value
	} else if value, ok := request["stop"]; ok {
		out["stop_sequences"] = value
	}
	copyFloat("presence_penalty", "presence_penalty")
	copyFloat("frequency_penalty", "frequency_penalty")
	if value, ok := request["tool_choice"]; ok {
		switch value.(type) {
		case string, nil:
			out["tool_choice"] = value
		default:
			return nil, "tool_choice"
		}
	}
	if len(out) == 0 {
		return nil, ""
	}
	return out, ""
}

func normalizeAssistantOutput(raw []byte) (NormalizedMessage, string, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return NormalizedMessage{}, "", errors.New("final assistant message must be a JSON object")
	}
	role, _ := payload["role"].(string)
	if role == "" {
		role = "assistant"
	}
	blocks, unsupported, err := normalizeMessageBlocks(payload)
	if err != nil || unsupported != "" {
		return NormalizedMessage{}, unsupported, err
	}
	// Output must remain typed thinking/text/toolCall only.
	for _, block := range blocks {
		switch block.Type {
		case "thinking", "text", "toolCall":
			continue
		default:
			return NormalizedMessage{}, block.Type, nil
		}
	}
	return NormalizedMessage{Role: role, Blocks: blocks}, "", nil
}

// normalizedBlockAllowedKeys lists the fields each recognized block type can
// represent losslessly. Any other field on a known block (for example an
// Anthropic thinking "signature", a text "citations" entry, or a tool-result
// "is_error" flag) may have affected model input and cannot be round-tripped,
// so normalization must exclude the call instead of silently dropping it.
var normalizedBlockAllowedKeys = map[string]map[string]struct{}{
	"text":          {"type": {}, "text": {}},
	"thinking":      {"type": {}, "thinking": {}, "text": {}},
	"toolCall":      {"type": {}, "id": {}, "tool_call_id": {}, "name": {}, "arguments": {}, "input": {}, "function": {}},
	"tool_use":      {"type": {}, "id": {}, "tool_call_id": {}, "name": {}, "arguments": {}, "input": {}, "function": {}},
	"functionCall":  {"type": {}, "id": {}, "tool_call_id": {}, "name": {}, "arguments": {}, "input": {}, "function": {}},
	"function_call": {"type": {}, "id": {}, "tool_call_id": {}, "name": {}, "arguments": {}, "input": {}, "function": {}},
	"toolResult":    {"type": {}, "tool_call_id": {}, "tool_use_id": {}, "toolCallId": {}, "content": {}},
	"tool_result":   {"type": {}, "tool_call_id": {}, "tool_use_id": {}, "toolCallId": {}, "content": {}},
}

// unsupportedBlockField returns "type.field" for the first block field that
// normalization v1 cannot represent losslessly, or "" when the block is
// representable. Unknown block types return "" here; the caller's type switch
// rejects them with the bare type name.
func unsupportedBlockField(blockType string, blockMap map[string]any) string {
	allowed, ok := normalizedBlockAllowedKeys[blockType]
	if !ok {
		return ""
	}
	keys := make([]string, 0, len(blockMap))
	for key := range blockMap {
		if _, ok := allowed[key]; !ok {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	return blockType + "." + keys[0]
}

func normalizeBlocks(raw []any, insideToolResult bool) ([]NormalizedBlock, string, error) {
	out := make([]NormalizedBlock, 0, len(raw))
	for _, item := range raw {
		blockMap, ok := item.(map[string]any)
		if !ok {
			return nil, "", errors.New("content block must be an object")
		}
		blockType, _ := blockMap["type"].(string)
		if unsupported := unsupportedBlockField(blockType, blockMap); unsupported != "" {
			return nil, unsupported, nil
		}
		switch blockType {
		case "text":
			text, _ := blockMap["text"].(string)
			out = append(out, NormalizedBlock{Type: "text", Text: text})
		case "thinking":
			thinking, _ := blockMap["thinking"].(string)
			if thinking == "" {
				thinking, _ = blockMap["text"].(string)
			}
			out = append(out, NormalizedBlock{Type: "thinking", Thinking: thinking})
		case "toolCall", "tool_use", "functionCall", "function_call":
			if fn, ok := blockMap["function"].(map[string]any); ok {
				fnKeys := make([]string, 0, len(fn))
				for key := range fn {
					if key != "name" && key != "arguments" {
						fnKeys = append(fnKeys, key)
					}
				}
				if len(fnKeys) > 0 {
					sort.Strings(fnKeys)
					return nil, blockType + ".function." + fnKeys[0], nil
				}
			}
			id, _ := blockMap["id"].(string)
			if id == "" {
				id, _ = blockMap["tool_call_id"].(string)
			}
			name, _ := blockMap["name"].(string)
			if name == "" {
				if fn, ok := blockMap["function"].(map[string]any); ok {
					name, _ = fn["name"].(string)
				}
			}
			args := blockMap["arguments"]
			if args == nil {
				args = blockMap["input"]
			}
			if args == nil {
				if fn, ok := blockMap["function"].(map[string]any); ok {
					args = fn["arguments"]
				}
			}
			if args == nil {
				args = map[string]any{}
			}
			encoded, err := json.Marshal(args)
			if err != nil {
				return nil, "", err
			}
			out = append(out, NormalizedBlock{
				Type:      "toolCall",
				ID:        id,
				Name:      name,
				Arguments: encoded,
			})
		case "toolResult", "tool_result":
			if insideToolResult {
				return nil, "nestedToolResult", nil
			}
			toolCallID, _ := blockMap["tool_call_id"].(string)
			if toolCallID == "" {
				toolCallID, _ = blockMap["tool_use_id"].(string)
			}
			if toolCallID == "" {
				toolCallID, _ = blockMap["toolCallId"].(string)
			}
			var content []NormalizedBlock
			switch typed := blockMap["content"].(type) {
			case string:
				if typed != "" {
					content = []NormalizedBlock{{Type: "text", Text: typed}}
				}
			case []any:
				var unsupported string
				var err error
				content, unsupported, err = normalizeBlocks(typed, true)
				if err != nil || unsupported != "" {
					return nil, unsupported, err
				}
			case nil:
				content = []NormalizedBlock{}
			default:
				return nil, "toolResultContent", nil
			}
			out = append(out, NormalizedBlock{
				Type:       "toolResult",
				ToolCallID: toolCallID,
				Content:    content,
			})
		case "":
			return nil, "missingBlockType", nil
		default:
			return nil, blockType, nil
		}
	}
	return out, "", nil
}
