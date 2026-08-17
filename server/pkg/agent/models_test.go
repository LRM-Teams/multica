package agent

import (
	"context"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestListModelsStaticProviders(t *testing.T) {
	ctx := context.Background()
	for _, provider := range []string{"cursor"} {
		got, err := ListModels(ctx, provider, "")
		if err != nil {
			t.Fatalf("ListModels(%q) error: %v", provider, err)
		}
		if len(got) == 0 {
			t.Errorf("ListModels(%q) returned no models", provider)
		}
		for i, m := range got {
			if m.ID == "" {
				t.Errorf("ListModels(%q)[%d] has empty ID", provider, i)
			}
			if m.Label == "" {
				t.Errorf("ListModels(%q)[%d] has empty Label", provider, i)
			}
		}
	}
}

func TestListModelsRemovedProvidersAreUnknown(t *testing.T) {
	ctx := context.Background()
	for _, provider := range []string{"gemini", "copilot", "hermes", "openclaw", "kimi", "codebuddy", "antigravity"} {
		if _, err := ListModels(ctx, provider, ""); err == nil {
			t.Errorf("ListModels(%q) must fail closed after the runtime was removed", provider)
		}
	}
}

func TestClaudeStaticModelsExposeOnlyCurrentCleanLineup(t *testing.T) {
	models := claudeStaticModels()
	ids := map[string]Model{}
	defaults := 0
	for _, m := range models {
		ids[m.ID] = m
		if m.Default {
			defaults++
		}
	}

	for _, want := range []string{"sonnet", "opus", "haiku", "claude-fable-5", "claude-sonnet-5", "claude-opus-5"} {
		if _, ok := ids[want]; !ok {
			t.Errorf("missing Claude model %q in: %+v", want, models)
		}
	}
	for id, wantLabel := range map[string]string{
		"sonnet":         "Sonnet 5",
		"opus":           "Opus 5",
		"haiku":          "Haiku",
		"claude-fable-5": "Fable 5",
		"claude-sonnet-5": "Sonnet 5 (pin)",
		"claude-opus-5":   "Opus 5 (pin)",
	} {
		if got := ids[id].Label; got != wantLabel {
			t.Errorf("visible label for %q = %q, want %q", id, got, wantLabel)
		}
	}
	if len(models) != 6 {
		t.Fatalf("visible Claude lineup = %+v, want the three official aliases plus Fable 5 and pinned Sonnet 5/Opus 5", models)
	}
	if defaults != 1 || !ids["sonnet"].Default {
		t.Errorf("expected Sonnet to remain the sole default, got defaults=%d models=%+v", defaults, models)
	}
	for _, model := range models {
		if strings.Contains(strings.ToLower(model.Label), "latest") || strings.Contains(strings.ToLower(model.Label), "pinned") {
			t.Errorf("visible label leaks implementation state: %+v", model)
		}
	}
}

func TestCodexStaticModelsExposesGPT55(t *testing.T) {
	// Codex CLI has no `models list` subcommand so the catalog is
	// hand-maintained. Regression guard for multica-ai/multica#2009 —
	// GPT-5.5 must be selectable, and the badge default must point at
	// the latest release rather than lagging a version behind.
	models := codexStaticModels()
	ids := map[string]Model{}
	for _, m := range models {
		ids[m.ID] = m
	}
	for _, want := range []string{
		"gpt-5.5", "gpt-5.5-mini",
		"gpt-5.4", "gpt-5.4-mini",
		"gpt-5.3-codex", "gpt-5",
		"o3", "o3-mini",
	} {
		if _, ok := ids[want]; !ok {
			t.Errorf("missing expected Codex model %q in: %+v", want, models)
		}
	}
	latest, ok := ids["gpt-5.5"]
	if !ok || !latest.Default {
		t.Errorf("expected `gpt-5.5` to be the default Codex entry, got %+v", latest)
	}
	defaults := 0
	for _, m := range models {
		if m.Default {
			defaults++
		}
		if m.Provider != "openai" {
			t.Errorf("all Codex entries must carry Provider=openai, got %+v", m)
		}
	}
	if defaults != 1 {
		t.Errorf("expected exactly one default Codex entry, got %d", defaults)
	}
}

func TestListModelsKiroWithoutBinary(t *testing.T) {
	ctx := context.Background()
	modelCacheMu.Lock()
	delete(modelCache, "kiro")
	modelCacheMu.Unlock()

	got, err := ListModels(ctx, "kiro", "/nonexistent/kiro-cli")
	if err != nil {
		t.Fatalf("ListModels(kiro) error: %v", err)
	}
	if got == nil {
		t.Error("expected non-nil slice even when binary is missing")
	}
}

func TestListModelsUnknownProvider(t *testing.T) {
	ctx := context.Background()
	_, err := ListModels(ctx, "nonexistent", "")
	if err == nil {
		t.Fatal("ListModels(unknown) expected error")
	}
}

func TestStaticCatalogsHaveAtMostOneDefault(t *testing.T) {
	// Each catalog should tag at most one entry as the display
	// default so the UI badge is unambiguous. More than one
	// usually means a copy/paste slip when adding new models.
	catalogs := map[string][]Model{
		"claude": claudeStaticModels(),
		"codex":  codexStaticModels(),
		"cursor": cursorStaticModels(),
	}
	for provider, models := range catalogs {
		count := 0
		for _, m := range models {
			if m.Default {
				count++
			}
		}
		if count > 1 {
			t.Errorf("%s: %d models marked Default, want 0 or 1", provider, count)
		}
	}
}

func TestParseOpenCodeModels(t *testing.T) {
	input := `PROVIDER/MODEL                     CONTEXT  MAX_OUT
openai/gpt-4o                      128000   16384
anthropic/claude-sonnet-4-6        200000   8192
openai/gpt-4o                      128000   16384
nonprefixed-line
`
	models := parseOpenCodeModels(input)
	if len(models) != 2 {
		t.Fatalf("expected 2 models (header skipped, duplicate deduped, non-slash skipped), got %d: %+v", len(models), models)
	}
	if models[0].ID != "openai/gpt-4o" || models[0].Provider != "openai" {
		t.Errorf("unexpected first model: %+v", models[0])
	}
	if models[1].ID != "anthropic/claude-sonnet-4-6" || models[1].Provider != "anthropic" {
		t.Errorf("unexpected second model: %+v", models[1])
	}
}

func TestParseOpenCodeModelsVerboseVariants(t *testing.T) {
	input := `openai/gpt-5
{
  "id": "gpt-5",
  "name": "GPT-5",
  "reasoning": true,
  "variants": {
    "high": { "reasoningEffort": "high" },
    "low": { "reasoningEffort": "low" },
    "xhigh": { "reasoningEffort": "xhigh" },
    "fast-mode": { "reasoningEffort": "low" },
    "disabled": { "disabled": true }
  }
}
anthropic/claude-sonnet-4-6
{
  "id": "claude-sonnet-4-6",
  "reasoning": true,
  "variants": {
    "max": { "thinking": { "type": "enabled", "budgetTokens": 32000 } },
    "high": { "thinking": { "type": "enabled", "budgetTokens": 16000 } }
  }
}
`
	models := parseOpenCodeModels(input)
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d: %+v", len(models), models)
	}
	if models[0].Thinking == nil {
		t.Fatalf("expected first model to expose thinking variants")
	}
	got := make([]string, 0, len(models[0].Thinking.SupportedLevels))
	for _, lvl := range models[0].Thinking.SupportedLevels {
		got = append(got, lvl.Value)
		if lvl.Value == "xhigh" && lvl.Label != "Extra high" {
			t.Errorf("xhigh label: got %q, want Extra high", lvl.Label)
		}
		if lvl.Value == "fast-mode" && lvl.Label != "Fast Mode" {
			t.Errorf("custom variant label: got %q, want Fast Mode", lvl.Label)
		}
	}
	want := []string{"low", "high", "xhigh", "fast-mode"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("variant order/values: got %v, want %v", got, want)
	}
	if models[1].Thinking == nil || len(models[1].Thinking.SupportedLevels) != 2 {
		t.Fatalf("expected second model variants, got %+v", models[1].Thinking)
	}
}

