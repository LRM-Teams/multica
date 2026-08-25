package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// Model describes a single LLM model exposed by an agent provider.
// The dropdown groups by Provider when the ID uses the
// `provider/model` form (e.g. "openai/gpt-4o" from opencode).
// Default is a *display* hint: the UI badges the entry the
// runtime explicitly advertises as its preferred pick (e.g. Grok's
// default marker or an ACP server's currentModelId). It has no effect
// at execution time — when agent.model is empty the daemon passes
// "" to the backend so each provider's own CLI resolves its own
// default, which is always closer to what the user's account /
// environment actually supports than a static guess here.
type Model struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Provider string `json:"provider,omitempty"`
	Default  bool   `json:"default,omitempty"`
	// Thinking advertises the runtime's reasoning/effort catalog for this
	// model. nil means the runtime/model has no thinking-level control
	// (or the daemon couldn't discover one); the UI hides its picker. The
	// catalog is per-model because Codex's `codex debug models` is itself
	// per-model and Claude's `--effort` superset has known per-model gaps
	// (`xhigh` is Opus-only, `max` is session-only). See MUL-2339.
	Thinking *ModelThinking `json:"thinking,omitempty"`
}

// ModelThinking carries the per-model reasoning/effort catalog
// surfaced by an agent runtime. Values are runtime-native — Codex
// emits "none|minimal|low|medium|high|xhigh"; Claude emits
// "low|medium|high|xhigh|max". The frontend renders SupportedLevels
// as-is so what users see matches each CLI's own UI.
type ModelThinking struct {
	SupportedLevels []ThinkingLevel `json:"supported_levels"`
	// DefaultLevel is the value the runtime picks when no override is
	// provided. Empty means "the runtime picks, we don't know" — the
	// UI shows "Default" as a generic option.
	DefaultLevel string `json:"default_level,omitempty"`
}

// ThinkingLevel is one entry in a ModelThinking.SupportedLevels list.
// Value is the literal token passed to the CLI (Claude `--effort <value>`
// or Codex `model_reasoning_effort=<value>`); Label is a display string;
// Description is optional helper copy lifted from the upstream catalog
// when available (Codex's `description` field).
type ThinkingLevel struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// modelCache memoizes dynamic discovery calls so repeated UI loads and chat
// runs don't re-shell slow agent CLIs. Entries expire after modelCacheTTL.
type modelCacheEntry struct {
	models    []Model
	expiresAt time.Time
}

var (
	modelCacheMu sync.Mutex
	modelCache   = map[string]modelCacheEntry{}
)

const modelCacheTTL = 10 * time.Minute

// ListModels returns the models supported by the given agent provider.
// Static providers return a baked-in list; dynamic providers query their
// runtime-specific discovery mechanism with caching. Each provider owns its
// failure behavior; Grok deliberately returns an empty catalog rather than a
// static fallback.
//
// For runtimes that advertise per-model thinking levels, the catalog carries
// those options through to the UI. Discovery failures leave Thinking == nil,
// which the UI treats as "no picker for this model" rather than blocking model
// selection.
//
// executablePath lets the caller point at a non-default binary; pass
// "" to use the provider's default name on PATH.
func ListModels(ctx context.Context, providerType, executablePath string) ([]Model, error) {
	switch providerType {
	case "claude":
		return claudeStaticModels(), nil
	case "codex":
		// Frank 2026-08-03: dynamic only from `codex debug models`.
		// Picker rows are visibility=list; thinking catalog is filled
		// from the same payload (no second shell).
		return cachedDiscovery(discoveryCacheKey(providerType, executablePath), func() ([]Model, error) {
			return discoverCodexModels(ctx, executablePath)
		})
	case "grok":
		return cachedDiscovery(discoveryCacheKey(providerType, executablePath), func() ([]Model, error) {
			return discoverGrokModels(ctx, executablePath)
		})
	case "cursor":
		return cachedDiscovery(providerType, func() ([]Model, error) {
			return discoverCursorModels(ctx, executablePath)
		})
	case "kiro":
		return cachedDiscovery(providerType, func() ([]Model, error) {
			return discoverKiroModels(ctx, executablePath)
		})
	case "opencode":
		return cachedDiscovery(discoveryCacheKey(providerType, executablePath), func() ([]Model, error) {
			return discoverOpenCodeModels(ctx, executablePath)
		})
	case "pi":
		return cachedDiscovery(providerType, func() ([]Model, error) {
			return discoverPiModels(ctx, executablePath)
		})
	default:
		return nil, fmt.Errorf("unknown agent type: %q", providerType)
	}
}

