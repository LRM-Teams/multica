package agent

import (
	"slices"
	"testing"
)

func TestIsKnownType(t *testing.T) {
	if !IsKnownType("claude") || !IsKnownType("codex") {
		t.Fatal("shipped providers must be known")
	}
	if IsKnownType("antigravity") || IsKnownType("") {
		t.Fatal("retired or empty providers must not be known")
	}
}

func TestMissingRequiredRuntimeIDsRetiresUnknownCatalog(t *testing.T) {
	providers := map[string]string{
		"rt-claude":       "claude",
		"rt-antigravity":  "antigravity",
		"rt-gone-unknown": "",
	}
	providerOf := func(id string) string { return providers[id] }
	accepted := []string{"rt-claude", "rt-antigravity", "rt-gone-unknown"}
	live := []string{"rt-claude"}

	got := MissingRequiredRuntimeIDs(accepted, live, providerOf, false)
	if len(got) != 0 {
		t.Fatalf("daemon defer unknown: got %v, want none", got)
	}

	got = MissingRequiredRuntimeIDs(accepted, live, providerOf, true)
	if !slices.Equal(got, []string{"rt-gone-unknown"}) {
		t.Fatalf("server fail-closed unknown: got %v", got)
	}
}

func TestMissingRequiredRuntimeIDsRejectsMissingShippedProvider(t *testing.T) {
	providerOf := func(id string) string {
		if id == "rt-codex" {
			return "codex"
		}
		return "claude"
	}
	got := MissingRequiredRuntimeIDs(
		[]string{"rt-claude", "rt-codex"},
		[]string{"rt-claude"},
		providerOf,
		true,
	)
	if !slices.Equal(got, []string{"rt-codex"}) {
		t.Fatalf("got %v, want missing shipped codex", got)
	}
}