func TestParseOpenCodeModelsMalformedVerboseBlockKeepsFollowingModels(t *testing.T) {
	input := `openai/gpt-5
{
  "id": "gpt-5",
  "reasoning": true,
  "variants": {
    "high": {}
  }
anthropic/claude-sonnet-4-6
{
  "id": "claude-sonnet-4-6",
  "reasoning": true,
  "variants": {
    "high": {},
    "max": {}
  }
}
`
	models := parseOpenCodeModels(input)
	if len(models) != 2 {
		t.Fatalf("expected both model rows to survive malformed JSON, got %d: %+v", len(models), models)
	}
	if models[0].ID != "openai/gpt-5" {
		t.Fatalf("unexpected first model: %+v", models[0])
	}
	if models[0].Thinking != nil {
		t.Fatalf("malformed first JSON block should not annotate thinking: %+v", models[0].Thinking)
	}
	if models[1].ID != "anthropic/claude-sonnet-4-6" {
		t.Fatalf("unexpected second model: %+v", models[1])
	}
	if models[1].Thinking == nil || len(models[1].Thinking.SupportedLevels) != 2 {
		t.Fatalf("valid following JSON block should still annotate thinking: %+v", models[1].Thinking)
	}
}

func TestDiscoverOpenCodeModelsFallsBackWhenVerboseFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake binary requires a POSIX shell")
	}

	dir := t.TempDir()
	fake := filepath.Join(dir, "opencode")
	script := `#!/bin/sh
if [ "$1" = "models" ] && [ "$2" = "--verbose" ]; then
  exit 2
fi
if [ "$1" = "models" ]; then
  cat <<'EOF'
PROVIDER/MODEL                     CONTEXT  MAX_OUT
openai/gpt-4o                      128000   16384
EOF
  exit 0
fi
exit 1
`
	writeTestExecutable(t, fake, []byte(script))

	models, err := discoverOpenCodeModels(context.Background(), fake)
	if err != nil {
		t.Fatalf("discoverOpenCodeModels: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("expected fallback non-verbose model, got %d: %+v", len(models), models)
	}
	if models[0].ID != "openai/gpt-4o" || models[0].Thinking != nil {
		t.Fatalf("unexpected fallback model: %+v", models[0])
	}
}