// ModelSelectionSupported reports whether setting `agent.model` has any
// effect for the given provider. Sourced from providerCapabilities
// (task #47) — do not re-list providers here.
func ModelSelectionSupported(providerType string) bool {
	return Capabilities(providerType).ModelSelectionSupported
}

// CustomModelIDSupported reports whether the provider accepts an arbitrary
// model id typed by the user. Sourced from providerCapabilities (task #47);
// the models API surfaces it as custom_model_id_supported so the UI never
// hardcodes a provider whitelist.
func CustomModelIDSupported(providerType string) bool {
	return Capabilities(providerType).CustomModelIDSupported
}

// ForceRestartSupported reports whether a busy/stuck agent of this provider
// can be force-interrupted via lifecycle restart (task #62). Sourced from
// providerCapabilities — FE should gate the restart button on this rather
// than hardcoding the canonical-resident allow-list.
func ForceRestartSupported(providerType string) bool {
	return Capabilities(providerType).ForceRestart
}

// cachedDiscovery invokes fn and caches the result for modelCacheTTL.
// The cache is keyed on providerType only; callers that need to
// distinguish discovery by host/user should include that in the key
// if we ever introduce such a mode.
func cachedDiscovery(key string, fn func() ([]Model, error)) ([]Model, error) {
	modelCacheMu.Lock()
	if entry, ok := modelCache[key]; ok && time.Now().Before(entry.expiresAt) {
		out := entry.models
		modelCacheMu.Unlock()
		return out, nil
	}
	modelCacheMu.Unlock()

	models, err := fn()
	if err != nil {
		return nil, err
	}

	// Don't cache an empty result. Zero models is almost always a transient
	// failure (discovery CLI timeout, not-logged-in, network blip) rather than
	// a runtime that genuinely has no models; caching it would keep the picker
	// blank for the full TTL even after the cause clears. Skipping the cache
	// lets the next request retry immediately. See #3729.
	if len(models) == 0 {
		return models, nil
	}

	modelCacheMu.Lock()
	modelCache[key] = modelCacheEntry{models: models, expiresAt: time.Now().Add(modelCacheTTL)}
	modelCacheMu.Unlock()
	return models, nil
}

func discoveryCacheKey(providerType, executablePath string) string {
	if executablePath == "" {
		return providerType
	}
	return providerType + ":" + executablePath
}

// ── Static catalogs ──

// claudeStaticModels is the current, user-visible Claude lineup. The runtime
// aliases stay stable so the installed Claude CLI can resolve them.
// Compatibility IDs deliberately stay out of this picker; persisted agents
// still resolve them through claudeCompatibilityModels below.
func claudeStaticModels() []Model {
	return []Model{
		{ID: "opus", Label: "Claude Opus", Provider: "anthropic"},
		{ID: "fable", Label: "Claude Fable", Provider: "anthropic"},
		{ID: "sonnet", Label: "Claude Sonnet", Provider: "anthropic"},
		{ID: "haiku", Label: "Claude Haiku", Provider: "anthropic"},
		{ID: "claude-opus-5", Label: "Claude Opus 5", Provider: "anthropic"},
		{ID: "claude-opus-4-8", Label: "Claude Opus 4.8", Provider: "anthropic"},
		{ID: "claude-opus-4-7", Label: "Claude Opus 4.7", Provider: "anthropic"},
		{ID: "claude-opus-4-6", Label: "Claude Opus 4.6", Provider: "anthropic"},
		{ID: "claude-fable-5", Label: "Claude Fable 5", Provider: "anthropic"},
		{ID: "claude-sonnet-5", Label: "Claude Sonnet 5", Provider: "anthropic"},
		{ID: "claude-sonnet-4-6", Label: "Claude Sonnet 4.6", Provider: "anthropic"},
		{ID: "claude-haiku-4-5", Label: "Claude Haiku 4.5", Provider: "anthropic"},
	}
}

// discoverCodexModels builds the user-visible model picker from
// `codex debug models --bundled`. Only visibility=list rows are
// returned (hide stays out of the dropdown). Thinking levels are
// attached from the same payload. Failures return a human-readable
// error with an empty list — no static fallback (product rule).
func discoverCodexModels(ctx context.Context, executablePath string) ([]Model, error) {
	if executablePath == "" {
		executablePath = "codex"
	}
	raw, err := runCodexDebugModels(ctx, executablePath)
	if err != nil {
		return nil, fmt.Errorf("unable to list models from Codex CLI (%s debug models): %w", executablePath, err)
	}
	models := parseCodexDebugModelsCatalog(raw)
	if len(models) == 0 {
		return nil, fmt.Errorf("Codex CLI returned no listable models (check login / CLI version)")
	}
	return models, nil
}

