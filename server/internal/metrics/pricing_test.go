package metrics

import "testing"

func TestPriceForModelAliasAnthropicFableAndOpus48(t *testing.T) {
	cases := []struct {
		model string
		want  ModelPrice
	}{
		{
			model: "claude-fable-5",
			want:  ModelPrice{Provider: "anthropic", Model: "claude-fable-5", InputPerM: 10, CacheReadPerM: 1, CacheWritePerM: 12.5, OutputPerM: 50},
		},
		{
			model: "anthropic/claude-fable-5",
			want:  ModelPrice{Provider: "anthropic", Model: "claude-fable-5", InputPerM: 10, CacheReadPerM: 1, CacheWritePerM: 12.5, OutputPerM: 50},
		},
		{
			model: "claude-opus-4-8",
			want:  ModelPrice{Provider: "anthropic", Model: "claude-opus-4.8", InputPerM: 5, CacheReadPerM: 0.5, CacheWritePerM: 6.25, OutputPerM: 25},
		},
	}

	for _, tc := range cases {
		for _, alias := range []string{tc.model, "cursor/" + tc.model} {
			got, ok := PriceForModelAlias(alias)
			if !ok {
				t.Fatalf("PriceForModelAlias(%q) did not resolve", alias)
			}
			if got != tc.want {
				t.Fatalf("PriceForModelAlias(%q) = %+v, want %+v", alias, got, tc.want)
			}
		}
	}
}

func TestPriceForModelAliasCursorCurrentModels(t *testing.T) {
	cases := []struct {
		model string
		want  ModelPrice
	}{
		{
			model: "composer-2.5",
			want:  ModelPrice{Provider: "cursor", Model: "composer-2.5", InputPerM: 0.5, CacheReadPerM: 0.2, OutputPerM: 2.5},
		},
		{
			model: "composer-2.5-fast",
			want:  ModelPrice{Provider: "cursor", Model: "composer-2.5-fast", InputPerM: 3, CacheReadPerM: 0.5, OutputPerM: 15},
		},
		{
			model: "auto-cost",
			want:  ModelPrice{Provider: "cursor", Model: "auto-cost", InputPerM: 1.25, CacheReadPerM: 0.25, CacheWritePerM: 1.25, OutputPerM: 6},
		},
	}

	for _, tc := range cases {
		got, ok := PriceForModelAlias(tc.model)
		if !ok {
			t.Fatalf("PriceForModelAlias(%q) did not resolve", tc.model)
		}
		if got != tc.want {
			t.Fatalf("PriceForModelAlias(%q) = %+v, want %+v", tc.model, got, tc.want)
		}
	}

	if _, ok := PriceForModelAlias("auto"); ok {
		t.Fatal("generic auto must remain unpriced because routed-model rates vary")
	}
}

func TestPriceForModelAliasGrok45CurrentAliases(t *testing.T) {
	want := ModelPrice{Provider: "cursor", Model: "grok-4.5", InputPerM: 2, CacheReadPerM: 0.5, OutputPerM: 6}
	for _, model := range []string{
		"Cursor Grok 4.5 High",
		"cursor-grok-4.5-high",
	} {
		got, ok := PriceForModelAlias(model)
		if !ok {
			t.Fatalf("PriceForModelAlias(%q) did not resolve", model)
		}
		if got != want {
			t.Fatalf("PriceForModelAlias(%q) = %+v, want %+v", model, got, want)
		}
	}

	wantFast := ModelPrice{Provider: "cursor", Model: "grok-4.5-fast", InputPerM: 4, CacheReadPerM: 1, OutputPerM: 18}
	for _, model := range []string{
		"Cursor Grok 4.5 High Fast",
		"cursor-grok-4.5-high-fast",
	} {
		got, ok := PriceForModelAlias(model)
		if !ok {
			t.Fatalf("PriceForModelAlias(%q) did not resolve", model)
		}
		if got != wantFast {
			t.Fatalf("PriceForModelAlias(%q) = %+v, want %+v", model, got, wantFast)
		}
	}

	for _, model := range []string{"grok-4.5", "grok-4.5-latest", "grok-build-latest"} {
		if _, ok := PriceForModelAlias(model); ok {
			t.Fatalf("generic xAI alias %q must not resolve as Cursor first-party", model)
		}
	}
}