// TestCachedDiscoveryDoesNotCacheEmpty verifies that an empty discovery result
// is not cached, so a transient failure (e.g. a `pi --list-models` timeout)
// doesn't keep the model picker blank for the full TTL. A non-empty result is
// still cached. See #3729.
func TestCachedDiscoveryDoesNotCacheEmpty(t *testing.T) {
	const emptyKey, nonEmptyKey = "test-cache-empty", "test-cache-nonempty"
	// modelCache is a package-level global; clear our keys up front and on
	// cleanup so the test stays hermetic under `go test -count=N` (a leftover
	// non-empty entry from a prior run would otherwise skip the callback).
	resetCache := func() {
		modelCacheMu.Lock()
		delete(modelCache, emptyKey)
		delete(modelCache, nonEmptyKey)
		modelCacheMu.Unlock()
	}
	resetCache()
	t.Cleanup(resetCache)

	emptyCalls := 0
	empty := func() ([]Model, error) {
		emptyCalls++
		return []Model{}, nil
	}
	for i := 0; i < 2; i++ {
		got, err := cachedDiscovery(emptyKey, empty)
		if err != nil {
			t.Fatalf("cachedDiscovery: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected empty result, got %+v", got)
		}
	}
	if emptyCalls != 2 {
		t.Fatalf("empty result must not be cached: expected fn called 2x, got %d", emptyCalls)
	}

	nonEmptyCalls := 0
	nonEmpty := func() ([]Model, error) {
		nonEmptyCalls++
		return []Model{{ID: "provider/model"}}, nil
	}
	for i := 0; i < 2; i++ {
		if _, err := cachedDiscovery(nonEmptyKey, nonEmpty); err != nil {
			t.Fatalf("cachedDiscovery: %v", err)
		}
	}
	if nonEmptyCalls != 1 {
		t.Fatalf("non-empty result must be cached: expected fn called 1x, got %d", nonEmptyCalls)
	}
}