// discoverGrokModels launches the same ACP server used for Grok execution and
// reads its initialize-time modelState. Discovery failures return an empty
// catalog rather than advertising models that the installed CLI or
// authenticated account may not support.
func discoverGrokModels(ctx context.Context, executablePath string) ([]Model, error) {
	if executablePath == "" {
		executablePath = "grok"
	}
	if _, err := exec.LookPath(executablePath); err != nil {
		return []Model{}, nil
	}
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, executablePath, "agent", "--no-leader", "--always-approve", "stdio")
	hideAgentWindow(cmd)
	cmd.Env = buildGrokEnv(nil, userGrokHome())
	cmd.Stderr = io.Discard
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return []Model{}, nil
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return []Model{}, nil
	}
	if err := cmd.Start(); err != nil {
		return []Model{}, nil
	}
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	type initializeResponse struct {
		ID     int             `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	responseCh := make(chan json.RawMessage, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
		for scanner.Scan() {
			var response initializeResponse
			if err := json.Unmarshal(scanner.Bytes(), &response); err != nil || response.ID != 1 {
				continue
			}
			if len(response.Error) > 0 {
				responseCh <- nil
			} else {
				responseCh <- response.Result
			}
			return
		}
		responseCh <- nil
	}()

	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": 1,
			"clientCapabilities": map[string]any{
				"fs":       map[string]bool{"readTextFile": false, "writeTextFile": false},
				"terminal": false,
			},
			"_meta": map[string]any{
				"startupHints": map[string]bool{
					"nonInteractive":    true,
					"skipGitStatus":     true,
					"skipProjectLayout": true,
				},
				"clientType":    "multica-daemon",
				"clientVersion": "1.0.0",
			},
		},
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return []Model{}, nil
	}
	if _, err := stdin.Write(append(payload, '\n')); err != nil {
		return []Model{}, nil
	}

	select {
	case raw := <-responseCh:
		return parseGrokACPInitializeModels(raw), nil
	case <-runCtx.Done():
		return []Model{}, nil
	}
}

var grokReasoningEffortLabels = map[string]string{
	"low":    "Low",
	"medium": "Medium",
	"high":   "High",
	"xhigh":  "Extra high",
	"max":    "Max",
	"ultra":  "Ultra",
}

// parseGrokACPInitializeModels projects the structured modelState advertised
// by Grok ACP. Reasoning defaults come from reasoningEfforts[].default;
// reasoningEffort is the user's current selection and is only a fallback when
// the catalog does not mark a default.
func parseGrokACPInitializeModels(raw json.RawMessage) []Model {
	var result struct {
		ProtocolVersion   int `json:"protocolVersion"`
		AgentCapabilities struct {
			LoadSession bool `json:"loadSession"`
		} `json:"agentCapabilities"`
		Meta struct {
			ModelState struct {
				CurrentModelID  string `json:"currentModelId"`
				AvailableModels []struct {
					ModelID string `json:"modelId"`
					Name    string `json:"name"`
					Meta    struct {
						ReasoningEffort  string            `json:"reasoningEffort"`
						ReasoningEfforts []json.RawMessage `json:"reasoningEfforts"`
					} `json:"_meta"`
				} `json:"availableModels"`
			} `json:"modelState"`
		} `json:"_meta"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &result) != nil || result.ProtocolVersion != 1 || !result.AgentCapabilities.LoadSession {
		return []Model{}
	}

	currentModelID := strings.TrimSpace(result.Meta.ModelState.CurrentModelID)
	models := make([]Model, 0, len(result.Meta.ModelState.AvailableModels))
	seenModels := make(map[string]bool, len(result.Meta.ModelState.AvailableModels))
	for _, advertised := range result.Meta.ModelState.AvailableModels {
		id := strings.TrimSpace(advertised.ModelID)
		if id == "" || seenModels[id] {
			continue
		}
		seenModels[id] = true
		label := strings.TrimSpace(advertised.Name)
		if label == "" {
			label = id
		}
		model := Model{ID: id, Label: label, Provider: "grok", Default: id == currentModelID}
		model.Thinking = parseGrokACPThinking(advertised.Meta.ReasoningEffort, advertised.Meta.ReasoningEfforts)
		models = append(models, model)
	}
	return models
}

func parseGrokACPThinking(current string, advertised []json.RawMessage) *ModelThinking {
	levels := make([]ThinkingLevel, 0, len(advertised))
	seen := make(map[string]bool, len(advertised))
	defaultLevel := ""
	for _, raw := range advertised {
		var value string
		var entry struct {
			ID          string `json:"id"`
			Value       string `json:"value"`
			Label       string `json:"label"`
			Description string `json:"description"`
			Default     bool   `json:"default"`
		}
		if json.Unmarshal(raw, &value) != nil {
			if json.Unmarshal(raw, &entry) != nil {
				continue
			}
			value = strings.TrimSpace(entry.ID)
			if value == "" {
				value = strings.TrimSpace(entry.Value)
			}
		}
		value = strings.TrimSpace(value)
		fallbackLabel, known := grokReasoningEffortLabels[value]
		if !known || seen[value] {
			continue
		}
		seen[value] = true
		label := strings.TrimSpace(entry.Label)
		if label == "" {
			label = fallbackLabel
		}
		levels = append(levels, ThinkingLevel{Value: value, Label: label, Description: strings.TrimSpace(entry.Description)})
		if entry.Default {
			// Grok 1.0.4 can mark both the current user selection and the
			// model default. The model default is the last marked entry,
			// matching the catalog order and Raft 1.0.16's projection.
			defaultLevel = value
		}
	}
	if len(levels) == 0 {
		return nil
	}
	if defaultLevel == "" {
		current = strings.TrimSpace(current)
		if seen[current] {
			defaultLevel = current
		}
	}
	return &ModelThinking{SupportedLevels: levels, DefaultLevel: defaultLevel}
}

// cursorStaticModels is a minimal fallback used when
// `cursor-agent --list-models` isn't available (binary missing,
// offline, etc). The real catalog is fetched dynamically because
// Cursor's model IDs shift (e.g. `composer-2-fast`,
// `claude-4.6-sonnet-medium`, `gemini-3.1-pro`) and any static
// list we ship goes stale fast.
func cursorStaticModels() []Model {
	return []Model{
		{ID: "auto", Label: "Auto", Provider: "cursor", Default: true},
	}
}

// discoverOpenCodeModels runs `opencode models --verbose` and parses its
// output. The CLI prints `provider/model` rows, followed by JSON metadata
// when verbose mode is enabled; we emit IDs verbatim so what the user sees
// matches what `--model` accepts, and project any model `variants` into the
// thinking-level picker because OpenCode's `run --variant` flag is its
// provider-specific reasoning-effort surface.
// On any failure (CLI missing, parse error, timeout) we fall back to
// an empty list so the creatable UI still works.
func discoverOpenCodeModels(ctx context.Context, executablePath string) ([]Model, error) {
	if executablePath == "" {
		executablePath = "opencode"
	}
	if _, err := exec.LookPath(executablePath); err != nil {
		return []Model{}, nil
	}
	// Newer opencode (1.15+) syncs its hosted free-model catalog over the
	// network on `opencode models`, which can take ~6s; the previous 5s cap
	// timed out and returned an empty list, so the runtime showed online but
	// the model picker was empty. See multica-ai/multica#3627.
	runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, executablePath, "models", "--verbose")
	hideAgentWindow(cmd)
	// Parse whatever the verbose command printed, even on a non-zero exit — a
	// stale config entry can make `opencode models` exit non-zero while still
	// listing the resolvable catalog (mirrors the pi path; see #3729/#3627).
	out, _ := cmd.Output()
	models := parseOpenCodeModels(string(out))
	if len(models) == 0 {
		// Verbose yielded nothing usable (unsupported flag, error text, or an
		// empty list). Retry the plain command, which omits the per-model JSON
		// but still prints the IDs.
		cmd = exec.CommandContext(runCtx, executablePath, "models")
		hideAgentWindow(cmd)
		out, _ = cmd.Output()
		models = parseOpenCodeModels(string(out))
	}
	if len(models) == 0 {
		return []Model{}, nil
	}
	return models, nil
}

// parseOpenCodeModels accepts the `opencode models` text output and
// extracts IDs. Non-verbose output is one `provider/model` row per line.
// Verbose output appends a pretty-printed JSON object after each ID; when
// that object contains `variants`, each enabled variant becomes a thinking
// level that the backend later passes through `opencode run --variant`.
func parseOpenCodeModels(output string) []Model {
	lines := strings.Split(output, "\n")
	var models []Model
	indexByID := map[string]int{}
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		id := parseOpenCodeModelIDLine(line)
		if id == "" {
			continue
		}
		idx, seen := indexByID[id]
		if !seen {
			provider := ""
			if slash := strings.Index(id, "/"); slash > 0 {
				provider = id[:slash]
			}
			idx = len(models)
			indexByID[id] = idx
			models = append(models, Model{ID: id, Label: id, Provider: provider})
		}

		next := i + 1
		for next < len(lines) && strings.TrimSpace(lines[next]) == "" {
			next++
		}
		if next >= len(lines) || !strings.HasPrefix(strings.TrimSpace(lines[next]), "{") {
			continue
		}
		raw, resumeAt := collectOpenCodeModelJSON(lines, next)
		if json.Valid(raw) {
			annotateOpenCodeModelMetadata(&models[idx], raw)
		}
		i = resumeAt - 1
	}
	return models
}