func TestParsePiModels(t *testing.T) {
	input := `openai:gpt-4o
anthropic:claude-opus-4-7
openai:gpt-4o
bareword
`
	models := parsePiModels(input)
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d: %+v", len(models), models)
	}
	if models[0].ID != "openai/gpt-4o" {
		t.Errorf("expected colon normalized to slash: %+v", models[0])
	}
}

func TestParsePiModelsTableFormat(t *testing.T) {
	input := `provider             model                   context  max-out  thinking  images
bailian-coding-plan  glm-4.7                 202.8K   16.4K    no        no
bailian-coding-plan  qwen3.6-plus            1M       65.5K    no        yes
opencode             claude-sonnet-4-6       1M       64K      yes       yes
opencode             claude-sonnet-4-6:exp   1M       64K      yes       yes
opencode             claude-sonnet-4-6       1M       64K      yes       yes
bareword-only-line
`
	models := parsePiModels(input)
	if len(models) != 4 {
		t.Fatalf("expected 4 models (header skipped, duplicate deduped, bareword skipped), got %d: %+v", len(models), models)
	}
	assertThinking := func(i int, want bool) {
		t.Helper()
		got := models[i].Thinking != nil
		if got != want {
			t.Fatalf("models[%d].Thinking presence = %v, want %v: %+v", i, got, want, models[i])
		}
	}
	if models[0].ID != "bailian-coding-plan/glm-4.7" || models[0].Provider != "bailian-coding-plan" {
		t.Errorf("unexpected first model: %+v", models[0])
	}
	assertThinking(0, false)
	if models[1].ID != "bailian-coding-plan/qwen3.6-plus" || models[1].Provider != "bailian-coding-plan" {
		t.Errorf("unexpected second model: %+v", models[1])
	}
	assertThinking(1, false)
	if models[2].ID != "opencode/claude-sonnet-4-6" || models[2].Provider != "opencode" {
		t.Errorf("unexpected third model: %+v", models[2])
	}
	assertThinking(2, true)
	// Colon inside a model name in column 1 must be preserved — only
	// the legacy `provider:model` form gets colon→slash normalization.
	if models[3].ID != "opencode/claude-sonnet-4-6:exp" || models[3].Provider != "opencode" {
		t.Errorf("expected ':' inside table-format model name to be preserved: %+v", models[3])
	}
	assertThinking(3, true)
}