func parseOpenCodeModelIDLine(line string) string {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return ""
	}
	id := fields[0]
	if strings.HasPrefix(id, `"`) || strings.HasPrefix(id, "{") || strings.HasPrefix(id, "[") {
		return ""
	}
	if !strings.Contains(id, "/") {
		return ""
	}
	// Skip header rows such as PROVIDER/MODEL.
	if id == strings.ToUpper(id) {
		return ""
	}
	return id
}

func collectOpenCodeModelJSON(lines []string, start int) ([]byte, int) {
	var b strings.Builder
	for i := start; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if i > start && parseOpenCodeModelIDLine(line) != "" {
			return []byte(b.String()), i
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(lines[i])
		if json.Valid([]byte(b.String())) {
			return []byte(b.String()), i + 1
		}
	}
	return []byte(b.String()), len(lines)
}

type opencodeModelMetadata struct {
	Reasoning bool                            `json:"reasoning"`
	Variants  map[string]opencodeModelVariant `json:"variants"`
}

type opencodeModelVariant struct {
	Disabled        bool            `json:"disabled"`
	ReasoningEffort string          `json:"reasoningEffort"`
	Thinking        json.RawMessage `json:"thinking"`
}

var opencodeVariantLabel = map[string]string{
	"none":    "None",
	"minimal": "Minimal",
	"low":     "Low",
	"medium":  "Medium",
	"high":    "High",
	"xhigh":   "Extra high",
	"max":     "Max",
}

var opencodeVariantOrder = map[string]int{
	"none":    0,
	"minimal": 1,
	"low":     2,
	"medium":  3,
	"high":    4,
	"xhigh":   5,
	"max":     6,
}

func annotateOpenCodeModelMetadata(model *Model, raw []byte) {
	var meta opencodeModelMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return
	}
	if !meta.Reasoning && !openCodeVariantsLookReasoning(meta.Variants) {
		return
	}
	levels := openCodeThinkingLevelsFromVariants(meta.Variants)
	if len(levels) == 0 {
		return
	}
	model.Thinking = &ModelThinking{SupportedLevels: levels}
}

func openCodeVariantsLookReasoning(variants map[string]opencodeModelVariant) bool {
	for name, variant := range variants {
		if _, known := opencodeVariantOrder[name]; known {
			return true
		}
		if variant.ReasoningEffort != "" || len(variant.Thinking) > 0 {
			return true
		}
	}
	return false
}

func openCodeThinkingLevelsFromVariants(variants map[string]opencodeModelVariant) []ThinkingLevel {
	if len(variants) == 0 {
		return nil
	}
	values := make([]string, 0, len(variants))
	for value, variant := range variants {
		if value == "" || variant.Disabled {
			continue
		}
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool {
		left, leftKnown := opencodeVariantOrder[values[i]]
		right, rightKnown := opencodeVariantOrder[values[j]]
		if leftKnown && rightKnown {
			return left < right
		}
		if leftKnown != rightKnown {
			return leftKnown
		}
		return values[i] < values[j]
	})
	levels := make([]ThinkingLevel, 0, len(values))
	for _, value := range values {
		label, ok := opencodeVariantLabel[value]
		if !ok {
			label = strings.Title(strings.ReplaceAll(value, "-", " ")) //nolint:staticcheck
		}
		levels = append(levels, ThinkingLevel{Value: value, Label: label})
	}
	return levels
}

// discoverPiModels runs `pi --list-models` and parses its output.
// Older pi versions print the list to stderr; newer versions use
// stdout. We capture both and parse whichever is non-empty.
func discoverPiModels(ctx context.Context, executablePath string) ([]Model, error) {
	if executablePath == "" {
		executablePath = "pi"
	}
	if _, err := exec.LookPath(executablePath); err != nil {
		return []Model{}, nil
	}
	// Newer pi fetches its catalog from each configured provider over the
	// network, so discovery time scales with provider count — a multi-provider
	// setup measured ~4.6-4.8s, right at the old 5s cap. When jitter pushed it
	// over, the daemon killed the command before it printed anything and the
	// model picker came back empty while the runtime stayed online. 15s matches
	// the opencode discovery cap (see #3729, same class as #3627).
	runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, executablePath, "--list-models")
	hideAgentWindow(cmd)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil && len(stdout) == 0 && stderr.Len() == 0 {
		return []Model{}, nil
	}
	text := string(stdout)
	if strings.TrimSpace(text) == "" {
		text = stderr.String()
	}
	return parsePiModels(text), nil
}

// parsePiModels accepts the `pi --list-models` output. Pi historically
// emitted `provider:model` per line and now emits a multi-column table
// (`provider  model  context …`); both shapes are normalized to
// `provider/model` to match opencode/UI conventions. The case-insensitive
// `provider` token in column 0 is treated as the table header and skipped.
func parsePiModels(output string) []Model {
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var models []Model
	seen := map[string]bool{}
	thinkingCol := -1
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// pi interleaves human-readable diagnostics with the catalog when an
		// agent config references stale patterns — e.g.
		//   Warning: No models match pattern "opencode-go/mimo-v2-omni"
		// Skip them before field-splitting; otherwise prose tokens are coined
		// into bogus models like `No/models` or `Warning/`. See #3729.
		if isPiDiscoveryNoise(line) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		first := fields[0]
		if strings.EqualFold(first, "provider") {
			thinkingCol = -1
			for i, field := range fields {
				if strings.EqualFold(field, "thinking") {
					thinkingCol = i
					break
				}
			}
			continue
		}
		var id string
		if strings.ContainsAny(first, ":/") {
			// Legacy `provider:model` format — normalize colon to slash.
			// Restricted to this branch so a model name with a `:` in
			// the table format's column 1 is not silently rewritten.
			id = strings.Replace(first, ":", "/", 1)
		} else if len(fields) >= 2 {
			id = first + "/" + fields[1]
		} else {
			continue
		}
		// A real id has a non-empty provider and model on both sides of the
		// slash. Drop anything that doesn't (e.g. a stray `something:` token),
		// a cheap structural backstop on top of the diagnostic filter above.
		if slash := strings.Index(id, "/"); slash <= 0 || slash == len(id)-1 {
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		provider := ""
		if i := strings.Index(id, "/"); i > 0 {
			provider = id[:i]
		}
		model := Model{ID: id, Label: id, Provider: provider}
		if thinkingCol >= 0 && thinkingCol < len(fields) && piThinkingCellEnabled(fields[thinkingCol]) {
			model.Thinking = piDefaultThinking()
		}
		models = append(models, model)
	}
	return models
}

func piThinkingCellEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "yes", "true", "on", "1":
		return true
	default:
		return false
	}
}

func piDefaultThinking() *ModelThinking {
	return &ModelThinking{
		SupportedLevels: []ThinkingLevel{
			{Value: "off", Label: "Off"},
			{Value: "minimal", Label: "Minimal"},
			{Value: "low", Label: "Low"},
			{Value: "medium", Label: "Medium"},
			{Value: "high", Label: "High"},
			{Value: "xhigh", Label: "Extra high"},
		},
	}
}

// isPiDiscoveryNoise reports whether a `pi --list-models` line is a diagnostic
// message rather than a catalog row. pi prints these alongside the table when
// an agent config references stale provider/model patterns, e.g.
//
//	Warning: No models match pattern "opencode-go/mimo-v2-omni"
//
// The `Warning:` prefix is not guaranteed across versions, so the unmatched-
// pattern message is also matched on its own. These are prose, not
// `provider model` rows; without skipping them the field splitter coins bogus
// models like `No/models`. See #3729.
func isPiDiscoveryNoise(line string) bool {
	lower := strings.ToLower(line)
	if strings.Contains(lower, "no models match pattern") {
		return true
	}
	return strings.HasPrefix(lower, "warning:") ||
		strings.HasPrefix(lower, "error:") ||
		strings.HasPrefix(lower, "info:")
}

// discoverKiroModels spins up a throwaway `kiro-cli acp` process and parses
// the models block Kiro returns from session/new.
func discoverKiroModels(ctx context.Context, executablePath string) ([]Model, error) {
	return discoverACPModels(ctx, executablePath, acpDiscoveryProvider{
		defaultBin:   "kiro-cli",
		clientName:   "multica-model-discovery",
		tmpdirPrefix: "multica-kiro-discovery-",
	})
}