// TestDiscoverPiModelsNonZeroExit verifies that discoverPiModels still returns
// the resolvable catalog when `pi --list-models` exits non-zero. Pi exits
// non-zero (and warns) when an agent config references stale provider/model
// patterns that no longer match the local catalog. Before the fix the daemon
// discarded the populated output on any non-zero exit and returned an empty
// list, so the UI model picker was blank even though the runtime was online and
// agents ran fine. See GitHub #3729.
func TestDiscoverPiModelsNonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake pi binary is a /bin/sh script")
	}

	const table = "provider         model        context  max-out  thinking  images\n" +
		"glm-coding-plan  glm-4.7      202.8K   16.4K    no        no"
	// The unmatched-pattern warning, with and without the `Warning:` prefix —
	// the prefix is not guaranteed across pi versions, and the bare form is
	// what slips past a naive guard into a bogus `No/models` model.
	const prefixed = `Warning: No models match pattern "opencode-go/mimo-v2-omni"`
	const bare = `No models match pattern "opencode-go/mimo-v2-pro"`

	cases := []struct {
		name   string
		script string
	}{
		{
			// Newer pi prints the catalog to stdout; the stale-pattern
			// warning goes to stderr and the process exits non-zero.
			name: "catalog on stdout",
			script: "#!/bin/sh\n" +
				"cat <<'EOF'\n" + table + "\nEOF\n" +
				"echo " + strconv.Quote(prefixed) + " >&2\n" +
				"exit 1\n",
		},
		{
			// Older pi prints the catalog (and the warning) to stderr; same
			// non-zero exit. The stderr fallback must still parse the catalog.
			name: "catalog and prefixed warning on stderr",
			script: "#!/bin/sh\n" +
				"cat >&2 <<'EOF'\n" + table + "\n" + prefixed + "\nEOF\n" +
				"exit 1\n",
		},
		{
			// Same, but the warning has no `Warning:` prefix — must not leak in
			// as a `No/models` row.
			name: "catalog and bare warning on stderr",
			script: "#!/bin/sh\n" +
				"cat >&2 <<'EOF'\n" + table + "\n" + bare + "\nEOF\n" +
				"exit 1\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakePath := filepath.Join(t.TempDir(), "pi")
			writeTestExecutable(t, fakePath, []byte(tc.script))

			models, err := discoverPiModels(context.Background(), fakePath)
			if err != nil {
				t.Fatalf("discoverPiModels: %v", err)
			}
			// Exactly the resolvable model — no warning line coined into a
			// bogus entry, no header row.
			if len(models) != 1 || models[0].ID != "glm-coding-plan/glm-4.7" {
				t.Fatalf("expected exactly [glm-coding-plan/glm-4.7] despite non-zero exit, got %+v", models)
			}
		})
	}
}

// TestDiscoverOpenCodeModelsFallsBackOnVerboseNoise verifies that a non-zero
// `opencode models --verbose` whose stdout is unparseable noise still falls
// back to the plain `opencode models` command instead of returning empty. The
// earlier fix skipped the fallback whenever verbose printed any bytes, which
// regressed this case. Mirrors the pi hardening in #3729.
func TestDiscoverOpenCodeModelsFallsBackOnVerboseNoise(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake opencode binary is a /bin/sh script")
	}

	// `opencode models --verbose` => $2 == "--verbose": emit noise + exit 1.
	// `opencode models`           => no $2: print the plain catalog.
	script := "#!/bin/sh\n" +
		"if [ \"$2\" = \"--verbose\" ]; then\n" +
		"  echo 'panic: catalog sync failed'\n" +
		"  exit 1\n" +
		"fi\n" +
		"echo 'openai/gpt-4o'\n"

	fakePath := filepath.Join(t.TempDir(), "opencode")
	writeTestExecutable(t, fakePath, []byte(script))

	models, err := discoverOpenCodeModels(context.Background(), fakePath)
	if err != nil {
		t.Fatalf("discoverOpenCodeModels: %v", err)
	}
	if len(models) != 1 || models[0].ID != "openai/gpt-4o" {
		t.Fatalf("expected fallback to plain `opencode models` to yield [openai/gpt-4o], got %+v", models)
	}
}

func TestParseCursorModels(t *testing.T) {
	input := `Available models

auto - Auto
composer-2-fast - Composer 2 Fast (current, default)
composer-2 - Composer 2
claude-4.6-sonnet-medium - Sonnet 4.6 1M
claude-opus-4-7-high - Opus 4.7 1M
gemini-3.1-pro - Gemini 3.1 Pro
`
	models := parseCursorModels(input)
	if len(models) != 6 {
		t.Fatalf("expected 6 models, got %d: %+v", len(models), models)
	}
	ids := map[string]Model{}
	for _, m := range models {
		ids[m.ID] = m
	}
	for _, want := range []string{"auto", "composer-2-fast", "composer-2", "claude-4.6-sonnet-medium", "claude-opus-4-7-high", "gemini-3.1-pro"} {
		if _, ok := ids[want]; !ok {
			t.Errorf("missing expected model %q in: %+v", want, models)
		}
	}
	if def := ids["composer-2-fast"]; !def.Default {
		t.Errorf("composer-2-fast should be marked default, got %+v", def)
	}
	if def := ids["composer-2-fast"]; def.Label != "Composer 2 Fast" {
		t.Errorf("default label should be stripped of parenthetical, got %q", def.Label)
	}
	// Non-default entry should not carry Default=true.
	if auto := ids["auto"]; auto.Default {
		t.Errorf("non-default entry should not be flagged default: %+v", auto)
	}
}