// acpDiscoveryProvider configures how discoverACPModels launches an
// ACP-speaking agent CLI. The shared helper drives every CLI in
// the same way (initialize → session/new → parse models block) — the
// per-provider differences are which binary to spawn, which env
// vars suppress interactive prompts during init, what argv puts
// the binary into ACP server mode (most use `acp`, Copilot uses
// `--acp`), and what to label temporary work directories so they're
// easy to identify in logs.
type acpDiscoveryProvider struct {
	defaultBin   string
	clientName   string
	extraEnv     []string
	tmpdirPrefix string
	// acpArgs is the argv passed to the binary to start it in ACP
	// server mode. Defaults to []string{"acp"} when nil/empty.
	acpArgs []string
}

// discoverACPModels runs the ACP handshake for any agent CLI that
// implements the standard `initialize` + `session/new` flow and
// advertises its model catalog in the response under
// `models.availableModels` / `models.currentModelId`. This covers
// Kimi today; future ACP backends can plug in by adding
// an acpDiscoveryProvider entry instead of duplicating the loop.
func discoverACPModels(ctx context.Context, executablePath string, p acpDiscoveryProvider) ([]Model, error) {
	if executablePath == "" {
		executablePath = p.defaultBin
	}
	if _, err := exec.LookPath(executablePath); err != nil {
		return []Model{}, nil
	}
	runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	cmdArgs := p.acpArgs
	if len(cmdArgs) == 0 {
		cmdArgs = []string{"acp"}
	}
	cmd := exec.CommandContext(runCtx, executablePath, cmdArgs...)
	hideAgentWindow(cmd)
	if len(p.extraEnv) > 0 {
		cmd.Env = append(os.Environ(), p.extraEnv...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return []Model{}, nil
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return []Model{}, nil
	}
	// Discard stderr; noisy logs here don't help us and we don't
	// want them bleeding into the daemon log every 60s.
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return []Model{}, nil
	}
	// Ensure the child process is always reaped.
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	writeACP := func(id int, method string, params map[string]any) error {
		msg := map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"method":  method,
			"params":  params,
		}
		data, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		data = append(data, '\n')
		_, err = stdin.Write(data)
		return err
	}

	// Send initialize + session/new.
	if err := writeACP(1, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientInfo":         map[string]any{"name": p.clientName, "version": "0.1.0"},
		"clientCapabilities": map[string]any{},
	}); err != nil {
		return []Model{}, nil
	}

	// session/new requires a valid cwd — use a temp directory we
	// clean up afterwards, not the daemon's workdir (which might
	// be in the middle of another task's worktree).
	tmp, err := os.MkdirTemp("", p.tmpdirPrefix)
	if err != nil {
		return []Model{}, nil
	}
	defer os.RemoveAll(tmp)

	if err := writeACP(2, "session/new", map[string]any{
		"cwd":        tmp,
		"mcpServers": []any{},
	}); err != nil {
		return []Model{}, nil
	}

	// Read responses until we see the one for id=2 (session/new).
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 4*1024*1024)
	deadline := time.After(12 * time.Second)
	done := make(chan []Model, 1)
	go func() {
		defer close(done)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var env struct {
				ID     json.Number     `json:"id"`
				Result json.RawMessage `json:"result"`
			}
			if err := json.Unmarshal([]byte(line), &env); err != nil {
				continue
			}
			if env.ID.String() != "2" || len(env.Result) == 0 {
				continue
			}
			done <- parseACPSessionNewModels(env.Result)
			return
		}
	}()

	select {
	case models := <-done:
		if models == nil {
			return []Model{}, nil
		}
		return models, nil
	case <-deadline:
		return []Model{}, nil
	case <-runCtx.Done():
		return []Model{}, nil
	}
}

// parseACPSessionNewModels extracts the model catalog from an ACP
// `session/new` response. Kimi (and any other ACP
// agent that follows the standard schema) emit:
//
//	{
//	  "sessionId": "...",
//	  "models": {
//	    "availableModels": [
//	      {"modelId": "...", "name": "...", "description": "..."}
//	    ],
//	    "currentModelId": "..."
//	  }
//	}
//
// Returns nil (not an empty slice) when the payload is missing so
// the caller can distinguish "parsed with no models" (valid but
// empty catalog) from "couldn't find the structure at all".
func parseACPSessionNewModels(raw json.RawMessage) []Model {
	type acpModelInfo struct {
		ModelID      string `json:"modelId"`
		ModelIDSnake string `json:"model_id"`
		Name         string `json:"name"`
		Description  string `json:"description"`
	}
	var resp struct {
		Models struct {
			AvailableModels      []acpModelInfo `json:"availableModels"`
			AvailableModelsSnake []acpModelInfo `json:"available_models"`
			CurrentModelID       string         `json:"currentModelId"`
			CurrentModelIDSnake  string         `json:"current_model_id"`
		} `json:"models"`
	}
	var presence struct {
		Models json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(raw, &presence); err != nil || len(presence.Models) == 0 {
		// No `models` key at all (or malformed payload): genuinely nothing to
		// report, distinct from "models present but the catalog is empty".
		return nil
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil
	}
	availableModels := resp.Models.AvailableModels
	if len(availableModels) == 0 && resp.Models.AvailableModelsSnake != nil {
		availableModels = resp.Models.AvailableModelsSnake
	}
	currentModelID := strings.TrimSpace(resp.Models.CurrentModelID)
	if currentModelID == "" {
		currentModelID = strings.TrimSpace(resp.Models.CurrentModelIDSnake)
	}
	models := make([]Model, 0, len(availableModels))
	seen := map[string]bool{}
	for _, m := range availableModels {
		modelID := strings.TrimSpace(m.ModelID)
		if modelID == "" {
			modelID = strings.TrimSpace(m.ModelIDSnake)
		}
		if modelID == "" || seen[modelID] {
			continue
		}
		seen[modelID] = true
		label := acpModelLabel(m.Name, modelID)
		provider := ""
		if idx := strings.Index(modelID, ":"); idx > 0 {
			provider = modelID[:idx]
		}
		models = append(models, Model{
			ID:       modelID,
			Label:    label,
			Provider: provider,
			Default:  modelID == currentModelID,
		})
	}
	return models
}

func acpModelLabel(name, modelID string) string {
	label := strings.TrimSpace(name)
	if label == "" || strings.EqualFold(label, "unknown") {
		return modelID
	}
	return label
}

// discoverCursorModels runs `cursor-agent --list-models` and parses
// the `id - Label` rows. Cursor's catalog changes often and ships
// many variants of the same base model (thinking / fast / max
// suffixes) — static baking would be obsolete within weeks. On any
// failure we fall back to the minimal static catalog so the UI
// stays usable when cursor-agent isn't installed on the daemon host.
func discoverCursorModels(ctx context.Context, executablePath string) ([]Model, error) {
	if executablePath == "" {
		executablePath = "cursor-agent"
	}
	if _, err := exec.LookPath(executablePath); err != nil {
		return cursorStaticModels(), nil
	}
	// 15s to match the other network-backed discovery paths (pi/opencode/ACP);
	// cursor-agent fetches its frequently-changing catalog, so a tight cap can
	// time out and fall back to the minimal static list. See #3729.
	runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, executablePath, "--list-models")
	hideAgentWindow(cmd)
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return cursorStaticModels(), nil
	}
	models := parseCursorModels(string(out))
	if len(models) == 0 {
		return cursorStaticModels(), nil
	}
	return models, nil
}

// parseCursorModels extracts model IDs from `cursor-agent --list-models`.
// Output format (as of cursor-agent 2026.04):
//
//	Available models
//	<blank>
//	auto - Auto
//	composer-2-fast - Composer 2 Fast (current, default)
//	composer-2 - Composer 2
//	…
//
// The model tagged `(default)` is surfaced as Default=true so the
// UI badge points at cursor's own recommendation rather than a
// hard-coded guess from our catalog.
func parseCursorModels(output string) []Model {
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var models []Model
	seen := map[string]bool{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Row format: "<id> - <label>". Skip the "Available models" header.
		idx := strings.Index(line, " - ")
		if idx <= 0 {
			continue
		}
		id := strings.TrimSpace(line[:idx])
		label := strings.TrimSpace(line[idx+3:])
		if !isCatalogModelID(id) {
			// Reuse the identifier guard — cursor IDs are in the
			// same character set (alnum + `-./_`), so anything
			// that fails it is either malformed or a header line.
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		isDefault := strings.Contains(label, "default")
		// Strip the "(current, default)" suffix from the display
		// label since we surface that through the Default flag.
		if paren := strings.Index(label, "("); paren > 0 {
			label = strings.TrimSpace(label[:paren])
		}
		if label == "" {
			label = id
		}
		models = append(models, Model{
			ID:       id,
			Label:    label,
			Provider: "cursor",
			Default:  isDefault,
		})
	}
	return models
}

// isCatalogModelID reports whether s looks like a valid
// agent-name or model-id token: starts with a letter, contains only
// identifier-safe characters, and isn't a section header
// (trailing colon). Rejects TUI decoration like `│`, `╭`, `◇`, `|`.
func isCatalogModelID(s string) bool {
	if s == "" || strings.HasSuffix(s, ":") {
		return false
	}
	first := s[0]
	if !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z')) {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.' || r == '/':
		default:
			return false
		}
	}
	return true
}