func TestParseCursorModelsSkipsHeaderAndBlankLines(t *testing.T) {
	input := `Available models

composer-2 - Composer 2
`
	models := parseCursorModels(input)
	if len(models) != 1 || models[0].ID != "composer-2" {
		t.Fatalf("unexpected: %+v", models)
	}
}

func TestParseACPSessionNewModels(t *testing.T) {
	// Mirrors a typical ACP session/new model catalog.
	raw := []byte(`{
      "sessionId": "ses_123",
      "models": {
        "availableModels": [
          {"modelId": "nous:moonshotai/kimi-k2.5", "name": "moonshotai/kimi-k2.5", "description": "Provider: Nous"},
          {"modelId": "nous:anthropic/claude-opus-4.7", "name": "anthropic/claude-opus-4.7", "description": "Provider: Nous • current"},
          {"modelId": "nous:moonshotai/kimi-k2.5", "name": "duplicate", "description": "dup"}
        ],
        "currentModelId": "nous:anthropic/claude-opus-4.7"
      }
    }`)
	models := parseACPSessionNewModels(raw)
	if len(models) != 2 {
		t.Fatalf("expected 2 models (duplicate deduped), got %d: %+v", len(models), models)
	}
	if models[0].ID != "nous:moonshotai/kimi-k2.5" || models[0].Provider != "nous" {
		t.Errorf("unexpected first model: %+v", models[0])
	}
	if models[0].Default {
		t.Errorf("non-current entry must not be marked default: %+v", models[0])
	}
	if !models[1].Default {
		t.Errorf("current entry must be marked default: %+v", models[1])
	}
	if models[1].ID != "nous:anthropic/claude-opus-4.7" {
		t.Errorf("expected current model second: %+v", models[1])
	}
}

func TestParseACPSessionNewModelsSnakeCaseAndUnknownNames(t *testing.T) {
	raw := []byte(`{
      "session_id": "ses_123",
      "models": {
        "available_models": [
          {"model_id": "nous:moonshotai/kimi-k2.6", "name": "Unknown", "description": "Provider: Nous"},
          {"model_id": "nous:anthropic/claude-sonnet-4.6", "name": "unknown", "description": "Provider: Nous"}
        ],
        "current_model_id": "nous:moonshotai/kimi-k2.6"
      }
    }`)
	models := parseACPSessionNewModels(raw)
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d: %+v", len(models), models)
	}
	if models[0].Label != "nous:moonshotai/kimi-k2.6" {
		t.Errorf("Unknown label should fall back to model id, got %+v", models[0])
	}
	if !models[0].Default {
		t.Errorf("snake_case current_model_id should mark default: %+v", models[0])
	}
	if models[1].Label != "nous:anthropic/claude-sonnet-4.6" {
		t.Errorf("lowercase unknown label should fall back to model id, got %+v", models[1])
	}
}

func TestParseACPSessionNewModelsMissingField(t *testing.T) {
	// session/new without the models field should yield nil so the caller
	// can distinguish "no catalog" from "empty catalog".
	raw := []byte(`{"sessionId": "ses_123"}`)
	if got := parseACPSessionNewModels(raw); got != nil && len(got) != 0 {
		t.Errorf("expected nil/empty, got %+v", got)
	}
}

func TestParseACPSessionNewModelsGarbage(t *testing.T) {
	if got := parseACPSessionNewModels([]byte("not json")); got != nil {
		t.Errorf("expected nil for non-JSON, got %+v", got)
	}
}

func TestKiroModelSelectionSupported(t *testing.T) {
	if !ModelSelectionSupported("kiro") {
		t.Error("kiro should be model-selection-supported")
	}
}

func TestCustomModelIDSupported(t *testing.T) {
	for _, provider := range []string{"claude", "codex", "cursor", "pi"} {
		if !CustomModelIDSupported(provider) {
			t.Errorf("%q should support custom model IDs", provider)
		}
	}
	for _, provider := range []string{"opencode", "grok", "kiro"} {
		if CustomModelIDSupported(provider) {
			t.Errorf("%q must not support custom model IDs", provider)
		}
	}
}

func TestCachedDiscovery(t *testing.T) {
	calls := 0
	fn := func() ([]Model, error) {
		calls++
		return []Model{{ID: "x", Label: "x"}}, nil
	}
	// First call populates the cache; reset for isolation.
	modelCacheMu.Lock()
	delete(modelCache, "testkey")
	modelCacheMu.Unlock()

	if _, err := cachedDiscovery("testkey", fn); err != nil {
		t.Fatal(err)
	}
	if _, err := cachedDiscovery("testkey", fn); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("expected 1 underlying call due to cache, got %d", calls)
	}
}

func TestParseCodexDebugModelsCatalog_ListOnly(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
  "models": [
    {"slug":"gpt-5.6-sol","display_name":"GPT-5.6-Sol","visibility":"list","default_reasoning_level":"low","supported_reasoning_levels":[{"effort":"low","description":"fast"}]},
    {"slug":"gpt-5.4","display_name":"GPT-5.4","visibility":"hide","default_reasoning_level":"medium","supported_reasoning_levels":[{"effort":"medium","description":"bal"}]},
    {"slug":"gpt-5.5","display_name":"GPT-5.5","visibility":"list","default_reasoning_level":"medium","supported_reasoning_levels":[{"effort":"medium","description":"bal"}]}
  ]
}`)
	got := parseCodexDebugModelsCatalog(raw)
	if len(got) != 2 {
		t.Fatalf("len=%d want 2 (hide filtered)", len(got))
	}
	if got[0].ID != "gpt-5.6-sol" || !got[0].Default {
		t.Fatalf("first=%+v", got[0])
	}
	if got[0].Thinking == nil || len(got[0].Thinking.SupportedLevels) != 1 {
		t.Fatalf("thinking=%+v", got[0].Thinking)
	}
	if got[1].ID != "gpt-5.5" || got[1].Default {
		t.Fatalf("second=%+v", got[1])
	}
}

func TestDiscoverClaudeModels_FallsBackToStatic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	modelCacheMu.Lock()
	delete(modelCache, "claude")
	delete(modelCache, "claude:/nonexistent/claude")
	modelCacheMu.Unlock()
	got, err := ListModels(ctx, "claude", "/nonexistent/claude")
	if err != nil {
		t.Fatalf("expected static fallback (no error) when Claude cannot list models, got %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected static fallback lineup when Claude cannot list models, got empty list")
	}
	ids := map[string]bool{}
	for _, m := range got {
		if m.ID == "" {
			t.Errorf("fallback model has empty ID: %+v", m)
		}
		ids[m.ID] = true
	}
	for _, want := range []string{"sonnet", "opus", "haiku", "claude-fable-5"} {
		if !ids[want] {
			t.Errorf("static fallback missing %q: %+v", want, got)
		}
	}
}

func TestDiscoverCodexModels_MissingBinary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	modelCacheMu.Lock()
	delete(modelCache, "codex")
	delete(modelCache, "codex:/nonexistent/codex")
	modelCacheMu.Unlock()
	got, err := ListModels(ctx, "codex", "/nonexistent/codex")
	if err == nil {
		t.Fatal("expected error when Codex CLI missing")
	}
	if len(got) != 0 {
		t.Fatalf("expected empty list, got %+v", got)
	}
}
